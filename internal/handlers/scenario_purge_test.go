package handlers

import (
	"errors"
	"testing"
	"time"

	"github.com/llmcoc/server/internal/models"
	"gorm.io/gorm"
)

func softDeleteScenario(t *testing.T, id uint) {
	t.Helper()
	if err := models.DB.Model(&models.Scenario{}).Where("id = ?", id).Update("is_active", false).Error; err != nil {
		t.Fatalf("soft delete scenario: %v", err)
	}
}

func seedGenerationLog(t *testing.T, scenarioID uint) {
	t.Helper()
	row := models.ScenarioGenerationLog{ScenarioID: scenarioID, ScenarioName: "模组", LogText: "log", StoryDocument: "story"}
	if err := models.DB.Create(&row).Error; err != nil {
		t.Fatalf("seed generation log: %v", err)
	}
}

func scenarioExists(t *testing.T, id uint) bool {
	t.Helper()
	var n int64
	if err := models.DB.Model(&models.Scenario{}).Where("id = ?", id).Count(&n).Error; err != nil {
		t.Fatalf("count scenario: %v", err)
	}
	return n > 0
}

func generationLogCount(t *testing.T, scenarioID uint) int64 {
	t.Helper()
	var n int64
	if err := models.DB.Model(&models.ScenarioGenerationLog{}).Where("scenario_id = ?", scenarioID).Count(&n).Error; err != nil {
		t.Fatalf("count generation logs: %v", err)
	}
	return n
}

func newScenarioPurgeFixture(t *testing.T) purgeFixture {
	t.Helper()
	f := newPurgeFixture(t)
	if err := models.DB.AutoMigrate(&models.OnlineModule{}, &models.OnlineModuleDocument{}); err != nil {
		t.Fatalf("auto-migrate online module: %v", err)
	}
	return f
}

func TestPurgeUnusedScenarios_DeletesSoftDeletedWithLogs(t *testing.T) {
	f := newScenarioPurgeFixture(t)
	gone := seedScenario(t, "已删除")
	seedGenerationLog(t, gone)
	seedGenerationLog(t, gone)
	softDeleteScenario(t, gone)

	n, err := purgeUnusedScenarios()
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged = %d, want 1", n)
	}
	if scenarioExists(t, gone) || generationLogCount(t, gone) != 0 {
		t.Error("soft-deleted scenario and its generation logs should be gone")
	}
	if !scenarioExists(t, f.scenarioID) {
		t.Error("an active scenario must stay")
	}
}

func TestPurgeUnusedScenarios_KeepsActiveAndReferenced(t *testing.T) {
	f := newScenarioPurgeFixture(t)
	seedGenerationLog(t, f.scenarioID)
	referenced := seedScenario(t, "还有房间")
	seedGenerationLog(t, referenced)
	softDeleteScenario(t, referenced)
	f.seedPurgeSession(t, models.SessionStatusEnded, time.Hour)
	if err := models.DB.Model(&models.GameSession{}).Where("scenario_id = ?", f.scenarioID).
		UpdateColumn("scenario_id", referenced).Error; err != nil {
		t.Fatalf("point session at scenario: %v", err)
	}

	n, err := purgeUnusedScenarios()
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 0 {
		t.Fatalf("purged = %d, want 0", n)
	}
	if !scenarioExists(t, f.scenarioID) || generationLogCount(t, f.scenarioID) != 1 {
		t.Error("active scenario and its logs must stay")
	}
	if !scenarioExists(t, referenced) || generationLogCount(t, referenced) != 1 {
		t.Error("a soft-deleted scenario still used by a session must stay with its logs")
	}
}

func TestPurgeUnusedScenarios_KeepsOnlineModuleRecordButClearsReference(t *testing.T) {
	newScenarioPurgeFixture(t)
	gone := seedScenario(t, "已删除")
	softDeleteScenario(t, gone)
	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, func(m *models.OnlineModule) { m.ScenarioID = &gone })
	old := time.Now().Add(-time.Hour)
	models.DB.Model(&models.OnlineModule{}).Where("id = ?", m.ID).UpdateColumn("updated_at", old)

	if n, err := purgeUnusedScenarios(); err != nil || n != 1 {
		t.Fatalf("purged = %d, err = %v, want 1", n, err)
	}
	got := loadOnlineModule(t, m.ID)
	if got.ScenarioID != nil {
		t.Errorf("scenario_id = %v, want cleared so the module can be compiled again", *got.ScenarioID)
	}
	if got.Status != models.OnlineModuleStatusExtracted {
		t.Errorf("status = %q, extracted text must be kept", got.Status)
	}
	if !got.UpdatedAt.Before(time.Now().Add(-30 * time.Minute)) {
		t.Error("clearing the reference must not refresh updated_at")
	}
}

func TestDeleteUnusedScenario_RollsBackWhenSessionAppearsMeanwhile(t *testing.T) {
	f := newScenarioPurgeFixture(t)
	seedGenerationLog(t, f.scenarioID)
	f.seedPurgeSession(t, models.SessionStatusLobby, time.Hour)
	softDeleteScenario(t, f.scenarioID)
	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, func(m *models.OnlineModule) { m.ScenarioID = &f.scenarioID })

	err := models.DB.Transaction(func(tx *gorm.DB) error {
		return deleteUnusedScenario(tx, f.scenarioID)
	})
	if !errors.Is(err, errScenarioPurgeSkipped) {
		t.Fatalf("err = %v, want errScenarioPurgeSkipped", err)
	}
	if !scenarioExists(t, f.scenarioID) {
		t.Error("scenario must survive")
	}
	if generationLogCount(t, f.scenarioID) != 1 {
		t.Error("skipped purge must roll back the generation log delete")
	}
	if got := loadOnlineModule(t, m.ID); got.ScenarioID == nil {
		t.Error("skipped purge must roll back clearing the online module reference")
	}
}

func TestPurgeOnce_RemovesScenarioInSameRoundAsItsLastSession(t *testing.T) {
	f := newScenarioPurgeFixture(t)
	seedGenerationLog(t, f.scenarioID)
	old := f.seedPurgeSession(t, models.SessionStatusEnded, 40*24*time.Hour)
	softDeleteScenario(t, f.scenarioID)

	purgeOnce(time.Now())

	if sessionExists(t, old) {
		t.Error("expired session should be purged")
	}
	if scenarioExists(t, f.scenarioID) || generationLogCount(t, f.scenarioID) != 0 {
		t.Error("scenario freed by that purge should go in the same round")
	}
}

func TestPurgeOnce_KeepsScenarioWhileFavoritedSessionRemains(t *testing.T) {
	f := newScenarioPurgeFixture(t)
	old := f.seedPurgeSession(t, models.SessionStatusEnded, 40*24*time.Hour)
	f.favorite(t, old)
	softDeleteScenario(t, f.scenarioID)

	purgeOnce(time.Now())

	if !sessionExists(t, old) || !scenarioExists(t, f.scenarioID) {
		t.Error("a favorited session keeps both itself and its scenario")
	}
}
