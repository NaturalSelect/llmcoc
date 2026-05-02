// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// ActionContext carries the shared execution environment for every action handler.
type ActionContext struct {
	Ctx      context.Context
	GCtx     *GameContext
	Sid      uint
	Handles  map[models.AgentRole]agentHandle
	TempNPCs *[]models.SessionNPC
	Writer   *WriterState
	RbIdx    rulebook.Index

	// Mutable flags written by action handlers and read by the dispatch loop.
	HasEnd             *bool
	TimeAdvancedInTurn *bool
	SwitchRole         *bool
	KPNarration        *string
	PendingWrite       *string
	Interrupt          *bool
}

// Action is implemented by every tool call handler.
type Action interface {
	Execute(call ToolCall, actx ActionContext) []ToolResult
}

// noSideEffectActions is the set of tool call types that have no side effects
// (sideeffect: false in the KP system prompt). These are pure query/compute
// actions that must NOT be mixed with side-effect actions in the same phase.
var noSideEffectActions = map[ToolCallType]bool{
	ToolRollDice:          true,
	ToolCheckRule:         true,
	ToolReadRulebookConst: true,
	ToolQueryClues:        true,
	ToolQueryCharacter:    true,
	ToolQueryNPCCard:      true,
}

// actionRegistry maps each ToolCallType to its handler.
// Handlers not listed here produce no result and are silently skipped.
var actionRegistry = map[ToolCallType]Action{
	ToolCheckRule:         checkRuleAction{},
	ToolReadRulebookConst: readRulebookConstAction{},
	ToolRollDice:          rollDiceAction{},
	ToolNPCAct:            npcActAction{},
	ToolActNPC:            actNPCAction{},
	ToolCreateNPC:         createNPCAction{},
	ToolDestroyNPC:        destroyNPCAction{},
	ToolDestoryNPC:        destroyNPCAction{}, // compat alias
	ToolUpdateCharacters:  updateCharactersAction{},
	ToolManageInventory:   manageInventoryAction{},
	ToolRecordMonster:     recordMonsterAction{},
	ToolManageSpell:       manageSpellAction{},
	ToolManageRelation:    manageRelationAction{},
	ToolYield:             yieldAction{},
	ToolEndGame:           endGameAction{},
	ToolTriggerMadness:    triggerMadnessAction{},
	ToolQueryClues:        queryCluesAction{},
	ToolQueryCharacter:    queryCharacterAction{},
	ToolQueryNPCCard:      queryNPCCardAction{},
	ToolUpdateNPCCard:     updateNPCCardAction{},
	ToolWrite:             writeAction{},
	ToolAdvanceTime:       advanceTimeAction{},
	ToolUpdateLLMNote:     updateLLMNoteAction{},
	ToolUpdateNPCLLMNote:  updateNPCLLMNoteAction{},
	ToolResponse:          responseAction{},
	ToolStartCombat:       startCombatAction{},
	ToolCombatAct:         combatActAction{},
	ToolEndCombat:         endCombatAction{},
	ToolStartChase:        startChaseAction{},
	ToolChaseAct:          chaseActAction{},
	ToolEndChase:          endChaseAction{},
}

// ── Rule / lookup actions ─────────────────────────────────────────────────────

type checkRuleAction struct{}

func (checkRuleAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d check_rule q=%s", actx.Sid, call.Question)
	doneL := timedDebug("Lawyer", "session=%d question=%s", actx.Sid, call.Question)
	results := runLawyer(actx.Ctx, actx.Handles[models.AgentRoleLawyer], call.Question, actx.RbIdx)
	doneL()
	debugf("tool", "session=%d check_rule result=%s", actx.Sid, formatLawyerResults(results))
	return []ToolResult{{Action: ToolCheckRule, Result: formatLawyerResults(results)}}
}

type readRulebookConstAction struct{}

