// NOTE: orchestrator_director_history_test.go 用一个只返回终止性response调用的假
// Provider,端到端跑run()两轮,验证Director跨轮ContextManager(context_transcript.go)
// 的核心回归行为:①下一轮第1次调用收到的msgs,以上一轮最后一次调用的msgs+其assistant
// 输出为逐字段相同的前缀(发送版=归档版,prompt cache前缀跨轮稳定);②reasoning在下一轮
// 仍然原样保留;③超阈值时最旧一轮被整体丢弃;④CacheBreakpoint只打在head末尾和上一轮
// 末尾;⑤round<=1硬失败不落库,transcript保持不变。不涉及真实游戏业务逻辑,response
// 之外的工具都不会被调用。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// directorHistoryFakeProvider 只实现ChatWithTools:每次调用都记录收到的msgs快照,
// 返回一个终止性的response工具调用(reply/tool_call_id里嵌入调用序号,便于断言
// 区分是哪一轮产生的),并附带用例配置好的usage/reasoning,用于驱动阈值判断与
// reasoning透传断言。failFirstCall非nil时下一次调用直接返回该error,不产生任何
// assistant消息,用于模拟round<=1硬失败。
type directorHistoryFakeProvider struct {
	mu              sync.Mutex
	calls           [][]llm.ChatMessage
	usage           llm.Usage
	reasoningBlocks []llm.ReasoningBlock
	reasoning       string
	failFirstCall   error
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

// resultFor 构造第n次调用(1-indexed)应该返回的ToolChatResult;测试断言复用同一个
// 方法构造期望值,避免与ChatWithTools内部拼装逻辑重复一份容易失步的字符串格式化。
func (p *directorHistoryFakeProvider) resultFor(n int) llm.ToolChatResult {
	args, _ := json.Marshal(map[string]string{"reply": fmt.Sprintf("KP回复第%d次调用", n)})
	return llm.ToolChatResult{
		ToolCalls:       []llm.ToolCall{{ID: fmt.Sprintf("call-%d", n), Name: string(ToolResponse), Arguments: string(args)}},
		ReasoningBlocks: p.reasoningBlocks,
		Reasoning:       p.reasoning,
		Usage:           p.usage,
	}
}

func (p *directorHistoryFakeProvider) ChatWithTools(_ context.Context, _ string, msgs []llm.ChatMessage, _ []llm.ToolDefinition) (llm.ToolChatResult, error) {
	p.mu.Lock()
	n := len(p.calls) + 1
	p.calls = append(p.calls, append([]llm.ChatMessage(nil), msgs...))
	failErr := p.failFirstCall
	p.mu.Unlock()

	// NOTE: failFirstCall一旦设置就对"下一次"调用生效(不要求是全局第1次调用)，
	// 用于模拟某一次run()内round<=1硬失败——调用方在两次run()之间设置该字段。
	if failErr != nil {
		return llm.ToolChatResult{}, failErr
	}
	return p.resultFor(n), nil
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
// gctx.Session.TurnRound,也是持久化后TranscriptTurn.Round的取值。
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

// loadTranscript 读取(session_id, agent_key)对应的持久化TranscriptData,不存在时
// 直接Fatal——调用方应仅在预期已经Commit过的场景下使用。
func loadTranscript(t *testing.T, sessionID uint, agentKey string) models.TranscriptData {
	t.Helper()
	var rec models.AgentTranscript
	if err := models.DB.Where("session_id = ? AND agent_key = ?", sessionID, agentKey).First(&rec).Error; err != nil {
		t.Fatalf("load agent transcript(session=%d agent_key=%s): %v", sessionID, agentKey, err)
	}
	return rec.Data.Data
}

// TestRun_DirectorContextPrefixStableAcrossTurns 是本次重设计的核心回归测试:
// 验证ContextManager让"发送版=归档版",上一轮的原生assistant输出(含tool_calls与
// reasoning)原样出现在下一轮请求的固定位置上,且CacheBreakpoint只打在head末尾与
// 上一轮末尾——这三点共同保证Anthropic prompt cache前缀能跨轮命中。
func TestRun_DirectorContextPrefixStableAcrossTurns(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 101
	if err := models.DB.Create(&models.GameSession{ID: sessionID}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	prov := &directorHistoryFakeProvider{
		reasoningBlocks: []llm.ReasoningBlock{{Type: "thinking", Text: "turn1密谈推理", Signature: "sig-turn1"}},
	}
	setDirectorFakeHandle(t, sessionID, prov, 0) // window=0:不触发trim,只验证前缀稳定性

	if _, err := run(context.Background(), newDirectorRunGctx(sessionID, 1, "我环顾四周")); err != nil {
		t.Fatalf("run() turn1: %v", err)
	}
	if _, err := run(context.Background(), newDirectorRunGctx(sessionID, 2, "我继续调查")); err != nil {
		t.Fatalf("run() turn2: %v", err)
	}
	if got := prov.callCount(); got != 2 {
		t.Fatalf("provider call count = %d, want 2(每轮1次工具调用即以response结束)", got)
	}

	turn1Msgs := prov.msgsAt(0)
	turn2Msgs := prov.msgsAt(1)

	// ①核心断言:turn2第1次调用收到的msgs,前 len(turn1Msgs)+1 条应与
	// "turn1Msgs + turn1产出的assistant消息"逐字段相同(CacheBreakpoint是位置相关的
	// 标记,不属于"内容"本身,单独在下面④里断言,这里比较前先清零两侧再比较)。
	turn1Result := prov.resultFor(1)
	wantAssistant := llm.ChatMessage{
		Role:            "assistant",
		Content:         turn1Result.Content,
		ToolCalls:       turn1Result.ToolCalls,
		ReasoningBlocks: turn1Result.ReasoningBlocks,
		Reasoning:       turn1Result.Reasoning,
	}
	wantPrefix := append(append([]llm.ChatMessage(nil), turn1Msgs...), wantAssistant)
	if len(turn2Msgs) < len(wantPrefix) {
		t.Fatalf("turn2Msgs长度%d应不小于期望前缀长度%d", len(turn2Msgs), len(wantPrefix))
	}
	for i, want := range wantPrefix {
		got := turn2Msgs[i]
		got.CacheBreakpoint = false
		want.CacheBreakpoint = false
		if !reflect.DeepEqual(got, want) {
			t.Errorf("turn2Msgs[%d]与期望前缀不一致:\ngot  %+v\nwant %+v", i, got, want)
		}
	}

	// ②reasoning在第2轮仍然原样存在(上面的DeepEqual已经隐含覆盖了这一点,这里再
	// 显式断言一次,防止将来误改比较逻辑导致这条关键属性被悄悄漏检)。
	assistantInTurn2 := turn2Msgs[len(turn1Msgs)]
	if len(assistantInTurn2.ReasoningBlocks) == 0 || assistantInTurn2.ReasoningBlocks[0].Text != "turn1密谈推理" {
		t.Errorf("turn2收到的历史里,turn1的reasoning应原样保留,got %+v", assistantInTurn2.ReasoningBlocks)
	}

	// ④CacheBreakpoint只打在head末尾(system+scenario的scenario消息)与上一轮末尾
	// (turn1归档的最后一条消息,即response对应的tool结果消息)。head长度固定为2
	// (见buildKPHead:system+scenario);turn1归档的历史长度为4
	// (buildKPTurnOpening的2条opening消息 + assistant + tool结果)。
	const headLen = 2
	const turn1HistoryLen = 4
	for i, m := range turn2Msgs {
		want := i == headLen-1 || i == headLen+turn1HistoryLen-1
		if m.CacheBreakpoint != want {
			t.Errorf("turn2Msgs[%d].CacheBreakpoint=%v, want %v", i, m.CacheBreakpoint, want)
		}
	}
}

// TestRun_DirectorTranscriptTrimsOldestTurnWhenOverThreshold 验证③:超过Window
// 阈值时,ContextManager从最旧的已提交历史轮开始整体丢弃,不拆散某一轮内部的
// tool_call与对应tool结果。
func TestRun_DirectorTranscriptTrimsOldestTurnWhenOverThreshold(t *testing.T) {
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
	afterTurn1 := loadTranscript(t, sessionID, "director")
	if len(afterTurn1.Turns) != 1 {
		t.Fatalf("只有1轮时不应trim(trimTranscriptTurns对len<=1直接跳过),got %d轮", len(afterTurn1.Turns))
	}

	if _, err := run(context.Background(), newDirectorRunGctx(sessionID, 2, "第二轮输入")); err != nil {
		t.Fatalf("run() turn2: %v", err)
	}
	afterTurn2 := loadTranscript(t, sessionID, "director")
	turns := afterTurn2.Turns
	if len(turns) != 1 {
		t.Fatalf("超阈值后应整体trim掉最旧一轮、只剩最新1轮,got %d轮: %+v", len(turns), turns)
	}
	if turns[0].Round != 2 {
		t.Errorf("剩下的应是第2轮,got Round=%d", turns[0].Round)
	}
	for _, m := range turns[0].Messages {
		if strings.Contains(m.Content, "第一轮输入") {
			t.Errorf("第2轮的历史消息不应包含第1轮的玩家输入文本,got %q", m.Content)
		}
		for _, tc := range m.ToolCalls {
			if strings.Contains(tc.Arguments, "第1次调用") {
				t.Errorf("第1轮的tool_call不应残留在trim之后的历史里,got %q", tc.Arguments)
			}
		}
	}
}

// TestRun_DirectorHardFailureLeavesTranscriptUnchanged 验证⑤:round<=1的硬失败
// (第1轮ChatWithTools直接报错,尚未取得任何进展)不应该调用Commit,已持久化的
// transcript要保持字节不变;全新会话上round<=1直接失败时,也不应该创建任何
// transcript记录。
func TestRun_DirectorHardFailureLeavesTranscriptUnchanged(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 103
	if err := models.DB.Create(&models.GameSession{ID: sessionID}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	prov := &directorHistoryFakeProvider{}
	setDirectorFakeHandle(t, sessionID, prov, 0)

	if _, err := run(context.Background(), newDirectorRunGctx(sessionID, 1, "第一轮输入")); err != nil {
		t.Fatalf("run() turn1: %v", err)
	}
	before := loadTranscript(t, sessionID, "director")

	prov.failFirstCall = errors.New("网关超时")
	if _, err := run(context.Background(), newDirectorRunGctx(sessionID, 2, "第二轮输入")); err == nil {
		t.Fatal("run() turn2 应因round<=1硬失败返回错误")
	}
	after := loadTranscript(t, sessionID, "director")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("round<=1硬失败不应触碰transcript:\nbefore %+v\nafter  %+v", before, after)
	}

	const freshSessionID = 104
	if err := models.DB.Create(&models.GameSession{ID: freshSessionID}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	failProv := &directorHistoryFakeProvider{failFirstCall: errors.New("网关超时")}
	setDirectorFakeHandle(t, freshSessionID, failProv, 0)
	if _, err := run(context.Background(), newDirectorRunGctx(freshSessionID, 1, "输入")); err == nil {
		t.Fatal("run() 应因round<=1硬失败返回错误")
	}
	var count int64
	models.DB.Model(&models.AgentTranscript{}).Where("session_id = ?", freshSessionID).Count(&count)
	if count != 0 {
		t.Errorf("round<=1硬失败不应创建transcript记录,got count=%d", count)
	}
}
