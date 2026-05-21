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

	log.Printf("[scripter] stage=foundation_seed start")
	seed, err := generateFoundationSeedWithQA(ctx, r, constraints)
	if err != nil {
		log.Printf("[scripter] stage=foundation_seed error=%v", err)
		return ScenarioCreationOutput{}, fmt.Errorf("Foundation Seed 失败: %w", err)
	}
	log.Printf("[scripter] stage=foundation_seed done anomaly=%q relation=%q mythos_seed=%q", truncateRunes(seed.Anomaly, 300), seed.MythosRelation, truncateRunes(seed.MythosSeed, 300))
	logScripterArtifact("Stage 1 Foundation Seed", seed)

	log.Printf("[scripter] stage=faction_map start mythos_seed=%q", truncateRunes(seed.MythosSeed, 300))
	factions, err := generateFactionMapWithQA(ctx, r, constraints, seed)
	if err != nil {
		log.Printf("[scripter] stage=faction_map error=%v", err)
		return ScenarioCreationOutput{}, fmt.Errorf("Factions & Timelines 失败: %w", err)
	}
	log.Printf("[scripter] stage=faction_map done mythos_anchor=%q factions=%d rules_notes=%d ending_signals=%d", truncateRunes(factions.MythosAnchor, 300), len(factions.Factions), len(factions.RulesNotes), len(factions.EndingSignals))
	logScripterArtifact("Stage 2 Factions & Timelines", factions)

	log.Printf("[scripter] stage=world_state start mythos_anchor=%q", truncateRunes(factions.MythosAnchor, 300))
	world, err := generateWorldStateWithQA(ctx, r, constraints, seed, factions)
	if err != nil {
		log.Printf("[scripter] stage=world_state error=%v", err)
		return ScenarioCreationOutput{}, fmt.Errorf("World Dressing 失败: %w", err)
	}
	log.Printf("[scripter] stage=world_state done locations=%d clue_facts=%d horror_surface=%q", len(world.Locations), len(world.ClueFacts), truncateRunes(world.HorrorLayers.Surface, 300))
	logScripterArtifact("Stage 3 World Dressing", world)

	log.Printf("[scripter] stage=assembly start")
	draft, err := assembleSandboxDraftWithQA(ctx, r, constraints, seed, factions, world)
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
		repaired, repairErr := assembleSandboxDraft(ctx, r, constraints, seed, factions, world, &draft, issues)
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
	normalizeDraftBeforeReturn(&draft, r.req, constraints, seed, factions, world)
	log.Printf("[scripter] normalization done name=%q players=%d-%d slot=%d scenes=%d npcs=%d clues=%d partial_wins=%d", draft.Name, draft.MinPlayers, draft.MaxPlayers, draft.Content.GameStartSlot, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues), len(draft.Content.PartialWins))
	if issues := validateDraftCompatibility(draft); len(issues) > 0 {
		log.Printf("[scripter] draft compatibility issues after normalization: %v", issues)
	}
	log.Printf("[scripter] sandbox draft name=%q scenes=%d npcs=%d clues=%d", draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	logScripterArtifact("Stage 4 ScenarioDraft", draft)

	return ScenarioCreationOutput{Draft: draft, Iterations: iterations}, nil
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

// ---------------------------------------------------------------------------
// Four LLM stages
// ---------------------------------------------------------------------------

type FoundationSeed struct {
	Anomaly        string `json:"anomaly"`
	MythosRelation string `json:"mythos_relation"`
	MythosSeed     string `json:"mythos_seed"`
}

type FoundationSeedQA struct {
	Pass             bool     `json:"pass"`
	Reason           string   `json:"reason"`
	RejectReasons    []string `json:"reject_reasons"`
	SuggestedScope   string   `json:"suggested_scope"`
	RuleCheckSummary string   `json:"rule_check_summary"`
}

type foundationSeedQAToolCall struct {
	Action           ToolCallType `json:"action"`
	Think            string       `json:"think,omitempty"`
	Question         string       `json:"question,omitempty"`
	Pass             bool         `json:"pass,omitempty"`
	Reason           string       `json:"reason,omitempty"`
	RejectReasons    []string     `json:"reject_reasons,omitempty"`
	SuggestedScope   string       `json:"suggested_scope,omitempty"`
	RuleCheckSummary string       `json:"rule_check_summary,omitempty"`
}

const foundationSeedSystemPrompt = `<role>COC7沙盒基础种子设计师</role>
<task>只生成FoundationSeed JSON。这个阶段是纯创意阶段，不查询也不引用规则书。</task>
<output>只输出合法JSON对象，不要Markdown、标题、解释或代码围栏。</output>
<schema>{"anomaly":"具体、奇怪、无法立刻解释的事实","mythos_relation":"byproduct或consequence","mythos_seed":"待Stage2核验的神话元素方向"}</schema>
<rules>
- anomaly必须是具体事实，不是类型标签；读完就应让调查员想追问。
- mythos_relation只能是byproduct或consequence：byproduct=异常是神话力量副产品；consequence=神话是人类行为后果。
- mythos_seed只是方向，不做规则裁定，不编造数值。
- 用户brief若非空，必须保留其核心意图。
- 如果收到qa_rejection，必须重写异常与mythos_seed之间的关系；不要只改措辞。
</rules>`

const foundationSeedExample = `{"anomaly":"邮局每天把信投递到三十年前已经拆除的地址，仍有人在夜里取走这些信。","mythos_relation":"consequence","mythos_seed":"与梦境、信件、非人传讯或典籍残页有关的神话锚点"}`

const foundationSeedQAExample = `{"pass":true,"reason":"check_rule结果显示该方向可以保守接入梦境、典籍或非人传讯等规则书神话元素；异常与mythos_seed存在可核验桥接。","reject_reasons":[],"suggested_scope":"保留非线性通信方向，Stage2锁定具体典籍或实体时继续保守标注。","rule_check_summary":"check_rule返回了可用的规则书神话方向，未要求使用未核验数值。"}`

const foundationSeedQAToolCallExample = `[{"action":"think","think":"我需要先核验mythos_seed是否能对应规则书神话方向。"},{"action":"check_rule","question":"COC7规则书中是否存在与梦境、非人传讯或典籍残页相关的神话实体、典籍、法术或禁忌知识方向？"}]`

const foundationSeedQASystemPrompt = `<role>COC7沙盒基础种子QA</role>
<task>审核FoundationSeed中的异常是否能保守对应到克苏鲁神话/规则书方向。你必须像Director一样通过工具调用完成审核。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：说明本轮审核计划。
  {"action":"think","think":"计划"}
- check_rule：向COC规则专家查询规则书。必须问具体且可验证的问题：要么问"规则书中是否有X现象/效果的直接条目"，要么问"规则书记载的Y条目会直接导致什么可观察后果"；禁止泛问"某实体有什么效果"或"某方向有哪些可能"。
  {"action":"check_rule","question":"规则问题"}
- response：最终审核结论。只有读到check_rule工具结果后才能调用。
  {"action":"response","pass":true,"reason":"审核理由","reject_reasons":[],"suggested_scope":"给Stage2或重写阶段的范围","rule_check_summary":"实际依据的check_rule摘要"}
</tools>
<batch_rules>
- 第一轮必须输出think和至少一个check_rule；禁止第一轮直接response。
- check_rule和response禁止出现在同一轮。先check_rule，读取<INTERNAL_TOOL_RESULT>后，下一轮再response。
- 审核时必须依次完成三步验证，每步都需要check_rule支撑，不可跳过：
  ① anomaly本身有规则书支撑（类型1或类型2）；
  ② mythos_seed描述的神话机制在规则书中有对应条目；
  ③ mythos_seed机制能合理产生anomaly——即从mythos_seed出发，anomaly是其推导的可观察表现(anomaly需要包含完整的推理链)。
- 如果check_rule结果不足，可继续调用新的check_rule；不要凭常识补齐。
- response必须包含pass、reason、reject_reasons、suggested_scope、rule_check_summary。
</batch_rules>
<audit_rules>
- 只审核Stage1 seed，不锁定具体数值，不扩写派系/NPC/场景。
- pass=true只允许两类anomaly，必须由check_rule结果支撑，不得凭气氛或方向感判断：
  【类型1·规则书直接记载】anomaly描述的现象是规则书中某神话实体、法术、典籍或遭遇的直接记录效果；check_rule必须能引用具体条目名称或机制。
  【类型2·规则书现象的推导】anomaly是类型1现象的逻辑推理结果，且提供了完整推导链且不依赖额外假设（例：规则书记载某实体造成地震→地震导致动物异常迁徙）；check_rule必须能找到"直接原因"对应的规则书条目。
- anomaly与mythos_seed的联系必须成立：即使anomaly和mythos_seed分别有规则书依据，如果无法通过check_rule确认"mythos_seed所描述的神话机制是产生该anomaly的直接原因或一步推导来源"，pass=false；两件独立有规则书依据但彼此无因果关系的事实不构成合格的seed。
- 以下情况必须pass=false，不接受任何例外：
  ① anomaly依赖规则书未记载的自设物品、自设法器或自设特殊物质（例如"具有时间停滞效果的金色河沙"），即使mythos_anchor本身有效；
  ② anomaly只能通过多步推测、创作延伸或"神话气氛"才能接到规则书方向；
  ③ check_rule只返回"大致类似"或"可以想象"而无具体条目；
  ④ anomaly的核心是普通犯罪、心理疾病、伪科学或高科技现象；
  ⑤ anomaly与mythos_seed之间的联系无法通过check_rule确认，只能凭创作合理性推断。
- reject_reasons必须具体指出anomaly属于哪种不合格类型，以及与check_rule结果哪里对应不上。
- rule_check_summary必须写出check_rule返回的具体条目名称或机制，以及异常与神话的联系依据，不能只写"未找到"或留空。
</audit_rules>`

func generateFoundationSeedWithQA(ctx context.Context, room *scripterRoom, constraints ScripterConstraints) (FoundationSeed, error) {
	const candidateCount = 5
	const maxAttempts = 30
	var candidates []FoundationSeed
	for run := 1; run <= candidateCount; run++ {
		usedAnomalies := make([]string, len(candidates))
		for i, c := range candidates {
			usedAnomalies[i] = c.Anomaly
		}
		session := newFoundationSeedSession(room, constraints, usedAnomalies)
		var lastQA *FoundationSeedQA
		var passed bool
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			seed, err := session.generate(ctx, attempt)
			if err != nil {
				return FoundationSeed{}, err
			}
			qa, err := session.review(ctx, attempt, seed)
			if err != nil {
				return FoundationSeed{}, err
			}
			lastQA = &qa
			log.Printf("[scripter:foundation_seed_qa] run=%d attempt=%d pass=%v reason=%q rejects=%q suggested_scope=%q rule_check=%q", run, attempt, qa.Pass, truncateRunes(qa.Reason, 500), strings.Join(qa.RejectReasons, " | "), truncateRunes(qa.SuggestedScope, 500), truncateRunes(qa.RuleCheckSummary, 500))
			logScripterArtifact(fmt.Sprintf("Stage 1 Foundation Seed Run %d QA Attempt %d", run, attempt), qa)
			if qa.Pass {
				passed = true
				// 去重：anomaly相同则跳过
				dup := false
				for _, c := range candidates {
					if strings.EqualFold(strings.TrimSpace(c.Anomaly), strings.TrimSpace(seed.Anomaly)) {
						dup = true
						break
					}
				}
				if !dup {
					candidates = append(candidates, seed)
					log.Printf("[scripter:foundation_seed_qa] run=%d accepted candidates=%d anomaly=%q", run, len(candidates), truncateRunes(seed.Anomaly, 200))
				} else {
					log.Printf("[scripter:foundation_seed_qa] run=%d duplicate anomaly skipped", run)
				}
				break
			}
			session.feedRejection(attempt, seed, qa)
		}
		if !passed {
			return FoundationSeed{}, fmt.Errorf("Foundation Seed QA run=%d 连续拒绝 %d 次，拒绝原因=%v", run, maxAttempts, rejectionRejectReasons(lastQA))
		}
	}
	picked := candidates[rand.Intn(len(candidates))]
	log.Printf("[scripter:foundation_seed_qa] picked from %d candidates anomaly=%q", len(candidates), truncateRunes(picked.Anomaly, 200))
	return picked, nil
}

