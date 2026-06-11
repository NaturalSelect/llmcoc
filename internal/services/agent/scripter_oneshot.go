// scripter_oneshot.go — Single-shot scenario generation with translate_anchor validation.
//
// The architect runs in a tool-call loop:
//  1. think (optional internal reasoning)
//  2. translate_anchor (one or more times) — validates CoC element via rulebook
//  3. submit — carries the complete oneshotResult JSON
//
// This preserves real-time rulebook validation while eliminating separate
// IronyCore / MisdirectionFabric / InvestigationGraph stages.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// ---------------------------------------------------------------------------
// Output type
// ---------------------------------------------------------------------------

// oneshotResult is the JSON payload inside the architect's submit tool call.
// It carries both the standard ScenarioDraft fields and design-metadata fields
// (delta_operator, surface_reading, etc.) used for IronyCore compat and reward agent.
type oneshotResult struct {
	// Design metadata
	DeltaOperator     string `json:"delta_operator,omitempty"`
	DeltaOperatorDesc string `json:"delta_operator_desc,omitempty"`
	SurfaceReading    string `json:"surface_reading,omitempty"`
	DeepTruth         string `json:"deep_truth,omitempty"`
	FalseDelta        string `json:"false_delta,omitempty"`
	SharedEvidence    string `json:"shared_evidence,omitempty"`
	EmotionalWeight   string `json:"emotional_weight,omitempty"`
	RewardConcept     string `json:"reward_concept,omitempty"`
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

func (r oneshotResult) toScenarioDraft() ScenarioDraft {
	return ScenarioDraft{
		Name: r.Name, Description: r.Description,
		Author: r.Author, Tags: r.Tags,
		MinPlayers: r.MinPlayers, MaxPlayers: r.MaxPlayers,
		Difficulty: r.Difficulty, Content: r.Content,
	}
}

func (r oneshotResult) toIronyCore() IronyCore {
	return IronyCore{
		DeltaOperator:     r.DeltaOperator,
		DeltaOperatorDesc: r.DeltaOperatorDesc,
		SurfaceReading:    r.SurfaceReading,
		DeepTruth:         r.DeepTruth,
		FalseDelta:        r.FalseDelta,
		SharedEvidence:    r.SharedEvidence,
		EmotionalWeight:   r.EmotionalWeight,
	}
}

// oneshotExample is the JSON schema example used for parsing/repair prompts.
const oneshotExample = `{"delta_operator":"role_swap","delta_operator_desc":"","surface_reading":"老人每晚去图书馆取走特定书籍——表面是盗窃","deep_truth":"书是他自己的，他在取回被窃之物","false_delta":"identity_collapse","shared_evidence":"老人对书籍位置异乎寻常地熟悉，从不乱翻","emotional_weight":"「盗贼」与「失主」的身份在道德上互换","reward_concept":"与食尸鬼有关的古籍手稿","name":"示例模组","description":"围绕派系时间线和调查员可拉动杠杆展开的COC情境简报。","author":"agent-team","tags":"sandbox,coc","min_players":1,"max_players":4,"difficulty":"normal","content":{"system_prompt":"你是KP，管理会自行推进的局势。【KP独有】δ内部真相：书是Douglas自己的，他在取回被窃之物。","setting":"镇图书馆连续三夜有书籍失踪，守墓人向警方报告了一个体型异常的入侵者。","intro":"你们进入局势。立即可做的事：①询问守墓人入侵者描述；②检查失窃书目；③决定是否公开异常气味。","game_start_slot":16,"map_description":"【文字地图】图书馆→书架区↔档案室↔墓地。","mythos_anchor":"食尸鬼（Ghoul）：COC7规则书已收录；具体属性按规则书裁定。","scenes":[{"id":"library_main","name":"图书馆大厅","description":"可见：失窃公告。可发现：书目来自同一捐赠者。杠杆：公开规律会导致图书馆关闭。风险：拖延三天后永久关闭。出口：书架区、档案室。感官：潮湿泥土气息与旧纸味格格不入。","triggers":["available_from_start"]}],"npcs":[{"name":"守墓人Henrik","description":"公开身份：图书馆保安。议程：维护秩序。秘密：曾处理Douglas遗物。","attitude":"警惕、简短","stats":{"STR":55,"CON":60,"SIZ":65,"DEX":50,"APP":40,"INT":55,"POW":50,"EDU":55,"SAN":50,"HP":12,"MP":10}}],"clues":["[真实]失窃书目规律(书架区): 全部来自同一捐赠者。","[隐藏]神话本质(墓地): 食尸鬼是死者变形后的存在，保留人类记忆；SAN检定1/1d6；具体属性按规则书裁定。","[误导]守墓人描述(大厅): 体型异常、动作迅速——在deep_truth揭示后仍然准确，只是「盗贼」身份完全颠倒。"],"win_condition":"如果调查员让Douglas重获藏书，则他退隐墓地，书籍谜团以悲哀收场。","lose_condition":"如果图书馆永久关闭，则Douglas转向其他途径，某个新目标成为下一个遭遇者。","partial_wins":["如果阻止了入侵但未弄清身份，则图书馆恢复秩序，但Douglas的执念继续。"]}}`

// ---------------------------------------------------------------------------
// System prompt
// ---------------------------------------------------------------------------

func oneshotSystemPrompt() string {
	return `<role>COC7剧本生成专家</role>
<task>
根据用户请求，一步完成完整COC7剧本的设计与编译。

内部创作流程必须遵循COC模组写作法：先确定恐怖内核，再确定调查焦点，再搭建洋葱式谜团与非线性线索网络，最后编译为可运行的剧本JSON。COC的核心是谜团、调查、氛围与逐步揭露的恐怖，不是战斗。

在内部（不输出中间步骤）按以下六步推理，然后通过工具提交结果：

【步骤①：核心概念与恐怖内核】
先明确：
- 恐怖内核：身体恐怖（变异/腐蚀） / 宇宙恐怖（知识即疯狂） / 哥特恐怖（家族诅咒） / 社会恐怖（"你身边的人都已经被替换了"） / 环境恐怖（"土地本身在排斥人类"） 
- 神话关联度：旧日支配者本体 / 眷属 / 神话物品 / 神话知识污染
- 时代与地域风味：只作为氛围和行动约束，不直接代替谜团
- 调查焦点：失踪、离奇死亡、古物失窃、异常仪式、家族秘密、地方传闻等一个明确入口

要求：
- 开场问题必须让调查员愿意主动调查
- 不要先想战斗或Boss，而是先想调查员最初看到的异常
- brief若为空，也必须先构造一个可调查的表层事件

【步骤②：洋葱式谜团与δ-认知翻转设计】
先把谜团分成三层，再选择delta_operator：
- 表层事件：调查员一开始接触到的案件/异常
- 中层真相：邪教活动、人类阴谋、伪装、掩盖、利益冲突、误认
- 深层恐怖：真正的神话真相、不可名状的知识、存在论崩塌

然后从下方认知翻转类型参考表选择 delta_operator，设计：
- surface_reading：普通观察者立刻形成的推断（不需要预知任何真相）
- deep_truth：揭示后的实际情况
- false_delta：经验读者优先猜测的错误翻转类型（必须与delta_operator作用于不同语义维度）
- shared_evidence：在两种解读框架下均成立的歧义证据
- emotional_weight：揭示时被摧毁的具体认知边界/关系/身份

内部自查②：
✓ surface_reading无需预知真相即可形成？
✓ 表层事件、中层真相、深层恐怖彼此递进，而不是同一句话改写？
✓ delta_operator唯一精确地解释surface→deep变换（换类型就失效）？
✓ 知道deep_truth后，surface_reading的所有表层观察仍然说得通（后验必然性）？
✓ false_delta与delta_operator作用于不同语义维度？

【步骤③：COC神话元素选择与验证】
通过 translate_anchor 工具将 deep_truth 核心概念翻译为COC7规则书元素：
- 必须先调用 translate_anchor 获得规则书裁定，再调用 submit
- 若首选元素在禁用列表中，继续 translate_anchor 寻找替代
- mythos_anchor 应优先支持调查、异化、理智侵蚀和氛围恐怖，而不是鼓励直接战斗解决问题

【步骤④：线索网络、误导与场景设计】
把剧情设计成线索矩阵，而不是单一路径。
- core clue：推进所必需的关键信息
- support clue：帮助理解背景、提高推理确定性的辅助线索
- red herring：增强真实感但不能堵死推进的误导线索
- clue carrier：文件 / NPC / 现场 / 超自然痕迹 / 仪式遗留 / 梦境等
- false_lead：在 deep_truth 揭示后必须仍有合理解释（后验兼容）
- misdirector_npc：有内在动机，不是功能性欺骗工具
- true_trace：兼容两种解读的歧义证据
- reveal_trigger：触发认知翻转的具体事件

场景要求：
- 至少隐含导入、调查、启示、高潮、余波这几个功能中的大部分；不要求显式分标题，但内容要能承载这些阶段
- 每个scene必须包含：可见信息、可发现信息、杠杆、风险、出口、感官细节
- 场景应区分相对安全区、危险区、接近神话本质的区域
- 场景需要随着调查推进而解锁，而不是一股脑全开

线索要求：
- 关键推进信息不能只有单一路径；如果A线索错过，也要能通过B或C抵达同一真相
- 至少一条[误导]线索在真相揭晓后仍能解释得通，不能是纯假线索
- 至少一条[隐藏]线索承担“神话本质”说明，并与 mythos_anchor 强绑定

内部自查④：
✓ false_lead在deep_truth框架下仍能被合理解释？
✓ 是否存在至少两条不同来源的推进路径，而不是把唯一关键线索锁在单一检定里？
✓ 场景之间是可回访、可交叉验证的调查网络，而不是线性过关房间？

【步骤⑤：NPC、时间线、SAN与结局推进】
NPC应承担叙事功能，而不是填表：
- 至少考虑知情者、阻碍者、牺牲品/示警者中的若干角色
- 每个重要NPC要有公开身份、议程、秘密或保留信息的理由

时间线要求：
- 必须存在“过去线”痕迹：事情为何发展到现在
- 必须存在“现在线”推进：无人干预时，局势会继续恶化、转移或完成某种仪式/行动
- current_state：无人干预时正在做的具体行动（非"等待调查员"）
- intervention_pivot：调查员可执行的具体动作（非"可以干预"空话）
- ending_signals → win/lose/partial_wins：条件句结构

SAN要求：
- 恐怖暴露应渐进升级：先是诡异与不协调，再到尸体/仪式，再到直视神话本质
- 不要求在clues里写精确数值表，但至少要体现由轻到重的理智压力升级

内部自查⑤：
✓ 每个派系或关键行动者有自主行动的current_state？
✓ 每个intervention_pivot是具体可执行动作？
✓ 恐怖体验是否呈渐进式升级，而不是一上来直接终极真相？

【步骤⑥：剧本编译最终检查】
✓ setting只描述surface_reading视角，未泄露deep_truth？
✓ intro包含至少3个立即可执行的具体行动？
✓ scenes体现调查网络、场景功能与五感氛围，而不是空泛地点介绍？
✓ clues每条以[真实]/[隐藏]/[误导]开头；至少一条[隐藏]神话本质涵盖mythos_anchor？
✓ 至少一条[误导]线索在deep_truth揭示后仍能合理解释？
✓ 关键推进信息是否具备多入口，而不是依赖单一检定成功？
✓ system_prompt含三项KP协议（时间推进/信息分层/不主动引导）+ deep_truth注入？
✓ win/lose_condition使用条件句，不是二元裁定？
✓ 所有NPC stats含SAN字段？
✓ 最终体验重点是“调查员亲手揭开可怕真相”，而不是“被剧情推着走”或“靠战斗通关”？

其他硬性要求：
- 避免政治话题
- 以克苏鲁宇宙恐惧为基调（渺小感、理智侵蚀、不可知深渊）
- 禁用科学术语/现代技术细节，不要把神话现象解释成硬科幻或工程异常
- 避免把战斗写成主要解法；对抗神话时优先调查、规避、谈判、阻止仪式、改变局势
</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：内部推理（可选，无副作用）
  {"action":"think","think":"推理内容"}
- translate_anchor：将一个创意概念翻译为COC7规则书中最匹配的具体元素；提交前必须至少调用一次
  {"action":"translate_anchor","concept":"概念描述（如「死者被古老力量束缚继续行动」）","reason":"这个概念在剧本中承担什么角色"}
- submit：提交完整剧本；只有在translate_anchor确认元素可用后才调用；必须单独一轮输出
  {"action":"submit","draft":{...完整oneshotResult JSON对象...}}
</tools>
` + formatDeltaOperatorTable() + `
<draft_schema>
submit.draft 必须包含以下字段：
{
  // 设计元数据（用于日志/奖励agent/IronyCore兼容）
  "delta_operator": "认知翻转类型ID",
  "delta_operator_desc": "仅自定义时填写",
  "surface_reading": "表层推断",
  "deep_truth": "揭示真相",
  "false_delta": "错误翻转类型ID",
  "shared_evidence": "歧义证据",
  "emotional_weight": "揭示时崩塌的认知内容",
  "reward_concept": "通关奖励叙事概念（若无则留空字符串）",
  // ScenarioDraft 字段
  "name": "剧本名称",
  "description": "剧本描述",
  "author": "agent-team",
  "tags": "sandbox,coc",
  "min_players": 1,
  "max_players": 4,
  "difficulty": "normal",
  "content": {
    "system_prompt": "KP四项协议 + deep_truth注入",
    "setting": "surface_reading视角的当前局势（不泄露deep_truth）",
    "intro": "入场位置 + 至少3个立即可执行的具体行动",
    "game_start_slot": 16,
    "map_description": "文字地图；体现可回访、可交叉验证的调查网络",
    "mythos_anchor": "translate_anchor确认的COC7元素全称",
    "scenes": [{"id":"...","name":"...","description":"可见/可发现/杠杆/风险/出口/感官细节；体现安全区/危险区/神话逼近区中的至少一种功能","triggers":["available_from_start"]}],
    "npcs": [{"name":"...","description":"公开身份/议程/秘密或保留理由","attitude":"...","stats":{"STR":50,"CON":50,"SIZ":50,"DEX":50,"APP":50,"INT":60,"POW":50,"EDU":60,"SAN":50,"HP":10,"MP":10}}],
    "clues": ["[真实]来自地点A的推进线索：...", "[真实]来自NPC或文件的平行推进线索：...", "[隐藏]神话本质(...): ...", "[误导]..."],
    "win_condition": "如果[条件]，则[处境变化]，[什么不可挽回地改变]",
    "lose_condition": "如果[条件]，则[局势进入新稳定态]，[什么不可挽回地改变]",
    "partial_wins": ["如果[条件]，则[部分结局]"]
  }
}
</draft_schema>`
}

const oneshotArchitectToolCallExample = `[{"action":"translate_anchor","concept":"死者被古老力量束缚继续行动","reason":"作为本剧本mythos_anchor的核心概念"}]`

// ---------------------------------------------------------------------------
// Tool types
// ---------------------------------------------------------------------------

const (
	toolOneshotTranslateAnchor ToolCallType = "translate_anchor"
	toolOneshotSubmit          ToolCallType = "submit"

	// Shared translator tool call types (used by scripter_reward.go as well).
	toolTranslatorAskLawyer ToolCallType = "ask_lawyer"
	toolTranslatorRespond   ToolCallType = "respond"
)

type oneshotArchitectToolCall struct {
	Action  ToolCallType   `json:"action"`
	Think   string         `json:"think,omitempty"`
	Concept string         `json:"concept,omitempty"` // translate_anchor
	Reason  string         `json:"reason,omitempty"`  // translate_anchor
	Draft   *oneshotResult `json:"draft,omitempty"`   // submit
}

// ---------------------------------------------------------------------------
// Architect loop
// ---------------------------------------------------------------------------

func runOneshotArchitectLoop(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage) (oneshotResult, []llm.ChatMessage, error) {
	if room.architect.provider == nil {
		return oneshotResult{}, msgs, fmt.Errorf("architect provider unavailable")
	}
	const maxRounds = 30
	translatedOnce := false
	for round := 1; round <= maxRounds; round++ {
		if ctx.Err() != nil {
			return oneshotResult{}, msgs, ctx.Err()
		}
		logStagePrompt(fmt.Sprintf("oneshot_loop_round_%d", round), msgs)
		raw, err := room.architect.provider.JsonChat(ctx, msgs)
		if err != nil {
			return oneshotResult{}, msgs, err
		}
		log.Printf("[scripter:oneshot_loop] round=%d raw_len=%d raw=%s", round, len(raw), truncateRunes(raw, scripterRawLogLimit))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		calls, parseErr := parseOneshotArchitectToolCalls(ctx, room.parser, raw)
		if parseErr != nil {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: JSON解析失败，必须重新输出合法JSON数组。"})
			continue
		}
		if len(calls) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须输出至少一个工具调用。"})
			continue
		}

		// submit must be alone
		if oneshotSubmitMixed(calls) {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: submit必须单独一轮输出，不能和think、translate_anchor或任何其他action混在同一个JSON数组中。若还需翻译，本轮只输出translate_anchor；若已有足够信息，下一轮只输出一个submit。"})
			continue
		}

		invalid := false
		hasTranslate := false
		var submitDraft *oneshotResult
		var toolResults []string

		for _, call := range calls {
			switch call.Action {
			case ToolThink:
				// silent
			case toolOneshotTranslateAnchor:
				hasTranslate = true
				translatedOnce = true
				toolResults = append(toolResults, executeOneshotTranslateAnchor(ctx, room, call))
			case toolOneshotSubmit:
				if call.Draft == nil {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: submit的draft字段不能为空。"})
					invalid = true
				} else {
					submitDraft = call.Draft
				}
			default:
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(
					"SYSTEM REJECT: 此阶段只允许think/translate_anchor/submit，不允许%s。", call.Action)})
				invalid = true
			}
		}
		if invalid {
			continue
		}
		if len(toolResults) > 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: strings.Join(toolResults, "\n")})
		}
		if hasTranslate {
			// wait for the LLM to process results before submitting
			continue
		}
		if submitDraft != nil {
			if !translatedOnce {
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须先调用translate_anchor验证神话元素，再调用submit。"})
				continue
			}
			return *submitDraft, msgs, nil
		}
		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须先调用translate_anchor，再用submit提交完整剧本。"})
	}
	return oneshotResult{}, msgs, fmt.Errorf("oneshot architect 未在%d轮内提交结果", maxRounds)
}

