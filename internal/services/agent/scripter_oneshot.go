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
		"reward_concept": {"type": "string", "description": "本篇通关奖励的一句话叙事概念，由你依据故事文档已确立的施动者、地点、人物与mythos_anchor设计并指认一件调查员通关后能带走的实体载体（典籍/手稿/笔记/器物）；故事原文未明写奖励时也必须设计，但只能建立在文档已有的人事物之上，不得新增人物或地点、不得改动结局；不写规则数值与SAN代价；不得留空。修复阶段若<previous_draft>已有此字段，原样保留原值"},
		"name": {"type": "string", "description": "剧本标题；无明确标题时从文档内具体名词提炼，不用低语/回响/深渊/阴影/凝视/苏醒/沉睡/诅咒等滥用词"},
		"author": {"type": "string", "description": "固定为agent-team"},
		"tags": {"type": "string", "description": "2-3个逗号分隔标签，指向本剧本独有的核心叙事装置/桥段；须避开recent_scenario_tags_blacklist"},
		"min_players": {"type": "integer"},
		"max_players": {"type": "integer"},
		"difficulty": {"type": "string", "description": "如 normal"},
		"content": {
			"type": "object",
			"properties": {
				"setting": {"type": "string", "description": "表层情境原文或忠实改写，必须保留文档中嵌入的具体年月日"},
				"tone_tags": {"type": "array", "items": {"type": "string"}, "description": "必须逐字等于diversity_constraints.tone_tags"},
				"invest_focus": {"type": "string", "description": "调查入口的简短概括；除非must_fix明确要求，否则保持previous_draft原值"},
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
					"description": "守秘人专属材料：核心真相与施动者设定+运营建议；本对象必须给出，不得整体省略",
					"properties": {
						"core_truth": {"type": "string", "description": "KP独有的内部真相：复述故事核心真相与mythos_anchor为何不可替换；必填，不得压缩为一句话"},
						"antagonist_dossier": {"type": "string", "description": "施动者（邪教/施法者/神话生物）细化设定，须完整保留不得压缩为一句话；文档无独立施动者可留空"},
						"difficulty_down": {"type": "string"},
						"difficulty_up": {"type": "string"},
						"solo_advice": {"type": "string"},
						"group_advice": {"type": "string"},
						"horror_tips": {"type": "string"},
						"theme_guidance": {"type": "string"}
					},
					"required": ["core_truth"]
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
			"required": ["setting", "tone_tags", "invest_focus", "intro", "game_start_slot", "map_description", "playthrough_outline", "mythos_anchor", "scenes", "npcs", "clues", "endings", "keeper_appendix"]
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

// parseOneshotResult 解析 submit 的工具调用参数为 OneshotResult。
// oneshotDraftJSONSchema 顶层字段多、content 又是十几个字段的深层嵌套对象，模型偶尔会把
// 整个 content 错误地序列化成一个字符串（而不是原生嵌套对象），此时原始 Go 反序列化错误
// 是"cannot unmarshal string into Go struct field..."，容易被模型误读成"JSON语法错误"去
// 反复重试同样的错误。这里先探测 content 字段的实际 JSON 类型，命中该失误时给出明确指出
// 具体字段和具体问题的提示，帮助模型下一轮准确纠正。
func parseOneshotResult(toolName, rawArgs string) (OneshotResult, string) {
	var probe struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &probe); err == nil && len(probe.Content) > 0 {
		if trimmed := strings.TrimSpace(string(probe.Content)); strings.HasPrefix(trimmed, `"`) {
			return OneshotResult{}, fmt.Sprintf("SYSTEM REJECT: %s的content字段必须是JSON对象（{...}），实际收到的是字符串。请勿把整个content值当成字符串包裹，需以原生嵌套对象重新提交完整参数。", toolName)
		}
	}
	var result OneshotResult
	if err := json.Unmarshal([]byte(rawArgs), &result); err != nil {
		return OneshotResult{}, fmt.Sprintf("SYSTEM REJECT: %s参数不是合法JSON，请重新调用。 err: %s", toolName, err.Error())
	}
	return result, ""
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
		Setting:        "1924年9月3日，初秋的傍晚，你们受镇图书馆之邀前来协助整理一批新捐赠的藏书。馆内灯光温暖，管理员热情地引你们入座，窗外街区安静而寻常。",
		ToneTags:       []string{"forbidden-knowledge", "cosmic-dread", "occult-noir"},
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
			CoreTruth:         "失窃的书是Douglas生前的旧藏，他死后被镇北墓地的食尸鬼群落接纳、保留了生前记忆，如今潜回取回属于自己的东西。核心恐惧不在于怪物的样貌，而在于「死亡并非终点、逝者以非人的方式继续存在」这一认知对调查员世界观的不可逆冲击，mythos_anchor(食尸鬼)因此不可替换——换成普通盗贼案件，这层恐惧根基就不存在了。",
			AntagonistDossier: "食尸鬼Douglas：来历——原是镇民Douglas Whitfield，六周前病逝下葬后在镇北墓地被食尸鬼群落接纳同化；栖身范围——夜间活动于镇北墓地及其地下墓穴，不轻易进入人类聚居区腹地；可观察影响——墓地周边泥土翻动痕迹、窗台残留矿物质与墓地土质一致、深夜佝偻身影翻墙；行为驱动——保留生前对旧藏书的执念，唯一诉求是取回属于自己的书，并非无差别猎食。",
			DifficultyDown:    "让守墓人主动提供泥土线索，缩短调查员定位墓地的时间",
			DifficultyUp:      "取书者提前带走部分证据，迫使调查员更依赖NPC口述重建事实",
			SoloAdvice:        "单人团可让守墓人承担更多主动提示功能",
		},
	},
}

