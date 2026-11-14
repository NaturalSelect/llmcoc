package handlers

// NOTE: 模组生成和上传故事编译的 WebSocket 接口：GET /api/scenarios/generate/ws、GET /api/scenarios/compile-story/ws。
// 协议：连接建立后客户端发送的第一个文本帧就是请求体（与原来的 POST JSON 相同，故事文档放不进 URL）；
// 之后服务端只推 {type, data} 帧，type 沿用原 SSE 事件名（progress / batch_progress / done / error / batch_done），
// 请求校验失败时推一帧 rejected。任务结束后服务端关闭连接；客户端中途断开不影响任务，照常跑完并入库。
// 管理员鉴权由路由上的中间件在升级前完成，gorilla 只允许单写者：握手后的写操作都在 scenarioWritePump 里完成。

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/gorilla/websocket"
	"github.com/llmcoc/server/internal/services/agent"
)

const (
	// 连接后必须在这段时间内发出请求帧，否则直接断开。
	scenarioWSRequestTimeout = 30 * time.Second
	// 请求帧里带着完整故事文档，读上限远大于房间 WebSocket；文档体积的业务上限在 CompileStoryWS 里单独校验。
	scenarioWSReadLimit = 2 << 20
	// 事件队列满时 emit 会阻塞流水线，直到客户端读走或连接断开。
	scenarioEventQueue = 128
	// 与前端 MAX_STORY_BYTES 和线上模组的编译上限保持一致。
	maxStoryDocumentBytes = 512 * 1024
)

// scenarioJob 是校验通过后要在后台跑的任务，通过 emit 推送事件。
type scenarioJob func(emit scenarioEmit)

// GenerateScenarioWS 以 WebSocket 方式运行 AI 模组生成流水线，支持通过 count 批量生成。
func GenerateScenarioWS(c *gin.Context) {
	serveScenarioWS(c, func(raw []byte) (scenarioJob, string) {
		var req GenerateScenarioReq
		if err := binding.JSON.BindBody(raw, &req); err != nil {
			return nil, err.Error()
		}
		count := req.Count
		if count <= 0 {
			count = 1
		}
		if count > maxScenarioGenCount {
			return nil, fmt.Sprintf("生成数量不能超过 %d", maxScenarioGenCount)
		}
		return func(emit scenarioEmit) { runScenarioGeneration(req, count, emit) }, ""
	})
}

// CompileStoryWS 以 WebSocket 方式将管理员上传的故事文档直接编译为结构化模组，
// 跳过 Story Architect 生成阶段（模型只做 ETL）。不支持批量：一个连接编译一篇故事。
func CompileStoryWS(c *gin.Context) {
	serveScenarioWS(c, func(raw []byte) (scenarioJob, string) {
		var req CompileStoryReq
		if err := binding.JSON.BindBody(raw, &req); err != nil {
			return nil, err.Error()
		}
		if len(req.StoryDocument) > maxStoryDocumentBytes {
			return nil, "故事文档过大（最大 500KB）"
		}
		compile := agent.CompileStoryRequest{StoryDocument: req.StoryDocument, Name: req.Name}
		return func(emit scenarioEmit) { runCompileStory(compile, nil, nil, emit) }, ""
	})
}

// serveScenarioWS 升级连接、读取请求帧并把任务的事件转发给客户端。parse 返回 nil 任务时，第二个返回值是拒绝原因。
func serveScenarioWS(c *gin.Context, parse func(raw []byte) (scenarioJob, string)) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		wsLog.Debug("scenario ws upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	conn.SetReadLimit(scenarioWSReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(scenarioWSRequestTimeout))
	msgType, raw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var job scenarioJob
	reason := "请求必须是 JSON 文本帧"
	if msgType == websocket.TextMessage {
		job, reason = parse(raw)
	}
	if job == nil {
		scenarioWriteFrame(conn, "rejected", gin.H{"message": reason})
		scenarioWriteClose(conn)
		return
	}

	events := make(chan scenarioGenEvent, scenarioEventQueue)
	gone := make(chan struct{})
	jobDone := make(chan struct{})
	// NOTE: 连接断开后 gone 关闭，emit 随即变成空操作；否则任务会卡在满队列上，后台生成与落库就做不完。
	emit := func(name string, data any) {
		select {
		case events <- scenarioGenEvent{name: name, data: data}:
		case <-gone:
		}
	}
	go func() {
		// NOTE: defer 后进先出：先关 jobDone 再关 events，写协程看到 events 关闭时 jobDone 一定已经关闭。
		defer close(events)
		defer close(jobDone)
		job(emit)
	}()

	readDone := make(chan struct{})
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		scenarioWritePump(conn, events, readDone, gone)
	}()

	wsReadLoop(conn)
	close(readDone)
	_ = conn.Close()
	<-pumpDone

	select {
	case <-jobDone:
	default:
		scenarioLog.Info("scenario ws client disconnected, continuing in background")
	}
}

// scenarioWritePump 是连接唯一的写者：转发任务事件并定时 ping。events 关闭说明任务结束，发正常关闭帧后退出。
// 退出时关闭 gone，通知 emit 不要再等这条连接。
func scenarioWritePump(conn *websocket.Conn, events <-chan scenarioGenEvent, readDone <-chan struct{}, gone chan<- struct{}) {
	defer close(gone)
	defer conn.Close()
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				scenarioWriteClose(conn)
				return
			}
			if !scenarioWriteFrame(conn, evt.name, evt.data) {
				return
			}
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)); err != nil {
				return
			}
		case <-readDone:
			return
		}
	}
}

// scenarioWriteFrame 写一帧，返回 false 表示连接已不可用。序列化失败只丢这一帧，不断开连接。
func scenarioWriteFrame(conn *websocket.Conn, typ string, data any) bool {
	payload, err := json.Marshal(struct {
		Type string `json:"type"`
		Data any    `json:"data"`
	}{typ, data})
	if err != nil {
		scenarioLog.Error("scenario ws marshal failed", "type", typ, "err", err)
		return true
	}
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return conn.WriteMessage(websocket.TextMessage, payload) == nil
}

func scenarioWriteClose(conn *websocket.Conn) {
	msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(wsWriteTimeout))
}
