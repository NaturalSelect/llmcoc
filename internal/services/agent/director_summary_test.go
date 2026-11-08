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

// summaryChatProvider 只实现 Chat：按序返回预设文本或错误，并记录收到的消息。
type summaryChatProvider struct {
	mu      sync.Mutex
	replies []string
	errs    []error
	calls   [][]llm.ChatMessage
}

func (p *summaryChatProvider) Chat(_ context.Context, _ string, msgs []llm.ChatMessage) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := len(p.calls)
	p.calls = append(p.calls, append([]llm.ChatMessage(nil), msgs...))
	if i < len(p.errs) && p.errs[i] != nil {
		return "", p.errs[i]
	}
	if i < len(p.replies) {
		return p.replies[i], nil
	}
	return "", errors.New("no more replies")
}

func (p *summaryChatProvider) ChatStream(context.Context, string, []llm.ChatMessage) (<-chan string, <-chan error, error) {
	return nil, nil, errors.New("unsupported")
}

func (p *summaryChatProvider) JsonChat(context.Context, string, []llm.ChatMessage) (string, error) {
	return "", nil
}

func (p *summaryChatProvider) ChatWithTools(context.Context, string, []llm.ChatMessage, []llm.ToolDefinition) (llm.ToolChatResult, error) {
	return llm.ToolChatResult{}, errors.New("unsupported")
}

var _ llm.Provider = (*summaryChatProvider)(nil)

// summaryTestTurns 构造 n 个 floor，每个 floor 一个单元：opening + narrate + ask_players。
func summaryTestTurns(n int) []models.TranscriptTurn {
	var turns []models.TranscriptTurn
	for f := 1; f <= n; f++ {
		turns = append(turns, models.TranscriptTurn{
			Seq: f - 1, Floor: f, Unit: 1,
			Messages: []models.TranscriptMsg{
				{Role: "user", Content: fmt.Sprintf("<player_turn floor=%d>行动%d</player_turn>", f, f)},
				{Role: "assistant", ToolCalls: []models.TranscriptToolCall{
					{ID: fmt.Sprintf("u%d", f), Name: "update_characters", Arguments: `{}`},
					{ID: fmt.Sprintf("r%d", f), Name: "narrate", Arguments: fmt.Sprintf(`{"reply":"回复%d"}`, f)},
				}},
				{Role: "tool", ToolCallID: fmt.Sprintf("u%d", f), Content: fmt.Sprintf("HP变化%d", f)},
				{Role: "tool", ToolCallID: fmt.Sprintf("r%d", f), Content: "已发给玩家"},
			},
		})
	}
	return turns
}

func newSummaryTestManager(turns []models.TranscriptTurn, window int64, lastCtx int64) *ContextManager {
	cm := &ContextManager{
		sessionID: 90,
		agentKey:  "director",
		head:      []llm.ChatMessage{{Role: "system", Content: "sys"}},
		opts:      ContextOptions{Window: window, Strategy: TrimSummarize},
		data:      models.TranscriptData{NextSeq: len(turns), NextFloor: len(turns) + 1, Turns: turns},
	}
	if lastCtx > 0 {
		cm.data.Stats = []models.TurnCacheStat{{LastContextTokens: lastCtx}}
	}
	return cm
}

func TestMaybeCompact_BelowThresholdDoesNothing(t *testing.T) {
	initAgentTestDB(t)
	cm := newSummaryTestManager(summaryTestTurns(6), 100_000, 1_000)
	called := false
	if cm.MaybeCompact(context.Background(), func(context.Context, string) (string, error) {
		called = true
		return "x", nil
	}) {
		t.Fatal("未超阈值不应改写历史")
	}
	if called || len(cm.data.Turns) != 6 {
		t.Fatalf("called=%v turns=%d", called, len(cm.data.Turns))
	}
}

func TestMaybeCompact_WindowZeroNeverTriggers(t *testing.T) {
	cm := newSummaryTestManager(summaryTestTurns(6), 0, 999_999_999)
	if cm.MaybeCompact(context.Background(), func(context.Context, string) (string, error) {
		t.Fatal("window=0 时不应调用总结")
		return "", nil
	}) {
		t.Fatal("window=0 时不应改写历史")
	}
}