// oneshotExample is the JSON schema example used for parsing/repair prompts.
var oneshotExample = marshalExample(oneshotResultExample)

// StoryOutput is the story architect's final submission: a free-text story
// document plus the confirmed mythos anchor. It carries no strongly-typed
// scene/NPC/clue structure — the compiler stage is responsible for
// extracting structure (including reward_concept) from Document.
type StoryOutput struct {
	Document     string `json:"story_document"`
	MythosAnchor string `json:"mythos_anchor"`
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
- NPC人味：每个重要NPC给一个标志性小细节（口头禅、习惯动作、随身物件、外貌特征选其一）；NPC之间至少存在两条现实关系（亲属/雇佣/债务/旧怨/邻里）；可以保留一个与主线无关的纯地方色彩NPC
- 密度不均：允许一处地点信息厚重、另一些地点只有一两笔；不给每个地点机械配满同样数量的要素
- 不写机械数值：正文不出现骰子表达式（"1D6""1d3""2D6+3"）、技能成功率、伤害点数、理智增减的具体点数等规则数值；检定、伤害、法术效果、理智冲击等机制后果一律写它造成的具体状态、感受或场面变化（伤在哪、能不能动、看见听见了什么、心智被冲击成什么样），这些数值本身留给编译后的结构化字段供KP参考，不落进叙事段落`

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
- 不得改变<diversity_constraints>中tone_tags的值
- 仅当must_fix涉及神话元素本身时，才调用translate_anchor核验；否则不要调用
- 修复神话本质说明时，引用的法术/物品/怪物/机制名必须与must_fix或<previous_draft>中已确认的规则书元素一致，不得新造
- setting/intro必须保持冷开场：中性日常，不剧透真相、不渲染恐怖
</task>
<tools>
- translate_anchor：仅当must_fix涉及神话元素时，将一个创意概念翻译为COC7规则书中最匹配的具体元素
- generate_npc_name：需要新增或替换NPC姓名时，从预置姓名池随机生成，不要自行编造
- ready_to_submit：完成上述核验（或确认无需核验）后调用，确认准备好提交修复后的完整剧本；必须单独一轮调用。调用后系统会请你直接输出完整JSON本体
</tools>
<draft_schema>
最终提交的JSON对象必须保持与<previous_draft>完全相同的字段结构：
{
  "reward_concept": "保持previous_draft原值",
  "name": "剧本名称",
  "author": "保持previous_draft原值",
  "tags": "2-3个逗号分隔的标签，须避开<recent_scenario_tags_blacklist>",
  "min_players": 1,
  "max_players": 4,
  "difficulty": "normal",
  "content": {
    "setting": "表层日常局势，须保留已嵌入的具体年月日",
    "tone_tags": ["必须等于diversity_constraints.tone_tags"],
    "invest_focus": "调查入口的简短概括；除非must_fix明确要求，否则保持previous_draft原值",
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
    "keeper_appendix": {"core_truth":"必填，核心真相+mythos_anchor必要性","antagonist_dossier":"施动者细化设定，不得压缩为一句话；无独立施动者可留空","difficulty_down":"...","difficulty_up":"...","solo_advice":"...","group_advice":"...","horror_tips":"...","theme_guidance":"..."},
    "mechanics": [{"name":"...","type":"counter|clock|tracker","description":"...","stages":[{"label":"...","effect":"...","trigger":"..."}]}]
  }
}
</draft_schema>`
}