type foundationSeedSession struct {
	room          *scripterRoom
	constraints   ScripterConstraints
	architectMsgs []llm.ChatMessage
	qaMsgs        []llm.ChatMessage
}

func newFoundationSeedSession(room *scripterRoom, constraints ScripterConstraints, usedAnomalies []string) *foundationSeedSession {
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)
	usedBlock := ""
	if len(usedAnomalies) > 0 {
		quoted := make([]string, len(usedAnomalies))
		for i, a := range usedAnomalies {
			quoted[i] = fmt.Sprintf("- %s", a)
		}
		usedBlock = fmt.Sprintf("\n<already_generated_anomalies>\n%s\n</already_generated_anomalies>\n以上anomaly已被本次生成使用，必须生成完全不同的anomaly，不得在主题、场所或现象类型上重复。", strings.Join(quoted, "\n"))
	}
	architectPrompt := fmt.Sprintf(`<request_json>%s</request_json>
<constraints>%s</constraints>
<geography_note>地理只作为风味，不要让它替代异常事实。</geography_note>%s
请生成第1版FoundationSeed。`, string(reqJSON), string(constraintsJSON), usedBlock)
	qaPrompt := fmt.Sprintf(`<constraints>%s</constraints>
你是持续运行的FoundationSeed QA会话。每次收到<foundation_seed_candidate>后，通过think/check_rule/response工具调用审核它。`, string(constraintsJSON))
	return &foundationSeedSession{
		room:        room,
		constraints: constraints,
		architectMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.architect.systemPrompt(foundationSeedSystemPrompt)},
			{Role: "user", Content: architectPrompt},
		},
		qaMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.qa.systemPrompt(foundationSeedQASystemPrompt)},
			{Role: "user", Content: qaPrompt},
		},
	}
}

func (s *foundationSeedSession) generate(ctx context.Context, attempt int) (FoundationSeed, error) {
	logStagePrompt(fmt.Sprintf("foundation_seed_attempt_%d", attempt), s.architectMsgs)
	var seed FoundationSeed
	if err := chatAndParseJSON(ctx, s.room.architect, s.room.parser, s.architectMsgs, &seed, foundationSeedExample, "foundation_seed"); err != nil {
		return FoundationSeed{}, err
	}
	seed = normalizeFoundationSeed(seed, s.room.req)
	seedJSON, _ := json.Marshal(seed)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{Role: "assistant", Content: string(seedJSON)})
	log.Printf("[scripter:foundation_seed] attempt=%d generated anomaly=%q relation=%q mythos_seed=%q", attempt, truncateRunes(seed.Anomaly, 500), seed.MythosRelation, truncateRunes(seed.MythosSeed, 500))
	return seed, nil
}

func (s *foundationSeedSession) review(ctx context.Context, attempt int, seed FoundationSeed) (FoundationSeedQA, error) {
	seedJSON, _ := json.Marshal(seed)
	s.qaMsgs = append(s.qaMsgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(`<foundation_seed_candidate attempt="%d">%s</foundation_seed_candidate>
请审核这个候选。`, attempt, string(seedJSON))})
	qa, msgs, err := runFoundationSeedQALoop(ctx, s.room, s.qaMsgs)
	s.qaMsgs = msgs
	return qa, err
}

func (s *foundationSeedSession) feedRejection(attempt int, seed FoundationSeed, qa FoundationSeedQA) {
	seedJSON, _ := json.Marshal(seed)
	qaJSON, _ := json.Marshal(qa)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(`<qa_rejection attempt="%d">
<rejected_seed>%s</rejected_seed>
<qa_result>%s</qa_result>
</qa_rejection>
请基于同一个创作上下文重写FoundationSeed：必须解决QA指出的规则书对应问题，不要只改措辞；仍只输出合法JSON对象。`, attempt, string(seedJSON), string(qaJSON))})
}

func runFoundationSeedQALoop(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage) (FoundationSeedQA, []llm.ChatMessage, error) {
	const maxQARounds = 30
	seenCheckRule := false
	for round := 1; round <= maxQARounds; round++ {
		if ctx.Err() != nil {
			return FoundationSeedQA{}, msgs, ctx.Err()
		}
		logStagePrompt(fmt.Sprintf("foundation_seed_qa_round_%d", round), msgs)
		calls, raw, err := runFoundationSeedQA(ctx, room.qa, room.parser, msgs)
		if err != nil {
			return FoundationSeedQA{}, msgs, err
		}
		log.Printf("[scripter:foundation_seed_qa] round=%d raw_len=%d raw=%s", round, len(raw), truncateRunes(raw, scripterRawLogLimit))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})
		if len(calls) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须输出至少一个工具调用。"})
			continue
		}
		hasResponse := false
		hasCheckRule := false
		for _, call := range calls {
			switch call.Action {
			case ToolCheckRule:
				hasCheckRule = true
			case ToolResponse:
				hasResponse = true
			case ToolThink:
			default:
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf("SYSTEM REJECT: FoundationSeed QA只允许think/check_rule/response，不允许%s。", call.Action)})
				continue
			}
		}
		if hasResponse && hasCheckRule {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: check_rule和response不能在同一轮。先调用check_rule，读取结果后下一轮再response。"})
			continue
		}
		if hasResponse {
			if !seenCheckRule {
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 第一轮禁止直接response，必须先调用check_rule。"})
				continue
			}
			qa, err := foundationSeedQAFromResponse(calls)
			if err != nil {
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: " + err.Error()})
				continue
			}
			return qa, msgs, nil
		}
		toolResults := executeFoundationSeedQATools(ctx, room, calls)
		if len(toolResults) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 需要调用check_rule获取规则书依据，然后再response。"})
			continue
		}
		seenCheckRule = true
		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: formatFoundationSeedQAToolResults(toolResults)})
	}
	return FoundationSeedQA{}, msgs, fmt.Errorf("Foundation Seed QA 未在%d轮内给出response", maxQARounds)
}

func runFoundationSeedQA(ctx context.Context, qa agentHandle, parser agentHandle, msgs []llm.ChatMessage) ([]foundationSeedQAToolCall, string, error) {
	if qa.provider == nil {
		return nil, "", fmt.Errorf("qa provider unavailable")
	}
	raw, err := qa.provider.Chat(ctx, msgs)
	if err != nil {
		return nil, "", err
	}
	calls, err := parseFoundationSeedQAToolCalls(ctx, parser, raw)
	if err != nil {
		return nil, raw, err
	}
	return calls, raw, nil
}

func parseFoundationSeedQAToolCalls(ctx context.Context, parser agentHandle, raw string) ([]foundationSeedQAToolCall, error) {
	stripped := strings.TrimSpace(llm.StripCodeFence(llm.JsonArryProtect(raw)))
	var calls []foundationSeedQAToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil {
		return calls, nil
	} else if parser.provider != nil {
		fixed, repairErr := repairJSONWith(ctx, parser, stripped, err, foundationSeedQAToolCallExample)
		if repairErr != nil {
			return nil, repairErr
		}
		fixed = strings.TrimSpace(llm.JsonArryProtect(fixed))
		if err := json.Unmarshal([]byte(fixed), &calls); err != nil {
			return nil, err
		}
		return calls, nil
	} else {
		return nil, err
	}
}

