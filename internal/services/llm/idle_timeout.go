package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// NOTE: 连续 2 分钟收不到任何字节(含 SSE ping/注释行这类保活数据)就判定上游卡死并中止请求；
// 不限制整体耗时，长时间持续出字的推理流不会被截断。
const llmIdleTimeout = 2 * time.Minute

// NOTE: 所有 fantasy provider 共用一个 client，底层复用 http.DefaultTransport 的连接池与代理设置。
var llmHTTPClient = &http.Client{Transport: &idleTimeoutTransport{base: http.DefaultTransport, idle: llmIdleTimeout}}

type idleTimeoutError struct {
	idle time.Duration
}

func (e *idleTimeoutError) Error() string {
	return fmt.Sprintf("no data received for %s, request aborted", e.idle)
}

// NOTE: 等价于"流意外中断"：fantasy 的错误包装会把 io.ErrUnexpectedEOF 转成可重试的 ProviderError，
// 这样超时能直接进入现有重试逻辑，不需要改重试判定。
func (e *idleTimeoutError) Unwrap() error { return io.ErrUnexpectedEOF }

// idleTimeoutTransport 给每个请求挂一个空闲计时器：发出请求后等响应头、以及读响应体时等下一批
// 字节，任何一次等待超过 idle 就取消请求。
type idleTimeoutTransport struct {
	base http.RoundTripper
	idle time.Duration
}

func (t *idleTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	watch := &idleWatch{idle: t.idle, cancel: cancel}
	watch.timer = time.AfterFunc(t.idle, watch.expire)

	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		watch.close()
		if watch.expired.Load() {
			return nil, watch.err()
		}
		return nil, err
	}
	watch.disarm()
	resp.Body = &idleTimeoutBody{ReadCloser: resp.Body, watch: watch}
	return resp, nil
}

// idleWatch 的计时器只在"阻塞等待上游数据"期间走表：消费方处理得慢(不调用 Read)的时间不算空闲，
// 避免把下游背压误判成上游卡死。
type idleWatch struct {
	idle    time.Duration
	cancel  context.CancelFunc
	timer   *time.Timer
	expired atomic.Bool
}

func (w *idleWatch) expire() {
	w.expired.Store(true)
	w.cancel()
}

func (w *idleWatch) arm()    { w.timer.Reset(w.idle) }
func (w *idleWatch) disarm() { w.timer.Stop() }

func (w *idleWatch) close() {
	w.timer.Stop()
	w.cancel()
}

func (w *idleWatch) err() error { return &idleTimeoutError{idle: w.idle} }

type idleTimeoutBody struct {
	io.ReadCloser
	watch *idleWatch
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	b.watch.arm()
	n, err := b.ReadCloser.Read(p)
	b.watch.disarm()
	// NOTE: 超时是靠取消请求 context 实现的，底层读到的是 context canceled，这里换成明确的超时错误。
	if err != nil && err != io.EOF && b.watch.expired.Load() {
		err = b.watch.err()
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.watch.close()
	return b.ReadCloser.Close()
}
