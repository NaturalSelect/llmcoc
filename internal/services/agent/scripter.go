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
	"regexp"
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
	Draft         ScenarioDraft `json:"draft"`
	IronyCore     *IronyCore    `json:"irony_core"`
	Iterations    int           `json:"iterations"`
	GenerationLog string        `json:"generation_log"`
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

// NOTE: 8 种神话力量介入机制，替代原泛恐怖美学分类；每种描述神话如何进入人类世界，而非恐怖风格或具体怪物。
var scripterHorrorModes = []string{
	"cult_ritual",
	"forbidden_knowledge",
	"mythos_infiltration",
	"bloodline_corruption",
	"mythos_predation",
	"sealed_awakening",
	"dimensional_intrusion",
	"sorcerous_usurpation",
}

var scripterInvestFocuses = []string{
	"disappearance",
	"bizarre_death",
	"artifact_theft",
	"ritual_interruption",
	"family_secret",
	"local_legend",
	"sealed_location",
	"identity_replacement",
}

// NOTE: 中文标签对应 8 种介入机制，描述神话力量进入人类世界的方式，而非恐怖美学或具体怪物。
var horrorModeChineseLabels = map[string]string{
	"cult_ritual":           "邪教仪式——崇拜者通过献祭、召唤、开门或转化仪式引入神话力量",
	"forbidden_knowledge":   "禁忌知识——典籍、铭文、公式或梦中授知传播足以腐化理解者的真相",
	"mythos_infiltration":   "异族渗透——非人种族通过伪装、替换、混血或代理人潜入人类社会",
	"bloodline_corruption":  "血脉腐化——家族中的非人血统、祖先契约或遗传诅咒逐渐显现",
	"mythos_predation":      "神话猎食——神话生物将人类视为食物、宿主、祭品或繁殖材料",
	"sealed_awakening":      "封印苏醒——人为活动破坏古老封印，使沉睡的神话存在重新活动",
	"dimensional_intrusion": "异界侵入——梦境、异维度或异常时空突破边界并侵蚀现实",
	"sorcerous_usurpation":  "巫术夺舍——施法者借助附身、意识转移或身体窃取延续自身",
}

var investFocusChineseLabels = map[string]string{
	"disappearance":        "失踪：从人或物的持续消失进入调查",
	"bizarre_death":        "离奇死亡：从异常尸体、死亡方式或死亡时间进入调查",
	"artifact_theft":       "古物失窃：从重要物品被盗、替换或争夺进入调查",
	"ritual_interruption":  "仪式中断：从未完成或被打断的仪式现场进入调查",
	"family_secret":        "家族秘密：从血缘、遗产、旧信件或亲属隐瞒进入调查",
	"local_legend":         "地方传闻：从口述传说、禁地或旧俗异常复现进入调查",
	"sealed_location":      "封闭地点：从被封锁、隔离或无法离开的空间进入调查",
	"identity_replacement": "身份替换：从某人不再像本人或关系证据矛盾进入调查",
}

// NOTE: 散文声线池：每次生成随机注入一种"作者声线"，让玩家可见散文（简介/背景/开场）
// 摆脱统一的设计文档腔，更接近人类作者的文风。只影响用词与节奏，不影响剧情事实。
var scripterProseVoices = []string{
	"地方志体",
	"新闻纪实体",
	"冷硬白描体",
	"口述回忆体",
	"田园散文体",
	"旅行手记体",
}

var proseVoiceGuides = map[string]string{
	"地方志体":  "以县志、地方志的平实笔调记录地点、物产与人事；克制，不渲染，不评价",
	"新闻纪实体": "像地方报纸的纪实报道：时间、地点、人名具体，句子短，只陈述可核实的事",
	"冷硬白描体": "短句白描，落在具体名词上，少用形容词；观察者视角，不抒情",
	"口述回忆体": "像当事人事后向熟人讲述：带一点口语衬词，偶尔停在某个具体小物件上，再拉回正题",
	"田园散文体": "季节、天气、日常劳作的舒缓白描；节奏慢，细节温和",
	"旅行手记体": "途中见闻的实用记录：路线、食宿、价钱、风物，偶有一句节制的个人感想",
}

func defaultScripterEra() string {
	return scriptEra[rand.Intn(len(scriptEra))]
}

const scriptSessionId = math.MaxInt64

var scripterCounter int
var scripterCounterMu sync.Mutex
var scripterRunMu sync.Mutex

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// ScripterProgressFunc 接收生成流水线的阶段进度事件（stage 为阶段标识，status 为
// start/done/error 等状态，detail 为面向管理员的中文描述），用于 SSE 实时推送。
type ScripterProgressFunc func(stage, status, detail string)

func RunScripterScenarioTeam(ctx context.Context, req ScenarioCreationRequest) (ScenarioCreationOutput, error) {
	return RunScripterScenarioTeamWithProgress(ctx, req, nil)
}

func RunScripterScenarioTeamWithProgress(ctx context.Context, req ScenarioCreationRequest, progress ScripterProgressFunc) (ScenarioCreationOutput, error) {
	scripterRunMu.Lock()
	defer scripterRunMu.Unlock()

	room, err := newScripterRoom(req)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	room.progressFn = progress
	scripterCounterMu.Lock()
	sessionID := fmt.Sprintf("%v", scriptSessionId-int64(scripterCounter))
	scripterCounter++
	scripterCounterMu.Unlock()
	room.sessionID = sessionID
	room.generationLog = newScripterGenerationLog(sessionID, room.req)
	ctx = context.WithValue(ctx, "session", sessionID)
	ctx = contextWithScripterGenerationLog(ctx, room.generationLog)
	out, err := room.Run(ctx)
	if err != nil {
		return out, err
	}
	out.GenerationLog = room.generationLogText()
	return out, nil
}

// ---------------------------------------------------------------------------
// scripterRoom
// ---------------------------------------------------------------------------

type scripterRoom struct {
	architect agentHandle
	qa        agentHandle
	lawyer    agentHandle
	// NOTE: translator 是独立的发散联想/资料转译 Agent，不复用 lawyer 的 provider/model。
	translator      agentHandle
	sessionID       string
	req             ScenarioCreationRequest
	npcBlacklist    []string
	titleSamples    []string
	mythosBlacklist []string
	tagsBlacklist   []string
	generationLog   *scripterGenerationLog
	progressFn      ScripterProgressFunc
}

// emitProgress 向订阅者（SSE）推送阶段进度；未订阅时为空操作。
func (r *scripterRoom) emitProgress(stage, status, detail string) {
	if r == nil || r.progressFn == nil {
		return
	}
	r.progressFn(stage, status, detail)
}

func (r *scripterRoom) architectModelName() string {
	if r != nil && r.architect.config != nil {
		if modelName := strings.TrimSpace(r.architect.config.ModelName); modelName != "" {
			return modelName
		}
	}
	return defaultScripterAuthor
}