func oneshotSubmitMixed(calls []oneshotArchitectToolCall) bool {
	submitCount := 0
	for _, c := range calls {
		if c.Action == toolOneshotSubmit {
			submitCount++
		}
	}
	return submitCount > 0 && len(calls) != 1
}

func parseOneshotArchitectToolCalls(ctx context.Context, parser agentHandle, raw string) ([]oneshotArchitectToolCall, error) {
	stripped := strings.TrimSpace(llm.StripCodeFence(llm.JsonArryProtect(raw)))
	var calls []oneshotArchitectToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil {
		return calls, nil
	} else if parser.provider != nil {
		fixed, repairErr := repairJSONWith(ctx, parser, stripped, err, oneshotArchitectToolCallExample)
		if repairErr != nil {
			return nil, repairErr
		}
		fixed = strings.TrimSpace(llm.JsonArryProtect(fixed))
		if err2 := json.Unmarshal([]byte(fixed), &calls); err2 != nil {
			return nil, err2
		}
		return calls, nil
	} else {
		return nil, err
	}
}

// ---------------------------------------------------------------------------
// translate_anchor execution — calls translator sub-agent
// ---------------------------------------------------------------------------

func executeOneshotTranslateAnchor(ctx context.Context, room *scripterRoom, call oneshotArchitectToolCall) string {
	concept := strings.TrimSpace(call.Concept)
	if concept == "" {
		return `<translate_anchor_result error="concept字段为空，无法翻译"/>`
	}
	reason := strings.TrimSpace(call.Reason)
	log.Printf("[scripter:oneshot_translate_anchor] concept=%q reason=%q", truncateRunes(concept, 200), truncateRunes(reason, 200))
	result, err := runOneshotTranslatorAgent(ctx, room, concept, reason)
	if err != nil {
		log.Printf("[scripter:oneshot_translate_anchor] error concept=%q err=%v", truncateRunes(concept, 200), err)
		return fmt.Sprintf(`<translate_anchor_result concept=%q status="translator_error">%s</translate_anchor_result>`, concept, err.Error())
	}
	result = strings.TrimSpace(result)
	if result == "" {
		return fmt.Sprintf(`<translate_anchor_result concept=%q status="no_result">translator未返回可用结论；可尝试调整概念描述重新翻译，或转向人类法师、诅咒物品、古老地点等方向。</translate_anchor_result>`, concept)
	}
	return fmt.Sprintf(`<translate_anchor_result concept=%q status="translated">%s</translate_anchor_result>`, concept, result)
}

