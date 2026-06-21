package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/services/imagestore"
)

func TestServeImage(t *testing.T) {
	restore := imagestore.SetDefaultDir(t.TempDir())
	t.Cleanup(restore)
	ref, err := imagestore.DefaultStore().SaveDataURL("data:image/png;base64,YWJj")
	if err != nil {
		t.Fatalf("save image: %v", err)
	}
	r := gin.New()
	r.GET("/api/images/:hash", ServeImage)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/images/"+ref.Hash, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if w.Body.String() != "abc" {
		t.Fatalf("body = %q", w.Body.String())
	}
}
