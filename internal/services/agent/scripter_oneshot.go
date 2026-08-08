// scripter_oneshot.go — OneshotResult type, translator sub-agent, blacklist
// helpers, and the repair/logic-review machinery shared by the story→compile
// pipeline (see scripter_story.go / scripter_compile.go for the two
// generation stages themselves).
//
// runOneshotArchitectLoop remains here because repairOneshotDraft reuses it
// to patch an already-compiled ScenarioDraft (translate_anchor + submit),
// independent of the story stage's own tool loop (runStoryArchitectLoop).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// ---------------------------------------------------------------------------
// Output type
// ---------------------------------------------------------------------------

// OneshotResult is the JSON payload inside the architect's submit tool call.
type OneshotResult struct {
	RewardConcept string `json:"reward_concept"`
	// ScenarioDraft fields
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Author      string                 `json:"author"`
	Tags        string                 `json:"tags"`
	MinPlayers  int                    `json:"min_players"`
	MaxPlayers  int                    `json:"max_players"`
	Difficulty  string                 `json:"difficulty"`
	Content     models.ScenarioContent `json:"content"`
}

// oneshotDraftJSONSchema 是 submit_compiled_scenario（scripter_compile.go）与
// submit（本文件 runOneshotArchitectLoop）共用的工具参数 JSON Schema，逐字段对应
// OneshotResult/models.ScenarioContent 的真实结构；两个工具直接把它整体作为
// Parameters（原生参数，字段就是调用参数本身），不再包一层"draft"键。
//
// NOTE: 此前两处工具的参数都是 {"type":"object","properties":{"draft":{"type":"object",
// "description":"见xxx_schema"}}}——draft 的真正字段结构只存在于旁边的自然语言 prompt 里，
// 工具的正式 schema 里这个唯一参数是个没有 properties 的空壳。部分模型/兼容端点的原生
// function calling 依赖这个正式 schema 做结构化解码；draft 需要一次性装下整份剧本
// （scenes/npcs/clues/endings 等十余个嵌套字段），空壳 schema 会让这类模型在没有结构可循
// 的情况下长时间生成推理内容却始终无法收敛出合法的工具调用，最终 content 和 tool_calls
// 双双为空（表现为"LLM 一直返回空字符串"）。修复分两步：① 把结构显式声明出来；
// ② 去掉多余的 draft 包装层，让工具参数本身就是结构化数据（原生参数），而不是"一个参数、
// 里面塞一整块JSON"。
const oneshotDraftJSONSchema = `{
	"type": "object",
	"description": "完整oneshotResult JSON对象",
	"properties": {
		"reward_concept": {"type": "string", "description": "逐字等于reward_concept输入，输入为空则留空字符串"},
		"name": {"type": "string", "description": "剧本标题；无明确标题时从文档内具体名词提炼，不用低语/回响/深渊/阴影/凝视/苏醒/沉睡/诅咒等滥用词"},
		"author": {"type": "string", "description": "固定为agent-team"},
		"tags": {"type": "string", "description": "2-3个逗号分隔标签，指向本剧本独有的核心叙事装置/桥段；须避开recent_scenario_tags_blacklist"},
		"min_players": {"type": "integer"},
		"max_players": {"type": "integer"},
		"difficulty": {"type": "string", "description": "如 normal"},
		"content": {
			"type": "object",
			"properties": {
				"system_prompt": {"type": "string", "description": "KP三项协议（时间推进/信息分层/不主动引导）+ 核心真相与mythos_anchor必要性 + 施动者细化设定，不得压缩为一句话"},
				"setting": {"type": "string", "description": "表层情境原文或忠实改写，必须保留文档中嵌入的具体年月日"},
				"tone_tags": {"type": "array", "items": {"type": "string"}, "description": "必须逐字等于diversity_constraints.tone_tags"},
				"horror_mode": {"type": "string", "description": "必须逐字等于diversity_constraints.horror_mode"},
				"invest_focus": {"type": "string", "description": "必须逐字等于diversity_constraints.invest_focus"},
				"intro": {"type": "string", "description": "调查员到场情境与基本理由；不列出、不推荐、不暗示任何具体行动或下一步"},
				"game_start_slot": {"type": "integer", "description": "0-47，每槽30分钟；未写明具体时刻时取16"},
				"map_description": {"type": "string", "description": "按地点关系概括的文字地图，体现可回访、可交叉验证的调查网络"},
				"playthrough_outline": {"type": "string", "description": "由编译器依据全文脉络归纳的逐场景流程大纲，每个场景写明进入条件、可接触人物、可得发现、通向哪里与分支"},
				"mythos_anchor": {"type": "string", "description": "必须逐字等于mythos_anchor输入"},
				"scenes": {
					"type": "array",
					"description": "通读全文识别出的所有调查员会实际走到的地方，无论是否有独立小标题",
					"items": {
						"type": "object",
						"properties": {
							"id": {"type": "string", "description": "snake_case英文标识"},
							"name": {"type": "string"},
							"description": {"type": "string", "description": "合并该地点在文中散落的全部信息：直接可见与可感知的、深查才能发现的（含所需检定）、可能发生的危险、通往哪些地方"},
							"triggers": {"type": "array", "items": {"type": "string"}, "description": "默认[\"available_from_start\"]，仅文档明确写出解锁条件时才用条件触发"}
						},
						"required": ["id", "name", "description", "triggers"]
					}
				},
				"npcs": {
					"type": "array",
					"description": "通读全文识别出的所有有名有姓、调查员可能接触到的人物；他们通常介绍在其所在地点的段落里",
					"items": {
						"type": "object",
						"properties": {
							"name": {"type": "string"},
							"description": {"type": "string", "description": "合并该人物在文中散落的全部信息：身份与营生、他想要什么、正在做什么、瞒着或不愿说的事、标志性细节、与他人的关系"},
							"attitude": {"type": "string", "description": "文档写明的初始态度"},
							"stats": {"type": "object", "additionalProperties": {"type": "integer"}, "description": "COC7标准属性：STR/CON/SIZ/DEX/APP/INT/POW/EDU/SAN/HP/MP"},
							"skills": {"type": "object", "additionalProperties": {"type": "integer"}, "description": "按职业身份3-6项最相关技能，键为技能名，值为COC7标准范围（普通人类通常15-75）"},
							"spells": {"type": "array", "items": {"type": "string"}, "description": "仅文档明确写明会施法者才填，普通人类留空数组"}
						},
						"required": ["name", "description", "attitude", "stats"]
					}
				},
				"clues": {
					"type": "array",
					"description": "通读全文识别出的所有调查员能亲自获得的具体发现；至少2条nature为真实且互相独立可组合",
					"items": {
						"type": "object",
						"properties": {
							"summary": {"type": "string", "description": "写清这条发现是什么、从哪来、调查员由此知道了什么"},
							"source": {"type": "string"},
							"skill_check": {"type": "string", "description": "可留空"},
							"on_success": {"type": "string", "description": "可留空"},
							"on_failure": {"type": "string", "description": "可留空"},
							"nature": {"type": "string", "enum": ["真实", "隐藏", "误导"]}
						},
						"required": ["summary", "nature"]
					}
				},
				"endings": {
					"type": "array",
					"description": "通读全文识别出的所有互不相同的收场，每种独立收场都要对应一个ending，不得合并或省略",
					"items": {
						"type": "object",
						"properties": {
							"name": {"type": "string"},
							"trigger": {"type": "string", "description": "保持如果[条件]，则[处境变化]的条件句结构"},
							"description": {"type": "string"},
							"san_reward": {"type": "string", "description": "如恢复1d6/损失1d6"},
							"is_failure": {"type": "boolean", "description": "标记灾难/失败向结局"}
						},
						"required": ["name", "trigger"]
					}
				},
				"timeline": {
					"type": "array",
					"description": "过去线痕迹或当天推进的时间节点；文档未写明则留空数组",
					"items": {
						"type": "object",
						"properties": {
							"time": {"type": "string"},
							"event": {"type": "string", "description": "中性事实记录句（谁在何时做了什么/什么状态发生变化）；不引用人物原话、不含引号对白"},
							"phase": {"type": "string", "enum": ["past", "current"]}
						},
						"required": ["time", "event"]
					}
				},
				"keeper_appendix": {
					"type": "object",
					"description": "难度调节/单双人团建议/恐怖呈现提示；文档未提供则整体省略（null）",
					"properties": {
						"difficulty_down": {"type": "string"},
						"difficulty_up": {"type": "string"},
						"solo_advice": {"type": "string"},
						"group_advice": {"type": "string"},
						"horror_tips": {"type": "string"},
						"theme_guidance": {"type": "string"}
					}
				},
				"entry_identities": {
					"type": "array",
					"description": "不同职业调查员的差异化入场方式；文档未区分职业入场则留空数组",
					"items": {
						"type": "object",
						"properties": {
							"profession": {"type": "string"},
							"init_resource": {"type": "string"},
							"init_limit": {"type": "string"},
							"recommend_clues": {"type": "string"}
						},
						"required": ["profession", "init_resource"]
					}
				},
				"mechanics": {
					"type": "array",
					"description": "可量化追踪的机制（如计数器、行动时钟），仅供KP参考；文档未设计此类机制则留空数组",
					"items": {
						"type": "object",
						"properties": {
							"name": {"type": "string"},
							"type": {"type": "string", "enum": ["counter", "clock", "tracker"]},
							"description": {"type": "string"},
							"stages": {
								"type": "array",
								"items": {
									"type": "object",
									"properties": {
										"label": {"type": "string"},
										"effect": {"type": "string"},
										"trigger": {"type": "string"}
									},
									"required": ["label"]
								}
							}
						},
						"required": ["name", "type", "description"]
					}
				}
			},
			"required": ["system_prompt", "setting", "tone_tags", "horror_mode", "invest_focus", "intro", "game_start_slot", "map_description", "playthrough_outline", "mythos_anchor", "scenes", "npcs", "clues", "endings"]
		}
	},
	"required": ["reward_concept", "name", "author", "tags", "min_players", "max_players", "difficulty", "content"]
}`

