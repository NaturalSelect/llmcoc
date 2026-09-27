// context_transcript_test.go 覆盖 context_transcript.go 的阈值判断、按整轮 trim 的
// 边界条件、llm.ChatMessage↔models.TranscriptMsg 转换的 reasoning 无损往返，以及
// ContextManager 的 Build/Observe/Commit 全流程——尤其是本次重设计的核心回归点：
// 第 2 轮 Build 的结果必须以第 1 轮的完整消息链为逐字段相同的前缀(CacheBreakpoint 除外，
// 断点位置本身跨轮移动是设计意图)。DB 用内存 SQLite(initAgentTestDB)，不依赖真实网络。
package agent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// makeTranscriptTurn 构造一个只含单条 assistant 消息、内容为 runeCount 个"字"的历史轮，
// 用于让 transcriptTurnRunes 的结果可精确预测。
func makeTranscriptTurn(seq, round, runeCount int) models.TranscriptTurn {
	return models.TranscriptTurn{Seq: seq, Round: round, Messages: []models.TranscriptMsg{
		{Role: "assistant", Content: strings.Repeat("字", runeCount)},
	}}
}

// makeDirectorTurn 构造一个 Director 风格的历史轮：opening 两条 user 消息(第1条是
// state+<player_turn>，第2条模拟<system-reminder>) + 一条带 ToolCalls 的 assistant
// 消息(并列调用roll_dice与response) + 两条对应的tool结果消息，用于压缩策略相关测试。
// rejected=true 时 response 对应的 tool 结果换成"SYSTEM REJECT"前缀，模拟该次调用
// 被批次策略拒绝、其参数不应被采纳进压缩后的结果。
func makeDirectorTurn(seq, round int, reply string, rejected bool) models.TranscriptTurn {
	responseResult := "已执行。"
	if rejected {
		responseResult = "SYSTEM REJECT: response.options 有 3 条，超过上限2条。"
	}
	return models.TranscriptTurn{Seq: seq, Round: round, Messages: []models.TranscriptMsg{
		{Role: "user", Content: fmt.Sprintf("<player_turn seq=%d>行动</player_turn>", seq)},
		{Role: "user", Content: "<system-reminder>更小seq的player_turn仅供参考</system-reminder>"},
		{Role: "assistant", ToolCalls: []models.TranscriptToolCall{
			{ID: "c1", Name: "roll_dice", Arguments: `{"dice":"1d100"}`},
			{ID: "c2", Name: "response", Arguments: fmt.Sprintf(`{"reply":%q}`, reply)},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "掷出57"},
		{Role: "tool", ToolCallID: "c2", Content: responseResult},
	}}
}

// msgEqualIgnoringBreakpoint 比较两条消息除 CacheBreakpoint 外的全部字段；断点位置
// 跨轮移动是设计意图，前缀稳定性测试里不应该把它当成"内容变化"。
func msgEqualIgnoringBreakpoint(a, b llm.ChatMessage) bool {
	a.CacheBreakpoint = false
	b.CacheBreakpoint = false
	return reflect.DeepEqual(a, b)
}

func msgsEqualIgnoringBreakpoint(a, b []llm.ChatMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !msgEqualIgnoringBreakpoint(a[i], b[i]) {
			return false
		}
	}
	return true
}

// ── contextReserve / contextOverThreshold ────────────────────────────────────

func TestContextReserve(t *testing.T) {
	cases := []struct {
		name   string
		window int64
		want   int64
	}{
		{"大窗口用固定预留20k", 300_000, 20_000},
		{"恰好等于阈值走比例分支", 200_000, 40_000},
		{"小窗口用20%比例", 50_000, 10_000},
		{"零窗口", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := contextReserve(tc.window); got != tc.want {
				t.Errorf("contextReserve(%d) = %d, want %d", tc.window, got, tc.want)
			}
		})
	}
}

func TestContextOverThreshold(t *testing.T) {
	cases := []struct {
		name   string
		window int64
		used   int64
		want   bool
	}{
		{"window<=0永不触发", 0, 1_000_000, false},
		{"负window永不触发", -1, 1_000_000, false},
		{"低于阈值", 100_000, 79_999, false},
		{"恰好等于阈值(临界)", 100_000, 80_000, true},
		{"超过阈值", 100_000, 90_000, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := contextOverThreshold(tc.window, tc.used); got != tc.want {
				t.Errorf("contextOverThreshold(%d, %d) = %v, want %v", tc.window, tc.used, got, tc.want)
			}
		})
	}
}

// ── rune 计数 ─────────────────────────────────────────────────────────────────

func TestChatMessagesRuneCount(t *testing.T) {
	msgs := []llm.ChatMessage{
		{Role: "user", Content: "你好"}, // 2 rune，若按字节计会是6
		{Role: "assistant", Content: "world", ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "roll_dice", Arguments: `{"a":1}`}, // 7 rune
		}},
	}
	// 2("你好") + 5("world") + 7(`{"a":1}`) = 14
	if got := chatMessagesRuneCount(msgs); got != 14 {
		t.Errorf("chatMessagesRuneCount = %d, want 14", got)
	}
}

