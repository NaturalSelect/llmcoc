package handlers

import (
	"errors"
	"testing"
	"time"

	"github.com/llmcoc/server/internal/models"
	"gorm.io/gorm"
)

type purgeFixture struct {
	userID     uint
	cardID     uint
	scenarioID uint
}

// NOTE: initTestDB 的内存库默认不开外键；回收的删除顺序错了只有开着外键才会暴露，所以这里显式打开（测试库是单连接，PRAGMA 会生效）。
func newPurgeFixture(t *testing.T) purgeFixture {
	t.Helper()
	initTestDB(t)
	if err := models.DB.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	userID := seedUser(t, "purge_user", models.RoleUser, 0, 3)
	return purgeFixture{
		userID:     userID,
		cardID:     seedCard(t, userID, "调查员"),
		scenarioID: seedScenario(t, "模组"),
	}
}

// seedPurgeSession 建一个房间并给每张引用 session_id 的子表（收藏除外）各插一行，updated_at 改成 age 之前。
func (f purgeFixture) seedPurgeSession(t *testing.T, status models.SessionStatus, age time.Duration) uint {
	t.Helper()
	s := models.GameSession{Name: "房间", ScenarioID: f.scenarioID, Status: status, CreatedBy: f.userID}
	if err := models.DB.Create(&s).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	// NOTE: UpdateColumn 不会自动刷新 updated_at；时间保持本地时间，与 GORM 写入的格式一致。
	if err := models.DB.Model(&models.GameSession{}).Where("id = ?", s.ID).
		UpdateColumn("updated_at", time.Now().Add(-age)).Error; err != nil {
		t.Fatalf("age session: %v", err)
	}
	rows := []any{
		&models.Message{SessionID: s.ID, Role: models.MessageRoleSystem, Kind: models.MessageKindSystem, Status: models.MessageStatusDone, Content: "x"},
		&models.SessionPlayer{SessionID: s.ID, UserID: f.userID, CharacterCardID: f.cardID, JoinedAt: time.Now()},
		&models.SessionNPC{SessionID: s.ID, Name: "NPC"},
		&models.SessionNPCMemory{SessionID: s.ID, Name: "NPC"},
		&models.SessionTurnAction{SessionID: s.ID, Round: 1, UserID: f.userID},
		&models.AgentTranscript{SessionID: s.ID, AgentKey: "director"},
		&models.GameEvaluation{SessionID: s.ID},
	}
	for _, row := range rows {
		if err := models.DB.Create(row).Error; err != nil {
			t.Fatalf("seed child %T: %v", row, err)
		}
	}
	return s.ID
}

func (f purgeFixture) favorite(t *testing.T, sessionID uint) {
	t.Helper()
	if err := models.DB.Create(&models.SessionFavorite{UserID: f.userID, SessionID: sessionID}).Error; err != nil {
		t.Fatalf("favorite: %v", err)
	}
}

func sessionExists(t *testing.T, id uint) bool {
	t.Helper()
	var n int64
	if err := models.DB.Model(&models.GameSession{}).Where("id = ?", id).Count(&n).Error; err != nil {
		t.Fatalf("count session: %v", err)
	}
	return n > 0
}

// childRowCount 统计所有引用该房间的子表行数（含收藏）。
func childRowCount(t *testing.T, id uint) int64 {
	t.Helper()
	var total int64
	for _, table := range append([]any{&models.SessionFavorite{}}, sessionChildTables...) {
		var n int64
		if err := models.DB.Model(table).Where("session_id = ?", id).Count(&n).Error; err != nil {
			t.Fatalf("count %T: %v", table, err)
		}
		total += n
	}
	return total
}

func TestPurgeExpiredSessions_DeletesEndedSessionWithAllChildren(t *testing.T) {
	f := newPurgeFixture(t)
	old := f.seedPurgeSession(t, models.SessionStatusEnded, 40*24*time.Hour)
	other := f.seedPurgeSession(t, models.SessionStatusPlaying, 40*24*time.Hour)
	wantOther := childRowCount(t, other)

	n, err := purgeExpiredSessions(time.Now())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged = %d, want 1", n)
	}
	if sessionExists(t, old) {
		t.Error("expired ended session should be deleted")
	}
	if got := childRowCount(t, old); got != 0 {
		t.Errorf("child rows left behind = %d, want 0", got)
	}
	if !sessionExists(t, other) || childRowCount(t, other) != wantOther {
		t.Error("an unrelated session and its rows must stay untouched")
	}
	for name, model := range map[string]any{"user": &models.User{}, "card": &models.CharacterCard{}, "scenario": &models.Scenario{}} {
		var c int64
		models.DB.Model(model).Count(&c)
		if c == 0 {
			t.Errorf("%s must not be deleted along with the session", name)
		}
	}
}

