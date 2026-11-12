package models

import (
	"encoding/json"
	"testing"
)

func TestBackfillGameTime(t *testing.T) {
	db := openSeedTestDB(t)
	if err := db.AutoMigrate(&User{}, &Scenario{}, &GameSession{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}

	newScenario := func(name, content string) uint {
		t.Helper()
		s := Scenario{Name: name, Data: JSONField[json.RawMessage]{Data: json.RawMessage(content)}}
		if err := db.Create(&s).Error; err != nil {
			t.Fatalf("create scenario %s: %v", name, err)
		}
		return s.ID
	}
	newSession := func(name string, scenarioID uint, status SessionStatus, round int) uint {
		t.Helper()
		s := GameSession{Name: name, ScenarioID: scenarioID, Status: status, CreatedBy: 1, TurnRound: round}
		if err := db.Create(&s).Error; err != nil {
			t.Fatalf("create session %s: %v", name, err)
		}
		return s.ID
	}

	dated := newScenario("有日期", `{"content":{"setting":"1923年10月15日，阿卡姆","game_start_slot":36}}`)
	undated := newScenario("无日期", `{"content":{"setting":"阿卡姆的雨夜","game_start_slot":36}}`)

	// NOTE: 第1轮等于开局时刻，之后每轮加 30 分钟：第5轮是 +2 小时，第13轮是 +6 小时。
	round1 := newSession("第1轮", dated, SessionStatusPlaying, 1)
	round5 := newSession("第5轮", dated, SessionStatusPlaying, 5)
	crossDay := newSession("跨日", dated, SessionStatusPlaying, 13)
	noDate := newSession("无日期房间", undated, SessionStatusPlaying, 3)
	lobby := newSession("大厅", dated, SessionStatusLobby, 1)
	ended := newSession("已结束", dated, SessionStatusEnded, 9)

	backfillGameTime()

	type times struct{ start, now string }
	read := func(id uint) times {
		var s GameSession
		if err := db.First(&s, id).Error; err != nil {
			t.Fatalf("load session %d: %v", id, err)
		}
		return times{s.GameStartTime, s.GameTime}
	}
	cases := []struct {
		name string
		id   uint
		want times
	}{
		{"第1轮", round1, times{"1923-10-15T18:00", "1923-10-15T18:00"}},
		{"第5轮", round5, times{"1923-10-15T18:00", "1923-10-15T20:00"}},
		{"跨日", crossDay, times{"1923-10-15T18:00", "1923-10-16T00:00"}},
		{"无日期房间保持未设定", noDate, times{}},
		{"大厅不回填", lobby, times{}},
		{"已结束不回填", ended, times{}},
	}
	for _, c := range cases {
		if got := read(c.id); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}