// ---------------------------------------------------------------------------
// Architect loop
// ---------------------------------------------------------------------------

// runOneshotArchitectLoop 驱动修复阶段的两段式提交。第一段（runOneshotVerificationPhase）
// 是原生工具循环：translate_anchor/generate_npc_name按需多次调用，以ready_to_submit（独占
// 一轮、空参数）收尾。第二段（runOneshotSubmitPhase）改为纯JsonChat直接输出完整JSON——
// oneshotDraftJSONSchema十几个顶层字段、content又深层嵌套，原生tool calling单次调用维持
// 这种结构时不稳定（同compileStoryToModule的既有结论：曾观测到必填字段留空或content被
// 整体序列化成字符串），JsonChat+RepairJSON是这个代码库里同类"大段结构化JSON"生成的既有
// 做法。目前仅由 repairOneshotDraft 复用，用于在已编译草案上做核验 + 结构修复。
func runOneshotArchitectLoop(ctx context.Context, room *scripterRoom, conv *scripterConversation, stageName string) (OneshotResult, error) {
	stageName = firstNonEmpty(stageName, "oneshot_architect")

	findings, err := runOneshotVerificationPhase(ctx, room, conv, stageName)
	if err != nil {
		return OneshotResult{}, err
	}
	return runOneshotSubmitPhase(ctx, room, conv, findings, stageName)
}

// readyToSubmitTool 是核验阶段的收尾信号工具（solo，空参数）：模型确认已完成必要的
// translate_anchor/generate_npc_name核验（或确认本轮无需核验）后调用，驱动器随即返回，
// 交由 runOneshotSubmitPhase 发起JsonChat提交。
func readyToSubmitTool() scripterTool {
	return scripterTool{
		solo: true,
		def: llm.ToolDefinition{
			Name:        toolNameReadyToSubmit,
			Description: "确认已完成必要的translate_anchor/generate_npc_name核验（或确认本轮无需核验），准备提交修复后的完整剧本；必须单独一轮调用，无需参数",
			Parameters:  jsonSchemaObject(`{"type": "object", "properties": {}}`),
		},
	}
}

// runOneshotVerificationPhase 跑一次只含 translate_anchor/generate_npc_name/ready_to_submit
// 的工具循环，把期间每次translate_anchor的结果文本拼接为findings返回，供
// runOneshotSubmitPhase的JsonChat提交带上核验结论（否则模型在纯JsonChat轮会"忘记"
// 核验阶段问到的规则书元素）。没有调用过translate_anchor时findings为空串。
// NOTE: 核验阶段的原生工具往返（tool_calls/role=tool 消息）只落在 conv.branch() 分支
// 上，不写回主链——主链之后要交给 JsonChat 提交，部分兼容端点在同一次请求里混入原生
// tool_calls 历史与 response_format=json_object 会报错；核验学到的结论改以 findings
// 文本形式追加进主链，跨轮依然可见。
func runOneshotVerificationPhase(ctx context.Context, room *scripterRoom, conv *scripterConversation, stageName string) (string, error) {
	tools := []scripterTool{
		translateAnchorTool("将一个创意概念翻译为COC7规则书中最匹配的具体元素；仅当must_fix涉及神话元素时才需要调用"),
		generateNPCNameTool(),
		readyToSubmitTool(),
	}

	var findings []string
	dispatch := func(ctx context.Context, call llm.ToolCall) toolOutcome {
		switch call.Name {
		case toolNameTranslateAnchor:
			var args translateAnchorArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: translate_anchor参数不是合法JSON，请重新调用。"}
			}
			text, _ := executeOneshotTranslateAnchor(ctx, room, args.Concept, args.Reason)
			findings = append(findings, text)
			return toolOutcome{result: text}
		case toolNameGenerateNPCName:
			return dispatchGenerateNPCName(ctx, room, call)
		case toolNameReadyToSubmit:
			return toolOutcome{result: "已确认，请直接输出完整剧本JSON。", done: true}
		default:
			return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 此阶段只允许translate_anchor/generate_npc_name/ready_to_submit，不允许%s。", call.Name)}
		}
	}

	const maxRounds = 20
	if err := runToolLoop(ctx, toolLoopOptions{
		room:      room,
		handle:    room.architect,
		stage:     stageName + "_verify",
		conv:      conv.branch(),
		tools:     tools,
		maxRounds: maxRounds,
		dispatch:  dispatch,
	}); err != nil {
		return "", err
	}
	return strings.Join(findings, "\n"), nil
}

