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

const growthDefaultPrompt = `你是COC TRPG的成长裁定员。根据本局聊天记录判断每位调查员本局的表现，决定其技能成长。

仅输出JSON，不要任何额外文字：
{
  "characters": [
    {
      "character_name": "角色名",
      "skill_changes": [
        {"skill": "技能名", "delta": 5}
      ],
      "growth_description": "成长说明（30字以内）"
    }
  ]
}

约束：
- 每个角色最多3项技能提升
- 每项 delta 为正整数，范围 1-10
- 只能提升本局实际使用过的技能
- 技能提升后不能超过99点（由系统保证上限，你只需给出合理的delta）
- 若角色本局几乎没有使用任何技能，可以给出空 skill_changes 列表`

// RunGrowth runs the growth agent at the end of a session.
// On failure, returns an empty GrowthResult (no growth, no error to caller).
func RunGrowth(ctx context.Context, session *models.GameSession, messages []models.Message) (GrowthResult, error) {
	handle := loadSingleAgent(models.AgentRoleGrowth)

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

	// Character skill snapshot
	var charInfo strings.Builder
	for _, p := range session.Players {
		card := p.CharacterCard
		charInfo.WriteString(fmt.Sprintf("\n【%s（%s）】当前技能：", card.Name, card.Occupation))
		// List skills sorted deterministically enough for LLM context
		skills := card.Skills.Data
		count := 0
		for skill, val := range skills {
			charInfo.WriteString(fmt.Sprintf("%s=%d ", skill, val))
			count++
			if count >= 30 { // cap to avoid token overflow
				charInfo.WriteString("...")
				break
			}
		}
	}

	userPrompt := fmt.Sprintf("剧本：%s\n\n调查员信息：%s\n\n聊天记录：\n%s",
		session.Scenario.Name, charInfo.String(), logBuilder.String())

	msgs := []llm.ChatMessage{
		{Role: "system", Content: handle.systemPrompt(growthDefaultPrompt)},
		{Role: "user", Content: userPrompt},
	}

	resp, err := handle.provider.Chat(ctx, msgs)
	if err != nil {
		log.Printf("[agent] growth error: %v; skipping growth", err)
		return GrowthResult{}, nil
	}

	resp = llm.StripCodeFence(resp)
	var result GrowthResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		log.Printf("[agent] growth JSON parse error: %v; skipping growth", err)
		return GrowthResult{}, nil
	}

	// Clamp deltas to valid range
	for ci := range result.Characters {
		var valid []SkillChange
		for _, sc := range result.Characters[ci].SkillChanges {
			if sc.Delta < 1 {
				sc.Delta = 1
			}
			if sc.Delta > 10 {
				sc.Delta = 10
			}
			valid = append(valid, sc)
		}
		// Limit to 3 changes per character
		if len(valid) > 3 {
			valid = valid[:3]
		}
		result.Characters[ci].SkillChanges = valid
	}

	return result, nil
}
