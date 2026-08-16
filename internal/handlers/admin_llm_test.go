package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func adminLLMRouter() *gin.Engine {
	r := gin.New()
	admin := r.Group("/admin/llm", withAuth(1, "admin", "admin"))
	admin.GET("/stats", AdminGetLLMStats)
	admin.DELETE("/stats", AdminResetLLMStats)
	return r
}

func TestAdminGetLLMStats_Empty(t *testing.T) {
	initTestDB(t)
	llm.ResetStats()
	r := adminLLMRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/llm/stats", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp llm.StatsResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Overall.Count != 0 {
		t.Fatalf("want empty overall count, got %d", resp.Overall.Count)
	}
}

func TestAdminGetLLMStats_ReflectsPersistedRows(t *testing.T) {
	initTestDB(t)
	llm.ResetStats()
	t.Cleanup(llm.ResetStats)

	if err := models.DB.Create(&models.LLMLatencyStat{
		Role: "writer", Model: "gpt-4o", Method: "chat",
		Count: 4, SumMs: 800, ErrCount: 1, MaxMs: 300,
	}).Error; err != nil {
		t.Fatalf("seed latency stat: %v", err)
	}
	llm.LoadStats()

	r := adminLLMRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/llm/stats", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp llm.StatsResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Overall.Count != 4 || resp.Overall.AvgMs != 200 || resp.Overall.ErrCount != 1 {
		t.Fatalf("unexpected overall stats: %+v", resp.Overall)
	}
	if len(resp.ByRole) != 1 || resp.ByRole[0].Key != "writer" {
		t.Fatalf("unexpected by-role stats: %+v", resp.ByRole)
	}
}

func TestAdminResetLLMStats_ClearsMemoryAndDB(t *testing.T) {
	initTestDB(t)
	llm.ResetStats()

	if err := models.DB.Create(&models.LLMLatencyStat{
		Role: "director", Model: "gpt-4o", Method: "chat", Count: 2, SumMs: 100,
	}).Error; err != nil {
		t.Fatalf("seed latency stat: %v", err)
	}
	llm.LoadStats()

	r := adminLLMRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/admin/llm/stats", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := llm.Stats().Overall.Count; got != 0 {
		t.Fatalf("memory stats after reset = %d, want 0", got)
	}
	var count int64
	models.DB.Model(&models.LLMLatencyStat{}).Count(&count)
	if count != 0 {
		t.Fatalf("db rows after reset = %d, want 0", count)
	}
}
