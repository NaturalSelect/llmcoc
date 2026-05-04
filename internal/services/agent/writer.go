// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const writerDefaultPrompt = `你是日本官能小说的文字编辑,擅长强调感官冲击,心理描写和细节刻画,不避讳描写暴力、血腥、性暗示等元素。根据导演提供的叙事指令,为RPG玩家描述当前场景。

要求:
- 语言: 简体中文
- NPC对话用引号标注,场景描写具体生动(高信息密度)
- 不得出现"SAN值""HP""技能值""检定""孤注一掷"等游戏术语
- 直接输出叙事文字,不加任何前言或格式标记
- 与上文保持连贯,不重复已描述的内容`

// appendWriter calls the Writer agent with the given direction and appends
// the generated narrative to writerState.Buffer.
//
// WriterState.History accumulates the full conversation (direction → narrative)
// so each subsequent call can continue seamlessly from where the previous left off.
// This satisfies requirement: Writer maintains output history for text continuity.
func appendWriter(ctx context.Context, h agentHandle, state *WriterState, direction string, gctx GameContext) error {
	if direction == "" {
		direction = "继续描述当前场景"
	}
	direction += "\n以上是导演的叙事指令,请根据指令续写叙事内容, 不要虚构调查员发言(除非调查员明确要求这样做), 这样可以保持剧情的连续性。"

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
					"\n\n【注意】%s正经历疯狂症状(KP掌控其行为):%s — 请在叙事中自然体现,勿使用游戏术语。",
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
			Content: "(已了解当前游戏状态,准备续写叙事。)",
		})
	}

	// Build messages: system + accumulated history + new direction.
	msgs := make([]llm.ChatMessage, 0, len(state.History)+2)
	msgs = append(msgs, llm.ChatMessage{
		Role:    "system",
		Content: h.systemPrompt(writerDefaultPrompt),
	})
	trunc := func(msg []llm.ChatMessage, maxToken int) []llm.ChatMessage {
		newMsg := make([]llm.ChatMessage, 0)
		tokenCount := 0
		for i := len(msg) - 1; i >= 0; i-- {
			tokenCount += len([]rune(msg[i].Content))
			if tokenCount > maxToken {
				break
			}
			newMsg = append([]llm.ChatMessage{msg[i]}, newMsg...)
		}
		return newMsg
	}
	msgs = append(msgs, trunc(state.History, 20000)...)
	msgs = append(msgs, llm.ChatMessage{
		Role:    "user",
		Content: "叙事指令:" + direction,
	})

	resp, err := h.provider.Chat(ctx, msgs)
	if err != nil {
		return err
	}

	debugf("Writer", "response len=%d preview=%s", len([]rune(resp)), resp)

	// Update history with this exchange for continuity in subsequent calls.
	state.History = append(state.History,
		llm.ChatMessage{Role: "user", Content: "叙事指令:" + direction},
		llm.ChatMessage{Role: "assistant", Content: resp},
	)

	// Accumulate narrative in the buffer (separated by newlines between chunks).
	if state.Buffer != "" {
		state.Buffer += "\n\n"
	}
	state.Buffer += resp
	return nil
}

const characterEvolutionPrompt = `你是COC TRPG的角色成长编辑。根据角色原有的背景故事、性格特征,以及本次冒险的叙事经历,更新角色的背景故事和性格特征,体现冒险对角色的影响和成长。

要求:
- 保留角色的核心身份,但反映冒险带来的变化
- 背景故事可以追加新的经历
- 从角色的语言和行为中提炼性格特征
- 篇幅与原有内容相近,不要过度冗长,一般仅追加一两句话即可,如果过长请考虑总结
- 总篇幅在200字以内
- 仅输出JSON,不要任何额外文字:
{"new_backstory": "更新后的背景故事(200字以内)", "new_traits": "更新后的性格特征(200字以内)"}
`

// CharacterEvolutionResult is the writer agent output for a single character's evolution.
type CharacterEvolutionResult struct {
	NewBackstory string `json:"new_backstory"`
	NewTraits    string `json:"new_traits"`
}

var evolutionExample = func() string {
	data, err := json.Marshal(CharacterEvolutionResult{})
	if err != nil {
		return ""
	}
	return string(data)
}()

// RunCharacterEvolution uses the Writer agent to generate an updated backstory and traits
// for the given character card, based on the session's WriterHistory.
// The full WriterHistory is reused as conversation context (all messages are already cached
// by the provider from the game session). Only the final evolution request is a new message.
// Returns an error if the Writer agent is not configured or the LLM call fails.
func RunCharacterEvolution(ctx context.Context, card *models.CharacterCard, writerHistory []models.ChatMsg) (CharacterEvolutionResult, error) {
	if len(writerHistory) == 0 {
		return CharacterEvolutionResult{NewBackstory: card.Backstory, NewTraits: card.Traits}, nil
	}

	handle, err := loadSingleAgent(models.AgentRoleEvaluator)
	if err != nil {
		return CharacterEvolutionResult{}, fmt.Errorf("evaluator agent 未配置: %w", err)
	}

	// Copy WriterHistory as-is — all messages hit the provider's prompt cache.
	msgs := make([]llm.ChatMessage, 0, len(writerHistory)+2)
	msgs = append(msgs, llm.ChatMessage{
		Role:    "system",
		Content: handle.systemPrompt(characterEvolutionPrompt),
	})
	for _, m := range writerHistory {
		msgs = append(msgs, llm.ChatMessage{Role: m.Role, Content: m.Content})
	}
	// Append the evolution request as the only new (non-cached) message.
	msgs = append(msgs, llm.ChatMessage{
		Role: "user",
		Content: fmt.Sprintf(
			"根据以上冒险叙事,更新调查员【%s】的背景故事和性格特征(你只能附加一小段, 不超过 2 句话,与原文一起输出)。\n原背景故事:%s\n原性格特征:%s\n\n仅输出JSON:{\"new_backstory\": \"...\", \"new_traits\": \"...\"}",
			card.Name, card.Backstory, card.Traits,
		),
	})

	resp, err := handle.provider.Chat(ctx, msgs)
	if err != nil {
		return CharacterEvolutionResult{}, fmt.Errorf("character evolution LLM error: %w", err)
	}

	var result CharacterEvolutionResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		for i := 0; i < 30; i++ {
			resp, err = RepairJSON(ctx, resp, err, evolutionExample)
			if err == nil {
				err = json.Unmarshal([]byte(resp), &result)
				if err == nil {
					break
				}
			}
			log.Printf("[agent] character evolution JSON parse error for %q: %v; attempt %d to repair with parser", card.Name, err, i+1)
		}
		if err != nil {
			log.Printf("[agent] character evolution JSON parse error for %q: %v", card.Name, err)
			return CharacterEvolutionResult{}, fmt.Errorf("character evolution JSON parse error: %w", err)
		}
	}

	return result, nil
}
