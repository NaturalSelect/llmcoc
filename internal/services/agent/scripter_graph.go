// scripter_graph.go — Stage 3: InvestigationGraph generation and verification.
//
// The LLM generates a formal graph JSON; verifyInvestigationGraph (in
// scripter_delta.go) then checks five structural properties in pure Go.
// Violations are fed back as natural-language repair requests (up to 3 rounds).
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
// Prompts (Stage 3)
// ---------------------------------------------------------------------------

const invGraphSystemPrompt = `<role>COC7沙盒调查图设计师</role>
<task>根据IronyCore和MisdirectionFabric，生成形式化的InvestigationGraph——一个带节点ID、知识标签、δ信号和边集合的有向图JSON。
图必须满足：
- 从hook_node出发通过leads_to边可到达所有resolution_nodes
- 非终止节点不能有空leads_to（否则玩家会卡住）
- requires边不形成循环
- 至少一个delta_signal=false_delta节点，至少一个true_delta节点
- 所有到达终止节点的路径合计覆盖required_knowledge中的全部知识点
</task>
<response_format>json_object</response_format>
<output>只输出合法JSON对象，不要Markdown、标题、解释或代码围栏。</output>
<schema>{
  "hook_node": "入口节点ID",
  "nodes": [
    {
      "id": "唯一节点ID（英文下划线格式）",
      "type": "hook|investigation|encounter|resolution",
      "name": "节点名称（中文地点/事件名）",
      "knowledge": ["在此节点可获取的知识点（自然语言，精确且简短）"],
      "delta_signal": "false_delta|true_delta|ambiguous",
      "leads_to": ["直接可前往的节点ID列表"],
      "requires": ["此节点仅在访问过这些节点后才可访问（通常为空）"]
    }
  ],
  "resolution_nodes": ["终止节点ID列表（至少一个）"],
  "required_knowledge": ["到达任意终止节点前调查员必须已知的知识点"]
}</schema>
<node_design_rules>
- hook类型节点：场景入口，delta_signal=ambiguous，leads_to指向2-3个初始调查节点
- investigation类型节点：普通调查点，knowledge列出可获取事实，delta_signal反映该节点支持哪种δ推断
- encounter类型节点：与NPC/实体的关键遭遇，通常是true_delta或ambiguous
- resolution类型节点：scenario终止状态，leads_to为空，knowledge描述最终确认的事实
- delta_signal=false_delta的节点：该节点强化false_delta推断（调查员深陷误导）
- delta_signal=true_delta的节点：该节点提供指向deep_truth的关键证据（但可被误读）
- delta_signal=ambiguous的节点：该节点同时兼容两种解读
</node_design_rules>
<misdirection_rules>
- false_lead应体现在某个delta_signal=false_delta的节点中
- true_trace应体现在某个delta_signal=ambiguous节点中（因为它兼容两种解读）
- reveal_trigger应是从某个节点leads_to到true_delta节点的转折点
- retrospective_key可以是hook节点或早期ambiguous节点中的某条knowledge
</misdirection_rules>
<rules>
- required_knowledge：只包含"理解结局所必须掌握的核心事实"，通常3-5条；不要列举所有线索
- leads_to中的ID必须全部存在于nodes列表中
- hook_node必须在nodes列表中且type=hook
- resolution_nodes中的每个ID必须在nodes列表中且type=resolution
- 如果收到repair_request，逐条修复列出的问题，不要只改节点名称
</rules>`

const invGraphQASystemPrompt = `<role>调查图结构QA</role>
<task>审核InvestigationGraph是否满足结构完备性：可达性、无死端、信息覆盖、δ平衡。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- response：{"action":"response","pass":true,"reason":"...","reject_reasons":[],"suggested_fix":"..."}
</tools>
<audit_rules>
1. 可达性：从hook_node通过leads_to能到达所有resolution_nodes。
2. 无死端：非终止节点均有非空leads_to。
3. 信息覆盖：每条到终止节点的路径积累required_knowledge中的全部知识点。
4. δ平衡：至少一个false_delta节点和一个true_delta节点。
5. 节点ID一致性：leads_to和resolution_nodes中引用的ID全部存在于nodes列表中。
</audit_rules>`

