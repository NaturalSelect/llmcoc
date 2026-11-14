// NOTE: Package models handles database initialization and schema migrations.
package models

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/llmcoc/server/internal/config"
	"github.com/llmcoc/server/internal/logging"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

// NOTE: InitDB connects to the SQLite database, configures it with WAL mode,
// runs auto-migrations for all known models, and seeds default data.
func InitDB() error {
	dbPath := config.Global.Database.Path
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return err
	}

	var err error
	DB, err = gorm.Open(sqlite.Open(dbPath+"?_journal_mode=WAL&_foreign_keys=on"), &gorm.Config{
		// NOTE: 关闭 record not found 的日志噪音，轮询类接口（如 GetChatStatus）
		// 用 First() 查询"是否已提交"是正常业务分支，不是需要告警的错误。
		Logger: logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  true,
		}),
	})
	if err != nil {
		return err
	}

	// NOTE: with_jailbreak 字段新增前，Director/NPC/NPCNSFW 一直硬编码开启越狱提示词；
	// 记录该列此前是否已存在，若是老库升级，则迁移后把这三个角色的既有记录回填为 true，避免行为静默改变。
	needsJailbreakBackfill := !DB.Migrator().HasColumn(&AgentConfig{}, "WithJailbreak")
	// NOTE: 剧中时间改为 RFC 3339 字符串前由 TurnRound 推算；老库升级时把进行中的房间折算成 game_time，
	// 只在列首次出现时回填一次，之后 game_time 由 advance_time 维护，不能再被覆盖。
	needsGameTimeBackfill := !DB.Migrator().HasColumn(&GameSession{}, "GameTime")

	// Auto-migrate all tables
	if err := DB.AutoMigrate(
		&User{},
		&CharacterCard{},
		&CharacterDraft{},
		&Scenario{},
		&ScenarioGenerationLog{},
		&GameSession{},
		&SessionPlayer{},
		&SessionFavorite{},
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
		&SiteSetting{},
		&InviteCode{},
		&Announcement{},
		&LawyerCacheStats{},
		&LLMLatencyStat{},
		&AgentTranscript{},
		&OnlineModule{},
		&OnlineModuleDocument{},
	); err != nil {
		return err
	}

	if needsJailbreakBackfill {
		DB.Model(&AgentConfig{}).
			Where("role IN ?", []string{string(AgentRoleDirector), string(AgentRoleNPC), string(AgentRoleNPCNSFW)}).
			Update("with_jailbreak", true)
	}

	if needsGameTimeBackfill {
		backfillGameTime()
	}

	backfillLegacyItems()
	seedDefaultData()
	logging.For("models").Info("database initialized", "path", dbPath)
	return nil
}

func seedDefaultData() {
	seedDefaultShopItems()
	seedDefaultAgentConfigs()
	seedDefaultSiteSettings()
}

func seedDefaultSiteSettings() {
	defaults := map[string]string{
		"require_invite_code":        "false",
		"initial_coins":              "600",
		"initial_card_slots":         "3",
		"regenerate_appearance_cost": "100",
		"regenerate_backstory_cost":  "100",
		"regenerate_traits_cost":     "100",
		"regenerate_portrait_cost":   "100",
		"revive_base_cost":           "2000",
		"end_session_cost":           "200",
		// NOTE: dramaturg_history_max_tokens 专用于剧构顾问自己的进度对话历史。
		"dramaturg_history_max_tokens": "100000",
		"max_character_drafts":         "3",
		// NOTE: 全局 NSFW 总开关（区别于房间级 enable_nsfw）；默认允许，保持升级前后行为一致。
		"allow_nsfw": "true",
		// NOTE: 默认平衡调整规则，与 models.DefaultBalanceRules 保持同一来源；
		// 已有该 key（含空字符串）时绝不覆盖。
		"balance_rules": DefaultBalanceRules,
		// NOTE: Lawyer 规则缓存向量检索的相似度阈值，仅在启用 embedding 角色后生效；合法范围 [-1, 1]。
		"lawyer_cache_min_similarity": DefaultLawyerCacheMinSimilarity,
		// NOTE: 已结束房间的保留天数，按结束时间（updated_at）计算，0 表示不自动回收；被收藏的房间永不回收。
		"session_retention_days": "30",
	}
	for k, v := range defaults {
		var s SiteSetting
		if DB.Where("key = ?", k).First(&s).Error != nil {
			DB.Create(&SiteSetting{Key: k, Value: v})
		}
	}
}

