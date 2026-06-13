// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
)

// parseStateChange parses a director change string (e.g. "HP -3(角色名)" or
// "cthulhu_mythos +1(角色名)") into a CharacterUpdate.
// Supported fields: HP, SAN, MP, POW, cthulhu_mythos, race, occupation, wound_state.
// Returns false if the string cannot be matched to a known field.
var stateChangeFields = []string{
	"cthulhu_mythos", "wound_state", "HP", "SAN", "MP", "POW", "race", "occupation",
	"str", "con", "siz", "dex", "app", "int", "edu",
}

func parseStateChange(change string) (CharacterUpdate, string, bool) {
	change = strings.TrimSpace(change)
	// Check longest field name first to avoid prefix collisions.
	for _, field := range stateChangeFields {
		if !strings.HasPrefix(strings.ToUpper(change), strings.ToUpper(field)) {
			continue
		}
		rest := strings.TrimSpace(change[len(field):])
		var deltaStr, charName string
		if idx := strings.Index(rest, "("); idx >= 0 {
			deltaStr = strings.TrimSpace(rest[:idx])
			charName = strings.TrimSuffix(strings.TrimPrefix(rest[idx:], "("), ")")
		} else {
			deltaStr = rest
		}

		if strings.ToLower(field) == "race" || strings.ToLower(field) == "occupation" || strings.ToLower(field) == "wound_state" {
			// For string fields, deltaStr is actually the new value string.
			return CharacterUpdate{
				CharacterName: charName,
				Field:         strings.ToLower(field),
				NewValue:      strings.TrimSpace(deltaStr),
			}, "", true
		}

		var delta int
		// Enforce +/- sign for numeric fields: positive must have '+', negative must have '-'.
		// Plain numbers without a sign (e.g. "40") are rejected.
		if !strings.HasPrefix(deltaStr, "+") && !strings.HasPrefix(deltaStr, "-") {
			errMsg := fmt.Sprintf("[%s] 数值字段缺少+/-符号: %q，正确格式如 %s +40(角色名) 或 %s -3(角色名)",
				field, change, field, field)
			log.Printf("[editor] %s", errMsg)
			return CharacterUpdate{}, errMsg, false
		}
		_, scanErr := fmt.Sscanf(deltaStr, "%d", &delta)
		if scanErr != nil {
			errMsg := fmt.Sprintf("[%s] 无法解析数值: %q，正确格式如 %s +40(角色名) 或 %s -3(角色名)",
				field, deltaStr, field, field)
			log.Printf("[editor] %s", errMsg)
			return CharacterUpdate{}, errMsg, false
		}
		return CharacterUpdate{
			CharacterName: charName,
			Field:         strings.ToLower(field),
			Delta:         delta,
		}, "", true
	}
	errMsg := fmt.Sprintf("无法识别的变更字段: %q，支持的字段: HP, SAN, MP, POW, STR, CON, SIZ, DEX, APP, INT, EDU, cthulhu_mythos, race, occupation, wound_state。正确格式如 HP -3(角色名)", change)
	log.Printf("[editor] %s", errMsg)
	return CharacterUpdate{}, errMsg, false
}

