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

func adminConfigRouter() *gin.Engine {
	r := gin.New()
	adm := r.Group("/admin/config", withAuth(1, "admin", "admin"))
	adm.GET("/providers", AdminListProviders)
	adm.POST("/providers", AdminCreateProvider)
	adm.PUT("/providers/:id", AdminUpdateProvider)
	adm.DELETE("/providers/:id", AdminDeleteProvider)
	adm.GET("/agents", AdminListAgents)
	adm.PUT("/agents/:role", AdminUpdateAgent)
	adm.POST("/providers/:id/ping", func(c *gin.Context) {
		// Default handler uses real factory; tests override via adminPingProviderWithFactory.
		AdminPingProvider(c)
	})
	return r
}

// ── Provider CRUD ─────────────────────────────────────────────────────────────

func TestAdminListProviders_Empty(t *testing.T) {
	initTestDB(t)
	r := adminConfigRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/config/providers", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp []any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 0 {
		t.Errorf("want empty, got %d", len(resp))
	}
}

func TestAdminCreateProvider_Success(t *testing.T) {
	initTestDB(t)
	r := adminConfigRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/config/providers", map[string]any{
		"name":     "OpenAI",
		"provider": "openai",
		"api_key":  "sk-test",
		"base_url": "https://api.openai.com/v1",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	// API key must NOT be returned.
	if _, ok := resp["api_key"]; ok {
		t.Error("api_key must not appear in response")
	}
	if resp["api_key_set"] != true {
		t.Errorf("api_key_set = %v, want true", resp["api_key_set"])
	}
}

func TestAdminCreateProvider_InvalidBody(t *testing.T) {
	initTestDB(t)
	r := adminConfigRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/config/providers", map[string]any{
		"provider": "unknown-provider", // invalid oneof
	}))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestAdminUpdateProvider_NotFound(t *testing.T) {
	initTestDB(t)
	r := adminConfigRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/providers/9999", map[string]any{
		"name": "New",
	}))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestAdminUpdateProvider_MaskedAPIKey(t *testing.T) {
	initTestDB(t)
	pid := seedProvider(t, "OAI")
	r := adminConfigRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", fmt.Sprintf("/admin/config/providers/%d", pid), map[string]any{
		"name":    "OAI Updated",
		"api_key": "*****", // masked placeholder → must not overwrite real key
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var p models.LLMProviderConfig
	models.DB.First(&p, pid)
	if p.APIKey != "test-key" {
		t.Errorf("api_key was overwritten; want 'test-key', got %q", p.APIKey)
	}
}

func TestAdminDeleteProvider_Success(t *testing.T) {
	initTestDB(t)
	pid := seedProvider(t, "ToDelete")
	r := adminConfigRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", fmt.Sprintf("/admin/config/providers/%d", pid), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var count int64
	models.DB.Model(&models.LLMProviderConfig{}).Count(&count)
	if count != 0 {
		t.Errorf("provider not deleted, count=%d", count)
	}
}

func TestAdminDeleteProvider_InUse(t *testing.T) {
	initTestDB(t)
	pid := seedProvider(t, "Bound")
	// Bind an agent config to the provider.
	models.DB.Create(&models.AgentConfig{
		Role:             "director",
		ProviderConfigID: &pid,
		ModelName:        "gpt-4o",
		IsActive:         true,
	})

	r := adminConfigRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", fmt.Sprintf("/admin/config/providers/%d", pid), nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 (in-use), got %d", w.Code)
	}
}

// ── Agent Config ──────────────────────────────────────────────────────────────

