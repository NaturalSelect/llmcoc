package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/handlers/mocks"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"go.uber.org/mock/gomock"
)

func charRouter(userID uint) *gin.Engine {
	r := gin.New()
	auth := withAuth(userID, "tester", "user")
	r.GET("/characters", auth, ListCharacters)
	r.POST("/characters", auth, CreateCharacter)
	r.GET("/characters/:id", auth, GetCharacter)
	r.PUT("/characters/:id", auth, UpdateCharacter)
	r.DELETE("/characters/:id", auth, DeleteCharacter)
	return r
}

func charRouterWithGenerate(userID uint, h *CharacterHandlers) *gin.Engine {
	r := gin.New()
	auth := withAuth(userID, "tester", "user")
	r.POST("/characters/generate", auth, h.GenerateCharacter)
	return r
}

// ── ListCharacters ─────────────────────────────────────────────────────────────

func TestListCharacters_Empty(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)
	r := charRouter(uid)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/characters", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp []any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 0 {
		t.Errorf("want empty list, got %d", len(resp))
	}
}

func TestListCharacters_OnlyOwn(t *testing.T) {
	initTestDB(t)
	uid1 := seedUser(t, "alice", "user", 0, 3)
	uid2 := seedUser(t, "bob", "user", 0, 3)
	seedCard(t, uid1, "Card A")
	seedCard(t, uid2, "Card B")

	r := charRouter(uid1)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/characters", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp []any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 1 {
		t.Errorf("want 1 card (own only), got %d", len(resp))
	}
}

// ── GetCharacter ───────────────────────────────────────────────────────────────

func TestGetCharacter_OwnCard(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)
	cid := seedCard(t, uid, "My Card")

	r := charRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/characters/%d", cid), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestGetCharacter_OtherCard_Forbidden(t *testing.T) {
	initTestDB(t)
	uid1 := seedUser(t, "alice", "user", 0, 3)
	uid2 := seedUser(t, "bob", "user", 0, 3)
	cid := seedCard(t, uid2, "Bob's Card")

	r := charRouter(uid1)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/characters/%d", cid), nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestGetCharacter_NotFound(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)
	r := charRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/characters/9999", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// ── CreateCharacter ────────────────────────────────────────────────────────────

func TestCreateCharacter_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)

	r := charRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters", map[string]any{
		"name":   "Investigator",
		"gender": "男",
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateCharacter_SlotFull(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 1) // only 1 slot
	seedCard(t, uid, "Existing")

	r := charRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters", map[string]any{
		"name": "Second Card",
	}))

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403 (slot full), got %d", w.Code)
	}
}

func TestCreateCharacter_InvalidBody(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)
	r := charRouter(uid)
	w := httptest.NewRecorder()
	// Missing required "name" field.
	r.ServeHTTP(w, jsonReq("POST", "/characters", map[string]any{}))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// ── UpdateCharacter ────────────────────────────────────────────────────────────

func TestUpdateCharacter_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)
	cid := seedCard(t, uid, "Old Name")

	r := charRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", fmt.Sprintf("/characters/%d", cid), map[string]any{
		"name": "New Name",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var card models.CharacterCard
	models.DB.First(&card, cid)
	if card.Name != "New Name" {
		t.Errorf("name = %q, want 'New Name'", card.Name)
	}
}

func TestUpdateCharacter_Forbidden(t *testing.T) {
	initTestDB(t)
	uid1 := seedUser(t, "alice", "user", 0, 3)
	uid2 := seedUser(t, "bob", "user", 0, 3)
	cid := seedCard(t, uid2, "Bob's")

	r := charRouter(uid1)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", fmt.Sprintf("/characters/%d", cid), map[string]any{
		"name": "Hijacked",
	}))

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

// ── DeleteCharacter ────────────────────────────────────────────────────────────

func TestDeleteCharacter_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)
	cid := seedCard(t, uid, "ToDelete")

	r := charRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", fmt.Sprintf("/characters/%d", cid), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	// Soft delete: is_active = false.
	var card models.CharacterCard
	models.DB.First(&card, cid)
	if card.IsActive {
		t.Error("card should be soft-deleted (is_active=false)")
	}
}

func TestDeleteCharacter_Forbidden(t *testing.T) {
	initTestDB(t)
	uid1 := seedUser(t, "alice", "user", 0, 3)
	uid2 := seedUser(t, "bob", "user", 0, 3)
	cid := seedCard(t, uid2, "Bob's")

	r := charRouter(uid1)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", fmt.Sprintf("/characters/%d", cid), nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

// ── GenerateCharacter ──────────────────────────────────────────────────────────

func TestGenerateCharacter_LLMSuccess(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)

	ctrl := gomock.NewController(t)
	mockProv := mocks.NewMockProvider(ctrl)
	mockProv.EXPECT().AdjustSkills(gomock.Any(), gomock.Any()).Return(map[string]int{"侦查": 50, "聆听": 50}, nil).AnyTimes()
	mockProv.EXPECT().GenerateCharacter(gomock.Any(), gomock.Any()).Return(&llm.GeneratedCharacter{
		Backstory:  "神秘的背景",
		Appearance: "优雅的外貌",
		Traits:     "好奇心旺盛",
	}, nil)

	mockFac := mocks.NewMockCharacterLLMFactory(ctrl)
	mockFac.EXPECT().LoadProvider(models.AgentRoleWriter).Return(mockProv, nil)
	mockFac.EXPECT().LoadProvider(models.AgentRoleDirector).Return(mockProv, nil).AnyTimes()

	h := NewCharacterHandlers(mockFac)
	r := charRouterWithGenerate(uid, h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/generate", map[string]any{
		"name":       "阿加莎",
		"gender":     "女",
		"occupation": "侦探",
		"era":        "1920s",
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["name"] != "阿加莎" {
		t.Errorf("name = %v, want 阿加莎", resp["name"])
	}
}

func TestGenerateCharacter_LLMError_Fallback(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)

	ctrl := gomock.NewController(t)
	mockProv := mocks.NewMockProvider(ctrl)
	mockProv.EXPECT().GenerateCharacter(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("LLM unavailable"))

	mockFac := mocks.NewMockCharacterLLMFactory(ctrl)
	mockFac.EXPECT().LoadProvider(models.AgentRoleWriter).Return(mockProv, nil)
	mockFac.EXPECT().LoadProvider(models.AgentRoleDirector).Return(mockProv, nil).AnyTimes()

	h := NewCharacterHandlers(mockFac)
	r := charRouterWithGenerate(uid, h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/generate", map[string]any{
		"name":       "测试调查员",
		"gender":     "男",
		"occupation": "医生",
	}))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGenerateCharacter_NoProvider_Fallback(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)

	ctrl := gomock.NewController(t)
	mockFac := mocks.NewMockCharacterLLMFactory(ctrl)
	mockFac.EXPECT().LoadProvider(models.AgentRoleWriter).Return(nil, errors.New("no provider"))
	mockFac.EXPECT().LoadProvider(models.AgentRoleDirector).Return(nil, nil).AnyTimes()

	h := NewCharacterHandlers(mockFac)
	r := charRouterWithGenerate(uid, h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/generate", map[string]any{
		"name":   "测试调查员",
		"gender": "男",
	}))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGenerateCharacter_SlotFull(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 1)
	seedCard(t, uid, "Existing")

	ctrl := gomock.NewController(t)
	h := NewCharacterHandlers(mocks.NewMockCharacterLLMFactory(ctrl)) // no EXPECT: won't be called
	r := charRouterWithGenerate(uid, h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/generate", map[string]any{}))

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403 (slot full), got %d", w.Code)
	}
}
