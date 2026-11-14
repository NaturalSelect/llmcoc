package handlers

// NOTE: 线上模组的后台任务和管理员实时推送。下载、上传提取（含慢速的 OCR）都在后台协程里跑，
// 进度经 GET /api/admin/online-modules/ws 推给所有在线的管理员页面。推送协议很简单：
// 连接后第一帧是全量快照，之后只有 module（upsert）和 deleted 两种帧，没有序号和重放，
// 掉线重连就拿一份新快照。客户端不会发数据帧，读循环只负责控制帧和超时（复用房间 WebSocket 的实现）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/onlinemod"
	"gorm.io/gorm"
)

const (
	onlineSubscriberQueue = 64
	// onlineSnapshotRecent 是快照里按最近更新时间带上的条目数；进行中的任务不论多旧都会带上。
	onlineSnapshotRecent = 50
	onlineStageNoteRunes = 100

	onlineInterruptedMessage = "服务重启导致任务中断，请重新下载或上传"
)

var errOnlineShuttingDown = errors.New("服务正在关闭")

type onlineFrame struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type onlineSub struct {
	ch   chan onlineFrame
	done chan struct{}
	slow bool
}

// onlineHub 把条目变化广播给所有订阅者。
// NOTE: 发布时在锁内读库、订阅时在锁内取快照：这样订阅前的变化一定已经体现在快照里，
// 订阅后的变化一定会按顺序送到，快照之后不会再收到比快照更旧的状态。
type onlineHub struct {
	mu   sync.Mutex
	subs map[*onlineSub]struct{}
}

var onlineHubInst = &onlineHub{subs: map[*onlineSub]struct{}{}}

func loadOnlineSnapshot() ([]models.OnlineModule, error) {
	mods := make([]models.OnlineModule, 0, onlineSnapshotRecent)
	if err := models.DB.Order("updated_at DESC, id DESC").Limit(onlineSnapshotRecent).Find(&mods).Error; err != nil {
		return nil, err
	}
	seen := make(map[uint]bool, len(mods))
	for _, m := range mods {
		seen[m.ID] = true
	}
	var active []models.OnlineModule
	if err := models.DB.Where("status = ?", models.OnlineModuleStatusDownloading).Find(&active).Error; err != nil {
		return nil, err
	}
	for _, m := range active {
		if !seen[m.ID] {
			mods = append(mods, m)
		}
	}
	return mods, nil
}

func (h *onlineHub) subscribe() (*onlineSub, onlineFrame, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	mods, err := loadOnlineSnapshot()
	if err != nil {
		return nil, onlineFrame{}, err
	}
	s := &onlineSub{ch: make(chan onlineFrame, onlineSubscriberQueue), done: make(chan struct{})}
	h.subs[s] = struct{}{}
	return s, onlineFrame{Type: "snapshot", Data: gin.H{"modules": mods}}, nil
}

func (h *onlineHub) unsubscribe(s *onlineSub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(s)
}

func (h *onlineHub) removeLocked(s *onlineSub) {
	if _, ok := h.subs[s]; !ok {
		return
	}
	delete(h.subs, s)
	close(s.done)
}

func (h *onlineHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		h.removeLocked(s)
	}
}

func (h *onlineHub) broadcastLocked(f onlineFrame) {
	for s := range h.subs {
		select {
		case s.ch <- f:
		default:
			s.slow = true
			h.removeLocked(s)
		}
	}
}

func (h *onlineHub) publishModule(id uint) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) == 0 {
		return
	}
	var m models.OnlineModule
	if err := models.DB.First(&m, id).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			onlineLog.Error("load online module for push failed", "id", id, "err", err)
		}
		return
	}
	h.broadcastLocked(onlineFrame{Type: "module", Data: m})
}

func (h *onlineHub) publishDeleted(id uint) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.broadcastLocked(onlineFrame{Type: "deleted", Data: gin.H{"id": id}})
}

func publishOnlineModuleID(id uint) { onlineHubInst.publishModule(id) }

// OnlineModuleWS 升级为 WebSocket 并推送线上模组的任务进度：GET /api/admin/online-modules/ws?token=。
// 管理员鉴权由路由上的中间件在升级前完成。
func OnlineModuleWS(c *gin.Context) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		wsLog.Debug("online ws upgrade failed", "err", err)
		return
	}
	sub, snapshot, err := onlineHubInst.subscribe()
	if err != nil {
		onlineLog.Error("online ws snapshot failed", "err", err)
		msg := websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "snapshot failed")
		_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(wsWriteTimeout))
		_ = conn.Close()
		return
	}

	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		onlineWritePump(conn, sub, snapshot)
	}()
	wsReadLoop(conn)

	onlineHubInst.unsubscribe(sub)
	_ = conn.Close()
	<-pumpDone
}

// onlineWritePump 是连接唯一的写者：先写快照，再转发变化并定时 ping。退出时关闭连接，读循环随之返回。
func onlineWritePump(conn *websocket.Conn, sub *onlineSub, snapshot onlineFrame) {
	defer conn.Close()
	if !onlineWriteFrame(conn, snapshot) {
		return
	}
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case f := <-sub.ch:
			if !onlineWriteFrame(conn, f) {
				return
			}
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)); err != nil {
				return
			}
		case <-sub.done:
			if sub.slow {
				onlineLog.Warn("online ws subscriber too slow, dropped")
				msg := websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "slow consumer")
				_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(wsWriteTimeout))
			}
			return
		}
	}
}

