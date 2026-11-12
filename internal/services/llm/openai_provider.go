// NOTE: 实现基于 openai-go/v3 的图片生成能力(GenerateImage)。聊天相关能力已全部迁移到
// fantasy_provider.go(charm.land/fantasy 的 LanguageModel)，本文件里的 openAIProvider
// 只保留画图所需的最小字段集。
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
)

type openAIProvider struct {
	client  openai.Client
	apiKey  string
	model   string
	baseURL string
	// imageViaChat 为 true 时 GenerateImage 改走 /chat/completions 而非 /images/generations，
	// 用于只能通过 Chat 接口调用的画图模型/中转网关。
	imageViaChat bool
}

func newOpenAIProvider(apiKey, baseURL, model string, imageViaChat bool) *openAIProvider {
	// NOTE: 自己维护 30 次重试(见 GenerateImage)，关掉 SDK 内置重试避免两层重试叠加。
	opts := []option.RequestOption{option.WithMaxRetries(0), option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &openAIProvider{
		client:       openai.NewClient(opts...),
		apiKey:       apiKey,
		model:        model,
		baseURL:      baseURL,
		imageViaChat: imageViaChat,
	}
}

// imageModelFamily 归类图片模型支持的参数范围(quality/size 的合法取值因模型而异)。
type imageModelFamily int

const (
	imageModelOther imageModelFamily = iota
	imageModelGptImage
	imageModelDallE3
)

func classifyImageModel(model string) imageModelFamily {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "gpt-image"):
		return imageModelGptImage
	case strings.Contains(m, "dall-e-3"), strings.Contains(m, "dall-e3"):
		return imageModelDallE3
	default:
		return imageModelOther
	}
}

// imageQualityForModel 按模型名选择最高画质参数;不传 quality 时接口会用默认档位
// (dall-e-3 默认 standard、gpt-image-1 默认 auto)，画质会低于预期。
// dall-e-2 不支持 quality 参数,返回空字符串让 omitzero 跳过。
func imageQualityForModel(model string) openai.ImageGenerateParamsQuality {
	switch classifyImageModel(model) {
	case imageModelGptImage:
		return openai.ImageGenerateParamsQualityHigh
	case imageModelDallE3:
		return openai.ImageGenerateParamsQualityHD
	default:
		return ""
	}
}

// imageSizeForModel 把 Director 选择的语义化画面方向翻译成具体模型的合法尺寸值。
// 未知模型或未知/空 aspect 一律回落方图 1024x1024——非法尺寸会被 GenerateImage 的
// 30 次重试循环放大成长时间失败,宁可忽略方向也不要发出会被拒绝的尺寸。
func imageSizeForModel(model string, aspect ImageAspect) openai.ImageGenerateParamsSize {
	family := classifyImageModel(model)
	switch aspect {
	case ImageAspectLandscape:
		switch family {
		case imageModelGptImage:
			return openai.ImageGenerateParamsSize1536x1024
		case imageModelDallE3:
			return openai.ImageGenerateParamsSize1792x1024
		}
	case ImageAspectPortrait:
		switch family {
		case imageModelGptImage:
			return openai.ImageGenerateParamsSize1024x1536
		case imageModelDallE3:
			return openai.ImageGenerateParamsSize1024x1792
		}
	}
	return openai.ImageGenerateParamsSize1024x1024
}

// Embed 调用 /embeddings 并返回 L2 归一化后的向量。不做重试：调用方有标签匹配兜底，失败即退回。
func (p *openAIProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, errors.New("embedding input is empty")
	}
	for _, t := range texts {
		if strings.TrimSpace(t) == "" {
			return nil, errors.New("embedding input contains an empty text")
		}
	}
	model := strings.TrimSpace(p.model)
	if model == "" {
		return nil, errors.New("embedding model is empty")
	}

	start := time.Now()
	vectors, err := p.embed(ctx, model, texts)
	recordLatency("embedding", model, "embed", time.Since(start), err)
	return vectors, err
}

func (p *openAIProvider) embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	resp, err := p.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Model: openai.EmbeddingModel(model),
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM embedding error: %w", err)
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("LLM embedding returned %d vectors for %d inputs", len(resp.Data), len(texts))
	}

	vectors := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index < 0 || int(d.Index) >= len(texts) || vectors[d.Index] != nil {
			return nil, fmt.Errorf("LLM embedding returned an invalid index %d", d.Index)
		}
		v, err := normalizeEmbedding(d.Embedding)
		if err != nil {
			return nil, err
		}
		vectors[d.Index] = v
	}
	return vectors, nil
}

// normalizeEmbedding 转成 float32 并做 L2 归一化；空向量、非有限值或零向量都视为失败。
func normalizeEmbedding(raw []float64) ([]float32, error) {
	if len(raw) == 0 {
		return nil, errors.New("LLM embedding returned an empty vector")
	}
	var sum float64
	for _, x := range raw {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, errors.New("LLM embedding contains a non-finite value")
		}
		sum += x * x
	}
	if sum == 0 {
		return nil, errors.New("LLM embedding is a zero vector")
	}
	norm := math.Sqrt(sum)
	out := make([]float32, len(raw))
	for i, x := range raw {
		out[i] = float32(x / norm)
	}
	return out, nil
}

