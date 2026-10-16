// NOTE: 覆盖扩散模型提示词翻译阶段：Writer 纯文本输出清理（thinking 头/空白/空结果）、
// 负面提示词为固定常量且全年龄会话追加分级标签、以及翻译请求的消息形状。禁止真实网络与真实 LLM。
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// chatFakeProvider 按序返回预设的 Chat 响应并记录调用，用于验证 Writer 的纯文本翻译。
type chatFakeProvider struct {
	sequentialFakeProvider
	chatResponses []string
	chatIdx       int
	chatKeys      []string
	chatMessages  [][]llm.ChatMessage
	chatErr       error
}

func (p *chatFakeProvider) Chat(_ context.Context, cacheKey string, msgs []llm.ChatMessage) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chatKeys = append(p.chatKeys, cacheKey)
	p.chatMessages = append(p.chatMessages, append([]llm.ChatMessage(nil), msgs...))
	if p.chatErr != nil {
		return "", p.chatErr
	}
	if p.chatIdx >= len(p.chatResponses) {
		return "", nil
	}
	resp := p.chatResponses[p.chatIdx]
	p.chatIdx++
	return resp, nil
}

func newDiffusionTranslatorHandle(prov llm.Provider) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleWriter, IsActive: true},
		enabled:  true,
	}
}

