// NOTE: lanzou_test.go 用 httptest 模拟蓝奏云整条链：分享页 → fn 页 → ajaxfile → 带挑战的直链。
// 页面片段取自实测过的真实结构；客户端把所有主机名都拨到本地 TLS 服务器，所以走的是真实的主机白名单。
package onlinemod

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const (
	testArg1  = "3A7F9C21B0D4E85F6A13C97D2E4B80F15C6A39D7"
	testSolve = "668d48590b9a149128d8db62bc1dc92f4efbc03f" // 由独立脚本算出，见 acw_test.go
)

const sharePageHTML = `<!DOCTYPE html><html><head><title>鬼屋.zip - 蓝奏云</title></head><body>
<div class="n_box_3fn" id="filenajax">鬼屋 &amp; 雾夜.zip</div>
<div class="n_box_3fn" style="display:none">备用名.zip</div>
<iframe class="n_downlink" name="1" src="/fn?AbCdEf123456" frameborder="0" scrolling="no"></iframe>
</body></html>`

func fnPageHTML(apiBase string) string {
	return `<html><body><script>
var wp_sign = 'SIGN_VALUE_123';
var ajaxdata = 'AJAX_DATA_456';
var domain1 = '` + apiBase + `/ajaxfile.php?file=98765';
var domain2 = 'https://apifile.lanzouw.com/ajaxfile.php?file=98765';
</script></body></html>`
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type lanzouServer struct {
	t        *testing.T
	zipBody  []byte
	mu       sync.Mutex
	posted   url.Values
	postRef  string
	postHdr  http.Header
	fnRef    string
	fileHits int
	cookieOK bool
	// 可调：ajaxfile 的响应与直链的行为。
	ajaxResp string
	// alwaysChallenge 让直链永远返回挑战页，即使带了正确 cookie。
	alwaysChallenge bool
	// wrongSolution 让服务器期望另一个 cookie 值，模拟算法失效。
	wrongSolution bool
}

func (ls *lanzouServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/iNWGo4fuqqli", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sharePageHTML))
	})
	mux.HandleFunc("/fn", func(w http.ResponseWriter, r *http.Request) {
		ls.mu.Lock()
		ls.fnRef = r.Header.Get("Referer")
		ls.mu.Unlock()
		if r.URL.RawQuery != "AbCdEf123456" {
			http.Error(w, "bad query "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(fnPageHTML("https://" + r.Host)))
	})
	mux.HandleFunc("/ajaxfile.php", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "need POST", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ls.mu.Lock()
		ls.posted = r.PostForm
		ls.postRef = r.Header.Get("Referer")
		ls.postHdr = r.Header.Clone()
		ls.mu.Unlock()
		if r.URL.Query().Get("file") != "98765" {
			http.Error(w, "bad file id", http.StatusBadRequest)
			return
		}
		resp := ls.ajaxResp
		if resp == "" {
			resp = `{"zt":"1","dom":"https://developer4.lanrar.com","url":"CODE_abc"}`
		}
		_, _ = w.Write([]byte(resp))
	})
	mux.HandleFunc("/file/CODE_abc", func(w http.ResponseWriter, r *http.Request) {
		ls.mu.Lock()
		ls.fileHits++
		ls.mu.Unlock()
		want := testSolve
		if ls.wrongSolution {
			want = strings.Repeat("0", 40)
		}
		if c, err := r.Cookie("acw_sc__v2"); err == nil && c.Value == want && !ls.alwaysChallenge {
			ls.mu.Lock()
			ls.cookieOK = true
			ls.mu.Unlock()
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(ls.zipBody)
			return
		}
		// 挑战页：没有或错误的 cookie。Content-Type 故意写成 octet-stream，检验按内容判断 HTML。
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("<html><script>var arg1='" + testArg1 + "';setTimeout('reload()',2000)</script></html>"))
	})
	return mux
}

// 整条链：直链 host 由 ajaxfile 返回的 dom 决定，这里 dom 是 developer4.lanrar.com（白名单内），
// 所以测试客户端把它拨到本地服务器。
func newLanzouFixture(t *testing.T) (*lanzouServer, *Client, func()) {
	t.Helper()
	ls := &lanzouServer{t: t, zipBody: makeZip(t, map[string]string{"a.txt": "正文"})}
	srv := httptest.NewTLSServer(ls.handler())
	return ls, newAliasClient(srv), srv.Close
}

