// NOTE: game_time_backfill.go 在老库升级时给进行中的房间补上剧中时间。
// 旧版时间由 TurnRound(每轮30分钟)和模组开局槽位算出，这里按同一公式折算成 GameTime；
// 只处理 playing 房间：lobby 在开局时赋值，ended 不再需要。模组开局时间为空(setting 里没有日期)时跳过，
// 房间的时间保持未设定，由 KP 在下一个周期里用 advance_time 设定。
package models

import (
	"time"

	"github.com/llmcoc/server/internal/logging"
)

const legacyRoundMinutes = 30

func backfillGameTime() {
	log := logging.For("models")
	var sessions []GameSession
	if err := DB.Preload("Scenario").Where("status = ?", SessionStatusPlaying).Find(&sessions).Error; err != nil {
		log.Warn("backfill game time query failed", "error", err)
		return
	}
	updated := 0
	for _, s := range sessions {
		start := s.Scenario.Content.Data.GameStartTime
		if start == "" {
			continue
		}
		base, err := ParseGameTime(start)
		if err != nil {
			log.Warn("backfill game time: bad scenario start time", "session", s.ID, "start", start, "error", err)
			continue
		}
		round := s.TurnRound
		if round < 1 {
			round = 1
		}
		now := base.Add(time.Duration(round-1) * legacyRoundMinutes * time.Minute).Format(GameTimeLayout)
		err = DB.Model(&GameSession{}).Where("id = ?", s.ID).
			UpdateColumns(map[string]any{"game_start_time": start, "game_time": now}).Error
		if err != nil {
			log.Warn("backfill game time failed", "session", s.ID, "error", err)
			continue
		}
		updated++
	}
	if updated > 0 {
		log.Info("game time backfilled", "sessions", updated)
	}
}
