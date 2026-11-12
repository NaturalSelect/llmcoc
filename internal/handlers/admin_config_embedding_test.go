package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/handlers/mocks"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"go.uber.org/mock/gomock"
)

type fakeEmbeddingProvider struct {
	vectors    [][]float32
	err        error
	called     bool
	texts      []string
	chatCalled bool
}

func (p *fakeEmbeddingProvider) Chat(ctx context.Context, cacheKey string, messages []llm.ChatMessage) (string, error) {
	p.chatCalled = true
	return "pong", nil
}

func (p *fakeEmbeddingProvider) ChatStream(ctx context.Context, cacheKey string, messages []llm.ChatMessage, _ []llm.ToolDefinition) (<-chan string, <-chan error, error) {
	return nil, nil, nil
}

func (p *fakeEmbeddingProvider) JsonChat(ctx context.Context, cacheKey string, messages []llm.ChatMessage) (string, error) {
	return "{}", nil
}

func (p *fakeEmbeddingProvider) ChatWithTools(ctx context.Context, cacheKey string, messages []llm.ChatMessage, tools []llm.ToolDefinition) (llm.ToolChatResult, error) {
	return llm.ToolChatResult{}, nil
}

func (p *fakeEmbeddingProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	p.called = true
	p.texts = texts
	return p.vectors, p.err
}

var _ llm.Provider = (*fakeEmbeddingProvider)(nil)
var _ llm.Embedder = (*fakeEmbeddingProvider)(nil)

func pingEmbeddingRouter(fac ProviderFactory) *gin.Engine {
	r := gin.New()
	r.POST("/admin/config/providers/:id/ping", withAuth(1, "admin", "admin"), func(c *gin.Context) {
		adminPingProviderWithFactory(c, fac)
	})
	return r
}

func TestAdminUpdateAgent_EmbeddingRoleAccepted(t *testing.T) {
	initTestDB(t)
	r := adminConfigRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/agents/embedding", map[string]any{
		"model_name": "text-embedding-3-small",
		"is_active":  true,
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT embedding want 200, got %d: %s", w.Code, w.Body.String())
	}

	var cfg models.AgentConfig
	if err := models.DB.Where("role = ?", "embedding").First(&cfg).Error; err != nil {
		t.Fatalf("embedding AgentConfig not found: %v", err)
	}
	if cfg.ModelName != "text-embedding-3-small" || !cfg.IsActive {
		t.Fatalf("stored config = %+v", cfg)
	}
}

func TestAdminPingProvider_EmbeddingSuccess(t *testing.T) {
	initTestDB(t)
	pid := seedProvider(t, "Embedder")

	ctrl := gomock.NewController(t)
	prov := &fakeEmbeddingProvider{vectors: [][]float32{{0.6, 0.8, 0}}}
	mockFac := mocks.NewMockProviderFactory(ctrl)
	mockFac.EXPECT().NewProvider(gomock.Any(), "text-embedding-3-small", 0, float32(0), false, "none", 0, false).Return(prov)

	w := httptest.NewRecorder()
	pingEmbeddingRouter(mockFac).ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/admin/config/providers/%d/ping", pid), map[string]any{
		"model_name": "text-embedding-3-small",
		"role":       "embedding",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !prov.called || len(prov.texts) != 1 {
		t.Fatalf("Embed must be called once, called=%v texts=%v", prov.called, prov.texts)
	}
	if prov.chatCalled {
		t.Fatal("Chat must not be called for embedding ping")
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["ok"] != true || resp["mode"] != "embedding" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if resp["dimensions"] != float64(3) {
		t.Fatalf("dimensions = %v, want 3", resp["dimensions"])
	}
	if _, ok := resp["latency_ms"]; !ok {
		t.Fatal("response must include latency_ms")
	}
}

func TestAdminPingProvider_EmbeddingModeWithoutRole(t *testing.T) {
	initTestDB(t)
	pid := seedProvider(t, "Embedder")

	ctrl := gomock.NewController(t)
	prov := &fakeEmbeddingProvider{vectors: [][]float32{{1, 0}}}
	mockFac := mocks.NewMockProviderFactory(ctrl)
	mockFac.EXPECT().NewProvider(gomock.Any(), "m", 0, float32(0), false, "none", 0, false).Return(prov)

	w := httptest.NewRecorder()
	pingEmbeddingRouter(mockFac).ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/admin/config/providers/%d/ping", pid), map[string]any{
		"model_name": "m",
		"mode":       "embedding",
	}))
	if w.Code != http.StatusOK || !prov.called {
		t.Fatalf("mode=embedding must route to Embed: code=%d called=%v body=%s", w.Code, prov.called, w.Body.String())
	}
}

func TestAdminPingProvider_EmbeddingRequiresModel(t *testing.T) {
	initTestDB(t)
	pid := seedProvider(t, "Embedder")

	ctrl := gomock.NewController(t)
	mockFac := mocks.NewMockProviderFactory(ctrl)

	w := httptest.NewRecorder()
	pingEmbeddingRouter(mockFac).ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/admin/config/providers/%d/ping", pid), map[string]any{
		"role": "embedding",
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "请先填写模型名称") {
		t.Fatalf("body = %s, want model hint", w.Body.String())
	}
}

func TestAdminPingProvider_EmbeddingUnsupported(t *testing.T) {
	initTestDB(t)
	pid := seedProvider(t, "TextOnly")

	ctrl := gomock.NewController(t)
	mockProv := mocks.NewMockProvider(ctrl)
	mockFac := mocks.NewMockProviderFactory(ctrl)
	mockFac.EXPECT().NewProvider(gomock.Any(), "text-embedding-3-small", 0, float32(0), false, "none", 0, false).Return(mockProv)

	w := httptest.NewRecorder()
	pingEmbeddingRouter(mockFac).ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/admin/config/providers/%d/ping", pid), map[string]any{
		"model_name": "text-embedding-3-small",
		"role":       "embedding",
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "不支持嵌入接口") {
		t.Fatalf("body = %s, want unsupported hint", w.Body.String())
	}
}

func TestAdminPingProvider_EmbeddingFailures(t *testing.T) {
	cases := []struct {
		name string
		prov *fakeEmbeddingProvider
	}{
		{"provider error", &fakeEmbeddingProvider{err: errors.New("boom")}},
		{"empty vector", &fakeEmbeddingProvider{vectors: [][]float32{{}}}},
		{"no vectors", &fakeEmbeddingProvider{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			initTestDB(t)
			pid := seedProvider(t, "Embedder")

			ctrl := gomock.NewController(t)
			mockFac := mocks.NewMockProviderFactory(ctrl)
			mockFac.EXPECT().NewProvider(gomock.Any(), "m", 0, float32(0), false, "none", 0, false).Return(tc.prov)

			w := httptest.NewRecorder()
			pingEmbeddingRouter(mockFac).ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/admin/config/providers/%d/ping", pid), map[string]any{
				"model_name": "m",
				"role":       "embedding",
			}))
			if w.Code != http.StatusBadGateway {
				t.Fatalf("want 502, got %d: %s", w.Code, w.Body.String())
			}
			var resp map[string]any
			_ = json.NewDecoder(w.Body).Decode(&resp)
			if resp["ok"] != false || resp["mode"] != "embedding" {
				t.Fatalf("unexpected response: %#v", resp)
			}
		})
	}
}
