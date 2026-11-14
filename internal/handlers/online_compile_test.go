package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/onlinemod"
)

type compileStub func(ctx context.Context, req agent.CompileStoryRequest, progress agent.ScripterProgressFunc) (agent.ScenarioCreationOutput, error)

// gate 是测试里卡住流水线的闸门。用例中途失败时也要放行，否则后台任务一直不结束，会拖垮后面所有用例。
type gate struct {
	ch   chan struct{}
	once sync.Once
}

func newGate() *gate { return &gate{ch: make(chan struct{})} }

func (g *gate) open() { g.once.Do(func() { close(g.ch) }) }

// NOTE: Cleanup 后注册先执行：必须在 stubCompilePipeline 之后调用，才能赶在它等待后台任务之前放行。
func (g *gate) openOnCleanup(t *testing.T) { t.Cleanup(g.open) }

// stubCompilePipeline 替换故事编译流水线。还原前必须等后台任务结束，它们会在请求返回后继续读这个全局变量。
func stubCompilePipeline(t *testing.T, fn compileStub) {
	t.Helper()
	prev := compileStoryPipeline
	compileStoryPipeline = fn
	t.Cleanup(func() {
		waitOnlineTasks(t)
		compileStoryPipeline = prev
	})
}

func compileOutput(name string) agent.ScenarioCreationOutput {
	return agent.ScenarioCreationOutput{
		Draft:         agent.ScenarioDraft{Name: name, Description: "简介", Tags: "悬疑"},
		GenerationLog: "LLM 记录",
		StoryDocument: "故事全文",
	}
}

type compileLogBody struct {
	Base  int      `json:"base"`
	Lines []string `json:"lines"`
}

func getCompileLog(t *testing.T, r http.Handler, id uint) compileLogBody {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/admin/online-modules/%d/compile-log", id), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("compile-log status = %d (%s)", w.Code, w.Body.String())
	}
	var out compileLogBody
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode compile-log: %v", err)
	}
	return out
}

func startCompile(r http.Handler, id, docID uint, name string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	body := map[string]any{"document_id": docID}
	if name != "" {
		body["name"] = name
	}
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/admin/online-modules/%d/compile", id), body))
	return w
}

func seedCompilable(t *testing.T) (models.OnlineModule, models.OnlineModuleDocument) {
	t.Helper()
	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, nil)
	return m, seedOnlineDoc(t, m.ID, onlineStory(""), nil)
}

