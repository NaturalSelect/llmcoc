// NOTE: db_agent_seed_test.go 验证 seedDefaultAgentConfigs 对已下线 Agent 角色的清理，
// 以及不再为 translator 播种配置行。
package models

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSeedDefaultAgentConfigs_RemovesTranslator(t *testing.T) {
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
