package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// pickPainterHandle 选择本次配图使用的 Painter:nsfw 请求只路由到 painter_nsfw,
// 它未配置或未启用时返回 false,不回落默认 Painter,避免成人向画面被送进受限的图片模型。
func pickPainterHandle(handles map[models.AgentRole]agentHandle, nsfw bool) (agentHandle, bool) {
	h, ok := handles[painterRoleFor(nsfw)]
	return h, ok && h.isEnabled()
}

func painterRoleFor(nsfw bool) models.AgentRole {
	if nsfw {
		return models.AgentRolePainterNSFW
	}
	return models.AgentRolePainter
}

// normalizeImageAspect 校验/归一化 LLM 传入的 aspect 参数;非法或未识别值一律
// 回落方图,因为这是 LLM 不可信输入进入系统的边界,不能把乱填值透传给 Provider。
func normalizeImageAspect(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(llm.ImageAspectLandscape):
		return string(llm.ImageAspectLandscape)
	case string(llm.ImageAspectPortrait):
		return string(llm.ImageAspectPortrait)
	default:
		return string(llm.ImageAspectSquare)
	}
}

const imageSysPrompt = `
画面要求：
- 画风：日式漫画。画成连续日式漫画中的单独一格：粗细有变化的墨线，配色克制的赛璐璐平涂，用网点式排线表现阴影，不要画成写实风格。
- 单格：只画一个瞬间、一个画面。不要多格拼版，不要画面边框，不要对话气泡、旁白框和拟声词，不要出现任何可读文字。
- 一致性：读者会连续翻阅很多格。人物的脸、发型、服装和标志性配饰要严格按描述保持不变，反复出现的场景要能认出来，让同一个人、同一个地方在不同格里看起来一致。
- 构图：像日式漫画分镜一样取景，按镜头提示选择景别和角度，把关键动作或情绪放在视觉焦点，四周留出呼吸空间。
- 光影：用强烈的明暗对比表现氛围和纵深。
- 背景：按画面描述补充合适的背景细节，线条风格与人物一致。
- 表情与姿态：表情和姿态要清晰、有表现力，一眼就能看出人物此刻的情绪。
{{NSFW_IMAGE_POLICY}}
`

// NOTE: 根据排队请求生成图片data URL;handler负责把结果持久化到助手消息。
func RunPainter(ctx context.Context, gctx GameContext, request ImagePromptRequest) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return "", fmt.Errorf("image prompt is empty")
	}
	start := time.Now()
	handles, err := getCachedAgents(gctx.Session.ID)
	if err != nil {
		debugf("Painter", "session=%d load agents error elapsed=%.0fms err=%v", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000, err)
		return "", err
	}
	handle, ok := pickPainterHandle(handles, request.NSFW)
	if !ok {
		debugf("Painter", "session=%d unavailable: painter disabled nsfw=%v elapsed=%.0fms", gctx.Session.ID, request.NSFW, float64(time.Since(start).Microseconds())/1000)
		return "", fmt.Errorf("%s agent 未配置或未启用", painterRoleFor(request.NSFW))
	}
	aspect := normalizeImageAspect(request.Aspect)
	// NOTE: request.NSFW 由调用方按房间开关过滤,这里只按请求渲染分级,不能再并房间开关,否则房间开启时普通配图也会被画成 NSFW。
	debugf("Painter", "session=%d start prompt_len=%d aspect=%s nsfw=%v prompt=%q", gctx.Session.ID, len([]rune(prompt)), aspect, request.NSFW, prompt)
	dataURL, err := paintImage(withWriterGameSessionID(ctx, gctx), handle, handles[models.AgentRoleDiffusionTranslator],
		fmt.Sprintf("%v", gctx.Session.ID), prompt, imageSysPrompt, llm.ImageAspect(aspect), request.NSFW)
	if err != nil {
		debugf("Painter", "session=%d error elapsed=%.0fms err=%v", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000, err)
		return "", err
	}
	debugf("Painter", "session=%d success elapsed=%.0fms data_url_len=%d", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000, len(dataURL))
	return dataURL, nil
}

