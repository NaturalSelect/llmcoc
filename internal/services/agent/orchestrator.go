// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
	"github.com/llmcoc/server/internal/services/llm"
)

const MaxKpRound = 30

// MaxKpRoundEncounter 是战斗/追逐激活时的单轮预算：DEX 序需要每个存活参战者各发
// 一次 combat_act/chase_act，参战人数多时远超 MaxKpRound 的常规预算。
const MaxKpRoundEncounter = 40

var internalTagPattern = regexp.MustCompile(`(?s)<(?:ack|direction|response_options)\b[^>]*>.*?</(?:ack|direction|response_options)>`)

// activeSessions prevents concurrent agent runs for the same game session.
var activeSessions sync.Map

// agentHandle pairs a Provider with its DB config and enable state.
type agentHandle struct {
	provider llm.Provider
	config   *models.AgentConfig
	enabled  bool
}

func (h agentHandle) isEnabled() bool {
	if !h.enabled || h.provider == nil {
		return false
	}
	if h.config == nil {
		return true
	}
	return h.config.IsActive
}

// systemPrompt 返回该 Agent 最终使用的系统提示词;运行时不支持覆盖默认提示词,
// 但若该 Agent 配置了 WithJailbreak,会在外层包裹越狱提示词,对所有角色统一生效。
func (h agentHandle) systemPrompt(defaultPrompt string) string {
	if h.config != nil && h.config.WithJailbreak {
		return withJailbreakPrompt(defaultPrompt)
	}
	return defaultPrompt
}

// roleName 返回 agent 配置的角色名,用于 prompt cache key 隔离。
func (h agentHandle) roleName() string {
	if h.config != nil {
		return string(h.config.Role)
	}
	return "unknown"
}

// cacheKey 组成 sessionID:role,用于 prompt cache 隔离。
func (h agentHandle) cacheKey(sessionID string) string {
	return sessionID + ":" + h.roleName()
}

func newAgentHandleFromConfig(cfg *models.AgentConfig, temperatureOverride *float32) (agentHandle, error) {
	if cfg == nil {
		return agentHandle{}, fmt.Errorf("agent config is nil")
	}
	if !cfg.IsActive {
		return agentHandle{config: cfg, enabled: false}, nil
	}
	if cfg.ProviderConfigID == nil || cfg.ProviderConfig == nil || !cfg.ProviderConfig.IsActive {
		return agentHandle{}, fmt.Errorf("agent %q 未绑定可用的 LLM provider", cfg.Role)
	}
	maxTok := cfg.MaxTokens
	if maxTok == 0 {
		maxTok = 1024
	}
	temperature := cfg.Temperature
	if temperatureOverride != nil {
		temperature = *temperatureOverride
	}
	p := llm.NewProviderFromConfig(cfg.ProviderConfig, cfg.ModelName, maxTok, temperature, cfg.DisableTemperature, cfg.ThinkingLevel, cfg.ThinkingBudgetTokens, cfg.ImageViaChat)
	return agentHandle{provider: p, config: cfg, enabled: true}, nil
}

// loadSingleAgent loads an enabled agentHandle for the given role from the database.
func loadSingleAgent(role models.AgentRole) (agentHandle, error) {
	var cfg models.AgentConfig
	err := models.DB.Preload("ProviderConfig").
		Where("role = ?", role).
		First(&cfg).Error
	if err != nil {
		return agentHandle{}, fmt.Errorf("agent %q 未配置,请在管理面板配置 LLM provider", role)
	}
	// NOTE: 诊断日志——用于确认后台保存的 MaxTokens 是否真的被本次加载读到，
	// 排查"后台改了值但生成时仍用旧值"这类问题。
	alog.Debug("load single agent", "role", role, "max_tokens", cfg.MaxTokens, "model", cfg.ModelName)
	h, err := newAgentHandleFromConfig(&cfg, nil)
	if err != nil {
		return agentHandle{}, err
	}
	if !h.isEnabled() {
		return agentHandle{}, fmt.Errorf("agent %q 已禁用,请在管理面板启用", role)
	}
	return h, nil
}

// batchLoadAgents fetches all AgentConfigs in a single DB query.
// Disabled optional agents are returned as disabled handles; disabled required
// agents still fail fast so the core pipeline does not run with nil providers.
func batchLoadAgents() (map[models.AgentRole]agentHandle, error) {
	var configs []models.AgentConfig
	models.DB.Preload("ProviderConfig").Find(&configs)

	index := make(map[models.AgentRole]*models.AgentConfig, len(configs))
	for i := range configs {
		index[configs[i].Role] = &configs[i]
	}

	roles := []models.AgentRole{
		models.AgentRoleDirector,
		models.AgentRoleWriter,
		models.AgentRoleWriterNSFW,
		models.AgentRoleLawyer,
		models.AgentRoleNPC,
		models.AgentRoleNPCNSFW,
		models.AgentRolePainter,
		models.AgentRolePainterNSFW,
		models.AgentRoleDiffusionTranslator,
		models.AgentRoleDramaturg,
	}
	requiredRoles := map[models.AgentRole]bool{
		models.AgentRoleDirector: true,
		models.AgentRoleLawyer:   true,
		models.AgentRoleNPC:      true,
	}
	result := make(map[models.AgentRole]agentHandle, len(roles))
	for _, role := range roles {
		cfg, ok := index[role]
		if !ok {
			if requiredRoles[role] {
				return nil, fmt.Errorf("agent %q 未配置,请在管理面板配置 LLM provider", role)
			}
			result[role] = agentHandle{enabled: false}
			continue
		}
		h, err := newAgentHandleFromConfig(cfg, nil)
		if err != nil {
			if requiredRoles[role] {
				return nil, err
			}
			result[role] = agentHandle{config: cfg, enabled: false}
			continue
		}
		if requiredRoles[role] && !h.isEnabled() {
			return nil, fmt.Errorf("agent %q 已禁用,请在管理面板启用", role)
		}
		result[role] = h
	}
	return result, nil
}

