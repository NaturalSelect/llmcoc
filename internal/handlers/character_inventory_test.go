package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func inventoryRouter(userID uint, role string) *gin.Engine {
	r := gin.New()
	auth := withAuth(userID, "tester", role)
	r.GET("/characters/:id/inventory", auth, GetCharacterInventory)
	r.POST("/characters/:id/inventory", auth, AddCharacterInventoryItem)
	r.PUT("/characters/:id/inventory/equip", auth, SetCharacterItemEquipped)
	r.DELETE("/characters/:id/inventory/*item", auth, RemoveCharacterInventoryItem)
	return r
}

func TestAddInventoryItem_StacksAndKeepsType(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	r := inventoryRouter(uid, "user")

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq("POST", "/characters/"+itoa(cardID)+"/inventory", map[string]any{
			"item": map[string]any{"name": "左轮手枪", "desc": ".38", "count": 2, "type": "weapon"},
		}))
		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
		}
	}
	inv := cardInventory(t, cardID)
	if len(inv) != 1 || inv[0].Count != 4 || inv[0].Type != models.ItemTypeWeapon || inv[0].Desc != ".38" {
		t.Fatalf("inventory = %+v", inv)
	}
}

// TestAddInventoryItem_RejectsStoryItem 验证手动接口不能添加剧情物品，它只能由 KP 在对局里发放。
func TestAddInventoryItem_RejectsStoryItem(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	r := inventoryRouter(uid, "user")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/"+itoa(cardID)+"/inventory", map[string]any{
		"item": map[string]any{"name": "请柬", "count": 1, "type": "story"},
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 0 {
		t.Fatalf("inventory should stay empty, got %+v", inv)
	}
}

// TestAddInventoryItem_LegacyStringBody 验证旧客户端提交的整串物品仍会被解析成结构化物品。
func TestAddInventoryItem_LegacyStringBody(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	r := inventoryRouter(uid, "user")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/"+itoa(cardID)+"/inventory", map[string]any{"item": "绷带(染血, x3)"}))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	inv := cardInventory(t, cardID)
	if len(inv) != 1 || inv[0].Name != "绷带" || inv[0].Desc != "染血" || inv[0].Count != 3 {
		t.Fatalf("inventory = %+v", inv)
	}
}

func TestAddInventoryItem_EmptyName(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	r := inventoryRouter(uid, "user")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/"+itoa(cardID)+"/inventory", map[string]any{
		"item": map[string]any{"name": "  "},
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRemoveInventoryItem_ByDescAndCount(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []models.Item{stack("子弹", "", 6), stack("子弹", "穿甲", 2)})
	r := inventoryRouter(uid, "user")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/characters/"+itoa(cardID)+"/inventory/子弹?desc=%E7%A9%BF%E7%94%B2&count=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	inv := cardInventory(t, cardID)
	if len(inv) != 2 || inv[0].Count != 6 || inv[1].Count != 1 {
		t.Fatalf("after partial remove inventory = %+v", inv)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/characters/"+itoa(cardID)+"/inventory/子弹", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 1 || inv[0].Desc != "穿甲" {
		t.Fatalf("whole-stack remove without desc should only hit the plain stack, got %+v", inv)
	}
}

func TestRemoveInventoryItem_NotFound(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []models.Item{stack("手电筒", "", 1)})
	r := inventoryRouter(uid, "user")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/characters/"+itoa(cardID)+"/inventory/绷带", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 1 {
		t.Errorf("inventory should be unchanged, got %+v", inv)
	}
}

func TestInventory_OtherUsersCard_Forbidden(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	other := seedUser(t, "other", "user", 0, 3)
	cardID := seedCard(t, other, "Not Yours")
	setCardInventory(t, cardID, []models.Item{stack("手电筒", "", 1)})
	r := inventoryRouter(uid, "user")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/characters/"+itoa(cardID)+"/inventory/手电筒", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSetItemEquipped_EquipAndUnequip(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []models.Item{stack("风衣", "", 1), stack("风衣", "破旧", 1)})
	r := inventoryRouter(uid, "user")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/characters/"+itoa(cardID)+"/inventory/equip", map[string]any{
		"item":     map[string]any{"name": "风衣", "desc": "破旧"},
		"equipped": true,
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	inv := cardInventory(t, cardID)
	if len(inv) != 2 || inv[0].Equipped || !inv[1].Equipped {
		t.Fatalf("only the matching stack should be equipped, got %+v", inv)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/characters/"+itoa(cardID)+"/inventory/equip", map[string]any{
		"item":     map[string]any{"name": "风衣", "desc": "破旧"},
		"equipped": false,
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); inv[1].Equipped {
		t.Fatalf("item should be unequipped, got %+v", inv)
	}
}

func TestSetItemEquipped_NotFound(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []models.Item{stack("手电筒", "", 1)})
	r := inventoryRouter(uid, "user")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/characters/"+itoa(cardID)+"/inventory/equip", map[string]any{
		"item":     map[string]any{"name": "绷带"},
		"equipped": true,
	}))
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSetItemEquipped_EmptyName(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	r := inventoryRouter(uid, "user")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/characters/"+itoa(cardID)+"/inventory/equip", map[string]any{
		"item":     map[string]any{"name": "  "},
		"equipped": true,
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSetItemEquipped_OtherUsersCard_Forbidden(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	other := seedUser(t, "other", "user", 0, 3)
	cardID := seedCard(t, other, "Not Yours")
	setCardInventory(t, cardID, []models.Item{stack("手电筒", "", 1)})
	r := inventoryRouter(uid, "user")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/characters/"+itoa(cardID)+"/inventory/equip", map[string]any{
		"item":     map[string]any{"name": "手电筒"},
		"equipped": true,
	}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); inv[0].Equipped {
		t.Fatalf("forbidden request must not change the card, got %+v", inv)
	}
}
