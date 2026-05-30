// scripter_irony.go — Stage 1: IronyCore generation (literary mindset, NO CoC).
//
// The system prompt deliberately omits any CoC / TTRPG context.  The LLM is
// addressed as a literary mystery architect.  CoC mechanics are introduced
// only in Stage 2 (scripter_misdirection.go).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

// ---------------------------------------------------------------------------
// Prompts (Stage 1)
// ---------------------------------------------------------------------------

// ironyCoreSystemPrompt is built dynamically so the operator table is always
// current without requiring prompt template edits.
func ironyCoreSystemPrompt() string {
	return `<role>剧本揭示架构师</role>
<task>
  - 设计故事的核心揭示结构：surface_reading（表层推断）通过 delta_operator（认知翻转类型）映射到 deep_truth（揭示真相）。
  - 同时设计两个配套元素：false_delta（经验读者会优先猜测的、与 delta_operator 语义维度不同的翻转类型）和 shared_evidence（在 delta_operator 与 false_delta 两种解读框架下均成立的歧义证据）。
  - 避免涉及政治。
</task>
<response_format>json_object</response_format>
<output>直接输出 JSON 对象，不要 Markdown、标题、解释或代码围栏。</output>
` + formatDeltaOperatorTable() + `
<fields>
{
  "delta_operator": "从 surface_reading 到 deep_truth 的认知翻转类型——从上表选 ID，或自定义英文下划线格式的新 ID",
  "delta_operator_desc": "仅自定义新翻转类型时填写：说明「理解的哪个维度」发生了变化（中文）；使用已有类型时留空字符串",
  "surface_reading": "在不知道真相的情况下，普通观察者对给定情境的第一推断——必须是立刻可形成的判断，不是「不确定」或「存在谜团」",
  "deep_truth": "揭示后的实际关系或事实",
  "entities": ["涉及的具体人物、地点或物件"],
  "false_delta": "经验读者对翻转类型的第一猜测（填翻转类型 ID）——必须与 delta_operator 作用于不同的语义维度，不是同一翻转的轻重或细节版本",
  "shared_evidence": "一条歧义证据：在不知道真相时，它能同时被 delta_operator 和 false_delta 两种解读框架支持，无法从证据表面区分哪种解读正确",
  "emotional_weight": "揭示时被重新定义的具体内容——某段关系的真实性质 / 某个身份的自我认知 / 某种信念的道德基础；不接受「震惊」「感动」等通用描述"
}
</fields>
<rules>
提交前逐条自查：
1. surface_reading 是普通观察者在给定情境下会立刻形成的推断，无需预知任何真相？
2. delta_operator 唯一且精确地解释 surface_reading → deep_truth 的变换，换其他翻转类型就失效？
3. false_delta 与 delta_operator 作用于不同的语义维度，不是同一翻转的简化或细节版本？
4. shared_evidence 在不知道真相时，被 delta_operator 和 false_delta 两种解读框架均能合理支持？
5. 知道 deep_truth 后，surface_reading 的所有表层观察仍然说得通，没有无法兼容的线索？
6. emotional_weight 指向一个具体的关系 / 身份 / 信念重新定义，不是通用情绪描述？
- 仔细思考，逐步推理，不要急于提交；设计一个有趣的结构，避免平庸或牵强；若核心概念太弱，调整整体 surface_reading / deep_truth / delta_operator 组合。
- 收到 qa_rejection 时，必须重新设计翻转结构，不只改措辞。
</rules>`
}