func TestResolveLanzouAndDownloadFullChain(t *testing.T) {
	ls, c, closeFn := newLanzouFixture(t)
	defer closeFn()
	ctx := context.Background()

	direct, filename, err := c.ResolveLanzou(ctx, "https://wwbth.lanzov.com/iNWGo4fuqqli")
	if err != nil {
		t.Fatal(err)
	}
	if direct != "https://developer4.lanrar.com/file/CODE_abc" {
		t.Errorf("direct = %q", direct)
	}
	if filename != "鬼屋 & 雾夜.zip" {
		t.Errorf("filename = %q", filename)
	}

	ls.mu.Lock()
	posted, postRef, hdr, fnRef := ls.posted, ls.postRef, ls.postHdr, ls.fnRef
	ls.mu.Unlock()
	want := map[string]string{
		"action": "downprocess", "websignkey": "AJAX_DATA_456", "signs": "AJAX_DATA_456",
		"sign": "SIGN_VALUE_123", "websign": "", "kd": "1", "ves": "1",
	}
	for k, v := range want {
		if vals, ok := posted[k]; !ok || len(vals) != 1 || vals[0] != v {
			t.Errorf("表单 %s = %v, want %q", k, vals, v)
		}
	}
	if len(posted) != len(want) {
		t.Errorf("表单多了参数: %v", posted)
	}
	if fnRef != "https://wwbth.lanzov.com/iNWGo4fuqqli" {
		t.Errorf("fn 页的 Referer = %q，应为分享页地址", fnRef)
	}
	if !strings.HasPrefix(postRef, "https://wwbth.lanzov.com/fn?") {
		t.Errorf("ajaxfile 的 Referer = %q，应为 fn 页地址", postRef)
	}
	if hdr.Get("X-Requested-With") != "XMLHttpRequest" {
		t.Errorf("缺少 X-Requested-With: %v", hdr)
	}
	if !strings.HasPrefix(hdr.Get("Content-Type"), "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q", hdr.Get("Content-Type"))
	}
	if hdr.Get("User-Agent") != userAgent {
		t.Errorf("User-Agent = %q", hdr.Get("User-Agent"))
	}

	data, err := c.Download(ctx, direct, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, ls.zipBody) {
		t.Fatalf("下载内容与服务器上的 zip 不一致（%d 字节 vs %d 字节）", len(data), len(ls.zipBody))
	}
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.fileHits != 2 {
		t.Errorf("直链请求次数 = %d，应为 2（挑战一次 + 带 cookie 重试一次）", ls.fileHits)
	}
	if !ls.cookieOK {
		t.Error("第二次请求没有带上正确的 acw_sc__v2")
	}
}