func executeFoundationSeedQATools(ctx context.Context, room *scripterRoom, calls []foundationSeedQAToolCall) []ToolResult {
	toolResults := make([]ToolResult, 0, len(calls))
	for _, call := range calls {
		if call.Action != ToolCheckRule {
			continue
		}
		question := strings.TrimSpace(call.Question)
		if question == "" {
			toolResults = append(toolResults, ToolResult{Action: ToolCheckRule, Result: "无结果, 默认禁止, 任何操作均不允许。原因：check_rule.question为空。"})
			continue
		}
		log.Printf("[scripter:foundation_seed_qa] check_rule q=%s", truncateRunes(question, scripterPromptLogLimit))
		results := runLawyer(ctx, room.lawyer, question, rulebook.GlobalIndex)
		result := formatLawyerResults(results)
		log.Printf("[scripter:foundation_seed_qa] check_rule result=%s", truncateRunes(result, scripterRepairLogLimit))
		toolResults = append(toolResults, ToolResult{Action: ToolCheckRule, Result: result})
	}
	return toolResults
}

func formatFoundationSeedQAToolResults(results []ToolResult) string {
	bs, err := json.Marshal(results)
	if err != nil {
		return fmt.Sprintf("<INTERNAL_TOOL_RESULT>ERROR: %v</INTERNAL_TOOL_RESULT>", err)
	}
	return fmt.Sprintf("<INTERNAL_TOOL_RESULT>\n%s\n</INTERNAL_TOOL_RESULT>", string(bs))
}

func foundationSeedQAFromResponse(calls []foundationSeedQAToolCall) (FoundationSeedQA, error) {
	var response *foundationSeedQAToolCall
	for i := range calls {
		if calls[i].Action == ToolResponse {
			if response != nil {
				return FoundationSeedQA{}, fmt.Errorf("response只能有一个")
			}
			response = &calls[i]
		}
	}
	if response == nil {
		return FoundationSeedQA{}, fmt.Errorf("缺少response")
	}
	qa := FoundationSeedQA{
		Pass:             response.Pass,
		Reason:           strings.TrimSpace(response.Reason),
		RejectReasons:    response.RejectReasons,
		SuggestedScope:   strings.TrimSpace(response.SuggestedScope),
		RuleCheckSummary: strings.TrimSpace(response.RuleCheckSummary),
	}
	for i := range qa.RejectReasons {
		qa.RejectReasons[i] = strings.TrimSpace(qa.RejectReasons[i])
	}
	if qa.Reason == "" {
		return FoundationSeedQA{}, fmt.Errorf("response.reason不能为空")
	}
	if qa.RuleCheckSummary == "" {
		return FoundationSeedQA{}, fmt.Errorf("response.rule_check_summary不能为空")
	}
	if !qa.Pass && len(qa.RejectReasons) == 0 {
		qa.RejectReasons = []string{qa.Reason}
	}
	return qa, nil
}

func rejectionRejectReasons(rejection *FoundationSeedQA) []string {
	if rejection == nil {
		return nil
	}
	if len(rejection.RejectReasons) > 0 {
		return rejection.RejectReasons
	}
	if strings.TrimSpace(rejection.Reason) != "" {
		return []string{strings.TrimSpace(rejection.Reason)}
	}
	return []string{"未给出拒绝原因"}
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

func normalizeFoundationSeed(seed FoundationSeed, req ScenarioCreationRequest) FoundationSeed {
	seed.Anomaly = strings.TrimSpace(seed.Anomaly)
	seed.MythosRelation = strings.ToLower(strings.TrimSpace(seed.MythosRelation))
	seed.MythosSeed = strings.TrimSpace(seed.MythosSeed)
	if seed.Anomaly == "" {
		seed.Anomaly = firstNonEmpty(req.Brief, "一个公开场所反复出现无法解释的细节，普通解释都只能解释其中一半。")
	}
	switch {
	case seed.MythosRelation == "byproduct" || strings.Contains(seed.MythosRelation, "副产品"):
		seed.MythosRelation = "byproduct"
	case seed.MythosRelation == "consequence" || strings.Contains(seed.MythosRelation, "后果"):
		seed.MythosRelation = "consequence"
	default:
		seed.MythosRelation = "consequence"
	}
	if seed.MythosSeed == "" {
		seed.MythosSeed = "待核验的神话实体、典籍、法术或物品"
	}
	return seed
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

// runSandboxQALoop runs the think→response QA loop for stages 2-4.
// No rulebook tool calls are allowed; only think and response.
func runSandboxQALoop(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage, toolCallExample string, tag string) (SandboxQA, []llm.ChatMessage, error) {
	if room.qa.provider == nil {
		return SandboxQA{Pass: true, Reason: "qa provider unavailable, skipping"}, msgs, nil
	}
	const maxQARounds = 20
	seenThink := false
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
				seenThink = true
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
		if hasResponse && !seenThink {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 第一轮必须先think，再response。"})
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

type FactionMap struct {
	MythosAnchor  string        `json:"mythos_anchor"`
	RulesNotes    []string      `json:"rules_notes"`
	Factions      []FactionPlan `json:"factions"`
	EndingSignals []string      `json:"ending_signals"`
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

const factionMapSystemPrompt = `<role>COC7沙盒派系与时间线设计师</role>
<task>从FoundationSeed和固定结构约束生成FactionMap。此阶段是唯一允许使用规则书上下文的阶段，并在此锁定mythos_anchor。</task>
<output>只输出合法JSON对象，不要Markdown、标题、解释或代码围栏。</output>
<schema>{"mythos_anchor":"已核验或保守标注的神话锚点","rules_notes":["规则来源/不确定性/保守处理"],"factions":[{"name":"派系名","goal":"目标","current_state":"当前正在做什么","timeline":[{"node":"第0/1/2节点","trigger":"推进条件或时间窗口","intervention_pivot":"调查员怎样能改变方向"}],"npcs":[{"name":"NPC姓名","public_identity":"公开身份","agenda":"独立议程","secret":"秘密","attitude":"初始态度","stats_note":"人类15-90或按规则书注记"}]}],"ending_signals":["如果[条件]，则[谁的处境如何变化]，[什么不可挽回地改变]"]}</schema>
<rules>
- 生成2-4个派系；每个派系timeline有2-3个节点，每个节点必须有intervention_pivot。
- NPC从派系中派生，不是独立线索容器；每个重要NPC必须能被KP作为静态NPC卡使用。
- mythos_anchor一旦写定，后续阶段不得更换；如果规则上下文不足，必须在rules_notes显式写“不确定/保守处理”。
- 不要写线性剧情门；时间线描述无人干预时世界如何运动。
- 如果收到qa_rejection，必须修复干预枢纽或派系自主性问题；不要只改措辞。
</rules>`

const factionMapExample = `{"mythos_anchor":"保守锚定：梦境相关典籍残页；具体法术效果需KP按规则书裁定","rules_notes":["规则上下文不足时保守处理，不赋予未核验法术数值"],"factions":[{"name":"邮政夜班","goal":"维持信件继续被取走以保护旧谎言","current_state":"正在销毁三十年前的投递登记","timeline":[{"node":"第0天：继续投递异常信件","trigger":"调查员进入邮局或夜晚到来","intervention_pivot":"取得登记簿可迫使他们承认旧谎言"},{"node":"第1天：转移剩余信件","trigger":"无人阻止且早班交接完成","intervention_pivot":"说服夜班员可暂停转移"}],"npcs":[{"name":"陈维舟","public_identity":"夜班分拣员","agenda":"保护已退休邮差","secret":"知道拆除地址仍有取信人","attitude":"紧张而防备","stats_note":"普通人类属性15-70"}]}],"ending_signals":["如果异常信件公开且母亲确认真相，则邮政夜班失去遮掩空间，但收信者会转向新的取信点"]}`

func generateFactionMap(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, seed FoundationSeed) (FactionMap, error) {
	ruleCtx, conservative := buildStage2RuleContext(ctx, seed)
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)
	seedJSON, _ := json.Marshal(seed)
	userPrompt := fmt.Sprintf(`<request_json>%s</request_json>
<constraints>%s</constraints>
<foundation_seed>%s</foundation_seed>
<difficulty_spec>
%s
</difficulty_spec>
<stage2_rule_context conservative="%v">
%s
</stage2_rule_context>
<recent_npc_name_blacklist>%s</recent_npc_name_blacklist>
请生成FactionMap。`, string(reqJSON), string(constraintsJSON), string(seedJSON), difficultySpec(room.req.Difficulty), conservative, ruleCtx, formatNPCNameBlacklist(room.npcBlacklist))
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(factionMapSystemPrompt)},
		{Role: "user", Content: userPrompt},
	}
	logStagePrompt("faction_map", msgs)
	var factions FactionMap
	if err := chatAndParseJSON(ctx, room.architect, room.parser, msgs, &factions, factionMapExample, "faction_map"); err != nil {
		return FactionMap{}, err
	}
	return normalizeFactionMap(factions, seed, conservative), nil
}

func normalizeFactionMap(factions FactionMap, seed FoundationSeed, conservative bool) FactionMap {
	factions.MythosAnchor = strings.TrimSpace(factions.MythosAnchor)
	if factions.MythosAnchor == "" {
		factions.MythosAnchor = "保守锚定：" + seed.MythosSeed
	}
	if conservative && !containsUncertaintyNote(factions.RulesNotes) {
		factions.RulesNotes = append(factions.RulesNotes, "规则核验上下文不完整；未确认元素按保守神话锚点处理，具体数值由KP按规则书裁定。")
	}
	if len(factions.Factions) == 0 {
		factions.Factions = []FactionPlan{{
			Name:         "旧决定的守护者",
			Goal:         "阻止外人理解异常与人类悲剧之间的关系",
			CurrentState: "正在销毁或重写能暴露旧决定的记录",
			Timeline: []TimelineNode{{
				Node:              "第0天：维持表面秩序",
				Trigger:           "调查员开始询问异常来源",
				InterventionPivot: "公开关键记录会迫使其改变行动",
			}},
			NPCs: []FactionNPC{{Name: "周砚", PublicIdentity: "地方办事员", Agenda: "维持旧决定不被公开", Secret: "知道异常的真实来源但选择隐瞒", Attitude: "礼貌回避", StatsNote: "普通人类属性15-70"}},
		}}
	}
	if len(factions.EndingSignals) == 0 {
		factions.EndingSignals = []string{"如果调查员让关键派系承认旧决定，则异常的社会遮掩被打破，但神话锚点会寻找新的承载者。"}
	}
	return factions
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
// Stage 2 FactionMap QA session
// ---------------------------------------------------------------------------

const factionMapQASystemPrompt = `<role>COC7沙盒派系QA</role>
<task>审核FactionMap是否符合沙盒设计原则：派系自主行动、有效干预枢纽、代价式结局信号。不审核规则书内容（Stage2已完成锚定）。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：说明本轮审核计划。
  {"action":"think","think":"计划"}
- response：最终审核结论。必须先输出至少一个think轮次后才能response。
  {"action":"response","pass":true,"reason":"审核理由","reject_reasons":[],"suggested_fix":"给architect的具体修改方向"}
</tools>
<batch_rules>
- 第一轮必须包含think；第一轮禁止直接response。
- response必须包含pass、reason、reject_reasons、suggested_fix。
</batch_rules>
<audit_rules>
审核只关注以下四点，其他不管：
1. 派系自主性：每个派系必须有non-empty current_state，且timeline节点描述无人干预时的世界运动，而不是"等待调查员触发X"。如果所有派系的current_state都是空白或被动等待，pass=false。
2. 干预枢纽有效性：每个timeline节点的intervention_pivot必须描述一个具体的可执行动作，而不是"调查员可以干预"这种空话。如果所有节点的intervention_pivot都缺乏具体动作，pass=false。
3. NPC-派系绑定：每个NPC必须有属于其所在派系的具体agenda和secret，不能是空白或"协助调查员"式的被动描述。
4. 结局信号格式：ending_signals必须描述"如果[条件]，则[谁失去什么/得到什么]，[什么不可挽回地改变]"，不能是简单的"调查员赢了/输了"。
不审核：规则书准确性、神话锚点的规则合规性、NPC属性数值、世界/地点细节。
</audit_rules>`

const factionMapQAToolCallExample = `[{"action":"think","think":"审核派系自主性和干预枢纽有效性。"},{"action":"response","pass":true,"reason":"每个派系有明确当前行动，干预枢纽有具体动作，NPC议程与派系目标绑定，结局信号描述代价分配。","reject_reasons":[],"suggested_fix":"无需修改。"}]`

type factionMapSession struct {
	room          *scripterRoom
	constraints   ScripterConstraints
	seed          FoundationSeed
	ruleCtx       string
	conservative  bool
	architectMsgs []llm.ChatMessage
	qaMsgs        []llm.ChatMessage
}

func newFactionMapSession(room *scripterRoom, constraints ScripterConstraints, seed FoundationSeed, ruleCtx string, conservative bool) *factionMapSession {
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)
	seedJSON, _ := json.Marshal(seed)
	architectPrompt := fmt.Sprintf(`<request_json>%s</request_json>
<constraints>%s</constraints>
<foundation_seed>%s</foundation_seed>
<difficulty_spec>
%s
</difficulty_spec>
<stage2_rule_context conservative="%v">
%s
</stage2_rule_context>
<recent_npc_name_blacklist>%s</recent_npc_name_blacklist>
请生成第1版FactionMap。`, string(reqJSON), string(constraintsJSON), string(seedJSON), difficultySpec(room.req.Difficulty), conservative, ruleCtx, formatNPCNameBlacklist(room.npcBlacklist))
	qaPrompt := fmt.Sprintf(`<foundation_seed>%s</foundation_seed>
你是持续运行的FactionMap QA会话。每次收到<faction_map_candidate>后，通过think/response工具调用审核它。`, string(seedJSON))
	return &factionMapSession{
		room:         room,
		constraints:  constraints,
		seed:         seed,
		ruleCtx:      ruleCtx,
		conservative: conservative,
		architectMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.architect.systemPrompt(factionMapSystemPrompt)},
			{Role: "user", Content: architectPrompt},
		},
		qaMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.qa.systemPrompt(factionMapQASystemPrompt)},
			{Role: "user", Content: qaPrompt},
		},
	}
}