func TestMaybeCompact_SummarizesOldestBeatsDownToLowWater(t *testing.T) {
	initAgentTestDB(t)
	turns := summaryTestTurns(6)
	var perTurn int64
	for _, tt := range turns {
		perTurn = max(perTurn, transcriptTurnRunes(tt))
	}
	// head 很小；用 lastCtx 让 tokensPerRune≈1。窗口取 3.5 个单元的字符数，低水位约 1.75 个单元，
	// 所以应该只保留最近 1 个单元，其余全部总结。
	window := perTurn * 7 / 2
	cm := newSummaryTestManager(turns, window, perTurn*6+chatMessagesRuneCount(cm0Head()))

	var gotInput string
	ok := cm.MaybeCompact(context.Background(), func(_ context.Context, in string) (string, error) {
		gotInput = in
		return "前五个周期的摘要", nil
	})
	if !ok {
		t.Fatal("应改写历史")
	}
	if len(cm.data.Turns) != 2 {
		t.Fatalf("应为 1 个总结 + 1 个保留的单元, got %d: %+v", len(cm.data.Turns), cm.data.Turns)
	}
	s := cm.data.Turns[0]
	if s.Kind != "summary" || s.Messages[0].Role != "user" ||
		!strings.Contains(s.Messages[0].Content, `<story_so_far floors="1-5">`) ||
		!strings.Contains(s.Messages[0].Content, "前五个周期的摘要") {
		t.Errorf("总结 turn 错误: %+v", s)
	}
	if got := cm.data.Turns[1]; got.Floor != 6 || got.Kind != "" {
		t.Errorf("保留的单元 = %+v, want floor 6 原样", got)
	}
	for _, want := range []string{"## 发言权周期 1", "[KP回复] 回复1", "[结算 update_characters] HP变化3", "行动2"} {
		if !strings.Contains(gotInput, want) {
			t.Errorf("总结输入缺少 %q:\n%s", want, gotInput)
		}
	}
	if strings.Contains(gotInput, "回复6") {
		t.Errorf("保留的单元不应进入总结输入:\n%s", gotInput)
	}
	if saved := loadTranscript(t, 90, "director"); len(saved.Turns) != 2 || saved.Turns[0].Kind != "summary" {
		t.Errorf("总结后的历史应已落库: %+v", saved.Turns)
	}
	if cm.stat.CompactedTurns != 5 {
		t.Errorf("CompactedTurns = %d, want 5", cm.stat.CompactedTurns)
	}
	if last := cm.data.Stats[len(cm.data.Stats)-1].LastContextTokens; last >= perTurn*6 {
		t.Errorf("总结后应覆盖旧的 LastContextTokens，避免下个周期重复触发: %d", last)
	}
}

func cm0Head() []llm.ChatMessage { return []llm.ChatMessage{{Role: "system", Content: "sys"}} }

func TestMaybeCompact_MergesExistingSummaryIntoNewOne(t *testing.T) {
	initAgentTestDB(t)
	turns := append([]models.TranscriptTurn{{
		Kind:      "summary",
		Compacted: true,
		Messages:  []models.TranscriptMsg{{Role: "user", Content: "<story_so_far floors=\"1-2\">更早的摘要</story_so_far>"}},
	}}, summaryTestTurns(6)[2:]...) // floor 3..6
	cm := newSummaryTestManager(turns, 1_000, 900)

	var gotInput string
	if !cm.MaybeCompact(context.Background(), func(_ context.Context, in string) (string, error) {
		gotInput = in
		return "合并后的摘要", nil
	}) {
		t.Fatal("应改写历史")
	}
	if !strings.Contains(gotInput, "更早的摘要") {
		t.Errorf("已有总结应作为输入的一部分:\n%s", gotInput)
	}
	if n := countKind(cm.data.Turns, "summary"); n != 1 || cm.data.Turns[0].Kind != "summary" {
		t.Errorf("总结 turn 应只有 1 个且在最前面: %+v", cm.data.Turns)
	}
}

func countKind(turns []models.TranscriptTurn, kind string) int {
	n := 0
	for _, t := range turns {
		if t.Kind == kind {
			n++
		}
	}
	return n
}

func TestMaybeCompact_OnlyASummaryDoesNothing(t *testing.T) {
	turns := []models.TranscriptTurn{{
		Kind: "summary", Compacted: true,
		Messages: []models.TranscriptMsg{{Role: "user", Content: "<story_so_far floors=\"1-2\">x</story_so_far>"}},
	}}
	cm := newSummaryTestManager(turns, 1_000, 900)
	if cm.MaybeCompact(context.Background(), func(context.Context, string) (string, error) {
		t.Fatal("只有一个旧总结时不应再总结它")
		return "", nil
	}) {
		t.Fatal("不应改写历史")
	}
}

func TestMaybeCompact_FailureKeepsHistoryUnchanged(t *testing.T) {
	initAgentTestDB(t)
	cm := newSummaryTestManager(summaryTestTurns(6), 1_000, 900)
	before := len(cm.data.Turns)
	if cm.MaybeCompact(context.Background(), func(context.Context, string) (string, error) {
		return "", errors.New("gateway down")
	}) {
		t.Fatal("总结失败不应报告已改写")
	}
	if len(cm.data.Turns) != before || countKind(cm.data.Turns, "summary") != 0 {
		t.Errorf("总结失败应保持原样: %+v", cm.data.Turns)
	}
}

