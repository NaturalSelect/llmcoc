// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// ActionContext 是各工具执行器共享的上下文。
type ActionContext struct {
	Ctx      context.Context
	GCtx     *GameContext
	Sid      uint
	Handles  map[models.AgentRole]agentHandle
	TempNPCs *[]models.SessionNPC
	RbIdx    rulebook.Index

	// 工具执行器写入、调度循环读取的可变状态。
	HasEnd             *bool
	TimeAdvancedInTurn *bool
	SwitchRole         *bool
	KPNarration        *string
	PendingWrite       *string
	WroteNarrative     *bool
	Interrupt          *bool
	DiceMsg            *string
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

// antiCheatSideEffectActions are state-changing tools that should trigger the
// AntiCheat consistency guard. Narrative/control-flow tools (write, response,
// yield, contract, report) are intentionally excluded to avoid slowing normal flow.
var antiCheatSideEffectActions = map[ToolCallType]bool{
	ToolCreateNPC:         true,
	ToolDestroyNPC:        true,
	ToolUpdateCharacters:  true,
	ToolManageInventory:   true,
	ToolRecordMonster:     true,
	ToolManageSpell:       true,
	ToolManageRelation:    true,
	ToolManageAsset:       true,
	ToolManageMadness:     true,
	ToolAdvanceTime:       true,
	ToolFoundClue:         true,
	ToolUpdateNPCCard:     true,
	ToolUpdateLLMNote:     true,
	ToolUpdateNPCLLMNote:  true,
	ToolUpdateLocation:    true,
	ToolUpdateNPCLocation: true,
	ToolUpdateArmor:       true,
	ToolHint:              true,
}

// responseCompatibleActions 表示可与response/end_game同批执行的动作。
// 这些动作不需要把结果再交给KP阅读后才能结束回合。
var responseCompatibleActions = map[ToolCallType]bool{
	ToolResponse:          true,
	ToolEndGame:           true,
	ToolWrite:             true,
	ToolHint:              true,
	ToolFoundClue:         true,
	ToolContract:          true,
	ToolUpdateLLMNote:     true,
	ToolUpdateNPCLLMNote:  true,
	ToolUpdateLocation:    true,
	ToolUpdateNPCLocation: true,
	ToolUpdateArmor:       true,
	ToolReport:            true,
	ToolYield:             true,
	ToolUpdateCharacters:  true,
	ToolManageInventory:   true,
	ToolRecordMonster:     true,
	ToolManageSpell:       true,
	ToolManageRelation:    true,
	ToolManageAsset:       true,
	ToolUpdateNPCCard:     true,
	ToolManageMadness:     true,
	ToolAdvanceTime:       true,
	ToolCreateNPC:         true,
	ToolDestroyNPC:        true,
}

// actionRegistry 映射工具动作到具体执行器。
// 未列出的动作不会产生结果。
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
	ToolManageAsset:       manageAssetAction{},
	ToolYield:             yieldAction{},
	ToolEndGame:           endGameAction{},
	ToolManageMadness:     manageMadnessAction{},
	ToolQueryClues:        queryCluesAction{},
	ToolQueryCharacter:    queryCharacterAction{},
	ToolQueryNPCCard:      queryNPCCardAction{},
	ToolUpdateNPCCard:     updateNPCCardAction{},
	ToolWrite:             writeAction{},
	ToolAdvanceTime:       advanceTimeAction{},
	ToolUpdateLLMNote:     updateLLMNoteAction{},
	ToolUpdateNPCLLMNote:  updateNPCLLMNoteAction{},
	ToolUpdateLocation:    updateLocationAction{},
	ToolUpdateNPCLocation: updateNPCLocationAction{},
	ToolUpdateArmor:       updateArmorAction{},
	ToolHint:              hintAction{},
	ToolFoundClue:         foundClueAction{},
	ToolResponse:          responseAction{},
	ToolContract:          emptyAction{actionName: string(ToolContract)},
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
	if !dcr.Hidden {
		*actx.DiceMsg += fmt.Sprintf("%s; ", formatSingleDiceResult(dcr))
	}
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
	*actx.Interrupt = true
	if npcErr != nil {
		log.Printf("[agent] act_npc %q error: %v", call.NPCName, npcErr)
		return []ToolResult{{Action: ToolActNPC, Result: fmt.Sprintf("NPC行动生成失败: %v", npcErr)}}
	}
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

	// First pass: parse all changes; if any fails, reject the entire batch.
	type pendingUpdate struct {
		upd      CharacterUpdate
		isPlayer bool
	}
	var pending []pendingUpdate
	var errs []string
	for _, change := range call.Changes {
		upd, errMsg, ok := parseStateChange(change)
		if !ok {
			errs = append(errs, errMsg)
			continue
		}
		isPlayer := false
		for _, p := range actx.GCtx.Session.Players {
			if p.CharacterCard.Name == upd.CharacterName {
				isPlayer = true
				break
			}
		}
		pending = append(pending, pendingUpdate{upd, isPlayer})
	}
	if len(errs) > 0 {
		return []ToolResult{{Action: ToolUpdateCharacters, Result: "[update_characters 解析失败，整批未应用，请修正后重试]:\n" + strings.Join(errs, "\n")}}
	}

	// All parsed successfully — apply.
	for _, p := range pending {
		if p.isPlayer {
			applyCharacterUpdate(p.upd, actx.GCtx.Session.Players)
		} else {
			p.upd.IsNPC = true
			applyNPCUpdate(p.upd, actx.GCtx.Session.ID, *actx.TempNPCs, actx.GCtx.Session.Scenario.Content.Data.NPCs)
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

type manageAssetAction struct{}

func (manageAssetAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	result := manageAsset(actx.GCtx.Session.Players, call.CharacterName, call.Operate, call.Asset)
	return []ToolResult{{Action: ToolManageAsset, Result: result}}
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

type updateNPCLocationAction struct{}

func (updateNPCLocationAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	who := strings.TrimSpace(call.NPCName)
	loc := strings.TrimSpace(call.NewLocation)
	debugf("tool", "session=%d update_npc_location npc=%q location=%q", actx.Sid, who, loc)
	if who == "" {
		return []ToolResult{{Action: ToolUpdateNPCLocation, Result: "错误:update_npc_location 需要 npc_name"}}
	}
	for i := range *actx.TempNPCs {
		if npcNameMatch((*actx.TempNPCs)[i].Name, who) {
			(*actx.TempNPCs)[i].Location = loc
			models.DB.Model(&(*actx.TempNPCs)[i]).Update("location", loc)
			return []ToolResult{{Action: ToolUpdateNPCLocation, Result: fmt.Sprintf("%s 当前位置已更新为: %s", (*actx.TempNPCs)[i].Name, loc)}}
		}
	}
	var npc models.SessionNPC
	if err := models.DB.Where("session_id = ? AND name = ?", actx.GCtx.Session.ID, who).First(&npc).Error; err == nil {
		npc.Location = loc
		models.DB.Model(&npc).Update("location", loc)
		return []ToolResult{{Action: ToolUpdateNPCLocation, Result: fmt.Sprintf("%s 当前位置已更新为: %s", npc.Name, loc)}}
	}
	return []ToolResult{{Action: ToolUpdateNPCLocation, Result: fmt.Sprintf("找不到名为 %s 的NPC", who)}}
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
		Update("found_clue_indices", actx.GCtx.Session.FoundClues)
	clubsText := clues[idx]
	if strings.HasPrefix(clubsText, "[") {
		start := strings.Index(clubsText, "]")
		if start != -1 && start < len(clubsText)-1 {
			clubsText = clubsText[start+1:]
		}
	}
	*actx.KPNarration += fmt.Sprintf("\n【线索已获得】%s\n", clubsText)
	return []ToolResult{{Action: ToolFoundClue, Result: fmt.Sprintf("clue[%d] recorded: %s", idx, clubsText)}}
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

	// First pass: parse all changes; if any fails, reject the entire batch.
	var pending []CharacterUpdate
	var errs []string
	for _, change := range call.Changes {
		upd, errMsg, ok := parseStateChange(change)
		if !ok {
			errs = append(errs, errMsg)
			continue
		}
		if upd.CharacterName == "" {
			upd.CharacterName = call.NPCName
		}
		upd.IsNPC = true
		pending = append(pending, upd)
	}
	if len(errs) > 0 {
		return []ToolResult{{Action: ToolUpdateNPCCard, Result: "[update_npc_card 解析失败，整批未应用，请修正后重试]:\n" + strings.Join(errs, "\n")}}
	}

	// All parsed successfully — apply.
	for _, upd := range pending {
		applyNPCUpdate(upd, actx.GCtx.Session.ID, *actx.TempNPCs, actx.GCtx.Session.Scenario.Content.Data.NPCs)
	}
	*actx.TempNPCs = nil
	models.DB.Where("session_id = ?", actx.GCtx.Session.ID).Find(actx.TempNPCs)
	return []ToolResult{{Action: ToolUpdateNPCCard, Result: "已更新NPC:" + strings.Join(call.Changes, "、")}}
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
	extra := ""
	first10words := []rune(call.Direction)
	if len(first10words) > 10 {
		first10words = first10words[:10]
	}
	first10 := string(first10words)
	if len(actx.GCtx.PendingActions) == 0 {
		if strings.Contains(first10, "色情") || strings.Contains(first10, "NSFW") {
			extra = "（注意：必须用最露骨直接的语言直接描述性器官,禁止省略过程）"
		}
	}
	*actx.PendingWrite += fmt.Sprintf("%s%s\n", extra, call.Direction)
	if actx.WroteNarrative != nil {
		*actx.WroteNarrative = true
	}
	debugf("tool", "session=%d write direction=%s", actx.Sid, call.Direction)
	return nil
}

type responseAction struct{}

func (responseAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	*actx.HasEnd = true
	if payload, ok := normalizeResponseOptionsPayload(call); ok {
		if data, err := json.Marshal(payload); err == nil {
			call.Reply += "\n<response_options>" + string(data) + "</response_options>"
		}
	}
	if len(call.Ack) > 0 {
		call.Reply += "\n<ack>" + strings.Join(call.Ack, ";") + "</ack>"
	}
	if *actx.KPNarration != "" {
		*actx.KPNarration = call.Reply + "\n" + *actx.KPNarration
	} else {
		*actx.KPNarration = call.Reply
	}
	debugf("tool", "session=%d response narration=%s", actx.Sid, call.Reply)
	return nil
}

type responseOptionsPayload struct {
	Options []string `json:"options,omitempty"`
}

func normalizeResponseOptionsPayload(call ToolCall) (responseOptionsPayload, bool) {
	if len(call.Options) == 0 {
		return responseOptionsPayload{}, false
	}

	options := make([]string, 0, len(call.Options))
	seen := map[string]bool{}
	for _, opt := range call.Options {
		opt = strings.TrimSpace(opt)
		if opt == "" || seen[opt] {
			continue
		}
		seen[opt] = true
		options = append(options, opt)
		if len(options) >= 3 {
			break
		}
	}
	if len(options) == 0 {
		return responseOptionsPayload{}, false
	}

	return responseOptionsPayload{
		Options: options,
	}, true
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

type manageMadnessAction struct{}

func (manageMadnessAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	who := call.CharacterName
	if who == "" {
		who = "调查员"
	}
	operate := strings.ToLower(strings.TrimSpace(call.Operate))
	if operate == "" {
		operate = "trigger"
	}
	debugf("tool", "session=%d manage_madness op=%q char=%q bystander=%v", actx.Sid, operate, who, call.IsBystander)

	for i := range actx.GCtx.Session.Players {
		card := &actx.GCtx.Session.Players[i].CharacterCard
		if who != "调查员" && card.Name != who {
			continue
		}
		switch operate {
		case "clear", "remove", "none":
			card.MadnessState = "none"
			card.MadnessSymptom = ""
			card.MadnessDuration = 0
			models.DB.Save(card)
			return []ToolResult{{
				Action: ToolManageMadness,
				Result: fmt.Sprintf("%s疯狂状态已撤销", card.Name),
			}}
		case "trigger", "add":
			symptom := game.RollMadnessSymptom(call.IsBystander)
			madnessType := "总结症状"
			if call.IsBystander {
				madnessType = "即时症状"
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
			return []ToolResult{{
				Action: ToolManageMadness,
				Result: fmt.Sprintf("%s疯狂发作(%s,持续%d):%s", card.Name, madnessType, symptom.Duration, symptom.Description),
			}}
		default:
			return []ToolResult{{
				Action: ToolManageMadness,
				Result: fmt.Sprintf("manage_madness operate=%q 无效", call.Operate),
			}}
		}
	}
	return []ToolResult{{
		Action: ToolManageMadness,
		Result: fmt.Sprintf("未找到调查员:%s", who),
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