// Run executes the agent pipeline synchronously and returns the structured output.
// Only one pipeline may run per session at a time; concurrent calls receive an error.
func Run(ctx context.Context, gctx GameContext) (RunOutput, error) {
	if _, loaded := activeSessions.LoadOrStore(gctx.Session.ID, struct{}{}); loaded {
		return RunOutput{}, fmt.Errorf("当前房间正在处理上一条消息,请稍候")
	}
	defer activeSessions.Delete(gctx.Session.ID)

	ctx = context.WithValue(ctx, "session", fmt.Sprintf("%v", gctx.Session.ID))
	return run(ctx, gctx)
}

var sessionAgents = sync.Map{}

func getCachedAgents(sessionID uint) (agent map[models.AgentRole]agentHandle, err error) {
	tmp, ok := sessionAgents.Load(sessionID)
	if !ok {
		agent, err = batchLoadAgents()
		if err != nil {
			return nil, err
		}
		sessionAgents.Store(sessionID, agent)
	} else {
		agent = tmp.(map[models.AgentRole]agentHandle)
	}
	return agent, err
}

func deleteCachedAgents(sessionID uint) {
	sessionAgents.Delete(sessionID)
}

func ClearAllCachedAgents() {
	agents := make([]uint, 0)
	sessionAgents.Range(func(key, value any) bool {
		agents = append(agents, key.(uint))
		return true
	})
	for _, id := range agents {
		sessionAgents.Delete(id)
	}
}

func visibleActionNeedsWriter(action ToolCallType) bool {
	switch action {
	case ToolRollDice,
		ToolCreateNPC,
		ToolDestroyNPC,
		ToolActNPC,
		ToolUpdateCharacters,
		ToolManageInventory,
		ToolRecordMonster,
		ToolManageSpell,
		ToolManageRelation,
		ToolManageAsset,
		ToolManageMadness,
		ToolAdvanceTime,
		ToolUpdateNPCCard,
		ToolUpdateLocation,
		ToolUpdateNPCLocation,
		ToolUpdateArmor,
		ToolStartCombat,
		ToolCombatAct,
		ToolEndCombat,
		ToolStartChase,
		ToolChaseAct,
		ToolEndChase:
		return true
	default:
		return false
	}
}

func fallbackWriterDirection(kpReply string) string {
	reply := cleanPlayerVisibleText(kpReply)
	if reply == "" {
		return ""
	}
	return "根据以下KP消息生成白字描述,只展开已经发生的可见事实和环境/NPC反应,停在玩家选择点,不要替玩家决定后续行动:\n" + reply
}

func cleanPlayerVisibleText(text string) string {
	return strings.TrimSpace(internalTagPattern.ReplaceAllString(text, ""))
}

// NOTE: 状态栏只用四种说法：思考、查询规则、掷骰检定、撰写叙事。一批工具里取最靠前的阶段，
// 工具名对玩家没有意义，不再逐个列出。
const (
	progressThinking = "KP 正在思考…"
	progressRule     = "KP 正在查询规则…"
	progressDice     = "KP 正在掷骰检定…"
	progressWriting  = "KP 正在撰写叙事…"
)

func progressKPIteration(_, _ int) string {
	return progressThinking
}

func progressPlannedCalls(calls []llm.ToolCall) string {
	return progressForCalls(calls)
}

func progressExecutingCalls(calls []llm.ToolCall) string {
	return progressForCalls(calls)
}

func progressExecutingCall(call llm.ToolCall) string {
	return progressForCalls([]llm.ToolCall{call})
}

func progressForCalls(calls []llm.ToolCall) string {
	phase := progressThinking
	rank := progressRank(phase)
	for _, call := range calls {
		p := progressToolPhase(ToolCallType(call.Name))
		if r := progressRank(p); r > rank {
			phase, rank = p, r
		}
	}
	return phase
}

func progressRank(phase string) int {
	switch phase {
	case progressRule:
		return 3
	case progressDice:
		return 2
	case progressWriting:
		return 1
	}
	return 0
}

func progressToolPhase(action ToolCallType) string {
	switch action {
	case ToolCheckRule:
		return progressRule
	case ToolRollDice, ToolCombatAct, ToolChaseAct:
		return progressDice
	case ToolWrite, ToolNarrate, ToolAskPlayers, ToolEndGame:
		return progressWriting
	default:
		return progressThinking
	}
}

// formatNativeCallNames 把一轮原生 tool_calls 的工具名拼成日志友好的字符串。
func formatNativeCallNames(calls []llm.ToolCall) string {
	names := make([]string, len(calls))
	for i, call := range calls {
		names[i] = call.Name
	}
	return strings.Join(names, ",")
}

func formatSingleDiceResult(r DiceCheckResult) string {
	hidden := ""
	if r.Hidden {
		hidden = " (暗骰)"
	}
	level := ""
	if r.Level != "" {
		level = fmt.Sprintf("难度:%s ", r.Level)
	}
	verdict := ""
	if r.Target > 0 && r.Result != "" {
		verdict = fmt.Sprintf("目标:%d 结果:%s ", r.Target, r.Result)
	}
	detail := ""
	if r.Detail != "" {
		detail = r.Detail + " "
	}
	return fmt.Sprintf("%v鉴定: %v %v%v%v%v", r.What, r.Roll, level, verdict, detail, hidden)
}

