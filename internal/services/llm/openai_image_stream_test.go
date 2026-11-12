// NOTE: 覆盖 /images/generations 的流式出图：请求带 stream=true、只取 completed 事件、
// 网关忽略 stream 直接返回 JSON 时照常解析、dall-e 仍走一次性请求。全部基于内存假 HTTP 端点，禁止真实网络。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newFakeImageStreamServer 起一个假 /images/generations 端点：events 非空时按 SSE 依次下发，
// 否则返回 jsonBody。返回最后一次请求体。
func newFakeImageStreamServer(t *testing.T, events []string, jsonBody string) (*httptest.Server, *map[string]any) {
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
		if len(events) == 0 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(jsonBody))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, ev := range events {
			fmt.Fprintf(w, "data: %s\n\n", ev)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

const (
	partialImageEvent   = `{"type":"image_generation.partial_image","b64_json":"cGFydGlhbA==","partial_image_index":0}`
	completedImageEvent = `{"type":"image_generation.completed","b64_json":"ZmluYWw=","output_format":"png"}`
)

func TestGenerateImage_StreamUsesCompletedEvent(t *testing.T) {
	srv, captured := newFakeImageStreamServer(t, []string{partialImageEvent, completedImageEvent}, "")
	p := newOpenAIProvider("test-key", srv.URL, "gpt-image-1", false)

	data, mime, err := p.generateImage(context.Background(), "a cat", ImageOptions{Aspect: ImageAspectLandscape})
	if err != nil {
		t.Fatalf("generateImage error: %v", err)
	}
	if data != "ZmluYWw=" || mime != "image/png" {
		t.Errorf("got data=%q mime=%q, want final image", data, mime)
	}

	body := *captured
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	if _, ok := body["partial_images"]; ok {
		t.Errorf("partial_images must not be requested, got %v", body["partial_images"])
	}
	if body["size"] != "1536x1024" || body["quality"] != "high" {
		t.Errorf("size/quality = %v/%v, want 1536x1024/high", body["size"], body["quality"])
	}
}

func TestGenerateImage_StreamKeepsOutputMime(t *testing.T) {
	events := []string{`{"type":"image_generation.completed","b64_json":"ZmluYWw=","output_format":"jpeg"}`}
	srv, _ := newFakeImageStreamServer(t, events, "")
	p := newOpenAIProvider("test-key", srv.URL, "gpt-image-1", false)

	_, mime, err := p.generateImage(context.Background(), "a cat", ImageOptions{})
	if err != nil {
		t.Fatalf("generateImage error: %v", err)
	}
	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", mime)
	}
}

func TestGenerateImage_StreamFallsBackToJSONResponse(t *testing.T) {
	srv, captured := newFakeImageStreamServer(t, nil, `{"data":[{"b64_json":"ZmFrZS1pbWFnZQ=="}]}`)
	p := newOpenAIProvider("test-key", srv.URL, "nai-diffusion-4-5-full", false)

	data, mime, err := p.generateImage(context.Background(), "1girl, solo", ImageOptions{Diffusion: true, NegativePrompt: "lowres"})
	if err != nil {
		t.Fatalf("generateImage error: %v", err)
	}
	if data != "ZmFrZS1pbWFnZQ==" || mime != "image/png" {
		t.Errorf("got data=%q mime=%q", data, mime)
	}
	if (*captured)["stream"] != true {
		t.Errorf("stream = %v, want true", (*captured)["stream"])
	}
	if (*captured)["negative_prompt"] != "lowres" {
		t.Errorf("negative_prompt = %v, want passthrough", (*captured)["negative_prompt"])
	}
}

func TestGenerateImage_DallEStaysNonStreaming(t *testing.T) {
	srv, captured := newFakeImagesServer(t)
	p := newOpenAIProvider("test-key", srv.URL, "dall-e-3", false)

	if _, _, err := p.generateImage(context.Background(), "a cat", ImageOptions{}); err != nil {
		t.Fatalf("generateImage error: %v", err)
	}
	if _, ok := (*captured)["stream"]; ok {
		t.Errorf("dall-e must not send stream, got %v", (*captured)["stream"])
	}
}

func TestGenerateImage_StreamWithoutCompletedEvent(t *testing.T) {
	srv, _ := newFakeImageStreamServer(t, []string{partialImageEvent}, "")
	p := newOpenAIProvider("test-key", srv.URL, "gpt-image-1", false)

	_, _, err := p.generateImage(context.Background(), "a cat", ImageOptions{})
	if err == nil || !strings.Contains(err.Error(), "without a completed image") {
		t.Fatalf("err = %v, want ended-without-completed error", err)
	}
}

func TestGenerateImage_StreamErrorEvent(t *testing.T) {
	events := []string{`{"error":{"message":"content policy violation"}}`}
	srv, _ := newFakeImageStreamServer(t, events, "")
	p := newOpenAIProvider("test-key", srv.URL, "gpt-image-1", false)

	_, _, err := p.generateImage(context.Background(), "a cat", ImageOptions{})
	if err == nil || !strings.Contains(err.Error(), "content policy violation") {
		t.Fatalf("err = %v, want stream error carrying upstream message", err)
	}
}

func TestGenerateImage_StreamRejectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad size"}}`))
	}))
	t.Cleanup(srv.Close)
	p := newOpenAIProvider("test-key", srv.URL, "gpt-image-1", false)

	_, _, err := p.generateImage(context.Background(), "a cat", ImageOptions{})
	if err == nil || !strings.Contains(err.Error(), "bad size") {
		t.Fatalf("err = %v, want HTTP error carrying upstream message", err)
	}
}