func (s *factionMapSession) generate(ctx context.Context, attempt int) (FactionMap, error) {
	logStagePrompt(fmt.Sprintf("faction_map_attempt_%d", attempt), s.architectMsgs)
	var factions FactionMap
	if err := chatAndParseJSON(ctx, s.room.architect, s.room.parser, s.architectMsgs, &factions, factionMapExample, "faction_map"); err != nil {
		return FactionMap{}, err
	}
	factions = normalizeFactionMap(factions, s.seed, s.conservative)
	factionsJSON, _ := json.Marshal(factions)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{Role: "assistant", Content: string(factionsJSON)})
	log.Printf("[scripter:faction_map] attempt=%d generated anchor=%q factions=%d", attempt, truncateRunes(factions.MythosAnchor, 300), len(factions.Factions))
	return factions, nil
}

func (s *factionMapSession) review(ctx context.Context, attempt int, factions FactionMap) (SandboxQA, error) {
	factionsJSON, _ := json.Marshal(factions)
	s.qaMsgs = append(s.qaMsgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(`<faction_map_candidate attempt="%d">%s</faction_map_candidate>
请审核这个候选。`, attempt, string(factionsJSON))})
	qa, msgs, err := runSandboxQALoop(ctx, s.room, s.qaMsgs, factionMapQAToolCallExample, "faction_map")
	s.qaMsgs = msgs
	return qa, err
}

func (s *factionMapSession) feedRejection(attempt int, factions FactionMap, qa SandboxQA) {
	factionsJSON, _ := json.Marshal(factions)
	qaJSON, _ := json.Marshal(qa)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(`<qa_rejection attempt="%d">
<rejected_factions>%s</rejected_factions>
<qa_result>%s</qa_result>
</qa_rejection>
请基于同一个创作上下文重写FactionMap：必须解决QA指出的沙盒结构问题；不要只改措辞；仍只输出合法JSON对象。`, attempt, string(factionsJSON), string(qaJSON))})
}

func generateFactionMapWithQA(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, seed FoundationSeed) (FactionMap, error) {
	ruleCtx, conservative := buildStage2RuleContext(ctx, seed)
	session := newFactionMapSession(room, constraints, seed, ruleCtx, conservative)
	const maxAttempts = 20
	var lastQA *SandboxQA
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		factions, err := session.generate(ctx, attempt)
		if err != nil {
			return FactionMap{}, err
		}
		qa, err := session.review(ctx, attempt, factions)
		if err != nil {
			return FactionMap{}, err
		}
		lastQA = &qa
		log.Printf("[scripter:faction_map_qa] attempt=%d pass=%v reason=%q rejects=%q", attempt, qa.Pass, truncateRunes(qa.Reason, 500), strings.Join(qa.RejectReasons, " | "))
		logScripterArtifact(fmt.Sprintf("Stage 2 FactionMap QA Attempt %d", attempt), qa)
		if qa.Pass {
			return factions, nil
		}
		session.feedRejection(attempt, factions, qa)
	}
	rejects := sandboxQARejectReasons(lastQA)
	return FactionMap{}, fmt.Errorf("FactionMap QA 连续拒绝 %d 次，拒绝原因=%v", maxAttempts, rejects)
}

type WorldState struct {
	Locations    []LocationState `json:"locations"`
	HorrorLayers HorrorLayers    `json:"horror_layers"`
	ClueFacts    []ClueFact      `json:"clue_facts"`
}

type LocationState struct {
	Name           string       `json:"name"`
	SurfaceVisible string       `json:"surface_visible"`
	Discoverable   string       `json:"discoverable"`
	DeepLayer      string       `json:"deep_layer"`
	Levers         []WorldLever `json:"levers"`
	Noise          []string     `json:"noise"`
}

type WorldLever struct {
	Action string `json:"action"`
	Change string `json:"change"`
}

type HorrorLayers struct {
	Surface string `json:"surface"`
	Middle  string `json:"middle"`
	Core    string `json:"core"`
}

type ClueFact struct {
	Layer       string `json:"layer"`
	Fact        string `json:"fact"`
	Source      string `json:"source"`
	Acquisition string `json:"acquisition"`
}

const worldStateSystemPrompt = `<role>COC7沙盒世界状态设计师</role>
<task>从FoundationSeed、FactionMap和结构约束生成WorldState。不要查询规则书，不要更换mythos_anchor。</task>
<output>只输出合法JSON对象，不要Markdown、标题、解释或代码围栏。</output>
<schema>{"locations":[{"name":"地点名","surface_visible":"到达即可见","discoverable":"主动询问/检查/交涉后可得","deep_layer":"触及核心层后才感知","levers":[{"action":"调查员行动","change":"世界状态如何变化"}],"noise":["不解释自己的非必要细节"]}],"horror_layers":{"surface":"表面可见条件","middle":"深入调查或时间推进可见","core":"主动追查并付出代价可见"},"clue_facts":[{"layer":"real|hidden|misleading","fact":"从地点和NPC状态中抽出的自包含事实","source":"地点/NPC/物件","acquisition":"获得方式"}]}</schema>
<rules>
- locations描述当前状态，不是访问顺序；每个重要地点至少有一个可行动杠杆。
- 重要NPC如果不在地点中出现，也要通过地点、证词或物件给出可作用的杠杆。
- noise不是隐藏线索，不要把噪声转换成必经答案。
- clue_facts只能从地点、派系、NPC当前状态中抽取；不是独立线索迷宫。
- 如果收到qa_rejection，必须修复派系杠杆覆盖或线索可达性问题；不要只改措辞。
</rules>`

