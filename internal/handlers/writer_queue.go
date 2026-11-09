package handlers

// NOTE: Director 发布的 writer/image 消息先以 pending 落库，这里负责补全正文与配图。Director 的工具循环不等它们，
// 但同一周期里之后的 kp/ask 消息要等 Writer 队列写完才发布（见 publishRound），玩家先读到叙事再被提问。
// writer 落库即广播 message_new，再靠 message_update 补全；image 落库时不广播，画完后才以 message_new 推送，id 仍是落库时的，
// 客户端按 id 排序，所以配图会回到它在叙事里的位置。
// Writer 每个会话一个 FIFO 队列：一条 writer 消息对应一次 RunWriterStream，也就是 Writer transcript 的一轮，
// 所以 Writer 的历史顺序等于消息顺序。Painter 不排队，每条 image 消息各自并发生成。
// 房主打断（kp-stop）不会取消这里的任务，因为对应的消息已经对玩家可见。
// 流式正文按 writerTokenFlushInterval 合并成 writer_token 帧，帧里带 UTF-16 偏移；连接建立时 WS 会补发正在生成的全文，
// 断线重连或进房时客户端都能接上，不会因为漏了中间的 token 就停在半截。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/imagestore"
)

const (
	writerJobTimeout  = 600 * time.Second
	painterJobTimeout = 600 * time.Second

	writerFailedText  = "叙事生成失败"
	painterFailedText = "配图生成失败"

	// NOTE: 网关常把几百个 token 一次性吐出来，逐 token 广播会瞬间塞满订阅者队列(hubSubscriberQueue)，
	// 整个房间的连接都被当成慢消费者踢掉。合并后每个连接每秒最多收到约 10 帧正文。
	writerTokenFlushInterval = 100 * time.Millisecond
)

type writerTask struct {
	messageID uint
	gctx      agent.GameContext
	direction string
	nsfw      bool
	done      chan struct{} // 任务结束(无论成败)后关闭
}

type writerQueue struct {
	tasks   []writerTask
	running bool
	last    chan struct{} // 最后入队任务的 done：它关闭时队列里此前的任务都已结束
}

var writerQueues = struct {
	mu sync.Mutex
	m  map[uint]*writerQueue
}{m: make(map[uint]*writerQueue)}

// writerTokenEvent 是 writer_token 事件的载荷：Text 从正文的第 Offset 个 UTF-16 码元开始(与浏览器字符串下标一致)。
type writerTokenEvent struct {
	MessageID uint   `json:"message_id"`
	Offset    int    `json:"offset"`
	Text      string `json:"text"`
}

// writerResetEvent 是 writer_reset 事件的载荷：这条消息已广播的流式正文作废，客户端清掉本地拼接的内容。
type writerResetEvent struct {
	MessageID uint `json:"message_id"`
}

// liveWriter 是会话里正在生成的那条 writer 消息的已生成正文，供合并广播和新连接补发。
type liveWriter struct {
	mu        sync.Mutex
	sessionID uint
	messageID uint
	text      strings.Builder
	units     int // text 的 UTF-16 长度
	sentBytes int // 已广播部分在 text 里的字节数
	sentUnits int // 已广播部分的 UTF-16 长度
}

// liveWriters 按会话记录正在生成的 writer；同一会话的 writer 串行执行，所以每个会话至多一条。
var liveWriters = struct {
	mu sync.Mutex
	m  map[uint]*liveWriter
}{m: make(map[uint]*liveWriter)}

func startLiveWriter(sessionID, messageID uint) *liveWriter {
	lw := &liveWriter{sessionID: sessionID, messageID: messageID}
	liveWriters.mu.Lock()
	liveWriters.m[sessionID] = lw
	liveWriters.mu.Unlock()
	return lw
}

func (lw *liveWriter) stop() {
	liveWriters.mu.Lock()
	if liveWriters.m[lw.sessionID] == lw {
		delete(liveWriters.m, lw.sessionID)
	}
	liveWriters.mu.Unlock()
}

func (lw *liveWriter) append(token string) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	lw.text.WriteString(token)
	for _, r := range token {
		lw.units += utf16.RuneLen(r)
	}
}