func (readRulebookConstAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d read_rulebook_const constant=%q", actx.Sid, call.Constant)
	result := rulebook.ReadConstant(call.Constant)
	return []ToolResult{{Action: ToolReadRulebookConst, Result: result}}
}

// ── Dice action ───────────────────────────────────────────────────────────────

type rollDiceAction struct{}

func (rollDiceAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	if call.Dice == nil {
		return nil
	}
	dcr := executeSingleDiceCheck(*call.Dice, actx.GCtx.Session.Players)
	debugf("tool", "session=%d roll_dice result=%s", actx.Sid, formatSingleDiceResult(dcr))
	return []ToolResult{{Action: ToolRollDice, Result: formatSingleDiceResult(dcr)}}
}

// ── NPC actions ───────────────────────────────────────────────────────────────

type npcActAction struct{}

func (npcActAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	question := call.Question
	if question == "" {
		question = call.NPCCtx
	}
	debugf("tool", "session=%d npc_act npc=%q question=%s", actx.Sid, call.NPCName, question)
	doneNPC := timedDebug("NPC", "session=%d npc=%s", actx.Sid, call.NPCName)
	action, npcErr := actNPC(actx.Ctx, actx.Handles[models.AgentRoleNPC], *actx.GCtx, call.NPCName, question, *actx.TempNPCs)
	doneNPC()
	if npcErr != nil {
		log.Printf("[agent] NPC %q error: %v", call.NPCName, npcErr)
	}
	debugf("tool", "session=%d npc_act result=%s", actx.Sid, formatNPCAction(action))
	*actx.Interrupt = true
	return []ToolResult{{Action: ToolNPCAct, Result: formatNPCAction(action)}}
}

type actNPCAction struct{}

func (actNPCAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	question := call.Question
	if question == "" {
		question = call.NPCCtx
	}
	debugf("tool", "session=%d act_npc npc=%q question=%s", actx.Sid, call.NPCName, question)
	doneNPC := timedDebug("NPC", "session=%d npc=%s", actx.Sid, call.NPCName)
	action, npcErr := actNPC(actx.Ctx, actx.Handles[models.AgentRoleNPC], *actx.GCtx, call.NPCName, question, *actx.TempNPCs)
	doneNPC()
	if npcErr != nil {
		log.Printf("[agent] act_npc %q error: %v", call.NPCName, npcErr)
	}
	*actx.Interrupt = true
	return []ToolResult{{Action: ToolActNPC, Result: formatNPCAction(action)}}
}

type createNPCAction struct{}

func (createNPCAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	result := createNPC(actx.GCtx.Session.ID, call.CharCard)
	*actx.TempNPCs = nil
	models.DB.Where("session_id = ?", actx.GCtx.Session.ID).Find(actx.TempNPCs)
	return []ToolResult{{Action: ToolCreateNPC, Result: result}}
}

type destroyNPCAction struct{}

func (destroyNPCAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	result := destroyNPC(actx.GCtx.Session.ID, call.NPCName, call.DestroyReason)
	*actx.TempNPCs = nil
	models.DB.Where("session_id = ?", actx.GCtx.Session.ID).Find(actx.TempNPCs)
	return []ToolResult{{Action: call.Action, Result: result}}
}

// ── Character state actions ───────────────────────────────────────────────────

type updateCharactersAction struct{}

func (updateCharactersAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	if len(call.Changes) == 0 {
		return nil
	}
	debugf("tool", "session=%d update_characters changes=%v", actx.Sid, call.Changes)
	for _, change := range call.Changes {
		upd, ok := parseStateChange(change)
		if !ok {
			continue
		}
		isPlayer := false
		for _, p := range actx.GCtx.Session.Players {
			if p.CharacterCard.Name == upd.CharacterName {
				isPlayer = true
				break
			}
		}
		if isPlayer {
			applyCharacterUpdate(upd, actx.GCtx.Session.Players)
		} else {
			upd.IsNPC = true
			applyNPCUpdate(upd, actx.GCtx.Session.ID, *actx.TempNPCs, actx.GCtx.Session.Scenario.Content.Data.NPCs)
		}
	}
	return []ToolResult{{Action: ToolUpdateCharacters, Result: "已更新:" + strings.Join(call.Changes, "、")}}
}

