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
	aspect := normalizeImageAspect(call.Aspect)

	handle, ok := pickPainterHandle(actx.Handles, call.NSFW)
	if !ok {
		if call.NSFW {
			debugf("tool", "session=%d generate_image rejected: painter_nsfw disabled prompt_len=%d prompt=%q", actx.Sid, len([]rune(imagePrompt)), truncateRunes(imagePrompt, 200))
			return []ToolResult{{Action: ToolGenerateImage, Result: "image generation rejected: NSFW image generation is not enabled"}}
		}
		debugf("tool", "session=%d generate_image unavailable: painter disabled prompt_len=%d prompt=%q", actx.Sid, len([]rune(imagePrompt)), truncateRunes(imagePrompt, 200))
		return []ToolResult{{Action: ToolGenerateImage, Result: "image generation unavailable"}}
	}
	if _, ok := handle.provider.(llm.ImageGenerator); !ok {
		debugf("tool", "session=%d generate_image unavailable: provider lacks image generation prompt_len=%d prompt=%q", actx.Sid, len([]rune(imagePrompt)), truncateRunes(imagePrompt, 200))
		return []ToolResult{{Action: ToolGenerateImage, Result: "image generation unavailable"}}
	}
	req := ImagePromptRequest{Prompt: imagePrompt, Aspect: aspect, NSFW: call.NSFW}
	*actx.Outbox = append(*actx.Outbox, Output{Kind: OutputImage, Image: &req, NSFW: call.NSFW})
	debugf("tool", "session=%d generate_image queued prompt_len=%d aspect=%s nsfw=%v prompt=%q", actx.Sid, len([]rune(imagePrompt)), aspect, call.NSFW, imagePrompt)
	return []ToolResult{{Action: ToolGenerateImage, Result: "image generation queued"}}
}

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

// normalizeImageAspect 校验/归一化 Director 传入的 aspect 参数;非法或未识别值一律
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

type describeCharactersAction struct{}

func (describeCharactersAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	names := sanitizeImageCharacters(call.Characters)
	if len(names) == 0 {
		return []ToolResult{{Action: ToolDescribeCharacters, Result: "没有提供角色名。"}}
	}
	if actx.GCtx == nil {
		return []ToolResult{{Action: ToolDescribeCharacters, Result: fallbackMissingCharacterVisualDescription(names)}}
	}
	cards := collectImagePromptCharacterCards(names, actx.GCtx.Session.Players)
	if len(cards) == 0 {
		return []ToolResult{{Action: ToolDescribeCharacters, Result: fallbackMissingCharacterVisualDescription(names)}}
	}

	description := ""
	writerHandle := actx.Handles[models.AgentRoleWriter]
	if writerHandle.isEnabled() {
		ctx := actx.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		ctx = withWriterGameSessionID(ctx, *actx.GCtx)
		var err error
		description, err = writeImagePromptCharacterVisualDescription(ctx, writerHandle, "", fmt.Sprintf("%v", actx.GCtx.Session.ID), cards)
		if err != nil {
			debugf("tool", "session=%d describe_characters writer fallback matched_character_count=%d err=%v", actx.Sid, len(cards), err)
		}
	} else {
		debugf("tool", "session=%d describe_characters writer fallback matched_character_count=%d err=%v", actx.Sid, len(cards), fmt.Errorf("writer agent unavailable"))
	}
	description = strings.TrimSpace(description)
	if description == "" {
		description = fallbackCharacterVisualDescriptions(cards)
	}
	debugf("tool", "session=%d describe_characters result_len=%d requested_count=%d matched_character_count=%d", actx.Sid, len([]rune(description)), len(names), len(cards))
	return []ToolResult{{Action: ToolDescribeCharacters, Result: description}}
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
	generator, ok := handle.provider.(llm.ImageGenerator)
	if !ok {
		debugf("Painter", "session=%d unavailable: provider lacks image generation elapsed=%.0fms", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000)
		return "", fmt.Errorf("当前 %s provider 不支持图片生成", painterRoleFor(request.NSFW))
	}
	aspect := normalizeImageAspect(request.Aspect)
	// NOTE: 被标记 NSFW 的请求本身就是成人向画面,分级政策和负面标签必须按请求而非仅按房间开关渲染。
	allowNSFW := request.NSFW || gctx.Session.EnableNSFW
	debugf("Painter", "session=%d start prompt_len=%d aspect=%s nsfw=%v prompt=%q", gctx.Session.ID, len([]rune(prompt)), aspect, request.NSFW, prompt)
	opts := llm.ImageOptions{Aspect: llm.ImageAspect(aspect)}
	if handle.config != nil && handle.config.DiffusionMode {
		translator := handles[models.AgentRoleDiffusionTranslator]
		if !translator.isEnabled() {
			return "", fmt.Errorf("扩散模型模式需要启用 %s agent 翻译提示词", models.AgentRoleDiffusionTranslator)
		}
		tagPrompt, err := translateToDiffusionPrompt(withWriterGameSessionID(ctx, gctx), translator, fmt.Sprintf("%v", gctx.Session.ID), prompt, allowNSFW)
		if err != nil {
			debugf("Painter", "session=%d diffusion translate error elapsed=%.0fms err=%v", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000, err)
			return "", err
		}
		prompt = tagPrompt
		opts.Diffusion = true
		opts.NegativePrompt = diffusionNegativePromptFor(allowNSFW)
		debugf("Painter", "session=%d diffusion prompt=%q negative=%q", gctx.Session.ID, prompt, opts.NegativePrompt)
	} else {
		prompt = fmt.Sprintf("%v\n%v", prompt, renderNSFW(imageSysPrompt, allowNSFW))
	}
	base64Data, mimeType, err := generator.GenerateImage(ctx, prompt, opts)
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

func sanitizeImageCharacters(names []string) []string {
	result := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, name)
	}
	return result
}