// formatNPCAction 把 AI 玩家的声明渲染成给 KP 的 tool 结果：标明未裁定，空字段省略。
func formatNPCAction(a NPCAction) string {
	var sb strings.Builder
	sb.WriteString("【NPC玩家声明·未裁定】" + a.NPCName)
	if s := strings.TrimSpace(a.Intent); s != "" {
		sb.WriteString(" 意图:" + s)
	}
	if s := strings.TrimSpace(a.Action); s != "" {
		sb.WriteString(" 动作:" + s)
	}
	if s := strings.TrimSpace(a.Dialogue); s != "" {
		sb.WriteString(fmt.Sprintf(" 台词:\"%s\"", s))
	}
	if s := strings.TrimSpace(a.Check); s != "" {
		sb.WriteString(" 申请检定:" + s)
	}
	if s := strings.TrimSpace(a.OOC); s != "" {
		sb.WriteString(" 场外(仅KP可见):" + s)
	}
	return sb.String()
}

// executeSingleDiceCheck wraps executeDiceChecks for a single check.
func executeSingleDiceCheck(dc DiceCheck, players []models.SessionPlayer) DiceCheckResult {
	results := executeDiceChecks([]DiceCheck{dc}, players)
	if len(results) == 0 {
		return DiceCheckResult{DiceCheck: dc}
	}
	return results[0]
}

// ── Turn tracking ─────────────────────────────────────────────────────────────

// clearConsumedTurnActions 按D4精确清理本轮SessionTurnAction:删除已消费的声明,
// 但保留"遭遇仍激活、参与者存活、本轮尚未被combat_act/chase_act读到"的PC声明——
// 这些声明还没被消费,删掉会导致按批次收集时靠后批次玩家的输入静默丢失(修复的
// 核心bug)。combat/chase是run()结束时的局部状态:end_combat/end_chase会把它们
// 置nil，天然覆盖"遭遇本次结束→全删"这一分支，与非战斗/追逐场景的历史行为一致。
func clearConsumedTurnActions(gctx GameContext, combat *models.CombatState, chase *models.ChaseState) {
	keep := unresolvedEncounterUserIDs(gctx.Session.Players, combat, chase)
	q := models.DB.Where("session_id = ? AND round = ?", gctx.Session.ID, gctx.Session.TurnRound)
	if len(keep) > 0 {
		keepIDs := make([]uint, 0, len(keep))
		for id := range keep {
			keepIDs = append(keepIDs, id)
		}
		q = q.Where("user_id NOT IN ?", keepIDs)
	}
	q.Delete(&models.SessionTurnAction{})
	alog.Debug("turn actions cleared", "session", gctx.Session.ID, "round", gctx.Session.TurnRound, "kept", len(keep))
}

// unresolvedEncounterUserIDs 返回遭遇激活时声明尚未被消费的存活PC UserID集合:
// 本轮未行动、且不处于待澄清暂停——待澄清暂停会用新问题取代旧声明,旧声明必须
// 清掉,否则下一次玩家的新回答与旧声明共存会让Director误读(见待澄清分支设计)。
func unresolvedEncounterUserIDs(players []models.SessionPlayer, combat *models.CombatState, chase *models.ChaseState) map[uint]bool {
	keep := map[uint]bool{}
	collectUnresolved := func(name string, isNPC, hasActed, pendingClarification bool, userID uint) {
		if isNPC || hasActed || pendingClarification {
			return
		}
		if isCharacterAlive(name, false, players, nil) {
			keep[userID] = true
		}
	}
	if combat != nil && combat.Active {
		for _, p := range combat.Participants {
			collectUnresolved(p.Name, p.IsNPC, p.HasActed, p.PendingClarification, p.UserID)
		}
	}
	if chase != nil && chase.Active {
		for _, p := range chase.Participants {
			collectUnresolved(p.Name, p.IsNPC, p.HasActed, p.PendingClarification, p.UserID)
		}
	}
	return keep
}

// gameTimeUnset 是 <now> 里剧中时间尚未设定时的提示，KP 据此在第一批工具调用里用 advance_time 设定。
const gameTimeUnset = "未设定——请在本周期第一批工具调用里用advance_time设定绝对时间(YYYY-MM-DDTHH:MM)"

// describeGameTime 把剧中时间渲染给 LLM：当前时间、星期，以及距开局已过多久。
// 「距开局已过」只给 LLM 看，玩家端的 time_point 和 time 事件里只有纯时间字符串。
func describeGameTime(start, now string) string {
	nowT, err := models.ParseGameTime(now)
	if err != nil {
		return gameTimeUnset
	}
	desc := fmt.Sprintf("%s(%s", nowT.Format(models.GameTimeLayout), chineseWeekday(nowT.Weekday()))
	if startT, err := models.ParseGameTime(start); err == nil {
		desc += "，距开局已过" + formatGameDuration(nowT.Sub(startT))
	}
	return desc + ")"
}

func chineseWeekday(d time.Weekday) string {
	return "周" + string([]rune("日一二三四五六")[d])
}

// formatGameDuration 渲染为 XhYYm，小时可以超过 24；不足一小时只写分钟。
func formatGameDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	mins := int(d / time.Minute)
	if mins >= 60 {
		return fmt.Sprintf("%dh%02dm", mins/60, mins%60)
	}
	return fmt.Sprintf("%dm", mins)
}