type manageInventoryAction struct{}

func (manageInventoryAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	result := manageInventory(actx.GCtx.Session.Players, call.CharacterName, call.Operate, call.Item)
	return []ToolResult{{Action: ToolManageInventory, Result: result}}
}

type recordMonsterAction struct{}

func (recordMonsterAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	result := manageSeenMonster(actx.GCtx.Session.Players, call.CharacterName, call.Operate, call.Monster)
	return []ToolResult{{Action: ToolRecordMonster, Result: result}}
}

type manageSpellAction struct{}

func (manageSpellAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	result := manageSpell(actx.GCtx.Session.Players, call.CharacterName, call.Operate, call.Spell)
	return []ToolResult{{Action: ToolManageSpell, Result: result}}
}

type manageRelationAction struct{}

func (manageRelationAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	result := manageSocialRelation(actx.GCtx.Session.Players, call.CharacterName, call.Operate, call.Relation)
	return []ToolResult{{Action: ToolManageRelation, Result: result}}
}

type updateLLMNoteAction struct{}

func (updateLLMNoteAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	who := call.CharacterName
	debugf("tool", "session=%d update_llm_note char=%q note=%q", actx.Sid, who, call.LLMNote)
	for i := range actx.GCtx.Session.Players {
		if actx.GCtx.Session.Players[i].CharacterCard.Name == who {
			actx.GCtx.Session.Players[i].LLMNote = call.LLMNote
			models.DB.Save(&actx.GCtx.Session.Players[i])
			return []ToolResult{{Action: ToolUpdateLLMNote, Result: fmt.Sprintf("已记录 %s 的状态(Session级备忘)", who)}}
		}
	}
	return []ToolResult{{Action: ToolUpdateLLMNote, Result: fmt.Sprintf("找不到名为 %s 的调查员", who)}}
}

type updateNPCLLMNoteAction struct{}

func (updateNPCLLMNoteAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	who := call.NPCName
	debugf("tool", "session=%d update_npc_llm_note npc=%q note=%q", actx.Sid, who, call.LLMNote)
	for i := range *actx.TempNPCs {
		if (*actx.TempNPCs)[i].Name == who {
			(*actx.TempNPCs)[i].LLMNote = call.LLMNote
			models.DB.Save(&(*actx.TempNPCs)[i])
			return []ToolResult{{Action: ToolUpdateNPCLLMNote, Result: fmt.Sprintf("已记录 %s 的状态(Session级备忘)", who)}}
		}
	}
	return []ToolResult{{Action: ToolUpdateNPCLLMNote, Result: fmt.Sprintf("找不到名为 %s 的NPC", who)}}
}

// ── NPC card actions ──────────────────────────────────────────────────────────

type queryNPCCardAction struct{}

func (queryNPCCardAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d query_npc_card npc=%q", actx.Sid, call.NPCName)
	return []ToolResult{{
		Action: ToolQueryNPCCard,
		Result: buildNPCDetail(call.NPCName, *actx.TempNPCs, actx.GCtx.Session.Scenario.Content.Data.NPCs),
	}}
}

type updateNPCCardAction struct{}

func (updateNPCCardAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	if call.NPCName == "" || len(call.Changes) == 0 {
		return []ToolResult{{Action: ToolUpdateNPCCard, Result: "错误: update_npc_card 参数不足:需要 npc_name 和 changes"}}
	}
	debugf("tool", "session=%d update_npc_card npc=%q changes=%v", actx.Sid, call.NPCName, call.Changes)
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
		applyNPCUpdate(upd, actx.GCtx.Session.ID, *actx.TempNPCs, actx.GCtx.Session.Scenario.Content.Data.NPCs)
		applied = append(applied, change)
	}
	*actx.TempNPCs = nil
	models.DB.Where("session_id = ?", actx.GCtx.Session.ID).Find(actx.TempNPCs)
	if len(applied) == 0 {
		return []ToolResult{{Action: ToolUpdateNPCCard, Result: "未识别可应用的变更"}}
	}
	return []ToolResult{{Action: ToolUpdateNPCCard, Result: "已更新NPC:" + strings.Join(applied, "、")}}
}