// 同一拍里的 write 单元和 narrate 单元只能一起被总结，或者一起被保留。
func TestCompactHistory_NeverSplitsABeat(t *testing.T) {
	initAgentTestDB(t)
	mk := func(floor, unit int, tool, args string) models.TranscriptTurn {
		return models.TranscriptTurn{Floor: floor, Unit: unit, Messages: []models.TranscriptMsg{
			{Role: "assistant", ToolCalls: []models.TranscriptToolCall{{ID: tool, Name: tool, Arguments: args}}},
			{Role: "tool", ToolCallID: tool, Content: "ok"},
		}}
	}
	turns := []models.TranscriptTurn{
		mk(1, 1, "write", `{"direction":"拍1叙事"}`),
		mk(1, 2, "narrate", `{"reply":"拍1回复"}`),
		mk(1, 3, "write", `{"direction":"拍2叙事"}`),
		mk(1, 4, "narrate", `{"reply":"拍2回复"}`),
	}
	cm := newSummaryTestManager(turns, 1_000, 0)

	var gotInput string
	// tokensPerRune 极大：任何保留的单元都放不进低水位，应取 limit 内最大的边界。
	changed, err := cm.compactHistory(context.Background(), func(_ context.Context, in string) (string, error) {
		gotInput = in
		return "摘要", nil
	}, 3, 1000, 0)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if !strings.Contains(gotInput, "拍1叙事") || !strings.Contains(gotInput, "拍1回复") {
		t.Errorf("拍1 应整体被总结:\n%s", gotInput)
	}
	if strings.Contains(gotInput, "拍2叙事") {
		t.Errorf("limit=3 时切点不能落在拍2 的 write 与 narrate 之间:\n%s", gotInput)
	}
	if len(cm.data.Turns) != 3 || cm.data.Turns[0].Kind != "summary" || cm.data.Turns[1].Unit != 3 {
		t.Errorf("应为 总结 + 拍2 的两个单元: %+v", cm.data.Turns)
	}
}

func TestCompactHistory_RejectedResponseIsNotABoundary(t *testing.T) {
	initAgentTestDB(t)
	turns := []models.TranscriptTurn{
		{Floor: 1, Unit: 1, Messages: []models.TranscriptMsg{
			{Role: "assistant", ToolCalls: []models.TranscriptToolCall{{ID: "r", Name: "narrate", Arguments: `{"reply":"x"}`}}},
			{Role: "tool", ToolCallID: "r", Content: "SYSTEM REJECT: empty"},
		}},
		{Floor: 1, Unit: 2, Messages: []models.TranscriptMsg{
			{Role: "assistant", ToolCalls: []models.TranscriptToolCall{{ID: "w", Name: "write", Arguments: `{"direction":"d"}`}}},
			{Role: "tool", ToolCallID: "w", Content: "ok"},
		}},
	}
	cm := newSummaryTestManager(turns, 1_000, 0)
	changed, err := cm.compactHistory(context.Background(), func(context.Context, string) (string, error) {
		t.Fatal("被拒绝的 narrate 不是拍边界，limit=1 内没有可切的位置")
		return "", nil
	}, 1, 1, 0)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v, want false/nil", changed, err)
	}
}

func TestLegacyResponseCallStillClosesBeat(t *testing.T) {
	unit := models.TranscriptTurn{Floor: 1, Unit: 1, Messages: []models.TranscriptMsg{
		{Role: "assistant", ToolCalls: []models.TranscriptToolCall{{ID: "r", Name: "response", Arguments: `{"reply":"旧名回复"}`}}},
		{Role: "tool", ToolCallID: "r", Content: "已发给玩家"},
	}}
	if !turnClosesBeat(unit) {
		t.Error("老会话里名为 response 的调用仍应视为收尾了一拍")
	}
	if got := renderUnitOutputs(unit.Messages); !strings.Contains(got, "[KP回复] 旧名回复") {
		t.Errorf("老会话的 response 调用应被总结为 KP 回复, got %q", got)
	}
}
func TestDirectorSummarizer_RetriesAndExtractsTag(t *testing.T) {
	prov := &summaryChatProvider{
		errs:    []error{errors.New("timeout"), nil, nil},
		replies: []string{"", "没有标签的输出", "前言\n<summary>\n  事件A\n事件B\n</summary>\n后记"},
	}
	h := agentHandle{provider: prov, config: &models.AgentConfig{Role: models.AgentRoleDirector}, enabled: true}

	got, err := directorSummarizer(h, 1)(context.Background(), "输入文本")
	if err != nil {
		t.Fatalf("summarizer: %v", err)
	}
	if got != "事件A\n事件B" {
		t.Errorf("got %q", got)
	}
	if len(prov.calls) != 3 {
		t.Fatalf("应重试到第 3 次成功, calls=%d", len(prov.calls))
	}
	if prov.calls[0][0].Role != "system" || !strings.Contains(prov.calls[0][0].Content, "<summary>") ||
		prov.calls[0][1].Content != "输入文本" {
		t.Errorf("消息结构错误: %+v", prov.calls[0])
	}
}

