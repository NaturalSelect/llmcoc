package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/llmcoc/server/internal/models"
)

func onlineWSDial(t *testing.T, srv *httptest.Server) *wsClient {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/admin/online-modules/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial: %v (status %d)", err, status)
	}
	return newWSClient(t, conn)
}

// waitOnlineSubscribers 等订阅数降到 want。
func waitOnlineSubscribers(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		onlineHubInst.mu.Lock()
		n := len(onlineHubInst.subs)
		onlineHubInst.mu.Unlock()
		if n == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("订阅数没有降到 %d", want)
}

type onlineSnapshotData struct {
	Modules []models.OnlineModule `json:"modules"`
}

func readOnlineSnapshot(t *testing.T, c *wsClient) []models.OnlineModule {
	t.Helper()
	f := wsRead(t, c)
	if f.Type != "snapshot" {
		t.Fatalf("第一帧应是 snapshot，实际 %q", f.Type)
	}
	var d onlineSnapshotData
	if err := json.Unmarshal(f.Data, &d); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return d.Modules
}

func readOnlineModuleFrame(t *testing.T, c *wsClient) models.OnlineModule {
	t.Helper()
	f := wsRead(t, c)
	if f.Type != "module" {
		t.Fatalf("应收到 module 帧，实际 %q: %s", f.Type, f.Data)
	}
	var m models.OnlineModule
	if err := json.Unmarshal(f.Data, &m); err != nil {
		t.Fatalf("decode module: %v", err)
	}
	return m
}

func TestOnlineWSSnapshotHasRecentAndAllActive(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	var first models.OnlineModule
	for i := 0; i < onlineSnapshotRecent+5; i++ {
		m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, func(m *models.OnlineModule) {
			m.SourceKey = fmt.Sprintf("snap-%d", i)
		})
		if i == 0 {
			first = m
		}
	}
	// 最旧的一条仍在下载：不在最近 50 条里，也必须出现在快照里。
	models.DB.Model(&models.OnlineModule{}).Where("id = ?", first.ID).
		UpdateColumns(map[string]any{"status": models.OnlineModuleStatusDownloading, "updated_at": time.Now().Add(-time.Hour)})

	mods := readOnlineSnapshot(t, onlineWSDial(t, srv))
	if len(mods) != onlineSnapshotRecent+1 {
		t.Fatalf("快照条目数 = %d, want %d", len(mods), onlineSnapshotRecent+1)
	}
	found := false
	for _, m := range mods {
		if m.ID == first.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("快照缺少仍在进行中的旧条目")
	}
}

func TestOnlineWSEmptySnapshotIsEmptyArray(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	f := wsRead(t, onlineWSDial(t, srv))
	if !strings.Contains(string(f.Data), `"modules":[]`) {
		t.Fatalf("空快照的 modules 应是 []，实际 %s", f.Data)
	}
}

// 整条链路：下载请求 → 进行中（解析） → 下载 → 提取 → 完成，每一步都经 WebSocket 推出，顺序固定。
func TestOnlineWSPushesDownloadStagesInOrder(t *testing.T) {
	releaseResolve := make(chan struct{})
	releaseDownload := make(chan struct{})
	src := &fakeOnlineSource{
		resolve: func(string) (string, string, error) {
			<-releaseResolve
			return "https://cdn.test/f", "module.zip", nil
		},
		download: func(string) ([]byte, error) {
			<-releaseDownload
			return onlineZip(t, "module.txt", onlineStory("")), nil
		},
	}
	r := initOnlineTest(t, src)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	c := onlineWSDial(t, srv)
	if mods := readOnlineSnapshot(t, c); len(mods) != 0 {
		t.Fatalf("初始快照应为空，实际 %d 条", len(mods))
	}

	w := serveOnline(r, jsonReq("POST", "/admin/online-modules/download", downloadBody(5051, "https://share.test/abc")))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	if m := readOnlineModuleFrame(t, c); m.Status != models.OnlineModuleStatusDownloading || m.Stage != models.OnlineStageResolve || m.TaskStartedAt == nil {
		t.Fatalf("第一帧应是解析阶段: %+v", m)
	}
	close(releaseResolve)
	if m := readOnlineModuleFrame(t, c); m.Stage != models.OnlineStageDownload || m.Status != models.OnlineModuleStatusDownloading {
		t.Fatalf("第二帧应是下载阶段: %+v", m)
	}
	close(releaseDownload)
	if m := readOnlineModuleFrame(t, c); m.Stage != models.OnlineStageExtract || m.Status != models.OnlineModuleStatusDownloading {
		t.Fatalf("第三帧应是提取阶段: %+v", m)
	}
	done := readOnlineModuleFrame(t, c)
	if done.Status != models.OnlineModuleStatusExtracted || done.Stage != "" || done.TaskStartedAt != nil || done.Error != "" {
		t.Fatalf("最后应是已提取且阶段清空: %+v", done)
	}
}

