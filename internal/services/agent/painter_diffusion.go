package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

const diffusionPromptSystemPrompt = `You are the prompt translator for a diffusion image model (NovelAI style, trained on Danbooru tags).
Chinese intent: 把导演给出的自然语言画面描述翻译成扩散模型可用的标签式提示词和负面提示词。

The user message is an English scene description, usually in Markdown sections (Scene/Subject/Environment/Lighting/Camera/Negative prompts).
Output exactly one JSON object and nothing else: {"prompt":"...","negative_prompt":"..."}

Rules for "prompt":
- Comma-separated English tags in Danbooru style, using spaces instead of underscores (e.g. "long hair", not "long_hair").
- Order: subject count first (1girl, 1boy, 2girls, multiple girls; "no humans" for scenes without people), then appearance (hair, eyes, clothing, expression, pose), then setting and props, then lighting and atmosphere, then camera and composition (e.g. from above, wide shot, close-up, depth of field).
- Use only what the description states. Do not invent characters, clothing or items.
- Keep the anime illustration style. Do not add photorealistic tags.
- Do not add quality tags such as masterpiece or best quality; the image service already adds them.
- Do not use weight syntax, brackets or full sentences. Describe what is visible instead of naming Mythos entities or rule terms.

Rules for "negative_prompt":
- Comma-separated tags for things to avoid. Turn the "Negative prompts" section into tags (e.g. "No readable text" becomes "text, watermark"). Use an empty string when there is nothing to avoid.

{{NSFW_IMAGE_POLICY}}
`

const diffusionTranslateAttempts = 3

// NOTE: 会话关闭 NSFW 时强制追加到负面提示词，扩散模型对自然语言分级约束不敏感，必须落到标签层面。
const allAgesNegativeTags = "nsfw, nude, sexually suggestive"

type diffusionPrompt struct {
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt"`
}

func parseDiffusionPrompt(resp string) (diffusionPrompt, error) {
	resp = strings.TrimSpace(stripThinkingBlock(resp))
	var out diffusionPrompt
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return diffusionPrompt{}, fmt.Errorf("diffusion prompt is not valid JSON: %w", err)
	}
	out.Prompt = compactPromptText(out.Prompt)
	out.NegativePrompt = compactPromptText(out.NegativePrompt)
	if out.Prompt == "" {
		return diffusionPrompt{}, errors.New("diffusion prompt is empty")
	}
	return out, nil
}

func withAllAgesNegative(negative string) string {
	if negative == "" {
		return allAgesNegativeTags
	}
	return allAgesNegativeTags + ", " + negative
}

// translateToDiffusionPrompt 用翻译 Agent 把 Director 的自然语言画面描述翻译成扩散模型的标签式提示词。
func translateToDiffusionPrompt(ctx context.Context, translator agentHandle, sessionID string, scenePrompt string, enableNSFW bool) (diffusionPrompt, error) {
	msgs := []llm.ChatMessage{
		{Role: "system", Content: translator.systemPrompt(renderNSFW(diffusionPromptSystemPrompt, enableNSFW))},
		{Role: "user", Content: scenePrompt},
	}
	var lastErr error
	for attempt := 1; attempt <= diffusionTranslateAttempts; attempt++ {
		resp, err := translator.provider.JsonChat(ctx, sessionID+":painter", msgs)
		if err != nil {
			return diffusionPrompt{}, err
		}
		out, err := parseDiffusionPrompt(resp)
		if err != nil {
			debugf("Painter", "diffusion translate parse failed attempt=%d err=%v", attempt, err)
			lastErr = err
			continue
		}
		if !enableNSFW {
			out.NegativePrompt = withAllAgesNegative(out.NegativePrompt)
		}
		return out, nil
	}
	return diffusionPrompt{}, fmt.Errorf("diffusion prompt translation failed after %d attempts: %w", diffusionTranslateAttempts, lastErr)
}
