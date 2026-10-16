// NOTE: 覆盖扩散模型(经 new-api 接入的 NovelAI)出图的请求形状：分辨率必须满足 64 的倍数且
// 总像素不超过 1024x1024，negative_prompt 作为顶层字段透传，且不能发送 NovelAI 不认的 quality。
// 全部基于内存假 HTTP 端点，禁止真实网络。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestImageSizeForDiffusion(t *testing.T) {
	tests := []struct {
		name   string
		aspect ImageAspect
		want   string
	}{
		{"横图", ImageAspectLandscape, "1216x832"},
		{"竖图", ImageAspectPortrait, "832x1216"},
		{"方图", ImageAspectSquare, "1024x1024"},
		{"空 aspect 回落方图", "", "1024x1024"},
		{"非法 aspect 回落方图", ImageAspect("garbage"), "1024x1024"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(imageSizeForDiffusion(tt.aspect))
			if got != tt.want {
				t.Errorf("imageSizeForDiffusion(%q) = %q, want %q", tt.aspect, got, tt.want)
			}
			var w, h int
			if _, err := fmt.Sscanf(got, "%dx%d", &w, &h); err != nil {
				t.Fatalf("parse size %q: %v", got, err)
			}
			if w%64 != 0 || h%64 != 0 || w*h > 1024*1024 {
				t.Errorf("size %q violates NovelAI limits (multiple of 64, <= 1024*1024 pixels)", got)
			}
		})
	}
}

// newFakeImagesServer 起一个假 /images/generations 端点，记录请求体并返回一张假图。
func newFakeImagesServer(t *testing.T) (*httptest.Server, *map[string]any) {
	t.Helper()
	captured := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("unmarshal request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"ZmFrZS1pbWFnZQ=="}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func TestGenerateImage_DiffusionRequestShape(t *testing.T) {
	srv, captured := newFakeImagesServer(t)
	p := newOpenAIProvider("test-key", srv.URL, "nai-diffusion-4-5-full", false)

	data, mime, err := p.generateImage(context.Background(), "1girl, solo, library", ImageOptions{
		Aspect:         ImageAspectPortrait,
		Diffusion:      true,
		NegativePrompt: "lowres, text",
	})
	if err != nil {
		t.Fatalf("generateImage error: %v", err)
	}
	if data != "ZmFrZS1pbWFnZQ==" || mime != "image/png" {
		t.Errorf("got data=%q mime=%q", data, mime)
	}

	body := *captured
	if body["model"] != "nai-diffusion-4-5-full" {
		t.Errorf("model = %v", body["model"])
	}
	if body["prompt"] != "1girl, solo, library" {
		t.Errorf("prompt = %v", body["prompt"])
	}
	if body["size"] != "832x1216" {
		t.Errorf("size = %v, want 832x1216", body["size"])
	}
	if body["negative_prompt"] != "lowres, text" {
		t.Errorf("negative_prompt = %v, want top-level passthrough", body["negative_prompt"])
	}
	if _, ok := body["quality"]; ok {
		t.Errorf("quality must not be sent in diffusion mode, got %v", body["quality"])
	}
	if body["response_format"] != "b64_json" {
		t.Errorf("response_format = %v", body["response_format"])
	}
}

func TestGenerateImage_DiffusionOmitsEmptyNegativePrompt(t *testing.T) {
	srv, captured := newFakeImagesServer(t)
	p := newOpenAIProvider("test-key", srv.URL, "nai-diffusion-4-5-full", false)

	if _, _, err := p.generateImage(context.Background(), "no humans, library", ImageOptions{Diffusion: true}); err != nil {
		t.Fatalf("generateImage error: %v", err)
	}
	if _, ok := (*captured)["negative_prompt"]; ok {
		t.Errorf("empty negative prompt must not be sent, got %v", (*captured)["negative_prompt"])
	}
}

func TestGenerateImage_NonDiffusionKeepsOpenAIParams(t *testing.T) {
	srv, captured := newFakeImagesServer(t)
	p := newOpenAIProvider("test-key", srv.URL, "gpt-image-1", false)

	if _, _, err := p.generateImage(context.Background(), "a cat", ImageOptions{Aspect: ImageAspectLandscape}); err != nil {
		t.Fatalf("generateImage error: %v", err)
	}
	body := *captured
	if body["size"] != "1536x1024" || body["quality"] != "high" {
		t.Errorf("size/quality = %v/%v, want 1536x1024/high", body["size"], body["quality"])
	}
	if _, ok := body["negative_prompt"]; ok {
		t.Errorf("negative_prompt must not leak into non-diffusion requests")
	}
}
