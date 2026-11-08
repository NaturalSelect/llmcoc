// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
)

// ActionContext 是各工具执行器共享的上下文。
type ActionContext struct {
	Ctx      context.Context
	GCtx     *GameContext
	Sid      uint
	Handles  map[models.AgentRole]agentHandle
	TempNPCs *[]models.SessionNPC

	// 工具执行器写入、调度循环读取的可变状态。
	HasEnd             *bool
	TimeAdvancedInTurn *bool
	// Outbox 收集本轮产生玩家可见消息的调用(write/generate_image/narrate/ask_players/end_game),
	// 一个调用一个 Output;轮末由 floorRun 整理顺序后发布。
	Outbox   *[]Output
	DiceMsg  *string
	KPStreak *int // 本周期内连续发布的 kp 消息数,ask_players 之前不清零,用于节奏提醒
	// Ask 由ask_players写入;周期自然结束于点名时非nil。
	Ask **AskResult

	// Combat/Chase 是本次run()持有的活战斗/追逐状态(nil=未激活);战斗/追逐工具
	// 执行器通过它们读写、并允许置nil(start_*建立/end_*清空)。二者互斥。
	// RoundClosed是run内一次性闸门(D2):一次run最多完整推进一个战斗/追逐轮,
	// 翻页后置位,同一run内后续combat_act/chase_act一律拒绝,逼Director用narrate收尾。
	Combat      **models.CombatState
	Chase       **models.ChaseState
	RoundClosed *bool
}

// Action is implemented by every tool call handler.
type Action interface {
	Execute(call ToolCall, actx ActionContext) []ToolResult
}

// noSideEffectActions is the set of tool call types that have no side effects
// (sideeffect: false in the KP system prompt). These are pure query/compute
// actions that must NOT be mixed with side-effect actions in the same phase.
var noSideEffectActions = map[ToolCallType]bool{
	ToolRollDice:           true,
	ToolCheckRule:          true,
	ToolConsultDramaturg:   true,
	ToolQueryClues:         true,
	ToolQueryCharacter:     true,
	ToolQueryNPCCard:       true,
	ToolDescribeCharacters: true,
	ToolGenerateImage:      true,
	ToolActNPC:             true, // returns NPC reaction that must be read before narrate
}

// narrateCompatibleActions 表示可与narrate/end_game同批执行的动作。
// 这些动作不需要把结果再交给KP阅读后才能结束回合。
var narrateCompatibleActions = map[ToolCallType]bool{
	ToolNarrate:                true,
	ToolAskPlayers:             true,
	ToolEndGame:                true,
	ToolWrite:                  true,
	ToolHint:                   true,
	ToolGenerateImage:          true,
	ToolUpdateSessionMemory:    true,
	ToolUpdateNPCSessionMemory: true,
	ToolUpdateLocation:         true,
	ToolUpdateNPCLocation:      true,
	ToolUpdateArmor:            true,
	ToolReport:                 true,
	ToolUpdateCharacters:       true,
	ToolManageInventory:        true,
	ToolRecordMonster:          true,
	ToolManageSpell:            true,
	ToolManageRelation:         true,
	ToolManageAsset:            true,
	ToolUpdateNPCCard:          true,
	ToolManageMadness:          true,
	ToolAdvanceTime:            true,
	ToolCreateNPC:              true,
	ToolDestroyNPC:             true,
	ToolEndCombat:              true,
	ToolEndChase:               true,
}

