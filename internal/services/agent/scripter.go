// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// ---------------------------------------------------------------------------
// Prompts for the 3-phase pipeline
// ---------------------------------------------------------------------------

var outlineSystemPrompt = `你是 COC TRPG(克苏鲁的呼唤第7版)模组设计师。
根据用户需求生成一个详细的模组大纲。

【规则书目录】
` + rulebook.RulebookDir + `

【可用工具】
1) search — 在规则书中语义搜索,由专属搜索专员处理
{"action":"search","query":"想了解的规则内容(自然语言描述)"}
- query 描述你想查什么,无需知道确切词
- 示例:{"action":"search","query":"食尸鬼的属性值和战斗能力"}
- 示例:{"action":"search","query":"克苏鲁通神术的施法代价"}

2) read_rulebook_const — 读取规则书内置常量目录/列表,存在假阴性风险(但不存在假阳性)
{"action":"read_rulebook_const","constant":"常量名"}
- 常量名:rulebook_dir / rulebook_detail_dir / aliens / books / great_old_ones_and_gods / monsters / mythos_creatures / spells

3) response — 输出最终大纲
{"action":"response","outline":"大纲纯文本"}

【执行规则】
- 每次输出必须是 JSON 数组
- 先通过 read_rulebook_const 查阅相关规则(怪物、法术、技能等)
- 再通过 search 工具调用,检索规则书原文以核实细节和数值,避免凭空编造。搜索结果会原样反馈给你,帮助你做出正确的设计决策。
- 一轮可包含多个 search/read_rulebook_const
- 一旦获取了所有需要的信息,就需要通过 response 输出完整大纲,结束本阶段
- 仅输出 JSON 数组,不加任何说明文字

【大纲要求】
- 包含:背景设定、叙事结构(遵循用户指定的叙事模板)、主要NPC(含动机和属性范围)、线索链条、胜利条件、失败条件、部分胜利情景
- 根据难度选择合理的BOSS(邪教、怪物、神话生物、外星人、旧日支配者、外神等)
- 所有神话元素(怪物,眷族,旧日支配者,外神等)必须来自 COC 规则书,不要杜撰
- NPC 属性值必须符合 COC 7版标准(人类 15-90,怪物参考规则书)
- 线索设计要有冗余(至少2条路径通向关键信息)

【NPC 多样性强制要求——必须全部满足】
- 至少一个NPC的真实立场与其外表/身份完全相反
- 至少一个NPC有独立行动线,其目标与调查员无关,玩家若忽视将导致可见的世界变化
- 至少一个NPC完全无辜且拒绝相信任何超自然现象,无论调查员如何说服
- 禁止所有主要NPC都是"知情者"身份

【线索分层强制要求】
线索必须分为三类:
- [真实] 指向核心真相,至少两条路径可到达同一关键信息(冗余)
- [误导] 1-2条表面合理但指向错误结论的线索,制造认知陷阱
- [隐藏] 仅在特定技能检定成功后才能发现的深层线索`

// draftPrompt has 3 format args: outline, scenarioExample, lengthSpec
const draftPrompt = `将以下模组大纲转换为完整的 JSON 模组。严格遵循示例格式。

【大纲】
%s

【JSON 格式示例】
%s

【输出要求】
- 仅输出 JSON,不要有其他文字
- system_prompt: 简洁的 KP 指导(2-3句)
- setting: 详细的时代/地点背景(100-200字)
- intro: 开场叙事(200-400字),以第二人称描写
%s
- game_start_slot: 开局时间槽(0-47,每槽30分钟,0=0:00,16=8:00,24=12:00,40=20:00),根据剧情背景选择合适的开局时刻
- map_description: 文字描述的场景地图,列出所有主要地点、空间关系和移动路径(100-200字),帮助KP在运行中准确感知调查员位置
- npcs: 每个NPC有 name/description/attitude/stats
- clues: 线索需标注类型前缀，格式为 "[真实]线索名(地点):描述" / "[误导]线索名(地点):描述" / "[隐藏]线索名(地点):描述(需XXX检定)"
- win_condition: 明确的胜利条件
- lose_condition: 明确的失败条件(如仪式完成、关键NPC死亡、调查员全灭等)
- partial_wins: 数组,列出1-3种部分胜利情景(如"消灭了BOSS但神话秘密已扩散")`

