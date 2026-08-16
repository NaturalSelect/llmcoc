// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
	"github.com/llmcoc/server/internal/services/llm"
)

const growthPrompt = `你是COC TRPG的成长裁判。根据本局完整聊天记录和角色技能列表,判断每位调查员哪些技能在本局中有实际运用(无论成功与否),这些技能将获得1d10成长。

排除规则:
- 克苏鲁神话、信用评级 不得成长
- 幸运、闪避(除非主动使用)、母语 等基础属性不计入
- 仅技能卡上存在的技能才可成长

仅输出JSON,不要任何额外文字:
{
  "characters": [
    {
      "character_name": "角色名",
      "skills": ["技能A", "技能B"]
    }
  ]
}`

type growthLLMOutput struct {
	Characters []struct {
		CharacterName string   `json:"character_name"`
		Skills        []string `json:"skills"`
	} `json:"characters"`
}

// RunGrowth uses an LLM to determine which skills each character used during the session,
// then applies a 1d10 gain to each qualifying skill (capped at 99).
// Falls back to empty result if LLM is unavailable.
func RunGrowth(ctx context.Context, session *models.GameSession, messages []models.Message) (GrowthResult, error) {
	// Clean up any legacy growth marks regardless of outcome.
	models.DB.Where("session_id = ?", session.ID).Delete(&models.SessionGrowthMark{})

	handle, err := loadSingleAgent(models.AgentRoleEvaluator)
	if err != nil {
		alog.Warn("growth evaluator agent unavailable, skipping growth", "err", err)
		return GrowthResult{}, nil
	}

	// Build chat log for LLM context.
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

	// Build per-character skill list so LLM only picks from existing skills.
	var charInfo strings.Builder
	for _, p := range session.Players {
		card := p.CharacterCard
		var skillNames []string
		for k := range card.Skills.Data {
			skillNames = append(skillNames, k)
		}
		charInfo.WriteString(fmt.Sprintf("调查员【%s】技能列表: %s\n", card.Name, strings.Join(skillNames, "、")))
	}

	msgs := []llm.ChatMessage{
		{Role: "system", Content: handle.systemPrompt(growthPrompt)},
		{Role: "user", Content: charInfo.String()},
		{Role: "user", Content: "聊天记录:\n" + logBuilder.String()},
	}

	resp, err := handle.provider.JsonChat(ctx, fmt.Sprintf("%v:evaluator", session.ID), msgs)
	if err != nil {
		alog.Error("growth LLM call failed, skipping growth", "err", err)
		return GrowthResult{}, nil
	}

	var llmOut growthLLMOutput
	if jsonErr := json.Unmarshal([]byte(resp), &llmOut); jsonErr != nil {
		for i := 0; i < 30; i++ {
			resp, jsonErr = RepairJSON(ctx, resp, jsonErr, `{"characters":[{"character_name":"...","skills":["技能A"]}]}`)
			if jsonErr == nil {
				jsonErr = json.Unmarshal([]byte(resp), &llmOut)
				if jsonErr == nil {
					break
				}
			}
			alog.Warn("growth JSON parse retry", "attempt", i+1, "err", jsonErr)
		}
		if jsonErr != nil {
			alog.Error("growth JSON parse failed, skipping growth", "err", jsonErr)
			return GrowthResult{}, nil
		}
	}

	// Build skill-value lookup: characterName → skill → current value.
	skillOf := make(map[string]map[string]int)
	for _, p := range session.Players {
		card := p.CharacterCard
		skillOf[card.Name] = card.Skills.Data
	}

	var result GrowthResult

	for _, charEntry := range llmOut.Characters {
		charName := charEntry.CharacterName
		currentSkills := skillOf[charName]
		if currentSkills == nil {
			alog.Warn("growth character not found in session players, skipping", "character", charName)
			continue
		}

		var changes []SkillChange

		for _, skill := range charEntry.Skills {
			current := currentSkills[skill]
			if current <= 0 {
				current = 1
			}
			chance := game.RollD100()
			if chance <= current {
				continue
			}

			gain, _ := game.Roll(1, 3)
			newVal := current + gain
			if newVal > 99 {
				gain = 99 - current
				newVal = 99
			}
			if gain > 0 {
				changes = append(changes, SkillChange{Skill: skill, Delta: gain})
				alog.Debug("growth skill gain", "character", charName, "skill", skill, "current", current, "gain", gain, "new", newVal)
			}
		}

		desc := buildGrowthDescription(changes)
		cg := CharacterGrowth{
			CharacterName:     charName,
			SkillChanges:      changes,
			GrowthDescription: desc,
		}
		result.Characters = append(result.Characters, cg)
	}

	return result, nil
}

func buildGrowthDescription(changes []SkillChange) string {
	if len(changes) == 0 {
		return "本局无技能成长"
	}
	desc := ""
	for i, sc := range changes {
		if i > 0 {
			desc += "、"
		}
		desc += fmt.Sprintf("%s +%d", sc.Skill, sc.Delta)
	}
	return desc
}