// runOneshotSubmitPhase 是核验通过后的纯JsonChat提交：把核验阶段的findings（若有）连同
// 提交指令追加进conv一起发给architect，直接要求输出完整oneshotResult JSON本体；解析失败
// 时走RepairJSON修复，name为空等业务校验失败时把上次输出连同具体修正要求追加进conv重新
// 发回去，让compiler带着上下文重新生成一次。成功提交后记一次成稿（conv.markDraft），供
// 后续修复轮次复用同一条对话。
func runOneshotSubmitPhase(ctx context.Context, room *scripterRoom, conv *scripterConversation, findings string, stageName string) (OneshotResult, error) {
	if room.architect.provider == nil {
		return OneshotResult{}, fmt.Errorf("%s provider unavailable", stageName)
	}
	sessionID := scripterSessionID(ctx, room)

	userContent := "现在请直接输出完整修复后的oneshotResult JSON本体，字段结构严格遵循前面draft_schema；不要输出解释性文字、前后缀说明或代码块围栏标记。"
	if strings.TrimSpace(findings) != "" {
		userContent = fmt.Sprintf("<verification_findings>\n%s\n</verification_findings>\n%s", findings, userContent)
	}
	conv.append(llm.ChatMessage{Role: "user", Content: userContent})

	cacheKey := room.architect.cacheKey(sessionID)
	const maxBusinessRetries = 3
	for attempt := 1; attempt <= maxBusinessRetries; attempt++ {
		stage := fmt.Sprintf("%s_submit_attempt_%d", stageName, attempt)
		logStagePrompt(stage, sessionID, conv.msgs)
		resp, err := room.architect.provider.JsonChat(ctx, cacheKey, conv.msgs)
		if err != nil {
			return OneshotResult{}, fmt.Errorf("%s failed: %w", stageName, err)
		}
		conv.record(ctx, room, stage, resp)
		conv.append(llm.ChatMessage{Role: "assistant", Content: resp})
		log.Printf("[scripter:%s] session=%s attempt=%d resp_len=%d", stageName, sessionID, attempt, len([]rune(resp)))

		result, reject := parseOneshotResult("submit", resp)
		if reject != "" {
			if fixed, repairErr := RepairJSON(ctx, resp, fmt.Errorf("%s", reject), oneshotDraftJSONSchema); repairErr == nil {
				result, reject = parseOneshotResult("submit", fixed)
			}
		}
		if reject != "" {
			conv.append(llm.ChatMessage{Role: "user", Content: reject + " 请重新输出完整JSON。"})
			continue
		}
		if strings.TrimSpace(result.Name) == "" {
			conv.append(llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: name字段不能为空。请连同完整JSON重新输出一次，其余字段保持与上次一致。"})
			continue
		}
		conv.markDraft()
		return result, nil
	}
	return OneshotResult{}, fmt.Errorf("%s 连续%d次未能提交合法剧本JSON", stageName, maxBusinessRetries)
}

// ---------------------------------------------------------------------------
// translate_anchor execution — calls translator sub-agent
// ---------------------------------------------------------------------------

// executeOneshotTranslateAnchor 由 oneshot architect repair 和 story architect
// 两个循环共用；concept/reason 直接来自各自 translate_anchor 工具调用的解码参数。
// 除了给模型看的结果文本外，还把结构化结论一并返回：story architect 用它在
// disabled=false时自动记下 selected_anchor，不再要求模型在提交故事时重复填写。
func executeOneshotTranslateAnchor(ctx context.Context, room *scripterRoom, concept, reason string) (string, *translatorConclusion) {
	sessionID := scripterSessionID(ctx, room)
	concept = strings.TrimSpace(concept)
	if concept == "" {
		return `<translate_anchor_result error="concept字段为空，无法翻译"/>`, nil
	}
	reason = strings.TrimSpace(reason)
	log.Printf("[scripter:oneshot_translate_anchor] session=%s concept=%q reason=%q", sessionID, truncateRunes(concept, 200), truncateRunes(reason, 200))
	conclusion, err := runOneshotTranslatorAgent(ctx, room, concept, reason)
	if err != nil {
		log.Printf("[scripter:oneshot_translate_anchor] session=%s error concept=%q err=%v", sessionID, truncateRunes(concept, 200), err)
		return fmt.Sprintf(`<translate_anchor_result concept=%q disabled="true">
content: 查询失败（%s），可尝试调整概念描述重新翻译，或转向人类法师、诅咒物品、古老地点等方向。
</translate_anchor_result>`, concept, err.Error()), nil
	}
	if conclusion == nil || strings.TrimSpace(conclusion.SelectedAnchor) == "" {
		return fmt.Sprintf(`<translate_anchor_result concept=%q disabled="true">
content: translator未返回可用结论，可尝试调整概念描述重新翻译，或转向人类法师、诅咒物品、古老地点等方向。
</translate_anchor_result>`, concept), nil
	}
	return fmt.Sprintf(`<translate_anchor_result concept=%q disabled="%t">
selected_anchor: %s
content: %s
</translate_anchor_result>`, concept, conclusion.Disabled, conclusion.SelectedAnchor, conclusion.Content), conclusion
}

// ---------------------------------------------------------------------------
// Translator sub-agent (validates CoC element via lawyer/rulebook)
// ---------------------------------------------------------------------------

const oneshotTranslatorSystemPrompt = `<role>COC7规则书概念翻译专家</role>
<task>收到一个创意概念，将它翻译为COC7规则书中最匹配、可在剧本中使用的具体元素（实体/典籍/法术/诅咒物品/机制）。通过 ask_lawyer 工具向规则书专家提问，依据裁定综合，最后用 respond 工具提交结论。你只负责如实报告规则书里查到了什么；候选是否命中最近使用元素禁用列表、要不要为此重新查、能不能将就用，都交给上层自行判断，你不必为此反复重试。</task>
<rules>
- 第一轮必须至少调用一次ask_lawyer；不得凭常识或记忆直接respond。
- 用户消息中的<recently_used_mythos_anchors>是最近已经用过的元素：查询候选时优先绕开它们；但如果规则书里最贴切的候选恰好在这份名单里，直接如实提交，在content中说明命中了这份名单，不必为了避开它而无限重试或勉强凑一个不贴切的候选。
- ask_lawyer问题要具体，优先确认候选元素是否在规则书中存在、出处、核心机制和使用边界。
- 不把lawyer原文无筛选地倾倒给architect；必须总结成可执行的结论。
- 不得编造规则书不存在的正式名称、页码、数值或能力。
- 法术不允许任何变体，必须完全符合规则书描述。
- 除非概念本身明确要求法术/仪式是唯一能承载的机制，否则优先把创意概念翻译为实体锚点（神话生物/旧日支配者眷属，或施法的人类角色本身），不要翻译成脱离施动者的法术条目本身。
- 若最终仍需要翻译为法术，必须在content中提醒：该法术必须由一个具体的实体（人、神话生物等）施放，且该实体才是故事应锚定的真正核心，法术只是其手段而非谜团根源。
- 翻译的结果必须直接来自规则书裁定，不能是基于规则书裁定的二次创作。
- 不可以是推导链条，无论其是否合理：必须直接引用规则书中的明确条文或裁定。
- 但推理链条的每一步都必须在规则书中有明确依据，不能凭常识或记忆自创。
- 规则书里确实找不到贴切候选时，selected_anchor写无，在content中说明已尝试过什么方向、为什么都不合适，不要乱编凑数。
</rules>`

// translatorConclusion 是 translator respond 工具的结构化结论，
// 字段与 respond 工具 schema 的属性一一对应。translator 只负责如实报告事实
// （查到了什么、为什么、有什么要注意的，合并写进 Content 一段连贯文字），
// 不负责判断candidate是否命中禁用列表——Disabled 由代码在收到 respond 后
// 依据禁用列表计算，不是LLM自报字段，因此不参与JSON解码（见下方dispatch）。
// 是否重试、要不要换个概念，这类决策交给上层调用方（Story Architect等）。
type translatorConclusion struct {
	SelectedAnchor string `json:"selected_anchor"`
	Content        string `json:"content"`
	Disabled       bool   `json:"-"`
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
					"selected_anchor": {"type": "string", "description": "规则书中最匹配的元素全称；确实没有可靠匹配时写无"},
					"content": {"type": "string", "description": "写给architect的完整结论，一段连贯文字：来源与依据、此元素如何承载原概念、必须避免的未核验数值/能力/误用；若selected_anchor为无或候选命中了最近使用元素禁用列表，说明原因并给出保守的替代方向"}
				},
				"required": ["selected_anchor", "content"]
			}`),
		},
	}
}

func runOneshotTranslatorAgent(ctx context.Context, room *scripterRoom, concept string, reason string) (*translatorConclusion, error) {
	// NOTE: translator 独立 provider/session key，不复用 lawyer；fail-fast，不退回 lawyer。
	if room.translator.provider == nil {
		return nil, fmt.Errorf("translator provider unavailable")
	}

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.translator.systemPrompt(oneshotTranslatorSystemPrompt)},
		{Role: "user", Content: fmt.Sprintf(`<translate_anchor_request>
