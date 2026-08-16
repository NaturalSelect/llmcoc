// NOTE: Package handlers implements the HTTP request handlers for the application's REST API.
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
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
