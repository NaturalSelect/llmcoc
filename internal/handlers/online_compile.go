package handlers

// NOTE: 线上模组的编译任务。故事编译流水线在 onlineRunner 的后台协程里跑，进度写进进程内的日志缓冲，
// 同时经管理员 WebSocket 以 log 帧推送；关掉弹窗、刷新页面或换一台管理员设备都不会丢日志（服务重启除外）。
// 编译期间条目沿用 downloading 状态，用 stage=compile 区分；结束后回到 extracted，失败原因写在 error 里，正文不受影响。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
)

const (
	onlineLogMaxLines  = 1000
	onlineLogLineRunes = 500
	// NOTE: 编译没有固定时长，而且要先等全局的生成锁；定时刷新 updated_at，避免超过 onlineDownloadStaleAfter 后被当成僵死任务抢走。
	onlineCompileHeartbeat = 2 * time.Minute

	onlineCompileInterruptedMessage = "服务重启导致编译中断，请重新编译"
)

type onlineLogBuf struct {
	// base 是 lines[0] 的绝对行号；行号从 0 开始、只增不减，缓冲溢出时丢最旧的行，base 随之前移。
	base  int
	lines []string
}

// onlineLogStore 按条目保存编译日志，只在内存里：日志是临时的进度信息，重启后清空。
type onlineLogStore struct {
	mu   sync.Mutex
	bufs map[uint]*onlineLogBuf
}

var onlineLogs = &onlineLogStore{bufs: map[uint]*onlineLogBuf{}}

// append 追加一行并返回它的绝对行号。
func (s *onlineLogStore) append(id uint, line string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.bufs[id]
	if b == nil {
		b = &onlineLogBuf{}
		s.bufs[id] = b
	}
	index := b.base + len(b.lines)
	b.lines = append(b.lines, line)
	if over := len(b.lines) - onlineLogMaxLines; over > 0 {
		b.lines = append([]string(nil), b.lines[over:]...)
		b.base += over
	}
	return index
}

func (s *onlineLogStore) snapshot(id uint) (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.bufs[id]
	if b == nil {
		return 0, []string{}
	}
	return b.base, append([]string(nil), b.lines...)
}

func (s *onlineLogStore) drop(id uint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bufs, id)
}

// logOnlineCompile 给日志行加上时间前缀，写进缓冲并推送给在线的管理员页面。
func logOnlineCompile(id uint, text string) {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > onlineLogLineRunes {
		text = string(r[:onlineLogLineRunes]) + "…"
	}
	line := "[" + time.Now().Format("15:04:05") + "] " + text
	index := onlineLogs.append(id, line)
	onlineHubInst.publishLog(id, index, line)
}

// compileProgressText 把流水线的进度事件渲染成一行日志。
func compileProgressText(stage, status, detail string) string {
	if stage == "exchange" {
		return "· LLM 交互 [" + status + "] " + detail
	}
	if detail != "" {
		return detail
	}
	return stage + " " + status
}

func compileFailureMessage(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, errOnlineShuttingDown) {
		return onlineCompileInterruptedMessage
	}
	return err.Error()
}

// compileFailureHandler 给后台任务的失败收尾用：返回要写进 error 的文案，同时把失败写进日志。
func compileFailureHandler(id uint) func(error) string {
	return func(err error) string {
		msg := compileFailureMessage(err)
		logOnlineCompile(id, "❌ "+msg)
		return msg
	}
}

// touchOnlineCompile 只刷新 updated_at，不改展示内容，所以也不用推送。
func touchOnlineCompile(id uint) {
	err := models.DB.Model(&models.OnlineModule{}).
		Where("id = ? AND status = ?", id, models.OnlineModuleStatusDownloading).
		Update("stage", models.OnlineStageCompile).Error
	if err != nil {
		onlineLog.Warn("touch online module compile task failed", "id", id, "err", err)
	}
}

// finishOnlineCompile 在编译入库成功后一次写完：回到 extracted、清掉任务字段并回填 scenario_id。
func finishOnlineCompile(id, scenarioID uint) error {
	res := models.DB.Model(&models.OnlineModule{}).
		Where("id = ? AND status = ?", id, models.OnlineModuleStatusDownloading).
		Updates(map[string]any{
			"status":          models.OnlineModuleStatusExtracted,
			"error":           "",
			"stage":           "",
			"stage_note":      "",
			"task_started_at": nil,
			"scenario_id":     scenarioID,
		})
	if res.Error != nil {
		onlineLog.Error("backfill online module scenario id failed", "id", id, "scenario_id", scenarioID, "err", res.Error)
		return fmt.Errorf("模组已入库，但回填线上模组记录失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		onlineLog.Warn("online module left compile state before it finished", "id", id, "scenario_id", scenarioID)
	}
	publishOnlineModuleID(id)
	return nil
}

func runOnlineCompile(ctx context.Context, id uint, req agent.CompileStoryRequest, docPath string, prepare func(*models.Scenario)) error {
	logOnlineCompile(id, fmt.Sprintf("—— 开始编译《%s》（文档：%s）——", req.Name, docPath))

	stopBeat := make(chan struct{})
	beatDone := make(chan struct{})
	go func() {
		defer close(beatDone)
		ticker := time.NewTicker(onlineCompileHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				touchOnlineCompile(id)
			case <-stopBeat:
				return
			}
		}
	}()
	defer func() {
		close(stopBeat)
		<-beatDone
	}()

	progress := func(stage, status, detail string) {
		text := compileProgressText(stage, status, detail)
		logOnlineCompile(id, text)
		if stage != "exchange" {
			setOnlineStage(id, models.OnlineStageCompile, text)
		}
	}
	scenario, err := compileStoryToScenario(ctx, req, prepare, nil, progress)
	if err != nil {
		return err
	}
	if err := finishOnlineCompile(id, scenario.ID); err != nil {
		return err
	}
	logOnlineCompile(id, fmt.Sprintf("✅ 编译完成：《%s》，已入库", scenario.Name))
	return nil
}

// OnlineModuleCompileLog 返回条目当前缓冲里的编译日志：lines[i] 的绝对行号是 base+i。
// 实时的新行经 WebSocket 的 log 帧推送，客户端按行号合并两路数据。
func OnlineModuleCompileLog(c *gin.Context) {
	m, ok := onlineModuleFromParam(c)
	if !ok {
		return
	}
	base, lines := onlineLogs.snapshot(m.ID)
	c.JSON(http.StatusOK, gin.H{"base": base, "lines": lines})
}
