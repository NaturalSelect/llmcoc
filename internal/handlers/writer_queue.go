package handlers

// NOTE: Director 发布的 writer/image 消息先以 pending 落库，这里负责异步补全正文与配图，Director 不等它们。
// Writer 每个会话一个 FIFO 队列：一条 writer 消息对应一次 RunWriterStream，也就是 Writer transcript 的一轮，
// 所以 Writer 的历史顺序等于消息顺序。Painter 不排队，每条 image 消息各自并发生成。
// 房主打断（kp-stop）不会取消这里的任务，因为对应的消息已经对玩家可见。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/imagestore"
)

const (
	writerJobTimeout  = 600 * time.Second
	painterJobTimeout = 600 * time.Second

	writerFailedText  = "叙事生成失败"
	painterFailedText = "配图生成失败"
)

type writerTask struct {
	messageID uint
	gctx      agent.GameContext
	direction string
	nsfw      bool
}

type writerQueue struct {
	tasks   []writerTask
	running bool
}

var writerQueues = struct {
	mu sync.Mutex
	m  map[uint]*writerQueue
}{m: make(map[uint]*writerQueue)}

// messageUpdateEvent 是 message_update 事件的载荷：只有出现的字段才需要更新。
type messageUpdateEvent struct {
	ID      uint                 `json:"id"`
	Status  models.MessageStatus `json:"status"`
	Content *string              `json:"content,omitempty"`
	Images  []string             `json:"images,omitempty"`
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
	q.tasks = append(q.tasks, task)
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
	}
}

func (h *SessionHandlers) runWriterTask(sessionID uint, task writerTask) {
	ctx, cancel := context.WithTimeout(context.Background(), writerJobTimeout)
	defer cancel()
	start := time.Now()
	text, err := h.Runner.RunWriterStream(ctx, task.gctx, task.direction, task.nsfw, func(token string) {
		if token == "" {
			return
		}
		hubFor(sessionID).Ephemeral("writer_token", map[string]any{"message_id": task.messageID, "text": token})
	})
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

// finishImageMessage 把图片引用写进 image 消息并广播 message_update；err 非空时标记 failed。
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
	event := messageUpdateEvent{ID: messageID, Status: status, Error: errText}
	if runErr == nil {
		event.Images = extractImageSources(msg.Content)
	}
	hubFor(msg.SessionID).Publish("message_update", event)
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