func collectImagePromptCharacterCards(names []string, players []models.SessionPlayer) []models.CharacterCard {
	names = sanitizeImageCharacters(names)
	if len(names) == 0 || len(players) == 0 {
		return nil
	}
	cards := make([]models.CharacterCard, 0, len(names))
	usedCards := map[uint]bool{}
	for _, name := range names {
		card, ok := findImagePromptCharacterCard(name, players, usedCards)
		if !ok {
			continue
		}
		if card.ID != 0 {
			usedCards[card.ID] = true
		}
		cards = append(cards, card)
	}
	return cards
}

func findImagePromptCharacterCard(name string, players []models.SessionPlayer, usedCards map[uint]bool) (models.CharacterCard, bool) {
	name = strings.TrimSpace(name)
	for _, p := range players {
		card := p.CharacterCard
		if card.ID != 0 && usedCards[card.ID] {
			continue
		}
		if strings.TrimSpace(card.Name) == name {
			return card, true
		}
	}
	lowerName := strings.ToLower(name)
	for _, p := range players {
		card := p.CharacterCard
		if card.ID != 0 && usedCards[card.ID] {
			continue
		}
		if strings.ToLower(strings.TrimSpace(card.Name)) == lowerName {
			return card, true
		}
	}
	return models.CharacterCard{}, false
}

const imagePromptCharacterVisualSystemPrompt = `你是 Writer 代理，负责为配图整理角色外貌素材。

只输出简洁的中文外貌描写，一段话或几条短要点即可。
只能使用提供的角色卡数据，不要编造服饰、装备、秘密、伤势、能力、性格、物品属性、神话名称、规则、背景设定、头衔、隐藏铭文或括号里的元数据。
聚焦可见的身体、面部、发型、服饰、姿态等具体视觉特征；物品栏不是随身物品清单，只提及明确穿戴或手持的可见饰品，凡不确定的物品一律省略。

只输出纯外貌描写，不要附带其他属性。
`

func writeImagePromptCharacterVisualDescription(ctx context.Context, h agentHandle, scenePrompt string, sessionID string, cards []models.CharacterCard) (string, error) {
	if !h.isEnabled() {
		return "", fmt.Errorf("writer agent unavailable")
	}
	msgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(imagePromptCharacterVisualSystemPrompt)},
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
	sb.WriteString("任务：把下列玩家角色卡数据整理成简洁的中文外貌描写，供配图使用。Director 会把这段文字直接写进后续的 image_prompt。外貌以 Appearance 为主要来源；物品栏只作为可见物品的参考，不是随身清单。省略物品属性、神话/背景设定名称、规则、头衔、隐藏铭文、括号里的元数据，以及一切不明确可见、穿戴或手持的内容。\n\n")
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
		sb.WriteString("\n物品栏：\n")
		wroteItem := false
		for _, item := range card.Inventory.Data {
			item = compactPromptText(item)
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

func fallbackCharacterVisualDescriptions(cards []models.CharacterCard) string {
	lines := make([]string, 0, len(cards))
	for _, card := range cards {
		name := compactPromptText(card.Name)
		if name == "" {
			name = "未命名调查员"
		}
		appearance := compactPromptText(card.Appearance)
		if appearance == "" {
			lines = append(lines, fmt.Sprintf("%s：角色卡没有记录外貌细节。", name))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s：角色卡记录的外貌为：%s", name, appearance))
	}
	return strings.Join(lines, "\n")
}

func fallbackMissingCharacterVisualDescription(names []string) string {
	return "没有找到匹配的调查员角色卡：" + strings.Join(names, "、") + "。"
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