func TestTranscriptTurnRunes(t *testing.T) {
	turn := models.TranscriptTurn{Seq: 0, Round: 1, Messages: []models.TranscriptMsg{
		{Role: "assistant", Content: "abc", ToolCalls: []models.TranscriptToolCall{
			{ID: "1", Name: "x", Arguments: "1234"},
		}},
		{Role: "tool", Content: "ok", ToolCallID: "1"},
	}}
	// 3("abc") + 4("1234") + 2("ok") = 9
	if got := transcriptTurnRunes(turn); got != 9 {
		t.Errorf("transcriptTurnRunes = %d, want 9", got)
	}
}

func TestContextEstimatedUsedTokens_FallsBackToRunes(t *testing.T) {
	msgs := []llm.ChatMessage{{Role: "user", Content: "12345"}} // 5 rune

	t.Run("usage全零时按rune估算兜底", func(t *testing.T) {
		if got := contextEstimatedUsedTokens(llm.Usage{}, msgs); got != 5 {
			t.Errorf("got %d, want 5", got)
		}
	})

	t.Run("有PromptTokens时直接用ContextTokens不看msgs", func(t *testing.T) {
		usage := llm.Usage{PromptTokens: 100, OutputTokens: 20}
		if got := contextEstimatedUsedTokens(usage, msgs); got != 120 {
			t.Errorf("got %d, want 120", got)
		}
	})

	t.Run("仅OutputTokens非零也走usage路径", func(t *testing.T) {
		usage := llm.Usage{OutputTokens: 20}
		if got := contextEstimatedUsedTokens(usage, msgs); got != 20 {
			t.Errorf("got %d, want 20", got)
		}
	})
}

// ── trimTranscriptTurns ───────────────────────────────────────────────────────

func TestTrimTranscriptTurns_NoTrimBelowThreshold(t *testing.T) {
	turns := []models.TranscriptTurn{
		makeTranscriptTurn(0, 1, 10_000),
		makeTranscriptTurn(1, 2, 10_000),
		makeTranscriptTurn(2, 3, 10_000),
	}
	// window=100_000 → lowWater=50_000；usedTokens=40_000 未过低水位，不应trim。
	got, dropped := trimTranscriptTurns(turns, 100_000, 40_000, 30_000)
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	if !reflect.DeepEqual(got, turns) {
		t.Errorf("got = %+v, want原样返回 %+v", got, turns)
	}
}

func TestTrimTranscriptTurns_DropsOldestTurnsToLowWater(t *testing.T) {
	turns := []models.TranscriptTurn{
		makeTranscriptTurn(0, 1, 30_000),
		makeTranscriptTurn(1, 2, 30_000),
		makeTranscriptTurn(2, 3, 30_000),
	}
	// window=100_000 → lowWater=50_000；usedTokens=totalRunes=90_000 → tokensPerRune=1。
	// 依次丢第1、2轮后 estimated=90_000-30_000-30_000=30_000<=50_000，停止，只剩第3轮。
	got, dropped := trimTranscriptTurns(turns, 100_000, 90_000, 90_000)
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
	if len(got) != 1 || got[0].Round != 3 {
		t.Errorf("got = %+v, want只剩第3轮", got)
	}
}

func TestTrimTranscriptTurns_NeverSplitsTurn(t *testing.T) {
	turns := []models.TranscriptTurn{
		makeTranscriptTurn(0, 1, 10_000),
		{Seq: 1, Round: 2, Messages: []models.TranscriptMsg{
			{Role: "assistant", Content: "assistant部分", ToolCalls: []models.TranscriptToolCall{
				{ID: "c1", Name: "roll_dice", Arguments: strings.Repeat("字", 5_000)},
			}},
			{Role: "tool", Content: strings.Repeat("字", 5_000), ToolCallID: "c1"},
		}},
	}
	// len(turns)-1=1，最多只能丢第1轮；第2轮即使被"看上"也只能整轮保留，不能拆开
	// 其内部 tool_call/tool 结果的配对。
	got, dropped := trimTranscriptTurns(turns, 100_000, 90_000, 90_000)
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if len(got) != 1 || got[0].Round != 2 {
		t.Fatalf("got = %+v, want只保留完整的第2轮", got)
	}
	if len(got[0].Messages) != 2 {
		t.Errorf("第2轮应保留完整2条消息(assistant+tool)，got %d条", len(got[0].Messages))
	}
}

func TestTrimTranscriptTurns_KeepsAtLeastOneTurn(t *testing.T) {
	turns := []models.TranscriptTurn{
		makeTranscriptTurn(0, 1, 1_000),
		makeTranscriptTurn(1, 2, 1_000),
		makeTranscriptTurn(2, 3, 1_000),
	}
	// usedTokens远大于window，即使丢光前面所有轮估算也降不到lowWater，也不能丢光。
	got, dropped := trimTranscriptTurns(turns, 100_000, 10_000_000, 3_000)
	if dropped != 2 {
		t.Fatalf("dropped = %d, want len(turns)-1=2", dropped)
	}
	if len(got) != 1 || got[0].Round != 3 {
		t.Errorf("got = %+v, want只保留最后1轮", got)
	}
}

func TestTrimTranscriptTurns_WindowZeroNeverTrims(t *testing.T) {
	turns := []models.TranscriptTurn{
		makeTranscriptTurn(0, 1, 1_000_000),
		makeTranscriptTurn(1, 2, 1_000_000),
	}
	got, dropped := trimTranscriptTurns(turns, 0, 999_999_999, 2_000_000)
	if dropped != 0 || !reflect.DeepEqual(got, turns) {
		t.Errorf("window=0时不应trim, dropped=%d got=%+v", dropped, got)
	}
}

