// NOTE: 覆盖 openai-responses 类型的 provider：请求走 /responses、usage 口径、推理强度写入、
// json_object、tool_choice=none 与工具调用往返。全部用 httptest，不联网。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func responsesSSE(event, data string) string {
	return "event: " + event + "\ndata: " + data + "\n\n"
}

// writeResponsesText 下发一次「只有文本」的 Responses 流：created、message、若干文本增量、completed(带 usage)。
func writeResponsesText(w http.ResponseWriter, texts []string, inputTokens, outputTokens, cachedTokens int) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher := w.(http.Flusher)
	emit := func(event, data string) {
		fmt.Fprint(w, responsesSSE(event, data))
		flusher.Flush()
	}
	emit("response.created", `{"type":"response.created","response":{"id":"resp_1","status":"in_progress","output":[]}}`)
	emit("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}}`)
	emit("response.content_part.added", `{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"msg_1","part":{"type":"output_text","text":""}}`)
	for _, text := range texts {
		delta, _ := json.Marshal(text)
		emit("response.output_text.delta", fmt.Sprintf(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_1","delta":%s}`, delta))
	}
	emit("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[]}}`)
	emit("response.completed", fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d,"input_tokens_details":{"cached_tokens":%d}}}}`,
		inputTokens, outputTokens, inputTokens+outputTokens, cachedTokens))
}

// writeResponsesToolCall 下发一次「先吐文本、再请求一个工具调用」的 Responses 流，参数拆成多块。
func writeResponsesToolCall(w http.ResponseWriter, texts []string, callID, name string, argChunks []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher := w.(http.Flusher)
	emit := func(event, data string) {
		fmt.Fprint(w, responsesSSE(event, data))
		flusher.Flush()
	}
	emit("response.created", `{"type":"response.created","response":{"id":"resp_1","status":"in_progress","output":[]}}`)
	emit("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}}`)
	for _, text := range texts {
		delta, _ := json.Marshal(text)
		emit("response.output_text.delta", fmt.Sprintf(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_1","delta":%s}`, delta))
	}
	emit("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[]}}`)

	nameJSON, _ := json.Marshal(name)
	callJSON, _ := json.Marshal(callID)
	emit("response.output_item.added", fmt.Sprintf(`{"type":"response.output_item.added","output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":%s,"name":%s,"arguments":"","status":"in_progress"}}`, callJSON, nameJSON))
	for _, chunk := range argChunks {
		chunkJSON, _ := json.Marshal(chunk)
		emit("response.function_call_arguments.delta", fmt.Sprintf(`{"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc_1","delta":%s}`, chunkJSON))
	}
	argsJSON, _ := json.Marshal(strings.Join(argChunks, ""))
	emit("response.output_item.done", fmt.Sprintf(`{"type":"response.output_item.done","output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":%s,"name":%s,"arguments":%s,"status":"completed"}}`, callJSON, nameJSON, argsJSON))
	emit("response.completed", `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":20,"output_tokens":6,"total_tokens":26}}}`)
}

func decodeBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal request body: %v; raw=%s", err, raw)
	}
	return body
}

// jsonPath 按 a.b.c 取嵌套字段，不存在时返回 nil。
func jsonPath(body map[string]any, path string) any {
	var cur any = body
	for _, key := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[key]
	}
	return cur
}

func newResponsesTestProvider(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, callIndex int), model, effort string) (Provider, *capturedChatRequest) {
	t.Helper()
	srv, captured := newFakeChatCompletionsServer(t, handler)
	return newProviderByType("openai-responses", "test-key", srv.URL, model, 0, 0, false, effort, 0, false), captured
}

func TestResponsesProvider_ChatWithTools(t *testing.T) {
	var path string
	p, captured := newResponsesTestProvider(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		path = r.URL.Path
		writeResponsesText(w, []string{"Hello ", "world"}, 12, 8, 5)
	}, "gpt-4o", "")

	ctx := context.WithValue(context.Background(), "session", "sess-1")
	result, err := p.ChatWithTools(ctx, "sess-1:director", []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("ChatWithTools error: %v", err)
	}
	if path != "/responses" {
		t.Errorf("request path = %q, want /responses", path)
	}
	if result.Content != "Hello world" {
		t.Errorf("Content = %q, want %q", result.Content, "Hello world")
	}
	if result.Usage.PromptTokens != 12 || result.Usage.CacheReadTokens != 5 || result.Usage.OutputTokens != 8 {
		t.Errorf("Usage = %+v, want prompt=12 cache_read=5 output=8", result.Usage)
	}
	if captured.count != 1 {
		t.Errorf("server received %d requests, want exactly 1", captured.count)
	}

	body := decodeBody(t, captured.body)
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	if body["store"] != false {
		t.Errorf("store = %v, want false", body["store"])
	}
	if got := body["prompt_cache_key"]; got != "sess-1:director" {
		t.Errorf("prompt_cache_key = %v, want sess-1:director", got)
	}
}

