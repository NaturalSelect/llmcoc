// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
		log.Printf("[growth] evaluator agent unavailable, skipping growth: %v", err)
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

	resp, err := handle.provider.Chat(ctx, msgs)
	if err != nil {
		log.Printf("[growth] LLM error: %v; skipping growth", err)
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
			log.Printf("[growth] JSON repair attempt %d: %v", i+1, jsonErr)
		}
		if jsonErr != nil {
			log.Printf("[growth] JSON parse failed after repairs: %v; skipping growth", jsonErr)
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
			log.Printf("[growth] character %q not found in session players; skipping", charName)
			continue
		}

		var changes []SkillChange

		for _, skill := range charEntry.Skills {
			current := currentSkills[skill]
			if current <= 0 {
				current = 1
			}

			gain, _ := game.Roll(1, 10)
			newVal := current + gain
			if newVal > 99 {
				gain = 99 - current
				newVal = 99
			}
			if gain > 0 {
				changes = append(changes, SkillChange{Skill: skill, Delta: gain})
				log.Printf("[growth] %s / %s: current=%d gain=%d new=%d", charName, skill, current, gain, newVal)
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