func TestTrimTranscriptTurns_SingleTurnNeverTrimmed(t *testing.T) {
	turns := []models.TranscriptTurn{makeTranscriptTurn(0, 1, 1_000_000)}
	got, dropped := trimTranscriptTurns(turns, 100_000, 999_999, 1_000_000)
	if dropped != 0 || !reflect.DeepEqual(got, turns) {
		t.Errorf("只剩1轮时不应trim, dropped=%d got=%+v", dropped, got)
	}
}

// ── compactDirectorTurn：单轮压缩 ─────────────────────────────────────────────

func TestCompactDirectorTurn_SkipsTurnWithoutToolCalls(t *testing.T) {
	// 老会话按旧扁平transcript播种的seq=0种子轮：只有一条纯文本user消息，没有任何
	// ToolCalls，不应该被判定为"可压缩"——否则会把玩家的历史文本误当作可丢弃的
	// 工具调用中间产物。
	turn := models.TranscriptTurn{Seq: 0, Round: 0, Messages: []models.TranscriptMsg{
		{Role: "user", Content: "老会话的扁平历史文本，没有任何工具调用"},
	}}
	if _, ok := compactDirectorTurn(turn); ok {
		t.Errorf("没有ToolCalls的轮不应被判定为可压缩")
	}
}

