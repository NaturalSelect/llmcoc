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

func scenarioRouter(userID uint, isAdmin bool) *gin.Engine {
	role := "user"
	if isAdmin {
		role = "admin"
	}
	r := gin.New()
	auth := withAuth(userID, "tester", role)
	r.GET("/scenarios", auth, ListScenarios)
	r.GET("/scenarios/:id", auth, GetScenario)
	r.POST("/scenarios", auth, CreateScenario)
	r.POST("/scenarios/compile-story", auth, CompileStoryByUpload)
	return r
}

func TestListScenarios_Empty(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	r := scenarioRouter(uid, false)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/scenarios", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp ScenarioListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("want empty, got %d", len(resp.Items))
	}
	if resp.Total != 0 {
		t.Errorf("total = %d, want 0", resp.Total)
	}
	if resp.Page != 1 || resp.PageSize != 20 || resp.TotalPages != 1 {
		t.Errorf("pagination = page %d size %d totalPages %d, want 1/20/1", resp.Page, resp.PageSize, resp.TotalPages)
	}
}

func TestListScenarios_Active(t *testing.T) {
	initTestDB(t)
	seedScenario(t, "Active Scenario")
	// Create an inactive scenario (GORM skips false for default:true columns, so use Exec).
	inactiveID := seedScenario(t, "Inactive")
	models.DB.Exec("UPDATE scenarios SET is_active = false WHERE id = ?", inactiveID)

	uid := seedUser(t, "u", "user", 0, 3)
	r := scenarioRouter(uid, false)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/scenarios", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp ScenarioListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Errorf("want 1 active scenario, got %d", len(resp.Items))
	}
	if resp.Total != 1 {
		t.Errorf("total = %d, want 1", resp.Total)
	}
}

func TestListScenarios_PaginatesSecondPage(t *testing.T) {
	initTestDB(t)
	for i := 1; i <= 25; i++ {
		seedScenario(t, fmt.Sprintf("Scenario %02d", i))
	}
	uid := seedUser(t, "u", "user", 0, 3)
	r := scenarioRouter(uid, false)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/scenarios?page=2&page_size=20", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp ScenarioListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 5 {
		t.Errorf("want 5 items, got %d", len(resp.Items))
	}
	if resp.Total != 25 {
		t.Errorf("total = %d, want 25", resp.Total)
	}
	if resp.Page != 2 || resp.PageSize != 20 || resp.TotalPages != 2 {
		t.Errorf("pagination = page %d size %d totalPages %d, want 2/20/2", resp.Page, resp.PageSize, resp.TotalPages)
	}
}

func TestGetScenario_Found(t *testing.T) {
	initTestDB(t)
	sid := seedScenario(t, "The Haunted House")
	uid := seedUser(t, "u", "user", 0, 3)
	r := scenarioRouter(uid, false)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/scenarios/%d", sid), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["name"] != "The Haunted House" {
		t.Errorf("name = %v, want 'The Haunted House'", resp["name"])
	}
}

func TestGetScenario_NotFound(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	r := scenarioRouter(uid, false)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/scenarios/9999", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestCreateScenario_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "admin", "admin", 0, 3)
	r := scenarioRouter(uid, true)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/scenarios", map[string]any{
		"name":    "Dark Shadows",
		"content": map[string]any{},
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["name"] != "Dark Shadows" {
		t.Errorf("name = %v, want 'Dark Shadows'", resp["name"])
	}
	// Defaults should be applied.
	if resp["difficulty"] != "normal" {
		t.Errorf("difficulty = %v, want 'normal'", resp["difficulty"])
	}
}

func TestCreateScenario_InvalidBody(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "admin", "admin", 0, 3)
	r := scenarioRouter(uid, true)

	w := httptest.NewRecorder()
	// Missing required "name" field.
	r.ServeHTTP(w, jsonReq("POST", "/scenarios", map[string]any{
		"content": map[string]any{},
	}))

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// TestCompileStoryByUpload_MissingStoryDocument 验证故事全文缺失时直接返回 400，
// 不会进入编译流水线（因此不依赖 LLM/agent 配置）。
func TestCompileStoryByUpload_MissingStoryDocument(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "admin", "admin", 0, 3)
	r := scenarioRouter(uid, true)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/scenarios/compile-story", map[string]any{
		"name": "测试模组",
	}))

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}
