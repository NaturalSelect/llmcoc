// scripter_misdirection.go — Stage 2: MisdirectionFabric generation.
//
// This is where CoC mechanics enter the pipeline.  The IronyCore's δ-operator
// is translated into a mythic anchor, and the misdirection network
// (false lead, misdirector NPC, true trace, reveal trigger, retrospective key)
// is designed in parallel.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// ---------------------------------------------------------------------------
// Prompts (Stage 2)
// ---------------------------------------------------------------------------

const misdirectionSystemPrompt = `<role>COC7剧本误导与神话背景设计师</role>
<task>你收到了一个悬疑剧本的揭示结构（irony_core），其中包含：
- surface_reading：调查员最初会自然形成的错误推断（表层叙事）
- deep_truth：真实情况，即揭示真相后的实际关系
- false_delta：调查员最容易优先猜测的那种「翻转方式」（例如以为是身份骗局，实际上是因果倒置）
- shared_evidence：在不知道真相时，同时支持两种解读的歧义证据

工作步骤：
① 用 translate_anchor 将 deep_truth 的核心概念翻译为具体的COC7规则书元素，确定 mythos_anchor；可多次翻译直到找到合适元素，需要仔细考虑选择什么元素。
② 若误导设计中还涉及其他神话概念（如 false_lead 引用的神话实体），继续用 translate_anchor 翻译核验。
③ 所有翻译完成后，用 submit 提交完整的 MisdirectionFabric；reward_concept填写通关奖励的叙事概念（若有），实际机械数据由系统独立生成。
</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：内部推理（可选，无副作用）
  {"action":"think","think":"推理内容"}
- translate_anchor：将一个创意概念翻译为COC7规则书中最匹配的具体元素（实体/典籍/法术/诅咒物品）；可多次调用，用于①将deep_truth概念翻译为mythos_anchor ②将误导设计概念翻译为规则书元素；提交前必须至少调用一次
  {"action":"translate_anchor","concept":"概念描述（如「死者被古老力量束缚继续行动」「能腐蚀心智的古籍」「深海中的智慧存在」）","reason":"这个概念在本剧本中承担什么角色（翻译deep_truth / 翻译误导元素）"}
- submit：提交完整的MisdirectionFabric；只有在translate_anchor确认元素可用后才调用
  {"action":"submit","fabric":{...完整的MisdirectionFabric JSON对象...}}
</tools>
<schema>{
  "false_lead": "一条有说服力的误导性线索，推动调查员相信 surface_reading 的错误推断；【关键要求】在 deep_truth 揭示后，这条线索必须仍能得到合理解释，不得与真相完全矛盾",
  "misdirector_npc": "一个NPC，其身份或日常行为自然支持错误推断——不一定是主动欺骗者；说明其行为为什么会让调查员误判",
  "true_trace": "一条表面支持错误推断、但在 deep_truth 框架下有更精确解释的歧义证据——两种解读都说得通",
  "reveal_trigger": "触发调查员从错误推断转向 deep_truth 的具体事件或发现",
  "retrospective_key": "回头看时，这个细节从剧本开头就在指向 deep_truth，但当时被忽视",
  "mythos_anchor": "translate_anchor 翻译并确认可用的COC7神话元素全称（具体实体名/典籍名/机制名）；必须与翻译结果一致",
  "rules_notes": ["规则书出处说明，或对不确定元素的保守处理说明"],
  "factions": [{"name":"派系名","goal":"目标","current_state":"无人干预时正在做什么（具体行动，非等待状态）","timeline":[{"node":"第N节点","trigger":"世界自行推进到此节点的条件","intervention_pivot":"调查员在此节点可执行的具体干预动作"}],"npcs":[{"name":"NPC名","public_identity":"公开身份","agenda":"独立议程","secret":"秘密","attitude":"初始态度","stats_note":"属性注记"}]}],
  "ending_signals": ["如果[条件]，则[谁的处境如何变化]，[什么不可挽回地改变]"],
  "reward_concept": "通关奖励的叙事概念（如「食尸鬼语言研究手稿，能帮助理解神话存在」）；与mythos_anchor有叙事关联；若无合适神话物品则留空字符串"
}</schema>
<rules>
- 必须先调用 translate_anchor 获得规则书反馈，再调用 submit；不得在未翻译的情况下直接submit。
- false_lead必须满足后验兼容：在 deep_truth 揭示后，这条线索必须仍有合理解释，不能是只有在错误推断成立时才说得通的假线索。
- misdirector_npc应有内在动机支持错误推断，不依赖"他是坏人"这种纯功能性设定。
- true_trace必须是歧义证据：表面支持错误推断，但只需一个额外上下文就能转向 deep_truth。
- mythos_anchor一旦选定后续阶段不得更换；不确定时在 rules_notes 显式说明。
- 派系必须有非空 current_state（无人干预时正在做什么），timeline 节点必须有具体 intervention_pivot。
- 如果收到 qa_rejection，必须修复 false_lead 的后验兼容性或派系自主性问题；不要只改措辞；可再次search_anchor后重新submit。
- mythos_anchor 必须来自规则书；若translate_anchor返回no_result，可改用其他概念描述重新翻译，或转向人类法师、诅咒物品、古老地点；仍无合适选项时才可创造新元素，但必须在rules_notes详细说明。
- reward_concept描述本剧本通关奖励的类型与叙事意义；与mythos_anchor有机关联；只需描述物品概念（如「与食尸鬼有关的古籍」），机械数据（SAN代价/技能收益）由独立agent生成；若无合适神话物品可留空字符串。
- 用户消息中注入的 stage2_rule_context 仅含规则书常量参考（生物/典籍/法术列表）；具体元素的详细裁定通过 translate_anchor 按需翻译——translate_anchor 结果中的"必须避免"和"不要"是强制性禁令，不得以任何理由绕过。
- 仔细思考, 不要急于提交；设计一个有趣的误导网络，避免过于平庸或过于牵强的设计；如果概念本身很弱，考虑在translate_anchor阶段调整概念描述来寻找更有趣的元素。
</rules>`