func TestCompactDirectorTurn_ExtractsResultAndStripsToolCalls(t *testing.T) {
	turn := makeDirectorTurn(0, 1, "第1轮回复", false)
	got, ok := compactDirectorTurn(turn)
	if !ok {
		t.Fatalf("有ToolCalls的轮应可压缩")
	}
	if !got.Compacted {
		t.Errorf("压缩后应标记Compacted=true")
	}
	if got.Seq != turn.Seq || got.Round != turn.Round {
		t.Errorf("Seq/Round不应改变: got %+v, want Seq=%d Round=%d", got, turn.Seq, turn.Round)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("压缩后应只剩[首条][标记][结果]共3条消息, got %d条: %+v", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Content != turn.Messages[0].Content {
		t.Errorf("首条消息应原样保留opening的第1条: got %q, want %q", got.Messages[0].Content, turn.Messages[0].Content)
	}
	if got.Messages[0].Role != "user" || len(got.Messages[0].ToolCalls) != 0 {
		t.Errorf("首条消息不应携带ToolCalls: %+v", got.Messages[0])
	}
	for i, m := range got.Messages {
		if len(m.ToolCalls) != 0 {
			t.Errorf("压缩后消息[%d]不应残留任何ToolCalls: %+v", i, m)
		}
	}
	if !strings.Contains(got.Messages[1].Content, `<trimmed_tool_calls rounds="1" calls="2">`) {
		t.Errorf("标记应记录1轮共2次调用: got %q", got.Messages[1].Content)
	}
	if !strings.Contains(got.Messages[1].Content, "roll_dice×1") || !strings.Contains(got.Messages[1].Content, "response×1") {
		t.Errorf("标记应按工具名分别计数: got %q", got.Messages[1].Content)
	}
	if got.Messages[2].Role != "assistant" || !strings.Contains(got.Messages[2].Content, "第1轮回复") {
		t.Errorf("结果应保留response的reply: got %+v", got.Messages[2])
	}
}

func TestCompactDirectorTurn_RejectedResponseNotAdoptedButStillCounted(t *testing.T) {
	turn := makeDirectorTurn(0, 1, "被拒绝的回复", true)
	got, ok := compactDirectorTurn(turn)
	if !ok {
		t.Fatalf("有ToolCalls的轮应可压缩")
	}
	if strings.Contains(got.Messages[2].Content, "被拒绝的回复") {
		t.Errorf("被SYSTEM REJECT拒绝的response不应进入结果: got %q", got.Messages[2].Content)
	}
	if got.Messages[2].Content != "<turn_result>（本轮未产生结果）</turn_result>" {
		t.Errorf("没有其他生效结果时应输出占位说明: got %q", got.Messages[2].Content)
	}
	if !strings.Contains(got.Messages[1].Content, `calls="2"`) {
		t.Errorf("被拒绝的调用仍应计入标记的调用总数: got %q", got.Messages[1].Content)
	}
}

func TestCompactDirectorTurn_NoResultPlaceholderWhenNoTerminalCall(t *testing.T) {
	// 只调用了roll_dice、没有response/write/end_game(如round<=1硬失败提前中断)：
	// 三个字段都提取不到值，应输出占位说明而不是空字符串或崩溃。
	turn := models.TranscriptTurn{Seq: 0, Round: 1, Messages: []models.TranscriptMsg{
		{Role: "user", Content: "<player_turn seq=0>行动</player_turn>"},
		{Role: "assistant", ToolCalls: []models.TranscriptToolCall{
			{ID: "c1", Name: "roll_dice", Arguments: `{"dice":"1d100"}`},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "掷出57"},
	}}
	got, ok := compactDirectorTurn(turn)
	if !ok {
		t.Fatalf("有ToolCalls的轮应可压缩")
	}
	if got.Messages[2].Content != "<turn_result>（本轮未产生结果）</turn_result>" {
		t.Errorf("没有终止性工具调用时应输出占位说明: got %q", got.Messages[2].Content)
	}
	if !strings.Contains(got.Messages[1].Content, `calls="1"`) || !strings.Contains(got.Messages[1].Content, "roll_dice×1") {
		t.Errorf("标记应仍然统计roll_dice: got %q", got.Messages[1].Content)
	}
}

// ── compactTranscriptTurns：整体压缩循环 ──────────────────────────────────────

func TestCompactTranscriptTurns_NoCompactBelowLowWater(t *testing.T) {
	turns := []models.TranscriptTurn{
		makeDirectorTurn(0, 1, "回复1", false),
		makeDirectorTurn(1, 2, "回复2", false),
	}
	// usedTokens/totalRunes都很小，远低于lowWater(window的一半)，不应压缩。
	got, n := compactTranscriptTurns(turns, 100_000, 1_000, 1_000)
	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
	if !reflect.DeepEqual(got, turns) {
		t.Errorf("未超阈值时应原样返回: got %+v, want %+v", got, turns)
	}
}

func TestCompactTranscriptTurns_CompactsOldestButNeverDropsOrTouchesLastTurn(t *testing.T) {
	turns := []models.TranscriptTurn{
		makeDirectorTurn(0, 1, "第1轮回复", false),
		makeDirectorTurn(1, 2, "第2轮回复", false),
		makeDirectorTurn(2, 3, "第3轮回复", false),
	}
	original := append([]models.TranscriptTurn(nil), turns...)
	// window=1、usedTokens远大于totalRunes：tokensPerRune被拉得很大，estimated长期
	// 停留在远高于lowWater(0.5)的水平，不依赖具体字符数计算即可确定性地让循环处理
	// 完所有"非最后一轮"的下标(i=0,1)，只是不会丢弃任何一轮(压缩策略的核心区别)。
	got, n := compactTranscriptTurns(turns, 1, 1_000_000, 1_000)
	if n != 2 {
		t.Fatalf("n = %d, want 2(两个非最后一轮都应被压缩)", n)
	}
	if !reflect.DeepEqual(turns, original) {
		t.Errorf("compactTranscriptTurns不应修改入参切片本身")
	}
	if len(got) != 3 {
		t.Fatalf("压缩策略不应改变轮数, got %d轮: %+v", len(got), got)
	}
	if !got[0].Compacted || !got[1].Compacted {
		t.Errorf("最旧的两轮都应被标记为已压缩: %+v", got)
	}
	if got[2].Compacted || !reflect.DeepEqual(got[2], turns[2]) {
		t.Errorf("最后一轮永远原样保留、不应被压缩: %+v", got[2])
	}
}

func TestCompactTranscriptTurns_IdempotentOnAlreadyCompactedTurns(t *testing.T) {
	turns := []models.TranscriptTurn{
		makeDirectorTurn(0, 1, "第1轮回复", false),
		makeDirectorTurn(1, 2, "第2轮回复", false),
	}
	once, n1 := compactTranscriptTurns(turns, 1, 1_000_000, 1_000)
	if n1 == 0 {
		t.Fatalf("第1次调用应至少压缩1轮")
	}
	twice, n2 := compactTranscriptTurns(once, 1, 1_000_000, 1_000)
	if n2 != 0 {
		t.Errorf("对已压缩过的轮不应重复压缩, n2 = %d", n2)
	}
	if !reflect.DeepEqual(once, twice) {
		t.Errorf("重复调用不应改变结果:\nonce  = %+v\ntwice = %+v", once, twice)
	}
}

// ── llm.ChatMessage ↔ models.TranscriptMsg：reasoning 无损往返 ────────────────

func TestLlmMsgToTranscript_PreservesReasoning(t *testing.T) {
	msg := llm.ChatMessage{
		Role:    "assistant",
		Content: "let me check",
		ToolCalls: []llm.ToolCall{
			{ID: "call1", Name: "roll_dice", Arguments: `{"sides":6}`},
		},
		ReasoningBlocks: []llm.ReasoningBlock{
			{Type: "thinking", Text: "secret", Signature: "sig"},
			{Type: "redacted_thinking", Data: "opaque=="},
		},
		Reasoning: "chain of thought",
	}

	tm := llmMsgToTranscript(msg)
	if tm.Reasoning != msg.Reasoning {
		t.Errorf("Reasoning未保留: got %q, want %q", tm.Reasoning, msg.Reasoning)
	}
	if len(tm.ReasoningBlocks) != 2 {
		t.Fatalf("ReasoningBlocks未保留: %+v", tm.ReasoningBlocks)
	}

	back := transcriptMsgToLLM(tm)
	if !msgEqualIgnoringBreakpoint(back, msg) {
		t.Errorf("往返后不一致:\n got  = %+v\n want = %+v", back, msg)
	}
}

func TestFlattenTranscriptTurns_RoundtripPreservesReasoning(t *testing.T) {
	original := []llm.ChatMessage{
		{Role: "assistant", Content: "查一下规则", ToolCalls: []llm.ToolCall{
			{ID: "call1", Name: "check_rule", Arguments: `{"q":"疯狂"}`},
		}, ReasoningBlocks: []llm.ReasoningBlock{{Type: "thinking", Text: "推理中", Signature: "sig1"}}},
		{Role: "tool", Content: "规则内容...", ToolCallID: "call1"},
		{Role: "assistant", Content: "好的，继续", Reasoning: "明文推理"},
	}

	turn := models.TranscriptTurn{Seq: 0, Round: 1, Messages: llmMsgsToTranscript(original)}
	got := flattenTranscriptTurns([]models.TranscriptTurn{turn})

	if len(got) != len(original) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(original))
	}
	for i, want := range original {
		if !msgEqualIgnoringBreakpoint(got[i], want) {
			t.Errorf("msg[%d] = %+v, want %+v", i, got[i], want)
		}
	}
}

// ── ContextManager.Build：断点标记 ─────────────────────────────────────────────

func TestContextManager_Build_HeadOnlyBreakpointWhenHistoryEmpty(t *testing.T) {
	cm := &ContextManager{
		sessionID: 1,
		agentKey:  "director",
		head:      []llm.ChatMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "scenario"}},
		opts:      ContextOptions{Window: 100_000},
	}
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "opening"})
	if len(msgs) != 3 {
		t.Fatalf("len(msgs) = %d, want 3", len(msgs))
	}
	if msgs[0].CacheBreakpoint {
		t.Errorf("head非末尾消息不应打断点: %+v", msgs[0])
	}
	if !msgs[1].CacheBreakpoint {
		t.Errorf("head末尾应打断点: %+v", msgs[1])
	}
	if msgs[2].CacheBreakpoint {
		t.Errorf("opening不应携带断点(由llm层自动追加): %+v", msgs[2])
	}
}

