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

// Run executes the agent pipeline synchronously and returns the structured output.
// Only one pipeline may run per session at a time; concurrent calls receive an error.
func Run(ctx context.Context, gctx GameContext) (RunOutput, error) {
	if _, loaded := activeSessions.LoadOrStore(gctx.Session.ID, struct{}{}); loaded {
		return RunOutput{}, fmt.Errorf("当前房间正在处理上一条消息，请稍候")
	}
	defer activeSessions.Delete(gctx.Session.ID)

	return run(ctx, gctx)
}

// run implements the master-slave agent loop.
//
// Architecture:
//   - Master (KP/Director): Has full scenario info. Outputs JSON arrays of tool calls.
//   - Slaves (Writer, Lawyer, Editor, NPC): No scenario info; provide specific services.
//
// The loop continues until the KP issues an "answer" tool call or max iterations reached.
// Writer maintains conversation history across write calls for narrative continuity.
// At "answer", the accumulated writer buffer is returned to the caller.
//
// Turn-action recording is handled by the caller (ChatStream handler) before run() is
// invoked, so run() does not call recordTurnAction itself.
func run(ctx context.Context, gctx GameContext) (RunOutput, error) {
	handles, err := batchLoadAgents()
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

	for iter := 0; iter < MaxKpRound; iter++ {
		if ctx.Err() != nil {
			return RunOutput{}, ctx.Err()
		}

		debugf("KP", "session=%d iter=%d/%d — calling LLM", sid, iter+1, MaxKpRound)

		// Dump kpMsgs for debugging
		for i, msg := range kpMsgs {
			debugf("KP_MSG", "session=%d iter=%d msg[%d] role=%q len=%d content=\n%s\n",
				sid, iter+1, i, msg.Role, len([]rune(msg.Content)), msg.Content)
		}

		doneKP := timedDebug("KP", "session=%d iter=%d Chat", sid, iter+1)
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

		// Record what the KP decided so the next iteration has proper context.
		kpMsgs = append(kpMsgs, llm.ChatMessage{Role: "assistant", Content: rawResp})

		var toolResults []ToolResult
		hasEnd := false
		hasWrite := false

		for _, call := range calls {
			if ctx.Err() != nil {
				return RunOutput{}, ctx.Err()
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

			case ToolReadRulebookConst:
				debugf("tool", "session=%d read_rulebook_const constant=%q", sid, call.Constant)
				result := rulebook.ReadConstant(call.Constant)
				toolResults = append(toolResults, ToolResult{
					Action: ToolReadRulebookConst,
					Result: result,
				})

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
				// Record growth mark for successful standard skill checks (COC classic rule).
				recordGrowthMark(gctx.Session.ID, dcr)
				toolResults = append(toolResults, ToolResult{
					Action: ToolRollDice,
					Result: formatSingleDiceResult(dcr),
				})

			case ToolNPCAct:
				// Backward compatibility: map legacy npc_act to act_npc style.
				question := call.Question
				if question == "" {
					question = call.NPCCtx
				}
				debugf("tool", "session=%d npc_act npc=%q question=%s", sid, call.NPCName, question)
				doneNPC := timedDebug("NPC", "session=%d npc=%s", sid, call.NPCName)
				action, npcErr := actNPC(ctx, handles[models.AgentRoleNPC], gctx, call.NPCName, question, tempNPCs)
				doneNPC()
				if npcErr != nil {
					log.Printf("[agent] NPC %q error: %v", call.NPCName, npcErr)
				}
				debugf("tool", "session=%d npc_act result=%s", sid, formatNPCAction(action))
				toolResults = append(toolResults, ToolResult{
					Action: ToolNPCAct,
					Result: formatNPCAction(action),
				})

			case ToolCreateNPC:
				result := createNPC(gctx.Session.ID, call.CharCard)
				// Reload temp NPCs so later calls in this round can see the new NPC.
				tempNPCs = nil
				models.DB.Where("session_id = ?", gctx.Session.ID).Find(&tempNPCs)
				toolResults = append(toolResults, ToolResult{Action: ToolCreateNPC, Result: result})

			case ToolDestroyNPC, ToolDestoryNPC:
				result := destroyNPC(gctx.Session.ID, call.NPCName, call.DestroyReason)
				// Reload temp NPCs after destruction.
				tempNPCs = nil
				models.DB.Where("session_id = ?", gctx.Session.ID).Find(&tempNPCs)
				toolResults = append(toolResults, ToolResult{Action: call.Action, Result: result})

			case ToolActNPC:
				question := call.Question
				if question == "" {
					question = call.NPCCtx
				}
				debugf("tool", "session=%d act_npc npc=%q question=%s", sid, call.NPCName, question)
				doneNPC := timedDebug("NPC", "session=%d npc=%s", sid, call.NPCName)
				action, npcErr := actNPC(ctx, handles[models.AgentRoleNPC], gctx, call.NPCName, question, tempNPCs)
				doneNPC()
				if npcErr != nil {
					log.Printf("[agent] act_npc %q error: %v", call.NPCName, npcErr)
				}
				toolResults = append(toolResults, ToolResult{Action: ToolActNPC, Result: formatNPCAction(action)})

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
						applyNPCUpdate(upd, gctx.Session.ID, tempNPCs, gctx.Session.Scenario.Content.Data.NPCs)
					}
				}
				toolResults = append(toolResults, ToolResult{
					Action: ToolUpdateCharacters,
					Result: "已更新：" + strings.Join(call.Changes, "、"),
				})

			case ToolManageInventory:
				result := manageInventory(gctx.Session.Players, call.CharacterName, call.Operate, call.Item)
				toolResults = append(toolResults, ToolResult{Action: ToolManageInventory, Result: result})

			case ToolRecordMonster:
				result := manageSeenMonster(gctx.Session.Players, call.CharacterName, call.Operate, call.Monster)
				toolResults = append(toolResults, ToolResult{Action: ToolRecordMonster, Result: result})

			case ToolManageSpell:
				result := manageSpell(gctx.Session.Players, call.CharacterName, call.Operate, call.Spell)
				toolResults = append(toolResults, ToolResult{Action: ToolManageSpell, Result: result})

			case ToolManageRelation:
				result := manageSocialRelation(gctx.Session.Players, call.CharacterName, call.Operate, call.Relation)
				toolResults = append(toolResults, ToolResult{Action: ToolManageRelation, Result: result})

			case ToolEndGame:
				// End game immediately when KP decides the scenario has reached its conclusion.
				if call.EndSummary != "" {
					toolResults = append(toolResults, ToolResult{
						Action: ToolEndGame,
						Result: "剧本结局：" + call.EndSummary,
					})
				}
				models.DB.Model(&models.GameSession{}).
					Where("id = ?", gctx.Session.ID).
					Update("status", models.SessionStatusEnded)
				// Clear remaining round actions for safety; no further rounds after end_game.
				models.DB.Where("session_id = ?", gctx.Session.ID).Delete(&models.SessionTurnAction{})
				gctx.Session.Status = models.SessionStatusEnded
				hasEnd = true
				if call.Reply != "" {
					kpNarration = call.Reply
				} else if call.EndSummary != "" {
					kpNarration = "本次冒险结束。" + call.EndSummary
				} else {
					kpNarration = "本次冒险到此结束，感谢各位调查员。"
				}
				debugf("tool", "session=%d end_game summary=%s", sid, call.EndSummary)

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
				// Return full scenario clues.
				debugf("tool", "session=%d query_clues all", sid)
				clues := gctx.Session.Scenario.Content.Data.Clues
				var clueResult string
				if len(clues) == 0 {
					clueResult = "（无匹配线索）"
				} else {
					clueResult = strings.Join(clues, "\n")
				}
				toolResults = append(toolResults, ToolResult{
					Action: ToolQueryClues,
					Result: fmt.Sprintf("线索查询结果（全部）：\n%s", clueResult),
				})

			case ToolQueryCharacter:
				// Return full character card(s) for the requested investigator(s).
				debugf("tool", "session=%d query_character name=%q", sid, call.CharacterName)
				toolResults = append(toolResults, ToolResult{
					Action: ToolQueryCharacter,
					Result: buildCharacterDetail(call.CharacterName, gctx.Session.Players),
				})

			case ToolQueryNPCCard:
				debugf("tool", "session=%d query_npc_card npc=%q", sid, call.NPCName)
				toolResults = append(toolResults, ToolResult{
					Action: ToolQueryNPCCard,
					Result: buildNPCDetail(call.NPCName, tempNPCs, gctx.Session.Scenario.Content.Data.NPCs),
				})

			case ToolUpdateNPCCard:
				if call.NPCName == "" || len(call.Changes) == 0 {
					toolResults = append(toolResults, ToolResult{
						Action: ToolUpdateNPCCard,
						Result: "update_npc_card 参数不足：需要 npc_name 和 changes",
					})
					continue
				}
				debugf("tool", "session=%d update_npc_card npc=%q changes=%v", sid, call.NPCName, call.Changes)
				applied := make([]string, 0, len(call.Changes))
				for _, change := range call.Changes {
					upd, ok := parseStateChange(change)
					if !ok {
						continue
					}
					if upd.CharacterName == "" {
						upd.CharacterName = call.NPCName
					}
					upd.IsNPC = true
					applyNPCUpdate(upd, gctx.Session.ID, tempNPCs, gctx.Session.Scenario.Content.Data.NPCs)
					applied = append(applied, change)
				}
				tempNPCs = nil
				models.DB.Where("session_id = ?", gctx.Session.ID).Find(&tempNPCs)
				if len(applied) == 0 {
					toolResults = append(toolResults, ToolResult{Action: ToolUpdateNPCCard, Result: "未识别可应用的变更"})
				} else {
					toolResults = append(toolResults, ToolResult{Action: ToolUpdateNPCCard, Result: "已更新NPC：" + strings.Join(applied, "、")})
				}

			case ToolWrite:
				// Writer slave: no scenario info; receives direction + its own history.
				hasWrite = true
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
					gctx.Session.ID, rounds, reason, formatGameTime(newRound, scenarioStartSlot(gctx.Session)))
				toolResults = append(toolResults, ToolResult{
					Action: ToolAdvanceTime,
					Result: fmt.Sprintf("时间推进%d回合（%s），当前时间：%s", rounds, reason, formatGameTime(newRound, scenarioStartSlot(gctx.Session))),
				})

			case ToolAnswer:
				hasEnd = true
				kpNarration = call.Reply
				debugf("tool", "session=%d answer narration=%s", sid, call.Reply)
			}

			if hasEnd {
				break
			}
		}

		if hasEnd {
			if !timeAdvancedInTurn && checkTurnReady(gctx) && hasWrite {
				advanceTurnRound(gctx)
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
			sb.WriteString("【工具执行结果】\n")
			for _, r := range toolResults {
				sb.WriteString(fmt.Sprintf("[%s] %s\n", r.Action, r.Result))
			}
			kpMsgs = append(kpMsgs, llm.ChatMessage{Role: "user", Content: sb.String()})
		}
	}

	// Max iterations reached — return whatever Writer produced.
	if writerState.Buffer == "" {
		writerState.Buffer = "（KP思考中，请稍后重试。）"
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
	return fmt.Sprintf("第%d天 %02d:%02d（%s）", day, hour, min, elapsed)
}