const misdirectionQASystemPrompt = `<role>COC7沙盒误导设计QA</role>
<task>审核误导设计是否满足：false_lead后验兼容（在deep_truth下仍可解释）、misdirector_npc动机合理、派系自主行动、干预枢纽具体可执行。不审核规则书准确性，不审核rewards机械数据。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：内部推理（可选，无副作用）
  {"action":"think","think":"推理内容"}
- response：最终审核结论。
  {"action":"response","pass":true,"reason":"审核理由","reject_reasons":[],"suggested_fix":"给architect的具体修改方向"}
</tools>
<audit_rules>
审核五点，任意一点不满足则pass=false，reject_reasons必须逐条列出：
1. false_lead后验兼容：false_lead在irony.deep_truth揭示后必须仍有合理解释。如果这条线索在真实情况下完全无法解释（只有在错误推断成立时才说得通），pass=false。
2. misdirector_npc动机合理：misdirector_npc必须有内在动机支持错误推断（被动的存在或行为即可），而不是被写成"专门误导调查员的功能性NPC"。
3. 派系自主性：每个派系必须有non-empty current_state，且timeline节点描述无人干预时的世界运动，而不是"等待调查员触发X"。
4. 干预枢纽有效性：每个timeline节点的intervention_pivot必须描述一个具体的可执行动作，而不是"调查员可以干预"这种空话。
5. reward_concept合理：若非空，必须描述与本剧本mythos_anchor有叙事联系的神话物品类型（tome或artifact）；不审核机械数据（由独立agent生成）。
不审核：规则书准确性、神话元素细节、NPC属性数值。
</audit_rules>`

const misdirectionExample = `{"false_lead":"墓地管理员证实有人定期入侵并窃取书籍，物品清单精确，显示这是有目的的盗窃行为","misdirector_npc":"守墓人，出于对秩序的维护本能，将任何进入禁区者描述为入侵者和盗贼","true_trace":"被带走的书籍上均有一个褪色的书写者姓名花押，与失踪者Douglas Kimball姓名首字母完全吻合","reveal_trigger":"调查员发现Douglas的旧藏书记录，核对后确认图书馆现有馆藏与其遗产清单高度重合","retrospective_key":"Douglas对每本书的位置了如指掌，从未触碰其他书籍——入侵者对馆内布局的熟悉程度超越任何盗贼","mythos_anchor":"食尸鬼（Ghoul）：COC7规则书第XX页；已核验为死者变形后的非人存在类型","rules_notes":["食尸鬼条目已核验，具体属性数值KP按规则书裁定"],"factions":[{"name":"旧知识的守护者","goal":"取回自己的书籍，维持与旧有身份的最后联结","current_state":"每夜进入图书馆取回一本书，行动越来越不谨慎","timeline":[{"node":"第0天：继续取书","trigger":"调查员进入调查","intervention_pivot":"直接与食尸鬼沟通——说出Douglas生前的名字或展示其遗物"},{"node":"第3天：被迫完全撤退","trigger":"调查员公开事件引来大批人员","intervention_pivot":"提前与其达成某种协议"}],"npcs":[{"name":"Douglas Kimball","public_identity":"已死亡的图书馆员（表面身份）","agenda":"取回自己的藏书，维持人性最后的碎片","secret":"已变为食尸鬼，但保留了对书籍的执念","attitude":"警惕、回避，若被识别则可能对话","stats_note":"食尸鬼属性按规则书；保留部分人类记忆"}]}],"ending_signals":["如果调查员让Douglas重获自己的藏书，则他彻底退隐墓地，书籍之谜以一种悲哀而非恐怖的方式收场"],"reward_concept":"Douglas生前的食尸鬼语言研究手稿，能帮助理解食尸鬼的思维与神话本质"}`