func TestContextManager_Build_MarksHeadAndHistoryEnds(t *testing.T) {
	cm := &ContextManager{
		sessionID: 1,
		agentKey:  "director",
		head:      []llm.ChatMessage{{Role: "system", Content: "sys"}},
		opts:      ContextOptions{Window: 100_000},
		data: models.TranscriptData{
			NextSeq: 2,
			Turns: []models.TranscriptTurn{
				makeTranscriptTurn(0, 1, 10),
				makeTranscriptTurn(1, 2, 10),
			},
		},
	}
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "opening"})
	// [head][turn0][turn1][opening] = 4条
	if len(msgs) != 4 {
		t.Fatalf("len(msgs) = %d, want 4", len(msgs))
	}
	if !msgs[0].CacheBreakpoint {
		t.Errorf("head末尾(index0)应打断点")
	}
	if msgs[1].CacheBreakpoint {
		t.Errorf("历史轮中间消息不应打断点: %+v", msgs[1])
	}
	if !msgs[2].CacheBreakpoint {
		t.Errorf("最后一个已提交历史轮末尾(index2)应打断点")
	}
	if msgs[3].CacheBreakpoint {
		t.Errorf("opening不应携带断点: %+v", msgs[3])
	}
	if cm.turnStart != 3 {
		t.Errorf("turnStart = %d, want 3", cm.turnStart)
	}
}

// ── ContextManager.Observe：阈值判断、整体trim、偏移量调整 ────────────────────

func TestContextManager_Observe_TrimsWholeTurnsAndAdjustsOffsets(t *testing.T) {
	cm := &ContextManager{
		sessionID: 1,
		agentKey:  "director",
		head:      []llm.ChatMessage{{Role: "system", Content: ""}},
		opts:      ContextOptions{Window: 100_000},
		data: models.TranscriptData{
			NextSeq: 3,
			Turns: []models.TranscriptTurn{
				makeTranscriptTurn(0, 1, 30_000),
				makeTranscriptTurn(1, 2, 30_000),
				makeTranscriptTurn(2, 3, 30_000),
			},
		},
	}
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: ""})
	// chatMessagesRuneCount(msgs) = 0+30000*3+0 = 90000。
	out := cm.Observe(llm.Usage{PromptTokens: 90_000}, msgs)

	if len(out) != 3 {
		t.Fatalf("len(out) = %d, want 3 (head+最后一轮+opening)", len(out))
	}
	if out[0].Role != "system" {
		t.Errorf("out[0]应是head, got %+v", out[0])
	}
	if out[1].Content != strings.Repeat("字", 30_000) {
		t.Errorf("out[1]应是保留的第3轮内容")
	}
	if !out[1].CacheBreakpoint {
		t.Errorf("trim后剩余历史轮末尾应保持断点")
	}
	if out[2].Role != "user" {
		t.Errorf("out[2]应是opening, got %+v", out[2])
	}
	if cm.historyLen != 1 {
		t.Errorf("historyLen = %d, want 1", cm.historyLen)
	}
	if cm.turnStart != 2 {
		t.Errorf("turnStart = %d, want 2", cm.turnStart)
	}
	if len(cm.data.Turns) != 1 || cm.data.Turns[0].Round != 3 {
		t.Errorf("data.Turns应只剩第3轮, got %+v", cm.data.Turns)
	}
	if cm.stat.TrimmedTurns != 2 {
		t.Errorf("stat.TrimmedTurns = %d, want 2", cm.stat.TrimmedTurns)
	}
	if cm.stat.Calls != 1 || cm.stat.PromptTokens != 90_000 {
		t.Errorf("stat未正确累计usage: %+v", cm.stat)
	}
}