var qaSystemPrompt = `你是 COC TRPG 模组质量审查员(qa_guard)。
审查模组的可玩性、一致性和规则合规性。

【规则书目录】
` + rulebook.RulebookDir + `

【可用工具】
1) search — 在规则书中语义搜索,由专属搜索专员处理
{"action":"search","query":"想了解的规则内容(自然语言描述)"}
- query 描述你想查什么,无需知道确切词

2) read_rulebook_const — 读取规则书内置常量目录/列表,存在假阴性风险(但不存在假阳性)
{"action":"read_rulebook_const","constant":"常量名"}
- 常量名:rulebook_dir / rulebook_detail_dir / aliens / books / great_old_ones_and_gods / monsters / mythos_creatures / spells

3) response — 输出审查结果
{"action":"response","result":{"score":N,"pass":bool,"strengths":[...],"issues":[...],"must_fix":[...]}}

【执行规则】
- 每次输出必须是 JSON 数组
- 先通过 search/read_rulebook_const 核实模组中涉及的怪物、法术、技能等是否合规,再输出 response
- 一轮可包含多个 search/read_rulebook_const,或单个 response,不混用
- 仅输出 JSON 数组,不加任何说明文字

【审查维度(总分100)】
1. 结构完整性(20分):场景、NPC、线索、胜负条件是否齐全,lose_condition和partial_wins是否有意义
2. 线索设计(20分):是否包含[真实]/[误导]/[隐藏]三类线索,冗余路径是否存在
3. 规则合规(20分):神话元素是否来自规则书,NPC属性值是否合规
4. 可玩性(20分):玩家是否有真实决策空间,胜负结果是否依赖玩家行为而非固定剧情
5. 文本质量(10分):背景和开场叙事的氛围营造、语言质量
6. 新颖性(10分):叙事结构是否跳出三幕套路,NPC是否存在反转立场或独立行动线,是否有至少一个让有经验COC玩家感到意外的设计

must_fix 中必须标注:
- 缺失 lose_condition 或 partial_wins
- 线索缺少[误导]或[隐藏]分类
- NPC全部为知情者身份(无多样性)
- 叙事结构完全套用三幕剧且无任何转折

score >= 80 且 must_fix 为空则 pass=true`

const revisionPrompt = `根据 QA 反馈修订以下模组 JSON。仅输出修订后的完整 JSON,不要有其他文字。

【原始大纲】
%s

【当前草案】
%s

【必须修复的问题】
%s

【JSON 格式示例】
%s`

// qaGuardResultExample is used as schema hint when parser LLM repairs QA result JSON.
const qaGuardResultExample = `{"score": 85, "pass": true, "strengths": ["优点1", "优点2"], "issues": ["问题1"], "must_fix": []}`

// scenarioExample is the anonymised lonely_island.json used as a structural reference.
const scenarioExample = `{
  "name": "示例模组名",
  "description": "模组简介",
  "author": "agent-team",
  "tags": "标签1,标签2",
  "min_players": 1,
  "max_players": 4,
  "difficulty": "normal",
  "content": {
    "system_prompt": "你是本场COC跑团的主持人(KP),你将主持名为《模组名》的剧本。保持克苏鲁宇宙恐怖的风格,营造神秘、压抑的氛围。",
    "game_start_slot": 16,
    "setting": "1923年,某地。详细的时代/地点背景描述(100-200字)……",
    "intro": "开场叙事(200-400字),以第二人称描写……",
    "map_description": "【文字地图】\n主要地点及空间关系:\n- 地点A(起点):描述,与地点B相邻,步行约5分钟\n- 地点B:描述,位于地点A东侧,与地点C有小路相连\n- 地点C(终点/BOSS所在):描述,地处偏僻,需经过地点B才能抵达\n关键路径:A→B→C；隐秘路径:A→(密道)→C",
    "scenes": [
      {"id": "arrival", "name": "场景名称", "description": "场景描述", "triggers": ["start"]},
      {"id": "explore", "name": "场景名称", "description": "场景描述", "triggers": ["arrived"]},
      {"id": "climax", "name": "场景名称", "description": "场景描述", "triggers": ["clue_found"]}
    ],
    "npcs": [
      {
        "name": "NPC名",
        "description": "年龄、外貌、身份背景描述",
        "attitude": "对调查员的态度和行为模式",
        "stats": {"STR": 60, "CON": 65, "SIZ": 55, "DEX": 50, "APP": 40, "INT": 70, "POW": 75, "EDU": 80, "HP": 12, "MP": 15}
      }
    ],
    "clues": [
      "[真实]线索名(发现地点):线索详细描述",
      "[真实]线索名(发现地点):线索详细描述（备用路径）",
      "[误导]线索名(发现地点):表面合理但指向错误结论的描述",
      "[隐藏]线索名(发现地点):线索详细描述（需图书馆利用/侦察/心理学检定）"
    ],
    "win_condition": "明确的胜利条件描述",
    "lose_condition": "明确的失败条件描述（如仪式在第X回合完成、关键NPC死亡等）",
    "partial_wins": [
      "部分胜利情景1：调查员阻止了仪式但BOSS逃脱",
      "部分胜利情景2：消灭了BOSS但神话知识已经泄露给了公众"
    ]
  }
}`