const worldStateExample = `{"locations":[{"name":"旧邮局分拣室","surface_visible":"夜班灯常亮，退信箱里有写给不存在地址的新信。","discoverable":"查登记簿可发现投递路线被同一人手写修改。","deep_layer":"信封内侧有像梦中潮湿盐霜一样的痕迹，读久后会想起从未去过的门牌。","levers":[{"action":"公开登记簿","change":"邮政夜班派系必须从销毁证据转为解释旧谎言"}],"noise":["墙上有一张与事件无关的过期剧院海报"]}],"horror_layers":{"surface":"异常信件和人为遮掩可以被普通谎言解释","middle":"信件持续回应无人能知道的问题，普通解释失效","core":"取信点不是地点而是神话锚点寻找收件人的方式"},"clue_facts":[{"layer":"real","fact":"投递登记被同一只手连续改写三十年，说明异常有长期人为维护者。","source":"旧邮局登记簿","acquisition":"会计、图书馆利用或说服夜班员"},{"layer":"hidden","fact":"不存在地址收到的回信回答了近期才发生的问题，说明通信并不受正常时间限制。","source":"未投递信件内页","acquisition":"拆阅信件并承担法律/道德风险"}]}`

func generateWorldState(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, seed FoundationSeed, factions FactionMap) (WorldState, error) {
	constraintsJSON, _ := json.Marshal(constraints)
	seedJSON, _ := json.Marshal(seed)
	factionsJSON, _ := json.Marshal(factions)
	userPrompt := fmt.Sprintf(`<constraints>%s</constraints>
<foundation_seed>%s</foundation_seed>
<faction_map>%s</faction_map>
<fixed_mythos_anchor>%s</fixed_mythos_anchor>
<length>%s</length>
<difficulty_spec>
%s
</difficulty_spec>
请生成WorldState。`, string(constraintsJSON), string(seedJSON), string(factionsJSON), factions.MythosAnchor, lengthSpec(room.req.TargetLength), difficultySpec(room.req.Difficulty))
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(worldStateSystemPrompt)},
		{Role: "user", Content: userPrompt},
	}
	logStagePrompt("world_state", msgs)
	var world WorldState
	if err := chatAndParseJSON(ctx, room.architect, room.parser, msgs, &world, worldStateExample, "world_state"); err != nil {
		return WorldState{}, err
	}
	return normalizeWorldState(world, seed, factions), nil
}

func normalizeWorldState(world WorldState, seed FoundationSeed, factions FactionMap) WorldState {
	if len(world.Locations) == 0 {
		world.Locations = []LocationState{{
			Name:           "调查入口",
			SurfaceVisible: seed.Anomaly,
			Discoverable:   seed.MythosSeed,
			DeepLayer:      "所有异常最终指向神话锚点：" + factions.MythosAnchor,
			Levers:         []WorldLever{{Action: "公开异常事实", Change: "相关派系必须暴露各自对异常的解释和利益"}},
			Noise:          []string{"一个与核心真相无关但具体的地方习惯仍照常发生。"},
		}}
	}
	for i := range world.Locations {
		if len(world.Locations[i].Levers) == 0 {
			world.Locations[i].Levers = []WorldLever{{Action: "主动调查或公开此处信息", Change: "至少一个派系的时间线改变方向或提前暴露"}}
		}
	}
	if strings.TrimSpace(world.HorrorLayers.Surface) == "" {
		world.HorrorLayers.Surface = seed.Anomaly
	}
	if strings.TrimSpace(world.HorrorLayers.Middle) == "" {
		world.HorrorLayers.Middle = "普通解释无法同时解释异常事实和派系遮掩。"
	}
	if strings.TrimSpace(world.HorrorLayers.Core) == "" {
		world.HorrorLayers.Core = "神话锚点显现为：" + factions.MythosAnchor
	}
	if len(world.ClueFacts) == 0 {
		world.ClueFacts = []ClueFact{{Layer: "real", Fact: seed.Anomaly, Source: world.Locations[0].Name, Acquisition: "到达并主动检查公开异常"}}
	}
	return world
}

// ---------------------------------------------------------------------------
// Stage 3 WorldState QA session
// ---------------------------------------------------------------------------

const worldStateQASystemPrompt = `<role>COC7沙盒世界状态QA</role>
<task>审核WorldState是否符合沙盒设计原则：地点为状态而非顺序门、派系有杠杆覆盖、线索可达、噪声无意义。不审核规则书内容。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：说明本轮审核计划。
  {"action":"think","think":"计划"}
- response：最终审核结论。必须先输出至少一个think轮次后才能response。
  {"action":"response","pass":true,"reason":"审核理由","reject_reasons":[],"suggested_fix":"给architect的具体修改方向"}
</tools>
<batch_rules>
- 第一轮必须包含think；第一轮禁止直接response。
- response必须包含pass、reason、reject_reasons、suggested_fix。
</batch_rules>
<audit_rules>
审核只关注以下四点，其他不管：
1. 派系杠杆覆盖：faction_map中每个有timeline的派系，必须在至少一个location的levers[]中有对应动作（做X则该派系时间线改变）；如果有派系在所有location的levers中完全消失，pass=false。
2. 入口可达性：至少有一个surface_visible或discoverable项目能让调查员有地方起步；不能所有关键事实都只在deep_layer（需要已知答案才能触及）。
3. 噪声纪律：noise[]中的项目不能是实际上指向唯一答案的必经线索；如果某条noise事实上是通向核心真相的必经信息，pass=false。
4. 线索来源可追溯：每条clue_facts中的fact必须能追溯到某个location、NPC或physical object；不能凭空出现。
不审核：神话锚点规则合规性、NPC属性数值、叙事质量、地点戏剧张力、Stage2派系设计细节。
</audit_rules>`

const worldStateQAToolCallExample = `[{"action":"think","think":"审核每个有timeline的派系是否在某地点levers中出现。"},{"action":"response","pass":true,"reason":"每个派系在至少一个地点有对应levers，入口有surface_visible线索，噪声不指向唯一答案，clue_facts有明确来源。","reject_reasons":[],"suggested_fix":"无需修改。"}]`

type worldStateSession struct {
	room          *scripterRoom
	constraints   ScripterConstraints
	seed          FoundationSeed
	factions      FactionMap
	architectMsgs []llm.ChatMessage
	qaMsgs        []llm.ChatMessage
}

func newWorldStateSession(room *scripterRoom, constraints ScripterConstraints, seed FoundationSeed, factions FactionMap) *worldStateSession {
	constraintsJSON, _ := json.Marshal(constraints)
	seedJSON, _ := json.Marshal(seed)
	factionsJSON, _ := json.Marshal(factions)
	architectPrompt := fmt.Sprintf(`<constraints>%s</constraints>
<foundation_seed>%s</foundation_seed>
<faction_map>%s</faction_map>
<fixed_mythos_anchor>%s</fixed_mythos_anchor>
<length>%s</length>
<difficulty_spec>
%s
</difficulty_spec>
请生成第1版WorldState。`, string(constraintsJSON), string(seedJSON), string(factionsJSON), factions.MythosAnchor, lengthSpec(room.req.TargetLength), difficultySpec(room.req.Difficulty))
	qaPrompt := fmt.Sprintf(`<foundation_seed>%s</foundation_seed>
<faction_map>%s</faction_map>
你是持续运行的WorldState QA会话。每次收到<world_state_candidate>后，通过think/response工具调用审核它。`, string(seedJSON), string(factionsJSON))
	return &worldStateSession{
		room:        room,
		constraints: constraints,
		seed:        seed,
		factions:    factions,
		architectMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.architect.systemPrompt(worldStateSystemPrompt)},
			{Role: "user", Content: architectPrompt},
		},
		qaMsgs: []llm.ChatMessage{
			{Role: "system", Content: room.qa.systemPrompt(worldStateQASystemPrompt)},
			{Role: "user", Content: qaPrompt},
		},
	}
}

func (s *worldStateSession) generate(ctx context.Context, attempt int) (WorldState, error) {
	logStagePrompt(fmt.Sprintf("world_state_attempt_%d", attempt), s.architectMsgs)
	var world WorldState
	if err := chatAndParseJSON(ctx, s.room.architect, s.room.parser, s.architectMsgs, &world, worldStateExample, "world_state"); err != nil {
		return WorldState{}, err
	}
	world = normalizeWorldState(world, s.seed, s.factions)
	worldJSON, _ := json.Marshal(world)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{Role: "assistant", Content: string(worldJSON)})
	log.Printf("[scripter:world_state] attempt=%d generated locations=%d clue_facts=%d", attempt, len(world.Locations), len(world.ClueFacts))
	return world, nil
}

func (s *worldStateSession) review(ctx context.Context, attempt int, world WorldState) (SandboxQA, error) {
	worldJSON, _ := json.Marshal(world)
	s.qaMsgs = append(s.qaMsgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(`<world_state_candidate attempt="%d">%s</world_state_candidate>
请审核这个候选。`, attempt, string(worldJSON))})
	qa, msgs, err := runSandboxQALoop(ctx, s.room, s.qaMsgs, worldStateQAToolCallExample, "world_state")
	s.qaMsgs = msgs
	return qa, err
}

