// NOTE: companion_summary_test.go 覆盖 AI 陪玩历史的 LLM 总结：渲染给总结模型的内容、运行开始前的总结、
// 总结失败时历史原样保留、一次运行内上下文又满时只总结一次，以及落库的上下文用量。
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// companionMemoryProvider 的 ChatWithTools 按序返回预设响应并记录收到的消息，Chat 当作总结调用并记录输入。
type companionMemoryProvider struct {
	mu            sync.Mutex
	toolResponses []llm.ToolChatResult
	toolMsgs      [][]llm.ChatMessage
	summaryReply  string
	summaryErr    error
	summaryInputs []string
}

func (p *companionMemoryProvider) Chat(_ context.Context, _ string, msgs []llm.ChatMessage) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.summaryInputs = append(p.summaryInputs, msgs[len(msgs)-1].Content)
	if p.summaryErr != nil {
		return "", p.summaryErr
	}
	return p.summaryReply, nil
}

func (p *companionMemoryProvider) ChatStream(context.Context, string, []llm.ChatMessage, []llm.ToolDefinition) (<-chan string, <-chan error, error) {
	return nil, nil, errors.New("unsupported")
}

func (p *companionMemoryProvider) JsonChat(context.Context, string, []llm.ChatMessage) (string, error) {
	return "{}", nil
}

func (p *companionMemoryProvider) ChatWithTools(_ context.Context, _ string, msgs []llm.ChatMessage, _ []llm.ToolDefinition) (llm.ToolChatResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := len(p.toolMsgs)
	p.toolMsgs = append(p.toolMsgs, append([]llm.ChatMessage(nil), msgs...))
	if i >= len(p.toolResponses) {
		return llm.ToolChatResult{}, errors.New("no more tool responses")
	}
	return p.toolResponses[i], nil
}

var _ llm.Provider = (*companionMemoryProvider)(nil)

const companionMemoryWindow = 6000

func useCompanionHandleWindow(t *testing.T, prov llm.Provider, window int) {
	t.Helper()
	prev := companionAgentLoader
	companionAgentLoader = func() (agentHandle, error) {
		return agentHandle{
			provider: prov,
			config:   &models.AgentConfig{Role: models.AgentRoleCompanion, IsActive: true, ContextWindow: window},
			enabled:  true,
		}, nil
	}
	t.Cleanup(func() { companionAgentLoader = prev })
}

// seedCompanionHistory 经 Commit 落库 n 个完整的陪玩单元，每个约 450 字，开头的 opening 带「旧消息 i」标记。
func seedCompanionHistory(t *testing.T, n int) {
	t.Helper()
	cm := LoadContext(companionTestSession, companionAgentKey, nil, ContextOptions{
		Window: companionMemoryWindow, Strategy: TrimSummarize, Summary: companionSummaryFormat,
	})
	for i := range n {
		opening := llm.ChatMessage{Role: "user", Content: fmt.Sprintf(
			"<companion_turn seq=%d>\n<new_messages>\n#%d [KP]: 旧消息%d %s\n</new_messages>\n<status>\nHP 10/12\n</status>\n<ask message_id=\"12\" question=\"怎么办？\"/>\n\n%s\n</companion_turn>",
			i, i+1, i, strings.Repeat("字", 400), companionOpeningHint)}
		msgs := cm.Build(opening)
		msgs = append(msgs,
			llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("s%d", i), Name: companionToolSubmitAction, Arguments: fmt.Sprintf(`{"text":"行动%d"}`, i)}}},
			llm.ChatMessage{Role: "tool", ToolCallID: fmt.Sprintf("s%d", i), Content: companionSubmitOK},
		)
		cm.Commit(i, msgs)
	}
}

func companionTurnsOf(t *testing.T) []models.TranscriptTurn {
	t.Helper()
	rec, ok := loadCompanionTranscript(t)
	if !ok {
		t.Fatal("应当有陪玩 transcript")
	}
	return rec.Data.Data.Turns
}