concept: %s
reason: %s
</translate_anchor_request>
<recently_used_mythos_anchors>
%s
</recently_used_mythos_anchors>
以上是最近已经用过的元素：查询候选时优先绕开；如果规则书里最贴切的候选恰好在这份名单里，如实提交并在content中说明，不必为了避开它而无限重试。`,
			concept, firstNonEmpty(reason, "(未说明)"), formatMythosBlacklist(room.mythosBlacklist))},
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
			if strings.TrimSpace(args.SelectedAnchor) == "" || strings.TrimSpace(args.Content) == "" {
				return toolOutcome{reject: "SYSTEM REJECT: respond的selected_anchor和content字段不能为空。"}
			}
			// NOTE: 是否命中禁用列表由代码判定，不要求LLM自报，也不因命中而拒绝重试——
			// 判定结果随结论一起交给上层（Story Architect等）决定要不要换概念重查。
			selected := strings.TrimSpace(args.SelectedAnchor)
			args.Disabled = selected == "无" || oneshotFindForbiddenAnchor(selected, room.mythosBlacklist) != ""
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
	sb.WriteString(fmt.Sprintf("tone_tags: %s\n", strings.Join(constraints.ToneTags, ", ")))
	sb.WriteString("硬约束：本次submit.draft.content.tone_tags必须逐字使用上述值，不得自行替换、翻译、改名或省略。\n")
	sb.WriteString("含义：tone_tags只约束文风、节奏、场面选择和NPC反应风格，不覆盖剧本事实、规则书裁定或工具结果。调查入口与神话力量介入人类世界的方式均不在本块约束范围内，由创作阶段自行决定。\n")
	sb.WriteString("</diversity_constraints>")
	return sb.String()
}

// ---------------------------------------------------------------------------
// Repair — patches an already-compiled ScenarioDraft against issue lists
// raised by validateDraftCompatibility / runStoryQAReview / runLogicReview.
// ---------------------------------------------------------------------------

// maxOneshotConvRunes 是 oneshot 修复消息链复用的字符数上限（近似token数）；超过后
// repairOneshotDraft 会放弃续接对话，降级为重建一条新链（改造前的行为）。
const maxOneshotConvRunes = 60000

// oneshotRepairInstruction 是延续原链续接修复时追加的指令：只包含标签黑名单、
// must_fix清单与核验/最小改动约束，不重复请求参数/多样性约束/上一版draft全文——
// 它们已经在链中可见。
func oneshotRepairInstruction(issues []string, tagsBlacklist []string) string {
	return fmt.Sprintf(
		`<recent_scenario_tags_blacklist>
