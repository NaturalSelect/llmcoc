// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
)

// parseStateChange parses a director change string (e.g. "HP -3（角色名）" or
// "cthulhu_mythos +1（角色名）") into a CharacterUpdate.
// Supported fields: HP, SAN, MP, cthulhu_mythos.
// Returns false if the string cannot be matched to a known field.
func parseStateChange(change string) (CharacterUpdate, bool) {
	change = strings.TrimSpace(change)
	// Check longest field name first to avoid prefix collisions.
	for _, field := range []string{"cthulhu_mythos", "HP", "SAN", "MP"} {
		if !strings.HasPrefix(strings.ToUpper(change), strings.ToUpper(field)) {
			continue
		}
		rest := strings.TrimSpace(change[len(field):])
		var deltaStr, charName string
		if idx := strings.Index(rest, "（"); idx >= 0 {
			deltaStr = strings.TrimSpace(rest[:idx])
			charName = strings.TrimSuffix(strings.TrimPrefix(rest[idx:], "（"), "）")
		} else {
			deltaStr = rest
		}
		var delta int
		fmt.Sscanf(deltaStr, "%d", &delta)
		return CharacterUpdate{
			CharacterName: charName,
			Field:         strings.ToLower(field),
			Delta:         delta,
		}, true
	}
	log.Printf("[editor] unrecognised change string: %q", change)
	return CharacterUpdate{}, false
}

func applyCharacterUpdate(upd CharacterUpdate, players []models.SessionPlayer) {
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != upd.CharacterName {
			continue
		}
		log.Printf("[editor] applying %s.%s delta=%d add=%q", card.Name, upd.Field, upd.Delta, upd.AddValue)

		switch strings.ToLower(upd.Field) {
		case "san":
			s := card.Stats.Data
			prevSAN := s.SAN
			s.SAN = clamp(s.SAN+upd.Delta, 0, s.MaxSAN)
			card.Stats.Data = s

			// ── 理智损失事件：检查疯狂触发 ──────────────────────────────────────
			if upd.Delta < 0 {
				sanLoss := prevSAN - s.SAN // actual points lost (positive)
				if sanLoss > 0 {
					card.DailySanLoss += sanLoss

					// 潜在疯狂期（临时/不定性）：哪怕只损失1点SAN，立即再次触发疯狂发作
					if card.MadnessState == "temporary" || card.MadnessState == "indefinite" {
						// Re-roll a new symptom for the relapse episode.
						sym := game.RollMadnessSymptom(true)
						card.MadnessSymptom = sym.Description
						card.MadnessDuration = 1
						log.Printf("[editor] %s: latent madness relapse triggered (state=%s, sanLoss=%d)", card.Name, card.MadnessState, sanLoss)
					} else {
						kind := game.EvalMadness(sanLoss, s.SAN, card.DailySanLoss, s.MaxSAN)
						applyMadnessToCard(card, kind)
					}
				}
			}
			models.DB.Save(card)

		case "hp":
			s := card.Stats.Data
			damage := 0
			if upd.Delta < 0 {
				damage = -upd.Delta
			}
			s.HP = clamp(s.HP+upd.Delta, 0, s.MaxHP)
			card.Stats.Data = s

			// ── 伤害事件：检查重伤/濒死/即死 ─────────────────────────────────
			if damage > 0 {
				switch {
				case game.CheckInstantDeath(damage, s.MaxHP):
					card.WoundState = "dead"
					card.IsUnconscious = true
					log.Printf("[editor] %s: instant death (damage %d > maxHP %d)", card.Name, damage, s.MaxHP)
				case game.CheckMajorWound(damage, s.MaxHP):
					card.WoundState = "major"
					log.Printf("[editor] %s: major wound (damage %d >= maxHP/2 %d)", card.Name, damage, s.MaxHP/2)
				}
			}
			// HP归零且已有重伤 → 濒死（需急救）
			if s.HP <= 0 && card.WoundState == "major" && card.WoundState != "dead" {
				card.WoundState = "dying"
				card.IsUnconscious = true
			} else if s.HP <= 0 && card.WoundState == "none" {
				// 仅轻伤HP归零 → 昏迷，不会直接死亡
				card.IsUnconscious = true
			}
			models.DB.Save(card)

		case "mp":
			s := card.Stats.Data
			s.MP = clamp(s.MP+upd.Delta, 0, s.MaxMP)
			card.Stats.Data = s
			models.DB.Save(card)

		case "cthulhu_mythos":
			// ── 克苏鲁神话技能增长 → 降低最大SAN上限 ─────────────────────────
			if upd.Delta > 0 {
				card.CthulhuMythosSkill = clamp(card.CthulhuMythosSkill+upd.Delta, 0, 99)
				newMaxSAN := 99 - card.CthulhuMythosSkill
				s := card.Stats.Data
				if s.MaxSAN > newMaxSAN {
					s.MaxSAN = newMaxSAN
				}
				if s.SAN > s.MaxSAN {
					s.SAN = s.MaxSAN
				}
				card.Stats.Data = s
				log.Printf("[editor] %s: cthulhu_mythos=%d, new MaxSAN=%d", card.Name, card.CthulhuMythosSkill, newMaxSAN)
				models.DB.Save(card)
			}

		case "skills":
			skills := card.Skills.Data
			if skills == nil {
				skills = make(map[string]int)
			}
			if upd.AddValue != "" {
				skills[upd.AddValue] = clamp(skills[upd.AddValue]+upd.Delta, 0, 99)
			}
			card.Skills.Data = skills
			models.DB.Save(card)

		case "spells":
			if upd.AddValue != "" {
				spells := card.Spells.Data
				// Avoid duplicates.
				for _, sp := range spells {
					if sp == upd.AddValue {
						return
					}
				}
				card.Spells.Data = append(spells, upd.AddValue)
				models.DB.Save(card)
			}

		case "social_relations":
			if upd.AddValue != "" {
				parts := strings.SplitN(upd.AddValue, "|", 3)
				rel := models.SocialRelation{}
				if len(parts) >= 1 {
					rel.Name = parts[0]
				}
				if len(parts) >= 2 {
					rel.Relationship = parts[1]
				}
				if len(parts) >= 3 {
					rel.Note = parts[2]
				}
				card.SocialRelations.Data = append(card.SocialRelations.Data, rel)
				models.DB.Save(card)
			}
		}
		return
	}
	log.Printf("[editor] character %q not found in session players", upd.CharacterName)
}

