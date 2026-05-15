// NOTE: Provides HTTP middleware for authentication and authorization.
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/llmcoc/server/internal/config"
	"github.com/llmcoc/server/internal/models"
)

// NOTE: Claims represents the JWT payload containing user identity and role.
type Claims struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// NOTE: AuthRequired is a Gin middleware that verifies JWT tokens and sets user context.
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "未提供认证令牌"})
			return
		}

		claims, err := validateToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "令牌无效或已过期"})
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}

func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists || role != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
			return
		}
		c.Next()
	}
}

// BanCheck checks whether the authenticated user is banned and aborts with 403 if so.
// Must be placed after AuthRequired in the middleware chain.
func BanCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("user_id")
		var user models.User
		if err := models.DB.Select("is_banned, ban_reason").First(&user, userID).Error; err == nil && user.IsBanned {
			msg := "账号已被封禁"
			if user.BanReason != "" {
				msg += "：" + user.BanReason
			}
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": msg})
			return
		}
		c.Next()
	}
}

func extractToken(c *gin.Context) string {
	bearer := c.GetHeader("Authorization")
	if strings.HasPrefix(bearer, "Bearer ") {
		return strings.TrimPrefix(bearer, "Bearer ")
	}
	// Also allow query param for SSE
	if t := c.Query("token"); t != "" {
		return t
	}
	return ""
}

func validateToken(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(config.Global.JWT.Secret), nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}