func TestPurgeExpiredSessions_KeepsFavorited(t *testing.T) {
	f := newPurgeFixture(t)
	id := f.seedPurgeSession(t, models.SessionStatusEnded, 40*24*time.Hour)
	f.favorite(t, id)
	before := childRowCount(t, id)

	n, err := purgeExpiredSessions(time.Now())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 0 || !sessionExists(t, id) {
		t.Fatalf("favorited session must be kept, purged=%d", n)
	}
	if got := childRowCount(t, id); got != before {
		t.Errorf("child rows = %d, want %d untouched", got, before)
	}
}

func TestPurgeExpiredSessions_KeepsRecentlyEnded(t *testing.T) {
	f := newPurgeFixture(t)
	id := f.seedPurgeSession(t, models.SessionStatusEnded, 10*24*time.Hour)

	if n, err := purgeExpiredSessions(time.Now()); err != nil || n != 0 || !sessionExists(t, id) {
		t.Fatalf("session ended 10 days ago must be kept, purged=%d err=%v", n, err)
	}
}

func TestPurgeExpiredSessions_KeepsUnfinishedSessions(t *testing.T) {
	f := newPurgeFixture(t)
	lobby := f.seedPurgeSession(t, models.SessionStatusLobby, 40*24*time.Hour)
	playing := f.seedPurgeSession(t, models.SessionStatusPlaying, 40*24*time.Hour)

	n, err := purgeExpiredSessions(time.Now())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 0 || !sessionExists(t, lobby) || !sessionExists(t, playing) {
		t.Fatalf("lobby and playing sessions must be kept, purged=%d", n)
	}
}

func TestPurgeExpiredSessions_RetentionZeroDisablesPurge(t *testing.T) {
	f := newPurgeFixture(t)
	id := f.seedPurgeSession(t, models.SessionStatusEnded, 400*24*time.Hour)
	if err := models.SetSiteSetting("session_retention_days", "0"); err != nil {
		t.Fatalf("SetSiteSetting: %v", err)
	}

	if n, err := purgeExpiredSessions(time.Now()); err != nil || n != 0 || !sessionExists(t, id) {
		t.Fatalf("retention 0 must disable purge, purged=%d err=%v", n, err)
	}
}

func TestPurgeExpiredSessions_HonorsConfiguredRetention(t *testing.T) {
	f := newPurgeFixture(t)
	id := f.seedPurgeSession(t, models.SessionStatusEnded, 10*24*time.Hour)
	if err := models.SetSiteSetting("session_retention_days", "7"); err != nil {
		t.Fatalf("SetSiteSetting: %v", err)
	}

	if n, err := purgeExpiredSessions(time.Now()); err != nil || n != 1 || sessionExists(t, id) {
		t.Fatalf("10-day-old session must be purged with 7-day retention, purged=%d err=%v", n, err)
	}
}

func TestPurgeExpiredSessions_SkipsSessionWithActiveFloor(t *testing.T) {
	f := newPurgeFixture(t)
	id := f.seedPurgeSession(t, models.SessionStatusEnded, 40*24*time.Hour)
	af := &activeFloor{sessionID: id, cancel: func(error) {}, done: make(chan struct{})}
	registerFloor(af)
	t.Cleanup(func() { unregisterFloor(af) })

	if n, err := purgeExpiredSessions(time.Now()); err != nil || n != 0 || !sessionExists(t, id) {
		t.Fatalf("session with a running floor must be skipped, purged=%d err=%v", n, err)
	}
}

func TestDeleteExpiredSession_RollsBackWhenFavoritedMeanwhile(t *testing.T) {
	f := newPurgeFixture(t)
	id := f.seedPurgeSession(t, models.SessionStatusEnded, 40*24*time.Hour)
	f.favorite(t, id)
	before := childRowCount(t, id)

	err := models.DB.Transaction(func(tx *gorm.DB) error {
		return deleteExpiredSession(tx, id, time.Now().Add(-30*24*time.Hour))
	})
	if !errors.Is(err, errPurgeSkipped) {
		t.Fatalf("err = %v, want errPurgeSkipped", err)
	}
	if !sessionExists(t, id) {
		t.Error("session must survive")
	}
	if got := childRowCount(t, id); got != before {
		t.Errorf("child rows = %d, want %d: skipped purge must roll back child deletes", got, before)
	}
}
