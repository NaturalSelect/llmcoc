// scripter_delta.go — δ-operator taxonomy, intermediate stage types, and
// formal InvestigationGraph verification algorithms.
//
// To add a new δ-operator: append one DeltaOperator entry to DeltaOperators
// and redeploy.  No other code changes are needed.
package agent

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// δ-operator extensible taxonomy
// ---------------------------------------------------------------------------

// DeltaOperator describes one transformation operator in the δ-framework.
type DeltaOperator struct {
	ID          string   // machine-readable identifier, e.g. "identity_collapse"
	Name        string   // display name
	Description string   // semantic dimension being transformed
	Examples    []string // literary / film archetypes (non-CoC)
}

// DeltaOperators is the canonical operator table.
// Append new entries here to extend the taxonomy; prompts are rendered
// dynamically from this slice, so no prompt templates need editing.
var DeltaOperators = []DeltaOperator{
	{
		ID: "identity_collapse", Name: "身份坍缩",
		Description: "施事身份错——X不是我们认为的X；身份被替换、伪装或分裂",
		Examples:    []string{"《蝴蝶梦》女管家真实身份", "双重人格凶手"},
	},
	{
		ID: "role_swap", Name: "角色互换",
		Description: "施事/受事互换——受害者与施害者的关系被倒置",
		Examples:    []string{"看似加害者实为被保护者", "举报者是真正的危险源"},
	},
	{
		ID: "causal_inversion", Name: "因果倒置",
		Description: "原因与结果方向错——我们观察到的后果被误认为原因",
		Examples:    []string{"症状被当作疾病本身", "防御行为被解读为攻击"},
	},
	{
		ID: "scale_lift", Name: "尺度跃升",
		Description: "场所/范围层级错——个人事件是宏观力量的截面",
		Examples:    []string{"一桩谋杀是整个制度性压迫的直接产物", "个人悲剧指向社会结构"},
	},
	{
		ID: "temporal_shift", Name: "时间移位",
		Description: "时间位置/序列被混淆——过去、未来或事件顺序与表象不符",
		Examples:    []string{"以为是预言的已经发生", "以为是历史的还在继续"},
	},
	{
		ID: "agency_collapse", Name: "能动性坍缩",
		Description: "主动/被动互换——主语和宾语的行动力被颠倒",
		Examples:    []string{"以为在追查的人其实是被引导的", "猎人与猎物角色互换"},
	},
	{
		ID: "moral_inversion", Name: "道德反转",
		Description: "善恶/受害判断被颠覆——规范性评价与外部表象相反",
		Examples:    []string{"公认的善举造成了真正的伤害", "\"怪物\"是真正的受害者"},
	},
	{
		ID: "intent_shift", Name: "意图偏移",
		Description: "行动表面正确但目的指向完全不同——不是\"谁做的\"而是\"为了什么\"发生了根本转变",
		Examples:    []string{"看似复仇实为自我牺牲", "看似背叛实为保护"},
	},
	{
		ID: "existence_inversion", Name: "存在反转",
		Description: "X的本体论层级被颠覆——以为活着的早已死亡，以为是人的是另一种存在，以为是真实事件的是虚构或幻觉（或反之）；翻转的不是\"X是谁\"而是\"X是什么/X是否存在\"",
		Examples:    []string{"《第六感》主角全程是鬼魂", "\"幸存者\"早在第一幕已死，读者一直在跟随一个亡者的视角"},
	},
	{
		ID: "perception_collapse", Name: "感知坍缩",
		Description: "目击者/叙述者的感知本身不可信——关键事件是幻觉、梦境、催眠或记忆篡改的产物，读者随叙述者一起经历了一套不真实的事件链",
		Examples:    []string{"《穆赫兰道》全片是死前意识的幻构", "主角亲历的「谋杀现场」是解离状态下的自我投射"},
	},
	{
		ID: "overread_trap", Name: "过读陷阱",
		Description: "真相从未隐藏——表层陈述就是事实，但观察者的模式识别本能驱使其构建了一个「更深层」的解读，而该解读恰恰是错误的；揭示时的翻转是「谜从来不存在」本身",
		Examples:    []string{"空城计：诸葛亮开城鼓琴，真相就是城中无兵，司马懿因过度解读而退兵", "目击者第一句话就是真相，侦探以为那是烟幕"},
	},
	{
		ID: "connection_inversion", Name: "关联反转",
		Description: "事件/实体之间的因果联系被颠覆——以为相互独立的事件实际出自同一意志/起源，或以为有关联的系列事件实际是彼此无关的巧合叠加；翻转的是「是否存在统一的幕后」",
		Examples:    []string{"三起看似偶发失踪实为同一捕猎者的连续猎食", "「连环凶案」实为三个不相关人分别独立作案在时间上的偶然重叠"},
	},
	// Future operators: append here ↓
}