func (r OneshotResult) toScenarioDraft() ScenarioDraft {
	return ScenarioDraft{
		Name: r.Name, Description: r.Description,
		Author: r.Author, Tags: r.Tags,
		MinPlayers: r.MinPlayers, MaxPlayers: r.MaxPlayers,
		Difficulty: r.Difficulty, Content: r.Content,
	}
}

// oneshotResultExample is a fully-populated example scenario reused by every
// schema/repair prompt that needs to show the complete oneshotResult structure.
var oneshotResultExample = OneshotResult{
	RewardConcept: "与食尸鬼有关的古籍手稿",
	Name:          "示例模组",
	Description:   "一座宁静小镇的老图书馆正在整理一批新到的捐赠藏书，馆方邀请你们前来协助编目。一次寻常的委托，一段关于旧书与小镇的日子就此开始。",
	Author:        "agent-team",
	Tags:          "食尸鬼,身后遗物,墓地图书馆",
	MinPlayers:    1,
	MaxPlayers:    4,
	Difficulty:    "normal",
	Content: models.ScenarioContent{
		SystemPrompt:   "你是KP，管理会自行推进的局势，不主动把调查员引向答案；按时间推进后果，按信息分层给出线索。【KP独有】内部真相：失窃的书是Douglas生前的旧藏，他死后被镇北墓地的食尸鬼群落接纳、保留了生前记忆，如今潜回取回属于自己的东西。核心恐惧不在于怪物的样貌，而在于「死亡并非终点、逝者以非人的方式继续存在」这一认知对调查员世界观的不可逆冲击。",
		Setting:        "1924年9月3日，初秋的傍晚，你们受镇图书馆之邀前来协助整理一批新捐赠的藏书。馆内灯光温暖，管理员热情地引你们入座，窗外街区安静而寻常。",
		ToneTags:       []string{"forbidden-knowledge", "cosmic-dread", "occult-noir"},
		HorrorMode:     "forbidden_knowledge",
		InvestFocus:    "artifact_theft",
		Intro:          "你们受镇图书馆之邀，来帮着整理清点一批新到的捐赠藏书。大厅里，馆员正在前台核对今天的编目单，先去打个招呼也好；门口的访客登记簿还空着一栏，顺手签上名字；再往里走走，认认书架区和档案室的门各朝哪边开。",
		GameStartSlot:  16,
		MapDescription: "【文字地图】图书馆→书架区↔档案室↔墓地。",
		PlaythroughOutline: "开场：调查员受邀到图书馆大厅协助编目，馆员提及近期失窃。" +
			"图书馆大厅（入口条件：开局即可进入；可接触NPC：守墓人Henrik；可得线索：被取走的书出自同一捐赠者；出口：查阅捐赠登记签收单可解锁书架区细节，安抚或盘问Henrik可解锁档案室话题）→" +
			"档案室（入口条件：从大厅获知捐赠者身份后可入；可得线索：窗台泥土成分与镇北墓地一致；出口：与「失窃书目的共同点」组合后指向墓地，调查员可选择连夜赶赴或次日再去）→" +
			"墓地（入口条件：完成泥土与书目两条线索组合；可得线索：取书者是食尸鬼；分支：调查员归还藏书导向「书归其主」，选择声张致图书馆关闭则导向「永久关闭」）。",
		MythosAnchor: "食尸鬼（Ghoul）：COC7规则书已收录；具体属性按规则书裁定。",
		Scenes: []models.SceneData{
			{
				ID:          "library_main",
				Name:        "图书馆大厅",
				Description: "可见：失窃公告。可发现：书目来自同一捐赠者。杠杆：公开规律会导致图书馆关闭。风险：拖延三天后永久关闭。出口：书架区、档案室。感官：潮湿泥土气息与旧纸味格格不入。",
				Triggers:    []string{"available_from_start"},
			},
		},
		NPCs: []models.NPCData{
			{
				Name:        "守墓人Henrik",
				Description: "公开身份：图书馆保安。议程：维护秩序。秘密：曾处理Douglas遗物。标志性细节：说话时总用拇指摩挲一把黄铜钥匙。关系：受馆长雇佣，与Douglas生前是牌友。",
				Attitude:    "警惕、简短",
				Stats:       map[string]int{"STR": 55, "CON": 60, "SIZ": 65, "DEX": 50, "APP": 40, "INT": 55, "POW": 50, "EDU": 55, "SAN": 50, "HP": 12, "MP": 10},
				Skills:      map[string]int{"侦查": 50, "格斗(斗殴)": 45, "潜行": 40, "聆听": 55},
				Spells:      []string{},
			},
		},
		Clues: []models.ClueData{
			{
				Summary:    "被取走的每一本都出自同一位捐赠者Douglas的旧藏",
				Source:     "书架区，核对编目卡与捐赠登记",
				SkillCheck: "图书馆使用",
				OnSuccess:  "锁定「目标是Douglas旧物」，但无法解释谁在取、为何取",
				OnFailure:  "馆员闲聊中主动提起捐赠者名字，调查员仍能获知这一事实",
				Nature:     "真实",
			},
			{
				Summary:    "窗台上的泥土矿物成分与镇北墓地一致，而非街道或花园的土",
				Source:     "档案室，检查窗台并做成分比对",
				SkillCheck: "侦查",
				OnSuccess:  "与「失窃书目的共同点」组合，可推翻活人盗贼说，指向墓地方向",
				OnFailure:  "守墓人闲聊中提到墓地土质特殊，调查员仍可获得同等信息",
				Nature:     "真实",
			},
			{
				Summary:    "取书者是食尸鬼（Ghoul）——死者变形后的存在，保留生前记忆与执念；SAN检定1/1d6",
				Source:     "墓地",
				SkillCheck: "克苏鲁神话",
				OnSuccess:  "真正的理智代价不来自它的外形，而来自承认「死亡并非终点、逝者仍以非人方式延续」这一认知本身；具体属性按规则书裁定",
				Nature:     "隐藏",
			},
			{
				Summary:   "守墓人亲眼见过夜里翻墙的佝偻身影，动作迟缓、指甲缝里全是泥，据此坚称是某个惯偷活人趁夜盗墓",
				Source:    "大厅，守墓人Henrik",
				OnSuccess: "这些体征全部属实，真相揭晓后仍成立；调查员一旦否定「活人盗贼」，反而会去比对泥土来源与墓地痕迹，被推向真正的方向",
				Nature:    "误导",
			},
		},
		Endings: []models.EndingData{
			{
				Name:        "书归其主",
				Trigger:     "如果调查员让Douglas重获藏书，则他退隐墓地",
				Description: "书籍谜团以悲哀收场，图书馆恢复平静",
				SANReward:   "恢复1d4",
			},
			{
				Name:        "永久关闭",
				Trigger:     "如果图书馆永久关闭，则Douglas转向其他途径",
				Description: "某个新目标成为下一个遭遇者，威胁并未真正解除",
				SANReward:   "损失1d6",
				IsFailure:   true,
			},
		},
		Timeline: []models.TimelineEvent{
			{Time: "六周前", Event: "Douglas Whitfield病逝，侄女Margaret按遗愿将藏书捐赠图书馆", Phase: "past"},
			{Time: "开局当晚", Event: "若无人阻止，取书者将在闭馆后潜入书架区，带走下一批目标书籍", Phase: "current"},
		},
		KeeperAppendix: &models.KeeperAppendix{
			DifficultyDown: "让守墓人主动提供泥土线索，缩短调查员定位墓地的时间",
			DifficultyUp:   "取书者提前带走部分证据，迫使调查员更依赖NPC口述重建事实",
			SoloAdvice:     "单人团可让守墓人承担更多主动提示功能",
		},
	},
}

