package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/handlers/mocks"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/imagestore"
	"go.uber.org/mock/gomock"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func initTestDB(t *testing.T) {
	t.Helper()
	sessionMutex = sync.Map{}
	sessionProcessing = sync.Map{}
	// 每个测试使用独立内存库,并限制单连接避免SQLite多连接看不到schema。
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&models.User{},
		&models.CharacterCard{},
		&models.CharacterDraft{},
		&models.Scenario{},
		&models.ScenarioGenerationLog{},
		&models.GameSession{},
		&models.SessionPlayer{},
		&models.SessionNPC{},
		&models.SessionTurnAction{},
		&models.Message{},
		&models.LLMProviderConfig{},
		&models.AgentConfig{},
		&models.GameEvaluation{},
		&models.ShopItem{},
		&models.Transaction{},
		&models.CoinRecharge{},
		&models.SiteSetting{},
		&models.InviteCode{},
	); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prev
		_ = sqlDB.Close()
	})
}

// seedPlayingSession creates a scenario, user, playing session, character and
// session player, and returns their IDs.
func seedPlayingSession(t *testing.T) (sessionID, userID uint) {
	t.Helper()

	scenario := models.Scenario{
		Name:       "Test Scenario",
		MinPlayers: 1,
		MaxPlayers: 4,
		IsActive:   true,
		Content:    models.JSONField[models.ScenarioContent]{Data: models.ScenarioContent{}},
	}
	if err := models.DB.Create(&scenario).Error; err != nil {
		t.Fatalf("create scenario: %v", err)
	}

	user := models.User{
		Username:     "tester",
		Email:        "tester@test.com",
		PasswordHash: "x",
		Role:         models.RoleUser,
		CardSlots:    3,
	}
	if err := models.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	session := models.GameSession{
		Name:       "Test Room",
		ScenarioID: scenario.ID,
		Status:     models.SessionStatusPlaying,
		MaxPlayers: 4,
		CreatedBy:  user.ID,
		TurnRound:  1,
	}
	if err := models.DB.Create(&session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}

	card := models.CharacterCard{
		UserID:   user.ID,
		Name:     "Test Char",
		IsActive: true,
		Stats:    models.JSONField[models.CharacterStats]{},
		Skills:   models.JSONField[map[string]int]{Data: map[string]int{}},
	}
	if err := models.DB.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}

	player := models.SessionPlayer{
		SessionID:       session.ID,
		UserID:          user.ID,
		CharacterCardID: card.ID,
		JoinedAt:        time.Now(),
	}
	if err := models.DB.Create(&player).Error; err != nil {
		t.Fatalf("create player: %v", err)
	}

	return session.ID, user.ID
}

// makeTestRouter builds a Gin engine with the chat route and auth values injected.
func makeTestRouter(h *SessionHandlers, userID uint, username string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("username", username)
		c.Next()
	})
	r.GET("/sessions/:id/chat-status", h.GetChatStatus)
	r.POST("/sessions/:id/chat", h.ChatStream)
	return r
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
	output  agent.RunOutput
}

type blockingWriterRunner struct {
	writerStarted chan struct{}
	writerRelease chan struct{}
}

func (*blockingWriterRunner) Run(context.Context, agent.GameContext) (agent.RunOutput, error) {
	return agent.RunOutput{
		KPReply:         "你听见门后传来脚步声。",
		WriterDirection: "描述门后的压迫感",
	}, nil
}

func (*blockingWriterRunner) RunWriter(context.Context, agent.GameContext, string) (string, error) {
	return "", nil
}

func (r *blockingWriterRunner) RunWriterStream(_ context.Context, _ agent.GameContext, _ string, onToken func(string)) (string, error) {
	select {
	case <-r.writerStarted:
	default:
		close(r.writerStarted)
	}
	<-r.writerRelease
	onToken("门后的呼吸声越来越近。")
	return "门后的呼吸声越来越近。", nil
}

func (*blockingWriterRunner) RunPainter(context.Context, agent.GameContext, agent.ImagePromptRequest) (string, error) {
	return "", nil
}

func (r *blockingRunner) Run(context.Context, agent.GameContext) (agent.RunOutput, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	<-r.release
	return r.output, nil
}

func (*blockingRunner) RunWriter(context.Context, agent.GameContext, string) (string, error) {
	return "", nil
}

