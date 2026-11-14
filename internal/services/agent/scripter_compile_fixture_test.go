// NOTE: 编译阶段测试共用的夹具：一份能通过提交关卡的完整草稿，以及把它逐块写成工具调用的辅助函数。
// 禁止真实网络/真实LLM。
package agent

import (
	"encoding/json"
	"fmt"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// compileFixtureConcept 是夹具草稿配套的 reward_concept。
const compileFixtureConcept = "与食尸鬼有关的古籍手稿"

// compileFixtureDraft 是一份完整的示例模组草稿。
var compileFixtureDraft = ScenarioDraft{
	Name:        "示例模组",
	Description: "一座宁静小镇的老图书馆正在整理一批新到的捐赠藏书，馆方邀请你们前来协助编目。一次寻常的委托，一段关于旧书与小镇的日子就此开始。",
	Author:      "agent-team",
	Tags:        "食尸鬼,身后遗物,墓地图书馆",
	MinPlayers:  1,
	MaxPlayers:  4,
	Difficulty:  "normal",
	Content: models.ScenarioContent{
		Setting:        "1924年9月3日，初秋的傍晚，你们受镇图书馆之邀前来协助整理一批新捐赠的藏书。馆内灯光温暖，管理员热情地引你们入座，窗外街区安静而寻常。",
		ToneTags:       []string{"forbidden-knowledge", "cosmic-dread", "occult-noir"},
		InvestFocus:    "artifact_theft",
		Intro:          "1924年9月3日，傍晚六点的钟声刚敲过，你们如约走进镇图书馆一层大厅，来帮着整理清点一批新到的捐赠藏书。馆员在前台核对着今天的编目单，大厅里只有翻纸的声音。",
		GameStartTime:  "1924-09-03T18:00",
		MapDescription: "【文字地图】图书馆→书架区↔档案室↔墓地。",
		PlaythroughOutline: "开场：调查员受邀到图书馆大厅协助编目，馆员提及近期失窃。" +
			"图书馆大厅（入口条件：开局即可进入；可接触NPC：守墓人Henrik；可得线索：被取走的书出自同一捐赠者；出口：查阅捐赠登记签收单可解锁书架区细节，安抚或盘问Henrik可解锁档案室话题）→" +
			"档案室（入口条件：从大厅获知捐赠者身份后可入；可得线索：窗台泥土成分与镇北墓地一致；出口：与「失窃书目的共同点」组合后指向墓地，调查员可选择连夜赶赴或次日再去）→" +
			"墓地（入口条件：完成泥土与书目两条线索组合；可得线索：取书者是食尸鬼；分支：调查员归还藏书导向「书归其主」，选择声张致图书馆关闭则导向「永久关闭」）。",
		MythosAnchor: "食尸鬼（Ghoul）：COC7规则书已收录；具体属性按规则书裁定。",
		MythosCore:   "神话本质：取书者是死后被食尸鬼群落接纳的Douglas，死亡并非终点这一认知本身才是真正的理智代价。",
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

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mustJSON: %v", err))
	}
	return string(b)
}

// compileFixtureClueKeys 是夹具里各条线索写入时使用的键，顺序与 compileFixtureDraft.Content.Clues 一致。
var compileFixtureClueKeys = []string{"donor_books", "window_soil", "ghoul_truth", "keeper_testimony"}

// compileWriteCalls 把一份草稿逐块写成工具调用：概览、守密人材料、列表字段各一个。
// concept 为空时概览里不带 reward_concept。
func compileWriteCalls(draft ScenarioDraft, concept string) []llm.ToolCall {
	c := draft.Content
	overview := map[string]any{
		"name": draft.Name, "tags": draft.Tags, "invest_focus": c.InvestFocus, "tone_tags": c.ToneTags,
		"setting": c.Setting, "intro": c.Intro, "game_start_time": c.GameStartTime,
		"map_description": c.MapDescription, "playthrough_outline": c.PlaythroughOutline,
	}
	if concept != "" {
		overview["reward_concept"] = concept
	}
	notes := map[string]any{"mythos_core": c.MythosCore}
	if k := c.KeeperAppendix; k != nil {
		notes["core_truth"] = k.CoreTruth
		notes["antagonist_dossier"] = k.AntagonistDossier
		notes["difficulty_down"] = k.DifficultyDown
		notes["difficulty_up"] = k.DifficultyUp
		notes["solo_advice"] = k.SoloAdvice
	}
	clues := make([]clueArg, len(c.Clues))
	for i, cl := range c.Clues {
		clues[i] = clueArg{Key: compileFixtureClueKeys[i], ClueData: cl}
	}
	calls := []llm.ToolCall{
		{ID: "w1", Name: toolNameSetOverview, Arguments: mustJSON(overview)},
		{ID: "w2", Name: toolNameSetKeeperNotes, Arguments: mustJSON(notes)},
		{ID: "w3", Name: toolNameUpsertScenes, Arguments: mustJSON(map[string]any{"scenes": c.Scenes})},
		{ID: "w4", Name: toolNameUpsertNPCs, Arguments: mustJSON(map[string]any{"npcs": c.NPCs})},
		{ID: "w5", Name: toolNameUpsertClues, Arguments: mustJSON(map[string]any{"clues": clues})},
		{ID: "w6", Name: toolNameUpsertEndings, Arguments: mustJSON(map[string]any{"endings": c.Endings})},
	}
	if len(c.Timeline) > 0 {
		calls = append(calls, llm.ToolCall{ID: "w7", Name: toolNameSetTimeline, Arguments: mustJSON(map[string]any{"events": c.Timeline})})
	}
	return calls
}

func compileSubmitCall(id string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: toolNameSubmitDraft, Arguments: "{}"}
}

// compileToolResponses 构造一次最顺利的编译：第一轮写入整份草稿，第二轮单独提交。
func compileToolResponses(draft ScenarioDraft, concept string) []llm.ToolChatResult {
	return []llm.ToolChatResult{
		{ToolCalls: compileWriteCalls(draft, concept)},
		{ToolCalls: []llm.ToolCall{compileSubmitCall("s1")}},
	}
}
