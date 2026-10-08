// NOTE: 覆盖 idle_timeout.go 的"连续无数据即超时、任何数据都续约"语义。空闲时长在测试里缩到
// 100ms 量级，全部基于内存假 HTTP 端点，不涉及真实网络。
package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/openaicompat"
)

const testIdle = 100 * time.Millisecond

func newTestIdleClient() *http.Client {
	return &http.Client{Transport: &idleTimeoutTransport{base: http.DefaultTransport, idle: testIdle}}
}

func TestIdleTimeoutTransport_HeaderStallTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	start := time.Now()
	resp, err := newTestIdleClient().Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("err = nil, want idle timeout")
	}
	var idleErr *idleTimeoutError
	if !errors.As(err, &idleErr) {
		t.Fatalf("err = %v (%T), want *idleTimeoutError", err, err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("timed out after %v, want close to %v", elapsed, testIdle)
	}
}

func TestIdleTimeoutTransport_KeepAliveRenewsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// 总时长 400ms 远超 100ms 空闲上限，但每 40ms 就有一次 ping。
		for i := 0; i < 10; i++ {
			_, _ = io.WriteString(w, ": ping\n\n")
			flusher.Flush()
			time.Sleep(40 * time.Millisecond)
		}
		_, _ = io.WriteString(w, "data: done\n\n")
	}))
	t.Cleanup(srv.Close)

	resp, err := newTestIdleClient().Get(srv.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll error: %v (ping data must renew the idle timer)", err)
	}
	if got := string(body); len(got) == 0 || got[len(got)-len("data: done\n\n"):] != "data: done\n\n" {
		t.Errorf("body = %q, want it to end with the final data event", got)
	}
}

func TestIdleTimeoutTransport_BodyStallTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	resp, err := newTestIdleClient().Get(srv.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 5)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "first" {
		t.Fatalf("first read = %q, %v, want %q", buf, err, "first")
	}
	start := time.Now()
	_, err = resp.Body.Read(buf)
	var idleErr *idleTimeoutError
	if !errors.As(err, &idleErr) {
		t.Fatalf("second read err = %v (%T), want *idleTimeoutError", err, err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("stall detected after %v, want close to %v", elapsed, testIdle)
	}
}

func TestIdleTimeoutTransport_SlowConsumerIsNotIdle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "a")
		w.(http.Flusher).Flush()
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(w, "b")
	}))
	t.Cleanup(srv.Close)

	resp, err := newTestIdleClient().Get(srv.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	defer resp.Body.Close()

	one := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, one); err != nil {
		t.Fatalf("first read error: %v", err)
	}
	// 消费方自己处理得慢(不调用 Read)的时间，不应被算作上游空闲。
	time.Sleep(3 * testIdle)
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(rest) != "b" {
		t.Fatalf("rest = %q, %v, want %q with no error", rest, err, "b")
	}
}

// TestIdleTimeout_SurfacesAsRetryableThroughFantasy 验证超时错误经过 fantasy openaicompat 的
// 错误包装后，仍能被现有的 isRetryableFantasyError 识别，从而走原有重试逻辑。
func TestIdleTimeout_SurfacesAsRetryableThroughFantasy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// NOTE: 必须先读完请求体，net/http 服务端才会开始监听客户端断开。
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	provider, err := openaicompat.New(openaicompat.WithAPIKey("k"), openaicompat.WithBaseURL(srv.URL), openaicompat.WithHTTPClient(newTestIdleClient()))
	if err != nil {
		t.Fatalf("openaicompat.New error: %v", err)
	}
	lm, err := provider.LanguageModel(context.Background(), "test-model")
	if err != nil {
		t.Fatalf("LanguageModel error: %v", err)
	}
	p := &fantasyProvider{lm: lm, model: "test-model", maxTokens: 10}

	stream, err := p.lm.Stream(context.Background(), p.buildCall(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, false, nil))
	if err == nil {
		_, err = p.consumeStream(stream, nil)
	}
	if err == nil {
		t.Fatal("err = nil, want idle timeout")
	}
	t.Logf("surfaced error: %T: %v", err, err)
	if !isRetryableFantasyError(err) {
		t.Errorf("isRetryableFantasyError(%v) = false, want true so the existing retry loop engages", err)
	}
}

// TestIdleTimeout_MidStreamStallIsRetryableOpenAICompat 验证已经收到部分内容后流卡住的场景：
// 超时错误会作为流内错误事件出现，并且同样能被现有重试判定识别。
func TestIdleTimeout_MidStreamStallIsRetryableOpenAICompat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `data: {"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"par"},"finish_reason":null}]}`+"\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	provider, err := openaicompat.New(openaicompat.WithAPIKey("k"), openaicompat.WithBaseURL(srv.URL), openaicompat.WithHTTPClient(newTestIdleClient()))
	if err != nil {
		t.Fatalf("openaicompat.New error: %v", err)
	}
	lm, err := provider.LanguageModel(context.Background(), "test-model")
	if err != nil {
		t.Fatalf("LanguageModel error: %v", err)
	}
	p := &fantasyProvider{lm: lm, model: "test-model", maxTokens: 10}

	start := time.Now()
	stream, err := p.lm.Stream(context.Background(), p.buildCall(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, false, nil))
	if err == nil {
		_, err = p.consumeStream(stream, nil)
	}
	if err == nil {
		t.Fatal("err = nil, want idle timeout after the stream stalls")
	}
	t.Logf("surfaced error: %T: %v", err, err)
	if !isRetryableFantasyError(err) {
		t.Errorf("isRetryableFantasyError(%v) = false, want true", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("stall detected after %v, want close to %v", elapsed, testIdle)
	}
}

// TestIdleTimeout_MidStreamStallIsRetryableAnthropic 对 Anthropic 通道做同样的验证：
// 它的 SDK 与错误包装路径和 OpenAI 兼容通道不同。
func TestIdleTimeout_MidStreamStallIsRetryableAnthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: message_start\n"+`data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"c","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`+"\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	provider, err := anthropic.New(anthropic.WithAPIKey("k"), anthropic.WithBaseURL(srv.URL), anthropic.WithHTTPClient(newTestIdleClient()))
	if err != nil {
		t.Fatalf("anthropic.New error: %v", err)
	}
	lm, err := provider.LanguageModel(context.Background(), "claude-x")
	if err != nil {
		t.Fatalf("LanguageModel error: %v", err)
	}
	p := &fantasyProvider{lm: lm, isAnthropic: true, model: "claude-x", maxTokens: 10}

	start := time.Now()
	stream, err := p.lm.Stream(context.Background(), p.buildCall(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, false, nil))
	if err == nil {
		_, err = p.consumeStream(stream, nil)
	}
	if err == nil {
		t.Fatal("err = nil, want idle timeout after the stream stalls")
	}
	t.Logf("surfaced error: %T: %v", err, err)
	if !isRetryableFantasyError(err) {
		t.Errorf("isRetryableFantasyError(%v) = false, want true", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("stall detected after %v, want close to %v", elapsed, testIdle)
	}
}