func (*blockingRunner) RunWriterStream(context.Context, agent.GameContext, string, func(string)) (string, error) {
	return "", nil
}

func (*blockingRunner) RunPainter(context.Context, agent.GameContext, agent.ImagePromptRequest) (string, error) {
	return "", nil
}

func decodeChatStatus(t *testing.T, body string) chatStatusResponse {
	t.Helper()
	var status chatStatusResponse
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("decode chat status: %v\nbody: %s", err, body)
	}
	return status
}

type parsedSSEEvent struct {
	name string
	data string
}

func parseSSEEvents(body string) []parsedSSEEvent {
	var events []parsedSSEEvent
	scanner := bufio.NewScanner(strings.NewReader(body))
	var curEvent string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			curEvent = strings.TrimPrefix(line, "event:")
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimPrefix(line, "data:")
			events = append(events, parsedSSEEvent{name: curEvent, data: data})
		}
	}
	return events
}

func firstSSEEventIndex(events []parsedSSEEvent, name string) int {
	for i, event := range events {
		if event.name == name {
			return i
		}
	}
	return -1
}

// parseSSE 按事件类型聚合SSE响应数据。
func parseSSE(body string) map[string][]string {
	events := map[string][]string{}
	for _, event := range parseSSEEvents(body) {
		events[event.name] = append(events[event.name], event.data)
	}
	return events
}

// formPost builds an application/x-www-form-urlencoded POST request.
func formPost(path, content string) *http.Request {
	body := strings.NewReader("content=" + url.QueryEscape(content))
	req, _ := http.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// ── pure unit tests ───────────────────────────────────────────────────────────

func TestChatTruncate(t *testing.T) {
	tests := []struct {
		input  string
		max    int
		expect string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello…"},
		{"你好世界", 3, "你好世…"},
		{"", 5, ""},
	}
	for _, tc := range tests {
		got := chatTruncate(tc.input, tc.max)
		if got != tc.expect {
			t.Errorf("chatTruncate(%q, %d) = %q, want %q", tc.input, tc.max, got, tc.expect)
		}
	}
}

// ── handler integration tests (in-memory SQLite) ─────────────────────────────

func TestChatStream_SessionNotFound(t *testing.T) {
	initTestDB(t)
	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))
	r := makeTestRouter(h, 1, "tester")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost("/sessions/9999/chat", "hello"))

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestChatStream_NotPlaying(t *testing.T) {
	initTestDB(t)

	scenarioID := seedScenario(t, "s")
	user := models.User{Username: "u", Email: "u@u.com", PasswordHash: "x", CardSlots: 3}
	models.DB.Create(&user)
	session := models.GameSession{
		Name: "r", ScenarioID: scenarioID,
		Status: models.SessionStatusLobby, MaxPlayers: 4, CreatedBy: user.ID,
	}
	models.DB.Create(&session)

	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))
	r := makeTestRouter(h, user.ID, "u")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", session.ID), "hello"))

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestChatStream_UserNotInSession(t *testing.T) {
	initTestDB(t)
	sessionID, _ := seedPlayingSession(t)

	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))
	r := makeTestRouter(h, 999, "stranger")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "hello"))

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestChatStream_EmptyContent(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)

	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))
	r := makeTestRouter(h, userID, "tester")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), ""))

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestChatStream_AgentError(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)

	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Return(agent.RunOutput{}, fmt.Errorf("LLM timeout"))

	h := NewSessionHandlers(runner)
	r := makeTestRouter(h, userID, "tester")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我去探索地下室"))

	if w.Code != http.StatusOK {
		t.Fatalf("SSE response must be 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type want text/event-stream, got %q", ct)
	}
	events := parseSSE(w.Body.String())
	if len(events["error"]) == 0 {
		t.Errorf("expected SSE error event; body:\n%s", w.Body.String())
	}
	if !strings.Contains(events["error"][0], "LLM timeout") {
		t.Errorf("error event data want 'LLM timeout', got %q", events["error"][0])
	}
}

