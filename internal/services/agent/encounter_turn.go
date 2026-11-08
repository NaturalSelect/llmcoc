// NOTE: encounter_turn.go 计算"此刻谁有发言权"：发言权完全由 KP 的 ask_players 点名决定，不再按 DEX 批次或全员收集。
package agent

import (
	"github.com/llmcoc/server/internal/models"
)

// TurnCollection 描述"此刻应该等哪些真人玩家提交行动"。
// UserIDs 为空表示 KP 持有发言权，此时没有任何玩家可以发言；非空是被点名且仍存活的玩家。
type TurnCollection struct {
	UserIDs  []uint
	Question string // 点名时的提问
}

// BuildTurnCollection 是"这一轮该收集谁"的唯一真相来源，由 ChatStream、GetChatStatus 共用。
// Spotlight 为 nil(没有点名记录)与 UserIDs 为空一样，都视为 KP 持有发言权。
func BuildTurnCollection(session models.GameSession) TurnCollection {
	sp := session.Spotlight.Data
	if sp == nil {
		return TurnCollection{UserIDs: []uint{}}
	}
	alive := map[uint]bool{}
	for _, p := range session.Players {
		if p.CharacterCard.WoundState != "dead" {
			alive[p.UserID] = true
		}
	}
	// NOTE: 只保留仍存活的被点名玩家；点名之后才死亡的玩家不再被等待。
	ids := make([]uint, 0, len(sp.UserIDs))
	for _, id := range sp.UserIDs {
		if alive[id] {
			ids = append(ids, id)
		}
	}
	return TurnCollection{UserIDs: ids, Question: sp.Question}
}