// reset 丢弃已生成的正文并通知客户端：流中途断开后 Writer 会整段重新请求，半截正文不能和重试的正文拼在一起。
func (lw *liveWriter) reset() {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	lw.text.Reset()
	lw.units, lw.sentBytes, lw.sentUnits = 0, 0, 0
	// NOTE: 持锁广播，和 flush 一样保证 reset 与前后的 writer_token 帧按顺序进入 hub。
	hubFor(lw.sessionID).Ephemeral("writer_reset", writerResetEvent{MessageID: lw.messageID})
}

// flush 把还没广播的正文作为一帧 writer_token 发出去。
func (lw *liveWriter) flush() {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if lw.sentBytes == lw.text.Len() {
		return
	}
	ev := writerTokenEvent{MessageID: lw.messageID, Offset: lw.sentUnits, Text: lw.text.String()[lw.sentBytes:]}
	lw.sentBytes, lw.sentUnits = lw.text.Len(), lw.units
	// NOTE: 持锁广播，保证同一条消息的帧按偏移顺序进入 hub。
	hubFor(lw.sessionID).Ephemeral("writer_token", ev)
}

// writerCatchUp 返回会话里正在生成的 writer 的已生成全文(offset=0)，供新连接接上流式正文。
func writerCatchUp(sessionID uint) (roomEvent, bool) {
	liveWriters.mu.Lock()
	lw := liveWriters.m[sessionID]
	liveWriters.mu.Unlock()
	if lw == nil {
		return roomEvent{}, false
	}
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if lw.text.Len() == 0 {
		return roomEvent{}, false
	}
	return roomEvent{Type: "writer_token", Data: writerTokenEvent{MessageID: lw.messageID, Text: lw.text.String()}}, true
}

// messageUpdateEvent 是 message_update 事件的载荷：只有出现的字段才需要更新。
type messageUpdateEvent struct {
	ID      uint                 `json:"id"`
	Status  models.MessageStatus `json:"status"`
	Content *string              `json:"content,omitempty"`
	Error   string               `json:"error,omitempty"`
}

// enqueueWriter 把一条 writer 消息放进会话的 FIFO 队列，立即返回；队列空闲时才会起 worker，worker 排空后退出。
func (h *SessionHandlers) enqueueWriter(sessionID uint, task writerTask) {
	writerQueues.mu.Lock()
	defer writerQueues.mu.Unlock()
	q := writerQueues.m[sessionID]
	if q == nil {
		q = &writerQueue{}
		writerQueues.m[sessionID] = q
	}
	task.done = make(chan struct{})
	q.tasks = append(q.tasks, task)
	q.last = task.done
	if !q.running {
		q.running = true
		go h.drainWriterQueue(sessionID, q)
	}
}

func (h *SessionHandlers) drainWriterQueue(sessionID uint, q *writerQueue) {
	for {
		writerQueues.mu.Lock()
		if len(q.tasks) == 0 {
			q.running = false
			delete(writerQueues.m, sessionID)
			writerQueues.mu.Unlock()
			return
		}
		task := q.tasks[0]
		q.tasks = q.tasks[1:]
		writerQueues.mu.Unlock()

		h.runWriterTask(sessionID, task)
		close(task.done)
	}
}

// writersDone 返回一个 chan，会话里此刻已入队的 writer 全部结束时关闭；队列为空时返回 nil。
func writersDone(sessionID uint) <-chan struct{} {
	writerQueues.mu.Lock()
	defer writerQueues.mu.Unlock()
	if q := writerQueues.m[sessionID]; q != nil {
		return q.last
	}
	return nil
}

func (h *SessionHandlers) runWriterTask(sessionID uint, task writerTask) {
	ctx, cancel := context.WithTimeout(context.Background(), writerJobTimeout)
	defer cancel()
	start := time.Now()
	lw := startLiveWriter(sessionID, task.messageID)
	stopTicker, tickerDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(tickerDone)
		ticker := time.NewTicker(writerTokenFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				lw.flush()
			case <-stopTicker:
				return
			}
		}
	}()
	ctx = agent.WithWriterRestart(ctx, lw.reset)
	text, err := h.Runner.RunWriterStream(ctx, task.gctx, task.direction, task.nsfw, func(token string) {
		if token != "" {
			lw.append(token)
		}
	})
	close(stopTicker)
	<-tickerDone
	// NOTE: 剩余正文先于 message_update 发出，客户端看到的流式正文与终态一致。
	lw.flush()
	defer lw.stop()
	if err != nil {
		chatLog.Error("writer task failed", "session", sessionID, "message", task.messageID,
			"elapsed_ms", float64(time.Since(start).Milliseconds()), "err", err)
	}
	finishWriterMessage(task.messageID, text, err)
}