func TestChatStream_RejectsSecondRequestWhileProcessing(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	runner := &blockingRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
		output:  agent.RunOutput{WriterText: "处理完成"},
	}
	r := makeTestRouter(NewSessionHandlers(runner), userID, "tester")

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "第一条行动"))
		firstDone <- w
	}()

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("first request did not enter runner")
	}

	second := httptest.NewRecorder()
	r.ServeHTTP(second, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "重复行动"))
	if second.Code != http.StatusConflict {
		t.Fatalf("second request status = %d, want 409; body: %s", second.Code, second.Body.String())
	}

	close(runner.release)
	select {
	case first := <-firstDone:
		if first.Code != http.StatusOK {
			t.Fatalf("first request status = %d", first.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}

	var userMessageCount int64
	models.DB.Model(&models.Message{}).
		Where("session_id = ? AND role = ?", sessionID, models.MessageRoleUser).
		Count(&userMessageCount)
	if userMessageCount != 1 {
		t.Fatalf("user message count = %d, want 1", userMessageCount)
	}
}

func TestGetChatStatus_SinglePlayerProcessingRecoversAfterRefresh(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	runner := &blockingRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
		output:  agent.RunOutput{WriterText: "处理完成"},
	}
	r := makeTestRouter(NewSessionHandlers(runner), userID, "tester")

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "调查门锁"))
	}()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("request did not enter runner")
	}

	statusRecorder := httptest.NewRecorder()
	r.ServeHTTP(statusRecorder, jsonReq(http.MethodGet, fmt.Sprintf("/sessions/%d/chat-status", sessionID), nil))
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("chat status code = %d", statusRecorder.Code)
	}
	status := decodeChatStatus(t, statusRecorder.Body.String())
	if !status.Processing || status.Phase != "processing" || !status.Submitted {
		t.Fatalf("unexpected processing status: %+v", status)
	}
	if status.StartedAt == nil || status.SubmittedAt == nil {
		t.Fatalf("processing timestamps missing: %+v", status)
	}

	close(runner.release)
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("request did not finish")
	}

	idleRecorder := httptest.NewRecorder()
	r.ServeHTTP(idleRecorder, jsonReq(http.MethodGet, fmt.Sprintf("/sessions/%d/chat-status", sessionID), nil))
	idle := decodeChatStatus(t, idleRecorder.Body.String())
	if idle.Processing || idle.Phase != "idle" {
		t.Fatalf("status should return idle after completion: %+v", idle)
	}
}

func TestChatStream_RemainsBusyUntilWriterFinishes(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	runner := &blockingWriterRunner{
		writerStarted: make(chan struct{}),
		writerRelease: make(chan struct{}),
	}
	r := makeTestRouter(NewSessionHandlers(runner), userID, "tester")

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我推开门"))
	}()
	select {
	case <-runner.writerStarted:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}

	statusRecorder := httptest.NewRecorder()
	r.ServeHTTP(statusRecorder, jsonReq(http.MethodGet, fmt.Sprintf("/sessions/%d/chat-status", sessionID), nil))
	status := decodeChatStatus(t, statusRecorder.Body.String())
	if !status.Processing || status.Phase != "processing" {
		t.Fatalf("writer phase should remain processing: %+v", status)
	}

	second := httptest.NewRecorder()
	r.ServeHTTP(second, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我继续前进"))
	if second.Code != http.StatusConflict {
		t.Fatalf("request during writer status = %d, want 409", second.Code)
	}

	close(runner.writerRelease)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("request did not finish after writer release")
	}
}