var randomTopicSystemPrompt = `你是COC TRPG(克苏鲁的呼唤7版)模组主题灵感提供器,输出多个主题名称,不要有任何其他文字。`

// ---------------------------------------------------------------------------
// Tool-call types for outline & QA phases
// ---------------------------------------------------------------------------

type pipelineToolCall struct {
	Action   string         `json:"action"`
	Keyword  string         `json:"keyword,omitempty"`  // grep (kept for backward compat)
	Query    string         `json:"query,omitempty"`    // search
	Constant string         `json:"constant,omitempty"` // read_rulebook_const
	Outline  string         `json:"outline,omitempty"`  // response (phase 1)
	Result   *qaGuardResult `json:"result,omitempty"`   // response (phase 3)
}

// ---------------------------------------------------------------------------
// Types (kept from original)
// ---------------------------------------------------------------------------

type ScenarioCreationRequest struct {
	Name         string `json:"name"`
	Theme        string `json:"theme"`
	Era          string `json:"era"`
	Difficulty   string `json:"difficulty"`
	MinPlayers   int    `json:"min_players"`
	MaxPlayers   int    `json:"max_players"`
	TargetLength string `json:"target_length"`
	Brief        string `json:"brief"`
	Salt         string `json:"salt"`
}

type ScenarioCreationOutput struct {
	Draft      ScenarioDraft `json:"draft"`
	QA         qaGuardResult `json:"qa"`
	Iterations int           `json:"iterations"`
}

type qaGuardResult struct {
	Score     int      `json:"score"`
	Pass      bool     `json:"pass"`
	Strengths []string `json:"strengths"`
	Issues    []string `json:"issues"`
	MustFix   []string `json:"must_fix"`
}

type ScenarioDraft struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Author      string                 `json:"author"`
	Tags        string                 `json:"tags"`
	MinPlayers  int                    `json:"min_players"`
	MaxPlayers  int                    `json:"max_players"`
	Difficulty  string                 `json:"difficulty"`
	Content     models.ScenarioContent `json:"content"`
}

// ---------------------------------------------------------------------------
// Entry point: 3-phase pipeline
// ---------------------------------------------------------------------------

func randomEra() string {
	eras := []string{"1880s", "1890s", "1900s", "1910s", "1920s", "1930s", "1940s", "1950s", "1960s", "1970s", "1980s", "1990s", "2000s", "2010s", "2020s"}
	return eras[rand.Intn(len(eras))]
}

// narrativeTemplates 叙事结构模板，随机注入大纲 prompt，打破三幕剧单一套路。
var narrativeTemplates = []string{
	"非线性结构：多条独立调查线并行，玩家可自由选择探索顺序，最终汇聚至核心真相",
	"倒叙结构：开场已是悲剧结局，调查员需逆向追溯原因，真相越挖越令人绝望",
	"限时压迫：存在明确的倒计时（如天文现象、仪式日期），若干回合内未完成则神话降临，Bad End 自动触发",
	"信息不对称：每位调查员仅掌握部分碎片信息，必须在互信与保密间做出选择",
	"道德两难：胜利条件相互冲突（拯救一人必然牺牲另一人），没有完美结局",
	"三幕经典结构：序章-调查-高潮，但在高潮处设置认知颠覆性转折（如BOSS实为受害者，或调查员之一是内鬼）",
}

// topicThreatOrigins 威胁来源维度
var topicThreatOrigins = []string{
	"腐化的人类组织（邪教、秘密学会、政府黑计划）",
	"沉睡已久的神话生物（即将苏醒或被意外唤醒）",
	"外星存在（宇宙级入侵或渗透）",
	"时间/维度异常（过去的错误渗入现在）",
	"被法术扭曲的自然力量（土地、海洋、生命体本身异变）",
}

