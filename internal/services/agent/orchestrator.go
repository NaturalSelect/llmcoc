// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

const MaxKpRound = 10

// activeSessions prevents concurrent agent runs for the same game session.
var activeSessions sync.Map

// agentHandle pairs a Provider with its optional DB config.
type agentHandle struct {
	provider llm.Provider
	config   *models.AgentConfig
}

// systemPrompt returns the configured prompt or the given default.
func (h agentHandle) systemPrompt(defaultPrompt string) string {
	if h.config != nil && h.config.SystemPrompt != "" {
		return h.config.SystemPrompt
	}
	return defaultPrompt
}

// batchLoadAgents fetches all active AgentConfigs in a single DB query.
// Returns an error if any required role lacks an active provider config.
func batchLoadAgents() (map[models.AgentRole]agentHandle, error) {
	var configs []models.AgentConfig
	models.DB.Preload("ProviderConfig").
		Where("is_active = ?", true).
		Find(&configs)

	index := make(map[models.AgentRole]*models.AgentConfig, len(configs))
	for i := range configs {
		index[configs[i].Role] = &configs[i]
	}

	makeHandle := func(role models.AgentRole) (agentHandle, error) {
		cfg, ok := index[role]
		if !ok {
			return agentHandle{}, fmt.Errorf("agent %q 未配置,请在管理面板配置 LLM provider", role)
		}
		if cfg.ProviderConfigID == nil || cfg.ProviderConfig == nil || !cfg.ProviderConfig.IsActive {
			return agentHandle{}, fmt.Errorf("agent %q 未绑定可用的 LLM provider", role)
		}
		maxTok := cfg.MaxTokens
		if maxTok == 0 {
			maxTok = 1024
		}
		p := llm.NewProviderFromConfig(cfg.ProviderConfig, cfg.ModelName, maxTok, cfg.Temperature)
		return agentHandle{provider: p, config: cfg}, nil
	}

	roles := []models.AgentRole{
		models.AgentRoleDirector,
		models.AgentRoleWriter,
		models.AgentRoleLawyer,
		models.AgentRoleNPC,
	}
	result := make(map[models.AgentRole]agentHandle, len(roles))
	for _, role := range roles {
		h, err := makeHandle(role)
		if err != nil {
			return nil, err
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

// run implements the master-slave agent loop.
//
// Architecture:
//   - Master (KP/Director): Has full scenario info. Outputs JSON arrays of tool calls.
//   - Slaves (Writer, Lawyer, Editor, NPC): No scenario info; provide specific services.
//
// The loop continues until the KP issues an "response" tool call or max iterations reached.
// Writer maintains conversation history across write calls for narrative continuity.
// At "response", the accumulated writer buffer is returned to the caller.
//
// Turn-action recording is handled by the caller (ChatStream handler) before run() is
// invoked, so run() does not call recordTurnAction itself.
func run(ctx context.Context, gctx GameContext) (RunOutput, error) {
	handles, err := getCachedAgents(gctx.Session.ID)
	if err != nil {
		return RunOutput{}, err
	}

	sid := gctx.Session.ID
	debugf("run", "session=%d user=%q input=%s",
		sid, gctx.UserName, gctx.UserInput)
	if len(gctx.PendingActions) > 1 {
		debugf("run", "session=%d multi-player round: %d actions", sid, len(gctx.PendingActions))
	}

	// Load temp NPCs for this session.
	var tempNPCs []models.SessionNPC
	models.DB.Where("session_id = ?", gctx.Session.ID).Find(&tempNPCs)

	// Writer state accumulates narrative across multiple write calls.
	// Load persisted history from DB so the Writer has continuity across rounds.
	writerState := &WriterState{}
	if len(gctx.Session.WriterHistory.Data) > 0 {
		writerState.History = chatMsgsToLLM(gctx.Session.WriterHistory.Data)
	}

	rbIdx := rulebook.GlobalIndex

	// timeAdvancedInTurn tracks whether advance_time was called so we can skip the
	// normal per-turn +1 advancement at the end (the KP already pushed the clock).
	timeAdvancedInTurn := false
	var kpNarration string

	// Seed kpMsgs with the real conversation history from DB so the KP has
	// multi-turn context from previous rounds (not just the current action).
	kpMsgs := convertHistory(gctx.History)

	kpMsgs = buildKPMessages(gctx, handles[models.AgentRoleDirector].systemPrompt(kpSystemPrompt), kpMsgs, tempNPCs)

	switchRole := false

	for iter := 0; iter < MaxKpRound; iter++ {
		if ctx.Err() != nil {
			return RunOutput{}, ctx.Err()
		}

		debugf("KP", "session=%d iter=%d/%d — calling LLM", sid, iter+1, MaxKpRound)

		doneKP := timedDebug("KP", "session=%d iter=%d Chat", sid, iter+1)
		// 请求一次JSON
		calls, rawResp, err := runKP(context.Background(), handles[models.AgentRoleDirector], kpMsgs)
		doneKP()
		if err != nil {
			log.Printf("[agent] KP iter %d error: %v", iter+1, err)
			if iter == 0 {
				return RunOutput{}, fmt.Errorf("KP agent failed: %w", err)
			}
			break
		}

		debugf("KP", "session=%d iter=%d → %d tool call(s): %s",
			sid, iter+1, len(calls), formatCallNames(calls))
		debugf("KP", "session=%d iter=%d raw_resp=%s",
			sid, iter+1, rawResp)
		if len(calls) == 0 {
			continue
		}

		// LLM 的结果加回去
		// Record what the KP decided so the next iteration has proper context.
		truncStr := func(s string, max int) string {
			if len(s) <= max {
				return s
			}
			return fmt.Sprintf("%s...", s[:max])
		}
		compressRawResp := func(calls []ToolCall) string {
			tmp := append([]ToolCall{}, calls...)
			for i := range tmp {
				act := &tmp[i]
				act.Reply = truncStr(act.Reply, 10)
				act.Direction = truncStr(act.Direction, 10)
			}
			data, _ := json.Marshal(tmp)
			return string(data)
		}
		kpMsgs = append(kpMsgs, llm.ChatMessage{Role: "assistant", Content: compressRawResp(calls)})

		var toolResults []ToolResult
		hasEnd := false
		hasWrite := false
		interrupt := false

		actx := ActionContext{
			Ctx:                ctx,
			GCtx:               &gctx,
			Sid:                sid,
			Handles:            handles,
			TempNPCs:           &tempNPCs,
			Writer:             writerState,
			RbIdx:              rbIdx,
			HasEnd:             &hasEnd,
			HasWrite:           &hasWrite,
			TimeAdvancedInTurn: &timeAdvancedInTurn,
			SwitchRole:         &switchRole,
			KPNarration:        &kpNarration,
			Interrupt:          &interrupt,
		}

		switchInThisBatch := false
		for _, call := range calls {
			if ctx.Err() != nil {
				return RunOutput{}, ctx.Err()
			}
			if interrupt {
				continue
			}

			if switchRole {
				// 如果发生了切换跳过本批次其他调用,期望KP在下一轮使用 write/response 工具交出控制权。
				if switchInThisBatch || (call.Action != ToolWrite && call.Action != ToolResponse) {
					debugf("tool", "session=%d iter=%d switching KP role to Player for next calls", sid, iter+1)
					toolResults = append(toolResults, ToolResult{
						Action: call.Action,
						Result: "Interrupted: KP has switched control to Player, skipping this tool call. Please use write or response in next message to proceed.",
					})
					continue
				}
			}

			prevSwitch := switchRole
			if handler, ok := actionRegistry[call.Action]; ok {
				results := handler.Execute(call, actx)
				toolResults = append(toolResults, results...)
			}
			if !switchInThisBatch && switchRole && !prevSwitch {
				switchInThisBatch = true
			}

			if hasEnd {
				break
			}
		}

		if hasEnd {
			if !timeAdvancedInTurn {
				for i := range gctx.Session.Players {
					card := &gctx.Session.Players[i].CharacterCard
					if card.MadnessState == "none" || card.MadnessState == "" {
						continue
					}
					card.MadnessDuration -= 1
					if card.MadnessDuration <= 0 {
						card.MadnessState = "none"
					}
					models.DB.Save(card)
					break
				}
				if checkTurnReady(gctx) && hasWrite {
					advanceTurnRound(gctx)
				}
			}
			saveWriterHistory(gctx.Session.ID, writerState)
			debugf("run", "session=%d completed iter=%d writer_len=%d narration_len=%d",
				sid, iter+1, len([]rune(writerState.Buffer)), len([]rune(kpNarration)))
			return RunOutput{WriterText: writerState.Buffer, KPReply: kpNarration}, nil
		}

		// Feed tool results back as a user message so the next KP call has proper
		// multi-turn context (assistant decided → tools ran → user reports results).
		if len(toolResults) > 0 {
			var sb strings.Builder
			data, err := json.Marshal(toolResults)
			if err != nil {
				return RunOutput{}, fmt.Errorf("failed to marshal tool results: %w", err)
			}
			sb.Write(data)
			// 输入用户数据
			kpMsgs = append(kpMsgs, llm.ChatMessage{Role: "user", Content: sb.String()})
		}
		// 等一轮之后继续跑
		time.Sleep(20 * time.Second)
	}

	// Max iterations reached — return whatever Writer produced.
	if writerState.Buffer == "" {
		writerState.Buffer = "(KP思考中,请稍后重试。)"
	}
	saveWriterHistory(gctx.Session.ID, writerState)
	return RunOutput{WriterText: writerState.Buffer, KPReply: kpNarration}, nil
}

// saveWriterHistory persists the Writer's conversation history to the session
// so it carries over across rounds for narrative continuity.
func saveWriterHistory(sessionID uint, state *WriterState) {
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", sessionID).
		Update("writer_history", models.JSONField[[]models.ChatMsg]{
			Data: llmToChatMsgs(state.History),
		})
}

// ── Combat state helpers ──────────────────────────────────────────────────────

// saveCombatState persists the CombatState JSON column on GameSession.
// Pass nil to clear an ended combat.
func saveCombatState(sessionID uint, cs *models.CombatState) {
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", sessionID).
		Update("combat_state", models.JSONField[*models.CombatState]{Data: cs})
}

// buildCombatState initialises a new CombatState from KP-provided participant inputs.
// Participants are sorted by DEX descending (ties keep input order).
func buildCombatState(inputs []CombatParticipantInput) models.CombatState {
	parts := make([]models.CombatParticipant, len(inputs))
	for i, inp := range inputs {
		parts[i] = models.CombatParticipant{
			Name:       inp.Name,
			DEX:        inp.DEX,
			HP:         inp.HP,
			IsNPC:      inp.IsNPC,
			WoundState: "none",
		}
	}
	// Stable sort: higher DEX acts first.
	sort.SliceStable(parts, func(i, j int) bool {
		return parts[i].DEX > parts[j].DEX
	})
	return models.CombatState{
		Active:       true,
		Round:        1,
		Participants: parts,
		ActorIndex:   0,
	}
}

// applyCombatAct applies one combatant's action to the CombatState and advances
// the actor pointer. Returns a human-readable result string for the KP.
func applyCombatAct(cs *models.CombatState, call ToolCall) (result string, switchRole bool) {
	actorName := call.CombatActorName
	act := call.CombatAction

	// Find the actor in the participant list.
	actorIdx := -1
	for i, p := range cs.Participants {
		if p.Name == actorName {
			actorIdx = i
			break
		}
	}
	if actorIdx < 0 {
		return fmt.Sprintf("错误:找不到战斗参与者 %q", actorName), false
	}

	actor := &cs.Participants[actorIdx]
	actor.HasActed = true

	switchRole = false
	if next := (actorIdx + 1) % len(cs.Participants); next != actorIdx {
		// NOTE: 如果下一个行动者是NPC,则保持KP角色不变让它继续决策；如果是玩家,则切换到玩家角色让KP决策玩家行动。
		nextActor := cs.Participants[next]
		switchRole = !nextActor.IsNPC && actor.IsNPC
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("【%s 行动】", actorName))

	if act != nil {
		switch act.Type {
		case "aim":
			actor.IsAiming = true
			sb.WriteString("正在瞄准,下轮攻击获得奖励骰。")
		case "take_cover":
			debt := act.APDebtNext
			if debt <= 0 {
				debt = 1
			}
			actor.APDebt += debt
			sb.WriteString(fmt.Sprintf("寻找掩体,下轮行动点-1。"))
		case "dodge":
			actor.HasDodgedOrFB = true
			sb.WriteString(fmt.Sprintf("闪避 %s。", act.TargetName))
		case "fight_back":
			actor.HasDodgedOrFB = true
			sb.WriteString(fmt.Sprintf("反击 %s。", act.TargetName))
		case "attack":
			// Clear aiming bonus after use.
			if actor.IsAiming {
				actor.IsAiming = false
				sb.WriteString("(使用瞄准奖励骰)")
			}
			sb.WriteString(fmt.Sprintf("攻击 %s(武器:%s)。", act.TargetName, act.WeaponName))
		default:
			sb.WriteString(fmt.Sprintf("执行动作:%s。", act.Type))
		}
	}

	// Advance actor index; if all have acted, start a new round.
	allActed := true
	for _, p := range cs.Participants {
		if !p.HasActed && p.WoundState != "dead" {
			allActed = false
			break
		}
	}
	if allActed {
		cs.Round++
		// Reset per-round flags and apply AP debts.
		for i := range cs.Participants {
			cs.Participants[i].HasActed = false
			cs.Participants[i].HasDodgedOrFB = false
			if cs.Participants[i].APDebt > 0 {
				cs.Participants[i].APDebt-- // consume one point of debt
			}
		}
		cs.ActorIndex = 0
		sb.WriteString(fmt.Sprintf(" 本轮结束,进入第%d轮。", cs.Round))
	} else {
		// Advance to next living actor.
		next := (actorIdx + 1) % len(cs.Participants)
		for cs.Participants[next].HasActed || cs.Participants[next].WoundState == "dead" {
			next = (next + 1) % len(cs.Participants)
		}
		cs.ActorIndex = next
		sb.WriteString(fmt.Sprintf(" 下一行动者:%s(DEX %d)。", cs.Participants[next].Name, cs.Participants[next].DEX))
	}

	if switchRole {
		sb.WriteString(fmt.Sprintf(" 控制权从KP,移交到玩家,请使用 write/response 移交控制权"))
	}

	return sb.String(), switchRole
}

// combatOrderSummary returns a compact DEX-order string for the KP result message.
func combatOrderSummary(parts []models.CombatParticipant) string {
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = fmt.Sprintf("%s(DEX%d)", p.Name, p.DEX)
	}
	return strings.Join(names, " → ")
}

// ── Chase state helpers ───────────────────────────────────────────────────────

// saveChaseState persists the ChaseState JSON column on GameSession.
// Pass nil to clear an ended chase.
func saveChaseState(sessionID uint, chs *models.ChaseState) {
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", sessionID).
		Update("chase_state", models.JSONField[*models.ChaseState]{Data: chs})
}

// buildChaseState initialises a new ChaseState from KP-provided participant inputs.
// MinMOV is computed from the participant list.
func buildChaseState(inputs []ChaseParticipantInput) models.ChaseState {
	parts := make([]models.ChaseParticipant, len(inputs))
	minMOV := -1
	for i, inp := range inputs {
		parts[i] = models.ChaseParticipant{
			Name:      inp.Name,
			IsNPC:     inp.IsNPC,
			MOV:       inp.MOV,
			Location:  inp.Location,
			IsPursuer: inp.IsPursuer,
		}
		if minMOV < 0 || inp.MOV < minMOV {
			minMOV = inp.MOV
		}
	}
	if minMOV < 0 {
		minMOV = 0
	}
	return models.ChaseState{
		Active:       true,
		Round:        1,
		MinMOV:       minMOV,
		Participants: parts,
		Obstacles:    nil,
	}
}

// applyChaseAct applies one participant's chase action and returns a result string.
func applyChaseAct(chs *models.ChaseState, call ToolCall) (result string, switchRole bool) {
	actorName := call.ChaseActorName
	act := call.ChaseAction

	actorIdx := -1
	for i, p := range chs.Participants {
		if p.Name == actorName {
			actorIdx = i
			break
		}
	}
	if actorIdx < 0 {
		return fmt.Sprintf("错误:找不到追逐参与者 %q", actorName), false
	}

	actor := &chs.Participants[actorIdx]
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("【%s 追逐行动】", actorName))

	if act == nil {
		return sb.String() + "(无行动详情)", false
	}

	if next := (actorIdx + 1) % len(chs.Participants); next != actorIdx {
		switchRole = !chs.Participants[next].IsNPC && actor.IsNPC
	}

	switch act.Type {
	case "move":
		actor.Location += act.MoveDelta
		sb.WriteString(fmt.Sprintf("移动%+d格,当前位置:%d。", act.MoveDelta, actor.Location))
	case "hazard":
		if act.APDebtNext > 0 {
			actor.APDebt += act.APDebtNext
			sb.WriteString(fmt.Sprintf("险境检定失败,下轮行动点-%d。", act.APDebtNext))
		} else {
			sb.WriteString("险境检定成功,正常通过。")
		}
	case "obstacle":
		// Create or update an obstacle.
		if act.ObstacleName != "" && act.ObstacleMaxHP > 0 {
			found := false
			for i, ob := range chs.Obstacles {
				if ob.Name == act.ObstacleName {
					chs.Obstacles[i].HP = act.ObstacleHP
					found = true
					sb.WriteString(fmt.Sprintf("障碍【%s】HP更新为%d/%d。", act.ObstacleName, act.ObstacleHP, ob.MaxHP))
					break
				}
			}
			if !found {
				chs.Obstacles = append(chs.Obstacles, models.ChaseObstacle{
					Name:  act.ObstacleName,
					HP:    act.ObstacleHP,
					MaxHP: act.ObstacleMaxHP,
				})
				sb.WriteString(fmt.Sprintf("新增障碍【%s】HP=%d/%d。", act.ObstacleName, act.ObstacleHP, act.ObstacleMaxHP))
			}
		}
	case "conflict":
		sb.WriteString(fmt.Sprintf("与%s发生冲突。", act.TargetName))
	default:
		sb.WriteString(fmt.Sprintf("执行追逐动作:%s。", act.Type))
	}

	// Check for chase-end conditions: pursuer reaches same location as prey.
	for _, p := range chs.Participants {
		if p.IsPursuer {
			for _, q := range chs.Participants {
				if !q.IsPursuer && p.Location >= q.Location {
					sb.WriteString(fmt.Sprintf(" ⚠ 追逐者%s已追上%s(位置%d≥%d),KP可宣告追逐结束。",
						p.Name, q.Name, p.Location, q.Location))
				}
			}
		}
	}

	if switchRole {
		sb.WriteString(fmt.Sprintf(" 控制权从KP,移交到玩家,请使用 write/response 移交控制权"))
	}

	return sb.String(), switchRole
}

// chaseParticipantSummary returns a compact participant list string.
func chaseParticipantSummary(parts []models.ChaseParticipant) string {
	names := make([]string, len(parts))
	for i, p := range parts {
		role := "猎物"
		if p.IsPursuer {
			role = "追逐者"
		}
		names[i] = fmt.Sprintf("%s(%s,MOV%d,位置%d)", p.Name, role, p.MOV, p.Location)
	}
	return strings.Join(names, "、")
}

// chaseAPSummary returns each participant's action points for the round.
func chaseAPSummary(parts []models.ChaseParticipant, minMOV int) string {
	items := make([]string, len(parts))
	for i, p := range parts {
		ap := 1 + (p.MOV - minMOV)
		if ap < 1 {
			ap = 1
		}
		items[i] = fmt.Sprintf("%s=%d", p.Name, ap)
	}
	return strings.Join(items, "、")
}

func formatSingleDiceResult(r DiceCheckResult) string {
	who := r.Character
	if who == "" {
		who = "调查员"
	}
	hidden := ""
	if r.Hidden {
		hidden = "(暗骰)"
	}
	return fmt.Sprintf("%s的%v鉴定: %v %v", who, r.What, r.Roll, hidden)
}

// formatNPCAction formats an NPCAction as a brief string for the KP.
func formatNPCAction(a NPCAction) string {
	result := a.NPCName + ":" + a.Action
	if a.Dialogue != "" {
		result += fmt.Sprintf("(对话:\"%s\")", a.Dialogue)
	}
	return result
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

// checkTurnReady returns true when every player in the session has a
// SessionTurnAction record for the current round.
func checkTurnReady(gctx GameContext) bool {
	if len(gctx.Session.Players) == 0 {
		return false
	}
	var count int64
	models.DB.Model(&models.SessionTurnAction{}).
		Where("session_id = ? AND round = ?", gctx.Session.ID, gctx.Session.TurnRound).
		Count(&count)
	return count >= int64(len(gctx.Session.Players))
}

// advanceTurnRound increments TurnRound and deletes turn action records for the
// completed round.
func advanceTurnRound(gctx GameContext) {
	nextRound := gctx.Session.TurnRound + 1
	models.DB.Model(&models.GameSession{}).Where("id = ?", gctx.Session.ID).Update("turn_round", nextRound)
	models.DB.Where("session_id = ? AND round = ?", gctx.Session.ID, gctx.Session.TurnRound).Delete(&models.SessionTurnAction{})
	log.Printf("[agent] session %d advanced to round %d", gctx.Session.ID, nextRound)
}

// formatCallNames returns a comma-joined list of action names for debug logging.
func formatCallNames(calls []ToolCall) string {
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, string(c.Action))
	}
	return strings.Join(names, ", ")
}