// oneshotExample is the JSON schema example used for parsing/repair prompts.
var oneshotExample = marshalExample(oneshotResultExample)

// StoryOutput is the story architect's final submission: a free-text story
// document plus the confirmed mythos anchor and optional reward concept.
// It carries no strongly-typed scene/NPC/clue structure — the compiler stage
// is responsible for extracting structure from Document.
type StoryOutput struct {
	Document      string `json:"story_document"`
	MythosAnchor  string `json:"mythos_anchor"`
	RewardConcept string `json:"reward_concept"`
}

// ---------------------------------------------------------------------------
// System prompt
// ---------------------------------------------------------------------------

// humanWritingRules 是人写化写作标准；architect 生成与 QA 审查共用同一份，避免双方标准漂移。
const humanWritingRules = `- 具体性：散文要落在具体名词上——人名、地名、年份、器物、气味、价钱、路名是取材范围，不是逐项打卡的清单；具体性看细节准不准，不看细节多不多；同一个功能点用一个细节钉住就够（如指路只给一个标志，不叠第二个）；不堆叠"神秘的/诡异的/不祥的"等抽象形容词
- 一段一焦点：一句话只推进一件事，一句里至多引入一个新专名或新意象；一段话围绕一个焦点纵深展开（远→近、整体→局部、看见→听见），不横向铺开多个互不相关的对象；三个以上互不相关的细节禁止用顿号或"一边…一边…"并列——细节之间要有因果、转折、时间先后或感知顺序的连接，禁止"她耳朵背、记性好"式的属性清单句，人物特征通过动作或对话带出
- 细节要挂钩：每个具体细节须至少服务一项——刻画人物、埋设后文、承载情绪或关系、给出可行动的调查入口；自检法是删掉它文字是否受损，无损即删；宁少一个活细节，不多一个死细节
- 细节复用优先：与其引入新道具，优先让已出现的细节再次出现、并让第二次出现改变含义（初见是日常记录，再见携带情绪或异样）；一段散文最多充分展开一个主细节，其余一笔带过
- 禁止编号与模板腔：正文不写①②③、1.2.3.、"首先/其次/最后"式结构，也不写行首要素标签（"可见信息：""议程：""秘密：""性质：真实"）；交代零散事实时可以用短列表，但每条必须是完整的句子
- 句式错落：长短句交替；不连续使用三个以上结构雷同的句子；不写成对仗排比
- 标题像人起的：不用"低语/回响/深渊/阴影/凝视/苏醒/沉睡/诅咒"等滥用词；优先取材于剧本内的具体名词（地名、物件、日期、一句当地人的话）
- NPC人味：每个重要NPC给一个标志性小细节（口头禅、习惯动作、随身物件、外貌特征选其一）；NPC之间至少存在两条现实关系（亲属/雇佣/债务/旧怨/邻里）；可以保留一个与主线无关的纯地方色彩NPC
- 密度不均：允许一处地点信息厚重、另一些地点只有一两笔；不给每个地点机械配满同样数量的要素`

const (
	// proseVoiceScopeStory 用于 story 阶段：声线覆盖整份成稿。
	proseVoiceScopeStory = "适用范围：整份故事文档都按该声线书写——开头、地点、人物、线索、结局是同一位作者写的同一份读物；不使用信头、落款、日期行等格式排版。"
	// proseVoiceScopeCompile 用于 compile/repair 阶段：产物是给KP与Director读的结构化数据，以信息完整为先。
	proseVoiceScopeCompile = "适用范围：只作用于 name/setting/intro 等玩家可见散文的用词与节奏；不改变字段的功能与信息要求；不使用信头、落款、日期行等格式排版；scenes/npcs/clues 等结构化字段以信息完整为先，不追求声线。"
)

// proseVoiceBlock 把随机抽取的作者声线注入用户消息；只约束文风质感，不改变字段功能。
// scope 按调用阶段传入 proseVoiceScopeStory 或 proseVoiceScopeCompile。
func proseVoiceBlock(constraints ScripterConstraints, scope string) string {
	voice := strings.TrimSpace(constraints.ProseVoice)
	if voice == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<prose_voice>\n")
	sb.WriteString(fmt.Sprintf("voice: %s\n", voice))
	if guide := proseVoiceGuides[voice]; guide != "" {
		sb.WriteString(fmt.Sprintf("guide: %s\n", guide))
	}
	sb.WriteString(scope + "\n")
	sb.WriteString("</prose_voice>")
	return sb.String()
}