// topicSocialLayers 社会层维度
var topicSocialLayers = []string{
	"底层社会（贫民窟、矿工、移民社区）",
	"学术精英（大学、博物馆、考古队）",
	"军事/政府（战场、情报机构、监狱）",
	"地下犯罪（黑市、帮派、走私网络）",
	"上流社会（豪门世家、宗教权贵、金融寡头）",
}

// topicTwists NPC/剧情转折维度
var topicTwists = []string{
	"调查员的雇主本身是幕后黑手",
	"BOSS实为受害者，真正的威胁尚未露面",
	"调查员之一在不知情下已被污染或控制",
	"胜利条件需要调查员主动牺牲某样珍贵之物",
	"真相曝光后，调查员面临：知情者是否比无知者更危险的选择",
}

func randomNarrativeTemplate() string {
	return narrativeTemplates[rand.Intn(len(narrativeTemplates))]
}

func randomTopicConstraints() string {
	threat := topicThreatOrigins[rand.Intn(len(topicThreatOrigins))]
	layer := topicSocialLayers[rand.Intn(len(topicSocialLayers))]
	twist := topicTwists[rand.Intn(len(topicTwists))]
	return fmt.Sprintf("威胁来源=%s | 社会背景=%s | 核心转折=%s", threat, layer, twist)
}

func RunScripterScenarioTeam(ctx context.Context, req ScenarioCreationRequest) (ScenarioCreationOutput, error) {
	// Defaults
	if req.MinPlayers <= 0 {
		req.MinPlayers = 1
	}
	if req.MaxPlayers <= 0 {
		req.MaxPlayers = 4
	}
	if req.Difficulty == "" {
		req.Difficulty = "normal"
	}
	if req.Era == "" {
		req.Era = randomEra()
	}

	reqJSON, _ := json.Marshal(req)
	log.Printf("[scripter] 开始3阶段生成 req=%s", reqJSON)

	// Load agents: architect + qa_guard + parser (JSON fixer)
	architect, err := loadSingleAgent(models.AgentRoleArchitect)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	qaAgent, err := loadSingleAgent(models.AgentRoleQAGuard)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	parser, err := loadSingleAgent(models.AgentRoleParser)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}

	if req.Theme == "" {
		req.Theme = generateRandomTopic(ctx, req.Salt)
	}
	debugf("script", "theme: %v", req.Theme)

	// Phase 1: Outline (with grep tool calls)
	outline, err := generateOutline(ctx, architect, req)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("phase1 outline 失败: %w", err)
	}
	log.Printf("[scripter] phase1 outline len=%d", len(outline))

	// Phase 2: Draft (pure JSON generation; parser as JSON fixer)
	draft, err := buildDraft(ctx, architect, parser, outline, req.TargetLength)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("phase2 draft 失败: %w", err)
	}
	applyGuardrails(&draft, req)
	log.Printf("[scripter] phase2 draft name=%q scenes=%d npcs=%d clues=%d",
		draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))

	// Phase 3: QA + Iteration (up to 2 revisions, with grep tool calls)
	var qaResult qaGuardResult
	for i := 0; i < 3; i++ {
		if ctx.Err() != nil {
			return ScenarioCreationOutput{}, ctx.Err()
		}
		qaResult, err = runQA(ctx, qaAgent, parser, req, draft)
		if err != nil {
			log.Printf("[scripter] phase3 QA失败 iter=%d: %v", i, err)
			return ScenarioCreationOutput{}, fmt.Errorf("phase3 QA 失败: %w", err)
		}
		log.Printf("[scripter] phase3 QA iter=%d score=%d pass=%v must_fix=%d",
			i, qaResult.Score, qaResult.Pass, len(qaResult.MustFix))

		if qaResult.Pass {
			return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: i + 1}, nil
		}

		// Last iteration — don't revise, just return best effort
		if i == 2 {
			break
		}

		// Revise draft based on QA feedback
		revised, revErr := reviseDraft(ctx, architect, parser, draft, qaResult.MustFix, outline)
		if revErr != nil {
			log.Printf("[scripter] revision 失败 iter=%d: %v", i, revErr)
			break // return best effort
		}
		applyGuardrails(&revised, req)
		draft = revised
		log.Printf("[scripter] revision iter=%d done", i)
	}

	// Return best effort even if QA didn't pass
	return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: 3}, nil
}