func scenarioStartSlot(session models.GameSession) int {
	return session.Scenario.Content.Data.GameStartSlot
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
		} else {
			sb.WriteString("技能：无\n")
		}
		if card.Backstory != "" {
			sb.WriteString("背景：" + card.Backstory + "\n")
		} else {
			sb.WriteString("背景：无\n")
		}
		if card.Traits != "" {
			sb.WriteString("特征：" + card.Traits + "\n")
		} else {
			sb.WriteString("特征：无\n")
		}
		if items := card.Inventory.Data; len(items) > 0 {
			sb.WriteString("物品栏：" + strings.Join(items, "、") + "\n")
		} else {
			sb.WriteString("物品栏：无\n")
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
		} else {
			sb.WriteString("社会关系：无\n")
		}
		if spells := card.Spells.Data; len(spells) > 0 {
			sb.WriteString("掌握法术：" + strings.Join(spells, "、") + "\n")
		} else {
			sb.WriteString("掌握法术：无\n")
		}
		if monsters := card.SeenMonsters.Data; len(monsters) > 0 {
			sb.WriteString("已见神话存在：" + strings.Join(monsters, "、") + "\n")
		} else {
			sb.WriteString("已见神话存在：无\n")
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
		sb.WriteString(fmt.Sprintf("=== %s（临时NPC，%s）===\n", npc.Name, status))
		sb.WriteString("描述：" + npc.Description + "\n")
		if strings.TrimSpace(npc.Attitude) != "" {
			sb.WriteString("态度：" + strings.TrimSpace(npc.Attitude) + "\n")
		}
		if strings.TrimSpace(npc.Goal) != "" {
			sb.WriteString("目标：" + strings.TrimSpace(npc.Goal) + "\n")
		}
		if strings.TrimSpace(npc.Secret) != "" {
			sb.WriteString("秘密：" + strings.TrimSpace(npc.Secret) + "\n")
		}
		if strings.TrimSpace(npc.RiskPref) != "" {
			sb.WriteString("风险偏好：" + strings.TrimSpace(npc.RiskPref) + "\n")
		}
		if hp, ok := findStat(npc.Stats.Data, "HP"); ok {
			sb.WriteString(fmt.Sprintf("HP：%d\n", hp))
		}
		if san, ok := findStat(npc.Stats.Data, "SAN"); ok {
			sb.WriteString(fmt.Sprintf("SAN：%d\n", san))
		}
		if mp, ok := findStat(npc.Stats.Data, "MP"); ok {
			sb.WriteString(fmt.Sprintf("MP：%d\n", mp))
		}
		if len(npc.Stats.Data) > 0 {
			parts := make([]string, 0, len(npc.Stats.Data))
			for k, v := range npc.Stats.Data {
				parts = append(parts, fmt.Sprintf("%s:%d", k, v))
			}
			sb.WriteString("属性：" + strings.Join(parts, " ") + "\n")
		}
		if len(npc.Skills.Data) > 0 {
			parts := make([]string, 0, len(npc.Skills.Data))
			for k, v := range npc.Skills.Data {
				parts = append(parts, fmt.Sprintf("%s:%d", k, v))
			}
			sb.WriteString("技能：" + strings.Join(parts, " ") + "\n")
		}
		if len(npc.Spells.Data) > 0 {
			sb.WriteString("法术：" + strings.Join(npc.Spells.Data, "、") + "\n")
		} else {
			sb.WriteString("法术：无\n")
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
		sb.WriteString(fmt.Sprintf("=== %s（剧本静态NPC）===\n", npc.Name))
		sb.WriteString("描述：" + npc.Description + "\n")
		sb.WriteString("态度：" + npc.Attitude + "\n")
		if len(npc.Stats) > 0 {
			parts := make([]string, 0, len(npc.Stats))
			for k, v := range npc.Stats {
				parts = append(parts, fmt.Sprintf("%s:%d", k, v))
			}
			sb.WriteString("属性：" + strings.Join(parts, " ") + "\n")
		}
	}

	if matched {
		return sb.String()
	}
	if npcName == "" {
		return "（当前无可查询NPC）"
	}
	return fmt.Sprintf("（未找到NPC：%s）", npcName)
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
				Content: m.Content,
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
		return "物品操作失败：缺少角色名或物品名"
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
			return fmt.Sprintf("%s 失去物品：%s", card.Name, item)
		}
		list = appendUniqueString(list, item)
		card.Inventory.Data = list
		models.DB.Save(card)
		return fmt.Sprintf("%s 获得物品：%s", card.Name, item)
	}
	return fmt.Sprintf("物品操作失败：未找到角色 %s", characterName)
}