var compileLinePrefix = regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2}\] `)

func TestOnlineModuleCompile_RunsInBackgroundAndBackfillsScenario(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	release := newGate()
	stubCompilePipeline(t, func(ctx context.Context, req agent.CompileStoryRequest, progress agent.ScripterProgressFunc) (agent.ScenarioCreationOutput, error) {
		progress("compile", "start", "Compiler 编译结构化数据…")
		progress("exchange", "compile", "  第一轮\n工具调用 ")
		<-release.ch
		if req.StoryDocument == "" || req.Name != "雨夜档案" {
			return agent.ScenarioCreationOutput{}, errors.New("请求不对")
		}
		return compileOutput("流水线给的名字"), nil
	})
	release.openOnCleanup(t)
	m, doc := seedCompilable(t)

	w := startCompile(r, m.ID, doc.ID, "雨夜档案")
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", w.Code, w.Body.String())
	}
	var accepted models.OnlineModule
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if accepted.Status != models.OnlineModuleStatusDownloading || accepted.Stage != models.OnlineStageCompile || accepted.TaskStartedAt == nil {
		t.Fatalf("202 响应应是编译中的条目: %+v", accepted)
	}

	// 任务还在跑时，日志已经能读到，阶段说明跟着流水线走。
	deadline := time.Now().Add(2 * time.Second)
	var logs compileLogBody
	for time.Now().Before(deadline) {
		if logs = getCompileLog(t, r, m.ID); len(logs.Lines) >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(logs.Lines) != 3 || logs.Base != 0 {
		t.Fatalf("运行中的日志 = %+v", logs)
	}
	for _, l := range logs.Lines {
		if !compileLinePrefix.MatchString(l) {
			t.Errorf("日志行缺少时间前缀: %q", l)
		}
	}
	if !strings.Contains(logs.Lines[0], "开始编译《雨夜档案》") || !strings.Contains(logs.Lines[1], "Compiler 编译结构化数据") {
		t.Errorf("日志内容不对: %v", logs.Lines)
	}
	if !strings.Contains(logs.Lines[2], "· LLM 交互 [compile] 第一轮 工具调用") {
		t.Errorf("exchange 行应压成一行: %q", logs.Lines[2])
	}
	running := loadOnlineModule(t, m.ID)
	if running.Status != models.OnlineModuleStatusDownloading || running.Stage != models.OnlineStageCompile || running.StageNote != "Compiler 编译结构化数据…" {
		t.Fatalf("运行中的状态 = %+v", running)
	}

	release.open()
	waitOnlineTasks(t)

	done := loadOnlineModule(t, m.ID)
	if done.Status != models.OnlineModuleStatusExtracted || done.Error != "" || done.Stage != "" || done.StageNote != "" || done.TaskStartedAt != nil {
		t.Fatalf("完成后应回到 extracted 并清空阶段: %+v", done)
	}
	if done.ScenarioID == nil {
		t.Fatalf("scenario_id 没有回填: %+v", done)
	}
	var sc models.Scenario
	if err := models.DB.First(&sc, *done.ScenarioID).Error; err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	if sc.Name != "雨夜档案" || sc.Author != "某作者（cnmods）" || !sc.IsActive {
		t.Fatalf("入库的模组 = %+v", sc)
	}
	var genLog models.ScenarioGenerationLog
	if err := models.DB.Where("scenario_id = ?", sc.ID).First(&genLog).Error; err != nil || genLog.StoryDocument != "故事全文" {
		t.Fatalf("生成记录 = %+v, err = %v", genLog, err)
	}
	logs = getCompileLog(t, r, m.ID)
	if last := logs.Lines[len(logs.Lines)-1]; !strings.Contains(last, "✅ 编译完成：《雨夜档案》") {
		t.Errorf("最后一行 = %q", last)
	}
}

func TestOnlineModuleCompile_FailureRestoresExtractedWithError(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	stubCompilePipeline(t, func(context.Context, agent.CompileStoryRequest, agent.ScripterProgressFunc) (agent.ScenarioCreationOutput, error) {
		return agent.ScenarioCreationOutput{}, errors.New("模型没有返回内容")
	})
	m, doc := seedCompilable(t)
	oldScenario := uint(99)
	models.DB.Model(&models.OnlineModule{}).Where("id = ?", m.ID).Update("scenario_id", oldScenario)

	if w := startCompile(r, m.ID, doc.ID, ""); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
	}
	waitOnlineTasks(t)

	got := loadOnlineModule(t, m.ID)
	if got.Status != models.OnlineModuleStatusExtracted || got.Stage != "" || got.TaskStartedAt != nil {
		t.Fatalf("失败后应回到 extracted: %+v", got)
	}
	if got.Error != "模组编译失败: 模型没有返回内容" {
		t.Fatalf("error = %q", got.Error)
	}
	if got.ScenarioID == nil || *got.ScenarioID != oldScenario {
		t.Fatalf("失败不应改动已有的 scenario_id: %v", got.ScenarioID)
	}
	var docs int64
	models.DB.Model(&models.OnlineModuleDocument{}).Where("online_module_id = ?", m.ID).Count(&docs)
	if docs != 1 {
		t.Fatalf("编译失败不应动文档, docs = %d", docs)
	}
	logs := getCompileLog(t, r, m.ID)
	if last := logs.Lines[len(logs.Lines)-1]; !strings.Contains(last, "❌ 模组编译失败: 模型没有返回内容") {
		t.Errorf("失败应写进日志: %v", logs.Lines)
	}
}

func TestOnlineModuleCompile_RejectsSecondRunWhileRunning(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	release := newGate()
	stubCompilePipeline(t, func(ctx context.Context, _ agent.CompileStoryRequest, _ agent.ScripterProgressFunc) (agent.ScenarioCreationOutput, error) {
		<-release.ch
		return compileOutput("甲"), nil
	})
	release.openOnCleanup(t)
	m, doc := seedCompilable(t)

	if w := startCompile(r, m.ID, doc.ID, "甲"); w.Code != http.StatusAccepted {
		t.Fatalf("first status = %d (%s)", w.Code, w.Body.String())
	}
	// 运行中状态是 downloading，不再满足「已提取」前置条件，同样得到 409。
	if w := startCompile(r, m.ID, doc.ID, "乙"); w.Code != http.StatusConflict {
		t.Fatalf("second status = %d, want 409 (%s)", w.Code, w.Body.String())
	}
	if w := serveOnline(r, jsonReq("DELETE", fmt.Sprintf("/admin/online-modules/%d", m.ID), nil)); w.Code != http.StatusConflict {
		t.Fatalf("编译中不能删除: status = %d", w.Code)
	}
	release.open()
	waitOnlineTasks(t)
}

func TestOnlineModuleCompile_ClaimRaceHasSingleWinner(t *testing.T) {
	initOnlineTest(t, &fakeOnlineSource{})
	m, _ := seedCompilable(t)
	first, err := claimOnlineModule(m.ID, models.OnlineStageCompile, "")
	if err != nil || !first {
		t.Fatalf("first claim = %v, %v", first, err)
	}
	second, err := claimOnlineModule(m.ID, models.OnlineStageCompile, "")
	if err != nil || second {
		t.Fatalf("second claim = %v, %v, want false", second, err)
	}
}

func TestOnlineModuleCompile_ServiceShuttingDown(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	stubCompilePipeline(t, func(context.Context, agent.CompileStoryRequest, agent.ScripterProgressFunc) (agent.ScenarioCreationOutput, error) {
		t.Error("关闭中不应启动流水线")
		return agent.ScenarioCreationOutput{}, nil
	})
	m, doc := seedCompilable(t)
	prev := onlineRunner
	onlineRunner = newOnlineTaskRunner()
	onlineRunner.stop(time.Second)
	t.Cleanup(func() { onlineRunner = prev })

	if w := startCompile(r, m.ID, doc.ID, ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (%s)", w.Code, w.Body.String())
	}
	got := loadOnlineModule(t, m.ID)
	if got.Status != models.OnlineModuleStatusExtracted || got.Stage != "" {
		t.Fatalf("关闭中不应把条目留在进行中: %+v", got)
	}
}

func TestOnlineModuleCompile_CancelledByShutdownIsReportedAsInterrupted(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	started := make(chan struct{})
	stubCompilePipeline(t, func(ctx context.Context, _ agent.CompileStoryRequest, _ agent.ScripterProgressFunc) (agent.ScenarioCreationOutput, error) {
		close(started)
		<-ctx.Done()
		return agent.ScenarioCreationOutput{}, ctx.Err()
	})
	m, doc := seedCompilable(t)
	prev := onlineRunner
	onlineRunner = newOnlineTaskRunner()
	t.Cleanup(func() { onlineRunner = prev })

	if w := startCompile(r, m.ID, doc.ID, ""); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
	}
	<-started
	onlineRunner.stop(5 * time.Second)

	got := loadOnlineModule(t, m.ID)
	if got.Status != models.OnlineModuleStatusExtracted || got.Error != onlineCompileInterruptedMessage {
		t.Fatalf("关机取消后的状态 = %+v", got)
	}
}

func TestOnlineModuleCompile_PanicIsReportedNotCrashing(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	stubCompilePipeline(t, func(context.Context, agent.CompileStoryRequest, agent.ScripterProgressFunc) (agent.ScenarioCreationOutput, error) {
		panic("boom")
	})
	m, doc := seedCompilable(t)

	if w := startCompile(r, m.ID, doc.ID, ""); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
	}
	waitOnlineTasks(t)
	got := loadOnlineModule(t, m.ID)
	if got.Status != models.OnlineModuleStatusExtracted || !strings.Contains(got.Error, "内部错误") {
		t.Fatalf("panic 应转成失败: %+v", got)
	}
}

func TestOnlineModuleCompile_PushesLogAndStageFramesOverWS(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	release := newGate()
	stubCompilePipeline(t, func(ctx context.Context, _ agent.CompileStoryRequest, progress agent.ScripterProgressFunc) (agent.ScenarioCreationOutput, error) {
		progress("compile", "start", "第一步")
		<-release.ch
		return compileOutput("雨夜档案"), nil
	})
	release.openOnCleanup(t)
	m, doc := seedCompilable(t)

	c := onlineWSDial(t, srv)
	readOnlineSnapshot(t, c)
	if w := startCompile(r, m.ID, doc.ID, "雨夜档案"); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
	}

	type logData struct {
		ID    uint   `json:"id"`
		Index int    `json:"index"`
		Line  string `json:"line"`
	}
	var lines []logData
	sawCompileModule := false
	for len(lines) < 2 {
		f := wsRead(t, c)
		switch f.Type {
		case "log":
			var d logData
			if err := json.Unmarshal(f.Data, &d); err != nil {
				t.Fatalf("decode log: %v", err)
			}
			lines = append(lines, d)
		case "module":
			var mod models.OnlineModule
			if err := json.Unmarshal(f.Data, &mod); err != nil {
				t.Fatalf("decode module: %v", err)
			}
			if mod.Stage == models.OnlineStageCompile && mod.StageNote == "第一步" {
				sawCompileModule = true
			}
		}
	}
	if lines[0].Index != 0 || lines[1].Index != 1 || lines[0].ID != m.ID || !compileLinePrefix.MatchString(lines[1].Line) || !strings.Contains(lines[1].Line, "第一步") {
		t.Fatalf("log 帧 = %+v", lines)
	}

	release.open()
	var final models.OnlineModule
	for {
		f := wsRead(t, c)
		if f.Type != "module" {
			continue
		}
		if err := json.Unmarshal(f.Data, &final); err != nil {
			t.Fatalf("decode module: %v", err)
		}
		// NOTE: 服务端先推日志帧再推阶段帧，所以阶段帧可能在上面的循环结束之后才到。
		if final.Stage == models.OnlineStageCompile && final.StageNote == "第一步" {
			sawCompileModule = true
		}
		if final.Status == models.OnlineModuleStatusExtracted {
			break
		}
	}
	if final.ScenarioID == nil || final.Stage != "" {
		t.Fatalf("最终的 module 帧 = %+v", final)
	}
	if !sawCompileModule {
		t.Error("没有收到带阶段说明的 module 帧")
	}
}

func TestOnlineModuleCompileLog_UnknownModuleAndEmpty(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/online-modules/9999/compile-log", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown module: status = %d, want 404", w.Code)
	}
	m, _ := seedCompilable(t)
	if got := getCompileLog(t, r, m.ID); got.Base != 0 || got.Lines == nil || len(got.Lines) != 0 {
		t.Fatalf("没有日志时应返回 base 0 和空数组: %+v", got)
	}
}

func TestOnlineLogStore_KeepsLatestLinesWithAbsoluteIndex(t *testing.T) {
	s := &onlineLogStore{bufs: map[uint]*onlineLogBuf{}}
	total := onlineLogMaxLines + 25
	for i := 0; i < total; i++ {
		if got := s.append(1, fmt.Sprintf("行%d", i)); got != i {
			t.Fatalf("append #%d 返回的行号 = %d", i, got)
		}
	}
	base, lines := s.snapshot(1)
	if base != 25 || len(lines) != onlineLogMaxLines {
		t.Fatalf("base = %d, len = %d", base, len(lines))
	}
	if lines[0] != "行25" || lines[len(lines)-1] != fmt.Sprintf("行%d", total-1) {
		t.Fatalf("保留的应是最新的行: %q ... %q", lines[0], lines[len(lines)-1])
	}
	// 快照是副本，改它不影响缓冲。
	lines[0] = "被改了"
	if _, again := s.snapshot(1); again[0] != "行25" {
		t.Fatal("snapshot 应返回副本")
	}
	// 另一个条目互不影响，删除后从头开始。
	if got := s.append(2, "x"); got != 0 {
		t.Fatalf("另一个条目的行号 = %d", got)
	}
	s.drop(1)
	if base, lines := s.snapshot(1); base != 0 || len(lines) != 0 {
		t.Fatalf("drop 后应为空: %d %v", base, lines)
	}
}

func TestLogOnlineCompile_CollapsesWhitespaceAndTruncates(t *testing.T) {
	initOnlineTest(t, &fakeOnlineSource{})
	const id = 4242
	t.Cleanup(func() { onlineLogs.drop(id) })

	logOnlineCompile(id, "a \n\t b")
	logOnlineCompile(id, strings.Repeat("长", onlineLogLineRunes+50))
	_, lines := onlineLogs.snapshot(id)
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "] a b") {
		t.Fatalf("空白应压成单个空格: %q", lines)
	}
	body := compileLinePrefix.ReplaceAllString(lines[1], "")
	if n := len([]rune(body)); n != onlineLogLineRunes+1 || !strings.HasSuffix(body, "…") {
		t.Fatalf("超长行应截断并加省略号, runes = %d", n)
	}
}

func TestOnlineModuleDelete_DropsCompileLog(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	m, _ := seedCompilable(t)
	logOnlineCompile(m.ID, "残留")
	t.Cleanup(func() { onlineLogs.drop(m.ID) })

	if w := serveOnline(r, jsonReq("DELETE", fmt.Sprintf("/admin/online-modules/%d", m.ID), nil)); w.Code != http.StatusOK {
		t.Fatalf("delete status = %d (%s)", w.Code, w.Body.String())
	}
	if base, lines := onlineLogs.snapshot(m.ID); base != 0 || len(lines) != 0 {
		t.Fatalf("删除条目后日志应清掉: %d %v", base, lines)
	}
}

func TestFailOrphanOnlineTasks_CompileAlwaysRestoresExtracted(t *testing.T) {
	initOnlineTest(t, &fakeOnlineSource{})
	started := time.Now()
	// 编译任务即使一份文档都查不到，也不能标成 failed：它从来没动过文档。
	m := seedOnlineModule(t, models.OnlineModuleStatusDownloading, func(m *models.OnlineModule) {
		m.Stage, m.StageNote, m.TaskStartedAt = models.OnlineStageCompile, "编译中", &started
	})

	FailOrphanOnlineTasks()

	got := loadOnlineModule(t, m.ID)
	if got.Status != models.OnlineModuleStatusExtracted || got.Error != onlineCompileInterruptedMessage || got.Stage != "" || got.TaskStartedAt != nil {
		t.Fatalf("编译任务的孤儿应恢复为 extracted: %+v", got)
	}
}

func TestOnlineCompileHeartbeatRefreshesUpdatedAt(t *testing.T) {
	initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusDownloading, func(m *models.OnlineModule) { m.Stage = models.OnlineStageCompile })
	old := time.Now().Add(-onlineDownloadStaleAfter - time.Minute)
	models.DB.Model(&models.OnlineModule{}).Where("id = ?", m.ID).UpdateColumn("updated_at", old)

	touchOnlineCompile(m.ID)

	got := loadOnlineModule(t, m.ID)
	if !got.UpdatedAt.After(old.Add(time.Minute)) || got.Stage != models.OnlineStageCompile {
		t.Fatalf("心跳应刷新 updated_at 且不改阶段: %+v", got)
	}
	if ok, err := claimOnlineModule(m.ID, models.OnlineStageCompile, ""); err != nil || ok {
		t.Fatalf("心跳之后不应被当成僵死任务抢走: %v %v", ok, err)
	}
}

func TestOnlineBrowse_LocalRefCarriesStage(t *testing.T) {
	src := &fakeOnlineSource{}
	src.list = func(onlinemod.ListQuery) (onlinemod.ListPage, error) {
		return onlinemod.ListPage{Items: []onlinemod.CnmodsItem{{KeyID: "7001", Title: "编译中的"}}}, nil
	}
	r := initOnlineTest(t, src)
	seedOnlineModule(t, models.OnlineModuleStatusDownloading, func(m *models.OnlineModule) {
		m.SourceKey, m.Stage = "7001", models.OnlineStageCompile
	})

	w := serveOnline(r, jsonReq("GET", "/admin/online-modules/browse", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Items []struct {
			Local *struct {
				Status string `json:"status"`
				Stage  string `json:"stage"`
			} `json:"local"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Local == nil || resp.Items[0].Local.Stage != models.OnlineStageCompile {
		t.Fatalf("浏览结果的 local 应带 stage: %+v", resp)
	}
}