func generateRandomTopic(ctx context.Context, seed string) string {
	agent, err := loadSingleAgentWithTemperature(models.AgentRoleArchitect, 1.2)
	if err != nil {
		return "未知冒险"
	}
	var raw string
	const iter = 3
	randGen := rand.New(rand.NewSource(time.Now().Unix()))
	constraints := randomTopicConstraints()
	msgs := []llm.ChatMessage{
		{Role: "system", Content: agent.systemPrompt(randomTopicSystemPrompt)},
		{Role: "user", Content: fmt.Sprintf("请生成一个COC模组主题灵感提供器,输出多个主题名称,不要有任何其他文字。\n种子: %s\n【创作约束(必须体现在主题中)】%s", seed, constraints)},
	}
	for i := 0; i < iter; i++ {
		if ctx.Err() != nil {
			return "未知冒险"
		}
		raw, err = agent.provider.Chat(ctx, msgs)
		if err != nil {
			break
		}
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})
		debugf("script", "architect iter=%d raw=%v", i+1, raw)
		rawRune := []rune(raw)
		if len(rawRune) < 5 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "输出太短了,请重新生成一个更有创意的主题名称"})
		} else {
			startPoint := randGen.Intn(5)
			endPoint := startPoint + 5 + randGen.Intn(len(rawRune)-startPoint-5)
			randSlice := rawRune[startPoint:endPoint]
			msgs = append(msgs, llm.ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("我们认为这里： %v 不够好, 请重新生成一个更有创意的主题, 输出只包含主题名称", string(randSlice)),
			})
		}
	}
	if err != nil {
		return "未知冒险"
	}
	return strings.TrimSpace(raw)
}

// ---------------------------------------------------------------------------
// Phase 1: Generate Outline (with tool-call loop for grep)
// ---------------------------------------------------------------------------

func generateOutline(ctx context.Context, architect agentHandle, req ScenarioCreationRequest) (string, error) {
	reqJSON, _ := json.Marshal(req)
	template := randomNarrativeTemplate()
	log.Printf("[outline] 叙事模板: %s", template)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: architect.systemPrompt(outlineSystemPrompt)},
		{Role: "user", Content: fmt.Sprintf("请使用随机NPC姓名，创作需求如下(JSON):\n%s\n\n【本次叙事结构模板(必须遵循)】\n%s", string(reqJSON), template)},
	}

	const maxIter = 30
	for iter := 0; iter < maxIter; iter++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		log.Printf("[outline] iter=%d", iter+1)

		var raw string
		var err error
		for i := 0; i < 3; i++ { // retry loop for transient LLM errors
			raw, err = architect.provider.Chat(ctx, msgs)
			if err != nil {
				return "", err
			}
			if raw != "" {
				break
			}
		}
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		debugf("outline", "raw: %v", raw)

		calls := parsePipelineCalls(raw)
		if len(calls) == 0 {
			// If no tool calls parsed, treat raw text as outline directly
			log.Printf("[outline] iter=%d 无tool call,使用原始文本作为大纲", iter+1)
			return strings.TrimSpace(raw), nil
		}

		// Check for response
		for _, c := range calls {
			if c.Action == "response" && c.Outline != "" {
				log.Printf("[outline] iter=%d response 完成", iter+1)
				return strings.TrimSpace(c.Outline), nil
			}
		}

		// Execute search calls
		feedback := executeSearchCalls(ctx, calls, "outline")
		if feedback == "" {
			return "", fmt.Errorf("outline 未返回有效 tool call")
		}
		msgs = append(msgs, llm.ChatMessage{
			Role:    "user",
			Content: "规则书搜索结果如下,请继续:\n\n" + feedback,
		})
	}

	return "", fmt.Errorf("outline 达到最大迭代仍未返回 response")
}

// ---------------------------------------------------------------------------
// Phase 2: Build Draft (pure JSON, no tool calls)
// ---------------------------------------------------------------------------

func buildDraft(ctx context.Context, architect, fixer agentHandle, outline string, targetLength string) (ScenarioDraft, error) {
	userMsg := fmt.Sprintf(draftPrompt, outline, scenarioExample, lengthSpec(targetLength))
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是 COC TRPG 模组 JSON 生成器。仅输出合法 JSON,不要有任何其他文字。"},
		{Role: "user", Content: userMsg},
	}

	var draft ScenarioDraft
	if err := chatAndParseDraft(ctx, architect, fixer, msgs, &draft); err != nil {
		return ScenarioDraft{}, err
	}
	return draft, nil
}

