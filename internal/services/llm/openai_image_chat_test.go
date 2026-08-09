// NOTE: 覆盖 openai_image_chat.go —— 部分画图模型只能通过 /chat/completions 调用，
// 图片数据以扩展字段 delta.images[].image_url.url 随 SSE 流返回，go-openai SDK 不认识
// 该字段会静默丢弃，因此这里绕开 SDK 手写 HTTP+SSE，测试用假 HTTP 端点覆盖：
// 单片完整 data URL、跨多片拼接 data URL、远程 URL 下载转 base64、无图片数据报错、
// 以及请求本身的形状(model/messages/stream/Authorization)。
// 禁止真实网络/真实LLM；全部基于内存假 HTTP 端点。
package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeImageChatImageURL struct {
	URL string `json:"url"`
}

type fakeImageChatImagePart struct {
	Type     string                `json:"type"`
	ImageURL fakeImageChatImageURL `json:"image_url"`
}

type fakeImageChatDelta struct {
	Content string                   `json:"content,omitempty"`
	Images  []fakeImageChatImagePart `json:"images,omitempty"`
}

type fakeImageChatChoice struct {
	Delta fakeImageChatDelta `json:"delta"`
}

type fakeImageChatChunk struct {
	Choices []fakeImageChatChoice `json:"choices"`
}

func imageChunk(url string) fakeImageChatChunk {
	return fakeImageChatChunk{Choices: []fakeImageChatChoice{{Delta: fakeImageChatDelta{
		Images: []fakeImageChatImagePart{{Type: "image_url", ImageURL: fakeImageChatImageURL{URL: url}}},
	}}}}
}

func contentChunk(content string) fakeImageChatChunk {
	return fakeImageChatChunk{Choices: []fakeImageChatChoice{{Delta: fakeImageChatDelta{Content: content}}}}
}

// capturedImageChatRequest 记录假端点收到的最后一次请求，供断言请求形状。
type capturedImageChatRequest struct {
	req  *http.Request
	body []byte
}

// newFakeImageChatServer 起一个假 /chat/completions SSE 端点，把 chunks 依次下发。
func newFakeImageChatServer(t *testing.T, chunks []fakeImageChatChunk) (*httptest.Server, *capturedImageChatRequest) {
	t.Helper()
	captured := &capturedImageChatRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.req = r
		captured.body = body

		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter 不支持 http.Flusher")
		}
		for _, c := range chunks {
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatalf("marshal chunk: %v", err)
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func TestGenerateImageViaChat_DataURLSingleChunk(t *testing.T) {
	wantURL := "data:image/jpeg;base64,ZmFrZS1qcGVn"
	srv, _ := newFakeImageChatServer(t, []fakeImageChatChunk{imageChunk(wantURL)})
	p := newOpenAIProvider("test-key", srv.URL, "gemini-3.1-flash-image", 0, 0, false, "", true)

	base64Data, mimeType, err := p.generateImageViaChat(context.Background(), p.model, "a cat")
	if err != nil {
		t.Fatalf("generateImageViaChat error: %v", err)
	}
	if base64Data != "ZmFrZS1qcGVn" {
		t.Errorf("base64Data = %q, want %q", base64Data, "ZmFrZS1qcGVn")
	}
	if mimeType != "image/jpeg" {
		t.Errorf("mimeType = %q, want image/jpeg", mimeType)
	}
}

func TestGenerateImageViaChat_DataURLFragmented(t *testing.T) {
	full := "data:image/png;base64,QUJDREVGRw=="
	// NOTE: 模拟网关像文本 delta 一样把同一张图片的 url 分片下发,验证按到达顺序拼接。
	mid := len(full) / 2
	srv, _ := newFakeImageChatServer(t, []fakeImageChatChunk{
		contentChunk(""), // 夹杂一个只有 content 没有 images 的分片,不应影响拼接
		imageChunk(full[:mid]),
		imageChunk(full[mid:]),
	})
	p := newOpenAIProvider("test-key", srv.URL, "gemini-3.1-flash-image", 0, 0, false, "", true)

	base64Data, mimeType, err := p.generateImageViaChat(context.Background(), p.model, "a cat")
	if err != nil {
		t.Fatalf("generateImageViaChat error: %v", err)
	}
	if base64Data != "QUJDREVGRw==" {
		t.Errorf("base64Data = %q, want %q", base64Data, "QUJDREVGRw==")
	}
	if mimeType != "image/png" {
		t.Errorf("mimeType = %q, want image/png", mimeType)
	}
}

func TestGenerateImageViaChat_RemoteURLDownloaded(t *testing.T) {
	imageBytes := []byte("fake-webp-bytes")
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/webp")
		w.Write(imageBytes)
	}))
	t.Cleanup(imgSrv.Close)

	srv, _ := newFakeImageChatServer(t, []fakeImageChatChunk{imageChunk(imgSrv.URL + "/generated.webp")})
	p := newOpenAIProvider("test-key", srv.URL, "some-chat-image-model", 0, 0, false, "", true)

	base64Data, mimeType, err := p.generateImageViaChat(context.Background(), p.model, "a cat")
	if err != nil {
		t.Fatalf("generateImageViaChat error: %v", err)
	}
	if mimeType != "image/webp" {
		t.Errorf("mimeType = %q, want image/webp", mimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		t.Fatalf("decode base64Data: %v", err)
	}
	if string(decoded) != string(imageBytes) {
		t.Errorf("decoded image = %q, want %q", decoded, imageBytes)
	}
}

func TestGenerateImageViaChat_NoImageData(t *testing.T) {
	srv, _ := newFakeImageChatServer(t, []fakeImageChatChunk{contentChunk("我不会画图")})
	p := newOpenAIProvider("test-key", srv.URL, "gemini-3.1-flash-image", 0, 0, false, "", true)

	if _, _, err := p.generateImageViaChat(context.Background(), p.model, "a cat"); err == nil {
		t.Fatal("want error when stream contains no images field, got nil")
	}
}

func TestGenerateImageViaChat_RequestShape(t *testing.T) {
	srv, captured := newFakeImageChatServer(t, []fakeImageChatChunk{
		imageChunk("data:image/png;base64,QUJD"),
	})
	p := newOpenAIProvider("my-api-key", srv.URL, "gemini-3.1-flash-image", 0, 0, false, "", true)

	if _, _, err := p.generateImageViaChat(context.Background(), p.model, "draw a cat"); err != nil {
		t.Fatalf("generateImageViaChat error: %v", err)
	}

	if captured.req.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", captured.req.Method)
	}
	if captured.req.URL.Path != "/chat/completions" {
		t.Errorf("path = %s, want /chat/completions", captured.req.URL.Path)
	}
	if got := captured.req.Header.Get("Authorization"); got != "Bearer my-api-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer my-api-key")
	}

	var body struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(captured.body, &body); err != nil {
		t.Fatalf("unmarshal request body: %v; raw=%s", err, captured.body)
	}
	if body.Model != "gemini-3.1-flash-image" {
		t.Errorf("model = %q, want gemini-3.1-flash-image", body.Model)
	}
	if !body.Stream {
		t.Error("stream should be true")
	}
	if len(body.Messages) != 1 || body.Messages[0].Role != "user" || !strings.Contains(body.Messages[0].Content, "draw a cat") {
		t.Errorf("messages = %+v, want single user message containing prompt", body.Messages)
	}
}
