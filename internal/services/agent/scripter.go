// NOTE: Scenario generation pipeline for sandbox-style COC situation briefs.
// Two-stage generation: story architect writes a free-text StoryOutput
// (scripter_story.go), then the compiler stage (scripter_compile.go)
// converts it into the structured ScenarioDraft.
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
	StoryDocument string        `json:"story_document"` // Story Architect 提交的原始故事文档全文
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
	"1920s", "modern",
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
// 摆脱统一的设计文档腔，更接近人类作者的文风。声线取材于COC剧本中常见的记录/文书类叙事
// 装置（委托文书、招募告示、地方志、追述证词），天然能承载邀约缘由与表层任务；只影响用词
// 与节奏，不影响剧情事实。
var scripterProseVoices = []string{
	"委托信体",
	"招募启事体",
	"地方志摘录体",
	"追述证词体",
}

var proseVoiceGuides = map[string]string{
	"委托信体":   "以委托人邀请调查员到场的口吻转述来意：直陈请求与理由，语气客气但克制；不写称呼、落款、日期行等信件格式，只借书信语气传达委托背景",
	"招募启事体":  "像报纸分类广告或招募告示：直陈需求、条件、地点，语气实用、公事公办，不做感情渲染",
	"地方志摘录体": "像县志、教区记事或地方档案的编纂者在整理旧档：只陈述有据可查的事实，日期与地名精确，语气平实不作评论；默认读者熟悉本地，可顺笔带一句与主线无关的地方琐事",
	"追述证词体":  "亲历者事后写下的第一人称记录口吻：按时间顺序平实交代来龙去脉，写下的细节都是“我当时注意到的”；允许一两处“当时没有多想”式的轻描淡写，但不预告后事、不渲染氛围；只借追述语气，不出现署名与写作场景",
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
	translator agentHandle
	// NOTE: compiler 负责把故事文本忠实编译为结构化ScenarioContent；未配置时 fallback 到 architect。
	compiler        agentHandle
	sessionID       string
	req             ScenarioCreationRequest
	titleSamples    []string
	mythosBlacklist []string
	tagsBlacklist   []string
	// usedNPCNames 记录本次生成任务中已通过 generate_npc_name 工具发放的姓名（小写归一化后的 key），
	// 用于避免同一份剧本内 NPC 重名；仅在内存中存在，不落库，不跨生成任务共享。
	usedNPCNames  map[string]bool
	generationLog *scripterGenerationLog
	progressFn    ScripterProgressFunc
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
	// NOTE: compiler 未配置或被禁用时不阻断房间创建，compileStoryToModule 会 fallback 到 architect。
	compiler, _ := loadSingleAgent(models.AgentRoleCompiler)
	return &scripterRoom{
		architect: architect, qa: qa, lawyer: lawyer, translator: translator, compiler: compiler,
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
	r.titleSamples = loadScenarioTitleSamples(80, r.sessionID)
	r.mythosBlacklist = loadRecentMythosAnchors(100, r.sessionID)
	r.tagsBlacklist = loadRecentScenarioTags(60, r.sessionID)
	r.usedNPCNames = make(map[string]bool)
	log.Printf("[scripter] session=%s context prepared title_samples=%d mythos_blacklist=%d tags_blacklist=%d",
		r.sessionID, len(r.titleSamples), len(r.mythosBlacklist), len(r.tagsBlacklist))
}

// ---------------------------------------------------------------------------
// Run — story-to-compile pipeline
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
	log.Printf("[scripter] session=%s story-to-compile generation start req=%s", sessionID, reqJSON)

	log.Printf("[scripter] session=%s stage=constraints start", sessionID)
	r.emitProgress("constraints", "start", "阶段 1/6：构建地理与多样性约束…")
	constraints := r.buildConstraints(ctx)
	log.Printf("[scripter] session=%s stage=constraints done geography=%q", sessionID, strings.Join(constraints.GeographyFlavor, " → "))
	r.emitProgress("constraints", "done", "约束就绪："+strings.Join(constraints.GeographyFlavor, " → "))
	logScripterArtifact("Constraints", sessionID, constraints)

	log.Printf("[scripter] session=%s stage=story start", sessionID)
	r.emitProgress("story", "start", "阶段 2/6：Story Architect 创作故事文档（多轮工具调用，耗时较长）…")
	story, err := generateStoryDocument(ctx, r, constraints)
	if err != nil {
		log.Printf("[scripter] session=%s stage=story error=%v", sessionID, err)
		r.emitProgress("story", "error", "故事文档生成失败："+err.Error())
		return ScenarioCreationOutput{}, fmt.Errorf("故事文档生成失败: %w", err)
	}
	log.Printf("[scripter] session=%s stage=story done doc_len=%d anchor=%q",
		sessionID, len([]rune(story.Document)), truncateRunes(story.MythosAnchor, 80))
	r.emitProgress("story", "done", fmt.Sprintf("故事文档完成：%d字，神话锚点：%s", len([]rune(story.Document)), truncateRunes(story.MythosAnchor, 40)))

	iterations := 1

	// 故事文档结构校验修复：最多2轮
	for round := 1; round <= 2; round++ {
		issues := validateStoryDocument(story)
		if len(issues) == 0 {
			break
		}
		log.Printf("[scripter] session=%s stage=story_repair round=%d issues=%d %v", sessionID, round, len(issues), issues)
		r.emitProgress("story_repair", "start", fmt.Sprintf("故事文档结构修复第 %d 轮：发现 %d 个问题", round, len(issues)))
		repaired, repairErr := repairStoryDocument(ctx, r, constraints, story, issues)
		if repairErr != nil {
			log.Printf("[scripter] session=%s stage=story_repair round=%d failed: %v", sessionID, round, repairErr)
			r.emitProgress("story_repair", "error", fmt.Sprintf("故事文档修复第 %d 轮失败（保留当前文档）", round))
			break
		}
		story = repaired
		iterations++
		log.Printf("[scripter] session=%s stage=story_repair round=%d done doc_len=%d", sessionID, round, len([]rune(story.Document)))
		r.emitProgress("story_repair", "done", fmt.Sprintf("故事文档结构修复第 %d 轮完成", round))
	}

	// 人写化审查：QA 审 AI 腔与写作质感，问题清单走一轮故事文档修复；失败不阻塞生成
	log.Printf("[scripter] session=%s stage=qa_humanize start", sessionID)
	r.emitProgress("qa_humanize", "start", "阶段 3/6：QA 人写化审查…")
	if qaIssues := runStoryQAReview(ctx, r, story.Document, constraints); len(qaIssues) > 0 {
		log.Printf("[scripter] session=%s stage=qa_humanize issues=%d %v", sessionID, len(qaIssues), qaIssues)
		r.emitProgress("qa_humanize", "start", fmt.Sprintf("人写化审查发现 %d 个问题，执行修复", len(qaIssues)))
		logScripterArtifact("QA Humanize Issues", sessionID, qaIssues)
		repaired, repairErr := repairStoryDocument(ctx, r, constraints, story, qaIssues)
		if repairErr != nil {
			log.Printf("[scripter] session=%s stage=qa_humanize repair failed: %v (keeping story)", sessionID, repairErr)
			r.emitProgress("qa_humanize", "error", "人写化修复失败（保留当前故事文档）")
		} else {
			story = repaired
			iterations++
			log.Printf("[scripter] session=%s stage=qa_humanize done doc_len=%d", sessionID, len([]rune(story.Document)))
			r.emitProgress("qa_humanize", "done", "人写化修复完成")
		}
	} else {
		log.Printf("[scripter] session=%s stage=qa_humanize no issues", sessionID)
		r.emitProgress("qa_humanize", "done", "人写化审查通过")
	}

	draft, compileIters, err := r.compileAndFinalize(ctx, story, constraints)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	iterations += compileIters
	logScripterArtifact("Final ScenarioDraft", sessionID, draft)

	return ScenarioCreationOutput{Draft: draft, IronyCore: &IronyCore{}, Iterations: iterations, GenerationLog: r.generationLogText(), StoryDocument: story.Document}, nil
}