func TestResponsesProvider_ReasoningEffort(t *testing.T) {
	cases := []struct {
		name   string
		model  string
		effort string
		want   any
	}{
		{"explicit effort on reasoning-by-name model", "deepseek-r1", "high", "high"},
		{"explicit low on reasoning-by-name model", "deepseek-r1", "low", "low"},
		// NOTE: fork 的 fantasy 把除 gpt-3/gpt-4/chatgpt/-chat 以外的名字都当推理模型，留空时默认值和显式 none 都由它写进请求体。
		{"empty effort on reasoning-by-name model uses default", "deepseek-r1", "", "high"},
		{"none on reasoning-by-name model is sent as none", "deepseek-r1", "none", "none"},
		{"gpt-5 with empty effort uses default", "gpt-5", "", "high"},
		// NOTE: 名字带 -chat 的被 fantasy 判为非推理模型会丢掉 reasoning，只有后台显式选择时才靠客户端级 JSON 覆盖强制写入。
		{"explicit effort on non-reasoning-by-name model is forced", "deepseek-chat", "high", "high"},
		{"empty effort on non-reasoning-by-name model sends nothing", "deepseek-chat", "", nil},
		{"none on non-reasoning-by-name model sends nothing", "deepseek-chat", "none", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, captured := newResponsesTestProvider(t, func(w http.ResponseWriter, r *http.Request, _ int) {
				writeResponsesText(w, []string{"ok"}, 1, 1, 0)
			}, tc.model, tc.effort)
			if _, err := p.ChatWithTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, nil); err != nil {
				t.Fatalf("ChatWithTools error: %v", err)
			}
			got := jsonPath(decodeBody(t, captured.body), "reasoning.effort")
			if got != tc.want {
				t.Errorf("reasoning.effort = %v, want %v; body=%s", got, tc.want, captured.body)
			}
		})
	}
}

func TestResponsesProvider_JsonChatSetsTextFormat(t *testing.T) {
	p, captured := newResponsesTestProvider(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeResponsesText(w, []string{`{"a":1}`}, 1, 1, 0)
	}, "deepseek-r1", "high")

	out, err := p.JsonChat(context.Background(), "", []ChatMessage{{Role: "user", Content: "give json"}})
	if err != nil {
		t.Fatalf("JsonChat error: %v", err)
	}
	if out != `{"a":1}` {
		t.Errorf("JsonChat = %q", out)
	}
	body := decodeBody(t, captured.body)
	if got := jsonPath(body, "text.format.type"); got != "json_object" {
		t.Errorf("text.format.type = %v, want json_object; body=%s", got, captured.body)
	}
	// NOTE: JSON 客户端也要带上强制写入的推理强度，否则两个客户端的行为会不一致。
	if got := jsonPath(body, "reasoning.effort"); got != "high" {
		t.Errorf("reasoning.effort = %v, want high on the JSON client too", got)
	}

	plain, plainCaptured := newResponsesTestProvider(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeResponsesText(w, []string{"hi"}, 1, 1, 0)
	}, "deepseek-r1", "high")
	if _, err := plain.Chat(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if got := jsonPath(decodeBody(t, plainCaptured.body), "text.format"); got != nil {
		t.Errorf("Chat sent text.format = %v, want none", got)
	}
}

