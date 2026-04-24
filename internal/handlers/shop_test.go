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

func shopRouter(userID uint) *gin.Engine {
	r := gin.New()
	auth := withAuth(userID, "tester", "user")
	r.GET("/shop/items", auth, ListShopItems)
	r.POST("/shop/purchase", auth, PurchaseItem)
	r.GET("/shop/transactions", auth, GetMyTransactions)
	return r
}

func TestListShopItems(t *testing.T) {
	initTestDB(t)
	seedShopItem(t, "卡槽扩充", 100, models.ItemTypeCardSlot, 1)
	seedShopItem(t, "金币包", 50, models.ItemTypeCoins, 100)
	uid := seedUser(t, "buyer", "user", 0, 3)

	r := shopRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/shop/items", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp []any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 2 {
		t.Errorf("want 2 items, got %d", len(resp))
	}
}

func TestListShopItems_InactiveHidden(t *testing.T) {
	initTestDB(t)
	// Create inactive item (GORM skips false for default:true columns, so use Exec).
	inactiveID := seedShopItem(t, "Hidden", 999, models.ItemTypeCardSlot, 1)
	models.DB.Exec("UPDATE shop_items SET is_active = false WHERE id = ?", inactiveID)
	uid := seedUser(t, "buyer", "user", 0, 3)

	r := shopRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/shop/items", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp []any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 0 {
		t.Errorf("inactive item must not appear, got %d items", len(resp))
	}
}

func TestPurchaseItem_Success_CardSlot(t *testing.T) {
	initTestDB(t)
	iid := seedShopItem(t, "卡槽扩充", 100, models.ItemTypeCardSlot, 2)
	uid := seedUser(t, "buyer", "user", 200, 3)

	r := shopRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/shop/purchase", map[string]any{
		"item_id": iid,
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var u models.User
	models.DB.First(&u, uid)
	if u.Coins != 100 {
		t.Errorf("coins = %d, want 100", u.Coins)
	}
	if u.CardSlots != 5 { // 3 + 2
		t.Errorf("card_slots = %d, want 5", u.CardSlots)
	}

	var tx models.Transaction
	if err := models.DB.Where("user_id = ?", uid).First(&tx).Error; err != nil {
		t.Errorf("transaction not recorded: %v", err)
	}
}

func TestPurchaseItem_InsufficientCoins(t *testing.T) {
	initTestDB(t)
	iid := seedShopItem(t, "卡槽扩充", 100, models.ItemTypeCardSlot, 1)
	uid := seedUser(t, "poor", "user", 50, 3) // only 50 coins

	r := shopRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/shop/purchase", map[string]any{
		"item_id": iid,
	}))

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("want 402, got %d", w.Code)
	}
}

func TestPurchaseItem_NotFound(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "buyer", "user", 200, 3)

	r := shopRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/shop/purchase", map[string]any{
		"item_id": uint(9999),
	}))

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestPurchaseItem_InvalidBody(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "buyer", "user", 200, 3)

	r := shopRouter(uid)
	w := httptest.NewRecorder()
	// Missing item_id.
	r.ServeHTTP(w, jsonReq("POST", "/shop/purchase", map[string]any{}))

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestGetMyTransactions_Empty(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "buyer", "user", 0, 3)

	r := shopRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/shop/transactions", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp []any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 0 {
		t.Errorf("want empty, got %d", len(resp))
	}
}

func TestGetMyTransactions_AfterPurchase(t *testing.T) {
	initTestDB(t)
	iid := seedShopItem(t, "卡槽", 10, models.ItemTypeCardSlot, 1)
	uid := seedUser(t, "buyer", "user", 100, 3)

	// Make a purchase first.
	r := shopRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/shop/purchase", map[string]any{"item_id": iid}))
	if w.Code != http.StatusOK {
		t.Fatalf("purchase failed: %d %s", w.Code, w.Body.String())
	}

	// Now get transactions.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/shop/transactions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var txs []any
	json.NewDecoder(w.Body).Decode(&txs)
	if len(txs) != 1 {
		t.Errorf("want 1 transaction, got %d", len(txs))
	}
	_ = fmt.Sprintf("item_id=%d", iid) // prevent unused import
}