%s
</recent_scenario_tags_blacklist>
<must_fix>
%s
</must_fix>
请先按需完成核验：仅当must_fix涉及神话元素本身时才调用translate_anchor，需要新增/替换NPC姓名时调用generate_npc_name；确认无需核验或核验完成后，调用ready_to_submit。逐条针对must_fix修复到位，除修复所需外不要改动其他内容；不要更换已确认的神话元素（mythos_anchor）；不得改变diversity_constraints中的tone_tags；若需修复tags，须避开recent_scenario_tags_blacklist中的所有标签。`,
		formatScenarioTagsBlacklist(tagsBlacklist),
		strings.Join(issues, "\n"),
	)
}

// repairOneshotDraft 修复已编译的 ScenarioDraft。conv 非空、已有历史消息且未超过
// maxOneshotConvRunes 时，延续原链——把历史成稿正文替换为占位符后只追加一条
// must_fix 消息，模型据此续接同一条对话；conv 为空、还没有任何历史消息、或链已超过
// 复用上限时，退化为重建一条全新的 system(repairSystemPrompt)+user 消息链（携带
// 上一版draft全文快照），即改造前的行为。
func repairOneshotDraft(ctx context.Context, room *scripterRoom, conv *scripterConversation, constraints ScripterConstraints, previous *ScenarioDraft, issues []string) (ScenarioDraft, error) {
	sessionID := scripterSessionID(ctx, room)

	if conv == nil || len(conv.msgs) == 0 || conv.runeLen() > maxOneshotConvRunes {
		prevJSON, _ := json.Marshal(previous)
		userMsg := fmt.Sprintf(
			`%s
