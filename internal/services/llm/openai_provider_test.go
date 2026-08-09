// NOTE: 覆盖原生 tool calling 的流式响应聚合逻辑（openai_tool_calls.go 的 toolCallAggregator）。
// 用 httptest 伪造一个 SSE 端点，按分片下发 delta.tool_calls，验证聚合出的完整工具调用列表
// 与真实端点行为一致：分片 arguments 正确拼接、并行工具调用不串位、Index 缺失时的退化路径、
// content 与 tool_calls 可共存、残缺条目（从未拿到 name）应被丢弃。
// 禁止真实网络/真实LLM；全部基于内存假 HTTP 端点。
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

var testToolDefs = []ToolDefinition{
	{Name: "test_tool", Description: "test", Parameters: json.RawMessage(`{"type":"object"}`)},
}

func intPtr(i int) *int { return &i }

func chunkWithToolCalls(calls ...openai.ToolCall) openai.ChatCompletionStreamResponse {
	return openai.ChatCompletionStreamResponse{
		Choices: []openai.ChatCompletionStreamChoice{{Delta: openai.ChatCompletionStreamChoiceDelta{ToolCalls: calls}}},
	}
}

func chunkWithContent(content string) openai.ChatCompletionStreamResponse {
	return openai.ChatCompletionStreamResponse{
		Choices: []openai.ChatCompletionStreamChoice{{Delta: openai.ChatCompletionStreamChoiceDelta{Content: content}}},
	}
}

// newFakeSSEProvider 起一个假 SSE 端点，把 chunks 依次编码为 data 帧下发（以 data: [DONE] 收尾），
// 返回指向该端点的 openAIProvider。
func newFakeSSEProvider(t *testing.T, chunks []openai.ChatCompletionStreamResponse) *openAIProvider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter 不支持 http.Flusher")
		}
		for _, c := range chunks {
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatalf("marshal chunk: %v", err)
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)
	// NOTE: model 名不能落在 go-openai 的 legacy completion 模型禁用名单里，用一个普通占位名即可。
	return newOpenAIProvider("test-key", srv.URL, "test-model", 0, 0, false, "", false)
}

// TestChatWithTools_FragmentedArguments 验证同一 index 的 arguments 分片按顺序追加拼接，
// id/name 只在首片写入且不被后续空值覆盖。
func TestChatWithTools_FragmentedArguments(t *testing.T) {
	chunks := []openai.ChatCompletionStreamResponse{
		chunkWithToolCalls(openai.ToolCall{Index: intPtr(0), ID: "call_1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "ask_lawyer"}}),
		chunkWithToolCalls(openai.ToolCall{Index: intPtr(0), Function: openai.FunctionCall{Arguments: `{"que`}}),
		chunkWithToolCalls(openai.ToolCall{Index: intPtr(0), Function: openai.FunctionCall{Arguments: `stion":"`}}),
		chunkWithToolCalls(openai.ToolCall{Index: intPtr(0), Function: openai.FunctionCall{Arguments: `食尸鬼是否`}}),
		chunkWithToolCalls(openai.ToolCall{Index: intPtr(0), Function: openai.FunctionCall{Arguments: `存在"}`}}),
	}
	p := newFakeSSEProvider(t, chunks)

	result, err := p.ChatWithTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, testToolDefs)
	if err != nil {
		t.Fatalf("ChatWithTools error: %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("期望1个工具调用，实际%d个: %+v", len(result.ToolCalls), result.ToolCalls)
	}
	call := result.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "ask_lawyer" {
		t.Errorf("id/name 未在首片正确写入: %+v", call)
	}
	wantArgs := `{"question":"食尸鬼是否存在"}`
	if call.Arguments != wantArgs {
		t.Errorf("分片 arguments 拼接错误：got %q want %q", call.Arguments, wantArgs)
	}
}

// TestChatWithTools_ParallelToolCallsNotInterleaved 验证两个工具调用交替下发分片时，
// 按 index 分组不会互相串位。
func TestChatWithTools_ParallelToolCallsNotInterleaved(t *testing.T) {
	chunks := []openai.ChatCompletionStreamResponse{
		chunkWithToolCalls(
			openai.ToolCall{Index: intPtr(0), ID: "call_a", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "translate_anchor"}},
			openai.ToolCall{Index: intPtr(1), ID: "call_b", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "translate_anchor"}},
		),
		chunkWithToolCalls(openai.ToolCall{Index: intPtr(0), Function: openai.FunctionCall{Arguments: `{"concept":"A`}}),
		chunkWithToolCalls(openai.ToolCall{Index: intPtr(1), Function: openai.FunctionCall{Arguments: `{"concept":"B`}}),
		chunkWithToolCalls(openai.ToolCall{Index: intPtr(0), Function: openai.FunctionCall{Arguments: `"}`}}),
		chunkWithToolCalls(openai.ToolCall{Index: intPtr(1), Function: openai.FunctionCall{Arguments: `"}`}}),
	}
	p := newFakeSSEProvider(t, chunks)

	result, err := p.ChatWithTools(context.Background(), "", []ChatMessage{{Role: "user"}}, testToolDefs)
	if err != nil {
		t.Fatalf("ChatWithTools error: %v", err)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("期望2个工具调用，实际%d个: %+v", len(result.ToolCalls), result.ToolCalls)
	}
	if result.ToolCalls[0].ID != "call_a" || result.ToolCalls[0].Arguments != `{"concept":"A"}` {
		t.Errorf("第一个工具调用串位: %+v", result.ToolCalls[0])
	}
	if result.ToolCalls[1].ID != "call_b" || result.ToolCalls[1].Arguments != `{"concept":"B"}` {
		t.Errorf("第二个工具调用串位: %+v", result.ToolCalls[1])
	}
}