const misdirectionQAToolCallExample = `[{"action":"response","pass":true,"reason":"false_lead在deep_truth框架下有合理解释（守墓人的描述并不虚假），misdirector_npc有内在动机，派系有自主current_state，干预枢纽具体可执行，reward_concept与mythos_anchor有叙事关联。","reject_reasons":[],"suggested_fix":""}]`

// ---------------------------------------------------------------------------
// Stage 2 architect tool-call loop
// ---------------------------------------------------------------------------

// local tool call types for Stage 2 architect only
const (
	toolTranslateAnchor    ToolCallType = "translate_anchor"
	toolMisdirectionSubmit ToolCallType = "submit"
)

type misdirectionArchitectToolCall struct {
	Action  ToolCallType        `json:"action"`
	Think   string              `json:"think,omitempty"`
	Concept string              `json:"concept,omitempty"` // translate_anchor: 概念描述
	Reason  string              `json:"reason,omitempty"`  // translate_anchor: 角色说明
	Fabric  *MisdirectionFabric `json:"fabric,omitempty"`  // submit: 完整输出
}

// misdirectionArchitectToolCallExample is used by the parser for JSON repair.
const misdirectionArchitectToolCallExample = `[{"action":"think","think":"translate_anchor已确认食尸鬼可用，开始设计误导网络"},{"action":"submit","fabric":{"false_lead":"墓地管理员证实有人定期入侵并窃取书籍","misdirector_npc":"守墓人将任何进入禁区者描述为入侵者","true_trace":"被带走的书籍上均有褪色的书写者姓名花押","reveal_trigger":"调查员发现旧藏书记录与遗产清单高度重合","retrospective_key":"入侵者从未触碰无关书籍","mythos_anchor":"食尸鬼（Ghoul）","rules_notes":["食尸鬼条目已核验"],"factions":[{"name":"旧知识的守护者","goal":"取回自己的书籍","current_state":"每夜进入图书馆取回一本书","timeline":[{"node":"第0天","trigger":"调查员进入调查","intervention_pivot":"说出Douglas生前的名字或展示其遗物"}],"npcs":[{"name":"Douglas Kimball","public_identity":"已死亡的图书馆员","agenda":"取回藏书","secret":"已变为食尸鬼","attitude":"警惕、回避","stats_note":"食尸鬼属性按规则书"}]}],"ending_signals":["如果调查员让Douglas重获藏书，则他彻底退隐墓地"],"reward_concept":"Douglas的食尸鬼语言研究手稿，能帮助调查员理解食尸鬼本质"}}]`

// runMisdirectionArchitectLoop runs the Stage 2 architect in a tool-call loop.
//
// Expected flow:
//  1. LLM calls search_anchor → system runs rulebook lookup → result injected as user message → loop continues.
//  2. LLM calls submit (after seeing search results) → fabric returned.
//
// If search_anchor and submit appear in the same round, the search results are
// appended and submit is ignored; the LLM must re-evaluate before submitting.
func runMisdirectionArchitectLoop(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage, tag string) (MisdirectionFabric, []llm.ChatMessage, error) {
	if room.architect.provider == nil {
		return MisdirectionFabric{}, msgs, fmt.Errorf("%s architect provider unavailable", tag)
	}
	const maxRounds = 30
	for round := 1; round <= maxRounds; round++ {
		if ctx.Err() != nil {
			return MisdirectionFabric{}, msgs, ctx.Err()
		}
		logStagePrompt(fmt.Sprintf("%s_loop_round_%d", tag, round), msgs)
		raw, err := room.architect.provider.JsonChat(ctx, msgs)
		if err != nil {
			return MisdirectionFabric{}, msgs, err
		}
		log.Printf("[scripter:%s_loop] round=%d raw_len=%d raw=%s", tag, round, len(raw), truncateRunes(raw, scripterRawLogLimit))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		calls, parseErr := parseMisdirectionArchitectToolCalls(ctx, room.parser, raw)
		if parseErr != nil {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: JSON解析失败，必须重新输出合法JSON数组。"})
			continue
		}
		if len(calls) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须输出至少一个工具调用。"})
			continue
		}

		invalid := false
		hasSearchAnchor := false
		var submitFabric *MisdirectionFabric
		var toolResults []string

		for _, call := range calls {
			switch call.Action {
			case ToolThink:
				// silent
			case toolTranslateAnchor:
				hasSearchAnchor = true
				toolResults = append(toolResults, executeMisdirectionTranslateAnchor(ctx, room, call))
			case toolMisdirectionSubmit:
				if call.Fabric == nil {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: submit的fabric字段不能为空。"})
					invalid = true
				} else {
					submitFabric = call.Fabric
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
		// Append all search results as one user message.
		if len(toolResults) > 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: strings.Join(toolResults, "\n")})
		}
		// If search_anchor was called this round, don't process submit yet —
		// the LLM needs to see the results before deciding.
		if hasSearchAnchor {
			continue
		}
		if submitFabric != nil {
			return *submitFabric, msgs, nil
		}
		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须先调用translate_anchor翻译神话元素，再用submit提交。"})
	}
	return MisdirectionFabric{}, msgs, fmt.Errorf("%s architect 未在%d轮内提交结果", tag, maxRounds)
}

