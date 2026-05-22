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

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// ---------------------------------------------------------------------------
// Prompts (Stage 2)
// ---------------------------------------------------------------------------

const misdirectionSystemPrompt = `<role>COC7沙盒神话翻译师与误导架构师</role>
<task>完成两件事：
①将揭示结构（irony_core中的认知翻转方式）翻译为COC7可运行的神话机制，确定使用哪个具体神话实体/典籍/机制（写入mythos_anchor）；
②设计系统性的误导网络（false_lead、misdirector_npc、true_trace、reveal_trigger、retrospective_key），确保false_lead在irony.deep_truth框架下事后仍能合理解释。
</task>
<response_format>json_object</response_format>
<output>只输出合法JSON对象，不要Markdown、标题、解释或代码围栏。</output>
<schema>{
  "false_lead": "一条有说服力的误导线索，将调查员推向错误推断；必须在irony.deep_truth框架下事后仍能合理解释",
  "misdirector_npc": "一个NPC，其存在/行为自然支持false_delta解读，不一定是主动欺骗者",
  "true_trace": "一条表面支持false_delta、但在deep_truth下有更精确解释的线索",
  "reveal_trigger": "触发从false_delta到deep_truth认知翻转的具体事件或发现",
  "retrospective_key": "揭示后回头看时，这个细节从开头就一直在指向deep_truth",
  "mythos_anchor": "本剧本使用的具体COC7神话实体/典籍/机制（如食尸鬼、深潜者等），不确定时在rules_notes说明",
  "rules_notes": ["规则来源/不确定性/保守处理说明"],
  "factions": [{"name":"派系名","goal":"目标","current_state":"当前正在做什么","timeline":[{"node":"第N节点","trigger":"推进条件","intervention_pivot":"调查员可执行的干预动作"}],"npcs":[{"name":"NPC名","public_identity":"公开身份","agenda":"独立议程","secret":"秘密","attitude":"初始态度","stats_note":"属性注记"}]}],
  "ending_signals": ["如果[条件]，则[谁的处境如何变化]，[什么不可挽回地改变]"]
}</schema>
<rules>
- false_lead必须满足后验必然性：在irony.deep_truth揭示后，这条线索必须仍有合理解释，不能是在真相框架下完全矛盾的假线索。
- misdirector_npc应有内在动机支持错误推断，不依赖"他是坏人"这种纯功能性设定。
- true_trace必须是歧义证据：表面支持错误推断，但只需一个额外上下文就能转向deep_truth。
- mythos_anchor一旦确定，后续阶段（调查路径图和最终剧本）不得更换；不确定时在rules_notes显式说明。
- 派系必须有non-empty current_state（无人干预时正在做什么），timeline节点必须有具体intervention_pivot。
- 如果收到qa_rejection，必须修复false_lead的后验兼容性或派系自主性问题；不要只改措辞。
- mythos_anchor 必须是一个具体的，有规则书支持的神话元素（如食尸鬼、深潜者、某个具体的古神或典籍等），而不是模糊的概念（如"某个邪神"）。如果规则书中没有完全符合的元素，可以选择最接近的一个并在rules_notes说明不完全匹配之处；如果完全没有合适的元素，尝试往人类法师、诅咒物品、古老地点等方向寻找替代锚点，仍找不到时才可以创造一个新元素，但必须在rules_notes详细说明其属性和与规则书元素的关系（如"类似于食尸鬼但更专注于守护知识"）。总之，mythos_anchor必须是具体且可操作的，而不是抽象或模糊的概念。
</rules>`

const misdirectionQASystemPrompt = `<role>COC7沙盒误导设计QA</role>
<task>审核误导设计是否满足：false_lead后验兼容（在deep_truth下仍可解释）、misdirector_npc动机合理、派系自主行动、干预枢纽具体可执行。不审核规则书内容。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：内部推理（可选，无副作用）
  {"action":"think","think":"推理内容"}
- response：最终审核结论。
  {"action":"response","pass":true,"reason":"审核理由","reject_reasons":[],"suggested_fix":"给architect的具体修改方向"}
</tools>
<audit_rules>
审核四点，任意一点不满足则pass=false，reject_reasons必须逐条列出：
1. false_lead后验兼容：false_lead在irony.deep_truth揭示后必须仍有合理解释。如果这条线索在真实情况下完全无法解释（只有在错误推断成立时才说得通），pass=false。
2. misdirector_npc动机合理：misdirector_npc必须有内在动机支持错误推断（被动的存在或行为即可），而不是被写成"专门误导调查员的功能性NPC"。
3. 派系自主性：每个派系必须有non-empty current_state，且timeline节点描述无人干预时的世界运动，而不是"等待调查员触发X"。
4. 干预枢纽有效性：每个timeline节点的intervention_pivot必须描述一个具体的可执行动作，而不是"调查员可以干预"这种空话。
不审核：规则书准确性、神话元素细节、NPC属性数值。
</audit_rules>`

