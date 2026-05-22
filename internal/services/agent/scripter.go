// NOTE: Scenario generation pipeline for sandbox-style COC situation briefs.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"math/rand"
	"strings"
	"sync"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// ---------------------------------------------------------------------------
// Compatibility/API types and entrypoint
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
	IronyCore  *IronyCore    `json:"irony_core,omitempty"`
	Iterations int           `json:"iterations"`
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

const (
	defaultScripterEra    = "1920s"
	defaultScripterAuthor = "agent-team"

	scripterPromptLogLimit = 8000
	scripterRawLogLimit    = 20000
	scripterRepairLogLimit = 12000
)

var genScenarioMutex sync.Mutex

func RunScripterScenarioTeam(ctx context.Context, req ScenarioCreationRequest) (ScenarioCreationOutput, error) {
	genScenarioMutex.Lock()
	defer genScenarioMutex.Unlock()

	room, err := newScripterRoom(req)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	return room.Run(ctx)
}

type scripterRoom struct {
	architect    agentHandle
	qa           agentHandle
	lawyer       agentHandle
	parser       agentHandle
	req          ScenarioCreationRequest
	npcBlacklist []string
	titleSamples []string
}

func newScripterRoom(req ScenarioCreationRequest) (*scripterRoom, error) {
	architect, err := loadSingleAgent(models.AgentRoleArchitect)
	if err != nil {
		return nil, err
	}
	qa, err := loadSingleAgent(models.AgentRoleQAGuard)
	if err != nil {
		return nil, err
	}
	lawyer, err := loadSingleAgent(models.AgentRoleLawyer)
	if err != nil {
		return nil, err
	}
	parser, err := loadSingleAgent(models.AgentRoleParser)
	if err != nil {
		return nil, err
	}
	return &scripterRoom{architect: architect, qa: qa, lawyer: lawyer, parser: parser, req: normalizeScenarioCreationRequest(req)}, nil
}