func TestContextManager_Observe_AccumulatesAcrossMultipleCalls(t *testing.T) {
	cm := &ContextManager{
		sessionID: 1,
		agentKey:  "director",
		head:      []llm.ChatMessage{{Role: "system"}},
		opts:      ContextOptions{Window: 100_000},
	}
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "opening"})
	msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: "r1"})
	msgs = cm.Observe(llm.Usage{PromptTokens: 1000, OutputTokens: 10, CacheReadTokens: 900, CacheCreationTokens: 5}, msgs)
	msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: "r2"})
	msgs = cm.Observe(llm.Usage{PromptTokens: 2000, OutputTokens: 20, CacheReadTokens: 1900, CacheCreationTokens: 3}, msgs)

	if cm.stat.Calls != 2 {
		t.Errorf("Calls = %d, want 2", cm.stat.Calls)
	}
	if cm.stat.PromptTokens != 3000 || cm.stat.OutputTokens != 30 {
		t.Errorf("PromptTokens/OutputTokens累计错误: %+v", cm.stat)
	}
	if cm.stat.CacheReadTokens != 2800 || cm.stat.CacheCreationTokens != 8 {
		t.Errorf("Cache*Tokens累计错误: %+v", cm.stat)
	}
}

func TestContextManager_WindowZero_NeverTrims(t *testing.T) {
	cm := &ContextManager{
		sessionID: 1,
		agentKey:  "director",
		head:      []llm.ChatMessage{{Role: "system"}},
		opts:      ContextOptions{Window: 0},
		data: models.TranscriptData{
			Turns: []models.TranscriptTurn{
				makeTranscriptTurn(0, 1, 1_000_000),
				makeTranscriptTurn(1, 2, 1_000_000),
			},
		},
	}
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "opening"})
	out := cm.Observe(llm.Usage{PromptTokens: 999_999_999}, msgs)
	if !msgsEqualIgnoringBreakpoint(out, msgs) {
		t.Errorf("window=0时不应trim")
	}
	if len(cm.data.Turns) != 2 {
		t.Errorf("window=0时data.Turns不应改变, got %d轮", len(cm.data.Turns))
	}
}

func TestContextManager_RuneBudgetTrim(t *testing.T) {
	cm := &ContextManager{
		sessionID: 1,
		agentKey:  "npc:cat",
		head:      []llm.ChatMessage{{Role: "system"}},
		opts:      ContextOptions{RuneBudget: 100_000},
		data: models.TranscriptData{
			Turns: []models.TranscriptTurn{
				makeTranscriptTurn(0, 1, 40_000),
				makeTranscriptTurn(1, 2, 40_000),
			},
		},
	}
	msgs := cm.Build() // 无opening，只看历史trim
	// budget=100_000 → reserve=20_000 → 触发阈值=80_000；totalRunes=80_000恰好触发；
	// lowWater=50_000；丢第1轮(40_000)后剩40_000<=lowWater，停止。
	out := cm.Observe(llm.Usage{}, msgs)
	if len(cm.data.Turns) != 1 {
		t.Fatalf("data.Turns应剩1轮, got %d", len(cm.data.Turns))
	}
	if cm.data.Turns[0].Round != 2 {
		t.Errorf("应丢弃最旧的第1轮, got %+v", cm.data.Turns)
	}
	if len(out) != 2 {
		t.Errorf("out应只剩head+1条历史消息, got %d", len(out))
	}
}

// TestContextManager_Observe_CompactStrategyRebuildsHistory 验证Observe按
// ContextOptions.Strategy分派到compactTranscriptTurns、并正确重建历史消息段——与
// TrimDropTurns不同，压缩不会减少轮数，只会改变轮内消息条数，所以[headLen:headLen+
// historyLen]必须整段重新展开，不能像丢弃整轮那样简单地在旧msgs上做前缀切割。
func TestContextManager_Observe_CompactStrategyRebuildsHistory(t *testing.T) {
	turn1 := makeDirectorTurn(0, 1, "第1轮回复", false)
	turn2 := makeDirectorTurn(1, 2, "第2轮回复", false)
	turn3 := makeDirectorTurn(2, 3, "第3轮回复", false)
	cm := &ContextManager{
		sessionID: 1,
		agentKey:  "director",
		head:      []llm.ChatMessage{{Role: "system", Content: "sys"}},
		opts:      ContextOptions{Window: 1, Strategy: TrimCompactDirectorTurns},
		data: models.TranscriptData{
			NextSeq: 3,
			Turns:   []models.TranscriptTurn{turn1, turn2, turn3},
		},
	}
	opening := llm.ChatMessage{Role: "user", Content: "本轮opening"}
	msgs := cm.Build(opening)
	// window=1时任何非零usage都会越过阈值；usedTokens远大于总字符数，与
	// TestCompactTranscriptTurns_CompactsOldestButNeverDropsOrTouchesLastTurn同样的
	// 手法，确定性地让循环处理完两个非最后一轮的下标。
	out := cm.Observe(llm.Usage{PromptTokens: 1_000_000}, msgs)

	if cm.stat.CompactedTurns != 2 {
		t.Fatalf("stat.CompactedTurns = %d, want 2", cm.stat.CompactedTurns)
	}
	if len(cm.data.Turns) != 3 {
		t.Fatalf("压缩策略不应丢弃任何轮, got %d轮", len(cm.data.Turns))
	}
	if !cm.data.Turns[0].Compacted || !cm.data.Turns[1].Compacted {
		t.Fatalf("最旧的两轮都应被压缩: %+v", cm.data.Turns)
	}
	if cm.data.Turns[2].Compacted {
		t.Fatalf("最后一轮永远不应被压缩: %+v", cm.data.Turns[2])
	}

	wantHist := flattenTranscriptTurns(cm.data.Turns)
	if cm.historyLen != len(wantHist) {
		t.Errorf("historyLen = %d, want %d", cm.historyLen, len(wantHist))
	}
	if cm.turnStart != cm.headLen+len(wantHist) {
		t.Errorf("turnStart = %d, want %d", cm.turnStart, cm.headLen+len(wantHist))
	}
	wantOut := append(append([]llm.ChatMessage{}, cm.head...), wantHist...)
	wantOut = append(wantOut, opening)
	if !msgsEqualIgnoringBreakpoint(out, wantOut) {
		t.Errorf("out应等于head+压缩后历史展开+opening:\ngot  %+v\nwant %+v", out, wantOut)
	}
	if !out[cm.headLen-1].CacheBreakpoint {
		t.Errorf("head末尾应打断点")
	}
	if !out[cm.headLen+len(wantHist)-1].CacheBreakpoint {
		t.Errorf("压缩后历史末尾应打断点")
	}
	if out[len(out)-1].CacheBreakpoint {
		t.Errorf("本轮opening不应携带断点(由llm层自动追加)")
	}
}

