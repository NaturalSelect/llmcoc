package handlers

import (
	"crypto/rand"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

const inviteCodeChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func generateInviteCode() (string, error) {
	b := make([]byte, 8)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(inviteCodeChars))))
		if err != nil {
			return "", err
		}
		b[i] = inviteCodeChars[n.Int64()]
	}
	return string(b), nil
}

// AdminGetSiteSettings returns all site settings.
func AdminGetSiteSettings(c *gin.Context) {
	var settings []models.SiteSetting
	models.DB.Find(&settings)
	c.JSON(http.StatusOK, settings)
}

// AdminUpdateSiteSetting upserts a site setting by key.
// The balance_rules key receives special validation: empty string is allowed and
// the value is trimmed to at most 2000 Unicode runes before saving.
func AdminUpdateSiteSetting(c *gin.Context) {
	key := c.Param("key")

	if key == "balance_rules" {
		adminUpdateBalanceRules(c)
		return
	}

	var body struct {
		Value string `json:"value" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := models.SetSiteSetting(key, body.Value); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"key": key, "value": body.Value})
}

// adminUpdateBalanceRules handles PUT /config/settings/balance_rules.
// Empty string is valid (disables all balance rules).
// The value is trimmed and must not exceed 2000 Unicode runes.
func adminUpdateBalanceRules(c *gin.Context) {
	var body struct {
		Value *string `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 value 字段"})
		return
	}
	trimmed := strings.TrimSpace(*body.Value)
	if len([]rune(trimmed)) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "平衡调整规则不能超过 2000 个字符（按 Unicode 字符计算）"})
		return
	}
	if err := models.SetSiteSetting("balance_rules", trimmed); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"key": "balance_rules", "value": trimmed})
}

// AdminListInviteCodes returns invite codes with creator/user info, paginated.
func AdminListInviteCodes(c *gin.Context) {
	page, pageSize, ok := parseAdminPagination(c)
	if !ok {
		return
	}

	var total int64
	if err := models.DB.Model(&models.InviteCode{}).Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询邀请码总数失败"})
		return
	}

	codes := make([]models.InviteCode, 0)
	if err := models.DB.Preload("Creator").Preload("UsedUser").
		Order("created_at DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&codes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询邀请码列表失败"})
		return
	}

	c.JSON(http.StatusOK, newPaginatedResponse(codes, page, pageSize, total))
}

// AdminCreateInviteCodes generates N invite codes.
func AdminCreateInviteCodes(c *gin.Context) {
	var body struct {
		Count int `json:"count" binding:"required,min=1,max=100"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	adminID := c.GetUint("user_id")
	codes := make([]models.InviteCode, 0, body.Count)
	for i := 0; i < body.Count; i++ {
		code, err := generateInviteCode()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "生成邀请码失败"})
			return
		}
		codes = append(codes, models.InviteCode{Code: code, CreatedBy: adminID})
	}

	if err := models.DB.Create(&codes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, codes)
}

// AdminDeleteInviteCode deletes an unused invite code.
func AdminDeleteInviteCode(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效ID"})
		return
	}

	var code models.InviteCode
	if err := models.DB.First(&code, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "邀请码不存在"})
		return
	}
	if code.UsedBy != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "已使用的邀请码不能删除"})
		return
	}
	models.DB.Delete(&code)
	c.JSON(http.StatusOK, gin.H{"message": "已删除"})
}