const misdirectionExample = `{"false_lead":"墓地管理员证实有人定期入侵并窃取书籍，物品清单精确，显示这是有目的的盗窃行为","misdirector_npc":"守墓人，出于对秩序的维护本能，将任何进入禁区者描述为入侵者和盗贼","true_trace":"被带走的书籍上均有一个褪色的书写者姓名花押，与失踪者Douglas Kimball姓名首字母完全吻合","reveal_trigger":"调查员发现Douglas的旧藏书记录，核对后确认图书馆现有馆藏与其遗产清单高度重合","retrospective_key":"Douglas对每本书的位置了如指掌，从未触碰其他书籍——入侵者对馆内布局的熟悉程度超越任何盗贼","mythos_anchor":"食尸鬼（Ghoul）：COC7规则书第XX页；已核验为死者变形后的非人存在类型","rules_notes":["食尸鬼条目已核验，具体属性数值KP按规则书裁定"],"factions":[{"name":"旧知识的守护者","goal":"取回自己的书籍，维持与旧有身份的最后联结","current_state":"每夜进入图书馆取回一本书，行动越来越不谨慎","timeline":[{"node":"第0天：继续取书","trigger":"调查员进入调查","intervention_pivot":"直接与食尸鬼沟通——说出Douglas生前的名字或展示其遗物"},{"node":"第3天：被迫完全撤退","trigger":"调查员公开事件引来大批人员","intervention_pivot":"提前与其达成某种协议"}],"npcs":[{"name":"Douglas Kimball","public_identity":"已死亡的图书馆员（表面身份）","agenda":"取回自己的藏书，维持人性最后的碎片","secret":"已变为食尸鬼，但保留了对书籍的执念","attitude":"警惕、回避，若被识别则可能对话","stats_note":"食尸鬼属性按规则书；保留部分人类记忆"}]}],"ending_signals":["如果调查员让Douglas重获自己的藏书，则他彻底退隐墓地，书籍之谜以一种悲哀而非恐怖的方式收场"]}`

const misdirectionQAToolCallExample = `[{"action":"response","pass":true,"reason":"false_lead在deep_truth框架下有合理解释（守墓人的描述并不虚假），misdirector_npc有内在动机，派系有自主current_state，干预枢纽具体可执行。","reject_reasons":[],"suggested_fix":""}]`

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
请生成第1版MisdirectionFabric。`,
		string(reqJSON), string(constraintsJSON), string(ironyJSON),
		difficultySpec(room.req.Difficulty), conservative, ruleCtx,
		formatNPCNameBlacklist(room.npcBlacklist))

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
	var fabric MisdirectionFabric
	if err := chatAndParseJSON(ctx, s.room.architect, s.room.parser, s.architectMsgs, &fabric, misdirectionExample, "misdirection"); err != nil {
		return MisdirectionFabric{}, err
	}
	fabric = normalizeMisdirectionFabric(fabric, s.irony, s.conservative)
	fabricJSON, _ := json.Marshal(fabric)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{Role: "assistant", Content: string(fabricJSON)})
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
请基于同一个创作上下文重写MisdirectionFabric：逐条解决must_fix列出的问题；不要只改措辞；仍只输出合法JSON对象。`,
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

func buildStage2RuleContextFromIrony(ctx context.Context, irony IronyCore) (string, bool) {
	var sb strings.Builder
	conservative := false

	log.Printf("[scripter:rule_context] start delta=%q surface=%q",
		irony.DeltaOperator, truncateRunes(irony.SurfaceReading, 200))
	sb.WriteString("【规则书常量摘要，仅供Stage2锚定神话元素】\n")
	for _, constant := range []string{"mythos_creatures", "monsters", "great_old_ones_and_gods", "books", "spells"} {
		text := strings.TrimSpace(rulebook.ReadConstant(constant))
		if text == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n[%s]\n%s\n", constant, truncateRunes(text, 1200)))
	}

	question := fmt.Sprintf(
		"为COC7沙盒剧本核验一个最小神话锚点。表层现象：%s。深层真相方向：%s。δ算子：%s。请只给可保守使用的实体/典籍/法术/物品方向和必须避免的未核验数值。",
		irony.SurfaceReading, irony.DeepTruth, irony.DeltaOperator)

	log.Printf("[scripter:rule_context] lawyer question=%s", truncateRunes(question, scripterPromptLogLimit))
	lawyerHandle, err := loadSingleAgent(models.AgentRoleLawyer)
	if err != nil {
		conservative = true
		log.Printf("[scripter:rule_context] lawyer unavailable: %v", err)
		sb.WriteString(fmt.Sprintf("\n【lawyer_unavailable】%v\n必须在rules_notes标记不确定元素。\n", err))
		return truncateRunes(sb.String(), 9000), conservative
	}

	results := runLawyer(ctx, lawyerHandle, question, rulebook.GlobalIndex)
	if len(results) == 0 {
		conservative = true
		sb.WriteString("\n【lawyer_no_result】规则专家未返回有效裁定；必须在rules_notes标记不确定元素。\n")
		return truncateRunes(sb.String(), 9000), conservative
	}
	log.Printf("[scripter:rule_context] lawyer results=%d", len(results))
	sb.WriteString("\n【lawyer_result】\n")
	sb.WriteString(formatLawyerResults(results))
	sb.WriteString("\n")
	log.Printf("[scripter:rule_context] done conservative=%v ctx_len=%d", conservative, len(sb.String()))
	return truncateRunes(sb.String(), 9000), conservative
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
