package llm

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var _ Embedder = (*fantasyOpenAIProvider)(nil)

// embeddingServer 起一个假的 /embeddings：记录请求体，按 respond 返回响应 JSON。
func embeddingServer(t *testing.T, respond func(req map[string]any) (int, string)) (*httptest.Server, *map[string]any) {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/embeddings") {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		status, body := respond(got)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestOpenAIEmbedReordersByIndexAndNormalizes(t *testing.T) {
	srv, got := embeddingServer(t, func(map[string]any) (int, string) {
		// NOTE: 故意把 index 1 排在前面，验证按 Index 归位而不是按返回顺序。
		return 200, `{"object":"list","model":"m","usage":{"prompt_tokens":1,"total_tokens":1},"data":[
			{"object":"embedding","index":1,"embedding":[0,2]},
			{"object":"embedding","index":0,"embedding":[3,4]}]}`
	})
	p := newOpenAIProvider("test-key", srv.URL, "text-embedding-3-small", false)

	vecs, err := p.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if (*got)["model"] != "text-embedding-3-small" {
		t.Errorf("model = %v, want text-embedding-3-small", (*got)["model"])
	}
	input, _ := (*got)["input"].([]any)
	if len(input) != 2 || input[0] != "first" || input[1] != "second" {
		t.Errorf("input = %v, want [first second]", (*got)["input"])
	}
	if len(vecs) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vecs))
	}
	if math.Abs(float64(vecs[0][0])-0.6) > 1e-6 || math.Abs(float64(vecs[0][1])-0.8) > 1e-6 {
		t.Errorf("vector 0 = %v, want normalized [0.6 0.8]", vecs[0])
	}
	if vecs[1][0] != 0 || math.Abs(float64(vecs[1][1])-1) > 1e-6 {
		t.Errorf("vector 1 = %v, want normalized [0 1]", vecs[1])
	}
}

func TestOpenAIEmbedRejectsBadResponses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"http error", 500, `{"error":{"message":"boom"}}`},
		{"no data", 200, `{"object":"list","model":"m","usage":{"prompt_tokens":1,"total_tokens":1},"data":[]}`},
		{"empty vector", 200, `{"object":"list","model":"m","usage":{"prompt_tokens":1,"total_tokens":1},"data":[{"object":"embedding","index":0,"embedding":[]}]}`},
		{"zero vector", 200, `{"object":"list","model":"m","usage":{"prompt_tokens":1,"total_tokens":1},"data":[{"object":"embedding","index":0,"embedding":[0,0]}]}`},
		{"index out of range", 200, `{"object":"list","model":"m","usage":{"prompt_tokens":1,"total_tokens":1},"data":[{"object":"embedding","index":5,"embedding":[1,0]}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := embeddingServer(t, func(map[string]any) (int, string) { return tc.status, tc.body })
			p := newOpenAIProvider("test-key", srv.URL, "text-embedding-3-small", false)
			if vecs, err := p.Embed(context.Background(), []string{"x"}); err == nil {
				t.Fatalf("want error, got vectors %v", vecs)
			}
		})
	}
}

func TestOpenAIEmbedRejectsBadInput(t *testing.T) {
	p := newOpenAIProvider("test-key", "http://127.0.0.1:1", "text-embedding-3-small", false)
	if _, err := p.Embed(context.Background(), nil); err == nil {
		t.Error("nil texts must be rejected before any request")
	}
	if _, err := p.Embed(context.Background(), []string{"ok", "  "}); err == nil {
		t.Error("blank text must be rejected before any request")
	}
	empty := newOpenAIProvider("test-key", "http://127.0.0.1:1", " ", false)
	if _, err := empty.Embed(context.Background(), []string{"x"}); err == nil {
		t.Error("empty model must be rejected before any request")
	}
}

func TestFantasyOpenAIProviderEmbedDelegates(t *testing.T) {
	srv, _ := embeddingServer(t, func(map[string]any) (int, string) {
		return 200, `{"object":"list","model":"m","usage":{"prompt_tokens":1,"total_tokens":1},"data":[{"object":"embedding","index":0,"embedding":[1,0]}]}`
	})
	p := newProviderByType("openai", "test-key", srv.URL, "text-embedding-3-small", 0, 0, false, "none", 0, false)
	e, ok := p.(Embedder)
	if !ok {
		t.Fatalf("OpenAI-compatible provider must implement Embedder, got %T", p)
	}
	vecs, err := e.Embed(context.Background(), []string{"x"})
	if err != nil || len(vecs) != 1 || vecs[0][0] != 1 {
		t.Fatalf("Embed via fantasy provider: vecs=%v err=%v", vecs, err)
	}
}

func TestAnthropicProviderDoesNotImplementEmbedder(t *testing.T) {
	p := newProviderByType("anthropic", "test-key", "https://example.invalid", "claude-x", 0, 0, false, "none", 0, false)
	if _, ok := p.(Embedder); ok {
		t.Fatal("Anthropic provider must not implement Embedder")
	}
}
