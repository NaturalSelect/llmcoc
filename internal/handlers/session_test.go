package handlers

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/handlers/mocks"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"go.uber.org/mock/gomock"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func initTestDB(t *testing.T) {
	t.Helper()
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
	r.POST("/sessions/:id/chat", h.ChatStream)
	return r
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

func TestChatStream_ForwardsImageSSEAndPersistsDataURL(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)

	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Return(agent.RunOutput{
		KPReply:      "你看见灯塔门缝中透出冷光。",
		ImagePrompts: []agent.ImagePromptRequest{{Prompt: "A foggy lighthouse", Characters: []string{"约翰"}}},
	}, nil)
	runner.EXPECT().RunPainter(gomock.Any(), gomock.Any(), agent.ImagePromptRequest{Prompt: "A foggy lighthouse", Characters: []string{"约翰"}}).Return("data:image/png;base64,YWJj", nil)

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
	wantImageTag := imageDataURLStartTag + "data:image/png;base64,YWJj" + imageDataURLEndTag
	if !strings.Contains(msg.Content, wantImageTag) {
		t.Fatalf("persisted content missing image data tag")
	}
}

func TestChatStream_ImageSSEAfterKPDone(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)

	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Return(agent.RunOutput{
		KPReply:      "灯塔门缝中透出冷光。",
		ImagePrompts: []agent.ImagePromptRequest{{Prompt: "A foggy lighthouse", Characters: []string{"约翰"}}},
	}, nil)
	runner.EXPECT().RunPainter(gomock.Any(), gomock.Any(), agent.ImagePromptRequest{Prompt: "A foggy lighthouse", Characters: []string{"约翰"}}).Return("data:image/png;base64,YWJj", nil)

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
	wantImageTag := imageDataURLStartTag + "data:image/png;base64,YWJj" + imageDataURLEndTag
	if !strings.Contains(msg.Content, wantImageTag) {
		t.Fatalf("persisted content missing image data tag")
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

func TestUpdateAssistantMessageWriterPreservesImageDataURL(t *testing.T) {
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

	dataURL := "data:image/png;base64,YWJj"
	if err := appendAssistantMessageImageDataURL(msg.ID, dataURL); err != nil {
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
	if !strings.Contains(updated.Content, imageDataURLStartTag+dataURL+imageDataURLEndTag) {
		t.Fatalf("final content should preserve image data tag")
	}
	if strings.Count(updated.Content, imageDataURLStartTag) != 1 {
		t.Fatalf("final content should contain one image data tag")
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
