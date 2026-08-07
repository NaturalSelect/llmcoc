package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"gorm.io/gorm"
)

// warehouseTransferReq 是仓库存入/取出接口的公共请求体。
type warehouseTransferReq struct {
	CharacterCardID uint   `json:"character_card_id" binding:"required"`
	Item            string `json:"item" binding:"required"`
}

// removeFirstItem 移除 list 中第一个与 item 完全匹配的元素，返回新列表及是否命中。
// 仓库场景下同名物品可能有多条（例如两张卡各存入一条"绷带(x3)"），
// 因此不能像 RemoveCharacterInventoryItem 那样一次性删除全部同名项，否则会造成物品凭空消失。
func removeFirstItem(list []string, item string) ([]string, bool) {
	for i, v := range list {
		if v == item {
			out := make([]string, 0, len(list)-1)
			out = append(out, list[:i]...)
			out = append(out, list[i+1:]...)
			return out, true
		}
	}
	return list, false
}

// loadOwnedUnlockedCard 加载指定人物卡，校验其属于当前用户且未被未结束(lobby/playing)的房间占用。
// 校验失败时直接写出 HTTP 响应，调用方应在 ok=false 时直接返回（并自行 rollback 所在事务）。
func loadOwnedUnlockedCard(c *gin.Context, tx *gorm.DB, userID, cardID uint) (models.CharacterCard, bool) {
	var card models.CharacterCard
	if err := tx.Where("id = ? AND user_id = ? AND is_active = ?", cardID, userID, true).First(&card).Error; err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "人物卡不存在或无权使用"})
		return card, false
	}
	if isCardLockedInSession(tx, card.ID) {
		c.JSON(http.StatusConflict, gin.H{"error": "该人物卡正在游戏中,副本结束后才能存取仓库"})
		return card, false
	}
	return card, true
}

// GetWarehouse 返回当前用户的账号级仓库物品列表。
func GetWarehouse(c *gin.Context) {
	userID := c.GetUint("user_id")
	var user models.User
	if err := models.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}
	items := user.Warehouse.Data
	if items == nil {
		items = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"warehouse": items})
}

// DepositToWarehouse 将指定人物卡的一件物品存入账号仓库。
func DepositToWarehouse(c *gin.Context) {
	userID := c.GetUint("user_id")
	var req warehouseTransferReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item := strings.TrimSpace(req.Item)
	if item == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "物品名不能为空"})
		return
	}

	tx := models.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	card, ok := loadOwnedUnlockedCard(c, tx, userID, req.CharacterCardID)
	if !ok {
		tx.Rollback()
		return
	}

	var user models.User
	if err := tx.First(&user, userID).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}

	newInv, hit := removeFirstItem(card.Inventory.Data, item)
	if !hit {
		tx.Rollback()
		c.JSON(http.StatusBadRequest, gin.H{"error": "该物品不在人物卡物品栏中"})
		return
	}
	card.Inventory = models.JSONField[[]string]{Data: newInv}
	user.Warehouse = models.JSONField[[]string]{Data: append(user.Warehouse.Data, item)}

	if err := tx.Save(&card).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新人物卡物品栏失败"})
		return
	}
	if err := tx.Save(&user).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新仓库失败"})
		return
	}
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交事务失败"})
		return
	}

	hotFixChar(&card)
	c.JSON(http.StatusOK, gin.H{
		"message":        "已存入仓库",
		"warehouse":      user.Warehouse.Data,
		"character_card": card,
	})
}

// WithdrawFromWarehouse 将账号仓库中的一件物品取出到指定人物卡。
func WithdrawFromWarehouse(c *gin.Context) {
	userID := c.GetUint("user_id")
	var req warehouseTransferReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item := strings.TrimSpace(req.Item)
	if item == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "物品名不能为空"})
		return
	}

	tx := models.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	card, ok := loadOwnedUnlockedCard(c, tx, userID, req.CharacterCardID)
	if !ok {
		tx.Rollback()
		return
	}

	var user models.User
	if err := tx.First(&user, userID).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}

	newWarehouse, hit := removeFirstItem(user.Warehouse.Data, item)
	if !hit {
		tx.Rollback()
		c.JSON(http.StatusBadRequest, gin.H{"error": "该物品不在仓库中"})
		return
	}
	user.Warehouse = models.JSONField[[]string]{Data: newWarehouse}
	card.Inventory = models.JSONField[[]string]{Data: append(card.Inventory.Data, item)}

	if err := tx.Save(&card).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新人物卡物品栏失败"})
		return
	}
	if err := tx.Save(&user).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新仓库失败"})
		return
	}
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交事务失败"})
		return
	}

	hotFixChar(&card)
	c.JSON(http.StatusOK, gin.H{
		"message":        "已取出到人物卡",
		"warehouse":      user.Warehouse.Data,
		"character_card": card,
	})
}

// DiscardWarehouseItem 直接从账号仓库丢弃一件物品，不涉及人物卡，因此不做 session 占用校验，
// 即使账号下所有人物卡都在游戏中，也允许整理仓库。
func DiscardWarehouseItem(c *gin.Context) {
	userID := c.GetUint("user_id")
	item := strings.TrimSpace(strings.TrimPrefix(c.Param("item"), "/"))
	if item == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "物品名不能为空"})
		return
	}

	var user models.User
	if err := models.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}

	newWarehouse, hit := removeFirstItem(user.Warehouse.Data, item)
	if !hit {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该物品不在仓库中"})
		return
	}
	user.Warehouse = models.JSONField[[]string]{Data: newWarehouse}

	if err := models.DB.Save(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新仓库失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已丢弃", "warehouse": newWarehouse})
}