// ---------------------------------------------------------------------------
// Translator sub-agent (validates CoC element via lawyer/rulebook)
// ---------------------------------------------------------------------------

const oneshotTranslatorSystemPrompt = `<role>COC7规则书概念翻译专家</role>
<task>收到一个创意概念，将它翻译为COC7规则书中最匹配、可在剧本中使用的具体元素（实体/典籍/法术/诅咒物品/机制）。通过 ask_lawyer 向规则书专家提问，依据裁定综合，最后用 respond 返回翻译结论。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：内部推理（可选，无副作用）
  {"action":"think","think":"推理内容"}
- ask_lawyer：向COC7规则书专家提出一个具体规则书问题；可多次调用
  {"action":"ask_lawyer","question":"具体规则书问题"}
- respond：返回最终翻译结论并退出；必须在至少一次ask_lawyer之后调用；必须单独一轮输出
  {"action":"respond","result":"结构化翻译结论"}
</tools>
<batch_rules>
- 每轮只能是以下两种批次之一：
  A. 查询批次：可包含 think 和一个或多个 ask_lawyer；不得包含 respond。
  B. 最终批次：只能包含一个 respond；不得包含 think、ask_lawyer 或任何其他action。
- 绝对禁止把 respond 和 think/ask_lawyer 放在同一个JSON数组中。
</batch_rules>
<result_requirements>
respond.result 必须包含：
1. status：found / no_result / uncertain
2. selected_anchor：最匹配元素全称；无可靠匹配时写无
3. rulebook_basis：来源和依据摘要
4. usable_interpretation：此元素如何承载原概念
5. must_avoid：必须避免的未核验数值、能力或误用
6. fallback：若status不是found，给architect的保守替代方向
7. blacklist_check：确认selected_anchor不在最近使用元素禁用列表中
</result_requirements>
<rules>
- 第一轮必须至少调用一次ask_lawyer；不得凭常识或记忆直接respond。
- 用户消息中的<recently_used_mythos_anchors>是硬性禁用列表；selected_anchor不得返回列表中的元素、别名或同源变体。
- 如果规则书裁定显示最匹配候选属于禁用列表，必须继续ask_lawyer寻找替代，或返回uncertain/no_result并给出非禁用fallback。
- ask_lawyer问题要具体，优先确认候选元素是否在规则书中存在、出处、核心机制和禁用边界。
- 不把lawyer原文无筛选地倾倒给architect；必须总结成可执行的翻译结论。
- 不得编造规则书不存在的正式名称、页码、数值或能力。
- 法术不允许任何变体，必须完全符合规则书描述。
- 若选择翻译为法术，必须在回复中提醒法术必须由一个具体的实体（人、神话生物等）施放。
</rules>`

