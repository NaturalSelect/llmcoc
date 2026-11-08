package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func newFrontendRouter(fsys fstest.MapFS) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.NoRoute(frontendHandler(fsys))
	return r
}

func builtFrontend() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<html>spa</html>")},
		"favicon.svg":          {Data: []byte("<svg/>")},
		"assets/app-abc123.js": {Data: []byte("console.log(1)")},
	}
}

func get(r *gin.Engine, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func TestFrontendHandler_IndexAndSPAFallback(t *testing.T) {
	r := newFrontendRouter(builtFrontend())

	for _, target := range []string{"/", "/index.html", "/sessions/7", "/admin/cache", "/login?redirect=%2Fshop"} {
		w := get(r, target)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", target, w.Code)
		}
		if w.Body.String() != "<html>spa</html>" {
			t.Fatalf("%s: body = %q, want index.html", target, w.Body.String())
		}
		if got := w.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s: Cache-Control = %q, want no-cache", target, got)
		}
	}
}

func TestFrontendHandler_DirectoryFallsBackToIndex(t *testing.T) {
	w := get(newFrontendRouter(builtFrontend()), "/assets")
	if w.Code != http.StatusOK || w.Body.String() != "<html>spa</html>" {
		t.Fatalf("directory request: status = %d body = %q, want index.html", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
}

func TestFrontendHandler_HashedAssetsAreImmutable(t *testing.T) {
	w := get(newFrontendRouter(builtFrontend()), "/assets/app-abc123.js")
	if w.Code != http.StatusOK || w.Body.String() != "console.log(1)" {
		t.Fatalf("status = %d body = %q, want the js file", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q, want immutable", got)
	}
}

func TestFrontendHandler_OtherStaticFilesShortCache(t *testing.T) {
	w := get(newFrontendRouter(builtFrontend()), "/favicon.svg")
	if w.Code != http.StatusOK || w.Body.String() != "<svg/>" {
		t.Fatalf("status = %d body = %q, want the svg file", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Fatalf("Cache-Control = %q, want max-age=3600", got)
	}
}

func TestFrontendHandler_PathTraversalCannotEscape(t *testing.T) {
	fsys := builtFrontend()
	fsys["secret.txt"] = &fstest.MapFile{Data: []byte("top secret")}
	// 前端目录之外的内容根本不在 fsys 里；这里确认带 .. 的路径被规整后不会读到意料之外的文件
	w := get(newFrontendRouter(fsys), "/assets/../../etc/passwd")
	if strings.Contains(w.Body.String(), "root:") {
		t.Fatalf("path traversal leaked file content: %q", w.Body.String())
	}
}

func TestFrontendHandler_NotBuilt(t *testing.T) {
	r := newFrontendRouter(fstest.MapFS{})
	for _, target := range []string{"/", "/sessions/7"} {
		w := get(r, target)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want 503", target, w.Code)
		}
		if !strings.Contains(w.Body.String(), "npm run build") {
			t.Fatalf("%s: body = %q, want build hint", target, w.Body.String())
		}
	}
}
