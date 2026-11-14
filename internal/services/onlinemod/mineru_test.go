// NOTE: mineru_test.go 覆盖 MinerU 客户端：同一个 httptest TLS 服务同时扮演 API、上传端点和结果 CDN，不联网。
// 白名单外的「恶意主机」是另一个计数服务，借自定义 DialContext 把 evil.test 拨到它上面：
// 如果实现漏了校验，请求会真的到达它，计数就会大于 0。
// 期望值（请求体字段、zip 内容、轮询次数）由测试自己按接口文档推出，不取自实现的输出。
package onlinemod

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	fakeMinerUToken   = "tok-SECRET-0123456789"
	fakeMinerUBatchID = "batch-123"
	fakeMinerUMD      = "# 鬼屋\n\n调查员在雨夜抵达废弃的宅邸。\n\n| 技能 | 值 |\n| --- | --- |\n| 侦查 | 60 |\n"
)

var fakePDF = []byte("%PDF-1.4\n% 假的 PDF 字节\x00\x01\xff\n%%EOF\n")

// NOTE: fakeMinerU 是假的 MinerU 服务。配置项和观察项都在 mu 之下读写，避免 -race 报数据竞争。
type fakeMinerU struct {
	srv *httptest.Server

	mu sync.Mutex
	// NOTE: 以下是配置项。
	states       []string // 第 n 次轮询返回 states[n-1]，用完后重复最后一项；空串表示 extract_result 为空
	errMsg       string
	applyCode    int
	applyMsg     string
	applyRaw     string
	applyStatus  int
	fileURL      string
	zipURL       string
	zipBody      []byte
	uploadStatus int
	redirectTo   string
	// NOTE: 以下是观察项。
	total        int
	applyAuth    string
	applyCT      string
	applyBody    []byte
	uploads      int
	uploadMethod string
	uploadHeader http.Header
	uploadBody   []byte
	uploadQuery  string
	polls        int
	pollAuth     []string
	cdnHits      int
	cdnAuth      string
	polled       chan struct{}
}

func newFakeMinerU(t *testing.T) *fakeMinerU {
	t.Helper()
	f := &fakeMinerU{polled: make(chan struct{}, 64)}
	srv := httptest.NewTLSServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	f.locked(func() {
		f.srv = srv
		f.states = []string{"done"}
		f.zipBody = buildZip(t, zipEnt{name: "full.md", body: []byte(fakeMinerUMD)})
		f.fileURL = srv.URL + "/upload/obj-1"
		f.zipURL = srv.URL + "/cdn/result.zip"
	})
	return f
}

func (f *fakeMinerU) locked(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func writeFakeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeMinerU) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.total++
	p := r.URL.Path
	switch {
	case r.Method == http.MethodPost && p == "/api/v4/file-urls/batch":
		f.applyAuth = r.Header.Get("Authorization")
		f.applyCT = r.Header.Get("Content-Type")
		f.applyBody = body
		if f.applyRaw != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(max(f.applyStatus, http.StatusOK))
			_, _ = io.WriteString(w, f.applyRaw)
			return
		}
		if f.applyCode != 0 {
			writeFakeJSON(w, http.StatusOK, map[string]any{"code": f.applyCode, "msg": f.applyMsg, "trace_id": "trace-1"})
			return
		}
		writeFakeJSON(w, http.StatusOK, map[string]any{
			"code": 0, "msg": "ok", "trace_id": "trace-1",
			"data": map[string]any{"batch_id": fakeMinerUBatchID, "file_urls": []string{f.fileURL}},
		})

	case strings.HasPrefix(p, "/upload/"):
		f.uploads++
		f.uploadMethod = r.Method
		f.uploadHeader = r.Header.Clone()
		f.uploadBody = body
		f.uploadQuery = r.URL.RawQuery
		w.WriteHeader(max(f.uploadStatus, http.StatusOK))

	case r.Method == http.MethodGet && p == "/api/v4/extract-results/batch/"+fakeMinerUBatchID:
		f.polls++
		f.pollAuth = append(f.pollAuth, r.Header.Get("Authorization"))
		select {
		case f.polled <- struct{}{}:
		default:
		}
		state := f.states[min(f.polls, len(f.states))-1]
		items := []any{}
		if state != "" {
			item := map[string]any{"file_name": "x.pdf", "data_id": "ignored", "state": state, "err_msg": ""}
			switch state {
			case "failed":
				item["err_msg"] = f.errMsg
			case "done":
				item["full_zip_url"] = f.zipURL
			case "running":
				item["extract_progress"] = map[string]any{"extracted_pages": 1, "total_pages": 2, "start_time": "2025-01-20 11:43:20"}
			}
			items = append(items, item)
		}
		writeFakeJSON(w, http.StatusOK, map[string]any{
			"code": 0, "msg": "ok",
			"data": map[string]any{"batch_id": fakeMinerUBatchID, "extract_result": items},
		})

	case r.Method == http.MethodGet && p == "/cdn/result.zip":
		f.cdnHits++
		f.cdnAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(f.zipBody)

	case p == "/redirect":
		http.Redirect(w, r, f.redirectTo, http.StatusTemporaryRedirect)

	default:
		http.NotFound(w, r)
	}
}

