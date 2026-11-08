package handlers

// NOTE: 房间状态变化（加入/离开/开局/结束/复活/每一拍结算）要通过 session 事件推给客户端，
// 开局和复活都要通过 session 事件推给客户端，复活提示还要补发 message_new；这里用 tapRoom 订阅 hub 断言。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
)

func sessionEventStatus(t *testing.T, ev roomEvent) (status, reason any) {
	t.Helper()
	if ev.Type != "session" || ev.Seq == 0 {
		t.Fatalf("event = %+v, want a persistent session event", ev)
	}
	return eventField(ev, "status"), eventField(ev, "reason")
}

func TestSessionEvents_JoinAndLeave(t *testing.T) {
	initTestDB(t)
	sessID, _ := setupLobbySession(t)
	joinerID := seedUser(t, "joiner", "user", 0, 3)
	otherID := seedUser(t, "other", "user", 0, 3)
	cardID := seedCard(t, joinerID, "Card")
	otherCard := seedCard(t, otherID, "Other")
	models.DB.Create(&models.SessionPlayer{SessionID: sessID, UserID: otherID, CharacterCardID: otherCard})
	tap := tapRoom(t, sessID)

	w := httptest.NewRecorder()
	sessionCRUDRouter(joinerID, "joiner", "user").ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/join", sessID), map[string]any{
		"character_card_id": cardID,
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("join: %d %s", w.Code, w.Body.String())
	}
	tap.waitCount("session", 1)
	if status, reason := sessionEventStatus(t, tap.ofType("session")[0]); status != models.SessionStatusLobby || reason != "join" {
		t.Errorf("join event = %v/%v, want lobby/join", status, reason)
	}
	if got := tap.persistentTypes(); len(got) != 1 {
		t.Errorf("join must not publish message_new (system messages are hidden from /messages), got %v", got)
	}

	w = httptest.NewRecorder()
	sessionCRUDRouter(joinerID, "joiner", "user").ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/leave", sessID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("leave: %d %s", w.Code, w.Body.String())
	}
	tap.waitCount("session", 2)
	if status, reason := sessionEventStatus(t, tap.ofType("session")[1]); status != models.SessionStatusLobby || reason != "leave" {
		t.Errorf("leave event = %v/%v, want lobby/leave", status, reason)
	}
}

func TestSessionEvents_LeaveThatDisbandsRoomPublishesNothing(t *testing.T) {
	initTestDB(t)
	sessID, _ := setupLobbySession(t)
	uid := seedUser(t, "last", "user", 0, 3)
	cardID := seedCard(t, uid, "Card")
	models.DB.Create(&models.SessionPlayer{SessionID: sessID, UserID: uid, CharacterCardID: cardID})
	tap := tapRoom(t, sessID)
	seqBefore := hubFor(sessID).Seq()

	w := httptest.NewRecorder()
	sessionCRUDRouter(uid, "last", "user").ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/leave", sessID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("leave: %d %s", w.Code, w.Body.String())
	}
	if got := hubFor(sessID).Seq(); got != seqBefore || len(tap.sub.ch) != 0 {
		t.Errorf("a disbanded room must not publish, hub seq %d -> %d, queued events %d", seqBefore, got, len(tap.sub.ch))
	}
}

func TestSessionEvents_StartPublishesOnlySession(t *testing.T) {
	initTestDB(t)
	sessID, ownerID := setupLobbySession(t)
	cardID := seedCard(t, ownerID, "Card")
	models.DB.Create(&models.SessionPlayer{SessionID: sessID, UserID: ownerID, CharacterCardID: cardID})
	tap := tapRoom(t, sessID)

	w := httptest.NewRecorder()
	sessionCRUDRouter(ownerID, "owner", "user").ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/start", sessID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	tap.waitCount("session", 1)

	if got := fmt.Sprint(tap.persistentTypes()); got != "[session]" {
		t.Fatalf("events = %s, want [session]", got)
	}
	if status, reason := sessionEventStatus(t, tap.ofType("session")[0]); status != models.SessionStatusPlaying || reason != "start" {
		t.Errorf("start event = %v/%v, want playing/start", status, reason)
	}
}

func TestSessionEvents_EndPublishesEnded(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "endowner", "user", 500, 3)
	sID := seedScenario(t, "S")
	sess := models.GameSession{Name: "R", ScenarioID: sID, Status: models.SessionStatusPlaying, MaxPlayers: 4, CreatedBy: uid}
	models.DB.Create(&sess)
	cardID := seedCard(t, uid, "Card")
	models.DB.Create(&models.SessionPlayer{SessionID: sess.ID, UserID: uid, CharacterCardID: cardID})
	tap := tapRoom(t, sess.ID)

	w := httptest.NewRecorder()
	sessionCRUDRouter(uid, "endowner", "user").ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/end", sess.ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("end: %d %s", w.Code, w.Body.String())
	}
	tap.waitCount("session", 1)
	if status, reason := sessionEventStatus(t, tap.ofType("session")[0]); status != models.SessionStatusEnded || reason != "end" {
		t.Errorf("end event = %v/%v, want ended/end", status, reason)
	}
}

