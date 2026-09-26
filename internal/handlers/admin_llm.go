// NOTE: Package handlers implements the HTTP request handlers for the application's REST API.
package handlers

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// AdminGetLLMStats handles GET /admin/llm/stats.
// 返回所有 LLM 调用的延迟统计快照：整体聚合，以及按角色、按模型拆分的聚合。
// 与 Provider ping（/admin/config/providers/:id/ping）返回的单次探活 latency_ms
// 是两回事：这里是持续累积的历史平均值。
func AdminGetLLMStats(c *gin.Context) {
	c.JSON(http.StatusOK, llm.Stats())
}

// AdminResetLLMStats handles DELETE /admin/llm/stats.
// 清空内存中的延迟统计并删除已落库的历史数据。
func AdminResetLLMStats(c *gin.Context) {
	llm.ResetStats()
	c.JSON(http.StatusOK, gin.H{"message": "LLM 延迟统计已清空"})
}

// sessionContextAgentView 是单个 (session_id, agent_key) 的 ContextManager transcript
// 概览，供后台核对某个具体会话的 prompt cache 前缀设计是否在实际调用中生效。
type sessionContextAgentView struct {
	AgentKey       string                 `json:"agent_key"`
	Turns          int                    `json:"turns"`
	NextSeq        int                    `json:"next_seq"`
	EstimatedRunes int64                  `json:"estimated_runes"`
	RecentStats    []models.TurnCacheStat `json:"recent_stats"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

// transcriptRuneCount 估算已提交历史轮的总字符数(含工具调用参数)，口径与
// internal/services/agent/context_transcript.go 的 chatMessagesRuneCount 一致，
// handlers 包不依赖 agent 包，这里单独算一份。
func transcriptRuneCount(turns []models.TranscriptTurn) int64 {
	var n int64
	for _, t := range turns {
		for _, m := range t.Messages {
			n += int64(len([]rune(m.Content)))
			for _, tc := range m.ToolCalls {
				n += int64(len([]rune(tc.Arguments)))
			}
		}
	}
	return n
}

// AdminGetSessionContext handles GET /admin/sessions/:id/context。
// 返回该会话下每个 agent(director/writer/dramaturg/npc:<name>)的 ContextManager
// transcript 概览：已提交轮数、估算字符数、最近若干条逐轮缓存命中统计。
func AdminGetSessionContext(c *gin.Context) {
	sid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的会话ID"})
		return
	}

	var recs []models.AgentTranscript
	if err := models.DB.Where("session_id = ?", sid).Find(&recs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询会话上下文失败"})
		return
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].AgentKey < recs[j].AgentKey })

	agents := make([]sessionContextAgentView, 0, len(recs))
	for _, rec := range recs {
		data := rec.Data.Data
		agents = append(agents, sessionContextAgentView{
			AgentKey:       rec.AgentKey,
			Turns:          len(data.Turns),
			NextSeq:        data.NextSeq,
			EstimatedRunes: transcriptRuneCount(data.Turns),
			RecentStats:    data.Stats,
			UpdatedAt:      rec.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"agents": agents})
}