// ── Query actions ─────────────────────────────────────────────────────────────

type queryCluesAction struct{}

func (queryCluesAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d query_clues all", actx.Sid)
	clues := actx.GCtx.Session.Scenario.Content.Data.Clues
	clueResult := "(无匹配线索)"
	if len(clues) > 0 {
		clueResult = strings.Join(clues, "\n")
	}
	return []ToolResult{{Action: ToolQueryClues, Result: fmt.Sprintf("线索查询结果(全部):\n%s", clueResult)}}
}

type queryCharacterAction struct{}

func (queryCharacterAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d query_character name=%q", actx.Sid, call.CharacterName)
	return []ToolResult{{
		Action: ToolQueryCharacter,
		Result: buildCharacterDetail(call.CharacterName, actx.GCtx.Session.Players),
	}}
}

// ── Narrative / flow actions ──────────────────────────────────────────────────

type writeAction struct{}

func (writeAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	*actx.PendingWrite += fmt.Sprintf("%s\n", call.Direction)
	debugf("tool", "session=%d write direction=%s", actx.Sid, call.Direction)
	return nil
}

type responseAction struct{}

func (responseAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	*actx.HasEnd = true
	if *actx.KPNarration != "" {
		*actx.KPNarration = call.Reply + "\n" + *actx.KPNarration
	} else {
		*actx.KPNarration = call.Reply
	}
	if *actx.PendingWrite != "" {
		doneW := timedDebug("Writer", "session=%d direction=%s", actx.Sid, *actx.PendingWrite)
		writeErr := appendWriter(actx.Ctx, actx.Handles[models.AgentRoleWriter], actx.Writer, *actx.PendingWrite, *actx.GCtx)
		doneW()
		if writeErr != nil {
			log.Printf("[agent] writer error: %v", writeErr)
		}
		debugf("tool", "session=%d write buffer_len=%d", actx.Sid, len([]rune(actx.Writer.Buffer)))
	}
	debugf("tool", "session=%d response narration=%s", actx.Sid, call.Reply)
	return nil
}

type yieldAction struct{}

func (yieldAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d KP yields control, remaining calls deferred to next round", actx.Sid)
	*actx.Interrupt = true
	return nil
}

// ── Time action ───────────────────────────────────────────────────────────────

type advanceTimeAction struct{}

func (advanceTimeAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	rounds := call.TimeRounds
	if rounds <= 0 {
		rounds = 1
	}
	newRound := actx.GCtx.Session.TurnRound + rounds
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", actx.GCtx.Session.ID).
		Update("turn_round", newRound)
	models.DB.Where("session_id = ? AND round < ?", actx.GCtx.Session.ID, newRound).
		Delete(&models.SessionTurnAction{})
	actx.GCtx.Session.TurnRound = newRound
	*actx.TimeAdvancedInTurn = true
	reason := call.TimeReason
	if reason == "" {
		reason = "时间推进"
	}
	for i := range actx.GCtx.Session.Players {
		card := &actx.GCtx.Session.Players[i].CharacterCard
		if card.MadnessState == "none" || card.MadnessState == "" {
			continue
		}
		card.MadnessDuration -= rounds
		if card.MadnessDuration <= 0 {
			card.MadnessState = "none"
			card.MadnessSymptom = ""
			card.MadnessDuration = 0
			debugf("madness", "session=%d char=%s madness ended", actx.Sid, card.Name)
		}
		models.DB.Save(card)
		break
	}
	log.Printf("[agent] session %d advance_time +%d rounds (%s) → %s",
		actx.GCtx.Session.ID, rounds, reason, formatGameTime(newRound, scenarioStartSlot(actx.GCtx.Session)))
	return []ToolResult{{
		Action: ToolAdvanceTime,
		Result: fmt.Sprintf("时间推进%d回合(%s),当前时间:%s", rounds, reason, formatGameTime(newRound, scenarioStartSlot(actx.GCtx.Session))),
	}}
}

