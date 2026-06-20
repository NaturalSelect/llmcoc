package handlers

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"gorm.io/gorm"
)

const (
	adminScenarioDefaultPage     = 1
	adminScenarioDefaultPageSize = 20
	adminScenarioMaxPageSize     = 100
)

type AdminScenarioListResponse struct {
	Items      []models.Scenario `json:"items"`
	Page       int               `json:"page"`
	PageSize   int               `json:"page_size"`
	Total      int64             `json:"total"`
	TotalPages int               `json:"total_pages"`
}

type AdminScenarioGenerationLogResponse struct {
	ScenarioID   uint      `json:"scenario_id"`
	ScenarioName string    `json:"scenario_name"`
	HasLog       bool      `json:"has_log"`
	LogText      string    `json:"log_text"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

func parseAdminPagination(c *gin.Context) (int, int, bool) {
	page, ok := parsePositiveIntQuery(c, "page", adminScenarioDefaultPage)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "page must be a positive integer"})
		return 0, 0, false
	}

	pageSize, ok := parsePositiveIntQuery(c, "page_size", adminScenarioDefaultPageSize)
	if !ok || pageSize > adminScenarioMaxPageSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "page_size must be a positive integer no greater than 100"})
		return 0, 0, false
	}

	return page, pageSize, true
}

func parsePositiveIntQuery(c *gin.Context, key string, fallback int) (int, bool) {
	raw := c.Query(key)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, false
	}
	return value, true
}

func AdminListScenarios(c *gin.Context) {
	page, pageSize, ok := parseAdminPagination(c)
	if !ok {
		return
	}

	var total int64
	if err := models.DB.Model(&models.Scenario{}).Where("is_active = ?", true).Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询模组总数失败"})
		return
	}

	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	if totalPages < 1 {
		totalPages = 1
	}

	scenarios := make([]models.Scenario, 0)
	if err := models.DB.Where("is_active = ?", true).
		Order("created_at DESC, id DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&scenarios).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询模组列表失败"})
		return
	}

	c.JSON(http.StatusOK, AdminScenarioListResponse{
		Items:      scenarios,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
	})
}

func AdminGetScenarioGenerationLog(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模组ID无效"})
		return
	}

	var scenario models.Scenario
	if err := models.DB.First(&scenario, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "模组不存在"})
		return
	}

	var generationLog models.ScenarioGenerationLog
	err = models.DB.Where("scenario_id = ?", scenario.ID).Order("created_at DESC, id DESC").First(&generationLog).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusOK, AdminScenarioGenerationLogResponse{
			ScenarioID:   scenario.ID,
			ScenarioName: scenario.Name,
			HasLog:       false,
		})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询生成记录失败"})
		return
	}

	c.JSON(http.StatusOK, AdminScenarioGenerationLogResponse{
		ScenarioID:   scenario.ID,
		ScenarioName: generationLog.ScenarioName,
		HasLog:       strings.TrimSpace(generationLog.LogText) != "",
		LogText:      generationLog.LogText,
		CreatedAt:    generationLog.CreatedAt,
		UpdatedAt:    generationLog.UpdatedAt,
	})
}

func AdminListUsers(c *gin.Context) {
	var users []models.User
	models.DB.Order("created_at DESC").Find(&users)
	c.JSON(http.StatusOK, users)
}

// NOTE: AdminRechargeCoins handles POST /admin/recharge.
// Allows administrators to add coins to a user's account manually.
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

func AdminDeleteShopItem(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var item models.ShopItem
	if err := models.DB.First(&item, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "商品不存在"})
		return
	}
	if err := models.DB.Model(&item).Update("is_active", false).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除商品失败"})
		return
	}
	log.Printf("[admin] delete_shop_item ok item_id=%d", item.ID)
	c.JSON(http.StatusOK, gin.H{"message": "商品已删除"})
}

// AdminGetCacheStats handles GET /admin/cache/stats.
// Returns lawyer cache hit/miss statistics and current size.
func AdminGetCacheStats(c *gin.Context) {
	stats := agent.GetLawyerCacheStats()
	c.JSON(http.StatusOK, stats)
}

// AdminClearCache handles DELETE /admin/cache.
// Clears all lawyer cache entries and resets hit/miss counters.
func AdminClearCache(c *gin.Context) {
	adminID := c.GetUint("user_id")
	agent.ClearLawyerCacheAll()
	log.Printf("[admin] clear_lawyer_cache admin_id=%d", adminID)
	c.JSON(http.StatusOK, gin.H{"message": "缓存已清空"})
}

// AdminListCacheKeys handles GET /admin/cache/keys.
// Returns paginated cache keys for admin browsing.
// Query params: page (default 1), page_size (default 20, max 100).
func AdminListCacheKeys(c *gin.Context) {
	page, ok := parsePositiveIntQuery(c, "page", 1)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "page must be a positive integer"})
		return
	}
	pageSize, ok := parsePositiveIntQuery(c, "page_size", 20)
	if !ok || pageSize > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "page_size must be a positive integer no greater than 100"})
		return
	}
	result := agent.ListLawyerCacheKeysPaginated(page, pageSize)
	c.JSON(http.StatusOK, result)
}

// AdminDeleteCacheEntry handles DELETE /admin/cache/entry.
// Deletes a single cache entry by key (passed as query param).
func AdminDeleteCacheEntry(c *gin.Context) {
	adminID := c.GetUint("user_id")
	key := c.Query("key")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 key 参数"})
		return
	}
	deleted := agent.DeleteLawyerCacheEntry(key)
	if !deleted {
		c.JSON(http.StatusNotFound, gin.H{"error": "缓存条目不存在"})
		return
	}
	log.Printf("[admin] delete_cache_entry admin_id=%d key=%q", adminID, key)
	c.JSON(http.StatusOK, gin.H{"message": "缓存条目已删除", "key": key})
}

// AdminBanUser handles PUT /admin/users/:id/ban.
func AdminBanUser(c *gin.Context) {
	adminID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var req struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)

	var user models.User
	if err := models.DB.First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}
	if user.Role == models.RoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "不能封禁管理员"})
		return
	}
	if err := models.DB.Model(&user).Updates(map[string]any{"is_banned": true, "ban_reason": req.Reason}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "封号失败"})
		return
	}
	log.Printf("[admin] ban_user admin_id=%d target_user=%d reason=%q", adminID, id, req.Reason)
	c.JSON(http.StatusOK, gin.H{"message": "已封号", "user_id": id})
}

// AdminUnbanUser handles PUT /admin/users/:id/unban.
func AdminUnbanUser(c *gin.Context) {
	adminID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var user models.User
	if err := models.DB.First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}
	if err := models.DB.Model(&user).Updates(map[string]any{"is_banned": false, "ban_reason": ""}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "解封失败"})
		return
	}
	log.Printf("[admin] unban_user admin_id=%d target_user=%d", adminID, id)
	c.JSON(http.StatusOK, gin.H{"message": "已解封", "user_id": id})
}
