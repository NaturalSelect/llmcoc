package handlers

// NOTE: 模组物理回收。管理员删除模组只是软删除（is_active=false），正文和生成记录（含故事全文）一直占着库空间。
// 软删除的模组一旦没有任何房间引用就连同生成记录物理删除。没有保留期：除内置调试模组每次启动会被恢复外，软删除后没有任何入口能恢复模组。
// SQLite 删除后空间留在库文件内部复用，文件本身不会变小。

import (
	"errors"

	"github.com/llmcoc/server/internal/models"
	"gorm.io/gorm"
)

var errScenarioPurgeSkipped = errors.New("scenario no longer eligible for purge")

// NOTE: 房间表对模组有外键，且房间没有软删除状态，只有被回收才会消失，所以「无房间引用」就是「关联房间都已删除」。
const unusedScenarioCond = "is_active = ? AND NOT EXISTS (SELECT 1 FROM game_sessions s WHERE s.scenario_id = scenarios.id)"

// purgeUnusedScenarios 返回删除的模组数。
func purgeUnusedScenarios() (int, error) {
	var ids []uint
	if err := models.DB.Model(&models.Scenario{}).
		Where(unusedScenarioCond, false).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}

	purged := 0
	for _, id := range ids {
		err := models.DB.Transaction(func(tx *gorm.DB) error {
			return deleteUnusedScenario(tx, id)
		})
		if errors.Is(err, errScenarioPurgeSkipped) {
			continue
		}
		if err != nil {
			purgeLog.Error("purge scenario failed", "scenario_id", id, "err", err)
			continue
		}
		purged++
	}
	return purged, nil
}

// NOTE: 新增用 scenario_id 引用模组的表必须在这里处理，开着外键时漏掉会挡住删除。
// 先清依赖再做带条件的 DELETE：第一条写语句拿到写锁后，最后的条件判断就是提交时的真实状态，
// 期间有人用这个模组开了房间则整个事务回滚。线上模组的记录保留，只清掉指向已删模组的引用，之后可以重新编译。
func deleteUnusedScenario(tx *gorm.DB, id uint) error {
	if err := tx.Where("scenario_id = ?", id).Delete(&models.ScenarioGenerationLog{}).Error; err != nil {
		return err
	}
	// NOTE: 用 UpdateColumns 不刷新 updated_at：线上模组靠它判断下载是否超时。
	if err := tx.Model(&models.OnlineModule{}).
		Where("scenario_id = ?", id).
		UpdateColumns(map[string]any{"scenario_id": nil}).Error; err != nil {
		return err
	}
	res := tx.Where("id = ?", id).
		Where(unusedScenarioCond, false).
		Delete(&models.Scenario{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errScenarioPurgeSkipped
	}
	return nil
}
