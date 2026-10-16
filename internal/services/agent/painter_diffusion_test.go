// NOTE: 覆盖扩散模型提示词翻译阶段：LLM 输出解析（含代码围栏/thinking 头/空 prompt）、
// 解析失败重试、全年龄会话必须强制追加负面标签、以及翻译请求的消息形状。禁止真实网络与真实 LLM。
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func TestParseDiffusionPrompt(t *testing.T) {
	tests := []struct {
		name         string
		resp         string
		wantPrompt   string
		wantNegative string
		wantErr      bool
	}{
		{"标准 JSON", `{"prompt":"1girl, solo","negative_prompt":"lowres"}`, "1girl, solo", "lowres", false},
		{"空白被压缩", "{\"prompt\":\" 1girl,\\n  solo \",\"negative_prompt\":\"  \"}", "1girl, solo", "", false},
		{"thinking 头被清理", "Thinking...\n> reasoning\n{\"prompt\":\"no humans\",\"negative_prompt\":\"text\"}", "no humans", "text", false},
		{"缺少 negative 视为空", `{"prompt":"no humans"}`, "no humans", "", false},
		{"prompt 为空报错", `{"prompt":"  ","negative_prompt":"text"}`, "", "", true},
		{"非 JSON 报错", `1girl, solo`, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDiffusionPrompt(tt.resp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Prompt != tt.wantPrompt || got.NegativePrompt != tt.wantNegative {
				t.Errorf("got %+v, want prompt=%q negative=%q", got, tt.wantPrompt, tt.wantNegative)
			}
		})
	}
}

func newDiffusionTranslatorHandle(prov llm.Provider) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleWriter, IsActive: true},
		enabled:  true,
	}
}

func TestTranslateToDiffusionPrompt_AllAgesAppendsNegativeTags(t *testing.T) {
	prov := &sequentialFakeProvider{jsonResponses: []string{`{"prompt":"1girl, library","negative_prompt":"text"}`}}

	got, err := translateToDiffusionPrompt(context.Background(), newDiffusionTranslatorHandle(prov), "7", "### Scene\nA library.", false)
	if err != nil {
		t.Fatalf("translate error: %v", err)
	}
	if got.Prompt != "1girl, library" {
		t.Errorf("prompt = %q", got.Prompt)
	}
	if got.NegativePrompt != allAgesNegativeTags+", text" {
		t.Errorf("negative = %q, want all-ages tags prepended", got.NegativePrompt)
	}
}

func TestTranslateToDiffusionPrompt_AllAgesWithEmptyNegative(t *testing.T) {
	prov := &sequentialFakeProvider{jsonResponses: []string{`{"prompt":"no humans","negative_prompt":""}`}}

	got, err := translateToDiffusionPrompt(context.Background(), newDiffusionTranslatorHandle(prov), "7", "scene", false)
	if err != nil {
		t.Fatalf("translate error: %v", err)
	}
	if got.NegativePrompt != allAgesNegativeTags {
		t.Errorf("negative = %q, want %q", got.NegativePrompt, allAgesNegativeTags)
	}
}

func TestTranslateToDiffusionPrompt_NSFWKeepsNegativeUntouched(t *testing.T) {
	prov := &sequentialFakeProvider{jsonResponses: []string{`{"prompt":"1girl","negative_prompt":"text"}`}}

	got, err := translateToDiffusionPrompt(context.Background(), newDiffusionTranslatorHandle(prov), "7", "scene", true)
	if err != nil {
		t.Fatalf("translate error: %v", err)
	}
	if got.NegativePrompt != "text" {
		t.Errorf("negative = %q, want unchanged when NSFW is enabled", got.NegativePrompt)
	}
}