func TestRenderCompanionTurnsForSummary(t *testing.T) {
	opening := "<companion_turn seq=3>\n<new_messages>\n#7 [KP]: 门后传来脚步声\n#8 [甲]: 我去看看\n</new_messages>\n" +
		"<status>\nHP 10/12 SAN 55/99\n位置：码头\n</status>\n<ask message_id=\"9\" question=\"你们怎么办？\"/>\n\n" +
		companionOpeningHint + "\n</companion_turn>"
	turns := []models.TranscriptTurn{
		{Seq: 0, Kind: "summary", Messages: []models.TranscriptMsg{{Role: "user", Content: "<story_so_far>\n更早的总结\n</story_so_far>"}}},
		{Seq: 3, Messages: []models.TranscriptMsg{
			{Role: "user", Content: opening},
			{Role: "assistant", ToolCalls: []models.TranscriptToolCall{
				{ID: "a", Name: companionToolReadMessages, Arguments: `{}`},
				{ID: "b", Name: companionToolViewCharacter, Arguments: `{}`},
				{ID: "c", Name: companionToolWriteNote, Arguments: `{"content":"密码是1925","shared":false}`},
				{ID: "d", Name: companionToolPostOOC, Arguments: `{"content":"我先去看看"}`},
			}},
			{Role: "tool", ToolCallID: "a", Content: "<messages count=\"1\">\n#5 [KP]: 更早的叙事\n</messages>"},
			{Role: "tool", ToolCallID: "b", Content: "<character>角色面板</character>"},
			{Role: "tool", ToolCallID: "c", Content: "已记录笔记（仅自己可见）。"},
			{Role: "tool", ToolCallID: "d", Content: "已发送桌边消息。"},
			{Role: "assistant", ToolCalls: []models.TranscriptToolCall{{ID: "e", Name: companionToolSubmitAction, Arguments: `{"options":["推门"],"text":"「谁在那里？」"}`}}},
			{Role: "tool", ToolCallID: "e", Content: companionSubmitOK},
		}},
		{Seq: 4, Messages: []models.TranscriptMsg{
			{Role: "user", Content: "<companion_turn seq=4>\n<new_messages>\n（没有新消息）\n</new_messages>\n</companion_turn>"},
			{Role: "assistant", ToolCalls: []models.TranscriptToolCall{
				{ID: "f", Name: companionToolWriteNote, Arguments: `{"content":"被拒绝的笔记"}`},
				{ID: "g", Name: companionToolSubmitAction, Arguments: `{"text":"点名已变化时的提交"}`},
			}},
			{Role: "tool", ToolCallID: "f", Content: "SYSTEM REJECT: 本次最多写 3 条笔记，配额已用完。"},
			{Role: "tool", ToolCallID: "g", Content: "点名已变化，无需回应。"},
		}},
	}
	got := renderCompanionTurnsForSummary(turns)

	for _, want := range []string{
		"<story_so_far>\n更早的总结", "<companion_turn seq=3>", "#7 [KP]: 门后传来脚步声", "#8 [甲]: 我去看看",
		`<ask message_id="9" question="你们怎么办？"/>`, "[你查看的消息]", "#5 [KP]: 更早的叙事",
		"[你写的笔记（仅自己可见）] 密码是1925", "[你发的桌边消息] 我先去看看", "[你的回应] 推门\n「谁在那里？」",
		"<companion_turn seq=4>", "（没有新消息）",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染结果缺少 %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{
		"<status>", "HP 10/12", companionOpeningHint, "角色面板", "被拒绝的笔记", "点名已变化时的提交",
	} {
		if strings.Contains(got, bad) {
			t.Errorf("渲染结果不该含 %q:\n%s", bad, got)
		}
	}
	if strings.Index(got, "更早的总结") > strings.Index(got, "seq=3") {
		t.Errorf("旧总结应排在最前面:\n%s", got)
	}
}

func TestCompanionSummarizesOldRunsBeforeNextRun(t *testing.T) {
	companionTestDB(t)
	seedCompanionHistory(t, 12)
	prov := &companionMemoryProvider{
		summaryReply:  "<summary>林默在码头调查失踪案，已多次行动。</summary>",
		toolResponses: []llm.ToolChatResult{companionSubmitRound("s", `{"text":"我继续观察"}`)},
	}
	useCompanionHandleWindow(t, prov, companionMemoryWindow)
	env := newFakeCompanionEnv()

	if err := (DefaultCompanion{}).RunCompanion(context.Background(), env); err != nil {
		t.Fatalf("RunCompanion: %v", err)
	}
	assertSubmits(t, env, "我继续观察")

	if len(prov.summaryInputs) != 1 {
		t.Fatalf("应当总结一次, 实际 %d 次", len(prov.summaryInputs))
	}
	input := prov.summaryInputs[0]
	if !strings.Contains(input, "旧消息0 ") || !strings.Contains(input, "[你的回应] 行动0") {
		t.Errorf("总结输入应含最早的消息和回应:\n%.300s", input)
	}
	if strings.Contains(input, "HP 10/12") {
		t.Errorf("总结输入不该含状态快照:\n%.300s", input)
	}

	sent := prov.toolMsgs[0]
	const headLen = 2
	if !strings.HasPrefix(sent[headLen].Content, "<story_so_far>\n林默在码头调查失踪案") {
		t.Fatalf("历史开头应是总结: %.200q", sent[headLen].Content)
	}
	all := ""
	for _, m := range sent[headLen+1:] {
		all += m.Content
	}
	if strings.Contains(all, "旧消息0 ") {
		t.Error("已总结的旧轮不该再原样出现")
	}
	if !strings.Contains(all, "旧消息11 ") {
		t.Error("最近的轮应原样保留")
	}

	turns := companionTurnsOf(t)
	if turns[0].Kind != "summary" || len(turns) >= 13 {
		t.Fatalf("transcript 应以总结开头且比 13 个单元少: kind=%q turns=%d", turns[0].Kind, len(turns))
	}
	if last := turns[len(turns)-1]; last.Kind == "summary" || !strings.Contains(last.Messages[0].Content, "<companion_turn seq=12>") {
		t.Errorf("最后一个单元应是本次运行: %+v", last)
	}
}

func TestCompanionSummaryFailureKeepsHistoryAndStillSubmits(t *testing.T) {
	companionTestDB(t)
	seedCompanionHistory(t, 12)
	prov := &companionMemoryProvider{
		summaryErr:    errors.New("upstream down"),
		toolResponses: []llm.ToolChatResult{companionSubmitRound("s", `{"text":"我继续观察"}`)},
	}
	useCompanionHandleWindow(t, prov, companionMemoryWindow)
	env := newFakeCompanionEnv()

	if err := (DefaultCompanion{}).RunCompanion(context.Background(), env); err != nil {
		t.Fatalf("RunCompanion: %v", err)
	}
	assertSubmits(t, env, "我继续观察")
	if len(prov.summaryInputs) != summaryMaxAttempts {
		t.Errorf("总结应重试到上限 %d 次, 实际 %d", summaryMaxAttempts, len(prov.summaryInputs))
	}
	turns := companionTurnsOf(t)
	if len(turns) != 13 || turns[0].Kind == "summary" {
		t.Errorf("总结失败时历史应原样保留并追加本次运行: turns=%d first=%q", len(turns), turns[0].Kind)
	}
}

func TestCompanionNoSummaryBelowThreshold(t *testing.T) {
	companionTestDB(t)
	seedCompanionHistory(t, 2)
	prov := &companionMemoryProvider{
		summaryReply:  "<summary>不该被用到</summary>",
		toolResponses: []llm.ToolChatResult{companionSubmitRound("s", `{"text":"我继续观察"}`)},
	}
	useCompanionHandleWindow(t, prov, companionMemoryWindow)

	if err := (DefaultCompanion{}).RunCompanion(context.Background(), newFakeCompanionEnv()); err != nil {
		t.Fatalf("RunCompanion: %v", err)
	}
	if len(prov.summaryInputs) != 0 {
		t.Errorf("未超阈值不该总结, 实际调用 %d 次", len(prov.summaryInputs))
	}
	if turns := companionTurnsOf(t); len(turns) != 3 {
		t.Errorf("turns = %d, 想要 3", len(turns))
	}
}

func TestCompanionSummarizesOnceWhenContextFillsMidRun(t *testing.T) {
	companionTestDB(t)
	seedCompanionHistory(t, 3)
	full := llm.Usage{PromptTokens: 5000, OutputTokens: 100}
	prov := &companionMemoryProvider{
		summaryReply: "<summary>已有的经历</summary>",
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("v1", companionToolViewCharacter, `{}`)}, Usage: full},
			{ToolCalls: []llm.ToolCall{fakeToolCall("v2", companionToolViewParty, `{}`)}, Usage: full},
			{ToolCalls: []llm.ToolCall{fakeToolCall("s", companionToolSubmitAction, `{"text":"我继续观察"}`)}, Usage: full},
		},
	}
	useCompanionHandleWindow(t, prov, companionMemoryWindow)
	env := newFakeCompanionEnv()

	if err := (DefaultCompanion{}).RunCompanion(context.Background(), env); err != nil {
		t.Fatalf("RunCompanion: %v", err)
	}
	assertSubmits(t, env, "我继续观察")
	if len(prov.summaryInputs) != 1 {
		t.Fatalf("一次运行内只总结一次, 实际 %d 次", len(prov.summaryInputs))
	}
	if len(prov.toolMsgs) != 3 {
		t.Fatalf("应当调用 3 次, 实际 %d", len(prov.toolMsgs))
	}
	if strings.Contains(prov.toolMsgs[0][2].Content, "<story_so_far>") {
		t.Error("第一次调用前历史还没有被总结")
	}
	second := prov.toolMsgs[1]
	if !strings.HasPrefix(second[2].Content, "<story_so_far>\n已有的经历") {
		t.Fatalf("第二次调用的历史应以总结开头: %.200q", second[2].Content)
	}
	// 总结之后本次运行已有的 opening、调用和结果原样保留，且 tool 结果仍紧跟它的调用。
	if tail := second[3:]; len(tail) != 3 || !strings.HasPrefix(tail[0].Content, "<companion_turn seq=3>") ||
		len(tail[1].ToolCalls) != 1 || tail[2].Role != "tool" || tail[2].ToolCallID != "v1" {
		t.Errorf("总结后的本次运行尾巴不对: %+v", tail)
	}

	turns := companionTurnsOf(t)
	if turns[0].Kind != "summary" {
		t.Errorf("transcript 应以总结开头, 实际 %q", turns[0].Kind)
	}
	last := turns[len(turns)-1]
	if len(last.Messages) != 7 || !strings.HasPrefix(last.Messages[0].Content, "<companion_turn seq=3>") {
		t.Errorf("本次运行应完整落库为最后一个单元(opening + 3 轮调用与结果): %d 条消息", len(last.Messages))
	}
}

