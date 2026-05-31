// NOTE: Scenario generation pipeline for sandbox-style COC situation briefs.
// Single-shot generation: one architect call produces the complete ScenarioDraft.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"strings"
	"sync"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// ---------------------------------------------------------------------------
// Public API types
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

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	defaultScripterAuthor = "agent-team"

	scripterPromptLogLimit = 8000
	scripterRawLogLimit    = 20000
	scripterRepairLogLimit = 12000
)

var scriptEra = []string{
	"1920s", "1950s", "1980s", "modern",
}

func defaultScripterEra() string {
	return scriptEra[rand.Intn(len(scriptEra))]
}

const scriptSessionId = math.MaxInt64

var scripterCounter int
var scripterCounterMu sync.Mutex

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

func RunScripterScenarioTeam(ctx context.Context, req ScenarioCreationRequest) (ScenarioCreationOutput, error) {
	room, err := newScripterRoom(req)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	scripterCounterMu.Lock()
	sessionID := scriptSessionId - int64(scripterCounter)
	scripterCounter++
	scripterCounterMu.Unlock()
	ctx = context.WithValue(ctx, "session", sessionID)
	return room.Run(ctx)
}

// ---------------------------------------------------------------------------
// scripterRoom
// ---------------------------------------------------------------------------

type scripterRoom struct {
	architect       agentHandle
	qa              agentHandle
	lawyer          agentHandle
	parser          agentHandle
	req             ScenarioCreationRequest
	npcBlacklist    []string
	titleSamples    []string
	mythosBlacklist []string
}

