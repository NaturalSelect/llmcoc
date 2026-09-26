// NOTE: 实现基于 openai-go/v3 的图片生成能力(GenerateImage)。聊天相关能力已全部迁移到
// fantasy_provider.go(charm.land/fantasy 的 LanguageModel)，本文件里的 openAIProvider
// 只保留画图所需的最小字段集。
package llm

import (
	"context"
	"errors"
	"fmt"
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

	resp, err := p.client.Images.Generate(ctx, openai.ImageGenerateParams{
		Model:          model,
		Prompt:         prompt,
		N:              param.NewOpt(int64(1)),
		Quality:        imageQualityForModel(model),
		Size:           imageSizeForModel(model, opts.Aspect),
		ResponseFormat: openai.ImageGenerateParamsResponseFormatB64JSON,
	})
	if err != nil {
		return "", "", fmt.Errorf("LLM image error: %w", err)
	}
	if len(resp.Data) == 0 || strings.TrimSpace(resp.Data[0].B64JSON) == "" {
		return "", "", errors.New("LLM returned no image data")
	}
	return resp.Data[0].B64JSON, "image/png", nil
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
