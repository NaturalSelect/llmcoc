package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/imagestore"
	"gorm.io/gorm"
)

// NOTE: 测试替换点，避免真实 LLM 调用。
var generatePortrait = agent.GeneratePortrait

// portraitJobs 以人物卡 ID 为键，保证同一张卡同时只有一个立绘生成任务。
var portraitJobs sync.Map

var errPortraitInsufficientCoins = errors.New("insufficient coins for portrait")

// GenerateCharacterPortrait 为人物卡生成（或重新生成）立绘。
// 卡主按 regenerate_portrait_cost 扣费，管理员替别人的卡生成不扣费。
func (h *CharacterHandlers) GenerateCharacterPortrait(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		var u models.User
		models.DB.First(&u, userID)
		if u.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权修改此人物卡"})
			return
		}
	}
	if !card.IsActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "已阵亡的调查员不能生成立绘"})
		return
	}

	if _, loaded := portraitJobs.LoadOrStore(card.ID, struct{}{}); loaded {
		c.JSON(http.StatusConflict, gin.H{"error": "立绘正在生成中"})
		return
	}
	defer portraitJobs.Delete(card.ID)

	isOwner := card.UserID == userID
	cost := siteSettingInt("regenerate_portrait_cost", 100)
	if isOwner {
		var user models.User
		models.DB.First(&user, userID)
		if user.Coins < cost {
			c.JSON(http.StatusPaymentRequired, gin.H{"error": "金币不足", "need": cost, "current": user.Coins})
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), painterJobTimeout)
	defer cancel()
	dataURL, err := generatePortrait(ctx, &card)
	if err != nil {
		characterLog.Error("portrait generation failed", "card", card.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI生成失败: " + err.Error()})
		return
	}
	ref, err := imagestore.PortraitStore().SaveDataURL(dataURL)
	if err != nil {
		characterLog.Error("portrait persist failed", "card", card.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存立绘失败"})
		return
	}

	oldHash := card.PortraitHash
	err = models.DB.Transaction(func(tx *gorm.DB) error {
		if isOwner {
			res := tx.Model(&models.User{}).Where("id = ? AND coins >= ?", userID, cost).
				UpdateColumn("coins", gorm.Expr("coins - ?", cost))
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errPortraitInsufficientCoins
			}
		}
		return tx.Exec("UPDATE character_cards SET portrait_hash = ? WHERE id = ?", ref.Hash, card.ID).Error
	})
	if err != nil {
		removePortraitIfUnreferenced(ref.Hash)
		if errors.Is(err, errPortraitInsufficientCoins) {
			var user models.User
			models.DB.First(&user, userID)
			c.JSON(http.StatusPaymentRequired, gin.H{"error": "金币不足", "need": cost, "current": user.Coins})
			return
		}
		characterLog.Error("portrait save failed", "card", card.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存立绘失败"})
		return
	}
	if oldHash != "" && oldHash != ref.Hash {
		removePortraitIfUnreferenced(oldHash)
	}

	resp := gin.H{"portrait_hash": ref.Hash}
	if isOwner {
		var user models.User
		models.DB.First(&user, userID)
		resp["coins"] = user.Coins
	}
	c.JSON(http.StatusOK, resp)
}

// removePortraitIfUnreferenced 在没有任何人物卡引用该立绘时删除文件。
func removePortraitIfUnreferenced(hash string) {
	if hash == "" {
		return
	}
	var refs int64
	if err := models.DB.Model(&models.CharacterCard{}).Where("portrait_hash = ?", hash).Count(&refs).Error; err != nil {
		characterLog.Error("portrait reference count failed", "hash", hash, "err", err)
		return
	}
	if refs > 0 {
		return
	}
	if err := imagestore.PortraitStore().Remove(hash); err != nil {
		characterLog.Error("portrait remove failed", "hash", hash, "err", err)
	}
}