func normalizeScenarioCreationRequest(req ScenarioCreationRequest) ScenarioCreationRequest {
	if req.MinPlayers <= 0 {
		req.MinPlayers = 1
	}
	if req.MaxPlayers <= 0 {
		req.MaxPlayers = 4
	}
	if req.MaxPlayers < req.MinPlayers {
		req.MaxPlayers = req.MinPlayers
	}
	if strings.TrimSpace(req.Difficulty) == "" {
		req.Difficulty = "normal"
	} else {
		req.Difficulty = strings.TrimSpace(req.Difficulty)
	}
	if strings.TrimSpace(req.TargetLength) == "" {
		req.TargetLength = "short"
	} else {
		req.TargetLength = strings.ToLower(strings.TrimSpace(req.TargetLength))
		if req.TargetLength != "short" && req.TargetLength != "medium" && req.TargetLength != "long" {
			req.TargetLength = "short"
		}
	}
	if strings.TrimSpace(req.Era) == "" {
		req.Era = defaultScripterEra
	} else {
		req.Era = strings.TrimSpace(req.Era)
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Theme = strings.TrimSpace(req.Theme)
	req.Brief = strings.TrimSpace(req.Brief)
	req.Salt = strings.TrimSpace(req.Salt)
	return req
}

func (r *scripterRoom) prepareContext() {
	r.npcBlacklist = loadRecentNPCNameBlacklist(200)
	r.titleSamples = loadScenarioTitleSamples(80)
	log.Printf("[scripter] context prepared npc_blacklist=%d title_samples=%d", len(r.npcBlacklist), len(r.titleSamples))
	if len(r.npcBlacklist) > 0 {
		log.Printf("[scripter] npc blacklist sample=%s", truncateRunes(strings.Join(r.npcBlacklist, ", "), 500))
	}
	if len(r.titleSamples) > 0 {
		log.Printf("[scripter] title samples=%s", truncateRunes(strings.Join(r.titleSamples, ", "), 500))
	}
}

func (r *scripterRoom) Run(ctx context.Context) (ScenarioCreationOutput, error) {
	r.prepareContext()
	if ctx.Err() != nil {
		return ScenarioCreationOutput{}, ctx.Err()
	}
	reqJSON, _ := json.Marshal(r.req)
	log.Printf("[scripter] sandbox generation start req=%s", reqJSON)

	log.Printf("[scripter] stage=constraints start")
	constraints := r.buildConstraints(ctx)
	log.Printf("[scripter] stage=constraints done archetype=%q entry=%q topology=%q phase=%q geography=%q", constraints.SituationArchetype, constraints.InvestigatorEntryPosition, constraints.FactionTopology, constraints.TemporalPhase, strings.Join(constraints.GeographyFlavor, " → "))
	logScripterArtifact("Pre-generation Constraints", constraints)

	log.Printf("[scripter] stage=irony_core start")
	irony, err := generateIronyCoreWithQA(ctx, r, constraints)
	if err != nil {
		log.Printf("[scripter] stage=irony_core error=%v", err)
		return ScenarioCreationOutput{}, fmt.Errorf("IronyCore 失败: %w", err)
	}
	log.Printf("[scripter] stage=irony_core done delta=%q surface=%q", irony.DeltaOperator, truncateRunes(irony.SurfaceReading, 300))
	logScripterArtifact("Stage 1 IronyCore", irony)

	log.Printf("[scripter] stage=misdirection start")
	misdirection, err := generateMisdirectionWithQA(ctx, r, constraints, irony)
	if err != nil {
		log.Printf("[scripter] stage=misdirection error=%v", err)
		return ScenarioCreationOutput{}, fmt.Errorf("MisdirectionFabric 失败: %w", err)
	}
	log.Printf("[scripter] stage=misdirection done false_lead=%q mythos_anchor=%q factions=%d", truncateRunes(misdirection.FalseLead, 200), truncateRunes(misdirection.MythosAnchor, 200), len(misdirection.Factions))
	logScripterArtifact("Stage 2 MisdirectionFabric", misdirection)

	log.Printf("[scripter] stage=investigation_graph start")
	graph, err := generateInvestigationGraphWithVerification(ctx, r, constraints, irony, misdirection)
	if err != nil {
		log.Printf("[scripter] stage=investigation_graph error=%v", err)
		return ScenarioCreationOutput{}, fmt.Errorf("InvestigationGraph 失败: %w", err)
	}
	log.Printf("[scripter] stage=investigation_graph done nodes=%d resolution=%d", len(graph.Nodes), len(graph.ResolutionNodes))
	logScripterArtifact("Stage 3 InvestigationGraph", graph)

	log.Printf("[scripter] stage=assembly start")
	draft, err := assembleSandboxDraftWithQA(ctx, r, constraints, irony, misdirection, graph)
	if err != nil {
		log.Printf("[scripter] stage=assembly error=%v", err)
		return ScenarioCreationOutput{}, fmt.Errorf("Assembly 失败: %w", err)
	}
	log.Printf("[scripter] stage=assembly done name=%q scenes=%d npcs=%d clues=%d", draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	iterations := 4
	const maxRepairRounds = 3
	for repairRound := 1; repairRound <= maxRepairRounds; repairRound++ {
		issues := validateDraftCompatibility(draft)
		if len(issues) == 0 {
			break
		}
		log.Printf("[scripter] stage=assembly_repair round=%d start issues=%d %v", repairRound, len(issues), issues)
		repaired, repairErr := assembleSandboxDraft(ctx, r, constraints, irony, misdirection, graph, &draft, issues)
		if repairErr != nil {
			log.Printf("[scripter] stage=assembly_repair round=%d failed: %v", repairRound, repairErr)
			break
		}
		draft = repaired
		applyGuardrails(&draft, r.req)
		iterations++
		log.Printf("[scripter] stage=assembly_repair round=%d done name=%q scenes=%d npcs=%d clues=%d", repairRound, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	}
	beforeNormalizeIssues := validateDraftCompatibility(draft)
	log.Printf("[scripter] normalization start pre_issues=%d", len(beforeNormalizeIssues))
	normalizeDraftBeforeReturn(&draft, r.req, constraints, irony, misdirection, graph)
	log.Printf("[scripter] normalization done name=%q players=%d-%d slot=%d scenes=%d npcs=%d clues=%d partial_wins=%d", draft.Name, draft.MinPlayers, draft.MaxPlayers, draft.Content.GameStartSlot, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues), len(draft.Content.PartialWins))
	if issues := validateDraftCompatibility(draft); len(issues) > 0 {
		log.Printf("[scripter] draft compatibility issues after normalization: %v", issues)
	}
	log.Printf("[scripter] sandbox draft name=%q scenes=%d npcs=%d clues=%d", draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	logScripterArtifact("Stage 4 ScenarioDraft", draft)

	return ScenarioCreationOutput{Draft: draft, IronyCore: &irony, Iterations: iterations}, nil
}

// ---------------------------------------------------------------------------
// Pre-generation structural constraints
// ---------------------------------------------------------------------------

type ScripterConstraints struct {
	SituationArchetype        string   `json:"situation_archetype"`
	InvestigatorEntryPosition string   `json:"investigator_entry_position"`
	FactionTopology           string   `json:"faction_topology"`
	TemporalPhase             string   `json:"temporal_phase"`
	Era                       string   `json:"era"`
	Theme                     string   `json:"theme"`
	GeographyFlavor           []string `json:"geography_flavor"`
	TargetLength              string   `json:"target_length"`
	PlayerRange               string   `json:"player_range"`
	Difficulty                string   `json:"difficulty"`
}

var situationArchetypeCandidates = []string{
	"过程失控型：某件没人打算让它发生的事情正在失控，各方都想止损但互相干扰",
	"秘密分割型：每个相关方只掌握部分真相，调查员可能是第一个拼出全貌的人",
	"倒计时型：某件事将在固定时间发生，问题是能否提前改变条件",
	"事后处理型：坏事已经发生，各方正在管理后果、掩盖痕迹或争夺残局",
	"变形进行型：人、地点、关系或组织正在变化，不同的人对变化有不同立场",
	"资源争夺型：多方争夺同一件有限的东西，调查员进场时争夺已经开始",
}

var investigatorEntryCandidates = []string{
	"外部调查员：局外人被委托调查，必须有清楚的介入动机",
	"意外卷入者：错误时间出现在错误地点，第一刻就没有安全退出",
	"单方招募者：一个派系雇用或请求他们，初始立场被框住",
	"当事参与者：他们本身与旧决定或当前后果有关联",
	"被驱逐者：有人警告他们离开或保持沉默，压力从入口就存在",
}

var factionTopologyCandidates = []string{
	"三角制衡：三方互相牵制，支持其中一方会得罪另一方",
	"内部裂变：表面一个阵营，内部已分裂，调查员可能触发裂缝",
	"捕食链：强弱关系明显，弱方需要调查员，强方想阻止调查员",
	"表面对立实则合谋：两方看似敌对，实际共享利益",
	"孤立核心：一个主要势力掌控局面，但内部有可利用裂缝",
}

var temporalPhaseCandidates = []string{
	"萌芽期：事情刚开始，线索新鲜但严重性尚不明确",
	"激烈期：局势快速发展，每拖延一天都有新的后果",
	"尾声期：主要事件已经发生，调查员追查后果和幸存者",
}

func (r *scripterRoom) buildConstraints(ctx context.Context) ScripterConstraints {
	seed := seedFromRequest(r.req)
	log.Printf("[scripter] constraints random_seed=%d salt=%q", seed, r.req.Salt)
	rng := rand.New(rand.NewSource(seed))
	geography, err := generateGeographyChain(ctx, r.architect, r.req.Era)
	if err != nil || len(geography) == 0 {
		if err != nil {
			log.Printf("[scripter] geography flavor generation failed: %v", err)
		} else {
			log.Printf("[scripter] geography flavor generation returned empty chain")
		}
		geography = fallbackGeographyFlavor(r.req)
		log.Printf("[scripter] geography fallback=%q", strings.Join(geography, " → "))
	} else {
		log.Printf("[scripter] geography generated=%q", strings.Join(geography, " → "))
	}
	return ScripterConstraints{
		SituationArchetype:        pickCandidate(rng, situationArchetypeCandidates),
		InvestigatorEntryPosition: pickCandidate(rng, investigatorEntryCandidates),
		FactionTopology:           pickCandidate(rng, factionTopologyCandidates),
		TemporalPhase:             pickCandidate(rng, temporalPhaseCandidates),
		Era:                       r.req.Era,
		Theme:                     firstNonEmpty(r.req.Theme, ""),
		GeographyFlavor:           geography,
		TargetLength:              r.req.TargetLength,
		PlayerRange:               fmt.Sprintf("%d-%d", r.req.MinPlayers, r.req.MaxPlayers),
		Difficulty:                r.req.Difficulty,
	}
}

func seedFromRequest(req ScenarioCreationRequest) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(req.Salt))
	_, _ = h.Write([]byte("\x00" + req.Name + "\x00" + req.Theme + "\x00" + req.Era + "\x00" + req.Brief + "\x00" + req.TargetLength))
	sum := int64(h.Sum64())
	if sum == 0 {
		return 1
	}
	return sum
}

func pickCandidate(rng *rand.Rand, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	return candidates[rng.Intn(len(candidates))]
}

var geographyElementSystemPrompt = `<role>事件发生地候选列举器</role>
<task>根据用户给定阶段列举50个可用于事件发生地的候选。该结果只作为布景风味，不决定剧情结构。</task>
<rules>
- country阶段输出具体国家或具体政权范围。
- settlement_scale阶段必须且只能从以下固定选项中选择一个：大都会、城市、市郊、乡镇、无人区。
- 非country阶段只输出类型/形态/区位模式，不输出具体地名、真实行政区名、真实城市名或真实街区名。
- natural_geography阶段必须输出自然地理/地形/水文/气候约束类型。
- human_geography阶段必须输出人口密度/当地风俗文化/社会结构。
- 只输出现实地理/人文地理候选，不输出幕后真相。
- 禁止输出伪科学、高科技、工程化异常或可诱导伪科学解释神话的候选。
- 除settlement_scale阶段只输出一个固定选项外，其他阶段每行一个名称，正好50个，不要编号、解释、标题或描述句。</rules>`

func generateGeographyChain(ctx context.Context, architect agentHandle, era string) ([]string, error) {
	if architect.provider == nil {
		return nil, fmt.Errorf("architect provider unavailable")
	}
	log.Printf("[scripter:geography] start era=%q", era)
	stages := []struct {
		Key      string
		Mode     string
		Examples string
	}{
		{Key: "country", Mode: "具体国家或具体政权范围", Examples: "美国"},
		{Key: "settlement_scale", Mode: "根据前置布景和时代，从固定选项中选择最适合调查沙盒的聚落尺度：大都会、城市、市郊、乡镇、无人区。只输出一个选项", Examples: "城市"},
		{Key: "natural_geography", Mode: "自然地理/地形/水文/气候约束类型，不输出具体地名", Examples: "林木覆盖的山谷"},
		{Key: "human_geography", Mode: "人口密度/当地风俗文化/社会结构，不输出具体地名", Examples: "港口工人社区"},
	}
	chain := make([]string, 0, len(stages))
	msgs := []llm.ChatMessage{{Role: "system", Content: architect.systemPrompt(geographyElementSystemPrompt)}}
	for _, stage := range stages {
		log.Printf("[scripter:geography] stage=%q selected_so_far=%q", stage.Key, strings.Join(chain, " → "))
		items, err := generateGeographyCandidates(ctx, architect, &msgs, era, stage.Key, stage.Mode, stage.Examples, chain)
		if err != nil {
			log.Printf("[scripter:geography] stage=%q error=%v", stage.Key, err)
			return chain, err
		}
		if len(items) == 0 {
			return chain, fmt.Errorf("%s 候选为空", stage.Key)
		}
		choice := ""
		if stage.Key == "settlement_scale" {
			items = filterSettlementScaleCandidates(items)
			if len(items) == 0 {
				items = []string{"城市"}
			}
			choice = items[0]
		} else {
			choice = items[rand.Intn(len(items))]
		}
		chain = append(chain, choice)
		log.Printf("[scripter] geography stage=%q candidates=%d chosen=%q", stage.Key, len(items), choice)
	}
	return chain, nil
}

func generateGeographyCandidates(ctx context.Context, architect agentHandle, msgs *[]llm.ChatMessage, era string, stageKey string, mode string, examples string, chain []string) ([]string, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	selected := "无，第一轮先选择具体国家或政权范围"
	if len(chain) > 0 {
		selected = strings.Join(chain, " → ")
	}
	countInstruction := "请只输出本阶段的50个候选。"
	if stageKey == "settlement_scale" {
		countInstruction = "请只输出一个最合适的固定选项，必须完全等于：大都会、城市、市郊、乡镇、无人区 之一。"
	}
	prompt := fmt.Sprintf("已随机选中的前置布景：%s\n现在进入下一阶段：%s\n时代：%s\n输出要求：%s\n示例范围：%s\n\n%s", selected, stageKey, era, mode, examples, countInstruction)
	log.Printf("[scripter:geography] prompt stage=%q len=%d body=%s", stageKey, len(prompt), truncateRunes(prompt, scripterPromptLogLimit))
	*msgs = append(*msgs, llm.ChatMessage{Role: "user", Content: prompt})
	raw, err := architect.provider.Chat(ctx, *msgs)
	if err != nil {
		log.Printf("[scripter:geography] chat error stage=%q err=%v", stageKey, err)
		return nil, err
	}
	log.Printf("[scripter:geography] raw stage=%q len=%d body=%s", stageKey, len(raw), truncateRunes(raw, scripterRawLogLimit))
	*msgs = append(*msgs, llm.ChatMessage{Role: "assistant", Content: raw})
	items := parseElementNames(raw)
	log.Printf("[scripter:geography] parsed stage=%q count=%d items=%q", stageKey, len(items), strings.Join(items, " | "))
	if len(items) == 0 {
		log.Printf("[scripter:geography] parse empty stage=%q raw=%s", stageKey, truncateRunes(raw, scripterRawLogLimit))
		return nil, fmt.Errorf("地理候选列表为空")
	}
	return items, nil
}

func fallbackGeographyFlavor(req ScenarioCreationRequest) []string {
	flavor := []string{firstNonEmpty(req.Era, defaultScripterEra), "城市"}
	if strings.TrimSpace(req.Theme) != "" {
		flavor = append(flavor, strings.TrimSpace(req.Theme))
	}
	flavor = append(flavor, "具备地方关系、交通阻力和可调查公共空间的地点")
	return flavor
}

func settlementScaleCandidates() []string {
	return []string{"大都会", "城市", "市郊", "乡镇", "无人区"}
}

func filterSettlementScaleCandidates(items []string) []string {
	allowed := map[string]bool{}
	for _, item := range settlementScaleCandidates() {
		allowed[item] = true
	}
	filtered := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if allowed[item] {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func parseElementNames(raw string) []string {
	raw = llm.StripCodeFence(strings.TrimSpace(raw))
	raw = strings.ReplaceAll(raw, "，", "\n")
	raw = strings.ReplaceAll(raw, ",", "\n")
	raw = strings.ReplaceAll(raw, "、", "\n")
	lines := strings.Split(raw, "\n")
	items := make([]string, 0, len(lines))
	seen := map[string]bool{}
	for _, line := range lines {
		name := normalizeElementName(line)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, name)
	}
	return items
}

func normalizeElementName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "-•*· ")
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, ".、)"); idx >= 0 && idx <= 4 {
		prefix := strings.TrimSpace(s[:idx])
		if prefix != "" {
			allDigits := true
			for _, r := range prefix {
				if r < '0' || r > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				s = strings.TrimSpace(s[idx+1:])
			}
		}
	}
	s = strings.Trim(s, " `\"'，。；;：:（）()【】[]《》")
	if s == "" || strings.Contains(s, "：") || strings.Contains(s, ":") {
		return ""
	}
	if len([]rune(s)) > 40 {
		return ""
	}
	return strings.TrimSpace(s)
}