const oneshotTranslatorToolCallExample = `[{"action":"ask_lawyer","question":"COC7规则书中哪个神话生物或机制最接近死者被古老力量束缚继续行动？请给出正式名称、出处和核心机制。"}]`

type oneshotTranslatorToolCall struct {
	Action   ToolCallType `json:"action"`
	Think    string       `json:"think,omitempty"`
	Question string       `json:"question,omitempty"`
	Result   string       `json:"result,omitempty"`
}

func runOneshotTranslatorAgent(ctx context.Context, room *scripterRoom, concept string, reason string) (string, error) {
	if room.architect.provider == nil {
		return "", fmt.Errorf("translator provider unavailable")
	}
	requestJSON, _ := json.Marshal(struct {
		Concept string `json:"concept"`
		Reason  string `json:"reason,omitempty"`
	}{Concept: concept, Reason: reason})

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(oneshotTranslatorSystemPrompt)},
		{Role: "user", Content: fmt.Sprintf(`<translate_anchor_request>%s</translate_anchor_request>
<recently_used_mythos_anchors>
%s
</recently_used_mythos_anchors>
以上最近使用过的元素为硬性禁用列表：selected_anchor不得返回这些元素、同名别名、简称或明显同源变体；若最匹配候选命中禁用列表，必须继续查询替代候选或返回uncertain/no_result。`,
			string(requestJSON), formatMythosBlacklist(room.mythosBlacklist))},
	}

	const maxRounds = 16
	askedLawyer := false
	for round := 1; round <= maxRounds; round++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		logStagePrompt(fmt.Sprintf("oneshot_translator_round_%d", round), msgs)
		raw, err := room.architect.provider.JsonChat(ctx, msgs)
		if err != nil {
			return "", err
		}
		log.Printf("[scripter:oneshot_translator] round=%d raw_len=%d raw=%s", round, len(raw), truncateRunes(raw, scripterRawLogLimit))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		calls, parseErr := parseOneshotTranslatorToolCalls(ctx, room.parser, raw)
		if parseErr != nil {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: JSON解析失败，必须重新输出合法JSON数组。"})
			continue
		}
		if len(calls) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须输出至少一个工具调用。"})
			continue
		}
		if oneshotTranslatorRespondMixed(calls) {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: respond必须单独一轮输出，不能和think、ask_lawyer或任何其他action混在同一个JSON数组中。"})
			continue
		}

		invalid := false
		var response string
		var toolResults []string
		for _, call := range calls {
			switch call.Action {
			case ToolThink:
				// silent
			case toolTranslatorAskLawyer:
				askedLawyer = true
				toolResults = append(toolResults, oneshotTranslatorAskLawyer(ctx, room, call))
			case toolTranslatorRespond:
				if !askedLawyer {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: respond前必须至少调用一次ask_lawyer。"})
					invalid = true
				} else if strings.TrimSpace(call.Result) == "" {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: respond的result字段不能为空。"})
					invalid = true
				} else {
					response = call.Result
				}
			default:
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(
					"SYSTEM REJECT: translator只允许think/ask_lawyer/respond，不允许%s。", call.Action)})
				invalid = true
			}
		}
		if invalid {
			continue
		}
		if len(toolResults) > 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: strings.Join(toolResults, "\n")})
			continue
		}
		if response != "" {
			if anchor := oneshotFindForbiddenAnchor(response, room.mythosBlacklist); anchor != "" {
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(
					"SYSTEM REJECT: selected_anchor命中了最近使用元素禁用列表：%s。必须继续ask_lawyer寻找替代候选，或返回uncertain/no_result并给出非禁用fallback。", anchor)})
				continue
			}
			return response, nil
		}
		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须调用ask_lawyer获取规则书裁定，或在已有裁定基础上调用respond返回结论。"})
	}
	return "", fmt.Errorf("translator未在%d轮内返回respond", maxRounds)
}