func parseMisdirectionArchitectToolCalls(ctx context.Context, parser agentHandle, raw string) ([]misdirectionArchitectToolCall, error) {
	stripped := strings.TrimSpace(llm.StripCodeFence(llm.JsonArryProtect(raw)))
	var calls []misdirectionArchitectToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil {
		return calls, nil
	} else if parser.provider != nil {
		fixed, repairErr := repairJSONWith(ctx, parser, stripped, err, misdirectionArchitectToolCallExample)
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

const misdirectionTranslatorSystemPrompt = `<role>COC7规则书概念翻译专家</role>
<task>你收到一个创意概念，需要把它翻译为COC7规则书中最匹配、可在剧本中使用的具体元素（实体/典籍/法术/诅咒物品/机制）。你不是规则书检索器；你必须通过 ask_lawyer 向规则书专家提问，依据裁定自行综合，最后用 respond 返回给上层architect的翻译结论。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：内部推理（可选，无副作用）
  {"action":"think","think":"推理内容"}
- ask_lawyer：向COC7规则书专家提出一个具体规则书问题；可多次调用，每次聚焦不同候选、名称、能力、典籍/法术细节或禁用边界
  {"action":"ask_lawyer","question":"具体规则书问题"}
- respond：返回最终翻译结论并退出；必须在至少一次ask_lawyer之后调用；必须单独一轮输出
  {"action":"respond","result":"结构化翻译结论"}
</tools>
<batch_rules>
- 每轮只能是以下两种批次之一：
  A. 查询批次：可包含 think 和一个或多个 ask_lawyer；不得包含 respond。
  B. 最终批次：只能包含一个 respond；不得包含 think、ask_lawyer 或任何其他action。
- 绝对禁止把 respond 和 think/ask_lawyer 放在同一个JSON数组中。错误示例：[think, ask_lawyer, respond]。
- 如果还需要向规则书专家提问，本轮只输出查询批次，等待工具结果后下一轮再单独输出 respond。
</batch_rules>
<result_requirements>
respond.result必须包含：
1. status：found / no_result / uncertain
2. selected_anchor：最匹配元素全称；无可靠匹配时写无
3. rulebook_basis：来自ask_lawyer裁定的来源和依据摘要
4. usable_interpretation：此元素如何承载原概念
5. must_avoid：必须避免的未核验数值、能力、行为或误用
6. fallback：若status不是found，给architect的保守替代方向
7. blacklist_check：确认selected_anchor不在最近使用元素禁用列表中；若接近禁用元素，说明为什么已避开
</result_requirements>
<rules>
- 第一轮必须至少调用一次ask_lawyer；不得凭常识或记忆直接respond。
- 用户消息中的<recently_used_mythos_anchors>是硬性禁用列表；respond.result的selected_anchor绝对不得返回列表中的元素，也不得返回同一元素的中文名、英文名、简称、带括号形式或明显同源变体。
- 如果规则书裁定显示最匹配候选属于最近使用元素禁用列表，不得选择它；必须继续ask_lawyer寻找替代候选，或返回status=uncertain/no_result并在fallback中提出非禁用方向。
- ask_lawyer问题要具体，优先确认候选元素是否在规则书中存在、出处、核心机制和禁用边界；提问时应主动说明需要避开最近使用过的元素。
- 如果一次裁定不足以确定匹配，可继续ask_lawyer比较其他候选或追问细节。
- 不把lawyer原文无筛选地倾倒给architect；必须总结成可执行的翻译结论。
- 不得编造规则书不存在的正式名称、页码、数值或能力。
- 你需要仔细推理和思考，在规则书中寻找强有力的支撑和最匹配的元素，而不是急于给出一个模糊的结论；如果概念本身很弱，考虑调整概念描述来寻找更有趣的元素；如果确实没有合适元素，也要尽量给出一个保守的替代方向，而不是直接respond no_result。
- 还需要考虑什么情况下能引入这个元素， 例如： 邪教徒呼唤外神的仪式，古老地点的诅咒，调查员与某个神话存在的直接互动等；如果这个元素只能在非常特定的情境下使用，必须在must_avoid里明确指出，避免architect误用。
</rules>`

const (
	toolTranslatorAskLawyer ToolCallType = "ask_lawyer"
	toolTranslatorRespond   ToolCallType = "respond"
)

type misdirectionTranslatorToolCall struct {
	Action   ToolCallType `json:"action"`
	Think    string       `json:"think,omitempty"`
	Question string       `json:"question,omitempty"`
	Result   string       `json:"result,omitempty"`
}

const misdirectionTranslatorToolCallExample = `[{"action":"ask_lawyer","question":"COC7规则书中哪个神话生物或机制最接近死者被古老力量束缚继续行动？请给出正式名称、出处、核心机制和必须避免的未核验内容。"}]`

func executeMisdirectionTranslateAnchor(ctx context.Context, room *scripterRoom, call misdirectionArchitectToolCall) string {
	concept := strings.TrimSpace(call.Concept)
	if concept == "" {
		return `<translate_anchor_result error="concept字段为空，无法翻译"/>`
	}
	reason := strings.TrimSpace(call.Reason)
	log.Printf("[scripter:translate_anchor] concept=%q reason=%q", truncateRunes(concept, 200), truncateRunes(reason, 200))
	result, err := runMisdirectionTranslatorAgent(ctx, room, concept, reason)
	if err != nil {
		log.Printf("[scripter:translate_anchor] translator error concept=%q err=%v", truncateRunes(concept, 200), err)
		return fmt.Sprintf(
			`<translate_anchor_result concept=%q status="translator_error">%s</translate_anchor_result>`,
			concept, err.Error())
	}
	result = strings.TrimSpace(result)
	if result == "" {
		return fmt.Sprintf(
			`<translate_anchor_result concept=%q status="no_result">translator未返回可用结论；可尝试调整概念描述重新翻译，或转向人类法师、诅咒物品、古老地点等方向。</translate_anchor_result>`,
			concept)
	}
	return fmt.Sprintf(`<translate_anchor_result concept=%q status="translated">%s</translate_anchor_result>`, concept, result)
}

func runMisdirectionTranslatorAgent(ctx context.Context, room *scripterRoom, concept string, reason string) (string, error) {
	if room.architect.provider == nil {
		return "", fmt.Errorf("translator provider unavailable")
	}
	requestJSON, _ := json.Marshal(struct {
		Concept string `json:"concept"`
		Reason  string `json:"reason,omitempty"`
	}{
		Concept: concept,
		Reason:  reason,
	})
	recentAnchors := formatMythosBlacklist(room.mythosBlacklist)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(misdirectionTranslatorSystemPrompt)},
		{Role: "user", Content: fmt.Sprintf(`<translate_anchor_request>%s</translate_anchor_request>
<recently_used_mythos_anchors>
%s
</recently_used_mythos_anchors>
以上最近使用过的元素为硬性禁用列表：selected_anchor 不得返回这些元素、同名别名、简称、括号中英文互译形式或明显同源变体；如最匹配候选命中禁用列表，必须继续查询替代候选或返回uncertain/no_result。`, string(requestJSON), recentAnchors)},
	}

	const maxRounds = 16
	askedLawyer := false
	for round := 1; round <= maxRounds; round++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		logStagePrompt(fmt.Sprintf("misdirection_translate_anchor_round_%d", round), msgs)
		raw, err := room.architect.provider.JsonChat(ctx, msgs)
		if err != nil {
			return "", err
		}
		log.Printf("[scripter:translate_anchor_translator] round=%d raw_len=%d raw=%s", round, len(raw), truncateRunes(raw, scripterRawLogLimit))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		calls, parseErr := parseMisdirectionTranslatorToolCalls(ctx, room.parser, raw)
		if parseErr != nil {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: JSON解析失败，必须重新输出合法JSON数组。"})
			continue
		}
		if len(calls) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须输出至少一个工具调用。"})
			continue
		}

		if translatorRespondMixed(calls) {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: respond必须单独一轮输出，不能和think、ask_lawyer或任何其他action混在同一个JSON数组中。若还需查询，本轮只输出ask_lawyer；若已有足够信息，下一轮只输出一个respond。"})
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
				toolResults = append(toolResults, executeMisdirectionTranslatorAskLawyer(ctx, room, call))
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
			if anchor := findForbiddenSelectedAnchor(response, room.mythosBlacklist); anchor != "" {
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf("SYSTEM REJECT: selected_anchor命中了最近使用元素禁用列表：%s。selected_anchor不得返回该元素、别名或同源变体；请继续ask_lawyer寻找替代候选，或返回uncertain/no_result并给出非禁用fallback。", anchor)})
				continue
			}
			return response, nil
		}
		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须调用ask_lawyer获取规则书裁定，或在已有裁定基础上调用respond返回结论。"})
	}
	return "", fmt.Errorf("translator未在%d轮内返回respond", maxRounds)
}