func TestChatStream_Success(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)

	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Return(agent.RunOutput{
		WriterDirection: "描述古籍翻开后的异变",
		KPReply:         "恐惧正在蔓延。",
	}, nil)
	runner.EXPECT().RunWriterStream(gomock.Any(), gomock.Any(), "描述古籍翻开后的异变", gomock.Any()).
		DoAndReturn(func(ctx context.Context, gctx agent.GameContext, direction string, onToken func(string)) (string, error) {
			onToken("克苏鲁")
			onToken("觉醒了")
			return "克苏鲁觉醒了", nil
		})

	h := NewSessionHandlers(runner)
	r := makeTestRouter(h, userID, "tester")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我翻开古籍"))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	events := parseSSE(w.Body.String())
	eventOrder := parseSSEEvents(w.Body.String())
	if len(events["progress"]) == 0 {
		t.Fatalf("expected progress event; body:\n%s", w.Body.String())
	}

	// Concatenate token events (Writer text).
	var gotWriter string
	for _, tok := range events["token"] {
		gotWriter += tok
	}
	if gotWriter != "克苏鲁觉醒了" {
		t.Errorf("writer tokens = %q, want %q", gotWriter, "克苏鲁觉醒了")
	}

	// Concatenate narration events (KP narration).
	var gotNarration string
	for _, tok := range events["narration"] {
		gotNarration += tok
	}
	if gotNarration != "恐惧正在蔓延。" {
		t.Errorf("narration tokens = %q, want %q", gotNarration, "恐惧正在蔓延。")
	}
	narrationIdx := firstSSEEventIndex(eventOrder, "narration")
	kpDoneIdx := firstSSEEventIndex(eventOrder, "kp_done")
	tokenIdx := firstSSEEventIndex(eventOrder, "token")
	if narrationIdx < 0 || kpDoneIdx < 0 || tokenIdx < 0 {
		t.Fatalf("expected narration, kp_done and token events; body:\n%s", w.Body.String())
	}
	if !(narrationIdx < kpDoneIdx && kpDoneIdx < tokenIdx) {
		t.Fatalf("SSE order want narration -> kp_done -> token, got narration=%d kp_done=%d token=%d", narrationIdx, kpDoneIdx, tokenIdx)
	}

	if len(events["done"]) == 0 {
		t.Errorf("expected SSE done event; body:\n%s", w.Body.String())
	}

	var msg models.Message
	err := models.DB.Where("session_id = ? AND role = ?", sessionID, models.MessageRoleAssistant).
		Last(&msg).Error
	if err != nil {
		t.Fatalf("no assistant message persisted: %v", err)
	}
	// DB保存为writer+KP拼接格式,便于历史上下文读取KP段。
	wantContent := "克苏鲁觉醒了\n\nKP:恐惧正在蔓延。"
	if msg.Content != wantContent {
		t.Errorf("persisted content = %q, want %q", msg.Content, wantContent)
	}
}

func TestChatStream_ForwardsImageSSEAndPersistsImageRef(t *testing.T) {
	initTestDB(t)
	restore := imagestore.SetDefaultDir(t.TempDir())
	t.Cleanup(restore)
	sessionID, userID := seedPlayingSession(t)

	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Return(agent.RunOutput{
		KPReply:      "你看见灯塔门缝中透出冷光。",
		ImagePrompts: []agent.ImagePromptRequest{{Prompt: "A foggy lighthouse"}},
	}, nil)
	runner.EXPECT().RunPainter(gomock.Any(), gomock.Any(), agent.ImagePromptRequest{Prompt: "A foggy lighthouse"}).Return("data:image/png;base64,YWJj", nil)

	h := NewSessionHandlers(runner)
	r := makeTestRouter(h, userID, "tester")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我走近灯塔"))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	events := parseSSE(w.Body.String())
	if len(events["image"]) != 1 {
		t.Fatalf("image event count mismatch: %d", len(events["image"]))
	}
	if events["image"][0] != "data:image/png;base64,YWJj" {
		t.Fatalf("image event should match painter result")
	}

	var msg models.Message
	if err := models.DB.Where("session_id = ? AND role = ?", sessionID, models.MessageRoleAssistant).Last(&msg).Error; err != nil {
		t.Fatalf("no assistant message persisted: %v", err)
	}
	if strings.Contains(msg.Content, "data:image") || strings.Contains(msg.Content, imageDataURLStartTag) {
		t.Fatalf("persisted content should not contain raw image data: %q", msg.Content)
	}
	refs := extractImageRefs(msg.Content)
	if len(refs) != 1 || refs[0].MIME != "image/png" {
		t.Fatalf("persisted content missing image ref: %q", msg.Content)
	}
	if _, err := imagestore.DefaultStore().Resolve(refs[0].Hash); err != nil {
		t.Fatalf("stored image not found: %v", err)
	}
}

