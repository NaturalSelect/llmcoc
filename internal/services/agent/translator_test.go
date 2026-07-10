// NOTE: translator_test.go 验证 Translator Agent 与 Lawyer Agent 的 provider 路由隔离。
// 禁止真实网络/真实LLM；使用内联 fake provider。
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NOTE: initTranslatorTestDB 初始化带 SiteSetting 表的测试内存数据库，
// 供 runLawyer 内部调用 models.GetSiteSetting 时不 panic。
func initTranslatorTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open translator test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&models.SiteSetting{}); err != nil {
		t.Fatalf("auto-migrate SiteSetting: %v", err)
	}
	// NOTE: 写入默认 balance_rules，与生产 seed 保持一致。
	db.Create(&models.SiteSetting{Key: "balance_rules", Value: models.DefaultBalanceRules})
	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prev
		_ = sqlDB.Close()
	})
}

// NOTE: sequentialFakeProvider 按序返回预设响应，并记录每次 Chat 调用的 cacheKey。
// 满足 llm.Provider 接口，禁止真实网络。
type sequentialFakeProvider struct {
	mu        sync.Mutex
	responses []string
	callIdx   int
	// NOTE: recordedKeys 记录每次 Chat 的 cacheKey，用于路由隔离验证。
	recordedKeys []string
	// NOTE: callerName 标识此 fake 代表哪个角色，仅用于错误提示。
	callerName string
}

func (p *sequentialFakeProvider) Chat(_ context.Context, cacheKey string, _ []llm.ChatMessage) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recordedKeys = append(p.recordedKeys, cacheKey)
	if p.callIdx >= len(p.responses) {
		// NOTE: 返回 respond 防止死循环；测试 验证 callIdx 是否在预期轮数内
		return `[{"action":"respond","result":"{\"status\":\"found\",\"selected_anchor\":\"fallback\",\"rulebook_basis\":\"test\",\"usable_interpretation\":\"test\",\"must_avoid\":\"none\",\"fallback\":\"none\",\"blacklist_check\":\"ok\"}"}]`, nil
	}
	resp := p.responses[p.callIdx]
	p.callIdx++
	return resp, nil
}

func (p *sequentialFakeProvider) ChatStream(_ context.Context, _ string, _ []llm.ChatMessage) (<-chan string, <-chan error, error) {
	return nil, nil, nil
}

func (p *sequentialFakeProvider) JsonChat(_ context.Context, _ string, _ []llm.ChatMessage) (string, error) {
	return "{}", nil
}

// NOTE: 编译期确认 sequentialFakeProvider 满足 llm.Provider 接口。
var _ llm.Provider = (*sequentialFakeProvider)(nil)

// NOTE: makeTranslatorRoom 构造一个只带 translator 和 lawyer 的 scripterRoom，
// 供 runOneshotTranslatorAgent 测试使用；architect/qa 字段留零值。
func makeTranslatorRoom(translatorProv, lawyerProv llm.Provider, sessionID string) *scripterRoom {
	return &scripterRoom{
		sessionID: sessionID,
		translator: agentHandle{
			provider: translatorProv,
			config:   &models.AgentConfig{Role: models.AgentRoleTranslator, IsActive: true},
			enabled:  true,
		},
		lawyer: agentHandle{
			provider: lawyerProv,
			config:   &models.AgentConfig{Role: models.AgentRoleLawyer, IsActive: true},
			enabled:  true,
		},
	}
}

// TestTranslatorProviderIsolation 验证：
// 1. translator Chat 走 translator provider，cache key 含 "translator" 不含 "lawyer"；
// 2. ask_lawyer 实际走 lawyer provider，cache key 含 "lawyer"；
// 3. 最终 respond 结果正确返回。
func TestTranslatorProviderIsolation(t *testing.T) {
	initTranslatorTestDB(t)

	translatorProv := &sequentialFakeProvider{
		callerName: "translator",
		// NOTE: Round 1 → translator 先 ask_lawyer；Round 2 → translator respond
		responses: []string{
			`[{"action":"ask_lawyer","question":"食尸鬼在COC7规则书中是否已收录？"}]`,
			`[{"action":"respond","result":"{\"status\":\"found\",\"selected_anchor\":\"食尸鬼（Ghoul）\",\"rulebook_basis\":\"COC7规则书已收录\",\"usable_interpretation\":\"死者变形后保留记忆继续行动\",\"must_avoid\":\"不得自创属性\",\"fallback\":\"无\",\"blacklist_check\":\"未命中\"}"}]`,
		},
	}

	lawyerProv := &sequentialFakeProvider{
		callerName: "lawyer",
		// NOTE: lawyer 直接返回 response，避免触发 grep/search_cache 等 IO 操作。
		responses: []string{
			`[{"action":"response","ruling":"食尸鬼（Ghoul）：COC7规则书已收录，死者变形后保留人类记忆继续行动。"}]`,
		},
	}

	room := makeTranslatorRoom(translatorProv, lawyerProv, "test-session-translator-1")

	ctx := context.Background()
	result, err := runOneshotTranslatorAgent(ctx, room, "死者被古老力量束缚继续行动", "作为剧本神话锚点")
	if err != nil {
		t.Fatalf("runOneshotTranslatorAgent failed: %v", err)
	}
	if strings.TrimSpace(result) == "" {
		t.Fatal("result should not be empty")
	}
	if !strings.Contains(result, "食尸鬼") {
		t.Errorf("result should contain selected anchor 食尸鬼, got: %q", result)
	}

	// NOTE: 验证 translator provider 被调用，且所有 cache key 含 "translator" 不含 "lawyer"
	if len(translatorProv.recordedKeys) < 2 {
		t.Fatalf("translator provider should be called at least twice, got %d", len(translatorProv.recordedKeys))
	}
	for i, key := range translatorProv.recordedKeys {
		if !strings.Contains(key, string(models.AgentRoleTranslator)) {
			t.Errorf("translator call[%d] cache key should contain %q, got %q", i, models.AgentRoleTranslator, key)
		}
		if strings.Contains(key, string(models.AgentRoleLawyer)) {
			t.Errorf("translator call[%d] cache key must NOT contain %q, got %q", i, models.AgentRoleLawyer, key)
		}
	}

	// NOTE: 验证 lawyer provider 被 ask_lawyer 调用，且 cache key 含 "lawyer"
	if len(lawyerProv.recordedKeys) == 0 {
		t.Fatal("lawyer provider should be called via ask_lawyer")
	}
	for i, key := range lawyerProv.recordedKeys {
		if !strings.Contains(key, string(models.AgentRoleLawyer)) {
			t.Errorf("lawyer call[%d] cache key should contain %q, got %q", i, models.AgentRoleLawyer, key)
		}
	}
}