// repairSystemPrompt 是结构修复/逻辑修复阶段的专用提示词：只按 must_fix 修补
// previous_draft，不重新创作。历史上这里复用 oneshotSystemPrompt（完整创作指南 +
// schema），每次修复都要重复发送约200行创作指令，与"最小改动"的修复指令相互冲突，
// 还会无条件触发 translate_anchor → Translator→Lawyer 链条。
func repairSystemPrompt() string {
	return `<role>COC7剧本修复器</role>
<task>
<previous_draft>是一份已编译完成的COC7剧本结构化JSON。<must_fix>列出了必须修复的问题清单。你的任务是逐条修复这些问题并重新提交完整draft，不做任何must_fix之外的设计改动。

修复纪律：
- 逐条针对must_fix修复到位；除修复所需外，不改动任何其他字段、人名、地名、数值、情节或文风
- 不得更换已确认的神话元素（content.mythos_anchor）——它已由规则书翻译确认
- 不得改变<diversity_constraints>中horror_mode/invest_focus/tone_tags的值
- 仅当must_fix涉及神话元素本身时，才调用translate_anchor核验；否则不要调用
- 修复神话本质说明时，引用的法术/物品/怪物/机制名必须与must_fix或<previous_draft>中已确认的规则书元素一致，不得新造
- setting/intro必须保持冷开场：中性日常，不剧透真相、不渲染恐怖
</task>
<tools>
- translate_anchor：仅当must_fix涉及神话元素时，将一个创意概念翻译为COC7规则书中最匹配的具体元素
- submit：提交修复后的完整剧本JSON；必须单独一轮调用，draft字段为完整oneshotResult JSON对象
</tools>
<draft_schema>
submit.draft 必须保持与<previous_draft>完全相同的字段结构：
{
  "reward_concept": "通关奖励叙事概念（若无则留空字符串）",
  "name": "剧本名称",
  "author": "保持previous_draft原值",
  "tags": "2-3个逗号分隔的标签，须避开<recent_scenario_tags_blacklist>",
  "min_players": 1,
  "max_players": 4,
  "difficulty": "normal",
  "content": {
    "system_prompt": "KP三项协议（时间推进/信息分层/不主动引导）+ 核心真相 + mythos_anchor必要性 + 施动者细化设定",
    "setting": "表层日常局势，须保留已嵌入的具体年月日",
    "tone_tags": ["必须等于diversity_constraints.tone_tags"],
    "horror_mode": "必须等于diversity_constraints.horror_mode",
    "invest_focus": "必须等于diversity_constraints.invest_focus",
    "intro": "入场情境；不列出、不推荐、不暗示任何具体行动或下一步",
    "game_start_slot": 16,
    "map_description": "文字地图",
    "playthrough_outline": "逐场景流程大纲：进入条件/可接触人物/可得发现/通向哪里与分支",
    "mythos_anchor": "已确认的COC7元素全称，不得更换",
    "scenes": [{"id":"snake_case","name":"...","description":"直接可见与可感知的、深查才能发现的（含所需检定）、可能发生的危险、通往哪些地方","triggers":["available_from_start"]}],
    "npcs": [{"name":"...","description":"身份与营生、想要什么、正在做什么、瞒着或不愿说的事、标志性细节、与他人的关系","attitude":"...","stats":{"STR":50,"CON":50,"SIZ":50,"DEX":50,"APP":50,"INT":60,"POW":50,"EDU":60,"SAN":50,"HP":10,"MP":10},"skills":{"侦查":50},"spells":[]}],
    "clues": [{"summary":"...","source":"...","skill_check":"可留空","on_success":"...","on_failure":"失败时不卡关的替代信息","nature":"真实|隐藏|误导"}],
    "endings": [{"name":"...","trigger":"如果[条件]，则[处境变化]","description":"...","san_reward":"恢复1d6","is_failure":false}],
    "timeline": [{"time":"...","event":"中性事实记录句，不含引号引用的人物原话","phase":"past|current"}],
    "keeper_appendix": {"difficulty_down":"...","difficulty_up":"...","solo_advice":"...","group_advice":"...","horror_tips":"...","theme_guidance":"..."},
    "entry_identities": [{"profession":"...","init_resource":"...","init_limit":"...","recommend_clues":"..."}],
    "mechanics": [{"name":"...","type":"counter|clock|tracker","description":"...","stages":[{"label":"...","effect":"...","trigger":"..."}]}]
  }
}
</draft_schema>`
}

// ---------------------------------------------------------------------------
// Architect loop
// ---------------------------------------------------------------------------

// runOneshotArchitectLoop 驱动 architect 工具循环：translate_anchor（可多次）+ submit（独占一轮）。
// 目前仅由 repairOneshotDraft 复用，用于在已编译草案上做 translate_anchor 校验 + 结构修复。
func runOneshotArchitectLoop(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage, stageName string) (OneshotResult, error) {
	stageName = firstNonEmpty(stageName, "oneshot_architect")

	tools := []scripterTool{
		translateAnchorTool("将一个创意概念翻译为COC7规则书中最匹配的具体元素；提交前必须至少调用一次"),
		generateNPCNameTool(),
		{
			solo: true,
			def: llm.ToolDefinition{
				Name:        toolNameSubmit,
				Description: "提交完整剧本；参数即完整oneshotResult本体（非嵌套在draft字段下）；只有在translate_anchor确认元素后才调用；必须单独一轮调用",
				Parameters:  jsonSchemaObject(oneshotDraftJSONSchema),
			},
		},
	}

	var submitted *OneshotResult
	dispatch := func(ctx context.Context, call llm.ToolCall) toolOutcome {
		switch call.Name {
		case toolNameTranslateAnchor:
			var args translateAnchorArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: translate_anchor参数不是合法JSON，请重新调用。"}
			}
			return toolOutcome{result: executeOneshotTranslateAnchor(ctx, room, args.Concept, args.Reason)}
		case toolNameGenerateNPCName:
			return dispatchGenerateNPCName(ctx, room, call)
		case toolNameSubmit:
			var result OneshotResult
			if err := json.Unmarshal([]byte(call.Arguments), &result); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: submit参数不是合法JSON，请重新调用。"}
			}
			if strings.TrimSpace(result.Name) == "" {
				return toolOutcome{reject: "SYSTEM REJECT: submit的name字段不能为空。"}
			}
			submitted = &result
			return toolOutcome{result: "已收到，剧本已提交。", done: true}
		default:
			return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 此阶段只允许translate_anchor/generate_npc_name/submit，不允许%s。", call.Name)}
		}
	}

	const maxRounds = 30
	if err := runScripterToolLoop(ctx, room, room.architect, stageName, msgs, tools, maxRounds, dispatch); err != nil {
		return OneshotResult{}, err
	}
	return *submitted, nil
}

// ---------------------------------------------------------------------------
// translate_anchor execution — calls translator sub-agent
// ---------------------------------------------------------------------------

// executeOneshotTranslateAnchor 由 oneshot architect repair 和 story architect
// 两个循环共用；concept/reason 直接来自各自 translate_anchor 工具调用的解码参数。
func executeOneshotTranslateAnchor(ctx context.Context, room *scripterRoom, concept, reason string) string {
	sessionID := scripterSessionID(ctx, room)
	concept = strings.TrimSpace(concept)
	if concept == "" {
		return `<translate_anchor_result error="concept字段为空，无法翻译"/>`
	}
	reason = strings.TrimSpace(reason)
	log.Printf("[scripter:oneshot_translate_anchor] session=%s concept=%q reason=%q", sessionID, truncateRunes(concept, 200), truncateRunes(reason, 200))
	conclusion, err := runOneshotTranslatorAgent(ctx, room, concept, reason)
	if err != nil {
		log.Printf("[scripter:oneshot_translate_anchor] session=%s error concept=%q err=%v", sessionID, truncateRunes(concept, 200), err)
		return fmt.Sprintf(`<translate_anchor_result concept=%q status="translator_error">%s</translate_anchor_result>`, concept, err.Error())
	}
	if conclusion == nil || strings.TrimSpace(conclusion.Status) == "" {
		return fmt.Sprintf(`<translate_anchor_result concept=%q status="no_result">translator未返回可用结论；可尝试调整概念描述重新翻译，或转向人类法师、诅咒物品、古老地点等方向。</translate_anchor_result>`, concept)
	}
	return fmt.Sprintf(`<translate_anchor_result concept=%q status=%q>%s</translate_anchor_result>`, concept, conclusion.Status, conclusion.Text())
}

// isTranslateAnchorFound 判断 translate_anchor 结果是否为成功匹配（status="found"）。
// 结果文本由 translatorConclusion 结构化字段渲染，状态在包装属性上，直接判属性即可；
// no_result / translator_error / uncertain / 空结果均视为未找到。
func isTranslateAnchorFound(result string) bool {
	return strings.Contains(result, `status="found"`)
}

// ---------------------------------------------------------------------------
// Translator sub-agent (validates CoC element via lawyer/rulebook)
// ---------------------------------------------------------------------------

