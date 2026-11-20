// NOTE: companion_room.go 是房主在 lobby 阶段管理 AI 陪玩的接口。「每个房间最多一个 AI」的约束点就在 AddCompanion：
// 在会话锁内检查 bot 是否已有席位。AI 占 max_players 名额，加入沿用真人的 joinSessionPlayer，所以卡必须属于 bot（卡池）。
package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
)

// hasHumanPlayer 报告席位里是否至少有一名真人；players 必须已预加载 User。
func hasHumanPlayer(players []models.SessionPlayer) bool {
	for _, p := range players {
		if p.User.Role != models.RoleBot {
			return true
		}
	}
	return false
}

// loadLobbyForCompanion 校验房主或管理员身份和 lobby 阶段，并返回带席位的房间。失败时已写好响应。
func loadLobbyForCompanion(c *gin.Context, requireLobby bool) (models.GameSession, bool) {
	userID := c.GetUint("user_id")
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var session models.GameSession
	if err := models.DB.Preload("Players.User").Preload("Players.CharacterCard").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return session, false
	}
	if session.CreatedBy != userID && !isAdminUser(c, userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有房主或管理员可以管理 AI 陪玩"})
		return session, false
	}
	if requireLobby && session.Status != models.SessionStatusLobby {
		c.JSON(http.StatusBadRequest, gin.H{"error": "房间已开始或已结束"})
		return session, false
	}
	return session, true
}

// GetCompanionCards 处理 GET /sessions/:id/companion-cards：房主挑选 AI 时能用的卡池卡片。
func GetCompanionCards(c *gin.Context) {
	if _, ok := loadLobbyForCompanion(c, false); !ok {
		return
	}
	cards := make([]companionCardView, 0)
	if botID := models.CompanionUserID(); botID != 0 {
		var pool []models.CharacterCard
		if err := models.DB.Where("user_id = ? AND is_deleted = ? AND is_active = ?", botID, false, true).
			Order("created_at DESC").Find(&pool).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询 AI 卡池失败"})
			return
		}
		ids := make([]uint, 0, len(pool))
		for _, card := range pool {
			ids = append(ids, card.ID)
		}
		locked := lockedCardIDs(models.DB, ids)
		for _, card := range pool {
			hotFixChar(&card)
			if locked[card.ID] || !checkCardCanJoinSession(&card) {
				continue
			}
			cards = append(cards, newCompanionCardView(card, false))
		}
	}
	c.JSON(http.StatusOK, gin.H{"enabled": agent.CompanionAvailable(), "cards": cards})
}

type addCompanionReq struct {
	CharacterCardID uint   `json:"character_card_id" binding:"required"`
	HandoutCode     string `json:"handout_code"`
}

// AddCompanion 处理 POST /sessions/:id/companion：房主从卡池里挑一张卡，把 AI 陪玩加进房间。
func AddCompanion(c *gin.Context) {
	var req addCompanionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	lock := getSessionLock(uint(sessionID))
	lock.Lock()
	defer lock.Unlock()

	session, ok := loadLobbyForCompanion(c, true)
	if !ok {
		return
	}
	if !agent.CompanionAvailable() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "管理员尚未启用 AI 陪玩"})
		return
	}
	botID := models.CompanionUserID()
	if botID == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI 陪玩账号不存在"})
		return
	}
	for _, p := range session.Players {
		if p.UserID == botID {
			c.JSON(http.StatusConflict, gin.H{"error": "房间里已经有 AI 陪玩"})
			return
		}
	}
	if len(session.Players) >= session.MaxPlayers {
		c.JSON(http.StatusBadRequest, gin.H{"error": "房间已满"})
		return
	}

	handout, terr := joinSessionPlayer(session.ID, botID, req.CharacterCardID, req.HandoutCode, "AI 陪玩")
	if terr != nil {
		respondTurnError(c, terr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已添加 AI 陪玩", "handout_code": handout})
}

// RemoveCompanion 处理 DELETE /sessions/:id/companion：房主在 lobby 阶段移除 AI 陪玩。
func RemoveCompanion(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	lock := getSessionLock(uint(sessionID))
	lock.Lock()
	defer lock.Unlock()

	session, ok := loadLobbyForCompanion(c, true)
	if !ok {
		return
	}
	botID := models.CompanionUserID()
	var seat *models.SessionPlayer
	for i := range session.Players {
		if botID != 0 && session.Players[i].UserID == botID {
			seat = &session.Players[i]
			break
		}
	}
	if seat == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间里没有 AI 陪玩"})
		return
	}
	if err := models.DB.Where("id = ?", seat.ID).Delete(&models.SessionPlayer{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "移除 AI 陪玩失败"})
		return
	}
	models.DB.Create(&models.Message{
		SessionID: session.ID,
		Role:      models.MessageRoleSystem,
		Kind:      models.MessageKindSystem,
		Status:    models.MessageStatusDone,
		Content:   fmt.Sprintf("AI 陪玩「%s」被移出了房间。", seat.CharacterCard.Name),
		Username:  "系统",
	})
	publishSessionChanged(session.ID, models.SessionStatusLobby, "leave")
	c.JSON(http.StatusOK, gin.H{"message": "已移除 AI 陪玩"})
}
