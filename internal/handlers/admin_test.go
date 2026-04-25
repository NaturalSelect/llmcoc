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

func adminRouter() *gin.Engine {
	r := gin.New()
	admin := r.Group("/admin", withAuth(1, "admin", "admin"))
	admin.GET("/users", AdminListUsers)
	admin.POST("/recharge", AdminRechargeCoins)
	admin.PUT("/users/:id/role", AdminSetRole)
	admin.GET("/recharge/history", AdminGetRechargeHistory)
	admin.POST("/shop/items", AdminCreateShopItem)
	admin.DELETE("/shop/items/:id", AdminDeleteShopItem)
	return r
}

func TestAdminListUsers_Empty(t *testing.T) {
	initTestDB(t)
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/users", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp []any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 0 {
		t.Errorf("want empty list, got %d items", len(resp))
	}
}

func TestAdminListUsers_HasUsers(t *testing.T) {
	initTestDB(t)
	seedUser(t, "alice", "user", 0, 3)
	seedUser(t, "bob", "user", 0, 3)
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/users", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp []any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 2 {
		t.Errorf("want 2 users, got %d", len(resp))
	}
}

func TestAdminRechargeCoins_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "charlie", "user", 100, 3)

	adminID := seedUser(t, "admin1", "admin", 0, 3)
	r := gin.New()
	r.POST("/admin/recharge", withAuth(adminID, "admin1", "admin"), AdminRechargeCoins)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/recharge", map[string]any{
		"user_id": uid,
		"amount":  50,
		"note":    "test recharge",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify coins updated in DB.
	var u models.User
	models.DB.First(&u, uid)
	if u.Coins != 150 {
		t.Errorf("want 150 coins, got %d", u.Coins)
	}
}

func TestAdminRechargeCoins_UserNotFound(t *testing.T) {
	initTestDB(t)
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/recharge", map[string]any{
		"user_id": 9999,
		"amount":  10,
	}))

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestAdminRechargeCoins_InvalidBody(t *testing.T) {
	initTestDB(t)
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/recharge", map[string]any{
		"amount": -1, // missing user_id, negative amount
	}))

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestAdminSetRole_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "diana", "user", 0, 3)

	r := gin.New()
	r.PUT("/admin/users/:id/role", withAuth(1, "admin", "admin"), AdminSetRole)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", fmt.Sprintf("/admin/users/%d/role", uid), map[string]any{
		"role": "admin",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var u models.User
	models.DB.First(&u, uid)
	if u.Role != "admin" {
		t.Errorf("role = %q, want admin", u.Role)
	}
}

func TestAdminSetRole_UserNotFound(t *testing.T) {
	initTestDB(t)
	r := gin.New()
	r.PUT("/admin/users/:id/role", withAuth(1, "admin", "admin"), AdminSetRole)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/users/9999/role", map[string]any{
		"role": "admin",
	}))

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestAdminSetRole_InvalidRole(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "ed", "user", 0, 3)
	r := gin.New()
	r.PUT("/admin/users/:id/role", withAuth(1, "admin", "admin"), AdminSetRole)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", fmt.Sprintf("/admin/users/%d/role", uid), map[string]any{
		"role": "superuser", // invalid
	}))

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestAdminGetRechargeHistory(t *testing.T) {
	initTestDB(t)
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/recharge/history", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestAdminCreateShopItem_Success(t *testing.T) {
	initTestDB(t)
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/shop/items", map[string]any{
		"name":      "卡槽扩充",
		"item_type": "card_slot",
		"price":     100,
		"value":     1,
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var item models.ShopItem
	models.DB.First(&item)
	if item.Name != "卡槽扩充" {
		t.Errorf("name = %q, want 卡槽扩充", item.Name)
	}
}

func TestAdminCreateShopItem_InvalidBody(t *testing.T) {
	initTestDB(t)
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/shop/items", "not json"))

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestAdminDeleteShopItem_Success(t *testing.T) {
	initTestDB(t)
	r := adminRouter()
	itemID := seedShopItem(t, "左轮手枪（.38）", 120, models.ItemTypeWeapon, 1)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", fmt.Sprintf("/admin/shop/items/%d", itemID), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var item models.ShopItem
	models.DB.First(&item, itemID)
	if item.IsActive {
		t.Fatalf("want inactive item after delete")
	}
}

func TestAdminDeleteShopItem_NotFound(t *testing.T) {
	initTestDB(t)
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/admin/shop/items/9999", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}
