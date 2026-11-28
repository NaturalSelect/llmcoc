package handlers

// NOTE: 分类器记录（input_guard / nsfw_route）只给管理员在游戏页看：落库后经房间 hub 推一个仅管理员可见的临时事件，
// 页面打开或重连时再用管理员接口补拉历史。记录里有发给第三方的玩家输入和公开聊天记录，所以不进任何玩家可见的接口。

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

const (
	// NOTE: 记录是排查阈值用的，不是审计数据：固定保留 14 天，没到期的随房间回收一起删。
	classifierLogRetention = 14 * 24 * time.Hour

	classifierLogDefaultLimit = 200
	classifierLogMaxLimit     = 500

	// NOTE: 挂载输入记录时只看该玩家最近这么多条未挂载的放行记录，足够覆盖重复提交。
	classifierAttachScan = 10
)

// NOTE: 记录是临时事件（seq=0，不补发）：用持久事件的话，非管理员的 seq 会出现空洞，每次分类都会让全房间的客户端重连。
// 用户 id 0 不存在，canSeeEvent 对非管理员的 0 号用户在 visibleTo 非空时一律拒绝，所以只含 0 的名单等于「仅管理员可见」。
var adminOnlyAudience = []uint{0}

// pushClassifierLog 把一条已落库的记录推给在线的管理员；同一个 id 再推一次就是更新。
func pushClassifierLog(sessionID uint, rec models.ClassifierLog) {
	hubFor(sessionID).EphemeralTo("classifier_log", gin.H{"log": rec}, adminOnlyAudience)
}

// recordClassifierLog 把一条分类记录落库并推给在线的管理员。落库失败只记日志：记录是调试信息，不能影响放行和路由。
// 调用方不能持有数据库事务，交出去之后也不能再改这条记录。
func recordClassifierLog(sessionID uint, rec *models.ClassifierLog) {
	rec.SessionID = sessionID
	if err := models.DB.Create(rec).Error; err != nil {
		chatLog.Warn("save classifier log failed", "session", sessionID, "kind", rec.Kind, "err", err)
		return
	}
	pushClassifierLog(sessionID, *rec)
}

// attachInputGuardLogs 在玩家声明落成消息之后，把每条声明对应的那次输入分类记录挂到周期上（floor 置为周期号）并推给管理员，
// 前端据此把记录并进玩家消息；没挂上的（被拦截、被同一玩家后一次提交取代、还在等别人）floor 保持 0。
// NOTE: 按提交者和原文匹配，取最近一条未挂载的放行记录。尽力而为：失败只记日志，不影响开周期；调用方不能持有事务。
func attachInputGuardLogs(sessionID uint, floor int, actions []models.SessionTurnAction) {
	for _, ta := range actions {
		var candidates []models.ClassifierLog
		err := models.DB.
			Where("session_id = ? AND kind = ? AND user_id = ? AND floor = 0 AND decision = ?",
				sessionID, models.ClassifierKindInputGuard, ta.UserID, models.ClassifierDecisionPassed).
			Order("id DESC").Limit(classifierAttachScan).Find(&candidates).Error
		if err != nil {
			chatLog.Warn("load classifier logs to attach failed", "session", sessionID, "user", ta.UserID, "err", err)
			continue
		}
		i := slices.IndexFunc(candidates, func(l models.ClassifierLog) bool {
			return l.Request.Data["player_input"] == ta.ActionSummary
		})
		if i < 0 {
			continue
		}
		rec := candidates[i]
		if err := models.DB.Model(&models.ClassifierLog{}).Where("id = ?", rec.ID).Update("floor", floor).Error; err != nil {
			chatLog.Warn("attach classifier log failed", "session", sessionID, "log", rec.ID, "err", err)
			continue
		}
		rec.Floor = floor
		pushClassifierLog(sessionID, rec)
	}
}

// AdminListClassifierLogs handles GET /admin/sessions/:id/classifier-logs?limit=N。
// 返回该房间最近 N 条（默认 200，最多 500）分类记录，按 id 从旧到新。
func AdminListClassifierLogs(c *gin.Context) {
	sid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的会话ID"})
		return
	}
	limit := classifierLogDefaultLimit
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 {
		limit = min(n, classifierLogMaxLimit)
	}

	logs := make([]models.ClassifierLog, 0, limit)
	if err := models.DB.Where("session_id = ?", sid).Order("id DESC").Limit(limit).Find(&logs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询分类记录失败"})
		return
	}
	slices.Reverse(logs)
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

// purgeExpiredClassifierLogs 删除保留期之前的记录，返回删除条数。
// NOTE: 时间保持本地时间，与 GORM 写 created_at 的格式一致，SQLite 按字符串比较。
func purgeExpiredClassifierLogs(now time.Time) (int, error) {
	res := models.DB.Where("created_at < ?", now.Add(-classifierLogRetention)).Delete(&models.ClassifierLog{})
	return int(res.RowsAffected), res.Error
}
