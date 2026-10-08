// NOTE: 覆盖 fantasy_provider.go 里"我们自己拥有"的逻辑：ChatMessage 与 fantasy.Prompt
// 互转（toPrompt）、cache breakpoint 精确落位（applyCacheBreakpoints）、工具 schema 转换
// （toFantasyInputSchema/toolCallInputJSON）、每次调用的 provider 专属选项与温度/思考互斥
// （buildProviderOptions/buildCall）、流式响应聚合（consumeStream）、usage 口径换算
// （usageFromFantasy）、重试判定（isRetryableFantasyError）。这些都是内存纯函数测试，不
// 涉及网络；fantasy 自身如何编解码 HTTP/SSE 由它自己的测试覆盖，这里不重复验证。
// 另外用两个端到端用例（真实走 charm.land/fantasy/providers/openaicompat，指向内存假
// HTTP 端点，SSE chunk 形状照抄 fantasy 自己测试里的 streamingMockServer）验证
// newFantasyProvider 的 BaseURL/APIKey 接线，以及"始终走 Stream 聚合 + 调用方负责重试"
// 没有接错——这一层纯单测覆盖不到。
// 禁止真实网络/真实LLM；全部基于内存假 HTTP 端点或手工构造的 fantasy.StreamResponse。
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	fantasyopenai "charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
)

// seqOf 把一组 fantasy.StreamPart 包装成 fantasy.StreamResponse（iter.Seq[StreamPart]），
// 用于在不发起真实网络请求的情况下测试 consumeStream 的聚合逻辑。
func seqOf(parts ...fantasy.StreamPart) fantasy.StreamResponse {
	return func(yield func(fantasy.StreamPart) bool) {
		for _, part := range parts {
			if !yield(part) {
				return
			}
		}
	}
}

// ---------- toolCallInputJSON / toFantasyInputSchema ----------

func TestToolCallInputJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"valid json object", `{"a":1}`, `{"a":1}`},
		{"empty string falls back", "", "{}"},
		{"whitespace falls back", "   ", "{}"},
		{"invalid json falls back", `{not json`, "{}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolCallInputJSON(tt.in); got != tt.want {
				t.Errorf("toolCallInputJSON(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestToFantasyInputSchema(t *testing.T) {
	t.Run("extracts properties and required as []string", func(t *testing.T) {
		schema := toFantasyInputSchema(json.RawMessage(`{
			"type": "object",
			"properties": {"question": {"type": "string"}},
			"required": ["question"]
		}`))
		if schema["type"] != "object" {
			t.Errorf("type = %v, want object", schema["type"])
		}
		required, ok := schema["required"].([]string)
		if !ok || len(required) != 1 || required[0] != "question" {
			t.Fatalf("required = %#v, want []string{\"question\"}", schema["required"])
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok || props["question"] == nil {
			t.Errorf("properties = %#v, want a question key", schema["properties"])
		}
	})

	t.Run("empty params default to empty object schema", func(t *testing.T) {
		schema := toFantasyInputSchema(nil)
		if schema["type"] != "object" {
			t.Errorf("type = %v, want object", schema["type"])
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok || len(props) != 0 {
			t.Errorf("properties = %#v, want empty map", schema["properties"])
		}
		if required, _ := schema["required"].([]string); len(required) != 0 {
			t.Errorf("required = %#v, want empty/nil", schema["required"])
		}
	})

	t.Run("blank type falls back to object", func(t *testing.T) {
		schema := toFantasyInputSchema(json.RawMessage(`{"type":"","properties":{}}`))
		if schema["type"] != "object" {
			t.Errorf("type = %v, want object fallback", schema["type"])
		}
	})
}

// ---------- anthropicEffortFromLevel / usageFromFantasy / sessionIDFromContext ----------

func TestAnthropicEffortFromLevel(t *testing.T) {
	tests := []struct {
		level      string
		wantEffort anthropic.Effort
		wantOK     bool
	}{
		{"low", anthropic.EffortLow, true},
		{"medium", anthropic.EffortMedium, true},
		{"high", anthropic.EffortHigh, true},
		{"xhigh", anthropic.EffortXHigh, true},
		{"max", anthropic.EffortMax, true},
		{"HIGH", anthropic.EffortHigh, true},
		{" high ", anthropic.EffortHigh, true},
		{"none", "", false},
		{"", "", false},
		{"unrecognized", "", false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.level), func(t *testing.T) {
			effort, ok := anthropicEffortFromLevel(tt.level)
			if ok != tt.wantOK || effort != tt.wantEffort {
				t.Errorf("anthropicEffortFromLevel(%q) = (%v, %v), want (%v, %v)", tt.level, effort, ok, tt.wantEffort, tt.wantOK)
			}
		})
	}
}

func TestUsageFromFantasy(t *testing.T) {
	got := usageFromFantasy(fantasy.Usage{InputTokens: 60, OutputTokens: 20, CacheReadTokens: 40, CacheCreationTokens: 5})
	want := Usage{PromptTokens: 105, OutputTokens: 20, CacheReadTokens: 40, CacheCreationTokens: 5}
	if got != want {
		t.Errorf("usageFromFantasy = %+v, want %+v", got, want)
	}

	if zero := usageFromFantasy(fantasy.Usage{}); zero != (Usage{}) {
		t.Errorf("usageFromFantasy(zero) = %+v, want zero value", zero)
	}
}

func TestSessionIDFromContext(t *testing.T) {
	if got := sessionIDFromContext(context.Background()); got != "" {
		t.Errorf("no value: got %q, want empty", got)
	}
	withSession := context.WithValue(context.Background(), "session", "sess-1")
	if got := sessionIDFromContext(withSession); got != "sess-1" {
		t.Errorf("got %q, want sess-1", got)
	}
	wrongType := context.WithValue(context.Background(), "session", 42)
	if got := sessionIDFromContext(wrongType); got != "" {
		t.Errorf("wrong type: got %q, want empty", got)
	}
}

// ---------- isRetryableFantasyError ----------

func TestIsRetryableFantasyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"429 retryable per fantasy built-in set", &fantasy.ProviderError{StatusCode: 429}, true},
		{"500 retryable per fantasy built-in set", &fantasy.ProviderError{StatusCode: 500}, true},
		{"401 not retryable", &fantasy.ProviderError{StatusCode: 401}, false},
		// 400/403/408 本身不在 fantasy 内置的可重试状态码集合里（fantasy 只认 408/409/429/5xx），
		// 是本项目历史上额外认为值得重试的网关错误码（见 retryCode4xx 注释），必须由我们自己的
		// 判断兜底，否则这三类错误会被误判为不可重试。
		{"400 retryable via project's extra retryCode4xx", &fantasy.ProviderError{StatusCode: 400}, true},
		{"403 retryable via project's extra retryCode4xx", &fantasy.ProviderError{StatusCode: 403}, true},
		{"408 retryable via both fantasy and extra set", &fantasy.ProviderError{StatusCode: 408}, true},
		{"transient flag forces retry regardless of status", &fantasy.ProviderError{StatusCode: 401, TransientError: true}, true},
		{"transport-level error fallback", fmt.Errorf("connection error: broken pipe"), true},
		{"plain unrelated error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableFantasyError(tt.err); got != tt.want {
				t.Errorf("isRetryableFantasyError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// ---------- toPrompt ----------

func TestToPrompt_SystemAndUserMessages(t *testing.T) {
	p := &fantasyProvider{isAnthropic: false}
	prompt, _ := p.toPrompt([]ChatMessage{
		{Role: "system", Content: "sys prompt"},
		{Role: "system", Content: "   "},
		{Role: "user", Content: "hello"},
		{Role: "user", Content: ""},
	})
	if len(prompt) != 2 {
		t.Fatalf("prompt = %+v, want 2 messages (blank system/user must be skipped)", prompt)
	}
	if prompt[0].Role != fantasy.MessageRoleSystem {
		t.Fatalf("prompt[0].Role = %v, want system", prompt[0].Role)
	}
	if text, ok := prompt[0].Content[0].(fantasy.TextPart); !ok || text.Text != "sys prompt" {
		t.Errorf("prompt[0] text = %+v, want %q", prompt[0].Content[0], "sys prompt")
	}
	if prompt[1].Role != fantasy.MessageRoleUser {
		t.Fatalf("prompt[1].Role = %v, want user", prompt[1].Role)
	}
	if text, ok := prompt[1].Content[0].(fantasy.TextPart); !ok || text.Text != "hello" {
		t.Errorf("prompt[1] text = %+v, want %q", prompt[1].Content[0], "hello")
	}
}

func TestToPrompt_AssistantTextAndToolCalls(t *testing.T) {
	p := &fantasyProvider{isAnthropic: false}
	prompt, _ := p.toPrompt([]ChatMessage{
		{Role: "assistant", Content: "sure, let me check", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "check_rule", Arguments: `{"q":"x"}`},
		}},
	})
	if len(prompt) != 1 {
		t.Fatalf("prompt = %+v, want 1 message", prompt)
	}
	parts := prompt[0].Content
	if len(parts) != 2 {
		t.Fatalf("parts = %+v, want [text, tool_call]", parts)
	}
	if text, ok := parts[0].(fantasy.TextPart); !ok || text.Text != "sure, let me check" {
		t.Errorf("parts[0] = %+v, want text %q", parts[0], "sure, let me check")
	}
	call, ok := parts[1].(fantasy.ToolCallPart)
	if !ok || call.ToolCallID != "call_1" || call.ToolName != "check_rule" || call.Input != `{"q":"x"}` {
		t.Errorf("parts[1] = %+v, want tool_call call_1/check_rule", parts[1])
	}
}

func TestToPrompt_EmptyAssistantMessageDropped(t *testing.T) {
	p := &fantasyProvider{isAnthropic: false}
	prompt, _ := p.toPrompt([]ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: ""},
		{Role: "user", Content: "still there?"},
	})
	if len(prompt) != 2 {
		t.Fatalf("prompt = %+v, want the empty assistant message dropped, leaving 2 user messages", prompt)
	}
}

func TestToPrompt_AnthropicReasoningBlocksRoundTrip(t *testing.T) {
	// NOTE: Anthropic 要求 thinking/redacted_thinking block 排在 assistant 消息内容最前面，
	// 且必须带上签名/加密数据才能通过多轮工具调用校验。
	p := &fantasyProvider{isAnthropic: true}
	prompt, _ := p.toPrompt([]ChatMessage{
		{
			Role:    "assistant",
			Content: "final answer",
			ReasoningBlocks: []ReasoningBlock{
				{Type: "thinking", Text: "thinking text", Signature: "sig-xyz"},
				{Type: "redacted_thinking", Data: "opaque-data"},
			},
		},
	})
	if len(prompt) != 1 {
		t.Fatalf("prompt = %+v, want 1 message", prompt)
	}
	parts := prompt[0].Content
	if len(parts) != 3 {
		t.Fatalf("parts = %+v, want [thinking, redacted_thinking, text]", parts)
	}
	thinking, ok := parts[0].(fantasy.ReasoningPart)
	if !ok || thinking.Text != "thinking text" {
		t.Fatalf("parts[0] = %+v, want thinking reasoning part", parts[0])
	}
	if meta := anthropic.GetReasoningMetadata(thinking.ProviderOptions); meta == nil || meta.Signature != "sig-xyz" {
		t.Errorf("thinking metadata = %+v, want signature sig-xyz", meta)
	}
	redacted, ok := parts[1].(fantasy.ReasoningPart)
	if !ok {
		t.Fatalf("parts[1] = %+v, want redacted_thinking reasoning part", parts[1])
	}
	if meta := anthropic.GetReasoningMetadata(redacted.ProviderOptions); meta == nil || meta.RedactedData != "opaque-data" {
		t.Errorf("redacted metadata = %+v, want RedactedData opaque-data", meta)
	}
	if text, ok := parts[2].(fantasy.TextPart); !ok || text.Text != "final answer" {
		t.Errorf("parts[2] = %+v, want text %q (must come after reasoning blocks)", parts[2], "final answer")
	}
}