// ResetMadnessAfterSession clears non-permanent madness state on session end.
// Returns true when any field was changed.
func ResetMadnessAfterSession(card *models.CharacterCard) bool {
	if card == nil {
		return false
	}
	if card.MadnessState == "permanent" {
		return false
	}
	changed := card.MadnessState != "none" || card.MadnessSymptom != "" || card.MadnessDuration != 0 || card.DailySanLoss != 0
	card.MadnessState = "none"
	card.MadnessSymptom = ""
	card.MadnessDuration = 0
	card.DailySanLoss = 0
	card.DailySanLossDay = 0
	return changed
}

func mapSkillToStat(card *models.CharacterCard, skill string) (val int, ok bool) {
	if card == nil {
		return 0, false
	}
	switch skill {
	case "力量", "STR":
		return card.Stats.Data.STR, true
	case "体质", "CON":
		return card.Stats.Data.CON, true
	case "体型", "SIZ":
		return card.Stats.Data.SIZ, true
	case "敏捷", "DEX":
		return card.Stats.Data.DEX, true
	case "外貌", "APP":
		return card.Stats.Data.APP, true
	case "灵感", "智力", "INT":
		return card.Stats.Data.INT, true
	case "意志", "POW":
		return card.Stats.Data.POW, true
	case "教育", "EDU":
		return card.Stats.Data.EDU, true
	case "幸运", "LUCK":
		return card.Stats.Data.Luck, true
	default:
		return 0, false
	}
}

// executeDiceChecks auto-rolls all checks the Director requested.
// Actual skill values are looked up from character cards when available.
// For check_type="sanity", the current SAN value is used and SAN loss is
// calculated automatically from the san_success_loss / san_fail_loss fields.
func executeDiceChecks(checks []DiceCheck, players []models.SessionPlayer) []DiceCheckResult {
	if len(checks) == 0 {
		return nil
	}
	results := make([]DiceCheckResult, 0, len(checks))
	for _, dc := range checks {
		diceVal := game.RollDiceExpr(dc.DiceExpr)
		res := DiceCheckResult{DiceCheck: dc, Roll: diceVal}
		if net := modifierNet(dc); net != 0 {
			m := game.RollD100Modified(net)
			diceVal = m.Roll
			res.Roll = diceVal
			res.Detail = m.Describe(net)
		}
		if target, ok := d100CheckTarget(dc, players); ok {
			res.Target = target
			res.Result = game.CheckResultLabel(diceVal, target, dc.Level)
		}
		results = append(results, res)
	}
	return results
}

// NOTE: 只给调查员的 1D100 检定查目标值：理智用当前 SAN，其余先查属性再查技能；
// 名字对不上、NPC、伤害骰都查不到，返回 false，结论仍由 KP 自行对照。
func d100CheckTarget(dc DiceCheck, players []models.SessionPlayer) (int, bool) {
	if !strings.EqualFold(strings.TrimSpace(dc.DiceExpr), "1d100") {
		return 0, false
	}
	what := checkName(dc.What)
	if what == "" {
		return 0, false
	}
	idx := investigatorIndex(players, dc.Character)
	if idx < 0 {
		return 0, false
	}
	card := &players[idx].CharacterCard
	if isSanityCheck(what) {
		return card.Stats.Data.SAN, true
	}
	if v, ok := mapSkillToStat(card, what); ok {
		return v, true
	}
	if v, ok := card.Skills.Data[what]; ok {
		return v, true
	}
	return 0, false
}

func checkName(what string) string {
	what = strings.TrimSpace(what)
	for _, suffix := range []string{"检定", "鉴定"} {
		what = strings.TrimSuffix(what, suffix)
	}
	return what
}

func isSanityCheck(what string) bool {
	return what == "理智" || strings.EqualFold(what, "SAN")
}

