package imagestore

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func samplePNG(t *testing.T) []byte {
	t.Helper()
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
	return buf.Bytes()
}

func sampleJPEG(t *testing.T, quality int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x * 4), G: uint8(y * 4), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("encode sample jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestCompressImagePNGToJPEG(t *testing.T) {
	raw := samplePNG(t)
	dst, mime, ext, changed := CompressImage(raw, "image/png")
	if !changed {
		t.Fatalf("expected png to be compressed")
	}
	if mime != "image/jpeg" || ext != ".jpg" {
		t.Fatalf("mime=%q ext=%q", mime, ext)
	}
	if _, _, err := image.Decode(bytes.NewReader(dst)); err != nil {
		t.Fatalf("compressed output not decodable: %v", err)
	}
}

func TestCompressImageJPEGReencodeSmaller(t *testing.T) {
	raw := sampleJPEG(t, 100)
	dst, mime, ext, changed := CompressImage(raw, "image/jpeg")
	if !changed {
		t.Fatalf("expected high quality jpeg to be recompressed smaller")
	}
	if mime != "image/jpeg" || ext != ".jpg" {
		t.Fatalf("mime=%q ext=%q", mime, ext)
	}
	if len(dst) >= len(raw) {
		t.Fatalf("compressed size %d not smaller than original %d", len(dst), len(raw))
	}
}

func TestCompressImageJPEGKeepsSmallerOriginal(t *testing.T) {
	raw := sampleJPEG(t, 10)
	dst, _, _, changed := CompressImage(raw, "image/jpeg")
	if changed {
		t.Fatalf("expected low quality jpeg to be kept as-is, got recompressed size %d vs original %d", len(dst), len(raw))
	}
	if string(dst) != string(raw) {
		t.Fatalf("expected original bytes to be returned unchanged")
	}
}

func TestCompressImageWebPSkipped(t *testing.T) {
	raw := []byte("fake-webp-bytes")
	dst, mime, ext, changed := CompressImage(raw, "image/webp")
	if changed {
		t.Fatalf("expected webp to be skipped")
	}
	if mime != "image/webp" || ext != ".webp" {
		t.Fatalf("mime=%q ext=%q", mime, ext)
	}
	if string(dst) != string(raw) {
		t.Fatalf("expected original bytes returned")
	}
}

func TestCompressImageInvalidDataFallsBack(t *testing.T) {
	raw := []byte("not a real image")
	dst, mime, ext, changed := CompressImage(raw, "image/png")
	if changed {
		t.Fatalf("expected invalid image data to fall back to original")
	}
	if mime != "image/png" || ext != ".png" {
		t.Fatalf("mime=%q ext=%q", mime, ext)
	}
	if string(dst) != string(raw) {
		t.Fatalf("expected original bytes returned")
	}
}

func TestCompressImageDisabledByEnv(t *testing.T) {
	t.Setenv("LLMCOC_IMAGE_QUALITY", "0")
	raw := samplePNG(t)
	dst, mime, ext, changed := CompressImage(raw, "image/png")
	if changed {
		t.Fatalf("expected compression disabled via env")
	}
	if mime != "image/png" || ext != ".png" {
		t.Fatalf("mime=%q ext=%q", mime, ext)
	}
	if string(dst) != string(raw) {
		t.Fatalf("expected original bytes returned")
	}
}

func TestCompressImageCustomQualityFromEnv(t *testing.T) {
	t.Setenv("LLMCOC_IMAGE_QUALITY", "50")
	raw := samplePNG(t)
	dst, _, _, changed := CompressImage(raw, "image/png")
	if !changed {
		t.Fatalf("expected compression to run with custom quality")
	}
	if len(dst) == 0 {
		t.Fatalf("expected non-empty compressed output")
	}
}