func sandboxQARejectReasons(qa *SandboxQA) []string {
	if qa == nil {
		return nil
	}
	if len(qa.RejectReasons) > 0 {
		return qa.RejectReasons
	}
	if strings.TrimSpace(qa.Reason) != "" {
		return []string{strings.TrimSpace(qa.Reason)}
	}
	return []string{"未给出拒绝原因"}
}

// formatSandboxMustFix 将 SandboxQA 的拒绝原因和建议格式化为编号行动列表，
// 供 feedRejection 注入 architect 消息，替代纯 JSON 审核结果。
func formatSandboxMustFix(qa SandboxQA) string {
	var sb strings.Builder
	reasons := qa.RejectReasons
	if len(reasons) == 0 && strings.TrimSpace(qa.Reason) != "" {
		reasons = []string{strings.TrimSpace(qa.Reason)}
	}
	for i, r := range reasons {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
	}
	if fix := strings.TrimSpace(qa.SuggestedFix); fix != "" {
		sb.WriteString("建议：" + fix)
	}
	return strings.TrimSpace(sb.String())
}

// ---------------------------------------------------------------------------
// Shared sandbox QA types and loop (Stage 2-4, structural/semantic only)
// ---------------------------------------------------------------------------

// SandboxQA is the verdict type for Stage 2, 3, and 4 QA sessions.
// Unlike FoundationSeedQA it contains no rulebook check summary.
type SandboxQA struct {
	Pass          bool     `json:"pass"`
	Reason        string   `json:"reason"`
	RejectReasons []string `json:"reject_reasons"`
	SuggestedFix  string   `json:"suggested_fix"`
}

type sandboxQAToolCall struct {
	Action        ToolCallType `json:"action"`
	Think         string       `json:"think,omitempty"`
	Pass          bool         `json:"pass,omitempty"`
	Reason        string       `json:"reason,omitempty"`
	RejectReasons []string     `json:"reject_reasons,omitempty"`
	SuggestedFix  string       `json:"suggested_fix,omitempty"`
}

// runSandboxQALoop runs the QA loop for stages 2-4.
// No rulebook tool calls are allowed; only think (optional) and response.
func runSandboxQALoop(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage, toolCallExample string, tag string) (SandboxQA, []llm.ChatMessage, error) {
	if room.qa.provider == nil {
		return SandboxQA{Pass: true, Reason: "qa provider unavailable, skipping"}, msgs, nil
	}
	const maxQARounds = 20
	for round := 1; round <= maxQARounds; round++ {
		if ctx.Err() != nil {
			return SandboxQA{}, msgs, ctx.Err()
		}
		logStagePrompt(fmt.Sprintf("%s_qa_round_%d", tag, round), msgs)
		raw, err := room.qa.provider.Chat(ctx, msgs)
		if err != nil {
			return SandboxQA{}, msgs, err
		}
		log.Printf("[scripter:%s_qa] round=%d raw_len=%d raw=%s", tag, round, len(raw), truncateRunes(raw, scripterRawLogLimit))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})
		calls, parseErr := parseSandboxQAToolCalls(ctx, room.parser, raw, toolCallExample)
		if parseErr != nil {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: JSON解析失败，必须重新输出合法JSON数组。"})
			continue
		}
		if len(calls) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须输出至少一个工具调用。"})
			continue
		}
		hasResponse := false
		invalidAction := false
		for _, call := range calls {
			switch call.Action {
			case ToolThink:
				// think is optional, silently accepted
			case ToolResponse:
				hasResponse = true
			default:
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf("SYSTEM REJECT: 此QA只允许think/response，不允许%s。", call.Action)})
				invalidAction = true
			}
		}
		if invalidAction {
			continue
		}
		if hasResponse {
			qa, err := sandboxQAFromResponse(calls)
			if err != nil {
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: " + err.Error()})
				continue
			}
			return qa, msgs, nil
		}
	}
	return SandboxQA{}, msgs, fmt.Errorf("%s QA 未在%d轮内给出response", tag, maxQARounds)
}

