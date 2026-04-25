package handlers

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/llmcoc/server/internal/config"
	"github.com/llmcoc/server/internal/middleware"
	"github.com/llmcoc/server/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type RegisterReq struct {
	Username   string `json:"username" binding:"required,min=3,max=50"`
	Email      string `json:"email" binding:"required,email"`
	Password   string `json:"password" binding:"required,min=6"`
	InviteCode string `json:"invite_code"`
}

type LoginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func Register(c *gin.Context) {
	var req RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[register] username=%q email=%q", req.Username, req.Email)

	// Check invite code if required
	var inviteCode models.InviteCode
	requireInvite := models.GetSiteSetting("require_invite_code", "false") == "true"
	if requireInvite {
		if req.InviteCode == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "需要邀请码才能注册"})
			return
		}
		if err := models.DB.Where("code = ? AND used_by IS NULL", req.InviteCode).First(&inviteCode).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "邀请码无效或已被使用"})
			return
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
		return
	}

	user := models.User{
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: string(hash),
		Role:         models.RoleUser,
		Coins:        config.Global.Shop.InitialCoins,
		CardSlots:    config.Global.Shop.InitialCardSlots,
	}

	txErr := models.DB.Transaction(func(tx *gorm.DB) error {
		var userCount int64
		if err := tx.Model(&models.User{}).Count(&userCount).Error; err != nil {
			return err
		}
		if userCount == 0 {
			user.Role = models.RoleAdmin
		}
		return tx.Create(&user).Error
	})
	if txErr != nil {
		log.Printf("[register] create user failed username=%q: %v", req.Username, txErr)
		c.JSON(http.StatusConflict, gin.H{"error": "用户名或邮箱已存在"})
		return
	}
	if user.Role == models.RoleAdmin {
		log.Printf("[register] first user, set as admin: username=%q", req.Username)
	}

	// Mark invite code as used
	if requireInvite {
		now := time.Now()
		models.DB.Model(&inviteCode).Updates(map[string]any{"used_by": user.ID, "used_at": now})
	}

	token, err := generateToken(&user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成令牌失败"})
		return
	}

	log.Printf("[register] ok user_id=%d username=%q", user.ID, user.Username)
	c.JSON(http.StatusCreated, gin.H{
		"token": token,
		"user":  user,
	})
}

func Login(c *gin.Context) {
	var req LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[login] username=%q", req.Username)

	var user models.User
	if err := models.DB.Where("username = ? OR email = ?", req.Username, req.Username).First(&user).Error; err != nil {
		log.Printf("[login] not_found username=%q", req.Username)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		log.Printf("[login] wrong_password user_id=%d", user.ID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	token, err := generateToken(&user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成令牌失败"})
		return
	}

	log.Printf("[login] ok user_id=%d username=%q role=%s", user.ID, user.Username, user.Role)
	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user":  user,
	})
}

func Me(c *gin.Context) {
	userID := c.GetUint("user_id")
	var user models.User
	if err := models.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}
	c.JSON(http.StatusOK, user)
}

// PublicSettings returns non-sensitive site settings for unauthenticated clients.
func PublicSettings(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"require_invite_code": models.GetSiteSetting("require_invite_code", "false") == "true",
	})
}

func generateToken(user *models.User) (string, error) {
	claims := &middleware.Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     string(user.Role),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(
				time.Duration(config.Global.JWT.ExpireHours) * time.Hour,
			)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.Global.JWT.Secret))
}