// compileAndFinalize 从已有故事文档出发执行 compile→repair→logic_review→reward_agent→normalize，
// 供完整 AI 生成流水线（Run）和管理员上传故事直接编译（RunCompileStoryWithProgress）两条路径共用。
// 返回编译产出的草稿与本阶段内的修复轮次数；error 非空时上层应直接返回失败。
func (r *scripterRoom) compileAndFinalize(ctx context.Context, story StoryOutput, constraints ScripterConstraints) (ScenarioDraft, int, error) {
	sessionID := r.sessionID
	iterations := 0

	log.Printf("[scripter] session=%s stage=compile start", sessionID)
	r.emitProgress("compile", "start", "Compiler 编译结构化数据…")
	draft, rewardConcept, err := compileStoryToModule(ctx, r, story, constraints)
	if err != nil {
		log.Printf("[scripter] session=%s stage=compile error=%v", sessionID, err)
		r.emitProgress("compile", "error", "编译失败："+err.Error())
		return ScenarioDraft{}, iterations, fmt.Errorf("编译失败: %w", err)
	}
	log.Printf("[scripter] session=%s stage=compile done name=%q scenes=%d npcs=%d clues=%d",
		sessionID, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	r.emitProgress("compile", "done", fmt.Sprintf("编译完成：《%s》，场景 %d 个、NPC %d 个、线索 %d 条",
		draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues)))

	applyGuardrails(&draft, r.req, r.architectModelName(), sessionID)

	// Repair loop: up to 2 rounds for structural issues
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
		applyGuardrails(&draft, r.req, r.architectModelName(), sessionID)
		iterations++
		log.Printf("[scripter] session=%s stage=repair round=%d done name=%q scenes=%d npcs=%d clues=%d",
			sessionID, round, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
		r.emitProgress("repair", "done", fmt.Sprintf("结构修复第 %d 轮完成", round))
	}

	// 逻辑审查：QA agent 以故事文档为真相源审因果可达性与编译忠实度，问题清单走一轮修复；失败不阻塞生成
	log.Printf("[scripter] session=%s stage=logic_review start", sessionID)
	r.emitProgress("logic_review", "start", "逻辑一致性审查…")
	if logicIssues := runLogicReview(ctx, r, &draft, story.Document); len(logicIssues) > 0 {
		log.Printf("[scripter] session=%s stage=logic_review issues=%d %v", sessionID, len(logicIssues), logicIssues)
		r.emitProgress("logic_review", "start", fmt.Sprintf("逻辑审查发现 %d 个问题，执行修复", len(logicIssues)))
		logScripterArtifact("Logic Review Issues", sessionID, logicIssues)
		repaired, repairErr := repairOneshotDraft(ctx, r, constraints, &draft, logicIssues)
		if repairErr != nil {
			log.Printf("[scripter] session=%s stage=logic_review repair failed: %v (keeping draft)", sessionID, repairErr)
			r.emitProgress("logic_review", "error", "逻辑修复失败（保留当前草稿）")
		} else {
			draft = repaired
			applyGuardrails(&draft, r.req, r.architectModelName(), sessionID)
			iterations++
			log.Printf("[scripter] session=%s stage=logic_review done name=%q scenes=%d npcs=%d clues=%d",
				sessionID, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
			r.emitProgress("logic_review", "done", "逻辑修复完成")
		}
	} else {
		log.Printf("[scripter] session=%s stage=logic_review no issues", sessionID)
		r.emitProgress("logic_review", "done", "逻辑审查通过")
	}

	// Reward agent (isolated context, optional)：reward_concept 由 compiler 在编译阶段
	// 通读故事文档全文提炼（见 compileStoryToModule），不再依赖 story 阶段的提交字段。
	// compiler 提示词与机制层校验已经基本保证 reward_concept 非空；下面的锚点兜底只覆盖
	// 极端情况下 compiler 两次提交仍为空的场景，避免奖励阶段被静默跳过。
	concept := strings.TrimSpace(rewardConcept)
	if concept == "" && strings.TrimSpace(draft.Content.MythosAnchor) != "" {
		concept = fallbackRewardConcept(draft.Content.MythosAnchor)
		log.Printf("[scripter] session=%s stage=reward_agent compiler未给出reward_concept，改用锚点兜底概念=%q", sessionID, concept)
	}
	if concept != "" {
		log.Printf("[scripter] session=%s stage=reward_agent start concept=%q anchor=%q",
			sessionID, truncateRunes(concept, 200), truncateRunes(draft.Content.MythosAnchor, 200))
		r.emitProgress("reward_agent", "start", "奖励物品设计…")
		rwd, rewardErr := runRewardAgent(ctx, r, concept, draft.Content.MythosAnchor)
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
	normalizeOneshotDraft(&draft, r.req, r.architectModelName(), constraints, r.usedNPCNames, sessionID)
	applyGuardrails(&draft, r.req, r.architectModelName(), sessionID)
	log.Printf("[scripter] session=%s normalization done name=%q players=%d-%d slot=%d scenes=%d npcs=%d clues=%d endings=%d",
		sessionID, draft.Name, draft.MinPlayers, draft.MaxPlayers, draft.Content.GameStartSlot,
		len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues), len(draft.Content.Endings))
	r.emitProgress("normalize", "done", fmt.Sprintf("规范化完成：《%s》，准备入库", draft.Name))

	if issues := validateDraftCompatibility(draft); len(issues) > 0 {
		log.Printf("[scripter] session=%s draft issues after normalization: %v", sessionID, issues)
	}

	return draft, iterations, nil
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
	ProseVoice      string   `json:"prose_voice"` // NOTE: 玩家可见散文的作者声线，只影响文风不影响事实
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

	// NOTE: 多样性组合改为纯随机挑选，不再让 AI 从围池内判断"最契合"——时代/主题等输入
	// 在多次生成间几乎不变，这类判断任务会收敛到少数刻板组合，反而削弱多样性；契合题材交给
	// 下游 Story Architect 在拿到约束后体现。
	horrorMode, investFocus, toneTags := selectDiversityConstraints(r.req, sessionID)
	log.Printf("[scripter] session=%s diversity horror_mode=%q invest_focus=%q tone_tags=%q",
		sessionID, horrorMode, investFocus, strings.Join(toneTags, ","))

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
- 每行一个名称；country/natural_geography阶段正好5个候选，settlement_scale阶段按用户消息要求只输出一个选项；不要编号、解释、标题或描述句。</rules>`

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
		switch stage.Key {
		case "settlement_scale":
			items = filterSettlementScaleCandidates(items)
			if len(items) == 0 {
				items = []string{"城市"}
			}
			choice = items[0]
		case "country":
			items = filterCountryCandidates(items, isModernEra(era))
			if len(items) == 0 {
				items = []string{"美国"}
			}
			choice = items[rand.Intn(len(items))]
		default:
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
	if stageKey == "country" {
		countInstruction += "\n候选中不得包含苏联、苏维埃社会主义共和国联盟。"
		if !isModernEra(era) {
			countInstruction += "\n当前时代非现代，候选中不得包含日本及日本相关政权、地区（如大日本帝国等）。"
		}
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

// buildDiversityCandidates 返回去重后的候选围池；DB为空时返回全笛卡尔积。
// 两层去重：1) 精确组合（mode+focus）不与最近8条重复；2) 边际值——mode或focus单独
// 不与最近2条重复，避免"只换focus不换mode"这类可感知的重复。任一层耗尽都逐级退化，
// 保证候选池不为空。
func buildDiversityCandidates(req ScenarioCreationRequest, sessionID string) []diversityCombo {
	recent := loadRecentDiversityCombos(8, sessionID) // 按 created_at DESC 排列，recent[0] 最新

	comboSet := map[string]bool{}
	for _, combo := range recent {
		if key := diversityComboKey(combo.HorrorMode, combo.InvestFocus); key != "" {
			comboSet[key] = true
		}
	}
	marginalModeSet := map[string]bool{}
	marginalFocusSet := map[string]bool{}
	for i, combo := range recent {
		if i >= 2 {
			break
		}
		if combo.HorrorMode != "" {
			marginalModeSet[combo.HorrorMode] = true
		}
		if combo.InvestFocus != "" {
			marginalFocusSet[combo.InvestFocus] = true
		}
	}

	// 全笛卡尔积
	candidates := make([]diversityCombo, 0, len(scripterHorrorModes)*len(scripterInvestFocuses))
	for _, mode := range scripterHorrorModes {
		for _, focus := range scripterInvestFocuses {
			candidates = append(candidates, diversityCombo{HorrorMode: mode, InvestFocus: focus})
		}
	}

	// 第一层：精确组合 + 边际值双重过滤
	strict := make([]diversityCombo, 0, len(candidates))
	for _, c := range candidates {
		if comboSet[diversityComboKey(c.HorrorMode, c.InvestFocus)] {
			continue
		}
		if marginalModeSet[c.HorrorMode] || marginalFocusSet[c.InvestFocus] {
			continue
		}
		strict = append(strict, c)
	}
	if len(strict) > 0 {
		return strict
	}

	// 第二层：退化为只排除精确组合
	available := make([]diversityCombo, 0, len(candidates))
	for _, c := range candidates {
		if !comboSet[diversityComboKey(c.HorrorMode, c.InvestFocus)] {
			available = append(available, c)
		}
	}
	if len(available) > 0 {
		return available
	}
	// 围池耗尽时退化为全笛卡尔积
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

// isModernEra 判断时代文本是否为现代；采用白名单方式，只有显式包含"现代"/"modern"
// 关键词才视为现代，未显式说明一律视为非现代。
func isModernEra(era string) bool {
	return strings.Contains(era, "现代") || strings.Contains(strings.ToLower(era), "modern")
}

// filterCountryCandidates 过滤country阶段候选：苏联始终禁止选为背景国家；
// 日本仅在非现代年代禁止（现代年代允许）。
func filterCountryCandidates(items []string, modern bool) []string {
	filtered := make([]string, 0, len(items))
	for _, item := range items {
		if strings.Contains(item, "苏联") {
			continue
		}
		if !modern && strings.Contains(item, "日本") {
			continue
		}
		filtered = append(filtered, item)
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

// eventQuoteRe 匹配用引号包裹的片段（可能是转录的人物原话），覆盖中文全角引号、
// ASCII直引号与中文书名号式的单/双直角引号四种常见写法。
var eventQuoteRe = regexp.MustCompile(`“[^”]{2,}”|"[^"]{2,}"|「[^」]{2,}」|『[^』]{2,}』`)

// eventDialogueVerbRe 匹配常见的对话转述动词。
var eventDialogueVerbRe = regexp.MustCompile(`说|问|答|吐露|坦白|承认|告诉|喊|嘟囔|辩解`)

// eventLooksLikeDialogueQuote 判定时间线事件描述是否疑似把对话引语误写成了中性事实记录：
// 引号片段与对话动词须同时命中才报告，避免误伤书名号引用或专有名词。
func eventLooksLikeDialogueQuote(event string) bool {
	return eventQuoteRe.MatchString(event) && eventDialogueVerbRe.MatchString(event)
}

func validateDraftCompatibility(draft ScenarioDraft) []string {
	var issues []string
	if strings.TrimSpace(draft.Name) == "" {
		issues = append(issues, "ScenarioDraft.name 为空")
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
	if trimmed := strings.TrimSpace(content.PlaythroughOutline); trimmed == "" {
		issues = append(issues, "content.playthrough_outline 为空，须给出串联场景/NPC/线索的游玩流程大纲，覆盖场景衔接与分支")
	} else if length := len([]rune(trimmed)); length < 100 {
		issues = append(issues, fmt.Sprintf("content.playthrough_outline 过短（当前%d字），须覆盖场景衔接与分支", length))
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
	validNature := map[string]bool{"真实": true, "隐藏": true, "误导": true}
	for i, clue := range content.Clues {
		if strings.TrimSpace(clue.Summary) == "" {
			issues = append(issues, fmt.Sprintf("content.clues[%d] 缺少 summary", i))
		}
		if !validNature[strings.TrimSpace(clue.Nature)] {
			issues = append(issues, fmt.Sprintf("content.clues[%d] 的 nature 必须是 真实/隐藏/误导 之一", i))
		}
	}
	if len(content.Endings) == 0 {
		issues = append(issues, "content.endings 为空，至少需要一个命名结局")
	}
	for i, ending := range content.Endings {
		if strings.TrimSpace(ending.Name) == "" || strings.TrimSpace(ending.Trigger) == "" {
			issues = append(issues, fmt.Sprintf("content.endings[%d] 缺少 name 或 trigger", i))
		}
	}
	for i, event := range content.Timeline {
		if eventLooksLikeDialogueQuote(event.Event) {
			issues = append(issues, fmt.Sprintf("content.timeline[%d].event 疑似把对话引语误写成了记录（%q），须改写为中性事实记录句（谁在何时做了什么），不引用人物原话", i, truncateRunes(event.Event, 60)))
		}
	}
	if strings.TrimSpace(content.MythosAnchor) == "" {
		issues = append(issues, "content.mythos_anchor 为空；神话锚点必须明确写出，作为宇宙法则的具体载体")
	}
	if !hasMythosEssenceClue(content) {
		issues = append(issues, "content.clues 中缺少揭示神话本质的[隐藏]线索（且content.mythos_core也为空）；至少需要一处说明神话真相本身")
	}
	realClueCount := 0
	for _, clue := range content.Clues {
		if strings.TrimSpace(clue.Nature) == "真实" {
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
		if strings.TrimSpace(clue.Nature) == "隐藏" && strings.Contains(clue.Summary, "神话本质") {
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

// ---------------------------------------------------------------------------
// Length / difficulty specs (injected into prompts)
// ---------------------------------------------------------------------------

func difficultySpec(difficulty string) string {
	switch strings.ToLower(strings.TrimSpace(difficulty)) {
	case "easy":
		return "- 局势推进缓慢，调查员有充足的时间反应，晚一步也来得及\n" +
			"- 多数发现是能直接查证的真实观察；会把人带偏的解释至多一处，稍加追问就能察觉不对\n" +
			"- 想让人开口或让局面松动，通常谈一次、过一个基础检定或翻一份公开记录就够，不需要付出代价\n" +
			"- 当地人对调查员多为中立到谨慎，好好说话能说得通\n" +
			"- 调查员可以走到最接近神话真相的地方，不必被迫付出理智代价"
	case "hard":
		return "- 局势推进很快，能插手的窗口很窄，错过一次就产生无法挽回的后果\n" +
			"- 表面上能直接查证的东西很少：多数关键信息要靠几处发现拼起来才看得懂；会把人带偏的解释有两到三处，且与真相高度相似，推翻它们需要真正的推理\n" +
			"- 想让局面松动几乎都要付出代价：对抗检定、道德上的取舍，或者暴露自己在查什么\n" +
			"- 当地人多数敌对或有意隐瞒，说服他们要付出实质代价\n" +
			"- 接近神话真相必然伴随显著的理智损失或人际代价"
	default: // normal
		return "- 局势按天推进，有几个明确的插手窗口，错过就要多绕一段路\n" +
			"- 发现有真有假：能直接查证的略多，需要拼起来才看得懂的次之，会把人带偏的有一到两处，其错误解释听上去相当合理\n" +
			"- 有些门一推就开，有些要过检定或先换到别的东西才推得开\n" +
			"- 当地人态度混杂，有人肯说，有人回避\n" +
			"- 想接近神话真相得主动去查，并且要付出一些代价"
	}
}

func lengthSpec(targetLength string) string {
	switch strings.ToLower(strings.TrimSpace(targetLength)) {
	case "long", "剧本时间长度: 1week-1month":
		return "- 地点：6-8处调查员会实际走到的地方\n" +
			"- 发现：10-12处调查员能亲自拿到手的具体东西（一份文件、一句证词、一处痕迹、一个检定结果）\n" +
			"- 人物：7-10位有名有姓的人，分属不同立场，各有各在做的事\n" +
			"- 收场：4-8种，每种都有名字，其中至少一种是失败或灾难\n" +
			"- 篇幅：约7000-12000字。事件时间线给出5-12个带具体日期的节点；给守密人的运营建议与不同职业的入场差异（2-5种）尽量写全；若剧情需要持续追踪某个进度，也写清它怎么走"
	case "medium", "剧本时间长度: 3-7d":
		return "- 地点：4-6处调查员会实际走到的地方\n" +
			"- 发现：7-10处调查员能亲自拿到手的具体东西（一份文件、一句证词、一处痕迹、一个检定结果）\n" +
			"- 人物：4-7位有名有姓的人，分属不同立场或利益\n" +
			"- 收场：3-5种，每种都有名字，其中至少一种是失败或灾难\n" +
			"- 篇幅：约4000-7000字。事件时间线建议给出3-6个带具体日期的节点；给守密人的运营建议、职业入场差异、可追踪的进度机制按素材需要提供，可以省略"
	default:
		return "- 地点：3-4处调查员会实际走到的地方\n" +
			"- 发现：5-7处调查员能亲自拿到手的具体东西（一份文件、一句证词、一处痕迹、一个检定结果）\n" +
			"- 人物：2-4位有名有姓的人，各有各的盘算\n" +
			"- 收场：至少2种，每种都有名字，其中至少一种是失败或灾难\n" +
			"- 篇幅：约2500-4000字。事件时间线、给守密人的运营建议、职业入场差异、可追踪的进度机制都属于可选，篇幅有限时略去，不要为凑数硬写"
	}
}

// ---------------------------------------------------------------------------
// JSON repair helpers
// ---------------------------------------------------------------------------

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
	loggedCount := 0 // msgs 中已经写入生成日志的前缀长度，避免每次尝试都把完整历史重复写入（O(N²)）
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
		// NOTE: 只把相对上次记录新增的消息写入生成日志，而不是每次尝试都重新写入
		// msgs 的完整历史；否则 maxAttempts=200 次下来日志构建总量是 O(N²)。
		newMessages := append([]llm.ChatMessage(nil), msgs[loggedCount:]...)
		fixed, chatErr := parser.provider.Chat(ctx, parser.cacheKey(sessionIDFromContextValue(ctx)), msgs)
		if chatErr != nil {
			return "", fmt.Errorf("parser 调用失败: %w", chatErr)
		}
		recordScripterLLMExchange(ctx, nil, fmt.Sprintf("parser_repair_attempt_%d", attempt), newMessages, fixed)
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
		// assistant 回复已经通过 recordScripterLLMExchange 的 response 参数记录。
		loggedCount = len(msgs)
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
	if len(msgs) > 0 {
		msg := msgs[len(msgs)-1]
		log.Printf("[scripter:%s] session=%s prompt[%d] role=%s len=%d body=%s", tag, sessionID, len(msgs)-1, msg.Role, len(msg.Content), truncateRunes(msg.Content, scripterPromptLogLimit))
	}
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
