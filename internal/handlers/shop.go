package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

// NOTE: siteSettingInt 读取 SiteSetting 并解析为 int，解析失败或空值时返回 fallback。
func siteSettingInt(key string, fallback int) int {
	s := models.GetSiteSetting(key, "")
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}

// NOTE: GetShopCosts 返回商城各项金币费率，供前端动态展示
func GetShopCosts(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"initial_coins":              siteSettingInt("initial_coins", 600),
		"initial_card_slots":         siteSettingInt("initial_card_slots", 3),
		"regenerate_appearance_cost": siteSettingInt("regenerate_appearance_cost", 100),
		"regenerate_backstory_cost":  siteSettingInt("regenerate_backstory_cost", 100),
		"regenerate_traits_cost":     siteSettingInt("regenerate_traits_cost", 100),
		"revive_base_cost":           siteSettingInt("revive_base_cost", 2000),
		"end_session_cost":           siteSettingInt("end_session_cost", 200),
		"writer_history_max_runes":   siteSettingInt("writer_history_max_runes", 20000),
	})
}

func ListShopItems(c *gin.Context) {
	var items []models.ShopItem
	models.DB.Where("is_active = ?", true).Order("price ASC").Find(&items)
	c.JSON(http.StatusOK, items)
}

func PurchaseItem(c *gin.Context) {
	userID := c.GetUint("user_id")

	var req struct {
		ItemID          uint  `json:"item_id" binding:"required"`
		CharacterCardID *uint `json:"character_card_id,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[shop] purchase user_id=%d item_id=%d", userID, req.ItemID)

	var item models.ShopItem
	if err := models.DB.First(&item, req.ItemID).Error; err != nil || !item.IsActive {
		c.JSON(http.StatusNotFound, gin.H{"error": "商品不存在"})
		return
	}

	var user models.User
	models.DB.First(&user, userID)
	if user.Coins < item.Price {
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error":   "金币不足",
			"need":    item.Price,
			"current": user.Coins,
		})
		return
	}

	// Deduct coins and apply item effect in a transaction
	tx := models.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	newCoins := user.Coins - item.Price
	newCardSlots := user.CardSlots
	var updatedCard *models.CharacterCard

	if err := tx.Model(&user).Update("coins", newCoins).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "扣除金币失败"})
		return
	}

	switch item.ItemType {
	case models.ItemTypeCardSlot:
		newCardSlots = user.CardSlots + item.Value
		if err := tx.Model(&user).Update("card_slots", newCardSlots).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "增加槽位失败"})
			return
		}

	case models.ItemTypeCoins:
		newCoins += item.Value
		if err := tx.Model(&user).Update("coins", newCoins).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "增加金币失败"})
			return
		}

	case models.ItemTypeEquipment, models.ItemTypeWeapon, models.ItemTypeAccessory:
		if req.CharacterCardID == nil || *req.CharacterCardID == 0 {
			tx.Rollback()
			c.JSON(http.StatusBadRequest, gin.H{"error": "该商品需要指定人物卡"})
			return
		}

		var card models.CharacterCard
		if err := tx.Where("id = ? AND user_id = ? AND is_active = ?", *req.CharacterCardID, userID, true).First(&card).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusBadRequest, gin.H{"error": "人物卡不存在或无权使用"})
			return
		}

		inv := card.Inventory.Data
		exists := false
		for _, v := range inv {
			if v == item.Name {
				exists = true
				break
			}
		}
		if !exists {
			s := item.Name
			if item.Description != "" {
				s = fmt.Sprintf("%s(%s)", item.Name, item.Description)
			}
			inv = append(inv, s)
			card.Inventory = models.JSONField[[]string]{Data: inv}
			if err := tx.Save(&card).Error; err != nil {
				tx.Rollback()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "更新人物卡物品栏失败"})
				return
			}
		}
		updatedCard = &card
	}

	transaction := models.Transaction{
		UserID:     userID,
		ShopItemID: item.ID,
		CoinsSpent: item.Price,
	}
	if err := tx.Create(&transaction).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "记录交易失败"})
		return
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交交易失败"})
		return
	}

	// Reload user
	models.DB.First(&user, userID)
	log.Printf("[shop] purchase ok user_id=%d item_id=%d coins_left=%d", userID, req.ItemID, user.Coins)
	resp := gin.H{
		"message":    "购买成功",
		"coins":      user.Coins,
		"card_slots": user.CardSlots,
		"item":       item,
	}
	if updatedCard != nil {
		resp["character_card"] = updatedCard
	}
	c.JSON(http.StatusOK, resp)
}

func GetMyTransactions(c *gin.Context) {
	userID := c.GetUint("user_id")
	var transactions []models.Transaction
	models.DB.Where("user_id = ?", userID).
		Preload("ShopItem").
		Order("created_at DESC").
		Limit(50).
		Find(&transactions)
	c.JSON(http.StatusOK, transactions)
}
