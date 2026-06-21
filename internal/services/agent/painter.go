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

	handle, ok := actx.Handles[models.AgentRolePainter]
	if !ok || !handle.isEnabled() {
		debugf("tool", "session=%d generate_image unavailable: painter disabled prompt_len=%d prompt=%q", actx.Sid, len([]rune(imagePrompt)), truncateRunes(imagePrompt, 200))
		return []ToolResult{{Action: ToolGenerateImage, Result: "image generation unavailable"}}
	}
	if _, ok := handle.provider.(llm.ImageGenerator); !ok {
		debugf("tool", "session=%d generate_image unavailable: provider lacks image generation prompt_len=%d prompt=%q", actx.Sid, len([]rune(imagePrompt)), truncateRunes(imagePrompt, 200))
		return []ToolResult{{Action: ToolGenerateImage, Result: "image generation unavailable"}}
	}
	if actx.PendingImages != nil {
		*actx.PendingImages = append(*actx.PendingImages, ImagePromptRequest{Prompt: imagePrompt})
	}
	debugf("tool", "session=%d generate_image queued prompt_len=%d prompt=%q", actx.Sid, len([]rune(imagePrompt)), imagePrompt)
	return []ToolResult{{Action: ToolGenerateImage, Result: "image generation queued"}}
}

type describeCharactersAction struct{}

func (describeCharactersAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	names := sanitizeImageCharacters(call.Characters)
	if len(names) == 0 {
		return []ToolResult{{Action: ToolDescribeCharacters, Result: "No character names were provided."}}
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
		description, err = writeImagePromptCharacterVisualDescription(ctx, writerHandle, "", cards)
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
	debugf("Painter", "session=%d start prompt_len=%d prompt=%q", gctx.Session.ID, len([]rune(prompt)), truncateRunes(prompt, 200))
	base64Data, mimeType, err := generator.GenerateImage(ctx, prompt, "1024x1024")
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

const imagePromptCharacterVisualSystemPrompt = `You are the Writer agent preparing character appearance notes for an image generation model.
Chinese intent: 将角色卡外貌转换为简短英文视觉外貌描写,不泄露隐藏信息或规则信息。

Output only concise English visual descriptions, as one paragraph or a short bullet list.
Use only the provided card data. Do not invent clothing, equipment, secrets, injuries, powers, personality traits, item properties, mythos names, rules, lore, titles, hidden inscriptions, or parenthetical metadata.
Focus on visible body/face/hair/clothing/posture and concrete visual cues; inventory is not a carried-items list, so mention only clearly worn or currently held visible accessories and omit all uncertain items.

You only generate a pure appearance description, without any other attributes.
`

func writeImagePromptCharacterVisualDescription(ctx context.Context, h agentHandle, scenePrompt string, cards []models.CharacterCard) (string, error) {
	if !h.isEnabled() {
		return "", fmt.Errorf("writer agent unavailable")
	}
	msgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(imagePromptCharacterVisualSystemPrompt)},
		{Role: "user", Content: buildImagePromptCharacterVisualUserPrompt(scenePrompt, cards)},
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

func buildImagePromptCharacterVisualUserPrompt(scenePrompt string, cards []models.CharacterCard) string {
	var sb strings.Builder
	sb.WriteString("Task: Convert the following player character card data into concise English visual descriptions for image generation. The Director will decide whether and how to incorporate this text into a later natural-language image prompt. Use the scene image prompt, if present, only to choose which visible appearance features matter. Use appearance as the main source; inventory is optional visible-item hints only, not a carried list. Omit item properties, mythos/lore names, rules, titles, hidden inscriptions, parenthetical metadata, and anything not clearly visible/worn/held. Chinese intent: 为Director提供英文外貌素材,由Director自行写入后续image_prompt。\n\n")
	sb.WriteString("Scene image prompt: ")
	sb.WriteString(compactPromptText(scenePrompt))
	sb.WriteString("\n\n")
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

func fallbackCharacterVisualDescriptions(cards []models.CharacterCard) string {
	lines := make([]string, 0, len(cards))
	for _, card := range cards {
		name := compactPromptText(card.Name)
		if name == "" {
			name = "Unnamed investigator"
		}
		appearance := compactPromptText(card.Appearance)
		if appearance == "" {
			lines = append(lines, fmt.Sprintf("%s: no recorded visual appearance details.", name))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: visible appearance from the character card: %s", name, appearance))
	}
	return strings.Join(lines, "\n")
}

func fallbackMissingCharacterVisualDescription(names []string) string {
	return "No matching investigator character cards were found for: " + strings.Join(names, ", ") + "."
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