func (s *worldStateSession) feedRejection(attempt int, world WorldState, qa SandboxQA) {
	worldJSON, _ := json.Marshal(world)
	qaJSON, _ := json.Marshal(qa)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(`<qa_rejection attempt="%d">
<rejected_world>%s</rejected_world>
<qa_result>%s</qa_result>
</qa_rejection>
请基于同一个创作上下文重写WorldState：必须解决QA指出的沙盒结构问题；不要只改措辞；仍只输出合法JSON对象。`, attempt, string(worldJSON), string(qaJSON))})
}

func generateWorldStateWithQA(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, seed FoundationSeed, factions FactionMap) (WorldState, error) {
	session := newWorldStateSession(room, constraints, seed, factions)
	const maxAttempts = 20
	var lastQA *SandboxQA
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		world, err := session.generate(ctx, attempt)
		if err != nil {
			return WorldState{}, err
		}
		qa, err := session.review(ctx, attempt, world)
		if err != nil {
			return WorldState{}, err
		}
		lastQA = &qa
		log.Printf("[scripter:world_state_qa] attempt=%d pass=%v reason=%q rejects=%q", attempt, qa.Pass, truncateRunes(qa.Reason, 500), strings.Join(qa.RejectReasons, " | "))
		logScripterArtifact(fmt.Sprintf("Stage 3 WorldState QA Attempt %d", attempt), qa)
		if qa.Pass {
			return world, nil
		}
		session.feedRejection(attempt, world, qa)
	}
	rejects := sandboxQARejectReasons(lastQA)
	return WorldState{}, fmt.Errorf("WorldState QA 连续拒绝 %d 次，拒绝原因=%v", maxAttempts, rejects)
}

const assemblySystemPrompt = `<role>COC7沙盒ScenarioDraft编译器</role>
<task>把FoundationSeed、FactionMap和WorldState编译为兼容models.ScenarioContent的ScenarioDraft。不要查询规则书，不要更换mythos_anchor。</task>
<output>只输出合法JSON对象，不要Markdown、标题、解释或代码围栏。</output>
<director_contract>
- content.setting写玩家当前能看见的局势，不是幕后背景倾倒。
- content.intro写入场位置和立即可做的行动。
- content.map_description写可导航地点关系。
- content.scenes是地点/局势状态摘要，不是有序剧情门；description必须包含可见信息、可发现信息、杠杆、风险、出口。
- content.npcs是静态NPC卡：派系、议程、秘密、态度，可选stats。
- content.clues是自包含事实字符串，必须以[真实]/[隐藏]/[误导]开头；不要依赖数组顺序理解。
- content.clues中必须包含至少一条以'[隐藏]神话本质'开头的线索，描述玩家调查中能发现的神话核心真相（实体本质、典籍目的或仪式代价）；必须注明获取方式和发现后需承担的代价。
- win_condition/lose_condition/partial_wins写代价分配和结束信号，不写二元奖励。
- system_prompt必须给KP时间推进、信息可见性和不主动引导三项协议。
- 如果收到qa_rejection，必须修复setting快照、intro可执行性、scene杠杆或KP协议问题；不要只改措辞；不要更换mythos_anchor。
</director_contract>`

const scenarioExample = `{"name":"示例沙盒模组名","description":"一份围绕异常事实、派系时间线和调查员可拉动杠杆展开的COC情境简报。","author":"agent-team","tags":"sandbox,coc","min_players":1,"max_players":4,"difficulty":"normal","content":{"system_prompt":"你是本场COC跑团的KP，职责是管理一个会自行推进的局势而不是执行线性故事。按派系时间线推进后果；按可见性分层给信息；不要主动把调查员引向正确答案。","setting":"玩家抵达时能看见的当前局势。只写公开事实、紧张关系和可感知异常，不剧透幕后真相。","intro":"你们以某种身份进入局势，眼前有三件可立即行动的事：询问某人、检查某地、决定是否公开某条信息。","game_start_slot":16,"map_description":"【文字地图】起点A连接地点B和地点C；地点B有公开冲突，地点C有可深入调查的物件；各地点可往返，没有固定访问顺序。","scenes":[{"id":"location_1","name":"地点名","description":"可见：到达即可看到的信息。可发现：主动调查可获得的信息。杠杆：调查员若做X，派系Y的时间线会改变。风险：拖延或失败的后果。出口：可前往的其他地点。","triggers":["available_from_start"]}],"npcs":[{"name":"NPC姓名","description":"公开身份；所属派系；真实议程；秘密；可被说服或施压的杠杆；可能知道但不会主动说出的事实。","attitude":"初始态度和压力下的反应","stats":{"STR":50,"CON":50,"SIZ":50,"DEX":50,"APP":50,"INT":60,"POW":50,"EDU":60,"HP":10,"MP":10}}],"clues":["[真实]登记簿矛盾(旧办公室): 自包含事实；获取方式；能改变哪个判断。","[隐藏]神话本质(封存物件): 神话核心真相（实体性质/典籍目的/仪式代价）；获取方式（需要什么技能或条件）；发现后要承担的代价（理智/后果）。","[误导]地方传闻(酒馆): 为什么它表面合理但只能解释一部分。"],"win_condition":"如果调查员让某个结束信号以较低代价固化，则对应派系失去关键优势，但另一个代价留存。","lose_condition":"如果关键时间线终点到达且无人干预，则局势进入新的稳定态，某人或某地不可挽回地改变。","partial_wins":["如果只救出某人但没有公开事实，则个人获救，派系结构保留。"]}}`

func assembleSandboxDraft(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, seed FoundationSeed, factions FactionMap, world WorldState, previous *ScenarioDraft, mustFix []string) (ScenarioDraft, error) {
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)
	seedJSON, _ := json.Marshal(seed)
	factionsJSON, _ := json.Marshal(factions)
	worldJSON, _ := json.Marshal(world)
	revisionBlock := ""
	if previous != nil || len(mustFix) > 0 {
		prevJSON, _ := json.Marshal(previous)
		revisionBlock = fmt.Sprintf("\n<previous_draft>%s</previous_draft>\n<must_fix>%s</must_fix>\n请只修复must_fix列出的DB/runtime关键字段，保持神话锚点、线索自包含性和沙盒语义不变。", string(prevJSON), strings.Join(mustFix, "\n- "))
	}
	userPrompt := fmt.Sprintf(`<request_json>%s</request_json>
<constraints>%s</constraints>
<foundation_seed>%s</foundation_seed>
<faction_map>%s</faction_map>
<world_state>%s</world_state>
<fixed_mythos_anchor>%s</fixed_mythos_anchor>
<json_example>%s</json_example>
<length>%s</length>
<difficulty_spec>
%s
</difficulty_spec>
<recent_npc_name_blacklist>%s</recent_npc_name_blacklist>
<title_samples_to_avoid>%s</title_samples_to_avoid>%s
请输出完整ScenarioDraft JSON。`, string(reqJSON), string(constraintsJSON), string(seedJSON), string(factionsJSON), string(worldJSON), factions.MythosAnchor, scenarioExample, lengthSpec(room.req.TargetLength), difficultySpec(room.req.Difficulty), formatNPCNameBlacklist(room.npcBlacklist), formatScenarioTitleBlacklist(room.titleSamples), revisionBlock)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(assemblySystemPrompt)},
		{Role: "user", Content: userPrompt},
	}
	if previous != nil || len(mustFix) > 0 {
		log.Printf("[scripter:scenario_draft] repair_mode previous_present=%v must_fix=%d issues=%v", previous != nil, len(mustFix), mustFix)
	}
	logStagePrompt("scenario_draft", msgs)
	var draft ScenarioDraft
	if err := chatAndParseJSON(ctx, room.architect, room.parser, msgs, &draft, scenarioExample, "scenario_draft"); err != nil {
		return ScenarioDraft{}, err
	}
	return draft, nil
}

// ---------------------------------------------------------------------------
// Stage 4 Assembly QA session
// ---------------------------------------------------------------------------

const assemblyQASystemPrompt = `<role>COC7沙盒剧本编译QA</role>
<task>审核ScenarioDraft是否符合Director runtime合约：setting是局势快照而非背景倾倒、intro有立即可执行动作、scene描述包含杠杆、clue自包含、KP协议完整。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：说明本轮审核计划。
  {"action":"think","think":"计划"}
- response：最终审核结论。必须先输出至少一个think轮次后才能response。
  {"action":"response","pass":true,"reason":"审核理由","reject_reasons":[],"suggested_fix":"给architect的具体修改方向"}
</tools>
<batch_rules>
- 第一轮必须包含think；第一轮禁止直接response。
- response必须包含pass、reason、reject_reasons、suggested_fix。
</batch_rules>
<audit_rules>
审核只关注以下五点，其他不管：
1. setting局势快照：setting只能包含玩家到达时能直接观察到的事实、紧张关系和可感知异常；不能包含幕后设定、NPC秘密、隐藏事实或玩家不能观察到的信息。
2. intro可执行性：intro必须为玩家提供至少2个立即可采取的行动选项（询问某人、检查某地、决定公开某信息等具体选项）；不能只是"你们抵达了地点"这种纯描述。
3. scene杠杆存在：至少有一个scene的description包含明确的杠杆描述（调查员做X会改变局势的说明）；如果所有scene都只有描述没有任何杠杆，pass=false。
4. clue自包含：每条clue必须包含[真实]/[隐藏]/[误导]前缀、事实本身和获取方式；不能有需要结合其他clue才能理解的条目，不能有"参见其他线索"式的引用。
5. KP协议完整性：system_prompt必须包含时间推进协议（派系时间线如何推进）、信息可见性协议（按层级给信息）、不主动引导协议（不把玩家引向正确答案）三项；缺少任意一项pass=false。
6. 神话本质线索：clues中必须至少有一条以'[隐藏]神话本质'开头的线索，描述玩家在调查中能发现的神话核心真相（实体本质、典籍内容或仪式目的）并注明获取方式和代价；缺少此条pass=false。
不审核：派系设计细节（Stage2已审）、世界状态细节（Stage3已审）、规则书合规性、NPC属性数值、游戏时长判断。
</audit_rules>`