func translatorRespondMixed(calls []misdirectionTranslatorToolCall) bool {
	respondCount := 0
	for _, call := range calls {
		if call.Action == toolTranslatorRespond {
			respondCount++
		}
	}
	return respondCount > 0 && len(calls) != 1
}

func findForbiddenSelectedAnchor(response string, anchors []string) string {
	selectedAnchor := extractTranslatorSelectedAnchor(response)
	if selectedAnchor == "" || selectedAnchor == "无" {
		return ""
	}
	normalizedSelected := normalizeMythosAnchorForCompare(selectedAnchor)
	if normalizedSelected == "" {
		return ""
	}
	for _, anchor := range anchors {
		if normalizedAnchor := normalizeMythosAnchorForCompare(anchor); normalizedAnchor != "" && strings.Contains(normalizedSelected, normalizedAnchor) {
			return anchor
		}
	}
	return ""
}

func extractTranslatorSelectedAnchor(response string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(response)), &obj); err == nil {
		if selected, ok := obj["selected_anchor"].(string); ok {
			return strings.TrimSpace(selected)
		}
	}
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "selected_anchor") || strings.HasPrefix(trimmed, "selected_anchor：") || strings.HasPrefix(trimmed, "selected_anchor:") {
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

func normalizeMythosAnchorForCompare(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(
		" ", "", "\t", "", "\n", "", "\r", "",
		"（", "", "）", "", "(", "", ")", "",
		"「", "", "」", "", "『", "", "』", "",
		"《", "", "》", "", "[", "", "]", "",
		"：", "", ":", "", "，", "", ",", "",
		"。", "", ".", "", "、", "", "/", "",
		"-", "", "_", "",
	)
	return replacer.Replace(s)
}

func parseMisdirectionTranslatorToolCalls(ctx context.Context, parser agentHandle, raw string) ([]misdirectionTranslatorToolCall, error) {
	stripped := strings.TrimSpace(llm.StripCodeFence(llm.JsonArryProtect(raw)))
	var calls []misdirectionTranslatorToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil {
		return calls, nil
	} else if parser.provider != nil {
		fixed, repairErr := repairJSONWith(ctx, parser, stripped, err, misdirectionTranslatorToolCallExample)
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

func executeMisdirectionTranslatorAskLawyer(ctx context.Context, room *scripterRoom, call misdirectionTranslatorToolCall) string {
	question := strings.TrimSpace(call.Question)
	if question == "" {
		return `<ask_lawyer_result error="question字段为空，无法查询规则书"/>`
	}
	log.Printf("[scripter:translate_anchor_translator] ask_lawyer question=%q", truncateRunes(question, 300))
	if room.lawyer.provider == nil {
		return fmt.Sprintf(
			`<ask_lawyer_result question=%q status="lawyer_unavailable">规则书专家不可用；不得声称已核验具体规则书元素。</ask_lawyer_result>`,
			question)
	}
	results := runLawyer(ctx, room.lawyer, question, rulebook.GlobalIndex)
	if len(results) == 0 {
		return fmt.Sprintf(
			`<ask_lawyer_result question=%q status="no_result">规则书专家未返回可用裁定；应换一个更具体的候选继续提问，或在最终结论中标记no_result/uncertain。</ask_lawyer_result>`,
			question)
	}
	return fmt.Sprintf(`<ask_lawyer_result question=%q status="found">%s</ask_lawyer_result>`,
		question, formatLawyerResults(results))
}

// ---------------------------------------------------------------------------
// Session (Stage 2)
// ---------------------------------------------------------------------------

type misdirectionSession struct {
	room          *scripterRoom
	constraints   ScripterConstraints
	irony         IronyCore
	ruleCtx       string
	conservative  bool
	architectMsgs []llm.ChatMessage
	qaMsgs        []llm.ChatMessage
}

func newMisdirectionSession(room *scripterRoom, constraints ScripterConstraints, irony IronyCore, ruleCtx string, conservative bool) *misdirectionSession {
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)
	ironyJSON, _ := json.Marshal(irony)

	architectPrompt := fmt.Sprintf(
		`<request_json>%s</request_json>
<constraints>%s</constraints>
<irony_core>%s</irony_core>
<difficulty_spec>
%s
</difficulty_spec>
<stage2_rule_context conservative="%v">
%s
</stage2_rule_context>
<recent_npc_name_blacklist>%s</recent_npc_name_blacklist>
<recently_used_mythos_anchors>%s</recently_used_mythos_anchors>
请生成第1版MisdirectionFabric。`,
		string(reqJSON), string(constraintsJSON), string(ironyJSON),
		difficultySpec(room.req.Difficulty), conservative, ruleCtx,
		formatNPCNameBlacklist(room.npcBlacklist),
		formatMythosBlacklist(room.mythosBlacklist))

	qaPrompt := fmt.Sprintf(
		`<irony_core>%s</irony_core>
你是持续运行的MisdirectionFabric QA会话。每次收到<misdirection_candidate>后，通过think/response工具调用审核它。`,
		string(ironyJSON))

	return &misdirectionSession{
		room:         room,
		constraints:  constraints,
		irony:        irony,
		ruleCtx:      ruleCtx,
		conservative: conservative,
		architectMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.architect.systemPrompt(misdirectionSystemPrompt)},
			{Role: "user", Content: architectPrompt},
		},
		qaMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.qa.systemPrompt(misdirectionQASystemPrompt)},
			{Role: "user", Content: qaPrompt},
		},
	}
}