func TestOnlineWSPushesFailure(t *testing.T) {
	src := &fakeOnlineSource{
		resolve: func(string) (string, string, error) { return "", "", errors.New("boom") },
	}
	r := initOnlineTest(t, src)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	c := onlineWSDial(t, srv)
	readOnlineSnapshot(t, c)

	serveOnline(r, jsonReq("POST", "/admin/online-modules/download", downloadBody(5051, "https://share.test/abc")))
	var last models.OnlineModule
	for i := 0; i < 3; i++ {
		last = readOnlineModuleFrame(t, c)
		if last.Status == models.OnlineModuleStatusFailed {
			break
		}
	}
	if last.Status != models.OnlineModuleStatusFailed || last.Error == "" || last.Stage != "" {
		t.Fatalf("应推送失败状态和原因: %+v", last)
	}
}

func TestOnlineWSPushesOCRStage(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	useOnlineOCR(t, &fakeOnlineOCR{text: onlineStory("")})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	m := seedOnlineModule(t, models.OnlineModuleStatusFailed, nil)
	c := onlineWSDial(t, srv)
	readOnlineSnapshot(t, c)

	w := serveOnline(r, onlineUpload(t, fmt.Sprintf("/admin/online-modules/%d/archive", m.ID), "scan.pdf", emptyTextPDF()))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var stages []string
	var final models.OnlineModule
	for i := 0; i < 8; i++ {
		final = readOnlineModuleFrame(t, c)
		if final.Status != models.OnlineModuleStatusDownloading {
			break
		}
		stages = append(stages, final.Stage+"|"+final.StageNote)
	}
	joined := strings.Join(stages, ",")
	if !strings.Contains(joined, models.OnlineStageOCR+"|scan.pdf") {
		t.Fatalf("应推送带文件名的 OCR 阶段，实际 %v", stages)
	}
	if final.Status != models.OnlineModuleStatusExtracted {
		t.Fatalf("最后应已提取: %+v", final)
	}
}

func TestOnlineWSPushesDeleteAndOverride(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, nil)
	seedOnlineDoc(t, m.ID, onlineStory("本模组禁止投喂给AI使用。"), []string{"禁止投喂AI"})
	c := onlineWSDial(t, srv)
	readOnlineSnapshot(t, c)

	if w := serveOnline(r, jsonReq("POST", fmt.Sprintf("/admin/online-modules/%d/license-override", m.ID), nil)); w.Code != http.StatusOK {
		t.Fatalf("override status = %d: %s", w.Code, w.Body.String())
	}
	if got := readOnlineModuleFrame(t, c); got.ID != m.ID || got.LicenseOverrideAt == nil {
		t.Fatalf("放行后应推送带放行时间的条目: %+v", got)
	}

	if w := serveOnline(r, jsonReq("DELETE", fmt.Sprintf("/admin/online-modules/%d", m.ID), nil)); w.Code != http.StatusOK {
		t.Fatalf("delete status = %d: %s", w.Code, w.Body.String())
	}
	f := wsRead(t, c)
	if f.Type != "deleted" || !strings.Contains(string(f.Data), fmt.Sprintf(`"id":%d`, m.ID)) {
		t.Fatalf("删除后应推送 deleted 帧: %+v %s", f, f.Data)
	}
}

func TestOnlineWSBroadcastsToAllSubscribers(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, nil)
	a, b := onlineWSDial(t, srv), onlineWSDial(t, srv)
	readOnlineSnapshot(t, a)
	readOnlineSnapshot(t, b)

	publishOnlineModuleID(m.ID)
	if got := readOnlineModuleFrame(t, a); got.ID != m.ID {
		t.Fatalf("a 收到 %+v", got)
	}
	if got := readOnlineModuleFrame(t, b); got.ID != m.ID {
		t.Fatalf("b 收到 %+v", got)
	}
}

func TestOnlineHubDropsSlowSubscriber(t *testing.T) {
	initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, nil)
	hub := &onlineHub{subs: map[*onlineSub]struct{}{}}
	slow, _, err := hub.subscribe()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= onlineSubscriberQueue; i++ {
		hub.publishModule(m.ID)
	}
	select {
	case <-slow.done:
	default:
		t.Fatal("队列溢出的订阅者应被踢掉")
	}
	if !slow.slow {
		t.Fatal("应标记为慢消费者")
	}
	hub.mu.Lock()
	n := len(hub.subs)
	hub.mu.Unlock()
	if n != 0 {
		t.Fatalf("被踢后订阅数 = %d", n)
	}
	// 重复退订不应 panic。
	hub.unsubscribe(slow)
}

func TestOnlineHubPublishWithoutSubscribersIsNoop(t *testing.T) {
	initOnlineTest(t, &fakeOnlineSource{})
	hub := &onlineHub{subs: map[*onlineSub]struct{}{}}
	hub.publishModule(12345)
	hub.publishDeleted(12345)
}

