// NOTE: 编译阶段的内存草稿与编辑工具。compiler 在 ReAct 循环里按字段调用工具修改草稿，
// 写入时只做字段级校验（失败整条拒绝、草稿不变），跨字段和全局规则留给提交关卡 gateIssues。
// 设计借自 ../angela 的 proposal：先逐块写入、可局部修改、最后统一提交。
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const (
	toolNameSetOverview     = "set_overview"
	toolNameSetKeeperNotes  = "set_keeper_notes"
	toolNameUpsertScenes    = "upsert_scenes"
	toolNameUpsertNPCs      = "upsert_npcs"
	toolNameUpsertClues     = "upsert_clues"
	toolNameUpsertEndings   = "upsert_endings"
	toolNameUpsertMechanics = "upsert_mechanics"
	toolNameSetTimeline     = "set_timeline"
	toolNameRemoveEntries   = "remove_entries"
	toolNameViewDraft       = "view_draft"
	toolNameSubmitDraft     = "submit_draft"
)

// NOTE: 可按键增删的列表字段；timeline 没有天然键，只能整体替换。
var compileKeyedSections = []string{"scenes", "npcs", "clues", "endings", "mechanics"}

var compileViewSections = []string{"overview", "keeper_notes", "scenes", "npcs", "clues", "endings", "mechanics", "timeline"}

// compileDraft 是编译过程中累积的模组草稿。clueKeys 与 draft.Content.Clues 一一对应：
// ClueData 本身没有键字段，键只在草稿内用来定位条目，不入库。
type compileDraft struct {
	draft         ScenarioDraft
	rewardConcept string
	clueKeys      []string
	fixedToneTags []string
}

func newCompileDraft(story StoryOutput, constraints ScripterConstraints) *compileDraft {
	d := &compileDraft{}
	d.draft.Difficulty = firstNonEmpty(constraints.Difficulty, "normal")
	d.draft.Content.MythosAnchor = story.MythosAnchor
	if tags := trimmedNonEmpty(constraints.ToneTags); len(tags) > 0 {
		d.fixedToneTags = tags
		d.draft.Content.ToneTags = slices.Clone(tags)
	}
	return d
}

func (d *compileDraft) clone() *compileDraft {
	c := d.draft.Content
	c.ToneTags = slices.Clone(c.ToneTags)
	c.Scenes = cloneWith(c.Scenes, func(s models.SceneData) models.SceneData {
		s.Triggers = slices.Clone(s.Triggers)
		return s
	})
	c.NPCs = cloneWith(c.NPCs, func(n models.NPCData) models.NPCData {
		n.Stats = maps.Clone(n.Stats)
		n.Skills = maps.Clone(n.Skills)
		n.Spells = slices.Clone(n.Spells)
		return n
	})
	c.Clues = slices.Clone(c.Clues)
	c.Endings = slices.Clone(c.Endings)
	c.Timeline = slices.Clone(c.Timeline)
	c.Mechanics = cloneWith(c.Mechanics, func(m models.MechanicData) models.MechanicData {
		m.Stages = slices.Clone(m.Stages)
		return m
	})
	if c.KeeperAppendix != nil {
		k := *c.KeeperAppendix
		c.KeeperAppendix = &k
	}
	if c.Reward != nil {
		r := *c.Reward
		c.Reward = &r
	}
	out := &compileDraft{
		draft:         d.draft,
		rewardConcept: d.rewardConcept,
		clueKeys:      slices.Clone(d.clueKeys),
		fixedToneTags: slices.Clone(d.fixedToneTags),
	}
	out.draft.Content = c
	return out
}