func TestTranslateToDiffusionPrompt_ReturnsCompactedTags(t *testing.T) {
	tests := []struct {
		name string
		resp string
		want string
	}{
		{"标准标签行", "1girl, library, bookshelf", "1girl, library, bookshelf"},
		{"空白与换行被压缩", " 1girl,\n  library \n", "1girl, library"},
		{"thinking 头被清理", "Thinking...\n> reasoning\nno humans, lighthouse, fog", "no humans, lighthouse, fog"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov := &chatFakeProvider{chatResponses: []string{tt.resp}}
			got, err := translateToDiffusionPrompt(context.Background(), newDiffusionTranslatorHandle(prov), "7", "scene", true)
			if err != nil {
				t.Fatalf("translate error: %v", err)
			}
			if got != tt.want {
				t.Errorf("prompt = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTranslateToDiffusionPrompt_EmptyResponseFails(t *testing.T) {
	prov := &chatFakeProvider{chatResponses: []string{"  \n "}}
	if _, err := translateToDiffusionPrompt(context.Background(), newDiffusionTranslatorHandle(prov), "7", "scene", true); err == nil {
		t.Fatal("want error when Writer returns no tags")
	}
}

func TestTranslateToDiffusionPrompt_ProviderErrorPropagated(t *testing.T) {
	prov := &chatFakeProvider{chatErr: errors.New("upstream down")}
	_, err := translateToDiffusionPrompt(context.Background(), newDiffusionTranslatorHandle(prov), "7", "scene", true)
	if err == nil || !strings.Contains(err.Error(), "upstream down") {
		t.Fatalf("err = %v, want upstream error propagated", err)
	}
}

func TestTranslateToDiffusionPrompt_UsesPlainChatNotJSON(t *testing.T) {
	prov := &chatFakeProvider{chatResponses: []string{"1girl"}}
	if _, err := translateToDiffusionPrompt(context.Background(), newDiffusionTranslatorHandle(prov), "7", "scene", true); err != nil {
		t.Fatalf("translate error: %v", err)
	}
	if len(prov.recordedKeys) != 0 {
		t.Errorf("JsonChat/ChatWithTools must not be used, got keys %v", prov.recordedKeys)
	}
}

func TestDiffusionNegativePromptFor(t *testing.T) {
	if got := diffusionNegativePromptFor(true); got != diffusionBaseNegativePrompt {
		t.Errorf("NSFW negative = %q, want the fixed base negative %q", got, diffusionBaseNegativePrompt)
	}
	allAges := diffusionNegativePromptFor(false)
	if allAges != allAgesNegativeTags+", "+diffusionBaseNegativePrompt {
		t.Errorf("all-ages negative = %q, want rating tags prepended to the base negative", allAges)
	}
	if strings.Contains(diffusionBaseNegativePrompt, "nsfw") {
		t.Error("base negative must not contain rating tags, otherwise NSFW requests would be blocked")
	}
}

func TestTranslateToDiffusionPrompt_MessageShape(t *testing.T) {
	prov := &chatFakeProvider{chatResponses: []string{"1girl"}}

	if _, err := translateToDiffusionPrompt(context.Background(), newDiffusionTranslatorHandle(prov), "42", "### Scene\nA lighthouse.", false); err != nil {
		t.Fatalf("translate error: %v", err)
	}
	if len(prov.chatKeys) != 1 || prov.chatKeys[0] != "42:painter" {
		t.Errorf("cacheKey = %v, want [42:painter]", prov.chatKeys)
	}
	msgs := prov.chatMessages[0]
	if len(msgs) != 2 || msgs[0].Role != "system" || msgs[1].Role != "user" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
	if msgs[1].Content != "### Scene\nA lighthouse." {
		t.Errorf("user content = %q, want the scene prompt verbatim", msgs[1].Content)
	}
	if strings.Contains(msgs[0].Content, "{{") {
		t.Errorf("system prompt still has unrendered placeholder: %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, nsfwImagePolicyOff) {
		t.Errorf("all-ages session must render the off image policy")
	}
}

// recordingImageProvider 记录 GenerateImage 收到的提示词与选项，用于验证两阶段串联结果。
type recordingImageProvider struct {
	sequentialFakeProvider
	prompt string
	opts   llm.ImageOptions
	calls  int
}

func (p *recordingImageProvider) GenerateImage(_ context.Context, prompt string, opts llm.ImageOptions) (string, string, error) {
	p.calls++
	p.prompt = prompt
	p.opts = opts
	return "ZmFrZS1pbWFnZQ==", "image/png", nil
}

var _ llm.ImageGenerator = (*recordingImageProvider)(nil)

func storePainterHandles(t *testing.T, sessionID uint, painterDiffusion bool, writer agentHandle, painter llm.Provider) {
	t.Helper()
	sessionAgents.Store(sessionID, map[models.AgentRole]agentHandle{
		models.AgentRoleWriter:     writer,
		models.AgentRoleWriterNSFW: {enabled: false},
		models.AgentRolePainter: {
			provider: painter,
			config:   &models.AgentConfig{Role: models.AgentRolePainter, IsActive: true, DiffusionMode: painterDiffusion},
			enabled:  true,
		},
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })
}

func TestRunPainter_DiffusionModeTranslatesThenGenerates(t *testing.T) {
	const sessionID = 9101
	writer := &chatFakeProvider{chatResponses: []string{"1girl, lighthouse, fog"}}
	painter := &recordingImageProvider{}
	storePainterHandles(t, sessionID, true, newDiffusionTranslatorHandle(writer), painter)

	gctx := GameContext{Session: models.GameSession{ID: sessionID}}
	dataURL, err := RunPainter(context.Background(), gctx, ImagePromptRequest{Prompt: "### Scene\nA foggy lighthouse.", Aspect: "portrait"})
	if err != nil {
		t.Fatalf("RunPainter error: %v", err)
	}
	if dataURL != "data:image/png;base64,ZmFrZS1pbWFnZQ==" {
		t.Errorf("dataURL = %q", dataURL)
	}
	if painter.prompt != "1girl, lighthouse, fog" {
		t.Errorf("image prompt = %q, want the translated tag prompt without system-reminder wrapping", painter.prompt)
	}
	wantOpts := llm.ImageOptions{Aspect: llm.ImageAspectPortrait, Diffusion: true, NegativePrompt: allAgesNegativeTags + ", " + diffusionBaseNegativePrompt}
	if painter.opts != wantOpts {
		t.Errorf("opts = %+v, want %+v", painter.opts, wantOpts)
	}
	if len(writer.chatMessages) != 1 || writer.chatMessages[0][1].Content != "### Scene\nA foggy lighthouse." {
		t.Errorf("translator must receive the Director prompt verbatim, got %+v", writer.chatMessages)
	}
}

func TestRunPainter_DiffusionModeFailsWithoutWriter(t *testing.T) {
	const sessionID = 9102
	painter := &recordingImageProvider{}
	storePainterHandles(t, sessionID, true, agentHandle{enabled: false}, painter)

	_, err := RunPainter(context.Background(), GameContext{Session: models.GameSession{ID: sessionID}}, ImagePromptRequest{Prompt: "a lighthouse"})
	if err == nil {
		t.Fatal("want error when no Writer is available to translate")
	}
	if painter.calls != 0 {
		t.Errorf("GenerateImage calls = %d, want 0 when translation cannot run", painter.calls)
	}
}

func TestRunPainter_DiffusionModeOffKeepsSingleStage(t *testing.T) {
	const sessionID = 9103
	writer := &chatFakeProvider{}
	painter := &recordingImageProvider{}
	storePainterHandles(t, sessionID, false, newDiffusionTranslatorHandle(writer), painter)

	if _, err := RunPainter(context.Background(), GameContext{Session: models.GameSession{ID: sessionID}}, ImagePromptRequest{Prompt: "a lighthouse"}); err != nil {
		t.Fatalf("RunPainter error: %v", err)
	}
	if len(writer.chatKeys) != 0 {
		t.Errorf("Writer must not be called when diffusion mode is off, got %v", writer.chatKeys)
	}
	if !strings.HasPrefix(painter.prompt, "<system-reminder>") || painter.opts.Diffusion {
		t.Errorf("legacy path changed: prompt=%q opts=%+v", painter.prompt, painter.opts)
	}
}
