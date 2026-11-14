package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/onlinemod"
)

type fakeOnlineSource struct {
	list     func(q onlinemod.ListQuery) (onlinemod.ListPage, error)
	resolve  func(shareURL string) (string, string, error)
	download func(direct string) ([]byte, error)
}

func (f *fakeOnlineSource) List(_ context.Context, q onlinemod.ListQuery) (onlinemod.ListPage, error) {
	if f.list == nil {
		return onlinemod.ListPage{}, errors.New("list not stubbed")
	}
	return f.list(q)
}

func (f *fakeOnlineSource) ResolveLanzou(_ context.Context, shareURL string) (string, string, error) {
	if f.resolve == nil {
		return "", "", errors.New("resolve not stubbed")
	}
	return f.resolve(shareURL)
}

func (f *fakeOnlineSource) Download(_ context.Context, direct string, _ int64) ([]byte, error) {
	if f.download == nil {
		return nil, errors.New("download not stubbed")
	}
	return f.download(direct)
}

const onlineTestAdminID = 7

func initOnlineTest(t *testing.T, src *fakeOnlineSource) *gin.Engine {
	t.Helper()
	initTestDB(t)
	if err := models.DB.AutoMigrate(&models.OnlineModule{}, &models.OnlineModuleDocument{}); err != nil {
		t.Fatalf("auto-migrate online module: %v", err)
	}

	prevClient, prevValid := onlineClient, onlineValidShareURL
	onlineClient = src
	onlineValidShareURL = func(raw string) bool { return strings.HasPrefix(raw, "https://share.test/") }
	t.Cleanup(func() { onlineClient, onlineValidShareURL = prevClient, prevValid })

	r := gin.New()
	g := r.Group("/admin", withAuth(onlineTestAdminID, "root", "admin"))
	g.GET("/online-modules/browse", OnlineModuleBrowse)
	g.GET("/online-modules", OnlineModuleList)
	g.POST("/online-modules/download", OnlineModuleDownload)
	g.GET("/online-modules/:id", OnlineModuleGet)
	g.GET("/online-modules/:id/documents/:docId", OnlineModuleDocument)
	g.POST("/online-modules/:id/archive", OnlineModuleUploadArchive)
	g.POST("/online-modules/:id/license-override", OnlineModuleLicenseOverride)
	g.POST("/online-modules/:id/compile", OnlineModuleCompile)
	g.DELETE("/online-modules/:id", OnlineModuleDelete)
	return r
}

// onlineStory 返回一段超过候选阈值的中文正文，extra 附在末尾。
func onlineStory(extra string) string {
	return strings.Repeat("雨夜里庄园的钟声敲了十二下。", 400) + extra
}

