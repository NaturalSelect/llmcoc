package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"gorm.io/gorm"
)

// warehouseTransferReq 是仓库存入/取出接口的公共请求体。
// Item 只用 Name 和 Desc 定位物品堆；Count 为 0 表示整堆，否则只转移该数量。
type warehouseTransferReq struct {
	CharacterCardID uint        `json:"character_card_id" binding:"required"`
	Item            models.Item `json:"item"`
	Count           int         `json:"count"`
}

// itemQuery 从 DELETE 接口的 query 里读取 desc 和 count；count 缺省或为 0 表示整堆。
func itemQuery(c *gin.Context) (desc string, count int, ok bool) {
	desc = strings.TrimSpace(c.Query("desc"))
	if raw := c.Query("count"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "数量无效"})
			return "", 0, false
		}
		count = n
	}
	return desc, count, true
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
		items = []models.Item{}
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
	name := strings.TrimSpace(req.Item.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "物品名不能为空"})
		return
	}
	if req.Count < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "数量无效"})
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

	newInv, taken, hit := models.TakeItem(card.Inventory.Data, name, req.Item.Desc, req.Count)
	if !hit {
		tx.Rollback()
		c.JSON(http.StatusBadRequest, gin.H{"error": "该物品不在人物卡物品栏中"})
		return
	}
	card.Inventory = models.JSONField[[]models.Item]{Data: newInv}
	user.Warehouse = models.JSONField[[]models.Item]{Data: models.AddItem(user.Warehouse.Data, taken)}

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
	name := strings.TrimSpace(req.Item.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "物品名不能为空"})
		return
	}
	if req.Count < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "数量无效"})
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

	newWarehouse, taken, hit := models.TakeItem(user.Warehouse.Data, name, req.Item.Desc, req.Count)
	if !hit {
		tx.Rollback()
		c.JSON(http.StatusBadRequest, gin.H{"error": "该物品不在仓库中"})
		return
	}
	user.Warehouse = models.JSONField[[]models.Item]{Data: newWarehouse}
	card.Inventory = models.JSONField[[]models.Item]{Data: models.AddItem(card.Inventory.Data, taken)}

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

// DiscardWarehouseItem 直接从账号仓库丢弃物品，不涉及人物卡，因此不做 session 占用校验，
// 即使账号下所有人物卡都在游戏中，也允许整理仓库。路径参数是物品名，query 带 desc 和 count。
func DiscardWarehouseItem(c *gin.Context) {
	userID := c.GetUint("user_id")
	name := strings.TrimSpace(strings.TrimPrefix(c.Param("item"), "/"))
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "物品名不能为空"})
		return
	}
	desc, count, ok := itemQuery(c)
	if !ok {
		return
	}

	var user models.User
	if err := models.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}

	newWarehouse, _, hit := models.TakeItem(user.Warehouse.Data, name, desc, count)
	if !hit {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该物品不在仓库中"})
		return
	}
	user.Warehouse = models.JSONField[[]models.Item]{Data: newWarehouse}

	if err := models.DB.Save(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新仓库失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已丢弃", "warehouse": newWarehouse})
}