const assemblyQAToolCallExample = `[{"action":"think","think":"审核setting是否只含可观察事实，intro是否有具体行动选项。"},{"action":"response","pass":true,"reason":"setting只含公开可见局势，intro提供了3个具体行动，每个scene有杠杆描述，clues自包含，system_prompt包含三项KP协议。","reject_reasons":[],"suggested_fix":"无需修改。"}]`

type assemblySession struct {
	room          *scripterRoom
	constraints   ScripterConstraints
	seed          FoundationSeed
	factions      FactionMap
	world         WorldState
	architectMsgs []llm.ChatMessage
	qaMsgs        []llm.ChatMessage
}

func newAssemblySession(room *scripterRoom, constraints ScripterConstraints, seed FoundationSeed, factions FactionMap, world WorldState) *assemblySession {
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)
	seedJSON, _ := json.Marshal(seed)
	factionsJSON, _ := json.Marshal(factions)
	worldJSON, _ := json.Marshal(world)
	architectPrompt := fmt.Sprintf(`<request_json>%s</request_json>
<constraints>%s</constraints>
<foundation_seed>%s</foundation_seed>
<faction_map>%s</faction_map>
<world_state>%s</world_state>
<fixed_mythos_anchor>%s</fixed_mythos_anchor>
<json_example>%s</json_example>
<length>%s</length>
<difficulty_spec>
%s
</difficulty_spec>
<recent_npc_name_blacklist>%s</recent_npc_name_blacklist>
<title_samples_to_avoid>%s</title_samples_to_avoid>
请生成第1版ScenarioDraft。`, string(reqJSON), string(constraintsJSON), string(seedJSON), string(factionsJSON), string(worldJSON), factions.MythosAnchor, scenarioExample, lengthSpec(room.req.TargetLength), difficultySpec(room.req.Difficulty), formatNPCNameBlacklist(room.npcBlacklist), formatScenarioTitleBlacklist(room.titleSamples))
	qaPrompt := fmt.Sprintf(`<foundation_seed>%s</foundation_seed>
你是持续运行的Assembly QA会话。每次收到<draft_candidate>后，通过think/response工具调用审核它。`, string(seedJSON))
	return &assemblySession{
		room:        room,
		constraints: constraints,
		seed:        seed,
		factions:    factions,
		world:       world,
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
	if err := chatAndParseJSON(ctx, s.room.architect, s.room.parser, s.architectMsgs, &draft, scenarioExample, "scenario_draft"); err != nil {
		return ScenarioDraft{}, err
	}
	draftJSON, _ := json.Marshal(draft)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{Role: "assistant", Content: string(draftJSON)})
	log.Printf("[scripter:assembly] attempt=%d generated name=%q scenes=%d npcs=%d clues=%d", attempt, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	return draft, nil
}

func (s *assemblySession) review(ctx context.Context, attempt int, draft ScenarioDraft) (SandboxQA, error) {
	draftJSON, _ := json.Marshal(draft)
	s.qaMsgs = append(s.qaMsgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(`<draft_candidate attempt="%d">%s</draft_candidate>
请审核这个候选。`, attempt, string(draftJSON))})
	qa, msgs, err := runSandboxQALoop(ctx, s.room, s.qaMsgs, assemblyQAToolCallExample, "assembly")
	s.qaMsgs = msgs
	return qa, err
}

func (s *assemblySession) feedRejection(attempt int, draft ScenarioDraft, qa SandboxQA) {
	draftJSON, _ := json.Marshal(draft)
	qaJSON, _ := json.Marshal(qa)
	s.architectMsgs = append(s.architectMsgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(`<qa_rejection attempt="%d">
<rejected_draft>%s</rejected_draft>
<qa_result>%s</qa_result>
</qa_rejection>
请基于同一个创作上下文重写ScenarioDraft：必须解决QA指出的runtime合约问题；不要只改措辞；不要更换mythos_anchor；仍只输出合法JSON对象。`, attempt, string(draftJSON), string(qaJSON))})
}

