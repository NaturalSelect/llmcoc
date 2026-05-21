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
	return `<role>短篇悬疑故事架构师</role>
<task>为一个短篇神秘故事设计核心的「揭示结构」：表层叙事与深层真相之间的转化算子（δ算子）。
你在设计一个文学谜题，不是游戏模组。忘记一切游戏规则和系统；用写短篇小说或电影剧本的逻辑工作。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
` + formatDeltaOperatorTable() + `
<tools>
- think：内部推理（可选，无副作用）
  {"action":"think","think":"推理内容"}
- generate：输出IronyCore。必须在至少一个think之后调用。
  {"action":"generate","delta_operator":"算子ID","surface_reading":"表层自然解读","deep_truth":"揭示后的真实关系","entities":["实体1","实体2"],"false_delta":"经验读者会首先推断的另一个算子ID","shared_evidence":"在算子类型层面也保持歧义的证据","emotional_weight":"揭示时某个具体关系/身份/信念被重新定义的感受"}
如需提出新算子，额外填写 delta_operator_desc：
  {"action":"generate","delta_operator":"my_new_id","delta_operator_desc":"该算子变换的语义维度（中文）",...}
</tools>
<batch_rules>
- 第一轮必须输出至少一个think；禁止第一轮直接generate。
- think和generate禁止在同一轮。先think，下一轮再generate。
</batch_rules>
<rules>
- surface_reading：给定情境下普通观察者会立刻产生的推断，不要是"不确定"或"存在谜团"。
- delta_operator：必须将surface_reading精确映射到deep_truth，且映射是非任意的（换一个算子就不对）。
- false_delta：必须与delta_operator在「变换的语义维度」上不同——不是同一算子的简化版，而是操作于不同的谓词语义角色；经验读者会首先形成这个推断，而不是delta_operator的推断。
- shared_evidence：这条证据在不知道真相的情况下，既可以被delta_operator的框架解读，也可以被false_delta的框架解读，且无法从证据类型本身判断哪个算子适用。
- emotional_weight：揭示时具体发生了什么——某段关系的真实性质、某个身份的自我认知、还是某种信念的道德基础被重新定义？不接受"震惊"、"感动"等通用描述。
- 禁止从游戏、规则书或桌游机制的角度思考。
- 如果收到qa_rejection，必须重新思考算子结构，不要只改措辞。
</rules>`
}

const ironyCoreQASystemPrompt = `<role>悬疑故事揭示结构审核员</role>
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
2. 算子精确性：delta_operator能否将surface_reading精确且非任意地映射到deep_truth？如果换一个算子也能产生相同映射，pass=false。
3. false_delta维度差异：false_delta与delta_operator是否在「变换的语义维度」上不同（不只是细节差异，而是操作于不同的谓词语义角色）？如果false_delta只是delta_operator的浅化版本，pass=false。
4. shared_evidence算子层面歧义：shared_evidence能否在不知道真相的情况下同时被delta_operator和false_delta两种框架合理解读，且无法从证据类型本身判断哪个算子适用？如果只有一种算子能解释该证据，pass=false。
5. 后验必然性：surface_reading（表层证据/状态）在deep_truth框架下必须有合理解释——揭示后回头看，所有表层观察仍然说得通，没有哪条线索在delta_true下变得完全无法解释。若有无法兼容的证据，pass=false。
6. emotional_weight具体性：揭示时是否有某个具体关系/身份/信念被重新定义？如果emotional_weight只是"震惊"、"感动"等通用描述，pass=false。
不审核：内容是否有趣、是否是常见谜题类型、风格倾向。
</audit_rules>`

