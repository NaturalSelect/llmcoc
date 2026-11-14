// NOTE: Package onlinemod 从中文 COC 模组站 cnmods 列出模组，经蓝奏云取得压缩包并抽取正文，
// 供「线上模组」导入流程使用。所有出站请求都受主机白名单、重定向跳数和体积上限约束，
// 避免用户提交的 URL 被当成 SSRF 跳板；不使用无头浏览器，被 CDN 挑战拦截时只尝试已知的
// acw_sc__v2 解题，仍失败就返回 ErrChallenge，交给人工下载后上传。
package onlinemod

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/llmcoc/server/internal/logging"
)

var log = logging.For("onlinemod")

const (
	userAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"
	maxRedirects = 5
	// maxPageBytes 是 HTML / JSON 响应的体积上限。
	maxPageBytes = 2 << 20

	defaultListURL  = "https://www.cnmods.net/prod-api/index/moduleListPage.do"
	listTimeout     = 15 * time.Second
	resolveTimeout  = 30 * time.Second
	downloadTimeout = 3 * time.Minute
)

var (
	// ErrChallenge 表示请求被 CDN 或蓝奏云的访问挑战拦截，无法用普通 HTTP 继续。
	ErrChallenge = errors.New("被 CDN 或蓝奏云的访问挑战拦截")
	// ErrURLNotAllowed 表示地址的协议或主机不在允许范围内。
	ErrURLNotAllowed = errors.New("地址不在允许范围内")
	// ErrTooLarge 表示响应体超过体积上限；超限一律报错，不截断。
	ErrTooLarge = errors.New("响应体超过大小上限")
)

// NOTE: 后缀匹配只认「等于该域名」或「以 . 加该域名结尾」，evilcnmods.net 不会被放行。
var allowedSuffixes = []string{
	"cnmods.net",
	"lanzov.com",
	"lanzoum.com",
	"lanzouw.com",
	"lanzouc.com",
	"woozooo.com",
	"lanrar.com",
	// NOTE: 蓝奏云直链会 302 到 webgetstore.com 的存储节点（联网测试实测），不放行就下不到文件。
	"webgetstore.com",
}

func defaultAllowHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, s := range allowedSuffixes {
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}

// Client 是访问 cnmods 与蓝奏云的受限 HTTP 客户端，可被多个 goroutine 共用。
type Client struct {
	// HTTP 必须带 cookiejar：下载直链的 acw_sc__v2 cookie 靠它携带。
	HTTP *http.Client
	// ListURL 是 cnmods 列表接口，默认 https://www.cnmods.net/prod-api/index/moduleListPage.do。
	ListURL string
	// AllowHost 判定主机名（不含端口）是否允许访问，默认按后缀白名单；测试里可替换。
	// 协议永远只允许 https，没有放宽开关；测试用 httptest.NewTLSServer。
	AllowHost func(host string) bool
}

// NewClient 返回使用默认白名单和 cookiejar 的客户端。不设 Client.Timeout，由调用方的 ctx 控制超时。
func NewClient() *Client {
	jar, _ := cookiejar.New(nil) // 选项为 nil 时不会返回错误
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        20,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		HTTP:      &http.Client{Transport: tr, Jar: jar},
		ListURL:   defaultListURL,
		AllowHost: defaultAllowHost,
	}
}

// ValidShareURL 判断用户提交的地址是否为 https 且主机在白名单内。
func ValidShareURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (&Client{}).checkURL(u) == nil
}

func (c *Client) checkURL(u *url.URL) error {
	if u == nil || u.Hostname() == "" {
		return fmt.Errorf("%w: 缺少主机名", ErrURLNotAllowed)
	}
	host := u.Hostname()
	if u.User != nil {
		return fmt.Errorf("%w: 主机 %s 的地址不能带用户信息", ErrURLNotAllowed, host)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" {
		return fmt.Errorf("%w: 仅允许 https（主机 %s，协议 %q）", ErrURLNotAllowed, host, scheme)
	}
	allow := c.AllowHost
	if allow == nil {
		allow = defaultAllowHost
	}
	if !allow(host) {
		return fmt.Errorf("%w: 主机 %s 不在白名单内", ErrURLNotAllowed, host)
	}
	return nil
}

func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if err := c.checkURL(req.URL); err != nil {
		return err
	}
	if len(via) > maxRedirects {
		return fmt.Errorf("重定向超过 %d 次", maxRedirects)
	}
	return nil
}

// client 返回 c.HTTP 的浅拷贝，并保证每一跳重定向都先过白名单校验，再交给调用方自带的 CheckRedirect。
func (c *Client) client() *http.Client {
	var hc http.Client
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	orig := hc.CheckRedirect
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := c.checkRedirect(req, via); err != nil {
			return err
		}
		if orig != nil {
			return orig(req, via)
		}
		return nil
	}
	return &hc
}

// fetch 发出请求并读取受限的响应体。不判断状态码，由调用方决定（挑战页可能不是 2xx）。
// 返回的 final 是跟随重定向后的最终地址。
func (c *Client) fetch(ctx context.Context, method, rawURL string, body io.Reader, header http.Header, maxBytes int64) (data []byte, final *url.URL, status int, err error) {
	return c.fetchWith(ctx, c.client(), method, rawURL, body, header, maxBytes)
}

// fetchWith 与 fetch 相同，但使用调用方给定的 http.Client（需来自 c.client()），让多次请求共用同一个 cookiejar。
func (c *Client) fetchWith(ctx context.Context, hc *http.Client, method, rawURL string, body io.Reader, header http.Header, maxBytes int64) (data []byte, final *url.URL, status int, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("无效的 URL: %w", err)
	}
	if err := c.checkURL(u); err != nil {
		return nil, nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	data, err = readBody(resp, maxBytes)
	if err != nil {
		return nil, nil, resp.StatusCode, err
	}
	return data, resp.Request.URL, resp.StatusCode, nil
}

func readBody(resp *http.Response, maxBytes int64) ([]byte, error) {
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("%w: 声明 %d 字节，上限 %d 字节", ErrTooLarge, resp.ContentLength, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: 超过 %d 字节", ErrTooLarge, maxBytes)
	}
	return data, nil
}

func checkStatus(status int, u *url.URL) error {
	if status >= 200 && status <= 299 {
		return nil
	}
	host := ""
	if u != nil {
		host = u.Hostname()
	}
	return fmt.Errorf("%s 返回 HTTP %d", host, status)
}

// withDefaultTimeout 仅在 ctx 没有截止时间时补一个默认超时。
func withDefaultTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// head 取响应开头用于日志或错误信息，非法 UTF-8 替换为 ?。
func head(b []byte, n int) string {
	return strings.ToValidUTF8(string(b[:min(n, len(b))]), "?")
}
