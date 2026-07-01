package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func charRouter(userID uint) *gin.Engine {
	r := gin.New()
	auth := withAuth(userID, "tester", "user")
	r.GET("/characters", auth, ListCharacters)
	r.POST("/characters", auth, CreateCharacter)
	r.GET("/characters/:id", auth, GetCharacter)
	r.PUT("/characters/:id", auth, UpdateCharacter)
	r.DELETE("/characters/:id", auth, DeleteCharacter)
	r.DELETE("/characters/:id/assets/:name", auth, RemoveCharacterAsset)
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
		"age":    25,
		"gender": "男",
		"assets": []map[string]any{{"name": "老宅", "category": "不动产", "note": "祖宅"}},
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp models.CharacterCard
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Assets.Data) != 1 || resp.Assets.Data[0].Name != "老宅" || resp.Assets.Data[0].Category != "不动产" {
		t.Fatalf("assets not returned: %#v", resp.Assets.Data)
	}
}

func TestCreateCharacter_RejectsClientStats(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)

	r := charRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters", map[string]any{
		"name":   "Cheater",
		"age":    25,
		"gender": "男",
		"stats":  map[string]any{"str": 90, "con": 90, "siz": 90, "dex": 90, "app": 90, "int": 90, "pow": 90, "edu": 90, "luck": 90},
	}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
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
		"name":   "New Name",
		"assets": []map[string]any{{"name": "古董怀表", "category": "随身物", "note": "父亲遗物"}},
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var card models.CharacterCard
	models.DB.First(&card, cid)
	if card.Name != "New Name" {
		t.Errorf("name = %q, want 'New Name'", card.Name)
	}
	if len(card.Assets.Data) != 1 || card.Assets.Data[0].Name != "古董怀表" || card.Assets.Data[0].Note != "父亲遗物" {
		t.Fatalf("assets not saved: %#v", card.Assets.Data)
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

// ── RemoveCharacterAsset ───────────────────────────────────────────────────────

func TestRemoveCharacterAsset_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)
	card := models.CharacterCard{
		UserID:   uid,
		Name:     "Investigator",
		IsActive: true,
		Stats:    models.JSONField[models.CharacterStats]{},
		Skills:   models.JSONField[map[string]int]{Data: map[string]int{}},
		Assets:   models.JSONField[[]models.Asset]{Data: []models.Asset{{Name: "老宅", Category: "不动产", Note: "祖宅"}, {Name: "汽车", Category: "载具", Note: "破旧"}}},
	}
	if err := models.DB.Create(&card).Error; err != nil {
		t.Fatalf("seed card: %v", err)
	}

	r := charRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", fmt.Sprintf("/characters/%d/assets/%s", card.ID, url.PathEscape("老宅")), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp models.CharacterCard
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Assets.Data) != 1 || resp.Assets.Data[0].Name != "汽车" {
		t.Fatalf("asset not removed: %#v", resp.Assets.Data)
	}
}

func TestRemoveCharacterAsset_NotFound(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", "user", 0, 3)
	r := charRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/characters/9999/assets/whatever", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestRemoveCharacterAsset_OtherCard_Forbidden(t *testing.T) {
	initTestDB(t)
	uid1 := seedUser(t, "alice", "user", 0, 3)
	uid2 := seedUser(t, "bob", "user", 0, 3)
	card := models.CharacterCard{
		UserID:   uid2,
		Name:     "Bob's",
		IsActive: true,
		Stats:    models.JSONField[models.CharacterStats]{},
		Skills:   models.JSONField[map[string]int]{Data: map[string]int{}},
		Assets:   models.JSONField[[]models.Asset]{Data: []models.Asset{{Name: "老宅"}}},
	}
	if err := models.DB.Create(&card).Error; err != nil {
		t.Fatalf("seed card: %v", err)
	}
	r := charRouter(uid1)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", fmt.Sprintf("/characters/%d/assets/%s", card.ID, url.PathEscape("老宅")), nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}
