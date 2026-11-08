package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
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
		&models.SessionFavorite{},
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
		&models.Announcement{},
		&models.LLMLatencyStat{},
		&models.AgentTranscript{},
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
// session player, and returns their IDs. Spotlight 留空(NULL)，等同 KP 持有发言权。
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

// seedTwoPlayerSession 创建含两名活跃玩家的游戏房间，返回 sessionID、两个 userID 及角色名。
// 房主是另一个不在房间里的账号 creator_mp。Spotlight 留空(NULL)。
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

	u1 := models.User{Username: "mp_user1", Email: "mp1@t.com", PasswordHash: "x", CardSlots: 3}
	models.DB.Create(&u1)
	c1 := models.CharacterCard{
		UserID: u1.ID, Name: charName1, IsActive: true,
		Stats:  models.JSONField[models.CharacterStats]{Data: models.CharacterStats{CON: 50, SIZ: 50, HP: 10, MaxHP: 10}},
		Skills: models.JSONField[map[string]int]{Data: map[string]int{}},
	}
	models.DB.Create(&c1)
	models.DB.Create(&models.SessionPlayer{SessionID: session.ID, UserID: u1.ID, CharacterCardID: c1.ID, JoinedAt: time.Now()})

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

// roomRouter 构造带房间命令接口的路由，并注入登录身份。
func roomRouter(h *SessionHandlers, userID uint, username, role string) *gin.Engine {
	r := gin.New()
	r.Use(withAuth(userID, username, role))
	r.GET("/sessions/:id/chat-status", h.GetChatStatus)
	r.GET("/sessions/:id/messages", GetMessages)
	r.POST("/sessions/:id/chat", h.ChatStream)
	r.POST("/sessions/:id/kp-turn", h.KPTurn)
	r.POST("/sessions/:id/kp-stop", h.KPStop)
	return r
}

func makeTestRouter(h *SessionHandlers, userID uint, username string) *gin.Engine {
	return roomRouter(h, userID, username, "user")
}

func decodeChatStatus(t *testing.T, body string) chatStatusResponse {
	t.Helper()
	var status chatStatusResponse
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("decode chat status: %v\nbody: %s", err, body)
	}
	return status
}

// formPost builds an application/x-www-form-urlencoded POST request.
func formPost(path, content string) *http.Request {
	body := strings.NewReader("content=" + url.QueryEscape(content))
	req, _ := http.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func postChat(r *gin.Engine, sessionID uint, content string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), content))
	return w
}

func postKPTurn(r *gin.Engine, sessionID uint) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, fmt.Sprintf("/sessions/%d/kp-turn", sessionID), nil))
	return w
}

func postKPStop(r *gin.Engine, sessionID uint) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, fmt.Sprintf("/sessions/%d/kp-stop", sessionID), nil))
	return w
}

func getChatStatus(t *testing.T, r *gin.Engine, sessionID uint) chatStatusResponse {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodGet, fmt.Sprintf("/sessions/%d/chat-status", sessionID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("chat-status code = %d: %s", w.Code, w.Body.String())
	}
	return decodeChatStatus(t, w.Body.String())
}

type chatAccepted struct {
	FloorStarted bool           `json:"floor_started"`
	Waiting      waitingPayload `json:"waiting"`
}

func decodeChatAccepted(t *testing.T, w *httptest.ResponseRecorder) chatAccepted {
	t.Helper()
	if w.Code != http.StatusAccepted {
		t.Fatalf("chat status = %d, want 202: %s", w.Code, w.Body.String())
	}
	var body chatAccepted
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode chat response: %v\nbody: %s", err, w.Body.String())
	}
	return body
}

func setSpotlight(t *testing.T, sessionID uint, sp *models.SpotlightState) {
	t.Helper()
	if err := models.DB.Model(&models.GameSession{}).Where("id = ?", sessionID).
		Update("spotlight", models.JSONField[*models.SpotlightState]{Data: sp}).Error; err != nil {
		t.Fatalf("set spotlight: %v", err)
	}
}