// ── Madness action ────────────────────────────────────────────────────────────

type triggerMadnessAction struct{}

func (triggerMadnessAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	who := call.CharacterName
	if who == "" {
		who = "调查员"
	}
	debugf("tool", "session=%d trigger_madness char=%q bystander=%v", actx.Sid, who, call.IsBystander)
	symptom := game.RollMadnessSymptom(call.IsBystander)
	madnessType := "总结症状"
	if call.IsBystander {
		madnessType = "即时症状"
	}
	for i := range actx.GCtx.Session.Players {
		card := &actx.GCtx.Session.Players[i].CharacterCard
		if who != "调查员" && card.Name != who {
			continue
		}
		if card.MadnessState == "none" || card.MadnessState == "" {
			if call.IsBystander {
				card.MadnessState = "temporary"
			} else {
				card.MadnessState = "indefinite"
			}
		}
		card.MadnessSymptom = symptom.Description
		card.MadnessDuration = symptom.Duration
		models.DB.Save(card)
		break
	}
	return []ToolResult{{
		Action: ToolTriggerMadness,
		Result: fmt.Sprintf("%s疯狂发作(%s,持续%d):%s", who, madnessType, symptom.Duration, symptom.Description),
	}}
}

// ── End game action ───────────────────────────────────────────────────────────

type endGameAction struct{}

func (endGameAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	var results []ToolResult
	if call.EndSummary != "" {
		results = append(results, ToolResult{Action: ToolEndGame, Result: "剧本结局:" + call.EndSummary})
	}
	if len(actx.GCtx.Session.Players) == 0 {
		models.DB.Preload("CharacterCard").
			Where("session_id = ?", actx.GCtx.Session.ID).
			Find(&actx.GCtx.Session.Players)
	}
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", actx.GCtx.Session.ID).
		Update("status", models.SessionStatusEnded)
	models.DB.Where("session_id = ?", actx.GCtx.Session.ID).Delete(&models.SessionTurnAction{})
	actx.GCtx.Session.Status = models.SessionStatusEnded
	*actx.HasEnd = true
	if call.Reply != "" {
		*actx.KPNarration = call.Reply
	} else if call.EndSummary != "" {
		*actx.KPNarration = "本次冒险结束。" + call.EndSummary
	} else {
		*actx.KPNarration = "本次冒险到此结束,感谢各位调查员。"
	}
	debugf("tool", "session=%d end_game summary=%s", actx.Sid, call.EndSummary)

	var msgs []models.Message
	models.DB.Where("session_id = ? AND role != ?", actx.GCtx.Session.ID, models.MessageRoleSystem).
		Order("created_at ASC").
		Limit(150).
		Find(&msgs)
	sessionSnap := actx.GCtx.Session
	if _, err := RunEndSession(actx.Ctx, &sessionSnap, msgs); err != nil {
		debugf("tool", "session=%d RunEndSession error: %v", actx.GCtx.Session.ID, err)
	}
	return results
}

// ── Combat actions ────────────────────────────────────────────────────────────

type startCombatAction struct{}

func (startCombatAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	if len(call.CombatParticipants) == 0 {
		return []ToolResult{{Action: ToolStartCombat, Result: "错误:start_combat 缺少 combat_participants 参数"}}
	}
	cs := buildCombatState(call.CombatParticipants)
	actx.GCtx.Session.CombatState = models.JSONField[*models.CombatState]{Data: &cs}
	saveCombatState(actx.GCtx.Session.ID, &cs)
	debugf("tool", "session=%d start_combat round=1 participants=%d", actx.Sid, len(cs.Participants))
	return []ToolResult{{
		Action: ToolStartCombat,
		Result: fmt.Sprintf("战斗开始！DEX顺序:%s", combatOrderSummary(cs.Participants)),
	}}
}