func (r *scripterRoom) architectModelName() string {
	if r != nil && r.architect.config != nil {
		if modelName := strings.TrimSpace(r.architect.config.ModelName); modelName != "" {
			return modelName
		}
	}
	return defaultScripterAuthor
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
	return &scripterRoom{
		architect: architect, qa: qa, lawyer: lawyer, parser: parser,
		req: normalizeScenarioCreationRequest(req),
	}, nil
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
	switch req.TargetLength {
	case "short":
		req.TargetLength = "剧本时间长度: 1-3d"
	case "medium":
		req.TargetLength = "剧本时间长度: 3-7d"
	case "long":
		req.TargetLength = "剧本时间长度: 1week-1month"
	}
	if strings.TrimSpace(req.Era) == "" {
		req.Era = defaultScripterEra()
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
	r.mythosBlacklist = loadRecentMythosAnchors(40)
	log.Printf("[scripter] context prepared npc_blacklist=%d title_samples=%d mythos_blacklist=%d",
		len(r.npcBlacklist), len(r.titleSamples), len(r.mythosBlacklist))
}

// ---------------------------------------------------------------------------
// Run — single-shot pipeline
// ---------------------------------------------------------------------------

func (r *scripterRoom) Run(ctx context.Context) (ScenarioCreationOutput, error) {
	r.prepareContext()
	if ctx.Err() != nil {
		return ScenarioCreationOutput{}, ctx.Err()
	}
	reqJSON, _ := json.Marshal(r.req)
	log.Printf("[scripter] single-shot generation start req=%s", reqJSON)

	log.Printf("[scripter] stage=constraints start")
	constraints := r.buildConstraints(ctx)
	log.Printf("[scripter] stage=constraints done geography=%q", strings.Join(constraints.GeographyFlavor, " → "))
	logScripterArtifact("Constraints", constraints)

	log.Printf("[scripter] stage=oneshot start")
	draft, irony, rewardConcept, err := generateOneshotDraft(ctx, r, constraints)
	if err != nil {
		log.Printf("[scripter] stage=oneshot error=%v", err)
		return ScenarioCreationOutput{}, fmt.Errorf("单步生成失败: %w", err)
	}
	log.Printf("[scripter] stage=oneshot done name=%q scenes=%d npcs=%d clues=%d delta=%q",
		draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues), irony.DeltaOperator)

	applyGuardrails(&draft, r.req, r.architectModelName())

	// Repair loop: up to 2 rounds for structural issues
	iterations := 1
	for round := 1; round <= 2; round++ {
		issues := validateDraftCompatibility(draft)
		if len(issues) == 0 {
			break
		}
		log.Printf("[scripter] stage=repair round=%d issues=%d %v", round, len(issues), issues)
		repaired, repairErr := repairOneshotDraft(ctx, r, constraints, &draft, irony, issues)
		if repairErr != nil {
			log.Printf("[scripter] stage=repair round=%d failed: %v", round, repairErr)
			break
		}
		draft = repaired
		applyGuardrails(&draft, r.req, r.architectModelName())
		iterations++
		log.Printf("[scripter] stage=repair round=%d done name=%q scenes=%d npcs=%d clues=%d",
			round, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	}

	// Reward agent (isolated context, optional)
	if strings.TrimSpace(rewardConcept) != "" {
		log.Printf("[scripter] stage=reward_agent start concept=%q anchor=%q",
			truncateRunes(rewardConcept, 200), truncateRunes(draft.Content.MythosAnchor, 200))
		rwd, rewardErr := runRewardAgent(ctx, r, rewardConcept, draft.Content.MythosAnchor)
		if rewardErr != nil {
			log.Printf("[scripter] stage=reward_agent error=%v (continuing without reward)", rewardErr)
		} else if rwd != nil {
			draft.Content.Reward = rwd
			log.Printf("[scripter] stage=reward_agent done name=%q type=%q", rwd.Name, rwd.Type)
		}
	}

	beforeIssues := validateDraftCompatibility(draft)
	log.Printf("[scripter] normalization start pre_issues=%d", len(beforeIssues))
	normalizeOneshotDraft(&draft, r.req, r.architectModelName(), constraints, irony)
	log.Printf("[scripter] normalization done name=%q players=%d-%d slot=%d scenes=%d npcs=%d clues=%d partial_wins=%d",
		draft.Name, draft.MinPlayers, draft.MaxPlayers, draft.Content.GameStartSlot,
		len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues), len(draft.Content.PartialWins))

	if issues := validateDraftCompatibility(draft); len(issues) > 0 {
		log.Printf("[scripter] draft issues after normalization: %v", issues)
	}
	logScripterArtifact("Final ScenarioDraft", draft)

	return ScenarioCreationOutput{Draft: draft, IronyCore: &irony, Iterations: iterations}, nil
}

// ---------------------------------------------------------------------------
// Constraints generation
// ---------------------------------------------------------------------------

type ScripterConstraints struct {
	Era             string   `json:"era"`
	Theme           string   `json:"theme"`
	GeographyFlavor []string `json:"geography_flavor"`
	TargetLength    string   `json:"target_length"`
	PlayerRange     string   `json:"player_range"`
	Difficulty      string   `json:"difficulty"`
}

func (r *scripterRoom) buildConstraints(ctx context.Context) ScripterConstraints {
	geography, err := generateGeographyChain(ctx, r.architect, r.req.Era)
	if err != nil || len(geography) == 0 {
		if err != nil {
			log.Printf("[scripter] geography flavor generation failed: %v", err)
		}
		geography = fallbackGeographyFlavor(r.req)
		log.Printf("[scripter] geography fallback=%q", strings.Join(geography, " → "))
	} else {
		log.Printf("[scripter] geography generated=%q", strings.Join(geography, " → "))
	}
	return ScripterConstraints{
		Era:             r.req.Era,
		Theme:           firstNonEmpty(r.req.Theme, ""),
		GeographyFlavor: geography,
		TargetLength:    r.req.TargetLength,
		PlayerRange:     fmt.Sprintf("%d-%d", r.req.MinPlayers, r.req.MaxPlayers),
		Difficulty:      r.req.Difficulty,
	}
}