// NOTE: COC7 规定奖励骰与惩罚骰不适用于理智检定，且各最多两个；非 1D100 的表达式也不适用。
// 返回奖励骰减惩罚骰的净数量，0 表示按普通 D100 掷。
func modifierNet(dc DiceCheck) int {
	if !strings.EqualFold(strings.TrimSpace(dc.DiceExpr), "1d100") || isSanityCheck(checkName(dc.What)) {
		return 0
	}
	return clamp(dc.Bonus, 0, 2) - clamp(dc.Penalty, 0, 2)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func buildPlayerStatus(players []models.SessionPlayer) string {
	if len(players) == 0 {
		return ""
	}
	s := "调查员状态:"
	for _, p := range players {
		card := p.CharacterCard
		line := fmt.Sprintf("\n• %s(%s)HP:%d/%d SAN:%d/%d",
			card.Name, card.Occupation,
			card.Stats.Data.HP, card.Stats.Data.MaxHP,
			card.Stats.Data.SAN, card.Stats.Data.MaxSAN)

		// 疯狂状态标注
		switch card.MadnessState {
		case "temporary":
			line += "【临时性疯狂:" + card.MadnessSymptom + "】"
		case "indefinite":
			line += "【不定性疯狂(任何SAN损失将再次触发发作):" + card.MadnessSymptom + "】"
		case "permanent":
			line += "【永久性疯狂:" + card.MadnessSymptom + "】"
		}

		// 伤亡状态标注
		switch card.WoundState {
		case "major":
			line += "【重伤】"
		case "dying":
			line += "【濒死(需急救)】"
		case "dead":
			line += "【已死亡】"
		}
		if card.IsUnconscious && card.WoundState != "dead" {
			line += "【昏迷】"
		}

		// 克苏鲁神话技能
		if card.CthulhuMythosSkill > 0 {
			line += fmt.Sprintf("(克苏鲁神话技能:%d,最大SAN上限:%d)", card.CthulhuMythosSkill, game.ComputeMaxSAN(card.CthulhuMythosSkill, card.Race == "" || card.Race == "人类"))
		}
		if len(card.SeenMonsters.Data) > 0 {
			line += fmt.Sprintf("(已见神话存在:%s)", strings.Join(card.SeenMonsters.Data, "、"))
		}
		s += line
	}
	return s
}

// buildCharacterDetail returns a compact XML character card dump for the given character name.
// If characterName is empty, all players' cards are returned.
func buildCharacterDetail(characterName string, players []models.SessionPlayer) string {
	if len(players) == 0 {
		return `<err>当前无调查员</err>`
	}

	var sb strings.Builder
	sb.WriteString("<chars>")
	matched := 0
	for _, p := range players {
		card := p.CharacterCard
		if characterName != "" && card.Name != characterName {
			continue
		}
		matched++
		st := card.Stats.Data
		sb.WriteString(fmt.Sprintf(`<c n=%q race=%q job=%q age=%q gender=%q loc=%q armor=%q>`, card.Name, card.Race, card.Occupation, compactInt(card.Age), card.Gender, p.Location, compactInt(p.Armor)))
		sb.WriteString(fmt.Sprintf(`<stat base="STR=%d CON=%d SIZ=%d DEX=%d APP=%d INT=%d POW=%d EDU=%d" hp="%d/%d" mp="%d/%d" san="%d/%d" luck="%d" mov="%d" build="%d" db=%q/>`,
			st.STR, st.CON, st.SIZ, st.DEX, st.APP, st.INT, st.POW, st.EDU,
			st.HP, st.MaxHP, st.MP, st.MaxMP, st.SAN, st.MaxSAN, st.Luck, st.MOV, st.Build, st.DB))
		if card.CthulhuMythosSkill > 0 {
			sb.WriteString(fmt.Sprintf(`<mythos skill="%d" max_san="%d"/>`, card.CthulhuMythosSkill, game.ComputeMaxSAN(card.CthulhuMythosSkill, card.Race == "" || card.Race == "人类")))
		}
		writeCompactMap(&sb, "skills", card.Skills.Data)
		writeCompactInventory(&sb, card.Inventory.Data)
		writeCompactList(&sb, "spells", card.Spells.Data)
		writeCompactList(&sb, "seen", card.SeenMonsters.Data)
		if rels := effectiveRelations(card.SocialRelations.Data, p.SessionRelations.Data); len(rels) > 0 {
			sb.WriteString("<rels>")
			for _, r := range rels {
				sb.WriteString(fmt.Sprintf(`<rel n=%q type=%q note=%q/>`, r.Name, r.Relationship, r.Note))
			}
			sb.WriteString("</rels>")
		}
		if len(card.Assets.Data) > 0 {
			sb.WriteString("<assets>")
			for _, a := range card.Assets.Data {
				sb.WriteString(fmt.Sprintf(`<asset n=%q cat=%q note=%q/>`, a.Name, a.Category, a.Note))
			}
			sb.WriteString("</assets>")
		}
		if p.SessionMemory != "" {
			sb.WriteString(fmt.Sprintf("<memory>%s</memory>", xmlEscape(p.SessionMemory)))
		}
		if card.MadnessState != "" && card.MadnessState != "none" {
			sb.WriteString(fmt.Sprintf(`<mad state=%q symptom=%q/>`, card.MadnessState, card.MadnessSymptom))
		}
		sb.WriteString("</c>")
	}
	if matched == 0 {
		return `<err>未找到角色：` + xmlEscape(characterName) + `</err>`
	}
	sb.WriteString("</chars>")
	return sb.String()
}

// buildNPCDetail returns a detailed NPC card dump for temporary/session NPCs.
// If npcName is empty, returns all known NPC details in this session context.
// npcNameMatch returns true when query is empty, is an exact match, or is a
// substring of name. For ASCII names, comparison is case-insensitive;
// for Chinese (and other non-ASCII) names, direct substring matching is used
// since they have no concept of case.
func npcNameMatch(name, query string) bool {
	if query == "" {
		return true
	}
	if name == query {
		return true
	}
	// Case-insensitive ASCII fallback.
	if strings.Contains(strings.ToLower(name), strings.ToLower(query)) {
		return true
	}
	// Direct substring match for Chinese / non-ASCII names.
	return strings.Contains(name, query)
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func compactInt(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

func writeCompactText(sb *strings.Builder, tag, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	sb.WriteString("<")
	sb.WriteString(tag)
	sb.WriteString(">")
	sb.WriteString(xmlEscape(text))
	sb.WriteString("</")
	sb.WriteString(tag)
	sb.WriteString(">")
}

// equippedLabels 把已装备物品的 Label 用「、」连成一行，没有装备时返回空串。
func equippedLabels(items []models.Item) string {
	var labels []string
	for _, it := range models.EquippedItems(items) {
		labels = append(labels, it.Label())
	}
	return strings.Join(labels, "、")
}

func writeCompactInventory(sb *strings.Builder, items []models.Item) {
	if len(items) == 0 {
		return
	}
	sb.WriteString("<inv>")
	for _, it := range items {
		sb.WriteString(fmt.Sprintf("<item n=%q", it.Name))
		if it.Desc != "" {
			sb.WriteString(fmt.Sprintf(" desc=%q", it.Desc))
		}
		if it.Count > 1 {
			sb.WriteString(fmt.Sprintf(` x="%d"`, it.Count))
		}
		if it.Type != "" && it.Type != models.ItemTypeMisc {
			sb.WriteString(fmt.Sprintf(" type=%q", string(it.Type)))
		}
		if it.Equipped {
			sb.WriteString(` eq="1"`)
		}
		sb.WriteString("/>")
	}
	sb.WriteString("</inv>")
}

func writeCompactList(sb *strings.Builder, tag string, items []string) {
	clean := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			clean = append(clean, item)
		}
	}
	if len(clean) == 0 {
		return
	}
	writeCompactText(sb, tag, strings.Join(clean, "; "))
}

func writeCompactMap(sb *strings.Builder, tag string, values map[string]int) {
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, values[key]))
	}
	writeCompactText(sb, tag, strings.Join(parts, " "))
}