func cloneWith[T any](in []T, f func(T) T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

// ---------------------------------------------------------------------------
// 工具定义
// ---------------------------------------------------------------------------

func compileDraftTools() []scripterTool {
	return []scripterTool{
		{def: llm.ToolDefinition{
			Name: toolNameSetOverview,
			Description: "设置模组概览字段，只覆盖传入的字段，未传的保持不变，传空字符串表示清空。" +
				"人数、难度、作者、简介由系统设定，不在此处填写。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "模组标题：照抄故事文档标题；无明确标题时取文档中一个地名或器物名，不写成句子"},
					"tags": {"type": "string", "description": "2-3个逗号分隔标签，指向本剧本独有的核心叙事装置或桥段，不用抽象风格词；须避开 recent_scenario_tags_blacklist"},
					"reward_concept": {"type": "string", "description": "通关奖励的一句话叙事概念：一件调查员能带走的实体载体（典籍/手稿/器物），说明它是什么、原属谁或存于何处、与核心真相的关系；不新增人物地点、不改动结局、不写规则数值"},
					"invest_focus": {"type": "string", "description": "调查入口的简短概括，忠实提炼，不得自创"},
					"tone_tags": {"type": "array", "items": {"type": "string"}, "description": "文风标签；系统已预填时不得改动"},
					"setting": {"type": "string", "description": "表层情境原文或忠实改写，必须保留具体年月日；同时作为模组简介展示"},
					"intro": {"type": "string", "description": "以叙事散文呈现调查员到场情境：先自然写出具体场所、具体年月日+时刻、要达成的那件事，再写至少两处可视可听可感的场景细节；不得用【】或标签式罗列，不推荐具体行动"},
					"game_start_time": {"type": "string", "description": "开局时间，格式 YYYY-MM-DDTHH:MM，不带时区；年月日须与 setting 一致，文档未写明时刻时取 08:00"},
					"map_description": {"type": "string", "description": "按地点关系概括的文字地图，体现可回访、可交叉验证的调查网络"},
					"playthrough_outline": {"type": "string", "description": "逐场景的游玩流程大纲：进入条件、可接触人物、可得发现、通向哪里与分支，以及哪些条件导向哪种收场；只能引用已写入草稿的场景、人物、发现与收场"}
				}
			}`),
		}},
		{def: llm.ToolDefinition{
			Name: toolNameSetKeeperNotes,
			Description: "设置守密人材料，只覆盖传入的字段，未传的保持不变，传空字符串表示清空。" +
				"mythos_core 只写在这里，不得再写成线索。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"mythos_core": {"type": "string", "description": "神话本质的核心揭示：这个故事里神话真相本身是什么，须与 mythos_anchor 强绑定，法术名/物品名/怪物名须与 mythos_anchor 及故事文档一致"},
					"core_truth": {"type": "string", "description": "KP 核心真相，以及 mythos_anchor 为何不可替换；必填，不得压缩为一句话"},
					"antagonist_dossier": {"type": "string", "description": "施动者（邪教/个人施法者/神话生物）的细化设定，须完整保留；文档确无独立施动者时留空"},
					"difficulty_down": {"type": "string"},
					"difficulty_up": {"type": "string"},
					"solo_advice": {"type": "string"},
					"group_advice": {"type": "string"},
					"horror_tips": {"type": "string"},
					"theme_guidance": {"type": "string"}
				}
			}`),
		}},
		{def: llm.ToolDefinition{
			Name:        toolNameUpsertScenes,
			Description: "写入场景。按 id 处理：已有的 id 整条覆盖（位置不变），新 id 追加到末尾；一次可带多条，任何一条不合格则整个调用不生效。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"scenes": {"type": "array", "minItems": 1, "items": {
						"type": "object",
						"properties": {
							"id": {"type": "string", "description": "snake_case 英文标识"},
							"name": {"type": "string"},
							"description": {"type": "string", "description": "合并该地点在文中散落的全部信息：直接可见与可感知的、深查才能发现的（含所需检定）、成败后果、可能的危险、通往哪些地方；规则数值与遭遇步骤要保留"},
							"triggers": {"type": "array", "items": {"type": "string"}, "description": "默认 [\"available_from_start\"]；仅当文中明确需要先获得某个发现或完成某个行动才会来到此处时才使用条件触发"}
						},
						"required": ["id", "name", "description"]
					}}
				},
				"required": ["scenes"]
			}`),
		}},
		{def: llm.ToolDefinition{
			Name:        toolNameUpsertNPCs,
			Description: "写入 NPC。按 name 处理：已有的名字整条覆盖，新名字追加；改名请先 remove_entries 再写入。任何一条不合格则整个调用不生效。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"npcs": {"type": "array", "minItems": 1, "items": {
						"type": "object",
						"properties": {
							"name": {"type": "string"},
							"description": {"type": "string", "description": "合并该人物在文中散落的全部信息：身份营生、想要什么、正在做什么、瞒着或不愿说的事（须写明「秘密」或「保留」）、标志性细节、与他人的关系"},
							"attitude": {"type": "string", "description": "文档写明或可直接判断的初始态度"},
							"stats": {"type": "object", "additionalProperties": {"type": "integer"}, "description": "STR/CON/SIZ/DEX/APP/INT/POW/EDU/SAN/HP/MP；文中有数值表时直接采用，否则按 COC7 惯例给出合理值"},
							"skills": {"type": "object", "additionalProperties": {"type": "integer"}, "description": "按身份给出 3-6 项最相关技能，普通人类技能值通常 15-75"},
							"spells": {"type": "array", "items": {"type": "string"}, "description": "仅文中明确写明会施法者才填"}
						},
						"required": ["name", "description", "attitude"]
					}}
				},
				"required": ["npcs"]
			}`),
		}},
		{def: llm.ToolDefinition{
			Name:        toolNameUpsertClues,
			Description: "写入线索。按 key 处理：key 是你自起的短标识，只用于后续覆盖或删除，不会进入成品；已有 key 整条覆盖，新 key 追加。任何一条不合格则整个调用不生效。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"clues": {"type": "array", "minItems": 1, "items": {
						"type": "object",
						"properties": {
							"key": {"type": "string", "description": "短标识，如 diary_page"},
							"summary": {"type": "string", "description": "写清这条发现是什么、从哪来、调查员由此知道了什么"},
							"source": {"type": "string", "description": "来自哪个地点、哪个人或哪件物品"},
							"skill_check": {"type": "string", "description": "文中写明的检定名与难度，未写明可留空"},
							"on_success": {"type": "string"},
							"on_failure": {"type": "string"},
							"nature": {"type": "string", "enum": ["真实", "隐藏", "误导"], "description": "真实：可直接查证、指向主线；隐藏：揭示神话存在本身的发现；误导：本身真实却被某人给出看似合理的错误解释"}
						},
						"required": ["key", "summary", "nature"]
					}}
				},
				"required": ["clues"]
			}`),
		}},
		{def: llm.ToolDefinition{
			Name:        toolNameUpsertEndings,
			Description: "写入结局。按 name 处理：已有的名字整条覆盖，新名字追加；每一种独立的收场都要有一条，不得合并或省略。任何一条不合格则整个调用不生效。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"endings": {"type": "array", "minItems": 1, "items": {
						"type": "object",
						"properties": {
							"name": {"type": "string"},
							"trigger": {"type": "string", "description": "如果[条件]，则[处境变化]的条件句"},
							"description": {"type": "string"},
							"san_reward": {"type": "string", "description": "如 恢复1d6 / 损失1d6；文中已写明的数值优先采用，否则按结局性质换算"},
							"is_failure": {"type": "boolean", "description": "灾难或失败向的收场为 true"}
						},
						"required": ["name", "trigger"]
					}}
				},
				"required": ["endings"]
			}`),
		}},
		{def: llm.ToolDefinition{
			Name:        toolNameUpsertMechanics,
			Description: "写入可量化追踪的机制（计数器、行动时钟等），仅供 KP 参考。按 name 处理：已有的名字整条覆盖，新名字追加。文档没有此类机制时不要调用。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"mechanics": {"type": "array", "minItems": 1, "items": {
						"type": "object",
						"properties": {
							"name": {"type": "string"},
							"type": {"type": "string", "enum": ["counter", "clock", "tracker"]},
							"description": {"type": "string"},
							"stages": {"type": "array", "items": {
								"type": "object",
								"properties": {
									"label": {"type": "string"},
									"effect": {"type": "string"},
									"trigger": {"type": "string"}
								},
								"required": ["label"]
							}}
						},
						"required": ["name", "type"]
					}}
				},
				"required": ["mechanics"]
			}`),
		}},
		{def: llm.ToolDefinition{
			Name:        toolNameSetTimeline,
			Description: "整体替换时间线（没有按条目的增删）。传空数组表示清空；文档确实没有可提取的时间节点时不要调用。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"events": {"type": "array", "items": {
						"type": "object",
						"properties": {
							"time": {"type": "string"},
							"event": {"type": "string", "description": "中性事实记录句：谁在何时做了什么、什么状态发生了变化；不引用人物原话"},
							"phase": {"type": "string", "enum": ["past", "current"]}
						},
						"required": ["time", "event"]
					}}
				},
				"required": ["events"]
			}`),
		}},
		{def: llm.ToolDefinition{
			Name:        toolNameRemoveEntries,
			Description: "按键删除列表条目：scenes 的键是 id，npcs/endings/mechanics 的键是 name，clues 的键是 key。只要有一个键不存在，整个调用不生效，回执会列出当前有效的键。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"section": {"type": "string", "enum": ["scenes", "npcs", "clues", "endings", "mechanics"]},
					"keys": {"type": "array", "minItems": 1, "items": {"type": "string"}}
				},
				"required": ["section", "keys"]
			}`),
		}},
		{def: llm.ToolDefinition{
			Name: toolNameViewDraft,
			Description: "查看草稿。不带参数：返回各部分的填写情况、条目键，以及此刻提交会遇到的问题清单。" +
				"带 section：返回该部分的完整内容。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"section": {"type": "string", "enum": ["overview", "keeper_notes", "scenes", "npcs", "clues", "endings", "mechanics", "timeline"]}
				}
			}`),
		}},
		askLawyerTool("向COC7规则书专家提出具体问题，核对怪物属性、法术效果、技能检定等规则书事实；故事文档已写明的内容不要重复核验"),
		{solo: true, def: llm.ToolDefinition{
			Name:        toolNameSubmitDraft,
			Description: "提交草稿。系统会统一校验；未通过时返回问题清单，用编辑工具局部修改后再次提交。必须单独一轮调用。",
			Parameters:  jsonSchemaObject(`{"type": "object", "properties": {}}`),
		}},
	}
}