// ── ContextManager：Build→Observe→Commit 全流程(需要DB) ───────────────────────

func TestContextManager_Commit_PersistsNewTurnAndStats(t *testing.T) {
	initAgentTestDB(t)

	cm := LoadContext(1, "director", []llm.ChatMessage{{Role: "system", Content: "sys"}}, ContextOptions{Window: 100_000})
	if !cm.IsEmpty() {
		t.Fatalf("初始transcript应为空")
	}
	if seq := cm.NextSeq(); seq != 0 {
		t.Fatalf("NextSeq() = %d, want 0", seq)
	}

	opening := llm.ChatMessage{Role: "user", Content: "第1轮玩家输入"}
	msgs := cm.Build(opening)
	assistant := llm.ChatMessage{
		Role: "assistant", Content: "第1轮回复",
		ReasoningBlocks: []llm.ReasoningBlock{{Type: "thinking", Text: "推理", Signature: "sig"}},
	}
	msgs = append(msgs, assistant)
	msgs = cm.Observe(llm.Usage{PromptTokens: 500, OutputTokens: 50, CacheReadTokens: 100}, msgs)

	cm.Commit(1, msgs)

	var rec models.AgentTranscript
	if err := models.DB.Where("session_id = ? AND agent_key = ?", 1, "director").First(&rec).Error; err != nil {
		t.Fatalf("读取持久化记录失败: %v", err)
	}
	if rec.Version != contextTranscriptVersion {
		t.Errorf("Version = %d, want %d", rec.Version, contextTranscriptVersion)
	}
	data := rec.Data.Data
	if len(data.Turns) != 1 {
		t.Fatalf("应有1个已提交轮次, got %d", len(data.Turns))
	}
	if data.Turns[0].Seq != 0 || data.Turns[0].Round != 1 {
		t.Errorf("第1轮 Seq/Round不对: %+v", data.Turns[0])
	}
	if len(data.Turns[0].Messages) != 2 {
		t.Fatalf("第1轮应保存opening+assistant共2条消息, got %d", len(data.Turns[0].Messages))
	}
	if data.Turns[0].Messages[1].Reasoning != "" || len(data.Turns[0].Messages[1].ReasoningBlocks) != 1 {
		t.Errorf("reasoning应原样归档, got %+v", data.Turns[0].Messages[1])
	}
	if data.NextSeq != 1 {
		t.Errorf("NextSeq = %d, want 1", data.NextSeq)
	}
	if len(data.Stats) != 1 || data.Stats[0].Calls != 1 || data.Stats[0].PromptTokens != 500 {
		t.Errorf("Stats未正确写入: %+v", data.Stats)
	}
}