func loadSpotlight(t *testing.T, sessionID uint) *models.SpotlightState {
	t.Helper()
	var s models.GameSession
	if err := models.DB.First(&s, sessionID).Error; err != nil {
		t.Fatalf("load session: %v", err)
	}
	return s.Spotlight.Data
}

func sessionCreator(t *testing.T, sessionID uint) uint {
	t.Helper()
	var s models.GameSession
	if err := models.DB.First(&s, sessionID).Error; err != nil {
		t.Fatalf("load session: %v", err)
	}
	return s.CreatedBy
}

func loadMessages(t *testing.T, sessionID uint) []models.Message {
	t.Helper()
	var msgs []models.Message
	if err := models.DB.Where("session_id = ?", sessionID).Order("id ASC").Find(&msgs).Error; err != nil {
		t.Fatalf("load messages: %v", err)
	}
	return msgs
}

func messageKinds(msgs []models.Message) []models.MessageKind {
	kinds := make([]models.MessageKind, len(msgs))
	for i, m := range msgs {
		kinds[i] = m.Kind
	}
	return kinds
}

func countMessages(sessionID uint, role models.MessageRole) int64 {
	var n int64
	models.DB.Model(&models.Message{}).Where("session_id = ? AND role = ?", sessionID, role).Count(&n)
	return n
}