type combatActAction struct{}

func (combatActAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	cs := actx.GCtx.Session.CombatState.Data
	if cs == nil {
		return []ToolResult{{Action: ToolCombatAct, Result: "错误:当前没有进行中的战斗"}}
	}
	result, sr := applyCombatAct(cs, call)
	if sr {
		*actx.SwitchRole = true
	}
	actx.GCtx.Session.CombatState = models.JSONField[*models.CombatState]{Data: cs}
	saveCombatState(actx.GCtx.Session.ID, cs)
	debugf("tool", "session=%d combat_act actor=%q action=%q", actx.Sid, call.CombatActorName, call.CombatAction)
	*actx.Interrupt = true
	return []ToolResult{{Action: ToolCombatAct, Result: result}}
}

type endCombatAction struct{}

func (endCombatAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	actx.GCtx.Session.CombatState = models.JSONField[*models.CombatState]{Data: nil}
	saveCombatState(actx.GCtx.Session.ID, nil)
	reason := call.CombatEndReason
	if reason == "" {
		reason = "战斗结束"
	}
	debugf("tool", "session=%d end_combat reason=%q", actx.Sid, reason)
	return []ToolResult{{Action: ToolEndCombat, Result: fmt.Sprintf("战斗已结束:%s", reason)}}
}

// ── Chase actions ─────────────────────────────────────────────────────────────

type startChaseAction struct{}

func (startChaseAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	if len(call.ChaseParticipants) == 0 {
		return []ToolResult{{Action: ToolStartChase, Result: "错误:start_chase 缺少 chase_participants 参数"}}
	}
	chs := buildChaseState(call.ChaseParticipants)
	actx.GCtx.Session.ChaseState = models.JSONField[*models.ChaseState]{Data: &chs}
	saveChaseState(actx.GCtx.Session.ID, &chs)
	debugf("tool", "session=%d start_chase round=1 participants=%d minMOV=%d", actx.Sid, len(chs.Participants), chs.MinMOV)
	return []ToolResult{{
		Action: ToolStartChase,
		Result: fmt.Sprintf("追逐开始！参与者:%s；最低MOV=%d,各参与者行动点=%s",
			chaseParticipantSummary(chs.Participants),
			chs.MinMOV,
			chaseAPSummary(chs.Participants, chs.MinMOV),
		),
	}}
}

type chaseActAction struct{}

func (chaseActAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	chs := actx.GCtx.Session.ChaseState.Data
	if chs == nil {
		return []ToolResult{{Action: ToolChaseAct, Result: "错误:当前没有进行中的追逐"}}
	}
	result, sr := applyChaseAct(chs, call)
	if sr {
		*actx.SwitchRole = true
	}
	actx.GCtx.Session.ChaseState = models.JSONField[*models.ChaseState]{Data: chs}
	saveChaseState(actx.GCtx.Session.ID, chs)
	debugf("tool", "session=%d chase_act actor=%q action=%v", actx.Sid, call.ChaseActorName, call.ChaseAction)
	*actx.Interrupt = true
	return []ToolResult{{Action: ToolChaseAct, Result: result}}
}

type endChaseAction struct{}

func (endChaseAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	actx.GCtx.Session.ChaseState = models.JSONField[*models.ChaseState]{Data: nil}
	saveChaseState(actx.GCtx.Session.ID, nil)
	reason := call.ChaseEndReason
	if reason == "" {
		reason = "追逐结束"
	}
	debugf("tool", "session=%d end_chase reason=%q", actx.Sid, reason)
	return []ToolResult{{Action: ToolEndChase, Result: fmt.Sprintf("追逐已结束:%s", reason)}}
}