func onlineZip(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func serveOnline(r *gin.Engine, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func onlineUpload(t *testing.T, path, filename string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func downloadBody(keyID any, shareURL string) map[string]any {
	return map[string]any{
		"keyId":     keyID,
		"title":     "雨夜庄园",
		"article":   "某作者",
		"url":       shareURL,
		"minAmount": "2",
		"maxAmount": 4,
	}
}

func seedOnlineModule(t *testing.T, status string, mutate func(*models.OnlineModule)) models.OnlineModule {
	t.Helper()
	m := models.OnlineModule{
		Source:    models.OnlineModuleSourceCnmods,
		SourceKey: fmt.Sprintf("k%d", time.Now().UnixNano()),
		Title:     "雨夜庄园",
		Author:    "某作者",
		ShareURL:  "https://share.test/abc",
		Status:    status,
	}
	if mutate != nil {
		mutate(&m)
	}
	if err := models.DB.Create(&m).Error; err != nil {
		t.Fatalf("seed online module: %v", err)
	}
	return m
}

func seedOnlineDoc(t *testing.T, moduleID uint, text string, hits []string) models.OnlineModuleDocument {
	t.Helper()
	if hits == nil {
		hits = []string{}
	}
	d := models.OnlineModuleDocument{
		OnlineModuleID: moduleID,
		Path:           "module.txt",
		Kind:           "txt",
		Runes:          len([]rune(text)),
		Candidate:      true,
		Text:           text,
		LicenseHits:    models.JSONField[[]string]{Data: hits},
	}
	if err := models.DB.Create(&d).Error; err != nil {
		t.Fatalf("seed online doc: %v", err)
	}
	return d
}

func loadOnlineModule(t *testing.T, id uint) models.OnlineModule {
	t.Helper()
	var m models.OnlineModule
	if err := models.DB.First(&m, id).Error; err != nil {
		t.Fatalf("load online module: %v", err)
	}
	return m
}

func TestOnlineModuleDownload_Success(t *testing.T) {
	var gotShare, gotDirect string
	src := &fakeOnlineSource{
		resolve: func(shareURL string) (string, string, error) {
			gotShare = shareURL
			return "https://cdn.test/file/x", "module.zip", nil
		},
		download: func(direct string) ([]byte, error) {
			gotDirect = direct
			return onlineZip(t, "module.txt", onlineStory("")), nil
		},
	}
	r := initOnlineTest(t, src)

	w := serveOnline(r, jsonReq("POST", "/admin/online-modules/download", downloadBody(5051, "https://share.test/abc")))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var m models.OnlineModule
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.Status != models.OnlineModuleStatusExtracted || m.Error != "" {
		t.Fatalf("status = %q, error = %q", m.Status, m.Error)
	}
	if m.SourceKey != "5051" || m.Author != "某作者" || m.ArchiveName != "module.zip" || len(m.ArchiveSHA256) != 64 {
		t.Fatalf("unexpected module: %+v", m)
	}
	if m.Meta.Data.MinAmount != 2 || m.Meta.Data.MaxAmount != 4 {
		t.Fatalf("meta = %+v", m.Meta.Data)
	}
	if gotShare != "https://share.test/abc" || gotDirect != "https://cdn.test/file/x" {
		t.Fatalf("share = %q, direct = %q", gotShare, gotDirect)
	}

	var docs []models.OnlineModuleDocument
	models.DB.Where("online_module_id = ?", m.ID).Find(&docs)
	if len(docs) != 1 || !docs[0].Candidate || docs[0].Kind != "txt" || len(docs[0].LicenseHits.Data) != 0 {
		t.Fatalf("unexpected documents: %+v", docs)
	}
}

func TestOnlineModuleDownload_RedownloadClearsOverride(t *testing.T) {
	src := &fakeOnlineSource{
		resolve:  func(string) (string, string, error) { return "https://cdn.test/f", "module.zip", nil },
		download: func(string) ([]byte, error) { return onlineZip(t, "module.txt", onlineStory("")), nil },
	}
	r := initOnlineTest(t, src)
	now := time.Now()
	uid := uint(onlineTestAdminID)
	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, func(m *models.OnlineModule) {
		m.SourceKey = "5051"
		m.LicenseOverrideBy = &uid
		m.LicenseOverrideAt = &now
	})
	seedOnlineDoc(t, m.ID, "旧正文", []string{"禁止投喂AI"})

	w := serveOnline(r, jsonReq("POST", "/admin/online-modules/download", downloadBody("5051", "https://share.test/abc")))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	got := loadOnlineModule(t, m.ID)
	if got.LicenseOverrideAt != nil || got.LicenseOverrideBy != nil {
		t.Fatalf("override should be cleared, got %+v", got)
	}
	var count int64
	models.DB.Model(&models.OnlineModuleDocument{}).Where("online_module_id = ?", m.ID).Count(&count)
	if count != 1 {
		t.Fatalf("documents = %d, want 1 (old ones replaced)", count)
	}
}

func TestOnlineModuleDownload_ChallengeMarksFailed(t *testing.T) {
	src := &fakeOnlineSource{
		resolve: func(string) (string, string, error) {
			return "", "", fmt.Errorf("share page: %w", onlinemod.ErrChallenge)
		},
	}
	r := initOnlineTest(t, src)

	w := serveOnline(r, jsonReq("POST", "/admin/online-modules/download", downloadBody(5051, "https://share.test/abc")))
	if w.Code != http.StatusOK {
		t.Fatalf("failure should still be 200, got %d: %s", w.Code, w.Body.String())
	}
	var m models.OnlineModule
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if m.Status != models.OnlineModuleStatusFailed || !strings.Contains(m.Error, "手动下载") {
		t.Fatalf("status = %q, error = %q", m.Status, m.Error)
	}
}

func TestOnlineModuleDownload_UnsupportedArchiveMarksFailed(t *testing.T) {
	src := &fakeOnlineSource{
		resolve:  func(string) (string, string, error) { return "https://cdn.test/f", "module.rar", nil },
		download: func(string) ([]byte, error) { return []byte("Rar!\x1a\x07\x00rest"), nil },
	}
	r := initOnlineTest(t, src)

	w := serveOnline(r, jsonReq("POST", "/admin/online-modules/download", downloadBody(5051, "https://share.test/abc")))
	var m models.OnlineModule
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if m.Status != models.OnlineModuleStatusFailed || !strings.Contains(m.Error, "rar") {
		t.Fatalf("status = %q, error = %q", m.Status, m.Error)
	}
}

func TestOnlineModuleDownload_RejectsBadInput(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	cases := map[string]map[string]any{
		"url not allowed": downloadBody(1, "https://evil.example.com/x"),
		"missing key":     {"title": "雨夜庄园", "url": "https://share.test/abc"},
		"missing title":   {"keyId": 1, "url": "https://share.test/abc"},
	}
	for name, body := range cases {
		w := serveOnline(r, jsonReq("POST", "/admin/online-modules/download", body))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", name, w.Code, w.Body.String())
		}
	}
	var count int64
	models.DB.Model(&models.OnlineModule{}).Count(&count)
	if count != 0 {
		t.Fatalf("rejected requests must not create records, got %d", count)
	}
}

func TestOnlineModuleDownload_ConcurrentReturns409(t *testing.T) {
	src := &fakeOnlineSource{}
	r := initOnlineTest(t, src)
	seedOnlineModule(t, models.OnlineModuleStatusDownloading, func(m *models.OnlineModule) { m.SourceKey = "5051" })

	w := serveOnline(r, jsonReq("POST", "/admin/online-modules/download", downloadBody(5051, "https://share.test/abc")))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", w.Code, w.Body.String())
	}
}

