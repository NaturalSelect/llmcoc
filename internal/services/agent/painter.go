package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

type generateImageAction struct{}

func (generateImageAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	imagePrompt := strings.TrimSpace(call.ImagePrompt)
	if imagePrompt == "" {
		debugf("tool", "session=%d generate_image rejected: empty image_prompt", actx.Sid)
		return []ToolResult{{Action: ToolGenerateImage, Result: "image generation failed: image_prompt is required"}}
	}

	handle, ok := actx.Handles[models.AgentRolePainter]
	if !ok || !handle.isEnabled() {
		debugf("tool", "session=%d generate_image unavailable: painter disabled prompt_len=%d prompt=%q", actx.Sid, len([]rune(imagePrompt)), truncateRunes(imagePrompt, 200))
		return []ToolResult{{Action: ToolGenerateImage, Result: "image generation unavailable"}}
	}
	if _, ok := handle.provider.(llm.ImageGenerator); !ok {
		debugf("tool", "session=%d generate_image unavailable: provider lacks image generation prompt_len=%d prompt=%q", actx.Sid, len([]rune(imagePrompt)), truncateRunes(imagePrompt, 200))
		return []ToolResult{{Action: ToolGenerateImage, Result: "image generation unavailable"}}
	}
	if actx.PendingImages != nil {
		*actx.PendingImages = append(*actx.PendingImages, imagePrompt)
	}
	debugf("tool", "session=%d generate_image queued prompt_len=%d prompt=%q", actx.Sid, len([]rune(imagePrompt)), truncateRunes(imagePrompt, 200))
	return []ToolResult{{Action: ToolGenerateImage, Result: "image generation queued"}}
}

// NOTE: 根据排队提示词生成一张临时图片data URL;结果只用于当前SSE,不写入数据库。
func RunPainter(ctx context.Context, gctx GameContext, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("image prompt is empty")
	}
	start := time.Now()
	debugf("Painter", "session=%d start prompt_len=%d prompt=%q", gctx.Session.ID, len([]rune(prompt)), truncateRunes(prompt, 200))
	ctx = withWriterGameSessionID(ctx, gctx)
	handles, err := getCachedAgents(gctx.Session.ID)
	if err != nil {
		debugf("Painter", "session=%d load agents error elapsed=%.0fms err=%v", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000, err)
		return "", err
	}
	handle, ok := handles[models.AgentRolePainter]
	if !ok || !handle.isEnabled() {
		debugf("Painter", "session=%d unavailable: painter disabled elapsed=%.0fms", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000)
		return "", fmt.Errorf("painter agent 未配置或未启用")
	}
	generator, ok := handle.provider.(llm.ImageGenerator)
	if !ok {
		debugf("Painter", "session=%d unavailable: provider lacks image generation elapsed=%.0fms", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000)
		return "", fmt.Errorf("当前 Painter provider 不支持图片生成")
	}
	base64Data, mimeType, err := generator.GenerateImage(ctx, prompt, "1024x1024")
	if err != nil {
		debugf("Painter", "session=%d error elapsed=%.0fms err=%v", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000, err)
		return "", err
	}
	dataURL, ok := buildImageDataURL(base64Data, mimeType)
	if !ok {
		debugf("Painter", "session=%d invalid image data elapsed=%.0fms mime=%q base64_len=%d", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000, mimeType, len(base64Data))
		return "", fmt.Errorf("empty image data")
	}
	debugf("Painter", "session=%d success elapsed=%.0fms mime=%q base64_len=%d", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000, mimeType, len(base64Data))
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
