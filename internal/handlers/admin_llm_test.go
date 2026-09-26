package handlers

import (
	"encoding/json"
	"fmt"
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

func adminSessionContextRouter() *gin.Engine {
	r := gin.New()
	admin := r.Group("/admin", withAuth(1, "admin", "admin"))
	admin.GET("/sessions/:id/context", AdminGetSessionContext)
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
		PromptTokens: 1000, OutputTokens: 200, CacheReadTokens: 800, CacheCreationTokens: 100,
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
	if resp.Overall.CacheReadTokens != 800 || resp.Overall.PromptTokens != 1000 {
		t.Fatalf("unexpected overall cache token stats: %+v", resp.Overall)
	}
	if resp.Overall.CacheHitRate != 0.8 {
		t.Fatalf("unexpected overall cache hit rate: got %v, want 0.8", resp.Overall.CacheHitRate)
	}
	if len(resp.ByRole) != 1 || resp.ByRole[0].Key != "writer" {
		t.Fatalf("unexpected by-role stats: %+v", resp.ByRole)
	}
	if resp.ByRole[0].CacheHitRate != 0.8 {
		t.Fatalf("unexpected by-role cache hit rate: got %v, want 0.8", resp.ByRole[0].CacheHitRate)
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

func TestAdminGetSessionContext_ReturnsAgentTranscriptSummary(t *testing.T) {
	initTestDB(t)
	const sessionID = 42

	data := models.TranscriptData{
		NextSeq: 2,
		Turns: []models.TranscriptTurn{
			{Seq: 0, Round: 1, Messages: []models.TranscriptMsg{{Role: "user", Content: "第一轮"}}},
			{Seq: 1, Round: 2, Messages: []models.TranscriptMsg{{Role: "user", Content: "第二轮内容更长一些"}}},
		},
		Stats: []models.TurnCacheStat{
			{Seq: 0, Round: 1, Calls: 1, PromptTokens: 100, CacheReadTokens: 0},
			{Seq: 1, Round: 2, Calls: 1, PromptTokens: 200, CacheReadTokens: 190},
		},
	}
	if err := models.DB.Create(&models.AgentTranscript{
		SessionID: sessionID, AgentKey: "director", Version: 1,
		Data: models.JSONField[models.TranscriptData]{Data: data},
	}).Error; err != nil {
		t.Fatalf("seed agent transcript: %v", err)
	}

	r := adminSessionContextRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/admin/sessions/%d/context", sessionID), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Agents []struct {
			AgentKey       string                 `json:"agent_key"`
			Turns          int                    `json:"turns"`
			NextSeq        int                    `json:"next_seq"`
			EstimatedRunes int64                  `json:"estimated_runes"`
			RecentStats    []models.TurnCacheStat `json:"recent_stats"`
		} `json:"agents"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Agents) != 1 {
		t.Fatalf("want 1 agent, got %d: %+v", len(resp.Agents), resp.Agents)
	}
	agent := resp.Agents[0]
	if agent.AgentKey != "director" || agent.Turns != 2 || agent.NextSeq != 2 {
		t.Fatalf("unexpected agent summary: %+v", agent)
	}
	wantRunes := int64(len([]rune("第一轮")) + len([]rune("第二轮内容更长一些")))
	if agent.EstimatedRunes != wantRunes {
		t.Fatalf("estimated runes = %d, want %d", agent.EstimatedRunes, wantRunes)
	}
	if len(agent.RecentStats) != 2 || agent.RecentStats[1].CacheReadTokens != 190 {
		t.Fatalf("unexpected recent stats: %+v", agent.RecentStats)
	}
}

func TestAdminGetSessionContext_NoTranscriptReturnsEmptyList(t *testing.T) {
	initTestDB(t)

	r := adminSessionContextRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/sessions/9999/context", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Agents []any `json:"agents"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Agents) != 0 {
		t.Fatalf("want empty agents, got %+v", resp.Agents)
	}
}

func TestAdminGetSessionContext_InvalidIDReturnsBadRequest(t *testing.T) {
	initTestDB(t)

	r := adminSessionContextRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/sessions/not-a-number/context", nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}