// TestChatWithTools_NilIndexDegradedPath 验证部分兼容端点省略 Index 字段时的退化路径：
// 后续分片 ID 为空则并入上一条；出现新的非空 ID 则视为新工具调用。
func TestChatWithTools_NilIndexDegradedPath(t *testing.T) {
	chunks := []openai.ChatCompletionStreamResponse{
		chunkWithToolCalls(openai.ToolCall{ID: "call_1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "ask_lawyer"}}),
		chunkWithToolCalls(openai.ToolCall{Function: openai.FunctionCall{Arguments: `{"question":"A"}`}}),
		chunkWithToolCalls(openai.ToolCall{ID: "call_2", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "ask_lawyer"}}),
		chunkWithToolCalls(openai.ToolCall{Function: openai.FunctionCall{Arguments: `{"question":"B"}`}}),
	}
	p := newFakeSSEProvider(t, chunks)

	result, err := p.ChatWithTools(context.Background(), "", []ChatMessage{{Role: "user"}}, testToolDefs)
	if err != nil {
		t.Fatalf("ChatWithTools error: %v", err)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("期望2个工具调用，实际%d个: %+v", len(result.ToolCalls), result.ToolCalls)
	}
	if result.ToolCalls[0].ID != "call_1" || result.ToolCalls[0].Arguments != `{"question":"A"}` {
		t.Errorf("第一个退化路径工具调用错误: %+v", result.ToolCalls[0])
	}
	if result.ToolCalls[1].ID != "call_2" || result.ToolCalls[1].Arguments != `{"question":"B"}` {
		t.Errorf("第二个退化路径工具调用错误: %+v", result.ToolCalls[1])
	}
}

// TestChatWithTools_ContentAndToolCallsCoexist 验证模型附带文本前言 + 工具调用时两者都被保留。
func TestChatWithTools_ContentAndToolCallsCoexist(t *testing.T) {
	chunks := []openai.ChatCompletionStreamResponse{
		chunkWithContent("好的，我来查询规则书。"),
		chunkWithToolCalls(openai.ToolCall{Index: intPtr(0), ID: "call_1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "ask_lawyer", Arguments: `{"question":"x"}`}}),
	}
	p := newFakeSSEProvider(t, chunks)

	result, err := p.ChatWithTools(context.Background(), "", []ChatMessage{{Role: "user"}}, testToolDefs)
	if err != nil {
		t.Fatalf("ChatWithTools error: %v", err)
	}
	if result.Content != "好的，我来查询规则书。" {
		t.Errorf("content 丢失: got %q", result.Content)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "ask_lawyer" {
		t.Errorf("tool_calls 丢失或错误: %+v", result.ToolCalls)
	}
}

// TestChatWithTools_DropsIncompleteEntryWithoutName 验证某 index 从未收到 name（协议异常/提前
// 中断）时聚合器丢弃该条目而非拼出一个无名工具调用；最终表现为一次空响应，触发 ChatWithTools
// 的重试并在耗尽后返回 ErrEmptyLLMResponse。
func TestChatWithTools_DropsIncompleteEntryWithoutName(t *testing.T) {
	chunks := []openai.ChatCompletionStreamResponse{
		chunkWithToolCalls(openai.ToolCall{Index: intPtr(0), Function: openai.FunctionCall{Arguments: `{"a":1}`}}),
	}
	p := newFakeSSEProvider(t, chunks)

	_, err := p.ChatWithTools(context.Background(), "", []ChatMessage{{Role: "user"}}, testToolDefs)
	if !errors.Is(err, ErrEmptyLLMResponse) {
		t.Fatalf("期望 ErrEmptyLLMResponse（残缺条目应被丢弃），实际 err=%v", err)
	}
}
