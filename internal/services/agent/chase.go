// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/llmcoc/server/internal/models"
)

// ── Chase state helpers ───────────────────────────────────────────────────────

// saveChaseState persists the ChaseState JSON column on GameSession.
// Pass nil to clear an ended chase.
func saveChaseState(sessionID uint, chs *models.ChaseState) {
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", sessionID).
		Update("chase_state", models.JSONField[*models.ChaseState]{Data: chs})
}

// buildChaseState initialises a new ChaseState from KP-provided participant inputs.
// PC 的 DEX 权威始终来自角色卡;MOV是速度检定后的调整值,只能来自模型(检定结果),
// 角色卡上的基础MOV不适用于本次追逐。NPC的DEX优先取TempNPCs中的真实值。
func buildChaseState(inputs []ChaseParticipantInput, actx ActionContext) (models.ChaseState, error) {
	parts := make([]models.ChaseParticipant, 0, len(inputs))
	var missing []string
	minMOV := -1
	for _, inp := range inputs {
		name := strings.TrimSpace(inp.Name)
		if name == "" {
			continue
		}
		p := models.ChaseParticipant{
			Name:      name,
			IsNPC:     inp.IsNPC,
			DEX:       inp.DEX,
			MOV:       inp.MOV,
			Location:  inp.Location,
			IsPursuer: inp.IsPursuer,
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
		if minMOV < 0 || p.MOV < minMOV {
			minMOV = p.MOV
		}
		parts = append(parts, p)
	}
	if len(missing) > 0 {
		return models.ChaseState{}, fmt.Errorf("找不到调查员:%s;房间内真实角色名为:%s",
			strings.Join(missing, "、"), playerCharacterNames(actx.GCtx.Session.Players))
	}
	if len(parts) == 0 {
		return models.ChaseState{}, fmt.Errorf("start_chase 至少需要一名参与者")
	}
	if minMOV < 0 {
		minMOV = 0
	}
	sort.SliceStable(parts, func(i, j int) bool {
		return parts[i].DEX > parts[j].DEX
	})
	return models.ChaseState{
		Active:       true,
		Round:        1,
		MinMOV:       minMOV,
		Participants: parts,
		ActorIndex:   0,
	}, nil
}

// findChaseParticipant 按名字精确查找参与者,找不到返回nil。
func findChaseParticipant(chs *models.ChaseState, name string) *models.ChaseParticipant {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	for i := range chs.Participants {
		if chs.Participants[i].Name == name {
			return &chs.Participants[i]
		}
	}
	return nil
}

// nextChaseActorIndex 从当前ActorIndex起,按DEX序找下一个"本轮未行动且存活"的参与者。
// 全部行动完或全部出局时返回-1,表示本追逐轮已结算完毕。
func nextChaseActorIndex(chs *models.ChaseState, gctx GameContext, tempNPCs []models.SessionNPC) int {
	n := len(chs.Participants)
	for step := 1; step <= n; step++ {
		idx := (chs.ActorIndex + step) % n
		p := chs.Participants[idx]
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

// applyChaseAct 结算追逐轮中一名参与者本轮的全部行动(一次调用报告移动/闯险境/闯
// 障碍/冲突的整串序列,而非逐行动点单独调用),并推进/翻页ActorIndex/Round。
// roundClosed 是本次run()内的一次性闸门(D2),语义与combat.go的applyCombatAct一致。
func applyChaseAct(chs *models.ChaseState, call ToolCall, actx ActionContext, roundClosed *bool) string {
	if *roundClosed {
		return "SYSTEM REJECT: 本会话轮的追逐轮已经结算完毕,请立即调用response收尾,不要继续调用chase_act。"
	}
	if len(chs.Participants) == 0 {
		return "错误:当前没有追逐参与者"
	}

	actorName := strings.TrimSpace(call.ChaseActorName)
	current := &chs.Participants[chs.ActorIndex]
	if actorName != current.Name {
		return fmt.Sprintf("SYSTEM REJECT: 追逐轮行动顺序由后端强制,当前应轮到 %s(DEX%d) 行动,而不是 %s。请按<chase_state>给出的顺序调用chase_act。",
			current.Name, current.DEX, actorName)
	}

	// 声明可见性闸门(D3):轮到的是PC但本轮还没提交行动声明时拒绝推进——按批次收集后,
	// 这种情况意味着该PC所在的批次还没交齐,不应该靠Director瞎编一个行动替玩家决定。
	if !current.IsNPC && !hasDeclaration(actx.GCtx.PendingActions, current.UserID, current.Name) {
		return fmt.Sprintf("SYSTEM REJECT: 当前行动者 %s 本轮尚未提交行动声明,请立即调用response收尾,在回复中点名等待%s行动。",
			current.Name, current.Name)
	}

	act := call.ChaseAction
	if act == nil {
		return fmt.Sprintf("错误:chase_act缺少chase_action字段(行动者:%s)", actorName)
	}

	// 待澄清暂停(D3): 仅conflict类型下被攻击方是PC、且反应意图无法从其已提交内容推断
	// 时可用;险境中的谨慎/鲁莽选择不适用(未声明时按不额外花费行动点、不买奖励骰处理,
	// 不构成代选)。
	if act.NeedsClarification {
		if act.Type != "conflict" {
			return "SYSTEM REJECT: needs_clarification只能用于conflict类型下被攻击方的反应暂停(闪避/反击);险境中的谨慎/鲁莽选择不进入待澄清,未声明时按不额外花费行动点、不买奖励骰处理。"
		}
		target := findChaseParticipant(chs, act.TargetName)
		if target == nil || target.IsNPC {
			return "SYSTEM REJECT: needs_clarification只能用于被攻击方是调查员、且其本轮已提交意图无法推断闪避/反击倾向的情形;NPC的反应应由你直接决定,不得用此信号回避决策。"
		}
		question := strings.TrimSpace(act.ClarifyQuestion)
		if question == "" {
			question = fmt.Sprintf("你在追逐中被%s攻击,选择闪避还是反击？", actorName)
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
		if target := findChaseParticipant(chs, act.TargetName); target != nil {
			target.PendingClarification = false
			target.PendingQuestion = ""
		}
	}

	current.HasActed = true

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("【%s追逐行动】", actorName))
	if act.MoveDelta != 0 {
		current.Location += act.MoveDelta
		sb.WriteString(fmt.Sprintf("移动%+d格(当前位置%d)。", act.MoveDelta, current.Location))
	}
	switch act.Type {
	case "hazard":
		if act.APDebtNext > 0 {
			current.APDebt += act.APDebtNext
			sb.WriteString(fmt.Sprintf("险境检定失败,下轮行动点扣除%d。", act.APDebtNext))
		} else {
			sb.WriteString("险境检定成功,正常通过。")
		}
	case "obstacle":
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
					Name:    act.ObstacleName,
					Between: act.ObstacleBetween,
					HP:      act.ObstacleHP,
					MaxHP:   act.ObstacleMaxHP,
				})
				sb.WriteString(fmt.Sprintf("新增障碍【%s】(位于地点%d-%d间)HP=%d/%d。",
					act.ObstacleName, act.ObstacleBetween[0], act.ObstacleBetween[1], act.ObstacleHP, act.ObstacleMaxHP))
			}
		}
	case "conflict":
		sb.WriteString(fmt.Sprintf("与%s发生冲突。", act.TargetName))
	case "move":
		// 位置变化已在上面统一处理,无需额外内容。
	default:
		sb.WriteString(fmt.Sprintf("执行追逐动作:%s。", act.Type))
	}

	for _, p := range chs.Participants {
		if !p.IsPursuer {
			continue
		}
		for _, q := range chs.Participants {
			if !q.IsPursuer && p.Location >= q.Location {
				sb.WriteString(fmt.Sprintf(" ⚠ 追逐者%s已追上%s(位置%d≥%d),你可以宣告追逐结束。",
					p.Name, q.Name, p.Location, q.Location))
			}
		}
	}

	next := nextChaseActorIndex(chs, *actx.GCtx, *actx.TempNPCs)
	if next < 0 {
		chs.Round++
		for i := range chs.Participants {
			chs.Participants[i].HasActed = false
			if chs.Participants[i].APDebt > 0 {
				chs.Participants[i].APDebt--
			}
		}
		chs.ActorIndex = 0
		*roundClosed = true
		sb.WriteString(fmt.Sprintf(" 本轮全员行动完毕,进入第%d轮;本会话轮的追逐结算已完成,请调用response收尾。", chs.Round))
	} else {
		chs.ActorIndex = next
		nextP := chs.Participants[next]
		sb.WriteString(fmt.Sprintf(" 下一行动者:%s(DEX%d)。", nextP.Name, nextP.DEX))
		if !nextP.IsNPC && !hasDeclaration(actx.GCtx.PendingActions, nextP.UserID, nextP.Name) {
			sb.WriteString(fmt.Sprintf("%s本轮尚未提交行动声明,推进到此为止,请立即调用response收尾并在回复中点名等待%s行动。", nextP.Name, nextP.Name))
		}
	}
	return sb.String()
}