// askAndStop 是一个最小的 Run 替身：不发布任何消息，直接点名 userIDs。
func askRun(question string, userIDs []uint, names []string) func(context.Context, agent.GameContext) (agent.RunOutput, error) {
	return func(context.Context, agent.GameContext) (agent.RunOutput, error) {
		return agent.RunOutput{Ask: &agent.AskResult{UserIDs: userIDs, Names: names, Question: question}}, nil
	}
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

// ── ChatStream: rejections ───────────────────────────────────────────────────

func TestChatStream_SessionNotFound(t *testing.T) {
	initTestDB(t)
	r := makeTestRouter(NewSessionHandlers(&fakeRunner{}), 1, "tester")
	if w := postChat(r, 9999, "hello"); w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestChatStream_NotPlaying(t *testing.T) {
	initTestDB(t)
	scenarioID := seedScenario(t, "s")
	user := models.User{Username: "u", Email: "u@u.com", PasswordHash: "x", CardSlots: 3}
	models.DB.Create(&user)
	session := models.GameSession{Name: "r", ScenarioID: scenarioID, Status: models.SessionStatusLobby, MaxPlayers: 4, CreatedBy: user.ID}
	models.DB.Create(&session)

	r := makeTestRouter(NewSessionHandlers(&fakeRunner{}), user.ID, "u")
	if w := postChat(r, session.ID, "hello"); w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestChatStream_SpectatorForbidden(t *testing.T) {
	initTestDB(t)
	sessionID, _ := seedPlayingSession(t)
	r := makeTestRouter(NewSessionHandlers(&fakeRunner{}), 999, "stranger")
	if w := postChat(r, sessionID, "hello"); w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestChatStream_EmptyAndOversizedContent(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	r := makeTestRouter(NewSessionHandlers(&fakeRunner{}), userID, "tester")
	if w := postChat(r, sessionID, ""); w.Code != http.StatusBadRequest {
		t.Errorf("empty content: want 400, got %d", w.Code)
	}
	if w := postChat(r, sessionID, strings.Repeat("a", 2001)); w.Code != http.StatusBadRequest {
		t.Errorf("oversized content: want 400, got %d", w.Code)
	}
}

func TestChatStream_KPHoldsFloorRejectsWith409(t *testing.T) {
	for name, sp := range map[string]*models.SpotlightState{
		"NULL 视为 KP 持有": nil,
		"空 Spotlight":    {},
	} {
		t.Run(name, func(t *testing.T) {
			initTestDB(t)
			sessionID, userID := seedPlayingSession(t)
			if sp != nil {
				setSpotlight(t, sessionID, sp)
			}
			runner := &fakeRunner{}
			w := postChat(makeTestRouter(NewSessionHandlers(runner), userID, "tester"), sessionID, "我先说")
			if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "KP 正在推进剧情") {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if len(runner.runCalls()) != 0 {
				t.Error("rejected input must not start a floor")
			}
			var actions int64
			models.DB.Model(&models.SessionTurnAction{}).Where("session_id = ?", sessionID).Count(&actions)
			if actions != 0 {
				t.Errorf("rejected input must not create a turn action, got %d", actions)
			}
		})
	}
}

func TestChatStream_UnnamedPlayerRejectedWith403(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{user2ID}, Names: []string{"鲍勃"}, Question: "你打算怎么开门？"})

	runner := &fakeRunner{}
	w := postChat(makeTestRouter(NewSessionHandlers(runner), user1ID, "mp_user1"), sessionID, "我抢着说话")
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "KP 正在等待 鲍勃 回应") || !strings.Contains(w.Body.String(), "你打算怎么开门？") {
		t.Errorf("reject message = %s", w.Body.String())
	}
	if len(runner.runCalls()) != 0 || countMessages(sessionID, models.MessageRoleUser) != 0 {
		t.Error("rejected input must not run the KP or persist messages")
	}
}

func TestChatStream_PausedKPAsksForHostContinue(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	setSpotlight(t, sessionID, &models.SpotlightState{Stop: "host_stop"})

	w := postChat(makeTestRouter(NewSessionHandlers(&fakeRunner{}), userID, "tester"), sessionID, "我想说话")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "KP 已暂停") {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
}

// ── ChatStream: starting a floor ─────────────────────────────────────────────

func TestChatStream_NamedPlayerStartsFloorAndPassesContext(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{userID}, Names: []string{"Test Char"}, Question: "你听见了什么？"})

	runner := &fakeRunner{run: askRun("接下来呢？", []uint{userID}, []string{"Test Char"})}
	tap := tapRoom(t, sessionID)

	w := postChat(makeTestRouter(NewSessionHandlers(runner), userID, "tester"), sessionID, "我贴着门听")
	if body := decodeChatAccepted(t, w); !body.FloorStarted {
		t.Fatalf("floor_started = false, body: %s", w.Body.String())
	}
	tap.waitCount("floor_end", 1)

	runs := runner.runCalls()
	if len(runs) != 1 {
		t.Fatalf("Run called %d times, want 1", len(runs))
	}
	got := runs[0]
	if got.KPFloor != "" || got.UserInput != "我贴着门听" || got.UserName != "Test Char" || got.AskedQuestion != "你听见了什么？" {
		t.Errorf("player floor context = floor %q input %q name %q asked %q", got.KPFloor, got.UserInput, got.UserName, got.AskedQuestion)
	}
	if len(got.PendingActions) != 1 || got.PendingActions[0].Content != "我贴着门听" {
		t.Errorf("PendingActions = %+v", got.PendingActions)
	}
	if got.Publish == nil || got.Progress == nil {
		t.Error("handler must inject Publish and Progress")
	}

	msgs := loadMessages(t, sessionID)
	if len(msgs) != 1 || msgs[0].Kind != models.MessageKindPlayer || msgs[0].Status != models.MessageStatusDone ||
		msgs[0].Floor != 1 || msgs[0].Content != "我贴着门听" || msgs[0].UserID == nil || *msgs[0].UserID != userID {
		t.Fatalf("player message = %+v", msgs)
	}

	sp := loadSpotlight(t, sessionID)
	if sp == nil || len(sp.UserIDs) != 1 || sp.UserIDs[0] != userID || sp.Question != "接下来呢？" || sp.Stop != "" {
		t.Fatalf("spotlight after ask = %+v", sp)
	}
	want := []string{"message_new", "spotlight", "floor_start", "spotlight", "floor_end"}
	if got := strings.Join(tap.persistentTypes(), ","); got != strings.Join(want, ",") {
		t.Errorf("persistent events = %s, want %s", got, strings.Join(want, ","))
	}
	if reason := eventField(tap.ofType("floor_end")[0], "reason"); reason != "ask" {
		t.Errorf("floor_end reason = %v, want ask", reason)
	}
	if _, running := getSessionProcessing(sessionID); running {
		t.Error("processing must be released after the floor ends")
	}
}

