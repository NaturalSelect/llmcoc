// NOTE: client_test.go 覆盖客户端本身：列表解析、白名单、重定向、体积上限。
// 全部用 httptest.NewTLSServer，不联网；默认只允许 https，所以测试服务器必须是 TLS。
package onlinemod

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// newLocalClient 返回信任测试证书、只放行 127.0.0.1 的客户端。
func newLocalClient(srv *httptest.Server) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		HTTP:      &http.Client{Transport: srv.Client().Transport, Jar: jar},
		ListURL:   srv.URL + "/prod-api/index/moduleListPage.do",
		AllowHost: func(h string) bool { return h == "127.0.0.1" },
	}
}

// newAliasClient 把所有主机名都拨到测试服务器，用真实的白名单校验，
// 这样可以用 wwbth.lanzov.com 之类的真实域名走完整条链而不联网。
func newAliasClient(srv *httptest.Server) *Client {
	tr := srv.Client().Transport.(*http.Transport).Clone()
	// 测试证书不含这些域名，所以跳过校验；仅限测试。
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	addr := srv.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	jar, _ := cookiejar.New(nil)
	c := NewClient()
	c.HTTP = &http.Client{Transport: tr, Jar: jar}
	return c
}

func TestListParsesItems(t *testing.T) {
	var gotQuery string
	var gotUA string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotUA = r.Header.Get("User-Agent")
		if r.URL.Path != "/prod-api/index/moduleListPage.do" {
			t.Errorf("路径 = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"data":{"list":[
			{"keyId":4821,"title":"鬼屋","article":"作者甲","moduleType":"COC","moduleAge":"现代","structure":"线性",
			 "minAmount":2,"maxAmount":"4","minDuration":null,"maxDuration":"6.0","freeLevel":"free",
			 "releaseDate":"2024-01-02","url":"https://wwbth.lanzov.com/iNWGo4fuqqli","opinion":"<p>好</p>",
			 "moduleVersion":"7版","occurrencePlace":"中国","original":"原创"},
			{"keyId":"abc-7","title":"雾夜","article":null,"minAmount":"","maxAmount":3,"url":"https://x"}
		],"totalElements":4730,"totalPages":473}}`))
	}))
	defer srv.Close()

	page, err := newLocalClient(srv).List(context.Background(), ListQuery{Page: 2, Title: "鬼屋 & 雾"})
	if err != nil {
		t.Fatal(err)
	}
	if gotUA != "Mozilla/5.0 (Windows NT 10.0; Win64; x64)" {
		t.Errorf("User-Agent = %q", gotUA)
	}
	// title 必须是 URL 编码后的形式。
	if !strings.Contains(gotQuery, "title=%E9%AC%BC%E5%B1%8B+%26+%E9%9B%BE") &&
		!strings.Contains(gotQuery, "title=%E9%AC%BC%E5%B1%8B%20%26%20%E9%9B%BE") {
		t.Errorf("title 未正确编码: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "page=2") || !strings.Contains(gotQuery, "moduleType=COC") {
		t.Errorf("查询串缺少 page 或 moduleType: %s", gotQuery)
	}
	if page.Page != 2 || page.TotalPages != 473 || page.TotalElements != 4730 || len(page.Items) != 2 {
		t.Fatalf("分页信息不对: %+v", page)
	}
	a, b := page.Items[0], page.Items[1]
	want := CnmodsItem{
		KeyID: "4821", Title: "鬼屋", Article: "作者甲", ModuleType: "COC", ModuleAge: "现代", Structure: "线性",
		MinAmount: 2, MaxAmount: 4, MinDuration: 0, MaxDuration: 6, FreeLevel: "free",
		ReleaseDate: "2024-01-02", URL: "https://wwbth.lanzov.com/iNWGo4fuqqli", Opinion: "<p>好</p>",
		ModuleVersion: "7版", OccurrencePlace: "中国", Original: "原创",
	}
	if a != want {
		t.Errorf("第一条\n got  %+v\n want %+v", a, want)
	}
	if b.KeyID != "abc-7" || b.Title != "雾夜" || b.Article != "" || b.MinAmount != 0 || b.MaxAmount != 3 {
		t.Errorf("第二条解码不对: %+v", b)
	}
}

func TestListDefaults(t *testing.T) {
	var q map[string][]string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		_, _ = w.Write([]byte(`{"code":1,"data":{"list":null,"totalElements":0,"totalPages":0}}`))
	}))
	defer srv.Close()

	page, err := newLocalClient(srv).List(context.Background(), ListQuery{Page: -3})
	if err != nil {
		t.Fatal(err)
	}
	if q["page"][0] != "1" || q["moduleType"][0] != "COC" {
		t.Errorf("默认查询参数 = %v", q)
	}
	if _, has := q["title"]; has {
		t.Errorf("没有关键词时不应带 title: %v", q)
	}
	if page.Items == nil || len(page.Items) != 0 || page.Page != 1 {
		t.Errorf("空列表应为非 nil 空切片且 Page=1: %+v", page)
	}
}

func TestListCodeNotOne(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":500,"msg":"服务繁忙","data":null}`))
	}))
	defer srv.Close()
	_, err := newLocalClient(srv).List(context.Background(), ListQuery{})
	if err == nil || !strings.Contains(err.Error(), "code=500") || !strings.Contains(err.Error(), "服务繁忙") {
		t.Fatalf("err = %v", err)
	}
}

