// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

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
	HasEnd *bool
	// Outbox 收集本轮产生玩家可见消息的调用(write/narrate/ask_players/end_game),
	// 一个调用一个 Output;轮末由 floorRun 整理顺序后发布。
	Outbox   *[]Output
	KPStreak *int // 本周期内连续发布的 kp 消息数,ask_players 之前不清零,用于节奏提醒
	// Ask 由ask_players写入;周期自然结束于点名时非nil。
	Ask **AskResult

	// Combat/Chase 是本次run()持有的活战斗/追逐状态(nil=未激活);战斗/追逐工具
	// 执行器通过它们读写、并允许置nil(start_*建立/end_*清空)。二者互斥。
	// RoundClosed是run内一次性闸门(D2):一次run最多完整推进一个战斗/追逐轮,
	// 翻页后置位,同一run内后续combat_act/chase_act一律拒绝,逼Director转去交代战况或点名。
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
	ToolRollDice:         true,
	ToolCheckRule:        true,
	ToolConsultDramaturg: true,
	ToolQueryClues:       true,
	ToolQueryCharacter:   true,
	ToolQueryNPCCard:     true,
	ToolGrepStory:        true,
	ToolReadStory:        true,
	ToolGenerateNPCName:  true,
}

// narrateCompatibleActions 表示可与narrate/end_game同批执行的动作。
// 这些动作不需要把结果再交给KP阅读后才能结束回合。
var narrateCompatibleActions = map[ToolCallType]bool{
	ToolNarrate:                true,
	ToolWhisper:                true,
	ToolRevealClue:             true,
	ToolAskPlayers:             true,
	ToolEndGame:                true,
	ToolWrite:                  true,
	ToolHint:                   true,
	ToolUpdateSessionMemory:    true,
	ToolUpdateNPCSessionMemory: true,
	ToolUpdateLocation:         true,
	ToolUpdateNPCLocation:      true,
	ToolUpdateArmor:            true,
	ToolUpdateClock:            true,
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
	ToolCreateNPC:              createNPCAction{},
	ToolGenerateNPCName:        generateNPCNameAction{},
	ToolDestroyNPC:             destroyNPCAction{},
	ToolUpdateCharacters:       updateCharactersAction{},
	ToolManageInventory:        manageInventoryAction{},
	ToolRecordMonster:          recordMonsterAction{},
	ToolManageSpell:            manageSpellAction{},
	ToolManageRelation:         manageRelationAction{},
	ToolManageAsset:            manageAssetAction{},
	ToolEndGame:                endGameAction{},
	ToolManageMadness:          manageMadnessAction{},
	ToolRealityCheck:           realityCheckAction{},
	ToolQueryClues:             queryCluesAction{},
	ToolRevealClue:             revealClueAction{},
	ToolQueryCharacter:         queryCharacterAction{},
	ToolQueryNPCCard:           queryNPCCardAction{},
	ToolGrepStory:              grepStoryAction{},
	ToolReadStory:              readStoryAction{},
	ToolUpdateNPCCard:          updateNPCCardAction{},
	ToolWrite:                  writeAction{},
	ToolAdvanceTime:            advanceTimeAction{},
	ToolUpdateSessionMemory:    updateSessionMemoryAction{},
	ToolUpdateNPCSessionMemory: updateNPCSessionMemoryAction{},
	ToolUpdateLocation:         updateLocationAction{},
	ToolUpdateNPCLocation:      updateNPCLocationAction{},
	ToolUpdateArmor:            updateArmorAction{},
	ToolUpdateClock:            updateClockAction{},
	ToolHint:                   hintAction{},
	ToolNarrate:                narrateAction{},
	ToolWhisper:                whisperAction{},
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
	return []ToolResult{{Action: ToolCheckRule, Result: formatLawyerResultsXML(results)}}
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
	return []ToolResult{{
		Action: ToolRollDice,
		Result: formatDiceResultXML(dcr),
		Events: []models.GameEvent{diceEvent(dcr, actx.GCtx.Session.Players)},
	}}
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

// NOTE: generate_npc_name 只返回姓名、不落库；已被调查员、临时NPC和模组NPC占用的姓名不会再发放。
// 中日文姓名由 Writer 角色的模型现场创作，失败时回退静态姓名池。
type generateNPCNameAction struct{}

func (generateNPCNameAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	args := generateNPCNameArgs{Culture: call.Culture, Gender: call.Gender, Count: call.Count}
	used := sessionTakenNames(actx.GCtx, *actx.TempNPCs)
	names, err := pickNPCNames(actx.Ctx, actx.Handles[models.AgentRoleWriter], fmt.Sprint(actx.Sid), args, used)
	if err != nil {
		return []ToolResult{{Action: ToolGenerateNPCName, Result: "SYSTEM REJECT: " + err.Error()}}
	}
	debugf("tool", "session=%d generate_npc_name culture=%s gender=%s names=%v", actx.Sid, call.Culture, call.Gender, names)
	return []ToolResult{{Action: ToolGenerateNPCName, Result: formatNPCNamesXML(names)}}
}

// NOTE: sessionTakenNames 汇总本局已占用的姓名（小写归一化）：调查员、临时NPC、模组NPC。
func sessionTakenNames(gctx *GameContext, tempNPCs []models.SessionNPC) map[string]bool {
	used := map[string]bool{}
	for _, p := range gctx.Session.Players {
		markNPCNamesUsed(used, p.CharacterCard.Name)
	}
	for _, npc := range tempNPCs {
		markNPCNamesUsed(used, npc.Name)
	}
	for _, npc := range gctx.Session.Scenario.Content.Data.NPCs {
		markNPCNamesUsed(used, npc.Name)
	}
	return used
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
	day := models.GameDayKey(actx.GCtx.Session.GameTime)
	var events []models.GameEvent
	for _, p := range pending {
		if p.isPlayer {
			players := actx.GCtx.Session.Players
			idx := investigatorIndex(players, p.upd.CharacterName)
			before := players[idx].CharacterCard
			applyCharacterUpdate(p.upd, players, day)
			events = append(events, cardStatEvents(before, players[idx].CharacterCard)...)
		} else {
			p.upd.IsNPC = true
			applyNPCUpdate(p.upd, actx.GCtx.Session.ID, *actx.TempNPCs, actx.GCtx.Session.Scenario.Content.Data.NPCs)
		}
	}
	return []ToolResult{{Action: ToolUpdateCharacters, Result: "已更新:" + strings.Join(call.Changes, "、") + "; 如果减少了SAN注意检查疯狂条件", Events: events}}
}

type manageInventoryAction struct{}

func (manageInventoryAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	var it models.Item
	if call.ItemName != "" {
		it = models.Item{Name: call.ItemName, Desc: call.ItemDesc, Count: max(1, call.ItemCount), Type: call.ItemType}
	} else {
		it = models.ParseItemLabel(call.Item)
		it.Type = call.ItemType
	}
	// NOTE: 物品描述会原样展示给玩家，模型有时会照抄提示词里的 [tome] 类型标记，这里统一去掉。
	it.Name = stripInternalTypeTags(it.Name)
	it.Desc = stripInternalTypeTags(it.Desc)
	players := actx.GCtx.Session.Players
	idx := investigatorIndex(players, call.CharacterName)
	if idx < 0 {
		result := manageInventory(players, call.CharacterName, call.Operate, it, call.ItemEquipped)
		return []ToolResult{{Action: ToolManageInventory, Result: result}}
	}
	before := slices.Clone(players[idx].CharacterCard.Inventory.Data)
	result := manageInventory(players, call.CharacterName, call.Operate, it, call.ItemEquipped)
	events := inventoryDiffEvents(call.CharacterName, before, players[idx].CharacterCard.Inventory.Data)
	return []ToolResult{{Action: ToolManageInventory, Result: result, Events: events}}
}

type recordMonsterAction struct{}

func (recordMonsterAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	var result string
	players := actx.GCtx.Session.Players
	events := listChangeEvents(players, call.CharacterName, "monster",
		func(p models.SessionPlayer) []listEntry { return stringEntries(p.CharacterCard.SeenMonsters.Data) },
		func() { result = manageSeenMonster(players, call.CharacterName, call.Operate, call.Monster) })
	return []ToolResult{{Action: ToolRecordMonster, Result: result, Events: events}}
}

type manageSpellAction struct{}

func (manageSpellAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	var result string
	players := actx.GCtx.Session.Players
	events := listChangeEvents(players, call.CharacterName, "spell",
		func(p models.SessionPlayer) []listEntry { return stringEntries(p.CharacterCard.Spells.Data) },
		func() { result = manageSpell(players, call.CharacterName, call.Operate, call.Spell) })
	return []ToolResult{{Action: ToolManageSpell, Result: result, Events: events}}
}

type manageRelationAction struct{}

func (manageRelationAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	var result string
	players := actx.GCtx.Session.Players
	events := listChangeEvents(players, call.CharacterName, "relation", relationEntries,
		func() { result = manageSocialRelation(players, call.CharacterName, call.Operate, call.Relation) })
	return []ToolResult{{Action: ToolManageRelation, Result: result, Events: events}}
}

type manageAssetAction struct{}

func (manageAssetAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	var result string
	players := actx.GCtx.Session.Players
	events := listChangeEvents(players, call.CharacterName, "asset",
		func(p models.SessionPlayer) []listEntry { return assetEntries(p.CharacterCard) },
		func() { result = manageAsset(players, call.CharacterName, call.Operate, call.Asset) })
	return []ToolResult{{Action: ToolManageAsset, Result: result, Events: events}}
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
			return []ToolResult{{
				Action: ToolUpdateLocation,
				Result: fmt.Sprintf("%s 当前位置已更新为: %s", who, loc),
				Events: []models.GameEvent{{Type: "location", Character: who, Location: loc}},
			}}
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
			old := actx.GCtx.Session.Players[i].Armor
			actx.GCtx.Session.Players[i].Armor = val
			models.DB.Model(&actx.GCtx.Session.Players[i]).Update("armor", val)
			var events []models.GameEvent
			if old != val {
				events = []models.GameEvent{statEvent(who, "armor", old, val)}
			}
			return []ToolResult{{Action: ToolUpdateArmor, Result: fmt.Sprintf("%s 护甲已更新为 %d点", who, val), Events: events}}
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
	var revealed []models.SessionClue
	if models.DB != nil && actx.GCtx.Session.ID != 0 {
		models.DB.Where("session_id = ?", actx.GCtx.Session.ID).Order("id ASC").Find(&revealed)
	}
	if len(clues) == 0 && len(revealed) == 0 {
		return []ToolResult{{Action: ToolQueryClues, Result: "<clues/>"}}
	}
	var sb strings.Builder
	sb.WriteString("<clues>\n")
	for i, c := range clues {
		sb.WriteString(fmt.Sprintf(`<clue idx="%d" nature="%s"`, i, xmlAttr(c.Nature)))
		for _, f := range []struct{ name, val string }{
			{"source", c.Source},
			{"check", c.SkillCheck},
			{"success", c.OnSuccess},
			{"failure", c.OnFailure},
		} {
			if s := strings.TrimSpace(f.val); s != "" {
				sb.WriteString(" " + f.name + `="` + xmlAttr(s) + `"`)
			}
		}
		sb.WriteString(">" + xmlEscape(c.Summary) + "</clue>\n")
	}
	for _, r := range revealed {
		sb.WriteString(`<revealed title="` + xmlAttr(r.Title) + `"`)
		if s := strings.TrimSpace(r.Source); s != "" {
			sb.WriteString(` source="` + xmlAttr(s) + `"`)
		}
		sb.WriteString(">" + xmlEscape(r.Content) + "</revealed>\n")
	}
	sb.WriteString("</clues>")
	return []ToolResult{{Action: ToolQueryClues, Result: sb.String()}}
}

type revealClueAction struct{}

func (revealClueAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	title := strings.TrimSpace(call.Title)
	content := strings.TrimSpace(call.Content)
	source := strings.TrimSpace(call.Source)
	if title == "" || content == "" {
		return []ToolResult{{Action: ToolRevealClue, Result: "SYSTEM REJECT: reveal_clue 必须同时提供非空的 title 和 content。"}}
	}
	if models.DB != nil && actx.GCtx.Session.ID != 0 {
		var existing models.SessionClue
		err := models.DB.Where("session_id = ? AND title = ?", actx.GCtx.Session.ID, title).First(&existing).Error
		if err == nil {
			updates := map[string]any{"content": content}
			if source != "" {
				updates["source"] = source
			}
			models.DB.Model(&existing).Updates(updates)
		} else {
			clue := models.SessionClue{
				SessionID: actx.GCtx.Session.ID,
				Title:     title,
				Content:   content,
				Source:    source,
				AddedBy:   "KP",
			}
			models.DB.Create(&clue)
		}
	}
	debugf("tool", "session=%d reveal_clue title=%q source=%q", actx.Sid, title, source)
	return []ToolResult{{
		Action: ToolRevealClue,
		Result: "已登记线索至全队线索板：" + title,
		Events: []models.GameEvent{{Type: "clue", Name: title, Detail: content}},
	}}
}

type updateClockAction struct{}

func (updateClockAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	reject := func(msg string) []ToolResult {
		return []ToolResult{{Action: ToolUpdateClock, Result: "SYSTEM REJECT: " + msg}}
	}
	name := strings.TrimSpace(call.ClockName)
	if name == "" {
		return reject("update_clock 必须提供非空的 clock_name。")
	}
	op := strings.ToLower(strings.TrimSpace(call.Operate))
	if op == "" {
		op = "advance"
	}
	if op != "advance" && op != "set" && op != "remove" {
		return reject(fmt.Sprintf("update_clock 的 operate=%q 无效，仅支持 advance/set/remove。", call.Operate))
	}

	session := &actx.GCtx.Session
	clocks := append([]models.SessionClock(nil), session.Clocks.Data...)
	idx := -1
	for i := range clocks {
		if strings.TrimSpace(clocks[i].Name) == name {
			idx = i
			break
		}
	}

	// NOTE: 查找同名模组机制以自动补全默认格数、说明与阶段名。
	var mech *models.MechanicData
	for i := range session.Scenario.Content.Data.Mechanics {
		m := &session.Scenario.Content.Data.Mechanics[i]
		if strings.TrimSpace(m.Name) == name {
			mech = m
			break
		}
	}

	saveClocks := func(next []models.SessionClock) {
		session.Clocks = models.JSONField[[]models.SessionClock]{Data: next}
		if models.DB != nil && session.ID != 0 {
			models.DB.Model(&models.GameSession{}).Where("id = ?", session.ID).Update("clocks", session.Clocks)
		}
	}

	if op == "remove" {
		if idx < 0 {
			return reject(fmt.Sprintf("未找到名为「%s」的危机时钟。", name))
		}
		removed := clocks[idx]
		next := append(clocks[:idx], clocks[idx+1:]...)
		saveClocks(next)
		var events []models.GameEvent
		if removed.Visible {
			events = []models.GameEvent{{Type: "clock", Op: "remove", Name: removed.Name}}
		}
		debugf("tool", "session=%d update_clock op=remove name=%q visible=%v", actx.Sid, removed.Name, removed.Visible)
		return []ToolResult{{
			Action: ToolUpdateClock,
			Result: fmt.Sprintf(`<clock op="remove" name="%s"/>`, xmlAttr(removed.Name)),
			Events: events,
		}}
	}

	clampMax := func(v int) int {
		if v < 1 {
			return 1
		}
		if v > 20 {
			return 20
		}
		return v
	}

	var cur models.SessionClock
	existed := idx >= 0
	wasVisible := false
	prevVal := 0
	if existed {
		cur = clocks[idx]
		wasVisible = cur.Visible
		prevVal = cur.Current
		if call.ClockMax > 0 {
			cur.Max = clampMax(call.ClockMax)
		}
		if desc := strings.TrimSpace(call.ClockDesc); desc != "" {
			cur.Description = desc
		}
		if call.ClockVisible != nil {
			cur.Visible = *call.ClockVisible
		}
	} else {
		maxVal := 6
		if call.ClockMax > 0 {
			maxVal = clampMax(call.ClockMax)
		} else if mech != nil && len(mech.Stages) > 0 {
			maxVal = clampMax(len(mech.Stages))
		}
		desc := strings.TrimSpace(call.ClockDesc)
		if desc == "" && mech != nil {
			desc = strings.TrimSpace(mech.Description)
		}
		visible := true
		if call.ClockVisible != nil {
			visible = *call.ClockVisible
		}
		cur = models.SessionClock{
			Name:        name,
			Current:     0,
			Max:         maxVal,
			Description: desc,
			Visible:     visible,
		}
	}

	if op == "set" {
		target := 0
		if call.ClockValue != nil {
			target = *call.ClockValue
		}
		cur.Current = target
	} else {
		delta := 1
		if call.ClockDelta != nil {
			delta = *call.ClockDelta
		}
		cur.Current += delta
	}
	if cur.Current < 0 {
		cur.Current = 0
	}
	if cur.Current > cur.Max {
		cur.Current = cur.Max
	}

	if stage := strings.TrimSpace(call.ClockStage); stage != "" {
		cur.Stage = stage
	} else if mech != nil && len(mech.Stages) > 0 {
		if cur.Current > 0 && cur.Current <= len(mech.Stages) {
			cur.Stage = strings.TrimSpace(mech.Stages[cur.Current-1].Label)
		} else if cur.Current == 0 {
			cur.Stage = ""
		}
	}

	if existed {
		clocks[idx] = cur
	} else {
		clocks = append(clocks, cur)
	}
	saveClocks(clocks)

	var events []models.GameEvent
	if cur.Visible {
		evOp := "update"
		if !existed || !wasVisible {
			evOp = "create"
		}
		fromVal, toVal := prevVal, cur.Current
		events = []models.GameEvent{{
			Type:   "clock",
			Op:     evOp,
			Name:   cur.Name,
			From:   &fromVal,
			To:     &toVal,
			Amount: cur.Max,
			Detail: cur.Stage,
		}}
	} else if wasVisible {
		// NOTE: 从公开时钟切换为暗钟时通知前端刷新手册，避免残留旧进度条。
		events = []models.GameEvent{{Type: "clock", Op: "remove", Name: cur.Name}}
	}

	visLabel := "公开"
	if !cur.Visible {
		visLabel = "暗钟(仅KP可见)"
	}
	fullNote := ""
	if cur.Current >= cur.Max {
		fullNote = ` full="true"`
	}
	stageAttr := ""
	if cur.Stage != "" {
		stageAttr = fmt.Sprintf(` stage="%s"`, xmlAttr(cur.Stage))
	}
	debugf("tool", "session=%d update_clock op=%s name=%q %d->%d/%d visible=%v", actx.Sid, op, cur.Name, prevVal, cur.Current, cur.Max, cur.Visible)
	return []ToolResult{{
		Action: ToolUpdateClock,
		Result: fmt.Sprintf(`<clock op="%s" name="%s" current="%d" max="%d" visibility="%s"%s%s/>`,
			op, xmlAttr(cur.Name), cur.Current, cur.Max, visLabel, stageAttr, fullNote),
		Events: events,
	}}
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
	*actx.Outbox = append(*actx.Outbox, Output{Kind: OutputWriter, Text: direction, NSFW: nsfw, CallID: call.CallID})
	debugf("tool", "session=%d write direction=%s nsfw=%v", actx.Sid, direction, nsfw)
	return []ToolResult{{Action: ToolWrite, Result: "已入队：Writer 正在后台按 direction 写正文并同步给玩家，写完后正文会以 <writer_output> 提醒追加到对话里。在那之前你不知道正文的具体写法，不要复述或猜测；你仍持有发言权。"}}
}

// narrateKPStreakReminder 是连续发布这么多条 kp 消息仍没有点名时附在 narrate 结果后的提醒(只提醒,不强制)。
const narrateKPStreakReminder = 4

type narrateAction struct{}

func (narrateAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	out := Output{Kind: OutputKP, Text: strings.TrimSpace(call.Reply), CallID: call.CallID}
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
// 让Director用正确的名字重发,避免静默点错人把房间卡死。一次只能点一个人。
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
		return reject(fmt.Sprintf("%s。targets 必须是一位存活调查员的角色名(当前存活：%s)，请修正后重发。", strings.Join(parts, "；"), strings.Join(aliveNames, "、")))
	}

	if len(ids) > 1 {
		return reject(fmt.Sprintf("一次只能点名一个人，targets 解析出了 %d 人(%s)。请选当前最该回应的一位重发，其他人等他回应后再点名。", len(ids), strings.Join(names, "、")))
	}

	// 遭遇中卡在待澄清暂停的PC必须被点名，否则冻结的攻击方永远等不到回答。
	// 同时有多人待澄清时点其中任一位即可，其余的留到下个周期再问。
	pendingIDs := map[uint]bool{}
	var pendingNames []string
	collectPending := func(name string, isNPC, isPending bool, userID uint) {
		if isNPC || !isPending {
			return
		}
		for _, p := range players {
			if (userID != 0 && p.UserID == userID) || p.CharacterCard.Name == name {
				if alive(p) && !pendingIDs[p.UserID] {
					pendingIDs[p.UserID] = true
					pendingNames = append(pendingNames, p.CharacterCard.Name)
				}
				return
			}
		}
	}
	if actx.Combat != nil && *actx.Combat != nil && (*actx.Combat).Active {
		for _, p := range (*actx.Combat).Participants {
			collectPending(p.Name, p.IsNPC, p.PendingClarification, p.UserID)
		}
	}
	if actx.Chase != nil && *actx.Chase != nil && (*actx.Chase).Active {
		for _, p := range (*actx.Chase).Participants {
			collectPending(p.Name, p.IsNPC, p.PendingClarification, p.UserID)
		}
	}
	if len(pendingNames) > 0 && (len(ids) == 0 || !pendingIDs[ids[0]]) {
		return reject(fmt.Sprintf("战斗/追逐中调查员 %s 正处于待澄清暂停，本次必须点名其中一位(一次只能点一个人)，question 问出待澄清的问题。", strings.Join(pendingNames, "、")))
	}

	if len(ids) == 0 {
		return reject(fmt.Sprintf("ask_players.targets 没有解析出任何存活调查员，请填写一位存活调查员的角色名(当前存活：%s)。", strings.Join(aliveNames, "、")))
	}

	*actx.HasEnd = true
	ask := &AskResult{UserIDs: ids, Names: names, Question: question}
	if actx.Ask != nil {
		*actx.Ask = ask
	}
	*actx.Outbox = append(*actx.Outbox, Output{Kind: OutputAsk, Text: question, Ask: ask, Options: normalizeAskOptions(call.Options), CallID: call.CallID})
	if actx.KPStreak != nil {
		*actx.KPStreak = 0
	}
	debugf("tool", "session=%d ask_players targets=%v question=%s", actx.Sid, names, question)

	result := fmt.Sprintf("已点名：%s。发言权已交出，本周期结束，等待他回应。", names[0])
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

// whisperAction 向单个存活调查员发送仅其本人可见的密讯；名字不存在或角色已死时拒绝。
type whisperAction struct{}

func (whisperAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	reject := func(msg string) []ToolResult {
		return []ToolResult{{Action: ToolWhisper, Result: "SYSTEM REJECT: " + msg}}
	}
	target := strings.TrimSpace(call.CharacterName)
	content := strings.TrimSpace(call.Content)
	if target == "" {
		return reject("whisper.character_name 不能为空，请填一位存活调查员的角色名。")
	}
	if content == "" {
		return reject("whisper.content 不能为空，请写明要发给该调查员的密讯内容。")
	}

	players := actx.GCtx.Session.Players
	var aliveNames []string
	var matched *models.SessionPlayer
	for i := range players {
		p := &players[i]
		alive := isCharacterAlive(p.CharacterCard.Name, false, players, nil)
		if alive {
			aliveNames = append(aliveNames, p.CharacterCard.Name)
		}
		if p.CharacterCard.Name == target {
			matched = p
		}
	}
	if matched == nil {
		return reject(fmt.Sprintf("找不到调查员 %s。character_name 必须是一位存活调查员的角色名(当前存活：%s)。", target, strings.Join(aliveNames, "、")))
	}
	if !isCharacterAlive(matched.CharacterCard.Name, false, players, nil) {
		return reject(fmt.Sprintf("调查员 %s 已死亡，无法接收密讯。", target))
	}

	*actx.Outbox = append(*actx.Outbox, Output{
		Kind:          OutputKP,
		Text:          content,
		VisibleTo:     []uint{matched.UserID},
		RecipientName: matched.CharacterCard.Name,
		Hallucination: call.IsHallucination,
		CallID:        call.CallID,
	})
	debugf("tool", "session=%d whisper to=%s user=%d hallucination=%v", actx.Sid, matched.CharacterCard.Name, matched.UserID, call.IsHallucination)
	resMsg := fmt.Sprintf("已向「%s」发送密讯（仅该调查员可见）。你仍持有发言权。", matched.CharacterCard.Name)
	if call.IsHallucination {
		resMsg = fmt.Sprintf("已向「%s」发送幻觉耳语（仅该调查员可见；对局中不会向玩家暴露幻觉标记，仅在结团复盘时揭晓）。你仍持有发言权。", matched.CharacterCard.Name)
	}
	return []ToolResult{{
		Action: ToolWhisper,
		Result: resMsg,
	}}
}

// ── Time action ───────────────────────────────────────────────────────────────

type advanceTimeAction struct{}

func (advanceTimeAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	session := &actx.GCtx.Session
	reject := func(msg string) []ToolResult {
		return []ToolResult{{Action: ToolAdvanceTime, Result: "SYSTEM REJECT: " + msg}}
	}
	newTime, err := models.NormalizeGameTime(call.GameTime)
	if err != nil {
		return reject(fmt.Sprintf("advance_time 的 game_time 必须是 RFC 3339 本地时间，格式 YYYY-MM-DDTHH:MM，不带时区，例如 1924-09-03T21:30。收到: %q。", call.GameTime))
	}
	newT, _ := models.ParseGameTime(newTime)
	var delta time.Duration
	if cur, err := models.ParseGameTime(session.GameTime); err == nil {
		if newT.Before(cur) {
			return reject(fmt.Sprintf("时间不能倒退：当前时间是 %s，收到的 %s 更早。请写不早于当前时间的绝对时间。", session.GameTime, newTime))
		}
		delta = newT.Sub(cur)
	}
	updates := map[string]any{"game_time": newTime}
	if session.GameStartTime == "" {
		updates["game_start_time"] = newTime
	}
	models.DB.Model(&models.GameSession{}).Where("id = ?", session.ID).Updates(updates)
	if session.GameStartTime == "" {
		session.GameStartTime = newTime
	}
	session.GameTime = newTime
	reason := call.TimeReason
	if reason == "" {
		reason = "时间推进"
	}
	alog.Debug("advance time", "session", session.ID, "game_time", newTime, "advanced", delta, "reason", reason)
	// NOTE: 事件不带 reason：那是 KP 自己写的文字，可能透露剧情。
	return []ToolResult{{
		Action: ToolAdvanceTime,
		Result: fmt.Sprintf(`<time_advanced now="%s" delta="%s" reason="%s"/>`, xmlAttr(describeGameTime(session.GameStartTime, newTime)), xmlAttr(formatGameDuration(delta)), xmlAttr(reason)),
		Events: []models.GameEvent{{Type: "time", Time: newTime}},
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
				Result: fmt.Sprintf(`<madness character="%s" state="none"/>`, xmlAttr(card.Name)),
				Events: []models.GameEvent{{Type: "madness", Character: card.Name, State: "none"}},
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
				Result: fmt.Sprintf(`<madness character="%s" state="%s" label="%s" table="%s" duration="%d">%s</madness>`,
					xmlAttr(card.Name), madnessType, stateLabel, symptomTable, symptom.Duration, xmlEscape(symptom.Description)),
				Events: []models.GameEvent{{Type: "madness", Character: card.Name, State: madnessType, Symptom: symptom.Description, Duration: symptom.Duration}},
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

// realityCheckRoll 独立封装 1D100 掷骰，便于单测确定性覆盖边界值。
var realityCheckRoll = func() int {
	total, _ := game.Roll(1, 100)
	return total
}

// realityCheckAction 对处于临时性或不定性疯狂（潜伏疯狂期）的调查员执行 COC7 现实检定。
// 成功（1D100 ≤ SAN）识破幻觉不损 SAN；失败扣 1 点 SAN（更新今日累计损失）并提醒 KP 触发新一轮疯狂发作。
type realityCheckAction struct{}

func (realityCheckAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	reject := func(msg string) []ToolResult {
		return []ToolResult{{Action: ToolRealityCheck, Result: "SYSTEM REJECT: " + msg}}
	}
	target := strings.TrimSpace(call.CharacterName)
	if target == "" {
		return reject("reality_check.character_name 不能为空，请填一位存活调查员的角色名。")
	}

	players := actx.GCtx.Session.Players
	var matched *models.SessionPlayer
	var aliveNames []string
	for i := range players {
		p := &players[i]
		if isCharacterAlive(p.CharacterCard.Name, false, players, nil) {
			aliveNames = append(aliveNames, p.CharacterCard.Name)
		}
		if p.CharacterCard.Name == target {
			matched = p
		}
	}
	if matched == nil {
		return reject(fmt.Sprintf("找不到调查员 %s。当前存活：%s。", target, strings.Join(aliveNames, "、")))
	}
	if !isCharacterAlive(matched.CharacterCard.Name, false, players, nil) {
		return reject(fmt.Sprintf("调查员 %s 已死亡，无法进行现实检定。", target))
	}

	card := &matched.CharacterCard
	switch card.MadnessState {
	case "temporary", "indefinite":
	case "permanent":
		return reject(fmt.Sprintf("调查员 %s 已陷入永久性疯狂，无法通过现实检定恢复理智认知。", card.Name))
	default:
		return reject(fmt.Sprintf("调查员 %s 当前未处于临时性或不定性疯狂（潜伏疯狂期），无需进行现实检定。", card.Name))
	}

	beforeSAN := card.Stats.Data.SAN
	roll := realityCheckRoll()
	reason := strings.TrimSpace(call.Reason)
	debugf("tool", "session=%d reality_check char=%q roll=%d target=%d reason=%q", actx.Sid, card.Name, roll, beforeSAN, reason)

	if roll <= beforeSAN {
		ev := models.GameEvent{
			Type:      "reality_check",
			Character: card.Name,
			Roll:      roll,
			Target:    beforeSAN,
			Result:    "成功",
			Detail:    reason,
		}
		return []ToolResult{{
			Action: ToolRealityCheck,
			Result: fmt.Sprintf(`<reality_check character="%s" roll="%d" target="%d" passed="true" san="%d"/>`+"\n现实检定成功：调查员识破了眼前的幻觉，不损失理智。请清楚揭示现实真相。",
				xmlAttr(card.Name), roll, beforeSAN, beforeSAN),
			Events: []models.GameEvent{ev},
		}}
	}

	// NOTE: COC7 现实检定失败：扣除 1 点 SAN 并累计当日损失，触发新一轮疯狂发作。
	stats := card.Stats.Data
	afterSAN := clamp(beforeSAN-1, 0, stats.MaxSAN)
	stats.SAN = afterSAN
	card.Stats.Data = stats
	if beforeSAN > afterSAN {
		day := models.GameDayKey(actx.GCtx.Session.GameTime)
		if card.DailySanLossDay != day {
			card.DailySanLossDay = day
			card.DailySanLoss = 0
		}
		card.DailySanLoss += beforeSAN - afterSAN
	}
	if models.DB != nil && card.ID != 0 {
		models.DB.Save(card)
	}

	events := []models.GameEvent{{
		Type:      "reality_check",
		Character: card.Name,
		Roll:      roll,
		Target:    beforeSAN,
		Result:    "失败",
		Detail:    reason,
	}}
	if afterSAN != beforeSAN {
		fromVal, toVal := beforeSAN, afterSAN
		events = append(events, models.GameEvent{
			Type:      "stat",
			Character: card.Name,
			Field:     "san",
			From:      &fromVal,
			To:        &toVal,
			Detail:    "现实检定失败",
		})
	}

	threshold := stats.MaxSAN / 5
	note := "现实检定失败：调查员深信幻觉为真，已扣除 1 点 SAN。按 COC7 规则，现实检定失败会立刻触发新一轮疯狂发作，请调用 manage_madness(trigger) 结算发作症状。"
	if afterSAN == 0 {
		note = "现实检定失败：已扣除 1 点 SAN 且 SAN 归零，请立刻调用 manage_madness(trigger, madness_type=\"permanent\") 结算永久性疯狂。"
	} else if threshold > 0 && card.DailySanLoss >= threshold && card.MadnessState != "indefinite" {
		note = fmt.Sprintf("现实检定失败：已扣除 1 点 SAN，今日累计理智损失已达不定性疯狂阈值(%d)，请调用 manage_madness(trigger, madness_type=\"indefinite\") 结算新一轮疯狂发作。", threshold)
	}
	return []ToolResult{{
		Action: ToolRealityCheck,
		Result: fmt.Sprintf(`<reality_check character="%s" roll="%d" target="%d" passed="false" san_before="%d" san_after="%d" daily_san_loss="%d" indefinite_threshold="%d"/>`+"\n%s",
			xmlAttr(card.Name), roll, beforeSAN, beforeSAN, afterSAN, card.DailySanLoss, threshold, note),
		Events: events,
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

	// NOTE: 结算必须在任何副作用之前校验，不合格时让 Director 补全后重发，游戏不会结束。
	if len(actx.GCtx.Session.Players) == 0 {
		models.DB.Preload("CharacterCard").
			Where("session_id = ?", actx.GCtx.Session.ID).
			Find(&actx.GCtx.Session.Players)
	}
	if reject := validateSettlement(actx.GCtx.Session.Players, call.Settlement); reject != "" {
		debugf("tool", "session=%d end_game rejected: %s", actx.Sid, reject)
		return []ToolResult{{Action: ToolEndGame, Result: reject}}
	}

	var results []ToolResult
	if call.EndSummary != "" {
		results = append(results, ToolResult{Action: ToolEndGame, Result: "剧本结局:" + call.EndSummary})
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
	*actx.Outbox = append(*actx.Outbox, Output{Kind: OutputKP, Text: closing, Ended: true, CallID: call.CallID})
	debugf("tool", "session=%d end_game summary=%s win=%v", actx.Sid, call.EndSummary, win)

	msgs := LoadEndGameMessages(actx.GCtx.Session.ID)
	sessionSnap := actx.GCtx.Session
	events, err := RunEndSessionWithEnding(actx.Ctx, &sessionSnap, msgs, win, call.Settlement, call.EndingName, call.EndSummary)
	if err != nil {
		debugf("tool", "session=%d RunEndSession error: %v", actx.GCtx.Session.ID, err)
	}
	if len(events) > 0 {
		results = append(results, ToolResult{Action: ToolEndGame, Result: "结算已完成。", Events: events})
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