func parseSandboxQAToolCalls(ctx context.Context, parser agentHandle, raw string, example string) ([]sandboxQAToolCall, error) {
	stripped := strings.TrimSpace(llm.StripCodeFence(llm.JsonArryProtect(raw)))
	var calls []sandboxQAToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil {
		return calls, nil
	} else if parser.provider != nil {
		fixed, repairErr := repairJSONWith(ctx, parser, stripped, err, example)
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

func sandboxQAFromResponse(calls []sandboxQAToolCall) (SandboxQA, error) {
	var response *sandboxQAToolCall
	for i := range calls {
		if calls[i].Action == ToolResponse {
			if response != nil {
				return SandboxQA{}, fmt.Errorf("response只能有一个")
			}
			response = &calls[i]
		}
	}
	if response == nil {
		return SandboxQA{}, fmt.Errorf("缺少response")
	}
	qa := SandboxQA{
		Pass:          response.Pass,
		Reason:        strings.TrimSpace(response.Reason),
		RejectReasons: response.RejectReasons,
		SuggestedFix:  strings.TrimSpace(response.SuggestedFix),
	}
	for i := range qa.RejectReasons {
		qa.RejectReasons[i] = strings.TrimSpace(qa.RejectReasons[i])
	}
	if qa.Reason == "" {
		return SandboxQA{}, fmt.Errorf("response.reason不能为空")
	}
	if !qa.Pass && len(qa.RejectReasons) == 0 {
		qa.RejectReasons = []string{qa.Reason}
	}
	return qa, nil
}

type FactionPlan struct {
	Name         string         `json:"name"`
	Goal         string         `json:"goal"`
	CurrentState string         `json:"current_state"`
	Timeline     []TimelineNode `json:"timeline"`
	NPCs         []FactionNPC   `json:"npcs"`
}

type TimelineNode struct {
	Node              string `json:"node"`
	Trigger           string `json:"trigger"`
	InterventionPivot string `json:"intervention_pivot"`
}

type FactionNPC struct {
	Name           string `json:"name"`
	PublicIdentity string `json:"public_identity"`
	Agenda         string `json:"agenda"`
	Secret         string `json:"secret"`
	Attitude       string `json:"attitude"`
	StatsNote      string `json:"stats_note"`
}

func containsUncertaintyNote(notes []string) bool {
	for _, note := range notes {
		if strings.Contains(note, "不确定") || strings.Contains(note, "未确认") || strings.Contains(strings.ToLower(note), "uncertain") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Stage 4: Assembly (compile δ-framework artifacts into ScenarioDraft)
// ---------------------------------------------------------------------------

const scenarioExample = `{"name":"示例沙盒模组名","description":"一份围绕异常事实、派系时间线和调查员可拉动杠杆展开的COC情境简报。","author":"agent-team","tags":"sandbox,coc","min_players":1,"max_players":4,"difficulty":"normal","content":{"system_prompt":"你是本场COC跑团的KP，职责是管理一个会自行推进的局势而不是执行线性故事。按派系时间线推进后果；按可见性分层给信息；不要主动把调查员引向正确答案。","setting":"玩家抵达时能看见的当前局势。只写公开事实、紧张关系和可感知异常，不剧透幕后真相。","intro":"你们以某种身份进入局势，眼前有三件可立即行动的事：询问某人、检查某地、决定是否公开某条信息。","game_start_slot":16,"map_description":"【文字地图】起点A连接地点B和地点C；地点B有公开冲突，地点C有可深入调查的物件；各地点可往返，没有固定访问顺序。","scenes":[{"id":"location_1","name":"地点名","description":"可见：到达即可看到的信息。可发现：主动调查可获得的信息。杠杆：调查员若做X，派系Y的时间线会改变。风险：拖延或失败的后果。出口：可前往的其他地点。","triggers":["available_from_start"]}],"npcs":[{"name":"NPC姓名","description":"公开身份；所属派系；真实议程；秘密；可被说服或施压的杠杆；可能知道但不会主动说出的事实。","attitude":"初始态度和压力下的反应","stats":{"STR":50,"CON":50,"SIZ":50,"DEX":50,"APP":50,"INT":60,"POW":50,"EDU":60,"HP":10,"MP":10}}],"clues":["[真实]登记簿矛盾(旧办公室): 自包含事实；获取方式；能改变哪个判断。","[隐藏]神话本质(封存物件): 神话核心真相（实体性质/典籍目的/仪式代价）；获取方式（需要什么技能或条件）；发现后要承担的代价（理智/后果）。","[误导]地方传闻(酒馆): 为什么它表面合理但只能解释一部分。"],"win_condition":"如果调查员让某个结束信号以较低代价固化，则对应派系失去关键优势，但另一个代价留存。","lose_condition":"如果关键时间线终点到达且无人干预，则局势进入新的稳定态，某人或某地不可挽回地改变。","partial_wins":["如果只救出某人但没有公开事实，则个人获救，派系结构保留。"]}}`

const assemblySystemPrompt = `<role>COC7沙盒剧本编译器</role>
<task>将三阶段设计产物（揭示结构、误导设计、调查路径图）编译为完整的COC7沙盒剧本JSON（ScenarioDraft）。不要查询规则书，不要更换已确认的神话元素（mythos_anchor）。</task>
<response_format>json_object</response_format>
<output>只输出合法JSON对象，不要Markdown、标题、解释或代码围栏。</output>
<director_contract>
- content.setting写玩家当前能看见的局势（irony.surface_reading的视角），不是幕后真实揭示（irony.deep_truth）。
- content.intro写入场位置和立即可做的行动，基于graph.hook_node附近地点。
- content.map_description基于graph.nodes中的地点节点写可导航关系。
- content.scenes是地点/局势状态摘要，对应InvNode；description必须包含可见信息、可发现信息、杠杆、风险、出口。
- content.npcs来自misdirection.factions中的NPC；misdirection.misdirector_npc必须包含在内。
- content.clues是自包含事实字符串，必须以[真实]/[隐藏]/[误导]开头；delta_signal=false_delta→[误导]；delta_signal=true_delta→[隐藏]；delta_signal=ambiguous→[真实]。
- content.clues中必须包含至少一条以'[隐藏]神话本质'开头的线索，涵盖misdirection.mythos_anchor所描述的神话元素。
- content.clues中至少一条[误导]线索在irony.deep_truth揭示后事后仍能合理解释（不能是在真相下完全矛盾的线索）。
- win_condition/lose_condition/partial_wins使用misdirection.ending_signals。
- system_prompt给KP时间推进、信息可见性分层和不主动引导三项协议；同时注入irony.deep_truth作为KP独有内部真相。
- 如果收到qa_rejection，必须修复标注问题；不要只改措辞；不要更换已确认的神话元素（mythos_anchor）。
</director_contract>`

const assemblyQASystemPrompt = `<role>COC7沙盒剧本编译QA</role>
<task>审核ScenarioDraft是否符合沙盒设计原则，不审核规则书内容。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：内部推理（可选，无副作用）
  {"action":"think","think":"推理内容"}
- response：最终审核结论。
  {"action":"response","pass":true,"reason":"审核理由","reject_reasons":[],"suggested_fix":"给architect的具体修改方向"}
</tools>
<audit_rules>
审核六点，任意一点不满足则pass=false，reject_reasons必须逐条列出：
1. setting只描述当前可见局势（irony.surface_reading视角），未提前泄露irony.deep_truth或misdirection.true_trace。
2. intro包含至少三个基于hook_node附近地点、立即可执行的具体行动。
3. 每个scene描述必须包含：可见信息、主动调查可发现的信息、杠杆（调查员的选择如何影响派系时间线）、风险、出口。
4. clues：每条以[真实]/[隐藏]/[误导]开头；至少一条[隐藏]线索涵盖misdirection.mythos_anchor；至少一条[误导]线索在irony.deep_truth揭示后事后仍能合理解释。
5. content.system_prompt包含三项KP协议（时间推进、信息可见性分层、不主动引导），并注入了irony.deep_truth作为KP内部真相。
6. win_condition/lose_condition使用条件句结构（"如果[条件]，则[处境变化]，[什么不可挽回地改变]"），不是"成功/失败"二元裁定。
不审核：规则书准确性、神话元素细节。
</audit_rules>`

const assemblyQAToolCallExample = `[{"action":"response","pass":true,"reason":"setting只含表面阅读层，intro提供了3个具体行动，每个scene有五项结构，clues包含[隐藏]神话本质和在deep_truth下仍可解释的[误导]线索，system_prompt含三项KP协议及deep_truth注入。","reject_reasons":[],"suggested_fix":"无需修改。"}]`

func assembleSandboxDraft(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, irony IronyCore, misdirection MisdirectionFabric, graph InvestigationGraph, previous *ScenarioDraft, mustFix []string) (ScenarioDraft, error) {
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)
	ironyJSON, _ := json.Marshal(irony)
	misdirectionJSON, _ := json.Marshal(misdirection)
	graphJSON, _ := json.Marshal(graph)
	var userParts []string
	userParts = append(userParts, fmt.Sprintf("【请求参数】%s", reqJSON))
	userParts = append(userParts, fmt.Sprintf("【结构约束】%s", constraintsJSON))
	userParts = append(userParts, fmt.Sprintf("【Stage1 IronyCore】%s", ironyJSON))
	userParts = append(userParts, fmt.Sprintf("【Stage2 MisdirectionFabric】%s", misdirectionJSON))
	userParts = append(userParts, fmt.Sprintf("【Stage3 InvestigationGraph】%s", graphJSON))
	userParts = append(userParts, fmt.Sprintf("【输出示例（仅格式参考）】%s", scenarioExample))
	userParts = append(userParts, fmt.Sprintf("【规模/难度】长度：%s\n难度：%s", lengthSpec(room.req.TargetLength), difficultySpec(room.req.Difficulty)))
	userParts = append(userParts, fmt.Sprintf("【NPC名称黑名单】%s", formatNPCNameBlacklist(room.npcBlacklist)))
	userParts = append(userParts, fmt.Sprintf("【剧本标题黑名单】%s", formatScenarioTitleBlacklist(room.titleSamples)))
	if previous != nil && len(mustFix) > 0 {
		prevJSON, _ := json.Marshal(previous)
		userParts = append(userParts, fmt.Sprintf("【上一版本草案】%s", prevJSON))
		userParts = append(userParts, fmt.Sprintf("【必须修复（按编号全部处理）】\n%s", strings.Join(mustFix, "\n")))
	}
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(assemblySystemPrompt)},
		{Role: "user", Content: strings.Join(userParts, "\n\n")},
	}
	logStagePrompt("assembly", msgs)
	var draft ScenarioDraft
	if err := chatAndParseJSON(ctx, room.architect, room.parser, msgs, &draft, scenarioExample, "assembly"); err != nil {
		return ScenarioDraft{}, fmt.Errorf("assembly chatAndParseJSON: %w", err)
	}
	logParsedJSON("assembly_draft", draft)
	applyGuardrails(&draft, room.req)
	return draft, nil
}

// ---------------------------------------------------------------------------
// Assembly QA session
// ---------------------------------------------------------------------------

type assemblySession struct {
	room          *scripterRoom
	architectMsgs []llm.ChatMessage
	qaMsgs        []llm.ChatMessage
}

func newAssemblySession(room *scripterRoom, constraints ScripterConstraints, irony IronyCore, misdirection MisdirectionFabric, graph InvestigationGraph) *assemblySession {
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)
	ironyJSON, _ := json.Marshal(irony)
	misdirectionJSON, _ := json.Marshal(misdirection)
	graphJSON, _ := json.Marshal(graph)
	architectPrompt := fmt.Sprintf(
		"【请求参数】%s\n\n【结构约束】%s\n\n【Stage1 IronyCore】%s\n\n【Stage2 MisdirectionFabric】%s\n\n【Stage3 InvestigationGraph】%s\n\n【输出示例（仅格式参考）】%s\n\n【规模/难度】长度：%s\n难度：%s\n\n【NPC名称黑名单】%s\n\n【剧本标题黑名单】%s\n\n请生成第1版ScenarioDraft。",
		string(reqJSON), string(constraintsJSON), string(ironyJSON), string(misdirectionJSON), string(graphJSON),
		scenarioExample, lengthSpec(room.req.TargetLength), difficultySpec(room.req.Difficulty),
		formatNPCNameBlacklist(room.npcBlacklist), formatScenarioTitleBlacklist(room.titleSamples))
	qaPrompt := fmt.Sprintf(
		"【Stage1 IronyCore】%s\n\n【Stage2 MisdirectionFabric】%s\n\n【Stage3 InvestigationGraph】%s\n\n你是持续运行的剧本编译QA会话。每次收到<draft_candidate>后，通过think/response工具调用审核它。",
		string(ironyJSON), string(misdirectionJSON), string(graphJSON))
	return &assemblySession{
		room: room,
		architectMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.architect.systemPrompt(assemblySystemPrompt)},
			{Role: "user", Content: architectPrompt},
		},
		qaMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.qa.systemPrompt(assemblyQASystemPrompt)},
			{Role: "user", Content: qaPrompt},
		},
	}
}

func (s *assemblySession) generate(ctx context.Context, attempt int) (ScenarioDraft, error) {
	logStagePrompt(fmt.Sprintf("assembly_attempt_%d", attempt), s.architectMsgs)
	var draft ScenarioDraft
	if err := chatAndParseJSON(ctx, s.room.architect, s.room.parser, s.architectMsgs, &draft, scenarioExample, fmt.Sprintf("assembly#%d", attempt)); err != nil {
		return ScenarioDraft{}, err
	}
	draftJSON, _ := json.Marshal(draft)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{Role: "assistant", Content: string(draftJSON)})
	log.Printf("[scripter:assembly] attempt=%d generated name=%q scenes=%d npcs=%d clues=%d", attempt, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	return draft, nil
}

func (s *assemblySession) review(ctx context.Context, attempt int, draft ScenarioDraft) (SandboxQA, error) {
	draftJSON, _ := json.Marshal(draft)
	s.qaMsgs = append(s.qaMsgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(`<draft_candidate attempt="%d">%s</draft_candidate>请用think/response工具调用审核这个候选。`, attempt, string(draftJSON))})
	logStagePrompt(fmt.Sprintf("assembly_qa_attempt_%d", attempt), s.qaMsgs)
	qa, msgs, err := runSandboxQALoop(ctx, s.room, s.qaMsgs, assemblyQAToolCallExample, "assembly")
	s.qaMsgs = msgs
	return qa, err
}

