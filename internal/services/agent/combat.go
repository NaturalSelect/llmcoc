// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/llmcoc/server/internal/models"
)

// ── Combat state helpers ──────────────────────────────────────────────────────

// saveCombatState persists the CombatState JSON column on GameSession.
// Pass nil to clear an ended combat.
func saveCombatState(sessionID uint, cs *models.CombatState) {
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", sessionID).
		Update("combat_state", models.JSONField[*models.CombatState]{Data: cs})
}

// buildCombatState initialises a new CombatState from KP-provided participant inputs.
// PC 的 DEX 权威始终来自角色卡(防止模型幻觉瞎报数字);模型提供的PC dex/hp一律丢弃。
// NPC 的 DEX 优先取 TempNPCs 中的真实值,找不到才退回模型提供的数值。
// Participants 按DEX降序排列,DEX相同按CombatSkill降序,再相同保持输入顺序。
func buildCombatState(inputs []CombatParticipantInput, actx ActionContext) (models.CombatState, error) {
	parts := make([]models.CombatParticipant, 0, len(inputs))
	var missing []string
	for _, inp := range inputs {
		name := strings.TrimSpace(inp.Name)
		if name == "" {
			continue
		}
		p := models.CombatParticipant{
			Name:        name,
			IsNPC:       inp.IsNPC,
			CombatSkill: inp.CombatSkill,
			DEX:         inp.DEX,
		}
		if inp.IsNPC {
			for _, npc := range *actx.TempNPCs {
				if npcNameMatch(npc.Name, name) {
					if dex := npc.Stats.Data["DEX"]; dex > 0 {
						p.DEX = dex
					}
					break
				}
			}
		} else {
			found := false
			for _, pl := range actx.GCtx.Session.Players {
				if pl.CharacterCard.Name == name {
					p.DEX = pl.CharacterCard.Stats.Data.DEX
					p.UserID = pl.UserID
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, name)
				continue
			}
		}
		parts = append(parts, p)
	}
	if len(missing) > 0 {
		return models.CombatState{}, fmt.Errorf("找不到调查员:%s;房间内真实角色名为:%s",
			strings.Join(missing, "、"), playerCharacterNames(actx.GCtx.Session.Players))
	}
	if len(parts) == 0 {
		return models.CombatState{}, fmt.Errorf("start_combat 至少需要一名参战者")
	}
	sort.SliceStable(parts, func(i, j int) bool {
		if parts[i].DEX != parts[j].DEX {
			return parts[i].DEX > parts[j].DEX
		}
		return parts[i].CombatSkill > parts[j].CombatSkill
	})
	return models.CombatState{
		Active:       true,
		Round:        1,
		Participants: parts,
		ActorIndex:   0,
	}, nil
}

// playerCharacterNames 返回房间内所有调查员的真实角色名,用于start_combat/start_chase
// 报错时提示模型改用正确名字,而不是让它凭空猜测。
func playerCharacterNames(players []models.SessionPlayer) string {
	names := make([]string, 0, len(players))
	for _, p := range players {
		if name := strings.TrimSpace(p.CharacterCard.Name); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "(无)"
	}
	return strings.Join(names, "、")
}

// findCombatParticipant 按名字精确查找参战者,找不到返回nil。
func findCombatParticipant(cs *models.CombatState, name string) *models.CombatParticipant {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	for i := range cs.Participants {
		if cs.Participants[i].Name == name {
			return &cs.Participants[i]
		}
	}
	return nil
}

// isCharacterAlive 判断PC/NPC是否仍存活:PC看角色卡WoundState,NPC看NPC卡IsAlive/WoundState。
// 供combat_act/chase_act翻页、以及BuildTurnCollection判断参战者是否还能继续行动;
// 多处状态机共用,只依赖players/tempNPCs而不需要完整GameContext。
func isCharacterAlive(name string, isNPC bool, players []models.SessionPlayer, tempNPCs []models.SessionNPC) bool {
	if isNPC {
		for _, npc := range tempNPCs {
			if npc.Name == name {
				return npcCompactState(npc) != "dead"
			}
		}
		return true // 找不到NPC卡时保守认为存活(可能刚创建/尚未同步)
	}
	for _, pl := range players {
		if pl.CharacterCard.Name == name {
			return pl.CharacterCard.WoundState != "dead"
		}
	}
	return true
}