func TestChatStream_ImageSSEAfterKPDone(t *testing.T) {
	initTestDB(t)
	restore := imagestore.SetDefaultDir(t.TempDir())
	t.Cleanup(restore)
	sessionID, userID := seedPlayingSession(t)

	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Return(agent.RunOutput{
		KPReply:      "灯塔门缝中透出冷光。",
		ImagePrompts: []agent.ImagePromptRequest{{Prompt: "A foggy lighthouse"}},
	}, nil)
	runner.EXPECT().RunPainter(gomock.Any(), gomock.Any(), agent.ImagePromptRequest{Prompt: "A foggy lighthouse"}).Return("data:image/png;base64,YWJj", nil)

	h := NewSessionHandlers(runner)
	r := makeTestRouter(h, userID, "tester")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我走近灯塔"))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	order := parseSSEEvents(w.Body.String())
	kpDoneIdx := firstSSEEventIndex(order, "kp_done")
	imageIdx := firstSSEEventIndex(order, "image")
	if kpDoneIdx < 0 || imageIdx < 0 {
		t.Fatalf("expected kp_done and image events")
	}
	if !(kpDoneIdx < imageIdx) {
		t.Fatalf("image should be streamed after kp_done, got kp_done=%d image=%d", kpDoneIdx, imageIdx)
	}

	var msg models.Message
	if err := models.DB.Where("session_id = ? AND role = ?", sessionID, models.MessageRoleAssistant).Last(&msg).Error; err != nil {
		t.Fatalf("no assistant message persisted: %v", err)
	}
	if strings.Contains(msg.Content, "data:image") || strings.Contains(msg.Content, imageDataURLStartTag) {
		t.Fatalf("persisted content should not contain raw image data: %q", msg.Content)
	}
	if refs := extractImageRefs(msg.Content); len(refs) != 1 {
		t.Fatalf("persisted content missing image ref: %q", msg.Content)
	}
}

func TestChatStream_StripsImageDataFromAgentHistory(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	dataURL := "data:image/png;base64,OLD"
	if err := models.DB.Create(&models.Message{
		SessionID: sessionID,
		Role:      models.MessageRoleAssistant,
		Content:   "旧场景描述\n" + imageDataURLStartTag + dataURL + imageDataURLEndTag,
		Username:  "KP",
	}).Error; err != nil {
		t.Fatalf("seed assistant message: %v", err)
	}

	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	var capturedHistory []models.Message
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
			capturedHistory = append([]models.Message(nil), gctx.History...)
			return agent.RunOutput{WriterText: "ok"}, nil
		})

	h := NewSessionHandlers(runner)
	r := makeTestRouter(h, userID, "tester")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "继续调查"))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if len(capturedHistory) == 0 {
		t.Fatal("expected runner to receive history")
	}
	for _, msg := range capturedHistory {
		if strings.Contains(msg.Content, imageDataURLStartTag) || strings.Contains(msg.Content, imageDataURLEndTag) || strings.Contains(msg.Content, "data:image") {
			t.Fatalf("agent history should be stripped")
		}
	}

	var stored models.Message
	if err := models.DB.Where("session_id = ? AND role = ?", sessionID, models.MessageRoleAssistant).First(&stored).Error; err != nil {
		t.Fatalf("reload stored assistant message: %v", err)
	}
	if !strings.Contains(stored.Content, imageDataURLStartTag+dataURL+imageDataURLEndTag) {
		t.Fatalf("DB content should keep image data tag")
	}
}

func TestSaveChatMessagesMarksWriterPending(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)

	msg, err := saveChatMessages(uint64(sessionID), userID, "Test Char", "我检查门锁", nil, agent.RunOutput{
		WriterDirection: "描述门锁上的痕迹",
		KPReply:         "你注意到锁孔边缘有新鲜划痕。",
	})
	if err != nil {
		t.Fatalf("save chat messages: %v", err)
	}
	if msg == nil {
		t.Fatal("assistant message is nil")
	}
	if !strings.Contains(msg.Content, writerPendingTag) {
		t.Fatalf("pending content missing marker: %q", msg.Content)
	}

	if err := updateAssistantMessageWriter(msg.ID, "你注意到锁孔边缘有新鲜划痕。", "门锁泛着冷光。"); err != nil {
		t.Fatalf("update writer: %v", err)
	}
	var updated models.Message
	if err := models.DB.First(&updated, msg.ID).Error; err != nil {
		t.Fatalf("reload message: %v", err)
	}
	if strings.Contains(updated.Content, writerPendingTag) {
		t.Fatalf("final content should remove pending marker: %q", updated.Content)
	}
}