// NOTE: 扩散模型(NovelAI)分辨率边长须为 64 的倍数且总像素不超过 1024x1024，否则 new-api 直接拒绝并被重试循环放大。
func imageSizeForDiffusion(aspect ImageAspect) openai.ImageGenerateParamsSize {
	switch aspect {
	case ImageAspectLandscape:
		return "1216x832"
	case ImageAspectPortrait:
		return "832x1216"
	}
	return openai.ImageGenerateParamsSize1024x1024
}

func buildImageParams(model, prompt string, opts ImageOptions) openai.ImageGenerateParams {
	params := openai.ImageGenerateParams{
		Model:          model,
		Prompt:         prompt,
		N:              param.NewOpt(int64(1)),
		ResponseFormat: openai.ImageGenerateParamsResponseFormatB64JSON,
	}
	if !opts.Diffusion {
		params.Quality = imageQualityForModel(model)
		params.Size = imageSizeForModel(model, opts.Aspect)
		return params
	}
	params.Size = imageSizeForDiffusion(opts.Aspect)
	if opts.NegativePrompt != "" {
		// NOTE: negative_prompt 不是 OpenAI 标准字段，new-api 的 NovelAI 渠道从请求体顶层读取它。
		params.SetExtraFields(map[string]any{"negative_prompt": opts.NegativePrompt})
	}
	return params
}

func (p *openAIProvider) generateImage(ctx context.Context, prompt string, opts ImageOptions) (string, string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", "", errors.New("image prompt is empty")
	}
	model := strings.TrimSpace(p.model)
	if model == "" {
		return "", "", errors.New("image model is empty")
	}

	if p.imageViaChat {
		return p.generateImageViaChat(ctx, model, prompt)
	}

	params := buildImageParams(model, prompt, opts)
	if shouldStreamImage(model) {
		return p.generateImageStream(ctx, params)
	}

	resp, err := p.client.Images.Generate(ctx, params)
	if err != nil {
		return "", "", fmt.Errorf("LLM image error: %w", err)
	}
	return firstImageData(resp)
}

func firstImageData(resp *openai.ImagesResponse) (string, string, error) {
	if len(resp.Data) == 0 || strings.TrimSpace(resp.Data[0].B64JSON) == "" {
		return "", "", errors.New("LLM returned no image data")
	}
	return resp.Data[0].B64JSON, "image/png", nil
}

// NOTE: OpenAI 文档里 dall-e-2/3 不支持 stream 参数，会直接拒绝并被 30 次重试循环放大成长时间失败，
// 所以只有它们保持一次性请求；其余模型(gpt-image、扩散渠道、第三方网关)一律走流式。
func shouldStreamImage(model string) bool {
	return !strings.Contains(strings.ToLower(model), "dall-e")
}

// generateImageStream 以 SSE 请求 /images/generations，只取最终的 completed 事件。
// 不请求中间预览图(partial_images)，预览图会额外计费且这里用不上。
func (p *openAIProvider) generateImageStream(ctx context.Context, params openai.ImageGenerateParams) (string, string, error) {
	var raw *http.Response
	stream := p.client.Images.GenerateStreaming(ctx, params, option.WithResponseInto(&raw))
	defer stream.Close()
	if err := stream.Err(); err != nil {
		return "", "", fmt.Errorf("LLM image error: %w", err)
	}

	// NOTE: 部分网关会忽略 stream 参数直接返回完整 JSON。此时图片已经生成并计费，必须按普通响应解析，不能重发请求。
	if raw != nil && strings.HasPrefix(raw.Header.Get("Content-Type"), "application/json") {
		var resp openai.ImagesResponse
		if err := json.NewDecoder(raw.Body).Decode(&resp); err != nil {
			return "", "", fmt.Errorf("LLM image error: decode response: %w", err)
		}
		return firstImageData(&resp)
	}

	for stream.Next() {
		ev := stream.Current()
		if ev.Type != "image_generation.completed" {
			continue
		}
		if strings.TrimSpace(ev.B64JSON) == "" {
			return "", "", errors.New("LLM returned no image data")
		}
		mime := "image/png"
		switch ev.OutputFormat {
		case "jpeg", "webp":
			mime = "image/" + ev.OutputFormat
		}
		return ev.B64JSON, mime, nil
	}
	if err := stream.Err(); err != nil {
		return "", "", fmt.Errorf("LLM image stream error: %w", err)
	}
	return "", "", errors.New("LLM image stream ended without a completed image")
}

func (p *openAIProvider) GenerateImage(ctx context.Context, prompt string, opts ImageOptions) (string, string, error) {
	for i := 0; i < 30; i++ {
		start := time.Now()
		data, mime, err := p.generateImage(ctx, prompt, opts)
		recordLatency("painter", p.model, "image", time.Since(start), err)
		if err != nil {
			log.Error("generate image failed", "attempt", i+1, "err", err)
			continue
		}
		if data == "" {
			continue
		}
		return data, mime, nil
	}
	return "", "", errors.New("LLM failed to generate image after 30 attempts")
}