// findPlayerIntent 在本轮已收集的玩家意图里,按UserID(优先)或角色名找到某个PC本轮提交的原文。
// 找不到返回空串(单人房PendingActions为空属正常情况,调用方应静默跳过)。
func findPlayerIntent(pendingActions []PlayerAction, userID uint, charName string) string {
	for _, pa := range pendingActions {
		if pa.IsAdmin {
			continue
		}
		if userID != 0 && pa.UserID == userID {
			return pa.Content
		}
		if pa.PlayerName == charName {
			return pa.Content
		}
	}
	return ""
}

// hasDeclaration 判断某PC本轮是否已经提交过行动声明。与findPlayerIntent不同,这里不
// 跳过管理员输入——管理员同时是参战玩家时,若被跳过会被声明可见性闸门永久锁死。
// 供combat_act/chase_act的声明可见性闸门使用:轮到的PC没有声明就拒绝推进。
func hasDeclaration(pendingActions []PlayerAction, userID uint, charName string) bool {
	for _, pa := range pendingActions {
		if userID != 0 && pa.UserID == userID {
			return true
		}
		if pa.PlayerName == charName {
			return true
		}
	}
	return false
}

// nextCombatActorIndex 从当前ActorIndex起,按DEX序找下一个"本轮未行动且存活"的参战者。
// 全部行动完或全部阵亡/离场时返回-1,表示本战斗轮已结算完毕。
func nextCombatActorIndex(cs *models.CombatState, gctx GameContext, tempNPCs []models.SessionNPC) int {
	n := len(cs.Participants)
	for step := 1; step <= n; step++ {
		idx := (cs.ActorIndex + step) % n
		p := cs.Participants[idx]
		if p.HasActed {
			continue
		}
		if !isCharacterAlive(p.Name, p.IsNPC, gctx.Session.Players, tempNPCs) {
			continue
		}
		return idx
	}
	return -1
}