const oneshotTranslatorSystemPrompt = `<role>COC7规则书概念翻译专家</role>
<task>收到一个创意概念，将它翻译为COC7规则书中最匹配、可在剧本中使用的具体元素（实体/典籍/法术/诅咒物品/机制）。通过 ask_lawyer 工具向规则书专家提问，依据裁定综合，最后用 respond 工具以结构化字段返回翻译结论。</task>
<rules>
- 第一轮必须至少调用一次ask_lawyer；不得凭常识或记忆直接respond。
- 用户消息中的<recently_used_mythos_anchors>是硬性禁用列表；selected_anchor不得返回列表中的元素、别名或同源变体。
- 如果规则书裁定显示最匹配候选属于禁用列表，必须继续ask_lawyer寻找替代，或返回uncertain/no_result并给出非禁用fallback。
- ask_lawyer问题要具体，优先确认候选元素是否在规则书中存在、出处、核心机制和禁用边界。
- 不把lawyer原文无筛选地倾倒给architect；必须总结成可执行的翻译结论。
- 不得编造规则书不存在的正式名称、页码、数值或能力。
- 法术不允许任何变体，必须完全符合规则书描述。
- 除非概念本身明确要求法术/仪式是唯一能承载的机制，否则优先把创意概念翻译为实体锚点（神话生物/旧日支配者眷属，或施法的人类角色本身），不要翻译成脱离施动者的法术条目本身。
- 若最终仍需要翻译为法术，必须在回复中提醒：该法术必须由一个具体的实体（人、神话生物等）施放，且该实体才是故事应锚定的真正核心，法术只是其手段而非谜团根源。
- 翻译的结果必须直接来自规则书裁定，不能是基于规则书裁定的二次创作。
- 可以是合理的推导链条（例如： 规则书支持A，从A引发了B，B正好符合概念要求，那么B可以是selected_anchor，但必须在rulebook_basis里清晰说明推导链条和每一步的规则书依据）。
- 但推理链条的每一步都必须在规则书中有明确依据，不能凭常识或记忆自创。
- 如果找不到就直接返回没有，不要乱编。
</rules>`

// translatorConclusion 是 translator respond 工具的结构化结论，
// 字段与 respond 工具 schema 的属性一一对应，不再使用字符串内嵌 JSON。
type translatorConclusion struct {
	Status               string `json:"status"`
	SelectedAnchor       string `json:"selected_anchor"`
	RulebookBasis        string `json:"rulebook_basis"`
	UsableInterpretation string `json:"usable_interpretation"`
	MustAvoid            string `json:"must_avoid"`
	Fallback             string `json:"fallback"`
	BlacklistCheck       string `json:"blacklist_check"`
}

// Text 把结构化结论渲染为给 architect 阅读的结论文本。
func (c *translatorConclusion) Text() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "status: %s\n", c.Status)
	fmt.Fprintf(&sb, "selected_anchor: %s\n", c.SelectedAnchor)
	fmt.Fprintf(&sb, "rulebook_basis: %s\n", c.RulebookBasis)
	fmt.Fprintf(&sb, "usable_interpretation: %s\n", c.UsableInterpretation)
	fmt.Fprintf(&sb, "must_avoid: %s\n", c.MustAvoid)
	fmt.Fprintf(&sb, "fallback: %s\n", c.Fallback)
	fmt.Fprintf(&sb, "blacklist_check: %s", c.BlacklistCheck)
	return sb.String()
}

