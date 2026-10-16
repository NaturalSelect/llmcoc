// NOTE: 覆盖 painter_nsfw 取代管理员 NSFW 配图开关后的行为：nsfw=true 的配图只路由到 painter_nsfw，
// 未启用时直接拒绝且不回落默认 Painter；普通配图不会误用 painter_nsfw；RunPainter 按请求而非仅按房间
// 开关渲染分级。禁止真实网络与真实 LLM。
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

func TestGenerateImageActionNSFWRouting(t *testing.T) {
	cases := []struct {
		name        string
		nsfwCall    bool
		painter     *agentHandle
		painterNSFW *agentHandle
		wantQueued  bool
		wantResult  string
	}{
		{
			name:       "普通配图使用默认Painter",
			painter:    handlePtr(newPainterTestHandle(models.AgentRolePainter, &recordingImageProvider{}, true, false)),
			wantQueued: true,
			wantResult: "image generation queued",
		},
		{
			name:        "nsfw配图使用painter_nsfw",
			nsfwCall:    true,
			painter:     handlePtr(newPainterTestHandle(models.AgentRolePainter, &recordingImageProvider{}, true, false)),
			painterNSFW: handlePtr(newPainterTestHandle(models.AgentRolePainterNSFW, &recordingImageProvider{}, true, false)),
			wantQueued:  true,
			wantResult:  "image generation queued",
		},
		{
			name:       "nsfw配图且painter_nsfw未配置则拒绝,不回落默认Painter",
			nsfwCall:   true,
			painter:    handlePtr(newPainterTestHandle(models.AgentRolePainter, &recordingImageProvider{}, true, false)),
			wantQueued: false,
			wantResult: "NSFW image generation is not enabled",
		},
		{
			name:        "nsfw配图且painter_nsfw被禁用则拒绝",
			nsfwCall:    true,
			painter:     handlePtr(newPainterTestHandle(models.AgentRolePainter, &recordingImageProvider{}, true, false)),
			painterNSFW: handlePtr(newPainterTestHandle(models.AgentRolePainterNSFW, &recordingImageProvider{}, false, false)),
			wantQueued:  false,
			wantResult:  "NSFW image generation is not enabled",
		},
		{
			name:        "普通配图不会路由到painter_nsfw",
			painterNSFW: handlePtr(newPainterTestHandle(models.AgentRolePainterNSFW, &recordingImageProvider{}, true, false)),
			wantQueued:  false,
			wantResult:  "image generation unavailable",
		},
		{
			name:        "painter的provider不支持画图时nsfw配图不可用",
			nsfwCall:    true,
			painterNSFW: handlePtr(newPainterTestHandle(models.AgentRolePainterNSFW, &sequentialFakeProvider{}, true, false)),
			wantQueued:  false,
			wantResult:  "image generation unavailable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handles := map[models.AgentRole]agentHandle{}
			if tc.painter != nil {
				handles[models.AgentRolePainter] = *tc.painter
			}
			if tc.painterNSFW != nil {
				handles[models.AgentRolePainterNSFW] = *tc.painterNSFW
			}
			pending := []ImagePromptRequest{}
			actx := ActionContext{Handles: handles, PendingImages: &pending}

			results := generateImageAction{}.Execute(ToolCall{ImagePrompt: "a foggy lighthouse", Aspect: "portrait", NSFW: tc.nsfwCall}, actx)
			if len(results) != 1 || !strings.Contains(results[0].Result, tc.wantResult) {
				t.Fatalf("results = %+v, want result containing %q", results, tc.wantResult)
			}
			if tc.wantQueued {
				if len(pending) != 1 {
					t.Fatalf("pending = %+v, want exactly one queued request", pending)
				}
				if pending[0].NSFW != tc.nsfwCall || pending[0].Aspect != "portrait" {
					t.Errorf("queued request = %+v, want NSFW=%v aspect=portrait", pending[0], tc.nsfwCall)
				}
			} else if len(pending) != 0 {
				t.Errorf("pending = %+v, want nothing queued", pending)
			}
		})
	}
}

func handlePtr(h agentHandle) *agentHandle { return &h }

func TestRunPainter_NSFWRequestUsesPainterNSFWProvider(t *testing.T) {
	const sessionID = 9201
	normal := &recordingImageProvider{}
	nsfw := &recordingImageProvider{}
	sessionAgents.Store(uint(sessionID), map[models.AgentRole]agentHandle{
		models.AgentRolePainter:     newPainterTestHandle(models.AgentRolePainter, normal, true, false),
		models.AgentRolePainterNSFW: newPainterTestHandle(models.AgentRolePainterNSFW, nsfw, true, false),
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })

	// 房间 NSFW 开关关闭,但请求本身被标记 NSFW:分级政策必须按请求渲染为允许 NSFW。
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
