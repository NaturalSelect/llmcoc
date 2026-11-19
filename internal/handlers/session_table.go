package handlers

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

const (
	maxOOCContentRunes = 500
	oocHistoryLimit    = 200
)

func isSessionMemberOrHost(session *models.GameSession, userID uint, isAdmin bool) bool {
	if isAdmin || session.CreatedBy == userID {
		return true
	}
	for _, p := range session.Players {
		if p.UserID == userID {
			return true
		}
	}
	return false
}

// GetOOCMessages 返回房间最近 200 条桌边闲聊(OOC)消息（按时间升序）。
func GetOOCMessages(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.Preload("Players").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	if !canViewSession(&session, userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权查看该房间"})
		return
	}

	var msgs []models.SessionOOCMessage
	models.DB.Where("session_id = ?", session.ID).
		Order("created_at DESC, id DESC").
		Limit(oocHistoryLimit).
		Find(&msgs)
	slices.Reverse(msgs)
	if msgs == nil {
		msgs = []models.SessionOOCMessage{}
	}
	c.JSON(http.StatusOK, msgs)
}

type postOOCReq struct {
	Content string `json:"content"`
}

// PostOOCMessage 在桌边频道发言：房间参与者、房主与管理员可用，不限房间阶段，不触碰 KP 叙事流。
func PostOOCMessage(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.Preload("Players.CharacterCard").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	isAdmin := isAdminUser(c, userID)
	if !isSessionMemberOrHost(&session, userID, isAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有房间成员可以发送桌边讨论消息"})
		return
	}

	var req postOOCReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息内容不能为空"})
		return
	}
	if utf8.RuneCountInString(content) > maxOOCContentRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("桌边消息最多 %d 字", maxOOCContentRunes)})
		return
	}

	username := c.GetString("username")
	if username == "" {
		var u models.User
		if err := models.DB.Select("username").First(&u, userID).Error; err == nil {
			username = u.Username
		}
	}
	charName := ""
	for _, p := range session.Players {
		if p.UserID == userID {
			charName = p.CharacterCard.Name
			break
		}
	}

	msg := models.SessionOOCMessage{
		SessionID:     session.ID,
		UserID:        userID,
		Username:      username,
		CharacterName: charName,
		Content:       content,
	}
	if err := models.DB.Create(&msg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发送桌边消息失败"})
		return
	}

	hubFor(session.ID).Publish("ooc_message", msg)
	c.JSON(http.StatusCreated, msg)
}
