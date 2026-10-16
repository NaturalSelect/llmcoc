// NOTE: testhelpers_test.go 汇集 agent 包测试共用的辅助：带 SiteSetting 表的内存数据库、
// 按序返回预设响应的 fake provider，以及工具调用构造函数。禁止真实网络/真实LLM。
package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NOTE: initSiteSettingTestDB 初始化带 SiteSetting 表的测试内存数据库，
// 供 runLawyer 内部调用 models.GetSiteSetting 时不 panic。
func initSiteSettingTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open site setting test db: %v", err)
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

// NOTE: sequentialFakeProvider 支持原生工具调用 ChatWithTools（reward_agent/architect/
// qa/lawyer 统一走此协议）与纯文本 JsonChat（compile 阶段），按序返回预设响应，
// 并记录调用的 cacheKey，用于路由隔离验证。满足 llm.Provider 接口，禁止真实网络。
// Chat/ChatStream 不参与业务路径，仅为满足接口而提供的桩实现。
type sequentialFakeProvider struct {
	mu sync.Mutex

	// ChatWithTools 路径的预设响应序列（原生工具调用）。
	toolResponses []llm.ToolChatResult
	toolCallIdx   int

	// JsonChat 路径的预设响应序列（纯文本JSON模式，如compile阶段）；序列耗尽时返回"{}"。
	jsonResponses []string
	jsonCallIdx   int

	// NOTE: recordedKeys 记录 ChatWithTools/JsonChat 调用的 cacheKey 顺序，用于路由隔离验证。
	recordedKeys []string
	// NOTE: recordedTools 记录每次 ChatWithTools 调用时传入的工具定义列表（按调用顺序），
	// 用于验证各轮发给模型的工具定义保持一致（如 lawyer 每轮都发全集，保证 prompt cache 前缀稳定）。
	recordedTools [][]llm.ToolDefinition
	// NOTE: recordedMessages 记录每次 ChatWithTools 调用时传入的完整消息历史（按调用顺序），
	// 用于验证工具调用回执（tool 消息）内容，如 SYSTEM REJECT 提示是否正确回传。
	recordedMessages [][]llm.ChatMessage
	// NOTE: callerName 标识此 fake 代表哪个角色，仅用于错误提示。
	callerName string
}

func (p *sequentialFakeProvider) Chat(_ context.Context, _ string, _ []llm.ChatMessage) (string, error) {
	return "", nil
}

func (p *sequentialFakeProvider) ChatStream(_ context.Context, _ string, _ []llm.ChatMessage) (<-chan string, <-chan error, error) {
	return nil, nil, nil
}

func (p *sequentialFakeProvider) JsonChat(_ context.Context, cacheKey string, msgs []llm.ChatMessage) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recordedKeys = append(p.recordedKeys, cacheKey)
	p.recordedMessages = append(p.recordedMessages, append([]llm.ChatMessage(nil), msgs...))
	if p.jsonCallIdx >= len(p.jsonResponses) {
		return "{}", nil
	}
	resp := p.jsonResponses[p.jsonCallIdx]
	p.jsonCallIdx++
	return resp, nil
}

func (p *sequentialFakeProvider) ChatWithTools(_ context.Context, cacheKey string, msgs []llm.ChatMessage, tools []llm.ToolDefinition) (llm.ToolChatResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recordedKeys = append(p.recordedKeys, cacheKey)
	p.recordedTools = append(p.recordedTools, tools)
	p.recordedMessages = append(p.recordedMessages, append([]llm.ChatMessage(nil), msgs...))
	if p.toolCallIdx >= len(p.toolResponses) {
		// NOTE: 序列耗尽时返回空工具调用，触发驱动器"连续空轮"快速失败，避免死循环拖满maxRounds。
		return llm.ToolChatResult{}, nil
	}
	resp := p.toolResponses[p.toolCallIdx]
	p.toolCallIdx++
	return resp, nil
}

// NOTE: 编译期确认 sequentialFakeProvider 满足 llm.Provider 接口。
var _ llm.Provider = (*sequentialFakeProvider)(nil)

// fakeToolCall 是测试里快速构造 llm.ToolCall 的辅助函数；argsJSON 是原生工具调用的
// JSON 参数字符串。
func fakeToolCall(id, name, argsJSON string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: name, Arguments: argsJSON}
}

// lawyerDirectResponseToolResponses 构造 lawyer 最简两轮工具响应序列：
// 第1轮（受 firstRoundTools 限制，只能 search_cache）→ 第2轮直接 response 给出裁定。
// 供只关心"lawyer provider 被调用且返回裁定"、不关心裁定内容细节的路由测试复用。
func lawyerDirectResponseToolResponses(ruling string) []llm.ToolChatResult {
	respondArgs, _ := json.Marshal(map[string]string{"ruling": ruling})
	return []llm.ToolChatResult{
		{ToolCalls: []llm.ToolCall{fakeToolCall("lc_1", toolNameSearchCache, `{"keyword":"#测试"}`)}},
		{ToolCalls: []llm.ToolCall{fakeToolCall("lc_2", toolNameLawyerResponse, string(respondArgs))}},
	}
}