// applyCombatAct 结算战斗轮中一名行动者的行动,并推进/翻页ActorIndex/Round。
// roundClosed 是本次run()内的一次性闸门(D2):一次run内战斗轮最多完整推进一轮,
// 翻页后立即置位,同一run内后续combat_act一律拒绝,逼Director用response收尾。
func applyCombatAct(cs *models.CombatState, call ToolCall, actx ActionContext, roundClosed *bool) string {
	if *roundClosed {
		return "SYSTEM REJECT: 本会话轮的战斗轮已经结算完毕,请立即调用response收尾,不要继续调用combat_act。"
	}
	if len(cs.Participants) == 0 {
		return "错误:当前没有战斗参与者"
	}

	actorName := strings.TrimSpace(call.CombatActorName)
	current := &cs.Participants[cs.ActorIndex]
	if actorName != current.Name {
		return fmt.Sprintf("SYSTEM REJECT: 战斗轮行动顺序由后端强制,当前应轮到 %s(DEX%d) 行动,而不是 %s。请按<combat_state>给出的顺序调用combat_act。",
			current.Name, current.DEX, actorName)
	}

	// 声明可见性闸门(D3):轮到的是PC但本轮还没提交行动声明时拒绝推进——按批次收集后,
	// 这种情况意味着该PC所在的批次还没交齐,不应该靠Director瞎编一个行动替玩家决定。
	if !current.IsNPC && !hasDeclaration(actx.GCtx.PendingActions, current.UserID, current.Name) {
		return fmt.Sprintf("SYSTEM REJECT: 当前行动者 %s 本轮尚未提交行动声明,请立即调用response收尾,在回复中点名等待%s行动。",
			current.Name, current.Name)
	}

	act := call.CombatAction
	if act == nil {
		return fmt.Sprintf("错误:combat_act缺少combat_action字段(行动者:%s)", actorName)
	}

	// 待澄清暂停(D1): 被攻击方是PC,且反应意图无法从其已提交内容推断时,暂停在当前
	// 行动者(攻击方)处——不置HasActed、不推进ActorIndex/Round,等玩家下一会话轮澄清。
	if act.NeedsClarification {
		target := findCombatParticipant(cs, act.TargetName)
		if target == nil || target.IsNPC {
			return "SYSTEM REJECT: needs_clarification只能用于被攻击方是调查员、且其本轮已提交意图无法推断闪避/反击倾向的情形;NPC的反应应由你直接决定,不得用此信号回避决策。"
		}
		question := strings.TrimSpace(act.ClarifyQuestion)
		if question == "" {
			question = fmt.Sprintf("你被%s攻击,选择闪避还是反击？", actorName)
		}
		target.PendingClarification = true
		target.PendingQuestion = question
		return fmt.Sprintf("【%s的攻击暂停】等待%s澄清反应方式。请立即调用response,在reply中问出:%q,不要继续处理后续参战者。",
			actorName, target.Name, question)
	}

	// 成功结算,清空自己和目标身上残留的待澄清标记(续接场景)。
	current.PendingClarification = false
	current.PendingQuestion = ""
	if act.TargetName != "" {
		if target := findCombatParticipant(cs, act.TargetName); target != nil {
			target.PendingClarification = false
			target.PendingQuestion = ""
		}
	}

	current.HasActed = true

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("【%s行动】", actorName))
	switch act.Type {
	case "aim":
		current.IsAiming = true
		sb.WriteString("正在瞄准,下轮攻击获得奖励骰。")
	case "take_cover":
		debt := act.APDebtNext
		if debt <= 0 {
			debt = 1
		}
		current.APDebt += debt
		sb.WriteString(fmt.Sprintf("寻找掩体,下轮行动点扣除%d。", debt))
	case "dodge", "fight_back":
		verb := "闪避"
		if act.Type == "fight_back" {
			verb = "反击"
		}
		if target := findCombatParticipant(cs, act.TargetName); target != nil {
			target.HasDodgedOrFB = true
		}
		sb.WriteString(fmt.Sprintf("攻击%s,对方选择%s。", act.TargetName, verb))
	case "attack":
		if current.IsAiming {
			current.IsAiming = false
			sb.WriteString("(使用瞄准奖励骰)")
		}
		sb.WriteString(fmt.Sprintf("攻击%s(武器:%s)。", act.TargetName, act.WeaponName))
	default:
		sb.WriteString(fmt.Sprintf("执行动作:%s。", act.Type))
	}

	next := nextCombatActorIndex(cs, *actx.GCtx, *actx.TempNPCs)
	if next < 0 {
		cs.Round++
		for i := range cs.Participants {
			cs.Participants[i].HasActed = false
			cs.Participants[i].HasDodgedOrFB = false
			if cs.Participants[i].APDebt > 0 {
				cs.Participants[i].APDebt--
			}
		}
		cs.ActorIndex = 0
		*roundClosed = true
		sb.WriteString(fmt.Sprintf(" 本轮全员行动完毕,进入第%d轮;本会话轮的战斗结算已完成,请调用response收尾。", cs.Round))
	} else {
		cs.ActorIndex = next
		nextP := cs.Participants[next]
		sb.WriteString(fmt.Sprintf(" 下一行动者:%s(DEX%d)。", nextP.Name, nextP.DEX))
		if !nextP.IsNPC && !hasDeclaration(actx.GCtx.PendingActions, nextP.UserID, nextP.Name) {
			sb.WriteString(fmt.Sprintf("%s本轮尚未提交行动声明,推进到此为止,请立即调用response收尾并在回复中点名等待%s行动。", nextP.Name, nextP.Name))
		}
	}
	return sb.String()
}

// combatOrderSummary returns a compact DEX-order string for the KP result message.
func combatOrderSummary(parts []models.CombatParticipant) string {
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = fmt.Sprintf("%s(DEX%d)", p.Name, p.DEX)
	}
	return strings.Join(names, " → ")
}

// combatantVitals 实时读取一名参战者的HP/MaxHP/伤势描述;权威来自角色卡/NPC卡,
// CombatState本身不存第二份真相。
func combatantVitals(p models.CombatParticipant, gctx GameContext, tempNPCs []models.SessionNPC) (hp, maxHP int, wound string) {
	if p.IsNPC {
		for _, npc := range tempNPCs {
			if npc.Name == p.Name {
				return npc.Stats.Data["HP"], npc.Stats.Data["MaxHP"], npcDisplayState(npc)
			}
		}
		return 0, 0, "未知"
	}
	for _, pl := range gctx.Session.Players {
		if pl.CharacterCard.Name == p.Name {
			st := pl.CharacterCard.Stats.Data
			wound := woundStateLabel(pl.CharacterCard.WoundState)
			return st.HP, st.MaxHP, wound
		}
	}
	return 0, 0, "未知"
}

// woundStateLabel 把角色卡的wound_state字面量转成中文短描述,供状态提示渲染使用。
func woundStateLabel(state string) string {
	switch strings.TrimSpace(state) {
	case "major":
		return "重伤"
	case "dying":
		return "濒死"
	case "dead":
		return "已死亡"
	}
	return "健康"
}