const ironyCoreQASystemPrompt = `<role>TPRG剧本揭示结构审核员</role>
<task>审核IronyCore的结构质量。只关注揭示结构的逻辑完备性，不评判内容好坏。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：内部推理（可选）
  {"action":"think","think":"推理内容"}
- response：最终审核结论。
  {"action":"response","pass":true,"reason":"审核理由","reject_reasons":[],"suggested_fix":"给architect的具体修改方向"}
</tools>
<audit_rules>
审核六点，任意一点不满足则pass=false，reject_reasons必须逐条列出：
1. surface_reading自然性：在给定entities和情境下，surface_reading是否是普通观察者的自然第一推断？如果需要已知部分真相才能产生这个推断，pass=false。
2. 翻转精确性：delta_operator能否唯一且非任意地解释从surface_reading到deep_truth的认知翻转？如果换其他翻转类型也能产生相同的映射，pass=false。
3. false_delta维度差异：false_delta与delta_operator是否作用于不同的语义维度（不是同类翻转的轻重或细节版本）？如果false_delta与delta_operator实质上描述同一种认知翻转，pass=false。
4. shared_evidence歧义性：shared_evidence在不知道真相时，能否同时被delta_operator和false_delta两种解读框架支持？如果该证据只有一种解读能解释，pass=false。
5. 后验必然性：揭示irony.deep_truth后回头看，surface_reading中的全部表层观察仍然说得通；没有哪条表层线索在deep_truth框架下完全无法解释。若有无法兼容的线索，pass=false。
6. emotional_weight具体性：揭示时是否有某个具体关系/身份/信念被重新定义？如果emotional_weight只是"震惊"、"感动"等通用描述，pass=false。
不审核：内容是否有趣、是否是常见谜题类型、风格倾向。
</audit_rules>`

const ironyCoreExample = `{"delta_operator":"role_swap","delta_operator_desc":"","surface_reading":"老人每晚去图书馆偷走特定书籍","deep_truth":"书是他自己的，他在取回被窃之物","entities":["老人","图书馆员","书籍收藏"],"false_delta":"identity_collapse","shared_evidence":"老人对书籍的熟悉程度异乎寻常，每次都精准定位，从不乱翻","emotional_weight":"「盗贼」与「失主」的身份在道德上互换，追书之人才是真正的受害者"}`

const ironyCoreQAToolCallExample = `[{"action":"response","pass":true,"reason":"surface_reading自然，翻转类型映射精确，false_delta作用于不同语义维度，shared_evidence在两种解读下均成立，后验必然性成立，emotional_weight具体。","reject_reasons":[],"suggested_fix":""}]`

// ---------------------------------------------------------------------------
// Tool-call types (Stage 1)
// ---------------------------------------------------------------------------

type ironyCoreQAToolCall struct {
	Action        ToolCallType `json:"action"`
	Think         string       `json:"think,omitempty"`
	Pass          bool         `json:"pass,omitempty"`
	Reason        string       `json:"reason,omitempty"`
	RejectReasons []string     `json:"reject_reasons,omitempty"`
	SuggestedFix  string       `json:"suggested_fix,omitempty"`
}

// ---------------------------------------------------------------------------
// Session (Stage 1)
// ---------------------------------------------------------------------------

type ironySession struct {
	room          *scripterRoom
	constraints   ScripterConstraints
	architectMsgs []llm.ChatMessage
	qaMsgs        []llm.ChatMessage
}

func newIronySession(room *scripterRoom, constraints ScripterConstraints, usedOperators []string) *ironySession {
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)

	usedBlock := ""
	if len(usedOperators) > 0 {
		usedBlock = fmt.Sprintf("\n<already_used_operators>%s</already_used_operators>\n以上算子已在本次生成中使用，请优先选择不同算子以增加多样性。",
			strings.Join(usedOperators, ", "))
	}

	architectPrompt := fmt.Sprintf(
		`<request_json>%s</request_json>
<constraints>%s</constraints>
<note>geography_flavor 和 era 只作为布景风味。你的任务是设计揭示结构，不是描述背景。</note>%s
请设计IronyCore。`, string(reqJSON), string(constraintsJSON), usedBlock)

	qaPrompt := fmt.Sprintf(
		`<constraints>%s</constraints>
你是持续运行的IronyCore QA会话。每次收到<irony_core_candidate>后，通过think/response工具调用审核它。`,
		string(constraintsJSON))

	return &ironySession{
		room:        room,
		constraints: constraints,
		architectMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.architect.systemPrompt(ironyCoreSystemPrompt())},
			{Role: "user", Content: architectPrompt},
		},
		qaMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.qa.systemPrompt(ironyCoreQASystemPrompt)},
			{Role: "user", Content: qaPrompt},
		},
	}
}