// NOTE: evilServer 是白名单外的主机，只数请求。
type evilServer struct {
	srv  *httptest.Server
	hits atomic.Int32
}

func newEvilServer(t *testing.T) *evilServer {
	t.Helper()
	e := &evilServer{}
	e.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *evilServer) url(path string) string {
	_, port, _ := net.SplitHostPort(e.srv.Listener.Addr().String())
	return "https://evil.test:" + port + path
}

// NOTE: client 返回指向假服务的 MinerU：只放行 127.0.0.1，轮询间隔调到毫秒级。
// evil 非空时，evil.test 会被拨到那台服务上。
func (f *fakeMinerU) client(token string, evil *evilServer) *MinerU {
	tr := f.srv.Client().Transport.(*http.Transport).Clone()
	// NOTE: 测试证书不含 evil.test，所以跳过校验；仅限测试。
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	mainAddr := f.srv.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		target := mainAddr
		if evil != nil && host == "evil.test" {
			target = evil.srv.Listener.Addr().String()
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
	return &MinerU{
		token:        token,
		baseURL:      f.srv.URL,
		hc:           &http.Client{Transport: tr},
		allowHost:    func(h string) bool { return h == "127.0.0.1" },
		pollInterval: 5 * time.Millisecond,
		pollTimeout:  10 * time.Second,
	}
}

// NOTE: runMinerU 带 20 秒兜底超时，避免实现卡住时整个测试挂死。
func runMinerU(t *testing.T, m *MinerU, name string, data []byte) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return m.ParsePDF(ctx, name, data)
}

