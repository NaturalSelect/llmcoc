// NOTE: db_agent_seed_test.go 验证 seedDefaultAgentConfigs 对已下线 Agent 角色的清理，
// 以及为 diffusion_translator 播种默认启用的配置行。
package models

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openSeedTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&LLMProviderConfig{}, &AgentConfig{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	prev := DB
	DB = db
	t.Cleanup(func() {
		DB = prev
		_ = sqlDB.Close()
	})
	return db
}

func TestSeedDefaultAgentConfigs_RemovesTranslator(t *testing.T) {
	db := openSeedTestDB(t)

	if err := db.Create(&AgentConfig{Role: "translator", ModelName: "gpt-4o", IsActive: true}).Error; err != nil {
		t.Fatalf("seed legacy translator row: %v", err)
	}

	seedDefaultAgentConfigs()

	var count int64
	db.Model(&AgentConfig{}).Where("role = ?", "translator").Count(&count)
	if count != 0 {
		t.Errorf("translator rows after seed = %d, want 0", count)
	}
	db.Model(&AgentConfig{}).Where("role = ?", string(AgentRoleLawyer)).Count(&count)
	if count != 1 {
		t.Errorf("lawyer rows after seed = %d, want 1 (seed should still create active roles)", count)
	}
}

func TestSeedDefaultAgentConfigs_SeedsDiffusionTranslator(t *testing.T) {
	db := openSeedTestDB(t)

	seedDefaultAgentConfigs()

	var cfg AgentConfig
	if err := db.Where("role = ?", string(AgentRoleDiffusionTranslator)).First(&cfg).Error; err != nil {
		t.Fatalf("diffusion_translator row not seeded: %v", err)
	}
	if !cfg.IsActive {
		t.Error("diffusion_translator should be active by default so enabling a painter's diffusion mode works out of the box")
	}
	if cfg.Temperature > 0.5 {
		t.Errorf("temperature = %v, want a low value so the tag output stays faithful to the scene", cfg.Temperature)
	}
}