func (s *assemblySession) feedRejection(attempt int, draft ScenarioDraft, qa SandboxQA) {
	draftJSON, _ := json.Marshal(draft)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(`<qa_rejection attempt="%d">
<rejected_draft>%s</rejected_draft>
<must_fix>
%s
</must_fix>
</qa_rejection>
请基于同一创作上下文重写ScenarioDraft：逐条解决must_fix列出的问题；不要只改措辞；不要更换已确认的神话元素（mythos_anchor）；仍只输出合法JSON对象。`, attempt, string(draftJSON), formatSandboxMustFix(qa))})
}

func assembleSandboxDraftWithQA(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, irony IronyCore, misdirection MisdirectionFabric, graph InvestigationGraph) (ScenarioDraft, error) {
	session := newAssemblySession(room, constraints, irony, misdirection, graph)
	const maxAttempts = 10
	var lastQA *SandboxQA
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		draft, err := session.generate(ctx, attempt)
		if err != nil {
			return ScenarioDraft{}, err
		}
		applyGuardrails(&draft, room.req)
		qa, err := session.review(ctx, attempt, draft)
		if err != nil {
			return ScenarioDraft{}, err
		}
		lastQA = &qa
		log.Printf("[scripter:assembly_qa] attempt=%d pass=%v reason=%q rejects=%q", attempt, qa.Pass, truncateRunes(qa.Reason, 500), strings.Join(qa.RejectReasons, " | "))
		logScripterArtifact(fmt.Sprintf("Stage 4 Assembly QA Attempt %d", attempt), qa)
		if qa.Pass {
			return draft, nil
		}
		session.feedRejection(attempt, draft, qa)
	}
	rejects := sandboxQARejectReasons(lastQA)
	return ScenarioDraft{}, fmt.Errorf("Assembly QA 连续拒绝 %d 次，拒绝原因=%v", maxAttempts, rejects)
}

// ---------------------------------------------------------------------------
// Assembly guardrails and validation helpers
// ---------------------------------------------------------------------------

func logScripterArtifact(stage string, artifact any) {
	bs, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		log.Printf("[scripter-artifact] %s marshal failed: %v", stage, err)
		return
	}
	log.Printf("[scripter-artifact] %s len=%d\n%s", stage, len(bs), string(bs))
}

func logStagePrompt(tag string, msgs []llm.ChatMessage) {
	log.Printf("[scripter:%s] prompt messages=%d", tag, len(msgs))
	for i, msg := range msgs {
		log.Printf("[scripter:%s] prompt[%d] role=%s len=%d body=%s", tag, i, msg.Role, len(msg.Content), truncateRunes(msg.Content, scripterPromptLogLimit))
	}
}

func logParsedJSON(tag string, value any) {
	bs, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		log.Printf("[scripter:%s] parsed JSON marshal failed: %v", tag, err)
		return
	}
	log.Printf("[scripter:%s] parsed JSON len=%d body=%s", tag, len(bs), truncateRunes(string(bs), scripterRawLogLimit))
}

func validateDraftCompatibility(draft ScenarioDraft) []string {
	var issues []string
	if strings.TrimSpace(draft.Name) == "" {
		issues = append(issues, "ScenarioDraft.name 为空")
	}
	if strings.TrimSpace(draft.Description) == "" {
		issues = append(issues, "ScenarioDraft.description 为空")
	}
	if strings.TrimSpace(draft.Difficulty) == "" {
		issues = append(issues, "ScenarioDraft.difficulty 为空")
	}
	content := draft.Content
	if strings.TrimSpace(content.SystemPrompt) == "" {
		issues = append(issues, "content.system_prompt 为空")
	}
	if strings.TrimSpace(content.Setting) == "" {
		issues = append(issues, "content.setting 为空")
	}
	if strings.TrimSpace(content.Intro) == "" {
		issues = append(issues, "content.intro 为空")
	}
	if content.GameStartSlot < 0 || content.GameStartSlot > 47 {
		issues = append(issues, "content.game_start_slot 必须在0-47之间")
	}
	if strings.TrimSpace(content.MapDescription) == "" {
		issues = append(issues, "content.map_description 为空")
	}
	if len(content.Scenes) == 0 {
		issues = append(issues, "content.scenes 为空")
	}
	for i, scene := range content.Scenes {
		if strings.TrimSpace(scene.ID) == "" || strings.TrimSpace(scene.Name) == "" || strings.TrimSpace(scene.Description) == "" {
			issues = append(issues, fmt.Sprintf("content.scenes[%d] 缺少id/name/description", i))
		}
	}
	if len(content.NPCs) == 0 {
		issues = append(issues, "content.npcs 为空")
	}
	for i, npc := range content.NPCs {
		if strings.TrimSpace(npc.Name) == "" || strings.TrimSpace(npc.Description) == "" || strings.TrimSpace(npc.Attitude) == "" {
			issues = append(issues, fmt.Sprintf("content.npcs[%d] 缺少name/description/attitude", i))
		}
	}
	if len(content.Clues) == 0 {
		issues = append(issues, "content.clues 为空")
	}
	for i, clue := range content.Clues {
		clue = strings.TrimSpace(clue)
		if !(strings.HasPrefix(clue, "[真实]") || strings.HasPrefix(clue, "[隐藏]") || strings.HasPrefix(clue, "[误导]")) {
			issues = append(issues, fmt.Sprintf("content.clues[%d] 缺少[真实]/[隐藏]/[误导]前缀", i))
		}
	}
	if strings.TrimSpace(content.WinCondition) == "" {
		issues = append(issues, "content.win_condition 为空")
	}
	if strings.TrimSpace(content.LoseCondition) == "" {
		issues = append(issues, "content.lose_condition 为空")
	}
	return issues
}

func applyGuardrails(draft *ScenarioDraft, req ScenarioCreationRequest) {
	if draft == nil {
		return
	}
	if strings.TrimSpace(req.Name) != "" && draft.Name != strings.TrimSpace(req.Name) {
		log.Printf("[scripter:guardrails] override name from=%q to=%q", draft.Name, strings.TrimSpace(req.Name))
		draft.Name = strings.TrimSpace(req.Name)
	}
	if req.MinPlayers > 0 && draft.MinPlayers != req.MinPlayers {
		log.Printf("[scripter:guardrails] override min_players from=%d to=%d", draft.MinPlayers, req.MinPlayers)
		draft.MinPlayers = req.MinPlayers
	}
	if req.MaxPlayers > 0 && draft.MaxPlayers != req.MaxPlayers {
		log.Printf("[scripter:guardrails] override max_players from=%d to=%d", draft.MaxPlayers, req.MaxPlayers)
		draft.MaxPlayers = req.MaxPlayers
	}
	if draft.MaxPlayers > 0 && draft.MinPlayers > 0 && draft.MaxPlayers < draft.MinPlayers {
		log.Printf("[scripter:guardrails] clamp max_players from=%d to min_players=%d", draft.MaxPlayers, draft.MinPlayers)
		draft.MaxPlayers = draft.MinPlayers
	}
	if strings.TrimSpace(req.Difficulty) != "" && draft.Difficulty != strings.TrimSpace(req.Difficulty) {
		log.Printf("[scripter:guardrails] override difficulty from=%q to=%q", draft.Difficulty, strings.TrimSpace(req.Difficulty))
		draft.Difficulty = strings.TrimSpace(req.Difficulty)
	}
	if strings.TrimSpace(draft.Author) == "" {
		log.Printf("[scripter:guardrails] default author=%q", defaultScripterAuthor)
		draft.Author = defaultScripterAuthor
	}
}

