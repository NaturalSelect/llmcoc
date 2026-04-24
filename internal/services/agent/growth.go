package agent

import (
	"context"
	"fmt"
	"log"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
)

// RunGrowth applies COC 7th Edition classic skill growth at session end.
//
// Rules (COC Keeper Rulebook p.61):
//  1. Skills that were successfully used during the session received a growth mark
//     (recorded in SessionGrowthMark by the orchestrator at check time).
//  2. For each marked skill, roll 1d100.
//     - If the roll > current skill value → the skill improves by 1d10 (capped at 99).
//  3. If the skill was already ≥ 90 and the growth check succeeds, award +2d6 SAN.
//  4. 克苏鲁神话 and 信用评级 are excluded (never receive growth marks).
//  5. Bonus-dice checks and luck/sanity checks do not generate growth marks.
//
// On empty marks the function returns an empty GrowthResult (no error to caller).
func RunGrowth(_ context.Context, session *models.GameSession, _ []models.Message) (GrowthResult, error) {
	// Load all growth marks recorded during this session.
	var marks []models.SessionGrowthMark
	models.DB.Where("session_id = ?", session.ID).Find(&marks)
	if len(marks) == 0 {
		return GrowthResult{}, nil
	}

	// Group marks by character name.
	byChar := make(map[string][]string)
	for _, m := range marks {
		byChar[m.CharacterName] = append(byChar[m.CharacterName], m.Skill)
	}

	// Build a skill-value lookup: characterName → skill → current value.
	skillOf := make(map[string]map[string]int)
	for _, p := range session.Players {
		card := p.CharacterCard
		skillOf[card.Name] = card.Skills.Data
	}

	var result GrowthResult

	for charName, skills := range byChar {
		currentSkills := skillOf[charName]
		if currentSkills == nil {
			log.Printf("[growth] character %q not found in session players; skipping", charName)
			continue
		}

		var changes []SkillChange
		sanBonus := 0

		for _, skill := range skills {
			current := currentSkills[skill]
			if current <= 0 {
				current = 1
			}

			// Growth check: roll 1d100. If roll > current value, skill improves.
			roll := game.RollD100()
			log.Printf("[growth] %s / %s: current=%d roll=%d", charName, skill, current, roll)

			if roll > current {
				gain, _ := game.Roll(1, 10)
				newVal := current + gain
				if newVal > 99 {
					gain = 99 - current
					newVal = 99
				}
				if gain > 0 {
					changes = append(changes, SkillChange{Skill: skill, Delta: gain})
				}

				// COC rule: skill already ≥ 90 and growth succeeds → +2d6 SAN.
				if current >= 90 {
					sanGain, _ := game.Roll(2, 6)
					sanBonus += sanGain
				}
			}
		}

		desc := buildGrowthDescription(changes, sanBonus)
		cg := CharacterGrowth{
			CharacterName:     charName,
			SkillChanges:      changes,
			GrowthDescription: desc,
		}
		result.Characters = append(result.Characters, cg)

		// Apply SAN bonus directly to the character card.
		if sanBonus > 0 {
			applySANBonus(session.Players, charName, sanBonus)
		}
	}

	// Clean up growth marks for this session.
	models.DB.Where("session_id = ?", session.ID).Delete(&models.SessionGrowthMark{})

	return result, nil
}

func buildGrowthDescription(changes []SkillChange, sanBonus int) string {
	if len(changes) == 0 && sanBonus == 0 {
		return "本局无技能成长"
	}
	desc := ""
	for i, sc := range changes {
		if i > 0 {
			desc += "、"
		}
		desc += fmt.Sprintf("%s +%d", sc.Skill, sc.Delta)
	}
	if sanBonus > 0 {
		if desc != "" {
			desc += fmt.Sprintf("；专家技能成长奖励 SAN +%d", sanBonus)
		} else {
			desc = fmt.Sprintf("专家技能成长奖励 SAN +%d", sanBonus)
		}
	}
	return desc
}

func applySANBonus(players []models.SessionPlayer, charName string, bonus int) {
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != charName {
			continue
		}
		stats := card.Stats.Data
		stats.SAN += bonus
		if stats.SAN > stats.MaxSAN {
			stats.SAN = stats.MaxSAN
		}
		card.Stats.Data = stats
		models.DB.Save(card)
		return
	}
}