const invGraphExample = `{
  "hook_node": "scene_hook",
  "nodes": [
    {"id":"scene_hook","type":"hook","name":"图书馆入口","knowledge":["近期有书籍失窃报告，守墓人报警"],"delta_signal":"ambiguous","leads_to":["investigate_library","talk_caretaker"],"requires":[]},
    {"id":"investigate_library","type":"investigation","name":"书架调查","knowledge":["失窃书目上均有同一人手写花押"],"delta_signal":"ambiguous","leads_to":["check_records","discover_trace"],"requires":[]},
    {"id":"talk_caretaker","type":"investigation","name":"询问守墓人","knowledge":["守墓人描述入侵者体型、举止异常"],"delta_signal":"false_delta","leads_to":["check_records","cemetery_approach"],"requires":[]},
    {"id":"check_records","type":"investigation","name":"查阅记录","knowledge":["花押属于已故图书馆员Douglas Kimball","Douglas三年前死于意外"],"delta_signal":"ambiguous","leads_to":["cemetery_approach"],"requires":[]},
    {"id":"discover_trace","type":"investigation","name":"发现痕迹","knowledge":["书架间有非人类气味，地板有爪痕"],"delta_signal":"true_delta","leads_to":["cemetery_approach"],"requires":[]},
    {"id":"cemetery_approach","type":"encounter","name":"墓地夜间遭遇","knowledge":["遭遇非人存在，行为指向寻回旧物而非攻击","实体对Douglas名字有反应"],"delta_signal":"true_delta","leads_to":["resolution_low_cost","resolution_confrontation"],"requires":[]},
    {"id":"resolution_low_cost","type":"resolution","name":"和平收场","knowledge":["Douglas已变形但保留记忆，取回藏书后退隐"],"delta_signal":"true_delta","leads_to":[],"requires":[]},
    {"id":"resolution_confrontation","type":"resolution","name":"对抗收场","knowledge":["实体被迫消失，但书籍问题未解决"],"delta_signal":"ambiguous","leads_to":[],"requires":[]}
  ],
  "resolution_nodes": ["resolution_low_cost","resolution_confrontation"],
  "required_knowledge": ["花押属于Douglas Kimball","实体保留人类记忆并寻回旧物"]
}`

const invGraphQAToolCallExample = `[{"action":"response","pass":true,"reason":"从hook_node可达所有resolution_nodes，非终止节点均有leads_to，路径覆盖required_knowledge，有false_delta和true_delta节点。","reject_reasons":[],"suggested_fix":""}]`

// ---------------------------------------------------------------------------
// Session (Stage 3)
// ---------------------------------------------------------------------------

type invGraphSession struct {
	room          *scripterRoom
	constraints   ScripterConstraints
	irony         IronyCore
	misdirection  MisdirectionFabric
	architectMsgs []llm.ChatMessage
	qaMsgs        []llm.ChatMessage
}