// oneshotTranslatorRespondTool 是 translator 的 respond 工具定义（solo，终止本轮循环）。
func oneshotTranslatorRespondTool() scripterTool {
	return scripterTool{
		solo: true,
		def: llm.ToolDefinition{
			Name:        toolNameRespond,
			Description: "返回最终翻译结论并退出；必须在至少一次ask_lawyer之后调用；必须单独一轮调用",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"status": {"type": "string", "enum": ["found", "no_result", "uncertain"], "description": "翻译结论状态"},
					"selected_anchor": {"type": "string", "description": "最匹配元素全称；无可靠匹配时写无"},
					"rulebook_basis": {"type": "string", "description": "来源和依据摘要；若为推导链条，逐步写明每步的规则书依据"},
					"usable_interpretation": {"type": "string", "description": "此元素如何承载原概念"},
					"must_avoid": {"type": "string", "description": "必须避免的未核验数值、能力或误用"},
					"fallback": {"type": "string", "description": "若status不是found，给architect的保守替代方向"},
					"blacklist_check": {"type": "string", "description": "确认selected_anchor不在最近使用元素禁用列表中"}
				},
				"required": ["status", "selected_anchor", "rulebook_basis", "usable_interpretation", "must_avoid", "fallback", "blacklist_check"]
			}`),
		},
	}
}

func runOneshotTranslatorAgent(ctx context.Context, room *scripterRoom, concept string, reason string) (*translatorConclusion, error) {
	// NOTE: translator 独立 provider/session key，不复用 lawyer；fail-fast，不退回 lawyer。
	if room.translator.provider == nil {
		return nil, fmt.Errorf("translator provider unavailable")
	}
	requestJSON, _ := json.Marshal(struct {
		Concept string `json:"concept"`
		Reason  string `json:"reason"`
	}{Concept: concept, Reason: reason})

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.translator.systemPrompt(oneshotTranslatorSystemPrompt)},
		{Role: "user", Content: fmt.Sprintf(`<translate_anchor_request>%s</translate_anchor_request>
<recently_used_mythos_anchors>
%s
</recently_used_mythos_anchors>
以上最近使用过的元素为硬性禁用列表：selected_anchor不得返回这些元素、同名别名、简称或明显同源变体；若最匹配候选命中禁用列表，必须继续查询替代候选或返回uncertain/no_result。`,
			string(requestJSON), formatMythosBlacklist(room.mythosBlacklist))},
	}

	tools := []scripterTool{
		askLawyerTool("向COC7规则书专家提出一个具体规则书问题；可多次调用"),
		oneshotTranslatorRespondTool(),
	}

	askedLawyer := false
	var conclusion *translatorConclusion
	dispatch := func(ctx context.Context, call llm.ToolCall) toolOutcome {
		switch call.Name {
		case toolNameAskLawyer:
			var args askLawyerArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: ask_lawyer参数不是合法JSON，请重新调用。"}
			}
			askedLawyer = true
			// NOTE: ask_lawyer 仍然走 room.lawyer，与 translator Chat 路由严格隔离。
			return toolOutcome{result: oneshotTranslatorAskLawyer(ctx, room, args.Question)}
		case toolNameRespond:
			if !askedLawyer {
				return toolOutcome{reject: "SYSTEM REJECT: respond前必须至少调用一次ask_lawyer。"}
			}
			var args translatorConclusion
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: respond参数不是合法JSON，请重新调用。"}
			}
			if strings.TrimSpace(args.Status) == "" || strings.TrimSpace(args.SelectedAnchor) == "" {
				return toolOutcome{reject: "SYSTEM REJECT: respond的status和selected_anchor字段不能为空。"}
			}
			if anchor := oneshotFindForbiddenAnchor(args.SelectedAnchor, room.mythosBlacklist); anchor != "" {
				return toolOutcome{reject: fmt.Sprintf(
					"SYSTEM REJECT: selected_anchor命中了最近使用元素禁用列表：%s。必须继续ask_lawyer寻找替代候选，或返回uncertain/no_result并给出非禁用fallback。", anchor)}
			}
			conclusion = &args
			return toolOutcome{result: "已收到，翻译结论已提交。", done: true}
		default:
			return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: translator只允许ask_lawyer/respond，不允许%s。", call.Name)}
		}
	}

	const maxRounds = 16
	if err := runScripterToolLoop(ctx, room, room.translator, "oneshot_translator", msgs, tools, maxRounds, dispatch); err != nil {
		return nil, err
	}
	return conclusion, nil
}

func oneshotTranslatorAskLawyer(ctx context.Context, room *scripterRoom, question string) string {
	sessionID := scripterSessionID(ctx, room)
	question = strings.TrimSpace(question)
	if question == "" {
		return `<ask_lawyer_result error="question字段为空，无法查询规则书"/>`
	}
	log.Printf("[scripter:oneshot_translator] session=%s ask_lawyer question=%q", sessionID, truncateRunes(question, 300))
	if room.lawyer.provider == nil {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="lawyer_unavailable">规则书专家不可用；不得声称已核验具体规则书元素。</ask_lawyer_result>`, question)
	}
	results := runLawyer(ctx, room.lawyer, question)
	if len(results) == 0 {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="no_result">规则书专家未返回可用裁定；应换一个更具体的候选继续提问，或在最终结论中标记no_result/uncertain。</ask_lawyer_result>`, question)
	}
	return fmt.Sprintf(`<ask_lawyer_result question=%q status="found">%s</ask_lawyer_result>`,
		question, formatLawyerResults(results))
}

// ---------------------------------------------------------------------------
// Blacklist helpers
// ---------------------------------------------------------------------------

// oneshotFindForbiddenAnchor 检查 selectedAnchor（respond 工具的结构化字段，
// 可能带括号别名等修饰）是否命中禁用列表；返回命中的禁用元素，未命中返回空串。
func oneshotFindForbiddenAnchor(selectedAnchor string, anchors []string) string {
	selected := strings.TrimSpace(selectedAnchor)
	if selected == "" || selected == "无" {
		return ""
	}
	normalizedSelected := oneshotNormalizeAnchorKey(selected)
	if normalizedSelected == "" {
		return ""
	}
	for _, anchor := range anchors {
		if n := oneshotNormalizeAnchorKey(anchor); n != "" && strings.Contains(normalizedSelected, n) {
			return anchor
		}
	}
	return ""
}

func oneshotNormalizeAnchorKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(
		" ", "", "\t", "", "\n", "", "\r", "",
		"（", "", "）", "", "(", "", ")", "",
		"「", "", "」", "", "《", "", "》", "", "[", "", "]", "",
		"：", "", ":", "", "，", "", ",", "", "。", "", ".", "", "、", "", "/", "",
		"-", "", "_", "",
	)
	return replacer.Replace(s)
}

func diversityConstraintsBlock(constraints ScripterConstraints) string {
	var sb strings.Builder
	sb.WriteString("<diversity_constraints>\n")
	sb.WriteString(fmt.Sprintf("horror_mode: %s\n", constraints.HorrorMode))
	if label := horrorModeChineseLabels[constraints.HorrorMode]; label != "" {
		sb.WriteString(fmt.Sprintf("horror_mode_zh: %s\n", label))
	}
	sb.WriteString(fmt.Sprintf("invest_focus: %s\n", constraints.InvestFocus))
	if label := investFocusChineseLabels[constraints.InvestFocus]; label != "" {
		sb.WriteString(fmt.Sprintf("invest_focus_zh: %s\n", label))
	}
	sb.WriteString(fmt.Sprintf("tone_tags: %s\n", strings.Join(constraints.ToneTags, ", ")))
	sb.WriteString("硬约束：本次submit.draft.content.horror_mode、invest_focus、tone_tags必须逐字使用上述值，不得自行替换、翻译、改名或省略。\n")
	sb.WriteString("含义：horror_mode指明神话力量介入人类世界的主要机制（非恐怖风格、美学或具体怪物）；invest_focus决定调查入口；tone_tags只约束文风、节奏、场面选择和NPC反应风格，不覆盖剧本事实、规则书裁定或工具结果。\n")
	sb.WriteString("</diversity_constraints>")
	return sb.String()
}

// ---------------------------------------------------------------------------
// Repair — patches an already-compiled ScenarioDraft against issue lists
// raised by validateDraftCompatibility / runStoryQAReview / runLogicReview.
// ---------------------------------------------------------------------------

func repairOneshotDraft(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, previous *ScenarioDraft, issues []string) (ScenarioDraft, error) {
	sessionID := scripterSessionID(ctx, room)
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)
	prevJSON, _ := json.Marshal(previous)

	userMsg := fmt.Sprintf(
		`<request_json>%s</request_json>
<constraints>%s</constraints>
%s
%s
<previous_draft>%s</previous_draft>
<recent_scenario_tags_blacklist>
%s
</recent_scenario_tags_blacklist>
<must_fix>
%s
</must_fix>
请修复上述问题并通过submit提交修复后的完整剧本JSON。逐条针对must_fix修复到位，除修复所需外不要改动其他内容；不要更换已确认的神话元素（mythos_anchor）；不得改变diversity_constraints中的horror_mode/invest_focus/tone_tags；仅当must_fix涉及神话元素本身时才调用translate_anchor核验；若需修复tags，须避开<recent_scenario_tags_blacklist>中的所有标签。`,
		string(reqJSON), string(constraintsJSON),
		diversityConstraintsBlock(constraints),
		proseVoiceBlock(constraints, proseVoiceScopeCompile),
		string(prevJSON),
		formatScenarioTagsBlacklist(room.tagsBlacklist),
		strings.Join(issues, "\n"),
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(repairSystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	logStagePrompt("oneshot_repair", sessionID, msgs)

	result, err := runOneshotArchitectLoop(ctx, room, msgs, "oneshot_repair_architect")
	if err != nil {
		return ScenarioDraft{}, fmt.Errorf("oneshot repair failed: %w", err)
	}

	draft := result.toScenarioDraft()
	log.Printf("[scripter:oneshot_repair] session=%s done name=%q scenes=%d npcs=%d clues=%d",
		sessionID, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	return draft, nil
}

// ---------------------------------------------------------------------------
// Shared review issue-list schema — used by runStoryQAReview (scripter_story.go)
// and runLogicReview below.
// ---------------------------------------------------------------------------

type qaReviewResult struct {
	Issues []string `json:"issues"`
}

// reportIssuesTool 是 qa_humanize / logic_review 共用的 report_issues 工具定义（solo，独占一轮）。
func reportIssuesTool(description string) scripterTool {
	return scripterTool{
		solo: true,
		def: llm.ToolDefinition{
			Name:        toolNameReportIssues,
			Description: description,
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"issues": {
						"type": "array",
						"items": {"type": "string"},
						"description": "问题清单，每条指明具体字段/段落及可执行的修改方向；按严重程度排序，最多8条；没有问题则给空数组"
					}
				},
				"required": ["issues"]
			}`),
		},
	}
}

// runReportIssuesTool 跑一次单工具（report_issues）循环并返回issues；room固定传nil，
// 保持与迁移前chatAndParseJSON对qa_humanize/logic_review恒传nil room一致（不发SSE exchange，
// 只落到ctx携带的生成日志）。
func runReportIssuesTool(ctx context.Context, handle agentHandle, stage string, msgs []llm.ChatMessage, description string) ([]string, error) {
	tools := []scripterTool{reportIssuesTool(description)}
	var issues []string
	dispatch := func(ctx context.Context, call llm.ToolCall) toolOutcome {
		if call.Name != toolNameReportIssues {
			return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 此阶段只允许report_issues，不允许%s。", call.Name)}
		}
		var args qaReviewResult
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			return toolOutcome{reject: "SYSTEM REJECT: report_issues参数不是合法JSON，请重新调用。"}
		}
		issues = args.Issues
		return toolOutcome{result: "已收到问题清单。", done: true}
	}
	const maxRounds = 4
	if err := runScripterToolLoop(ctx, nil, handle, stage, msgs, tools, maxRounds, dispatch); err != nil {
		return nil, err
	}
	return issues, nil
}

// ---------------------------------------------------------------------------
// Logic review — 用闲置的 QA agent 审查因果逻辑与神话一致性，问题清单喂给修复循环。
// 与 runStoryQAReview 平行但职责不同：这里只审逻辑可达性与编译忠实度，不审文风。
// ---------------------------------------------------------------------------