const ironyCoreToolCallExample = `[{"action":"think","think":"我需要选择一个能将surface_reading精确映射到deep_truth的算子..."},{"action":"generate","delta_operator":"role_swap","surface_reading":"老人每晚去图书馆偷走特定书籍","deep_truth":"书是他自己的，他在取回被窃之物","entities":["老人","图书馆员","书籍收藏"],"false_delta":"identity_collapse","shared_evidence":"老人对书籍的熟悉程度异乎寻常，每次都精准定位，从不乱翻","emotional_weight":"「盗贼」与「失主」的身份在道德上互换，追书之人才是真正的受害者"}]`

const ironyCoreQAToolCallExample = `[{"action":"response","pass":true,"reason":"surface_reading自然，算子映射精确，false_delta在语义维度上不同，shared_evidence算子层面歧义，后验必然性成立，emotional_weight具体。","reject_reasons":[],"suggested_fix":""}]`

// ---------------------------------------------------------------------------
// Tool-call types (Stage 1)
// ---------------------------------------------------------------------------

type ironyCoreArchitectToolCall struct {
	Action            ToolCallType `json:"action"`
	Think             string       `json:"think,omitempty"`
	DeltaOperator     string       `json:"delta_operator,omitempty"`
	DeltaOperatorDesc string       `json:"delta_operator_desc,omitempty"`
	SurfaceReading    string       `json:"surface_reading,omitempty"`
	DeepTruth         string       `json:"deep_truth,omitempty"`
	Entities          []string     `json:"entities,omitempty"`
	FalseDelta        string       `json:"false_delta,omitempty"`
	SharedEvidence    string       `json:"shared_evidence,omitempty"`
	EmotionalWeight   string       `json:"emotional_weight,omitempty"`
}

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
	core, msgs, err := runIronyCoreArchitectLoop(ctx, s.room, s.architectMsgs, attempt)
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
请基于同一个创作上下文重新设计IronyCore：逐条解决must_fix列出的结构问题；不要只改措辞；仍只输出合法JSON数组工具调用。`,
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
// Architect loop (think → generate)
// ---------------------------------------------------------------------------

func runIronyCoreArchitectLoop(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage, attempt int) (IronyCore, []llm.ChatMessage, error) {
	const maxRounds = 20
	seenThink := false
	for round := 1; round <= maxRounds; round++ {
		if ctx.Err() != nil {
			return IronyCore{}, msgs, ctx.Err()
		}
		logStagePrompt(fmt.Sprintf("irony_core_architect_a%d_r%d", attempt, round), msgs)
		raw, err := room.architect.provider.Chat(ctx, msgs)
		if err != nil {
			return IronyCore{}, msgs, err
		}
		log.Printf("[scripter:irony_core_architect] a=%d r=%d raw=%s", attempt, round, truncateRunes(raw, scripterRawLogLimit))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		calls, parseErr := parseIronyCoreArchitectCalls(ctx, room.parser, raw)
		if parseErr != nil {
			log.Printf("[scripter:irony_core_architect] a=%d r=%d SYSTEM_REJECT json_parse_err=%v", attempt, round, parseErr)
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: JSON解析失败，必须重新输出合法JSON数组。"})
			continue
		}
		if len(calls) == 0 {
			log.Printf("[scripter:irony_core_architect] a=%d r=%d SYSTEM_REJECT empty_calls", attempt, round)
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须输出至少一个工具调用。"})
			continue
		}

		hasGenerate, hasThink, invalidAction := false, false, false
		for _, call := range calls {
			switch call.Action {
			case ToolThink:
				hasThink = true
			case "generate":
				hasGenerate = true
			default:
				log.Printf("[scripter:irony_core_architect] a=%d r=%d SYSTEM_REJECT invalid_action=%s", attempt, round, call.Action)
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf("SYSTEM REJECT: IronyCore生成只允许think/generate，不允许%s。", call.Action)})
				invalidAction = true
			}
		}
		if invalidAction {
			continue
		}
		if hasGenerate && hasThink {
			log.Printf("[scripter:irony_core_architect] a=%d r=%d SYSTEM_REJECT mixed_think_generate", attempt, round)
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: think和generate不能在同一轮。先think，下一轮再generate。"})
			continue
		}
		if hasThink {
			seenThink = true
			log.Printf("[scripter:irony_core_architect] a=%d r=%d think seenThink=%v", attempt, round, seenThink)
			continue // wait for next round with generate
		}
		if hasGenerate {
			if !seenThink {
				log.Printf("[scripter:irony_core_architect] a=%d r=%d SYSTEM_REJECT generate_without_prior_think", attempt, round)
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 第一轮禁止直接generate，必须先think。"})
				continue
			}
			core, err := ironyCoreFromGenerateCall(calls)
			if err != nil {
				log.Printf("[scripter:irony_core_architect] a=%d r=%d SYSTEM_REJECT validation_err=%v", attempt, round, err)
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: " + err.Error()})
				continue
			}
			log.Printf("[scripter:irony_core_architect] a=%d r=%d generate_ok delta=%q", attempt, round, core.DeltaOperator)
			return core, msgs, nil
		}
	}
	return IronyCore{}, msgs, fmt.Errorf("IronyCore 生成未在%d轮内给出generate", maxRounds)
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

func parseIronyCoreArchitectCalls(ctx context.Context, parser agentHandle, raw string) ([]ironyCoreArchitectToolCall, error) {
	stripped := strings.TrimSpace(llm.StripCodeFence(llm.JsonArryProtect(raw)))
	var calls []ironyCoreArchitectToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil {
		return calls, nil
	}
	if parser.provider == nil {
		return nil, fmt.Errorf("JSON解析失败且parser不可用")
	}
	fixed, repairErr := repairJSONWith(ctx, parser, stripped, fmt.Errorf("parse failed"), ironyCoreToolCallExample)
	if repairErr != nil {
		return nil, repairErr
	}
	fixed = strings.TrimSpace(llm.JsonArryProtect(fixed))
	var calls2 []ironyCoreArchitectToolCall
	if err := json.Unmarshal([]byte(fixed), &calls2); err != nil {
		return nil, err
	}
	return calls2, nil
}

func ironyCoreFromGenerateCall(calls []ironyCoreArchitectToolCall) (IronyCore, error) {
	var gen *ironyCoreArchitectToolCall
	for i := range calls {
		if calls[i].Action == "generate" {
			if gen != nil {
				return IronyCore{}, fmt.Errorf("generate只能有一个")
			}
			gen = &calls[i]
		}
	}
	if gen == nil {
		return IronyCore{}, fmt.Errorf("缺少generate")
	}
	if strings.TrimSpace(gen.DeltaOperator) == "" {
		return IronyCore{}, fmt.Errorf("generate.delta_operator不能为空")
	}
	if strings.TrimSpace(gen.SurfaceReading) == "" {
		return IronyCore{}, fmt.Errorf("generate.surface_reading不能为空")
	}
	if strings.TrimSpace(gen.DeepTruth) == "" {
		return IronyCore{}, fmt.Errorf("generate.deep_truth不能为空")
	}
	if strings.TrimSpace(gen.FalseDelta) == "" {
		return IronyCore{}, fmt.Errorf("generate.false_delta不能为空")
	}
	if strings.TrimSpace(gen.SharedEvidence) == "" {
		return IronyCore{}, fmt.Errorf("generate.shared_evidence不能为空")
	}
	return IronyCore{
		DeltaOperator:     strings.TrimSpace(gen.DeltaOperator),
		DeltaOperatorDesc: strings.TrimSpace(gen.DeltaOperatorDesc),
		SurfaceReading:    strings.TrimSpace(gen.SurfaceReading),
		DeepTruth:         strings.TrimSpace(gen.DeepTruth),
		Entities:          gen.Entities,
		FalseDelta:        strings.TrimSpace(gen.FalseDelta),
		SharedEvidence:    strings.TrimSpace(gen.SharedEvidence),
		EmotionalWeight:   strings.TrimSpace(gen.EmotionalWeight),
	}, nil
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