func TestChatStream_MultiPlayerWaitsForEveryNamedPlayer(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{
		UserIDs: []uint{user1ID, user2ID}, Names: []string{"爱丽丝", "鲍勃"}, Question: "你们怎么办？",
	})
	runner := &fakeRunner{run: askRun("好的", []uint{user1ID}, []string{"爱丽丝"})}
	h := NewSessionHandlers(runner)
	tap := tapRoom(t, sessionID)

	first := decodeChatAccepted(t, postChat(makeTestRouter(h, user1ID, "mp_user1"), sessionID, "我查探房间"))
	if first.FloorStarted {
		t.Fatal("first submitter must not start the floor")
	}
	if first.Waiting.Pending != 1 || first.Waiting.Total != 2 ||
		len(first.Waiting.SubmittedNames) != 1 || first.Waiting.SubmittedNames[0] != "爱丽丝" ||
		len(first.Waiting.PendingNames) != 1 || first.Waiting.PendingNames[0] != "鲍勃" {
		t.Errorf("waiting payload = %+v", first.Waiting)
	}
	tap.waitCount("waiting", 1)
	if len(runner.runCalls()) != 0 || countMessages(sessionID, models.MessageRoleUser) != 0 {
		t.Fatal("nothing may run or be persisted before everyone named has submitted")
	}

	status := getChatStatus(t, makeTestRouter(h, user1ID, "mp_user1"), sessionID)
	if status.Phase != "waiting" || !status.WaitingForPlayers || !status.Submitted || status.KPRunning {
		t.Errorf("submitted player status = %+v", status)
	}
	other := getChatStatus(t, makeTestRouter(h, user2ID, "mp_user2"), sessionID)
	if other.Phase != "awaiting_me" || other.Submitted {
		t.Errorf("pending player status = %+v", other)
	}

	second := decodeChatAccepted(t, postChat(makeTestRouter(h, user2ID, "mp_user2"), sessionID, "我检查窗户"))
	if !second.FloorStarted {
		t.Fatal("last submitter must start the floor")
	}
	tap.waitCount("floor_end", 1)

	got := runner.runCalls()[0]
	if len(got.PendingActions) != 2 || got.PendingActions[0].PlayerName != "爱丽丝" || got.PendingActions[1].PlayerName != "鲍勃" {
		t.Errorf("PendingActions = %+v, want both players in room order", got.PendingActions)
	}
	if n := countMessages(sessionID, models.MessageRoleUser); n != 2 {
		t.Errorf("player messages = %d, want 2", n)
	}
}

func TestChatStream_ResubmitReplacesDeclaration(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{user1ID, user2ID}, Names: []string{"爱丽丝", "鲍勃"}})
	h := NewSessionHandlers(&fakeRunner{})
	r := makeTestRouter(h, user1ID, "mp_user1")

	postChat(r, sessionID, "第一版")
	postChat(r, sessionID, "第二版")

	var actions []models.SessionTurnAction
	models.DB.Where("session_id = ? AND user_id = ?", sessionID, user1ID).Find(&actions)
	if len(actions) != 1 || actions[0].ActionSummary != "第二版" {
		t.Fatalf("turn actions = %+v, want a single latest declaration", actions)
	}
}

