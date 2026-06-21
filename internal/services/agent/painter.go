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

var animeImageStyleRequirements = []string{
	"anime style",
	"2D illustration",
	"Japanese animation aesthetic",
	"clean line art",
	"expressive lighting",
	"atmospheric horror",
	"high detail",
	"avoid photorealism",
	"realistic photography",
	"3D render",
}

const animeImageStylePrompt = "anime style, 2D illustration, Japanese animation aesthetic, clean line art, expressive lighting, atmospheric horror, high detail, avoid photorealism, realistic photography, 3D render"

// NOTE: Painter统一追加二次元画风,防止普通场景提示词绕过图片风格设定。
func animeStyledImagePrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	lowerPrompt := strings.ToLower(prompt)
	for _, requirement := range animeImageStyleRequirements {
		if !strings.Contains(lowerPrompt, strings.ToLower(requirement)) {
			return prompt + ", " + animeImageStylePrompt
		}
	}
	return prompt
}

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
	characters := sanitizeImageCharacters(call.Characters)
	if actx.PendingImages != nil {
		*actx.PendingImages = append(*actx.PendingImages, ImagePromptRequest{Prompt: imagePrompt, Characters: characters})
	}
	debugf("tool", "session=%d generate_image queued prompt_len=%d character_count=%d characters=%q prompt=%q", actx.Sid, len([]rune(imagePrompt)), len(characters), truncateRunes(strings.Join(characters, ","), 120), imagePrompt)
	return []ToolResult{{Action: ToolGenerateImage, Result: "image generation queued"}}
}

// NOTE: 根据排队请求生成图片data URL;handler负责把结果持久化到助手消息。
func RunPainter(ctx context.Context, gctx GameContext, request ImagePromptRequest) (string, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return "", fmt.Errorf("image prompt is empty")
	}
	characters := sanitizeImageCharacters(request.Characters)
	start := time.Now()
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
	enrichedPrompt, matchedCharacters := enrichImagePromptWithWriterCharacterDescriptions(ctx, gctx.Session.ID, prompt, characters, gctx.Session.Players, handles[models.AgentRoleWriter])
	styledPrompt := animeStyledImagePrompt(enrichedPrompt)
	debugf("Painter", "session=%d start prompt_len=%d enriched_prompt_len=%d styled_prompt_len=%d character_count=%d matched_character_count=%d prompt=%q", gctx.Session.ID, len([]rune(prompt)), len([]rune(enrichedPrompt)), len([]rune(styledPrompt)), len(characters), matchedCharacters, truncateRunes(prompt, 200))
	base64Data, mimeType, err := generator.GenerateImage(ctx, styledPrompt, "1024x1024")
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

// NOTE: Painter只接收Writer整理后的视觉描述,避免把原始角色卡明细直接交给图片模型。
func enrichImagePromptWithWriterCharacterDescriptions(ctx context.Context, sessionID uint, prompt string, names []string, players []models.SessionPlayer, writerHandle agentHandle) (string, int) {
	cards := collectImagePromptCharacterCards(names, players)
	if len(cards) == 0 {
		return prompt, 0
	}
	if !writerHandle.isEnabled() {
		debugf("Painter", "session=%d character visual description skipped matched_character_count=%d err=%v", sessionID, len(cards), fmt.Errorf("writer agent unavailable"))
		return prompt, len(cards)
	}
	description, err := writeImagePromptCharacterVisualDescription(ctx, writerHandle, cards)
	if err != nil {
		debugf("Painter", "session=%d character visual description skipped matched_character_count=%d err=%v", sessionID, len(cards), err)
		return prompt, len(cards)
	}
	return strings.TrimSpace(prompt) + "\n\nCharacter visual descriptions written by Writer from player cards (use these details):\n" + description, len(cards)
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

const imagePromptCharacterVisualSystemPrompt = `You are the Writer agent preparing character appearance notes for an anime image model.

Output only concise English visual descriptions, as one paragraph or a short bullet list.
Use only the provided card data. Do not invent clothing, equipment, secrets, injuries, powers, personality traits, item properties, mythos names, rules, lore, titles, hidden inscriptions, or parenthetical metadata.
Focus on visible body/face/hair/clothing/posture and concrete visual cues; inventory is not a carried-items list, so mention only clearly worn or currently held visible accessories and omit all uncertain items.

You only generate a pure appearance description, without any other attributes.
`

func writeImagePromptCharacterVisualDescription(ctx context.Context, h agentHandle, cards []models.CharacterCard) (string, error) {
	if !h.isEnabled() {
		return "", fmt.Errorf("writer agent unavailable")
	}
	msgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(imagePromptCharacterVisualSystemPrompt)},
		{Role: "user", Content: buildImagePromptCharacterVisualUserPrompt(cards)},
	}
	resp, err := h.provider.Chat(ctx, msgs)
	if err != nil {
		return "", err
	}
	resp = strings.TrimSpace(resp)
	if resp == "" {
		return "", fmt.Errorf("writer returned empty visual description")
	}
	debugf("writeImagePromptCharacterVisualDescription", "session=%d got character visual description from Writer %v", ctx.Value("session_id"), resp)
	return resp, nil
}

func buildImagePromptCharacterVisualUserPrompt(cards []models.CharacterCard) string {
	var sb strings.Builder
	sb.WriteString("Task: Convert the following player character card data into concise English visual descriptions for image generation. Use appearance as the main source; inventory is optional visible-item hints only, not a carried list. Omit item properties, mythos/lore names, rules, titles, hidden inscriptions, parenthetical metadata, and anything not clearly visible/worn/held.\n\n")
	for i, card := range cards {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("Character card:\n")
		sb.WriteString("Name: ")
		sb.WriteString(compactPromptText(card.Name))
		sb.WriteString("\nAppearance: ")
		appearance := compactPromptText(card.Appearance)
		if appearance == "" {
			appearance = "(not provided)"
		}
		sb.WriteString(appearance)
		sb.WriteString("\nInventory:\n")
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
			sb.WriteString("- (none)\n")
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