// ---------------------------------------------------------------------------
// 分发
// ---------------------------------------------------------------------------

// dispatch 处理编辑、查看和 ask_lawyer；submit_draft 由编译会话处理，因为它要计拒绝次数。
func (d *compileDraft) dispatch(ctx context.Context, room *scripterRoom, call llm.ToolCall) toolOutcome {
	switch call.Name {
	case toolNameSetOverview:
		return d.setOverview(call.Arguments)
	case toolNameSetKeeperNotes:
		return d.setKeeperNotes(call.Arguments)
	case toolNameUpsertScenes:
		return d.upsertScenes(call.Arguments)
	case toolNameUpsertNPCs:
		return d.upsertNPCs(call.Arguments)
	case toolNameUpsertClues:
		return d.upsertClues(call.Arguments)
	case toolNameUpsertEndings:
		return d.upsertEndings(call.Arguments)
	case toolNameUpsertMechanics:
		return d.upsertMechanics(call.Arguments)
	case toolNameSetTimeline:
		return d.setTimeline(call.Arguments)
	case toolNameRemoveEntries:
		return d.removeEntries(call.Arguments)
	case toolNameViewDraft:
		return d.viewTool(call.Arguments, room.tagsBlacklist)
	case toolNameAskLawyer:
		var args askLawyerArgs
		if err := decodeStrict(call.Arguments, &args); err != nil {
			return compileRejectArgs(call.Name, err)
		}
		return toolOutcome{result: scripterAskLawyer(ctx, room, args.Question)}
	default:
		return compileReject("此阶段不允许调用 %s。", call.Name)
	}
}

