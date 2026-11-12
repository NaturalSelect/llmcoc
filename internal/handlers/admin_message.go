package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"gorm.io/gorm"
)

// NOTE: 导演指令可能含模组秘密，不进 messageResponse 和 WebSocket 广播，只经这个管理员接口按需读取。

type writerDirectionResponse struct {
	ID        uint   `json:"id"`
	CallID    string `json:"call_id"`
	Auto      bool   `json:"auto"`
	Direction string `json:"direction"` // 功能上线前的旧消息为空串
}

// AdminGetMessageDirection handles GET /admin/messages/:id/direction。
// 返回一条 writer 消息对应的 Director 导演指令原文，供管理员调试。
func AdminGetMessageDirection(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的消息ID"})
		return
	}

	var msg models.Message
	if err := models.DB.First(&msg, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "消息不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询消息失败"})
		return
	}
	if msg.Kind != models.MessageKindWriter {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不是剧情消息"})
		return
	}

	resp := writerDirectionResponse{ID: msg.ID, Direction: msg.WriterDirection}
	if meta := msg.Meta.Data; meta != nil {
		resp.CallID, resp.Auto = meta.CallID, meta.Auto
	}
	c.JSON(http.StatusOK, resp)
}
