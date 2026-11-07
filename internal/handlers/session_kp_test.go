package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/handlers/mocks"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"go.uber.org/mock/gomock"
)

func kpRouter(h *SessionHandlers, userID uint, username string) *gin.Engine {
	r := gin.New()
	r.Use(withAuth(userID, username, "user"))
	r.GET("/sessions/:id/chat-status", h.GetChatStatus)
	r.POST("/sessions/:id/chat", h.ChatStream)
	r.POST("/sessions/:id/kp-turn", h.KPTurn)
	return r
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

func postKPTurn(r *gin.Engine, sessionID uint) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, fmt.Sprintf("/sessions/%d/kp-turn", sessionID), nil))
	return w
}

func countMessages(sessionID uint, role models.MessageRole) int64 {
	var n int64
	models.DB.Model(&models.Message{}).Where("session_id = ? AND role = ?", sessionID, role).Count(&n)
	return n
}

func TestStartSession_WritesEmptySpotlight(t *testing.T) {
	initTestDB(t)
	sessID, ownerID := setupLobbySession(t)
	cardID := seedCard(t, ownerID, "Card")
	models.DB.Create(&models.SessionPlayer{SessionID: sessID, UserID: ownerID, CharacterCardID: cardID})

	r := sessionCRUDRouter(ownerID, "owner", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/start", sessID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	sp := loadSpotlight(t, sessID)
	if sp == nil {
		t.Fatal("spotlight must be non-nil after start so the KP drives the room")
	}
	if len(sp.UserIDs) != 0 || sp.AutoContinues != 0 {
		t.Errorf("spotlight = %+v, want empty (KP holds the floor)", sp)
	}
}

func TestChatStream_Spotlight_UnnamedPlayerRejected(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{
		UserIDs: []uint{user2ID}, Names: []string{"鲍勃"}, Question: "你打算怎么开门？",
	})

	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl)) // 不设期望：任何 Run 调用都会让测试失败
	w := httptest.NewRecorder()
	kpRouter(h, user1ID, "mp_user1").ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我抢着说话"))

	events := parseSSE(w.Body.String())
	if len(events["error"]) == 0 {
		t.Fatalf("expected error event; body:\n%s", w.Body.String())
	}
	msg := events["error"][0]
	if !strings.Contains(msg, "KP 正在等待 鲍勃 回应") || !strings.Contains(msg, "你打算怎么开门？") {
		t.Errorf("reject message = %q", msg)
	}
	if n := countMessages(sessionID, models.MessageRoleUser); n != 0 {
		t.Errorf("rejected input must not be persisted, got %d user messages", n)
	}
	var actions int64
	models.DB.Model(&models.SessionTurnAction{}).Where("session_id = ?", sessionID).Count(&actions)
	if actions != 0 {
		t.Errorf("rejected input must not create a turn action, got %d", actions)
	}
}

func TestChatStream_Spotlight_KPHoldsFloorRejectsEveryone(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{})

	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))
	w := httptest.NewRecorder()
	kpRouter(h, user1ID, "mp_user1").ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我先说"))

	events := parseSSE(w.Body.String())
	if len(events["error"]) == 0 || !strings.Contains(events["error"][0], "KP 正在推进剧情") {
		t.Fatalf("want KP-is-driving rejection, body:\n%s", w.Body.String())
	}
}

