package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
)

func adminRouter() *gin.Engine {
	r := gin.New()
	admin := r.Group("/admin", withAuth(1, "admin", "admin"))
	admin.GET("/users", AdminListUsers)
	admin.GET("/scenarios", AdminListScenarios)
	admin.GET("/scenarios/:id/generation-log", AdminGetScenarioGenerationLog)
	admin.POST("/recharge", AdminRechargeCoins)
	admin.PUT("/users/:id/role", AdminSetRole)
	admin.GET("/recharge/history", AdminGetRechargeHistory)
	admin.POST("/shop/items", AdminCreateShopItem)
	admin.DELETE("/shop/items/:id", AdminDeleteShopItem)
	admin.GET("/cache/entry", AdminGetCacheEntry)
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

func TestAdminListScenarios_Empty(t *testing.T) {
	initTestDB(t)
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/scenarios", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AdminScenarioListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("want empty items, got %d", len(resp.Items))
	}
	if resp.Total != 0 {
		t.Errorf("total = %d, want 0", resp.Total)
	}
	if resp.Page != 1 || resp.PageSize != 20 || resp.TotalPages != 1 {
		t.Errorf("pagination = page %d size %d totalPages %d, want 1/20/1", resp.Page, resp.PageSize, resp.TotalPages)
	}
}

func TestAdminListScenarios_PaginatesFirstPage(t *testing.T) {
	initTestDB(t)
	for i := 1; i <= 25; i++ {
		seedScenario(t, fmt.Sprintf("Scenario %02d", i))
	}
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/scenarios?page=1&page_size=10", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AdminScenarioListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 10 {
		t.Errorf("want 10 items, got %d", len(resp.Items))
	}
	if resp.Total != 25 {
		t.Errorf("total = %d, want 25", resp.Total)
	}
	if resp.TotalPages != 3 {
		t.Errorf("total_pages = %d, want 3", resp.TotalPages)
	}
	if resp.Page != 1 || resp.PageSize != 10 {
		t.Errorf("pagination = page %d size %d, want 1/10", resp.Page, resp.PageSize)
	}
}

func TestAdminListScenarios_PaginatesThirdPage(t *testing.T) {
	initTestDB(t)
	for i := 1; i <= 25; i++ {
		seedScenario(t, fmt.Sprintf("Scenario %02d", i))
	}
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/scenarios?page=3&page_size=10", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AdminScenarioListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 5 {
		t.Errorf("want 5 items, got %d", len(resp.Items))
	}
	if resp.Total != 25 {
		t.Errorf("total = %d, want 25", resp.Total)
	}
	if resp.TotalPages != 3 {
		t.Errorf("total_pages = %d, want 3", resp.TotalPages)
	}
}

func TestAdminListScenarios_ExcludesInactive(t *testing.T) {
	initTestDB(t)
	for i := 1; i <= 3; i++ {
		seedScenario(t, fmt.Sprintf("Active %d", i))
	}
	inactiveID := seedScenario(t, "Inactive")
	models.DB.Exec("UPDATE scenarios SET is_active = false WHERE id = ?", inactiveID)
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/scenarios?page=1&page_size=10", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AdminScenarioListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 3 {
		t.Errorf("want 3 active items, got %d", len(resp.Items))
	}
	if resp.Total != 3 {
		t.Errorf("total = %d, want 3", resp.Total)
	}
}

func TestAdminListScenarios_InvalidPagination(t *testing.T) {
	tests := []string{
		"/admin/scenarios?page=0",
		"/admin/scenarios?page=bad",
		"/admin/scenarios?page_size=0",
		"/admin/scenarios?page_size=bad",
		"/admin/scenarios?page_size=101",
	}

	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			initTestDB(t)
			r := adminRouter()

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonReq("GET", path, nil))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAdminGetScenarioGenerationLog_ReturnsLog(t *testing.T) {
	initTestDB(t)
	scenarioID := seedScenario(t, "Generated")
	if err := models.DB.Create(&models.ScenarioGenerationLog{
		ScenarioID:   scenarioID,
		ScenarioName: "Generated",
		LogText:      "[Architect]\nuser: prompt\nassistant: response",
	}).Error; err != nil {
		t.Fatalf("seed generation log: %v", err)
	}
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/admin/scenarios/%d/generation-log", scenarioID), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AdminScenarioGenerationLogResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.HasLog || resp.ScenarioID != scenarioID || resp.LogText == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestAdminGetScenarioGenerationLog_NoLog(t *testing.T) {
	initTestDB(t)
	scenarioID := seedScenario(t, "Manual")
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/admin/scenarios/%d/generation-log", scenarioID), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AdminScenarioGenerationLogResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.HasLog || resp.LogText != "" || resp.ScenarioID != scenarioID {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestAdminGetCacheEntry_ReturnsEntry(t *testing.T) {
	initTestDB(t)
	agent.ClearLawyerCacheAll()
	t.Cleanup(agent.ClearLawyerCacheAll)
	key := "#手枪 #伤害"
	value := "手枪伤害为1D10。"
	hashes := agent.LawyerCacheHashes{RulebookHash: "rule-hash", SpellbookHash: "spell-hash", MonsterbookHash: "monster-hash"}
	payload := map[string]any{
		"hashes":   hashes,
		"saved_at": "2026-01-01T00:00:00Z",
		"entries":  []map[string]string{{"key": key, "value": value}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal cache payload: %v", err)
	}
	path := filepath.Join(t.TempDir(), "lawyer_cache.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write cache payload: %v", err)
	}
	t.Setenv("LAWYER_CACHE_PATH", path)
	agent.LoadLawyerCache(hashes)
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/cache/entry?key="+url.QueryEscape(key), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp agent.CacheEntry
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Key != key || resp.Value != value {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if want := int64(len(key) + len(value)); resp.Size != want {
		t.Fatalf("size = %d, want %d", resp.Size, want)
	}
}

func TestAdminGetCacheEntry_MissingKey(t *testing.T) {
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/cache/entry", nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminGetCacheEntry_NotFound(t *testing.T) {
	initTestDB(t)
	agent.ClearLawyerCacheAll()
	t.Cleanup(agent.ClearLawyerCacheAll)
	r := adminRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/cache/entry?key="+url.QueryEscape("不存在"), nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
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
	itemID := seedShopItem(t, "左轮手枪(.38)", 120, models.ItemTypeWeapon, 1)

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