func newInvGraphSession(room *scripterRoom, constraints ScripterConstraints, irony IronyCore, misdirection MisdirectionFabric) *invGraphSession {
	constraintsJSON, _ := json.Marshal(constraints)
	ironyJSON, _ := json.Marshal(irony)
	misdirectionJSON, _ := json.Marshal(misdirection)

	architectPrompt := fmt.Sprintf(
		`<constraints>%s</constraints>
<irony_core>%s</irony_core>
<misdirection_fabric>%s</misdirection_fabric>
<fixed_mythos_anchor>%s</fixed_mythos_anchor>
<length>%s</length>
<difficulty_spec>
%s
</difficulty_spec>
<json_example>%s</json_example>
请生成第1版InvestigationGraph。`,
		string(constraintsJSON), string(ironyJSON), string(misdirectionJSON),
		misdirection.MythosAnchor,
		lengthSpec(room.req.TargetLength), difficultySpec(room.req.Difficulty),
		invGraphExample)

	qaPrompt := fmt.Sprintf(
		`<irony_core>%s</irony_core>
你是持续运行的InvestigationGraph QA会话。每次收到<inv_graph_candidate>后，通过think/response工具调用审核它。`,
		string(ironyJSON))

	return &invGraphSession{
		room:         room,
		constraints:  constraints,
		irony:        irony,
		misdirection: misdirection,
		architectMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.architect.systemPrompt(invGraphSystemPrompt)},
			{Role: "user", Content: architectPrompt},
		},
		qaMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.qa.systemPrompt(invGraphQASystemPrompt)},
			{Role: "user", Content: qaPrompt},
		},
	}
}

func (s *invGraphSession) generate(ctx context.Context, attempt int) (InvestigationGraph, error) {
	logStagePrompt(fmt.Sprintf("inv_graph_attempt_%d", attempt), s.architectMsgs)
	var graph InvestigationGraph
	if err := chatAndParseJSON(ctx, s.room.architect, s.room.parser, s.architectMsgs, &graph, invGraphExample, "inv_graph"); err != nil {
		return InvestigationGraph{}, err
	}
	graphJSON, _ := json.Marshal(graph)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{Role: "assistant", Content: string(graphJSON)})
	log.Printf("[scripter:inv_graph] attempt=%d nodes=%d resolutions=%d hook=%q",
		attempt, len(graph.Nodes), len(graph.ResolutionNodes), graph.HookNode)
	return graph, nil
}

func (s *invGraphSession) reviewLLM(ctx context.Context, attempt int, graph InvestigationGraph) (SandboxQA, error) {
	graphJSON, _ := json.Marshal(graph)
	s.qaMsgs = append(s.qaMsgs, llm.ChatMessage{
		Role: "user",
		Content: fmt.Sprintf(`<inv_graph_candidate attempt="%d">%s</inv_graph_candidate>
请审核这个候选。`, attempt, string(graphJSON)),
	})
	qa, msgs, err := runSandboxQALoop(ctx, s.room, s.qaMsgs, invGraphQAToolCallExample, "inv_graph")
	s.qaMsgs = msgs
	return qa, err
}

func (s *invGraphSession) feedRepair(attempt int, graph InvestigationGraph, violations []string) {
	graphJSON, _ := json.Marshal(graph)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{
		Role: "user",
		Content: fmt.Sprintf(`<repair_request attempt="%d">
<current_graph>%s</current_graph>
<violations>
%s
</violations>
</repair_request>
请修复上述结构问题：逐条解决violations中列出的问题；不要只改节点名称；仍只输出合法JSON对象。`,
			attempt, string(graphJSON), formatGraphViolations(violations)),
	})
}

// ---------------------------------------------------------------------------
// Top-level Stage 3 entry point
// ---------------------------------------------------------------------------

