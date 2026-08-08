// NOTE: 覆盖 anthropic_convert.go 的纯逻辑转换：system 摘出、连续 tool 消息合并进单条
// user 消息、cache_control 断点精确落位（system 最后块/tools 最后一个/转换后最后两条
// user 消息各自的最后一个 block）、空内容防御、以及响应侧的 text/tool_use 拆分。
// 全部为内存纯函数测试，不涉及网络。
package llm

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestToAnthropicRequest_SystemExtracted(t *testing.T) {
	system, msgs, err := toAnthropicRequest([]ChatMessage{
		{Role: "system", Content: "sys prompt"},
		{Role: "user", Content: "hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(system) != 1 || system[0].Text != "sys prompt" {
		t.Fatalf("system not extracted correctly: %+v", system)
	}
	if len(msgs) != 1 || msgs[0].Role != anthropic.MessageParamRoleUser {
		t.Fatalf("expected single user message, got: %+v", msgs)
	}
}

func TestToAnthropicRequest_MergesToolResults(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "scenario context"},
		{Role: "user", Content: "current turn"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "check_rule", Arguments: `{"q":"x"}`},
				{ID: "call_2", Name: "roll_dice", Arguments: `{}`},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "rule result"},
		{Role: "tool", ToolCallID: "call_2", Content: "dice result"},
	}

	_, msgs, err := toAnthropicRequest(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages (2 user + 1 assistant + 1 merged tool_result user), got %d: %+v", len(msgs), msgs)
	}

	toolResultMsg := msgs[3]
	if toolResultMsg.Role != anthropic.MessageParamRoleUser {
		t.Fatalf("expected merged tool results in a user message, got role %v", toolResultMsg.Role)
	}
	if len(toolResultMsg.Content) != 2 {
		t.Fatalf("expected 2 tool_result blocks merged into one message, got %d", len(toolResultMsg.Content))
	}
	if toolResultMsg.Content[0].OfToolResult == nil || toolResultMsg.Content[0].OfToolResult.ToolUseID != "call_1" {
		t.Fatalf("first tool_result should reference call_1: %+v", toolResultMsg.Content[0].OfToolResult)
	}
	if toolResultMsg.Content[1].OfToolResult == nil || toolResultMsg.Content[1].OfToolResult.ToolUseID != "call_2" {
		t.Fatalf("second tool_result should reference call_2: %+v", toolResultMsg.Content[1].OfToolResult)
	}

	assistantMsg := msgs[2]
	if assistantMsg.Role != anthropic.MessageParamRoleAssistant || len(assistantMsg.Content) != 2 {
		t.Fatalf("expected assistant message with 2 tool_use blocks: %+v", assistantMsg)
	}
	if assistantMsg.Content[0].OfToolUse == nil || assistantMsg.Content[0].OfToolUse.ID != "call_1" {
		t.Fatalf("first tool_use should be call_1: %+v", assistantMsg.Content[0].OfToolUse)
	}
}