// chaseOrderSummary returns a compact DEX-order string for the KP result message.
func chaseOrderSummary(parts []models.ChaseParticipant) string {
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = fmt.Sprintf("%s(DEX%d)", p.Name, p.DEX)
	}
	return strings.Join(names, " → ")
}

// chaseActionPoints 计算某参与者本轮的行动点:基础值1+(MOV-最低MOV),扣除AP欠债,下限0。
func chaseActionPoints(p models.ChaseParticipant, minMOV int) int {
	ap := 1 + (p.MOV - minMOV)
	if ap < 1 {
		ap = 1
	}
	ap -= p.APDebt
	if ap < 0 {
		ap = 0
	}
	return ap
}

// chaseStateBrief 渲染注入Director prompt的结构化追逐状态提示。
// nil或未激活时返回空串。
func chaseStateBrief(chs *models.ChaseState, gctx GameContext, tempNPCs []models.SessionNPC) string {
	if chs == nil || !chs.Active || len(chs.Participants) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<chase_state>追逐第%d轮｜行动顺序(DEX降序,全员最低MOV=%d): %s\n",
		chs.Round, chs.MinMOV, chaseOrderSummary(chs.Participants)))
	for i, p := range chs.Participants {
		marker := "  "
		if i == chs.ActorIndex {
			marker = "→ "
		}
		acted := "待行动"
		if p.HasActed {
			acted = "已行动"
		}
		role := "猎物"
		if p.IsPursuer {
			role = "追逐者"
		}
		ap := chaseActionPoints(p, chs.MinMOV)
		sb.WriteString(fmt.Sprintf("%s%s(%s,DEX%d,MOV%d) 位置%d 本轮行动点%d %s",
			marker, p.Name, role, p.DEX, p.MOV, p.Location, ap, acted))
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
	if len(chs.Obstacles) > 0 {
		obs := make([]string, len(chs.Obstacles))
		for i, ob := range chs.Obstacles {
			obs[i] = fmt.Sprintf("%s(地点%d-%d间,HP%d/%d)", ob.Name, ob.Between[0], ob.Between[1], ob.HP, ob.MaxHP)
		}
		sb.WriteString("障碍: " + strings.Join(obs, "、") + "\n")
	}
	current := chs.Participants[chs.ActorIndex]
	sb.WriteString(fmt.Sprintf("当前行动者: %s(DEX%d)。", current.Name, current.DEX))

	var pending []string
	for _, p := range chs.Participants {
		if p.PendingClarification {
			pending = append(pending, fmt.Sprintf("%s: %s", p.Name, p.PendingQuestion))
		}
	}
	if len(pending) > 0 {
		sb.WriteString(" 待澄清: " + strings.Join(pending, "；") + "。")
	}
	sb.WriteString("\n轮次、行动顺序与行动点由后端维护,不要自行记忆;每次只对当前行动者调用一次chase_act(一次性报告其本轮全部行动),推进到下一个尚未提交声明的调查员为止即用response收尾并在回复中点名等待该调查员;全员行动完后端自动进入下一轮。</chase_state>")
	return sb.String()
}

