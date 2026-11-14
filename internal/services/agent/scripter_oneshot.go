// scripter_oneshot.go — StoryOutput 类型、黑名单辅助函数、逻辑审查，以及编译产出的
// 归一化兜底，供 story→compile 流水线共用（两个生成阶段本身见 scripter_story.go /
// scripter_compile.go）。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// ---------------------------------------------------------------------------
// Output type
// ---------------------------------------------------------------------------

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
const humanWritingRules = `- 出版体例：成稿是交给守密人直接使用的COC模组，不是短篇小说，也不是只供编译器读取的设计草案。使用清晰的Markdown标题组织阅读顺序，可以有章节、事件与地点、守密人信息、时间线、结局、角色与怪物等出版模组常见段落；禁止输出JSON字段或内部设计标签
- 可执行性：每个调查地点按出版稿写法交代调查员抵达时第一眼看见、听见或闻到什么，深入调查能发现什么，需要什么检定，成功与失败各造成什么具体后果，可能发生什么危险，以及还能通往哪里；地点之间的路线、耗时、分支和回访关系直接写在正文里
- 信息分层：玩家当场能接触到的内容与仅供守密人了解的真相分开；守密人提示直接放在对应地点或事件之后，用简短明确的语气说明何时介入、如何控制节奏、哪些信息不能提前透露，不要把可执行提示埋在纯氛围散文里
- 具体性：散文要落在具体名词上——人名、地名、日期、距离、价钱、器物、气味、路名是取材范围，不是逐项打卡的清单；具体性看细节准不准，不看细节多不多；同一个功能点用一个细节钉住就够，不堆叠空泛的"神秘""诡异""不祥"
- 一段一焦点：一句话只推进一件事，一句里至多引入一个新专名或新意象；一段话围绕一个焦点纵深展开，不横向铺开多个互不相关的对象；人物特征通过动作、选择或对话带出，不写成人物卡属性清单
- 细节要挂钩：每个具体细节至少服务于刻画人物、埋设后文、承载情绪或关系、给出可行动的调查入口之一；无损删除的细节就删掉
- 细节复用优先：与其不断引入新道具，优先让已经出现的细节再次出现，并让第二次出现改变它的含义；一处地点可以写得很厚，过渡地点可以简略，但不要让每处地点机械配齐同样数量的要素
- 列表有边界：时间线、当地人分别知道的事情、装备、遭遇步骤和角色数据可以用短列表或数据块，这是出版模组的排版方式；每条都必须是完整的可执行信息，不得写成"可见信息：""秘密：""风险：""出口：""性质：真实"这类JSON字段式标签
- 规则可执行：检定要写明技能或属性、难度和成功/失败后的具体结果。守密人说明、战斗、遭遇、理智冲击、伤害、法术效果以及角色和怪物数据中，可以并且应当保留COC7需要的具体数值、骰值和规则名称；不要为了追求散文感把结果改成"受到一些伤害"或"情况变糟"。开头导入和纯环境描写只避免无意义的规则噪声
- 语言节奏：长短句交替；不连续使用三个以上结构雷同的句子；不把每段都写成排比、总结或提示词翻译
- NPC人味：每个重要NPC给一个标志性小细节；NPC之间至少存在两条现实关系；可以保留一个与主线无关但能体现地方生活的角色
- 反设计文档腔：不出现"本模组设计了……""这条线索的作用是……""核心线索/红鲱鱼/线索矩阵/承载者/施动者/宇宙公理"等作者工作语言，除非是在明确给守密人的运营说明中必要地解释运行方式`

// ---------------------------------------------------------------------------
// Blacklist helpers
// ---------------------------------------------------------------------------

