// NOTE: lanzou.go 把蓝奏云分享页解析成 CDN 直链：分享页 → /fn? 页 → POST ajaxfile.php。
// 只用普通 HTTP；遇到 JS 挑战（典型是 *.lanzoum.com）直接返回 ErrChallenge，不做浏览器模拟。
package onlinemod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var (
	reDownIframe  = regexp.MustCompile(`(?is)<iframe\b[^>]*\bn_downlink\b[^>]*>`)
	reSrcAttr     = regexp.MustCompile(`(?is)\bsrc\s*=\s*["']([^"']+)["']`)
	reFileNameID  = regexp.MustCompile(`(?is)<div\b[^>]*\bid\s*=\s*["']filenajax["'][^>]*>(.*?)</div>`)
	reFileNameCls = regexp.MustCompile(`(?is)<div\b[^>]*\bclass\s*=\s*["']n_box_3fn["'][^>]*>(.*?)</div>`)
	reHTMLTag     = regexp.MustCompile(`<[^>]*>`)
	reWPSign      = regexp.MustCompile(`var\s+wp_sign\s*=\s*['"]([^'"]*)['"]`)
	reAjaxData    = regexp.MustCompile(`var\s+ajaxdata\s*=\s*['"]([^'"]*)['"]`)
	reDomain1     = regexp.MustCompile(`var\s+domain1\s*=\s*['"]([^'"]+)['"]`)
	reDomain2     = regexp.MustCompile(`var\s+domain2\s*=\s*['"]([^'"]+)['"]`)
	reScriptArg1  = regexp.MustCompile(`arg1\s*=`)
)

func isLanzoum(host string) bool {
	host = strings.ToLower(host)
	return host == "lanzoum.com" || strings.HasSuffix(host, ".lanzoum.com")
}

func submatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// ResolveLanzou 把蓝奏云分享页地址解析成 CDN 直链和文件名。
func (c *Client) ResolveLanzou(ctx context.Context, shareURL string) (direct, filename string, err error) {
	ctx, cancel := withDefaultTimeout(ctx, resolveTimeout)
	defer cancel()
	hc := c.client()

	body, shareFinal, status, err := c.fetchWith(ctx, hc, http.MethodGet, shareURL, nil, nil, maxPageBytes)
	if err != nil {
		return "", "", err
	}
	if err := checkStatus(status, shareFinal); err != nil {
		return "", "", err
	}
	page := string(body)

	fnPath := ""
	if tag := reDownIframe.FindString(page); tag != "" {
		fnPath = html.UnescapeString(submatch(reSrcAttr, tag))
	}
	if fnPath == "" {
		if reScriptArg1.MatchString(page) || isLanzoum(shareFinal.Hostname()) {
			log.Warn("lanzou share page challenged", "host", shareFinal.Hostname(), "body_head", head(body, 200))
			return "", "", fmt.Errorf("%w: 分享页 %s 返回脚本挑战", ErrChallenge, shareFinal.Hostname())
		}
		log.Warn("lanzou share page has no download entry", "host", shareFinal.Hostname(), "body_head", head(body, 200))
		return "", "", fmt.Errorf("分享页 %s 没有下载入口（链接可能失效或需要提取码）", shareFinal.Hostname())
	}
	filename = cleanFilename(submatch(reFileNameID, page))
	if filename == "" {
		filename = cleanFilename(submatch(reFileNameCls, page))
	}

	fnURL, err := shareFinal.Parse(fnPath)
	if err != nil {
		return "", "", fmt.Errorf("无效的下载入口地址: %w", err)
	}
	fnBody, fnFinal, status, err := c.fetchWith(ctx, hc, http.MethodGet, fnURL.String(), nil,
		http.Header{"Referer": {shareFinal.String()}}, maxPageBytes)
	if err != nil {
		return "", "", err
	}
	if err := checkStatus(status, fnFinal); err != nil {
		return "", "", err
	}
	fn := string(fnBody)

	wpSign := submatch(reWPSign, fn)
	ajaxData := submatch(reAjaxData, fn)
	apiURL := submatch(reDomain1, fn)
	if apiURL == "" {
		apiURL = submatch(reDomain2, fn)
	}
	if wpSign == "" || ajaxData == "" || apiURL == "" {
		if reScriptArg1.MatchString(fn) {
			log.Warn("lanzou fn page challenged", "host", fnFinal.Hostname(), "body_head", head(fnBody, 200))
			return "", "", fmt.Errorf("%w: 下载入口页 %s 返回脚本挑战", ErrChallenge, fnFinal.Hostname())
		}
		log.Warn("lanzou fn page missing fields", "host", fnFinal.Hostname(), "body_head", head(fnBody, 200))
		return "", "", errors.New("下载入口页缺少 wp_sign / ajaxdata / domain（页面结构可能已变化）")
	}

	form := url.Values{
		"action":     {"downprocess"},
		"websignkey": {ajaxData},
		"signs":      {ajaxData},
		"sign":       {wpSign},
		"websign":    {""},
		"kd":         {"1"},
		"ves":        {"1"},
	}
	hdr := http.Header{
		"Content-Type":     {"application/x-www-form-urlencoded; charset=UTF-8"},
		"Referer":          {fnFinal.String()},
		"Origin":           {fnFinal.Scheme + "://" + fnFinal.Host},
		"X-Requested-With": {"XMLHttpRequest"},
		"Accept":           {"application/json, text/javascript, */*; q=0.01"},
	}
	apiBody, apiFinal, status, err := c.fetchWith(ctx, hc, http.MethodPost, apiURL, strings.NewReader(form.Encode()), hdr, maxPageBytes)
	if err != nil {
		return "", "", err
	}
	if err := checkStatus(status, apiFinal); err != nil {
		return "", "", err
	}

	var resp struct {
		Zt  flexString `json:"zt"`
		Dom flexString `json:"dom"`
		URL flexString `json:"url"`
		Inf flexString `json:"inf"`
	}
	if err := json.Unmarshal(apiBody, &resp); err != nil {
		log.Warn("lanzou ajaxfile decode failed", "err", err, "body_head", head(apiBody, 500))
		return "", "", fmt.Errorf("解析蓝奏云直链响应失败: %w", err)
	}
	if resp.Zt != "1" {
		return "", "", fmt.Errorf("蓝奏云拒绝了下载请求 (zt=%s): %s", resp.Zt, resp.Inf)
	}
	if resp.Dom == "" || resp.URL == "" {
		return "", "", errors.New("蓝奏云直链响应缺少 dom 或 url")
	}
	direct = strings.TrimRight(string(resp.Dom), "/") + "/file/" + strings.TrimLeft(string(resp.URL), "/")
	u, err := url.Parse(direct)
	if err != nil {
		return "", "", fmt.Errorf("蓝奏云返回了无效的直链: %w", err)
	}
	if err := c.checkURL(u); err != nil {
		return "", "", err
	}
	log.Debug("lanzou resolved", "host", u.Hostname(), "filename", filename)
	return direct, filename, nil
}

// NOTE: 文件名只用于判断扩展名和展示，去掉标签与路径分隔符即可。
func cleanFilename(raw string) string {
	s := html.UnescapeString(reHTMLTag.ReplaceAllString(raw, ""))
	s = strings.NewReplacer("/", "_", "\\", "_").Replace(strings.TrimSpace(s))
	return strings.ToValidUTF8(s, "")
}