func TestChatStream_WaitingPayloadSpecialCharsAndUsernameFallback(t *testing.T) {
	initTestDB(t)
	specialName := `角色"特殊\名称<>&`
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, specialName, "")
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{user1ID, user2ID}, Names: []string{"a", "b"}})
	h := NewSessionHandlers(&fakeRunner{})

	w := postChat(makeTestRouter(h, user1ID, "mp_user1"), sessionID, "我查探房间")
	body := decodeChatAccepted(t, w)
	if len(body.Waiting.SubmittedNames) != 1 || body.Waiting.SubmittedNames[0] != specialName {
		t.Errorf("submitted_names = %v, want special name round-tripped", body.Waiting.SubmittedNames)
	}
	if len(body.Waiting.PendingNames) != 1 || body.Waiting.PendingNames[0] != "mp_user2" {
		t.Errorf("pending_names = %v, want username fallback", body.Waiting.PendingNames)
	}
}

func TestChatStream_RejectsSecondRequestWhileFloorRunning(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{userID}, Names: []string{"Test Char"}})

	started, release := make(chan struct{}), make(chan struct{})
	runner := &fakeRunner{run: func(context.Context, agent.GameContext) (agent.RunOutput, error) {
		close(started)
		<-release
		return agent.RunOutput{}, nil
	}}
	r := makeTestRouter(NewSessionHandlers(runner), userID, "tester")
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postChat(r, sessionID, "第一条行动"))
	<-started

	if w := postChat(r, sessionID, "重复行动"); w.Code != http.StatusConflict {
		t.Fatalf("second request status = %d, want 409: %s", w.Code, w.Body.String())
	}
	status := getChatStatus(t, r, sessionID)
	if !status.KPRunning || status.Phase != "kp_running" || status.Floor == nil || status.Floor.Floor != 1 {
		t.Errorf("status while running = %+v", status)
	}

	close(release)
	tap.waitCount("floor_end", 1)
	if n := countMessages(sessionID, models.MessageRoleUser); n != 1 {
		t.Fatalf("user message count = %d, want 1", n)
	}
}

func TestChatStream_AgentErrorLeavesKPHoldingWithStop(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{userID}, Names: []string{"Test Char"}})

	runner := &fakeRunner{run: func(context.Context, agent.GameContext) (agent.RunOutput, error) {
		return agent.RunOutput{}, errors.New("LLM timeout")
	}}
	r := makeTestRouter(NewSessionHandlers(runner), userID, "tester")
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postChat(r, sessionID, "我去探索地下室"))
	tap.waitCount("floor_end", 1)

	if reason := eventField(tap.ofType("floor_end")[0], "reason"); reason != "error" {
		t.Errorf("floor_end reason = %v, want error", reason)
	}
	sp := loadSpotlight(t, sessionID)
	if sp == nil || len(sp.UserIDs) != 0 || sp.Stop != "error" {
		t.Fatalf("spotlight = %+v, want KP holding with stop=error", sp)
	}
	status := getChatStatus(t, r, sessionID)
	if status.Phase != "kp_floor" || status.Spotlight.Stop != "error" || !status.CanKPContinue {
		t.Errorf("status = %+v", status)
	}
	if w := postChat(r, sessionID, "再试一次"); w.Code != http.StatusConflict {
		t.Errorf("chat while paused = %d, want 409", w.Code)
	}
}

func TestChatStream_StripsImageDataFromAgentHistory(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{userID}, Names: []string{"Test Char"}})
	dataURL := "data:image/png;base64,OLD"
	if err := models.DB.Create(&models.Message{
		SessionID: sessionID,
		Role:      models.MessageRoleAssistant,
		Kind:      models.MessageKindKP,
		Content:   "旧场景描述\n" + imageDataURLStartTag + dataURL + imageDataURLEndTag,
		Username:  "KP",
	}).Error; err != nil {
		t.Fatalf("seed assistant message: %v", err)
	}

	runner := &fakeRunner{run: askRun("q", []uint{userID}, []string{"Test Char"})}
	tap := tapRoom(t, sessionID)
	decodeChatAccepted(t, postChat(makeTestRouter(NewSessionHandlers(runner), userID, "tester"), sessionID, "继续调查"))
	tap.waitCount("floor_end", 1)

	history := runner.runCalls()[0].History
	if len(history) == 0 {
		t.Fatal("expected runner to receive history")
	}
	for _, msg := range history {
		if strings.Contains(msg.Content, imageDataURLStartTag) || strings.Contains(msg.Content, "data:image") {
			t.Fatalf("agent history should be stripped: %q", msg.Content)
		}
	}
	var stored models.Message
	if err := models.DB.Where("session_id = ? AND role = ?", sessionID, models.MessageRoleAssistant).First(&stored).Error; err != nil {
		t.Fatalf("reload stored assistant message: %v", err)
	}
	if !strings.Contains(stored.Content, imageDataURLStartTag+dataURL+imageDataURLEndTag) {
		t.Fatal("DB content should keep the image data tag")
	}
}

