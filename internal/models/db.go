package models

import (
	"log"
	"os"
	"path/filepath"

	"github.com/llmcoc/server/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func InitDB() error {
	dbPath := config.Global.Database.Path
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return err
	}

	var err error
	DB, err = gorm.Open(sqlite.Open(dbPath+"?_journal_mode=WAL&_foreign_keys=on"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return err
	}

	// Auto-migrate all tables
	if err := DB.AutoMigrate(
		&User{},
		&CharacterCard{},
		&Scenario{},
		&GameSession{},
		&SessionPlayer{},
		&SessionNPC{},
		&SessionNPCMemory{},
		&SessionTurnAction{},
		&Message{},
		&ShopItem{},
		&Transaction{},
		&CoinRecharge{},
		&LLMProviderConfig{},
		&AgentConfig{},
		&GameEvaluation{},
		&SessionGrowthMark{},
	); err != nil {
		return err
	}

	seedDefaultData()
	log.Printf("Database initialized: %s", dbPath)
	return nil
}

func seedDefaultData() {
	seedDefaultShopItems()
	seedDefaultAgentConfigs()
}

func seedDefaultShopItems() {
	// Seed default shop items
	var count int64
	DB.Model(&ShopItem{}).Count(&count)
	if count == 0 {
		items := []ShopItem{
			{
				Name:        "人物卡槽位扩展 +1",
				Description: "永久增加 1 个人物卡槽位，让你可以保存更多的调查员档案。",
				ItemType:    ItemTypeCardSlot,
				Price:       10,
				Value:       1,
				IsActive:    true,
			},
			{
				Name:        "人物卡槽位扩展 +3",
				Description: "永久增加 3 个人物卡槽位，特惠套装。",
				ItemType:    ItemTypeCardSlot,
				Price:       25,
				Value:       3,
				IsActive:    true,
			},
		}
		DB.Create(&items)
	}
}

func seedDefaultAgentConfigs() {
	// Create a default LLMProviderConfig from env vars if none exist yet
	var provCount int64
	DB.Model(&LLMProviderConfig{}).Count(&provCount)
	if provCount == 0 {
		apiKey := os.Getenv("LLM_API_KEY")
		if apiKey != "" {
			providerType := os.Getenv("LLM_PROVIDER")
			if providerType == "" {
				providerType = "openai"
			}
			defProv := LLMProviderConfig{
				Name:     "默认",
				Provider: providerType,
				BaseURL:  os.Getenv("LLM_BASE_URL"),
				APIKey:   apiKey,
				IsActive: true,
			}
			DB.Create(&defProv)
		}
	}

	// Remove obsolete agent roles that no longer exist in the pipeline.
	DB.Where("role IN ?", []string{"judger", "editor", "lore_researcher", "encounter_designer"}).Delete(&AgentConfig{})

	var provID *uint
	var prov LLMProviderConfig
	if DB.First(&prov).Error == nil {
		id := prov.ID
		provID = &id
	}

	model := os.Getenv("LLM_MODEL")
	if model == "" {
		model = "gpt-4o"
	}

	// Ensure each active agent has a config row (upsert-style: create only if missing).
	required := []AgentConfig{
		{Role: AgentRoleDirector, ProviderConfigID: provID, ModelName: model, MaxTokens: 1500, Temperature: 0.7, IsActive: true},
		{Role: AgentRoleScripter, ProviderConfigID: provID, ModelName: model, MaxTokens: 1800, Temperature: 0.5, IsActive: true},
		{Role: AgentRoleArchitect, ProviderConfigID: provID, ModelName: model, MaxTokens: 4000, Temperature: 0.7, IsActive: true},
		{Role: AgentRoleQAGuard, ProviderConfigID: provID, ModelName: model, MaxTokens: 2000, Temperature: 0.3, IsActive: true},
		{Role: AgentRoleWriter, ProviderConfigID: provID, ModelName: model, MaxTokens: 800, Temperature: 0.85, IsActive: true},
		{Role: AgentRoleLawyer, ProviderConfigID: provID, ModelName: model, MaxTokens: 800, Temperature: 0.3, IsActive: true},
		{Role: AgentRoleNPC, ProviderConfigID: provID, ModelName: model, MaxTokens: 600, Temperature: 0.9, IsActive: true},
		{Role: AgentRoleParser, ProviderConfigID: provID, ModelName: model, MaxTokens: 4000, Temperature: 0.1, IsActive: true},
		{Role: AgentRoleEvaluator, ProviderConfigID: provID, ModelName: model, MaxTokens: 1200, Temperature: 0.5, IsActive: true},
		{Role: AgentRoleGrowth, ProviderConfigID: provID, ModelName: model, MaxTokens: 1000, Temperature: 0.4, IsActive: true},
	}
	for _, ag := range required {
		var existing AgentConfig
		if DB.Where("role = ?", ag.Role).First(&existing).Error != nil {
			DB.Create(&ag)
		}
	}
}