func manageSeenMonster(players []models.SessionPlayer, characterName, operate, monster string) string {
	if characterName == "" || monster == "" {
		return "神话存在记录失败：缺少角色名或神话存在名称"
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
			return fmt.Sprintf("%s 已移除神话存在记录：%s", card.Name, monster)
		}
		list = appendUniqueString(list, monster)
		card.SeenMonsters.Data = list
		models.DB.Save(card)
		return fmt.Sprintf("%s 已记录神话存在：%s", card.Name, monster)
	}
	return fmt.Sprintf("神话存在记录失败：未找到角色 %s", characterName)
}

func manageSpell(players []models.SessionPlayer, characterName, operate, spell string) string {
	if characterName == "" || spell == "" {
		return "法术操作失败：缺少角色名或法术名"
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
			return fmt.Sprintf("%s 遗失法术：%s", card.Name, spell)
		}
		list = appendUniqueString(list, spell)
		card.Spells.Data = list
		models.DB.Save(card)
		return fmt.Sprintf("%s 学会法术：%s", card.Name, spell)
	}
	return fmt.Sprintf("法术操作失败：未找到角色 %s", characterName)
}

func manageSocialRelation(players []models.SessionPlayer, characterName, operate string, rel *models.SocialRelation) string {
	if characterName == "" || rel == nil || rel.Name == "" {
		return "社会关系操作失败：缺少角色名或关系条目"
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
			return fmt.Sprintf("%s 移除社会关系：%s", card.Name, rel.Name)
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
		return fmt.Sprintf("%s 更新社会关系：%s（%s）", card.Name, rel.Name, rel.Relationship)
	}
	return fmt.Sprintf("社会关系操作失败：未找到角色 %s", characterName)
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

// skillsExcludedFromGrowth lists skills that never receive growth marks per COC rules.
var skillsExcludedFromGrowth = map[string]bool{
	"克苏鲁神话": true,
	"信用评级":  true,
}

// recordGrowthMark saves a growth mark if the check qualifies under COC rules:
// - must be a success
// - must be a standard skill check (not sanity / luck)
// - must not have used bonus dice
// - skill must not be on the exclusion list
// - character must be named
// Duplicate marks for the same session+character+skill are silently ignored.
func recordGrowthMark(sessionID uint, dcr DiceCheckResult) {
	if !dcr.Success {
		return
	}
	if dcr.CheckType == "sanity" || dcr.CheckType == "luck" {
		return
	}
	if dcr.BonusDice > 0 {
		return
	}
	skill := dcr.Skill
	charName := dcr.Character
	if skill == "" || charName == "" {
		return
	}
	if skillsExcludedFromGrowth[skill] {
		return
	}
	// Upsert: only insert if no mark exists yet for this session+character+skill.
	var existing models.SessionGrowthMark
	result := models.DB.Where("session_id = ? AND character_name = ? AND skill = ?",
		sessionID, charName, skill).First(&existing)
	if result.Error != nil {
		models.DB.Create(&models.SessionGrowthMark{
			SessionID:     sessionID,
			CharacterName: charName,
			Skill:         skill,
		})
	}
}
