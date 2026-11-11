package imagestore

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/llmcoc/server/internal/config"
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

func TestPortraitDirIsSiblingOfImageDir(t *testing.T) {
	t.Setenv("LLMCOC_IMAGE_DIR", "")
	t.Setenv("LLMCOC_PORTRAIT_DIR", "")
	prev := config.Global.Database.Path
	config.Global.Database.Path = filepath.Join("var", "app", "llmcoc.db")
	t.Cleanup(func() { config.Global.Database.Path = prev })

	if got, want := DefaultDir(), filepath.Join("var", "app", "images"); got != want {
		t.Fatalf("DefaultDir = %q, want %q", got, want)
	}
	if got, want := PortraitDir(), filepath.Join("var", "app", "portraits"); got != want {
		t.Fatalf("PortraitDir = %q, want %q", got, want)
	}
	nested, err := PortraitDirNested()
	if err != nil {
		t.Fatalf("PortraitDirNested: %v", err)
	}
	if nested {
		t.Fatal("default portrait dir must not be inside the image dir")
	}
}

func TestPortraitDirEnvAndOverride(t *testing.T) {
	t.Setenv("LLMCOC_PORTRAIT_DIR", "/tmp/custom-portraits")
	if got := PortraitDir(); got != "/tmp/custom-portraits" {
		t.Fatalf("PortraitDir = %q", got)
	}
	restore := SetPortraitDir("/tmp/override-portraits")
	if got := PortraitDir(); got != "/tmp/override-portraits" {
		t.Fatalf("PortraitDir with override = %q", got)
	}
	restore()
	if got := PortraitDir(); got != "/tmp/custom-portraits" {
		t.Fatalf("PortraitDir after restore = %q", got)
	}
}

func TestPortraitDirNestedDetectsOverlap(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LLMCOC_IMAGE_DIR", root)
	for name, portraits := range map[string]string{
		"same":   root,
		"inside": filepath.Join(root, "portraits"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(SetPortraitDir(portraits))
			nested, err := PortraitDirNested()
			if err != nil {
				t.Fatalf("PortraitDirNested: %v", err)
			}
			if !nested {
				t.Fatalf("portrait dir %q should be reported as nested in %q", portraits, root)
			}
		})
	}
	t.Run("sibling", func(t *testing.T) {
		t.Cleanup(SetPortraitDir(root + "-portraits"))
		nested, err := PortraitDirNested()
		if err != nil {
			t.Fatalf("PortraitDirNested: %v", err)
		}
		if nested {
			t.Fatal("sibling dir with the same prefix must not be reported as nested")
		}
	})
}

func TestPortraitURL(t *testing.T) {
	if got := PortraitURL("ABCDEF"); got != "/api/portraits/abcdef" {
		t.Fatalf("PortraitURL = %q", got)
	}
}

func TestRemoveDeletesFileAndIsIdempotent(t *testing.T) {
	store := New(t.TempDir())
	ref, err := store.SaveDataURL("data:image/png;base64,YWJj")
	if err != nil {
		t.Fatalf("SaveDataURL: %v", err)
	}
	if err := store.Remove(ref.Hash); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(ref.Path); !os.IsNotExist(err) {
		t.Fatalf("file still exists, stat err = %v", err)
	}
	if err := store.Remove(ref.Hash); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
	if err := store.Remove("not-a-hash"); !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("Remove invalid hash err = %v, want ErrInvalidHash", err)
	}
}

func TestImageCleanupLeavesPortraitDirAlone(t *testing.T) {
	base := t.TempDir()
	images := filepath.Join(base, "images")
	portraits := filepath.Join(base, "portraits")
	t.Setenv("LLMCOC_IMAGE_DIR", images)
	t.Cleanup(SetPortraitDir(portraits))

	portrait, err := PortraitStore().SaveDataURL("data:image/png;base64,cG9ydHJhaXQ=")
	if err != nil {
		t.Fatalf("save portrait: %v", err)
	}
	if _, err := DefaultStore().SaveDataURL("data:image/png;base64,c2NlbmU="); err != nil {
		t.Fatalf("save scene image: %v", err)
	}
	old := time.Now().Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(portrait.Path, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	report := DefaultStore().CleanupOlderThanReport(time.Now().Add(time.Hour))
	if report.Deleted != 1 || len(report.Errors) != 0 {
		t.Fatalf("cleanup report = %#v, want exactly the scene image deleted", report)
	}
	if _, err := os.Stat(portrait.Path); err != nil {
		t.Fatalf("portrait removed by image cleanup: %v", err)
	}
}