func buildNPCDetail(npcName string, tempNPCs []models.SessionNPC, scenarioNPCs []models.NPCData) string {
	var sb strings.Builder
	sb.WriteString("<npcs>")
	matched := 0

	// 1. 优先搜索临时 NPC（动态状态）
	for _, npc := range tempNPCs {
		if !npcNameMatch(npc.Name, npcName) {
			continue
		}
		matched++
		status := npcCompactState(npc)
		sb.WriteString(fmt.Sprintf(`<npc n=%q race=%q job=%q mythos=%q src="tmp" st=%q wound_state=%q loc=%q>`, npc.Name, npc.Race, npc.Occupation, compactInt(npc.CthulhuMythosSkill), status, npc.WoundState, npc.Location))
		writeCompactText(&sb, "desc", npc.Description)
		writeCompactText(&sb, "att", strings.TrimSpace(npc.Attitude))
		writeCompactText(&sb, "goal", strings.TrimSpace(npc.Goal))
		writeCompactText(&sb, "secret", strings.TrimSpace(npc.Secret))
		writeCompactText(&sb, "risk", strings.TrimSpace(npc.RiskPref))
		writeCompactText(&sb, "memory", npc.SessionMemory)
		writeCompactMap(&sb, "stats", npc.Stats.Data)
		writeCompactMap(&sb, "skills", npc.Skills.Data)
		writeCompactList(&sb, "spells", npc.Spells.Data)
		sb.WriteString("</npc>")
	}

	// 如果临时 NPC 中没有匹配，则回退到剧本静态 NPC（只读基准信息）
	if matched == 0 {
		for _, npc := range scenarioNPCs {
			if !npcNameMatch(npc.Name, npcName) {
				continue
			}
			matched++
			race := npc.Race
			if race == "" {
				race = "人类"
			}
			sb.WriteString(fmt.Sprintf(`<npc n=%q race=%q job=%q mythos=%q src="scenario">`, npc.Name, race, npc.Occupation, compactInt(npc.CthulhuMythosSkill)))
			writeCompactText(&sb, "desc", npc.Description)
			writeCompactText(&sb, "att", npc.Attitude)
			writeCompactMap(&sb, "stats", npc.Stats)
			writeCompactMap(&sb, "skills", npc.Skills)
			writeCompactList(&sb, "spells", npc.Spells)
			sb.WriteString("</npc>")
		}
	}

	if matched == 0 {
		if npcName == "" {
			return `<err>当前无可查询NPC</err>`
		}
		return `<err>未找到NPC：` + xmlEscape(npcName) + `</err>`
	}
	sb.WriteString("</npcs>")
	return sb.String()
}

// buildPlayerBrief returns a minimal one-line summary of all players for the KP context.
// It only shows critical combat stats and flags when full details should be queried.
// Full character details are available on-demand via the query_character tool.
func buildPlayerBrief(players []models.SessionPlayer) string {
	if len(players) == 0 {
		return ""
	}
	hasNotHuman := false
	hasSessionMemory := false
	s := "【调查员概况(完整人物卡请用 query_character 获取)】\n所有角色均已成年"
	for _, p := range players {
		card := p.CharacterCard
		line := fmt.Sprintf("\n<character> %s(%s,种族:%s)HP:%d/%d SAN:%d/%d",
			card.Name, card.Occupation, card.Race,
			card.Stats.Data.HP, card.Stats.Data.MaxHP,
			card.Stats.Data.SAN, card.Stats.Data.MaxSAN)
		if card.Race != "" && card.Race != "人类" {
			line += " 【非人类】"
			hasNotHuman = true
		}
		if p.Location != "" {
			line += fmt.Sprintf("【位置:%s】", p.Location)
		}
		if p.Armor > 0 {
			line += fmt.Sprintf("【护甲:%d】", p.Armor)
		}
		line += fmt.Sprintf("【DEX:%d】", card.Stats.Data.DEX)
		line += fmt.Sprintf("【POW:%d】", card.Stats.Data.POW)
		line += fmt.Sprintf("【APP:%d】", card.Stats.Data.APP)
		line += fmt.Sprintf("【MOV:%d】", card.Stats.Data.MOV)
		if strings.TrimSpace(p.SessionMemory) != "" {
			line += "【有Session级特殊状态:需query_character查看】"
			hasSessionMemory = true
		}
		switch card.MadnessState {
		case "temporary":
			line += "【临时性疯狂】"
		case "indefinite":
			line += "【不定性疯狂】"
		case "permanent":
			line += "【永久性疯狂】"
		}
		if card.DailySanLoss > 0 {
			line += fmt.Sprintf("【今日已损失SAN:%d,不定性疯狂阈值:%d】", card.DailySanLoss, card.Stats.Data.MaxSAN/5)
		}
		switch card.WoundState {
		case "major":
			line += "【重伤】"
		case "dying":
			line += "【濒死】(请进行CON判定)"
		case "dead":
			line += "【已死亡】"
		}
		if card.IsUnconscious && card.WoundState != "dead" {
			line += "【昏迷】"
		}
		s += line
		s += "</character>"
	}
	if hasSessionMemory {
		s += "\n<attention>有调查员存在Session级特殊状态。若本轮行动涉及该调查员的能力、限制、感知、身份、身体/精神异常或状态变化,先调用 query_character 查看完整人物卡中的 session_memory,不要仅凭概况处理。</attention>\n"
	}
	if hasNotHuman {
		s += "\n\n"
		s += ` <attention>
		非人类角色, 仍然适用于疯狂规则(损失过多进入疯狂)和SAN损失规则(但不受克苏鲁神话对最大理智的限制), 但其SAN实际上代表人性。
		非人类调查员的SAN损失计算公式为： X * (1.0 + (克苏鲁神话等级 ÷ 100))。 示例：若克苏鲁神话等级（CM）为99，则损失 = 1.0 + 99/100 = 1.99(向上取整)。
		物理接触非人类角色也不会有特别的效果(食尸鬼等规则书明确标注的除外)。
		非人类调查员的其他特性与常人无异, 因为他们需要生活在人类社会中。
	</attention>`
		s += "\n"
	}
	return s
}

