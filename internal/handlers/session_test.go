package handlers

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	// Each test gets its own named in-memory DB so concurrent/sequential tests
	// don't share state via the SQLite shared-cache.
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.CharacterCard{},
		&models.Scenario{},
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
	); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() { models.DB = prev })
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

// parseSSE returns a map[eventType][]data parsed from an SSE response body.
// Gin's SSE encoder writes "event:name" and "data:value" (no space after colon).
func parseSSE(body string) map[string][]string {
	events := map[string][]string{}
	scanner := bufio.NewScanner(strings.NewReader(body))
	var curEvent string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			curEvent = strings.TrimPrefix(line, "event:")
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimPrefix(line, "data:")
			events[curEvent] = append(events[curEvent], data)
		}
	}
	return events
}

// formPost builds an application/x-www-form-urlencoded POST request.
func formPost(path, content string) *http.Request {
	body := strings.NewReader("content=" + content)
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
		WriterText: "克苏鲁觉醒了",
		KPReply:    "恐惧正在蔓延。",
	}, nil)

	h := NewSessionHandlers(runner)
	r := makeTestRouter(h, userID, "tester")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我翻开古籍"))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	events := parseSSE(w.Body.String())

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

	if len(events["done"]) == 0 {
		t.Errorf("expected SSE done event; body:\n%s", w.Body.String())
	}

	var msg models.Message
	err := models.DB.Where("session_id = ? AND role = ?", sessionID, models.MessageRoleAssistant).
		Last(&msg).Error
	if err != nil {
		t.Fatalf("no assistant message persisted: %v", err)
	}
	// DB stores writer + narration combined.
	wantContent := "克苏鲁觉醒了\n\n恐惧正在蔓延。"
	if msg.Content != wantContent {
		t.Errorf("persisted content = %q, want %q", msg.Content, wantContent)
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