func TestToPrompt_AnthropicReasoningOnlyAssistantSurvives(t *testing.T) {
	p := &fantasyProvider{isAnthropic: true}
	prompt, _ := p.toPrompt([]ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ReasoningBlocks: []ReasoningBlock{{Type: "thinking", Text: "t", Signature: "s"}}},
	})
	if len(prompt) != 2 {
		t.Fatalf("prompt = %+v, want the reasoning-only assistant message to survive", prompt)
	}
}

func TestToPrompt_OpenAICompatPlainReasoning(t *testing.T) {
	p := &fantasyProvider{isAnthropic: false}
	prompt, _ := p.toPrompt([]ChatMessage{
		{Role: "assistant", Content: "answer", Reasoning: "let me think first"},
	})
	parts := prompt[0].Content
	if len(parts) != 2 {
		t.Fatalf("parts = %+v, want [reasoning, text]", parts)
	}
	if reasoning, ok := parts[0].(fantasy.ReasoningPart); !ok || reasoning.Text != "let me think first" {
		t.Errorf("parts[0] = %+v, want plain reasoning text", parts[0])
	}
	if text, ok := parts[1].(fantasy.TextPart); !ok || text.Text != "answer" {
		t.Errorf("parts[1] = %+v, want text %q", parts[1], "answer")
	}
}

func TestToPrompt_ToolMessagesStaySeparate(t *testing.T) {
	// NOTE: 不同于旧实现手动合并连续 tool 消息，fantasy 的 Anthropic 实现会用
	// groupIntoBlocks 自动把连续 user/tool 消息分组进单条 Anthropic 请求消息，
	// 所以这里每条工具结果各自保持独立的 fantasy.Message，不需要手动合并。
	p := &fantasyProvider{isAnthropic: true}
	prompt, _ := p.toPrompt([]ChatMessage{
		{Role: "tool", ToolCallID: "call_1", Content: "rule result"},
		{Role: "tool", ToolCallID: "call_2", Content: "dice result"},
	})
	if len(prompt) != 2 {
		t.Fatalf("prompt = %+v, want 2 separate tool messages", prompt)
	}
	want := []struct{ id, content string }{{"call_1", "rule result"}, {"call_2", "dice result"}}
	for i, w := range want {
		if prompt[i].Role != fantasy.MessageRoleTool {
			t.Fatalf("prompt[%d].Role = %v, want tool", i, prompt[i].Role)
		}
		result, ok := prompt[i].Content[0].(fantasy.ToolResultPart)
		if !ok || result.ToolCallID != w.id {
			t.Fatalf("prompt[%d] = %+v, want tool_result for %q", i, prompt[i].Content[0], w.id)
		}
		if text, ok := result.Output.(fantasy.ToolResultOutputContentText); !ok || text.Text != w.content {
			t.Errorf("prompt[%d] output = %+v, want text %q", i, result.Output, w.content)
		}
	}
}

// ---------- applyCacheBreakpoints ----------

func toolCacheControl(t *testing.T, tool fantasy.Tool) *anthropic.CacheControl {
	t.Helper()
	ft, ok := tool.(fantasy.FunctionTool)
	if !ok {
		t.Fatalf("tool = %+v, want fantasy.FunctionTool", tool)
	}
	return anthropic.GetCacheControl(ft.ProviderOptions)
}

func TestApplyCacheBreakpoints_Placement(t *testing.T) {
	p := &fantasyProvider{isAnthropic: true}
	prompt, _ := p.toPrompt([]ChatMessage{
		{Role: "system", Content: "sys prompt"},
		{Role: "user", Content: "scenario context"},
		{Role: "user", Content: "current turn"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "check_rule", Arguments: "{}"},
			{ID: "call_2", Name: "roll_dice", Arguments: "{}"},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "rule result"},
		{Role: "tool", ToolCallID: "call_2", Content: "dice result"},
	})
	tools := toFantasyTools([]ToolDefinition{
		{Name: "tool_a", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "tool_b", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
	})

	applyCacheBreakpoints(prompt, tools, nil)

	if anthropic.GetCacheControl(prompt[0].ProviderOptions) == nil {
		t.Error("system message should be cache-marked")
	}
	if got := toolCacheControl(t, tools[0]); got != nil {
		t.Error("first tool should NOT be cache-marked")
	}
	if got := toolCacheControl(t, tools[1]); got == nil {
		t.Error("last tool should be cache-marked")
	}
	if anthropic.GetCacheControl(prompt[1].ProviderOptions) != nil {
		t.Error("earliest user message (scenario context) should NOT be cache-marked")
	}
	if anthropic.GetCacheControl(prompt[2].ProviderOptions) == nil {
		t.Error("second-to-last group (current turn) should be cache-marked")
	}
	if prompt[3].ProviderOptions != nil {
		t.Error("assistant message should never be cache-marked")
	}
	if anthropic.GetCacheControl(prompt[4].ProviderOptions) != nil {
		t.Error("first tool result in the last group should NOT be cache-marked")
	}
	if anthropic.GetCacheControl(prompt[5].ProviderOptions) == nil {
		t.Error("last tool result (last message of the last group) should be cache-marked")
	}

	marked := 0
	for _, m := range prompt {
		if anthropic.GetCacheControl(m.ProviderOptions) != nil {
			marked++
		}
	}
	if marked != 3 { // system + "current turn" 组 + 最后一个 tool-result 组，各 1 条
		t.Errorf("total cache-marked messages = %d, want 3", marked)
	}
}