func TestUpdateAssistantMessageWriterPreservesImageRef(t *testing.T) {
	initTestDB(t)
	restore := imagestore.SetDefaultDir(t.TempDir())
	t.Cleanup(restore)
	sessionID, userID := seedPlayingSession(t)

	msg, err := saveChatMessages(uint64(sessionID), userID, "Test Char", "我检查门锁", nil, agent.RunOutput{
		WriterDirection: "描述门锁上的痕迹",
		KPReply:         "你注意到锁孔边缘有新鲜划痕。",
	})
	if err != nil {
		t.Fatalf("save chat messages: %v", err)
	}
	if msg == nil {
		t.Fatal("assistant message is nil")
	}

	dataURL := "data:image/png;base64,YWJj"
	if err := appendAssistantMessageImage(msg.ID, dataURL); err != nil {
		t.Fatalf("append image data url: %v", err)
	}
	if err := updateAssistantMessageWriter(msg.ID, "你注意到锁孔边缘有新鲜划痕。", "门锁泛着冷光。"); err != nil {
		t.Fatalf("update writer: %v", err)
	}

	var updated models.Message
	if err := models.DB.First(&updated, msg.ID).Error; err != nil {
		t.Fatalf("reload message: %v", err)
	}
	if strings.Contains(updated.Content, writerPendingTag) {
		t.Fatalf("final content should remove pending marker")
	}
	if strings.Contains(updated.Content, dataURL) || strings.Contains(updated.Content, imageDataURLStartTag) {
		t.Fatalf("final content should not preserve raw image data: %q", updated.Content)
	}
	if refs := extractImageRefs(updated.Content); len(refs) != 1 || refs[0].MIME != "image/png" {
		t.Fatalf("final content should contain one image ref: %q", updated.Content)
	}
}

func TestStartWriterJobClearsPendingWhenWriterReturnsEmpty(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)

	output := agent.RunOutput{
		WriterDirection: "描述门锁上的痕迹",
		KPReply:         "你注意到锁孔边缘有新鲜划痕。",
	}
	msg, err := saveChatMessages(uint64(sessionID), userID, "Test Char", "我检查门锁", nil, output)
	if err != nil {
		t.Fatalf("save chat messages: %v", err)
	}
	if msg == nil || !strings.Contains(msg.Content, writerPendingTag) {
		t.Fatalf("pending marker not saved: %#v", msg)
	}

	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().RunWriterStream(gomock.Any(), gomock.Any(), output.WriterDirection, gomock.Any()).
		Return("", nil)
	h := NewSessionHandlers(runner)
	clientDone := make(chan struct{})
	defer close(clientDone)
	ch := h.startWriterJob(msg.ID, agent.GameContext{}, output, clientDone)
	if ch == nil {
		t.Fatal("writer job channel is nil")
	}
	for range ch {
	}

	var updated models.Message
	if err := models.DB.First(&updated, msg.ID).Error; err != nil {
		t.Fatalf("reload message: %v", err)
	}
	if strings.Contains(updated.Content, writerPendingTag) {
		t.Fatalf("empty writer result should clear pending marker: %q", updated.Content)
	}
	if updated.Content != "KP:你注意到锁孔边缘有新鲜划痕。" {
		t.Fatalf("content = %q", updated.Content)
	}
}

func TestChatStream_UserMessagePersisted(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)

	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Return(agent.RunOutput{WriterText: "ok"}, nil)

	h := NewSessionHandlers(runner)
	r := makeTestRouter(h, userID, "tester")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "玩家输入"))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var msg models.Message
	err := models.DB.Where("session_id = ? AND role = ? AND username = ?",
		sessionID, models.MessageRoleUser, "Test Char").First(&msg).Error
	if err != nil {
		t.Fatalf("user message not persisted: %v", err)
	}
	if msg.Content != "玩家输入" {
		t.Errorf("user message content = %q, want %q", msg.Content, "玩家输入")
	}
}