func oneshotTranslatorRespondMixed(calls []oneshotTranslatorToolCall) bool {
	n := 0
	for _, c := range calls {
		if c.Action == toolTranslatorRespond {
			n++
		}
	}
	return n > 0 && len(calls) != 1
}

func parseOneshotTranslatorToolCalls(ctx context.Context, parser agentHandle, raw string) ([]oneshotTranslatorToolCall, error) {
	stripped := strings.TrimSpace(llm.StripCodeFence(llm.JsonArryProtect(raw)))
	var calls []oneshotTranslatorToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil {
		return calls, nil
	} else if parser.provider != nil {
		fixed, repairErr := repairJSONWith(ctx, parser, stripped, err, oneshotTranslatorToolCallExample)
		if repairErr != nil {
			return nil, repairErr
		}
		fixed = strings.TrimSpace(llm.JsonArryProtect(fixed))
		if err2 := json.Unmarshal([]byte(fixed), &calls); err2 != nil {
			return nil, err2
		}
		return calls, nil
	} else {
		return nil, err
	}
}

func oneshotTranslatorAskLawyer(ctx context.Context, room *scripterRoom, call oneshotTranslatorToolCall) string {
	question := strings.TrimSpace(call.Question)
	if question == "" {
		return `<ask_lawyer_result error="question字段为空，无法查询规则书"/>`
	}
	log.Printf("[scripter:oneshot_translator] ask_lawyer question=%q", truncateRunes(question, 300))
	if room.lawyer.provider == nil {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="lawyer_unavailable">规则书专家不可用；不得声称已核验具体规则书元素。</ask_lawyer_result>`, question)
	}
	results := runLawyer(ctx, room.lawyer, question, rulebook.GlobalIndex)
	if len(results) == 0 {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="no_result">规则书专家未返回可用裁定；应换一个更具体的候选继续提问，或在最终结论中标记no_result/uncertain。</ask_lawyer_result>`, question)
	}
	return fmt.Sprintf(`<ask_lawyer_result question=%q status="found">%s</ask_lawyer_result>`,
		question, formatLawyerResults(results))
}

