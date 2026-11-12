package handlers

// NOTE: 已结束房间自动回收。时间基准是结束时刻（game_sessions.updated_at，结束后没有代码再改它），
// 被任意用户收藏的房间永不回收。配图文件由 imagestore 的定期清理负责，这里不处理文件。

import (
	"context"
	"errors"
	"time"

	"github.com/llmcoc/server/internal/logging"
	"github.com/llmcoc/server/internal/models"
	"gorm.io/gorm"
)

var purgeLog = logging.For("session-purge")

var errPurgeSkipped = errors.New("session no longer eligible for purge")

// NOTE: 所有用 session_id 引用房间的表。新增这类表必须加进来，否则会留下孤儿行，开着外键时还会挡住删除。
// session_favorites 故意不在里面：房间被收藏就不能删，最后的条件删除靠它判断，先删收藏行会让这个判断失效。
var sessionChildTables = []any{
	&models.Message{},
	&models.SessionPlayer{},
	&models.SessionNPC{},
	&models.SessionNPCMemory{},
	&models.SessionTurnAction{},
	&models.AgentTranscript{},
	&models.GameEvaluation{},
}

// StartSessionPurge 启动时先回收一次，之后每 interval 回收一次，ctx 取消时退出。
func StartSessionPurge(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	go func() {
		run := func() {
			n, err := purgeExpiredSessions(time.Now())
			if err != nil {
				purgeLog.Error("session purge failed", "err", err)
			}
			if n > 0 {
				purgeLog.Info("purged expired sessions", "count", n)
			}
		}
		run()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

// purgeExpiredSessions 返回删除的房间数；保留天数 <= 0 时不做任何事。
func purgeExpiredSessions(now time.Time) (int, error) {
	days := siteSettingInt("session_retention_days", 30)
	if days <= 0 {
		return 0, nil
	}
	// NOTE: cutoff 必须保持本地时间，GORM 写 updated_at 用的是本地时间，SQLite 按字符串比较，转 UTC 会错位。
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)

	var ids []uint
	if err := models.DB.Model(&models.GameSession{}).
		Where(expiredSessionCond, models.SessionStatusEnded, cutoff).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}

	purged := 0
	for _, id := range ids {
		if getActiveFloor(id) != nil || writersDone(id) != nil {
			continue
		}
		err := models.DB.Transaction(func(tx *gorm.DB) error {
			return deleteExpiredSession(tx, id, cutoff)
		})
		if errors.Is(err, errPurgeSkipped) {
			continue
		}
		if err != nil {
			purgeLog.Error("purge session failed", "session_id", id, "err", err)
			continue
		}
		removeSessionLock(id)
		purged++
	}
	return purged, nil
}

const expiredSessionCond = "status = ? AND updated_at < ? AND NOT EXISTS (SELECT 1 FROM session_favorites f WHERE f.session_id = game_sessions.id)"

// NOTE: 先删子表再删房间，开着外键时顺序反了会被挡住。第一条 DELETE 拿到写锁之后，房间不会再被别的连接修改，
// 所以最后带条件的 DELETE 判断的就是提交时的真实状态；期间被复活或被收藏则整个事务回滚。
func deleteExpiredSession(tx *gorm.DB, id uint, cutoff time.Time) error {
	for _, table := range sessionChildTables {
		if err := tx.Where("session_id = ?", id).Delete(table).Error; err != nil {
			return err
		}
	}
	res := tx.Where("id = ?", id).
		Where(expiredSessionCond, models.SessionStatusEnded, cutoff).
		Delete(&models.GameSession{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errPurgeSkipped
	}
	return nil
}