// ---------------------------------------------------------------------------
// Phase 3: QA (with tool-call loop for grep)
// ---------------------------------------------------------------------------

func runQA(ctx context.Context, qaAgent agentHandle, parser agentHandle, req ScenarioCreationRequest, draft ScenarioDraft) (qaGuardResult, error) {
	reqJSON, _ := json.Marshal(req)
	draftJSON, _ := json.Marshal(draft)

	userMsg := fmt.Sprintf("审查以下 COC 模组的质量。\n\n【原始需求】\n%s\n\n【模组草案】\n%s",
		string(reqJSON), string(draftJSON))

	msgs := []llm.ChatMessage{
		{Role: "system", Content: qaAgent.systemPrompt(qaSystemPrompt)},
		{Role: "user", Content: userMsg},
	}

	const maxIter = 30
	for iter := 0; iter < maxIter; iter++ {
		if ctx.Err() != nil {
			return qaGuardResult{}, ctx.Err()
		}
		log.Printf("[qa] iter=%d", iter+1)

		raw, err := qaAgent.provider.Chat(ctx, msgs)
		if err != nil {
			return qaGuardResult{}, err
		}
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		calls := parsePipelineCalls(raw)
		if len(calls) == 0 {
			// Try direct JSON parse as fallback, use parser LLM on failure
			result, err := parseQAResultWithLLM(ctx, parser, raw)
			if err == nil {
				return result, nil
			}
			return qaGuardResult{}, fmt.Errorf("qa_guard 未返回可解析的 tool call 或 JSON, %v", err)
		}

		// Check for response
		for _, c := range calls {
			if c.Action == "response" {
				if c.Result != nil {
					log.Printf("[qa] iter=%d response score=%d pass=%v", iter+1, c.Result.Score, c.Result.Pass)
					return *c.Result, nil
				}
				// result field failed to parse in pipelineToolCall — extract raw response JSON and repair
				log.Printf("[qa] iter=%d response c.Result==nil,尝试从原始输出解析", iter+1)
				result, repErr := parseQAResultWithLLM(ctx, parser, raw)
				if repErr != nil {
					return qaGuardResult{}, fmt.Errorf("qa result LLM修复失败: %w", repErr)
				}
				return result, nil
			}
		}

		// Execute search calls
		feedback := executeSearchCalls(ctx, calls, "qa")
		if feedback == "" {
			return qaGuardResult{}, fmt.Errorf("qa_guard 未返回有效 tool call")
		}
		msgs = append(msgs, llm.ChatMessage{
			Role:    "user",
			Content: "规则书搜索结果如下,请据此完成审查:\n\n" + feedback,
		})
	}

	return qaGuardResult{}, fmt.Errorf("qa_guard 达到最大迭代仍未返回 response")
}

// ---------------------------------------------------------------------------
// Revision: targeted fix based on QA feedback (pure JSON, no tool calls)
// ---------------------------------------------------------------------------

func reviseDraft(ctx context.Context, architect, fixer agentHandle, draft ScenarioDraft, mustFix []string, outline string) (ScenarioDraft, error) {
	draftJSON, _ := json.Marshal(draft)
	issues := strings.Join(mustFix, "\n- ")

	userMsg := fmt.Sprintf(revisionPrompt, outline, string(draftJSON), issues, scenarioExample)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是 COC TRPG 模组修订器。根据QA反馈修订模组。仅输出修订后的完整 JSON,不要有其他文字。"},
		{Role: "user", Content: userMsg},
	}

	var revised ScenarioDraft
	if err := chatAndParseDraft(ctx, architect, fixer, msgs, &revised); err != nil {
		return ScenarioDraft{}, err
	}
	return revised, nil
}

// ---------------------------------------------------------------------------
// Shared: parse tool calls & execute grep
// ---------------------------------------------------------------------------

func parsePipelineCalls(raw string) []pipelineToolCall {
	stripped := llm.StripCodeFence(raw)
	var calls []pipelineToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil && len(calls) > 0 {
		return calls
	}
	if s := strings.Index(stripped, "["); s >= 0 {
		if e := strings.LastIndex(stripped, "]"); e > s {
			_ = json.Unmarshal([]byte(stripped[s:e+1]), &calls)
		}
	}
	return calls
}