// ---------------------------------------------------------------------------
// Blacklist helpers
// ---------------------------------------------------------------------------

func oneshotFindForbiddenAnchor(response string, anchors []string) string {
	selected := oneshotExtractSelectedAnchor(response)
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

func oneshotExtractSelectedAnchor(response string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(response)), &obj); err == nil {
		if v, ok := obj["selected_anchor"].(string); ok {
			return strings.TrimSpace(v)
		}
	}
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "selected_anchor") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) != 2 {
				parts = strings.SplitN(trimmed, "：", 2)
			}
			if len(parts) == 2 {
				return strings.Trim(strings.TrimSpace(parts[1]), " `\"'，。；;")
			}
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

// ---------------------------------------------------------------------------
// Top-level generation functions
// ---------------------------------------------------------------------------

func generateOneshotDraft(ctx context.Context, room *scripterRoom, constraints ScripterConstraints) (ScenarioDraft, IronyCore, string, error) {
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)

	userMsg := fmt.Sprintf(
		`<request_json>%s</request_json>
<constraints>%s</constraints>
<recently_used_mythos_anchors>
%s
</recently_used_mythos_anchors>
<recent_npc_name_blacklist>%s</recent_npc_name_blacklist>
<scenario_title_blacklist>%s</scenario_title_blacklist>
<length_spec>
%s
</length_spec>
<difficulty_spec>
%s
</difficulty_spec>
请设计并生成完整的COC7剧本。`,
		string(reqJSON), string(constraintsJSON),
		formatMythosBlacklist(room.mythosBlacklist),
		formatNPCNameBlacklist(room.npcBlacklist),
		formatScenarioTitleBlacklist(room.titleSamples),
		lengthSpec(room.req.TargetLength)+"\n线索会被直接展示给玩家, 但类型前缀(真实/隐藏/误导)会被隐藏, 设计误导线索时需要注意。",
		difficultySpec(room.req.Difficulty),
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(oneshotSystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	logStagePrompt("oneshot", msgs)

	result, _, err := runOneshotArchitectLoop(ctx, room, msgs)
	if err != nil {
		return ScenarioDraft{}, IronyCore{}, "", err
	}

	if !knownDeltaOperatorID(result.DeltaOperator) && result.DeltaOperator != "" {
		log.Printf("[scripter:oneshot_novel_operator] operator=%q desc=%q — not in DeltaOperators",
			result.DeltaOperator, result.DeltaOperatorDesc)
	}
	log.Printf("[scripter:oneshot] done delta=%q anchor=%q scenes=%d npcs=%d clues=%d",
		result.DeltaOperator, truncateRunes(result.Content.MythosAnchor, 80),
		len(result.Content.Scenes), len(result.Content.NPCs), len(result.Content.Clues))
	logScripterArtifact("Oneshot Result", result)

	return result.toScenarioDraft(), result.toIronyCore(), strings.TrimSpace(result.RewardConcept), nil
}

func repairOneshotDraft(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, previous *ScenarioDraft, irony IronyCore, issues []string) (ScenarioDraft, error) {
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)
	prevJSON, _ := json.Marshal(previous)
	ironyJSON, _ := json.Marshal(irony)

	userMsg := fmt.Sprintf(
		`<request_json>%s</request_json>
<constraints>%s</constraints>
<irony_context>%s</irony_context>
<previous_draft>%s</previous_draft>
<must_fix>
%s
</must_fix>
请修复上述问题并重新调用translate_anchor验证神话元素，然后通过submit提交修复后的完整剧本JSON。不要只改措辞；不要更换已确认的神话元素（mythos_anchor）。`,
		string(reqJSON), string(constraintsJSON),
		string(ironyJSON), string(prevJSON),
		strings.Join(issues, "\n"),
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(oneshotSystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	logStagePrompt("oneshot_repair", msgs)

	result, _, err := runOneshotArchitectLoop(ctx, room, msgs)
	if err != nil {
		return ScenarioDraft{}, fmt.Errorf("oneshot repair failed: %w", err)
	}

	draft := result.toScenarioDraft()
	log.Printf("[scripter:oneshot_repair] done name=%q scenes=%d npcs=%d clues=%d",
		draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	return draft, nil
}

// ---------------------------------------------------------------------------
// Normalization
// ---------------------------------------------------------------------------

func normalizeOneshotDraft(draft *ScenarioDraft, req ScenarioCreationRequest, author string, constraints ScripterConstraints, irony IronyCore) {
	if draft == nil {
		return
	}
	author = strings.TrimSpace(author)
	if author == "" {
		author = defaultScripterAuthor
	}
	if strings.TrimSpace(draft.Name) == "" {
		reading := truncateRunes(strings.TrimSpace(irony.SurfaceReading), 12)
		if reading == "" {
			draft.Name = "未命名剧本"
		} else {
			draft.Name = "δ-调查：" + reading
		}
		log.Printf("[scripter:normalize] filled name=%q", draft.Name)
	}
	if strings.TrimSpace(req.Name) != "" && draft.Name != strings.TrimSpace(req.Name) {
		draft.Name = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(draft.Description) == "" {
		draft.Description = fmt.Sprintf("围绕「%s」展开的剧本：调查员进入一个由δ结构驱动的局势，表象与深层真相由一个可逆转的认知算子分隔。", irony.SurfaceReading)
		log.Printf("[scripter:normalize] filled description")
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
			"你是本场COC跑团的KP，职责是管理会自行推进的局势而不是执行线性故事。按派系时间线推进后果；按表面可见、主动询问、需要行动、不可直接获得四层管理信息；不要主动把调查员引向正确答案。【KP独有，勿向玩家直说】δ内部真相：%s。固定神话锚点：%s；具体数值按规则书裁定。",
			firstNonEmpty(irony.DeepTruth, "真相将通过调查逐步揭示"),
			firstNonEmpty(draft.Content.MythosAnchor, "按规则书已收录神话元素处理"),
		)
		log.Printf("[scripter:normalize] filled system_prompt")
	}
	if strings.TrimSpace(draft.Content.Setting) == "" {
		draft.Content.Setting = fmt.Sprintf(
			"%s的%s中，调查员面对一个已经开始运动的局势：%s。公开层面只看得到表象、地方压力和派系互相遮掩；无人干预时，各方会按自己的时间线继续行动。",
			constraints.Era, strings.Join(constraints.GeographyFlavor, " / "),
			firstNonEmpty(irony.SurfaceReading, "一个可被多种方式解读的局势已经开始"),
		)
		log.Printf("[scripter:normalize] filled setting")
	}
	if strings.TrimSpace(draft.Content.Intro) == "" {
		draft.Content.Intro = "你们进入局势。眼前可立即行动：前往最近的关键地点，询问公开目击者，或决定是否把已知异常告诉某个派系。"
		log.Printf("[scripter:normalize] filled intro")
	}
	if strings.TrimSpace(draft.Content.MapDescription) == "" {
		draft.Content.MapDescription = "【文字地图】各调查地点是剧本状态节点，不是顺序关卡：入口连接所有可调查地点；地点之间可往返；时间推进时，各地点状态可能因派系行动而改变。"
		log.Printf("[scripter:normalize] filled map_description")
	}
	if len(draft.Content.Scenes) == 0 {
		draft.Content.Scenes = []models.SceneData{{
			ID:          "location_1",
			Name:        "调查入口",
			Description: "可见：异常已经公开出现。可发现：主动调查可获得第一批事实。杠杆：公开或隐瞒信息会改变派系反应。风险：拖延会推进时间线。出口：所有相关地点。",
			Triggers:    []string{"available_from_start"},
		}}
		log.Printf("[scripter:normalize] generated default scene")
	}
	for i := range draft.Content.Scenes {
		if strings.TrimSpace(draft.Content.Scenes[i].ID) == "" {
			draft.Content.Scenes[i].ID = fmt.Sprintf("location_%d", i+1)
		}
		if strings.TrimSpace(draft.Content.Scenes[i].Name) == "" {
			draft.Content.Scenes[i].Name = fmt.Sprintf("地点%d", i+1)
		}
		if strings.TrimSpace(draft.Content.Scenes[i].Description) == "" {
			draft.Content.Scenes[i].Description = "可见：当前局势的表面信息。可发现：主动调查可获得的事实。杠杆：调查员行动会改变派系时间线。风险：拖延会让世界推进。出口：可前往相关地点。"
		}
		if len(draft.Content.Scenes[i].Triggers) == 0 {
			draft.Content.Scenes[i].Triggers = []string{"available_from_start"}
		}
	}
	if len(draft.Content.NPCs) == 0 {
		draft.Content.NPCs = []models.NPCData{{
			Name:        "关键NPC",
			Description: "公开身份：地方相关人员。真实议程：自保并观察局势。秘密：掌握部分真相但不会主动全盘托出。",
			Attitude:    "谨慎防备",
		}}
		log.Printf("[scripter:normalize] generated default npc")
	}
	for i := range draft.Content.NPCs {
		if strings.TrimSpace(draft.Content.NPCs[i].Name) == "" {
			draft.Content.NPCs[i].Name = fmt.Sprintf("关键NPC%d", i+1)
		}
		if strings.TrimSpace(draft.Content.NPCs[i].Description) == "" {
			draft.Content.NPCs[i].Description = "公开身份、所属派系、真实议程、秘密和可被调查员影响的杠杆。"
		}
		if strings.TrimSpace(draft.Content.NPCs[i].Attitude) == "" {
			draft.Content.NPCs[i].Attitude = "谨慎观察调查员，只有在压力或交换下才透露深层信息。"
		}
	}
	if len(draft.Content.Clues) == 0 {
		draft.Content.Clues = []string{
			"[真实]公开异常(调查入口): " + firstNonEmpty(irony.SurfaceReading, "一个无法普通解释的局势已经开始") + "；获取方式：到达现场并主动询问或检查。",
			"[误导]表象线索(初步调查): 支持错误推断的表象证据；表面合理但只能解释一部分。",
		}
		log.Printf("[scripter:normalize] generated default clues count=2")
	}
	for i, clue := range draft.Content.Clues {
		draft.Content.Clues[i] = normalizeClueString(clue)
	}
	// Extract [隐藏]神话本质 → MythosCore
	var filteredClues []string
	for _, clue := range draft.Content.Clues {
		if strings.Contains(clue, "神话本质") {
			if strings.TrimSpace(draft.Content.MythosCore) == "" {
				text := clue
				if strings.HasPrefix(text, "[") {
					if end := strings.Index(text, "]"); end != -1 {
						text = strings.TrimSpace(text[end+1:])
					}
				}
				draft.Content.MythosCore = text
				log.Printf("[scripter:normalize] extracted mythos_core=%q", truncateRunes(text, 200))
			}
		} else {
			filteredClues = append(filteredClues, clue)
		}
	}
	draft.Content.Clues = filteredClues
	if strings.TrimSpace(draft.Content.MythosCore) == "" && strings.TrimSpace(draft.Content.MythosAnchor) != "" {
		draft.Content.MythosCore = fmt.Sprintf("神话本质(核心发现): %s；到达终止节点并触发揭示后承担理智代价。", draft.Content.MythosAnchor)
		log.Printf("[scripter:normalize] synthesized mythos_core from anchor")
	}
	if strings.TrimSpace(draft.Content.WinCondition) == "" {
		draft.Content.WinCondition = "如果调查员让关键事实公开并改变至少一个派系时间线，则局势以较低代价固化，但神话锚点的余波仍保留。"
		log.Printf("[scripter:normalize] filled win_condition")
	}
	if strings.TrimSpace(draft.Content.LoseCondition) == "" {
		draft.Content.LoseCondition = "如果关键时间线终点到达且调查员没有改变任何派系行动，则局势进入新的稳定态，某人或某地不可挽回地改变。"
		log.Printf("[scripter:normalize] filled lose_condition")
	}
	if len(draft.Content.PartialWins) == 0 {
		draft.Content.PartialWins = []string{"如果调查员保护了个人或证据，但没有改变所有派系时间线，则余波继续存在。"}
		log.Printf("[scripter:normalize] filled partial_wins")
	}
}
