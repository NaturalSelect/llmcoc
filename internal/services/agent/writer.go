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

const writerDefaultPrompt = `你是中文网络小说风格的场景文字编辑，擅长清晰、具体、有节奏的叙事。根据导演指令续写当前场景。

要求:
- 简体中文，中文网络小说风格，高信息密度
- NPC对话用引号标注
- 禁止出现"SAN值""HP""技能值""检定"等游戏术语
- 直接输出叙事文字，不加任何前言或格式标记
- 与上文保持连贯，不重复已描述的内容
- 人物发言禁止虚构，原话直接引用；无发言指令时只写场景和NPC描写

风格控制:
- 日常/调查/移动/交谈场景要保持自然、具体、可见可闻：写人物动作、物件位置、光线、声音、对话反应，不要强行制造怪异感。
- 恐怖感需要反差。只有当导演指令明确出现怪物、神话实体、异常现象、尸体、血腥、疯狂、袭击或强烈危险时，才提升到压迫、诡异、血腥或感官冲击的描写。
- 怪物登场前的普通场景不要一直铺陈阴冷、黏腻、腐败、被注视、空气凝固、不可名状等套话；这些词会削弱真正异常出现时的冲击力。
- 如果上一段很诡异，但本段导演指令只是普通行动或对话，应把语气拉回具体事件，不要惯性延续恐怖滤镜。
- 怪物或异常真正出现时，先用一两个正常细节建立基线，再写异常打破基线，突出反差。
- 避免无病呻吟和空泛心理描写。不要频繁写“某种不安”“难以言说”“仿佛有什么东西”等没有具体对象的句子。
- 暴力、血腥、性暗示只在导演指令需要时使用；不要为了风格主动添加。`

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
	msgs = append(msgs, trunc(state.History, 7000)...)
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

const characterEvolutionPrompt = `你是无限流故事的角色成长编辑。根据角色原有的背景故事、性格特征,以及本次冒险的叙事经历,更新角色的背景故事和性格特征,体现冒险对角色的影响和成长。

要求:
- 保留角色的核心身份,但反映冒险带来的变化
- 背景故事可以追加新的经历
- 从角色的语言和行为中提炼性格特征
- 篇幅与原有内容相近,不要过度冗长,一般仅追加一两句话即可,如果过长请考虑总结
- 性格要求反应角色的言行(二次元标签风格),不要空洞的形容词(如"勇敢")或抽象的概念(如"正义"),也不要直接描述冒险经历(如"经历了xxx事件"),而是总结出更具象的特征(如"喜欢收集奇怪的物品"、"对陌生人充满戒心"、"在压力下会变得暴躁")。
- 总篇幅在200字以内
- 仅输出JSON,不要任何额外文字:
{"new_backstory": "更新后的背景故事(200字以内)", "new_traits": "更新后的性格特征(1-5个标签以空格分隔)"}
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
			"根据以上冒险叙事,更新角色【%s】的背景故事和性格特征。\n原背景故事:%s\n原性格特征:%s\n\n仅输出JSON:{\"new_backstory\": \"...\", \"new_traits\": \"...\"}",
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
