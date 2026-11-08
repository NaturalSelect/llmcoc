package agent

import (
	"context"
	"errors"

	"github.com/llmcoc/server/internal/services/llm"
)

const diffusionPromptSystemPrompt = `你是扩散画图模型(NovelAI 风格，基于 Danbooru 标签训练)的提示词翻译。
你的任务：把导演给出的中文画面描述翻译成扩散模型可用的英文标签式提示词。

用户消息是一段简短的中文画面描述。
只输出一行用英文逗号分隔的标签，不要输出任何其他内容：不要 JSON，不要标题，不要解释，不要代码块。

规则：
- 使用 Danbooru 风格的英文标签，单词之间用空格而不是下划线(例如写 "long hair"，不写 "long_hair")。
- 尽量详尽。好的结果通常有 25 到 50 个标签。扩散模型只会画标签里写到的东西，标签太少画面就平淡空泛。先把描述里的每一个视觉细节都翻译出来，再补上场景明显隐含的标签。
- 顺序：先写人数(1girl、1boy、2girls、multiple girls；没有人物的场景写 "no humans")，再写外貌(发色与长度、发型、瞳色、服饰与配饰、表情、姿势、视线、手部位置)，再写场景与道具(地点类型、室内或室外、背景物件、前景物件、天气、时间)，再写光线与氛围(光源、光线方向、阴影、色调、情绪)，最后写镜头与构图(例如 from above、wide shot、close-up、cowboy shot、depth of field、dutch angle)。
- 每个人物分别写清：发型、眼睛、服饰、表情、姿势。每个环境分别写清：地点、关键物件、光线、色调。
- 把复合的描述拆成多个具体标签。例如"昏暗潮湿的地窖"拆成 "cellar, dim lighting, damp, stone wall, dark, shadow"。
- 可以补充直接由描述推出的小细节，例如旧图书馆里阳光中的灰尘。不要凭空添加描述没有暗示的新角色、有名字的物品或剧情元素。
- 保持日式漫画观感：在合适时加入 "comic"、"lineart"、"thick outlines"、"screentone"、"halftone" 等标签，保留动漫风格的人物和赛璐璐平涂。除非描述明确要求黑白，否则不要使用 "monochrome" 或 "greyscale"。不要添加写实类标签。
- 只画单独一格：绝不添加多格拼版、"speech bubble"、"text"、"sound effects"、"english text" 这类标签。
- 一致性很重要，因为这些图会被当作一部连续的日式漫画来读。对反复出现的人物，发色与长度、发型、瞳色、服饰要完全按描述写出，并且每张图里的标签措辞保持一致。
- 景别根据描述里的镜头提示来选(例如 close-up、upper body、cowboy shot、wide shot、from above、from below、from side)。
- 不要添加 masterpiece、best quality 这类质量标签，图片服务已经自动添加。
- 不要使用权重语法、括号或完整句子。神话实体和规则术语不要直接写名字，改为描述画面上能看到的样子。
- 不要重复标签，也不要把互相矛盾的标签放在一起。
- 不要输出负面标签或 "no xxx" 之类的写法，负面标签由系统添加。
{{NSFW_IMAGE_POLICY}}
`

// NOTE: 扩散模型的负面提示词固定不变，不让 LLM 生成：通用的画质/水印类负面标签不随画面变化。
const diffusionBaseNegativePrompt = "lowres, bad anatomy, bad hands, extra digits, fewer digits, cropped, jpeg artifacts, signature, watermark, username, text, blurry, speech bubble, english text, panel border, multiple views, realistic, photorealistic"

// NOTE: 会话关闭 NSFW 时强制追加到负面提示词，扩散模型对自然语言分级约束不敏感，必须落到标签层面。
const allAgesNegativeTags = "nsfw, nude, sexually suggestive"

// diffusionNegativePromptFor 返回本次出图使用的负面提示词；全年龄请求在固定负面标签前追加分级标签。
func diffusionNegativePromptFor(enableNSFW bool) string {
	if enableNSFW {
		return diffusionBaseNegativePrompt
	}
	return allAgesNegativeTags + ", " + diffusionBaseNegativePrompt
}

// translateToDiffusionPrompt 用 diffusion_translator 把 Director 的自然语言画面描述翻译成扩散模型的标签式正面提示词。
func translateToDiffusionPrompt(ctx context.Context, translator agentHandle, sessionID string, scenePrompt string, enableNSFW bool) (string, error) {
	msgs := []llm.ChatMessage{
		{Role: "system", Content: translator.systemPrompt(renderNSFW(diffusionPromptSystemPrompt, enableNSFW))},
		{Role: "user", Content: scenePrompt},
	}
	resp, err := translator.provider.Chat(ctx, translator.cacheKey(sessionID), msgs)
	if err != nil {
		return "", err
	}
	prompt := compactPromptText(resp)
	if prompt == "" {
		return "", errors.New("diffusion prompt is empty")
	}
	return prompt, nil
}
