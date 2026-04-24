package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

const MaxKpRound = 6

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
			return agentHandle{}, fmt.Errorf("agent %q 未配置，请在管理面板配置 LLM provider", role)
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

// RunAsync starts the agent pipeline in a goroutine and returns a channel that
// emits one RunResult containing the Writer's stream (or an error).
// Only one pipeline may run per session at a time; concurrent calls receive an error.
func RunAsync(ctx context.Context, gctx GameContext) <-chan RunResult {
	ch := make(chan RunResult, 1)
	go func() {
		if _, loaded := activeSessions.LoadOrStore(gctx.Session.ID, struct{}{}); loaded {
			ch <- RunResult{Err: fmt.Errorf("当前房间正在处理上一条消息，请稍候")}
			return
		}
		defer activeSessions.Delete(gctx.Session.ID)

		stream, err := run(ctx, gctx)
		ch <- RunResult{Stream: stream, Err: err}
	}()
	return ch
}

// run implements the master-slave agent loop.
//
// Architecture:
//   - Master (KP/Director): Has full scenario info. Outputs JSON arrays of tool calls.
//   - Slaves (Writer, Lawyer, Editor, NPC): No scenario info; provide specific services.
//
// The loop continues until the KP issues an "end" tool call or max iterations reached.
// Writer maintains conversation history across write calls for narrative continuity.
// At "end", the accumulated writer buffer is streamed to the player.
//
// Turn-action recording is handled by the caller (ChatStream handler) before run() is
// invoked, so run() does not call recordTurnAction itself.
func run(ctx context.Context, gctx GameContext) (<-chan string, error) {
	handles, err := batchLoadAgents()
	if err != nil {
		return nil, err
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
	writerState := &WriterState{}

	rbIdx := rulebook.GlobalIndex

	// timeAdvancedInTurn tracks whether advance_time was called so we can skip the
	// normal per-turn +1 advancement at the end (the KP already pushed the clock).
	timeAdvancedInTurn := false

	var kpMsgs []llm.ChatMessage
	// NOTE: load from DB

	// NOTE: build message chain
	kpMsgs = buildKPMessages(gctx, handles[models.AgentRoleDirector].systemPrompt(kpSystemPrompt), kpMsgs)

	for iter := 0; iter < MaxKpRound; iter++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		debugf("KP", "session=%d iter=%d/%d — calling LLM", sid, iter+1, MaxKpRound)

		// Dump kpMsgs for debugging
		for i, msg := range kpMsgs {
			debugf("KP_MSG", "session=%d iter=%d msg[%d] role=%q len=%d content=\n%s\n",
				sid, iter+1, i, msg.Role, len([]rune(msg.Content)), msg.Content)
		}

		doneKP := timedDebug("KP", "session=%d iter=%d Chat", sid, iter+1)
		calls, rawResp, err := runKP(ctx, handles[models.AgentRoleDirector], kpMsgs)
		doneKP()
		if err != nil {
			log.Printf("[agent] KP iter %d error: %v", iter+1, err)
			if iter == 0 {
				return nil, fmt.Errorf("KP agent failed: %w", err)
			}
			break
		}

		debugf("KP", "session=%d iter=%d → %d tool call(s): %s",
			sid, iter+1, len(calls), formatCallNames(calls))
		debugf("KP", "session=%d iter=%d raw_resp=%s",
			sid, iter+1, rawResp)

		// Record what the KP decided so the next iteration has proper context.
		kpMsgs = append(kpMsgs, llm.ChatMessage{Role: "assistant", Content: rawResp})

		var toolResults []ToolResult
		hasEnd := false

		for _, call := range calls {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			switch call.Action {

			case ToolCheckRule:
				// Lawyer slave: receives only a semantic question, no scenario context.
				debugf("tool", "session=%d check_rule q=%s", sid, call.Question)
				doneL := timedDebug("Lawyer", "session=%d question=%s", sid, call.Question)
				results := runLawyer(ctx, handles[models.AgentRoleLawyer], call.Question, rbIdx)
				doneL()
				toolResults = append(toolResults, ToolResult{
					Action: ToolCheckRule,
					Result: formatLawyerResults(results),
				})
				debugf("tool", "session=%d check_rule result=%s", sid, formatLawyerResults(results))

			case ToolRollDice:
				if call.Dice == nil {
					continue
				}
				debugf("tool", "session=%d roll_dice skill=%q val=%d char=%q type=%s bonus=%d penalty=%d",
					sid, call.Dice.Skill, call.Dice.Value, call.Dice.Character, call.Dice.CheckType, call.Dice.BonusDice, call.Dice.PenaltyDice)
				dcr := executeSingleDiceCheck(*call.Dice, gctx.Session.Players)
				debugf("tool", "session=%d roll_dice result=%s", sid, formatSingleDiceResult(dcr))
				// Auto-apply SAN loss immediately so character state stays consistent.
				if dcr.CheckType == "sanity" && dcr.SanLoss > 0 {
					who := dcr.Character
					if who == "" {
						who = "调查员"
					}
					applyStateChangesFallback(
						[]string{fmt.Sprintf("SAN -%d（%s）", dcr.SanLoss, who)},
						gctx.Session.Players,
					)
				}
				toolResults = append(toolResults, ToolResult{
					Action: ToolRollDice,
					Result: formatSingleDiceResult(dcr),
				})

			case ToolNPCAct:
				// NPC slave: receives NPC profile + context brief, no scenario text.
				debugf("tool", "session=%d npc_act npc=%q ctx=%s", sid, call.NPCName, call.NPCCtx)
				doneNPC := timedDebug("NPC", "session=%d npc=%s", sid, call.NPCName)
				action, npcErr := runNPC(ctx, handles[models.AgentRoleNPC], gctx, call.NPCName, call.NPCCtx, tempNPCs)
				doneNPC()
				if npcErr != nil {
					log.Printf("[agent] NPC %q error: %v", call.NPCName, npcErr)
				}
				debugf("tool", "session=%d npc_act result=%s", sid, formatNPCAction(action))
				toolResults = append(toolResults, ToolResult{
					Action: ToolNPCAct,
					Result: formatNPCAction(action),
				})

			case ToolUpdateCharacters:
				if len(call.Changes) == 0 {
					continue
				}
				debugf("tool", "session=%d update_characters changes=%v", sid, call.Changes)
				// Apply state changes directly: parse each string and dispatch to the
				// appropriate apply function without an LLM intermediary.
				for _, change := range call.Changes {
					upd, ok := parseStateChange(change)
					if !ok {
						continue
					}
					// Determine target: check players first, fall back to session NPCs.
					isPlayer := false
					for _, p := range gctx.Session.Players {
						if p.CharacterCard.Name == upd.CharacterName {
							isPlayer = true
							break
						}
					}
					if isPlayer {
						applyCharacterUpdate(upd, gctx.Session.Players)
					} else {
						upd.IsNPC = true
						applyNPCUpdate(upd, gctx.Session.ID, tempNPCs)
					}
				}
				toolResults = append(toolResults, ToolResult{
					Action: ToolUpdateCharacters,
					Result: "已更新：" + strings.Join(call.Changes, "、"),
				})

			case ToolTriggerMadness:
				// Roll on the appropriate COC 7th edition madness table.
				who := call.CharacterName
				if who == "" {
					who = "调查员"
				}
				debugf("tool", "session=%d trigger_madness char=%q bystander=%v", sid, who, call.IsBystander)
				symptom := game.RollMadnessSymptom(call.IsBystander)
				madnessType := "总结症状"
				if call.IsBystander {
					madnessType = "即时症状"
				}
				// 持久化疯狂状态到角色卡（不定性疯狂需要判断，临时性直接用即时路径）
				for i := range gctx.Session.Players {
					card := &gctx.Session.Players[i].CharacterCard
					if who != "调查员" && card.Name != who {
						continue
					}
					if card.MadnessState == "none" || card.MadnessState == "" {
						if call.IsBystander {
							card.MadnessState = "temporary"
						} else {
							// 独处时按不定性疯狂处理（总结症状）
							card.MadnessState = "indefinite"
						}
					}
					card.MadnessSymptom = symptom.Description
					card.MadnessDuration = 1
					models.DB.Save(card)
					break
				}
				toolResults = append(toolResults, ToolResult{
					Action: ToolTriggerMadness,
					Result: fmt.Sprintf("%s疯狂发作（%s，持续%s）：%s", who, madnessType, symptom.Duration, symptom.Description),
				})

			case ToolQueryClues:
				// Return scenario clues filtered by optional keyword.
				debugf("tool", "session=%d query_clues keyword=%q", sid, call.Keyword)
				clues := gctx.Session.Scenario.Content.Data.Clues
				keyword := strings.ToLower(call.Keyword)
				var matched []string
				for _, c := range clues {
					if keyword == "" || strings.Contains(strings.ToLower(c), keyword) {
						matched = append(matched, c)
					}
				}
				var clueResult string
				if len(matched) == 0 {
					clueResult = "（无匹配线索）"
				} else {
					clueResult = strings.Join(matched, "\n")
				}
				toolResults = append(toolResults, ToolResult{
					Action: ToolQueryClues,
					Result: fmt.Sprintf("线索查询结果（关键词：%q）：\n%s", call.Keyword, clueResult),
				})

			case ToolQueryCharacter:
				// Return full character card(s) for the requested investigator(s).
				debugf("tool", "session=%d query_character name=%q", sid, call.CharacterName)
				toolResults = append(toolResults, ToolResult{
					Action: ToolQueryCharacter,
					Result: buildCharacterDetail(call.CharacterName, gctx.Session.Players),
				})

			case ToolWrite:
				// Writer slave: no scenario info; receives direction + its own history.
				debugf("tool", "session=%d write direction=%s", sid, call.Direction)
				doneW := timedDebug("Writer", "session=%d direction=%s", sid, call.Direction)
				writeErr := appendWriter(ctx, handles[models.AgentRoleWriter], writerState, call.Direction, gctx)
				doneW()
				if writeErr != nil {
					log.Printf("[agent] writer error: %v", writeErr)
				}
				debugf("tool", "session=%d write buffer_len=%d", sid, len([]rune(writerState.Buffer)))

			case ToolAdvanceTime:
				rounds := call.TimeRounds
				if rounds <= 0 {
					rounds = 1
				}
				newRound := gctx.Session.TurnRound + rounds
				models.DB.Model(&models.GameSession{}).
					Where("id = ?", gctx.Session.ID).
					Update("turn_round", newRound)
				// Remove stale turn-action records for the skipped rounds.
				models.DB.Where("session_id = ? AND round < ?", gctx.Session.ID, newRound).
					Delete(&models.SessionTurnAction{})
				gctx.Session.TurnRound = newRound
				timeAdvancedInTurn = true
				reason := call.TimeReason
				if reason == "" {
					reason = "时间推进"
				}
				log.Printf("[agent] session %d advance_time +%d rounds (%s) → %s",
					gctx.Session.ID, rounds, reason, formatGameTime(newRound))
				toolResults = append(toolResults, ToolResult{
					Action: ToolAdvanceTime,
					Result: fmt.Sprintf("时间推进%d回合（%s），当前时间：%s", rounds, reason, formatGameTime(newRound)),
				})

			case ToolEnd:
				hasEnd = true
				debugf("tool", "session=%d end narration=%s", sid, call.Narration)
				// Append KP narration after writer text.
				if call.Narration != "" {
					if writerState.Buffer != "" {
						writerState.Buffer += "\n\n"
					}
					writerState.Buffer += call.Narration
				}
			}

			if hasEnd {
				break
			}
		}

		if hasEnd {
			if !timeAdvancedInTurn && checkTurnReady(gctx) {
				advanceTurnRound(gctx)
			}
			debugf("run", "session=%d completed iter=%d buffer_len=%d", sid, iter+1, len([]rune(writerState.Buffer)))
			return streamBuffer(writerState.Buffer), nil
		}

		// Feed tool results back as a user message so the next KP call has proper
		// multi-turn context (assistant decided → tools ran → user reports results).
		if len(toolResults) > 0 {
			var sb strings.Builder
			sb.WriteString("【工具执行结果】\n")
			for _, r := range toolResults {
				sb.WriteString(fmt.Sprintf("[%s] %s\n", r.Action, r.Result))
			}
			kpMsgs = append(kpMsgs, llm.ChatMessage{Role: "user", Content: sb.String()})
		}
	}

	// Max iterations reached — stream whatever Writer produced.
	if writerState.Buffer == "" {
		writerState.Buffer = "（KP思考中，请稍后重试。）"
	}
	return streamBuffer(writerState.Buffer), nil
}