// oneshotFindForbiddenAnchor 检查候选锚点（可能带括号别名等修饰）是否命中禁用列表；
// 返回命中的禁用元素，未命中返回空串。
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
	if seed := strings.TrimSpace(constraints.NarrativeSeed); seed != "" {
		sb.WriteString(fmt.Sprintf("narrative_seed: %s\n", seed))
		sb.WriteString("含义：narrative_seed只是本次构思可参考的切入角度，用来避免多次生成落入同一种套路，不是必须体现的情节要素，也不需要在成稿中提及这个角度本身；按需取舍怎么落地。\n")
	}
	sb.WriteString("</diversity_constraints>")
	return sb.String()
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
8. Intro目的性、画面感与顺序：intro是否先以叙事散文清楚交代了调查员到场的基本理由/表层任务，把地点（具体场所）、时间（具体年月日+时刻）、目标（要达成的那件事）三项自然写进叙事句子（而不是另起一行用【】或地点/时间/目标式标签罗列），再展开至少两处能让人看见/听见/感受到的具体场景细节，而不是先堆砌场景画面、把背景交代拖到最后才补上，也不是"一切如常""安静而寻常"这类空泛概括？三项事实是否都能在<story_document>里找到依据、没有新增事实？"目标"是否只写了要达成的那件事本身，而没有变成行动步骤建议（"先去问谁""应该搜查哪里"）？intro其余部分是否仍不列出、不推荐任何具体行动或下一步（行动留给玩家自行探索）？
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
		alog.Error("logic review marshal payload failed", "session", sessionID, "err", err)
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
		alog.Warn("logic review failed, skipping", "session", sessionID, "err", err)
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
		alog.Debug("normalize filled name", "session", sessionID, "name", draft.Name)
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
	if strings.TrimSpace(draft.Content.Setting) == "" {
		draft.Content.Setting = fmt.Sprintf(
			"%s。这是平常的一天，你们因各自的缘由来到此地，眼前的一切安静而寻常，尚无任何异样。",
			constraints.Era,
		)
		alog.Debug("normalize filled setting", "session", sessionID)
	}
	// NOTE: 必须在 setting 兜底之后处理：模型给的开局时间不合法时，用 setting 里的年月日加 08:00 回退。
	if normalized, err := models.NormalizeGameTime(draft.Content.GameStartTime); err == nil {
		draft.Content.GameStartTime = normalized
	} else {
		draft.Content.GameStartTime = models.LegacyGameStartTime(draft.Content.Setting, 16)
		alog.Debug("normalize reset game_start_time", "session", sessionID, "fallback", draft.Content.GameStartTime)
	}
	// 简介与背景设定已合并：description 不再由模型单独产出，统一取 content.setting；
	// setting 本身仍为空的极端情况下才使用固定兜底文案。
	if strings.TrimSpace(draft.Description) == "" {
		if strings.TrimSpace(draft.Content.Setting) != "" {
			draft.Description = draft.Content.Setting
		} else {
			draft.Description = "一段看似寻常的经历正在等待几位到访者。接受这份邀约，故事便从平常的一天开始。"
		}
		alog.Debug("normalize filled description", "session", sessionID)
	}
	if strings.TrimSpace(draft.Content.Intro) == "" {
		dateStr := settingDateRe.FindString(draft.Content.Setting)
		if dateStr == "" {
			dateStr = "当天"
		}
		clock := ""
		if t, err := models.ParseGameTime(draft.Content.GameStartTime); err == nil {
			clock = fmt.Sprintf("%d点%02d分", t.Hour(), t.Minute())
		}
		draft.Content.Intro = fmt.Sprintf(
			"%s%s，光线平常，人声细碎，你们按各自的缘由抵达此地，只等着弄清此行要处理的这件事。",
			dateStr, clock,
		)
		alog.Debug("normalize filled intro", "session", sessionID)
	}
	if strings.TrimSpace(draft.Content.MapDescription) == "" {
		draft.Content.MapDescription = "【文字地图】各调查地点是剧本状态节点，不是顺序关卡：入口连接所有可调查地点；地点之间可往返；时间推进时，各地点状态可能因派系行动而改变。"
		alog.Debug("normalize filled map_description", "session", sessionID)
	}
	if len(constraints.ToneTags) > 0 && !sameStringSlice(draft.Content.ToneTags, constraints.ToneTags) {
		alog.Debug("normalize override tone_tags", "session", sessionID, "from", strings.Join(draft.Content.ToneTags, ","), "to", strings.Join(constraints.ToneTags, ","))
		draft.Content.ToneTags = append([]string(nil), constraints.ToneTags...)
	}
	if len(draft.Content.Scenes) == 0 {
		draft.Content.Scenes = []models.SceneData{{
			ID:          "location_1",
			Name:        "调查入口",
			Description: "调查员进门就能感觉到异常已经公开出现，主动调查能获得第一批事实；是否公开或隐瞒所知会改变各方反应，拖延下去局势会继续推进；由此可以前往其他相关地点。",
			Triggers:    []string{"available_from_start"},
		}}
		alog.Debug("normalize generated default scene", "session", sessionID)
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
		alog.Debug("normalize generated default npc", "session", sessionID)
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
		alog.Debug("normalize generated default clues", "session", sessionID, "count", 3)
	}
	for i := range draft.Content.Clues {
		draft.Content.Clues[i].Summary = strings.TrimSpace(draft.Content.Clues[i].Summary)
		if strings.TrimSpace(draft.Content.Clues[i].Nature) == "" {
			draft.Content.Clues[i].Nature = "真实"
		}
	}
	// NOTE: 神话本质由 compiler 通过 set_keeper_notes 直接写进 MythosCore，这里只在它仍为空时用锚点合成一句兜底。
	if strings.TrimSpace(draft.Content.MythosCore) == "" && strings.TrimSpace(draft.Content.MythosAnchor) != "" {
		draft.Content.MythosCore = fmt.Sprintf("神话本质(核心发现): %s；到达终止节点并触发揭示后承担理智代价。", draft.Content.MythosAnchor)
		alog.Debug("normalize synthesized mythos_core from anchor", "session", sessionID)
	}
	if len(draft.Content.Endings) == 0 {
		draft.Content.Endings = []models.EndingData{
			{Name: "余波固化", Trigger: "调查员让关键事实公开并改变至少一个派系时间线", Description: "局势以较低代价固化，但神话锚点的余波仍保留。", SANReward: "恢复1d6"},
			{Name: "新的稳定态", Trigger: "关键时间线终点到达且调查员没有改变任何派系行动", Description: "局势进入新的稳定态，某人或某地不可挽回地改变。", IsFailure: true, SANReward: "损失1d10"},
		}
		alog.Debug("normalize filled default endings", "session", sessionID, "count", 2)
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
		alog.Debug("normalize filled keeper_appendix core_truth", "session", sessionID)
	}
}