func executeSearchCalls(ctx context.Context, calls []pipelineToolCall, tag string) string {
	var sb strings.Builder
	for _, c := range calls {
		switch c.Action {
		case "search":
			if c.Query == "" {
				continue
			}
			log.Printf("[%s] search query=%q", tag, c.Query)
			lawyerHandle, err := loadSingleAgent(models.AgentRoleLawyer)
			if err != nil {
				log.Printf("[%s] search: lawyer agent 加载失败: %v", tag, err)
				sb.WriteString(fmt.Sprintf("【search:%s】\n(lawyer agent 不可用)\n\n", c.Query))
				continue
			}
			results := runLawyer(ctx, lawyerHandle, c.Query, rulebook.GlobalIndex)
			sb.WriteString(fmt.Sprintf("【search:%s】\n%s\n\n", c.Query, formatLawyerResults(results)))
		case "read_rulebook_const":
			if c.Constant == "" {
				continue
			}
			text := rulebook.ReadConstant(c.Constant)
			sb.WriteString(fmt.Sprintf("【read_rulebook_const:%s】\n%s\n\n", c.Constant, text))
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// chatAndParseDraft calls the generator LLM once, then hands JSON repair to
// the parser agent when unmarshal fails.
func chatAndParseDraft(ctx context.Context, generator agentHandle, parser agentHandle, msgs []llm.ChatMessage, out *ScenarioDraft) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Step 1: generator produces the draft
	raw, err := generator.provider.Chat(ctx, msgs)
	if err != nil {
		return err
	}
	parseErr := parseJSONObject(raw, out)
	if parseErr == nil {
		return nil
	}
	log.Printf("[draft] generator JSON parse failed: %v", parseErr)

	// Step 2: parser agent repairs the JSON
	fixed, repairErr := repairJSONWith(ctx, parser, raw, parseErr, scenarioExample)
	if repairErr != nil {
		return fmt.Errorf("draft JSON 修复失败: %w (原始错误: %v)", repairErr, parseErr)
	}
	if err := parseJSONObject(fixed, out); err == nil {
		return nil
	} else {
		// First repair can return syntactically valid JSON but still mismatched schema.
		// Feed the concrete schema error back into parser once more.
		log.Printf("[draft] parser output schema mismatch, retry parser: %v", err)
		repairedAgain, repairErr2 := repairJSONWith(ctx, parser, fixed, err, scenarioExample)
		if repairErr2 != nil {
			return fmt.Errorf("修复后的 JSON 结构仍不匹配,二次修复失败: %w (结构错误: %v)", repairErr2, err)
		}
		if err2 := parseJSONObject(repairedAgain, out); err2 != nil {
			return fmt.Errorf("二次修复后的 JSON 仍无法解析为 ScenarioDraft: %w", err2)
		}
	}
	return nil
}

// RepairJSON uses the parser agent to fix malformed JSON. Exported so other
// subsystems (e.g. director) can reuse the same low-temperature fixer.
// rawJSON is the broken output, parseErr is the error from json.Unmarshal,
// schemaExample is a correct JSON example showing the expected structure.
// Returns the repaired JSON string, or an error if repair fails.
func RepairJSON(ctx context.Context, rawJSON string, parseErr error, schemaExample string) (string, error) {
	parser, err := loadSingleAgent(models.AgentRoleParser)
	if err != nil {
		return "", fmt.Errorf("parser agent 未配置: %w", err)
	}
	return repairJSONWith(ctx, parser, rawJSON, parseErr, schemaExample)
}

func repairJSONWith(ctx context.Context, parser agentHandle, rawJSON string, parseErr error, schemaExample string) (string, error) {
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是 JSON 修复工具。用户会给你一段有问题的 JSON 和错误信息,你需要修复它使其匹配目标格式。仅输出修正后的合法 JSON,不要有任何其他文字。"},
	}

	const maxAttempts = 20
	currentErr := parseErr
	raw := rawJSON
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		fixPrompt := fmt.Sprintf(
			"以下 JSON 无法解析为目标结构。\n\n"+
				"【解析错误】\n%s\n\n"+
				"【原始 JSON】\n%s\n\n"+
				"【目标格式示例】\n%s\n\n"+
				"请修复并输出完整的合法 JSON。",
			currentErr.Error(), raw, schemaExample)
		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fixPrompt})

		fixed, chatErr := parser.provider.Chat(ctx, msgs)
		if chatErr != nil {
			return "", fmt.Errorf("parser 调用失败: %w", chatErr)
		}
		debugf("Parser", "Fixed JSON: %v", fixed)

		// Verify the fix by stripping code fences
		stripped := llm.StripCodeFence(strings.TrimSpace(fixed))
		if json.Valid([]byte(stripped)) {
			log.Printf("[parser] JSON 修复成功 attempt=%d", attempt)
			return stripped, nil
		}
		// Extract {...} if surrounded by text
		if s := strings.Index(stripped, "{"); s >= 0 {
			if e := strings.LastIndex(stripped, "}"); e > s {
				candidate := stripped[s : e+1]
				if json.Valid([]byte(candidate)) {
					log.Printf("[parser] JSON 修复成功(提取) attempt=%d", attempt)
					return candidate, nil
				}
			}
		}

		currentErr = fmt.Errorf("修复后的 JSON 仍然无效")
		raw = fixed
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: fixed})
		log.Printf("[parser] attempt=%d 修复后仍无效", attempt)
	}
	return "", fmt.Errorf("parser 修复失败(%d次尝试)", maxAttempts)
}