// seedTwoPlayerSession 创建含两名活跃玩家的游戏房间，返回 sessionID、两个 userID 及角色名。
func seedTwoPlayerSession(t *testing.T, charName1, charName2 string) (sessionID, user1ID, user2ID uint) {
	t.Helper()
	scenarioID := seedScenario(t, "multi-scenario")

	creator := models.User{Username: "creator_mp", Email: "creator_mp@t.com", PasswordHash: "x", CardSlots: 3}
	if err := models.DB.Create(&creator).Error; err != nil {
		t.Fatalf("create creator: %v", err)
	}
	session := models.GameSession{
		Name:       "Multi Room",
		ScenarioID: scenarioID,
		Status:     models.SessionStatusPlaying,
		MaxPlayers: 4,
		CreatedBy:  creator.ID,
		TurnRound:  1,
	}
	if err := models.DB.Create(&session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}

	// 玩家1
	u1 := models.User{Username: "mp_user1", Email: "mp1@t.com", PasswordHash: "x", CardSlots: 3}
	models.DB.Create(&u1)
	c1 := models.CharacterCard{
		UserID: u1.ID, Name: charName1, IsActive: true,
		Stats:  models.JSONField[models.CharacterStats]{Data: models.CharacterStats{CON: 50, SIZ: 50, HP: 10, MaxHP: 10}},
		Skills: models.JSONField[map[string]int]{Data: map[string]int{}},
	}
	models.DB.Create(&c1)
	models.DB.Create(&models.SessionPlayer{SessionID: session.ID, UserID: u1.ID, CharacterCardID: c1.ID, JoinedAt: time.Now()})

	// 玩家2
	u2 := models.User{Username: "mp_user2", Email: "mp2@t.com", PasswordHash: "x", CardSlots: 3}
	models.DB.Create(&u2)
	c2 := models.CharacterCard{
		UserID: u2.ID, Name: charName2, IsActive: true,
		Stats:  models.JSONField[models.CharacterStats]{Data: models.CharacterStats{CON: 50, SIZ: 50, HP: 10, MaxHP: 10}},
		Skills: models.JSONField[map[string]int]{Data: map[string]int{}},
	}
	models.DB.Create(&c2)
	models.DB.Create(&models.SessionPlayer{SessionID: session.ID, UserID: u2.ID, CharacterCardID: c2.ID, JoinedAt: time.Now()})

	return session.ID, u1.ID, u2.ID
}

// TestChatStream_WaitingPayload_Fields 验证 waiting SSE 载荷含必要字段，
// 并且已提交/待提交姓名分类准确（旧字段 pending/total 仍存在）。
func TestChatStream_WaitingPayload_Fields(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")

	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))
	// 玩家1先提交，尚未满足全员到位，应收到 waiting SSE
	r := makeTestRouter(h, user1ID, "mp_user1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我查探房间"))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	events := parseSSE(w.Body.String())
	if len(events["waiting"]) == 0 {
		t.Fatalf("expected waiting SSE event; body:\n%s", w.Body.String())
	}

	var payload struct {
		Pending        int64    `json:"pending"`
		Total          int64    `json:"total"`
		SubmittedNames []string `json:"submitted_names"`
		PendingNames   []string `json:"pending_names"`
	}
	rawData := strings.TrimSpace(events["waiting"][0])
	if err := json.Unmarshal([]byte(rawData), &payload); err != nil {
		t.Fatalf("waiting payload invalid JSON: %v\ndata: %s", err, rawData)
	}

	// 旧字段兼容
	if payload.Total != 2 {
		t.Errorf("total = %d, want 2", payload.Total)
	}
	if payload.Pending != 1 {
		t.Errorf("pending = %d, want 1", payload.Pending)
	}
	// 新字段
	if payload.SubmittedNames == nil {
		t.Error("submitted_names is nil, want non-nil slice")
	}
	if payload.PendingNames == nil {
		t.Error("pending_names is nil, want non-nil slice")
	}
	if len(payload.SubmittedNames) != 1 || payload.SubmittedNames[0] != "爱丽丝" {
		t.Errorf("submitted_names = %v, want [\"爱丽丝\"]", payload.SubmittedNames)
	}
	if len(payload.PendingNames) != 1 || payload.PendingNames[0] != "鲍勃" {
		t.Errorf("pending_names = %v, want [\"鲍勃\"]", payload.PendingNames)
	}
}

func TestGetChatStatus_MultiplayerWaitingRecoversSubmittedPlayer(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")

	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))
	user1Router := makeTestRouter(h, user1ID, "mp_user1")
	w := httptest.NewRecorder()
	user1Router.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我查探房间"))
	if w.Code != http.StatusOK {
		t.Fatalf("first submit status = %d", w.Code)
	}

	user1StatusRecorder := httptest.NewRecorder()
	user1Router.ServeHTTP(user1StatusRecorder, jsonReq(http.MethodGet, fmt.Sprintf("/sessions/%d/chat-status", sessionID), nil))
	user1Status := decodeChatStatus(t, user1StatusRecorder.Body.String())
	if user1Status.Phase != "waiting" || !user1Status.WaitingForPlayers || !user1Status.Submitted || user1Status.Processing {
		t.Fatalf("unexpected submitted player status: %+v", user1Status)
	}
	if user1Status.Waiting.Pending != 1 || user1Status.Waiting.Total != 2 {
		t.Fatalf("unexpected waiting counts: %+v", user1Status.Waiting)
	}

	user2Router := makeTestRouter(h, user2ID, "mp_user2")
	user2StatusRecorder := httptest.NewRecorder()
	user2Router.ServeHTTP(user2StatusRecorder, jsonReq(http.MethodGet, fmt.Sprintf("/sessions/%d/chat-status", sessionID), nil))
	user2Status := decodeChatStatus(t, user2StatusRecorder.Body.String())
	if user2Status.Phase != "waiting" || user2Status.Submitted || !user2Status.WaitingForPlayers {
		t.Fatalf("unexpected pending player status: %+v", user2Status)
	}
}

