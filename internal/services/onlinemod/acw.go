// NOTE: acw.go 负责下载直链，并处理阿里云 CDN 的 acw_sc__v2 挑战：页面里内嵌 arg1，
// 经固定的置换与异或得到 cookie 值，带上后重试一次。
//
// FIXME: solveACW 的常量表来自公开的逆向资料，没有对照过真实挑战页；站点改版后会失效，
// 失效时 Download 返回 ErrChallenge，由调用方引导人工下载。
package onlinemod

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	acwCookieName = "acw_sc__v2"
	acwMask       = "3000176000856006061501533003690027800375"
	// defaultMaxDownload 在调用方没给上限时使用。
	defaultMaxDownload int64 = 50 << 20
)

var acwPosList = [40]int{
	15, 35, 29, 24, 33, 16, 1, 38, 10, 9,
	19, 31, 40, 27, 22, 23, 25, 13, 6, 11,
	39, 18, 20, 8, 14, 21, 32, 26, 2, 30,
	7, 4, 17, 5, 3, 28, 34, 37, 12, 36,
}

var reACWArg1 = regexp.MustCompile(`arg1\s*=\s*['"]([0-9A-Fa-f]{40})['"]`)

// solveACW 把挑战页里的 arg1（40 位十六进制）换算成 acw_sc__v2 的值；输入不合法返回空串。
func solveACW(arg1 string) string {
	if len(arg1) != len(acwPosList) {
		return ""
	}
	unsbox := make([]byte, len(acwPosList))
	for i := 0; i < len(arg1); i++ {
		for j, p := range acwPosList {
			if p == i+1 {
				unsbox[j] = arg1[i]
			}
		}
	}
	var sb strings.Builder
	for i := 0; i+1 < len(unsbox) && i+1 < len(acwMask); i += 2 {
		a, err := strconv.ParseUint(string(unsbox[i:i+2]), 16, 8)
		if err != nil {
			return ""
		}
		b, err := strconv.ParseUint(acwMask[i:i+2], 16, 8)
		if err != nil {
			return ""
		}
		fmt.Fprintf(&sb, "%02x", a^b)
	}
	return sb.String()
}

// NOTE: 判断是否 HTML 看内容开头而不是 Content-Type，CDN 的挑战页常带着错误的类型头。
func looksLikeHTML(data []byte) bool {
	s := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	s = bytes.TrimLeft(s, " \t\r\n")
	s = s[:min(len(s), 32)]
	l := strings.ToLower(string(s))
	for _, p := range []string{"<!doctype html", "<html", "<head", "<body", "<script", "<meta", "<title"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// Download 下载直链。遇到 acw_sc__v2 挑战页就解题并带 cookie 重试一次，仍是 HTML 则返回 ErrChallenge。
func (c *Client) Download(ctx context.Context, direct string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxDownload
	}
	ctx, cancel := withDefaultTimeout(ctx, downloadTimeout)
	defer cancel()
	hc := c.client()
	if hc.Jar == nil {
		hc.Jar, _ = cookiejar.New(nil)
	}
	hdr := http.Header{"Accept": {"*/*"}}

	data, final, status, err := c.fetchWith(ctx, hc, http.MethodGet, direct, nil, hdr, maxBytes)
	if err != nil {
		return nil, err
	}
	if !looksLikeHTML(data) {
		if err := checkStatus(status, final); err != nil {
			return nil, err
		}
		return data, nil
	}

	m := reACWArg1.FindSubmatch(data)
	if m == nil {
		log.Warn("download returned html without acw arg1", "host", final.Hostname(), "status", status, "body_head", head(data, 200))
		if err := checkStatus(status, final); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s 返回 HTML 页面而不是文件", ErrChallenge, final.Hostname())
	}
	value := solveACW(string(m[1]))
	if value == "" {
		return nil, fmt.Errorf("%w: 无法解析 %s 的 acw_sc__v2 挑战", ErrChallenge, final.Hostname())
	}
	cookie := &http.Cookie{Name: acwCookieName, Value: value, Path: "/"}
	hc.Jar.SetCookies(final, []*http.Cookie{cookie})
	if u, perr := url.Parse(direct); perr == nil && u.Hostname() != final.Hostname() {
		hc.Jar.SetCookies(u, []*http.Cookie{cookie})
	}
	log.Debug("acw challenge solved, retrying", "host", final.Hostname())

	data, final, status, err = c.fetchWith(ctx, hc, http.MethodGet, direct, nil, hdr, maxBytes)
	if err != nil {
		return nil, err
	}
	if looksLikeHTML(data) {
		log.Warn("download still html after acw retry", "host", final.Hostname(), "status", status, "body_head", head(data, 200))
		return nil, fmt.Errorf("%w: %s 在带上 acw_sc__v2 后仍返回 HTML 页面", ErrChallenge, final.Hostname())
	}
	if err := checkStatus(status, final); err != nil {
		return nil, err
	}
	return data, nil
}