func normalizeDraftBeforeReturn(draft *ScenarioDraft, req ScenarioCreationRequest, constraints ScripterConstraints, irony IronyCore, misdirection MisdirectionFabric, graph InvestigationGraph) {
	if draft == nil {
		return
	}
	if strings.TrimSpace(draft.Name) == "" {
		draft.Name = defaultScenarioName(irony)
		log.Printf("[scripter:normalize] filled name=%q", draft.Name)
	}
	if strings.TrimSpace(draft.Description) == "" {
		draft.Description = fmt.Sprintf("围绕「%s」展开的沙盒调查：调查员进入一个由δ结构驱动的局势，表象与深层真相由一个可逆转的认知算子分隔。", irony.SurfaceReading)
		log.Printf("[scripter:normalize] filled description=%q", truncateRunes(draft.Description, 300))
	}
	if strings.TrimSpace(draft.Author) == "" {
		draft.Author = defaultScripterAuthor
		log.Printf("[scripter:normalize] filled author=%q", draft.Author)
	}
	if strings.TrimSpace(draft.Tags) == "" {
		draft.Tags = strings.Join(nonEmptyStrings("sandbox", "coc", constraints.Theme, constraints.TemporalPhase), ",")
		log.Printf("[scripter:normalize] filled tags=%q", draft.Tags)
	}
	if draft.MinPlayers <= 0 {
		draft.MinPlayers = req.MinPlayers
		log.Printf("[scripter:normalize] filled min_players from request=%d", draft.MinPlayers)
	}
	if draft.MinPlayers <= 0 {
		draft.MinPlayers = 1
		log.Printf("[scripter:normalize] default min_players=1")
	}
	if draft.MaxPlayers <= 0 {
		draft.MaxPlayers = req.MaxPlayers
		log.Printf("[scripter:normalize] filled max_players from request=%d", draft.MaxPlayers)
	}
	if draft.MaxPlayers <= 0 {
		draft.MaxPlayers = 4
		log.Printf("[scripter:normalize] default max_players=4")
	}
	if draft.MaxPlayers < draft.MinPlayers {
		log.Printf("[scripter:normalize] clamp max_players from=%d to min_players=%d", draft.MaxPlayers, draft.MinPlayers)
		draft.MaxPlayers = draft.MinPlayers
	}
	if strings.TrimSpace(draft.Difficulty) == "" {
		draft.Difficulty = firstNonEmpty(req.Difficulty, "normal")
		log.Printf("[scripter:normalize] filled difficulty=%q", draft.Difficulty)
	}
	if draft.Content.GameStartSlot < 0 {
		log.Printf("[scripter:normalize] clamp game_start_slot from=%d to=0", draft.Content.GameStartSlot)
		draft.Content.GameStartSlot = 0
	}
	if draft.Content.GameStartSlot > 47 {
		log.Printf("[scripter:normalize] clamp game_start_slot from=%d to=47", draft.Content.GameStartSlot)
		draft.Content.GameStartSlot = 47
	}
	if strings.TrimSpace(draft.Content.SystemPrompt) == "" {
		draft.Content.SystemPrompt = defaultSandboxSystemPrompt(misdirection, irony)
		log.Printf("[scripter:normalize] filled system_prompt len=%d", len(draft.Content.SystemPrompt))
	}
	if strings.TrimSpace(draft.Content.Setting) == "" {
		draft.Content.Setting = defaultSetting(constraints, irony, misdirection)
		log.Printf("[scripter:normalize] filled setting len=%d", len(draft.Content.Setting))
	}
	if strings.TrimSpace(draft.Content.Intro) == "" {
		draft.Content.Intro = defaultIntro(constraints, graph)
		log.Printf("[scripter:normalize] filled intro len=%d", len(draft.Content.Intro))
	}
	if strings.TrimSpace(draft.Content.MapDescription) == "" {
		draft.Content.MapDescription = defaultMapDescription(graph)
		log.Printf("[scripter:normalize] filled map_description len=%d", len(draft.Content.MapDescription))
	}
	if len(draft.Content.Scenes) == 0 {
		draft.Content.Scenes = scenesFromGraph(graph, irony)
		log.Printf("[scripter:normalize] generated scenes count=%d", len(draft.Content.Scenes))
	}
	for i := range draft.Content.Scenes {
		if strings.TrimSpace(draft.Content.Scenes[i].ID) == "" {
			draft.Content.Scenes[i].ID = fmt.Sprintf("location_%d", i+1)
			log.Printf("[scripter:normalize] filled scene[%d].id=%q", i, draft.Content.Scenes[i].ID)
		}
		if strings.TrimSpace(draft.Content.Scenes[i].Name) == "" {
			draft.Content.Scenes[i].Name = fmt.Sprintf("地点%d", i+1)
			log.Printf("[scripter:normalize] filled scene[%d].name=%q", i, draft.Content.Scenes[i].Name)
		}
		if strings.TrimSpace(draft.Content.Scenes[i].Description) == "" {
			draft.Content.Scenes[i].Description = "可见：当前局势的表面信息。可发现：主动调查可获得的事实。杠杆：调查员行动会改变派系时间线。风险：拖延会让世界推进。出口：可前往其他地点。"
			log.Printf("[scripter:normalize] filled scene[%d].description", i)
		}
		if len(draft.Content.Scenes[i].Triggers) == 0 {
			draft.Content.Scenes[i].Triggers = []string{"available_from_start"}
			log.Printf("[scripter:normalize] filled scene[%d].triggers=%v", i, draft.Content.Scenes[i].Triggers)
		}
	}
	if len(draft.Content.NPCs) == 0 {
		draft.Content.NPCs = npcsFromMisdirection(misdirection)
		log.Printf("[scripter:normalize] generated npcs count=%d", len(draft.Content.NPCs))
	}
	for i := range draft.Content.NPCs {
		if strings.TrimSpace(draft.Content.NPCs[i].Name) == "" {
			draft.Content.NPCs[i].Name = fmt.Sprintf("关键NPC%d", i+1)
			log.Printf("[scripter:normalize] filled npc[%d].name=%q", i, draft.Content.NPCs[i].Name)
		}
		if strings.TrimSpace(draft.Content.NPCs[i].Description) == "" {
			draft.Content.NPCs[i].Description = "公开身份、所属派系、真实议程、秘密和可被调查员影响的杠杆。"
			log.Printf("[scripter:normalize] filled npc[%d].description", i)
		}
		if strings.TrimSpace(draft.Content.NPCs[i].Attitude) == "" {
			draft.Content.NPCs[i].Attitude = "谨慎观察调查员，只有在压力或交换下才透露深层信息。"
			log.Printf("[scripter:normalize] filled npc[%d].attitude=%q", i, draft.Content.NPCs[i].Attitude)
		}
	}
	if len(draft.Content.Clues) == 0 {
		draft.Content.Clues = cluesFromGraph(graph, irony, misdirection)
		log.Printf("[scripter:normalize] generated clues count=%d", len(draft.Content.Clues))
	}
	for i, clue := range draft.Content.Clues {
		normalized := normalizeClueString(clue)
		if normalized != clue {
			log.Printf("[scripter:normalize] normalized clue[%d] from=%q to=%q", i, truncateRunes(clue, 300), truncateRunes(normalized, 300))
		}
		draft.Content.Clues[i] = normalized
	}
	if strings.TrimSpace(draft.Content.WinCondition) == "" {
		draft.Content.WinCondition = defaultWinCondition(misdirection)
		log.Printf("[scripter:normalize] filled win_condition=%q", truncateRunes(draft.Content.WinCondition, 300))
	}
	if strings.TrimSpace(draft.Content.LoseCondition) == "" {
		draft.Content.LoseCondition = defaultLoseCondition(misdirection)
		log.Printf("[scripter:normalize] filled lose_condition=%q", truncateRunes(draft.Content.LoseCondition, 300))
	}
	if len(draft.Content.PartialWins) == 0 {
		draft.Content.PartialWins = defaultPartialWins(misdirection)
		log.Printf("[scripter:normalize] filled partial_wins count=%d", len(draft.Content.PartialWins))
	}
}

func defaultScenarioName(irony IronyCore) string {
	reading := truncateRunes(strings.TrimSpace(irony.SurfaceReading), 12)
	if reading == "" {
		return "未命名沙盒调查"
	}
	return "δ-调查：" + reading
}

func defaultSandboxSystemPrompt(misdirection MisdirectionFabric, irony IronyCore) string {
	return fmt.Sprintf("你是本场COC跑团的KP，职责是管理会自行推进的局势而不是执行线性故事。按派系时间线推进后果；按表面可见、主动询问、需要行动、不可直接获得四层管理信息；不要主动把调查员引向正确答案。\n【KP独有，勿向玩家直说】固定神话锚点：%s。δ内部真相：%s。", firstNonEmpty(misdirection.MythosAnchor, "按剧本规则注记处理"), firstNonEmpty(irony.DeepTruth, "真相将通过调查逐步揭示"))
}

func defaultSetting(constraints ScripterConstraints, irony IronyCore, misdirection MisdirectionFabric) string {
	return fmt.Sprintf("%s的%s中，调查员面对一个已经开始运动的局势：%s。公开层面只看得到表象、地方压力和派系互相遮掩；无人干预时，各方会按自己的时间线继续行动。", constraints.Era, strings.Join(constraints.GeographyFlavor, " / "), firstNonEmpty(irony.SurfaceReading, "一个可被多种方式解读的局势已经开始"))
}

func defaultIntro(constraints ScripterConstraints, graph InvestigationGraph) string {
	var nodeNames []string
	for _, n := range graph.Nodes {
		if name := strings.TrimSpace(n.Name); name != "" && len(nodeNames) < 3 {
			nodeNames = append(nodeNames, name)
		}
	}
	if len(nodeNames) == 0 {
		nodeNames = []string{"调查入口"}
	}
	return fmt.Sprintf("你们以\"%s\"进入局势。眼前可立即行动：前往%s，询问公开目击者，或决定是否把已知异常告诉某个派系。", constraints.InvestigatorEntryPosition, strings.Join(nodeNames, "、"))
}

func defaultMapDescription(graph InvestigationGraph) string {
	var nodeNames []string
	for _, n := range graph.Nodes {
		if name := strings.TrimSpace(n.Name); name != "" {
			nodeNames = append(nodeNames, name)
		}
	}
	if len(nodeNames) == 0 {
		return "【文字地图】调查入口连接所有可调查地点；地点之间可往返，没有固定访问顺序。"
	}
	var lines []string
	lines = append(lines, "【文字地图】地点是沙盒状态节点，不是顺序关卡：")
	for i, name := range nodeNames {
		if i == 0 {
			lines = append(lines, fmt.Sprintf("- %s：默认入口，可向其他地点扩散调查。", name))
		} else {
			lines = append(lines, fmt.Sprintf("- %s：可从入口或其他地点前往，返回不会重置局势。", name))
		}
	}
	lines = append(lines, "时间推进时，各地点状态可能因派系行动而改变。")
	return strings.Join(lines, "\n")
}

func scenesFromGraph(graph InvestigationGraph, irony IronyCore) []models.SceneData {
	if len(graph.Nodes) == 0 {
		return []models.SceneData{{ID: "location_1", Name: "调查入口", Description: "可见：异常已经公开出现。可发现：主动调查可获得第一批事实。杠杆：公开或隐瞒信息会改变派系反应。风险：拖延会推进时间线。出口：所有相关地点。", Triggers: []string{"available_from_start"}}}
	}
	_ = irony
	scenes := make([]models.SceneData, 0, len(graph.Nodes))
	for i, node := range graph.Nodes {
		prefix := deltaSignalToCluePrefix(node.DeltaSignal)
		var knowledgeParts []string
		for _, k := range node.Knowledge {
			if strings.TrimSpace(k) != "" {
				knowledgeParts = append(knowledgeParts, prefix+k)
			}
		}
		knowledgeStr := firstNonEmpty(strings.Join(knowledgeParts, "；"), "主动调查可获得的事实")
		desc := fmt.Sprintf("可见：到达即可感知的局势。可发现：%s\n杠杆：调查员行动会改变派系时间线。风险：拖延会让世界推进。出口：可前往相关地点。", knowledgeStr)
		triggers := []string{"available_from_start"}
		if node.ID == graph.HookNode {
			triggers = []string{"available_from_start", "hook"}
		}
		scenes = append(scenes, models.SceneData{
			ID:          firstNonEmpty(node.ID, fmt.Sprintf("location_%d", i+1)),
			Name:        firstNonEmpty(node.Name, fmt.Sprintf("地点%d", i+1)),
			Description: desc,
			Triggers:    triggers,
		})
	}
	return scenes
}