func applyCharacterUpdate(upd CharacterUpdate, players []models.SessionPlayer) {
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != upd.CharacterName {
			continue
		}
		log.Printf("[editor] applying %s.%s delta=%d add=%q", card.Name, upd.Field, upd.Delta, upd.AddValue)

		isHuman := card.Race == "" || card.Race == "人类"
		switch strings.ToLower(upd.Field) {
		case "san":
			s := card.Stats.Data
			prevSAN := s.SAN
			newSAN := s.SAN + upd.Delta
			s.SAN = clamp(newSAN, 0, card.Stats.Data.MaxSAN)
			card.Stats.Data = s

			// ── 理智损失事件:检查疯狂触发 ──────────────────────────────────────
			if upd.Delta < 0 {
				sanLoss := prevSAN - s.SAN // actual points lost (positive)
				if sanLoss > 0 {
					card.DailySanLoss += sanLoss

					// 潜在疯狂期(临时/不定性):哪怕只损失1点SAN,立即再次触发疯狂发作
					if card.MadnessState == "temporary" || card.MadnessState == "indefinite" {
						// Re-roll a new symptom for the relapse episode.
						sym := game.RollMadnessSymptom(true)
						card.MadnessSymptom = sym.Description
						card.MadnessDuration = sym.Duration
						log.Printf("[editor] %s: latent madness relapse triggered (state=%s, sanLoss=%d)", card.Name, card.MadnessState, sanLoss)
					} else {
						kind := game.EvalMadness(sanLoss, s.SAN, card.DailySanLoss, s.MaxSAN)
						applyMadnessToCard(card, kind)
					}
				}
			}
			models.DB.Save(card)

		case "wound_state":
			s := card.Stats.Data
			switch strings.ToLower(strings.TrimSpace(upd.NewValue)) {
			case "none":
				card.WoundState = "none"
				if s.HP > 0 {
					card.IsUnconscious = false
				}
			case "major":
				card.WoundState = "major"
			case "dying":
				card.WoundState = "dying"
				card.IsUnconscious = true
				if s.HP > 0 {
					s.HP = 0
					card.Stats.Data = s
				}
			case "dead":
				card.WoundState = "dead"
				card.IsUnconscious = true
				if s.HP > 0 {
					s.HP = 0
					card.Stats.Data = s
				}
			default:
				log.Printf("[editor] %s: invalid wound_state %q", card.Name, upd.NewValue)
				continue
			}
			models.DB.Save(card)

		case "hp":
			s := card.Stats.Data
			wasDead := card.WoundState == "dead"
			damage := 0
			if upd.Delta < 0 {
				damage = -upd.Delta
			}
			s.HP = clamp(s.HP+upd.Delta, 0, s.MaxHP)
			card.Stats.Data = s

			// Positive HP changes can represent healing or supernatural revival. If a
			// dead investigator is restored above 0 HP, clear dead/unconscious so they
			// can participate in later multiplayer rounds again.
			if upd.Delta > 0 && wasDead && s.HP > 0 {
				card.WoundState = "none"
				card.IsUnconscious = false
				log.Printf("[editor] %s: revived by HP change %+d", card.Name, upd.Delta)
			}

			// ── 伤害事件:检查重伤/濒死/即死 ─────────────────────────────────
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
			} else if upd.Delta > 0 && (s.HP == s.MaxHP || upd.Delta >= s.MaxHP/2) {
				card.WoundState = "none"
				card.IsUnconscious = false
			}
			// HP归零且已有重伤 → 濒死(需急救)
			if s.HP <= 0 && card.WoundState == "major" && card.WoundState != "dead" {
				card.WoundState = "dying"
				card.IsUnconscious = true
			} else if s.HP <= 0 && card.WoundState == "none" {
				// 仅轻伤HP归零 → 昏迷,不会直接死亡
				card.IsUnconscious = true
			}
			models.DB.Save(card)

		case "mp":
			s := card.Stats.Data
			s.MP = clamp(s.MP+upd.Delta, 0, s.MaxMP)
			card.Stats.Data = s
			models.DB.Save(card)

		case "pow":
			// POW changes affect MaxMP (MaxMP = POW/5) and current MP proportionally.
			s := card.Stats.Data
			oldPOW := s.POW
			s.POW = clamp(s.POW+upd.Delta, 1, 500)
			newMaxMP := s.POW / 5
			if newMaxMP < 1 {
				newMaxMP = 1
			}
			// Scale current MP proportionally if MaxMP changes.
			if oldPOW > 0 && s.MaxMP > 0 {
				s.MP = clamp(s.MP*newMaxMP/s.MaxMP, 0, newMaxMP)
			}
			s.MaxMP = newMaxMP
			card.Stats.Data = s
			log.Printf("[editor] %s: POW %d→%d, MaxMP→%d", card.Name, oldPOW, s.POW, newMaxMP)
			models.DB.Save(card)

		case "cthulhu_mythos", "cthulhu_mythos_skill":
			// ── 克苏鲁神话技能增长 → 降低最大SAN上限 ─────────────────────────
			if upd.Delta > 0 {
				maxVal := 99
				card.CthulhuMythosSkill = clamp(card.CthulhuMythosSkill+upd.Delta, 0, maxVal)
				s := card.Stats.Data
				newMaxSAN := 99 - card.CthulhuMythosSkill
				if isHuman {
					if s.MaxSAN > newMaxSAN {
						s.MaxSAN = newMaxSAN
					}
					if s.SAN > s.MaxSAN {
						s.SAN = s.MaxSAN
					}
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

		case "race":
			card.Race = upd.NewValue
			if card.Race != "" && card.Race != "人类" {
				card.Stats.Data.MaxSAN = 99 // 非人类的最大SAN上限为99,且不受克苏鲁神话技能影响
			}
			models.DB.Save(card)
		case "occupation":
			card.Occupation = upd.NewValue
			models.DB.Save(card)
		case "str", "con", "siz", "dex", "app", "int", "edu":
			s := card.Stats.Data
			needUpdateMaxHp := false
			needUpdateDb := false
			needUpdateMov := false
			switch strings.ToLower(upd.Field) {
			case "str":
				s.STR = clamp(s.STR+upd.Delta, 1, 99)
				needUpdateMaxHp = true
				needUpdateMov = true
				needUpdateDb = true
			case "con":
				s.CON = clamp(s.CON+upd.Delta, 1, 99)
				needUpdateMaxHp = true
			case "siz":
				s.SIZ = clamp(s.SIZ+upd.Delta, 1, 99)
				needUpdateMaxHp = true
				needUpdateDb = true
				needUpdateMov = true
			case "dex":
				s.DEX = clamp(s.DEX+upd.Delta, 1, 99)
			case "app":
				s.APP = clamp(s.APP+upd.Delta, 1, 99)
			case "int":
				s.INT = clamp(s.INT+upd.Delta, 1, 99)
			case "edu":
				s.EDU = clamp(s.EDU+upd.Delta, 1, 99)
			}
			if needUpdateMaxHp {
				s.MaxHP = (s.CON + s.SIZ) / 10
				if (s.CON+s.SIZ)%10 != 0 {
					s.MaxHP += 1
				}
			}
			if needUpdateDb {
				combined := s.STR + s.SIZ
				var build int
				var db string
				switch {
				case combined <= 64:
					build, db = -2, "-2"
				case combined <= 84:
					build, db = -1, "-1"
				case combined <= 124:
					build, db = 0, "0"
				case combined <= 164:
					build, db = 1, "1D4"
				case combined <= 204:
					build, db = 2, "1D6"
				case combined <= 284:
					build, db = 3, "2D6"
				default:
					build, db = 4, "2D6+1D6"
				}
				s.Build = build
				s.DB = db
			}
			if needUpdateMov {
				var mov int
				if s.STR > s.SIZ && s.DEX > s.SIZ {
					mov = 9
				} else if s.STR < s.SIZ && s.DEX < s.SIZ {
					mov = 7
				} else {
					mov = 8
				}
				s.MOV = mov
			}
			card.Stats.Data = s
			models.DB.Save(card)
		default:
			log.Printf("[editor] unrecognised field in character update: %q", upd.Field)
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
		card.MadnessDuration = sym.Duration // flagged as active
		log.Printf("[editor] %s: indefinite madness triggered (daily loss threshold)", card.Name)

	case game.MadnessTemporary:
		// Temporary madness: roll INT check — if pass, character develops symptom; if fail, memory suppression
		intCheck := game.SkillCheck(card.Stats.Data.INT)
		if intCheck.Success {
			card.MadnessState = "temporary"
			sym := game.RollMadnessSymptom(true) // instantaneous (bystanders present)
			card.MadnessSymptom = sym.Description
			card.MadnessDuration = sym.Duration
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
				SessionID:          sessionID,
				Name:               sNPC.Name,
				Race:               sNPC.Race,
				Occupation:         sNPC.Occupation,
				Description:        sNPC.Description,
				Attitude:           sNPC.Attitude,
				Stats:              models.JSONField[map[string]int]{Data: sNPC.Stats},
				Skills:             models.JSONField[map[string]int]{Data: map[string]int{}},
				Spells:             models.JSONField[[]string]{Data: []string{}},
				CthulhuMythosSkill: sNPC.CthulhuMythosSkill,
				WoundState:         "none",
				IsAlive:            true,
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
	switch field {
	case "san":
		prev := npcStat(stats, "SAN")
		maxSAN := npcStat(stats, "MaxSAN")
		if maxSAN == 0 {
			maxSAN = 99
		}
		curr := clamp(prev+upd.Delta, 0, maxSAN)
		setNPCStat(stats, "SAN", curr)
		npc.Stats.Data = stats
		log.Printf("[editor] NPC %s: SAN %d→%d", npc.Name, prev, curr)

	case "hp":
		prev := npcStat(stats, "HP")
		maxHP := npcStat(stats, "MaxHP")
		if maxHP == 0 {
			maxHP = prev
		}
		curr := prev + upd.Delta
		if maxHP > 0 {
			curr = clamp(curr, 0, maxHP)
		} else if curr < 0 {
			curr = 0
		}
		damage := 0
		if upd.Delta < 0 {
			damage = -upd.Delta
		}
		wasDead := npc.WoundState == "dead"
		setNPCStat(stats, "HP", curr)
		if damage > 0 {
			switch {
			case maxHP > 0 && game.CheckInstantDeath(damage, maxHP):
				npc.WoundState = "dead"
				npc.IsAlive = false
			case maxHP > 0 && game.CheckMajorWound(damage, maxHP):
				npc.WoundState = "major"
				npc.IsAlive = true
			}
		} else if upd.Delta > 0 && wasDead && curr > 0 {
			npc.WoundState = "none"
			npc.IsAlive = true
		} else if upd.Delta > 0 && maxHP > 0 && (curr == maxHP || upd.Delta >= maxHP/2) {
			npc.WoundState = "none"
			npc.IsAlive = true
		}
		if curr <= 0 && npc.WoundState == "major" {
			npc.WoundState = "dying"
			npc.IsAlive = true
		} else if curr <= 0 && npc.WoundState == "dead" {
			npc.IsAlive = false
		} else if curr > 0 && npc.WoundState != "dead" {
			npc.IsAlive = true
		}
		npc.Stats.Data = stats
		log.Printf("[editor] NPC %s: HP %d→%d", npc.Name, prev, curr)

	case "mp":
		prev := npcStat(stats, "MP")
		maxMP := npcStat(stats, "MaxMP")
		if maxMP == 0 {
			maxMP = prev
		}
		curr := prev + upd.Delta
		if maxMP > 0 {
			curr = clamp(curr, 0, maxMP)
		} else if curr < 0 {
			curr = 0
		}
		setNPCStat(stats, "MP", curr)
		npc.Stats.Data = stats
		log.Printf("[editor] NPC %s: MP %d→%d", npc.Name, prev, curr)

	// case "pow":
	// 	prev := npcStat(stats, "POW")
	// 	oldMaxMP := npcStat(stats, "MaxMP")
	// 	oldMP := npcStat(stats, "MP")
	// 	curr := clamp(prev+upd.Delta, 1, 500)
	// 	newMaxMP := curr / 5
	// 	if newMaxMP < 1 {
	// 		newMaxMP = 1
	// 	}
	// 	if oldMaxMP > 0 {
	// 		setNPCStat(stats, "MP", clamp(oldMP*newMaxMP/oldMaxMP, 0, newMaxMP))
	// 	}
	// 	setNPCStat(stats, "POW", curr)
	// 	setNPCStat(stats, "MaxMP", newMaxMP)
	// 	npc.Stats.Data = stats
	// 	log.Printf("[editor] NPC %s: POW %d→%d, MaxMP→%d", npc.Name, prev, curr, newMaxMP)

	case "cthulhu_mythos", "cthulhu_mythos_skill":
		if upd.Delta > 0 {
			npc.CthulhuMythosSkill = clamp(npc.CthulhuMythosSkill+upd.Delta, 0, 99)
			newMaxSAN := 99 - npc.CthulhuMythosSkill
			isHuman := npc.Race == "" || npc.Race == "人类"
			if isHuman {
				maxSAN := npcStat(stats, "MaxSAN")
				if maxSAN == 0 || maxSAN > newMaxSAN {
					setNPCStat(stats, "MaxSAN", newMaxSAN)
				}
				if san := npcStat(stats, "SAN"); san > newMaxSAN {
					setNPCStat(stats, "SAN", newMaxSAN)
				}
			}
			npc.Stats.Data = stats
			log.Printf("[editor] NPC %s: cthulhu_mythos=%d, new MaxSAN=%d", npc.Name, npc.CthulhuMythosSkill, newMaxSAN)
		}

	case "wound_state":
		switch strings.ToLower(strings.TrimSpace(upd.NewValue)) {
		case "none":
			npc.WoundState = "none"
			if npcStat(stats, "HP") > 0 {
				npc.IsAlive = true
			}
		case "major":
			npc.WoundState = "major"
			npc.IsAlive = true
		case "dying":
			npc.WoundState = "dying"
			npc.IsAlive = true
			setNPCStat(stats, "HP", 0)
		case "dead":
			npc.WoundState = "dead"
			npc.IsAlive = false
			setNPCStat(stats, "HP", 0)
		default:
			log.Printf("[editor] NPC %s: invalid wound_state %q", npc.Name, upd.NewValue)
		}
		npc.Stats.Data = stats
		log.Printf("[editor] NPC %s: wound_state changed to %q", npc.Name, npc.WoundState)

	case "race":
		npc.Race = upd.NewValue
		if npc.Race != "" && npc.Race != "人类" {
			setNPCStat(stats, "MaxSAN", 99)
			npc.Stats.Data = stats
		}
		log.Printf("[editor] NPC %s: race changed to %q", npc.Name, npc.Race)
	case "occupation":
		npc.Occupation = upd.NewValue
		log.Printf("[editor] NPC %s: occupation changed to %q", npc.Name, npc.Occupation)
	// case "str", "con", "siz", "dex", "app", "int", "edu":
	// 	key := strings.ToUpper(field)
	// 	prev := npcStat(stats, key)
	// 	curr := clamp(prev+upd.Delta, 1, 99)
	// 	setNPCStat(stats, key, curr)
	// 	updateNPCDerivedStats(stats, field)
	// 	npc.Stats.Data = stats
	// 	log.Printf("[editor] NPC %s: %s %d→%d", npc.Name, key, prev, curr)
	default:
		log.Printf("[editor] unrecognised field in NPC update: %q", upd.Field)
	}
}

func npcStat(stats map[string]int, key string) int {
	if stats == nil {
		return 0
	}
	for _, candidate := range npcStatKeyCandidates(key) {
		if v, ok := stats[candidate]; ok {
			return v
		}
	}
	return 0
}

func setNPCStat(stats map[string]int, key string, value int) {
	if stats == nil {
		return
	}
	candidates := npcStatKeyCandidates(key)
	stats[candidates[0]] = value
	for _, candidate := range candidates[1:] {
		delete(stats, candidate)
	}
}

func npcStatKeyCandidates(key string) []string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "maxhp", "max_hp":
		return []string{"MaxHP", "max_hp", "MAXHP", "maxHP"}
	case "maxmp", "max_mp":
		return []string{"MaxMP", "max_mp", "MAXMP", "maxMP"}
	case "maxsan", "max_san":
		return []string{"MaxSAN", "max_san", "MAXSAN", "maxSAN"}
	case "luck":
		return []string{"Luck", "LUCK", "luck"}
	case "mov":
		return []string{"MOV", "mov"}
	case "build":
		return []string{"Build", "BUILD", "build"}
	case "hp", "san", "mp", "pow", "str", "con", "siz", "dex", "app", "int", "edu":
		upper := strings.ToUpper(strings.TrimSpace(key))
		return []string{upper, strings.ToLower(upper)}
	default:
		return []string{key, strings.ToUpper(key), strings.ToLower(key)}
	}
}

func updateNPCDerivedStats(stats map[string]int, changedField string) {
	switch strings.ToLower(strings.TrimSpace(changedField)) {
	case "str", "con", "siz", "dex":
	default:
		return
	}
	con := npcStat(stats, "CON")
	siz := npcStat(stats, "SIZ")
	if con > 0 && siz > 0 {
		maxHP := (con + siz) / 10
		if (con+siz)%10 != 0 {
			maxHP++
		}
		setNPCStat(stats, "MaxHP", maxHP)
		if hp := npcStat(stats, "HP"); hp > maxHP {
			setNPCStat(stats, "HP", maxHP)
		}
	}
	str := npcStat(stats, "STR")
	if str > 0 && siz > 0 {
		combined := str + siz
		var build int
		var db int
		switch {
		case combined <= 64:
			build, db = -2, -2
		case combined <= 84:
			build, db = -1, -1
		case combined <= 124:
			build, db = 0, 0
		case combined <= 164:
			build, db = 1, 1
		case combined <= 204:
			build, db = 2, 2
		case combined <= 284:
			build, db = 3, 3
		default:
			build = 4 + (combined-285)/80
			db = build
		}
		setNPCStat(stats, "Build", build)
		setNPCStat(stats, "DB", db)
	}
	dex := npcStat(stats, "DEX")
	if str > 0 && dex > 0 && siz > 0 {
		mov := 8
		if str > siz && dex > siz {
			mov = 9
		} else if str < siz && dex < siz {
			mov = 7
		}
		setNPCStat(stats, "MOV", mov)
	}
}

// TearDeadInvestigators soft-deletes (sets IsActive=false) any character card
// whose WoundState is "dead". Returns the names of torn cards.
// Called at end_game and EndSession to implement COC "撕卡" rules.
func TearDeadInvestigators(players []models.SessionPlayer) []string {
	var torn []string
	for i := range players {
		card := &players[i].CharacterCard
		if (card.WoundState == "dead" || card.Stats.Data.HP <= 0) && card.IsActive {
			card.IsActive = false
			models.DB.Model(card).Update("is_active", false)
			log.Printf("[editor] 撕卡: %s (WoundState=dead)", card.Name)
			torn = append(torn, card.Name)
		}
	}
	return torn
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
