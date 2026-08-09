// NOTE: 部分画图模型/中转网关不提供专用的 /images/generations 接口，只能通过标准的
// /chat/completions 接口调用；图片数据以扩展字段 delta.images[].image_url.url 的形式
// 随流式响应返回(data URL，形如 data:image/jpeg;base64,...)。该字段不属于 go-openai SDK
// ChatCompletionStreamChoiceDelta 结构体的标准字段，SDK 反序列化时会静默丢弃，因此这里
// 绕开 SDK，用原始 HTTP + SSE 解析实现，由 openAIProvider.imageViaChat 开关控制是否启用。
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// imageChatImagePart 对应网关扩展字段 delta.images[] 里的一个元素。
type imageChatImagePart struct {
	Type     string `json:"type"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// imageChatStreamChunk 是 chat/completions 流式响应单个 SSE 分片的精简结构，
// 只声明本次需要的字段。
type imageChatStreamChunk struct {
	Choices []struct {
		Delta struct {
			Images []imageChatImagePart `json:"images"`
		} `json:"delta"`
	} `json:"choices"`
}

// generateImageViaChat 通过 /chat/completions 接口调用只支持 Chat 方式的画图模型，
// 从流式响应的 delta.images[0].image_url.url 中取回图片(data URL 或远程 URL)，
// 返回值与 generateImage 保持一致：(base64Data, mimeType, err)。
func (p *openAIProvider) generateImageViaChat(ctx context.Context, model, prompt string) (string, string, error) {
	reqBody, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"stream": true,
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal image chat request: %w", err)
	}

	base := strings.TrimRight(p.baseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", "", fmt.Errorf("build image chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", "", fmt.Errorf("image chat request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", "", fmt.Errorf("image chat request failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	// imageURL 累积第一张图片(images[0])跨多个 SSE 分片到达的片段；Painter 每次只需要一张图，
	// 与专用图片接口的 N:1 语义保持一致。
	var imageURL strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // 图片 base64 数据量大，放大扫描缓冲区上限
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var chunk imageChatStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // NOTE: 忽略无法解析的分片(如心跳/注释行)，不中断流
		}
		for _, choice := range chunk.Choices {
			if len(choice.Delta.Images) == 0 {
				continue
			}
			imageURL.WriteString(choice.Delta.Images[0].ImageURL.URL)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", fmt.Errorf("read image chat stream: %w", err)
	}

	return parseImageChatURL(ctx, imageURL.String())
}

// parseImageChatURL 把累积出的 image_url.url 解析成 (base64Data, mimeType)。
// 优先按 data URL 直接解析；若网关返回的是远程可下载 URL，则下载后转 base64，
// 以保持 ImageGenerator 接口对外统一返回 base64 的约定。
func parseImageChatURL(ctx context.Context, raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("image chat response contained no image data")
	}
	if strings.HasPrefix(raw, "data:") {
		const marker = ";base64,"
		idx := strings.Index(raw, marker)
		if idx < 0 {
			return "", "", errors.New("unsupported data URL image format")
		}
		mimeType := raw[len("data:"):idx]
		base64Data := raw[idx+len(marker):]
		if mimeType == "" || base64Data == "" {
			return "", "", errors.New("empty data URL image fields")
		}
		return base64Data, mimeType, nil
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return downloadImageAsBase64(ctx, raw)
	}
	return "", "", fmt.Errorf("unrecognized image url format: %q", raw)
}

// downloadImageAsBase64 用于网关返回远程图片 URL(而非内联 data URL)的情况。
func downloadImageAsBase64(ctx context.Context, url string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("build image download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("download generated image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download generated image failed: status=%d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read downloaded image: %w", err)
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" || !strings.HasPrefix(mimeType, "image/") {
		mimeType = http.DetectContentType(body)
	}
	return base64.StdEncoding.EncodeToString(body), mimeType, nil
}
