// NOTE: 覆盖 painter_nsfw 取代管理员 NSFW 配图开关后的行为：nsfw=true 的配图只路由到 painter_nsfw，
// 未启用时直接拒绝且不回落默认 Painter；普通配图不会误用 painter_nsfw；RunPainter 只按请求自身的 NSFW
// 标记渲染分级，不受房间开关影响。禁止真实网络与真实 LLM。
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func newPainterTestHandle(role models.AgentRole, prov llm.Provider, active bool, diffusion bool) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: role, IsActive: active, DiffusionMode: diffusion},
		enabled:  true,
	}
}

func TestRunPainter_NSFWRequestUsesPainterNSFWProvider(t *testing.T) {
	const sessionID = 9201
	normal := &recordingImageProvider{}
	nsfw := &recordingImageProvider{}
	sessionAgents.Store(uint(sessionID), map[models.AgentRole]agentHandle{
		models.AgentRolePainter:     newPainterTestHandle(models.AgentRolePainter, normal, true, false),
		models.AgentRolePainterNSFW: newPainterTestHandle(models.AgentRolePainterNSFW, nsfw, true, false),
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })

	// RunPainter 只信任请求自身的 NSFW 标记(房间开关由调用方过滤),分级政策必须按请求渲染。
	gctx := GameContext{Session: models.GameSession{ID: sessionID, EnableNSFW: false}}
	if _, err := RunPainter(context.Background(), gctx, ImagePromptRequest{Prompt: "a scene", NSFW: true}); err != nil {
		t.Fatalf("RunPainter error: %v", err)
	}
	if nsfw.calls != 1 || normal.calls != 0 {
		t.Fatalf("calls nsfw=%d normal=%d, want 1/0", nsfw.calls, normal.calls)
	}
	if !strings.Contains(nsfw.prompt, nsfwImagePolicyOn) || strings.Contains(nsfw.prompt, nsfwImagePolicyOff) {
		t.Errorf("NSFW request must render the NSFW image policy, got %q", nsfw.prompt)
	}

	if _, err := RunPainter(context.Background(), gctx, ImagePromptRequest{Prompt: "a scene"}); err != nil {
		t.Fatalf("RunPainter error: %v", err)
	}
	if normal.calls != 1 || nsfw.calls != 1 {
		t.Fatalf("calls nsfw=%d normal=%d, want 1/1", nsfw.calls, normal.calls)
	}
	if !strings.Contains(normal.prompt, nsfwImagePolicyOff) {
		t.Errorf("ordinary request in all-ages room must keep the all-ages policy, got %q", normal.prompt)
	}
}

// TestRunPainter_RoomNSFWDoesNotMakeOrdinaryImagesNSFW 验证房间开启NSFW时,nsfw=false的配图
// 仍按全年龄渲染:分级政策只由请求自身的NSFW标记决定,不受房间开关影响。
func TestRunPainter_RoomNSFWDoesNotMakeOrdinaryImagesNSFW(t *testing.T) {
	const sessionID = 9204
	normal := &recordingImageProvider{}
	sessionAgents.Store(uint(sessionID), map[models.AgentRole]agentHandle{
		models.AgentRolePainter: newPainterTestHandle(models.AgentRolePainter, normal, true, false),
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })

	gctx := GameContext{Session: models.GameSession{ID: sessionID, EnableNSFW: true}}
	if _, err := RunPainter(context.Background(), gctx, ImagePromptRequest{Prompt: "a scene"}); err != nil {
		t.Fatalf("RunPainter error: %v", err)
	}
	if !strings.Contains(normal.prompt, nsfwImagePolicyOff) || strings.Contains(normal.prompt, nsfwImagePolicyOn) {
		t.Errorf("nsfw=false image in an NSFW room must keep the all-ages policy, got %q", normal.prompt)
	}
}

// TestRunPainter_RoomNSFWDiffusionOrdinaryImageKeepsAllAgesNegativeTags 验证扩散模式下同样如此:
// 翻译提示词和负面标签都按请求渲染,房间开启NSFW不能让普通配图丢掉全年龄负面标签。
func TestRunPainter_RoomNSFWDiffusionOrdinaryImageKeepsAllAgesNegativeTags(t *testing.T) {
	const sessionID = 9205
	translator := &chatFakeProvider{chatResponses: []string{"1girl"}}
	normal := &recordingImageProvider{}
	sessionAgents.Store(uint(sessionID), map[models.AgentRole]agentHandle{
		models.AgentRoleDiffusionTranslator: newDiffusionTranslatorHandle(translator),
		models.AgentRolePainter:             newPainterTestHandle(models.AgentRolePainter, normal, true, true),
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })

	gctx := GameContext{Session: models.GameSession{ID: sessionID, EnableNSFW: true}}
	if _, err := RunPainter(context.Background(), gctx, ImagePromptRequest{Prompt: "a scene"}); err != nil {
		t.Fatalf("RunPainter error: %v", err)
	}
	if normal.opts.NegativePrompt != diffusionNegativePromptFor(false) {
		t.Errorf("negative = %q, want the all-ages negative prompt", normal.opts.NegativePrompt)
	}
	system := translator.chatMessages[0][0].Content
	if !strings.Contains(system, nsfwImagePolicyOff) || strings.Contains(system, nsfwImagePolicyOn) {
		t.Errorf("translator system prompt must use the all-ages policy for nsfw=false, got:\n%s", system)
	}
}

func TestRunPainter_NSFWRequestWithoutPainterNSFWFails(t *testing.T) {
	const sessionID = 9202
	normal := &recordingImageProvider{}
	sessionAgents.Store(uint(sessionID), map[models.AgentRole]agentHandle{
		models.AgentRolePainter: newPainterTestHandle(models.AgentRolePainter, normal, true, false),
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })

	_, err := RunPainter(context.Background(), GameContext{Session: models.GameSession{ID: sessionID, EnableNSFW: true}}, ImagePromptRequest{Prompt: "a scene", NSFW: true})
	if err == nil || !strings.Contains(err.Error(), string(models.AgentRolePainterNSFW)) {
		t.Fatalf("err = %v, want error naming painter_nsfw", err)
	}
	if normal.calls != 0 {
		t.Errorf("default painter must not be used for NSFW requests, calls = %d", normal.calls)
	}
}

func TestRunPainter_DiffusionNSFWRequestSkipsAllAgesNegativeTags(t *testing.T) {
	const sessionID = 9203
	writer := &chatFakeProvider{chatResponses: []string{"1girl"}}
	nsfw := &recordingImageProvider{}
	sessionAgents.Store(uint(sessionID), map[models.AgentRole]agentHandle{
		models.AgentRoleDiffusionTranslator: newDiffusionTranslatorHandle(writer),
		models.AgentRolePainterNSFW:         newPainterTestHandle(models.AgentRolePainterNSFW, nsfw, true, true),
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })

	gctx := GameContext{Session: models.GameSession{ID: sessionID, EnableNSFW: false}}
	if _, err := RunPainter(context.Background(), gctx, ImagePromptRequest{Prompt: "a scene", NSFW: true}); err != nil {
		t.Fatalf("RunPainter error: %v", err)
	}
	if nsfw.opts.NegativePrompt != diffusionBaseNegativePrompt {
		t.Errorf("negative = %q, want only the fixed base negative (no all-ages tags) for NSFW requests", nsfw.opts.NegativePrompt)
	}
	if !nsfw.opts.Diffusion {
		t.Errorf("painter_nsfw with diffusion_mode must use the diffusion path, opts = %+v", nsfw.opts)
	}
}
