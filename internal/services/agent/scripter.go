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
	parser, err := loadSingleAgent(models.AgentRoleParser)
	if err != nil {
		return nil, err
	}
	return &scripterRoom{architect: architect, qa: qa, parser: parser, req: normalizeScenarioCreationRequest(req)}, nil
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
	log.Printf("[scripter] stage=constraints done archetype=%q entry=%q topology=%q phase=%q register=%q geography=%q", constraints.SituationArchetype, constraints.InvestigatorEntryPosition, constraints.FactionTopology, constraints.TemporalPhase, constraints.ThematicRegister, strings.Join(constraints.GeographyFlavor, " → "))
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
	factions, err := generateFactionMap(ctx, r, constraints, seed)
	if err != nil {
		log.Printf("[scripter] stage=faction_map error=%v", err)
		return ScenarioCreationOutput{}, fmt.Errorf("Factions & Timelines 失败: %w", err)
	}
	log.Printf("[scripter] stage=faction_map done mythos_anchor=%q factions=%d rules_notes=%d ending_signals=%d", truncateRunes(factions.MythosAnchor, 300), len(factions.Factions), len(factions.RulesNotes), len(factions.EndingSignals))
	logScripterArtifact("Stage 2 Factions & Timelines", factions)

	log.Printf("[scripter] stage=world_state start mythos_anchor=%q", truncateRunes(factions.MythosAnchor, 300))
	world, err := generateWorldState(ctx, r, constraints, seed, factions)
	if err != nil {
		log.Printf("[scripter] stage=world_state error=%v", err)
		return ScenarioCreationOutput{}, fmt.Errorf("World Dressing 失败: %w", err)
	}
	log.Printf("[scripter] stage=world_state done locations=%d clue_facts=%d horror_surface=%q", len(world.Locations), len(world.ClueFacts), truncateRunes(world.HorrorLayers.Surface, 300))
	logScripterArtifact("Stage 3 World Dressing", world)

	log.Printf("[scripter] stage=assembly start")
	draft, err := assembleSandboxDraft(ctx, r, constraints, seed, factions, world, nil, nil)
	if err != nil {
		log.Printf("[scripter] stage=assembly error=%v", err)
		return ScenarioCreationOutput{}, fmt.Errorf("Assembly 失败: %w", err)
	}
	log.Printf("[scripter] stage=assembly done raw name=%q scenes=%d npcs=%d clues=%d", draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	applyGuardrails(&draft, r.req)
	log.Printf("[scripter] guardrails applied name=%q players=%d-%d difficulty=%q author=%q", draft.Name, draft.MinPlayers, draft.MaxPlayers, draft.Difficulty, draft.Author)
	iterations := 4
	if issues := validateDraftCompatibility(draft); len(issues) > 0 {
		log.Printf("[scripter] draft compatibility issues before repair count=%d issues=%v", len(issues), issues)
		log.Printf("[scripter] stage=assembly_repair start")
		if repaired, err := assembleSandboxDraft(ctx, r, constraints, seed, factions, world, &draft, issues); err == nil {
			draft = repaired
			log.Printf("[scripter] stage=assembly_repair done raw name=%q scenes=%d npcs=%d clues=%d", draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
			applyGuardrails(&draft, r.req)
			log.Printf("[scripter] guardrails applied after repair name=%q players=%d-%d difficulty=%q author=%q", draft.Name, draft.MinPlayers, draft.MaxPlayers, draft.Difficulty, draft.Author)
			iterations++
		} else {
			log.Printf("[scripter] focused repair failed, using normalized first draft: %v", err)
		}
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
	ThematicRegister          string   `json:"thematic_register"`
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
	"循环期：这不是第一次，规律被发现后威胁规模才显现",
}

var thematicRegisterCandidates = []string{
	"宇宙冷漠：更大的力量不在乎调查员成败",
	"人性腐败：超自然只是人类欲望的放大器",
	"悲剧必然：没人想让事情发生，但它还是发生了",
	"不可信任：无法判断谁说的是真话，包括盟友",
	"禁忌代价：知道某些事会让人希望自己没有调查",
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
		ThematicRegister:          pickCandidate(rng, thematicRegisterCandidates),
		Era:                       r.req.Era,
		Theme:                     firstNonEmpty(r.req.Theme, "克苏鲁调查"),
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
- 非country阶段只输出类型/形态/区位模式，不输出具体地名、真实行政区名、真实城市名或真实街区名。
- natural_geography阶段必须输出自然地理/地形/水文/气候约束类型。
- human_geography阶段必须输出人口密度/当地风俗文化/社会结构。
- 只输出现实地理/人文地理候选，不输出幕后真相。
- 禁止输出伪科学、高科技、工程化异常或可诱导伪科学解释神话的候选。
- 每行一个名称，正好50个，不要编号、解释、标题或描述句。</rules>`

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
		{Key: "natural_geography", Mode: "自然地理/地形/水文/气候约束类型，不输出具体地名", Examples: "林木覆盖的山谷"},
		{Key: "human_geography", Mode: "人口密度/当地风俗文化/社会结构，不输出具体地名", Examples: "城市"},
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
		if stage.Key == "human_geography" {
			items = append(items, "城市")
		}
		choice := items[rand.Intn(len(items))]
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
	prompt := fmt.Sprintf("已随机选中的前置布景：%s\n现在进入下一阶段：%s\n时代：%s\n输出要求：%s\n示例范围：%s\n\n请只输出本阶段的20个候选。", selected, stageKey, era, mode, examples)
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
	flavor := []string{firstNonEmpty(req.Era, defaultScripterEra)}
	if strings.TrimSpace(req.Theme) != "" {
		flavor = append(flavor, strings.TrimSpace(req.Theme))
	}
	flavor = append(flavor, "具备地方关系、交通阻力和可调查公共空间的地点")
	return flavor
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
	HumanTragedy   string `json:"human_tragedy"`
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

type FoundationSeedRuleCheck struct {
	Question    string `json:"question"`
	Result      string `json:"result"`
	HardFailure bool   `json:"hard_failure"`
}

const foundationSeedSystemPrompt = `<role>COC7沙盒基础种子设计师</role>
<task>只生成FoundationSeed JSON。这个阶段是纯创意阶段，不查询也不引用规则书。</task>
<output>只输出合法JSON对象，不要Markdown、标题、解释或代码围栏。</output>
<schema>{"anomaly":"具体、奇怪、无法立刻解释的事实","human_tragedy":"谁做了可理解但导致灾难的决定/为什么/造成什么","mythos_relation":"byproduct或consequence","mythos_seed":"待Stage2核验的神话元素方向"}</schema>
<rules>
- anomaly必须是具体事实，不是类型标签；读完就应让调查员想追问。
- human_tragedy必须先于派系和NPC存在，是整个局势的道德中心。
- mythos_relation只能是byproduct或consequence：byproduct=异常是神话力量副产品；consequence=神话是人类行为后果。
- mythos_seed只是方向，不做规则裁定，不编造数值。
- 用户brief若非空，必须保留其核心意图。
- 如果收到qa_rejection，必须重写异常与mythos_seed之间的关系；不要只改措辞。
</rules>`

const foundationSeedExample = `{"anomaly":"邮局每天把信投递到三十年前已经拆除的地址，仍有人在夜里取走这些信。","human_tragedy":"一名邮差为了让失踪孩子的母亲继续相信孩子还活着，开始伪造回信；多年后谎言被某种不属于人的通信方式接管。","mythos_relation":"consequence","mythos_seed":"与梦境、信件、非人传讯或典籍残页有关的神话锚点"}`

const foundationSeedQAExample = `{"pass":true,"reason":"check_rule结果显示该方向可以保守接入梦境、典籍或非人传讯等规则书神话元素；异常与mythos_seed存在可核验桥接。","reject_reasons":[],"suggested_scope":"保留非线性通信方向，Stage2锁定具体典籍或实体时继续保守标注。","rule_check_summary":"check_rule返回了可用的规则书神话方向，未要求使用未核验数值。"}`

const foundationSeedQASystemPrompt = `<role>COC7沙盒基础种子QA</role>
<task>根据check_rule结果审核FoundationSeed中的异常是否能保守对应到克苏鲁神话/规则书方向。只做通过/拒绝，不扩写剧本。</task>
<output>只输出合法JSON对象，不要Markdown、标题、解释或代码围栏。</output>
<schema>{"pass":true,"reason":"审核理由","reject_reasons":["拒绝原因"],"suggested_scope":"若拒绝，给创作阶段的重写范围","rule_check_summary":"你实际依据的check_rule摘要"}</schema>
<rules>
- 必须以输入中的check_rule结果作为审核依据；不要只凭常识或氛围判断。
- 只审核Stage1 seed，不锁定具体数值，不扩写派系/NPC/场景。
- pass=true的最低标准：anomaly具体且怪异；check_rule结果能支持mythos_seed保守接到神话实体、典籍、法术、梦境、异界、生物、崇拜、禁忌知识或宇宙恐怖方向之一；不依赖伪科学、高科技或普通犯罪作为唯一解释。
- 如果check_rule hard_failure=true，pass=false。
- 如果check_rule正文给出了可保守支撑的核心神话锚点，即使同时指出部分具体表现不是规则书明文效果，也应pass=true；把未覆盖表现写入suggested_scope，要求Stage2保守标注，不要拒绝整个seed。
- 只有当check_rule正文没有任何可支撑核心锚点，或只支持普通怪谈/刑侦/心理疾病/科技异常/社会议题时，pass=false。
- 如果check_rule只给出宽泛目录但能证明存在可用神话类别，可pass=true，并在suggested_scope要求Stage2继续保守核验具体锚点。
- reject_reasons必须具体指出异常、mythos_seed和check_rule结果之间哪里对应不上。
- rule_check_summary必须简述check_rule返回的核心依据，不能留空。
</rules>`

func generateFoundationSeedWithQA(ctx context.Context, room *scripterRoom, constraints ScripterConstraints) (FoundationSeed, error) {
	var rejection *FoundationSeedQA
	const maxAttempts = 30
	var lastSeed FoundationSeed
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		seed, err := generateFoundationSeed(ctx, room, constraints, rejection, attempt)
		if err != nil {
			return FoundationSeed{}, err
		}
		lastSeed = seed
		qa, err := reviewFoundationSeed(ctx, room, constraints, seed)
		if err != nil {
			return FoundationSeed{}, err
		}
		log.Printf("[scripter:foundation_seed_qa] attempt=%d pass=%v reason=%q rejects=%q suggested_scope=%q rule_check=%q", attempt, qa.Pass, truncateRunes(qa.Reason, 500), strings.Join(qa.RejectReasons, " | "), truncateRunes(qa.SuggestedScope, 500), truncateRunes(qa.RuleCheckSummary, 500))
		logScripterArtifact(fmt.Sprintf("Stage 1 Foundation Seed QA Attempt %d", attempt), qa)
		if qa.Pass {
			return seed, nil
		}
		rejection = &qa
	}
	return FoundationSeed{}, fmt.Errorf("Foundation Seed QA 连续拒绝 %d 次，最后一次seed=%+v，拒绝原因=%v", maxAttempts, lastSeed, rejectionRejectReasons(rejection))
}

func generateFoundationSeed(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, rejection *FoundationSeedQA, attempt int) (FoundationSeed, error) {
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)
	rejectionBlock := ""
	if rejection != nil {
		rejectionJSON, _ := json.Marshal(rejection)
		rejectionBlock = fmt.Sprintf("\n<qa_rejection>%s</qa_rejection>\n请根据qa_rejection重写FoundationSeed：异常必须仍具体怪异，但mythos_seed必须能保守接入神话方向。", string(rejectionJSON))
	}
	userPrompt := fmt.Sprintf(`<request_json>%s</request_json>
<constraints>%s</constraints>
<attempt>%d</attempt>
<geography_note>地理只作为风味，不要让它替代异常事实和人类悲剧。</geography_note>%s
请生成FoundationSeed。`, string(reqJSON), string(constraintsJSON), attempt, rejectionBlock)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(foundationSeedSystemPrompt)},
		{Role: "user", Content: userPrompt},
	}
	logStagePrompt("foundation_seed", msgs)
	var seed FoundationSeed
	if err := chatAndParseJSON(ctx, room.architect, room.parser, msgs, &seed, foundationSeedExample, "foundation_seed"); err != nil {
		return FoundationSeed{}, err
	}
	seed = normalizeFoundationSeed(seed, room.req)
	log.Printf("[scripter:foundation_seed] attempt=%d generated anomaly=%q relation=%q mythos_seed=%q", attempt, truncateRunes(seed.Anomaly, 500), seed.MythosRelation, truncateRunes(seed.MythosSeed, 500))
	return seed, nil
}

func reviewFoundationSeed(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, seed FoundationSeed) (FoundationSeedQA, error) {
	ruleCheck := checkFoundationSeedRule(ctx, seed)
	seedJSON, _ := json.Marshal(seed)
	constraintsJSON, _ := json.Marshal(constraints)
	ruleCheckJSON, _ := json.Marshal(ruleCheck)
	userPrompt := fmt.Sprintf(`<constraints>%s</constraints>
<foundation_seed>%s</foundation_seed>
<check_rule>%s</check_rule>
请基于check_rule结果审核这个FoundationSeed。`, string(constraintsJSON), string(seedJSON), string(ruleCheckJSON))
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.qa.systemPrompt(foundationSeedQASystemPrompt)},
		{Role: "user", Content: userPrompt},
	}
	logStagePrompt("foundation_seed_qa", msgs)
	var qa FoundationSeedQA
	if err := chatAndParseJSON(ctx, room.qa, room.parser, msgs, &qa, foundationSeedQAExample, "foundation_seed_qa"); err != nil {
		return FoundationSeedQA{}, err
	}
	qa.Reason = strings.TrimSpace(qa.Reason)
	qa.SuggestedScope = strings.TrimSpace(qa.SuggestedScope)
	qa.RuleCheckSummary = strings.TrimSpace(qa.RuleCheckSummary)
	for i := range qa.RejectReasons {
		qa.RejectReasons[i] = strings.TrimSpace(qa.RejectReasons[i])
	}
	if qa.RuleCheckSummary == "" {
		qa.RuleCheckSummary = truncateRunes(ruleCheck.Result, 1000)
	}
	if ruleCheck.HardFailure {
		qa.Pass = false
		if len(qa.RejectReasons) == 0 {
			qa.RejectReasons = []string{"check_rule未返回可审核的规则书结果，不能让创作阶段只凭氛围通过。"}
		}
		if qa.Reason == "" {
			qa.Reason = "check_rule没有可用结果，无法证明该异常可保守对应到规则书神话方向。"
		}
	}
	if qa.Pass && qa.Reason == "" {
		qa.Reason = "QA通过：check_rule返回了可保守接入的神话方向。"
	}
	if !qa.Pass && len(qa.RejectReasons) == 0 {
		qa.RejectReasons = []string{firstNonEmpty(qa.Reason, "异常与mythos_seed的神话对应关系不足。")}
	}
	return qa, nil
}

func checkFoundationSeedRule(ctx context.Context, seed FoundationSeed) FoundationSeedRuleCheck {
	question := fmt.Sprintf("COC7规则书中是否存在可保守支撑此剧本基础异常的神话方向？异常：%s。人类悲剧：%s。候选mythos_seed：%s。只核验是否能对应到规则书中的神话实体、典籍、法术、梦境/异界、生物、崇拜或禁忌知识方向；不要给剧本创作建议，不要编造未核验数值。", seed.Anomaly, seed.HumanTragedy, seed.MythosSeed)
	log.Printf("[scripter:foundation_seed_qa] check_rule question len=%d body=%s", len(question), truncateRunes(question, scripterPromptLogLimit))
	lawyerHandle, err := loadSingleAgent(models.AgentRoleLawyer)
	if err != nil {
		result := fmt.Sprintf("check_rule unavailable: %v", err)
		log.Printf("[scripter:foundation_seed_qa] check_rule unavailable err=%v", err)
		return FoundationSeedRuleCheck{Question: question, Result: result, HardFailure: true}
	}
	results := runLawyer(ctx, lawyerHandle, question, rulebook.GlobalIndex)
	result := formatLawyerResults(results)
	hardFailure := foundationRuleCheckHardFailure(result)
	log.Printf("[scripter:foundation_seed_qa] check_rule results=%d hard_failure=%v body=%s", len(results), hardFailure, truncateRunes(result, scripterRepairLogLimit))
	return FoundationSeedRuleCheck{Question: question, Result: result, HardFailure: hardFailure}
}

func foundationRuleCheckHardFailure(result string) bool {
	text := strings.TrimSpace(result)
	if text == "" {
		return true
	}
	lower := strings.ToLower(text)
	hardBlockingPhrases := []string{"无结果", "默认禁止", "没有结果", "no result", "not found", "check_rule unavailable"}
	for _, phrase := range hardBlockingPhrases {
		if strings.Contains(lower, strings.ToLower(phrase)) {
			return true
		}
	}
	return false
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

func normalizeFoundationSeed(seed FoundationSeed, req ScenarioCreationRequest) FoundationSeed {
	seed.Anomaly = strings.TrimSpace(seed.Anomaly)
	seed.HumanTragedy = strings.TrimSpace(seed.HumanTragedy)
	seed.MythosRelation = strings.ToLower(strings.TrimSpace(seed.MythosRelation))
	seed.MythosSeed = strings.TrimSpace(seed.MythosSeed)
	if seed.Anomaly == "" {
		seed.Anomaly = firstNonEmpty(req.Brief, "一个公开场所反复出现无法解释的细节，普通解释都只能解释其中一半。")
	}
	if seed.HumanTragedy == "" {
		seed.HumanTragedy = "某个普通人为了保护重要的人隐瞒了一个事实，隐瞒本身逐渐成为更大灾难的入口。"
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
			NPCs: []FactionNPC{{Name: "周砚", PublicIdentity: "地方办事员", Agenda: "维持旧决定不被公开", Secret: seed.HumanTragedy, Attitude: "礼貌回避", StatsNote: "普通人类属性15-70"}},
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
			Discoverable:   seed.HumanTragedy,
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
- win_condition/lose_condition/partial_wins写代价分配和结束信号，不写二元奖励。
- system_prompt必须给KP时间推进、信息可见性和不主动引导三项协议。
</director_contract>`

const scenarioExample = `{"name":"示例沙盒模组名","description":"一份围绕异常事实、派系时间线和调查员可拉动杠杆展开的COC情境简报。","author":"agent-team","tags":"sandbox,coc","min_players":1,"max_players":4,"difficulty":"normal","content":{"system_prompt":"你是本场COC跑团的KP，职责是管理一个会自行推进的局势而不是执行线性故事。按派系时间线推进后果；按可见性分层给信息；不要主动把调查员引向正确答案。","setting":"玩家抵达时能看见的当前局势。只写公开事实、紧张关系和可感知异常，不剧透幕后真相。","intro":"你们以某种身份进入局势，眼前有三件可立即行动的事：询问某人、检查某地、决定是否公开某条信息。","game_start_slot":16,"map_description":"【文字地图】起点A连接地点B和地点C；地点B有公开冲突，地点C有可深入调查的物件；各地点可往返，没有固定访问顺序。","scenes":[{"id":"location_1","name":"地点名","description":"可见：到达即可看到的信息。可发现：主动调查可获得的信息。杠杆：调查员若做X，派系Y的时间线会改变。风险：拖延或失败的后果。出口：可前往的其他地点。","triggers":["available_from_start"]}],"npcs":[{"name":"NPC姓名","description":"公开身份；所属派系；真实议程；秘密；可被说服或施压的杠杆；可能知道但不会主动说出的事实。","attitude":"初始态度和压力下的反应","stats":{"STR":50,"CON":50,"SIZ":50,"DEX":50,"APP":50,"INT":60,"POW":50,"EDU":60,"HP":10,"MP":10}}],"clues":["[真实]登记簿矛盾(旧办公室): 自包含事实；获取方式；能改变哪个判断。","[隐藏]深层事实(封存物件): 自包含事实；获取方式；触及后要承担什么代价。","[误导]地方传闻(酒馆): 为什么它表面合理但只能解释一部分。"],"win_condition":"如果调查员让某个结束信号以较低代价固化，则对应派系失去关键优势，但另一个代价留存。","lose_condition":"如果关键时间线终点到达且无人干预，则局势进入新的稳定态，某人或某地不可挽回地改变。","partial_wins":["如果只救出某人但没有公开事实，则个人获救，派系结构保留。"]}}`

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

	question := fmt.Sprintf("为COC7沙盒剧本核验一个最小神话锚点。异常：%s。人类悲剧：%s。神话关系：%s。候选方向：%s。请只给可保守使用的实体/典籍/法术/物品方向和必须避免的未核验数值。", seed.Anomaly, seed.HumanTragedy, seed.MythosRelation, seed.MythosSeed)
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
		draft.Tags = strings.Join(nonEmptyStrings("sandbox", "coc", constraints.Theme, constraints.TemporalPhase, constraints.ThematicRegister), ",")
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