// logicReviewSystemPrompt 定义逻辑审查员的检查清单：因果可达性、推理路径、神话锚点必要性。
func logicReviewSystemPrompt() string {
	return `<role>剧本逻辑审查员</role>
<task>以<story_document>为唯一真相源，审查编译后COC剧本结构化数据的事实忠实度、因果逻辑与推理可达性。不审文风、不审用词。</task>
<checklist>
1. 事实忠实度：scenes/npcs/clues/endings中的人名、地名、因果关系、结局条件是否与<story_document>逐一对应，编译时有没有新增、删减或篡改任何事实？
2. 异常→线索→结论 可达性：从故事文本描述的当前异常出发，沿clues是否能到达故事文本的核心真相？是否存在至少两条独立路径？
3. NPC知情边界：每个NPC知道什么、不知道什么是否与故事文本一致？NPC不应知道超出其在故事中接触范围的信息
4. 误导排除后仍可推进：去掉所有nature="误导"的线索后，仅靠nature="真实"/"隐藏"的线索是否仍能推导到故事文本描述的真相？
5. 神话锚点必要性：mythos_anchor是否是故事文本中不可替换的关键因素（换成其他神话元素故事是否仍然成立）？
6. 洛氏恐怖强度：剧本是否体现了认知冲击、尺度错位、不可逆代价中的至少两项？而非仅靠血腥或惊吓桥段？
7. 结局条件因果：每个ending的trigger是否与故事文本描述的对应结局条件一致，且从不同终止状态逻辑推出？
8. Intro目的性：intro是否清楚交代了调查员到场的基本理由/表层任务，让玩家知道自己为何在此，且不列出、不推荐任何具体行动或下一步（行动留给玩家自行探索）？
</checklist>`
}

// buildLogicReviewPayload 送审因果逻辑相关字段：比人写化审查多送system_prompt/mythos/win-lose，
// 少送stats等与逻辑无关的噪音。
func buildLogicReviewPayload(draft *ScenarioDraft) map[string]any {
	scenes := make([]map[string]string, 0, len(draft.Content.Scenes))
	for _, s := range draft.Content.Scenes {
		scenes = append(scenes, map[string]string{"name": s.Name, "description": s.Description})
	}
	npcs := make([]map[string]string, 0, len(draft.Content.NPCs))
	for _, n := range draft.Content.NPCs {
		npcs = append(npcs, map[string]string{"name": n.Name, "description": n.Description, "attitude": n.Attitude})
	}
	return map[string]any{
		"name":          draft.Name,
		"system_prompt": draft.Content.SystemPrompt,
		"mythos_anchor": draft.Content.MythosAnchor,
		"mythos_core":   draft.Content.MythosCore,
		"scenes":        scenes,
		"npcs":          npcs,
		"clues":         draft.Content.Clues,
		"endings":       draft.Content.Endings,
	}
}