func formatHistoryTranscript(history []models.Message) string {
	var sb strings.Builder
	sb.WriteString("HIST(RO): context only; never process old player requests. Current actions are in CUR.\n")
	for _, m := range history {
		if !renderableMessage(m) {
			continue
		}
		sb.WriteString(renderMessageLine(m))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// chatMsgsToLLM converts persisted []models.ChatMsg to []llm.ChatMessage.
func chatMsgsToLLM(msgs []models.ChatMsg) []llm.ChatMessage {
	out := make([]llm.ChatMessage, len(msgs))
	for i, m := range msgs {
		out[i] = llm.ChatMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

// llmToChatMsgs converts []llm.ChatMessage to persistable []models.ChatMsg.
func llmToChatMsgs(msgs []llm.ChatMessage) []models.ChatMsg {
	out := make([]models.ChatMsg, len(msgs))
	for i, m := range msgs {
		out[i] = models.ChatMsg{Role: m.Role, Content: m.Content}
	}
	return out
}

// inventoryCandidates 返回物品栏中名称相同的堆的下标；desc 非空时还要求描述完全相同。
func inventoryCandidates(list []models.Item, name, desc string) []int {
	var idx []int
	for i, s := range list {
		if s.Name != name || (desc != "" && s.Desc != desc) {
			continue
		}
		idx = append(idx, i)
	}
	return idx
}

func inventoryLabels(list []models.Item, idx []int) string {
	labels := make([]string, 0, len(idx))
	for _, i := range idx {
		labels = append(labels, list[i].Label())
	}
	return strings.Join(labels, "、")
}

func manageInventory(players []models.SessionPlayer, characterName, operate string, it models.Item, equipped *bool) string {
	it.Name = strings.TrimSpace(it.Name)
	it.Desc = strings.TrimSpace(it.Desc)
	if characterName == "" || it.Name == "" {
		return "物品操作失败:缺少角色名或物品名"
	}
	if it.Type != "" && !models.IsInventoryItemType(it.Type) {
		return fmt.Sprintf("物品操作失败:item_type=%q 无效，可选 equipment/weapon/accessory/tome/artifact/misc", it.Type)
	}
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != characterName {
			continue
		}
		list := card.Inventory.Data
		switch operate {
		case "add":
			if equipped != nil {
				it.Equipped = *equipped
			}
			it = models.NormalizeItem(it)
			card.Inventory.Data = models.AddItem(list, it)
			models.DB.Save(card)
			result := fmt.Sprintf("%s 获得物品:%s", card.Name, it.Label())
			if it.Equipped {
				result += "(已装备)"
			}
			for _, s := range card.Inventory.Data {
				if s.SameStack(it) && s.Count > it.Count {
					result += fmt.Sprintf("，现共 %d 件", s.Count)
				}
			}
			return result
		case "remove":
			cands := inventoryCandidates(list, it.Name, it.Desc)
			if len(cands) == 0 {
				return fmt.Sprintf("%s 物品栏中没有 %s", card.Name, models.Item{Name: it.Name, Desc: it.Desc}.Label())
			}
			if len(cands) > 1 {
				return fmt.Sprintf("物品操作失败:%s 有多件同名物品(%s)，请用 item_desc 指定要移除哪一件，本次未修改任何物品", card.Name, inventoryLabels(list, cands))
			}
			stack := list[cands[0]]
			count := max(1, it.Count)
			out, _, _ := models.TakeItem(list, stack.Name, stack.Desc, count)
			card.Inventory.Data = out
			models.DB.Save(card)
			switch {
			case count < stack.Count:
				left := stack
				left.Count -= count
				return fmt.Sprintf("%s 物品数量减少:%s → %s", card.Name, stack.Label(), left.Label())
			case count == stack.Count:
				return fmt.Sprintf("%s 失去物品:%s", card.Name, stack.Label())
			default:
				return fmt.Sprintf("%s 失去物品:%s(只有 %d 件，已全部移除)", card.Name, stack.Label(), stack.Count)
			}
		case "update":
			if it.Desc == "" && it.Type == "" && equipped == nil {
				return "物品操作失败:update 需要提供 item_desc、item_type 或 item_equipped"
			}
			cands := inventoryCandidates(list, it.Name, "")
			if len(cands) == 0 {
				return fmt.Sprintf("%s 物品栏中没有 %s", card.Name, it.Name)
			}
			if len(cands) > 1 {
				return fmt.Sprintf("物品操作失败:%s 有多件同名物品(%s)，update 无法确定修改哪一件，请先 remove 对应物品再 add，本次未修改任何物品", card.Name, inventoryLabels(list, cands))
			}
			updated := slices.Clone(list)
			old := updated[cands[0]].Label()
			if it.Desc != "" {
				updated[cands[0]].Desc = it.Desc
			}
			if it.Type != "" {
				updated[cands[0]].Type = it.Type
			}
			equipNote := ""
			if equipped != nil && updated[cands[0]].Equipped != *equipped {
				updated[cands[0]].Equipped = *equipped
				equipNote = "(已卸下)"
				if *equipped {
					equipNote = "(已装备)"
				}
			}
			now := updated[cands[0]].Label()
			card.Inventory.Data = models.MergeStacks(updated)
			models.DB.Save(card)
			return fmt.Sprintf("%s 物品更新:%s → %s%s", card.Name, old, now, equipNote)
		default:
			return fmt.Sprintf("物品操作失败:未知的 operate=%q，只能是 add/remove/update", operate)
		}
	}
	return fmt.Sprintf("物品操作失败:未找到角色 %s", characterName)
}

func manageSeenMonster(players []models.SessionPlayer, characterName, operate, monster string) string {
	if characterName == "" || monster == "" {
		return "神话存在记录失败:缺少角色名或神话存在名称"
	}
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != characterName {
			continue
		}
		list := card.SeenMonsters.Data
		if operate == "remove" {
			list = removeString(list, monster)
			card.SeenMonsters.Data = list
			models.DB.Save(card)
			return fmt.Sprintf("%s 已移除神话存在记录:%s", card.Name, monster)
		}
		list = appendUniqueString(list, monster)
		card.SeenMonsters.Data = list
		models.DB.Save(card)
		return fmt.Sprintf("%s 已记录神话存在:%s", card.Name, monster)
	}
	return fmt.Sprintf("神话存在记录失败:未找到角色 %s", characterName)
}

func manageSpell(players []models.SessionPlayer, characterName, operate, spell string) string {
	if characterName == "" || spell == "" {
		return "法术操作失败:缺少角色名或法术名"
	}
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != characterName {
			continue
		}
		list := card.Spells.Data
		if operate == "remove" {
			list = removeString(list, spell)
			card.Spells.Data = list
			models.DB.Save(card)
			return fmt.Sprintf("%s 遗失法术:%s", card.Name, spell)
		}
		list = appendUniqueString(list, spell)
		card.Spells.Data = list
		models.DB.Save(card)
		return fmt.Sprintf("%s 学会法术:%s", card.Name, spell)
	}
	return fmt.Sprintf("法术操作失败:未找到角色 %s", characterName)
}

// manageSocialRelation 只写本局(tier1)的 SessionRelations，不再直接改人物卡：
// 浅层的一局性关系不应该挤占人物卡空间，是否提升进人物卡由结算时的 AI 判断决定
// (见 RunRelationPromotion / mergeSessionRelations)。
func manageSocialRelation(players []models.SessionPlayer, characterName, operate string, rel *models.SocialRelation) string {
	if characterName == "" || rel == nil || rel.Name == "" {
		return "社会关系操作失败:缺少角色名或关系条目"
	}
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != characterName {
			continue
		}
		if operate == "remove" {
			players[i].SessionRelations.Data = upsertSessionRelation(players[i].SessionRelations.Data, *rel, true)
			models.DB.Save(&players[i])
			return fmt.Sprintf("%s 本局关系已移除:%s", card.Name, rel.Name)
		}
		players[i].SessionRelations.Data = upsertSessionRelation(players[i].SessionRelations.Data, *rel, false)
		models.DB.Save(&players[i])
		return fmt.Sprintf("%s 本局关系已记录:%s(%s)(结算时决定是否写入人物卡)", card.Name, rel.Name, rel.Relationship)
	}
	return fmt.Sprintf("社会关系操作失败:未找到角色 %s", characterName)
}