// combatStateBrief 渲染注入Director prompt的结构化战斗状态提示。
// nil或未激活时返回空串。
func combatStateBrief(cs *models.CombatState, gctx GameContext, tempNPCs []models.SessionNPC) string {
	if cs == nil || !cs.Active || len(cs.Participants) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<combat_state>战斗第%d轮｜行动顺序(DEX降序): %s\n", cs.Round, combatOrderSummary(cs.Participants)))
	for i, p := range cs.Participants {
		hp, maxHP, wound := combatantVitals(p, gctx, tempNPCs)
		marker := "  "
		if i == cs.ActorIndex {
			marker = "→ "
		}
		acted := "待行动"
		if p.HasActed {
			acted = "已行动"
		}
		sb.WriteString(fmt.Sprintf("%s%s(DEX%d) HP%d/%d %s %s", marker, p.Name, p.DEX, hp, maxHP, wound, acted))
		if p.IsAiming {
			sb.WriteString(" [瞄准中]")
		}
		if p.APDebt > 0 {
			sb.WriteString(fmt.Sprintf(" [AP欠债%d]", p.APDebt))
		}
		if !p.IsNPC {
			if intent := findPlayerIntent(gctx.PendingActions, p.UserID, p.Name); intent != "" {
				sb.WriteString(" | 本轮意图: " + intent)
			} else {
				sb.WriteString(" | [本轮尚未提交]")
			}
		}
		sb.WriteString("\n")
	}
	current := cs.Participants[cs.ActorIndex]
	sb.WriteString(fmt.Sprintf("当前行动者: %s(DEX%d)。", current.Name, current.DEX))

	var pending []string
	for _, p := range cs.Participants {
		if p.PendingClarification {
			pending = append(pending, fmt.Sprintf("%s: %s", p.Name, p.PendingQuestion))
		}
	}
	if len(pending) > 0 {
		sb.WriteString(" 待澄清: " + strings.Join(pending, "；") + "。")
	}
	sb.WriteString("\n轮次与行动顺序由后端维护,不要自行记忆;每次只对当前行动者调用一次combat_act,推进到下一个尚未提交声明的调查员为止即用response收尾并在回复中点名等待该调查员;全员行动完后端自动进入下一轮。</combat_state>")
	return sb.String()
}

// ── Combat tool executors ────────────────────────────────────────────────────

type startCombatAction struct{}

func (startCombatAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d start_combat participants=%d", actx.Sid, len(call.CombatParticipants))
	if *actx.Combat != nil {
		return []ToolResult{{Action: ToolStartCombat, Result: "SYSTEM REJECT: 战斗已在进行中,不要重复调用start_combat。"}}
	}
	if *actx.Chase != nil {
		return []ToolResult{{Action: ToolStartCombat, Result: "SYSTEM REJECT: 追逐正在进行中,战斗与追逐互斥,请先end_chase。"}}
	}
	cs, err := buildCombatState(call.CombatParticipants, actx)
	if err != nil {
		return []ToolResult{{Action: ToolStartCombat, Result: "错误:" + err.Error()}}
	}
	*actx.Combat = &cs
	saveCombatState(actx.Sid, &cs)
	first := cs.Participants[0]
	return []ToolResult{{Action: ToolStartCombat, Result: fmt.Sprintf(
		"战斗开始,行动顺序(DEX降序): %s。下一步:请对当前行动者 %s 调用combat_act。",
		combatOrderSummary(cs.Participants), first.Name)}}
}

type combatActAction struct{}

func (combatActAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d combat_act actor=%q", actx.Sid, call.CombatActorName)
	cs := *actx.Combat
	if cs == nil {
		return []ToolResult{{Action: ToolCombatAct, Result: "SYSTEM REJECT: 当前没有进行中的战斗,请先调用start_combat。"}}
	}
	result := applyCombatAct(cs, call, actx, actx.RoundClosed)
	saveCombatState(actx.Sid, cs)
	return []ToolResult{{Action: ToolCombatAct, Result: result}}
}

type endCombatAction struct{}

func (endCombatAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d end_combat reason=%q", actx.Sid, call.CombatEndReason)
	if *actx.Combat == nil {
		return []ToolResult{{Action: ToolEndCombat, Result: "当前没有进行中的战斗。"}}
	}
	reason := strings.TrimSpace(call.CombatEndReason)
	if reason == "" {
		reason = "战斗结束"
	}
	*actx.Combat = nil
	saveCombatState(actx.Sid, nil)
	return []ToolResult{{Action: ToolEndCombat, Result: "战斗已结束:" + reason}}
}
