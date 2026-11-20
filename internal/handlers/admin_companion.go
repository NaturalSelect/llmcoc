// NOTE: admin_companion.go 管理 AI 陪玩的人物卡池。卡池就是 bot 用户名下的卡：管理员把自己名下的卡「移入」，
// 房主开房时从中挑选，也可以「移回」管理员名下。
package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

// companionCardView 是卡池里一张卡的展示信息，管理员页与房间选卡共用。
type companionCardView struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	Occupation   string `json:"occupation"`
	Age          int    `json:"age"`
	Gender       string `json:"gender"`
	PortraitHash string `json:"portrait_hash"`
	IsActive     bool   `json:"is_active"`
	WoundState   string `json:"wound_state"`
	HP           int    `json:"hp"`
	MaxHP        int    `json:"max_hp"`
	SAN          int    `json:"san"`
	MaxSAN       int    `json:"max_san"`
	InSession    bool   `json:"in_session"`
}

func newCompanionCardView(card models.CharacterCard, inSession bool) companionCardView {
	stats := card.Stats.Data
	return companionCardView{
		ID:           card.ID,
		Name:         card.Name,
		Occupation:   card.Occupation,
		Age:          card.Age,
		Gender:       card.Gender,
		PortraitHash: card.PortraitHash,
		IsActive:     card.IsActive,
		WoundState:   card.WoundState,
		HP:           stats.HP,
		MaxHP:        stats.MaxHP,
		SAN:          stats.SAN,
		MaxSAN:       stats.MaxSAN,
		InSession:    inSession,
	}
}

// AdminListCompanionCards 处理 GET /admin/companion-cards：bot 名下所有未删除的卡，带占用状态。
func AdminListCompanionCards(c *gin.Context) {
	botID := models.CompanionUserID()
	if botID == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI 陪玩账号不存在"})
		return
	}
	var cards []models.CharacterCard
	if err := models.DB.Where("user_id = ? AND is_deleted = ?", botID, false).
		Order("created_at DESC").Find(&cards).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询 AI 卡池失败"})
		return
	}
	ids := make([]uint, 0, len(cards))
	for _, card := range cards {
		ids = append(ids, card.ID)
	}
	locked := lockedCardIDs(models.DB, ids)

	out := make([]companionCardView, 0, len(cards))
	for _, card := range cards {
		hotFixChar(&card)
		out = append(out, newCompanionCardView(card, locked[card.ID]))
	}
	c.JSON(http.StatusOK, out)
}

// AdminAddCompanionCard 处理 POST /admin/companion-cards：把当前管理员名下的卡移入卡池。
func AdminAddCompanionCard(c *gin.Context) {
	adminID := c.GetUint("user_id")
	var req struct {
		CardID uint `json:"card_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	botID := models.CompanionUserID()
	if botID == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI 陪玩账号不存在"})
		return
	}

	var card models.CharacterCard
	if err := models.DB.First(&card, req.CardID).Error; err != nil || card.IsDeleted {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != adminID {
		c.JSON(http.StatusForbidden, gin.H{"error": "只能移入自己名下的人物卡"})
		return
	}
	if !card.IsActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "已失活的人物卡不能移入卡池"})
		return
	}
	if isCardLockedInSession(models.DB, card.ID) {
		c.JSON(http.StatusConflict, gin.H{"error": "人物卡正在房间中使用"})
		return
	}
	// NOTE: 只能用 Update 写 user_id，不能 Save 整行（portrait_hash 是只读列，整行写会丢失其他并发改动）。
	if err := models.DB.Model(&models.CharacterCard{}).Where("id = ?", card.ID).Update("user_id", botID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "移入卡池失败"})
		return
	}
	adminLog.Info("companion card added", "admin_id", adminID, "card_id", card.ID)
	c.JSON(http.StatusOK, gin.H{"message": "已移入 AI 卡池"})
}

// AdminReturnCompanionCard 处理 POST /admin/companion-cards/:id/return：把卡池里的卡移回当前管理员名下。
func AdminReturnCompanionCard(c *gin.Context) {
	adminID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	botID := models.CompanionUserID()
	if botID == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI 陪玩账号不存在"})
		return
	}

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil || card.IsDeleted || card.UserID != botID {
		c.JSON(http.StatusNotFound, gin.H{"error": "卡池里没有这张人物卡"})
		return
	}
	if isCardLockedInSession(models.DB, card.ID) {
		c.JSON(http.StatusConflict, gin.H{"error": "人物卡正在房间中使用"})
		return
	}
	// 失活的卡不占槽位，与 CreateCharacter 的统计口径一致。
	if card.IsActive {
		var admin models.User
		models.DB.First(&admin, adminID)
		var count int64
		models.DB.Model(&models.CharacterCard{}).Where("user_id = ? AND is_active = ?", adminID, true).Count(&count)
		if int(count) >= admin.CardSlots {
			c.JSON(http.StatusForbidden, gin.H{"error": "人物卡槽位已满"})
			return
		}
	}
	if err := models.DB.Model(&models.CharacterCard{}).Where("id = ?", card.ID).Update("user_id", adminID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "移回失败"})
		return
	}
	adminLog.Info("companion card returned", "admin_id", adminID, "card_id", card.ID)
	c.JSON(http.StatusOK, gin.H{"message": "已移回你的名下"})
}