func (s *ironySession) generate(ctx context.Context, attempt int) (IronyCore, error) {
	logStagePrompt(fmt.Sprintf("irony_core_attempt_%d", attempt), s.architectMsgs)
	core, msgs, err := runIronyCoreGenerate(ctx, s.room, s.architectMsgs, attempt)
	s.architectMsgs = msgs
	if err != nil {
		return IronyCore{}, err
	}
	core = normalizeIronyCore(core)
	if !knownDeltaOperatorID(core.DeltaOperator) {
		log.Printf("[scripter:novel_operator] attempt=%d operator=%q desc=%q — not in DeltaOperators; consider adding after review",
			attempt, core.DeltaOperator, core.DeltaOperatorDesc)
	}
	log.Printf("[scripter:irony_core] attempt=%d delta=%q false_delta=%q surface=%q",
		attempt, core.DeltaOperator, core.FalseDelta, truncateRunes(core.SurfaceReading, 200))
	return core, nil
}

func (s *ironySession) review(ctx context.Context, attempt int, core IronyCore) (SandboxQA, error) {
	coreJSON, _ := json.Marshal(core)
	s.qaMsgs = append(s.qaMsgs, llm.ChatMessage{
		Role:    "user",
		Content: fmt.Sprintf(`<irony_core_candidate attempt="%d">%s</irony_core_candidate>\n请审核这个候选。`, attempt, string(coreJSON)),
	})
	qa, msgs, err := runIronyCoreQALoop(ctx, s.room, s.qaMsgs)
	s.qaMsgs = msgs
	return qa, err
}

func (s *ironySession) feedRejection(attempt int, core IronyCore, qa SandboxQA) {
	coreJSON, _ := json.Marshal(core)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{
		Role: "user",
		Content: fmt.Sprintf(`<qa_rejection attempt="%d">
<rejected_irony_core>%s</rejected_irony_core>
<must_fix>
%s
</must_fix>
</qa_rejection>
请基于同一个创作上下文重新设计IronyCore：逐条解决must_fix列出的结构问题；不要只改措辞；仍只输出合法JSON对象。`,
			attempt, string(coreJSON), formatSandboxMustFix(qa)),
	})
}

// ---------------------------------------------------------------------------
// Top-level Stage 1 entry point
// ---------------------------------------------------------------------------

func generateIronyCoreWithQA(ctx context.Context, room *scripterRoom, constraints ScripterConstraints) (IronyCore, error) {
	session := newIronySession(room, constraints, nil)
	const maxAttempts = 20
	log.Printf("[scripter:irony_core] start maxAttempts=%d theme=%q", maxAttempts, truncateRunes(constraints.Theme, 100))
	var lastQA *SandboxQA
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			log.Printf("[scripter:irony_core] context cancelled at attempt=%d: %v", attempt, ctx.Err())
			return IronyCore{}, ctx.Err()
		}
		core, err := session.generate(ctx, attempt)
		if err != nil {
			return IronyCore{}, err
		}
		qa, err := session.review(ctx, attempt, core)
		if err != nil {
			return IronyCore{}, err
		}
		lastQA = &qa
		log.Printf("[scripter:irony_core_qa] attempt=%d pass=%v reason=%q rejects=%q",
			attempt, qa.Pass, truncateRunes(qa.Reason, 400), strings.Join(qa.RejectReasons, " | "))
		logScripterArtifact(fmt.Sprintf("Stage 1 IronyCore QA Attempt %d", attempt), qa)
		if qa.Pass {
			logScripterArtifact("Stage 1 IronyCore", core)
			return core, nil
		}
		log.Printf("[scripter:irony_core] feedRejection attempt=%d rejects=%d %q",
			attempt, len(qa.RejectReasons), strings.Join(qa.RejectReasons, " | "))
		session.feedRejection(attempt, core, qa)
	}
	return IronyCore{}, fmt.Errorf("IronyCore QA 连续拒绝 %d 次，拒绝原因=%v",
		maxAttempts, sandboxQARejectReasons(lastQA))
}

// ---------------------------------------------------------------------------
// Architect generate (direct json_object, no tool-call loop)
// ---------------------------------------------------------------------------