func TestDirectorSummarizer_GivesUpAfterMaxAttempts(t *testing.T) {
	prov := &summaryChatProvider{replies: []string{"a", "b", "c", "<summary>late</summary>"}}
	h := agentHandle{provider: prov, config: &models.AgentConfig{Role: models.AgentRoleDirector}, enabled: true}
	if _, err := directorSummarizer(h, 1)(context.Background(), "x"); err == nil {
		t.Fatal("连续 3 次没有标签应失败")
	}
	if len(prov.calls) != summaryMaxAttempts {
		t.Errorf("calls=%d, want %d", len(prov.calls), summaryMaxAttempts)
	}
}

func TestDirectorSummarizer_StopsOnCancelledContext(t *testing.T) {
	prov := &summaryChatProvider{replies: []string{"<summary>x</summary>"}}
	h := agentHandle{provider: prov, config: &models.AgentConfig{Role: models.AgentRoleDirector}, enabled: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := directorSummarizer(h, 1)(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(prov.calls) != 0 {
		t.Error("已取消时不应调用 provider")
	}
}

func TestRenderFloorsForSummary_SkipsRejectedAndTrimmedMarkers(t *testing.T) {
	turns := []models.TranscriptTurn{{
		Floor: 1, Unit: 1,
		Messages: []models.TranscriptMsg{
			{Role: "user", Content: "<player_turn floor=1>行动</player_turn>\n"},
			{Role: "user", Content: "<system-reminder>开场的第二条提醒，不该进入总结</system-reminder>"},
			{Role: "user", Content: `<trimmed_tool_calls rounds="1" calls="1">query_clues×1</trimmed_tool_calls>`},
			{Role: "assistant", ToolCalls: []models.TranscriptToolCall{
				{ID: "a", Name: "narrate", Arguments: `{"reply":"被拒绝的回复"}`},
				{ID: "b", Name: "write", Arguments: `{"direction":"走廊很暗"}`},
				{ID: "c", Name: "ask_players", Arguments: `{"targets":["张三"],"question":"你怎么做？"}`},
				{ID: "d", Name: "roll_dice", Arguments: `{}`},
				{ID: "e", Name: "act_npc", Arguments: `{"npc_name":"老管家","question":"你看到了什么"}`},
				{ID: "f", Name: "check_rule", Arguments: `{}`},
			}},
			{Role: "tool", ToolCallID: "a", Content: "SYSTEM REJECT: options"},
			{Role: "tool", ToolCallID: "b", Content: "已记录"},
			{Role: "tool", ToolCallID: "c", Content: "已点名"},
			{Role: "tool", ToolCallID: "d", Content: "掷出57"},
			{Role: "tool", ToolCallID: "e", Content: "管家低声说他什么也没看见"},
			{Role: "tool", ToolCallID: "f", Content: "规则原文很长很长"},
			{Role: "user", Content: "<system-reminder>你已经是第 3 次发出完全相同的工具调用</system-reminder>"},
		},
	}}
	out := renderFloorsForSummary(turns)
	for _, bad := range []string{"被拒绝的回复", "trimmed_tool_calls", "开场的第二条提醒", "完全相同的工具调用", "规则原文"} {
		if strings.Contains(out, bad) {
			t.Errorf("不应包含 %q:\n%s", bad, out)
		}
	}
	for _, want := range []string{"[叙事方向] 走廊很暗", "[点名 张三] 你怎么做？", "行动", "[掷骰] 掷出57", "[NPC 老管家] 管家低声说他什么也没看见"} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %q:\n%s", want, out)
		}
	}
}

func TestRenderFloorsForSummary_KeepsPlainTextSeedTurn(t *testing.T) {
	turns := []models.TranscriptTurn{{
		Messages: []models.TranscriptMsg{{Role: "user", Content: "老会话的扁平历史文本"}},
	}}
	if out := renderFloorsForSummary(turns); !strings.Contains(out, "老会话的扁平历史文本") {
		t.Errorf("没有工具调用的种子轮应原样放入总结输入:\n%s", out)
	}
}