// formatGameTime converts an absolute round number to a human-readable game time string.
// Each round = 30 minutes; 48 rounds = 1 day.
// startSlot is the scenario start slot in [0,47], where each slot is 30 minutes.
// Default start slot is 16 (08:00) when input is out of range.
// Also includes elapsed time since game start so the KP can track time-sensitive deadlines.
func formatGameTime(round int, startSlot int) string {
	if round <= 0 {
		round = 1
	}
	if startSlot < 0 || startSlot > 47 {
		startSlot = 16 // 08:00
	}
	zi := round - 1 // zero-indexed
	day := (zi+startSlot)/48 + 1
	slot := (zi + startSlot) % 48
	hour := slot / 2
	min := (slot % 2) * 30
	// Elapsed time since game start (round 1 = 0 elapsed).
	elapsedMins := zi * 30
	elapsedH := elapsedMins / 60
	elapsedM := elapsedMins % 60
	var elapsed string
	if elapsedH > 0 {
		elapsed = fmt.Sprintf("距开局已过%dh%02dm", elapsedH, elapsedM)
	} else {
		elapsed = fmt.Sprintf("距开局已过%dm", elapsedM)
	}
	return fmt.Sprintf("第%d天 %02d:%02d(%s)", day, hour, min, elapsed)
}

