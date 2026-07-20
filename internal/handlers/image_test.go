package handlers

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
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
	if got := w.Header().Get("ETag"); got != `"`+ref.Hash+`"` {
		t.Fatalf("ETag = %q", got)
	}
	if w.Body.String() != "abc" {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestServeImageCompressed(t *testing.T) {
	restore := imagestore.SetDefaultDir(t.TempDir())
	t.Cleanup(restore)

	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x * 4), G: uint8(y * 4), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode sample png: %v", err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	ref, err := imagestore.DefaultStore().SaveDataURL(dataURL)
	if err != nil {
		t.Fatalf("save image: %v", err)
	}
	if ref.MIME != "image/jpeg" {
		t.Fatalf("ref.MIME = %q, want image/jpeg", ref.MIME)
	}

	r := gin.New()
	r.GET("/api/images/:hash", ServeImage)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/images/"+ref.Hash, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q", got)
	}
	if _, _, err := image.Decode(bytes.NewReader(w.Body.Bytes())); err != nil {
		t.Fatalf("body not decodable as image: %v", err)
	}
}
