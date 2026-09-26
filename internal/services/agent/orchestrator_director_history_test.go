// NOTE: orchestrator_director_history_test.go 用一个只返回终止性response调用的假
// Provider,端到端跑run()两轮,验证Director跨轮持久化原生消息链(上一轮的
// tool_calls/tool结果原样可见)、以及超阈值时整体trim掉最旧历史轮这两条新接入的
// 行为;不涉及真实游戏业务逻辑,response之外的工具都不会被调用。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// directorHistoryFakeProvider 只实现ChatWithTools:每次调用都记录收到的msgs快照,
// 返回一个终止性的response工具调用(reply/tool_call_id里嵌入调用序号,便于断言
// 区分是哪一轮产生的),并附带用例配置好的usage,用于驱动阈值判断。
type directorHistoryFakeProvider struct {
	mu    sync.Mutex
	calls [][]llm.ChatMessage
	usage llm.Usage
}

func (p *directorHistoryFakeProvider) Chat(context.Context, string, []llm.ChatMessage) (string, error) {
	return "", nil
}

func (p *directorHistoryFakeProvider) ChatStream(context.Context, string, []llm.ChatMessage) (<-chan string, <-chan error, error) {
	return nil, nil, fmt.Errorf("directorHistoryFakeProvider: ChatStream not supported")
}

func (p *directorHistoryFakeProvider) JsonChat(context.Context, string, []llm.ChatMessage) (string, error) {
	return "", nil
}

func (p *directorHistoryFakeProvider) ChatWithTools(_ context.Context, _ string, msgs []llm.ChatMessage, _ []llm.ToolDefinition) (llm.ToolChatResult, error) {
	p.mu.Lock()
	n := len(p.calls)
	p.calls = append(p.calls, append([]llm.ChatMessage(nil), msgs...))
	p.mu.Unlock()

	args, _ := json.Marshal(map[string]string{"reply": fmt.Sprintf("KP回复第%d次调用", n+1)})
	return llm.ToolChatResult{
		ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("call-%d", n+1), Name: string(ToolResponse), Arguments: string(args)}},
		Usage:     p.usage,
	}, nil
}

func (p *directorHistoryFakeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *directorHistoryFakeProvider) msgsAt(i int) []llm.ChatMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[i]
}

var _ llm.Provider = (*directorHistoryFakeProvider)(nil)

// newDirectorRunGctx 构造一次run()所需的最小GameContext。round既是本轮
// gctx.Session.TurnRound,也是持久化后DirectorTurn.Round的取值。
func newDirectorRunGctx(sessionID uint, round int, userInput string) GameContext {
	return GameContext{
		Session:   models.GameSession{ID: sessionID, TurnRound: round},
		UserInput: userInput,
		UserName:  "玩家甲",
	}
}

// setDirectorFakeHandle 把只含一个Director假provider的agentHandle塞进sessionAgents
// 缓存,绕开batchLoadAgents真实查DB/校验Provider绑定的路径;Dramaturg显式禁用。
func setDirectorFakeHandle(t *testing.T, sessionID uint, prov llm.Provider, contextWindow int) {
	t.Helper()
	sessionAgents.Store(sessionID, map[models.AgentRole]agentHandle{
		models.AgentRoleDirector:  {provider: prov, config: &models.AgentConfig{Role: models.AgentRoleDirector, IsActive: true, ContextWindow: contextWindow}, enabled: true},
		models.AgentRoleDramaturg: {enabled: false},
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })
}

