// NOTE: 覆盖图片生成的两个模型相关映射：imageSizeForModel(语义化 aspect → 具体尺寸)
// 和 imageQualityForModel(模型 → 最高画质档位)。未知模型/未知 aspect 必须回落到
// 安全默认值(方图/不传quality)，否则会被 GenerateImage 的重试循环放大成长时间失败。
package llm

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestImageSizeForModel(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		aspect ImageAspect
		want   string
	}{
		{"gpt-image landscape", "gpt-image-1", ImageAspectLandscape, openai.CreateImageSize1536x1024},
		{"gpt-image portrait", "gpt-image-1", ImageAspectPortrait, openai.CreateImageSize1024x1536},
		{"gpt-image square", "gpt-image-1", ImageAspectSquare, openai.CreateImageSize1024x1024},
		{"dall-e-3 landscape", "dall-e-3", ImageAspectLandscape, openai.CreateImageSize1792x1024},
		{"dall-e-3 portrait", "dall-e-3", ImageAspectPortrait, openai.CreateImageSize1024x1792},
		{"dall-e3 无连字符变体仍识别为 dall-e-3", "dall-e3", ImageAspectLandscape, openai.CreateImageSize1792x1024},
		{"dall-e-2 不支持横竖图,回落方图", "dall-e-2", ImageAspectLandscape, openai.CreateImageSize1024x1024},
		{"未知模型回落方图", "some-third-party-model", ImageAspectLandscape, openai.CreateImageSize1024x1024},
		{"空 aspect 回落方图", "gpt-image-1", "", openai.CreateImageSize1024x1024},
		{"非法 aspect 回落方图", "dall-e-3", ImageAspect("garbage"), openai.CreateImageSize1024x1024},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imageSizeForModel(tt.model, tt.aspect)
			if got != tt.want {
				t.Errorf("imageSizeForModel(%q, %q) = %q, want %q", tt.model, tt.aspect, got, tt.want)
			}
		})
	}
}

func TestImageQualityForModel(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{"gpt-image 取 high", "gpt-image-1", openai.CreateImageQualityHigh},
		{"dall-e-3 取 hd", "dall-e-3", openai.CreateImageQualityHD},
		{"dall-e3 无连字符变体仍取 hd", "dall-e3", openai.CreateImageQualityHD},
		{"dall-e-2 不支持quality,留空", "dall-e-2", ""},
		{"未知模型留空", "some-third-party-model", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imageQualityForModel(tt.model)
			if got != tt.want {
				t.Errorf("imageQualityForModel(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}