// ── GetChatStatus ────────────────────────────────────────────────────────────

func TestGetChatStatus_Phases(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	creatorID := sessionCreator(t, sessionID)
	h := NewSessionHandlers(&fakeRunner{})
	status := func(userID uint, name string) chatStatusResponse {
		return getChatStatus(t, makeTestRouter(h, userID, name), sessionID)
	}

	// NULL Spotlight 与空 Spotlight 一样：KP 持有，普通玩家没有继续按钮。
	nullRoom := status(user1ID, "mp_user1")
	if nullRoom.Phase != "kp_floor" || !nullRoom.Spotlight.KPFloor || nullRoom.CanKPContinue || nullRoom.CanKPStop {
		t.Errorf("NULL spotlight: %+v", nullRoom)
	}
	if host := status(creatorID, "creator_mp"); !host.CanKPContinue || host.CanKPStop {
		t.Errorf("host while KP holds: %+v", host)
	}

	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{user2ID}, Names: []string{"鲍勃"}, Question: "你怎么办？"})
	asked := status(user2ID, "mp_user2")
	if asked.Phase != "awaiting_me" || !asked.Spotlight.AskedMe || asked.Spotlight.Question != "你怎么办？" || asked.Spotlight.KPFloor {
		t.Errorf("named player: %+v", asked)
	}
	other := status(user1ID, "mp_user1")
	if other.Phase != "waiting" || other.Spotlight.AskedMe || len(other.Spotlight.Names) != 1 || other.Spotlight.Names[0] != "鲍勃" {
		t.Errorf("unnamed player: %+v", other)
	}

	setSpotlight(t, sessionID, &models.SpotlightState{Stop: "loop"})
	if paused := status(user1ID, "mp_user1"); paused.Spotlight.Stop != "loop" || paused.Phase != "kp_floor" {
		t.Errorf("paused room: %+v", paused)
	}

	models.DB.Model(&models.GameSession{}).Where("id = ?", sessionID).Update("status", models.SessionStatusEnded)
	if ended := status(creatorID, "creator_mp"); ended.Phase != "idle" || ended.CanKPContinue || ended.CanKPStop {
		t.Errorf("ended room: %+v", ended)
	}
}

func TestGetChatStatus_DeadNamedPlayerIsNotWaitedOn(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	models.DB.Model(&models.CharacterCard{}).Where("user_id = ?", user2ID).Update("wound_state", "dead")
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{user1ID, user2ID}, Names: []string{"爱丽丝", "鲍勃"}})

	h := NewSessionHandlers(&fakeRunner{})
	status := getChatStatus(t, makeTestRouter(h, user1ID, "mp_user1"), sessionID)
	if status.Waiting.Total != 1 || len(status.Spotlight.UserIDs) != 1 || status.Spotlight.UserIDs[0] != user1ID {
		t.Errorf("dead player must be dropped from the collection: %+v", status)
	}
}

func TestBuildWaitingPayload_QueryFailureReturnsError(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	var session models.GameSession
	models.DB.Preload("Players.User").Preload("Players.CharacterCard").First(&session, sessionID)

	if err := models.DB.Migrator().DropTable(&models.SessionTurnAction{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if _, err := buildWaitingPayload(models.DB, session, agent.TurnCollection{UserIDs: []uint{user1ID}}); err == nil {
		t.Fatal("expected a query error to be surfaced")
	}
}