// TestContextManager_TwoRunsPrefixStable 是本次重设计的核心回归测试：模拟两次独立
// run() (各自LoadContext一次)，断言第2轮 Build 的结果，以第1轮 Commit 时的完整消息链
// (含reasoning)为逐字段相同的前缀，且断点位置落在head末尾与上一轮末尾。
func TestContextManager_TwoRunsPrefixStable(t *testing.T) {
	initAgentTestDB(t)
	const sid = uint(7)
	head := []llm.ChatMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "scenario"}}

	// ── 第1轮 ──
	cm1 := LoadContext(sid, "director", head, ContextOptions{Window: 100_000})
	seq1 := cm1.NextSeq()
	if seq1 != 0 {
		t.Fatalf("第1轮NextSeq() = %d, want 0", seq1)
	}
	msgs1 := cm1.Build(llm.ChatMessage{Role: "user", Content: "<player_turn seq=0>行动1</player_turn>"})
	msgs1 = append(msgs1, llm.ChatMessage{
		Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "roll_dice", Arguments: `{"sides":100}`}},
		ReasoningBlocks: []llm.ReasoningBlock{{Type: "thinking", Text: "先查规则", Signature: "sig1"}},
	})
	msgs1 = cm1.Observe(llm.Usage{PromptTokens: 800, OutputTokens: 20}, msgs1)
	msgs1 = append(msgs1, llm.ChatMessage{Role: "tool", Content: "掷出57", ToolCallID: "c1"})
	msgs1 = append(msgs1, llm.ChatMessage{Role: "assistant", Content: "调查员成功找到线索。"})
	msgs1 = cm1.Observe(llm.Usage{PromptTokens: 900, OutputTokens: 15}, msgs1)
	cm1.Commit(1, msgs1)

	// ── 第2轮：完全独立的LoadContext，模拟另一次run() ──
	cm2 := LoadContext(sid, "director", head, ContextOptions{Window: 100_000})
	seq2 := cm2.NextSeq()
	if seq2 != 1 {
		t.Fatalf("第2轮NextSeq() = %d, want 1", seq2)
	}
	msgs2 := cm2.Build(llm.ChatMessage{Role: "user", Content: "<player_turn seq=1>行动2</player_turn>"})

	// 前缀断言：msgs2的前len(msgs1)条，必须与第1轮完整链路逐字段相同(断点除外)。
	if len(msgs2) < len(msgs1) {
		t.Fatalf("msgs2长度(%d)应不小于msgs1(%d)", len(msgs2), len(msgs1))
	}
	if !msgsEqualIgnoringBreakpoint(msgs2[:len(msgs1)], msgs1) {
		t.Errorf("前缀不稳定:\nmsgs2[:len(msgs1)] = %+v\nmsgs1 = %+v", msgs2[:len(msgs1)], msgs1)
	}
	// 新增的部分应恰好是本轮opening。
	wantTail := []llm.ChatMessage{{Role: "user", Content: "<player_turn seq=1>行动2</player_turn>"}}
	if !msgsEqualIgnoringBreakpoint(msgs2[len(msgs1):], wantTail) {
		t.Errorf("尾部应是本轮opening: got %+v", msgs2[len(msgs1):])
	}

	// reasoning必须原样保留在历史里，不能被剥离。history顺序是
	// [opening1][assistant(工具调用+reasoning)][tool结果][assistant(最终回复)]，
	// 紧跟在head之后，assistant在index=len(head)+1。
	assistantIdx := len(head) + 1
	if len(msgs2[assistantIdx].ToolCalls) != 1 {
		t.Fatalf("历史里assistant的ToolCalls丢失: %+v", msgs2[assistantIdx])
	}
	if len(msgs2[assistantIdx].ReasoningBlocks) != 1 || msgs2[assistantIdx].ReasoningBlocks[0].Text != "先查规则" {
		t.Errorf("历史里的ReasoningBlocks被剥离或损坏: %+v", msgs2[assistantIdx])
	}

	// 断点位置：head末尾(index1)、上一轮末尾(msgs1的最后一条，即len(msgs1)-1)。
	if !msgs2[len(head)-1].CacheBreakpoint {
		t.Errorf("head末尾应打断点")
	}
	if !msgs2[len(msgs1)-1].CacheBreakpoint {
		t.Errorf("上一轮末尾(index %d)应打断点", len(msgs1)-1)
	}
	if msgs2[len(msgs2)-1].CacheBreakpoint {
		t.Errorf("本轮opening不应携带断点(由llm层自动追加)")
	}
}

func TestContextManager_VersionMismatch_StartsEmpty(t *testing.T) {
	initAgentTestDB(t)

	stale := models.AgentTranscript{
		SessionID: 3,
		AgentKey:  "director",
		Version:   contextTranscriptVersion + 1,
		Data: models.JSONField[models.TranscriptData]{Data: models.TranscriptData{
			NextSeq: 5,
			Turns:   []models.TranscriptTurn{makeTranscriptTurn(0, 1, 10)},
		}},
	}
	if err := models.DB.Create(&stale).Error; err != nil {
		t.Fatalf("create stale transcript: %v", err)
	}

	cm := LoadContext(3, "director", []llm.ChatMessage{{Role: "system"}}, ContextOptions{Window: 100_000})
	if !cm.IsEmpty() {
		t.Errorf("Version不匹配时应视为空transcript")
	}
	if cm.NextSeq() != 0 {
		t.Errorf("NextSeq() = %d, want 0", cm.NextSeq())
	}
}

func TestContextManager_LoadContext_MissingRecordStartsEmpty(t *testing.T) {
	initAgentTestDB(t)

	cm := LoadContext(999, "writer", []llm.ChatMessage{{Role: "system"}}, ContextOptions{})
	if !cm.IsEmpty() || cm.NextSeq() != 0 {
		t.Errorf("查无记录时应从空transcript开始")
	}
}

func TestContextManager_NextSeq_IncrementsAcrossCommits(t *testing.T) {
	initAgentTestDB(t)
	const sid = uint(11)
	head := []llm.ChatMessage{{Role: "system"}}

	for i := 0; i < 3; i++ {
		cm := LoadContext(sid, "director", head, ContextOptions{})
		wantSeq := i
		if got := cm.NextSeq(); got != wantSeq {
			t.Fatalf("第%d次NextSeq() = %d, want %d", i, got, wantSeq)
		}
		msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "action"})
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: "reply"})
		cm.Commit(i+1, msgs)
	}
}
