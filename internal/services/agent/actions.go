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
	ToolActNPC:            true, // returns NPC reaction that must be read before response
}

// responseCompatibleActions is the set of actions that MAY coexist with
// response/end_game in the same batch (they don't return results the KP needs
// to read before concluding the turn).
var responseCompatibleActions = map[ToolCallType]bool{
	ToolResponse:         true,
	ToolEndGame:          true,
	ToolWrite:            true,
	ToolHint:             true,
	ToolFoundClue:        true,
	ToolThink:            true,
	ToolUpdateLLMNote:    true,
	ToolUpdateNPCLLMNote: true,
	ToolUpdateLocation:   true,
	ToolUpdateArmor:      true,
	ToolReport:           true,
	ToolYield:            true,
	ToolUpdateCharacters: true,
	ToolManageInventory:  true,
	ToolRecordMonster:    true,
	ToolManageSpell:      true,
	ToolManageRelation:   true,
	ToolUpdateNPCCard:    true,
	ToolTriggerMadness:   true,
	ToolAdvanceTime:      true,
	ToolDestroyNPC:       true,
}

// actionRegistry maps each ToolCallType to its handler.
// Handlers not listed here produce no result and are silently skipped.
var actionRegistry = map[ToolCallType]Action{
	ToolCheckRule:         checkRuleAction{},
	ToolReadRulebookConst: readRulebookConstAction{},
	ToolRollDice:          rollDiceAction{},
	ToolActNPC:            actNPCAction{},
	ToolCreateNPC:         createNPCAction{},
	ToolDestroyNPC:        destroyNPCAction{},
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
	ToolUpdateLocation:    updateLocationAction{},
	ToolUpdateArmor:       updateArmorAction{},
	ToolHint:              hintAction{},
	ToolFoundClue:         foundClueAction{},
	ToolResponse:          responseAction{},
	ToolThink:             emptyAction{actionName: string(ToolThink)},
	ToolReport:            reportAction{},
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
	if call.Dice.What == "" {
		call.Dice.What = call.Reason
	}
	dcr := executeSingleDiceCheck(*call.Dice, actx.GCtx.Session.Players)
	debugf("tool", "session=%d roll_dice result=%s", actx.Sid, formatSingleDiceResult(dcr))
	return []ToolResult{{Action: ToolRollDice, Result: formatSingleDiceResult(dcr)}}
}

type actNPCAction struct{}

