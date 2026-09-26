// NOTE: director_history_test.go 覆盖 director_history.go 的阈值判断、按整轮 trim
// 的边界条件，以及 DB 优先加载/落盘的读写路径；DB 用内存 SQLite（initAgentTestDB），
// 不依赖真实网络。
package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// makeDirectorTurn 构造一个只含单条 assistant 消息、内容为 runeCount 个"字"的历史轮，
// 用于让 directorTurnRunes 的结果可精确预测。
func makeDirectorTurn(round int, runeCount int) models.DirectorTurn {
	return models.DirectorTurn{
		Round: round,
		Messages: []models.DirectorMsg{
			{Role: "assistant", Content: strings.Repeat("字", runeCount)},
		},
	}
}

// ── directorReserve / directorOverThreshold ──────────────────────────────────

func TestDirectorReserve(t *testing.T) {
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
			if got := directorReserve(tc.window); got != tc.want {
				t.Errorf("directorReserve(%d) = %d, want %d", tc.window, got, tc.want)
			}
		})
	}
}

func TestDirectorOverThreshold(t *testing.T) {
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
			if got := directorOverThreshold(tc.window, tc.used); got != tc.want {
				t.Errorf("directorOverThreshold(%d, %d) = %v, want %v", tc.window, tc.used, got, tc.want)
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

func TestDirectorTurnRunes(t *testing.T) {
	turn := models.DirectorTurn{Round: 1, Messages: []models.DirectorMsg{
		{Role: "assistant", Content: "abc", ToolCalls: []models.DirectorToolCall{
			{ID: "1", Name: "x", Arguments: "1234"},
		}},
		{Role: "tool", Content: "ok", ToolCallID: "1"},
	}}
	// 3("abc") + 4("1234") + 2("ok") = 9
	if got := directorTurnRunes(turn); got != 9 {
		t.Errorf("directorTurnRunes = %d, want 9", got)
	}
}

// ── directorEstimatedUsedTokens ───────────────────────────────────────────────

func TestDirectorEstimatedUsedTokens_FallsBackToRunes(t *testing.T) {
	msgs := []llm.ChatMessage{{Role: "user", Content: "12345"}} // 5 rune

	t.Run("usage全零时按rune估算兜底", func(t *testing.T) {
		if got := directorEstimatedUsedTokens(llm.Usage{}, msgs); got != 5 {
			t.Errorf("got %d, want 5", got)
		}
	})

	t.Run("有PromptTokens时直接用ContextTokens不看msgs", func(t *testing.T) {
		usage := llm.Usage{PromptTokens: 100, OutputTokens: 20}
		if got := directorEstimatedUsedTokens(usage, msgs); got != 120 {
			t.Errorf("got %d, want 120", got)
		}
	})

	t.Run("仅OutputTokens非零也走usage路径", func(t *testing.T) {
		usage := llm.Usage{OutputTokens: 20}
		if got := directorEstimatedUsedTokens(usage, msgs); got != 20 {
			t.Errorf("got %d, want 20", got)
		}
	})
}

// ── llmToDirectorMsgs / flattenDirectorTurns ─────────────────────────────────

func TestLlmToDirectorMsgs_StripsReasoning(t *testing.T) {
	msgs := []llm.ChatMessage{
		{
			Role:    "assistant",
			Content: "let me check",
			ToolCalls: []llm.ToolCall{
				{ID: "call1", Name: "roll_dice", Arguments: `{"sides":6}`},
			},
			ReasoningBlocks: []llm.ReasoningBlock{{Type: "thinking", Text: "secret", Signature: "sig"}},
			Reasoning:       "chain of thought",
		},
		{Role: "tool", Content: "rolled 4", ToolCallID: "call1"},
	}

	got := llmToDirectorMsgs(msgs)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Role != "assistant" || got[0].Content != "let me check" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if len(got[0].ToolCalls) != 1 || got[0].ToolCalls[0] != (models.DirectorToolCall{ID: "call1", Name: "roll_dice", Arguments: `{"sides":6}`}) {
		t.Errorf("ToolCalls不匹配: %+v", got[0].ToolCalls)
	}
	if got[1].Role != "tool" || got[1].ToolCallID != "call1" || got[1].Content != "rolled 4" {
		t.Errorf("got[1] = %+v", got[1])
	}
	// models.DirectorMsg 类型上根本没有 ReasoningBlocks/Reasoning 字段，
	// 编译期已保证归档时这两个字段被丢弃，无需再运行期断言。
}

func TestFlattenDirectorTurns_Roundtrip(t *testing.T) {
	original := []llm.ChatMessage{
		{Role: "assistant", Content: "查一下规则", ToolCalls: []llm.ToolCall{
			{ID: "call1", Name: "check_rule", Arguments: `{"q":"疯狂"}`},
		}},
		{Role: "tool", Content: "规则内容...", ToolCallID: "call1"},
		{Role: "assistant", Content: "好的，继续"},
	}

	turn := models.DirectorTurn{Round: 1, Messages: llmToDirectorMsgs(original)}
	got := flattenDirectorTurns([]models.DirectorTurn{turn})

	if len(got) != len(original) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(original))
	}
	for i, want := range original {
		g := got[i]
		if g.Role != want.Role || g.Content != want.Content || g.ToolCallID != want.ToolCallID {
			t.Errorf("msg[%d] = %+v, want role/content/toolCallID匹配 %+v", i, g, want)
		}
		if !reflect.DeepEqual(g.ToolCalls, want.ToolCalls) {
			t.Errorf("msg[%d].ToolCalls = %+v, want %+v", i, g.ToolCalls, want.ToolCalls)
		}
		if len(g.ReasoningBlocks) != 0 || g.Reasoning != "" {
			t.Errorf("msg[%d] 不应携带跨轮无效的reasoning: %+v", i, g)
		}
	}
}