func swapMinerUVar[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

func TestMinerUHappyPath(t *testing.T) {
	f := newFakeMinerU(t)
	f.locked(func() {
		f.states = []string{"pending", "running", "done"}
		f.fileURL = f.srv.URL + "/upload/obj-1?Expires=1700000000&Signature=abc%2Fdef"
		f.zipBody = buildZip(t,
			zipEnt{name: "images/p1.jpg", body: []byte{0xff, 0xd8, 0xff}},
			zipEnt{name: "layout.json", body: []byte(`{"pdf_info":[]}`)},
			zipEnt{name: "full.md", body: []byte(fakeMinerUMD)},
		)
	})
	m := f.client(fakeMinerUToken, nil)

	got, err := runMinerU(t, m, "x.pdf", fakePDF)
	if err != nil {
		t.Fatal(err)
	}
	if got != fakeMinerUMD {
		t.Fatalf("返回文本 = %q，期望 %q", got, fakeMinerUMD)
	}

	f.locked(func() {
		// NOTE: 申请请求。
		if f.applyAuth != "Bearer "+fakeMinerUToken {
			t.Errorf("申请请求的 Authorization = %q", f.applyAuth)
		}
		if f.applyCT != "application/json" {
			t.Errorf("申请请求的 Content-Type = %q", f.applyCT)
		}
		var req map[string]any
		if err := json.Unmarshal(f.applyBody, &req); err != nil {
			t.Fatalf("申请体不是 JSON: %v\n%s", err, f.applyBody)
		}
		if req["model_version"] != "vlm" {
			t.Errorf("model_version = %v，期望 vlm", req["model_version"])
		}
		if req["language"] != "ch" {
			t.Errorf("language = %v，期望 ch", req["language"])
		}
		if v, ok := req["enable_formula"].(bool); !ok || v {
			t.Errorf("enable_formula = %v，期望显式 false", req["enable_formula"])
		}
		if v, ok := req["enable_table"].(bool); !ok || !v {
			t.Errorf("enable_table = %v，期望 true", req["enable_table"])
		}
		files, _ := req["files"].([]any)
		if len(files) != 1 {
			t.Fatalf("files 长度 = %d，期望 1", len(files))
		}
		file, _ := files[0].(map[string]any)
		if file["name"] != "x.pdf" {
			t.Errorf("files[0].name = %v，期望 x.pdf", file["name"])
		}
		if v, ok := file["is_ocr"].(bool); !ok || !v {
			t.Errorf("files[0].is_ocr = %v，必须为 true（文字层是坏的，不强制 OCR 会读同一个坏文字层）", file["is_ocr"])
		}
		if id, _ := file["data_id"].(string); !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`).MatchString(id) {
			t.Errorf("data_id = %q，不符合文档的字符集或长度", id)
		}

		// NOTE: 上传请求。
		if f.uploads != 1 || f.uploadMethod != http.MethodPut {
			t.Errorf("上传次数 = %d，方法 = %q，期望 1 次 PUT", f.uploads, f.uploadMethod)
		}
		if _, has := f.uploadHeader["Content-Type"]; has {
			t.Errorf("上传不能带 Content-Type（文档要求），实际 %q", f.uploadHeader.Get("Content-Type"))
		}
		if a := f.uploadHeader.Get("Authorization"); a != "" {
			t.Errorf("上传请求带了 Authorization %q：token 不能发给预签名链接的主机", a)
		}
		if !bytes.Equal(f.uploadBody, fakePDF) {
			t.Errorf("上传 body = %q，期望等于输入 %q", f.uploadBody, fakePDF)
		}
		if f.uploadQuery != "Expires=1700000000&Signature=abc%2Fdef" {
			t.Errorf("预签名链接的查询串被改动: %q", f.uploadQuery)
		}

		// NOTE: 轮询：pending → running → done 共 3 次，每次都带 token
		if f.polls != 3 {
			t.Errorf("轮询次数 = %d，期望 3", f.polls)
		}
		for i, a := range f.pollAuth {
			if a != "Bearer "+fakeMinerUToken {
				t.Errorf("第 %d 次轮询的 Authorization = %q", i+1, a)
			}
		}

		// NOTE: 结果下载。
		if f.cdnHits != 1 {
			t.Errorf("下载次数 = %d，期望 1", f.cdnHits)
		}
		if f.cdnAuth != "" {
			t.Errorf("下载请求带了 Authorization %q：token 不能发给 CDN", f.cdnAuth)
		}
	})
}

func TestMinerUEmptyExtractResultKeepsWaiting(t *testing.T) {
	f := newFakeMinerU(t)
	f.locked(func() { f.states = []string{"", "waiting-file", "converting", "done"} })

	got, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
	if err != nil {
		t.Fatal(err)
	}
	if got != fakeMinerUMD {
		t.Fatalf("返回文本 = %q", got)
	}
	f.locked(func() {
		if f.polls != 4 {
			t.Errorf("轮询次数 = %d，期望 4", f.polls)
		}
	})
}

func TestMinerUFailedStateCarriesErrMsg(t *testing.T) {
	f := newFakeMinerU(t)
	const reason = "文件页数超过限制：共 312 页"
	f.locked(func() {
		f.states = []string{"running", "failed"}
		f.errMsg = reason
	})

	_, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
	if err == nil {
		t.Fatal("state=failed 应返回错误")
	}
	if !strings.Contains(err.Error(), reason) {
		t.Errorf("错误里应含 err_msg %q: %v", reason, err)
	}
	if strings.Contains(err.Error(), fakeMinerUToken) {
		t.Errorf("错误里不能出现 token: %v", err)
	}
	f.locked(func() {
		if f.polls != 2 {
			t.Errorf("轮询次数 = %d，failed 后应立即停止，期望 2", f.polls)
		}
		if f.cdnHits != 0 {
			t.Errorf("失败任务不应下载结果，实际下载 %d 次", f.cdnHits)
		}
	})
}

func TestMinerUUnknownStateIsFailure(t *testing.T) {
	f := newFakeMinerU(t)
	f.locked(func() { f.states = []string{"mystery-state"} })

	_, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
	if err == nil || !strings.Contains(err.Error(), "mystery-state") {
		t.Fatalf("未知 state 应按失败处理并带上状态名，实际 err = %v", err)
	}
	f.locked(func() {
		if f.polls != 1 {
			t.Errorf("未知状态不应继续等待，轮询次数 = %d", f.polls)
		}
	})
}

func TestMinerUApplyErrorCode(t *testing.T) {
	const msg = "每日解析任务数量已达上限"
	f := newFakeMinerU(t)
	f.locked(func() {
		f.applyCode = -60018
		f.applyMsg = msg
	})

	_, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
	if err == nil {
		t.Fatal("code 非 0 应返回错误")
	}
	for _, want := range []string{"-60018", msg} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误里应含 %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), fakeMinerUToken) {
		t.Errorf("错误里不能出现 token: %v", err)
	}
	f.locked(func() {
		if f.uploads != 0 || f.polls != 0 {
			t.Errorf("申请失败后不应上传或轮询：uploads=%d polls=%d", f.uploads, f.polls)
		}
	})
}

func TestMinerUApplyStringCodeAndHTTPStatus(t *testing.T) {
	f := newFakeMinerU(t)
	f.locked(func() {
		f.applyStatus = http.StatusUnauthorized
		f.applyRaw = `{"code":"A0202","msg":"Token 错误"}`
	})

	_, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
	if err == nil {
		t.Fatal("应返回错误")
	}
	for _, want := range []string{"A0202", "Token 错误", "401"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误里应含 %q: %v", want, err)
		}
	}
}

func TestMinerUErrorScrubsTokenEchoedByServer(t *testing.T) {
	f := newFakeMinerU(t)
	f.locked(func() {
		f.applyCode = -500
		f.applyMsg = "bad header: Authorization: Bearer " + fakeMinerUToken
	})

	_, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
	if err == nil {
		t.Fatal("应返回错误")
	}
	if strings.Contains(err.Error(), fakeMinerUToken) {
		t.Errorf("服务端回显的 token 必须被抹掉: %v", err)
	}
	if !strings.Contains(err.Error(), "-500") {
		t.Errorf("错误里应保留 code: %v", err)
	}
}

func TestMinerUUploadHTTPError(t *testing.T) {
	f := newFakeMinerU(t)
	f.locked(func() { f.uploadStatus = http.StatusForbidden })

	_, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("上传返回 403 应报错并带状态码，实际 err = %v", err)
	}
	f.locked(func() {
		if f.polls != 0 {
			t.Errorf("上传失败后不应轮询，轮询 %d 次", f.polls)
		}
	})
}

func TestMinerURejectsUploadURLOutsideAllowlist(t *testing.T) {
	evil := newEvilServer(t)
	f := newFakeMinerU(t)
	cases := []struct {
		name string
		url  string
	}{
		{"白名单外主机", evil.url("/upload/obj-1")},
		{"同一主机但 http", strings.Replace(f.srv.URL, "https://", "http://", 1) + "/upload/obj-1"},
		{"带用户信息", strings.Replace(f.srv.URL, "https://", "https://user:pw@", 1) + "/upload/obj-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.locked(func() { f.fileURL = tc.url })
			_, err := runMinerU(t, f.client(fakeMinerUToken, evil), "x.pdf", fakePDF)
			if !errors.Is(err, ErrURLNotAllowed) {
				t.Fatalf("err = %v，期望 ErrURLNotAllowed", err)
			}
			if n := evil.hits.Load(); n != 0 {
				t.Errorf("白名单外主机收到 %d 个请求，期望 0", n)
			}
			f.locked(func() {
				if f.uploads != 0 {
					t.Errorf("不应有上传，实际 %d 次", f.uploads)
				}
				if f.polls != 0 {
					t.Errorf("被拒绝后不应轮询，实际 %d 次", f.polls)
				}
			})
		})
	}
}

func TestMinerURejectsRedirectOutOfAllowlist(t *testing.T) {
	evil := newEvilServer(t)
	f := newFakeMinerU(t)
	f.locked(func() {
		f.fileURL = f.srv.URL + "/redirect"
		f.redirectTo = evil.url("/steal")
	})

	_, err := runMinerU(t, f.client(fakeMinerUToken, evil), "x.pdf", fakePDF)
	if !errors.Is(err, ErrURLNotAllowed) {
		t.Fatalf("重定向到白名单外应报 ErrURLNotAllowed，实际 err = %v", err)
	}
	if n := evil.hits.Load(); n != 0 {
		t.Errorf("白名单外主机收到 %d 个请求，期望 0", n)
	}
}

func TestMinerURejectsZipURLOutsideAllowlist(t *testing.T) {
	evil := newEvilServer(t)
	f := newFakeMinerU(t)
	f.locked(func() { f.zipURL = evil.url("/result.zip") })

	_, err := runMinerU(t, f.client(fakeMinerUToken, evil), "x.pdf", fakePDF)
	if !errors.Is(err, ErrURLNotAllowed) {
		t.Fatalf("err = %v，期望 ErrURLNotAllowed", err)
	}
	if n := evil.hits.Load(); n != 0 {
		t.Errorf("白名单外主机收到 %d 个请求，期望 0", n)
	}
	f.locked(func() {
		if f.cdnHits != 0 {
			t.Errorf("不应下载结果，实际 %d 次", f.cdnHits)
		}
	})
}

func TestMinerURejectsBaseURLOutsideDefaultAllowlist(t *testing.T) {
	f := newFakeMinerU(t)
	m := f.client(fakeMinerUToken, nil)
	// NOTE: allowHost 为 nil 时必须回落到 MinerU 自己的白名单，而不是 cnmods 的，也不是放行所有。
	m.allowHost = nil

	_, err := runMinerU(t, m, "x.pdf", fakePDF)
	if !errors.Is(err, ErrURLNotAllowed) {
		t.Fatalf("err = %v，期望 ErrURLNotAllowed", err)
	}
	f.locked(func() {
		if f.total != 0 {
			t.Errorf("假服务收到 %d 个请求，期望 0", f.total)
		}
	})
}

func TestMinerUResultZipLayouts(t *testing.T) {
	cases := []struct {
		name    string
		ents    []zipEnt
		want    string
		wantErr string
	}{
		{
			name: "full.md 在子目录下",
			ents: []zipEnt{
				{name: "9f2c7a/images/p1.jpg", body: []byte{0xff, 0xd8}},
				{name: "9f2c7a/layout.json", body: []byte(`{}`)},
				{name: "9f2c7a/full.md", body: []byte(fakeMinerUMD)},
			},
			want: fakeMinerUMD,
		},
		{
			name: "多个同名时取最浅的",
			ents: []zipEnt{
				{name: "a/b/full.md", body: []byte("DEEP")},
				{name: "full.md", body: []byte("ROOT")},
			},
			want: "ROOT",
		},
		{
			name: "反斜杠路径分隔符",
			ents: []zipEnt{{name: "out\\full.md", body: []byte("WIN")}},
			want: "WIN",
		},
		{
			name: "只有名字相近的文件",
			ents: []zipEnt{
				{name: "layout.json", body: []byte(`{}`)},
				{name: "images/full.md.png", body: []byte{1}},
				{name: "notes/xfull.md", body: []byte("不是它")},
				{name: "full.markdown", body: []byte("不是它")},
			},
			wantErr: "full.md",
		},
		{
			name:    "full.md 为空白",
			ents:    []zipEnt{{name: "full.md", body: []byte(" \n\t\n")}},
			wantErr: "没有内容",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeMinerU(t)
			f.locked(func() { f.zipBody = buildZip(t, tc.ents...) })

			got, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v，期望含 %q", err, tc.wantErr)
				}
				if got != "" {
					t.Errorf("出错时不应返回文本，实际 %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("返回 %q，期望 %q", got, tc.want)
			}
		})
	}
}

func TestMinerUResultIsNotZip(t *testing.T) {
	f := newFakeMinerU(t)
	f.locked(func() { f.zipBody = []byte("<html>这不是 zip</html>") })

	_, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
	if err == nil || !strings.Contains(err.Error(), "zip") {
		t.Fatalf("非 zip 结果应报错，实际 err = %v", err)
	}
}

func TestMinerUSizeLimits(t *testing.T) {
	t.Run("zip 条目数", func(t *testing.T) {
		swapMinerUVar(t, &maxZipEntries, 3)
		f := newFakeMinerU(t)
		f.locked(func() {
			f.zipBody = buildZip(t,
				zipEnt{name: "a.json", body: []byte("1")},
				zipEnt{name: "b.json", body: []byte("2")},
				zipEnt{name: "c.json", body: []byte("3")},
				zipEnt{name: "full.md", body: []byte("正文")},
			)
		})
		_, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
		if !errors.Is(err, errArchiveLimit) {
			t.Fatalf("4 个条目、上限 3，期望 errArchiveLimit，实际 %v", err)
		}
	})

	t.Run("zip 条目数恰好等于上限", func(t *testing.T) {
		swapMinerUVar(t, &maxZipEntries, 2)
		f := newFakeMinerU(t)
		f.locked(func() {
			f.zipBody = buildZip(t,
				zipEnt{name: "a.json", body: []byte("1")},
				zipEnt{name: "full.md", body: []byte("正文")},
			)
		})
		got, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
		if err != nil || got != "正文" {
			t.Fatalf("got = %q, err = %v", got, err)
		}
	})

	t.Run("full.md 解压后超限", func(t *testing.T) {
		swapMinerUVar(t, &maxMinerUMarkdownBytes, 64)
		f := newFakeMinerU(t)
		// NOTE: 65 字节的高压缩比内容：zip 本身很小，解压后超过 64 字节。
		f.locked(func() { f.zipBody = buildZip(t, zipEnt{name: "full.md", body: bytes.Repeat([]byte("a"), 65)}) })
		_, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("期望 ErrTooLarge，实际 %v", err)
		}
	})

	t.Run("full.md 恰好等于上限", func(t *testing.T) {
		swapMinerUVar(t, &maxMinerUMarkdownBytes, 64)
		f := newFakeMinerU(t)
		want := strings.Repeat("a", 64)
		f.locked(func() { f.zipBody = buildZip(t, zipEnt{name: "full.md", body: []byte(want)}) })
		got, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
		if err != nil || got != want {
			t.Fatalf("got = %q, err = %v", got, err)
		}
	})

	t.Run("zip 下载超限", func(t *testing.T) {
		swapMinerUVar(t, &maxMinerUZipBytes, 100)
		f := newFakeMinerU(t)
		f.locked(func() { f.zipBody = bytes.Repeat([]byte("PK"), 100) })
		_, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("期望 ErrTooLarge，实际 %v", err)
		}
	})

	t.Run("JSON 响应超限", func(t *testing.T) {
		swapMinerUVar(t, &maxMinerUJSONBytes, 32)
		f := newFakeMinerU(t)
		_, err := runMinerU(t, f.client(fakeMinerUToken, nil), "x.pdf", fakePDF)
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("期望 ErrTooLarge，实际 %v", err)
		}
	})
}

func TestMinerUPollTimeout(t *testing.T) {
	f := newFakeMinerU(t)
	f.locked(func() { f.states = []string{"running"} })
	m := f.client(fakeMinerUToken, nil)
	// NOTE: 间隔设成 1 小时：超时必须能打断等待，而不是等到下一次轮询。
	m.pollInterval = time.Hour
	m.pollTimeout = 150 * time.Millisecond

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := m.ParsePDF(ctx, "x.pdf", fakePDF)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrMinerUTimeout) {
		t.Fatalf("err = %v，期望 ErrMinerUTimeout", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("耗时 %v，超时没有及时生效", elapsed)
	}
	if ctx.Err() != nil {
		t.Errorf("测试兜底 ctx 已超时，说明实现卡住了")
	}
	f.locked(func() {
		if f.polls != 1 {
			t.Errorf("间隔 1 小时下应只轮询 1 次，实际 %d", f.polls)
		}
	})
}

func TestMinerUPollTimeoutWithShortInterval(t *testing.T) {
	f := newFakeMinerU(t)
	f.locked(func() { f.states = []string{"running"} })
	m := f.client(fakeMinerUToken, nil)
	m.pollInterval = 5 * time.Millisecond
	m.pollTimeout = 150 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := m.ParsePDF(ctx, "x.pdf", fakePDF)
	if !errors.Is(err, ErrMinerUTimeout) {
		t.Fatalf("err = %v，期望 ErrMinerUTimeout", err)
	}
	f.locked(func() {
		if f.polls < 2 {
			t.Errorf("5ms 间隔、150ms 内应轮询多次，实际 %d", f.polls)
		}
	})
}

func TestMinerUContextCancelReturnsImmediately(t *testing.T) {
	f := newFakeMinerU(t)
	f.locked(func() { f.states = []string{"running"} })
	m := f.client(fakeMinerUToken, nil)
	// NOTE: 间隔和总超时都设得很长：只有 ctx 取消能让它返回。
	m.pollInterval = time.Hour
	m.pollTimeout = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := m.ParsePDF(ctx, "x.pdf", fakePDF)
		done <- result{text, err}
	}()

	select {
	case <-f.polled:
	case <-time.After(10 * time.Second):
		t.Fatal("等不到第一次轮询")
	}
	cancel()

	select {
	case r := <-done:
		if !errors.Is(r.err, context.Canceled) {
			t.Errorf("err = %v，期望 context.Canceled", r.err)
		}
		if errors.Is(r.err, ErrMinerUTimeout) {
			t.Errorf("取消不应被报成超时: %v", r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消后 5 秒内没有返回")
	}
}

func TestMinerUContextAlreadyCanceled(t *testing.T) {
	f := newFakeMinerU(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := f.client(fakeMinerUToken, nil).ParsePDF(ctx, "x.pdf", fakePDF)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v，期望 context.Canceled", err)
	}
	f.locked(func() {
		if f.uploads != 0 {
			t.Errorf("已取消的 ctx 不应上传，实际 %d 次", f.uploads)
		}
	})
}

func TestMinerUNotConfigured(t *testing.T) {
	f := newFakeMinerU(t)
	for _, token := range []string{"", "   ", "\t\n"} {
		// NOTE: 空白 token 的裁剪发生在 NewMinerU 里，所以走它构造，再把网络换成假服务，实现出错时也不会联网。
		fake := f.client("unused", nil)
		m := NewMinerU(token)
		m.baseURL, m.hc, m.allowHost = fake.baseURL, fake.hc, fake.allowHost
		if m.Configured() {
			t.Errorf("token %q 不应视为已配置", token)
		}
		_, err := runMinerU(t, m, "x.pdf", fakePDF)
		if !errors.Is(err, ErrMinerUNotConfigured) {
			t.Errorf("token %q：err = %v，期望 ErrMinerUNotConfigured", token, err)
		}
	}
	f.locked(func() {
		if f.total != 0 {
			t.Errorf("未配置 token 时假服务收到 %d 个请求，期望 0", f.total)
		}
	})

	if m := NewMinerU(""); m.Configured() {
		t.Error("NewMinerU(\"\") 不应已配置")
	}
	if _, err := NewMinerU("").ParsePDF(context.Background(), "x.pdf", fakePDF); !errors.Is(err, ErrMinerUNotConfigured) {
		t.Errorf("err = %v，期望 ErrMinerUNotConfigured", err)
	}
	var nilClient *MinerU
	if nilClient.Configured() {
		t.Error("nil *MinerU 不应已配置")
	}
	if _, err := nilClient.ParsePDF(context.Background(), "x.pdf", fakePDF); !errors.Is(err, ErrMinerUNotConfigured) {
		t.Errorf("nil 接收者：err = %v，期望 ErrMinerUNotConfigured", err)
	}
	if !NewMinerU("tok").Configured() {
		t.Error("NewMinerU(\"tok\") 应已配置")
	}
}

func TestMinerURejectsEmptyAndOversizedInput(t *testing.T) {
	swapMinerUVar(t, &maxMinerUUploadBytes, 16)
	f := newFakeMinerU(t)
	m := f.client(fakeMinerUToken, nil)

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"nil", nil},
		{"空切片", []byte{}},
		{"超出上限 1 字节", bytes.Repeat([]byte("x"), 17)},
		{"远超上限", bytes.Repeat([]byte("x"), 1000)},
	} {
		if _, err := runMinerU(t, m, "x.pdf", tc.data); err == nil {
			t.Errorf("%s：应被拒绝", tc.name)
		}
	}
	f.locked(func() {
		if f.total != 0 {
			t.Errorf("被拒绝的输入不应产生网络请求，假服务收到 %d 个", f.total)
		}
	})

	// NOTE: 恰好等于上限的输入放行。
	atLimit := bytes.Repeat([]byte("x"), 16)
	got, err := runMinerU(t, m, "x.pdf", atLimit)
	if err != nil || got != fakeMinerUMD {
		t.Fatalf("恰好等于上限应放行：got = %q, err = %v", got, err)
	}
	f.locked(func() {
		if !bytes.Equal(f.uploadBody, atLimit) {
			t.Errorf("上传 body 长度 = %d，期望 16", len(f.uploadBody))
		}
	})
}

func TestMinerUDefaultAllowlist(t *testing.T) {
	for _, h := range []string{
		"mineru.net",
		"MinerU.NET",
		"mineru.net.",
		"cdn-mineru.openxlab.org.cn",
		"mineru.oss-cn-shanghai.aliyuncs.com",
	} {
		if !minerUAllowHost(h) {
			t.Errorf("%q 应放行", h)
		}
	}
	for _, h := range []string{
		"",
		"api.mineru.net",
		"evilmineru.net",
		"mineru.net.evil.com",
		"openxlab.org.cn",
		"cdn-mineru.openxlab.org.cn.evil.com",
		"mineru.oss-cn-beijing.aliyuncs.com",
		"mineru.oss-cn-hangzhou.aliyuncs.com",
		"oss-cn-shanghai.aliyuncs.com",
		"other.oss-cn-shanghai.aliyuncs.com",
		"aliyuncs.com",
		"www.cnmods.net",
		"127.0.0.1",
	} {
		if minerUAllowHost(h) {
			t.Errorf("%q 不应放行", h)
		}
	}
	// NOTE: 两份白名单互相独立：cnmods 的白名单不放行 MinerU 域名，反之亦然。
	if defaultAllowHost("mineru.net") || defaultAllowHost("cdn-mineru.openxlab.org.cn") {
		t.Error("cnmods 的白名单不应放行 MinerU 域名")
	}
	if minerUAllowHost("lanzov.com") || minerUAllowHost("webgetstore.com") {
		t.Error("MinerU 的白名单不应放行蓝奏云域名")
	}
}

func TestNewMinerUDefaults(t *testing.T) {
	m := NewMinerU("  tok  ")
	if m.token != "tok" {
		t.Errorf("token = %q，期望去掉首尾空白", m.token)
	}
	if m.baseURL != "https://mineru.net" {
		t.Errorf("baseURL = %q", m.baseURL)
	}
	if m.pollInterval != 5*time.Second || m.pollTimeout != 8*time.Minute {
		t.Errorf("pollInterval = %v, pollTimeout = %v，期望 5s / 8m", m.pollInterval, m.pollTimeout)
	}
	if m.hc == nil || m.allowHost == nil {
		t.Fatal("hc 和 allowHost 不应为空")
	}
	if !m.allowHost("mineru.net") || m.allowHost("evil.example.com") || m.allowHost("127.0.0.1") {
		t.Error("默认 allowHost 应走 MinerU 白名单")
	}
	// NOTE: 申请接口的地址本身也要过白名单：http 被拒。
	if err := m.checkUpstream("测试", "http://mineru.net/x"); !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("http 地址应被拒，err = %v", err)
	}
	if err := m.checkUpstream("测试", "https://mineru.oss-cn-shanghai.aliyuncs.com/api-upload/x?Signature=s"); err != nil {
		t.Errorf("上海 OSS 的 https 链接应放行，err = %v", err)
	}
	if err := m.checkUpstream("测试", "https://mineru.oss-cn-beijing.aliyuncs.com/api-upload/x"); !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("其它地域的 OSS 应被拒，err = %v", err)
	}
}

func TestRedactURLErrorDropsSignature(t *testing.T) {
	inner := io.ErrUnexpectedEOF
	err := redactURLError(&url.Error{
		Op:  "Put",
		URL: "https://mineru.oss-cn-shanghai.aliyuncs.com/api-upload/x?Signature=SECRETSIG&Expires=1",
		Err: inner,
	})
	if strings.Contains(err.Error(), "SECRETSIG") || strings.Contains(err.Error(), "api-upload") {
		t.Errorf("错误信息泄露了预签名地址: %v", err)
	}
	if !strings.Contains(err.Error(), "mineru.oss-cn-shanghai.aliyuncs.com") {
		t.Errorf("错误信息应保留主机: %v", err)
	}
	if !errors.Is(err, inner) {
		t.Errorf("应保留底层错误链: %v", err)
	}
	plain := errors.New("plain")
	if redactURLError(plain) != plain {
		t.Error("非 *url.Error 应原样返回")
	}
}

func TestMinerUFileNameAndDataID(t *testing.T) {
	for in, want := range map[string]string{
		"x.pdf":              "x.pdf",
		"鬼屋 第二版.PDF":         "鬼屋 第二版.PDF",
		"scan":               "scan.pdf",
		"dir/sub/a.pdf":      "a.pdf",
		`C:\docs\b.pdf`:      "b.pdf",
		"../../etc/passwd":   "passwd.pdf",
		"":                   "document.pdf",
		"  ":                 "document.pdf",
		"/":                  "document.pdf",
		"trailing/slash.pdf": "slash.pdf",
	} {
		if got := minerUFileName(in); got != want {
			t.Errorf("minerUFileName(%q) = %q，期望 %q", in, got, want)
		}
	}

	re := regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
	seen := map[string]bool{}
	for range 20 {
		id, err := newMinerUDataID()
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(id) {
			t.Errorf("data_id %q 不符合文档的字符集或长度", id)
		}
		if seen[id] {
			t.Errorf("data_id %q 重复", id)
		}
		seen[id] = true
	}
}
