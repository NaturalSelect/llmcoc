package imagestore

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveDataURLAndResolve(t *testing.T) {
	store := New(t.TempDir())
	ref, err := store.SaveDataURL("data:image/png;base64,YWJj")
	if err != nil {
		t.Fatalf("SaveDataURL: %v", err)
	}
	if ref.Hash == "" || ref.MIME != "image/png" || ref.Extension != ".png" {
		t.Fatalf("ref = %#v", ref)
	}
	stored, err := store.Resolve(ref.Hash)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if stored.MIME != "image/png" {
		t.Fatalf("mime = %q", stored.MIME)
	}
	data, err := os.ReadFile(stored.Path)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if string(data) != "abc" {
		t.Fatalf("stored bytes = %q", data)
	}
}

func TestCleanupOlderThanUsesModTime(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	oldRef, err := store.SaveDataURL("data:image/png;base64,b2xk")
	if err != nil {
		t.Fatalf("save old: %v", err)
	}
	newRef, err := store.SaveDataURL("data:image/png;base64,bmV3")
	if err != nil {
		t.Fatalf("save new: %v", err)
	}
	oldPath := filepath.Join(dir, oldRef.Hash[:2], oldRef.Hash+oldRef.Extension)
	oldTime := time.Now().Add(-15 * 24 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	deleted, err := store.CleanupOlderThan(time.Now().Add(-14 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("CleanupOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := store.Resolve(oldRef.Hash); err == nil {
		t.Fatalf("old image should be deleted")
	}
	if _, err := store.Resolve(newRef.Hash); err != nil {
		t.Fatalf("new image should remain: %v", err)
	}
}

func TestStartCleanupReturnsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartCleanup(ctx, New(t.TempDir()), 14*24*time.Hour, time.Hour)
}

func TestSaveDataURLCompression(t *testing.T) {
	store := New(t.TempDir())

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

	ref, err := store.SaveDataURL(dataURL)
	if err != nil {
		t.Fatalf("SaveDataURL: %v", err)
	}
	if ref.MIME != "image/jpeg" || ref.Extension != ".jpg" {
		t.Fatalf("ref = %#v, want compressed jpeg", ref)
	}

	stored, err := store.Resolve(ref.Hash)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if stored.MIME != "image/jpeg" {
		t.Fatalf("stored mime = %q", stored.MIME)
	}
	data, err := os.ReadFile(stored.Path)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("stored file not decodable as image: %v", err)
	}
}