// ── Chase tool executors ─────────────────────────────────────────────────────

type startChaseAction struct{}

func (startChaseAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d start_chase participants=%d", actx.Sid, len(call.ChaseParticipants))
	if *actx.Chase != nil {
		return []ToolResult{{Action: ToolStartChase, Result: "SYSTEM REJECT: 追逐已在进行中,不要重复调用start_chase。"}}
	}
	if *actx.Combat != nil {
		return []ToolResult{{Action: ToolStartChase, Result: "SYSTEM REJECT: 战斗正在进行中,战斗与追逐互斥,请先end_combat。"}}
	}
	chs, err := buildChaseState(call.ChaseParticipants, actx)
	if err != nil {
		return []ToolResult{{Action: ToolStartChase, Result: "错误:" + err.Error()}}
	}
	*actx.Chase = &chs
	saveChaseState(actx.Sid, &chs)
	first := chs.Participants[0]
	return []ToolResult{{Action: ToolStartChase, Result: fmt.Sprintf(
		"追逐开始,行动顺序(DEX降序): %s。下一步:请对当前行动者 %s 调用chase_act。",
		chaseOrderSummary(chs.Participants), first.Name)}}
}

type chaseActAction struct{}

func (chaseActAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d chase_act actor=%q", actx.Sid, call.ChaseActorName)
	chs := *actx.Chase
	if chs == nil {
		return []ToolResult{{Action: ToolChaseAct, Result: "SYSTEM REJECT: 当前没有进行中的追逐,请先调用start_chase。"}}
	}
	result := applyChaseAct(chs, call, actx, actx.RoundClosed)
	saveChaseState(actx.Sid, chs)
	return []ToolResult{{Action: ToolChaseAct, Result: result}}
}

type endChaseAction struct{}

func (endChaseAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d end_chase reason=%q", actx.Sid, call.ChaseEndReason)
	if *actx.Chase == nil {
		return []ToolResult{{Action: ToolEndChase, Result: "当前没有进行中的追逐。"}}
	}
	reason := strings.TrimSpace(call.ChaseEndReason)
	if reason == "" {
		reason = "追逐结束"
	}
	*actx.Chase = nil
	saveChaseState(actx.Sid, nil)
	return []ToolResult{{Action: ToolEndChase, Result: "追逐已结束:" + reason}}
}