func TestRun_DirectorSeesOwnPriorToolCallsAcrossTurns(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 101
	if err := models.DB.Create(&models.GameSession{ID: sessionID}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	prov := &directorHistoryFakeProvider{}
	setDirectorFakeHandle(t, sessionID, prov, 0) // window=0:不触发trim,只验证跨轮可见性

	if _, err := run(context.Background(), newDirectorRunGctx(sessionID, 1, "我环顾四周")); err != nil {
		t.Fatalf("run() turn1: %v", err)
	}
	if _, err := run(context.Background(), newDirectorRunGctx(sessionID, 2, "我继续调查")); err != nil {
		t.Fatalf("run() turn2: %v", err)
	}

	if got := prov.callCount(); got != 2 {
		t.Fatalf("provider call count = %d, want 2(每轮1次工具调用即以response结束)", got)
	}

	turn2Msgs := prov.msgsAt(1)
	var sawTurn1ToolCall, sawTurn1ToolResult bool
	for _, m := range turn2Msgs {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				if tc.Name == string(ToolResponse) && strings.Contains(tc.Arguments, "第1次调用") {
					sawTurn1ToolCall = true
				}
			}
		}
		if m.Role == "tool" && m.ToolCallID == "call-1" {
			sawTurn1ToolResult = true
		}
	}
	if !sawTurn1ToolCall {
		t.Errorf("turn2收到的msgs应包含turn1的原生tool_call,got %+v", turn2Msgs)
	}
	if !sawTurn1ToolResult {
		t.Errorf("turn2收到的msgs应包含turn1 tool_call对应的tool结果,got %+v", turn2Msgs)
	}

	var persisted models.GameSession
	if err := models.DB.First(&persisted, sessionID).Error; err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if len(persisted.DirectorHistory.Data) != 2 {
		t.Fatalf("persisted DirectorHistory应有2轮,got %d", len(persisted.DirectorHistory.Data))
	}
	if persisted.DirectorHistory.Data[0].Round != 1 || persisted.DirectorHistory.Data[1].Round != 2 {
		t.Errorf("持久化轮次的Round应为[1,2],got %+v", persisted.DirectorHistory.Data)
	}
}

func TestRun_DirectorHistoryTrimsOldestTurnWhenOverThreshold(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 102
	if err := models.DB.Create(&models.GameSession{ID: sessionID}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	// window很小、usage人为设很高,每一轮都必然越过阈值,逼出"整体trim最旧一轮"。
	prov := &directorHistoryFakeProvider{usage: llm.Usage{PromptTokens: 900}}
	setDirectorFakeHandle(t, sessionID, prov, 1000)

	if _, err := run(context.Background(), newDirectorRunGctx(sessionID, 1, "第一轮输入")); err != nil {
		t.Fatalf("run() turn1: %v", err)
	}
	var afterTurn1 models.GameSession
	if err := models.DB.First(&afterTurn1, sessionID).Error; err != nil {
		t.Fatalf("reload after turn1: %v", err)
	}
	if len(afterTurn1.DirectorHistory.Data) != 1 {
		t.Fatalf("只有1轮时不应trim(trimDirectorTurns对len<=1直接跳过),got %d轮", len(afterTurn1.DirectorHistory.Data))
	}

	if _, err := run(context.Background(), newDirectorRunGctx(sessionID, 2, "第二轮输入")); err != nil {
		t.Fatalf("run() turn2: %v", err)
	}

	var afterTurn2 models.GameSession
	if err := models.DB.First(&afterTurn2, sessionID).Error; err != nil {
		t.Fatalf("reload after turn2: %v", err)
	}
	turns := afterTurn2.DirectorHistory.Data
	if len(turns) != 1 {
		t.Fatalf("超阈值后应整体trim掉最旧一轮、只剩最新1轮,got %d轮: %+v", len(turns), turns)
	}
	if turns[0].Round != 2 {
		t.Errorf("剩下的应是第2轮,got Round=%d", turns[0].Round)
	}
	for _, m := range turns[0].Messages {
		if strings.Contains(m.Content, "第一轮输入") {
			t.Errorf("第2轮的归档消息不应包含第1轮的玩家输入文本,got %q", m.Content)
		}
		for _, tc := range m.ToolCalls {
			if strings.Contains(tc.Arguments, "第1次调用") {
				t.Errorf("第1轮的tool_call不应残留在trim之后的历史里,got %q", tc.Arguments)
			}
		}
	}
}