func seedDefaultShopItems() {
	// Seed default shop items
	var count int64
	DB.Model(&ShopItem{}).Count(&count)
	if count == 0 {
		items := []ShopItem{
			{
				Name:        "人物卡槽位扩展 +1",
				Description: "永久增加 1 个人物卡槽位,让你可以保存更多的调查员档案。",
				ItemType:    ItemTypeCardSlot,
				Price:       10,
				Value:       1,
				IsActive:    true,
			},
			{
				Name:        "人物卡槽位扩展 +3",
				Description: "永久增加 3 个人物卡槽位,特惠套装。",
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
	// Remove obsolete agent roles that no longer exist in the pipeline.
	// NOTE: providence 已改名为 dramaturg，需清理老库中遗留的 providence 行，否则会在后台Agent列表里以裸role字符串显示。
	// translator 已移除（architect 直接用 ask_lawyer 核验锚点），同理清理老库遗留行。
	// evaluator / growth 已移除（评分和技能提升改由 Director 在 end_game 里填写），同理清理老库遗留行。
	DB.Where("role IN ?", []string{"judger", "editor", "lore_researcher", "encounter_designer", "anti_cheat", "providence", "translator", "evaluator", "growth"}).Delete(&AgentConfig{})

	var provID *uint
	var prov LLMProviderConfig
	if DB.First(&prov).Error == nil {
		id := prov.ID
		provID = &id
	}

	const model = "gpt-4o"

	// Ensure each active agent has a config row (upsert-style: create only if missing).
	required := []AgentConfig{
		{Role: AgentRoleDirector, ProviderConfigID: provID, ModelName: model, MaxTokens: 2200, Temperature: 0.5, ThinkingLevel: "low", IsActive: true, WithJailbreak: true},
		{Role: AgentRoleArchitect, ProviderConfigID: provID, ModelName: model, MaxTokens: 4000, Temperature: 0.5, ThinkingLevel: "low", IsActive: true},
		{Role: AgentRoleQAGuard, ProviderConfigID: provID, ModelName: model, MaxTokens: 2200, Temperature: 0.5, ThinkingLevel: "low", IsActive: true},
		{Role: AgentRoleWriter, ProviderConfigID: provID, ModelName: model, MaxTokens: 1800, Temperature: 0.5, ThinkingLevel: "low", IsActive: true},
		// NOTE: writer_nsfw 默认关闭,需要管理员单独绑定 provider 才会被路由;未启用时自动回落默认 Writer。
		{Role: AgentRoleWriterNSFW, ProviderConfigID: provID, ModelName: model, MaxTokens: 1800, Temperature: 0.5, ThinkingLevel: "low", IsActive: false},
		{Role: AgentRoleLawyer, ProviderConfigID: provID, ModelName: model, MaxTokens: 1400, Temperature: 0.5, ThinkingLevel: "low", IsActive: true},
		{Role: AgentRoleNPC, ProviderConfigID: provID, ModelName: model, MaxTokens: 1600, Temperature: 0.5, ThinkingLevel: "low", IsActive: true, WithJailbreak: true},
		// NOTE: npc_nsfw 默认关闭,需要管理员单独绑定 provider 才会被路由;未启用时自动回落默认 NPC。
		{Role: AgentRoleNPCNSFW, ProviderConfigID: provID, ModelName: model, MaxTokens: 1600, Temperature: 0.5, ThinkingLevel: "low", IsActive: false, WithJailbreak: true},
		{Role: AgentRolePainter, ProviderConfigID: provID, ModelName: "dall-e-3", MaxTokens: 0, Temperature: 0.0, ThinkingLevel: "none", IsActive: false},
		// NOTE: painter_nsfw 默认关闭,需要管理员单独绑定 provider 才会放行 nsfw=true 的配图;未启用时这类请求被拒绝。
		{Role: AgentRolePainterNSFW, ProviderConfigID: provID, ModelName: "dall-e-3", MaxTokens: 0, Temperature: 0.0, ThinkingLevel: "none", IsActive: false},
		// NOTE: painter_hq 默认关闭,需要管理员绑定高质量图片模型;未启用时立绘生成直接报错,不回落默认 Painter。
		{Role: AgentRolePainterHQ, ProviderConfigID: provID, ModelName: "gpt-image-1", MaxTokens: 0, Temperature: 0.0, ThinkingLevel: "none", IsActive: false},
		// NOTE: embedding 默认关闭，需要管理员绑定 OpenAI 兼容的嵌入模型；未启用时 Lawyer 规则缓存退回标签子串匹配。
		{Role: AgentRoleEmbedding, ProviderConfigID: provID, ModelName: "text-embedding-3-small", MaxTokens: 0, Temperature: 0.0, ThinkingLevel: "none", IsActive: false},
		// NOTE: systemone 默认关闭，需要管理员绑定填了 TypeSafe Key 的 provider；未启用时玩家输入不经过分类直接放行。
		{Role: AgentRoleSystemOne, ProviderConfigID: provID, ModelName: "gemini-decision-1", MaxTokens: 0, Temperature: 0.0, ThinkingLevel: "none", IsActive: false},
		// NOTE: diffusion_translator 只输出一行标签,低温度减少擅自添加画面元素;仅在 Painter 开启 diffusion_mode 时才会被调用。
		{Role: AgentRoleDiffusionTranslator, ProviderConfigID: provID, ModelName: model, MaxTokens: 800, Temperature: 0.3, ThinkingLevel: "low", IsActive: true},
		{Role: AgentRoleParser, ProviderConfigID: provID, ModelName: model, MaxTokens: 4000, Temperature: 0.1, ThinkingLevel: "low", IsActive: true},
		// NOTE: Compiler 负责把故事文本忠实编译为结构化ScenarioContent；低温度以减少编译时的擅自发挥。
		// MaxTokens=4000 实测会被单次submit_compiled_scenario工具调用（scenes/npcs/clues/endings等
		// 十余个字段一次性生成）打满导致stop_reason=max_tokens截断，调大到8000留出余量。
		{Role: AgentRoleCompiler, ProviderConfigID: provID, ModelName: model, MaxTokens: 128000, Temperature: 0.15, ThinkingLevel: "low", IsActive: true},
		// NOTE: dramaturg 默认关闭,需要管理员单独绑定 provider 才会被路由;未启用时该工具不会
		// 出现在Director工具列表中,Director按自身[ACTIVE-PACING]规则自行判断节奏,不影响主流程。
		{Role: AgentRoleDramaturg, ProviderConfigID: provID, ModelName: model, MaxTokens: 500, Temperature: 0.5, ThinkingLevel: "low", IsActive: false},
	}
	for _, ag := range required {
		var existing AgentConfig
		if DB.Where("role = ?", ag.Role).First(&existing).Error != nil {
			DB.Create(&ag)
			// NOTE: IsActive 带 default:true，Create 会把零值 false 换成 true，需要显式回写才能真正默认关闭。
			if ag.Role == AgentRolePainterHQ || ag.Role == AgentRoleEmbedding || ag.Role == AgentRoleSystemOne {
				DB.Model(&ag).Update("is_active", false)
			}
		}
	}
}
