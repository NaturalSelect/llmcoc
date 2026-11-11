// NOTE: portrait.go 负责调查员立绘：Writer 先把外貌和当前穿戴写成画面描述，再由 Painter 出竖版全身图。
// 立绘不属于任何房间，固定按全年龄渲染，只使用 writer 和 painter，不回落也不路由到 NSFW 角色。
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// NOTE: 与 character.go 的 "nosession:writer" 同一约定，立绘没有会话，用固定占位当 prompt cache 前缀。
const portraitCacheSession = "nosession"

const portraitPromptHead = "调查员角色立绘：单人，全身站姿，素色背景。\n"

const portraitSysPrompt = `
画面要求：
- 画风：日式漫画。与游戏内配图保持同一画风：粗细有变化的墨线，配色克制的赛璐璐平涂，用网点式排线表现阴影，不要画成写实风格。
- 构图：单人全身立绘，从头到脚完整入画，自然站姿，正面或四分之三侧面，人物居中，四周留白。背景用素色或极简背景，不画场景。
- 外观：脸、发型、服装、配饰和手持物严格按描述绘制，不添加描述之外的道具，也不要省略描述里提到的穿戴物品。
- 表情与姿态：神态自然，姿态放松而有性格。
- 画面里不要出现任何可读文字，不要对话气泡、边框和多视图拼版。
{{NSFW_IMAGE_POLICY}}
`

// GeneratePortrait 为人物卡生成立绘，返回图片 data URL。
// 三个角色句柄先全部加载完再调用 LLM，Painter 不可用时不会白白消耗 Writer 的调用。
func GeneratePortrait(ctx context.Context, card *models.CharacterCard) (string, error) {
	if card == nil {
		return "", fmt.Errorf("character card is nil")
	}
	writer, err := loadSingleAgent(models.AgentRoleWriter)
	if err != nil {
		return "", err
	}
	painter, err := loadSingleAgent(models.AgentRolePainter)
	if err != nil {
		return "", err
	}
	var translator agentHandle
	if painter.config != nil && painter.config.DiffusionMode {
		translator, err = loadSingleAgent(models.AgentRoleDiffusionTranslator)
		if err != nil {
			return "", fmt.Errorf("扩散模型模式需要启用 %s agent 翻译提示词: %w", models.AgentRoleDiffusionTranslator, err)
		}
	}
	return generatePortraitWith(ctx, writer, painter, translator, card)
}

func generatePortraitWith(ctx context.Context, writer, painter, translator agentHandle, card *models.CharacterCard) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := painter.provider.(llm.ImageGenerator); !ok {
		return "", fmt.Errorf("当前 %s provider 不支持图片生成", models.AgentRolePainter)
	}
	desc, err := writeImagePromptCharacterVisualDescription(ctx, writer, "", portraitCacheSession, []models.CharacterCard{*card}, false)
	if err != nil {
		return "", fmt.Errorf("生成立绘描述失败: %w", err)
	}
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return "", fmt.Errorf("生成立绘描述失败: 描述为空")
	}
	return paintImage(ctx, painter, translator, portraitCacheSession, portraitPromptHead+desc, portraitSysPrompt, llm.ImageAspectPortrait, false)
}