func (actNPCAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	question := call.Question
	if question == "" {
		question = call.NPCCtx
	}
	if strings.TrimSpace(call.KPDirective) != "" {
		question = question + "\n【KP剧情指令(最高优先级，不得透露给玩家)】" + call.KPDirective
	}
	debugf("tool", "session=%d act_npc npc=%q question=%s", actx.Sid, call.NPCName, question)
	doneNPC := timedDebug("NPC", "session=%d npc=%s", actx.Sid, call.NPCName)
	if call.HideSecret {
		question = "(注意隐瞒你的秘密) " + question
	}
	if call.Spell != "" {
		if strings.Contains(call.Spell, "附魔") {
			question += " (你可以使用使用法术: " + call.Spell + ", 且可以附魔)"
		} else {
			question += " (你可以使用使用法术: " + call.Spell + " , 但不能附魔)"
		}
	} else {
		question += " (你没有法术可用, 且无法创造改造魔法物品)"
	}
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
	item := call.Item
	if call.ItemName != "" {
		// Assemble canonical "Name(Desc, xN)" from structured fields.
		item = buildInventoryItem(call.ItemName, call.ItemDesc, max(1, call.ItemCount))
	}
	result := manageInventory(actx.GCtx.Session.Players, call.CharacterName, call.Operate, item)
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

type updateLocationAction struct{}

func (updateLocationAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	who := call.CharacterName
	loc := strings.TrimSpace(call.NewLocation)
	debugf("tool", "session=%d update_location char=%q location=%q", actx.Sid, who, loc)
	for i := range actx.GCtx.Session.Players {
		if actx.GCtx.Session.Players[i].CharacterCard.Name == who {
			actx.GCtx.Session.Players[i].Location = loc
			models.DB.Model(&actx.GCtx.Session.Players[i]).Update("location", loc)
			return []ToolResult{{Action: ToolUpdateLocation, Result: fmt.Sprintf("%s 当前位置已更新为: %s", who, loc)}}
		}
	}
	return []ToolResult{{Action: ToolUpdateLocation, Result: fmt.Sprintf("找不到名为 %s 的调查员", who)}}
}

type updateArmorAction struct{}

func (updateArmorAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	who := call.CharacterName
	val := call.ArmorValue
	debugf("tool", "session=%d update_armor char=%q armor=%d", actx.Sid, who, val)
	for i := range actx.GCtx.Session.Players {
		if actx.GCtx.Session.Players[i].CharacterCard.Name == who {
			actx.GCtx.Session.Players[i].Armor = val
			models.DB.Model(&actx.GCtx.Session.Players[i]).Update("armor", val)
			return []ToolResult{{Action: ToolUpdateArmor, Result: fmt.Sprintf("%s 护甲已更新为 %d点", who, val)}}
		}
	}
	return []ToolResult{{Action: ToolUpdateArmor, Result: fmt.Sprintf("找不到名为 %s 的调查员", who)}}
}

type hintAction struct{}

func (hintAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d hint hint_len=%d content=%v", actx.Sid, len([]rune(call.Hint)), call.Hint)
	actx.GCtx.Session.KPHint = call.Hint
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", actx.GCtx.Session.ID).
		Update("kp_hint", call.Hint)
	return []ToolResult{{Action: ToolHint, Result: call.Hint}}
}

type foundClueAction struct{}

func (foundClueAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	clues := actx.GCtx.Session.Scenario.Content.Data.Clues
	idx := call.ClueIdx
	if idx < 0 || idx >= len(clues) {
		return []ToolResult{{Action: ToolFoundClue, Result: fmt.Sprintf("error: clue_idx %d out of range (0-%d), check query_clues for valid indices", idx, len(clues)-1)}}
	}
	debugf("tool", "session=%d found_clue idx=%d", actx.Sid, idx)

	// Deduplicate.
	for _, f := range actx.GCtx.Session.FoundClues.Data {
		if f == idx {
			return []ToolResult{{Action: ToolFoundClue, Result: fmt.Sprintf("clue[%d] already recorded", idx)}}
		}
	}

	actx.GCtx.Session.FoundClues.Data = append(actx.GCtx.Session.FoundClues.Data, idx)
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", actx.GCtx.Session.ID).
		Update("found_clues", actx.GCtx.Session.FoundClues)
	*actx.PendingWrite += fmt.Sprintf("\n【线索已获得】%s\n", clues[idx])
	return []ToolResult{{Action: ToolFoundClue, Result: fmt.Sprintf("clue[%d] recorded: %s", idx, clues[idx])}}
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
	clueResult := "(无线索)"
	if len(clues) > 0 {
		foundSet := make(map[int]bool, len(actx.GCtx.Session.FoundClues.Data))
		for _, i := range actx.GCtx.Session.FoundClues.Data {
			foundSet[i] = true
		}
		var sb strings.Builder
		for i, c := range clues {
			if foundSet[i] {
				sb.WriteString(fmt.Sprintf("[Idx: %d][已发现] %s\n", i, c))
			} else {
				sb.WriteString(fmt.Sprintf("[Idx: %d][未发现] %s\n", i, c))
			}
		}
		clueResult = sb.String()
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
	call.Reply += "\n<ack>" + strings.Join(call.Ack, ";") + "</ack>"
	call.Reply += "\n<direction>" + call.Direction + "</direction>"
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

	var closing string
	if call.Reply != "" {
		closing = call.Reply
	} else if call.EndSummary != "" {
		closing = "本次冒险结束。" + call.EndSummary
	} else {
		closing = "本次冒险到此结束,感谢各位调查员。"
	}
	// Preserve any narration already set by earlier actions in the same batch.
	if *actx.KPNarration != "" {
		*actx.KPNarration = closing + "\n" + *actx.KPNarration
	} else {
		*actx.KPNarration = closing
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

type emptyAction struct {
	actionName string
}

func (a emptyAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	return []ToolResult{}
}

type reportAction struct{}

func (reportAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d report report=%q", actx.Sid, call.Report)
	return []ToolResult{{Action: ToolReport, Result: call.Report}}
}
