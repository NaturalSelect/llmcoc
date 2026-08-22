// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"fmt"

	"github.com/llmcoc/server/internal/models"
)

// EncounterActor 是遭遇(战斗/追逐)当前DEX序列中一名参战者的展示态,供SSE等待载荷
// 渲染收集队列——前端据此判断谁在当前批次、谁在排队、谁已行动。
type EncounterActor struct {
	Name       string `json:"name"`
	IsNPC      bool   `json:"is_npc"`
	UserID     uint   `json:"user_id"`
	Alive      bool   `json:"alive"`
	HasActed   bool   `json:"has_acted"`
	IsCurrent  bool   `json:"is_current"`
	InBatch    bool   `json:"in_batch"`
	Clarifying bool   `json:"clarifying"`
}

// TurnCollection 描述"这一次ChatStream应该等哪些真人玩家提交行动"。
// Batched=true时UserIDs是遭遇DEX序列里当前可收集的一批真人玩家;
// Batched=false时退回房间内全体存活玩家——覆盖无遭遇、遭遇内没有存活PC参战者、
// 批次扫描结果为空这三种情况,行为与遭遇功能上线前完全一致。
type TurnCollection struct {
	UserIDs []uint
	Batched bool
	Label   string
	Order   []EncounterActor
}

// BuildTurnCollection 是"这一轮该收集谁"的唯一真相来源,由ChatStream、GetChatStatus、
// orchestrator.run()共用。战斗/追逐未激活时没有DEX顺序可言,退回全员收集。
func BuildTurnCollection(session models.GameSession) TurnCollection {
	if cs := session.CombatState.Data; cs != nil && cs.Active {
		return buildCombatTurnCollection(cs, session)
	}
	if chs := session.ChaseState.Data; chs != nil && chs.Active {
		return buildChaseTurnCollection(chs, session)
	}
	return TurnCollection{UserIDs: activePlayerIDList(session.Players)}
}

// activePlayerIDList 返回房间内全体存活玩家的UserID(角色卡WoundState!=dead),
// 是非遭遇场景、以及遭遇内无法算出有效批次时的收集集合。
func activePlayerIDList(players []models.SessionPlayer) []uint {
	ids := make([]uint, 0, len(players))
	for _, p := range players {
		if p.CharacterCard.WoundState == "dead" {
			continue
		}
		ids = append(ids, p.UserID)
	}
	return ids
}

func sessionTempNPCs(sessionID uint) []models.SessionNPC {
	var tempNPCs []models.SessionNPC
	models.DB.Where("session_id = ?", sessionID).Find(&tempNPCs)
	return tempNPCs
}

func buildCombatTurnCollection(cs *models.CombatState, session models.GameSession) TurnCollection {
	tempNPCs := sessionTempNPCs(session.ID)
	order := make([]EncounterActor, len(cs.Participants))
	anyAlivePC := false
	for i, p := range cs.Participants {
		alive := isCharacterAlive(p.Name, p.IsNPC, session.Players, tempNPCs)
		order[i] = EncounterActor{
			Name: p.Name, IsNPC: p.IsNPC, UserID: p.UserID,
			Alive: alive, HasActed: p.HasActed,
			IsCurrent: i == cs.ActorIndex, Clarifying: p.PendingClarification,
		}
		anyAlivePC = anyAlivePC || (!p.IsNPC && alive)
	}
	label := fmt.Sprintf("战斗 第%d轮", cs.Round)
	return finishTurnCollection(order, cs.ActorIndex, anyAlivePC, label, session.Players)
}

func buildChaseTurnCollection(chs *models.ChaseState, session models.GameSession) TurnCollection {
	tempNPCs := sessionTempNPCs(session.ID)
	order := make([]EncounterActor, len(chs.Participants))
	anyAlivePC := false
	for i, p := range chs.Participants {
		alive := isCharacterAlive(p.Name, p.IsNPC, session.Players, tempNPCs)
		order[i] = EncounterActor{
			Name: p.Name, IsNPC: p.IsNPC, UserID: p.UserID,
			Alive: alive, HasActed: p.HasActed,
			IsCurrent: i == chs.ActorIndex, Clarifying: p.PendingClarification,
		}
		anyAlivePC = anyAlivePC || (!p.IsNPC && alive)
	}
	label := fmt.Sprintf("追逐 第%d轮", chs.Round)
	return finishTurnCollection(order, chs.ActorIndex, anyAlivePC, label, session.Players)
}

// finishTurnCollection 把已经填好的DEX序列渲染态收拢成最终的收集结果:没有存活PC
// 参战者、或算出的批次为空时,按安全阀退回全员收集(Order仍原样返回供展示)。
func finishTurnCollection(order []EncounterActor, actorIndex int, anyAlivePC bool, label string, players []models.SessionPlayer) TurnCollection {
	if !anyAlivePC {
		return TurnCollection{UserIDs: activePlayerIDList(players), Batched: false, Label: label, Order: order}
	}
	ids := clarifyingBatch(order)
	if len(ids) == 0 {
		ids = scanForwardBatch(order, actorIndex)
	}
	if len(ids) == 0 {
		return TurnCollection{UserIDs: activePlayerIDList(players), Batched: false, Label: label, Order: order}
	}
	return TurnCollection{UserIDs: ids, Batched: true, Label: label, Order: order}
}

// clarifyingBatch 找出所有存活且卡在待澄清暂停的PC——这是最高优先级批次,不含被冻结
// 的攻击者(攻击方不需要重新声明,它此前的声明仍在PendingActions里,见applyCombatAct/
// applyChaseAct的续接逻辑)。
func clarifyingBatch(order []EncounterActor) []uint {
	var ids []uint
	for i := range order {
		if order[i].Clarifying && !order[i].IsNPC && order[i].Alive {
			order[i].InBatch = true
			ids = append(ids, order[i].UserID)
		}
	}
	return ids
}

// scanForwardBatch 从actorIndex起向后单向扫描(不回绕到下一轮),跳过死亡/已行动者;
// 批次为空时遇到NPC继续跳过(前导NPC由Director自主用act_npc/combat_act裁定,不等真
// 人);批次非空后再遇到NPC则停止——NPC的行动结果是批次内玩家尚不知道的新信息,必须
// 先被裁定,才能确定后续局面。
func scanForwardBatch(order []EncounterActor, actorIndex int) []uint {
	var ids []uint
	for idx := actorIndex; idx < len(order); idx++ {
		a := &order[idx]
		if a.HasActed || !a.Alive {
			continue
		}
		if a.IsNPC {
			if len(ids) > 0 {
				break
			}
			continue
		}
		a.InBatch = true
		ids = append(ids, a.UserID)
	}
	return ids
}