// ── trimDirectorTurns ─────────────────────────────────────────────────────────

func TestTrimDirectorTurns_NoTrimBelowThreshold(t *testing.T) {
	turns := []models.DirectorTurn{
		makeDirectorTurn(1, 10_000),
		makeDirectorTurn(2, 10_000),
		makeDirectorTurn(3, 10_000),
	}
	// window=100_000 → lowWater=50_000；usedTokens=40_000 未过低水位，不应trim。
	got, dropped := trimDirectorTurns(turns, 100_000, 40_000, 30_000)
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	if !reflect.DeepEqual(got, turns) {
		t.Errorf("got = %+v, want原样返回 %+v", got, turns)
	}
}

func TestTrimDirectorTurns_DropsOldestTurnsToLowWater(t *testing.T) {
	turns := []models.DirectorTurn{
		makeDirectorTurn(1, 30_000),
		makeDirectorTurn(2, 30_000),
		makeDirectorTurn(3, 30_000),
	}
	// window=100_000 → lowWater=50_000；usedTokens=totalRunes=90_000 → tokensPerRune=1。
	// 依次丢第1、2轮后 estimated=90_000-30_000-30_000=30_000<=50_000，停止，只剩第3轮。
	got, dropped := trimDirectorTurns(turns, 100_000, 90_000, 90_000)
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
	if len(got) != 1 || got[0].Round != 3 {
		t.Errorf("got = %+v, want只剩第3轮", got)
	}
}