// TestTranslatorProviderNilFastFail 验证 translator provider 为 nil 时 fail-fast，不尝试走 lawyer。
func TestTranslatorProviderNilFastFail(t *testing.T) {
	initTranslatorTestDB(t)

	lawyerProv := &sequentialFakeProvider{callerName: "lawyer"}
	room := makeTranslatorRoom(nil, lawyerProv, "test-session-translator-2")

	ctx := context.Background()
	_, err := runOneshotTranslatorAgent(ctx, room, "某概念", "")
	if err == nil {
		t.Fatal("should fail when translator provider is nil")
	}
	// NOTE: 错误中含 "translator" 字样，上下文清晰。
	if !strings.Contains(err.Error(), "translator") {
		t.Errorf("error message should mention translator, got: %q", err.Error())
	}
	// NOTE: lawyer provider 不应被调用（fail-fast）。
	if len(lawyerProv.recordedKeys) != 0 {
		t.Errorf("lawyer provider should NOT be called when translator is nil, got %d calls", len(lawyerProv.recordedKeys))
	}
}

// TestTranslatorAskLawyerUsesLawyerProvider 专门验证 oneshotTranslatorAskLawyer 走 room.lawyer，
// 而非 room.translator。
func TestTranslatorAskLawyerUsesLawyerProvider(t *testing.T) {
	initTranslatorTestDB(t)

	translatorProv := &sequentialFakeProvider{callerName: "translator"}
	lawyerProv := &sequentialFakeProvider{
		callerName: "lawyer",
		responses: []string{
			`[{"action":"response","ruling":"规则书已查阅：食尸鬼已收录"}]`,
		},
	}
	room := makeTranslatorRoom(translatorProv, lawyerProv, "test-session-translator-3")

	call := oneshotTranslatorToolCall{
		Action:   toolTranslatorAskLawyer,
		Question: "食尸鬼是否在COC7规则书中有正式数值？",
	}
	result := oneshotTranslatorAskLawyer(context.Background(), room, call)

	// NOTE: 结果应包含 ask_lawyer_result 标签
	if !strings.Contains(result, "ask_lawyer_result") {
		t.Errorf("ask_lawyer result should be wrapped in ask_lawyer_result tag, got: %q", result)
	}

	// NOTE: lawyer provider 必须被调用；translator provider 不应被调用
	if len(lawyerProv.recordedKeys) == 0 {
		t.Fatal("lawyer provider must be called by oneshotTranslatorAskLawyer")
	}
	if len(translatorProv.recordedKeys) != 0 {
		t.Errorf("translator provider must NOT be called by oneshotTranslatorAskLawyer, got %d calls", len(translatorProv.recordedKeys))
	}

	// NOTE: lawyer cache key 含 "lawyer"
	for i, key := range lawyerProv.recordedKeys {
		if !strings.Contains(key, string(models.AgentRoleLawyer)) {
			t.Errorf("lawyer call[%d] cache key should contain %q, got %q", i, models.AgentRoleLawyer, key)
		}
	}
}

// TestTranslatorRoleInModels 验证 AgentRoleTranslator 常量已定义。
func TestTranslatorRoleInModels(t *testing.T) {
	if models.AgentRoleTranslator == "" {
		t.Fatal("AgentRoleTranslator should be non-empty")
	}
	if string(models.AgentRoleTranslator) != "translator" {
		t.Errorf("AgentRoleTranslator = %q, want %q", models.AgentRoleTranslator, "translator")
	}
}
