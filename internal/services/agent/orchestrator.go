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
// Falls back to the global provider for any role without a config.
func batchLoadAgents() map[models.AgentRole]agentHandle {
	var configs []models.AgentConfig
	models.DB.Preload("ProviderConfig").
		Where("is_active = ?", true).
		Find(&configs)

	index := make(map[models.AgentRole]*models.AgentConfig, len(configs))
	for i := range configs {
		index[configs[i].Role] = &configs[i]
	}

	makeHandle := func(role models.AgentRole) agentHandle {
		cfg, ok := index[role]
		if !ok {
			return agentHandle{provider: llm.GetProvider()}
		}
		if cfg.ProviderConfigID != nil && cfg.ProviderConfig != nil && cfg.ProviderConfig.IsActive {
			maxTok := cfg.MaxTokens
			if maxTok == 0 {
				maxTok = 1024
			}
			p := llm.NewProviderFromConfig(cfg.ProviderConfig, cfg.ModelName, maxTok, cfg.Temperature)
			return agentHandle{provider: p, config: cfg}
		}
		return agentHandle{provider: llm.GetProvider(), config: cfg}
	}

	return map[models.AgentRole]agentHandle{
		models.AgentRoleDirector: makeHandle(models.AgentRoleDirector),
		models.AgentRoleWriter:   makeHandle(models.AgentRoleWriter),
		models.AgentRoleLawyer:   makeHandle(models.AgentRoleLawyer),
		models.AgentRoleEditor:   makeHandle(models.AgentRoleEditor),
		models.AgentRoleNPC:      makeHandle(models.AgentRoleNPC),
	}
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
func run(ctx context.Context, gctx GameContext) (<-chan string, error) {
	handles := batchLoadAgents()

	recordTurnAction(gctx)

	// Load temp NPCs for this session.
	var tempNPCs []models.SessionNPC
	models.DB.Where("session_id = ?", gctx.Session.ID).Find(&tempNPCs)

	// Writer state accumulates narrative across multiple write calls.
	writerState := &WriterState{}

	// Tool results accumulate within each iteration and are fed back to the KP.
	var toolResults []ToolResult
	rbIdx := rulebook.GlobalIndex

	// timeAdvancedInTurn tracks whether advance_time was called so we can skip the
	// normal per-turn +1 advancement at the end (the KP already pushed the clock).
	timeAdvancedInTurn := false

	const maxIter = 6
	for iter := 0; iter < maxIter; iter++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		calls, err := runKP(ctx, handles[models.AgentRoleDirector], gctx, toolResults)
		if err != nil {
			log.Printf("[agent] KP iter %d error: %v", iter+1, err)
			if iter == 0 {
				return nil, fmt.Errorf("KP agent failed: %w", err)
			}
			break
		}

		// Reset results for each new KP iteration.
		toolResults = nil
		hasEnd := false

		for _, call := range calls {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			switch call.Action {

			case ToolCheckRule:
				// Lawyer slave: receives only a semantic question, no scenario context.
				results := runLawyer(ctx, handles[models.AgentRoleLawyer], call.Question, rbIdx)
				toolResults = append(toolResults, ToolResult{
					Action: ToolCheckRule,
					Result: formatLawyerResults(results),
				})

			case ToolRollDice:
				if call.Dice == nil {
					continue
				}
				dcr := executeSingleDiceCheck(*call.Dice, gctx.Session.Players)
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
				action, npcErr := runNPC(ctx, handles[models.AgentRoleNPC], gctx, call.NPCName, call.NPCCtx, tempNPCs)
				if npcErr != nil {
					log.Printf("[agent] NPC %q error: %v", call.NPCName, npcErr)
				}
				toolResults = append(toolResults, ToolResult{
					Action: ToolNPCAct,
					Result: formatNPCAction(action),
				})

			case ToolUpdateCharacters:
				if len(call.Changes) == 0 {
					continue
				}
				// Editor slave: applies state changes, no scenario knowledge needed.
				editorResult, editorErr := runEditor(ctx, handles[models.AgentRoleEditor], call.Changes, gctx.Session.Players, tempNPCs)
				if editorErr != nil {
					log.Printf("[agent] editor error: %v — using fallback", editorErr)
					applyStateChangesFallback(call.Changes, gctx.Session.Players)
				} else {
					applyEditorResult(editorResult, gctx.Session.ID, gctx.Session.Players, tempNPCs)
					// Refresh tempNPCs in case Editor created new ones.
					models.DB.Where("session_id = ?", gctx.Session.ID).Find(&tempNPCs)
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

			case ToolWrite:
				// Writer slave: no scenario info; receives direction + its own history.
				writeErr := appendWriter(ctx, handles[models.AgentRoleWriter], writerState, call.Direction, gctx)
				if writeErr != nil {
					log.Printf("[agent] writer error: %v", writeErr)
				}
				// Writer output accumulates in writerState.Buffer; no feedback to KP needed.

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
			return streamBuffer(writerState.Buffer), nil
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

		// Look up actual skill / stat value from character card.
		for _, p := range players {
			if dc.Character == "" || p.CharacterCard.Name == dc.Character {
				card := p.CharacterCard
				if dc.CheckType == "sanity" {
					// Always use current SAN for sanity checks.
					if card.Stats.Data.SAN > 0 {
						skillVal = card.Stats.Data.SAN
					}
				} else if v, ok := card.Skills.Data[dc.Skill]; ok && v > 0 {
					skillVal = v
				}
				break
			}
		}
		if skillVal <= 0 {
			skillVal = 50
		}

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
