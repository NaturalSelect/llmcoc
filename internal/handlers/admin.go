package handlers

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func AdminListUsers(c *gin.Context) {
	var users []models.User
	models.DB.Order("created_at DESC").Find(&users)
	c.JSON(http.StatusOK, users)
}

func AdminRechargeCoins(c *gin.Context) {
	adminID := c.GetUint("user_id")

	var req struct {
		UserID uint   `json:"user_id" binding:"required"`
		Amount int    `json:"amount" binding:"required,min=1"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[admin] recharge admin_id=%d target_user=%d amount=%d", adminID, req.UserID, req.Amount)

	var user models.User
	if err := models.DB.First(&user, req.UserID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}

	tx := models.DB.Begin()
	if err := tx.Model(&user).Update("coins", user.Coins+req.Amount).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "充值失败"})
		return
	}

	recharge := models.CoinRecharge{
		UserID:  req.UserID,
		Amount:  req.Amount,
		AdminID: adminID,
		Note:    req.Note,
	}
	if err := tx.Create(&recharge).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "记录充值失败"})
		return
	}
	tx.Commit()

	models.DB.First(&user, req.UserID)
	log.Printf("[admin] recharge ok admin_id=%d target_user=%d amount=%d new_coins=%d", adminID, req.UserID, req.Amount, user.Coins)
	c.JSON(http.StatusOK, gin.H{
		"message": "充值成功",
		"user":    user,
	})
}

func AdminSetRole(c *gin.Context) {
	adminID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var req struct {
		Role string `json:"role" binding:"required,oneof=user admin"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[admin] set_role admin_id=%d target_user=%d new_role=%s", adminID, id, req.Role)

	var user models.User
	if err := models.DB.First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}
	models.DB.Model(&user).Update("role", req.Role)
	log.Printf("[admin] set_role ok user_id=%d role=%s", id, req.Role)
	c.JSON(http.StatusOK, gin.H{"message": "角色已更新", "user": user})
}

func AdminGetRechargeHistory(c *gin.Context) {
	var records []models.CoinRecharge
	models.DB.Preload("User").Preload("Admin").Order("created_at DESC").Limit(100).Find(&records)
	c.JSON(http.StatusOK, records)
}

func AdminCreateShopItem(c *gin.Context) {
	var item models.ShopItem
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[admin] create_shop_item name=%q price=%d type=%s", item.Name, item.Price, item.ItemType)
	if err := models.DB.Create(&item).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建商品失败"})
		return
	}
	log.Printf("[admin] create_shop_item ok item_id=%d", item.ID)
	c.JSON(http.StatusCreated, item)
}