func TestGetChatStatus_MultiplayerProcessingAppliesToAllPlayers(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	runner := &blockingRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
		output:  agent.RunOutput{WriterText: "本轮完成"},
	}
	h := NewSessionHandlers(runner)
	user1Router := makeTestRouter(h, user1ID, "mp_user1")
	user2Router := makeTestRouter(h, user2ID, "mp_user2")

	first := httptest.NewRecorder()
	user1Router.ServeHTTP(first, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我观察门口"))
	if first.Code != http.StatusOK {
		t.Fatalf("first submit status = %d", first.Code)
	}

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		w := httptest.NewRecorder()
		user2Router.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我检查窗户"))
	}()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("multiplayer request did not enter runner")
	}

	for _, tc := range []struct {
		name   string
		router *gin.Engine
	}{
		{name: "first player", router: user1Router},
		{name: "second player", router: user2Router},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statusRecorder := httptest.NewRecorder()
			tc.router.ServeHTTP(statusRecorder, jsonReq(http.MethodGet, fmt.Sprintf("/sessions/%d/chat-status", sessionID), nil))
			status := decodeChatStatus(t, statusRecorder.Body.String())
			if !status.Processing || status.Phase != "processing" || status.WaitingForPlayers {
				t.Fatalf("unexpected multiplayer processing status: %+v", status)
			}
		})
	}

	close(runner.release)
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("multiplayer request did not finish")
	}
}

// TestChatStream_WaitingPayload_SpecialCharsJSON 验证角色名含特殊字符时 JSON 仍合法。
func TestChatStream_WaitingPayload_SpecialCharsJSON(t *testing.T) {
	initTestDB(t)
	specialName := `角色"特殊\名称<>&`
	sessionID, user1ID, _ := seedTwoPlayerSession(t, specialName, "普通角色")

	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))
	r := makeTestRouter(h, user1ID, "mp_user1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "行动"))

	events := parseSSE(w.Body.String())
	if len(events["waiting"]) == 0 {
		t.Fatalf("expected waiting SSE event")
	}

	rawData := strings.TrimSpace(events["waiting"][0])
	var payload map[string]any
	if err := json.Unmarshal([]byte(rawData), &payload); err != nil {
		t.Fatalf("waiting payload invalid JSON: %v\ndata: %s", err, rawData)
	}

	submittedNames, _ := payload["submitted_names"].([]any)
	if len(submittedNames) != 1 {
		t.Fatalf("submitted_names len = %d, want 1", len(submittedNames))
	}
	if submittedNames[0] != specialName {
		t.Errorf("submitted_names[0] = %v, want %q", submittedNames[0], specialName)
	}
}

// TestChatStream_WaitingPayload_UsernameFallback 验证角色名为空时回退使用用户名。
func TestChatStream_WaitingPayload_UsernameFallback(t *testing.T) {
	initTestDB(t)
	// 玩家1 的角色名为空，期望回退为用户名 "mp_user1"
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "", "有名角色")

	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))
	r := makeTestRouter(h, user1ID, "mp_user1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "行动"))

	events := parseSSE(w.Body.String())
	if len(events["waiting"]) == 0 {
		t.Fatalf("expected waiting SSE event")
	}

	var payload struct {
		SubmittedNames []string `json:"submitted_names"`
	}
	rawData := strings.TrimSpace(events["waiting"][0])
	if err := json.Unmarshal([]byte(rawData), &payload); err != nil {
		t.Fatalf("waiting payload invalid JSON: %v", err)
	}

	// 角色名为空，期望显示用户名
	if len(payload.SubmittedNames) != 1 || payload.SubmittedNames[0] != "mp_user1" {
		t.Errorf("submitted_names = %v, want [\"mp_user1\"]", payload.SubmittedNames)
	}
}