// streamBuffer creates a channel that emits the text in small chunks,
// providing a natural streaming experience to the frontend.
func streamBuffer(text string) <-chan string {
	ch := make(chan string, 256)
	go func() {
		defer close(ch)
		runes := []rune(text)
		for i := 0; i < len(runes); {
			end := i + 4
			if end > len(runes) {
				end = len(runes)
			}
			ch <- string(runes[i:end])
			i = end
		}
	}()
	return ch
}

// formatSingleDiceResult formats a single dice result as a brief string for the KP.
func formatSingleDiceResult(r DiceCheckResult) string {
	who := r.Character
	if who == "" {
		who = "调查员"
	}
	hidden := ""
	if r.Hidden {
		hidden = "（暗骰）"
	}
	if r.Level == "seen" {
		return fmt.Sprintf("%s%s：%s", who, hidden, r.Message)
	}
	if r.CheckType == "sanity" {
		return fmt.Sprintf("%s理智检定%s：%s，骰值%d，SAN损失%d", who, hidden, r.Message, r.Roll, r.SanLoss)
	}
	return fmt.Sprintf("%s的%s检定%s：%s（骰值%d/%d）", who, r.Skill, hidden, r.Message, r.Roll, r.Value)
}

// formatNPCAction formats an NPCAction as a brief string for the KP.
func formatNPCAction(a NPCAction) string {
	result := a.NPCName + "：" + a.Action
	if a.Dialogue != "" {
		result += fmt.Sprintf("（对话：\"%s\"）", a.Dialogue)
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

// recordTurnAction inserts a SessionTurnAction row for the current player/round.
func recordTurnAction(gctx GameContext) {
	var userID uint
	for _, p := range gctx.Session.Players {
		if p.User.Username == gctx.UserName {
			userID = p.UserID
			break
		}
	}
	if userID == 0 {
		return // session creator / spectator – don't track
	}
	// Upsert: one record per (session, round, user).
	var existing models.SessionTurnAction
	result := models.DB.Where(
		"session_id = ? AND round = ? AND user_id = ?",
		gctx.Session.ID, gctx.Session.TurnRound, userID,
	).First(&existing)
	if result.Error != nil {
		action := models.SessionTurnAction{
			SessionID:     gctx.Session.ID,
			Round:         gctx.Session.TurnRound,
			UserID:        userID,
			Username:      gctx.UserName,
			ActionSummary: truncate(gctx.UserInput, 200),
		}
		models.DB.Create(&action)
	}
}

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
// Each round = 30 minutes; 48 rounds = 1 day (00:00–23:30).
// Round 1 = Day 1 00:00, Round 2 = Day 1 00:30, ..., Round 49 = Day 2 00:00.
func formatGameTime(round int) string {
	if round <= 0 {
		round = 1
	}
	zi := round - 1 // zero-indexed
	day := zi/48 + 1
	slot := zi % 48
	hour := slot / 2
	min := (slot % 2) * 30
	return fmt.Sprintf("第%d天 %02d:%02d（今日第%d回合）", day, hour, min, slot+1)
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "…"
	}
	return s
}

// applyStateChangesFallback is the simple regex-based stat applier used when
// the Editor agent fails.  It is intentionally identical to the former
// applyStateChanges in session.go but lives here so all stat-mutation logic is
// in one package.
func applyStateChangesFallback(changes []string, players []models.SessionPlayer) {
	for _, change := range changes {
		applySimpleStateChange(change, players)
	}
}

func applySimpleStateChange(change string, players []models.SessionPlayer) {
	// Matches "SAN -2（角色名）" or "HP +1（角色名）" etc.
	change = strings.TrimSpace(change)
	fields := map[string]struct{}{"SAN": {}, "HP": {}, "MP": {}}
	for stat := range fields {
		if !strings.HasPrefix(change, stat) {
			continue
		}
		// Extract delta and name via simple string parsing.
		rest := strings.TrimPrefix(change, stat)
		rest = strings.TrimSpace(rest)
		// rest = "-2（角色名）" or "-2"
		var deltaStr, charName string
		if idx := strings.Index(rest, "（"); idx >= 0 {
			deltaStr = strings.TrimSpace(rest[:idx])
			charName = strings.TrimSuffix(strings.TrimPrefix(rest[idx:], "（"), "）")
		} else {
			deltaStr = rest
		}
		var delta int
		fmt.Sscanf(deltaStr, "%d", &delta)
		for i := range players {
			card := &players[i].CharacterCard
			if charName != "" && card.Name != charName {
				continue
			}
			s := card.Stats.Data
			switch stat {
			case "SAN":
				prevSAN := s.SAN
				s.SAN = clamp(s.SAN+delta, 0, s.MaxSAN)
				card.Stats.Data = s
				// fallback路径下也需要追踪当日累计SAN损失，以正确触发不定性疯狂判定
				if delta < 0 {
					sanLoss := prevSAN - s.SAN
					if sanLoss > 0 {
						card.DailySanLoss += sanLoss
					}
				}
			case "HP":
				s.HP = clamp(s.HP+delta, 0, s.MaxHP)
				card.Stats.Data = s
			case "MP":
				s.MP = clamp(s.MP+delta, 0, s.MaxMP)
				card.Stats.Data = s
			}
			models.DB.Save(card)
			break
		}
		return
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
		skillVal := dc.Value

		// Look up actual skill / stat value from character card (index-based for mutation).
		var card *models.CharacterCard
		for pi := range players {
			if dc.Character == "" || players[pi].CharacterCard.Name == dc.Character {
				card = &players[pi].CharacterCard
				break
			}
		}
		if card != nil {
			if dc.CheckType == "sanity" {
				if card.Stats.Data.SAN > 0 {
					skillVal = card.Stats.Data.SAN
				}
			} else if v, ok := card.Skills.Data[dc.Skill]; ok && v > 0 {
				skillVal = v
			}
		}
		if skillVal <= 0 {
			skillVal = 50
		}

		// 已见过的神话存在不再握发SAN损失（COC第八章习惯恐惧规则）
		if dc.CheckType == "sanity" && dc.MonsterName != "" && card != nil {
			for _, seen := range card.SeenMonsters.Data {
				if seen == dc.MonsterName {
					results = append(results, DiceCheckResult{
						DiceCheck: dc,
						Level:     "seen",
						Success:   true,
						Message:   fmt.Sprintf("已见过「%s」，不再损失理智值", dc.MonsterName),
					})
					goto nextCheck
				}
			}
			// 初次遇到——记录到已见列表，后续检定将自动跳过
			card.SeenMonsters.Data = append(card.SeenMonsters.Data, dc.MonsterName)
			models.DB.Save(card)
		}

		{
			res := game.SkillCheck(skillVal)
			dcr := DiceCheckResult{
				DiceCheck: dc,
				Roll:      res.Value,
				Level:     res.Level,
				Success:   res.Success,
				Message:   res.Message,
			}

			// For sanity checks, also roll the SAN loss based on success/failure.
			if dc.CheckType == "sanity" {
				lossExpr := dc.SanFailLoss
				if res.Success {
					lossExpr = dc.SanSuccessLoss
				}
				if lossExpr == "" {
					if res.Success {
						lossExpr = "0"
					} else {
						lossExpr = "1D6" // sensible fallback
					}
				}
				dcr.SanLoss = game.RollDiceExpr(lossExpr)
				if dcr.SanLoss < 0 {
					dcr.SanLoss = 0
				}
				outcomeWord := "成功"
				if !res.Success {
					outcomeWord = "失败"
				}
				dcr.Message = fmt.Sprintf("%s，%s（SAN损失 %d 点）", res.Message, outcomeWord, dcr.SanLoss)
			}

			results = append(results, dcr)
		}
	nextCheck:
	}
	return results
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func buildPlayerStatus(players []models.SessionPlayer) string {
	if len(players) == 0 {
		return ""
	}
	s := "调查员状态："
	for _, p := range players {
		card := p.CharacterCard
		line := fmt.Sprintf("\n• %s（%s）HP:%d/%d SAN:%d/%d",
			card.Name, card.Occupation,
			card.Stats.Data.HP, card.Stats.Data.MaxHP,
			card.Stats.Data.SAN, card.Stats.Data.MaxSAN)

		// 疯狂状态标注
		switch card.MadnessState {
		case "temporary":
			line += "【临时性疯狂：" + card.MadnessSymptom + "】"
		case "indefinite":
			line += "【不定性疯狂（任何SAN损失将再次触发发作）：" + card.MadnessSymptom + "】"
		case "permanent":
			line += "【永久性疯狂：" + card.MadnessSymptom + "】"
		}

		// 伤亡状态标注
		switch card.WoundState {
		case "major":
			line += "【重伤】"
		case "dying":
			line += "【濒死（需急救）】"
		case "dead":
			line += "【已死亡】"
		}
		if card.IsUnconscious && card.WoundState != "dead" {
			line += "【昏迷】"
		}

		// 克苏鲁神话技能
		if card.CthulhuMythosSkill > 0 {
			line += fmt.Sprintf("（克苏鲁神话技能:%d，最大SAN上限:%d）", card.CthulhuMythosSkill, 99-card.CthulhuMythosSkill)
		}
		if len(card.SeenMonsters.Data) > 0 {
			line += fmt.Sprintf("（已见神话存在：%s）", strings.Join(card.SeenMonsters.Data, "、"))
		}
		s += line
	}
	return s
}

// buildCharacterDetail returns a detailed character card dump for the given character name.
// If characterName is empty, all players' cards are returned.
func buildCharacterDetail(characterName string, players []models.SessionPlayer) string {
	if len(players) == 0 {
		return "（当前无调查员）"
	}
	var sb strings.Builder
	for _, p := range players {
		card := p.CharacterCard
		if characterName != "" && card.Name != characterName {
			continue
		}
		st := card.Stats.Data
		sb.WriteString(fmt.Sprintf("=== %s（%s，%d岁，%s）===\n", card.Name, card.Occupation, card.Age, card.Gender))
		sb.WriteString(fmt.Sprintf("基础属性：STR%d CON%d SIZ%d DEX%d APP%d INT%d POW%d EDU%d\n",
			st.STR, st.CON, st.SIZ, st.DEX, st.APP, st.INT, st.POW, st.EDU))
		sb.WriteString(fmt.Sprintf("衍生属性：HP%d/%d MP%d/%d SAN%d/%d Luck%d MOV%d Build%d DB%s\n",
			st.HP, st.MaxHP, st.MP, st.MaxMP, st.SAN, st.MaxSAN, st.Luck, st.MOV, st.Build, st.DB))
		if card.CthulhuMythosSkill > 0 {
			sb.WriteString(fmt.Sprintf("克苏鲁神话技能：%d（最大SAN上限：%d）\n", card.CthulhuMythosSkill, 99-card.CthulhuMythosSkill))
		}
		if skills := card.Skills.Data; len(skills) > 0 {
			sb.WriteString("技能：")
			i := 0
			for name, val := range skills {
				if i > 0 {
					sb.WriteString("  ")
				}
				sb.WriteString(fmt.Sprintf("%s:%d", name, val))
				i++
			}
			sb.WriteString("\n")
		}
		if card.Backstory != "" {
			sb.WriteString("背景：" + card.Backstory + "\n")
		}
		if card.Traits != "" {
			sb.WriteString("特征：" + card.Traits + "\n")
		}
		if rels := card.SocialRelations.Data; len(rels) > 0 {
			sb.WriteString("社会关系：")
			for _, r := range rels {
				sb.WriteString(fmt.Sprintf("%s（%s）", r.Name, r.Relationship))
				if r.Note != "" {
					sb.WriteString("——" + r.Note)
				}
				sb.WriteString("  ")
			}
			sb.WriteString("\n")
		}
		if spells := card.Spells.Data; len(spells) > 0 {
			sb.WriteString("已知法术：" + strings.Join(spells, "、") + "\n")
		}
		if monsters := card.SeenMonsters.Data; len(monsters) > 0 {
			sb.WriteString("已见神话存在：" + strings.Join(monsters, "、") + "\n")
		}
		if card.MadnessState != "" && card.MadnessState != "none" {
			sb.WriteString(fmt.Sprintf("疯狂状态：%s——%s\n", card.MadnessState, card.MadnessSymptom))
		}
	}
	if sb.Len() == 0 {
		return fmt.Sprintf("（未找到角色：%s）", characterName)
	}
	return sb.String()
}

// buildPlayerBrief returns a minimal one-line summary of all players for the KP context.
// It only shows critical combat stats (HP/SAN) and special states.
// Full character details are available on-demand via the query_character tool.
func buildPlayerBrief(players []models.SessionPlayer) string {
	if len(players) == 0 {
		return ""
	}
	s := "【调查员概况（完整人物卡请用 query_character 获取）】"
	for _, p := range players {
		card := p.CharacterCard
		line := fmt.Sprintf("\n• %s（%s）HP:%d/%d SAN:%d/%d",
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

func buildHistorySummary(history []models.Message) string {
	var s string
	for _, m := range history {
		role := "KP"
		if m.Role == models.MessageRoleUser {
			role = m.Username
			if role == "" {
				role = "玩家"
			}
		}
		s += fmt.Sprintf("[%s]: %s\n", role, m.Content)
	}
	return s
}

// tailMessages returns the last n messages; returns all if len ≤ n.
func tailMessages(msgs []models.Message, n int) []models.Message {
	if len(msgs) <= n {
		return msgs
	}
	return msgs[len(msgs)-n:]
}

// buildLawyerSituation creates a concise situation description for a rule query.
func buildLawyerSituation(gctx GameContext, query string) string {
	return fmt.Sprintf("当前游戏情境：\n玩家行动：%s\n规则查询：%s", gctx.UserInput, query)
}

func formatDiceResults(results []DiceCheckResult) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		parts = append(parts, formatSingleDiceResult(r))
	}
	return strings.Join(parts, "；")
}