// actionRegistry 映射工具动作到具体执行器。
// 未列出的动作不会产生结果。
var actionRegistry = map[ToolCallType]Action{
	ToolCheckRule:              checkRuleAction{},
	ToolConsultDramaturg:       consultDramaturgAction{},
	ToolRollDice:               rollDiceAction{},
	ToolActNPC:                 actNPCAction{},
	ToolCreateNPC:              createNPCAction{},
	ToolDestroyNPC:             destroyNPCAction{},
	ToolUpdateCharacters:       updateCharactersAction{},
	ToolManageInventory:        manageInventoryAction{},
	ToolRecordMonster:          recordMonsterAction{},
	ToolManageSpell:            manageSpellAction{},
	ToolManageRelation:         manageRelationAction{},
	ToolManageAsset:            manageAssetAction{},
	ToolEndGame:                endGameAction{},
	ToolManageMadness:          manageMadnessAction{},
	ToolQueryClues:             queryCluesAction{},
	ToolQueryCharacter:         queryCharacterAction{},
	ToolQueryNPCCard:           queryNPCCardAction{},
	ToolDescribeCharacters:     describeCharactersAction{},
	ToolUpdateNPCCard:          updateNPCCardAction{},
	ToolWrite:                  writeAction{},
	ToolAdvanceTime:            advanceTimeAction{},
	ToolUpdateSessionMemory:    updateSessionMemoryAction{},
	ToolUpdateNPCSessionMemory: updateNPCSessionMemoryAction{},
	ToolUpdateLocation:         updateLocationAction{},
	ToolUpdateNPCLocation:      updateNPCLocationAction{},
	ToolUpdateArmor:            updateArmorAction{},
	ToolHint:                   hintAction{},
	ToolGenerateImage:          generateImageAction{},
	ToolNarrate:                narrateAction{},
	ToolAskPlayers:             askPlayersAction{},
	ToolReport:                 reportAction{},

	ToolStartCombat: startCombatAction{},
	ToolCombatAct:   combatActAction{},
	ToolEndCombat:   endCombatAction{},
	ToolStartChase:  startChaseAction{},
	ToolChaseAct:    chaseActAction{},
	ToolEndChase:    endChaseAction{},
}

// ── Rule / lookup actions ─────────────────────────────────────────────────────

type checkRuleAction struct{}

func (checkRuleAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d check_rule q=%s", actx.Sid, call.Question)
	doneL := timedDebug("Lawyer", "session=%d question=%s", actx.Sid, call.Question)
	results := runLawyer(actx.Ctx, actx.Handles[models.AgentRoleLawyer], call.Question)
	doneL()
	debugf("tool", "session=%d check_rule result=%s", actx.Sid, formatLawyerResults(results))
	return []ToolResult{{Action: ToolCheckRule, Result: formatLawyerResults(results)}}
}

// consultDramaturgAction 将Director汇报的进度转交给剧构顾问，获取节奏/走向指导。脱敏三层中的
// 代码层在这里落地：即使Director不小心写入了真实姓名，也会在传给剧构顾问前被替换掉。
type consultDramaturgAction struct{}

func (consultDramaturgAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	handle, ok := actx.Handles[models.AgentRoleDramaturg]
	if !ok || !handle.isEnabled() {
		debugf("tool", "session=%d consult_dramaturg unavailable: dramaturg disabled", actx.Sid)
		return []ToolResult{{Action: ToolConsultDramaturg, Result: "dramaturg unavailable"}}
	}
	note := strings.TrimSpace(call.ProgressNote)
	if note == "" {
		return []ToolResult{{Action: ToolConsultDramaturg, Result: "consult_dramaturg failed: progress_note is required"}}
	}
	if actx.GCtx != nil {
		note = sanitizeDramaturgNote(note, actx.GCtx.Session.Players)
	}
	debugf("tool", "session=%d consult_dramaturg note=%s", actx.Sid, note)
	doneL := timedDebug("Dramaturg", "session=%d", actx.Sid)
	guidance := runDramaturg(actx.Ctx, handle, *actx.GCtx, note)
	doneL()
	debugf("tool", "session=%d consult_dramaturg result=%s", actx.Sid, guidance)
	return []ToolResult{{Action: ToolConsultDramaturg, Result: guidance}}
}

// ── Dice action ───────────────────────────────────────────────────────────────

type rollDiceAction struct{}