func npcsFromMisdirection(misdirection MisdirectionFabric) []models.NPCData {
	var npcs []models.NPCData
	misdirectorName := strings.TrimSpace(misdirection.MisdirectorNPC)
	for _, faction := range misdirection.Factions {
		for _, npc := range faction.NPCs {
			name := strings.TrimSpace(npc.Name)
			if name == "" {
				continue
			}
			misdirectorNote := ""
			if misdirectorName != "" && (strings.EqualFold(name, misdirectorName) || strings.Contains(name, misdirectorName)) {
				misdirectorNote = "【核心误导NPC】此人支持假δ方向，是玩家最容易信任的误导来源。"
			}
			desc := fmt.Sprintf("公开身份：%s。所属派系：%s。派系目标：%s。个人议程：%s。秘密：%s。当前状态：%s。规则注记：%s。%s", firstNonEmpty(npc.PublicIdentity, "未公开"), firstNonEmpty(faction.Name, "未知派系"), firstNonEmpty(faction.Goal, "未明"), firstNonEmpty(npc.Agenda, "自保并观察局势"), firstNonEmpty(npc.Secret, "掌握部分真相但不会主动全盘托出"), firstNonEmpty(faction.CurrentState, "按时间线行动"), firstNonEmpty(npc.StatsNote, "普通人类属性15-70；特殊能力按规则书裁定"), misdirectorNote)
			npcs = append(npcs, models.NPCData{Name: name, Description: desc, Attitude: firstNonEmpty(npc.Attitude, "谨慎防备")})
		}
	}
	if len(npcs) == 0 {
		npcs = []models.NPCData{{
			Name:        firstNonEmpty(misdirectorName, "核心NPC"),
			Description: fmt.Sprintf("公开身份：地方相关人员。误导来源：%s。真实追踪：%s。", firstNonEmpty(misdirection.FalseLead, "支持假δ方向"), firstNonEmpty(misdirection.TrueTrace, "隐含真实δ")),
			Attitude:    "礼貌但回避关键问题",
		}}
	}
	return npcs
}

func cluesFromGraph(graph InvestigationGraph, irony IronyCore, misdirection MisdirectionFabric) []string {
	clues := make([]string, 0, len(graph.Nodes)+2)
	hasMythosCore := false
	for _, node := range graph.Nodes {
		prefix := deltaSignalToCluePrefix(node.DeltaSignal)
		name := firstNonEmpty(node.Name, node.ID)
		for _, k := range node.Knowledge {
			if strings.TrimSpace(k) != "" {
				clue := fmt.Sprintf("%s%s: %s；获取方式：到达该节点并主动调查。", prefix, name, k)
				if strings.Contains(clue, "神话本质") {
					hasMythosCore = true
				}
				clues = append(clues, clue)
			}
		}
	}
	if mythosAnchor := strings.TrimSpace(misdirection.MythosAnchor); mythosAnchor != "" && !hasMythosCore {
		clues = append(clues, fmt.Sprintf("[隐藏]神话本质(核心发现): %s；获取方式：到达终止节点并触发揭示；发现后承担理智代价。", mythosAnchor))
	}
	if len(clues) == 0 {
		clues = append(clues, "[真实]公开异常(调查入口): "+firstNonEmpty(irony.SurfaceReading, "一个无法普通解释的局势已经开始")+"；获取方式：到达现场并主动询问或检查。")
		clues = append(clues, "[误导]表象线索(初步调查): "+firstNonEmpty(irony.FalseDelta, "支持错误δ推断的表象证据")+"；表面合理但只能解释一部分。")
	}
	return clues
}

func defaultWinCondition(misdirection MisdirectionFabric) string {
	if len(misdirection.EndingSignals) > 0 {
		return "较低代价结束信号：" + misdirection.EndingSignals[0]
	}
	return "如果调查员让关键事实公开并改变至少一个派系时间线，则局势以较低代价固化，但神话锚点的余波仍保留。"
}

func defaultLoseCondition(misdirection MisdirectionFabric) string {
	n := len(misdirection.EndingSignals)
	if n >= 2 {
		return "高代价结束信号：" + misdirection.EndingSignals[n-1]
	}
	return "如果关键时间线终点到达且调查员没有改变任何派系行动，则局势进入新的稳定态，某人、某地或某个身份不可挽回地改变。"
}

func defaultPartialWins(misdirection MisdirectionFabric) []string {
	n := len(misdirection.EndingSignals)
	partials := make([]string, 0, n)
	for i, signal := range misdirection.EndingSignals {
		if i == 0 || i == n-1 {
			continue
		}
		partials = append(partials, "部分胜利："+signal)
		if len(partials) >= 3 {
			break
		}
	}
	if len(partials) == 0 {
		partials = append(partials, "部分胜利：调查员保护了个人或证据，但没有改变所有派系时间线，余波继续存在。")
	}
	return partials
}

func normalizeClueString(clue string) string {
	clue = strings.TrimSpace(clue)
	if clue == "" {
		return "[真实]未命名线索(现场): 存在一个可由调查员主动确认的事实；获取方式：主动调查。"
	}
	if strings.HasPrefix(clue, "[真实]") || strings.HasPrefix(clue, "[隐藏]") || strings.HasPrefix(clue, "[误导]") {
		return clue
	}
	return "[真实]" + clue
}

func cluePrefixForLayer(layer string) string {
	switch strings.ToLower(strings.TrimSpace(layer)) {
	case "appearance", "surface", "real", "true":
		return "[真实]"
	case "deep", "distorted", "distorted_deep", "hidden", "hide":
		return "[隐藏]"
	case "false", "misdirection", "misleading", "red_herring":
		return "[误导]"
	default:
		return "[真实]"
	}
}

// lengthSpec returns scene/clue/NPC count requirements based on target_length.
func difficultySpec(difficulty string) string {
	switch strings.ToLower(strings.TrimSpace(difficulty)) {
	case "easy":
		return "- 派系时间线节点推进缓慢，调查员有充足干预窗口\n- 线索分布：[真实]为主，少量[隐藏]，极少[误导]\n- 杠杆代价低：对话、基础检定或公开信息即可拉动，无需牺牲\n- NPC初始态度：中立到谨慎，社交检定可以成功说服\n- 恐怖核心层：可进入但不强迫付出理智代价"
	case "hard":
		return "- 派系时间线推进快，干预窗口紧张，超时则产生不可逆后果\n- 线索分布：[隐藏]和[误导]为主，表面可见的[真实]线索很少\n- 杠杆代价高：多数杠杆需要对抗检定、道德代价或信息暴露\n- NPC初始态度：多数敌对或欺骗性，说服有实质代价\n- 恐怖核心层：进入需承担显著理智或人际代价"
	default: // normal
		return "- 派系时间线推进速度适中，有几个明确干预窗口\n- 线索分布均衡：[真实]略多，[隐藏]次之，少量[误导]\n- 杠杆代价适中：部分杠杆可直接拉动，部分需要检定\n- NPC初始态度：混合，部分可说服，部分保持距离\n- 恐怖核心层：需要主动调查和付出一定代价"
	}
}

func lengthSpec(targetLength string) string {
	switch strings.ToLower(strings.TrimSpace(targetLength)) {
	case "long":
		return "- locations/scenes: 6-8个地点状态，每个有可见信息、可发现信息、杠杆、风险、出口\n- clues: 10-12条自包含事实线索，必须带[真实]/[隐藏]/[误导]前缀\n- NPC数量: 7-10个，来自派系且有独立议程"
	case "medium":
		return "- locations/scenes: 4-6个地点状态，每个有可见信息、可发现信息、杠杆、风险、出口\n- clues: 7-10条自包含事实线索，必须带[真实]/[隐藏]/[误导]前缀\n- NPC数量: 4-7个，来自派系且有独立议程"
	default:
		return "- locations/scenes: 3-4个地点状态，每个有可见信息、可发现信息、杠杆、风险、出口\n- clues: 5-7条自包含事实线索，必须带[真实]/[隐藏]/[误导]前缀\n- NPC数量: 2-4个，来自派系且有独立议程"
	}
}

// ---------------------------------------------------------------------------
// JSON repair helpers retained for other package code
// ---------------------------------------------------------------------------

func chatAndParseJSON[T any](ctx context.Context, generator agentHandle, parser agentHandle, msgs []llm.ChatMessage, out *T, schemaExample string, tag string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if generator.provider == nil {
		return fmt.Errorf("%s generator provider unavailable", tag)
	}
	log.Printf("[scripter:%s] chat start messages=%d", tag, len(msgs))
	raw, err := generator.provider.Chat(ctx, msgs)
	if err != nil {
		log.Printf("[scripter:%s] chat error=%v", tag, err)
		return err
	}
	log.Printf("[scripter:%s] raw len=%d body=%s", tag, len(raw), truncateRunes(raw, scripterRawLogLimit))
	parseErr := parseJSONObject(raw, out)
	if parseErr == nil {
		log.Printf("[scripter:%s] parse ok without repair", tag)
		logParsedJSON(tag, out)
		return nil
	}
	log.Printf("[scripter:%s] generator JSON parse failed: %v raw=%s", tag, parseErr, truncateRunes(raw, scripterRawLogLimit))
	fixed, repairErr := repairJSONWith(ctx, parser, raw, parseErr, schemaExample)
	if repairErr != nil {
		return fmt.Errorf("%s JSON 修复失败: %w (原始错误: %v)", tag, repairErr, parseErr)
	}
	if err := parseJSONObject(fixed, out); err == nil {
		return nil
	} else {
		log.Printf("[%s] parser output schema mismatch, retry parser: %v", tag, err)
		repairedAgain, repairErr2 := repairJSONWith(ctx, parser, fixed, err, schemaExample)
		if repairErr2 != nil {
			return fmt.Errorf("修复后的 %s JSON 结构仍不匹配,二次修复失败: %w (结构错误: %v)", tag, repairErr2, err)
		}
		if err2 := parseJSONObject(repairedAgain, out); err2 != nil {
			return fmt.Errorf("二次修复后的 %s JSON 仍无法解析: %w", tag, err2)
		}
	}
	return nil
}