func TestChatStream_Spotlight_NamedPlayerTriggersRunAndPassesQuestion(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{
		UserIDs: []uint{user1ID}, Names: []string{"爱丽丝"}, Question: "你听见了什么？",
	})

	var got agent.GameContext
	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
			got = gctx
			return agent.RunOutput{
				KPReply: "门缝里透出微光。鲍勃，你怎么办？",
				Ask:     &agent.AskResult{UserIDs: []uint{user2ID}, Names: []string{"鲍勃"}, Question: "鲍勃，你怎么办？"},
			}, nil
		})

	h := NewSessionHandlers(runner)
	w := httptest.NewRecorder()
	kpRouter(h, user1ID, "mp_user1").ServeHTTP(w, formPost(fmt.Sprintf("/sessions/%d/chat", sessionID), "我贴着门听"))

	if got.AskedQuestion != "你听见了什么？" {
		t.Errorf("AskedQuestion = %q", got.AskedQuestion)
	}
	if got.KPFloor != "" || got.UserInput != "我贴着门听" {
		t.Errorf("player-response segment must carry input and no KP floor: floor=%q input=%q", got.KPFloor, got.UserInput)
	}

	sp := loadSpotlight(t, sessionID)
	if sp == nil || len(sp.UserIDs) != 1 || sp.UserIDs[0] != user2ID || sp.Question != "鲍勃，你怎么办？" || sp.AutoContinues != 0 {
		t.Fatalf("spotlight after ask = %+v", sp)
	}

	events := parseSSE(w.Body.String())
	if len(events["spotlight"]) != 1 || len(events["kp_done"]) != 1 || len(events["done"]) != 1 {
		t.Fatalf("want one spotlight/kp_done/done, got body:\n%s", w.Body.String())
	}
	var payload struct {
		UserIDs  []uint `json:"user_ids"`
		Question string `json:"question"`
		KPFloor  bool   `json:"kp_floor"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(events["spotlight"][0])), &payload); err != nil {
		t.Fatalf("spotlight payload invalid: %v", err)
	}
	if len(payload.UserIDs) != 1 || payload.UserIDs[0] != user2ID || payload.KPFloor {
		t.Errorf("spotlight payload = %+v", payload)
	}

	if _, processing := getSessionProcessing(sessionID); processing {
		t.Error("processing must be released after an ask")
	}
}

func TestKPTurn_AutoContinuesUntilLimitThenOpensToAll(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{})
	if err := models.SetSiteSetting("kp_auto_continue_max", "1"); err != nil {
		t.Fatalf("set site setting: %v", err)
	}

	var floors []string
	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Times(2).
		DoAndReturn(func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
			floors = append(floors, gctx.KPFloor)
			return agent.RunOutput{KPReply: fmt.Sprintf("第%d段旁白", len(floors))}, nil
		})

	h := NewSessionHandlers(runner)
	creatorID := sessionCreator(t, sessionID)
	w := postKPTurn(kpRouter(h, creatorID, "creator_mp"), sessionID)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if len(floors) != 2 || floors[0] != agent.KPFloorOpening || floors[1] != agent.KPFloorContinue {
		t.Fatalf("KP floors = %v, want [opening continue]", floors)
	}

	events := parseSSE(w.Body.String())
	if len(events["kp_done"]) != 2 || len(events["spotlight"]) != 2 || len(events["done"]) != 1 {
		t.Fatalf("want 2 kp_done, 2 spotlight, 1 done; body:\n%s", w.Body.String())
	}

	sp := loadSpotlight(t, sessionID)
	if sp == nil || len(sp.UserIDs) != 2 {
		t.Fatalf("limit exceeded must open the floor to every living player, got %+v", sp)
	}
	open := map[uint]bool{sp.UserIDs[0]: true, sp.UserIDs[1]: true}
	if !open[user1ID] || !open[user2ID] {
		t.Errorf("spotlight users = %v, want both players", sp.UserIDs)
	}

	if n := countMessages(sessionID, models.MessageRoleAssistant); n != 2 {
		t.Errorf("assistant messages = %d, want 2", n)
	}
	if n := countMessages(sessionID, models.MessageRoleUser); n != 0 {
		t.Errorf("KP-initiated segments must not write user messages, got %d", n)
	}
	if _, processing := getSessionProcessing(sessionID); processing {
		t.Error("processing must be released when the driver finishes")
	}
}

func TestKPTurn_ClientDisconnectStillFinishesAllSegments(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{})
	if err := models.SetSiteSetting("kp_auto_continue_max", "1"); err != nil {
		t.Fatalf("set site setting: %v", err)
	}

	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).Times(2).
		Return(agent.RunOutput{KPReply: "旁白"}, nil)

	h := NewSessionHandlers(runner)
	creatorID := sessionCreator(t, sessionID)
	r := kpRouter(h, creatorID, "creator_mp")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := jsonReq(http.MethodPost, fmt.Sprintf("/sessions/%d/kp-turn", sessionID), nil).WithContext(ctx)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if n := countMessages(sessionID, models.MessageRoleAssistant); n != 2 {
		t.Errorf("assistant messages = %d, want 2 even though the client is gone", n)
	}
	if strings.Contains(w.Body.String(), "event:") {
		t.Errorf("nothing should be written to a disconnected client, got:\n%s", w.Body.String())
	}
	if _, processing := getSessionProcessing(sessionID); processing {
		t.Error("processing must be released after a disconnect")
	}
}

func TestKPTurn_HostContinueSkipsUnresponsiveAndKeepsSubmitted(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{
		UserIDs: []uint{user1ID, user2ID}, Names: []string{"爱丽丝", "鲍勃"}, Question: "你们怎么办？",
	})
	// 已经有过玩家发言，所以不是开场。
	uid := user1ID
	models.DB.Create(&models.Message{SessionID: sessionID, UserID: &uid, Role: models.MessageRoleUser, Content: "早先的行动", Username: "爱丽丝"})
	models.DB.Create(&models.SessionTurnAction{SessionID: sessionID, Round: 1, UserID: user1ID, Username: "爱丽丝", ActionSummary: "我推门"})

	var got agent.GameContext
	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
			got = gctx
			return agent.RunOutput{
				KPReply: "门开了。",
				Ask:     &agent.AskResult{UserIDs: []uint{user1ID}, Names: []string{"爱丽丝"}, Question: "你看到什么？"},
			}, nil
		})

	h := NewSessionHandlers(runner)
	creatorID := sessionCreator(t, sessionID)
	w := postKPTurn(kpRouter(h, creatorID, "creator_mp"), sessionID)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	if got.KPFloor != agent.KPFloorHostContinue {
		t.Errorf("KPFloor = %q, want host_continue", got.KPFloor)
	}
	if len(got.SkippedNames) != 1 || got.SkippedNames[0] != "鲍勃" {
		t.Errorf("SkippedNames = %v, want [鲍勃]", got.SkippedNames)
	}
	if len(got.PendingActions) != 1 || got.PendingActions[0].Content != "我推门" {
		t.Errorf("PendingActions = %+v, want Alice's submitted action", got.PendingActions)
	}
}

func TestKPTurn_OnlyHostOrAdmin(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{})

	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))
	w := postKPTurn(kpRouter(h, user1ID, "mp_user1"), sessionID)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-host player got %d, want 403", w.Code)
	}
}

func TestKPTurn_AdminAllowed(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{})
	adminID := seedUser(t, "root", models.RoleAdmin, 0, 3)

	ctrl := gomock.NewController(t)
	runner := mocks.NewMockAgentRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), gomock.Any()).
		Return(agent.RunOutput{KPReply: "开场", Ask: &agent.AskResult{UserIDs: []uint{1}, Names: []string{"爱丽丝"}, Question: "q"}}, nil)

	h := NewSessionHandlers(runner)
	r := gin.New()
	r.Use(withAuth(adminID, "root", "admin"))
	r.POST("/sessions/:id/kp-turn", h.KPTurn)
	w := postKPTurn(r, sessionID)
	if w.Code != http.StatusOK {
		t.Fatalf("admin got %d: %s", w.Code, w.Body.String())
	}
}

func TestKPTurn_LegacyRoomRejected(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃") // Spotlight 为 NULL
	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))
	creatorID := sessionCreator(t, sessionID)
	w := postKPTurn(kpRouter(h, creatorID, "creator_mp"), sessionID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("legacy room got %d, want 400", w.Code)
	}
}

func TestKPTurn_ConflictWhileProcessing(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{})
	if !beginSessionProcessing(sessionID) {
		t.Fatal("beginSessionProcessing failed")
	}

	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))
	creatorID := sessionCreator(t, sessionID)
	w := postKPTurn(kpRouter(h, creatorID, "creator_mp"), sessionID)
	if w.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409", w.Code)
	}
}

func TestGetChatStatus_SpotlightPhases(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	creatorID := sessionCreator(t, sessionID)
	ctrl := gomock.NewController(t)
	h := NewSessionHandlers(mocks.NewMockAgentRunner(ctrl))

	status := func(userID uint, name string) chatStatusResponse {
		w := httptest.NewRecorder()
		kpRouter(h, userID, name).ServeHTTP(w, jsonReq(http.MethodGet, fmt.Sprintf("/sessions/%d/chat-status", sessionID), nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status code = %d", w.Code)
		}
		return decodeChatStatus(t, w.Body.String())
	}

	legacy := status(user1ID, "mp_user1")
	if legacy.Spotlight != nil || legacy.CanKPContinue {
		t.Errorf("legacy room must report no spotlight: %+v", legacy)
	}

	setSpotlight(t, sessionID, &models.SpotlightState{})
	floor := status(user1ID, "mp_user1")
	if floor.Phase != "kp_floor" || floor.Spotlight == nil || !floor.Spotlight.KPFloor {
		t.Errorf("empty spotlight: %+v", floor)
	}
	if floor.CanKPContinue {
		t.Error("a plain player must not get the continue button")
	}
	if !status(creatorID, "creator_mp").CanKPContinue {
		t.Error("the host must be able to continue the KP")
	}

	setSpotlight(t, sessionID, &models.SpotlightState{
		UserIDs: []uint{user2ID}, Names: []string{"鲍勃"}, Question: "你怎么办？",
	})
	asked := status(user2ID, "mp_user2")
	if asked.Phase != "awaiting_me" || asked.Spotlight == nil || !asked.Spotlight.AskedMe || asked.Spotlight.Question != "你怎么办？" {
		t.Errorf("named player: %+v", asked)
	}
	other := status(user1ID, "mp_user1")
	if other.Phase == "awaiting_me" || other.Spotlight == nil || other.Spotlight.AskedMe {
		t.Errorf("unnamed player: %+v", other)
	}
	if len(other.Spotlight.Names) != 1 || other.Spotlight.Names[0] != "鲍勃" {
		t.Errorf("spotlight names = %v", other.Spotlight.Names)
	}
}

func TestNextSpotlight(t *testing.T) {
	players := []models.SessionPlayer{
		{UserID: 1, CharacterCard: models.CharacterCard{Name: "甲"}},
		{UserID: 2, CharacterCard: models.CharacterCard{Name: "乙", WoundState: "dead"}},
		{UserID: 3, CharacterCard: models.CharacterCard{Name: "丙"}},
	}

	t.Run("点名后清零并交出发言权", func(t *testing.T) {
		prev := &models.SpotlightState{AutoContinues: 2}
		next, retain := nextSpotlight(prev, &agent.AskResult{UserIDs: []uint{3}, Names: []string{"丙"}, Question: "q"}, players, 2)
		if retain || next.AutoContinues != 0 || len(next.UserIDs) != 1 || next.UserIDs[0] != 3 || next.Question != "q" {
			t.Errorf("got %+v retain=%v", next, retain)
		}
	})

	t.Run("没有点名且未超限时 KP 保留发言权", func(t *testing.T) {
		next, retain := nextSpotlight(&models.SpotlightState{AutoContinues: 1}, nil, players, 2)
		if !retain || next.AutoContinues != 2 || len(next.UserIDs) != 0 {
			t.Errorf("got %+v retain=%v", next, retain)
		}
	})

	t.Run("超限后对全体存活玩家开放，跳过死者", func(t *testing.T) {
		next, retain := nextSpotlight(&models.SpotlightState{AutoContinues: 2}, nil, players, 2)
		if retain || len(next.UserIDs) != 2 || next.UserIDs[0] != 1 || next.UserIDs[1] != 3 {
			t.Errorf("got %+v retain=%v", next, retain)
		}
		if next.AutoContinues != 0 {
			t.Errorf("opened floor should reset the counter, got %d", next.AutoContinues)
		}
	})

	t.Run("上限为0时第一段没点名就直接开放", func(t *testing.T) {
		next, retain := nextSpotlight(&models.SpotlightState{}, nil, players, 0)
		if retain || len(next.UserIDs) != 2 {
			t.Errorf("got %+v retain=%v", next, retain)
		}
	})

	t.Run("无人存活时不开放也不续跑", func(t *testing.T) {
		dead := []models.SessionPlayer{{UserID: 1, CharacterCard: models.CharacterCard{Name: "甲", WoundState: "dead"}}}
		next, retain := nextSpotlight(&models.SpotlightState{AutoContinues: 5}, nil, dead, 2)
		if retain || len(next.UserIDs) != 0 {
			t.Errorf("got %+v retain=%v", next, retain)
		}
	})
}