func TestApplyCacheBreakpoints_SingleUserMessage(t *testing.T) {
	p := &fantasyProvider{isAnthropic: true}
	prompt, _ := p.toPrompt([]ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "only message"},
	})
	applyCacheBreakpoints(prompt, nil, nil) // 不应 panic/越界
	if anthropic.GetCacheControl(prompt[1].ProviderOptions) == nil {
		t.Error("the only user message should be cache-marked")
	}
}

// TestApplyCacheBreakpoints_ExplicitMarksTakePriorityOverHeuristic 验证有显式标记
// (ChatMessage.CacheBreakpoint)时改走"system+显式标记(最多2个)+prompt末尾一条"的规则，
// 不再使用启发式；这是ContextManager用来在head末尾/已提交历史轮末尾锚定稳定断点的机制。
func TestApplyCacheBreakpoints_ExplicitMarksTakePriorityOverHeuristic(t *testing.T) {
	p := &fantasyProvider{isAnthropic: true}
	prompt, explicitBreaks := p.toPrompt([]ChatMessage{
		{Role: "system", Content: "sys prompt"},
		{Role: "user", Content: "head", CacheBreakpoint: true},
		{Role: "user", Content: "turn1 opening"},
		{Role: "assistant", Content: "turn1 reply"},
		{Role: "user", Content: "turn2 opening", CacheBreakpoint: true},
		{Role: "assistant", Content: "turn2 reply(still streaming, not yet a breakpoint)"},
	})
	if len(explicitBreaks) != 2 {
		t.Fatalf("explicitBreaks = %v, want 2 entries", explicitBreaks)
	}
	tools := toFantasyTools([]ToolDefinition{{Name: "tool_a", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}})

	applyCacheBreakpoints(prompt, tools, explicitBreaks)

	if anthropic.GetCacheControl(prompt[0].ProviderOptions) == nil {
		t.Error("system message should be cache-marked")
	}
	if got := toolCacheControl(t, tools[0]); got != nil {
		t.Error("tools should NOT be cache-marked when explicit marks are present")
	}
	if anthropic.GetCacheControl(prompt[1].ProviderOptions) == nil {
		t.Error("head message (explicit breakpoint) should be cache-marked")
	}
	if anthropic.GetCacheControl(prompt[2].ProviderOptions) != nil {
		t.Error("turn1 opening (not marked) should NOT be cache-marked")
	}
	if anthropic.GetCacheControl(prompt[3].ProviderOptions) != nil {
		t.Error("turn1 reply (not marked) should NOT be cache-marked")
	}
	if anthropic.GetCacheControl(prompt[4].ProviderOptions) == nil {
		t.Error("turn2 opening (explicit breakpoint) should be cache-marked")
	}
	last := len(prompt) - 1
	if anthropic.GetCacheControl(prompt[last].ProviderOptions) == nil {
		t.Error("last message in the prompt should always be cache-marked (moving frontier)")
	}

	marked := 0
	for _, m := range prompt {
		if anthropic.GetCacheControl(m.ProviderOptions) != nil {
			marked++
		}
	}
	if marked != 4 { // system + head + turn2 opening + 末尾一条，合计4个(<=Anthropic上限)
		t.Errorf("total cache-marked messages = %d, want 4", marked)
	}
}

// TestApplyCacheBreakpoints_ExplicitMarksKeepOnlyLastTwo 验证显式标记超过2个时只取最后2个，
// 加上system与末尾一条，合计不超过Anthropic单次请求4个断点的上限。
func TestApplyCacheBreakpoints_ExplicitMarksKeepOnlyLastTwo(t *testing.T) {
	p := &fantasyProvider{isAnthropic: true}
	prompt, explicitBreaks := p.toPrompt([]ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "head", CacheBreakpoint: true},
		{Role: "user", Content: "turn1", CacheBreakpoint: true},
		{Role: "user", Content: "turn2", CacheBreakpoint: true},
		{Role: "user", Content: "turn3 opening"},
	})
	if len(explicitBreaks) != 3 {
		t.Fatalf("explicitBreaks = %v, want 3 entries", explicitBreaks)
	}

	applyCacheBreakpoints(prompt, nil, explicitBreaks)

	if anthropic.GetCacheControl(prompt[1].ProviderOptions) != nil {
		t.Error("oldest explicit mark(head) should be dropped when more than 2 marks exist")
	}
	if anthropic.GetCacheControl(prompt[2].ProviderOptions) == nil {
		t.Error("second-to-last explicit mark(turn1) should be kept")
	}
	if anthropic.GetCacheControl(prompt[3].ProviderOptions) == nil {
		t.Error("last explicit mark(turn2) should be kept")
	}

	marked := 0
	for _, m := range prompt {
		if anthropic.GetCacheControl(m.ProviderOptions) != nil {
			marked++
		}
	}
	if marked != 4 { // system + 2个显式标记 + 末尾一条
		t.Errorf("total cache-marked messages = %d, want 4", marked)
	}
}

// ---------- buildProviderOptions ----------