// RepairJSON uses the parser agent to fix malformed JSON. Exported so other
// subsystems can reuse the same low-temperature fixer.
func RepairJSON(ctx context.Context, rawJSON string, parseErr error, schemaExample string) (string, error) {
	if strings.HasPrefix(rawJSON, "```json") {
		rawJSON = strings.TrimPrefix(rawJSON, "```json")
		rawJSON = strings.TrimSuffix(rawJSON, "```")
		return strings.TrimSpace(rawJSON), nil
	}
	isArray := strings.HasPrefix(strings.TrimSpace(schemaExample), "[") && strings.HasSuffix(strings.TrimSpace(schemaExample), "]")
	if isArray {
		fixed := false
		trimmed := strings.TrimSpace(rawJSON)
		if !strings.HasPrefix(trimmed, "[") {
			trimmed = "[" + trimmed
			fixed = true
		}
		if !strings.HasSuffix(trimmed, "]") {
			trimmed = trimmed + "]"
			fixed = true
		}
		if fixed && json.Valid([]byte(trimmed)) {
			debugf("repair", "fixed: %v", trimmed)
			return trimmed, nil
		}
	}
	parser, err := loadSingleAgent(models.AgentRoleParser)
	if err != nil {
		return "", fmt.Errorf("parser agent 未配置: %w", err)
	}
	return repairJSONWith(ctx, parser, rawJSON, parseErr, schemaExample)
}

func repairJSONWith(ctx context.Context, parser agentHandle, rawJSON string, parseErr error, schemaExample string) (string, error) {
	if parser.provider == nil {
		return "", fmt.Errorf("parser provider unavailable")
	}
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
		if strings.HasPrefix(fixed, "```json") {
			fixed = strings.TrimPrefix(fixed, "```json")
			fixed = strings.TrimSuffix(fixed, "```")
		}
		debugf("Parser", "Fixed JSON: %v", fixed)
		stripped := llm.StripCodeFence(strings.TrimSpace(fixed))
		if json.Valid([]byte(stripped)) {
			log.Printf("[parser] JSON 修复成功 attempt=%d", attempt)
			return stripped, nil
		}
		if s := strings.Index(stripped, "{"); s >= 0 {
			if e := strings.LastIndex(stripped, "}"); e > s {
				candidate := stripped[s : e+1]
				if json.Valid([]byte(candidate)) {
					log.Printf("[parser] JSON 修复成功(提取) attempt=%d", attempt)
					return candidate, nil
				}
			}
		}
		if s := strings.Index(stripped, "["); s >= 0 {
			if e := strings.LastIndex(stripped, "]"); e > s {
				candidate := stripped[s : e+1]
				if json.Valid([]byte(candidate)) {
					log.Printf("[parser] JSON 修复成功(提取数组) attempt=%d", attempt)
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

// ---------------------------------------------------------------------------
// Small legacy helpers kept for current tests and package compatibility
// ---------------------------------------------------------------------------

type scripterToolCall struct {
	Action     string         `json:"action"`
	Think      string         `json:"think,omitempty"`
	Question   string         `json:"question,omitempty"`
	Constant   string         `json:"constant,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Background *FogBackground `json:"background,omitempty"`
	Draft      *ScenarioDraft `json:"draft,omitempty"`
}

type FogBackground struct {
	TimeAndPlace       string   `json:"time_and_place"`
	InvestigatorHook   string   `json:"investigator_hook,omitempty"`
	DailyBeauty        string   `json:"daily_beauty,omitempty"`
	UnsettlingDetail   string   `json:"unsettling_detail,omitempty"`
	PublicProblem      string   `json:"public_problem,omitempty"`
	BriefPreserved     string   `json:"brief_preserved,omitempty"`
	AntiTropeExecution []string `json:"anti_trope_execution,omitempty"`
}

func validateScripterResponsePayload(call scripterToolCall, expected string) error {
	if strings.TrimSpace(call.Reason) == "" {
		return fmt.Errorf("response必须包含非空reason")
	}
	payloads := 0
	if call.Background != nil {
		payloads++
	}
	if call.Draft != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("response必须且只能包含一个payload, got=%d", payloads)
	}
	switch expected {
	case "background":
		if call.Background == nil {
			return fmt.Errorf("expected background")
		}
	case "draft":
		if call.Draft == nil {
			return fmt.Errorf("expected draft")
		}
	default:
		return fmt.Errorf("unknown expected payload %q", expected)
	}
	return nil
}

func parseScripterToolCalls(ctx context.Context, parser agentHandle, raw string, schemaExample string) ([]scripterToolCall, error) {
	stripped := strings.TrimSpace(llm.StripCodeFence(raw))
	var calls []scripterToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil {
		return calls, nil
	} else {
		parseErr := err
		if s := strings.Index(stripped, "["); s >= 0 {
			if e := strings.LastIndex(stripped, "]"); e > s {
				candidate := stripped[s : e+1]
				if err := json.Unmarshal([]byte(candidate), &calls); err == nil {
					return calls, nil
				} else {
					parseErr = err
				}
			}
		}
		if parser.provider == nil {
			return nil, fmt.Errorf("必须输出JSON数组: %w", parseErr)
		}
		fixed, repairErr := repairJSONWith(ctx, parser, stripped, parseErr, schemaExample)
		if repairErr != nil {
			return nil, repairErr
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(fixed)), &calls); err != nil {
			return nil, fmt.Errorf("修复后仍不是scripter tool-call数组: %w", err)
		}
		return calls, nil
	}
}

func scripterSchemaExample(expected string) string {
	switch expected {
	case "background":
		return `[{"action":"response","reason":"这个公开背景保留了用户brief并给出调查入口。","background":{"time_and_place":"时代与地点","investigator_hook":"调查入口"}}]`
	case "draft":
		return `[{"action":"response","reason":"最终草案兼容ScenarioContent并保持沙盒语义。","draft":` + scenarioExample + `}]`
	default:
		return `[{"action":"response","reason":"解释为什么这样响应。"}]`
	}
}

// grepRulebook searches the rulebook for exact keyword matches and returns a
// compact hit list. Used by lawyer.go.
func grepRulebook(keyword string) string {
	hits := rulebook.GrepRuleBook(keyword)
	if len(hits) == 0 {
		return ""
	}
	const maxLen = 20
	var sb strings.Builder
	for i, h := range hits {
		s := h.Text
		if len([]rune(s)) > maxLen {
			s = string([]rune(s)[:maxLen]) + "..."
		}
		sb.WriteString(fmt.Sprintf("[%v] Hit Line: %v Content: %v\n", i+1, h.LineNum, s))
	}
	return strings.TrimSpace(sb.String())
}

func loadRecentNPCNameBlacklist(limit int) []string {
	if limit <= 0 || models.DB == nil {
		return nil
	}
	var scenarios []models.Scenario
	if err := models.DB.Order("created_at DESC").Limit(limit * 3).Find(&scenarios).Error; err != nil {
		log.Printf("[scripter] load recent npc blacklist failed: %v", err)
		return nil
	}
	seen := map[string]bool{}
	names := make([]string, 0, limit)
	for i := range scenarios {
		if err := scenarios[i].DecodeData(); err != nil {
			continue
		}
		for _, npc := range scenarios[i].Content.Data.NPCs {
			name := normalizeNPCName(npc.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
			if len(names) >= limit {
				return names
			}
		}
	}
	return names
}

func loadScenarioTitleSamples(sampleSize int) []string {
	if sampleSize <= 0 || models.DB == nil {
		return nil
	}
	var scenarios []models.Scenario
	if err := models.DB.Order("created_at DESC").Limit(sampleSize).Find(&scenarios).Error; err != nil {
		log.Printf("[scripter] load scenario titles failed: %v", err)
		return nil
	}
	titles := make([]string, 0, len(scenarios))
	seen := map[string]bool{}
	for _, scenario := range scenarios {
		title := normalizeScenarioTitle(scenario.Name)
		if title == "" || seen[title] {
			continue
		}
		seen[title] = true
		titles = append(titles, title)
	}
	return titles
}

func formatNPCNameBlacklist(names []string) string {
	if len(names) == 0 {
		return "(无)"
	}
	return "- " + strings.Join(names, "\n- ")
}

func formatScenarioTitleBlacklist(names []string) string {
	if len(names) == 0 {
		return "(无)"
	}
	return "- " + strings.Join(names, "\n- ")
}

func normalizeScenarioTitle(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, " `\"'，。；;：:（）()【】[]《》")
	return strings.TrimSpace(name)
}

func normalizeNPCName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, " `\"'，。；;：:（）()【】[]")
	return strings.TrimSpace(name)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func truncateRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= maxLen {
		return string(runes)
	}
	return string(runes[:maxLen]) + "..."
}

func truncateForLog(s string, maxLen int) string {
	return truncateRunes(s, maxLen)
}