func scenarioStartSlot(session models.GameSession) int {
	return session.Scenario.Content.Data.GameStartSlot
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
		results = append(results, DiceCheckResult{
			DiceCheck: dc,
			Roll:      diceVal,
		})
	}
	return results
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
			line += fmt.Sprintf("(克苏鲁神话技能:%d,最大SAN上限:%d)", card.CthulhuMythosSkill, 99-card.CthulhuMythosSkill)
		}
		if len(card.SeenMonsters.Data) > 0 {
			line += fmt.Sprintf("(已见神话存在:%s)", strings.Join(card.SeenMonsters.Data, "、"))
		}
		s += line
	}
	return s
}

// buildCharacterDetail returns a detailed character card dump for the given character name.
// If characterName is empty, all players' cards are returned.
func buildCharacterDetail(characterName string, players []models.SessionPlayer) string {
	if len(players) == 0 {
		return "(当前无调查员)"
	}
	var sb strings.Builder
	for _, p := range players {
		card := p.CharacterCard
		if characterName != "" && card.Name != characterName {
			continue
		}
		st := card.Stats.Data
		sb.WriteString(fmt.Sprintf("=== %s(种族:%s,职业:%s,%d岁,性别:%s)===\n", card.Name, card.Race, card.Occupation, card.Age, card.Gender))
		if p.LLMNote != "" {
			sb.WriteString(fmt.Sprintf("⚠️ 当前状态(会话级备忘):%s\n", p.LLMNote))
		}
		sb.WriteString(fmt.Sprintf("基础属性:STR%d CON%d SIZ%d DEX%d APP%d INT%d POW%d EDU%d\n",
			st.STR, st.CON, st.SIZ, st.DEX, st.APP, st.INT, st.POW, st.EDU))
		sb.WriteString(fmt.Sprintf("衍生属性:HP%d/%d MP%d/%d SAN%d/%d Luck%d MOV%d Build%d DB%s\n",
			st.HP, st.MaxHP, st.MP, st.MaxMP, st.SAN, st.MaxSAN, st.Luck, st.MOV, st.Build, st.DB))
		if card.CthulhuMythosSkill > 0 {
			sb.WriteString(fmt.Sprintf("克苏鲁神话技能:%d(最大SAN上限:%d)\n", card.CthulhuMythosSkill, 99-card.CthulhuMythosSkill))
		}
		if skills := card.Skills.Data; len(skills) > 0 {
			sb.WriteString("技能:")
			i := 0
			for name, val := range skills {
				if i > 0 {
					sb.WriteString("  ")
				}
				sb.WriteString(fmt.Sprintf("%s:%d", name, val))
				i++
			}
			sb.WriteString("\n")
		} else {
			sb.WriteString("技能:无\n")
		}
		// if card.Backstory != "" {
		// 	sb.WriteString("背景:" + card.Backstory + "\n")
		// } else {
		// 	sb.WriteString("背景:无\n")
		// }
		// if card.Traits != "" {
		// 	sb.WriteString("特征:" + card.Traits + "\n")
		// } else {
		// 	sb.WriteString("特征:无\n")
		// }
		if items := card.Inventory.Data; len(items) > 0 {
			sb.WriteString("物品栏:" + strings.Join(items, "、") + "\n")
		} else {
			sb.WriteString("物品栏:无\n")
		}
		if rels := card.SocialRelations.Data; len(rels) > 0 {
			sb.WriteString("社会关系:")
			for _, r := range rels {
				sb.WriteString(fmt.Sprintf("%s(%s)", r.Name, r.Relationship))
				if r.Note != "" {
					sb.WriteString("——" + r.Note)
				}
				sb.WriteString("  ")
			}
			sb.WriteString("\n")
		} else {
			sb.WriteString("社会关系:无\n")
		}
		if spells := card.Spells.Data; len(spells) > 0 {
			sb.WriteString("掌握法术:" + strings.Join(spells, "、") + "\n")
		} else {
			sb.WriteString("掌握法术:无\n")
		}
		if monsters := card.SeenMonsters.Data; len(monsters) > 0 {
			sb.WriteString("已见神话存在:" + strings.Join(monsters, "、") + "\n")
		} else {
			sb.WriteString("已见神话存在:无\n")
		}
		if card.MadnessState != "" && card.MadnessState != "none" {
			sb.WriteString(fmt.Sprintf("疯狂状态:%s——%s\n", card.MadnessState, card.MadnessSymptom))
		}
	}
	if sb.Len() == 0 {
		return fmt.Sprintf("(未找到角色:%s)", characterName)
	}
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

func buildNPCDetail(npcName string, tempNPCs []models.SessionNPC, scenarioNPCs []models.NPCData) string {
	findStat := func(stats map[string]int, key string) (int, bool) {
		if stats == nil {
			return 0, false
		}
		if v, ok := stats[key]; ok {
			return v, true
		}
		if v, ok := stats[strings.ToUpper(key)]; ok {
			return v, true
		}
		if v, ok := stats[strings.ToLower(key)]; ok {
			return v, true
		}
		return 0, false
	}

	// Prefer session NPC cards (dynamic state, HP changes, alive/dead).
	var sb strings.Builder
	matched := false
	for _, npc := range tempNPCs {
		if !npcNameMatch(npc.Name, npcName) {
			continue
		}
		matched = true
		status := "存活"
		if !npc.IsAlive {
			status = "已死亡/失能"
		}
		sb.WriteString(fmt.Sprintf("=== %s(种族:%s,临时NPC,%s)===\n", npc.Name, npc.Race, status))
		sb.WriteString("描述:" + npc.Description + "\n")
		if strings.TrimSpace(npc.Attitude) != "" {
			sb.WriteString("态度:" + strings.TrimSpace(npc.Attitude) + "\n")
		}
		if strings.TrimSpace(npc.Goal) != "" {
			sb.WriteString("目标:" + strings.TrimSpace(npc.Goal) + "\n")
		}
		if strings.TrimSpace(npc.Secret) != "" {
			sb.WriteString("秘密:" + strings.TrimSpace(npc.Secret) + "\n")
		}
		if strings.TrimSpace(npc.RiskPref) != "" {
			sb.WriteString("风险偏好:" + strings.TrimSpace(npc.RiskPref) + "\n")
		}
		if npc.LLMNote != "" {
			sb.WriteString("当前备忘:" + npc.LLMNote + "\n")
		}
		if hp, ok := findStat(npc.Stats.Data, "HP"); ok {
			sb.WriteString(fmt.Sprintf("当前HP:%d\n", hp))
		}
		if san, ok := findStat(npc.Stats.Data, "SAN"); ok {
			sb.WriteString(fmt.Sprintf("SAN:%d\n", san))
		}
		if mp, ok := findStat(npc.Stats.Data, "MP"); ok {
			sb.WriteString(fmt.Sprintf("MP:%d\n", mp))
		}
		if len(npc.Stats.Data) > 0 {
			parts := make([]string, 0, len(npc.Stats.Data))
			for k, v := range npc.Stats.Data {
				parts = append(parts, fmt.Sprintf("%s:%d", k, v))
			}
			sb.WriteString("属性:" + strings.Join(parts, " ") + "\n")
		}
		if len(npc.Skills.Data) > 0 {
			parts := make([]string, 0, len(npc.Skills.Data))
			for k, v := range npc.Skills.Data {
				parts = append(parts, fmt.Sprintf("%s:%d", k, v))
			}
			sb.WriteString("技能:" + strings.Join(parts, " ") + "\n")
		}
		if len(npc.Spells.Data) > 0 {
			sb.WriteString("法术:" + strings.Join(npc.Spells.Data, "、") + "\n")
		} else {
			sb.WriteString("法术:无\n")
		}
	}
	if matched {
		return sb.String()
	}

	// Fallback: static scenario NPCs (read-only baseline from module).
	for _, npc := range scenarioNPCs {
		if !npcNameMatch(npc.Name, npcName) {
			continue
		}
		matched = true
		race := npc.Race
		if race == "" {
			race = "人类"
		}
		sb.WriteString(fmt.Sprintf("=== %s(%s,剧本静态NPC)===\n", npc.Name, race))
		sb.WriteString("描述:" + npc.Description + "\n")
		sb.WriteString("态度:" + npc.Attitude + "\n")
		if len(npc.Stats) > 0 {
			parts := make([]string, 0, len(npc.Stats))
			for k, v := range npc.Stats {
				parts = append(parts, fmt.Sprintf("%s:%d", k, v))
			}
			sb.WriteString("属性:" + strings.Join(parts, " ") + "\n")
		}
	}

	if matched {
		return sb.String()
	}
	if npcName == "" {
		return "(当前无可查询NPC)"
	}
	return fmt.Sprintf("(未找到NPC:%s)", npcName)
}

// buildPlayerBrief returns a minimal one-line summary of all players for the KP context.
// It only shows critical combat stats (HP/SAN) and special states.
// Full character details are available on-demand via the query_character tool.
func buildPlayerBrief(players []models.SessionPlayer) string {
	if len(players) == 0 {
		return ""
	}
	s := "【调查员概况(完整人物卡请用 query_character 获取)】"
	for _, p := range players {
		card := p.CharacterCard
		line := fmt.Sprintf("\n• %s(%s)HP:%d/%d SAN:%d/%d",
			card.Name, card.Occupation,
			card.Stats.Data.HP, card.Stats.Data.MaxHP,
			card.Stats.Data.SAN, card.Stats.Data.MaxSAN)
		switch card.MadnessState {
		case "temporary":
			line += "【临时性疯狂】"
		case "indefinite":
			line += "【不定性疯狂】"
		case "permanent":
			line += "【永久性疯狂】"
		}
		switch card.WoundState {
		case "major":
			line += "【重伤】"
		case "dying":
			line += "【濒死】"
		case "dead":
			line += "【已死亡】"
		}
		if card.IsUnconscious && card.WoundState != "dead" {
			line += "【昏迷】"
		}
		s += line
	}
	return s
}

// convertHistory maps []models.Message to []llm.ChatMessage, preserving the
// original multi-turn structure. User messages include the player name prefix
// so the model can distinguish speakers in multi-player sessions.
//
// Trailing user messages without a following assistant response are trimmed
// to ensure the KP never sees an incomplete conversation round (e.g. when a
// previous pipeline run failed after the user message was already persisted).
func convertHistory(history []models.Message) []llm.ChatMessage {
	msgs := make([]llm.ChatMessage, 0, len(history))
	for _, m := range history {
		switch m.Role {
		case models.MessageRoleUser:
			name := m.Username
			if name == "" {
				name = "玩家"
			}
			msgs = append(msgs, llm.ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("[%s]: %s", name, m.Content),
			})
		case models.MessageRoleAssistant:
			msgs = append(msgs, llm.ChatMessage{
				Role:    "assistant",
				Content: extraKPMessage(m.Content),
			})
			// Skip system messages — they are not part of the conversation context.
		}
	}

	// Trim trailing user messages that lack an assistant response.
	for len(msgs) > 0 && msgs[len(msgs)-1].Role == "user" {
		msgs = msgs[:len(msgs)-1]
	}
	return msgs
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

func manageInventory(players []models.SessionPlayer, characterName, operate, item string) string {
	if characterName == "" || item == "" {
		return "物品操作失败:缺少角色名或物品名"
	}
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != characterName {
			continue
		}
		list := card.Inventory.Data
		if operate == "remove" {
			list = removeString(list, item)
			card.Inventory.Data = list
			models.DB.Save(card)
			return fmt.Sprintf("%s 失去物品:%s", card.Name, item)
		}
		list = appendUniqueString(list, item)
		card.Inventory.Data = list
		models.DB.Save(card)
		return fmt.Sprintf("%s 获得物品:%s", card.Name, item)
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

func manageSocialRelation(players []models.SessionPlayer, characterName, operate string, rel *models.SocialRelation) string {
	if characterName == "" || rel == nil || rel.Name == "" {
		return "社会关系操作失败:缺少角色名或关系条目"
	}
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != characterName {
			continue
		}
		list := card.SocialRelations.Data
		if operate == "remove" {
			filtered := make([]models.SocialRelation, 0, len(list))
			for _, existing := range list {
				if existing.Name == rel.Name {
					continue
				}
				filtered = append(filtered, existing)
			}
			card.SocialRelations.Data = filtered
			models.DB.Save(card)
			return fmt.Sprintf("%s 移除社会关系:%s", card.Name, rel.Name)
		}

		updated := false
		for idx := range list {
			if list[idx].Name == rel.Name {
				list[idx] = *rel
				updated = true
				break
			}
		}
		if !updated {
			list = append(list, *rel)
		}
		card.SocialRelations.Data = list
		models.DB.Save(card)
		return fmt.Sprintf("%s 更新社会关系:%s(%s)", card.Name, rel.Name, rel.Relationship)
	}
	return fmt.Sprintf("社会关系操作失败:未找到角色 %s", characterName)
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
