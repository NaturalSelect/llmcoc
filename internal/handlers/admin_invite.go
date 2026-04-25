package handlers

import (
	"crypto/rand"
	"math/big"
	"net/http"
	"strconv"

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
func AdminUpdateSiteSetting(c *gin.Context) {
	key := c.Param("key")
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

// AdminListInviteCodes returns all invite codes with creator/user info.
func AdminListInviteCodes(c *gin.Context) {
	var codes []models.InviteCode
	models.DB.Preload("Creator").Preload("UsedUser").Order("created_at DESC").Find(&codes)
	c.JSON(http.StatusOK, codes)
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
