package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// NOTE: 连接上连续 2 分钟读不到任何字节就判定上游卡死并中止请求。计时挂在连接层，响应头之前和响应体
// 之中一视同仁：网关发来的任何字节(SSE ping/注释行、HTTP/2 PING、1xx 临时响应)都会续约，所以首字再慢，
// 只要上游持续发保活就不会超时。不限制整体耗时，长时间持续出字的推理流不会被截断。做法与 ../angela 的
// internal/log/http.go 一致。
const llmIdleTimeout = 2 * time.Minute

// NOTE: 所有 fantasy provider 共用一个 client，底层复用 http.DefaultTransport 的连接池与代理设置。
var llmHTTPClient = &http.Client{Transport: newIdleTimeoutTransport(llmIdleTimeout)}

type idleTimeoutError struct {
	idle time.Duration
}

func (e *idleTimeoutError) Error() string {
	return fmt.Sprintf("no data received for %s, request aborted", e.idle)
}

// NOTE: 等价于"流意外中断"：fantasy 的错误包装会把 io.ErrUnexpectedEOF 转成可重试的 ProviderError，
// 这样超时能直接进入现有重试逻辑，不需要改重试判定。
func (e *idleTimeoutError) Unwrap() error { return io.ErrUnexpectedEOF }

// newIdleTimeoutTransport 克隆默认 transport，并包装它拨出的每条连接：连续 idle 时长没有任何字节
// 可读，Read 就返回 idleTimeoutError。
func newIdleTimeoutTransport(idle time.Duration) http.RoundTripper {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	transport := t.Clone()
	dial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &idleTimeoutConn{Conn: conn, idle: idle}, nil
	}
	return transport
}

// idleTimeoutConn 每次 Read 前重设读期限，使 idle 成为滑动的空闲超时而不是连接总时长上限。
type idleTimeoutConn struct {
	net.Conn
	idle time.Duration
}

// NOTE: 连接池里空闲的连接，读期限停在上一次 Read 设的值；复用它发新请求时若不在写入时续约，
// 这个请求的等待窗口会被已空闲的时间缩短，甚至刚发出就超时。SetReadDeadline 对正在阻塞的 Read 同样生效。
func (c *idleTimeoutConn) Write(b []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	return c.Conn.Write(b)
}

func (c *idleTimeoutConn) Read(b []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	n, err := c.Conn.Read(b)
	// NOTE: 读期限到了底层报的是 i/o timeout，这里换成带明确原因、可被重试判定识别的错误。
	if errors.Is(err, os.ErrDeadlineExceeded) {
		err = &idleTimeoutError{idle: c.idle}
	}
	return n, err
}
