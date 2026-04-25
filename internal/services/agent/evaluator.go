// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const evaluatorDefaultPrompt = `你是COC TRPG的游戏结算员。游戏结束后，根据本局完整聊天记录对每位调查员进行综合评价，并给出游戏币奖励建议。

仅输出JSON，不要任何额外文字：
{
  "summary": "本局整体总结（100字以内）",
  "players": [
    {
      "character_name": "角色名",
      "comment": "对该调查员本局表现的点评（50字以内）",
      "score": 80,
      "base_coins": 20,
      "bonus_coins": 15
    }
  ]
}

说明：
- score：0-100分，综合角色扮演质量、探索贡献、线索发现、生存情况评分
- base_coins：固定为20，所有参与玩家均可获得
- bonus_coins：0-50，根据表现给出额外奖励，优秀表现给高分
- 每位在 players 列表中的调查员都必须给出评价`

// RunEvaluator runs the evaluator agent at the end of a session.
// Returns an EvaluationResult; on failure, falls back to a default reward for each player.
func RunEvaluator(ctx context.Context, session *models.GameSession, messages []models.Message) (EvaluationResult, error) {
	handle, err := loadSingleAgent(models.AgentRoleEvaluator)
	if err != nil {
		return fallbackEvaluation(session), nil
	}

	// Build a condensed chat log
	var logBuilder strings.Builder
	for _, m := range messages {
		role := "KP"
		if m.Role == models.MessageRoleUser {
			role = m.Username
			if role == "" {
				role = "玩家"
			}
		}
		logBuilder.WriteString(fmt.Sprintf("[%s]: %s\n", role, m.Content))
	}

	// Player summary (names + HP/SAN at end of game)
	var playerInfo strings.Builder
	playerInfo.WriteString("参与调查员：")
	for _, p := range session.Players {
		card := p.CharacterCard
		playerInfo.WriteString(fmt.Sprintf("\n• %s（%s）最终状态 HP:%d/%d SAN:%d/%d",
			card.Name, card.Occupation,
			card.Stats.Data.HP, card.Stats.Data.MaxHP,
			card.Stats.Data.SAN, card.Stats.Data.MaxSAN))
	}

	// Separate static context (scenario + player info) from dynamic context (chat log)
	// into distinct messages, following the buildKPMessages pattern.
	msgs := []llm.ChatMessage{
		{Role: "system", Content: handle.systemPrompt(evaluatorDefaultPrompt)},
		{Role: "user", Content: fmt.Sprintf("剧本：%s\n\n%s", session.Scenario.Name, playerInfo.String())},
		{Role: "user", Content: "聊天记录：\n" + logBuilder.String()},
	}

	resp, err := handle.provider.Chat(ctx, msgs)
	if err != nil {
		log.Printf("[agent] evaluator error: %v; using fallback rewards", err)
		return fallbackEvaluation(session), nil
	}

	resp = llm.StripCodeFence(resp)
	var result EvaluationResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		log.Printf("[agent] evaluator JSON parse error: %v; using fallback rewards", err)
		return fallbackEvaluation(session), nil
	}

	// Clamp bonus_coins to [0, 50] and ensure base_coins == 20
	for i := range result.Players {
		result.Players[i].BaseCoins = 20
		if result.Players[i].BonusCoins < 0 {
			result.Players[i].BonusCoins = 0
		}
		if result.Players[i].BonusCoins > 50 {
			result.Players[i].BonusCoins = 50
		}
	}

	return result, nil
}

func fallbackEvaluation(session *models.GameSession) EvaluationResult {
	result := EvaluationResult{
		Summary: "本局游戏已结束，感谢各位调查员的参与。",
	}
	for _, p := range session.Players {
		result.Players = append(result.Players, PlayerEvaluation{
			CharacterName: p.CharacterCard.Name,
			Comment:       "感谢参与本次调查。",
			Score:         60,
			BaseCoins:     20,
			BonusCoins:    0,
		})
	}
	return result
}

// loadSingleAgent loads an agentHandle for the given role from the database.
// Returns an error if the role has no active config or no active provider config.
func loadSingleAgent(role models.AgentRole) (agentHandle, error) {
	var cfg models.AgentConfig
	err := models.DB.Preload("ProviderConfig").
		Where("role = ? AND is_active = ?", role, true).
		First(&cfg).Error
	if err != nil {
		return agentHandle{}, fmt.Errorf("agent %q 未配置，请在管理面板配置 LLM provider", role)
	}
	if cfg.ProviderConfigID == nil || cfg.ProviderConfig == nil || !cfg.ProviderConfig.IsActive {
		return agentHandle{}, fmt.Errorf("agent %q 未绑定可用的 LLM provider", role)
	}
	maxTok := cfg.MaxTokens
	if maxTok == 0 {
		maxTok = 1024
	}
	p := llm.NewProviderFromConfig(cfg.ProviderConfig, cfg.ModelName, maxTok, cfg.Temperature)
	return agentHandle{provider: p, config: &cfg}, nil
}