func TestCompanionMidRunSummaryFailureIsNotRetried(t *testing.T) {
	companionTestDB(t)
	seedCompanionHistory(t, 3)
	full := llm.Usage{PromptTokens: 5000, OutputTokens: 100}
	prov := &companionMemoryProvider{
		summaryErr: errors.New("upstream down"),
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("v1", companionToolViewCharacter, `{}`)}, Usage: full},
			{ToolCalls: []llm.ToolCall{fakeToolCall("v2", companionToolViewParty, `{}`)}, Usage: full},
			{ToolCalls: []llm.ToolCall{fakeToolCall("s", companionToolSubmitAction, `{"text":"我继续观察"}`)}, Usage: full},
		},
	}
	useCompanionHandleWindow(t, prov, companionMemoryWindow)
	env := newFakeCompanionEnv()

	if err := (DefaultCompanion{}).RunCompanion(context.Background(), env); err != nil {
		t.Fatalf("RunCompanion: %v", err)
	}
	assertSubmits(t, env, "我继续观察")
	if len(prov.summaryInputs) != summaryMaxAttempts {
		t.Errorf("总结失败后同一次运行内不该再试: 调用 %d 次, 想要 %d", len(prov.summaryInputs), summaryMaxAttempts)
	}
}

func TestCompanionCommitRecordsLastContextTokens(t *testing.T) {
	companionTestDB(t)
	usage := llm.Usage{PromptTokens: 1234, OutputTokens: 56}
	prov := &companionMemoryProvider{
		toolResponses: []llm.ToolChatResult{{
			ToolCalls: []llm.ToolCall{fakeToolCall("s", companionToolSubmitAction, `{"text":"我继续观察"}`)},
			Usage:     usage,
		}},
	}
	useCompanionHandleWindow(t, prov, companionMemoryWindow)

	if err := (DefaultCompanion{}).RunCompanion(context.Background(), newFakeCompanionEnv()); err != nil {
		t.Fatalf("RunCompanion: %v", err)
	}
	rec, _ := loadCompanionTranscript(t)
	stats := rec.Data.Data.Stats
	if len(stats) != 1 || stats[0].LastContextTokens != usage.ContextTokens() {
		t.Errorf("Stats = %+v, 想要 LastContextTokens=%d", stats, usage.ContextTokens())
	}
}