func TestAdminListAgents(t *testing.T) {
	initTestDB(t)
	r := adminConfigRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/config/agents", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestAdminUpdateAgent_InvalidRole(t *testing.T) {
	initTestDB(t)
	r := adminConfigRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/agents/bogus-role", map[string]any{
		"model_name": "gpt-4o",
	}))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestAdminUpdateAgent_CreateNew(t *testing.T) {
	initTestDB(t)
	r := adminConfigRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/agents/director", map[string]any{
		"model_name":         "gpt-4o",
		"max_tokens":         512,
		"temperature":        0.7,
		"system_prompt":      "You are a director.",
		"provider_config_id": "", // empty string from JS → treated as nil
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var cfg models.AgentConfig
	models.DB.Where("role = ?", "director").First(&cfg)
	if cfg.ModelName != "gpt-4o" {
		t.Errorf("model_name = %q, want gpt-4o", cfg.ModelName)
	}
	if cfg.ProviderConfigID != nil {
		t.Errorf("provider_config_id should be nil when empty string is sent")
	}
}

func TestAdminUpdateAgent_UpdateExisting(t *testing.T) {
	initTestDB(t)
	pid := seedProvider(t, "GPT")
	// Pre-create agent config.
	models.DB.Create(&models.AgentConfig{
		Role:      "writer",
		ModelName: "old-model",
		IsActive:  true,
	})

	r := adminConfigRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/agents/writer", map[string]any{
		"model_name":         "new-model",
		"provider_config_id": float64(pid),
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var cfg models.AgentConfig
	models.DB.Where("role = ?", "writer").First(&cfg)
	if cfg.ModelName != "new-model" {
		t.Errorf("model_name = %q, want new-model", cfg.ModelName)
	}
	if cfg.ProviderConfigID == nil || *cfg.ProviderConfigID != pid {
		t.Errorf("provider_config_id = %v, want %d", cfg.ProviderConfigID, pid)
	}
}

// ── Ping ──────────────────────────────────────────────────────────────────────

func TestAdminPingProvider_NotFound(t *testing.T) {
	initTestDB(t)
	r := gin.New()
	r.POST("/admin/config/providers/:id/ping", withAuth(1, "admin", "admin"), func(c *gin.Context) {
		adminPingProviderWithFactory(c, DefaultProviderFactory)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/config/providers/9999/ping", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestAdminPingProvider_Success(t *testing.T) {
	initTestDB(t)
	pid := seedProvider(t, "Pingable")

	ctrl := gomock.NewController(t)
	mockProv := mocks.NewMockProvider(ctrl)
	mockProv.EXPECT().Chat(gomock.Any(), gomock.Any()).Return("pong", nil)

	mockFac := mocks.NewMockProviderFactory(ctrl)
	mockFac.EXPECT().NewProvider(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(mockProv)

	r := gin.New()
	r.POST("/admin/config/providers/:id/ping", withAuth(1, "admin", "admin"), func(c *gin.Context) {
		adminPingProviderWithFactory(c, mockFac)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/admin/config/providers/%d/ping", pid), map[string]any{
		"model_name": "gpt-4o-mini",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("ok = %v, want true", resp["ok"])
	}
}

func TestAdminPingProvider_LLMError(t *testing.T) {
	initTestDB(t)
	pid := seedProvider(t, "Broken")

	ctrl := gomock.NewController(t)
	mockProv := mocks.NewMockProvider(ctrl)
	mockProv.EXPECT().Chat(gomock.Any(), gomock.Any()).Return("", errors.New("connection refused"))

	mockFac := mocks.NewMockProviderFactory(ctrl)
	mockFac.EXPECT().NewProvider(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(mockProv)

	r := gin.New()
	r.POST("/admin/config/providers/:id/ping", withAuth(1, "admin", "admin"), func(c *gin.Context) {
		adminPingProviderWithFactory(c, mockFac)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/admin/config/providers/%d/ping", pid), nil))

	if w.Code != http.StatusBadGateway {
		t.Errorf("want 502, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != false {
		t.Errorf("ok = %v, want false", resp["ok"])
	}
}

// compile-time check: llm.Provider is used in this file.
var _ llm.Provider = (*mocks.MockProvider)(nil)