func TestBuildProviderOptions_Anthropic(t *testing.T) {
	tests := []struct {
		name            string
		reasoningEffort string
		cacheKey        string
		wantEffort      anthropic.Effort // 空串表示期望 nil
		wantUserID      string           // 空串表示期望没有 metadata
	}{
		{"no effort no cache key", "", "", "", ""},
		{"none effort treated as disabled", "none", "npc:1", "", "npc:1"},
		{"high effort", "high", "", anthropic.EffortHigh, ""},
		{"xhigh effort with cache key", "xhigh", "session-9", anthropic.EffortXHigh, "session-9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &fantasyProvider{isAnthropic: true, reasoningEffort: tt.reasoningEffort}
			po := p.buildProviderOptions(context.Background(), tt.cacheKey, false, false)
			ao, ok := po[anthropic.Name].(*anthropic.ProviderOptions)
			if !ok {
				t.Fatalf("provider options missing/wrong type: %#v", po)
			}
			if tt.wantEffort == "" {
				if ao.Effort != nil {
					t.Errorf("Effort = %v, want nil", *ao.Effort)
				}
			} else if ao.Effort == nil || *ao.Effort != tt.wantEffort {
				t.Errorf("Effort = %v, want %v", ao.Effort, tt.wantEffort)
			}
			if tt.wantUserID == "" {
				if ao.ExtraBody != nil {
					t.Errorf("ExtraBody = %v, want nil", ao.ExtraBody)
				}
			} else {
				md, ok := ao.ExtraBody["metadata"].(map[string]string)
				if !ok || md["user_id"] != tt.wantUserID {
					t.Errorf("ExtraBody metadata.user_id = %v, want %q", ao.ExtraBody, tt.wantUserID)
				}
			}
		})
	}
}

func TestBuildProviderOptions_AnthropicThinkingBudget(t *testing.T) {
	tests := []struct {
		name            string
		reasoningEffort string
		budgetTokens    int
		wantBudget      int64 // 0 表示期望不使用固定预算
		wantEffort      bool
	}{
		{"budget with thinking on uses fixed budget", "low", 2048, 2048, false},
		{"zero budget keeps adaptive effort", "high", 0, 0, true},
		{"budget ignored when thinking is none", "none", 2048, 0, false},
		{"budget ignored when thinking is unset", "", 2048, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &fantasyProvider{isAnthropic: true, reasoningEffort: tt.reasoningEffort, thinkingBudgetTokens: tt.budgetTokens}
			po := p.buildProviderOptions(context.Background(), "", false, false)
			ao, ok := po[anthropic.Name].(*anthropic.ProviderOptions)
			if !ok {
				t.Fatalf("provider options missing/wrong type: %#v", po)
			}
			if (ao.Effort != nil) != tt.wantEffort {
				t.Errorf("Effort set = %v, want %v", ao.Effort != nil, tt.wantEffort)
			}
			if tt.wantBudget == 0 {
				if ao.Thinking != nil {
					t.Errorf("Thinking = %+v, want nil", ao.Thinking)
				}
			} else if ao.Thinking == nil || ao.Thinking.BudgetTokens != tt.wantBudget {
				t.Errorf("Thinking = %+v, want budget %d", ao.Thinking, tt.wantBudget)
			}
		})
	}
}

func TestBuildCall_AnthropicFixedBudgetSuppressesTemperature(t *testing.T) {
	p := &fantasyProvider{isAnthropic: true, model: "claude-haiku-4-5", maxTokens: 4096, temperature: 0.65, reasoningEffort: "low", thinkingBudgetTokens: 2048}
	call := p.buildCall(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, false, nil)
	if call.Temperature != nil {
		t.Errorf("Temperature = %v, want nil when fixed-budget thinking is active", *call.Temperature)
	}
}

func TestBuildProviderOptions_OpenAICompat(t *testing.T) {
	sessionCtx := context.WithValue(context.Background(), "session", "sess-1")
	tests := []struct {
		name            string
		ctx             context.Context
		cacheKey        string
		jsonMode        bool
		hasTools        bool
		reasoningEffort string
		wantUser        string
		wantCacheKey    string
		wantResponseFmt bool
		wantEffort      string
	}{
		{name: "no session, no json: extra body empty", ctx: context.Background()},
		{name: "session sets user and prompt_cache_key", ctx: sessionCtx, wantUser: "sess-1", wantCacheKey: "sess-1"},
		{name: "explicit cacheKey overrides session fallback", ctx: sessionCtx, cacheKey: "npc:5", wantUser: "sess-1", wantCacheKey: "npc:5"},
		{name: "json mode without tools sets response_format", ctx: context.Background(), jsonMode: true, hasTools: false, wantResponseFmt: true},
		{name: "json mode with tools omits response_format", ctx: context.Background(), jsonMode: true, hasTools: true, wantResponseFmt: false},
		{name: "reasoning effort passed through", ctx: context.Background(), reasoningEffort: "medium", wantEffort: "medium"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &fantasyProvider{isAnthropic: false, reasoningEffort: tt.reasoningEffort}
			po := p.buildProviderOptions(tt.ctx, tt.cacheKey, tt.jsonMode, tt.hasTools)
			oc, ok := po[openaicompat.Name].(*openaicompat.ProviderOptions)
			if !ok {
				t.Fatalf("provider options missing/wrong type: %#v", po)
			}

			if tt.wantUser == "" {
				if oc.User != nil {
					t.Errorf("User = %v, want nil", *oc.User)
				}
			} else if oc.User == nil || *oc.User != tt.wantUser {
				t.Errorf("User = %v, want %q", oc.User, tt.wantUser)
			}

			if tt.wantEffort == "" {
				if oc.ReasoningEffort != nil {
					t.Errorf("ReasoningEffort = %v, want nil", *oc.ReasoningEffort)
				}
			} else if oc.ReasoningEffort == nil || *oc.ReasoningEffort != fantasyopenai.ReasoningEffort(tt.wantEffort) {
				t.Errorf("ReasoningEffort = %v, want %q", oc.ReasoningEffort, tt.wantEffort)
			}

			if tt.wantCacheKey == "" && !tt.wantResponseFmt {
				if oc.ExtraBody != nil {
					t.Fatalf("ExtraBody = %#v, want nil", oc.ExtraBody)
				}
				return
			}
			if oc.ExtraBody == nil {
				t.Fatalf("ExtraBody = nil, want populated")
			}
			if tt.wantCacheKey != "" {
				md, ok := oc.ExtraBody["metadata"].(map[string]string)
				if !ok || md["prompt_cache_key"] != tt.wantCacheKey {
					t.Errorf("metadata.prompt_cache_key = %v, want %q", oc.ExtraBody["metadata"], tt.wantCacheKey)
				}
			}
			_, hasResponseFmt := oc.ExtraBody["response_format"]
			if hasResponseFmt != tt.wantResponseFmt {
				t.Errorf("response_format present = %v, want %v", hasResponseFmt, tt.wantResponseFmt)
			}
		})
	}
}