%s
<previous_draft>%s</previous_draft>
<recent_scenario_tags_blacklist>
%s
</recent_scenario_tags_blacklist>
<must_fix>
%s
</must_fix>
请先按需完成核验：仅当must_fix涉及神话元素本身时才调用translate_anchor，需要新增/替换NPC姓名时调用generate_npc_name；确认无需核验或核验完成后，调用ready_to_submit。逐条针对must_fix修复到位，除修复所需外不要改动其他内容；不要更换已确认的神话元素（mythos_anchor）；不得改变diversity_constraints中的tone_tags；若需修复tags，须避开<recent_scenario_tags_blacklist>中的所有标签。`,
			scenarioRequestBlock(room.req, constraints),
			diversityConstraintsBlock(constraints),
			string(prevJSON),
			formatScenarioTagsBlacklist(room.tagsBlacklist),
			strings.Join(issues, "\n"),
		)
		if conv == nil {
			conv = newScripterConversation()
		}
		log.Printf("[scripter:oneshot_repair] session=%s conv为空或超出复用上限，重建消息链 rune_len=%d", sessionID, conv.runeLen())
		conv.reset(
			llm.ChatMessage{Role: "system", Content: room.architect.systemPrompt(repairSystemPrompt())},
			llm.ChatMessage{Role: "user", Content: userMsg},
		)
	} else {
		conv.supersedePriorDrafts()
		conv.append(llm.ChatMessage{Role: "user", Content: oneshotRepairInstruction(issues, room.tagsBlacklist)})
	}
	logStagePrompt("oneshot_repair", sessionID, conv.msgs)

	result, err := runOneshotArchitectLoop(ctx, room, conv, "oneshot_repair_architect")
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

// buildLogicReviewPayload 送审因果逻辑相关字段：比人写化审查多送core_truth/antagonist_dossier/mythos/win-lose，
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
	var coreTruth, antagonistDossier string
	if ka := draft.Content.KeeperAppendix; ka != nil {
		coreTruth = ka.CoreTruth
		antagonistDossier = ka.AntagonistDossier
	}
	return map[string]any{
		"name":               draft.Name,
		"core_truth":         coreTruth,
		"antagonist_dossier": antagonistDossier,
		"mythos_anchor":      draft.Content.MythosAnchor,
		"mythos_core":        draft.Content.MythosCore,
		"scenes":             scenes,
		"npcs":               npcs,
		"clues":              draft.Content.Clues,
		"endings":            draft.Content.Endings,
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
	// NOTE: normalizeScenarioTitle 此前只用于归一化黑名单样本，从未作用于产出标题，
	// 导致模型返回的《XX》会带书名号原样入库。
	draft.Name = normalizeScenarioTitle(draft.Name)
	if draft.Name == "" {
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
	if draft.Content.KeeperAppendix == nil {
		draft.Content.KeeperAppendix = &models.KeeperAppendix{}
	}
	if strings.TrimSpace(draft.Content.KeeperAppendix.CoreTruth) == "" {
		draft.Content.KeeperAppendix.CoreTruth = fmt.Sprintf(
			"【KP独有，勿向玩家直说】内部真相：%s。固定神话锚点：%s；具体数值按规则书裁定。",
			firstNonEmpty(draft.Content.MythosCore, "真相将通过调查逐步揭示"),
			firstNonEmpty(draft.Content.MythosAnchor, "按规则书已收录神话元素处理"),
		)
		log.Printf("[scripter:normalize] session=%s filled keeper_appendix.core_truth", sessionID)
	}
}
