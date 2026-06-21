package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

type generateImageAction struct{}

func (generateImageAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	imagePrompt := strings.TrimSpace(call.ImagePrompt)
	if imagePrompt == "" {
		return []ToolResult{{Action: ToolGenerateImage, Result: "image generation failed: image_prompt is required"}}
	}

	handle, ok := actx.Handles[models.AgentRolePainter]
	if !ok || !handle.isEnabled() {
		return []ToolResult{{Action: ToolGenerateImage, Result: "image generation unavailable"}}
	}
	if _, ok := handle.provider.(llm.ImageGenerator); !ok {
		return []ToolResult{{Action: ToolGenerateImage, Result: "image generation unavailable"}}
	}
	if actx.PendingImages != nil {
		*actx.PendingImages = append(*actx.PendingImages, imagePrompt)
	}
	return []ToolResult{{Action: ToolGenerateImage, Result: "image generation queued"}}
}

// NOTE: 根据排队提示词生成一张临时图片data URL;结果只用于当前SSE,不写入数据库。
func RunPainter(ctx context.Context, gctx GameContext, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("image prompt is empty")
	}
	ctx = withWriterGameSessionID(ctx, gctx)
	handles, err := getCachedAgents(gctx.Session.ID)
	if err != nil {
		return "", err
	}
	handle, ok := handles[models.AgentRolePainter]
	if !ok || !handle.isEnabled() {
		return "", fmt.Errorf("painter agent 未配置或未启用")
	}
	generator, ok := handle.provider.(llm.ImageGenerator)
	if !ok {
		return "", fmt.Errorf("当前 Painter provider 不支持图片生成")
	}
	base64Data, mimeType, err := generator.GenerateImage(ctx, prompt, "1024x1024")
	if err != nil {
		return "", err
	}
	dataURL, ok := buildImageDataURL(base64Data, mimeType)
	if !ok {
		return "", fmt.Errorf("empty image data")
	}
	return dataURL, nil
}

func buildImageDataURL(base64Data string, mimeType string) (string, bool) {
	base64Data = strings.Join(strings.Fields(base64Data), "")
	if base64Data == "" {
		return "", false
	}
	mimeType = strings.TrimSpace(mimeType)
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/png"
	}
	return "data:" + mimeType + ";base64," + base64Data, true
}