var geographyElementSystemPrompt = `<role>事件发生地候选列举器</role>
<task>根据用户给定阶段列举20个可用于事件发生地的候选。该结果只作为布景风味，不决定剧情结构。</task>
<rules>
- country阶段输出具体国家或具体政权范围。
- settlement_scale阶段必须且只能从以下固定选项中选择一个：大都会、城市、市郊、乡镇、无人区。
- 非country阶段只输出类型/形态/区位模式，不输出具体地名、真实行政区名、真实城市名或真实街区名。
- natural_geography阶段必须输出自然地理/地形/水文/气候约束类型。
- 只输出现实地理/人文地理候选，不输出幕后真相。
- 禁止输出伪科学、高科技、工程化异常或可诱导伪科学解释神话的候选。
- 除settlement_scale阶段只输出一个固定选项外，其他阶段每行一个名称，正好20个，不要编号、解释、标题或描述句。</rules>`

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
		{Key: "settlement_scale", Mode: "根据前置布景和时代，从固定选项中选择最适合调查剧本的聚落尺度：大都会、城市、市郊、乡镇、无人区。只输出一个选项", Examples: "城市"},
		{Key: "natural_geography", Mode: "自然地理/地形/水文/气候约束类型，不输出具体地名", Examples: "林木覆盖的山谷"},
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
	countInstruction := "请只输出本阶段的20个候选。"
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
	flavor := []string{firstNonEmpty(req.Era, defaultScripterEra()), "城市"}
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
// Structural validation
// ---------------------------------------------------------------------------

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

func applyGuardrails(draft *ScenarioDraft, req ScenarioCreationRequest, author string) {
	if draft == nil {
		return
	}
	author = strings.TrimSpace(author)
	if author == "" {
		author = defaultScripterAuthor
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
		draft.MaxPlayers = draft.MinPlayers
	}
	if strings.TrimSpace(req.Difficulty) != "" && draft.Difficulty != strings.TrimSpace(req.Difficulty) {
		log.Printf("[scripter:guardrails] override difficulty from=%q to=%q", draft.Difficulty, strings.TrimSpace(req.Difficulty))
		draft.Difficulty = strings.TrimSpace(req.Difficulty)
	}
	if draft.Author != author {
		draft.Author = author
	}
}

// ---------------------------------------------------------------------------
// Length / difficulty specs (injected into prompts)
// ---------------------------------------------------------------------------

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
	case "long", "剧本时间长度: 1week-1month":
		return "- locations/scenes: 6-8个地点状态，每个有可见信息、可发现信息、杠杆、风险、出口\n- clues: 10-12条自包含事实线索，必须带[真实]/[隐藏]/[误导]前缀\n- NPC数量: 7-10个，来自派系且有独立议程"
	case "medium", "剧本时间长度: 3-7d":
		return "- locations/scenes: 4-6个地点状态，每个有可见信息、可发现信息、杠杆、风险、出口\n- clues: 7-10条自包含事实线索，必须带[真实]/[隐藏]/[误导]前缀\n- NPC数量: 4-7个，来自派系且有独立议程"
	default:
		return "- locations/scenes: 3-4个地点状态，每个有可见信息、可发现信息、杠杆、风险、出口\n- clues: 5-7条自包含事实线索，必须带[真实]/[隐藏]/[误导]前缀\n- NPC数量: 2-4个，来自派系且有独立议程"
	}
}

// ---------------------------------------------------------------------------
// JSON repair helpers
// ---------------------------------------------------------------------------