func TestResolveLanzouFallsBackToDomain2(t *testing.T) {
	var apiHost string
	mux := http.NewServeMux()
	mux.HandleFunc("/s", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sharePageHTML))
	})
	mux.HandleFunc("/fn", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<script>var wp_sign = 'S';var ajaxdata = 'D';
			var domain2 = 'https://apifile.lanzouw.com/ajaxfile.php?file=1';</script>`))
	})
	mux.HandleFunc("/ajaxfile.php", func(w http.ResponseWriter, r *http.Request) {
		apiHost = r.Host
		_, _ = w.Write([]byte(`{"zt":1,"dom":"https://developer9.lanrar.com/","url":"/Z"}`))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	direct, _, err := newAliasClient(srv).ResolveLanzou(context.Background(), "https://wwbth.lanzov.com/s")
	if err != nil {
		t.Fatal(err)
	}
	if apiHost != "apifile.lanzouw.com" {
		t.Errorf("应使用 domain2，实际请求主机 = %q", apiHost)
	}
	if direct != "https://developer9.lanrar.com/file/Z" {
		t.Errorf("direct = %q（zt 为数字 1 也应接受，dom 结尾斜杠应去重）", direct)
	}
}

func TestResolveLanzouZtNotOne(t *testing.T) {
	ls, c, closeFn := newLanzouFixture(t)
	defer closeFn()
	ls.ajaxResp = `{"zt":"0","inf":"文件取消分享了"}`
	_, _, err := c.ResolveLanzou(context.Background(), "https://wwbth.lanzov.com/iNWGo4fuqqli")
	if err == nil || !strings.Contains(err.Error(), "文件取消分享了") || !strings.Contains(err.Error(), "zt=0") {
		t.Fatalf("err = %v，应带上 zt 与 inf", err)
	}
	if errors.Is(err, ErrChallenge) {
		t.Fatal("zt != 1 不是挑战")
	}
}

func TestResolveLanzouDirectOnDisallowedHost(t *testing.T) {
	ls, c, closeFn := newLanzouFixture(t)
	defer closeFn()
	ls.ajaxResp = `{"zt":"1","dom":"https://evil.example.com","url":"x"}`
	_, _, err := c.ResolveLanzou(context.Background(), "https://wwbth.lanzov.com/iNWGo4fuqqli")
	if !errors.Is(err, ErrURLNotAllowed) || !strings.Contains(err.Error(), "evil.example.com") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveLanzouRejectsBadShareURL(t *testing.T) {
	c := NewClient()
	for _, raw := range []string{"http://wwbth.lanzov.com/x", "https://evil.example.com/x", "https://evilcnmods.net/x"} {
		_, _, err := c.ResolveLanzou(context.Background(), raw)
		if !errors.Is(err, ErrURLNotAllowed) {
			t.Errorf("ResolveLanzou(%q) err = %v", raw, err)
		}
	}
}

func TestResolveLanzouChallenge(t *testing.T) {
	challenge := `<html><script>var arg1='` + testArg1 + `';var _0x1=function(){};setTimeout(...)</script></html>`
	t.Run("lanzoum 脚本挑战", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(challenge))
		}))
		defer srv.Close()
		_, _, err := newAliasClient(srv).ResolveLanzou(context.Background(), "https://wwbth.lanzoum.com/iNWGo4fuqqli")
		if !errors.Is(err, ErrChallenge) {
			t.Fatalf("err = %v，期望 ErrChallenge", err)
		}
	})
	t.Run("lanzoum 取不到 iframe 即使没有 arg1", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<html><body>请在浏览器中打开</body></html>`))
		}))
		defer srv.Close()
		_, _, err := newAliasClient(srv).ResolveLanzou(context.Background(), "https://cnmods.lanzoum.com/iabc")
		if !errors.Is(err, ErrChallenge) {
			t.Fatalf("err = %v，期望 ErrChallenge", err)
		}
	})
	t.Run("lanzov 含 arg1 但没有 iframe", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(challenge))
		}))
		defer srv.Close()
		_, _, err := newAliasClient(srv).ResolveLanzou(context.Background(), "https://wwbth.lanzov.com/iNWGo4fuqqli")
		if !errors.Is(err, ErrChallenge) {
			t.Fatalf("err = %v，期望 ErrChallenge", err)
		}
	})
	t.Run("lanzov 普通失效页不是挑战", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<html><body>文件取消分享了</body></html>`))
		}))
		defer srv.Close()
		_, _, err := newAliasClient(srv).ResolveLanzou(context.Background(), "https://wwbth.lanzov.com/iNWGo4fuqqli")
		if err == nil || errors.Is(err, ErrChallenge) {
			t.Fatalf("err = %v，应为非挑战错误", err)
		}
	})
	t.Run("fn 页返回挑战", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/fn" {
				_, _ = w.Write([]byte(challenge))
				return
			}
			_, _ = w.Write([]byte(sharePageHTML))
		}))
		defer srv.Close()
		_, _, err := newAliasClient(srv).ResolveLanzou(context.Background(), "https://wwbth.lanzov.com/iNWGo4fuqqli")
		if !errors.Is(err, ErrChallenge) {
			t.Fatalf("err = %v，期望 ErrChallenge", err)
		}
	})
}

func TestDownloadStillHTMLAfterRetryIsChallenge(t *testing.T) {
	for _, tc := range []struct {
		name string
		mod  func(*lanzouServer)
	}{
		{"算法失效（cookie 不被接受）", func(ls *lanzouServer) { ls.wrongSolution = true }},
		{"永远返回挑战", func(ls *lanzouServer) { ls.alwaysChallenge = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ls, c, closeFn := newLanzouFixture(t)
			defer closeFn()
			tc.mod(ls)
			_, err := c.Download(context.Background(), "https://developer4.lanrar.com/file/CODE_abc", 1<<20)
			if !errors.Is(err, ErrChallenge) {
				t.Fatalf("err = %v，期望 ErrChallenge", err)
			}
			ls.mu.Lock()
			defer ls.mu.Unlock()
			if ls.fileHits != 2 {
				t.Errorf("直链请求 %d 次，应恰好重试 1 次共 2 次", ls.fileHits)
			}
		})
	}
}

func TestDownloadHTMLWithoutArg1IsChallenge(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>access denied</body></html>`))
	}))
	defer srv.Close()
	_, err := newAliasClient(srv).Download(context.Background(), "https://developer4.lanrar.com/file/x", 1<<20)
	if !errors.Is(err, ErrChallenge) {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadPlainFileNoChallenge(t *testing.T) {
	body := makeZip(t, map[string]string{"a.txt": "x"})
	var hits int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/html") // 类型头说谎也不影响判断
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	got, err := newAliasClient(srv).Download(context.Background(), "https://developer4.lanrar.com/file/x", 1<<20)
	if err != nil || !bytes.Equal(got, body) || hits != 1 {
		t.Fatalf("err=%v hits=%d equal=%v", err, hits, bytes.Equal(got, body))
	}
}

func TestDownloadLimits(t *testing.T) {
	body := bytes.Repeat([]byte("PK\x03\x04"), 1000) // 4000 字节
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c := newAliasClient(srv)
	if _, err := c.Download(context.Background(), "https://developer4.lanrar.com/file/x", 3999); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("超出 1 字节应报 ErrTooLarge，err = %v", err)
	}
	if got, err := c.Download(context.Background(), "https://developer4.lanrar.com/file/x", 4000); err != nil || len(got) != 4000 {
		t.Fatalf("恰好等于上限应成功: len=%d err=%v", len(got), err)
	}
}

func TestDownloadRejectsDisallowedHost(t *testing.T) {
	_, err := NewClient().Download(context.Background(), "https://evil.example.com/file/x", 1<<20)
	if !errors.Is(err, ErrURLNotAllowed) || !strings.Contains(err.Error(), "evil.example.com") {
		t.Fatalf("err = %v", err)
	}
}