func compileReject(format string, args ...any) toolOutcome {
	return toolOutcome{reject: "SYSTEM REJECT: " + fmt.Sprintf(format, args...)}
}

func compileRejectArgs(tool string, err error) toolOutcome {
	return compileReject("%s 的参数不合法：%v。本次调用未生效，请按工具定义重新调用。", tool, err)
}

func compileRejectProblems(problems []string) toolOutcome {
	return compileReject("本次调用未写入任何内容，请修正后重新调用：\n- %s", strings.Join(problems, "\n- "))
}

func decodeStrict(raw string, v any) error {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func trimmedNonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func missingFields(label string, fields ...[2]string) string {
	var missing []string
	for _, f := range fields {
		if strings.TrimSpace(f[1]) == "" {
			missing = append(missing, f[0])
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("%s 缺少 %s", label, strings.Join(missing, "/"))
}

func duplicateKeyProblems(section string, keys []string) []string {
	seen := make(map[string]bool, len(keys))
	reported := make(map[string]bool)
	var problems []string
	for _, k := range keys {
		if k == "" {
			continue
		}
		if seen[k] && !reported[k] {
			reported[k] = true
			problems = append(problems, fmt.Sprintf("%s 中键 %q 在同一次调用里出现了多次", section, k))
		}
		seen[k] = true
	}
	return problems
}

// ---------------------------------------------------------------------------
// 概览与守密人材料
// ---------------------------------------------------------------------------

type overviewArgs struct {
	Name               *string   `json:"name"`
	Tags               *string   `json:"tags"`
	RewardConcept      *string   `json:"reward_concept"`
	InvestFocus        *string   `json:"invest_focus"`
	ToneTags           *[]string `json:"tone_tags"`
	Setting            *string   `json:"setting"`
	Intro              *string   `json:"intro"`
	GameStartTime      *string   `json:"game_start_time"`
	MapDescription     *string   `json:"map_description"`
	PlaythroughOutline *string   `json:"playthrough_outline"`
}

type fieldWrite struct {
	label string
	src   *string
	dst   *string
}

func applyFieldWrites(writes []fieldWrite) []string {
	var changed []string
	for _, w := range writes {
		if w.src == nil {
			continue
		}
		*w.dst = strings.TrimSpace(*w.src)
		changed = append(changed, w.label)
	}
	return changed
}

func (d *compileDraft) setOverview(raw string) toolOutcome {
	var a overviewArgs
	if err := decodeStrict(raw, &a); err != nil {
		return compileRejectArgs(toolNameSetOverview, err)
	}
	if a.GameStartTime != nil {
		if t := strings.TrimSpace(*a.GameStartTime); t != "" {
			if _, err := models.ParseGameTime(t); err != nil {
				return compileReject("game_start_time 必须是 YYYY-MM-DDTHH:MM 格式的本地时间（不带时区），本次调用未写入任何字段。")
			}
		}
	}
	var tone []string
	if a.ToneTags != nil {
		tone = trimmedNonEmpty(*a.ToneTags)
		if len(d.fixedToneTags) > 0 && !slices.Equal(tone, d.fixedToneTags) {
			return compileReject("tone_tags 由系统固定为 [%s]，不得改动，本次调用未写入任何字段。", strings.Join(d.fixedToneTags, ", "))
		}
	}
	c := &d.draft.Content
	changed := applyFieldWrites([]fieldWrite{
		{"name", a.Name, &d.draft.Name},
		{"tags", a.Tags, &d.draft.Tags},
		{"reward_concept", a.RewardConcept, &d.rewardConcept},
		{"invest_focus", a.InvestFocus, &c.InvestFocus},
		{"setting", a.Setting, &c.Setting},
		{"intro", a.Intro, &c.Intro},
		{"game_start_time", a.GameStartTime, &c.GameStartTime},
		{"map_description", a.MapDescription, &c.MapDescription},
		{"playthrough_outline", a.PlaythroughOutline, &c.PlaythroughOutline},
	})
	if a.ToneTags != nil {
		c.ToneTags = tone
		changed = append(changed, "tone_tags")
	}
	if len(changed) == 0 {
		return compileReject("set_overview 没有传入任何字段。")
	}
	return toolOutcome{result: fmt.Sprintf("已更新概览字段：%s。", strings.Join(changed, "、"))}
}

type keeperNotesArgs struct {
	MythosCore        *string `json:"mythos_core"`
	CoreTruth         *string `json:"core_truth"`
	AntagonistDossier *string `json:"antagonist_dossier"`
	DifficultyDown    *string `json:"difficulty_down"`
	DifficultyUp      *string `json:"difficulty_up"`
	SoloAdvice        *string `json:"solo_advice"`
	GroupAdvice       *string `json:"group_advice"`
	HorrorTips        *string `json:"horror_tips"`
	ThemeGuidance     *string `json:"theme_guidance"`
}

func (d *compileDraft) setKeeperNotes(raw string) toolOutcome {
	var a keeperNotesArgs
	if err := decodeStrict(raw, &a); err != nil {
		return compileRejectArgs(toolNameSetKeeperNotes, err)
	}
	c := &d.draft.Content
	k := &models.KeeperAppendix{}
	if c.KeeperAppendix != nil {
		*k = *c.KeeperAppendix
	}
	changed := applyFieldWrites([]fieldWrite{
		{"mythos_core", a.MythosCore, &c.MythosCore},
		{"core_truth", a.CoreTruth, &k.CoreTruth},
		{"antagonist_dossier", a.AntagonistDossier, &k.AntagonistDossier},
		{"difficulty_down", a.DifficultyDown, &k.DifficultyDown},
		{"difficulty_up", a.DifficultyUp, &k.DifficultyUp},
		{"solo_advice", a.SoloAdvice, &k.SoloAdvice},
		{"group_advice", a.GroupAdvice, &k.GroupAdvice},
		{"horror_tips", a.HorrorTips, &k.HorrorTips},
		{"theme_guidance", a.ThemeGuidance, &k.ThemeGuidance},
	})
	if len(changed) == 0 {
		return compileReject("set_keeper_notes 没有传入任何字段。")
	}
	c.KeeperAppendix = k
	return toolOutcome{result: fmt.Sprintf("已更新守密人材料：%s。", strings.Join(changed, "、"))}
}

// ---------------------------------------------------------------------------
// 列表字段
// ---------------------------------------------------------------------------

func keysOf[T any](list []T, key func(T) string) []string {
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = key(v)
	}
	return out
}

func (d *compileDraft) sectionKeys(section string) []string {
	c := d.draft.Content
	switch section {
	case "scenes":
		return keysOf(c.Scenes, func(s models.SceneData) string { return s.ID })
	case "npcs":
		return keysOf(c.NPCs, func(n models.NPCData) string { return n.Name })
	case "clues":
		return slices.Clone(d.clueKeys)
	case "endings":
		return keysOf(c.Endings, func(e models.EndingData) string { return e.Name })
	case "mechanics":
		return keysOf(c.Mechanics, func(m models.MechanicData) string { return m.Name })
	}
	return nil
}

// applyUpsert 按键原位覆盖或追加。keys 与 list 等长，itemKeys 与 items 等长。
func applyUpsert[T any](list []T, keys []string, items []T, itemKeys []string) (outList []T, outKeys []string, labels []string) {
	outList = slices.Clone(list)
	outKeys = slices.Clone(keys)
	for i, item := range items {
		k := itemKeys[i]
		if idx := slices.Index(outKeys, k); idx >= 0 {
			outList[idx] = item
			labels = append(labels, k+"（覆盖）")
			continue
		}
		outList = append(outList, item)
		outKeys = append(outKeys, k)
		labels = append(labels, k+"（新增）")
	}
	return outList, outKeys, labels
}

// removeByKeys 删除指定键；有任何一个键不存在时不删除，返回未知键。
func removeByKeys[T any](list []T, keys, remove []string) (outList []T, outKeys []string, unknown []string) {
	for _, k := range remove {
		if !slices.Contains(keys, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return list, keys, unknown
	}
	for i, k := range keys {
		if slices.Contains(remove, k) {
			continue
		}
		outList = append(outList, list[i])
		outKeys = append(outKeys, k)
	}
	return outList, outKeys, nil
}

func upsertReceipt(section string, labels []string, total int) toolOutcome {
	return toolOutcome{result: fmt.Sprintf("已写入 %s：%s；当前共 %d 条。", section, strings.Join(labels, "、"), total)}
}

func (d *compileDraft) upsertScenes(raw string) toolOutcome {
	var a struct {
		Scenes []models.SceneData `json:"scenes"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return compileRejectArgs(toolNameUpsertScenes, err)
	}
	if len(a.Scenes) == 0 {
		return compileReject("scenes 至少需要一条。")
	}
	keys := make([]string, len(a.Scenes))
	var problems []string
	for i := range a.Scenes {
		s := &a.Scenes[i]
		s.ID, s.Name, s.Description = strings.TrimSpace(s.ID), strings.TrimSpace(s.Name), strings.TrimSpace(s.Description)
		keys[i] = s.ID
		if p := missingFields(fmt.Sprintf("scenes[%d]", i), [2]string{"id", s.ID}, [2]string{"name", s.Name}, [2]string{"description", s.Description}); p != "" {
			problems = append(problems, p)
		}
		if len(trimmedNonEmpty(s.Triggers)) == 0 {
			s.Triggers = []string{"available_from_start"}
		}
	}
	problems = append(problems, duplicateKeyProblems("scenes", keys)...)
	if len(problems) > 0 {
		return compileRejectProblems(problems)
	}
	c := &d.draft.Content
	list, _, labels := applyUpsert(c.Scenes, d.sectionKeys("scenes"), a.Scenes, keys)
	c.Scenes = list
	return upsertReceipt("scenes", labels, len(list))
}

func (d *compileDraft) upsertNPCs(raw string) toolOutcome {
	var a struct {
		NPCs []models.NPCData `json:"npcs"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return compileRejectArgs(toolNameUpsertNPCs, err)
	}
	if len(a.NPCs) == 0 {
		return compileReject("npcs 至少需要一条。")
	}
	keys := make([]string, len(a.NPCs))
	var problems []string
	for i := range a.NPCs {
		n := &a.NPCs[i]
		n.Name, n.Description, n.Attitude = strings.TrimSpace(n.Name), strings.TrimSpace(n.Description), strings.TrimSpace(n.Attitude)
		keys[i] = n.Name
		if p := missingFields(fmt.Sprintf("npcs[%d]", i), [2]string{"name", n.Name}, [2]string{"description", n.Description}, [2]string{"attitude", n.Attitude}); p != "" {
			problems = append(problems, p)
		}
	}
	problems = append(problems, duplicateKeyProblems("npcs", keys)...)
	if len(problems) > 0 {
		return compileRejectProblems(problems)
	}
	c := &d.draft.Content
	list, _, labels := applyUpsert(c.NPCs, d.sectionKeys("npcs"), a.NPCs, keys)
	c.NPCs = list
	return upsertReceipt("npcs", labels, len(list))
}

// clueArg 在 ClueData 之外多一个只在草稿内使用的键。
type clueArg struct {
	Key string `json:"key"`
	models.ClueData
}

var validClueNature = []string{"真实", "隐藏", "误导"}

func (d *compileDraft) upsertClues(raw string) toolOutcome {
	var a struct {
		Clues []clueArg `json:"clues"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return compileRejectArgs(toolNameUpsertClues, err)
	}
	if len(a.Clues) == 0 {
		return compileReject("clues 至少需要一条。")
	}
	keys := make([]string, len(a.Clues))
	items := make([]models.ClueData, len(a.Clues))
	var problems []string
	for i := range a.Clues {
		cl := &a.Clues[i]
		cl.Key, cl.Summary, cl.Nature = strings.TrimSpace(cl.Key), strings.TrimSpace(cl.Summary), strings.TrimSpace(cl.Nature)
		keys[i] = cl.Key
		items[i] = cl.ClueData
		if p := missingFields(fmt.Sprintf("clues[%d]", i), [2]string{"key", cl.Key}, [2]string{"summary", cl.Summary}); p != "" {
			problems = append(problems, p)
		}
		if !slices.Contains(validClueNature, cl.Nature) {
			problems = append(problems, fmt.Sprintf("clues[%d] 的 nature 必须是 真实/隐藏/误导 之一", i))
		}
	}
	problems = append(problems, duplicateKeyProblems("clues", keys)...)
	if len(problems) > 0 {
		return compileRejectProblems(problems)
	}
	c := &d.draft.Content
	list, newKeys, labels := applyUpsert(c.Clues, d.clueKeys, items, keys)
	c.Clues, d.clueKeys = list, newKeys
	return upsertReceipt("clues", labels, len(list))
}

func (d *compileDraft) upsertEndings(raw string) toolOutcome {
	var a struct {
		Endings []models.EndingData `json:"endings"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return compileRejectArgs(toolNameUpsertEndings, err)
	}
	if len(a.Endings) == 0 {
		return compileReject("endings 至少需要一条。")
	}
	keys := make([]string, len(a.Endings))
	var problems []string
	for i := range a.Endings {
		e := &a.Endings[i]
		e.Name, e.Trigger = strings.TrimSpace(e.Name), strings.TrimSpace(e.Trigger)
		keys[i] = e.Name
		if p := missingFields(fmt.Sprintf("endings[%d]", i), [2]string{"name", e.Name}, [2]string{"trigger", e.Trigger}); p != "" {
			problems = append(problems, p)
		}
	}
	problems = append(problems, duplicateKeyProblems("endings", keys)...)
	if len(problems) > 0 {
		return compileRejectProblems(problems)
	}
	c := &d.draft.Content
	list, _, labels := applyUpsert(c.Endings, d.sectionKeys("endings"), a.Endings, keys)
	c.Endings = list
	return upsertReceipt("endings", labels, len(list))
}

var validMechanicType = []string{"counter", "clock", "tracker"}

func (d *compileDraft) upsertMechanics(raw string) toolOutcome {
	var a struct {
		Mechanics []models.MechanicData `json:"mechanics"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return compileRejectArgs(toolNameUpsertMechanics, err)
	}
	if len(a.Mechanics) == 0 {
		return compileReject("mechanics 至少需要一条。")
	}
	keys := make([]string, len(a.Mechanics))
	var problems []string
	for i := range a.Mechanics {
		m := &a.Mechanics[i]
		m.Name, m.Type = strings.TrimSpace(m.Name), strings.TrimSpace(m.Type)
		keys[i] = m.Name
		if p := missingFields(fmt.Sprintf("mechanics[%d]", i), [2]string{"name", m.Name}); p != "" {
			problems = append(problems, p)
		}
		if !slices.Contains(validMechanicType, m.Type) {
			problems = append(problems, fmt.Sprintf("mechanics[%d] 的 type 必须是 counter/clock/tracker 之一", i))
		}
		for j, st := range m.Stages {
			if strings.TrimSpace(st.Label) == "" {
				problems = append(problems, fmt.Sprintf("mechanics[%d].stages[%d] 缺少 label", i, j))
			}
		}
	}
	problems = append(problems, duplicateKeyProblems("mechanics", keys)...)
	if len(problems) > 0 {
		return compileRejectProblems(problems)
	}
	c := &d.draft.Content
	list, _, labels := applyUpsert(c.Mechanics, d.sectionKeys("mechanics"), a.Mechanics, keys)
	c.Mechanics = list
	return upsertReceipt("mechanics", labels, len(list))
}

func (d *compileDraft) setTimeline(raw string) toolOutcome {
	var a struct {
		Events []models.TimelineEvent `json:"events"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return compileRejectArgs(toolNameSetTimeline, err)
	}
	if a.Events == nil {
		return compileReject("set_timeline 缺少 events；要清空时间线请传空数组。")
	}
	var problems []string
	for i := range a.Events {
		ev := &a.Events[i]
		ev.Time, ev.Event, ev.Phase = strings.TrimSpace(ev.Time), strings.TrimSpace(ev.Event), strings.TrimSpace(ev.Phase)
		if p := missingFields(fmt.Sprintf("events[%d]", i), [2]string{"time", ev.Time}, [2]string{"event", ev.Event}); p != "" {
			problems = append(problems, p)
		}
		if ev.Phase != "" && ev.Phase != "past" && ev.Phase != "current" {
			problems = append(problems, fmt.Sprintf("events[%d] 的 phase 必须是 past 或 current", i))
		}
		if eventLooksLikeDialogueQuote(ev.Event) {
			problems = append(problems, fmt.Sprintf("events[%d].event 疑似把对话引语写成了记录（%q），须改写为中性事实记录句（谁在何时做了什么），不引用人物原话", i, truncateRunes(ev.Event, 60)))
		}
	}
	if len(problems) > 0 {
		return compileRejectProblems(problems)
	}
	d.draft.Content.Timeline = a.Events
	return toolOutcome{result: fmt.Sprintf("已替换 timeline，当前共 %d 条。", len(a.Events))}
}

func (d *compileDraft) removeEntries(raw string) toolOutcome {
	var a struct {
		Section string   `json:"section"`
		Keys    []string `json:"keys"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return compileRejectArgs(toolNameRemoveEntries, err)
	}
	a.Keys = trimmedNonEmpty(a.Keys)
	if !slices.Contains(compileKeyedSections, a.Section) {
		return compileReject("section 必须是 %s 之一。", strings.Join(compileKeyedSections, "/"))
	}
	if len(a.Keys) == 0 {
		return compileReject("keys 至少需要一个。")
	}
	c := &d.draft.Content
	keys := d.sectionKeys(a.Section)
	var unknown []string
	var total int
	switch a.Section {
	case "scenes":
		c.Scenes, _, unknown = removeByKeys(c.Scenes, keys, a.Keys)
		total = len(c.Scenes)
	case "npcs":
		c.NPCs, _, unknown = removeByKeys(c.NPCs, keys, a.Keys)
		total = len(c.NPCs)
	case "clues":
		c.Clues, d.clueKeys, unknown = removeByKeys(c.Clues, keys, a.Keys)
		total = len(c.Clues)
	case "endings":
		c.Endings, _, unknown = removeByKeys(c.Endings, keys, a.Keys)
		total = len(c.Endings)
	case "mechanics":
		c.Mechanics, _, unknown = removeByKeys(c.Mechanics, keys, a.Keys)
		total = len(c.Mechanics)
	}
	if len(unknown) > 0 {
		return compileReject("%s 中不存在这些键：%s；本次调用未删除任何条目。当前有效的键：%s。",
			a.Section, strings.Join(unknown, "、"), joinOrNone(keys))
	}
	return toolOutcome{result: fmt.Sprintf("已从 %s 删除：%s；当前共 %d 条。", a.Section, strings.Join(a.Keys, "、"), total)}
}

func joinOrNone(keys []string) string {
	if len(keys) == 0 {
		return "（空）"
	}
	return strings.Join(keys, "、")
}

// ---------------------------------------------------------------------------
// 查看
// ---------------------------------------------------------------------------

func (d *compileDraft) viewTool(raw string, tagsBlacklist []string) toolOutcome {
	var a struct {
		Section string `json:"section"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return compileRejectArgs(toolNameViewDraft, err)
	}
	if a.Section == "" {
		return toolOutcome{result: d.overview(tagsBlacklist)}
	}
	if !slices.Contains(compileViewSections, a.Section) {
		return compileReject("section 必须是 %s 之一。", strings.Join(compileViewSections, "/"))
	}
	return toolOutcome{result: d.sectionJSON(a.Section)}
}

func fieldState(s string) string {
	if n := len([]rune(strings.TrimSpace(s))); n > 0 {
		return fmt.Sprintf("已填写（%d字）", n)
	}
	return "未填写"
}

func (d *compileDraft) overview(tagsBlacklist []string) string {
	c := d.draft.Content
	var sb strings.Builder
	sb.WriteString("<draft_overview>\n")
	for _, f := range [][2]string{
		{"name", d.draft.Name}, {"tags", d.draft.Tags}, {"reward_concept", d.rewardConcept},
		{"invest_focus", c.InvestFocus}, {"setting", c.Setting}, {"intro", c.Intro},
		{"game_start_time", c.GameStartTime}, {"map_description", c.MapDescription},
		{"playthrough_outline", c.PlaythroughOutline}, {"mythos_core", c.MythosCore},
	} {
		fmt.Fprintf(&sb, "%s: %s\n", f[0], fieldState(f[1]))
	}
	var coreTruth string
	if c.KeeperAppendix != nil {
		coreTruth = c.KeeperAppendix.CoreTruth
	}
	fmt.Fprintf(&sb, "core_truth: %s\n", fieldState(coreTruth))
	for _, section := range compileKeyedSections {
		keys := d.sectionKeys(section)
		fmt.Fprintf(&sb, "%s（%d）：%s\n", section, len(keys), joinOrNone(keys))
	}
	fmt.Fprintf(&sb, "timeline（%d）\n", len(c.Timeline))
	sb.WriteString("</draft_overview>\n")
	issues := d.gateIssues(tagsBlacklist)
	if len(issues) == 0 {
		sb.WriteString("<gate_issues count=\"0\">此刻提交可以通过校验。</gate_issues>")
		return sb.String()
	}
	fmt.Fprintf(&sb, "<gate_issues count=\"%d\">\n", len(issues))
	for i, issue := range issues {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, issue)
	}
	sb.WriteString("</gate_issues>")
	return sb.String()
}

func (d *compileDraft) sectionJSON(section string) string {
	c := d.draft.Content
	var v any
	switch section {
	case "overview":
		v = map[string]any{
			"name": d.draft.Name, "tags": d.draft.Tags, "reward_concept": d.rewardConcept,
			"invest_focus": c.InvestFocus, "tone_tags": c.ToneTags, "setting": c.Setting, "intro": c.Intro,
			"game_start_time": c.GameStartTime, "map_description": c.MapDescription,
			"playthrough_outline": c.PlaythroughOutline,
		}
	case "keeper_notes":
		k := models.KeeperAppendix{}
		if c.KeeperAppendix != nil {
			k = *c.KeeperAppendix
		}
		v = map[string]any{
			"mythos_core": c.MythosCore, "core_truth": k.CoreTruth, "antagonist_dossier": k.AntagonistDossier,
			"difficulty_down": k.DifficultyDown, "difficulty_up": k.DifficultyUp, "solo_advice": k.SoloAdvice,
			"group_advice": k.GroupAdvice, "horror_tips": k.HorrorTips, "theme_guidance": k.ThemeGuidance,
		}
	case "scenes":
		v = c.Scenes
	case "npcs":
		v = c.NPCs
	case "clues":
		clues := make([]clueArg, len(c.Clues))
		for i, cl := range c.Clues {
			clues[i] = clueArg{Key: d.clueKeys[i], ClueData: cl}
		}
		v = clues
	case "endings":
		v = c.Endings
	case "mechanics":
		v = c.Mechanics
	case "timeline":
		v = c.Timeline
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("<draft_section name=%q error=%q/>", section, err.Error())
	}
	return fmt.Sprintf("<draft_section name=%q>\n%s</draft_section>", section, buf.String())
}

// ---------------------------------------------------------------------------
// 提交关卡
// ---------------------------------------------------------------------------

// gateIssues 汇总此刻提交会遇到的全部问题；为空表示可以通过。
func (d *compileDraft) gateIssues(tagsBlacklist []string) []string {
	issues := validateDraftCompatibility(d.draft)
	issues = append(issues, checkScenarioTagsOverlap(d.draft.Tags, tagsBlacklist)...)
	if strings.TrimSpace(d.rewardConcept) == "" {
		issues = append(issues, "reward_concept 为空：请从故事文档已有的施动者、地点、人物与 mythos_anchor 中指认或合成一件调查员通关后能带走的实体载体（典籍/手稿/器物），写成一句话叙事概念（它是什么、原属谁或存于何处、与核心真相的关系），用 set_overview 填写；不得新增人物或地点、不得改动结局、不写规则数值")
	}
	if hits := d.bannedKeywordHits(); len(hits) > 0 {
		issues = append(issues, fmt.Sprintf("以下字段出现了禁用关键词：%s。这些字段由你自主设计而非照抄故事文档，请换用其他具体说法重写，不得使用财务审计类桥段，也不得出现与COC模组主题不符的不当内容", strings.Join(hits, "；")))
	}
	return issues
}

// NOTE: 只检查 name/tags/reward_concept 三个由 compiler 自主设计的字段；正文其余字段忠实转录自已通过校验的故事文档。
func (d *compileDraft) bannedKeywordHits() []string {
	fields := [][2]string{{"name", d.draft.Name}, {"tags", d.draft.Tags}, {"reward_concept", d.rewardConcept}}
	var hits []string
	for _, f := range fields {
		kws := findKeywords(f[1], bannedStoryCliches)
		kws = append(kws, findKeywords(f[1], bannedStoryContent)...)
		if len(kws) > 0 {
			hits = append(hits, fmt.Sprintf("%s(%s)", f[0], strings.Join(kws, "、")))
		}
	}
	return hits
}
