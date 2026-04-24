package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"golang.org/x/crypto/bcrypt"
)

// sessionCRUDRouter builds a router for session CRUD endpoints (no agent required).
func sessionCRUDRouter(userID uint, username, role string) *gin.Engine {
	r := gin.New()
	auth := withAuth(userID, username, role)
	r.GET("/sessions", auth, ListSessions)
	r.GET("/sessions/:id", auth, GetSession)
	r.POST("/sessions", auth, CreateSession)
	r.POST("/sessions/:id/join", auth, JoinSession)
	r.POST("/sessions/:id/start", auth, StartSession)
	r.POST("/sessions/:id/end", auth, EndSession)
	r.GET("/sessions/:id/messages", auth, GetMessages)
	return r
}

// ── ListSessions ───────────────────────────────────────────────────────────────

func TestListSessions_Empty(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	r := sessionCRUDRouter(uid, "u", "user")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/sessions", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp []any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 0 {
		t.Errorf("want empty, got %d", len(resp))
	}
}

func TestListSessions_OnlyActiveStatuses(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	sid := seedScenario(t, "S")

	models.DB.Create(&models.GameSession{
		Name: "Lobby Room", ScenarioID: sid,
		Status: models.SessionStatusLobby, MaxPlayers: 4, CreatedBy: uid,
	})
	models.DB.Create(&models.GameSession{
		Name: "Playing Room", ScenarioID: sid,
		Status: models.SessionStatusPlaying, MaxPlayers: 4, CreatedBy: uid,
	})
	// Ended session should be excluded.
	models.DB.Create(&models.GameSession{
		Name: "Ended Room", ScenarioID: sid,
		Status: models.SessionStatusEnded, MaxPlayers: 4, CreatedBy: uid,
	})

	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/sessions", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp []any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 2 {
		t.Errorf("want 2 (lobby+playing), got %d", len(resp))
	}
}

// ── GetSession ─────────────────────────────────────────────────────────────────

func TestGetSession_Found(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	sid := seedScenario(t, "S")
	session := models.GameSession{
		Name: "Test Room", ScenarioID: sid,
		Status: models.SessionStatusLobby, MaxPlayers: 4, CreatedBy: uid,
	}
	models.DB.Create(&session)

	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/sessions/%d", session.ID), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/sessions/9999", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// ── CreateSession ──────────────────────────────────────────────────────────────

func TestCreateSession_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	sid := seedScenario(t, "Dark Scenario")

	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/sessions", map[string]any{
		"name":        "My Room",
		"scenario_id": sid,
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateSession_InvalidBody(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	// Missing required scenario_id.
	r.ServeHTTP(w, jsonReq("POST", "/sessions", map[string]any{"name": "x"}))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestCreateSession_ScenarioNotFound(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/sessions", map[string]any{
		"name":        "My Room",
		"scenario_id": 9999,
	}))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 (scenario not found), got %d", w.Code)
	}
}

// ── JoinSession ────────────────────────────────────────────────────────────────

// setupLobbySession creates a scenario, session owner, and a lobby session.
func setupLobbySession(t *testing.T) (sessionID, ownerID uint) {
	t.Helper()
	ownID := seedUser(t, "owner", "user", 0, 3)
	sID := seedScenario(t, "S")
	sess := models.GameSession{
		Name: "Lobby", ScenarioID: sID,
		Status: models.SessionStatusLobby, MaxPlayers: 4, CreatedBy: ownID,
	}
	models.DB.Create(&sess)
	return sess.ID, ownID
}

func TestJoinSession_Success(t *testing.T) {
	initTestDB(t)
	sessID, _ := setupLobbySession(t)
	joinerID := seedUser(t, "joiner", "user", 0, 3)
	cardID := seedCard(t, joinerID, "Card")

	r := sessionCRUDRouter(joinerID, "joiner", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/join", sessID), map[string]any{
		"character_card_id": cardID,
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestJoinSession_NotFound(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/sessions/9999/join", map[string]any{
		"character_card_id": uint(1),
	}))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestJoinSession_AlreadyJoined(t *testing.T) {
	initTestDB(t)
	sessID, _ := setupLobbySession(t)
	uid := seedUser(t, "joiner", "user", 0, 3)
	cardID := seedCard(t, uid, "Card")

	// Join once.
	models.DB.Create(&models.SessionPlayer{
		SessionID: sessID, UserID: uid, CharacterCardID: cardID,
	})

	r := sessionCRUDRouter(uid, "joiner", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/join", sessID), map[string]any{
		"character_card_id": cardID,
	}))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 (already joined), got %d", w.Code)
	}
}

