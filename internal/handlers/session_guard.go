// NOTE: 输入分类器的背景：分类器只看一句话时分不清「回答 KP 的问题」和「向 KP 索要答案」，所以随输入附上房间最近的玩家发言、KP 的话和点名。
package handlers

import (
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
)

const (
	// NOTE: 一批最多读这么多行；密讯会被滤掉，凑不够就翻下一批。
	guardContextBatch = 30
	// NOTE: 最多翻几批：一大批密讯排在公开消息之后时也只是少一点背景，不能让分类前的查询无限变长。
	guardContextMaxBatches = 4
)

// NOTE: 不含 writer：叙事正文篇幅长，又可能带出模组细节，发给第三方不划算。
var guardContextKinds = []models.MessageKind{
	models.MessageKindPlayer, models.MessageKindKP, models.MessageKindAsk,
}

// NOTE: loadGuardContext 取分类器要用的背景：提交者扮演的调查员名，以及房间里最近的公开消息（旧到新）。
// 结果会发给第三方，所以只放全员可见且已经广播出去的内容：密讯（含幻觉耳语）、系统消息、还在排队的消息一律不要，叙事正文和推荐行动也不带。
// 任何一步失败都只是少一块背景，不影响分类本身。
func loadGuardContext(sessionID, userID uint) agent.InputContext {
	var ic agent.InputContext

	var player models.SessionPlayer
	if err := models.DB.Preload("CharacterCard").
		Where("session_id = ? AND user_id = ?", sessionID, userID).First(&player).Error; err == nil {
		ic.Character = player.CharacterCard.Name
	}

	held := heldMessageIDs(sessionID)
	var newestFirst []models.Message
	var cursor uint
	for batch := 0; batch < guardContextMaxBatches && len(newestFirst) < agent.InputContextMessages; batch++ {
		q := models.DB.Where("session_id = ? AND role != ? AND kind IN ?", sessionID, models.MessageRoleSystem, guardContextKinds)
		if len(held) > 0 {
			q = q.Where("id NOT IN ?", held)
		}
		if cursor > 0 {
			q = q.Where("id < ?", cursor)
		}
		var rows []models.Message
		if err := q.Order("id DESC").Limit(guardContextBatch).Find(&rows).Error; err != nil {
			chatLog.Debug("load input guard context failed", "session", sessionID, "err", err)
			break
		}
		for _, m := range rows {
			if len(m.VisibleTo.Data) == 0 {
				newestFirst = append(newestFirst, m)
			}
		}
		if len(rows) < guardContextBatch {
			break
		}
		cursor = rows[len(rows)-1].ID
	}

	ic.Messages = make([]models.Message, 0, len(newestFirst))
	for i := len(newestFirst) - 1; i >= 0; i-- {
		ic.Messages = append(ic.Messages, newestFirst[i])
	}
	return ic
}