// applyMadnessToCard sets the madness fields on a CharacterCard based on MadnessKind.
// It rolls a madness symptom and updates the card in memory (caller must DB.Save).
func applyMadnessToCard(card *models.CharacterCard, kind game.MadnessKind) {
	switch kind {
	case game.MadnessPermanent:
		card.MadnessState = "permanent"
		sym := game.RollMadnessSymptom(false)
		card.MadnessSymptom = sym.Description
		card.MadnessDuration = 0
		log.Printf("[editor] %s: PERMANENT madness — SAN=0", card.Name)

	case game.MadnessIndefinite:
		card.MadnessState = "indefinite"
		sym := game.RollMadnessSymptom(false)
		card.MadnessSymptom = sym.Description
		card.MadnessDuration = 1 // flagged as active
		log.Printf("[editor] %s: indefinite madness triggered (daily loss threshold)", card.Name)

	case game.MadnessTemporary:
		// Temporary madness: roll INT check — if pass, character develops symptom; if fail, memory suppression
		intCheck := game.SkillCheck(card.Stats.Data.INT)
		if intCheck.Success {
			card.MadnessState = "temporary"
			sym := game.RollMadnessSymptom(true) // instantaneous (bystanders present)
			card.MadnessSymptom = sym.Description
			card.MadnessDuration = 1
			log.Printf("[editor] %s: temporary madness — INT check passed, symptom rolled", card.Name)
		} else {
			// Failed INT check → memory suppression, no visible madness (but sanity is still lost)
			log.Printf("[editor] %s: temporary madness suppressed by INT check failure", card.Name)
		}
	}
}

func applyNPCUpdate(upd CharacterUpdate, sessionID uint, tempNPCs []models.SessionNPC, scenarioNPCs []models.NPCData) {
	// Try in-memory list first (same pipeline run), then fall back to DB.
	for i := range tempNPCs {
		if npcNameMatch(tempNPCs[i].Name, upd.CharacterName) {
			applyNPCStatUpdate(&tempNPCs[i], upd)
			models.DB.Save(&tempNPCs[i])
			return
		}
	}
	// DB lookup.
	var npc models.SessionNPC
	if err := models.DB.Where("session_id = ? AND name = ?", sessionID, upd.CharacterName).First(&npc).Error; err != nil {
		// Exact DB match failed — try fuzzy match against in-memory session NPCs first.
		for i := range tempNPCs {
			if npcNameMatch(tempNPCs[i].Name, upd.CharacterName) {
				applyNPCStatUpdate(&tempNPCs[i], upd)
				models.DB.Save(&tempNPCs[i])
				return
			}
		}
		// If not found in session, materialize from static scenario NPC so KP can update/kill it.
		for _, sNPC := range scenarioNPCs {
			if !npcNameMatch(sNPC.Name, upd.CharacterName) {
				continue
			}
			npc = models.SessionNPC{
				SessionID:   sessionID,
				Name:        sNPC.Name,
				Description: sNPC.Description,
				Attitude:    sNPC.Attitude,
				Stats:       models.JSONField[map[string]int]{Data: sNPC.Stats},
				Skills:      models.JSONField[map[string]int]{Data: map[string]int{}},
				Spells:      models.JSONField[[]string]{Data: []string{}},
				IsAlive:     true,
			}
			models.DB.Create(&npc)
			break
		}
		if npc.ID == 0 {
			log.Printf("[editor] NPC %q not found in session %d", upd.CharacterName, sessionID)
			return
		}
	}
	applyNPCStatUpdate(&npc, upd)
	models.DB.Save(&npc)
}

func applyNPCStatUpdate(npc *models.SessionNPC, upd CharacterUpdate) {
	log.Printf("[editor] applying NPC %s.%s delta=%d", npc.Name, upd.Field, upd.Delta)
	stats := npc.Stats.Data
	if stats == nil {
		stats = make(map[string]int)
	}
	field := strings.ToLower(upd.Field)
	if field == "hp" || field == "san" || field == "mp" {
		key := strings.ToUpper(field)
		curr := 0
		if v, ok := stats[key]; ok {
			curr = v
		} else if v, ok := stats[field]; ok {
			curr = v
		}
		curr += upd.Delta
		if curr < 0 {
			curr = 0
		}
		stats[key] = curr
		delete(stats, field)
		if field == "hp" && curr == 0 {
			npc.IsAlive = false
		} else if field == "hp" && curr > 0 {
			npc.IsAlive = true
		}
		npc.Stats.Data = stats
	}
}

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