// paintImage 用给定的 Painter(扩散模式下再加 translator)出图并返回 data URL。
// sysPrompt 是非扩散模式下追加在画面描述后的画风要求,扩散模式下由 translator 的提示词负责画风。
func paintImage(ctx context.Context, painter, translator agentHandle, cacheSession, prompt, sysPrompt string, aspect llm.ImageAspect, nsfw bool) (string, error) {
	generator, ok := painter.provider.(llm.ImageGenerator)
	if !ok {
		role := painterRoleFor(nsfw)
		if painter.config != nil {
			role = painter.config.Role
		}
		return "", fmt.Errorf("当前 %s provider 不支持图片生成", role)
	}
	opts := llm.ImageOptions{Aspect: aspect}
	if painter.config != nil && painter.config.DiffusionMode {
		if !translator.isEnabled() {
			return "", fmt.Errorf("扩散模型模式需要启用 %s agent 翻译提示词", models.AgentRoleDiffusionTranslator)
		}
		tagPrompt, err := translateToDiffusionPrompt(ctx, translator, cacheSession, prompt, nsfw)
		if err != nil {
			return "", err
		}
		prompt = tagPrompt
		opts.Diffusion = true
		opts.NegativePrompt = diffusionNegativePromptFor(nsfw)
		debugf("Painter", "session=%s diffusion prompt=%q negative=%q", cacheSession, prompt, opts.NegativePrompt)
	} else {
		prompt = fmt.Sprintf("%v\n%v", prompt, renderNSFW(sysPrompt, nsfw))
	}
	base64Data, mimeType, err := generator.GenerateImage(ctx, prompt, opts)
	if err != nil {
		return "", err
	}
	dataURL, ok := buildImageDataURL(base64Data, mimeType)
	if !ok {
		return "", fmt.Errorf("empty image data")
	}
	return dataURL, nil
}

const imagePromptCharacterVisualSystemPrompt = `你是 Writer 代理，负责为配图整理角色外貌素材。

只输出中文外貌描写，把角色卡里能看到的外貌细节写全，不要遗漏，一段话或几条要点即可。
只能使用提供的角色卡数据，不要编造服饰、装备、秘密、伤势、能力、性格、物品属性、神话名称、规则、背景设定、头衔、隐藏铭文或括号里的元数据。
聚焦可见的身体、面部、发型、服饰、姿态等具体视觉特征；装备栏列出的是角色当前穿戴或手持的物品，描写时要把它们写成可见的服饰、配饰或手持物，不在装备栏里的物品一律不提。

只输出纯外貌描写，不要附带其他属性。
{{NSFW_APPEARANCE_POLICY}}
`

func writeImagePromptCharacterVisualDescription(ctx context.Context, h agentHandle, scenePrompt string, sessionID string, cards []models.CharacterCard, nsfw bool) (string, error) {
	if !h.isEnabled() {
		return "", fmt.Errorf("writer agent unavailable")
	}
	msgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(renderNSFW(imagePromptCharacterVisualSystemPrompt, nsfw))},
		{Role: "user", Content: buildImagePromptCharacterVisualUserPrompt(scenePrompt, cards)},
	}
	// NOTE: 独立缓存key(sessionID:painter),不再借用Writer的"sessionID:writer",避免
	// 两个agent的prompt cache互相污染彼此的前缀。
	resp, err := h.provider.Chat(ctx, sessionID+":painter", msgs)
	if err != nil {
		return "", err
	}
	// NOTE: Writer 模型可能输出 thinking 头,先清理再 TrimSpace,避免污染下游 image prompt。
	resp = strings.TrimSpace(resp)
	if resp == "" {
		return "", fmt.Errorf("writer returned empty visual description")
	}
	debugf("writeImagePromptCharacterVisualDescription", "session=%d got character visual description from Writer %v", ctx.Value("session_id"), resp)
	return resp, nil
}

func buildImagePromptCharacterVisualUserPrompt(scenePrompt string, cards []models.CharacterCard) string {
	var sb strings.Builder
	sb.WriteString("任务：把下列玩家角色卡数据整理成完整的中文外貌描写，供配图使用，角色卡里可见的外貌细节不要遗漏。这段文字会被直接写进后续的绘图提示词。外貌以 Appearance 为主要来源；装备栏是角色当前穿戴或手持的物品，可以写进外貌描写。省略物品属性、神话/背景设定名称、规则、头衔、隐藏铭文、括号里的元数据，以及一切不明确可见、穿戴或手持的内容。\n\n")
	if scene := compactPromptText(scenePrompt); scene != "" {
		sb.WriteString("画面描述（仅用于挑选相关的外貌特征）：")
		sb.WriteString(scene)
		sb.WriteString("\n\n")
	}
	for i, card := range cards {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("角色卡：\n")
		sb.WriteString("姓名：")
		sb.WriteString(compactPromptText(card.Name))
		sb.WriteString("\n外貌：")
		appearance := compactPromptText(card.Appearance)
		if appearance == "" {
			appearance = "（未提供）"
		}
		sb.WriteString(appearance)
		sb.WriteString("\n装备栏：\n")
		wroteItem := false
		for _, it := range models.EquippedItems(card.Inventory.Data) {
			item := compactPromptText(it.Label())
			if item == "" {
				continue
			}
			wroteItem = true
			sb.WriteString("- ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
		if !wroteItem {
			sb.WriteString("- （无）\n")
		}
	}
	return sb.String()
}

func compactPromptText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
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
