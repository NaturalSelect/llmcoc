// NOTE: 覆盖 idle_timeout.go 的"连接上连续无字节即超时、任何字节(含响应头之前的保活)都续约"语义。
// 空闲时长在测试里缩到 100ms 量级，全部基于本机假 HTTP 端点，不涉及真实网络。
package llm

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/openaicompat"
)

const testIdle = 100 * time.Millisecond

func newIdleClient(idle time.Duration) *http.Client {
	return &http.Client{Transport: newIdleTimeoutTransport(idle)}
}

func newTestIdleClient() *http.Client { return newIdleClient(testIdle) }

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

// 响应头到达之前上游持续发 1xx 临时响应(网关常用的保活方式)时，等待总时长超过空闲上限也不能超时。
func TestIdleTimeoutTransport_PreHeaderBytesRenewTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 5 次 1xx，间隔 40ms，总时长 200ms 超过 100ms 空闲上限；net/http 客户端最多容忍 5 次 1xx。
		for i := 0; i < 5; i++ {
			w.WriteHeader(http.StatusProcessing)
			time.Sleep(40 * time.Millisecond)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)

	resp, err := newTestIdleClient().Get(srv.URL)
	if err != nil {
		t.Fatalf("Get error: %v (bytes before the response header must renew the idle timer)", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "ok" {
		t.Fatalf("body = %q, %v, want %q with no error", body, err, "ok")
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

// 连接复用时，空闲期间挂着的那次读不能把旧的读期限带进新请求：新请求要拿到完整的空闲窗口。
func TestIdleTimeoutTransport_ReusedConnectionGetsFullWindow(t *testing.T) {
	const idle = 600 * time.Millisecond
	var addrs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addrs = append(addrs, r.RemoteAddr)
		if len(addrs) == 2 {
			time.Sleep(400 * time.Millisecond)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)

	client := newIdleClient(idle)
	get := func() {
		t.Helper()
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("Get error: %v", err)
		}
		defer resp.Body.Close()
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Fatalf("ReadAll error: %v", err)
		}
	}
	get()
	// 连接在池里空闲 400ms 后再发第二个请求；它的响应头要再等 400ms，总共 800ms 超过窗口起点，但没超过窗口长度。
	time.Sleep(400 * time.Millisecond)
	get()

	if len(addrs) != 2 || addrs[0] != addrs[1] {
		t.Fatalf("remote addrs = %v, want both requests on one reused connection", addrs)
	}
}

// 空闲超时被 fantasy 当作可重试错误的前提：错误链里能认出 io.ErrUnexpectedEOF。
func TestIdleTimeoutError_UnwrapsToUnexpectedEOF(t *testing.T) {
	var err error = &idleTimeoutError{idle: time.Second}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("errors.Is(%v, io.ErrUnexpectedEOF) = false", err)
	}
}

// 生产环境走 TLS：超时错误要能穿过 tls.Conn 原样到达调用方，而不是被换成别的错误。
func TestIdleTimeoutTransport_BodyStallTimesOutOverTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	transport := newIdleTimeoutTransport(testIdle).(*http.Transport)
	transport.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	resp, err := (&http.Client{Transport: transport}).Get(srv.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 5)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "first" {
		t.Fatalf("first read = %q, %v, want %q", buf, err, "first")
	}
	_, err = resp.Body.Read(buf)
	var idleErr *idleTimeoutError
	if !errors.As(err, &idleErr) {
		t.Fatalf("second read err = %v (%T), want *idleTimeoutError", err, err)
	}
}

// 连接被服务端直接断开等普通错误不能被误报成空闲超时。
func TestIdleTimeoutTransport_ConnectionResetIsNotIdleTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen error: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = bufio.NewReader(conn).ReadString('\n')
		conn.Close()
	}()

	resp, err := newTestIdleClient().Get("http://" + ln.Addr().String())
	if err == nil {
		resp.Body.Close()
		t.Fatal("err = nil, want a connection error")
	}
	var idleErr *idleTimeoutError
	if errors.As(err, &idleErr) {
		t.Fatalf("err = %v, want a plain connection error, not an idle timeout", err)
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