func assembleSandboxDraftWithQA(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, seed FoundationSeed, factions FactionMap, world WorldState) (ScenarioDraft, error) {
	session := newAssemblySession(room, constraints, seed, factions, world)
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
// Rulebook context helper for Stage 2 only
// ---------------------------------------------------------------------------

func buildStage2RuleContext(ctx context.Context, seed FoundationSeed) (string, bool) {
	var sb strings.Builder
	conservative := false
	log.Printf("[scripter:rule_context] start mythos_seed=%q relation=%q", truncateRunes(seed.MythosSeed, 500), seed.MythosRelation)
	sb.WriteString("【规则书常量摘要，仅供Stage2锚定神话元素】\n")
	for _, constant := range []string{"mythos_creatures", "monsters", "great_old_ones_and_gods", "books", "spells"} {
		text := strings.TrimSpace(rulebook.ReadConstant(constant))
		log.Printf("[scripter:rule_context] const=%q len=%d", constant, len(text))
		if text == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n[%s]\n%s\n", constant, truncateRunes(text, 1200)))
	}

	question := fmt.Sprintf("为COC7沙盒剧本核验一个最小神话锚点。异常：%s。神话关系：%s。候选方向：%s。请只给可保守使用的实体/典籍/法术/物品方向和必须避免的未核验数值。", seed.Anomaly, seed.MythosRelation, seed.MythosSeed)
	log.Printf("[scripter:rule_context] lawyer question len=%d body=%s", len(question), truncateRunes(question, scripterPromptLogLimit))
	lawyerHandle, err := loadSingleAgent(models.AgentRoleLawyer)
	if err != nil {
		conservative = true
		log.Printf("[scripter:rule_context] lawyer unavailable err=%v", err)
		sb.WriteString(fmt.Sprintf("\n【lawyer_unavailable】%v\n必须在rules_notes标记不确定元素，并避免生成未核验数值。\n", err))
		ctxText := truncateRunes(sb.String(), 9000)
		log.Printf("[scripter:rule_context] done conservative=%v len=%d body=%s", conservative, len(ctxText), truncateRunes(ctxText, scripterRepairLogLimit))
		return ctxText, conservative
	}
	results := runLawyer(ctx, lawyerHandle, question, rulebook.GlobalIndex)
	log.Printf("[scripter:rule_context] lawyer results=%d", len(results))
	if len(results) == 0 {
		conservative = true
		sb.WriteString("\n【lawyer_no_result】规则专家未返回有效裁定；必须在rules_notes标记不确定元素，并避免生成未核验数值。\n")
		ctxText := truncateRunes(sb.String(), 9000)
		log.Printf("[scripter:rule_context] done conservative=%v len=%d body=%s", conservative, len(ctxText), truncateRunes(ctxText, scripterRepairLogLimit))
		return ctxText, conservative
	}
	sb.WriteString("\n【lawyer_result】\n")
	sb.WriteString(formatLawyerResults(results))
	sb.WriteString("\n")
	ctxText := truncateRunes(sb.String(), 9000)
	log.Printf("[scripter:rule_context] done conservative=%v len=%d body=%s", conservative, len(ctxText), truncateRunes(ctxText, scripterRepairLogLimit))
	return ctxText, conservative
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

func normalizeDraftBeforeReturn(draft *ScenarioDraft, req ScenarioCreationRequest, constraints ScripterConstraints, seed FoundationSeed, factions FactionMap, world WorldState) {
	if draft == nil {
		return
	}
	if strings.TrimSpace(draft.Name) == "" {
		draft.Name = defaultScenarioName(seed)
		log.Printf("[scripter:normalize] filled name=%q", draft.Name)
	}
	if strings.TrimSpace(draft.Description) == "" {
		draft.Description = fmt.Sprintf("围绕“%s”展开的沙盒情境简报：调查员进入一个由旧决定、派系时间线和神话锚点共同推动的局势。", seed.Anomaly)
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
		draft.Content.SystemPrompt = defaultSandboxSystemPrompt(factions)
		log.Printf("[scripter:normalize] filled system_prompt len=%d", len(draft.Content.SystemPrompt))
	}
	if strings.TrimSpace(draft.Content.Setting) == "" {
		draft.Content.Setting = defaultSetting(constraints, seed, factions)
		log.Printf("[scripter:normalize] filled setting len=%d", len(draft.Content.Setting))
	}
	if strings.TrimSpace(draft.Content.Intro) == "" {
		draft.Content.Intro = defaultIntro(constraints, world)
		log.Printf("[scripter:normalize] filled intro len=%d", len(draft.Content.Intro))
	}
	if strings.TrimSpace(draft.Content.MapDescription) == "" {
		draft.Content.MapDescription = defaultMapDescription(world)
		log.Printf("[scripter:normalize] filled map_description len=%d", len(draft.Content.MapDescription))
	}
	if len(draft.Content.Scenes) == 0 {
		draft.Content.Scenes = scenesFromWorld(world)
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
		draft.Content.NPCs = npcsFromFactions(factions)
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
		draft.Content.Clues = cluesFromWorld(world, seed)
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
		draft.Content.WinCondition = defaultWinCondition(factions)
		log.Printf("[scripter:normalize] filled win_condition=%q", truncateRunes(draft.Content.WinCondition, 300))
	}
	if strings.TrimSpace(draft.Content.LoseCondition) == "" {
		draft.Content.LoseCondition = defaultLoseCondition(factions)
		log.Printf("[scripter:normalize] filled lose_condition=%q", truncateRunes(draft.Content.LoseCondition, 300))
	}
	if len(draft.Content.PartialWins) == 0 {
		draft.Content.PartialWins = defaultPartialWins(factions)
		log.Printf("[scripter:normalize] filled partial_wins count=%d", len(draft.Content.PartialWins))
	}
}

func defaultScenarioName(seed FoundationSeed) string {
	anomaly := truncateRunes(strings.TrimSpace(seed.Anomaly), 12)
	if anomaly == "" {
		return "未命名沙盒调查"
	}
	return "异常调查：" + anomaly
}

func defaultSandboxSystemPrompt(factions FactionMap) string {
	return fmt.Sprintf("你是本场COC跑团的KP，职责是管理会自行推进的局势而不是执行线性故事。按派系时间线推进后果；按表面可见、主动询问、需要行动、不可直接获得四层管理信息；不要主动把调查员引向正确答案。固定神话锚点：%s。", firstNonEmpty(factions.MythosAnchor, "按剧本规则注记处理"))
}

func defaultSetting(constraints ScripterConstraints, seed FoundationSeed, factions FactionMap) string {
	return fmt.Sprintf("%s的%s中，调查员面对一个已经开始运动的局势：%s。公开层面只看得到异常、地方压力和派系互相遮掩；无人干预时，各方会按自己的时间线继续行动。", constraints.Era, strings.Join(constraints.GeographyFlavor, " / "), seed.Anomaly+" 神话锚点已由KP后台固定为“"+factions.MythosAnchor+"”，但开局不向玩家直说")
}

func defaultIntro(constraints ScripterConstraints, world WorldState) string {
	locations := locationNames(world)
	if len(locations) == 0 {
		locations = []string{"调查入口"}
	}
	return fmt.Sprintf("你们以“%s”进入局势。眼前可立即行动：前往%s，询问公开目击者，或决定是否把已知异常告诉某个派系。", constraints.InvestigatorEntryPosition, strings.Join(locations, "、"))
}

func defaultMapDescription(world WorldState) string {
	locations := locationNames(world)
	if len(locations) == 0 {
		return "【文字地图】调查入口连接所有可调查地点；地点之间可往返，没有固定访问顺序。"
	}
	var lines []string
	lines = append(lines, "【文字地图】地点是沙盒状态节点，不是顺序关卡：")
	for i, name := range locations {
		if i == 0 {
			lines = append(lines, fmt.Sprintf("- %s：默认入口，可向其他地点扩散调查。", name))
		} else {
			lines = append(lines, fmt.Sprintf("- %s：可从入口或其他地点前往，返回不会重置局势。", name))
		}
	}
	lines = append(lines, "时间推进时，各地点状态可能因派系行动而改变。")
	return strings.Join(lines, "\n")
}

func scenesFromWorld(world WorldState) []models.SceneData {
	if len(world.Locations) == 0 {
		return []models.SceneData{{ID: "location_1", Name: "调查入口", Description: "可见：异常已经公开出现。可发现：主动调查可获得第一批事实。杠杆：公开或隐瞒信息会改变派系反应。风险：拖延会推进时间线。出口：所有相关地点。", Triggers: []string{"available_from_start"}}}
	}
	scenes := make([]models.SceneData, 0, len(world.Locations))
	for i, loc := range world.Locations {
		var leverParts []string
		for _, lever := range loc.Levers {
			leverParts = append(leverParts, fmt.Sprintf("%s → %s", lever.Action, lever.Change))
		}
		desc := fmt.Sprintf("可见：%s\n可发现：%s\n深层：%s\n杠杆：%s\n噪声：%s\n风险：拖延会让相关派系时间线推进；错误公开信息会改变NPC态度。\n出口：可返回其他地点继续调查。", firstNonEmpty(loc.SurfaceVisible, "地点表面状态"), firstNonEmpty(loc.Discoverable, "主动调查可获得的事实"), firstNonEmpty(loc.DeepLayer, "触及核心层后的感知"), firstNonEmpty(strings.Join(leverParts, "；"), "主动调查或公开信息 → 派系时间线改变"), firstNonEmpty(strings.Join(loc.Noise, "；"), "存在不指向答案的地方细节"))
		scenes = append(scenes, models.SceneData{ID: fmt.Sprintf("location_%d", i+1), Name: firstNonEmpty(loc.Name, fmt.Sprintf("地点%d", i+1)), Description: desc, Triggers: []string{"available_from_start"}})
	}
	return scenes
}

func npcsFromFactions(factions FactionMap) []models.NPCData {
	var npcs []models.NPCData
	for _, faction := range factions.Factions {
		for _, npc := range faction.NPCs {
			name := strings.TrimSpace(npc.Name)
			if name == "" {
				continue
			}
			desc := fmt.Sprintf("公开身份：%s。所属派系：%s。派系目标：%s。个人议程：%s。秘密：%s。当前状态：%s。规则注记：%s。", firstNonEmpty(npc.PublicIdentity, "未公开"), firstNonEmpty(faction.Name, "未知派系"), firstNonEmpty(faction.Goal, "未明"), firstNonEmpty(npc.Agenda, "自保并观察局势"), firstNonEmpty(npc.Secret, "掌握部分真相但不会主动全盘托出"), firstNonEmpty(faction.CurrentState, "按时间线行动"), firstNonEmpty(npc.StatsNote, "普通人类属性15-70；特殊能力按规则书裁定"))
			npcs = append(npcs, models.NPCData{Name: name, Description: desc, Attitude: firstNonEmpty(npc.Attitude, "谨慎防备")})
		}
	}
	if len(npcs) == 0 {
		npcs = []models.NPCData{{Name: "周砚", Description: "公开身份：地方协调者。所属派系：旧决定的守护者。真实议程：拖延调查并保护旧秘密。秘密：知道异常与某个可理解但错误的决定有关。", Attitude: "礼貌但回避关键问题"}}
	}
	return npcs
}

func cluesFromWorld(world WorldState, seed FoundationSeed) []string {
	clues := make([]string, 0, len(world.ClueFacts)+1)
	for _, fact := range world.ClueFacts {
		prefix := cluePrefixForLayer(fact.Layer)
		name := firstNonEmpty(fact.Source, "现场事实")
		body := firstNonEmpty(fact.Fact, seed.Anomaly)
		acquisition := firstNonEmpty(fact.Acquisition, "主动调查")
		clues = append(clues, fmt.Sprintf("%s%s: %s；获取方式：%s。", prefix, name, body, acquisition))
	}
	if len(clues) == 0 {
		clues = append(clues, "[真实]公开异常(调查入口): "+seed.Anomaly+"；获取方式：到达现场并主动询问或检查。")
	}
	return clues
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

func defaultWinCondition(factions FactionMap) string {
	if len(factions.EndingSignals) > 0 {
		return "较低代价结束信号：" + factions.EndingSignals[0]
	}
	return "如果调查员让关键事实公开并改变至少一个派系时间线，则局势以较低代价固化，但神话锚点的余波仍保留。"
}

func defaultLoseCondition(factions FactionMap) string {
	if len(factions.Factions) > 0 && len(factions.Factions[0].Timeline) > 0 {
		last := factions.Factions[0].Timeline[len(factions.Factions[0].Timeline)-1]
		return "高代价结束信号：无人干预时，" + firstNonEmpty(last.Node, factions.Factions[0].CurrentState) + "；此后局势进入新的稳定态。"
	}
	return "如果关键时间线终点到达且调查员没有改变任何派系行动，则局势进入新的稳定态，某人、某地或某个身份不可挽回地改变。"
}

func defaultPartialWins(factions FactionMap) []string {
	partials := make([]string, 0, len(factions.EndingSignals))
	for i, signal := range factions.EndingSignals {
		if i == 0 {
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

func locationNames(world WorldState) []string {
	names := make([]string, 0, len(world.Locations))
	for _, loc := range world.Locations {
		if strings.TrimSpace(loc.Name) != "" {
			names = append(names, strings.TrimSpace(loc.Name))
		}
	}
	return names
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
	Action     string          `json:"action"`
	Think      string          `json:"think,omitempty"`
	Question   string          `json:"question,omitempty"`
	Constant   string          `json:"constant,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	Background *FogBackground  `json:"background,omitempty"`
	Draft      *ScenarioDraft  `json:"draft,omitempty"`
	Foundation *FoundationSeed `json:"foundation_seed,omitempty"`
	Factions   *FactionMap     `json:"faction_map,omitempty"`
	World      *WorldState     `json:"world_state,omitempty"`
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
	if call.Foundation != nil {
		payloads++
	}
	if call.Factions != nil {
		payloads++
	}
	if call.World != nil {
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
	case "foundation_seed":
		if call.Foundation == nil {
			return fmt.Errorf("expected foundation_seed")
		}
	case "faction_map":
		if call.Factions == nil {
			return fmt.Errorf("expected faction_map")
		}
	case "world_state":
		if call.World == nil {
			return fmt.Errorf("expected world_state")
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
	case "foundation_seed":
		return `[{"action":"response","reason":"异常和人类悲剧先行。","foundation_seed":` + foundationSeedExample + `}]`
	case "faction_map":
		return `[{"action":"response","reason":"派系时间线围绕固定神话锚点运动。","faction_map":` + factionMapExample + `}]`
	case "world_state":
		return `[{"action":"response","reason":"地点状态提供可见层、深层和杠杆。","world_state":` + worldStateExample + `}]`
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