// runLogicReview 以 storyDoc 为真相源，返回因果逻辑与编译忠实度整改清单；storyDoc为空或
// 审查不可用/失败时返回nil（非致命，跳过即可）。
func runLogicReview(ctx context.Context, room *scripterRoom, draft *ScenarioDraft, storyDoc string) []string {
	if room == nil || room.qa.provider == nil || draft == nil || strings.TrimSpace(storyDoc) == "" {
		return nil
	}
	sessionID := scripterSessionID(ctx, room)
	payloadJSON, err := json.Marshal(buildLogicReviewPayload(draft))
	if err != nil {
		log.Printf("[scripter:logic_review] session=%s marshal payload failed: %v", sessionID, err)
		return nil
	}
	userMsg := fmt.Sprintf(`<story_document>%s</story_document>
<draft_for_review>%s</draft_for_review>
请按checklist审查以上剧本的因果逻辑、推理可达性与对故事文本的忠实度，通过report_issues工具提交问题清单。`,
		storyDoc, string(payloadJSON))
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.qa.systemPrompt(logicReviewSystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	result, err := runReportIssuesTool(ctx, room.qa, "logic_review", msgs,
		"提交本次审查发现的问题清单；没有问题时提交空数组")
	if err != nil {
		log.Printf("[scripter:logic_review] session=%s review failed: %v (skipping)", sessionID, err)
		return nil
	}
	issues := make([]string, 0, len(result))
	for _, issue := range result {
		if issue = strings.TrimSpace(issue); issue != "" {
			issues = append(issues, "[逻辑] "+issue)
		}
	}
	if len(issues) > 8 {
		issues = issues[:8]
	}
	return issues
}

// ---------------------------------------------------------------------------
// Normalization
// ---------------------------------------------------------------------------

func normalizeOneshotDraft(draft *ScenarioDraft, req ScenarioCreationRequest, author string, constraints ScripterConstraints, usedNPCNames map[string]bool, sessionIDs ...string) {
	if draft == nil {
		return
	}
	sessionID := ""
	if len(sessionIDs) > 0 {
		sessionID = sessionIDs[0]
	}
	author = strings.TrimSpace(author)
	if author == "" {
		author = defaultScripterAuthor
	}
	if strings.TrimSpace(draft.Name) == "" {
		draft.Name = "未命名剧本"
		log.Printf("[scripter:normalize] session=%s filled name=%q", sessionID, draft.Name)
	}
	if strings.TrimSpace(req.Name) != "" && draft.Name != strings.TrimSpace(req.Name) {
		draft.Name = strings.TrimSpace(req.Name)
	}
	if draft.Author != author {
		draft.Author = author
	}
	if strings.TrimSpace(draft.Tags) == "" {
		draft.Tags = strings.Join(nonEmptyStrings("sandbox", "coc", constraints.Theme), ",")
	}
	if draft.MinPlayers <= 0 {
		draft.MinPlayers = req.MinPlayers
	}
	if draft.MinPlayers <= 0 {
		draft.MinPlayers = 1
	}
	if draft.MaxPlayers <= 0 {
		draft.MaxPlayers = req.MaxPlayers
	}
	if draft.MaxPlayers <= 0 {
		draft.MaxPlayers = 4
	}
	if draft.MaxPlayers < draft.MinPlayers {
		draft.MaxPlayers = draft.MinPlayers
	}
	if strings.TrimSpace(draft.Difficulty) == "" {
		draft.Difficulty = firstNonEmpty(req.Difficulty, "normal")
	}
	if draft.Content.GameStartSlot < 0 {
		draft.Content.GameStartSlot = 0
	}
	if draft.Content.GameStartSlot > 47 {
		draft.Content.GameStartSlot = 47
	}
	if strings.TrimSpace(draft.Content.SystemPrompt) == "" {
		draft.Content.SystemPrompt = fmt.Sprintf(
			"你是本场COC跑团的KP，职责是管理会自行推进的局势而不是执行线性故事。按派系时间线推进后果；按表面可见、主动询问、需要行动、不可直接获得四层管理信息；不要主动把调查员引向正确答案。【KP独有，勿向玩家直说】内部真相：%s。固定神话锚点：%s；具体数值按规则书裁定。",
			"真相将通过调查逐步揭示",
			firstNonEmpty(draft.Content.MythosAnchor, "按规则书已收录神话元素处理"),
		)
		log.Printf("[scripter:normalize] session=%s filled system_prompt", sessionID)
	}
	if strings.TrimSpace(draft.Content.Setting) == "" {
		draft.Content.Setting = fmt.Sprintf(
			"%s的%s。这是平常的一天，你们因各自的缘由来到此地，眼前的一切安静而寻常，尚无任何异样。",
			constraints.Era, strings.Join(constraints.GeographyFlavor, " / "),
		)
		log.Printf("[scripter:normalize] session=%s filled setting", sessionID)
	}
	// 简介与背景设定已合并：description 不再由模型单独产出，统一取 content.setting；
	// setting 本身仍为空的极端情况下才使用固定兜底文案。
	if strings.TrimSpace(draft.Description) == "" {
		if strings.TrimSpace(draft.Content.Setting) != "" {
			draft.Description = draft.Content.Setting
		} else {
			draft.Description = "一段看似寻常的经历正在等待几位到访者。接受这份邀约，故事便从平常的一天开始。"
		}
		log.Printf("[scripter:normalize] session=%s filled description", sessionID)
	}
	if strings.TrimSpace(draft.Content.Intro) == "" {
		draft.Content.Intro = "你们按各自的缘由抵达此地，眼前一切安静而寻常。"
		log.Printf("[scripter:normalize] session=%s filled intro", sessionID)
	}
	if strings.TrimSpace(draft.Content.MapDescription) == "" {
		draft.Content.MapDescription = "【文字地图】各调查地点是剧本状态节点，不是顺序关卡：入口连接所有可调查地点；地点之间可往返；时间推进时，各地点状态可能因派系行动而改变。"
		log.Printf("[scripter:normalize] session=%s filled map_description", sessionID)
	}
	if strings.TrimSpace(constraints.HorrorMode) != "" {
		if constraints.DiversitySource == "ai" {
			// NOTE: AI 围池选择时尊重 architect 输出，仅补空值
			if strings.TrimSpace(draft.Content.HorrorMode) == "" {
				draft.Content.HorrorMode = strings.TrimSpace(constraints.HorrorMode)
			}
		} else {
			// fallback 或空: 维持强制覆盖
			if strings.TrimSpace(draft.Content.HorrorMode) != strings.TrimSpace(constraints.HorrorMode) {
				log.Printf("[scripter:normalize] session=%s override horror_mode from=%q to=%q", sessionID, draft.Content.HorrorMode, constraints.HorrorMode)
				draft.Content.HorrorMode = strings.TrimSpace(constraints.HorrorMode)
			}
		}
	}
	if strings.TrimSpace(constraints.InvestFocus) != "" {
		if constraints.DiversitySource == "ai" {
			// NOTE: AI 围池选择时尊重 architect 输出，仅补空值
			if strings.TrimSpace(draft.Content.InvestFocus) == "" {
				draft.Content.InvestFocus = strings.TrimSpace(constraints.InvestFocus)
			}
		} else {
			// fallback 或空: 维持强制覆盖
			if strings.TrimSpace(draft.Content.InvestFocus) != strings.TrimSpace(constraints.InvestFocus) {
				log.Printf("[scripter:normalize] session=%s override invest_focus from=%q to=%q", sessionID, draft.Content.InvestFocus, constraints.InvestFocus)
				draft.Content.InvestFocus = strings.TrimSpace(constraints.InvestFocus)
			}
		}
	}
	if len(constraints.ToneTags) > 0 && !sameStringSlice(draft.Content.ToneTags, constraints.ToneTags) {
		log.Printf("[scripter:normalize] session=%s override tone_tags from=%q to=%q", sessionID, strings.Join(draft.Content.ToneTags, ","), strings.Join(constraints.ToneTags, ","))
		draft.Content.ToneTags = append([]string(nil), constraints.ToneTags...)
	}
	if len(draft.Content.Scenes) == 0 {
		draft.Content.Scenes = []models.SceneData{{
			ID:          "location_1",
			Name:        "调查入口",
			Description: "调查员进门就能感觉到异常已经公开出现，主动调查能获得第一批事实；是否公开或隐瞒所知会改变各方反应，拖延下去局势会继续推进；由此可以前往其他相关地点。",
			Triggers:    []string{"available_from_start"},
		}}
		log.Printf("[scripter:normalize] session=%s generated default scene", sessionID)
	}
	for i := range draft.Content.Scenes {
		if strings.TrimSpace(draft.Content.Scenes[i].ID) == "" {
			draft.Content.Scenes[i].ID = fmt.Sprintf("location_%d", i+1)
		}
		if strings.TrimSpace(draft.Content.Scenes[i].Name) == "" {
			draft.Content.Scenes[i].Name = fmt.Sprintf("地点%d", i+1)
		}
		if strings.TrimSpace(draft.Content.Scenes[i].Description) == "" {
			draft.Content.Scenes[i].Description = "调查员在此能看到当前局势的表面信息，主动调查能获得更多事实；调查员的行动会影响局势推进，拖延下去世界会继续变化；由此可以前往相关地点。"
		}
		if len(draft.Content.Scenes[i].Triggers) == 0 {
			draft.Content.Scenes[i].Triggers = []string{"available_from_start"}
		}
	}
	if len(draft.Content.NPCs) == 0 {
		name := "关键NPC"
		if picked, err := pickRandomNPCName("western", "male", usedNPCNames); err == nil {
			name = picked
			markNPCNamesUsed(usedNPCNames, picked)
		}
		draft.Content.NPCs = []models.NPCData{{
			Name:        name,
			Description: "公开身份：地方相关人员。真实议程：自保并观察局势。秘密：掌握部分真相但不会主动全盘托出。",
			Attitude:    "谨慎防备",
		}}
		log.Printf("[scripter:normalize] session=%s generated default npc", sessionID)
	}
	for i := range draft.Content.NPCs {
		if strings.TrimSpace(draft.Content.NPCs[i].Name) == "" {
			name := fmt.Sprintf("关键NPC%d", i+1)
			if picked, err := pickRandomNPCName("western", "male", usedNPCNames); err == nil {
				name = picked
				markNPCNamesUsed(usedNPCNames, picked)
			}
			draft.Content.NPCs[i].Name = name
		}
		if strings.TrimSpace(draft.Content.NPCs[i].Description) == "" {
			draft.Content.NPCs[i].Description = "公开身份和所属派系，心里真正想要的和正在做的事，以及一个不愿说但能被调查员发现的秘密。"
		}
		if strings.TrimSpace(draft.Content.NPCs[i].Attitude) == "" {
			draft.Content.NPCs[i].Attitude = "谨慎观察调查员，只有在压力或交换下才透露深层信息。"
		}
	}
	if len(draft.Content.Clues) == 0 {
		draft.Content.Clues = []models.ClueData{
			{Summary: "公开异常(调查入口): 一个无法普通解释的局势已经开始", Nature: "真实", Source: "到达现场并主动询问或检查"},
			{Summary: "佐证细节(深入调查): 与公开异常相互印证的独立事实，须两条线索合并才能确认事态走向", Nature: "真实", Source: "深入调查或与相关人员交流"},
			{Summary: "表象线索(初步调查): 支持错误推断的表象证据；表面合理但只能解释一部分", Nature: "误导", Source: "初步调查"},
		}
		log.Printf("[scripter:normalize] session=%s generated default clues count=3", sessionID)
	}
	for i := range draft.Content.Clues {
		draft.Content.Clues[i].Summary = strings.TrimSpace(draft.Content.Clues[i].Summary)
		if strings.TrimSpace(draft.Content.Clues[i].Nature) == "" {
			draft.Content.Clues[i].Nature = "真实"
		}
	}
	// 提取标注"神话本质"的[隐藏]线索 → MythosCore；判定条件须与 hasMythosEssenceClue
	// （scripter.go）保持一致——必须同时满足 nature=隐藏 且 summary 含"神话本质"，
	// 否则可能误删恰好提到该字样的[真实]/[误导]线索，拉低真实线索计数。
	var filteredClues []models.ClueData
	for _, clue := range draft.Content.Clues {
		if strings.TrimSpace(clue.Nature) == "隐藏" && strings.Contains(clue.Summary, "神话本质") {
			if strings.TrimSpace(draft.Content.MythosCore) == "" {
				draft.Content.MythosCore = clue.Summary
			} else {
				draft.Content.MythosCore += "；" + clue.Summary
			}
			log.Printf("[scripter:normalize] session=%s extracted mythos_core=%q", sessionID, truncateRunes(clue.Summary, 200))
		} else {
			filteredClues = append(filteredClues, clue)
		}
	}
	draft.Content.Clues = filteredClues
	if strings.TrimSpace(draft.Content.MythosCore) == "" && strings.TrimSpace(draft.Content.MythosAnchor) != "" {
		draft.Content.MythosCore = fmt.Sprintf("神话本质(核心发现): %s；到达终止节点并触发揭示后承担理智代价。", draft.Content.MythosAnchor)
		log.Printf("[scripter:normalize] session=%s synthesized mythos_core from anchor", sessionID)
	}
	if len(draft.Content.Endings) == 0 {
		draft.Content.Endings = []models.EndingData{
			{Name: "余波固化", Trigger: "调查员让关键事实公开并改变至少一个派系时间线", Description: "局势以较低代价固化，但神话锚点的余波仍保留。", SANReward: "恢复1d6"},
			{Name: "新的稳定态", Trigger: "关键时间线终点到达且调查员没有改变任何派系行动", Description: "局势进入新的稳定态，某人或某地不可挽回地改变。", IsFailure: true, SANReward: "损失1d10"},
		}
		log.Printf("[scripter:normalize] session=%s filled default endings count=2", sessionID)
	}
}
