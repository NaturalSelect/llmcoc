package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func companionAdminRouter(adminID uint) *gin.Engine {
	r := gin.New()
	auth := withAuth(adminID, "root", "admin")
	r.GET("/admin/companion-cards", auth, AdminListCompanionCards)
	r.POST("/admin/companion-cards", auth, AdminAddCompanionCard)
	r.POST("/admin/companion-cards/:id/return", auth, AdminReturnCompanionCard)
	return r
}

// lockCardInLobby 让一张卡被未结束的房间占用。
func lockCardInLobby(t *testing.T, ownerID, cardID uint) {
	t.Helper()
	scenarioID := seedScenario(t, "pool scenario")
	session := models.GameSession{Name: "r", ScenarioID: scenarioID, Status: models.SessionStatusLobby, MaxPlayers: 4, CreatedBy: ownerID, TurnRound: 1}
	if err := models.DB.Create(&session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := models.DB.Create(&models.SessionPlayer{SessionID: session.ID, UserID: ownerID, CharacterCardID: cardID}).Error; err != nil {
		t.Fatalf("create player: %v", err)
	}
}

func TestAdminCompanionCards_AddThenListThenReturn(t *testing.T) {
	initTestDB(t)
	adminID := seedUser(t, "root", models.RoleAdmin, 0, 3)
	botID, err := models.EnsureCompanionUser()
	if err != nil {
		t.Fatalf("EnsureCompanionUser: %v", err)
	}
	cardID := seedCard(t, adminID, "卡池调查员")
	r := companionAdminRouter(adminID)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/companion-cards", map[string]any{"card_id": cardID}))
	if w.Code != http.StatusOK {
		t.Fatalf("add: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var card models.CharacterCard
	models.DB.First(&card, cardID)
	if card.UserID != botID {
		t.Fatalf("card owner = %d, want the bot %d", card.UserID, botID)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/companion-cards", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var list []companionCardView
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != cardID || list[0].InSession || list[0].HP != 10 {
		t.Fatalf("list = %+v, want the one idle pool card", list)
	}

	lockCardInLobby(t, botID, cardID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/companion-cards", nil))
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || !list[0].InSession {
		t.Fatalf("list after joining a room = %+v, want in_session=true", list)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/admin/companion-cards/%d/return", cardID), nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("return while in a room: want 409, got %d: %s", w.Code, w.Body.String())
	}

	models.DB.Where("character_card_id = ?", cardID).Delete(&models.SessionPlayer{})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/admin/companion-cards/%d/return", cardID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("return: want 200, got %d: %s", w.Code, w.Body.String())
	}
	models.DB.First(&card, cardID)
	if card.UserID != adminID {
		t.Errorf("card owner after return = %d, want admin %d", card.UserID, adminID)
	}
}

func TestAdminAddCompanionCard_Rejections(t *testing.T) {
	initTestDB(t)
	adminID := seedUser(t, "root", models.RoleAdmin, 0, 3)
	otherID := seedUser(t, "other", models.RoleUser, 0, 3)
	if _, err := models.EnsureCompanionUser(); err != nil {
		t.Fatalf("EnsureCompanionUser: %v", err)
	}
	r := companionAdminRouter(adminID)

	post := func(cardID uint) int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq("POST", "/admin/companion-cards", map[string]any{"card_id": cardID}))
		return w.Code
	}

	if code := post(seedCard(t, otherID, "别人的卡")); code != http.StatusForbidden {
		t.Errorf("someone else's card: want 403, got %d", code)
	}
	if code := post(9999); code != http.StatusNotFound {
		t.Errorf("missing card: want 404, got %d", code)
	}
	inactive := seedCard(t, adminID, "失活卡")
	models.DB.Model(&models.CharacterCard{}).Where("id = ?", inactive).Update("is_active", false)
	if code := post(inactive); code != http.StatusBadRequest {
		t.Errorf("inactive card: want 400, got %d", code)
	}
	locked := seedCard(t, adminID, "占用卡")
	lockCardInLobby(t, adminID, locked)
	if code := post(locked); code != http.StatusConflict {
		t.Errorf("locked card: want 409, got %d", code)
	}
}

func TestAdminReturnCompanionCard_SlotsFullAndNotInPool(t *testing.T) {
	initTestDB(t)
	adminID := seedUser(t, "root", models.RoleAdmin, 0, 1)
	botID, err := models.EnsureCompanionUser()
	if err != nil {
		t.Fatalf("EnsureCompanionUser: %v", err)
	}
	seedCard(t, adminID, "占满槽位")
	poolCard := seedCard(t, botID, "池中卡")
	ownCard := seedCard(t, adminID, "自己的第二张")
	r := companionAdminRouter(adminID)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/admin/companion-cards/%d/return", poolCard), nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("slots full: want 403, got %d: %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/admin/companion-cards/%d/return", ownCard), nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("card not in the pool: want 404, got %d: %s", w.Code, w.Body.String())
	}
}