// onlineWriteFrame 写一帧，返回 false 表示连接已不可用。序列化失败只丢这一帧，不断开连接。
func onlineWriteFrame(conn *websocket.Conn, f onlineFrame) bool {
	payload, err := json.Marshal(f)
	if err != nil {
		onlineLog.Error("online ws marshal failed", "type", f.Type, "err", err)
		return true
	}
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return conn.WriteMessage(websocket.TextMessage, payload) == nil
}

// onlineTaskRunner 管理后台任务的生命周期：关机时取消正在跑的任务并等它们写完状态。
type onlineTaskRunner struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	stopped bool
	wg      sync.WaitGroup
}

func newOnlineTaskRunner() *onlineTaskRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &onlineTaskRunner{ctx: ctx, cancel: cancel}
}

var onlineRunner = newOnlineTaskRunner()

// start 在后台协程里执行 fn。失败时把条目置为 failStatus 并写入失败原因，成功时 fn 自己已经落好状态，这里只负责推送。
// 调用方必须已经用 claimOnlineModule 抢到了下载状态。
func (r *onlineTaskRunner) start(id uint, failStatus string, fn func(ctx context.Context) error) error {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return errOnlineShuttingDown
	}
	r.wg.Add(1)
	r.mu.Unlock()

	go func() {
		defer r.wg.Done()
		if err := runOnlineTaskSafely(r.ctx, fn); err != nil {
			onlineLog.Warn("online module task failed", "id", id, "err", err)
			finishOnlineTask(id, failStatus, onlineFailureMessage(err))
			return
		}
		publishOnlineModuleID(id)
	}()
	return nil
}

// stop 拒绝新任务、取消运行中的任务，最多等 wait 让它们写完状态，然后断开所有推送连接。
func (r *onlineTaskRunner) stop(wait time.Duration) {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	r.cancel()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(wait):
		onlineLog.Warn("online module tasks did not finish before shutdown deadline")
	}
	onlineHubInst.closeAll()
}

// NOTE: 后台协程里的 panic 不会被 gin 的 Recovery 接住，会直接让整个进程退出，所以这里自己兜底。
func runOnlineTaskSafely(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("内部错误: %v", p)
		}
	}()
	return fn(ctx)
}

// StopOnlineTasks 在优雅退出时调用。
func StopOnlineTasks(wait time.Duration) { onlineRunner.stop(wait) }

// setOnlineStage 更新进行中任务的阶段并推送。条目已不在下载状态（被别的任务接管或已结束）时不动它。
// NOTE: 这里用 Updates 而不是 UpdateColumns：刷新 updated_at 相当于任务心跳，抢占判断靠它识别僵死任务。
func setOnlineStage(id uint, stage, note string) {
	if r := []rune(note); len(r) > onlineStageNoteRunes {
		note = string(r[:onlineStageNoteRunes])
	}
	err := models.DB.Model(&models.OnlineModule{}).
		Where("id = ? AND status = ?", id, models.OnlineModuleStatusDownloading).
		Updates(map[string]any{"stage": stage, "stage_note": note}).Error
	if err != nil {
		onlineLog.Error("update online module stage failed", "id", id, "stage", stage, "err", err)
		return
	}
	publishOnlineModuleID(id)
}

// finishOnlineTask 结束进行中的任务：写最终状态和说明，清掉阶段字段并推送。只作用于仍在下载状态的条目。
func finishOnlineTask(id uint, status, message string) {
	err := models.DB.Model(&models.OnlineModule{}).
		Where("id = ? AND status = ?", id, models.OnlineModuleStatusDownloading).
		Updates(map[string]any{
			"status":          status,
			"error":           message,
			"stage":           "",
			"stage_note":      "",
			"task_started_at": nil,
		}).Error
	if err != nil {
		onlineLog.Error("finish online module task failed", "id", id, "err", err)
	}
	publishOnlineModuleID(id)
}

// FailOrphanOnlineTasks 在启动时调用：上次进程退出时还在跑的任务已经不存在了。
// 已经有提取文档的条目恢复成 extracted（文档在提取成功时才会写入，所以内容是完整的），否则标成 failed。
func FailOrphanOnlineTasks() {
	var rows []models.OnlineModule
	if err := models.DB.Select("id").Where("status = ?", models.OnlineModuleStatusDownloading).Find(&rows).Error; err != nil {
		onlineLog.Error("list orphan online module tasks failed", "err", err)
		return
	}
	for _, r := range rows {
		var docs int64
		if err := models.DB.Model(&models.OnlineModuleDocument{}).Where("online_module_id = ?", r.ID).Count(&docs).Error; err != nil {
			onlineLog.Error("count online module documents failed", "id", r.ID, "err", err)
			continue
		}
		status := models.OnlineModuleStatusFailed
		if docs > 0 {
			status = models.OnlineModuleStatusExtracted
		}
		finishOnlineTask(r.ID, status, onlineInterruptedMessage)
		onlineLog.Warn("online module task interrupted by restart", "id", r.ID, "restored_status", status)
	}
}

// stageOCR 包一层 OCR：识别开始时把阶段切到 ocr 并带上文件名，结束后切回 extract。
type stageOCR struct {
	inner onlinemod.OCR
	id    uint
}

func (s stageOCR) ParsePDF(ctx context.Context, name string, data []byte) (string, error) {
	setOnlineStage(s.id, models.OnlineStageOCR, name)
	defer setOnlineStage(s.id, models.OnlineStageExtract, "")
	return s.inner.ParsePDF(ctx, name, data)
}