func TestTrimDirectorTurns_NeverSplitsTurn(t *testing.T) {
	turns := []models.DirectorTurn{
		makeDirectorTurn(1, 10_000),
		{Round: 2, Messages: []models.DirectorMsg{
			{Role: "assistant", Content: "assistant部分", ToolCalls: []models.DirectorToolCall{
				{ID: "c1", Name: "roll_dice", Arguments: strings.Repeat("字", 5_000)},
			}},
			{Role: "tool", Content: strings.Repeat("字", 5_000), ToolCallID: "c1"},
		}},
	}
	// len(turns)-1=1，最多只能丢第1轮；第2轮即使被"看上"也只能整轮保留，不能拆开
	// 其内部 tool_call/tool 结果的配对。
	got, dropped := trimDirectorTurns(turns, 100_000, 90_000, 90_000)
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

func TestTrimDirectorTurns_KeepsAtLeastOneTurn(t *testing.T) {
	turns := []models.DirectorTurn{
		makeDirectorTurn(1, 1_000),
		makeDirectorTurn(2, 1_000),
		makeDirectorTurn(3, 1_000),
	}
	// usedTokens远大于window，即使丢光前面所有轮估算也降不到lowWater，也不能丢光。
	got, dropped := trimDirectorTurns(turns, 100_000, 10_000_000, 3_000)
	if dropped != 2 {
		t.Fatalf("dropped = %d, want len(turns)-1=2", dropped)
	}
	if len(got) != 1 || got[0].Round != 3 {
		t.Errorf("got = %+v, want只保留最后1轮", got)
	}
}

func TestTrimDirectorTurns_WindowZeroNeverTrims(t *testing.T) {
	turns := []models.DirectorTurn{
		makeDirectorTurn(1, 1_000_000),
		makeDirectorTurn(2, 1_000_000),
	}
	got, dropped := trimDirectorTurns(turns, 0, 999_999_999, 2_000_000)
	if dropped != 0 || !reflect.DeepEqual(got, turns) {
		t.Errorf("window=0时不应trim, dropped=%d got=%+v", dropped, got)
	}
}

func TestTrimDirectorTurns_SingleTurnNeverTrimmed(t *testing.T) {
	turns := []models.DirectorTurn{makeDirectorTurn(1, 1_000_000)}
	got, dropped := trimDirectorTurns(turns, 100_000, 999_999, 1_000_000)
	if dropped != 0 || !reflect.DeepEqual(got, turns) {
		t.Errorf("只剩1轮时不应trim, dropped=%d got=%+v", dropped, got)
	}
}

// ── loadDirectorHistory / saveDirectorHistory ────────────────────────────────

func TestLoadDirectorHistory_PrefersDB(t *testing.T) {
	initAgentTestDB(t)

	dbTurns := []models.DirectorTurn{{Round: 1, Messages: []models.DirectorMsg{{Role: "assistant", Content: "来自DB"}}}}
	session := models.GameSession{DirectorHistory: models.JSONField[[]models.DirectorTurn]{Data: dbTurns}}
	if err := models.DB.Create(&session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}

	// gctx 里挂着一份过期快照，DB 优先意味着它不应该被使用。
	staleTurns := []models.DirectorTurn{{Round: 99, Messages: []models.DirectorMsg{{Role: "assistant", Content: "过期gctx快照"}}}}
	gctx := GameContext{Session: models.GameSession{ID: session.ID, DirectorHistory: models.JSONField[[]models.DirectorTurn]{Data: staleTurns}}}

	got := loadDirectorHistory(gctx)
	if len(got) != 1 || got[0].Messages[0].Content != "来自DB" {
		t.Errorf("应优先查库，got = %+v", got)
	}
}

func TestLoadDirectorHistory_FallsBackToGctxWhenDBMiss(t *testing.T) {
	initAgentTestDB(t)

	fallbackTurns := []models.DirectorTurn{{Round: 1, Messages: []models.DirectorMsg{{Role: "assistant", Content: "gctx兜底"}}}}
	gctx := GameContext{Session: models.GameSession{ID: 999999, DirectorHistory: models.JSONField[[]models.DirectorTurn]{Data: fallbackTurns}}}

	got := loadDirectorHistory(gctx)
	if len(got) != 1 || got[0].Messages[0].Content != "gctx兜底" {
		t.Errorf("查库未命中时应回落gctx，got = %+v", got)
	}
}

func TestSaveDirectorHistory_Persists(t *testing.T) {
	initAgentTestDB(t)

	session := models.GameSession{}
	if err := models.DB.Create(&session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}

	turns := []models.DirectorTurn{{Round: 1, Messages: []models.DirectorMsg{{Role: "assistant", Content: "保存我"}}}}
	saveDirectorHistory(session.ID, turns)

	var reloaded models.GameSession
	if err := models.DB.First(&reloaded, session.ID).Error; err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if len(reloaded.DirectorHistory.Data) != 1 || reloaded.DirectorHistory.Data[0].Messages[0].Content != "保存我" {
		t.Errorf("落盘后重读应看到新历史，got = %+v", reloaded.DirectorHistory.Data)
	}
}
