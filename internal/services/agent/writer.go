package agent

import (
	"context"
	"fmt"

	"github.com/llmcoc/server/internal/services/llm"
)

const writerDefaultPrompt = `你是COC风格的文字编辑。根据导演提供的叙事指令，为RPG玩家描述当前场景。

要求：
- NPC对话用引号标注，场景描写具体生动(100字以内，高信息密度)
- 不得出现"SAN值""HP""技能值""检定""孤注一掷"等游戏术语
- 直接输出叙事文字，不加任何前言或格式标记
- 与上文保持连贯，不重复已描述的内容`

// appendWriter calls the Writer agent with the given direction and appends
// the generated narrative to writerState.Buffer.
//
// WriterState.History accumulates the full conversation (direction → narrative)
// so each subsequent call can continue seamlessly from where the previous left off.
// This satisfies requirement: Writer maintains output history for text continuity.
func appendWriter(ctx context.Context, h agentHandle, state *WriterState, direction string, gctx GameContext) error {
	if direction == "" {
		direction = "继续描述当前场景，保持克苏鲁氛围。"
	}

	debugf("Writer", "direction=%s history_msgs=%d", direction, len(state.History))

	// Seed history with session context on the first call so Writer knows
	// the immediate situation (players, recent chat) without the full scenario.
	if len(state.History) == 0 {
		playerStatus := buildPlayerStatus(gctx.Session.Players)

		// Inject player status as a context message.
		contextHint := "【游戏状态参考】\n" + playerStatus

		// Inject madness symptoms if any player is in a madness state.
		for _, p := range gctx.Session.Players {
			card := p.CharacterCard
			if (card.MadnessState == "temporary" || card.MadnessState == "indefinite") && card.MadnessSymptom != "" {
				contextHint += fmt.Sprintf(
					"\n\n【注意】%s正经历疯狂症状（KP掌控其行为）：%s — 请在叙事中自然体现，勿使用游戏术语。",
					card.Name, card.MadnessSymptom,
				)
			}
		}

		state.History = append(state.History, llm.ChatMessage{
			Role:    "user",
			Content: contextHint,
		})
		state.History = append(state.History, llm.ChatMessage{
			Role:    "assistant",
			Content: "（已了解当前游戏状态，准备续写叙事。）",
		})
	}

	// Build messages: system + accumulated history + new direction.
	msgs := make([]llm.ChatMessage, 0, len(state.History)+2)
	msgs = append(msgs, llm.ChatMessage{
		Role:    "system",
		Content: h.systemPrompt(writerDefaultPrompt),
	})
	msgs = append(msgs, state.History...)
	msgs = append(msgs, llm.ChatMessage{
		Role:    "user",
		Content: "叙事指令：" + direction,
	})

	resp, err := h.provider.Chat(ctx, msgs)
	if err != nil {
		return err
	}

	debugf("Writer", "response len=%d preview=%s", len([]rune(resp)), resp)

	// Update history with this exchange for continuity in subsequent calls.
	state.History = append(state.History,
		llm.ChatMessage{Role: "user", Content: "叙事指令：" + direction},
		llm.ChatMessage{Role: "assistant", Content: resp},
	)

	// Accumulate narrative in the buffer (separated by newlines between chunks).
	if state.Buffer != "" {
		state.Buffer += "\n\n"
	}
	state.Buffer += resp
	return nil
}