func (s *misdirectionSession) generate(ctx context.Context, attempt int) (MisdirectionFabric, error) {
	logStagePrompt(fmt.Sprintf("misdirection_attempt_%d", attempt), s.architectMsgs)
	fabric, msgs, err := runMisdirectionArchitectLoop(ctx, s.room, s.architectMsgs, "misdirection")
	if err != nil {
		return MisdirectionFabric{}, err
	}
	s.architectMsgs = msgs
	fabric = normalizeMisdirectionFabric(fabric, s.irony, s.conservative)
	log.Printf("[scripter:misdirection] attempt=%d anchor=%q factions=%d",
		attempt, truncateRunes(fabric.MythosAnchor, 200), len(fabric.Factions))
	return fabric, nil
}

func (s *misdirectionSession) review(ctx context.Context, attempt int, fabric MisdirectionFabric) (SandboxQA, error) {
	fabricJSON, _ := json.Marshal(fabric)
	s.qaMsgs = append(s.qaMsgs, llm.ChatMessage{
		Role: "user",
		Content: fmt.Sprintf(`<misdirection_candidate attempt="%d">%s</misdirection_candidate>
请审核这个候选。`, attempt, string(fabricJSON)),
	})
	qa, msgs, err := runSandboxQALoop(ctx, s.room, s.qaMsgs, misdirectionQAToolCallExample, "misdirection")
	s.qaMsgs = msgs
	return qa, err
}