func TestOnlineModuleDownload_StaleDownloadingCanRetry(t *testing.T) {
	src := &fakeOnlineSource{
		resolve:  func(string) (string, string, error) { return "https://cdn.test/f", "module.zip", nil },
		download: func(string) ([]byte, error) { return onlineZip(t, "module.txt", onlineStory("")), nil },
	}
	r := initOnlineTest(t, src)
	m := seedOnlineModule(t, models.OnlineModuleStatusDownloading, func(m *models.OnlineModule) { m.SourceKey = "5051" })
	old := time.Now().Add(-time.Hour)
	models.DB.Model(&models.OnlineModule{}).Where("id = ?", m.ID).UpdateColumn("updated_at", old)

	w := serveOnline(r, jsonReq("POST", "/admin/online-modules/download", downloadBody(5051, "https://share.test/abc")))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if got := loadOnlineModule(t, m.ID); got.Status != models.OnlineModuleStatusExtracted {
		t.Fatalf("status = %q, want extracted", got.Status)
	}
}

func TestOnlineModuleUploadArchive(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusFailed, func(m *models.OnlineModule) { m.Error = "被拦截" })

	data := onlineZip(t, "module.txt", onlineStory("本模组禁止投喂给AI使用。"))
	w := serveOnline(r, onlineUpload(t, fmt.Sprintf("/admin/online-modules/%d/archive", m.ID), "manual.zip", data))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var detail struct {
		Module    models.OnlineModule `json:"module"`
		Documents []struct {
			ID          uint     `json:"id"`
			Runes       int      `json:"runes"`
			Candidate   bool     `json:"candidate"`
			LicenseHits []string `json:"license_hits"`
			Preview     string   `json:"preview"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.Module.Status != models.OnlineModuleStatusExtracted || detail.Module.Error != "" || detail.Module.ArchiveName != "manual.zip" {
		t.Fatalf("unexpected module: %+v", detail.Module)
	}
	if len(detail.Documents) != 1 || !detail.Documents[0].Candidate {
		t.Fatalf("unexpected documents: %+v", detail.Documents)
	}
	if len(detail.Documents[0].LicenseHits) == 0 {
		t.Fatalf("expected license hits for the 投喂 AI sentence")
	}
	if n := len([]rune(detail.Documents[0].Preview)); n == 0 || n > onlinePreviewRunes {
		t.Fatalf("preview runes = %d, want 1..%d", n, onlinePreviewRunes)
	}
}

func TestOnlineModuleUploadArchive_BadFileKeepsPreviousState(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, nil)
	seedOnlineDoc(t, m.ID, "已有正文", nil)

	w := serveOnline(r, onlineUpload(t, fmt.Sprintf("/admin/online-modules/%d/archive", m.ID), "bad.rar", []byte("Rar!\x1a\x07\x00rest")))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", w.Code, w.Body.String())
	}
	if got := loadOnlineModule(t, m.ID); got.Status != models.OnlineModuleStatusExtracted {
		t.Fatalf("status = %q, want extracted (restored)", got.Status)
	}
	var count int64
	models.DB.Model(&models.OnlineModuleDocument{}).Where("online_module_id = ?", m.ID).Count(&count)
	if count != 1 {
		t.Fatalf("existing documents must survive a failed upload, got %d", count)
	}
}

func TestOnlineModuleUploadArchive_NoFile(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusFailed, nil)

	w := serveOnline(r, jsonReq("POST", fmt.Sprintf("/admin/online-modules/%d/archive", m.ID), nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if got := loadOnlineModule(t, m.ID); got.Status != models.OnlineModuleStatusFailed {
		t.Fatalf("status = %q, want failed unchanged", got.Status)
	}
}

func TestOnlineModuleLicenseOverride(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})

	clean := seedOnlineModule(t, models.OnlineModuleStatusExtracted, nil)
	seedOnlineDoc(t, clean.ID, onlineStory(""), nil)
	w := serveOnline(r, jsonReq("POST", fmt.Sprintf("/admin/online-modules/%d/license-override", clean.ID), nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("no hits: status = %d, want 400", w.Code)
	}

	pending := seedOnlineModule(t, models.OnlineModuleStatusFailed, nil)
	w = serveOnline(r, jsonReq("POST", fmt.Sprintf("/admin/online-modules/%d/license-override", pending.ID), nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("not extracted: status = %d, want 409", w.Code)
	}

	hit := seedOnlineModule(t, models.OnlineModuleStatusExtracted, nil)
	seedOnlineDoc(t, hit.ID, onlineStory(""), []string{"禁止投喂AI"})
	w = serveOnline(r, jsonReq("POST", fmt.Sprintf("/admin/online-modules/%d/license-override", hit.ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("with hits: status = %d, body = %s", w.Code, w.Body.String())
	}
	got := loadOnlineModule(t, hit.ID)
	if got.LicenseOverrideAt == nil || got.LicenseOverrideBy == nil || *got.LicenseOverrideBy != onlineTestAdminID {
		t.Fatalf("override not recorded: %+v", got)
	}
}

func TestOnlineModuleCompile_Validation(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	compile := func(id uint, body map[string]any) *httptest.ResponseRecorder {
		return serveOnline(r, jsonReq("POST", fmt.Sprintf("/admin/online-modules/%d/compile", id), body))
	}

	failed := seedOnlineModule(t, models.OnlineModuleStatusFailed, nil)
	if w := compile(failed.ID, map[string]any{"document_id": 1}); w.Code != http.StatusConflict {
		t.Errorf("not extracted: status = %d, want 409", w.Code)
	}

	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, nil)
	if w := compile(m.ID, map[string]any{}); w.Code != http.StatusBadRequest {
		t.Errorf("missing document_id: status = %d, want 400", w.Code)
	}
	if w := compile(m.ID, map[string]any{"document_id": 9999}); w.Code != http.StatusNotFound {
		t.Errorf("unknown document: status = %d, want 404", w.Code)
	}

	other := seedOnlineModule(t, models.OnlineModuleStatusExtracted, nil)
	foreign := seedOnlineDoc(t, other.ID, onlineStory(""), nil)
	if w := compile(m.ID, map[string]any{"document_id": foreign.ID}); w.Code != http.StatusNotFound {
		t.Errorf("document of another module: status = %d, want 404", w.Code)
	}

	seedScenario(t, "已存在的模组")
	ok := seedOnlineDoc(t, m.ID, onlineStory(""), nil)
	if w := compile(m.ID, map[string]any{"document_id": ok.ID, "name": "已存在的模组"}); w.Code != http.StatusConflict {
		t.Errorf("duplicate name: status = %d, want 409 (%s)", w.Code, w.Body.String())
	}
}

func TestOnlineModuleCompile_LicenseGateThenOverride(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, nil)
	// 正文超过编译上限：放行后应该越过版权闸门、落在体积校验上，而不是进入 SSE。
	huge := seedOnlineDoc(t, m.ID, strings.Repeat("a", onlineCompileMaxBytes+1), []string{"禁止投喂AI"})
	path := fmt.Sprintf("/admin/online-modules/%d/compile", m.ID)

	w := serveOnline(r, jsonReq("POST", path, map[string]any{"document_id": huge.ID}))
	if w.Code != http.StatusConflict {
		t.Fatalf("before override: status = %d, want 409 (%s)", w.Code, w.Body.String())
	}

	w = serveOnline(r, jsonReq("POST", fmt.Sprintf("/admin/online-modules/%d/license-override", m.ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("override: status = %d (%s)", w.Code, w.Body.String())
	}

	w = serveOnline(r, jsonReq("POST", path, map[string]any{"document_id": huge.ID}))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("after override: status = %d, want 413 (%s)", w.Code, w.Body.String())
	}
}

func TestOnlineModuleBrowse_AttachesLocalStatus(t *testing.T) {
	var gotQuery onlinemod.ListQuery
	src := &fakeOnlineSource{
		list: func(q onlinemod.ListQuery) (onlinemod.ListPage, error) {
			gotQuery = q
			return onlinemod.ListPage{
				Items: []onlinemod.CnmodsItem{
					{KeyID: "5051", Title: "已下载", URL: "https://share.test/a"},
					{KeyID: "5052", Title: "未下载", URL: "https://share.test/b"},
				},
				Page: 2, TotalPages: 473, TotalElements: 4730,
			}, nil
		},
	}
	r := initOnlineTest(t, src)
	seedOnlineModule(t, models.OnlineModuleStatusExtracted, func(m *models.OnlineModule) { m.SourceKey = "5051" })

	w := serveOnline(r, jsonReq("GET", "/admin/online-modules/browse?page=2&title=%E9%9B%A8%E5%A4%9C", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotQuery.Page != 2 || gotQuery.Title != "雨夜" {
		t.Fatalf("query = %+v", gotQuery)
	}
	var resp struct {
		Items []struct {
			KeyID string `json:"keyId"`
			Local *struct {
				ID     uint   `json:"id"`
				Status string `json:"status"`
			} `json:"local"`
		} `json:"items"`
		Page          int `json:"page"`
		TotalPages    int `json:"total_pages"`
		TotalElements int `json:"total_elements"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 || resp.Page != 2 || resp.TotalPages != 473 || resp.TotalElements != 4730 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Items[0].Local == nil || resp.Items[0].Local.Status != "extracted" {
		t.Fatalf("first item should carry local status: %+v", resp.Items[0])
	}
	if resp.Items[1].Local != nil {
		t.Fatalf("second item should have no local record: %+v", resp.Items[1])
	}
}

func TestOnlineModuleBrowse_UpstreamFailureIs502(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{list: func(onlinemod.ListQuery) (onlinemod.ListPage, error) {
		return onlinemod.ListPage{}, errors.New("boom")
	}})
	w := serveOnline(r, jsonReq("GET", "/admin/online-modules/browse", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

func TestOnlineModuleListGetDocumentDelete(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, nil)
	text := onlineStory("")
	doc := seedOnlineDoc(t, m.ID, text, nil)

	w := serveOnline(r, jsonReq("GET", "/admin/online-modules?page=1&page_size=10", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"total":1`) {
		t.Fatalf("list: status = %d, body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "庄园的钟声") {
		t.Fatalf("list must not include document text")
	}

	w = serveOnline(r, jsonReq("GET", fmt.Sprintf("/admin/online-modules/%d", m.ID), nil))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), strings.Repeat("雨夜里庄园的钟声敲了十二下。", 30)) {
		t.Fatalf("detail should only carry a preview: status = %d", w.Code)
	}

	w = serveOnline(r, jsonReq("GET", fmt.Sprintf("/admin/online-modules/%d/documents/%d", m.ID, doc.ID), nil))
	var full struct {
		Text        string   `json:"text"`
		LicenseHits []string `json:"license_hits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &full); err != nil || full.Text != text || full.LicenseHits == nil {
		t.Fatalf("full document mismatch: err = %v, hits = %v, textLen = %d", err, full.LicenseHits, len(full.Text))
	}

	w = serveOnline(r, jsonReq("GET", "/admin/online-modules/9999", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing module: status = %d, want 404", w.Code)
	}

	w = serveOnline(r, jsonReq("DELETE", fmt.Sprintf("/admin/online-modules/%d", m.ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("delete: status = %d (%s)", w.Code, w.Body.String())
	}
	var left int64
	models.DB.Model(&models.OnlineModule{}).Count(&left)
	var leftDocs int64
	models.DB.Model(&models.OnlineModuleDocument{}).Count(&leftDocs)
	if left != 0 || leftDocs != 0 {
		t.Fatalf("delete left %d modules and %d documents", left, leftDocs)
	}
}

func TestOnlineModuleDelete_RefusesActiveDownload(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusDownloading, nil)
	w := serveOnline(r, jsonReq("DELETE", fmt.Sprintf("/admin/online-modules/%d", m.ID), nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

type fakeOnlineOCR struct {
	calls []string
	text  string
	err   error
}

func (f *fakeOnlineOCR) ParsePDF(_ context.Context, name string, _ []byte) (string, error) {
	f.calls = append(f.calls, name)
	return f.text, f.err
}

func useOnlineOCR(t *testing.T, ocr onlinemod.OCR) {
	t.Helper()
	prev := onlineOCR
	onlineOCR = ocr
	t.Cleanup(func() { onlineOCR = prev })
}

// emptyTextPDF 是没有任何文字的合法 PDF：本地抽不出内容，相当于扫描件。
// NOTE: 头部后面垫一段 PDF 注释，让文件体积超过候选正文的 2000 字节门槛，真实的扫描件都比这大得多。
func emptyTextPDF() []byte {
	var b bytes.Buffer
	var offs []int
	b.WriteString("%PDF-1.4\n")
	b.WriteString("%" + strings.Repeat("x", 2500) + "\n")
	add := func(body string) {
		offs = append(offs, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	add("<< /Type /Catalog /Pages 2 0 R >>")
	add("<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	add("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>")
	add("<< /Length 0 >>\nstream\n\nendstream")
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return b.Bytes()
}

func TestOnlineModuleUploadArchive_OCRFillsScannedPDF(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	ocr := &fakeOnlineOCR{text: onlineStory("")}
	useOnlineOCR(t, ocr)
	m := seedOnlineModule(t, models.OnlineModuleStatusFailed, nil)

	w := serveOnline(r, onlineUpload(t, fmt.Sprintf("/admin/online-modules/%d/archive", m.ID), "scan.pdf", emptyTextPDF()))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var detail struct {
		Documents []struct {
			Candidate bool `json:"candidate"`
			OCR       bool `json:"ocr"`
			Garbled   bool `json:"garbled"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(detail.Documents) != 1 || !detail.Documents[0].OCR || detail.Documents[0].Garbled || !detail.Documents[0].Candidate {
		t.Fatalf("unexpected documents: %+v", detail.Documents)
	}
	if len(ocr.calls) != 1 || ocr.calls[0] != "scan.pdf" {
		t.Fatalf("OCR calls = %v", ocr.calls)
	}
}

func TestOnlineModuleUploadArchive_NoOCRConfiguredSendsNothing(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	useOnlineOCR(t, nil)
	m := seedOnlineModule(t, models.OnlineModuleStatusFailed, nil)

	w := serveOnline(r, onlineUpload(t, fmt.Sprintf("/admin/online-modules/%d/archive", m.ID), "scan.pdf", emptyTextPDF()))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", w.Code, w.Body.String())
	}
}

func TestOnlineModuleUploadArchive_OCRFailureFailsWholeUpload(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	ocr := &fakeOnlineOCR{err: errors.New("boom")}
	useOnlineOCR(t, ocr)
	m := seedOnlineModule(t, models.OnlineModuleStatusFailed, func(m *models.OnlineModule) { m.Error = "旧错误" })

	w := serveOnline(r, onlineUpload(t, fmt.Sprintf("/admin/online-modules/%d/archive", m.ID), "scan.pdf", emptyTextPDF()))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", w.Code, w.Body.String())
	}
	if got := loadOnlineModule(t, m.ID); got.Status != models.OnlineModuleStatusFailed || got.Error != "旧错误" {
		t.Fatalf("状态应保持原样: %+v", got)
	}
}

func TestOnlineModuleCompile_RefusesGarbledDocument(t *testing.T) {
	r := initOnlineTest(t, &fakeOnlineSource{})
	m := seedOnlineModule(t, models.OnlineModuleStatusExtracted, nil)
	doc := seedOnlineDoc(t, m.ID, onlineStory(""), nil)
	if err := models.DB.Model(&doc).Update("garbled", true).Error; err != nil {
		t.Fatal(err)
	}

	w := serveOnline(r, jsonReq("POST", fmt.Sprintf("/admin/online-modules/%d/compile", m.ID), map[string]any{"document_id": doc.ID}))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "乱码") {
		t.Fatalf("status = %d, body = %s, want 409 mentioning 乱码", w.Code, w.Body.String())
	}
}
