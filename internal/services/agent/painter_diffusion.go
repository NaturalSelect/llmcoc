package agent

import (
	"context"
	"errors"

	"github.com/llmcoc/server/internal/services/llm"
)

const diffusionPromptSystemPrompt = `You are the prompt translator for a diffusion image model (NovelAI style, trained on Danbooru tags).
Chinese intent: 把导演给出的自然语言画面描述翻译成扩散模型可用的标签式提示词。

The user message is an English scene description, usually in Markdown sections (Scene/Subject/Environment/Lighting/Camera/Negative prompts).
Output exactly one line of comma-separated tags and nothing else: no JSON, no labels, no explanation, no code fences.

Rules:
- Danbooru-style English tags, using spaces instead of underscores (e.g. "long hair", not "long_hair").
- Order: subject count first (1girl, 1boy, 2girls, multiple girls; "no humans" for scenes without people), then appearance (hair, eyes, clothing, expression, pose), then setting and props, then lighting and atmosphere, then camera and composition (e.g. from above, wide shot, close-up, depth of field).
- Use only what the description states. Do not invent characters, clothing or items.
- Keep the anime illustration style. Do not add photorealistic tags.
- Do not add quality tags such as masterpiece or best quality; the image service already adds them.
- Do not use weight syntax, brackets or full sentences. Describe what is visible instead of naming Mythos entities or rule terms.
- Ignore the "Negative prompts" section; negative tags are added by the system.

{{NSFW_IMAGE_POLICY}}
`

// NOTE: 扩散模型的负面提示词固定不变，不让 LLM 生成：通用的画质/水印类负面标签不随画面变化。
const diffusionBaseNegativePrompt = "lowres, bad anatomy, bad hands, extra digits, fewer digits, cropped, jpeg artifacts, signature, watermark, username, text, blurry"

// NOTE: 会话关闭 NSFW 时强制追加到负面提示词，扩散模型对自然语言分级约束不敏感，必须落到标签层面。
const allAgesNegativeTags = "nsfw, nude, sexually suggestive"

// diffusionNegativePromptFor 返回本次出图使用的负面提示词；全年龄请求在固定负面标签前追加分级标签。
func diffusionNegativePromptFor(enableNSFW bool) string {
	if enableNSFW {
		return diffusionBaseNegativePrompt
	}
	return allAgesNegativeTags + ", " + diffusionBaseNegativePrompt
}

// translateToDiffusionPrompt 用 Writer 把 Director 的自然语言画面描述翻译成扩散模型的标签式正面提示词。
func translateToDiffusionPrompt(ctx context.Context, writer agentHandle, sessionID string, scenePrompt string, enableNSFW bool) (string, error) {
	msgs := []llm.ChatMessage{
		{Role: "system", Content: writer.systemPrompt(renderNSFW(diffusionPromptSystemPrompt, enableNSFW))},
		{Role: "user", Content: scenePrompt},
	}
	resp, err := writer.provider.Chat(ctx, sessionID+":painter", msgs)
	if err != nil {
		return "", err
	}
	// NOTE: Writer 模型可能输出 thinking 头，先清理再压缩空白，避免污染下游提示词。
	prompt := compactPromptText(stripThinkingBlock(resp))
	if prompt == "" {
		return "", errors.New("diffusion prompt is empty")
	}
	return prompt, nil
}