// ---------- buildCall ----------

func TestBuildCall_AnthropicThinkingSuppressesTemperature(t *testing.T) {
	// NOTE: Anthropic 扩展思考与自定义 temperature 互斥，开启思考后传 temperature 会被 API 拒绝。
	p := &fantasyProvider{isAnthropic: true, model: "claude-x", maxTokens: 111, temperature: 0.65, reasoningEffort: "high"}
	call := p.buildCall(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, false, nil)
	if call.Temperature != nil {
		t.Errorf("Temperature = %v, want nil when extended thinking is active", *call.Temperature)
	}
	if call.MaxOutputTokens == nil || *call.MaxOutputTokens != 111 {
		t.Errorf("MaxOutputTokens = %v, want 111", call.MaxOutputTokens)
	}
}

func TestBuildCall_AnthropicNoThinkingKeepsTemperature(t *testing.T) {
	p := &fantasyProvider{isAnthropic: true, model: "claude-x", maxTokens: 111, temperature: 0.65}
	call := p.buildCall(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, false, nil)
	// NOTE: 用 float32 回转比较，避免 float32(0.65) 直接和 float64 字面量 0.65 因精度不同误判。
	if call.Temperature == nil || float32(*call.Temperature) != p.temperature {
		t.Errorf("Temperature = %v, want %v (as float64)", call.Temperature, p.temperature)
	}
}

func TestBuildCall_DisableTemperatureAlwaysOmitsIt(t *testing.T) {
	p := &fantasyProvider{isAnthropic: true, model: "claude-x", maxTokens: 111, temperature: 0.65, disableTemperature: true}
	call := p.buildCall(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, false, nil)
	if call.Temperature != nil {
		t.Errorf("Temperature = %v, want nil when disabled", *call.Temperature)
	}
}

func TestBuildCall_OpenAICompatTemperatureIgnoresReasoningEffort(t *testing.T) {
	p := &fantasyProvider{isAnthropic: false, model: "gpt-x", maxTokens: 50, temperature: 0.5, reasoningEffort: "high"}
	call := p.buildCall(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, false, nil)
	if call.Temperature == nil || *call.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5 (OpenAI branch never suppresses temperature for reasoning_effort)", call.Temperature)
	}
}

func TestBuildCall_CacheBreakpointsOnlyForAnthropic(t *testing.T) {
	messages := []ChatMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"}}

	anthropicCall := (&fantasyProvider{isAnthropic: true, maxTokens: 10}).buildCall(context.Background(), "", messages, false, nil)
	if anthropic.GetCacheControl(anthropicCall.Prompt[0].ProviderOptions) == nil {
		t.Error("anthropic branch should cache-mark the system message")
	}

	openAICall := (&fantasyProvider{isAnthropic: false, maxTokens: 10}).buildCall(context.Background(), "", messages, false, nil)
	for i, m := range openAICall.Prompt {
		if anthropic.GetCacheControl(m.ProviderOptions) != nil {
			t.Errorf("openai-compat branch must never cache-mark messages, but prompt[%d] is marked", i)
		}
	}
}

func TestBuildCall_ToolsConverted(t *testing.T) {
	p := &fantasyProvider{isAnthropic: false, maxTokens: 10}
	call := p.buildCall(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, false, []ToolDefinition{
		{Name: "check_rule", Parameters: json.RawMessage(`{"type":"object"}`)},
	})
	if len(call.Tools) != 1 {
		t.Fatalf("Tools = %+v, want 1", call.Tools)
	}
	if ft, ok := call.Tools[0].(fantasy.FunctionTool); !ok || ft.Name != "check_rule" {
		t.Errorf("Tools[0] = %+v, want FunctionTool named check_rule", call.Tools[0])
	}
}

// ---------- consumeStream ----------

func TestConsumeStream_TextAggregationAndCallback(t *testing.T) {
	p := &fantasyProvider{isAnthropic: false}
	stream := seqOf(
		fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, Delta: "Hello "},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, Delta: ""},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, Delta: "world"},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: fantasy.Usage{InputTokens: 10, OutputTokens: 2}},
	)
	var got []string
	res, err := p.consumeStream(stream, func(delta string) { got = append(got, delta) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.content != "Hello world" {
		t.Errorf("content = %q, want %q", res.content, "Hello world")
	}
	if len(got) != 2 || got[0] != "Hello " || got[1] != "world" {
		t.Errorf(`onText callbacks = %v, want ["Hello ", "world"] (empty delta must not invoke callback)`, got)
	}
	if res.finishReason != string(fantasy.FinishReasonStop) {
		t.Errorf("finishReason = %q, want %q", res.finishReason, fantasy.FinishReasonStop)
	}
	if res.usage.PromptTokens != 10 || res.usage.OutputTokens != 2 {
		t.Errorf("usage = %+v, want PromptTokens=10 OutputTokens=2", res.usage)
	}
}

func TestConsumeStream_AnthropicThinkingBlockCapturesSignature(t *testing.T) {
	p := &fantasyProvider{isAnthropic: true}
	stream := seqOf(
		fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningDelta, ID: "0", Delta: "let me think, "},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningDelta, ID: "0", Delta: "step by step"},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningEnd, ID: "0", ProviderMetadata: fantasy.ProviderMetadata{
			anthropic.Name: &anthropic.ReasoningOptionMetadata{Signature: "sig-abc"},
		}},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, Delta: "the answer"},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop},
	)
	res, err := p.consumeStream(stream, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.blocks) != 1 {
		t.Fatalf("blocks = %+v, want exactly 1 thinking block", res.blocks)
	}
	if res.blocks[0].Type != "thinking" || res.blocks[0].Text != "let me think, step by step" || res.blocks[0].Signature != "sig-abc" {
		t.Errorf("block = %+v, want thinking block with combined text and signature", res.blocks[0])
	}
	if res.reasoning != "" {
		t.Errorf("reasoning = %q, want empty (Anthropic branch stores structured blocks, not the plain-text field)", res.reasoning)
	}
	if res.content != "the answer" {
		t.Errorf("content = %q, want %q", res.content, "the answer")
	}
}