func (s *misdirectionSession) feedRejection(attempt int, fabric MisdirectionFabric, qa SandboxQA) {
	fabricJSON, _ := json.Marshal(fabric)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{
		Role: "user",
		Content: fmt.Sprintf(`<qa_rejection attempt="%d">
<rejected_misdirection>%s</rejected_misdirection>
<must_fix>
%s
</must_fix>
</qa_rejection>
请基于同一个创作上下文重新设计MisdirectionFabric：逐条解决must_fix列出的问题；不要只改措辞；可再次translate_anchor翻译神话元素，最终通过submit提交新版本。`,

			attempt, string(fabricJSON), formatSandboxMustFix(qa)),
	})
}

// ---------------------------------------------------------------------------
// Top-level Stage 2 entry point
// ---------------------------------------------------------------------------

func generateMisdirectionWithQA(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, irony IronyCore) (MisdirectionFabric, error) {
	ruleCtx, conservative := buildStage2RuleContextFromIrony(ctx, irony)
	session := newMisdirectionSession(room, constraints, irony, ruleCtx, conservative)
	const maxAttempts = 20
	log.Printf("[scripter:misdirection] start delta=%q conservative=%v maxAttempts=%d",
		irony.DeltaOperator, conservative, maxAttempts)
	var lastQA *SandboxQA
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			log.Printf("[scripter:misdirection] context cancelled at attempt=%d: %v", attempt, ctx.Err())
			return MisdirectionFabric{}, ctx.Err()
		}
		fabric, err := session.generate(ctx, attempt)
		if err != nil {
			return MisdirectionFabric{}, err
		}
		qa, err := session.review(ctx, attempt, fabric)
		if err != nil {
			return MisdirectionFabric{}, err
		}
		lastQA = &qa
		log.Printf("[scripter:misdirection_qa] attempt=%d pass=%v reason=%q rejects=%q",
			attempt, qa.Pass, truncateRunes(qa.Reason, 400), strings.Join(qa.RejectReasons, " | "))
		logScripterArtifact(fmt.Sprintf("Stage 2 MisdirectionFabric QA Attempt %d", attempt), qa)
		if qa.Pass {
			logScripterArtifact("Stage 2 MisdirectionFabric", fabric)
			return fabric, nil
		}
		log.Printf("[scripter:misdirection] feedRejection attempt=%d rejects=%d %q",
			attempt, len(qa.RejectReasons), strings.Join(qa.RejectReasons, " | "))
		session.feedRejection(attempt, fabric, qa)
	}
	return MisdirectionFabric{}, fmt.Errorf("MisdirectionFabric QA 连续拒绝 %d 次，拒绝原因=%v",
		maxAttempts, sandboxQARejectReasons(lastQA))
}