// generateInvestigationGraphWithVerification generates the graph and runs the
// formal Go verification (verifyInvestigationGraph).  If violations are found,
// they are fed back to the LLM as repair requests.  Up to verifyRounds rounds
// of verification+repair are attempted before falling back to the LLM-QA loop.
func generateInvestigationGraphWithVerification(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, irony IronyCore, misdirection MisdirectionFabric) (InvestigationGraph, error) {
	session := newInvGraphSession(room, constraints, irony, misdirection)
	const maxAttempts = 15
	const maxVerifyRounds = 3 // Go-verification repair rounds per LLM attempt
	log.Printf("[scripter:inv_graph] start maxAttempts=%d maxVerifyRounds=%d anchor=%q",
		maxAttempts, maxVerifyRounds, truncateRunes(misdirection.MythosAnchor, 80))

	var lastQA *SandboxQA

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			log.Printf("[scripter:inv_graph] context cancelled at attempt=%d: %v", attempt, ctx.Err())
			return InvestigationGraph{}, ctx.Err()
		}

		graph, err := session.generate(ctx, attempt)
		if err != nil {
			return InvestigationGraph{}, err
		}

		// --- Formal Go verification first ---
		verified := false
		for vround := 1; vround <= maxVerifyRounds; vround++ {
			violations := verifyInvestigationGraph(graph)
			if len(violations) == 0 {
				verified = true
				log.Printf("[scripter:inv_graph] attempt=%d vround=%d verified OK", attempt, vround)
				break
			}
			log.Printf("[scripter:inv_graph] attempt=%d vround=%d violations=%d %v",
				attempt, vround, len(violations), violations)
			logScripterArtifact(fmt.Sprintf("Stage 3 InvGraph Violations a%d v%d", attempt, vround), violations)
			if vround == maxVerifyRounds {
				break
			}
			log.Printf("[scripter:inv_graph] feedRepair a=%d v=%d violations=%d", attempt, vround, len(violations))
			session.feedRepair(attempt, graph, violations)
			log.Printf("[scripter:inv_graph] re-generate after repair a=%d v=%d", attempt, vround)
			graph, err = session.generate(ctx, attempt)
			if err != nil {
				return InvestigationGraph{}, err
			}
		}

		if !verified {
			// Formal check failed after repair rounds; count as QA rejection
			log.Printf("[scripter:inv_graph] attempt=%d formal verification still failing, continuing to next attempt", attempt)
			session.feedRepair(attempt, graph, verifyInvestigationGraph(graph))
			continue
		}

		// --- LLM QA check (semantic quality) ---
		qa, err := session.reviewLLM(ctx, attempt, graph)
		if err != nil {
			return InvestigationGraph{}, err
		}
		lastQA = &qa
		log.Printf("[scripter:inv_graph_qa] attempt=%d pass=%v reason=%q rejects=%q",
			attempt, qa.Pass, truncateRunes(qa.Reason, 400), strings.Join(qa.RejectReasons, " | "))
		logScripterArtifact(fmt.Sprintf("Stage 3 InvGraph QA Attempt %d", attempt), qa)

		if qa.Pass {
			logScripterArtifact("Stage 3 InvestigationGraph", graph)
			return graph, nil
		}

		// Feed LLM QA rejection back
		log.Printf("[scripter:inv_graph] feedQARejection attempt=%d rejects=%d %q",
			attempt, len(qa.RejectReasons), strings.Join(qa.RejectReasons, " | "))
		graphJSON, _ := json.Marshal(graph)
		session.architectMsgs = append(session.architectMsgs, llm.ChatMessage{
			Role: "user",
			Content: fmt.Sprintf(`<qa_rejection attempt="%d">
<rejected_graph>%s</rejected_graph>
<must_fix>
%s
</must_fix>
</qa_rejection>
请基于同一创作上下文重写InvestigationGraph，逐条解决must_fix；仍只输出合法JSON对象。`,
				attempt, string(graphJSON), formatSandboxMustFix(qa)),
		})
	}

	return InvestigationGraph{}, fmt.Errorf("InvestigationGraph 生成失败，连续%d轮未通过验证，最后QA原因=%v",
		maxAttempts, sandboxQARejectReasons(lastQA))
}

// deltaSignalToCluePrefix maps an InvNode delta_signal to the clue string prefix
// used in ScenarioContent.Clues.
func deltaSignalToCluePrefix(signal string) string {
	switch strings.ToLower(strings.TrimSpace(signal)) {
	case "false_delta":
		return "[误导]"
	case "true_delta":
		return "[隐藏]"
	default: // ambiguous, hook
		return "[真实]"
	}
}