func manageAsset(players []models.SessionPlayer, characterName, operate string, asset *models.Asset) string {
	if characterName == "" || asset == nil || asset.Name == "" {
		return "资产操作失败:缺少角色名或资产条目"
	}
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != characterName {
			continue
		}
		list := card.Assets.Data
		if operate == "remove" {
			filtered := make([]models.Asset, 0, len(list))
			for _, existing := range list {
				if existing.Name == asset.Name {
					continue
				}
				filtered = append(filtered, existing)
			}
			card.Assets.Data = filtered
			models.DB.Save(card)
			return fmt.Sprintf("%s 移除资产:%s", card.Name, asset.Name)
		}

		updated := false
		for idx := range list {
			if list[idx].Name == asset.Name {
				list[idx] = *asset
				updated = true
				break
			}
		}
		if !updated {
			list = append(list, *asset)
		}
		card.Assets.Data = list
		models.DB.Save(card)
		return fmt.Sprintf("%s 更新资产:%s(%s)", card.Name, asset.Name, asset.Category)
	}
	return fmt.Sprintf("资产操作失败:未找到角色 %s", characterName)
}

func appendUniqueString(list []string, value string) []string {
	for _, item := range list {
		if item == value {
			return list
		}
	}
	return append(list, value)
}

func removeString(list []string, value string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item == value {
			continue
		}
		out = append(out, item)
	}
	return out
}