func TestTranslateToDiffusionPrompt_RetriesInvalidJSON(t *testing.T) {
	prov := &sequentialFakeProvider{jsonResponses: []string{`not json`, `{"prompt":"","negative_prompt":""}`, `{"prompt":"1boy","negative_prompt":""}`}}

	got, err := translateToDiffusionPrompt(context.Background(), newDiffusionTranslatorHandle(prov), "7", "scene", true)
	if err != nil {
		t.Fatalf("translate error: %v", err)
	}
	if got.Prompt != "1boy" {
		t.Errorf("prompt = %q, want 1boy from third attempt", got.Prompt)
	}
	if len(prov.recordedKeys) != 3 {
		t.Errorf("JsonChat calls = %d, want 3", len(prov.recordedKeys))
	}
}

func TestTranslateToDiffusionPrompt_FailsAfterAttempts(t *testing.T) {
	prov := &sequentialFakeProvider{jsonResponses: []string{`x`, `y`, `z`, `{"prompt":"late"}`}}

	_, err := translateToDiffusionPrompt(context.Background(), newDiffusionTranslatorHandle(prov), "7", "scene", true)
	if err == nil {
		t.Fatal("want error after exhausting attempts")
	}
	if len(prov.recordedKeys) != diffusionTranslateAttempts {
		t.Errorf("JsonChat calls = %d, want %d", len(prov.recordedKeys), diffusionTranslateAttempts)
	}
}

func TestTranslateToDiffusionPrompt_MessageShape(t *testing.T) {
	prov := &sequentialFakeProvider{jsonResponses: []string{`{"prompt":"1girl","negative_prompt":""}`}}

	if _, err := translateToDiffusionPrompt(context.Background(), newDiffusionTranslatorHandle(prov), "42", "### Scene\nA lighthouse.", false); err != nil {
		t.Fatalf("translate error: %v", err)
	}
	if len(prov.recordedKeys) != 1 || prov.recordedKeys[0] != "42:painter" {
		t.Errorf("cacheKey = %v, want [42:painter]", prov.recordedKeys)
	}
	msgs := prov.recordedMessages[0]
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

type failingJSONProvider struct{ sequentialFakeProvider }

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
	writer := &sequentialFakeProvider{jsonResponses: []string{`{"prompt":"1girl, lighthouse, fog","negative_prompt":"text"}`}}
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
	wantOpts := llm.ImageOptions{Aspect: llm.ImageAspectPortrait, Diffusion: true, NegativePrompt: allAgesNegativeTags + ", text"}
	if painter.opts != wantOpts {
		t.Errorf("opts = %+v, want %+v", painter.opts, wantOpts)
	}
	if len(writer.recordedMessages) != 1 || writer.recordedMessages[0][1].Content != "### Scene\nA foggy lighthouse." {
		t.Errorf("translator must receive the Director prompt verbatim, got %+v", writer.recordedMessages)
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
	writer := &sequentialFakeProvider{}
	painter := &recordingImageProvider{}
	storePainterHandles(t, sessionID, false, newDiffusionTranslatorHandle(writer), painter)

	if _, err := RunPainter(context.Background(), GameContext{Session: models.GameSession{ID: sessionID}}, ImagePromptRequest{Prompt: "a lighthouse"}); err != nil {
		t.Fatalf("RunPainter error: %v", err)
	}
	if len(writer.recordedKeys) != 0 {
		t.Errorf("Writer must not be called when diffusion mode is off, got %v", writer.recordedKeys)
	}
	if !strings.HasPrefix(painter.prompt, "<system-reminder>") || painter.opts.Diffusion {
		t.Errorf("legacy path changed: prompt=%q opts=%+v", painter.prompt, painter.opts)
	}
}

func (p *failingJSONProvider) JsonChat(context.Context, string, []llm.ChatMessage) (string, error) {
	return "", errors.New("upstream down")
}

func TestTranslateToDiffusionPrompt_ProviderErrorNotRetried(t *testing.T) {
	_, err := translateToDiffusionPrompt(context.Background(), newDiffusionTranslatorHandle(&failingJSONProvider{}), "7", "scene", true)
	if err == nil || !strings.Contains(err.Error(), "upstream down") {
		t.Fatalf("err = %v, want upstream error propagated", err)
	}
}