func TestListBadJSON(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>502 Bad Gateway</html>`))
	}))
	defer srv.Close()
	_, err := newLocalClient(srv).List(context.Background(), ListQuery{})
	if err == nil || !strings.Contains(err.Error(), "解析") {
		t.Fatalf("err = %v", err)
	}
}

func TestListHTTPStatusError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := newLocalClient(srv).List(context.Background(), ListQuery{})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectsDisallowedHostAndScheme(t *testing.T) {
	c := NewClient() // 默认白名单；被拒绝发生在发请求之前，不会联网
	tests := []struct {
		name    string
		listURL string
		wantIn  string
	}{
		{"非白名单主机", "https://evil.example.com/list", "evil.example.com"},
		{"后缀伪装", "https://evilcnmods.net/list", "evilcnmods.net"},
		{"http 被拒", "http://www.cnmods.net/list", "https"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.ListURL = tt.listURL
			_, err := c.List(context.Background(), ListQuery{})
			if !errors.Is(err, ErrURLNotAllowed) || !strings.Contains(err.Error(), tt.wantIn) {
				t.Fatalf("err = %v，期望 ErrURLNotAllowed 且提到 %q", err, tt.wantIn)
			}
		})
	}
}

func TestRedirectToDisallowedHostIsRejected(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example.com/steal", http.StatusFound)
	}))
	defer srv.Close()
	_, err := newLocalClient(srv).List(context.Background(), ListQuery{})
	if err == nil || !errors.Is(err, ErrURLNotAllowed) || !strings.Contains(err.Error(), "evil.example.com") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedirectDowngradeToHTTPIsRejected(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/x", http.StatusFound)
	}))
	defer srv.Close()
	_, err := newLocalClient(srv).List(context.Background(), ListQuery{})
	if !errors.Is(err, ErrURLNotAllowed) {
		t.Fatalf("err = %v", err)
	}
}

func TestRedirectLimit(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	}))
	defer srv.Close()
	_, err := newLocalClient(srv).List(context.Background(), ListQuery{})
	if err == nil || !strings.Contains(err.Error(), "重定向超过") {
		t.Fatalf("err = %v", err)
	}
	if n := hits.Load(); n > maxRedirects+1 {
		t.Fatalf("请求了 %d 次，超过 %d 跳上限", n, maxRedirects)
	}
}

func TestListResponseTooLarge(t *testing.T) {
	t.Run("分块超限", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			chunk := bytes.Repeat([]byte("a"), 64<<10)
			for i := 0; i < 40; i++ { // 2.5MB，超过 2MB
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		}))
		defer srv.Close()
		_, err := newLocalClient(srv).List(context.Background(), ListQuery{})
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("声明长度超限", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(3<<20))
			_, _ = w.Write(bytes.Repeat([]byte("a"), 3<<20))
		}))
		defer srv.Close()
		_, err := newLocalClient(srv).List(context.Background(), ListQuery{})
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDefaultAllowHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"cnmods.net", true},
		{"www.cnmods.net", true},
		{"WWBTH.LANZOV.COM", true},
		{"wwbth.lanzoum.com", true},
		{"apifile.woozooo.com", true},
		{"apifile.lanzouw.com", true},
		{"x.lanzouc.com", true},
		{"developer4.lanrar.com", true},
		{"zip3.webgetstore.com", true},
		{"lanzov.com.", true},
		{"evilcnmods.net", false},
		{"notwebgetstore.com", false},
		{"webgetstore.com.evil.com", false},
		{"cnmods.net.evil.com", false},
		{"notlanzov.com", false},
		{"lanzov.com.cn", false},
		{"127.0.0.1", false},
		{"localhost", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := defaultAllowHost(tt.host); got != tt.want {
			t.Errorf("defaultAllowHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestValidShareURL(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"https://wwbth.lanzov.com/iNWGo4fuqqli", true},
		{"https://cnmods.lanzoum.com/iabc", true},
		{"https://WWBTH.LANZOV.COM/x", true},
		{"https://www.cnmods.net/detail/1", true},
		{"http://wwbth.lanzov.com/iNWGo4fuqqli", false},
		{"https://evilcnmods.net/x", false},
		{"https://cnmods.net.evil.com/x", false},
		{"https://evil.com/?u=https://www.cnmods.net/", false},
		{"https://www.cnmods.net@evil.com/", false},
		{"https://wwbth.lanzov.com@evil.com/", false},
		{"https://user@wwbth.lanzov.com/x", false},
		{"https://127.0.0.1/x", false},
		{"https://localhost/x", false},
		{"ftp://wwbth.lanzov.com/x", false},
		{"//wwbth.lanzov.com/x", false},
		{"wwbth.lanzov.com/x", false},
		{"https:///x", false},
		{"javascript:alert(1)", false},
		{"", false},
		{"https://wwbth.lanzov.com/%zz", false},
	}
	for _, tt := range tests {
		if got := ValidShareURL(tt.raw); got != tt.want {
			t.Errorf("ValidShareURL(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

// CnmodsItem 会被 handlers 内嵌进别的结构体后序列化回前端：
// 键名必须是来源站的 camelCase，且不能有会被提升到外层的 UnmarshalJSON。
func TestCnmodsItemJSONShape(t *testing.T) {
	type outer struct {
		CnmodsItem
		Local *int `json:"local"`
	}
	b, err := json.Marshal(outer{CnmodsItem: CnmodsItem{KeyID: "9", MinAmount: 2, OccurrencePlace: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"keyId", "title", "article", "moduleType", "moduleAge", "structure", "minAmount",
		"maxAmount", "minDuration", "maxDuration", "freeLevel", "releaseDate", "url", "opinion",
		"moduleVersion", "occurrencePlace", "original", "local"} {
		if _, ok := m[k]; !ok {
			t.Errorf("序列化结果缺少键 %q: %s", k, b)
		}
	}
	var back outer
	if err := json.Unmarshal([]byte(`{"keyId":"9","minAmount":2,"local":7}`), &back); err != nil {
		t.Fatal(err)
	}
	if back.KeyID != "9" || back.MinAmount != 2 || back.Local == nil || *back.Local != 7 {
		t.Errorf("内嵌结构反序列化丢字段: %+v", back)
	}
}