func TestToAnthropicRequest_SkipsEmptyContent(t *testing.T) {
	_, msgs, err := toAnthropicRequest([]ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "  "},              // 空白，应跳过
		{Role: "assistant", Content: ""},            // 既无文本也无工具调用，应跳过
		{Role: "user", Content: "real message"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content[0].OfText == nil || msgs[0].Content[0].OfText.Text != "real message" {
		t.Fatalf("expected only the non-empty user message to survive: %+v", msgs)
	}
}

func TestToAnthropicRequest_EmptyInputReturnsError(t *testing.T) {
	_, msgs, err := toAnthropicRequest([]ChatMessage{
		{Role: "system", Content: "sys only"},
		{Role: "user", Content: "   "},
	})
	if err == nil {
		t.Fatalf("expected error for empty converted message list, got msgs=%+v", msgs)
	}
}

func TestToolCallInput_FallbackAndValid(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"valid json object", `{"a":1}`, `{"a":1}`},
		{"empty string falls back", "", "{}"},
		{"whitespace falls back", "   ", "{}"},
		{"invalid json falls back", `{not json`, "{}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := toolCallInput(c.in)
			if string(got) != c.want {
				t.Fatalf("toolCallInput(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestApplyCacheBreakpoints_Placement(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "sys prompt"},
		{Role: "user", Content: "scenario context"}, // 最早的 user 消息，不应被打断点
		{Role: "user", Content: "current turn"},      // 倒数第二条 user 消息
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "check_rule", Arguments: `{}`},
				{ID: "call_2", Name: "roll_dice", Arguments: `{}`},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "rule result"},
		{Role: "tool", ToolCallID: "call_2", Content: "dice result"}, // 合并后成为最后一条 user 消息
	}

	system, msgs, err := toAnthropicRequest(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools := toAnthropicTools([]ToolDefinition{
		{Name: "tool_a", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "tool_b", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
	})

	applyCacheBreakpoints(system, tools, msgs)

	if system[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("system block should be cache-marked")
	}

	if tools[0].OfTool.CacheControl.Type == "ephemeral" {
		t.Fatalf("first tool should NOT be cache-marked")
	}
	if tools[1].OfTool.CacheControl.Type != "ephemeral" {
		t.Fatalf("last tool should be cache-marked")
	}

	// msgs[0] = "scenario context"(user,最早) msgs[1] = "current turn"(user) msgs[2] = assistant msgs[3] = merged tool_result(user,最后)
	if msgs[0].Content[0].OfText.CacheControl.Type == "ephemeral" {
		t.Fatalf("earliest user message should NOT be cache-marked (only last two user messages are)")
	}
	if msgs[1].Content[0].OfText.CacheControl.Type != "ephemeral" {
		t.Fatalf("second-to-last user message should be cache-marked")
	}
	lastMsg := msgs[3]
	if lastMsg.Content[0].OfToolResult.CacheControl.Type == "ephemeral" {
		t.Fatalf("only the LAST block of the last user message should be cache-marked, not the first")
	}
	if lastMsg.Content[1].OfToolResult.CacheControl.Type != "ephemeral" {
		t.Fatalf("last block of the last user message should be cache-marked")
	}

	total := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			switch {
			case b.OfText != nil && b.OfText.CacheControl.Type == "ephemeral":
				total++
			case b.OfToolResult != nil && b.OfToolResult.CacheControl.Type == "ephemeral":
				total++
			case b.OfToolUse != nil && b.OfToolUse.CacheControl.Type == "ephemeral":
				total++
			}
		}
	}
	if total != 2 {
		t.Fatalf("expected exactly 2 message-level cache breakpoints, got %d", total)
	}
}

func TestApplyCacheBreakpoints_FewerThanTwoUserMessages(t *testing.T) {
	system, msgs, err := toAnthropicRequest([]ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "only message"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 不应 panic/越界
	applyCacheBreakpoints(system, nil, msgs)
	if msgs[0].Content[0].OfText.CacheControl.Type != "ephemeral" {
		t.Fatalf("the only user message should be cache-marked")
	}
}

func TestToAnthropicTools_ExtractsPropertiesAndRequired(t *testing.T) {
	tools := toAnthropicTools([]ToolDefinition{
		{
			Name:        "check_rule",
			Description: "查询规则",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {"question": {"type": "string"}},
				"required": ["question"]
			}`),
		},
	})
	if len(tools) != 1 || tools[0].OfTool == nil {
		t.Fatalf("expected 1 tool: %+v", tools)
	}
	tool := tools[0].OfTool
	if tool.Name != "check_rule" || tool.Description.Value != "查询规则" {
		t.Fatalf("name/description not mapped correctly: %+v", tool)
	}
	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "question" {
		t.Fatalf("required not mapped correctly: %+v", tool.InputSchema.Required)
	}
	props, ok := tool.InputSchema.Properties.(map[string]any)
	if !ok || props["question"] == nil {
		t.Fatalf("properties not mapped correctly: %+v", tool.InputSchema.Properties)
	}
}

func TestFromAnthropicMessage(t *testing.T) {
	msg := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			{Type: "text", Text: "hello "},
			{Type: "text", Text: "world"},
			{Type: "tool_use", ID: "id1", Name: "foo", Input: json.RawMessage(`{"a":1}`)},
		},
	}
	content, toolCalls := fromAnthropicMessage(msg)
	if content != "hello world" {
		t.Fatalf("expected concatenated text, got %q", content)
	}
	if len(toolCalls) != 1 || toolCalls[0].ID != "id1" || toolCalls[0].Name != "foo" || toolCalls[0].Arguments != `{"a":1}` {
		t.Fatalf("tool call not extracted correctly: %+v", toolCalls)
	}
}
