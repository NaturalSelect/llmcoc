// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"encoding/xml"
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

	hasIntrospection := false

	// warnning := "YOU DONOT FOLLOW THE RULES, THIS ABUSE IS RECORDED BY MONITOR SYSTEM.\n"
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

		for _, call := range calls {
			if call.Action == ToolIntrospection {
				hasIntrospection = true
			}
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

		if !hasIntrospection {
			kpMsgs = append(kpMsgs, llm.ChatMessage{Role: "system", Content: "ERROR: NOT FOLLOW THE RULES, YOU MUST USE THE INTROSPECTION TOOL BEFORE YOUR ANY TOOL CALL."})
			iter--
			debugf("kp", "no follow rule session %v", gctx.Session.ID)
			continue
		}

		var toolResults []ToolResult
		hasEnd := false
		interrupt := false
		pendgWrite := ""

		actx := ActionContext{
			Ctx:                ctx,
			GCtx:               &gctx,
			Sid:                sid,
			Handles:            handles,
			TempNPCs:           &tempNPCs,
			Writer:             writerState,
			RbIdx:              rbIdx,
			HasEnd:             &hasEnd,
			TimeAdvancedInTurn: &timeAdvancedInTurn,
			SwitchRole:         &switchRole,
			KPNarration:        &kpNarration,
			Interrupt:          &interrupt,
			PendingWrite:       &pendgWrite,
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
				if switchInThisBatch || (call.Action != ToolWrite && call.Action != ToolResponse && call.Action != ToolEndGame && call.Action != ToolHint) {
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
				if len(results) > 0 {
					toolResults = append(toolResults, results...)
				}
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
						card.MadnessSymptom = ""
						card.MadnessDuration = 0
						debugf("madness", "session=%d char=%s madness ended", sid, card.Name)
					}
					models.DB.Save(card)
					break
				}
				if checkTurnReady(gctx) && pendgWrite != "" {
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
			formatResult := func(r []ToolResult) string {
				var sb strings.Builder
				xml := ""
				newTc := make([]ToolResult, 0, len(r))
				for _, tr := range r {
					if tr.Action == ToolQueryCharacter || tr.Action == ToolQueryNPCCard {
						xml += tr.Result + "\n"
						continue
					}
					newTc = append(newTc, tr)
				}
				data, err := json.Marshal(newTc)
				if err != nil {
					return fmt.Sprintf("ERROR: failed to marshal tool results: %v", err)
				}
				sb.Write(data)
				if xml != "" {
					sb.WriteString("\n")
					sb.WriteString(xml)
				}
				return sb.String()
			}
			// 输入用户数据
			kpMsgs = append(kpMsgs, llm.ChatMessage{Role: "user", Content: formatResult(toolResults)})
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
	hidden := ""
	if r.Hidden {
		hidden = "(暗骰)"
	}
	return fmt.Sprintf("%v鉴定: %v %v", r.What, r.Roll, hidden)
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

// 用于生成 XML 的临时结构体（不导出）
type characterXML struct {
	XMLName struct{} `xml:"character"`
	Name    string   `xml:"name"`
	Race    string   `xml:"race"`
	Job     string   `xml:"occupation"`
	Age     int      `xml:"age"`
	Gender  string   `xml:"gender"`
	LLMNote string   `xml:"llm_note,omitempty"`
	Stats   struct {
		STR int `xml:"str"`
		CON int `xml:"con"`
		SIZ int `xml:"siz"`
		DEX int `xml:"dex"`
		APP int `xml:"app"`
		INT int `xml:"int"`
		POW int `xml:"pow"`
		EDU int `xml:"edu"`
	} `xml:"base_stats"`
	Derived struct {
		HP     int    `xml:"hp"`
		MaxHP  int    `xml:"max_hp"`
		MP     int    `xml:"mp"`
		MaxMP  int    `xml:"max_mp"`
		SAN    int    `xml:"san"`
		MaxSAN int    `xml:"max_san"`
		Luck   int    `xml:"luck"`
		MOV    int    `xml:"mov"`
		Build  int    `xml:"build"`
		DB     string `xml:"damage_bonus"`
	} `xml:"derived_stats"`
	CthulhuMythos   int           `xml:"cthulhu_mythos,omitempty"`
	MythosMaxSAN    int           `xml:"mythos_max_san,omitempty"`
	Skills          []keyValue    `xml:"skills>skill,omitempty"`
	Inventory       []string      `xml:"inventory>item,omitempty"`
	SocialRelations []relationXML `xml:"social_relations>relation,omitempty"`
	Spells          []string      `xml:"spells>spell,omitempty"`
	SeenMonsters    []string      `xml:"seen_monsters>monster,omitempty"`
	MadnessState    string        `xml:"madness_state,omitempty"`
	MadnessSymptom  string        `xml:"madness_symptom,omitempty"`
}

type relationXML struct {
	Name         string `xml:"name"`
	Relationship string `xml:"relationship"`
	Note         string `xml:"note,omitempty"`
}

func buildCharacterDetail(characterName string, players []models.SessionPlayer) string {
	if len(players) == 0 {
		return `<result><error>当前无调查员</error></result>`
	}

	var characters []characterXML
	for _, p := range players {
		card := p.CharacterCard
		if characterName != "" && card.Name != characterName {
			continue
		}
		st := card.Stats.Data
		c := characterXML{
			Name:   card.Name,
			Race:   card.Race,
			Job:    card.Occupation,
			Age:    card.Age,
			Gender: card.Gender,
			Stats: struct {
				STR int `xml:"str"`
				CON int `xml:"con"`
				SIZ int `xml:"siz"`
				DEX int `xml:"dex"`
				APP int `xml:"app"`
				INT int `xml:"int"`
				POW int `xml:"pow"`
				EDU int `xml:"edu"`
			}{
				STR: st.STR, CON: st.CON, SIZ: st.SIZ, DEX: st.DEX,
				APP: st.APP, INT: st.INT, POW: st.POW, EDU: st.EDU,
			},
			Derived: struct {
				HP     int    `xml:"hp"`
				MaxHP  int    `xml:"max_hp"`
				MP     int    `xml:"mp"`
				MaxMP  int    `xml:"max_mp"`
				SAN    int    `xml:"san"`
				MaxSAN int    `xml:"max_san"`
				Luck   int    `xml:"luck"`
				MOV    int    `xml:"mov"`
				Build  int    `xml:"build"`
				DB     string `xml:"damage_bonus"`
			}{
				HP: st.HP, MaxHP: st.MaxHP, MP: st.MP, MaxMP: st.MaxMP,
				SAN: st.SAN, MaxSAN: st.MaxSAN, Luck: st.Luck, MOV: st.MOV,
				Build: st.Build, DB: st.DB,
			},
			Inventory:    card.Inventory.Data,
			Spells:       card.Spells.Data,
			SeenMonsters: card.SeenMonsters.Data,
		}
		if p.LLMNote != "" {
			c.LLMNote = p.LLMNote
		}
		if card.CthulhuMythosSkill > 0 {
			c.CthulhuMythos = card.CthulhuMythosSkill
			c.MythosMaxSAN = 99 - card.CthulhuMythosSkill
		}
		if len(card.Skills.Data) > 0 {
			for k, v := range card.Skills.Data {
				c.Skills = append(c.Skills, keyValue{Key: k, Value: v})
			}
		}
		if len(card.SocialRelations.Data) > 0 {
			rels := make([]relationXML, len(card.SocialRelations.Data))
			for i, r := range card.SocialRelations.Data {
				rels[i] = relationXML{
					Name:         r.Name,
					Relationship: r.Relationship,
					Note:         r.Note,
				}
			}
			c.SocialRelations = rels
		}
		if card.MadnessState != "" && card.MadnessState != "none" {
			c.MadnessState = card.MadnessState
			c.MadnessSymptom = card.MadnessSymptom
		}
		characters = append(characters, c)
	}

	if len(characters) == 0 {
		return `<result><error>未找到角色：` + characterName + `</error></result>`
	}

	// 序列化为 XML
	out, err := xml.MarshalIndent(struct {
		XMLName    xml.Name       `xml:"characters"`
		Characters []characterXML `xml:"character"`
	}{Characters: characters}, "", "  ")
	if err != nil {
		return `<result><error>ERROR : ` + err.Error() + `</error></result>`
	}
	return string(out)
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

// 辅助类型：用于序列化键值对
type keyValue struct {
	Key   string `xml:"key,attr"`
	Value int    `xml:"value"`
}

// XML 结构：NPC（临时或静态）
type npcXML struct {
	Name        string     `xml:"name"`
	Race        string     `xml:"race"`
	Type        string     `xml:"type"`             // "临时NPC" 或 "剧本静态NPC"
	Status      string     `xml:"status,omitempty"` // 存活/死亡（仅临时NPC）
	Description string     `xml:"description"`
	Attitude    string     `xml:"attitude,omitempty"`
	Goal        string     `xml:"goal,omitempty"`
	Secret      string     `xml:"secret,omitempty"`
	RiskPref    string     `xml:"risk_pref,omitempty"`
	LLMNote     string     `xml:"llm_note,omitempty"`
	Stats       []keyValue `xml:"stats>entry,omitempty"`
	Skills      []keyValue `xml:"skills>entry,omitempty"`
	Spells      []string   `xml:"spells>spell,omitempty"`
}

// 将 map[string]int 转换为 []keyValue
func mapToKV(m map[string]int) []keyValue {
	if len(m) == 0 {
		return nil
	}
	kv := make([]keyValue, 0, len(m))
	for k, v := range m {
		kv = append(kv, keyValue{Key: k, Value: v})
	}
	return kv
}

func buildNPCDetail(npcName string, tempNPCs []models.SessionNPC, scenarioNPCs []models.NPCData) string {

	var npcs []npcXML
	// 1. 优先搜索临时 NPC（动态状态）
	for _, npc := range tempNPCs {
		if !npcNameMatch(npc.Name, npcName) {
			continue
		}
		status := "存活"
		if !npc.IsAlive {
			status = "已死亡/失能"
		}

		n := npcXML{
			Name:        npc.Name,
			Race:        npc.Race,
			Type:        "临时NPC",
			Status:      status,
			Description: npc.Description,
			Attitude:    strings.TrimSpace(npc.Attitude),
			Goal:        strings.TrimSpace(npc.Goal),
			Secret:      strings.TrimSpace(npc.Secret),
			RiskPref:    strings.TrimSpace(npc.RiskPref),
			LLMNote:     npc.LLMNote,
			Stats:       mapToKV(npc.Stats.Data),
			Skills:      mapToKV(npc.Skills.Data),
			Spells:      npc.Spells.Data,
		}
		// 剔除空字符串字段（omitempty 会在序列化时处理，但为了结构干净也可保留）
		npcs = append(npcs, n)
	}

	// 如果临时 NPC 中没有匹配，则回退到剧本静态 NPC（只读基准信息）
	if len(npcs) == 0 {
		for _, npc := range scenarioNPCs {
			if !npcNameMatch(npc.Name, npcName) {
				continue
			}
			race := npc.Race
			if race == "" {
				race = "人类"
			}
			n := npcXML{
				Name:        npc.Name,
				Race:        race,
				Type:        "剧本静态NPC",
				Description: npc.Description,
				Attitude:    npc.Attitude,
				Stats:       mapToKV(npc.Stats),
				// 静态 NPC 没有技能、法术、动态状态
			}
			npcs = append(npcs, n)
		}
	}

	// 输出结果
	if len(npcs) == 0 {
		if npcName == "" {
			return `<result><error>当前无可查询NPC</error></result>`
		}
		return `<result><error>未找到NPC：` + npcName + `</error></result>`
	}

	// 序列化为 XML
	out, err := xml.MarshalIndent(struct {
		XMLName xml.Name `xml:"result"`
		NPCs    []npcXML `xml:"npcs>npc"`
	}{NPCs: npcs}, "", "  ")
	if err != nil {
		return `<result><error>ERROR : ` + err.Error() + `</error></result>`
	}
	return string(out)
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
// Consecutive user messages from the same round (multi-player) are merged into
// a single user message so the history always alternates user/assistant.
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
			line := fmt.Sprintf("[%s]: %s", name, m.Content)
			// Merge consecutive user messages into one (multi-player rounds).
			if len(msgs) > 0 && msgs[len(msgs)-1].Role == "user" {
				msgs[len(msgs)-1].Content += "\n" + line
			} else {
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: line})
			}
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