// parseQAResultWithLLM tries direct JSON unmarshal of a qaGuardResult,
// falling back to parser LLM repair on failure.
func parseQAResultWithLLM(ctx context.Context, parser agentHandle, raw string) (qaGuardResult, error) {
	var result qaGuardResult
	if err := parseJSONObject(raw, &result); err == nil {
		return result, nil
	} else {
		log.Printf("[qa] 直接解析失败,使用parser LLM修复: %v", err)
		fixed, repairErr := repairJSONWith(ctx, parser, raw, err, qaGuardResultExample)
		if repairErr != nil {
			return qaGuardResult{}, fmt.Errorf("qa result JSON 修复失败: %w (原始错误: %v)", repairErr, err)
		}
		var result2 qaGuardResult
		if err2 := parseJSONObject(fixed, &result2); err2 != nil {
			return qaGuardResult{}, fmt.Errorf("修复后的 qa result 仍无法解析: %w", err2)
		}
		return result2, nil
	}
}

func parseJSONObject[T any](raw string, out *T) error {
	var err error
	stripped := llm.StripCodeFence(strings.TrimSpace(raw))
	if err = json.Unmarshal([]byte(stripped), out); err == nil {
		return nil
	}
	s := strings.Index(stripped, "{")
	e := strings.LastIndex(stripped, "}")
	if s >= 0 && e > s {
		if err = json.Unmarshal([]byte(stripped[s:e+1]), out); err == nil {
			return nil
		}
	}
	return fmt.Errorf("JSON 解析失败: %w", err)
}

func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// lengthSpec returns scene/clue count requirements based on target_length.
func lengthSpec(targetLength string) string {
	switch targetLength {
	case "long":
		return "- scenes: 6-10个场景,每个有 id/name/description/triggers\n- clues: 10-15条线索,格式为\"线索名(地点):描述\""
	case "medium":
		return "- scenes: 4-6个场景,每个有 id/name/description/triggers\n- clues: 7-10条线索,格式为\"线索名(地点):描述\""
	default: // short
		return "- scenes: 3-4个场景,每个有 id/name/description/triggers\n- clues: 5-7条线索,格式为\"线索名(地点):描述\""
	}
}

func applyGuardrails(draft *ScenarioDraft, req ScenarioCreationRequest) {
	draft.Name = firstNonEmpty(req.Name, draft.Name)
	draft.MinPlayers = req.MinPlayers
	draft.MaxPlayers = req.MaxPlayers
	draft.Difficulty = firstNonEmpty(req.Difficulty, draft.Difficulty)
	if draft.Author == "" {
		draft.Author = "agent-team"
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// grepRulebook searches the rulebook for exact keyword matches and returns
// surrounding context (30 lines before/after each hit), capped at 2000 chars.
func grepRulebook(keyword string) string {
	hits := rulebook.GrepRuleBook(keyword)
	if len(hits) == 0 {
		return ""
	}

	const maxLen = 20

	var sb strings.Builder
	for i, h := range hits {
		s := h.Text
		if len(s) > maxLen {
			runes := []rune(s)
			if len(runes) > maxLen {
				s = string(runes[:maxLen]) + "..."
			}
		}
		sb.WriteString(fmt.Sprintf("[%v] Hit Line: %v Content: %v\n", i+1, h.LineNum, s))
	}
	return strings.TrimSpace(sb.String())
}