func TestOnlineRunnerStopCancelsRunningTasks(t *testing.T) {
	initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusDownloading, nil)
	runner := newOnlineTaskRunner()
	started := make(chan struct{})
	if err := runner.start(m.ID, models.OnlineModuleStatusFailed, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	runner.stop(5 * time.Second)

	got := loadOnlineModule(t, m.ID)
	if got.Status != models.OnlineModuleStatusFailed || got.Error != onlineInterruptedMessage {
		t.Fatalf("停止后应标记为中断: %+v", got)
	}
	if err := runner.start(m.ID, models.OnlineModuleStatusFailed, func(context.Context) error { return nil }); !errors.Is(err, errOnlineShuttingDown) {
		t.Fatalf("停止后不应再接新任务，err = %v", err)
	}
}

func TestOnlineRunnerStopGivesUpAfterDeadline(t *testing.T) {
	initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusDownloading, nil)
	runner := newOnlineTaskRunner()
	release := make(chan struct{})
	started := make(chan struct{})
	_ = runner.start(m.ID, models.OnlineModuleStatusFailed, func(context.Context) error {
		close(started)
		<-release
		return nil
	})
	<-started
	begin := time.Now()
	runner.stop(100 * time.Millisecond)
	if d := time.Since(begin); d > 2*time.Second {
		t.Fatalf("stop 没有按期限放弃，耗时 %v", d)
	}
	close(release)
	runner.wg.Wait()
}

func TestOnlineRunnerRecoversFromPanic(t *testing.T) {
	initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusDownloading, nil)
	runner := newOnlineTaskRunner()
	if err := runner.start(m.ID, models.OnlineModuleStatusFailed, func(context.Context) error { panic("kaboom") }); err != nil {
		t.Fatal(err)
	}
	runner.wg.Wait()
	got := loadOnlineModule(t, m.ID)
	if got.Status != models.OnlineModuleStatusFailed || !strings.Contains(got.Error, "kaboom") {
		t.Fatalf("panic 应转成失败状态: %+v", got)
	}
}

func TestOnlineFinishTaskLeavesOtherStatesAlone(t *testing.T) {
	initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, func(m *models.OnlineModule) { m.Error = "" })
	finishOnlineTask(m.ID, models.OnlineModuleStatusFailed, "不该写入")
	setOnlineStage(m.ID, models.OnlineStageOCR, "x.pdf")
	got := loadOnlineModule(t, m.ID)
	if got.Status != models.OnlineModuleStatusExtracted || got.Error != "" || got.Stage != "" {
		t.Fatalf("不在下载状态的条目不应被任务收尾改动: %+v", got)
	}
}

func TestOnlineSetStageTruncatesNote(t *testing.T) {
	initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusDownloading, nil)
	setOnlineStage(m.ID, models.OnlineStageOCR, strings.Repeat("长", 500))
	got := loadOnlineModule(t, m.ID)
	if n := len([]rune(got.StageNote)); n != onlineStageNoteRunes {
		t.Fatalf("stage_note 字数 = %d, want %d", n, onlineStageNoteRunes)
	}
}

func TestFailOrphanOnlineTasks(t *testing.T) {
	initOnlineTest(t, &fakeOnlineSource{})
	started := time.Now()
	withDocs := seedOnlineModule(t, models.OnlineModuleStatusDownloading, func(m *models.OnlineModule) {
		m.Stage, m.StageNote, m.TaskStartedAt = models.OnlineStageExtract, "a.pdf", &started
	})
	seedOnlineDoc(t, withDocs.ID, "旧正文", nil)
	noDocs := seedOnlineModule(t, models.OnlineModuleStatusDownloading, func(m *models.OnlineModule) {
		m.Stage, m.TaskStartedAt = models.OnlineStageDownload, &started
	})
	idle := seedOnlineModule(t, models.OnlineModuleStatusFailed, func(m *models.OnlineModule) { m.Error = "原有错误" })

	FailOrphanOnlineTasks()

	got := loadOnlineModule(t, withDocs.ID)
	if got.Status != models.OnlineModuleStatusExtracted || got.Error != onlineInterruptedMessage || got.Stage != "" || got.TaskStartedAt != nil {
		t.Fatalf("有文档的应恢复为 extracted: %+v", got)
	}
	got = loadOnlineModule(t, noDocs.ID)
	if got.Status != models.OnlineModuleStatusFailed || got.Error != onlineInterruptedMessage || got.Stage != "" {
		t.Fatalf("没有文档的应标记为 failed: %+v", got)
	}
	if got := loadOnlineModule(t, idle.ID); got.Error != "原有错误" {
		t.Fatalf("不在进行中的条目不应被改动: %+v", got)
	}
}

func TestOnlineModuleDownload_ServiceShuttingDown(t *testing.T) {
	src := &fakeOnlineSource{}
	r := initOnlineTest(t, src)
	prev := onlineRunner
	onlineRunner = newOnlineTaskRunner()
	onlineRunner.stop(time.Second)
	t.Cleanup(func() { onlineRunner = prev })

	w := serveOnline(r, jsonReq("POST", "/admin/online-modules/download", downloadBody(5051, "https://share.test/abc")))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (%s)", w.Code, w.Body.String())
	}
	var m models.OnlineModule
	models.DB.Where("source_key = ?", "5051").First(&m)
	if m.Status == models.OnlineModuleStatusDownloading {
		t.Fatalf("关闭中不应把条目留在下载状态: %+v", m)
	}
}