// ---------------------------------------------------------------------------
// Rulebook context helper (adapted from old buildStage2RuleContext)
// ---------------------------------------------------------------------------

// buildStage2RuleContextFromIrony returns a static constants reference for Stage 2.
// Dynamic rule queries are handled by the search_anchor tool (isolated lawyer context)
// so no lawyer call is made here — architect context stays clean.
func buildStage2RuleContextFromIrony(_ context.Context, _ IronyCore) (string, bool) {
	var sb strings.Builder
	sb.WriteString("【规则书常量摘要，仅供Stage2参考；具体元素详情通过search_anchor按需查询】\n")
	for _, constant := range []string{"mythos_creatures", "monsters", "great_old_ones_and_gods", "books", "spells"} {
		text := strings.TrimSpace(rulebook.ReadConstant(constant))
		if text == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n[%s]\n%s\n", constant, truncateRunes(text, 1200)))
	}
	return truncateRunes(sb.String(), 9000), false
}

// ---------------------------------------------------------------------------
// Normalization
// ---------------------------------------------------------------------------

func normalizeMisdirectionFabric(fabric MisdirectionFabric, irony IronyCore, conservative bool) MisdirectionFabric {
	fabric.MythosAnchor = strings.TrimSpace(fabric.MythosAnchor)
	if fabric.MythosAnchor == "" {
		fabric.MythosAnchor = "保守锚定：" + irony.DeepTruth
		log.Printf("[scripter:misdirection_normalize] MythosAnchor was empty, set from deep_truth")
	}
	if conservative && !containsUncertaintyNote(fabric.RulesNotes) {
		fabric.RulesNotes = append(fabric.RulesNotes,
			"规则核验上下文不完整；未确认元素按保守神话锤点处理，具体数值由KP按规则书裁定。")
		log.Printf("[scripter:misdirection_normalize] added conservative rules_note")
	}
	if strings.TrimSpace(fabric.FalseLead) == "" {
		fabric.FalseLead = irony.SharedEvidence + "；表面解读指向" + irony.FalseDelta
		log.Printf("[scripter:misdirection_normalize] FalseLead was empty, filled from shared_evidence")
	}
	if strings.TrimSpace(fabric.TrueTrace) == "" {
		fabric.TrueTrace = irony.SharedEvidence + "；深入后可发现另一层解读"
		log.Printf("[scripter:misdirection_normalize] TrueTrace was empty, filled from shared_evidence")
	}
	if len(fabric.Factions) == 0 {
		fabric.Factions = []FactionPlan{{
			Name:         "旧决定的守护者",
			Goal:         "阻止外人理解异常与人类悲剧之间的关系",
			CurrentState: "正在销毁或重写能暴露旧决定的记录",
			Timeline: []TimelineNode{{
				Node:              "第0天：维持表面秩序",
				Trigger:           "调查员开始询问异常来源",
				InterventionPivot: "公开关键记录会迫使其改变行动",
			}},
			NPCs: []FactionNPC{{
				Name: "周砚", PublicIdentity: "地方办事员",
				Agenda: "维持旧决定不被公开", Secret: "知道异常的真实来源但选择隐瞒",
				Attitude: "礼貌回避", StatsNote: "普通人类属性15-70",
			}},
		}}
		log.Printf("[scripter:misdirection_normalize] Factions was empty, generated default count=1")
	}
	if len(fabric.EndingSignals) == 0 {
		fabric.EndingSignals = []string{
			"如果调查员让关键派系承认旧决定，则异常的社会遮掩被打破，但神话锤点会寻找新的承载者。",
		}
		log.Printf("[scripter:misdirection_normalize] EndingSignals was empty, generated default count=1")
	}
	return fabric
}