func TestResponsesProvider_ChatStreamHistoryToolsForcesNone(t *testing.T) {
	p, captured := newResponsesTestProvider(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeResponsesText(w, []string{"夜色", "渐深。"}, 10, 4, 0)
	}, "gpt-4o", "")

	tools := []ToolDefinition{{Name: "paint", Description: "画图", Parameters: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}}}`)}}
	tokens, errCh, err := p.ChatStream(context.Background(), "", []ChatMessage{{Role: "user", Content: "写"}}, tools)
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	var got []string
	for tok := range tokens {
		got = append(got, tok)
	}
	if e := <-errCh; e != nil {
		t.Fatalf("stream error: %v", e)
	}
	if strings.Join(got, "|") != "夜色|渐深。" {
		t.Errorf("tokens = %q, want 夜色|渐深。", got)
	}

	body := decodeBody(t, captured.body)
	if body["tool_choice"] != "none" {
		t.Errorf("tool_choice = %v, want none", body["tool_choice"])
	}
	if ts, _ := body["tools"].([]any); len(ts) != 1 {
		t.Errorf("tools = %v, want 1 declared tool", body["tools"])
	}
}

func TestResponsesProvider_ChatStreamToolsRoundTrip(t *testing.T) {
	var calls int
	var bodies [][]byte
	srv, captured := newFakeChatCompletionsServer(t, func(w http.ResponseWriter, r *http.Request, idx int) {
		calls = idx + 1
		writeResponsesToolCall(w, []string{"夜色", "渐深。"}, "call_1", "paint", []string{`{"prompt":"月下`, `的钟楼"}`})
	})
	p := newProviderByType("openai-responses", "test-key", srv.URL, "gpt-4o", 0, 0, false, "", 0, false)
	tools := []ToolDefinition{{Name: "paint", Description: "画图", Parameters: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}}}`)}}

	var deltas []string
	res, err := p.(ToolStreamer).ChatStreamTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "写"}}, tools, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatalf("ChatStreamTools error: %v", err)
	}
	bodies = append(bodies, captured.body)
	if strings.Join(deltas, "|") != "夜色|渐深。" {
		t.Errorf("onText deltas = %q", deltas)
	}
	if res.Content != "夜色渐深。" {
		t.Errorf("Content = %q", res.Content)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "call_1" || res.ToolCalls[0].Name != "paint" || res.ToolCalls[0].Arguments != `{"prompt":"月下的钟楼"}` {
		t.Fatalf("ToolCalls = %+v", res.ToolCalls)
	}
	if res.Usage.PromptTokens != 20 || res.Usage.OutputTokens != 6 {
		t.Errorf("Usage = %+v, want prompt=20 output=6", res.Usage)
	}
	if calls != 1 {
		t.Errorf("server received %d requests, want 1", calls)
	}
	if body := decodeBody(t, bodies[0]); body["tool_choice"] == "none" {
		t.Errorf("tool_choice = none, ChatStreamTools must leave the choice to the model")
	}

	history := []ChatMessage{
		{Role: "user", Content: "写"},
		{Role: "assistant", Content: res.Content, ToolCalls: res.ToolCalls},
		{Role: "tool", ToolCallID: "call_1", Content: "illust://1"},
	}
	if _, err := p.(ToolStreamer).ChatStreamTools(context.Background(), "", history, tools, nil); err != nil {
		t.Fatalf("second ChatStreamTools error: %v", err)
	}
	input, _ := decodeBody(t, captured.body)["input"].([]any)
	var sawCall, sawOutput bool
	for _, item := range input {
		m, _ := item.(map[string]any)
		switch m["type"] {
		case "function_call":
			sawCall = m["call_id"] == "call_1" && m["name"] == "paint"
		case "function_call_output":
			sawOutput = m["call_id"] == "call_1" && m["output"] == "illust://1"
		}
	}
	if !sawCall || !sawOutput {
		t.Errorf("history replay: function_call=%v function_call_output=%v; input=%v", sawCall, sawOutput, input)
	}
}

func TestResponsesProvider_ImplementsOptionalCapabilities(t *testing.T) {
	p := newProviderByType("openai-responses", "k", "http://127.0.0.1:1", "m", 0, 0, false, "", 0, false)
	if _, ok := p.(Embedder); !ok {
		t.Errorf("openai-responses provider must implement Embedder, got %T", p)
	}
	if _, ok := p.(ImageGenerator); !ok {
		t.Errorf("openai-responses provider must implement ImageGenerator, got %T", p)
	}
}

// TestResponsesProvider_StreamProbeSkipsWarnings 守住首事件探测：fantasy 在任何网络事件之前先发一个 Warnings
// (gpt-5 加 temperature 就会触发)，HTTP 错误排在它后面；探测若把 Warnings 当成首事件，错误就只会走 errCh，
// 整体重试语义失效。用 401 避免触发 8 秒重试等待。
func TestResponsesProvider_StreamProbeSkipsWarnings(t *testing.T) {
	newUnauthorized := func(t *testing.T) (Provider, *capturedChatRequest) {
		srv, captured := newFakeChatCompletionsServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"invalid_request_error"}}`))
		})
		return newProviderByType("openai-responses", "bad-key", srv.URL, "gpt-5", 0, 0.5, false, "", 0, false), captured
	}

	t.Run("ChatStream", func(t *testing.T) {
		p, captured := newUnauthorized(t)
		tokens, errCh, err := p.ChatStream(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, nil)
		if err == nil {
			for range tokens {
			}
			t.Fatalf("ChatStream err = nil, want the HTTP error returned directly; errCh=%v", <-errCh)
		}
		if captured.count != 1 {
			t.Errorf("server received %d requests, want 1", captured.count)
		}
	})

	t.Run("ChatStreamTools", func(t *testing.T) {
		p, captured := newUnauthorized(t)
		_, err := p.(ToolStreamer).ChatStreamTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, nil, nil)
		if err == nil {
			t.Fatal("ChatStreamTools err = nil, want the HTTP error")
		}
		if captured.count != 1 {
			t.Errorf("server received %d requests, want 1", captured.count)
		}
	})
}