func sessionIDFromContextValue(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value := ctx.Value("session")
	if value == nil {
		return ""
	}
	if sessionID, ok := value.(string); ok {
		return sessionID
	}
	return fmt.Sprintf("%v", value)
}

func scripterSessionID(ctx context.Context, room *scripterRoom) string {
	if room != nil && room.sessionID != "" {
		return room.sessionID
	}
	return sessionIDFromContextValue(ctx)
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
	// NOTE: translator 独立加载，不可用或禁用时 fail-fast，绝不退回 lawyer。
	translator, err := loadSingleAgent(models.AgentRoleTranslator)
	if err != nil {
		return nil, fmt.Errorf("translator agent 加载失败: %w", err)
	}
	return &scripterRoom{
		architect: architect, qa: qa, lawyer: lawyer, translator: translator,
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
	r.npcBlacklist = loadRecentNPCNameBlacklist(200, r.sessionID)
	r.titleSamples = loadScenarioTitleSamples(80, r.sessionID)
	r.mythosBlacklist = loadRecentMythosAnchors(100, r.sessionID)
	r.tagsBlacklist = loadRecentScenarioTags(60, r.sessionID)
	log.Printf("[scripter] session=%s context prepared npc_blacklist=%d title_samples=%d mythos_blacklist=%d tags_blacklist=%d",
		r.sessionID, len(r.npcBlacklist), len(r.titleSamples), len(r.mythosBlacklist), len(r.tagsBlacklist))
}

// ---------------------------------------------------------------------------
// Run — single-shot pipeline
// ---------------------------------------------------------------------------

func (r *scripterRoom) Run(ctx context.Context) (ScenarioCreationOutput, error) {
	if r.sessionID == "" {
		r.sessionID = sessionIDFromContextValue(ctx)
	}
	sessionID := r.sessionID
	if r.generationLog == nil {
		r.generationLog = newScripterGenerationLog(sessionID, r.req)
	}
	ctx = contextWithScripterGenerationLog(ctx, r.generationLog)
	r.prepareContext()
	if ctx.Err() != nil {
		return ScenarioCreationOutput{}, ctx.Err()
	}
	reqJSON, _ := json.Marshal(r.req)
	log.Printf("[scripter] session=%s single-shot generation start req=%s", sessionID, reqJSON)

	log.Printf("[scripter] session=%s stage=constraints start", sessionID)
	r.emitProgress("constraints", "start", "阶段 1/5：构建地理与多样性约束…")
	constraints := r.buildConstraints(ctx)
	log.Printf("[scripter] session=%s stage=constraints done geography=%q", sessionID, strings.Join(constraints.GeographyFlavor, " → "))
	r.emitProgress("constraints", "done", "约束就绪："+strings.Join(constraints.GeographyFlavor, " → "))
	logScripterArtifact("Constraints", sessionID, constraints)

	log.Printf("[scripter] session=%s stage=oneshot start", sessionID)
	r.emitProgress("oneshot", "start", "阶段 2/5：Architect 生成模组正文（多轮工具调用，耗时较长）…")
	draft, _, rewardConcept, skeleton, err := generateOneshotDraft(ctx, r, constraints)
	if err != nil {
		log.Printf("[scripter] session=%s stage=oneshot error=%v", sessionID, err)
		r.emitProgress("oneshot", "error", "正文生成失败："+err.Error())
		return ScenarioCreationOutput{}, fmt.Errorf("单步生成失败: %w", err)
	}
	log.Printf("[scripter] session=%s stage=oneshot done name=%q scenes=%d npcs=%d clues=%d",
		sessionID, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	r.emitProgress("oneshot", "done", fmt.Sprintf("正文草稿完成：《%s》，场景 %d 个、NPC %d 个、线索 %d 条",
		draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues)))

	applyGuardrailsWithNPCBlacklist(&draft, r.req, r.architectModelName(), sessionID, r.npcBlacklist)

	// Repair loop: up to 2 rounds for structural issues
	iterations := 1
	for round := 1; round <= 2; round++ {
		issues := validateDraftCompatibility(draft)
		issues = append(issues, checkScenarioTagsOverlap(draft.Tags, r.tagsBlacklist)...)
		if len(issues) == 0 {
			break
		}
		log.Printf("[scripter] session=%s stage=repair round=%d issues=%d %v", sessionID, round, len(issues), issues)
		r.emitProgress("repair", "start", fmt.Sprintf("结构修复第 %d 轮：发现 %d 个结构问题", round, len(issues)))
		repaired, repairErr := repairOneshotDraft(ctx, r, constraints, &draft, issues)
		if repairErr != nil {
			log.Printf("[scripter] session=%s stage=repair round=%d failed: %v", sessionID, round, repairErr)
			r.emitProgress("repair", "error", fmt.Sprintf("结构修复第 %d 轮失败（保留当前草稿）", round))
			break
		}
		draft = repaired
		applyGuardrailsWithNPCBlacklist(&draft, r.req, r.architectModelName(), sessionID, r.npcBlacklist)
		iterations++
		log.Printf("[scripter] session=%s stage=repair round=%d done name=%q scenes=%d npcs=%d clues=%d",
			sessionID, round, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
		r.emitProgress("repair", "done", fmt.Sprintf("结构修复第 %d 轮完成", round))
	}

	// 人写化审查：QA 审 AI 腔与写作质感，问题清单走一轮修复；失败不阻塞生成
	log.Printf("[scripter] session=%s stage=qa_humanize start", sessionID)
	r.emitProgress("qa_humanize", "start", "阶段 3/5：QA 人写化审查…")
	if qaIssues := runOneshotQAReview(ctx, r, &draft, constraints); len(qaIssues) > 0 {
		log.Printf("[scripter] session=%s stage=qa_humanize issues=%d %v", sessionID, len(qaIssues), qaIssues)
		r.emitProgress("qa_humanize", "start", fmt.Sprintf("人写化审查发现 %d 个问题，执行修复", len(qaIssues)))
		logScripterArtifact("QA Humanize Issues", sessionID, qaIssues)
		repaired, repairErr := repairOneshotDraft(ctx, r, constraints, &draft, qaIssues)
		if repairErr != nil {
			log.Printf("[scripter] session=%s stage=qa_humanize repair failed: %v (keeping draft)", sessionID, repairErr)
			r.emitProgress("qa_humanize", "error", "人写化修复失败（保留当前草稿）")
		} else {
			draft = repaired
			applyGuardrailsWithNPCBlacklist(&draft, r.req, r.architectModelName(), sessionID, r.npcBlacklist)
			iterations++
			log.Printf("[scripter] session=%s stage=qa_humanize done name=%q scenes=%d npcs=%d clues=%d",
				sessionID, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
			r.emitProgress("qa_humanize", "done", "人写化修复完成")
		}
	} else {
		log.Printf("[scripter] session=%s stage=qa_humanize no issues", sessionID)
		r.emitProgress("qa_humanize", "done", "人写化审查通过")
	}

	// 逻辑审查：QA agent 审因果可达性与神话一致性，问题清单走一轮修复；失败不阻塞生成
	log.Printf("[scripter] session=%s stage=logic_review start", sessionID)
	r.emitProgress("logic_review", "start", "阶段 4/5：逻辑一致性审查…")
	if logicIssues := runLogicReview(ctx, r, &draft, skeleton); len(logicIssues) > 0 {
		log.Printf("[scripter] session=%s stage=logic_review issues=%d %v", sessionID, len(logicIssues), logicIssues)
		r.emitProgress("logic_review", "start", fmt.Sprintf("逻辑审查发现 %d 个问题，执行修复", len(logicIssues)))
		logScripterArtifact("Logic Review Issues", sessionID, logicIssues)
		repaired, repairErr := repairOneshotDraft(ctx, r, constraints, &draft, logicIssues)
		if repairErr != nil {
			log.Printf("[scripter] session=%s stage=logic_review repair failed: %v (keeping draft)", sessionID, repairErr)
			r.emitProgress("logic_review", "error", "逻辑修复失败（保留当前草稿）")
		} else {
			draft = repaired
			applyGuardrailsWithNPCBlacklist(&draft, r.req, r.architectModelName(), sessionID, r.npcBlacklist)
			iterations++
			log.Printf("[scripter] session=%s stage=logic_review done name=%q scenes=%d npcs=%d clues=%d",
				sessionID, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
			r.emitProgress("logic_review", "done", "逻辑修复完成")
		}
	} else {
		log.Printf("[scripter] session=%s stage=logic_review no issues", sessionID)
		r.emitProgress("logic_review", "done", "逻辑审查通过")
	}

	// Reward agent (isolated context, optional)
	if strings.TrimSpace(rewardConcept) != "" {
		log.Printf("[scripter] session=%s stage=reward_agent start concept=%q anchor=%q",
			sessionID, truncateRunes(rewardConcept, 200), truncateRunes(draft.Content.MythosAnchor, 200))
		r.emitProgress("reward_agent", "start", "阶段 5/5：奖励物品设计…")
		rwd, rewardErr := runRewardAgent(ctx, r, rewardConcept, draft.Content.MythosAnchor)
		if rewardErr != nil {
			log.Printf("[scripter] session=%s stage=reward_agent error=%v (continuing without reward)", sessionID, rewardErr)
			r.emitProgress("reward_agent", "error", "奖励设计失败（跳过，不影响模组）")
		} else if rwd != nil {
			draft.Content.Reward = rwd
			log.Printf("[scripter] session=%s stage=reward_agent done name=%q type=%q", sessionID, rwd.Name, rwd.Type)
			r.emitProgress("reward_agent", "done", fmt.Sprintf("奖励设计完成：%s", rwd.Name))
		}
	}

	beforeIssues := validateDraftCompatibility(draft)
	log.Printf("[scripter] session=%s normalization start pre_issues=%d", sessionID, len(beforeIssues))
	r.emitProgress("normalize", "start", "规范化与收尾…")
	normalizeOneshotDraft(&draft, r.req, r.architectModelName(), constraints, sessionID)
	applyGuardrailsWithNPCBlacklist(&draft, r.req, r.architectModelName(), sessionID, r.npcBlacklist)
	log.Printf("[scripter] session=%s normalization done name=%q players=%d-%d slot=%d scenes=%d npcs=%d clues=%d partial_wins=%d",
		sessionID, draft.Name, draft.MinPlayers, draft.MaxPlayers, draft.Content.GameStartSlot,
		len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues), len(draft.Content.PartialWins))
	r.emitProgress("normalize", "done", fmt.Sprintf("规范化完成：《%s》，准备入库", draft.Name))

	if issues := validateDraftCompatibility(draft); len(issues) > 0 {
		log.Printf("[scripter] session=%s draft issues after normalization: %v", sessionID, issues)
	}
	logScripterArtifact("Final ScenarioDraft", sessionID, draft)

	return ScenarioCreationOutput{Draft: draft, IronyCore: &IronyCore{}, Iterations: iterations, GenerationLog: r.generationLogText()}, nil
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
	HorrorMode      string   `json:"horror_mode"`
	InvestFocus     string   `json:"invest_focus"`
	ToneTags        []string `json:"tone_tags"`
	ProseVoice      string   `json:"prose_voice"`      // NOTE: 玩家可见散文的作者声线，只影响文风不影响事实
	DiversitySource string   `json:"diversity_source"` // NOTE: "ai"=AI围池选择，"fallback"=随机降级
}

func (r *scripterRoom) buildConstraints(ctx context.Context) ScripterConstraints {
	sessionID := scripterSessionID(ctx, r)
	geography, err := generateGeographyChain(ctx, r, r.req.Era)
	if err != nil || len(geography) == 0 {
		if err != nil {
			log.Printf("[scripter] session=%s geography flavor generation failed: %v", sessionID, err)
		}
		geography = fallbackGeographyFlavor(r.req)
		log.Printf("[scripter] session=%s geography fallback=%q", sessionID, strings.Join(geography, " → "))
	} else {
		log.Printf("[scripter] session=%s geography generated=%q", sessionID, strings.Join(geography, " → "))
	}

	// NOTE: 先尝试 AI 从围池内选择，失败则降级随机
	candidates := buildDiversityCandidates(r.req, sessionID)
	var horrorMode, investFocus, source string
	mode, focus, src, aiErr := selectDiversityConstraintsWithAI(ctx, r, candidates)
	if aiErr != nil || src != "ai" || mode == "" {
		horrorMode, investFocus, _ = selectDiversityConstraints(r.req, sessionID)
		source = "fallback"
		log.Printf("[scripter] session=%s diversity fallback horror_mode=%q invest_focus=%q", sessionID, horrorMode, investFocus)
	} else {
		horrorMode = mode
		investFocus = focus
		source = "ai"
		log.Printf("[scripter] session=%s diversity ai horror_mode=%q invest_focus=%q", sessionID, horrorMode, investFocus)
	}
	toneTags := toneTagsForDiversity(horrorMode, investFocus, r.req)
	log.Printf("[scripter] session=%s diversity final horror_mode=%q invest_focus=%q tone_tags=%q source=%q",
		sessionID, horrorMode, investFocus, strings.Join(toneTags, ","), source)

	proseVoice := scripterProseVoices[rand.Intn(len(scripterProseVoices))]
	log.Printf("[scripter] session=%s prose_voice=%q", sessionID, proseVoice)

	return ScripterConstraints{
		Era:             r.req.Era,
		Theme:           firstNonEmpty(r.req.Theme, ""),
		GeographyFlavor: geography,
		TargetLength:    r.req.TargetLength,
		PlayerRange:     fmt.Sprintf("%d-%d", r.req.MinPlayers, r.req.MaxPlayers),
		Difficulty:      r.req.Difficulty,
		HorrorMode:      horrorMode,
		InvestFocus:     investFocus,
		ToneTags:        toneTags,
		ProseVoice:      proseVoice,
		DiversitySource: source,
	}
}

var geographyElementSystemPrompt = `<role>事件发生地候选列举器</role>
<task>根据用户给定阶段列举5个可用于事件发生地的候选。该结果只作为布景风味，不决定剧情结构。</task>
<rules>
- country阶段输出具体国家或具体政权范围。
- settlement_scale阶段必须且只能从以下固定选项中选择一个：大都会、城市、市郊、乡镇、无人区。
- 非country阶段只输出类型/形态/区位模式，不输出具体地名、真实行政区名、真实城市名或真实街区名。
- natural_geography阶段必须输出自然地理/地形/水文/气候约束类型。
- 只输出现实地理/人文地理候选，不输出幕后真相。
- 禁止输出伪科学、高科技、工程化异常或可诱导伪科学解释神话的候选。
- 除settlement_scale阶段只输出一个固定选项外，其他阶段每行一个名称，正好5个，不要编号、解释、标题或描述句。</rules>`

func generateGeographyChain(ctx context.Context, room *scripterRoom, era string) ([]string, error) {
	var architect agentHandle
	if room != nil {
		architect = room.architect
	}
	if architect.provider == nil {
		return nil, fmt.Errorf("architect provider unavailable")
	}
	sessionID := sessionIDFromContextValue(ctx)
	log.Printf("[scripter:geography] session=%s start era=%q", sessionID, era)
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
		log.Printf("[scripter:geography] session=%s stage=%q selected_so_far=%q", sessionID, stage.Key, strings.Join(chain, " → "))
		items, err := generateGeographyCandidates(ctx, room, &msgs, era, stage.Key, stage.Mode, stage.Examples, chain)
		if err != nil {
			log.Printf("[scripter:geography] session=%s stage=%q error=%v", sessionID, stage.Key, err)
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
		log.Printf("[scripter] session=%s geography stage=%q candidates=%d chosen=%q", sessionID, stage.Key, len(items), choice)
	}
	return chain, nil
}

func generateGeographyCandidates(ctx context.Context, room *scripterRoom, msgs *[]llm.ChatMessage, era string, stageKey string, mode string, examples string, chain []string) ([]string, error) {
	var architect agentHandle
	if room != nil {
		architect = room.architect
	}
	if architect.provider == nil {
		return nil, fmt.Errorf("architect provider unavailable")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	sessionID := sessionIDFromContextValue(ctx)
	selected := "无，第一轮先选择具体国家或政权范围"
	if len(chain) > 0 {
		selected = strings.Join(chain, " → ")
	}
	countInstruction := "请只输出本阶段的5个候选。"
	if stageKey == "settlement_scale" {
		countInstruction = "请只输出一个最合适的固定选项，必须完全等于：大都会、城市、市郊、乡镇、无人区 之一。"
	}
	prompt := fmt.Sprintf("已随机选中的前置布景：%s\n现在进入下一阶段：%s\n时代：%s\n输出要求：%s\n示例范围：%s\n\n%s", selected, stageKey, era, mode, examples, countInstruction)
	log.Printf("[scripter:geography] session=%s prompt stage=%q len=%d body=%s", sessionID, stageKey, len(prompt), truncateRunes(prompt, scripterPromptLogLimit))
	*msgs = append(*msgs, llm.ChatMessage{Role: "user", Content: prompt})
	callMessages := append([]llm.ChatMessage(nil), (*msgs)...)
	raw, err := architect.provider.Chat(ctx, sessionIDFromContextValue(ctx)+":"+string(models.AgentRoleArchitect), *msgs)
	if err != nil {
		log.Printf("[scripter:geography] session=%s chat error stage=%q err=%v", sessionID, stageKey, err)
		return nil, err
	}
	recordScripterLLMExchange(ctx, room, fmt.Sprintf("geography_%s", stageKey), callMessages, raw)
	log.Printf("[scripter:geography] session=%s raw stage=%q len=%d body=%s", sessionID, stageKey, len(raw), truncateRunes(raw, scripterRawLogLimit))
	*msgs = append(*msgs, llm.ChatMessage{Role: "assistant", Content: raw})
	items := parseElementNames(raw)
	log.Printf("[scripter:geography] session=%s parsed stage=%q count=%d items=%q", sessionID, stageKey, len(items), strings.Join(items, " | "))
	if len(items) == 0 {
		log.Printf("[scripter:geography] session=%s parse empty stage=%q raw=%s", sessionID, stageKey, truncateRunes(raw, scripterRawLogLimit))
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

// buildDiversityCandidates 返回剔除近N条已用组合后的候选围池；DB为空时返回全笛卡尔积。
func buildDiversityCandidates(req ScenarioCreationRequest, sessionID string) []diversityCombo {
	recent := loadRecentDiversityCombos(5, sessionID)
	recentSet := map[string]bool{}
	for _, combo := range recent {
		key := diversityComboKey(combo.HorrorMode, combo.InvestFocus)
		if key != "" {
			recentSet[key] = true
		}
	}

	// 全笛卡尔积
	candidates := make([]diversityCombo, 0, len(scripterHorrorModes)*len(scripterInvestFocuses))
	for _, mode := range scripterHorrorModes {
		for _, focus := range scripterInvestFocuses {
			candidates = append(candidates, diversityCombo{HorrorMode: mode, InvestFocus: focus})
		}
	}

	// 剔除最近用过的组合
	available := make([]diversityCombo, 0, len(candidates))
	for _, c := range candidates {
		if !recentSet[diversityComboKey(c.HorrorMode, c.InvestFocus)] {
			available = append(available, c)
		}
	}
	// 围池耗尽时退化为全笛卡尔积
	if len(available) > 0 {
		return available
	}
	return candidates
}

func selectDiversityConstraints(req ScenarioCreationRequest, sessionID string) (horrorMode string, investFocus string, toneTags []string) {
	candidates := buildDiversityCandidates(req, sessionID)
	if len(candidates) == 0 {
		// NOTE: 围池意外耗尽时的最终兜底；forbidden_knowledge 与 disappearance 均属候选池。
		return "forbidden_knowledge", "disappearance", toneTagsForDiversity("forbidden_knowledge", "disappearance", req)
	}
	chosen := candidates[rand.Intn(len(candidates))]
	return chosen.HorrorMode, chosen.InvestFocus, toneTagsForDiversity(chosen.HorrorMode, chosen.InvestFocus, req)
}

// selectDiversityConstraintsWithAI 让 architect 从围池候选中选最契合题材的组合；
// 失败时返回 ("", "", "fallback", nil)，调用方应降级到随机选取。
func selectDiversityConstraintsWithAI(ctx context.Context, room *scripterRoom, candidates []diversityCombo) (horrorMode, investFocus, source string, err error) {
	if len(candidates) == 0 {
		return "", "", "fallback", nil
	}
	if room == nil || room.architect.provider == nil {
		return "", "", "fallback", nil
	}
	sessionID := scripterSessionID(ctx, room)

	// 构建候选列表文本
	var candLines []string
	for i, c := range candidates {
		modeLabel := horrorModeChineseLabels[c.HorrorMode]
		focusLabel := investFocusChineseLabels[c.InvestFocus]
		candLines = append(candLines, fmt.Sprintf("%d. mode=%s(%s) / focus=%s(%s)",
			i+1, c.HorrorMode, modeLabel, c.InvestFocus, focusLabel))
	}
	candidatesText := strings.Join(candLines, "\n")

	geographyFlavor := ""
	if room.req.Theme != "" {
		geographyFlavor = room.req.Theme
	}
	era := room.req.Era
	theme := room.req.Theme

	systemPrompt := `<role>COC7剧本架构师</role>
<task>从候选围池内挑最契合题材与时代氛围的一个神话介入机制(horror_mode / 神话力量介入人类世界的主要机制) + 调查焦点(invest_focus / 调查切入点)组合。</task>`

	userPrompt := fmt.Sprintf(`时代(Era): %s
主题(Theme): %s
地理风味: %s

候选围池(编号 → 恐怖模式 / 调查焦点):
%s

强制规则: 只能从围池内选, 不得创造新的 mode 或 focus; 严格输出两行, 第一行 mode: <英文值>, 第二行 focus: <英文值>, 不要任何其他内容或解释。`,
		era, theme, geographyFlavor, candidatesText)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(systemPrompt)},
		{Role: "user", Content: userPrompt},
	}

	log.Printf("[scripter:diversity_ai] session=%s prompt candidates=%d", sessionID, len(candidates))
	callMessages := append([]llm.ChatMessage(nil), msgs...)
	raw, chatErr := room.architect.provider.Chat(ctx, room.sessionID+":"+string(models.AgentRoleArchitect), msgs)
	if chatErr != nil {
		log.Printf("[scripter:diversity_ai] session=%s chat error=%v", sessionID, chatErr)
		return "", "", "fallback", chatErr
	}
	raw = strings.TrimSpace(raw)
	recordScripterLLMExchange(ctx, room, "diversity_ai", callMessages, raw)
	log.Printf("[scripter:diversity_ai] session=%s raw=%s", sessionID, truncateRunes(raw, scripterRawLogLimit))

	mode, focus, ok := parseDiversityAIResponse(raw, candidates)
	if !ok {
		log.Printf("[scripter:diversity_ai] session=%s parse failed or out-of-pool, falling back to random", sessionID)
		return "", "", "fallback", nil
	}
	log.Printf("[scripter:diversity_ai] session=%s selected mode=%q focus=%q source=ai", sessionID, mode, focus)
	return mode, focus, "ai", nil
}

// parseDiversityAIResponse 解析 AI 围池选择的纯文本响应，验证组合是否在候选内。
func parseDiversityAIResponse(raw string, candidates []diversityCombo) (horrorMode, investFocus string, ok bool) {
	var modeVal, focusVal string
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "mode:") {
			modeVal = strings.TrimSpace(line[len("mode:"):])
		} else if strings.HasPrefix(lower, "focus:") {
			focusVal = strings.TrimSpace(line[len("focus:"):])
		}
	}
	if modeVal == "" || focusVal == "" {
		return "", "", false
	}
	// 围池校验
	for _, c := range candidates {
		if strings.TrimSpace(c.HorrorMode) == modeVal && strings.TrimSpace(c.InvestFocus) == focusVal {
			return modeVal, focusVal, true
		}
	}
	return "", "", false
}

type diversityCombo struct {
	HorrorMode  string
	InvestFocus string
}

func diversityComboKey(horrorMode, investFocus string) string {
	horrorMode = strings.TrimSpace(horrorMode)
	investFocus = strings.TrimSpace(investFocus)
	if horrorMode == "" || investFocus == "" {
		return ""
	}
	return horrorMode + "|" + investFocus
}

func loadRecentDiversityCombos(limit int, sessionID string) []diversityCombo {
	if limit <= 0 || models.DB == nil {
		return nil
	}
	var scenarios []models.Scenario
	if err := models.DB.Order("created_at DESC").Limit(limit).Find(&scenarios).Error; err != nil {
		log.Printf("[scripter] session=%s load diversity combos failed: %v", sessionID, err)
		return nil
	}
	combos := make([]diversityCombo, 0, len(scenarios))
	for i := range scenarios {
		if err := scenarios[i].DecodeData(); err != nil {
			continue
		}
		mode := strings.TrimSpace(scenarios[i].Content.Data.HorrorMode)
		focus := strings.TrimSpace(scenarios[i].Content.Data.InvestFocus)
		if mode == "" || focus == "" {
			continue
		}
		combos = append(combos, diversityCombo{HorrorMode: mode, InvestFocus: focus})
	}
	return combos
}

func toneTagsForDiversity(horrorMode, investFocus string, req ScenarioCreationRequest) []string {
	tags := make([]string, 0, 4)
	addTag := func(tag string) {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return
		}
		for _, existing := range tags {
			if existing == tag {
				return
			}
		}
		if len(tags) < 4 {
			tags = append(tags, tag)
		}
	}

	// NOTE: 按神话介入机制映射文风标签；旧 mode 字符串落入 default 分支，历史数据可读但不产生新候选。
	switch horrorMode {
	case "cult_ritual":
		addTag("ritualistic")
		addTag("social-dread")
	case "forbidden_knowledge":
		addTag("forbidden-knowledge")
		addTag("cosmic-dread")
	case "mythos_infiltration":
		addTag("paranoia")
		addTag("social-dread")
	case "bloodline_corruption":
		addTag("gothic")
		addTag("body-horror")
	case "mythos_predation":
		addTag("visceral")
		addTag("survival-dread")
	case "sealed_awakening":
		addTag("ancient-ruins")
		addTag("cosmic-dread")
	case "dimensional_intrusion":
		addTag("reality-distortion")
		addTag("cosmic-dread")
	case "sorcerous_usurpation":
		addTag("occult")
		addTag("loss-of-agency")
	default:
		addTag("slow-burn")
	}

	switch investFocus {
	case "disappearance":
		addTag("vanishing")
	case "bizarre_death":
		addTag("morbid")
	case "artifact_theft":
		addTag("occult-noir")
	case "ritual_interruption":
		addTag("ritualistic")
	case "family_secret":
		addTag("gothic")
	case "local_legend":
		addTag("folk-horror")
	case "sealed_location":
		addTag("claustrophobic")
	case "identity_replacement":
		addTag("paranoia")
	}

	era := strings.ToLower(strings.TrimSpace(req.Era))
	if strings.Contains(era, "1920") || strings.Contains(era, "1950") {
		addTag("noir")
	}
	theme := strings.ToLower(strings.TrimSpace(req.Theme + " " + req.Brief))
	switch {
	case strings.Contains(theme, "民俗") || strings.Contains(theme, "传说") || strings.Contains(theme, "folk") || strings.Contains(theme, "legend"):
		addTag("folk-horror")
	case strings.Contains(theme, "家族") || strings.Contains(theme, "宅") || strings.Contains(theme, "gothic") || strings.Contains(theme, "family"):
		addTag("gothic")
	case strings.Contains(theme, "身份") || strings.Contains(theme, "替换") || strings.Contains(theme, "identity"):
		addTag("paranoia")
	case strings.Contains(theme, "仪式") || strings.Contains(theme, "ritual"):
		addTag("ritualistic")
	}

	for _, fallback := range []string{"slow-burn", "investigative", "coc-dread"} {
		if len(tags) >= 2 {
			break
		}
		addTag(fallback)
	}
	return tags
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

// settingDateRe 匹配 setting 中嵌入的具体年月日，如"1923年10月15日"或"1923年10月15号"。
var settingDateRe = regexp.MustCompile(`\d{3,4}年\d{1,2}月\d{1,2}[日号]`)

// settingHasDate 检查 setting 文本是否包含具体的年月日。
func settingHasDate(s string) bool {
	return settingDateRe.MatchString(s)
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
	} else if !settingHasDate(content.Setting) {
		issues = append(issues, "content.setting 缺少具体年月日（如\"1923年10月15日\"）；setting须嵌入与时代、地点及剧情氛围一致的开局日期")
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
	if strings.TrimSpace(content.MythosAnchor) == "" {
		issues = append(issues, "content.mythos_anchor 为空；神话锚点必须明确写出，作为宇宙法则的具体载体")
	}
	if !hasMythosEssenceClue(content) {
		issues = append(issues, "content.clues 中缺少揭示神话本质的[隐藏]线索（且content.mythos_core也为空）；至少需要一处说明神话真相本身")
	}
	realClueCount := 0
	for _, clue := range content.Clues {
		if strings.HasPrefix(strings.TrimSpace(clue), "[真实]") {
			realClueCount++
		}
	}
	if realClueCount < 2 {
		issues = append(issues, fmt.Sprintf("content.clues 中[真实]线索仅%d条，至少需要2条互相独立、可组合推导的[真实]线索", realClueCount))
	}
	if len(content.NPCs) > 0 && !anyNPCHasSecretDescription(content.NPCs) {
		issues = append(issues, "content.npcs 中没有任何一位NPC的description写明「秘密」或「保留」信息，NPC需要有不主动交代的知情边界")
	}
	if trimmed := strings.TrimSpace(content.SystemPrompt); trimmed != "" && !strings.Contains(trimmed, "真相") && !strings.Contains(trimmed, "内部") {
		issues = append(issues, "content.system_prompt 未体现KP独有的内部真相，建议明确写出「内部真相」或类似表述")
	}
	return issues
}

// checkScenarioTagsOverlap 检查草稿标签是否与近期模组标签重复；命中视为结构问题，走既有修复循环。
func checkScenarioTagsOverlap(draftTags string, blacklist []string) []string {
	if len(blacklist) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(blacklist))
	for _, tag := range blacklist {
		seen[tag] = true
	}
	var issues []string
	for _, tag := range splitScenarioTags(draftTags) {
		if seen[tag] {
			issues = append(issues, fmt.Sprintf("tags 中的「%s」与近期模组标签重复，请更换为更能体现本剧本独特叙事装置的具体标签", tag))
		}
	}
	return issues
}

// hasMythosEssenceClue 容错检查：神话本质既可能仍在clues里以[隐藏]线索呈现，
// 也可能已被normalizeOneshotDraft提取进content.mythos_core，两者满足其一即可。
func hasMythosEssenceClue(content models.ScenarioContent) bool {
	if strings.TrimSpace(content.MythosCore) != "" {
		return true
	}
	for _, clue := range content.Clues {
		clue = strings.TrimSpace(clue)
		if strings.HasPrefix(clue, "[隐藏]") && strings.Contains(clue, "神话本质") {
			return true
		}
	}
	return false
}

func anyNPCHasSecretDescription(npcs []models.NPCData) bool {
	for _, npc := range npcs {
		if strings.Contains(npc.Description, "秘密") || strings.Contains(npc.Description, "保留") {
			return true
		}
	}
	return false
}

func applyGuardrails(draft *ScenarioDraft, req ScenarioCreationRequest, author string, sessionIDs ...string) {
	if draft == nil {
		return
	}
	sessionID := ""
	if len(sessionIDs) > 0 {
		sessionID = sessionIDs[0]
	}
	applyGuardrailsBase(draft, req, author, sessionID)
}

func applyGuardrailsWithNPCBlacklist(draft *ScenarioDraft, req ScenarioCreationRequest, author string, sessionID string, npcBlacklist []string) {
	if draft == nil {
		return
	}
	applyGuardrailsBase(draft, req, author, sessionID)
	enforceNPCBlacklist(draft, npcBlacklist, sessionID)
}

func applyGuardrailsBase(draft *ScenarioDraft, req ScenarioCreationRequest, author string, sessionID string) {
	author = strings.TrimSpace(author)
	if author == "" {
		author = defaultScripterAuthor
	}
	if strings.TrimSpace(req.Name) != "" && draft.Name != strings.TrimSpace(req.Name) {
		log.Printf("[scripter:guardrails] session=%s override name from=%q to=%q", sessionID, draft.Name, strings.TrimSpace(req.Name))
		draft.Name = strings.TrimSpace(req.Name)
	}
	if req.MinPlayers > 0 && draft.MinPlayers != req.MinPlayers {
		log.Printf("[scripter:guardrails] session=%s override min_players from=%d to=%d", sessionID, draft.MinPlayers, req.MinPlayers)
		draft.MinPlayers = req.MinPlayers
	}
	if req.MaxPlayers > 0 && draft.MaxPlayers != req.MaxPlayers {
		log.Printf("[scripter:guardrails] session=%s override max_players from=%d to=%d", sessionID, draft.MaxPlayers, req.MaxPlayers)
		draft.MaxPlayers = req.MaxPlayers
	}
	if draft.MaxPlayers > 0 && draft.MinPlayers > 0 && draft.MaxPlayers < draft.MinPlayers {
		draft.MaxPlayers = draft.MinPlayers
	}
	if strings.TrimSpace(req.Difficulty) != "" && draft.Difficulty != strings.TrimSpace(req.Difficulty) {
		log.Printf("[scripter:guardrails] session=%s override difficulty from=%q to=%q", sessionID, draft.Difficulty, strings.TrimSpace(req.Difficulty))
		draft.Difficulty = strings.TrimSpace(req.Difficulty)
	}
	if draft.Author != author {
		draft.Author = author
	}
}

func enforceNPCBlacklist(draft *ScenarioDraft, npcBlacklist []string, sessionID string) {
	if draft == nil || len(npcBlacklist) == 0 {
		return
	}
	blacklist := map[string]bool{}
	for _, name := range npcBlacklist {
		key := npcBlacklistKey(name)
		if key != "" {
			blacklist[key] = true
		}
	}
	if len(blacklist) == 0 {
		return
	}
	used := map[string]bool{}
	for i := range draft.Content.NPCs {
		name := strings.TrimSpace(draft.Content.NPCs[i].Name)
		if name == "" {
			continue
		}
		key := npcBlacklistKey(name)
		if !blacklist[key] {
			used[key] = true
			continue
		}
		newName := uniqueNPCReplacementName(name, i+1, blacklist, used)
		draft.Content.NPCs[i].Name = newName
		used[npcBlacklistKey(newName)] = true
		log.Printf("[scripter:guardrails] session=%s npc name blacklisted from=%q to=%q", sessionID, name, newName)
	}
}

func uniqueNPCReplacementName(original string, index int, blacklist map[string]bool, used map[string]bool) string {
	base := strings.TrimSpace(original)
	if base == "" {
		base = fmt.Sprintf("替身NPC%d", index)
	}
	candidates := []string{
		fmt.Sprintf("%s·异名", base),
		fmt.Sprintf("%s·线人", base),
		fmt.Sprintf("%s·替身", base),
		fmt.Sprintf("替身NPC%d", index),
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		key := npcBlacklistKey(candidate)
		if candidate != "" && key != "" && !blacklist[key] && !used[key] {
			return candidate
		}
	}
	for i := 1; i <= 99; i++ {
		candidate := fmt.Sprintf("替身NPC%d_%d", index, i)
		key := npcBlacklistKey(candidate)
		if !blacklist[key] && !used[key] {
			return candidate
		}
	}
	return fmt.Sprintf("替身NPC%d", index)
}

func npcBlacklistKey(name string) string {
	return strings.ToLower(normalizeNPCName(name))
}

// ---------------------------------------------------------------------------
// Length / difficulty specs (injected into prompts)
// ---------------------------------------------------------------------------

func difficultySpec(difficulty string) string {
	switch strings.ToLower(strings.TrimSpace(difficulty)) {
	case "easy":
		return "- 派系时间线节点推进缓慢，调查员有充足干预窗口\n- 线索分布：[真实]为主，少量[隐藏]，[误导]至多1条且其错误解释较易被识破\n- 杠杆代价低：对话、基础检定或公开信息即可拉动，无需牺牲\n- NPC初始态度：中立到谨慎，社交检定可以成功说服\n- 恐怖核心层：可进入但不强迫付出理智代价"
	case "hard":
		return "- 派系时间线推进快，干预窗口紧张，超时则产生不可逆后果\n- 线索分布：[隐藏]和[误导]为主（[误导]2-3条，错误解释与真相高度相似、极具迷惑性，四要素完整），表面可见的[真实]线索很少\n- 杠杆代价高：多数杠杆需要对抗检定、道德代价或信息暴露\n- NPC初始态度：多数敌对或欺骗性，说服有实质代价\n- 恐怖核心层：进入需承担显著理智或人际代价"
	default: // normal
		return "- 派系时间线推进速度适中，有几个明确干预窗口\n- 线索分布均衡：[真实]略多，[隐藏]次之，[误导]1-2条且错误解释具有中等迷惑性\n- 杠杆代价适中：部分杠杆可直接拉动，部分需要检定\n- NPC初始态度：混合，部分可说服，部分保持距离\n- 恐怖核心层：需要主动调查和付出一定代价"
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

func chatAndParseJSON[T any](ctx context.Context, generator agentHandle, msgs []llm.ChatMessage, out *T, schemaExample string, tag string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if generator.provider == nil {
		return fmt.Errorf("%s generator provider unavailable", tag)
	}
	sessionID := sessionIDFromContextValue(ctx)
	log.Printf("[scripter:%s] session=%s chat start messages=%d", tag, sessionID, len(msgs))
	callMessages := append([]llm.ChatMessage(nil), msgs...)
	raw, err := generator.provider.Chat(ctx, generator.cacheKey(sessionIDFromContextValue(ctx)), msgs)
	if err != nil {
		log.Printf("[scripter:%s] session=%s chat error=%v", tag, sessionID, err)
		return err
	}
	recordScripterLLMExchange(ctx, nil, tag, callMessages, raw)
	log.Printf("[scripter:%s] session=%s raw len=%d body=%s", tag, sessionID, len(raw), truncateRunes(raw, scripterRawLogLimit))
	parseErr := parseJSONObject(raw, out)
	if parseErr == nil {
		log.Printf("[scripter:%s] session=%s parse ok without repair", tag, sessionID)
		logParsedJSON(tag, sessionID, out)
		return nil
	}
	log.Printf("[scripter:%s] session=%s JSON parse failed: %v raw=%s", tag, sessionID, parseErr, truncateRunes(raw, scripterRawLogLimit))
	fixed, repairErr := RepairJSON(ctx, raw, parseErr, schemaExample)
	if repairErr != nil {
		return fmt.Errorf("%s JSON 修复失败: %w (原始错误: %v)", tag, repairErr, parseErr)
	}
	if err := parseJSONObject(fixed, out); err == nil {
		return nil
	} else {
		log.Printf("[%s] session=%s parser output schema mismatch, retry parser: %v", tag, sessionID, err)
		repairedAgain, repairErr2 := RepairJSON(ctx, fixed, err, schemaExample)
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
			debugf("repair", "session=%s fixed: %v", sessionIDFromContextValue(ctx), trimmed)
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
	sessionID := sessionIDFromContextValue(ctx)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是 JSON 修复工具。用户会给你一段有问题的 JSON 和错误信息,你需要修复它使其匹配目标格式。仅输出修正后的合法 JSON,不要有任何其他文字。\n想清楚再修改，例子是给你看的不是让你无脑套用。"},
	}
	const maxAttempts = 200
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
				"请修复并输出完整的合法 JSON。\n想清楚再修改，例子是给你看的不是让你无脑套用。\n如果有数组，禁止改变元素的个数\n"+
				"注意: 仅输出修正后的 JSON,不要有任何其他文字。",
			currentErr.Error(), raw, schemaExample)
		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fixPrompt})
		callMessages := append([]llm.ChatMessage(nil), msgs...)
		fixed, chatErr := parser.provider.Chat(ctx, parser.cacheKey(sessionIDFromContextValue(ctx)), msgs)
		if chatErr != nil {
			return "", fmt.Errorf("parser 调用失败: %w", chatErr)
		}
		recordScripterLLMExchange(ctx, nil, fmt.Sprintf("parser_repair_attempt_%d", attempt), callMessages, fixed)
		if strings.HasPrefix(fixed, "```json") {
			fixed = strings.TrimPrefix(fixed, "```json")
			fixed = strings.TrimSuffix(fixed, "```")
		}
		debugf("Parser", "session=%s Fixed JSON: %v", sessionID, fixed)
		stripped := fixed
		if json.Valid([]byte(stripped)) {
			log.Printf("[parser] session=%s JSON 修复成功 attempt=%d", sessionID, attempt)
			return stripped, nil
		}
		if s := strings.Index(stripped, "{"); s >= 0 {
			if e := strings.LastIndex(stripped, "}"); e > s {
				candidate := stripped[s : e+1]
				if json.Valid([]byte(candidate)) {
					log.Printf("[parser] session=%s JSON 修复成功(提取) attempt=%d", sessionID, attempt)
					return candidate, nil
				}
			}
		}
		if s := strings.Index(stripped, "["); s >= 0 {
			if e := strings.LastIndex(stripped, "]"); e > s {
				candidate := stripped[s : e+1]
				if json.Valid([]byte(candidate)) {
					log.Printf("[parser] session=%s JSON 修复成功(提取数组) attempt=%d", sessionID, attempt)
					return candidate, nil
				}
			}
		}
		currentErr = fmt.Errorf("修复后的 JSON 仍然无效")
		raw = fixed
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: fixed})
		log.Printf("[parser] session=%s attempt=%d 修复后仍无效", sessionID, attempt)
	}
	return "", fmt.Errorf("parser 修复失败(%d次尝试)", maxAttempts)
}