func runIronyCoreGenerate(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage, attempt int) (IronyCore, []llm.ChatMessage, error) {
	const maxRounds = 5
	for round := 1; round <= maxRounds; round++ {
		if ctx.Err() != nil {
			return IronyCore{}, msgs, ctx.Err()
		}
		logStagePrompt(fmt.Sprintf("irony_core_a%d_r%d", attempt, round), msgs)
		raw, err := room.architect.provider.JsonChat(ctx, msgs)
		if err != nil {
			return IronyCore{}, msgs, err
		}
		log.Printf("[scripter:irony_core_architect] a=%d r=%d raw=%s", attempt, round, truncateRunes(raw, scripterRawLogLimit))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		core, parseErr := parseIronyCoreJSON(ctx, room.parser, raw)
		if parseErr != nil {
			log.Printf("[scripter:irony_core_architect] a=%d r=%d SYSTEM_REJECT json_parse_err=%v", attempt, round, parseErr)
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: JSON解析失败，必须重新输出合法JSON对象。"})
			continue
		}
		if err := validateIronyCoreFields(core); err != nil {
			log.Printf("[scripter:irony_core_architect] a=%d r=%d SYSTEM_REJECT validation_err=%v", attempt, round, err)
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: " + err.Error()})
			continue
		}
		log.Printf("[scripter:irony_core_architect] a=%d r=%d ok delta=%q", attempt, round, core.DeltaOperator)
		return core, msgs, nil
	}
	return IronyCore{}, msgs, fmt.Errorf("IronyCore 生成未在%d轮内成功", maxRounds)
}

// ---------------------------------------------------------------------------
// QA loop (think → response)
// ---------------------------------------------------------------------------

func runIronyCoreQALoop(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage) (SandboxQA, []llm.ChatMessage, error) {
	// IronyCore QA is pure structural review — no check_rule allowed.
	return runSandboxQALoop(ctx, room, msgs, ironyCoreQAToolCallExample, "irony_core")
}

// ---------------------------------------------------------------------------
// Parse / validate helpers
// ---------------------------------------------------------------------------

func parseIronyCoreJSON(ctx context.Context, parser agentHandle, raw string) (IronyCore, error) {
	stripped := strings.TrimSpace(llm.StripCodeFence(raw))
	var core IronyCore
	if err := json.Unmarshal([]byte(stripped), &core); err == nil {
		return core, nil
	}
	if parser.provider == nil {
		return IronyCore{}, fmt.Errorf("JSON解析失败且parser不可用")
	}
	fixed, repairErr := repairJSONWith(ctx, parser, stripped, fmt.Errorf("parse failed"), ironyCoreExample)
	if repairErr != nil {
		return IronyCore{}, repairErr
	}
	var core2 IronyCore
	if err := json.Unmarshal([]byte(strings.TrimSpace(fixed)), &core2); err != nil {
		return IronyCore{}, err
	}
	return core2, nil
}

func validateIronyCoreFields(core IronyCore) error {
	if strings.TrimSpace(core.DeltaOperator) == "" {
		return fmt.Errorf("delta_operator不能为空")
	}
	if strings.TrimSpace(core.SurfaceReading) == "" {
		return fmt.Errorf("surface_reading不能为空")
	}
	if strings.TrimSpace(core.DeepTruth) == "" {
		return fmt.Errorf("deep_truth不能为空")
	}
	if strings.TrimSpace(core.FalseDelta) == "" {
		return fmt.Errorf("false_delta不能为空")
	}
	if strings.TrimSpace(core.SharedEvidence) == "" {
		return fmt.Errorf("shared_evidence不能为空")
	}
	return nil
}

func normalizeIronyCore(core IronyCore) IronyCore {
	core.DeltaOperator = strings.TrimSpace(core.DeltaOperator)
	core.SurfaceReading = strings.TrimSpace(core.SurfaceReading)
	core.DeepTruth = strings.TrimSpace(core.DeepTruth)
	core.FalseDelta = strings.TrimSpace(core.FalseDelta)
	core.SharedEvidence = strings.TrimSpace(core.SharedEvidence)
	core.EmotionalWeight = strings.TrimSpace(core.EmotionalWeight)
	for i, e := range core.Entities {
		core.Entities[i] = strings.TrimSpace(e)
	}
	return core
}
