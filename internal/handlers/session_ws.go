package handlers

// NOTE: 房间实时推送的 WebSocket 接口。只负责"服务端 -> 客户端"的事件推送（协议见 docs/realtime-protocol.md），
// 所有命令（发言、触发 KP、停止）仍走 REST，客户端经 WS 发来的数据帧一律丢弃。
// gorilla 只允许单写者：连接的所有写操作都在 wsWritePump 一个 goroutine 里完成，读循环只负责控制帧与超时。

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/llmcoc/server/internal/logging"
	"github.com/llmcoc/server/internal/models"
	"gorm.io/gorm"
)

var wsLog = logging.For("ws")

const (
	wsPingInterval = 30 * time.Second
	wsReadTimeout  = 60 * time.Second // 必须大于 wsPingInterval，留出至少一次丢 pong 的余量
	wsWriteTimeout = 10 * time.Second
	// 客户端不应该发数据帧；限制很小的读上限，超限直接断开。
	wsReadLimit = 1024
)

// wsEpoch 是进程启动时生成一次的标识，重启后会变。客户端带着旧 epoch 重连说明服务端丢了内存里的事件缓冲，
// 此时 seq 已经不可比，只能让客户端重新拉取全量数据。
var wsEpoch = rand.Text()

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     wsCheckOrigin,
}

// wsCheckOrigin 放行非浏览器客户端（无 Origin 头）和同源页面；其他来源的页面不能借用户的 token 连进来。
// 反向代理必须原样透传 Host 头，否则同源页面也会被拒。
func wsCheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

type wsHelloData struct {
	Epoch string `json:"epoch"`
	Seq   uint64 `json:"seq"`
}

// WS 升级为 WebSocket 并订阅房间事件：GET /api/sessions/:id/ws?since=N&epoch=E。
func (h *SessionHandlers) WS(c *gin.Context) {
	sessionID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	userID := c.GetUint("user_id")
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}

	var since uint64
	if raw := c.Query("since"); raw != "" {
		if since, err = strconv.ParseUint(raw, 10, 64); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "since 参数无效"})
			return
		}
	}
	clientEpoch := c.Query("epoch")

	// NOTE: 权限检查必须在 Upgrade 之前，这样失败时还能返回普通的 JSON 错误。
	var session models.GameSession
	if err := models.DB.Preload("Players").First(&session, sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		} else {
			wsLog.Error("ws load session failed", "session", sessionID, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询房间失败"})
		}
		return
	}
	if !canViewSession(&session, userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权查看该房间"})
		return
	}

	// NOTE: 升级失败时 gorilla 已经写好了 HTTP 错误响应，这里直接返回。
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		wsLog.Debug("ws upgrade failed", "session", sessionID, "user", userID, "err", err)
		return
	}

	hub := hubFor(uint(sessionID))
	sub, replay, gap := hub.Subscribe(since)

	initial := []roomEvent{{Type: "hello", Data: wsHelloData{Epoch: wsEpoch, Seq: sub.startSeq}}}
	resync := gap || (clientEpoch != "" && clientEpoch != wsEpoch)
	if resync {
		initial = append(initial, roomEvent{Type: "resync", Data: struct{}{}})
	} else {
		initial = append(initial, replay...)
	}
	wsLog.Debug("ws connected", "session", sessionID, "user", userID, "since", since,
		"resync", resync, "replay", len(replay), "seq", sub.startSeq)

	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		wsWritePump(conn, sub, initial, sessionID, userID)
	}()

	wsReadLoop(conn)

	hub.Unsubscribe(sub)
	_ = conn.Close()
	<-pumpDone
	wsLog.Debug("ws closed", "session", sessionID, "user", userID)
}

// wsReadLoop 阻塞到连接断开或读超时。数据帧读出后丢弃，只有 pong 会续期读超时。
func wsReadLoop(conn *websocket.Conn) {
	conn.SetReadLimit(wsReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	})
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

// wsWritePump 是连接唯一的写者：先写 initial（hello / resync / replay），再转发实时事件并定时 ping。
// 退出时关闭连接，读循环随之返回。
func wsWritePump(conn *websocket.Conn, sub *subscriber, initial []roomEvent, sessionID uint64, userID uint) {
	defer conn.Close()
	for _, ev := range initial {
		if !wsWriteEvent(conn, ev) {
			return
		}
	}

	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case ev := <-sub.ch:
			if !wsWriteEvent(conn, ev) {
				return
			}
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)); err != nil {
				return
			}
		case <-sub.done:
			if sub.slow {
				wsLog.Warn("ws subscriber too slow, dropped", "session", sessionID, "user", userID)
				msg := websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "slow consumer")
				_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(wsWriteTimeout))
			}
			return
		}
	}
}

// wsWriteEvent 写一帧，返回 false 表示连接已不可用。
// 序列化失败只丢这一帧而不断开：Data 来自业务代码，不该因为一个坏事件让所有人掉线；
// 也不用 WriteJSON，它在编码失败时仍会发出一个空的文本帧。
func wsWriteEvent(conn *websocket.Conn, ev roomEvent) bool {
	payload, err := json.Marshal(ev)
	if err != nil {
		wsLog.Error("ws marshal event failed", "type", ev.Type, "seq", ev.Seq, "err", err)
		return true
	}
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return conn.WriteMessage(websocket.TextMessage, payload) == nil
}