// formatDeltaOperatorTable renders DeltaOperators as a human-readable block
// for injection into LLM prompts.  Adding a new entry to DeltaOperators is
// sufficient to include it in all generated prompts automatically.
func formatDeltaOperatorTable() string {
	var sb strings.Builder
	sb.WriteString("【认知翻转类型参考表】\n")
	sb.WriteString("每个故事的揭示结构依赖一种「认知翻转」——当真相揭晓时，读者对某件事的理解发生了什么根本性变化。\n")
	sb.WriteString("delta_operator 字段填写下表中的 ID（或自定义一个新 ID，同时在 delta_operator_desc 写明其含义）。\n")
	for _, op := range DeltaOperators {
		sb.WriteString(fmt.Sprintf("- %s（%s）：%s", op.ID, op.Name, op.Description))
		if len(op.Examples) > 0 {
			sb.WriteString("  例：" + strings.Join(op.Examples, "；"))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("以上类型不够用时，自定义新 ID（英文下划线格式）并在 delta_operator_desc 写明含义即可。\n")
	return sb.String()
}

// knownDeltaOperatorID returns true if id matches a registered operator.
func knownDeltaOperatorID(id string) bool {
	for _, op := range DeltaOperators {
		if op.ID == id {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Stage 1 intermediate: IronyCore
// ---------------------------------------------------------------------------

// IronyCore is the δ-framework representation of the thematic irony.
// Generated WITHOUT CoC context; CoC translation happens in Stage 2.
type IronyCore struct {
	// DeltaOperator is an operator ID from DeltaOperators, or a novel ID
	// proposed by the LLM (logged as [scripter:novel_operator]).
	DeltaOperator string `json:"delta_operator"`
	// DeltaOperatorDesc is non-empty only when DeltaOperator is a novel
	// operator not present in DeltaOperators.
	DeltaOperatorDesc string   `json:"delta_operator_desc,omitempty"`
	SurfaceReading    string   `json:"surface_reading"` // natural first interpretation
	DeepTruth         string   `json:"deep_truth"`      // revealed reality
	Entities          []string `json:"entities"`        // named participants
	// FalseDelta is the operator experienced players will first infer;
	// it must differ from DeltaOperator on at least one semantic dimension.
	FalseDelta string `json:"false_delta"`
	// SharedEvidence is ambiguous between SurfaceReading and DeepTruth at
	// the operator-type level, not just at the entity-specific level.
	SharedEvidence  string `json:"shared_evidence"`
	EmotionalWeight string `json:"emotional_weight"`
}

// ---------------------------------------------------------------------------
// Stage 2 intermediate: MisdirectionFabric
// ---------------------------------------------------------------------------

// MisdirectionFabric extends FactionMap with explicit misdirection structure
// and CoC translation.  FactionPlan is preserved for assembly compatibility.
type MisdirectionFabric struct {
	// Misdirection fields
	FalseLead        string `json:"false_lead"`        // compelling evidence that raises b(δ_wrong)
	MisdirectorNPC   string `json:"misdirector_npc"`   // NPC whose presence supports false delta
	TrueTrace        string `json:"true_trace"`        // hint compatible with true delta, easily misread
	RevealTrigger    string `json:"reveal_trigger"`    // event that collapses the false interpretation
	RetrospectiveKey string `json:"retrospective_key"` // what "was always there" pointing to true delta
	// CoC translation fields (equivalent to FactionMap)
	MythosAnchor  string        `json:"mythos_anchor"`
	RulesNotes    []string      `json:"rules_notes"`
	Factions      []FactionPlan `json:"factions"`
	EndingSignals []string      `json:"ending_signals"`
}

// ---------------------------------------------------------------------------
// Stage 3 intermediate: InvestigationGraph
// ---------------------------------------------------------------------------

// InvNode is one node in the investigation graph.
type InvNode struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"` // hook|investigation|encounter|resolution
	Name      string   `json:"name"`
	Knowledge []string `json:"knowledge"` // facts learnable at this node
	// DeltaSignal indicates which hypothesis this node supports.
	// mislead → [误导], reveal → [隐藏], ambiguous → [真实]
	DeltaSignal string   `json:"delta_signal"`
	LeadsTo     []string `json:"leads_to"` // forward edges (reachable next nodes)
	Requires    []string `json:"requires"` // prerequisite nodes; keep minimal
}

// InvestigationGraph is the formal structural representation of the scenario.
type InvestigationGraph struct {
	HookNode          string    `json:"hook_node"` // entry node ID
	Nodes             []InvNode `json:"nodes"`
	ResolutionNodes   []string  `json:"resolution_nodes"`   // terminal node IDs
	RequiredKnowledge []string  `json:"required_knowledge"` // Φ: epistemic completeness set
}

// ---------------------------------------------------------------------------
// Formal verification of InvestigationGraph
// ---------------------------------------------------------------------------

// verifyInvestigationGraph runs five structural checks and returns a list of
// violation descriptions.  An empty return means the graph is viable.
// All checks are pure Go; no LLM calls are made.
func verifyInvestigationGraph(graph InvestigationGraph) []string {
	var violations []string

	if len(graph.Nodes) == 0 {
		return []string{"nodes 为空，无法验证图结构"}
	}

	// Build node lookup map
	nodeByID := make(map[string]*InvNode, len(graph.Nodes))
	for i := range graph.Nodes {
		nodeByID[graph.Nodes[i].ID] = &graph.Nodes[i]
	}

	resolutionSet := make(map[string]bool, len(graph.ResolutionNodes))
	for _, r := range graph.ResolutionNodes {
		resolutionSet[r] = true
	}

	// Validate hook_node exists
	if _, ok := nodeByID[graph.HookNode]; !ok {
		violations = append(violations, fmt.Sprintf("hook_node %q 不在 nodes 列表中", graph.HookNode))
		return violations // cannot continue without valid hook
	}

	if len(graph.ResolutionNodes) == 0 {
		violations = append(violations, "resolution_nodes 为空：至少需要一个终止节点")
	}

	// --- Check 1: DAG on requires edges (Kahn's topological sort) ---
	if cycles := detectRequiresCycles(graph.Nodes); len(cycles) > 0 {
		violations = append(violations, cycles...)
	}

	// --- Check 2: BFS reachability from hook_node via leads_to ---
	reachable := bfsReachableNodes(graph.HookNode, graph.Nodes)
	for _, rn := range graph.ResolutionNodes {
		if !reachable[rn] {
			violations = append(violations,
				fmt.Sprintf("终止节点 %q 从入口 %q 不可到达（检查 leads_to 边是否形成连通路径）", rn, graph.HookNode))
		}
	}

	// --- Check 3: No dead ends among non-resolution nodes ---
	for _, node := range graph.Nodes {
		if resolutionSet[node.ID] {
			continue
		}
		if len(node.LeadsTo) == 0 {
			violations = append(violations,
				fmt.Sprintf("节点 %q 是死端：非终止节点但 leads_to 为空，玩家将卡住", node.ID))
		}
	}

	// --- Check 4: Epistemic completeness on all paths to resolution ---
	if len(graph.RequiredKnowledge) > 0 {
		const maxPaths = 60 // cap to avoid exponential blowup on dense graphs
		for _, rn := range graph.ResolutionNodes {
			if !reachable[rn] {
				continue // already reported above
			}
			paths := findAllSimplePaths(graph.HookNode, rn, nodeByID, maxPaths)
			if len(paths) == 0 {
				continue
			}
			for _, path := range paths {
				covered := make(map[string]bool)
				for _, nid := range path {
					if n, ok := nodeByID[nid]; ok {
						for _, k := range n.Knowledge {
							covered[k] = true
						}
					}
				}
				var missing []string
				for _, req := range graph.RequiredKnowledge {
					if !covered[req] {
						missing = append(missing, req)
					}
				}
				if len(missing) > 0 {
					// Truncate path display for readability
					pathStr := strings.Join(path, "→")
					if len([]rune(pathStr)) > 120 {
						pathStr = string([]rune(pathStr)[:120]) + "..."
					}
					violations = append(violations,
						fmt.Sprintf("路径 [%s] → %q 缺少必要知识: %v", pathStr, rn, missing))
				}
			}
		}
	}

	// --- Check 5: δ-balance ---
	hasFalseDelta, hasTrueDelta := false, false
	for _, node := range graph.Nodes {
		switch node.DeltaSignal {
		case "mislead":
			hasFalseDelta = true
		case "reveal":
			hasTrueDelta = true
		}
	}
	if !hasFalseDelta {
		violations = append(violations,
			"图中没有 delta_signal=mislead 的节点：至少需要一个让玩家形成错误推断的调查节点")
	}
	if !hasTrueDelta {
		violations = append(violations,
			"图中没有 delta_signal=reveal 的节点：至少需要一个指向真实关系的发现节点")
	}

	return violations
}

// detectRequiresCycles runs Kahn's algorithm on the requires-edge subgraph.
// Returns a non-empty slice if cycles are detected.
func detectRequiresCycles(nodes []InvNode) []string {
	// Build in-degree and adjacency for requires edges.
	// requires[A] → [B, C] means B and C depend on A.
	inDegree := make(map[string]int, len(nodes))
	dependents := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		if _, ok := inDegree[n.ID]; !ok {
			inDegree[n.ID] = 0
		}
		for _, req := range n.Requires {
			dependents[req] = append(dependents[req], n.ID)
			inDegree[n.ID]++
		}
	}

	queue := make([]string, 0, len(nodes))
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range dependents[cur] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if visited < len(nodes) {
		return []string{"requires 依赖图存在循环（拓扑排序未能访问全部节点）——请确保 requires 只引用不构成环的前置节点"}
	}
	return nil
}

// bfsReachableNodes returns the set of node IDs reachable from start
// by following leads_to edges.
func bfsReachableNodes(start string, nodes []InvNode) map[string]bool {
	leadsTo := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		leadsTo[n.ID] = n.LeadsTo
	}
	visited := make(map[string]bool, len(nodes))
	queue := []string{start}
	visited[start] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range leadsTo[cur] {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return visited
}

// findAllSimplePaths enumerates all simple paths from start to end via
// leads_to edges, capped at maxPaths to bound runtime on dense graphs.
func findAllSimplePaths(start, end string, nodeByID map[string]*InvNode, maxPaths int) [][]string {
	var results [][]string
	visited := make(map[string]bool, len(nodeByID))

	var dfs func(cur string, path []string)
	dfs = func(cur string, path []string) {
		if len(results) >= maxPaths {
			return
		}
		newPath := append(append([]string(nil), path...), cur)
		if cur == end {
			results = append(results, newPath)
			return
		}
		visited[cur] = true
		defer func() { visited[cur] = false }()
		if n, ok := nodeByID[cur]; ok {
			for _, next := range n.LeadsTo {
				if !visited[next] {
					dfs(next, newPath)
				}
			}
		}
	}
	dfs(start, nil)
	return results
}

// formatGraphViolations formats a list of violations as a numbered action list
// for LLM repair prompts.
func formatGraphViolations(violations []string) string {
	var sb strings.Builder
	for i, v := range violations {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, v))
	}
	return strings.TrimSpace(sb.String())
}