func TestSessionEvents_AdminEndAllPublishesPerRoom(t *testing.T) {
	initTestDB(t)
	adminID := seedUser(t, "admin", "admin", 0, 3)
	sID := seedScenario(t, "S")
	var ids [2]uint
	for i := range ids {
		sess := models.GameSession{Name: fmt.Sprintf("R%d", i), ScenarioID: sID, Status: models.SessionStatusPlaying, MaxPlayers: 4, CreatedBy: adminID}
		models.DB.Create(&sess)
		ids[i] = sess.ID
	}
	taps := [2]*roomTap{tapRoom(t, ids[0]), tapRoom(t, ids[1])}

	r := gin.New()
	r.POST("/admin/sessions/end-all", withAuth(adminID, "admin", "admin"), AdminEndAllSessions)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/sessions/end-all", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("end-all: %d %s", w.Code, w.Body.String())
	}
	for i, tap := range taps {
		tap.waitCount("session", 1)
		if status, reason := sessionEventStatus(t, tap.ofType("session")[0]); status != models.SessionStatusEnded || reason != "end" {
			t.Errorf("room %d event = %v/%v, want ended/end", ids[i], status, reason)
		}
	}
}

func TestSessionEvents_RevivePublishesNoticeThenSession(t *testing.T) {
	initTestDB(t)
	adminID := seedUser(t, "admin", "admin", 0, 3)
	sID := seedScenario(t, "S")
	sess := models.GameSession{Name: "R", ScenarioID: sID, Status: models.SessionStatusEnded, MaxPlayers: 4, CreatedBy: adminID}
	models.DB.Create(&sess)
	cardID := seedCard(t, adminID, "Card")
	models.DB.Create(&models.SessionPlayer{SessionID: sess.ID, UserID: adminID, CharacterCardID: cardID})
	tap := tapRoom(t, sess.ID)

	r := gin.New()
	r.POST("/sessions/:id/revive", withAuth(adminID, "admin", "admin"), ReviveSession)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/revive", sess.ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("revive: %d %s", w.Code, w.Body.String())
	}
	tap.waitCount("session", 1)

	if got := fmt.Sprint(tap.persistentTypes()); got != "[message_new session]" {
		t.Fatalf("events = %s, want [message_new session]", got)
	}
	msg := eventMessage(tap.ofType("message_new")[0])
	if msg.Kind != models.MessageKindSystem || msg.ID == 0 || msg.Content == "" {
		t.Errorf("revive notice = %+v", msg)
	}
	if status, reason := sessionEventStatus(t, tap.ofType("session")[0]); status != models.SessionStatusPlaying || reason != "revive" {
		t.Errorf("revive event = %v/%v, want playing/revive", status, reason)
	}
}

func TestSessionEvents_FloorPublishesCardsAfterEachBeatAndAfterFloorEnd(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	setSpotlight(t, sessionID, &models.SpotlightState{})
	runner := &fakeRunner{}
	runner.run = func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Items: []agent.Output{{Kind: agent.OutputKP, Text: "第一拍。"}}})
		return agent.RunOutput{Messages: 1, Ask: &agent.AskResult{UserIDs: []uint{userID}, Names: []string{"Test Char"}, Question: "怎么办？"}}, nil
	}
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	tap.waitCount("session", 2)

	// NOTE: 顺序：开场 spotlight/floor_start → 一拍的 message_new 与 session → 周期收尾的 spotlight/floor_end 与 session。
	got := fmt.Sprint(tap.persistentTypes())
	want := "[spotlight floor_start message_new session spotlight floor_end session]"
	if got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
	for i, ev := range tap.ofType("session") {
		if status, reason := sessionEventStatus(t, ev); status != models.SessionStatusPlaying || reason != "cards" {
			t.Errorf("session event %d = %v/%v, want playing/cards", i, status, reason)
		}
	}
}

func TestSessionEvents_EndGameReportsEndedStatus(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{})
	runner := &fakeRunner{}
	runner.run = func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		models.DB.Model(&models.GameSession{}).Where("id = ?", sessionID).Update("status", models.SessionStatusEnded)
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Items: []agent.Output{{Kind: agent.OutputKP, Text: "全剧终。", Ended: true}}})
		return agent.RunOutput{Messages: 1, Ended: true}, nil
	}
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	tap.waitCount("session", 2)

	for i, ev := range tap.ofType("session") {
		if status, _ := sessionEventStatus(t, ev); status != models.SessionStatusEnded {
			t.Errorf("session event %d status = %v, want ended", i, status)
		}
	}
}