func TestJoinSession_CardNotOwned(t *testing.T) {
	initTestDB(t)
	sessID, ownerID := setupLobbySession(t)
	joinerID := seedUser(t, "joiner", "user", 0, 3)
	// Card belongs to owner, not joiner.
	cardID := seedCard(t, ownerID, "Owner's Card")

	r := sessionCRUDRouter(joinerID, "joiner", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/join", sessID), map[string]any{
		"character_card_id": cardID,
	}))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestJoinSession_WrongPassword(t *testing.T) {
	initTestDB(t)
	ownerID := seedUser(t, "owner", "user", 0, 3)
	sID := seedScenario(t, "S")
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct"), bcrypt.MinCost)
	sess := models.GameSession{
		Name: "PasswordRoom", ScenarioID: sID,
		Status: models.SessionStatusLobby, MaxPlayers: 4, CreatedBy: ownerID,
		HasPassword: true, Password: string(hash),
	}
	models.DB.Create(&sess)

	joinerID := seedUser(t, "joiner", "user", 0, 3)
	cardID := seedCard(t, joinerID, "Card")

	r := sessionCRUDRouter(joinerID, "joiner", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/join", sess.ID), map[string]any{
		"character_card_id": cardID,
		"password":          "wrong",
	}))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

// ── StartSession ───────────────────────────────────────────────────────────────

func TestStartSession_NotFound(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/sessions/9999/start", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestStartSession_NotOwner(t *testing.T) {
	initTestDB(t)
	sessID, _ := setupLobbySession(t)
	nonOwner := seedUser(t, "nonowner", "user", 0, 3)
	r := sessionCRUDRouter(nonOwner, "nonowner", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/start", sessID), nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestStartSession_AlreadyPlaying(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	sID := seedScenario(t, "S")
	sess := models.GameSession{
		Name: "R", ScenarioID: sID,
		Status: models.SessionStatusPlaying, MaxPlayers: 4, CreatedBy: uid,
	}
	models.DB.Create(&sess)

	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/start", sess.ID), nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 (already playing), got %d", w.Code)
	}
}

func TestStartSession_NoPlayers(t *testing.T) {
	initTestDB(t)
	sessID, ownerID := setupLobbySession(t)
	r := sessionCRUDRouter(ownerID, "owner", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/start", sessID), nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 (no players), got %d", w.Code)
	}
}

func TestStartSession_Success(t *testing.T) {
	initTestDB(t)
	sessID, ownerID := setupLobbySession(t)
	cardID := seedCard(t, ownerID, "Card")
	models.DB.Create(&models.SessionPlayer{
		SessionID: sessID, UserID: ownerID, CharacterCardID: cardID,
	})

	r := sessionCRUDRouter(ownerID, "owner", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/start", sessID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var sess models.GameSession
	models.DB.First(&sess, sessID)
	if sess.Status != models.SessionStatusPlaying {
		t.Errorf("status = %q, want playing", sess.Status)
	}
}

// ── EndSession ─────────────────────────────────────────────────────────────────

func TestEndSession_NotFound(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/sessions/9999/end", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestEndSession_NotOwner(t *testing.T) {
	initTestDB(t)
	sessID, _ := setupLobbySession(t)
	nonOwner := seedUser(t, "nonowner", "user", 0, 3)
	r := sessionCRUDRouter(nonOwner, "nonowner", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/end", sessID), nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestEndSession_AlreadyEnded(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	sID := seedScenario(t, "S")
	sess := models.GameSession{
		Name: "R", ScenarioID: sID,
		Status: models.SessionStatusEnded, MaxPlayers: 4, CreatedBy: uid,
	}
	models.DB.Create(&sess)

	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/end", sess.ID), nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 (already ended), got %d", w.Code)
	}
}

// ── GetMessages ────────────────────────────────────────────────────────────────

func TestGetMessages_Success(t *testing.T) {
	initTestDB(t)
	sessID, userID := seedPlayingSession(t)

	// Insert a user message.
	models.DB.Create(&models.Message{
		SessionID: sessID,
		Role:      models.MessageRoleUser,
		Content:   "Hello KP",
		Username:  "tester",
	})

	r := sessionCRUDRouter(userID, "tester", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/sessions/%d/messages", sessID), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var msgs []any
	json.NewDecoder(w.Body).Decode(&msgs)
	if len(msgs) == 0 {
		t.Error("expected at least one message")
	}
}

func TestGetMessages_NotInSession(t *testing.T) {
	initTestDB(t)
	sessID, _ := seedPlayingSession(t)
	stranger := seedUser(t, "stranger", "user", 0, 3)

	r := sessionCRUDRouter(stranger, "stranger", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/sessions/%d/messages", sessID), nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

// ── compile check ──────────────────────────────────────────────────────────────
// Verify formatters don't add unused import warnings.
var _ = strings.Contains