func chatAndParseJSON[T any](ctx context.Context, generator agentHandle, parser agentHandle, msgs []llm.ChatMessage, out *T, schemaExample string, tag string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if generator.provider == nil {
		return fmt.Errorf("%s generator provider unavailable", tag)
	}
	log.Printf("[scripter:%s] chat start messages=%d", tag, len(msgs))
	raw, err := generator.provider.JsonChat(ctx, msgs)
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
	log.Printf("[scripter:%s] JSON parse failed: %v raw=%s", tag, parseErr, truncateRunes(raw, scripterRawLogLimit))
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
			return fmt.Errorf("修复后的 %s JSON 结构仍不匹配,二次修复失败: %w", tag, repairErr2)
		}
		if err2 := parseJSONObject(repairedAgain, out); err2 != nil {
			return fmt.Errorf("二次修复后的 %s JSON 仍无法解析: %w", tag, err2)
		}
	}
	return nil
}

// RepairJSON is exported for use by other subsystems.
func RepairJSON(ctx context.Context, rawJSON string, parseErr error, schemaExample string) (string, error) {
	findFirst := strings.Index(rawJSON, "```")
	if findFirst != -1 {
		rawJSON = rawJSON[findFirst:]
		if strings.HasPrefix(rawJSON, "```json") {
			rawJSON = strings.TrimPrefix(rawJSON, "```json")
			rawJSON = strings.TrimSuffix(rawJSON, "```")
			return strings.TrimSpace(rawJSON), nil
		}
		if strings.HasPrefix(rawJSON, "```") {
			rawJSON = strings.TrimPrefix(rawJSON, "```")
			rawJSON = strings.TrimSuffix(rawJSON, "```")
			return strings.TrimSpace(rawJSON), nil
		}
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
	parserAgent, err := loadSingleAgent(models.AgentRoleParser)
	if err != nil {
		return "", fmt.Errorf("parser agent 未配置: %w", err)
	}
	return repairJSONWith(ctx, parserAgent, rawJSON, parseErr, schemaExample)
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
	stripped := llm.StripCodeFence(strings.TrimSpace(raw))
	if err := json.Unmarshal([]byte(stripped), out); err == nil {
		return nil
	}
	s := strings.Index(stripped, "{")
	e := strings.LastIndex(stripped, "}")
	if s >= 0 && e > s {
		if err := json.Unmarshal([]byte(stripped[s:e+1]), out); err == nil {
			return nil
		}
	}
	return fmt.Errorf("JSON 解析失败: %s", truncateRunes(stripped, 200))
}

// ---------------------------------------------------------------------------
// Legacy tool-call types kept for other package code (orchestrator etc.)
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
		return `[{"action":"response","reason":"最终草案兼容ScenarioContent并保持剧本语义。","draft":` + oneshotExample + `}]`
	default:
		return `[{"action":"response","reason":"解释为什么这样响应。"}]`
	}
}

// grepRulebook searches the rulebook for exact keyword matches.
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

// ---------------------------------------------------------------------------
// DB helpers
// ---------------------------------------------------------------------------

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

func loadRecentMythosAnchors(limit int) []string {
	if limit <= 0 || models.DB == nil {
		return nil
	}
	var scenarios []models.Scenario
	if err := models.DB.Order("created_at DESC").Limit(limit * 2).Find(&scenarios).Error; err != nil {
		log.Printf("[scripter] load recent mythos anchors failed: %v", err)
		return nil
	}
	seen := map[string]bool{}
	anchors := make([]string, 0, limit)
	for i := range scenarios {
		if err := scenarios[i].DecodeData(); err != nil {
			continue
		}
		anchor := strings.TrimSpace(scenarios[i].Content.Data.MythosAnchor)
		if anchor == "" || seen[anchor] {
			continue
		}
		seen[anchor] = true
		anchors = append(anchors, anchor)
		if len(anchors) >= limit {
			break
		}
	}
	return anchors
}

// ---------------------------------------------------------------------------
// Format helpers
// ---------------------------------------------------------------------------

func formatMythosBlacklist(anchors []string) string {
	if len(anchors) == 0 {
		return "(无)"
	}
	return "- " + strings.Join(anchors, "\n- ")
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

// ---------------------------------------------------------------------------
// Logging helpers
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

// ---------------------------------------------------------------------------
// Utility
// ---------------------------------------------------------------------------

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