// marshalExample renders a fully-populated value as a compact JSON string for use
// in schema/repair prompts. The value must marshal cleanly; a marshal failure means
// the example itself is malformed, so it panics at init time rather than silently
// emitting an empty string that would hide the whole structure from the repair LLM.
func marshalExample(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshalExample: %v", err))
	}
	return string(data)
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
	Question   string         `json:"question,omitempty"`
	Constant   string         `json:"constant,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Background *FogBackground `json:"background,omitempty"`
	Draft      *ScenarioDraft `json:"draft,omitempty"`
}

type FogBackground struct {
	TimeAndPlace       string   `json:"time_and_place"`
	InvestigatorHook   string   `json:"investigator_hook"`
	DailyBeauty        string   `json:"daily_beauty"`
	UnsettlingDetail   string   `json:"unsettling_detail"`
	PublicProblem      string   `json:"public_problem"`
	BriefPreserved     string   `json:"brief_preserved"`
	AntiTropeExecution []string `json:"anti_trope_execution"`
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

func parseScripterToolCalls(ctx context.Context, raw string, schemaExample string) ([]scripterToolCall, error) {
	stripped := raw
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
		fixed, repairErr := RepairJSON(ctx, stripped, parseErr, schemaExample)
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
		return marshalExample([]scripterToolCall{{
			Action: "response",
			Reason: "这个公开背景保留了用户brief并给出调查入口。",
			Background: &FogBackground{
				TimeAndPlace:     "时代与地点",
				InvestigatorHook: "调查入口",
			},
		}})
	case "draft":
		draft := oneshotResultExample.toScenarioDraft()
		return marshalExample([]scripterToolCall{{
			Action: "response",
			Reason: "最终草案兼容ScenarioContent并保持剧本语义。",
			Draft:  &draft,
		}})
	default:
		return marshalExample([]scripterToolCall{{
			Action: "response",
			Reason: "解释为什么这样响应。",
		}})
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

func loadRecentNPCNameBlacklist(limit int, sessionIDs ...string) []string {
	sessionID := ""
	if len(sessionIDs) > 0 {
		sessionID = sessionIDs[0]
	}
	if limit <= 0 || models.DB == nil {
		return nil
	}
	var scenarios []models.Scenario
	if err := models.DB.Order("created_at DESC").Limit(limit * 3).Find(&scenarios).Error; err != nil {
		log.Printf("[scripter] session=%s load recent npc blacklist failed: %v", sessionID, err)
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

func loadScenarioTitleSamples(sampleSize int, sessionIDs ...string) []string {
	sessionID := ""
	if len(sessionIDs) > 0 {
		sessionID = sessionIDs[0]
	}
	if sampleSize <= 0 || models.DB == nil {
		return nil
	}
	var scenarios []models.Scenario
	if err := models.DB.Order("created_at DESC").Limit(sampleSize).Find(&scenarios).Error; err != nil {
		log.Printf("[scripter] session=%s load scenario titles failed: %v", sessionID, err)
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

func loadRecentMythosAnchors(limit int, sessionIDs ...string) []string {
	sessionID := ""
	if len(sessionIDs) > 0 {
		sessionID = sessionIDs[0]
	}
	if limit <= 0 || models.DB == nil {
		return nil
	}
	var scenarios []models.Scenario
	if err := models.DB.Order("created_at DESC").Limit(limit * 2).Find(&scenarios).Error; err != nil {
		log.Printf("[scripter] session=%s load recent mythos anchors failed: %v", sessionID, err)
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

// loadRecentScenarioTags 收集近期模组的 Scenario.Tags 中的独立标签，用于叙事桥段级去重。
func loadRecentScenarioTags(limit int, sessionIDs ...string) []string {
	sessionID := ""
	if len(sessionIDs) > 0 {
		sessionID = sessionIDs[0]
	}
	if limit <= 0 || models.DB == nil {
		return nil
	}
	var scenarios []models.Scenario
	if err := models.DB.Order("created_at DESC").Limit(limit * 2).Find(&scenarios).Error; err != nil {
		log.Printf("[scripter] session=%s load recent scenario tags failed: %v", sessionID, err)
		return nil
	}
	seen := map[string]bool{}
	tags := make([]string, 0, limit)
	for i := range scenarios {
		if err := scenarios[i].DecodeData(); err != nil {
			continue
		}
		for _, tag := range splitScenarioTags(scenarios[i].Tags) {
			if seen[tag] {
				continue
			}
			seen[tag] = true
			tags = append(tags, tag)
			if len(tags) >= limit {
				return tags
			}
		}
	}
	return tags
}

// splitScenarioTags 将逗号（含中文顿号/分号变体）分隔的标签字符串拆成独立标签。
func splitScenarioTags(raw string) []string {
	raw = strings.NewReplacer("，", ",", "、", ",", ";", ",", "；", ",").Replace(raw)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
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

func formatScenarioTagsBlacklist(tags []string) string {
	if len(tags) == 0 {
		return "(无)"
	}
	return "- " + strings.Join(tags, "\n- ")
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

func logScripterArtifact(stage string, sessionID string, artifact any) {
	bs, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		log.Printf("[scripter-artifact] session=%s %s marshal failed: %v", sessionID, stage, err)
		return
	}
	log.Printf("[scripter-artifact] session=%s %s len=%d\n%s", sessionID, stage, len(bs), string(bs))
}

func logStagePrompt(tag string, sessionID string, msgs []llm.ChatMessage) {
	log.Printf("[scripter:%s] session=%s prompt messages=%d", tag, sessionID, len(msgs))
	for i, msg := range msgs {
		log.Printf("[scripter:%s] session=%s prompt[%d] role=%s len=%d body=%s", tag, sessionID, i, msg.Role, len(msg.Content), truncateRunes(msg.Content, scripterPromptLogLimit))
	}
}

func logParsedJSON(tag string, sessionID string, value any) {
	bs, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		log.Printf("[scripter:%s] session=%s parsed JSON marshal failed: %v", tag, sessionID, err)
		return
	}
	log.Printf("[scripter:%s] session=%s parsed JSON len=%d body=%s", tag, sessionID, len(bs), truncateRunes(string(bs), scripterRawLogLimit))
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

func sameStringSlice(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
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