// finishWriterMessage 写入 Writer 正文并广播 message_update。没有正文就标记 failed；
// 出错但已经有部分正文时保留这部分正文，比整条丢失更有用。
func finishWriterMessage(messageID uint, text string, runErr error) {
	text = strings.TrimSpace(text)
	status := models.MessageStatusDone
	errText := ""
	if text == "" {
		status = models.MessageStatusFailed
		errText = writerFailedText
	}
	msg, err := settleMessage(messageID, func(m *models.Message) {
		m.Content = text
		m.Status = status
		setMessageError(m, errText)
	})
	if err != nil {
		chatLog.Error("writer message update failed", "message", messageID, "err", err)
		return
	}
	hubFor(msg.SessionID).Publish("message_update", messageUpdateEvent{ID: messageID, Status: status, Content: &text, Error: errText})
	if runErr != nil && text != "" {
		chatLog.Warn("writer message kept partial text", "message", messageID, "err", runErr)
	}
}

// runPainterTask 为一条 image 消息生成图片并回写；失败时消息标记 failed。
func (h *SessionHandlers) runPainterTask(messageID uint, gctx agent.GameContext, request agent.ImagePromptRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), painterJobTimeout)
	defer cancel()
	start := time.Now()
	ref, err := h.paint(ctx, gctx, request)
	if err != nil {
		chatLog.Error("painter task failed", "session", gctx.Session.ID, "message", messageID,
			"elapsed_ms", float64(time.Since(start).Milliseconds()), "err", err)
	}
	finishImageMessage(messageID, ref, err)
}

func (h *SessionHandlers) paint(ctx context.Context, gctx agent.GameContext, request agent.ImagePromptRequest) (storedImageRef, error) {
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.Prompt == "" {
		return storedImageRef{}, fmt.Errorf("painter prompt is empty")
	}
	dataURL, err := h.Runner.RunPainter(ctx, gctx, request)
	if err != nil {
		return storedImageRef{}, err
	}
	dataURL = strings.TrimSpace(dataURL)
	if !isValidImageDataURL(dataURL) {
		return storedImageRef{}, fmt.Errorf("painter returned invalid image data")
	}
	stored, err := imagestore.DefaultStore().SaveDataURL(dataURL)
	if err != nil {
		return storedImageRef{}, fmt.Errorf("persist painter image: %w", err)
	}
	return storedImageRef{Hash: stored.Hash, MIME: stored.MIME}, nil
}

// finishImageMessage 把图片引用写进 image 消息，再以 message_new 推给房间：配图画完前客户端看不到这条消息。
// err 非空时标记 failed，同样推送，客户端据此显示失败。
func finishImageMessage(messageID uint, ref storedImageRef, runErr error) {
	status := models.MessageStatusDone
	errText := ""
	if runErr != nil {
		status = models.MessageStatusFailed
		errText = painterFailedText
	}
	msg, err := settleMessage(messageID, func(m *models.Message) {
		if runErr == nil {
			m.Content = appendInternalImageTags("", []storedImageRef{ref}, nil)
		}
		m.Status = status
		setMessageError(m, errText)
	})
	if err != nil {
		chatLog.Error("image message update failed", "message", messageID, "err", err)
		return
	}
	hubFor(msg.SessionID).Publish("message_new", gin.H{"message": newMessageResponse(msg)})
}

// settleMessage 读出消息、交给 apply 修改，再只回写 content/status/meta 三列，返回修改后的消息。
func settleMessage(messageID uint, apply func(m *models.Message)) (models.Message, error) {
	var msg models.Message
	if err := models.DB.First(&msg, messageID).Error; err != nil {
		return msg, err
	}
	apply(&msg)
	err := models.DB.Model(&models.Message{}).Where("id = ?", messageID).
		Updates(map[string]any{"content": msg.Content, "status": msg.Status, "meta": msg.Meta}).Error
	return msg, err
}

func setMessageError(m *models.Message, errText string) {
	meta := m.Meta.Data
	if meta == nil {
		if errText == "" {
			return
		}
		meta = &models.MessageMeta{}
	}
	meta.Error = errText
	m.Meta.Data = meta
}
