// NOTE: item_backfill.go 在启动时把旧的字符串物品列表改写成结构化 Item 数组。
// 只匹配以 `["` 开头的旧行（新格式以 `[{` 开头），可以重复执行；单行失败只记日志，
// 该行仍能靠 Item.UnmarshalJSON 的兼容解码读出，下次保存时会写成新格式。
package models

import (
	"gorm.io/gorm"

	"github.com/llmcoc/server/internal/logging"
)

const legacyItemListPattern = `["%`

func backfillLegacyItems() {
	log := logging.For("models")
	cards := backfillLegacyItemColumn(&CharacterCard{}, "inventory", func(c CharacterCard) (uint, []Item) {
		return c.ID, c.Inventory.Data
	})
	users := backfillLegacyItemColumn(&User{}, "warehouse", func(u User) (uint, []Item) {
		return u.ID, u.Warehouse.Data
	})
	if cards > 0 || users > 0 {
		log.Info("legacy items backfilled", "character_cards", cards, "users", users)
	}
}

func backfillLegacyItemColumn[M any](model *M, column string, extract func(M) (uint, []Item)) int {
	log := logging.For("models")
	updated := 0
	var batch []M
	err := DB.Model(model).
		Select("id", column).
		Where(column+" LIKE ?", legacyItemListPattern).
		FindInBatches(&batch, 100, func(tx *gorm.DB, _ int) error {
			for _, row := range batch {
				id, items := extract(row)
				merged := MergeStacks(items)
				err := DB.Model(model).Where("id = ?", id).
					UpdateColumn(column, JSONField[[]Item]{Data: merged}).Error
				if err != nil {
					log.Warn("backfill legacy items failed", "column", column, "id", id, "error", err)
					continue
				}
				updated++
			}
			return nil
		}).Error
	if err != nil {
		log.Warn("backfill legacy items query failed", "column", column, "error", err)
	}
	return updated
}