func (rollDiceAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	if call.Dice == nil {
		return nil
	}
	dcr := executeSingleDiceCheck(*call.Dice, actx.GCtx.Session.Players)
	debugf("tool", "session=%d roll_dice result=%s", actx.Sid, formatSingleDiceResult(dcr))
	if dcr.Roll == 0 {
		return []ToolResult{{Action: ToolRollDice, Result: fmt.Sprintf("不支持的表达式: %v", call.Dice.DiceExpr)}}
	}
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
		question = question + "\n【KP私下给你的指令(最高优先级，其他玩家看不到)】" + call.KPDirective
	}
	debugf("tool", "session=%d act_npc npc=%q question=%s nsfw=%v", actx.Sid, call.NPCName, question, call.NSFW)
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
	npcHandle, nsfwMode := pickNPCHandle(actx.Handles, actx.GCtx.Session.EnableNSFW && call.NSFW)
	action, npcErr := actNPC(actx.Ctx, npcHandle, *actx.GCtx, call.NPCName, question, *actx.TempNPCs, nsfwMode)
	doneNPC()
	if npcErr != nil {
		alog.Error("act_npc failed", "npc", call.NPCName, "err", npcErr)
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
	day := gameDay(actx.GCtx.Session.TurnRound, scenarioStartSlot(actx.GCtx.Session))
	for _, p := range pending {
		if p.isPlayer {
			applyCharacterUpdate(p.upd, actx.GCtx.Session.Players, day)
		} else {
			p.upd.IsNPC = true
			applyNPCUpdate(p.upd, actx.GCtx.Session.ID, *actx.TempNPCs, actx.GCtx.Session.Scenario.Content.Data.NPCs)
		}
	}
	return []ToolResult{{Action: ToolUpdateCharacters, Result: "已更新:" + strings.Join(call.Changes, "、") + "; 如果减少了SAN注意检查疯狂条件"}}
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

type updateSessionMemoryAction struct{}

func (updateSessionMemoryAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	who := call.CharacterName
	debugf("tool", "session=%d update_session_memory char=%q memory=%q", actx.Sid, who, call.SessionMemory)
	for i := range actx.GCtx.Session.Players {
		if actx.GCtx.Session.Players[i].CharacterCard.Name == who {
			actx.GCtx.Session.Players[i].SessionMemory = call.SessionMemory
			models.DB.Save(&actx.GCtx.Session.Players[i])
			return []ToolResult{{Action: ToolUpdateSessionMemory, Result: fmt.Sprintf("已记录 %s 的会话记忆", who)}}
		}
	}
	return []ToolResult{{Action: ToolUpdateSessionMemory, Result: fmt.Sprintf("找不到名为 %s 的调查员", who)}}
}

type updateNPCSessionMemoryAction struct{}

func (updateNPCSessionMemoryAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	who := call.NPCName
	debugf("tool", "session=%d update_npc_session_memory npc=%q memory=%q", actx.Sid, who, call.SessionMemory)
	for i := range *actx.TempNPCs {
		if (*actx.TempNPCs)[i].Name == who {
			(*actx.TempNPCs)[i].SessionMemory = call.SessionMemory
			models.DB.Save(&(*actx.TempNPCs)[i])
			return []ToolResult{{Action: ToolUpdateNPCSessionMemory, Result: fmt.Sprintf("已记录 %s 的会话记忆", who)}}
		}
	}
	return []ToolResult{{Action: ToolUpdateNPCSessionMemory, Result: fmt.Sprintf("找不到名为 %s 的NPC", who)}}
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
		var sb strings.Builder
		for i, c := range clues {
			sb.WriteString(fmt.Sprintf("[Idx: %d] [%s] %s", i, c.Nature, c.Summary))
			if strings.TrimSpace(c.Source) != "" {
				sb.WriteString("（来源：" + c.Source + "）")
			}
			if strings.TrimSpace(c.SkillCheck) != "" {
				sb.WriteString("｜检定：" + c.SkillCheck)
			}
			if strings.TrimSpace(c.OnSuccess) != "" {
				sb.WriteString("｜成功：" + c.OnSuccess)
			}
			if strings.TrimSpace(c.OnFailure) != "" {
				sb.WriteString("｜失败推进：" + c.OnFailure)
			}
			sb.WriteString("\n")
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
	direction := strings.TrimSpace(call.Direction)
	if direction == "" {
		return []ToolResult{{Action: ToolWrite, Result: "SYSTEM REJECT: write.direction 不能为空，请写明这一段要展开的场景、动作和感官细节。"}}
	}
	nsfw := actx.GCtx.Session.EnableNSFW && call.NSFW
	*actx.Outbox = append(*actx.Outbox, Output{Kind: OutputWriter, Text: direction, NSFW: nsfw})
	debugf("tool", "session=%d write direction=%s nsfw=%v", actx.Sid, direction, nsfw)
	return []ToolResult{{Action: ToolWrite, Result: "已作为一条独立的玩家可见消息发布（正文由 Writer 展开）。你仍持有发言权。"}}
}

// narrateKPStreakReminder 是连续发布这么多条 kp 消息仍没有点名时附在 narrate 结果后的提醒(只提醒,不强制)。
const narrateKPStreakReminder = 4

type narrateAction struct{}

func (narrateAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	out := Output{Kind: OutputKP, Text: strings.TrimSpace(call.Reply)}
	*actx.Outbox = append(*actx.Outbox, out)
	debugf("tool", "session=%d narrate reply=%s", actx.Sid, out.Text)

	result := "已发给玩家。你仍持有发言权：继续推进，或用 ask_players 点名调查员交出发言权，或用 end_game 结束游戏。"
	if actx.KPStreak != nil {
		*actx.KPStreak++
		if *actx.KPStreak >= narrateKPStreakReminder {
			result += fmt.Sprintf("\n提醒：你已连续发布 %d 条回复而没有点名任何调查员。场景需要调查员做决定、声明行动或回答时，请用 ask_players 点名交出发言权，不要继续独自推进。", *actx.KPStreak)
		}
	}
	return []ToolResult{{Action: ToolNarrate, Result: result}}
}

// askPlayersAction 把名字解析成玩家并交出发言权;任一名字不存在或角色已死整批拒绝,
// 让Director用正确的名字重发,避免静默点错人把房间卡死。
type askPlayersAction struct{}

func (askPlayersAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	reject := func(msg string) []ToolResult {
		return []ToolResult{{Action: ToolAskPlayers, Result: "SYSTEM REJECT: " + msg}}
	}
	question := strings.TrimSpace(call.Question)
	if question == "" {
		return reject("ask_players.question 不能为空，请写明需要被点名者决定或回应什么。")
	}

	players := actx.GCtx.Session.Players
	alive := func(p models.SessionPlayer) bool {
		return isCharacterAlive(p.CharacterCard.Name, false, players, nil)
	}
	var aliveNames []string
	for _, p := range players {
		if alive(p) {
			aliveNames = append(aliveNames, p.CharacterCard.Name)
		}
	}

	seen := map[uint]bool{}
	var ids []uint
	var names []string
	add := func(p models.SessionPlayer) {
		if seen[p.UserID] {
			return
		}
		seen[p.UserID] = true
		ids = append(ids, p.UserID)
		names = append(names, p.CharacterCard.Name)
	}

	var unknown, dead []string
	for _, target := range call.Targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		if strings.EqualFold(target, "all") {
			for _, p := range players {
				if alive(p) {
					add(p)
				}
			}
			continue
		}
		found := false
		for _, p := range players {
			if p.CharacterCard.Name != target {
				continue
			}
			found = true
			if alive(p) {
				add(p)
			} else {
				dead = append(dead, target)
			}
			break
		}
		if !found {
			unknown = append(unknown, target)
		}
	}
	if len(unknown) > 0 || len(dead) > 0 {
		var parts []string
		if len(unknown) > 0 {
			parts = append(parts, fmt.Sprintf("找不到调查员 %s", strings.Join(unknown, "、")))
		}
		if len(dead) > 0 {
			parts = append(parts, fmt.Sprintf("调查员 %s 已死亡", strings.Join(dead, "、")))
		}
		return reject(fmt.Sprintf("%s。targets 必须是存活调查员的角色名(当前存活：%s)或 [\"all\"]，请修正后重发。", strings.Join(parts, "；"), strings.Join(aliveNames, "、")))
	}

	// 遭遇中卡在待澄清暂停的PC必须被点名，否则冻结的攻击方永远等不到回答。
	var merged []string
	mergePending := func(name string, isNPC, pending bool, userID uint) {
		if isNPC || !pending {
			return
		}
		for _, p := range players {
			if (userID != 0 && p.UserID == userID) || p.CharacterCard.Name == name {
				if alive(p) && !seen[p.UserID] {
					add(p)
					merged = append(merged, p.CharacterCard.Name)
				}
				return
			}
		}
	}
	if actx.Combat != nil && *actx.Combat != nil && (*actx.Combat).Active {
		for _, p := range (*actx.Combat).Participants {
			mergePending(p.Name, p.IsNPC, p.PendingClarification, p.UserID)
		}
	}
	if actx.Chase != nil && *actx.Chase != nil && (*actx.Chase).Active {
		for _, p := range (*actx.Chase).Participants {
			mergePending(p.Name, p.IsNPC, p.PendingClarification, p.UserID)
		}
	}

	if len(ids) == 0 {
		return reject(fmt.Sprintf("ask_players.targets 没有解析出任何存活调查员，请填写角色名(当前存活：%s)或 [\"all\"]。", strings.Join(aliveNames, "、")))
	}

	*actx.HasEnd = true
	ask := &AskResult{UserIDs: ids, Names: names, Question: question}
	if actx.Ask != nil {
		*actx.Ask = ask
	}
	*actx.Outbox = append(*actx.Outbox, Output{Kind: OutputAsk, Text: question, Ask: ask, Options: normalizeAskOptions(call.Options)})
	if actx.KPStreak != nil {
		*actx.KPStreak = 0
	}
	debugf("tool", "session=%d ask_players targets=%v question=%s", actx.Sid, names, question)

	result := fmt.Sprintf("已点名：%s。发言权已交出，本周期结束，等待他们回应。", strings.Join(names, "、"))
	if len(merged) > 0 {
		result += fmt.Sprintf("(%s 正处于待澄清暂停，已自动并入点名对象。)", strings.Join(merged, "、"))
	}
	return []ToolResult{{Action: ToolAskPlayers, Result: result}}
}

// normalizeAskOptions 去空、去重并截到最多 2 条推荐行动；没有时返回 nil。
func normalizeAskOptions(raw []string) []string {
	var options []string
	seen := map[string]bool{}
	for _, opt := range raw {
		opt = strings.TrimSpace(opt)
		if opt == "" || seen[opt] {
			continue
		}
		seen[opt] = true
		options = append(options, opt)
		if len(options) >= 2 {
			break
		}
	}
	return options
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
	alog.Debug("advance time", "session", actx.GCtx.Session.ID, "rounds", rounds, "reason", reason, "game_time", formatGameTime(newRound, scenarioStartSlot(actx.GCtx.Session)))
	return []ToolResult{{
		Action: ToolAdvanceTime,
		Result: fmt.Sprintf("时间推进%d回合(每轮=30分钟)(%s),当前时间:%s", rounds, reason, formatGameTime(newRound, scenarioStartSlot(actx.GCtx.Session))),
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
			madnessType := strings.ToLower(strings.TrimSpace(call.MadnessType))
			var stateLabel string
			switch madnessType {
			case "temporary":
				stateLabel = "临时性疯狂"
			case "indefinite":
				stateLabel = "不定性疯狂"
			case "permanent":
				stateLabel = "永久性疯狂"
			default:
				return []ToolResult{{
					Action: ToolManageMadness,
					Result: fmt.Sprintf("manage_madness 缺少或无效的 madness_type(收到%q)，必须为 temporary/indefinite/permanent 之一", call.MadnessType),
				}}
			}
			symptom := game.RollMadnessSymptom(call.IsBystander)
			symptomTable := "总结症状"
			if call.IsBystander {
				symptomTable = "即时症状"
			}
			card.MadnessState = madnessType
			card.MadnessSymptom = symptom.Description
			card.MadnessDuration = symptom.Duration
			models.DB.Save(card)
			return []ToolResult{{
				Action: ToolManageMadness,
				Result: fmt.Sprintf("%s%s(%s,持续%d):%s", card.Name, stateLabel, symptomTable, symptom.Duration, symptom.Description),
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
	// NOTE: win 字段必须显式填写；缺失时拒绝执行任何副作用，让 Director 可重发。
	if call.Win == nil {
		debugf("tool", "session=%d end_game rejected: win field missing", actx.Sid)
		return []ToolResult{{
			Action: ToolEndGame,
			Result: "end_game 缺少必填字段 win（true=胜利/false=失败），请重新发送并明确填写 win。",
		}}
	}
	win := *call.Win

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
	// NOTE: 先把结束语放进 outbox，再跑结算；结算失败不影响游戏已经结束这一事实。
	*actx.Outbox = append(*actx.Outbox, Output{Kind: OutputKP, Text: closing, Ended: true})
	debugf("tool", "session=%d end_game summary=%s win=%v", actx.Sid, call.EndSummary, win)

	msgs := LoadEndGameMessages(actx.GCtx.Session.ID)
	sessionSnap := actx.GCtx.Session
	// NOTE: 将 win 透传给 RunEndSession，控制是否执行成长类结算。
	if _, err := RunEndSession(actx.Ctx, &sessionSnap, msgs, win); err != nil {
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