func TestConsumeStream_AnthropicDropsReasoningBlockWithoutSignature(t *testing.T) {
	// NOTE: 某个思考块从未拿到签名/加密数据（协议异常/提前中断）时必须整块丢弃，
	// 而不是拼出一个无法通过下一轮回放校验的残缺 ReasoningBlock。
	p := &fantasyProvider{isAnthropic: true}
	stream := seqOf(
		fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningDelta, ID: "0", Delta: "incomplete thought"},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningEnd, ID: "0"},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningEnd, ID: "1", ProviderMetadata: fantasy.ProviderMetadata{
			anthropic.Name: &anthropic.ReasoningOptionMetadata{RedactedData: "opaque-blob"},
		}},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop},
	)
	res, err := p.consumeStream(stream, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.blocks) != 1 {
		t.Fatalf("blocks = %+v, want exactly 1 block (incomplete id 0 must be dropped)", res.blocks)
	}
	if res.blocks[0].Type != "redacted_thinking" || res.blocks[0].Data != "opaque-blob" {
		t.Errorf("block = %+v, want redacted_thinking with opaque-blob", res.blocks[0])
	}
}

func TestConsumeStream_OpenAICompatPlainReasoningAggregation(t *testing.T) {
	p := &fantasyProvider{isAnthropic: false}
	stream := seqOf(
		fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningDelta, Delta: "让我想想，"},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningDelta, Delta: "应该是这样。"},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, Delta: "答案"},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop},
	)
	res, err := p.consumeStream(stream, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.reasoning != "让我想想，应该是这样。" {
		t.Errorf("reasoning = %q, want combined plain text", res.reasoning)
	}
	if len(res.blocks) != 0 {
		t.Errorf("blocks = %+v, want none for OpenAI-compat branch", res.blocks)
	}
	if res.content != "答案" {
		t.Errorf("content = %q, want %q", res.content, "答案")
	}
}

func TestConsumeStream_ToolCallsAggregated(t *testing.T) {
	p := &fantasyProvider{isAnthropic: false}
	stream := seqOf(
		fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: "call_1", ToolCallName: "ask_lawyer", ToolCallInput: `{"question":"x"}`},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: "call_2", ToolCallName: "roll_dice", ToolCallInput: `{}`},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls},
	)
	res, err := p.consumeStream(stream, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.toolCalls) != 2 {
		t.Fatalf("toolCalls = %+v, want 2", res.toolCalls)
	}
	if res.toolCalls[0] != (ToolCall{ID: "call_1", Name: "ask_lawyer", Arguments: `{"question":"x"}`}) {
		t.Errorf("toolCalls[0] = %+v", res.toolCalls[0])
	}
	if res.toolCalls[1] != (ToolCall{ID: "call_2", Name: "roll_dice", Arguments: `{}`}) {
		t.Errorf("toolCalls[1] = %+v", res.toolCalls[1])
	}
}

func TestConsumeStream_ErrorPartSurfacesAsError(t *testing.T) {
	p := &fantasyProvider{isAnthropic: false}
	injected := errors.New("boom")
	stream := seqOf(
		fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, Delta: "partial"},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: injected},
	)
	_, err := p.consumeStream(stream, nil)
	if !errors.Is(err, injected) {
		t.Fatalf("err = %v, want %v", err, injected)
	}
}

// ---------- 端到端：真实走 openaicompat.New，指向内存假 HTTP 端点 ----------

// capturedChatRequest 记录假端点收到的请求次数与最后一次请求体，供断言重试次数与请求形状。
type capturedChatRequest struct {
	count int
	body  []byte
}

func newFakeChatCompletionsServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, callIndex int)) (*httptest.Server, *capturedChatRequest) {
	t.Helper()
	captured := &capturedChatRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.body = body
		idx := captured.count
		captured.count++
		handler(w, r, idx)
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

// writeSSEChatCompletion 按 fantasy 自己测试套件里 streamingMockServer 验证过的 chunk 形状
// （标准 OpenAI chat.completion.chunk SSE 格式）下发一次完整流式响应：role 起始块、若干文本
// 增量块、finish_reason 块、独立的 usage 块，最后以 [DONE] 收尾。
func writeSSEChatCompletion(w http.ResponseWriter, contents []string, finishReason string, usage map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher := w.(http.Flusher)
	writeChunk := func(choices []map[string]any, extra map[string]any) {
		chunk := map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion.chunk", "created": 1700000000,
			"model": "test-model", "choices": choices,
		}
		for k, v := range extra {
			chunk[k] = v
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	writeChunk([]map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}, "finish_reason": nil}}, nil)
	for _, c := range contents {
		writeChunk([]map[string]any{{"index": 0, "delta": map[string]any{"content": c}, "finish_reason": nil}}, nil)
	}
	writeChunk([]map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": finishReason}}, nil)
	writeChunk([]map[string]any{}, map[string]any{"usage": usage})
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// TestFantasyProvider_OpenAICompatEndToEnd_Success 验证 newFantasyProvider 对 openaicompat
// 分支的 BaseURL/APIKey 接线，以及"始终走 Stream 聚合"这条路径完整可用：真实通过
// charm.land/fantasy/providers/openaicompat 发起请求，命中内存假 HTTP 端点，校验返回的文本、
// usage 换算（PromptTokens 必须等于原始 prompt_tokens，即 input+cache_read 之和）、以及请求
// 只发了一次（没有意外重试）。
func TestFantasyProvider_OpenAICompatEndToEnd_Success(t *testing.T) {
	srv, captured := newFakeChatCompletionsServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		writeSSEChatCompletion(w, []string{"Hello ", "world"}, "stop", map[string]any{
			"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 25,
			"prompt_tokens_details": map[string]any{"cached_tokens": 5},
		})
	})

	p, err := newFantasyProvider(false, "test-key", srv.URL, "test-model", 0, 0, false, "", 0)
	if err != nil {
		t.Fatalf("newFantasyProvider error: %v", err)
	}

	result, err := p.ChatWithTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("ChatWithTools error: %v", err)
	}
	if result.Content != "Hello world" {
		t.Errorf("Content = %q, want %q", result.Content, "Hello world")
	}
	if result.Usage.PromptTokens != 12 {
		t.Errorf("PromptTokens = %d, want 12 (input+cache_read must reconstruct the raw prompt_tokens)", result.Usage.PromptTokens)
	}
	if result.Usage.OutputTokens != 8 {
		t.Errorf("OutputTokens = %d, want 8", result.Usage.OutputTokens)
	}
	if result.Usage.CacheReadTokens != 5 {
		t.Errorf("CacheReadTokens = %d, want 5", result.Usage.CacheReadTokens)
	}
	if captured.count != 1 {
		t.Errorf("server received %d requests, want exactly 1 (no unexpected retry)", captured.count)
	}

	var body struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(captured.body, &body); err != nil {
		t.Fatalf("unmarshal captured request body: %v; raw=%s", err, captured.body)
	}
	if body.Model != "test-model" || !body.Stream {
		t.Errorf("request shape = %+v, want model=test-model stream=true", body)
	}
	if len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "hi" {
		t.Errorf("request messages = %+v, want single user message %q", body.Messages, "hi")
	}
}

// TestFantasyProvider_UsageSinkInvokedOnChat 验证Chat()(不像ChatWithTools那样把Usage放进
// 返回值里)在ctx携带WithUsageSink时会用最终usage回调一次，供拿不到ToolChatResult的调用方
// (Writer/Dramaturg/NPC等用Chat/ChatStream的agent)把usage送进自己的ContextManager；同时
// 验证全局(role×model)统计不依赖sink,始终会被记录。
func TestFantasyProvider_UsageSinkInvokedOnChat(t *testing.T) {
	resetStatsForTest()
	srv, _ := newFakeChatCompletionsServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeSSEChatCompletion(w, []string{"ok"}, "stop", map[string]any{
			"prompt_tokens": 40, "completion_tokens": 5, "total_tokens": 45,
			"prompt_tokens_details": map[string]any{"cached_tokens": 30},
		})
	})
	p, err := newFantasyProvider(false, "test-key", srv.URL, "test-model", 0, 0, false, "", 0)
	if err != nil {
		t.Fatalf("newFantasyProvider error: %v", err)
	}

	var got Usage
	var called bool
	ctx := WithUsageSink(context.Background(), func(u Usage) { called = true; got = u })
	if _, err := p.Chat(ctx, "sess:writer", []ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if !called {
		t.Fatal("usage sink should have been invoked")
	}
	if got.PromptTokens != 40 || got.CacheReadTokens != 30 || got.OutputTokens != 5 {
		t.Errorf("sink usage = %+v, want prompt=40 cache_read=30 output=5", got)
	}

	stats := Stats()
	if stats.Overall.PromptTokens != 40 || stats.Overall.CacheReadTokens != 30 {
		t.Errorf("global stats after Chat = %+v, want prompt=40 cache_read=30", stats.Overall)
	}
}

// TestFantasyProvider_OpenAICompatEndToEnd_NonRetryableErrorExhaustsFast 验证一个不可重试的
// HTTP 错误（401）不会触发 chat() 内部 8 秒退避重试：JsonChat 的外层 3 次尝试各自只发一次
// 请求就快速失败，最终返回 ErrEmptyLLMResponse，整体耗时应在毫秒级，远低于任何一次退避时长。
func TestFantasyProvider_OpenAICompatEndToEnd_NonRetryableErrorExhaustsFast(t *testing.T) {
	srv, captured := newFakeChatCompletionsServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"invalid_request_error"}}`))
	})

	p, err := newFantasyProvider(false, "bad-key", srv.URL, "test-model", 0, 0, false, "", 0)
	if err != nil {
		t.Fatalf("newFantasyProvider error: %v", err)
	}

	start := time.Now()
	_, jsonErr := p.JsonChat(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}})
	elapsed := time.Since(start)

	if !errors.Is(jsonErr, ErrEmptyLLMResponse) {
		t.Fatalf("JsonChat err = %v, want ErrEmptyLLMResponse", jsonErr)
	}
	if elapsed > 5*time.Second {
		t.Errorf("elapsed = %v, want well under the 8s retry backoff (401 must not trigger a retry sleep)", elapsed)
	}
	if captured.count != 3 {
		t.Errorf("server received %d requests, want exactly 3 (JsonChat's outer retry budget, one HTTP call each)", captured.count)
	}
}
