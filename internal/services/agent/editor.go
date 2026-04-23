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

const editorDefaultPrompt = `你是COC TRPG人物卡编辑器。你会收到一组状态变化指令，和当前所有角色的状态。
请解析每条变化，输出精确的JSON更新列表。

仅输出JSON，不要任何额外文字：
{
  "updates": [
    {
      "character_name": "角色名",
      "field": "字段名",
      "delta": 0,
      "add_value": "",
      "is_npc": false
    }
  ],
  "new_npcs": []
}

field 可选值和说明：
- "san" / "hp" / "mp"：数值变化，填 delta（正数增加，负数减少）
- "cthulhu_mythos"：克苏鲁神话技能提升（阅读神话典籍/首次目击怪物），填 delta=提升点数
- "skills"：技能变化，填 add_value="技能名" + delta=变化量
- "spells"：学会新法术，填 add_value="法术名"，delta=0
- "social_relations"：添加社会关系，填 add_value="姓名|关系|备注"，delta=0

new_npcs 在需要创建新临时NPC时填写（如引入新怪物、新NPC）：
[{"name":"xxx","description":"xxx","stats":{"hp":10},"skills":{"格斗":40}}]

注意：
- 若同一角色有多项变化，输出多条记录
- 若变化描述的对象是临时NPC（怪物/配角），设 is_npc=true
- 若变化不涉及任何数值更新（如纯叙事），则 updates 和 new_npcs 均为空数组`

func runEditor(
	ctx context.Context,
	h agentHandle,
	stateChanges []string,
	players []models.SessionPlayer,
	tempNPCs []models.SessionNPC,
) (EditorResult, error) {
	if len(stateChanges) == 0 {
		return EditorResult{}, nil
	}

	// Build a compact snapshot of current character states.
	var sb strings.Builder
	sb.WriteString("当前调查员状态：\n")
	for _, p := range players {
		card := p.CharacterCard
		sb.WriteString(fmt.Sprintf("• %s  HP:%d/%d SAN:%d/%d MP:%d/%d\n",
			card.Name,
			card.Stats.Data.HP, card.Stats.Data.MaxHP,
			card.Stats.Data.SAN, card.Stats.Data.MaxSAN,
			card.Stats.Data.MP, card.Stats.Data.MaxMP,
		))
		if len(card.Spells.Data) > 0 {
			sb.WriteString(fmt.Sprintf("  已知法术：%s\n", strings.Join(card.Spells.Data, "、")))
		}
	}
	if len(tempNPCs) > 0 {
		sb.WriteString("\n临时NPC状态：\n")
		for _, npc := range tempNPCs {
			alive := "存活"
			if !npc.IsAlive {
				alive = "已死亡"
			}
			hp := npc.Stats.Data["hp"]
			sb.WriteString(fmt.Sprintf("• %s [%s] HP:%d\n", npc.Name, alive, hp))
		}
	}

	changesText := strings.Join(stateChanges, "\n")
	userPrompt := fmt.Sprintf("状态变化指令：\n%s\n\n%s", changesText, sb.String())

	msgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(editorDefaultPrompt)},
		{Role: "user", Content: userPrompt},
	}

	resp, err := h.provider.Chat(ctx, msgs)
	if err != nil {
		return EditorResult{}, fmt.Errorf("editor LLM error: %w", err)
	}

	resp = llm.StripCodeFence(resp)
	var result EditorResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		log.Printf("[editor] JSON parse error: %v (response: %.200s)", err, resp)
		return EditorResult{}, fmt.Errorf("editor JSON parse error: %w", err)
	}
	return result, nil
}

// applyEditorResult writes all updates from EditorResult to the database.
// It updates CharacterCard stats/spells/social_relations and creates new SessionNPCs.
func applyEditorResult(result EditorResult, sessionID uint, players []models.SessionPlayer, tempNPCs []models.SessionNPC) {
	for _, upd := range result.Updates {
		if upd.IsNPC {
			applyNPCUpdate(upd, sessionID, tempNPCs)
		} else {
			applyCharacterUpdate(upd, players)
		}
	}

	for _, newNPC := range result.NewNPCs {
		if newNPC.Name == "" {
			continue
		}
		log.Printf("[editor] creating new NPC: %s", newNPC.Name)
		npc := models.SessionNPC{
			SessionID:   sessionID,
			Name:        newNPC.Name,
			Description: newNPC.Description,
			IsAlive:     true,
		}
		npc.Stats.Data = newNPC.Stats
		npc.Skills.Data = newNPC.Skills
		models.DB.Create(&npc)
	}
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

func applyNPCUpdate(upd CharacterUpdate, sessionID uint, tempNPCs []models.SessionNPC) {
	// Try in-memory list first (same pipeline run), then fall back to DB.
	for i := range tempNPCs {
		if tempNPCs[i].Name == upd.CharacterName {
			applyNPCStatUpdate(&tempNPCs[i], upd)
			models.DB.Save(&tempNPCs[i])
			return
		}
	}
	// DB lookup.
	var npc models.SessionNPC
	if err := models.DB.Where("session_id = ? AND name = ?", sessionID, upd.CharacterName).First(&npc).Error; err != nil {
		log.Printf("[editor] NPC %q not found in session %d", upd.CharacterName, sessionID)
		return
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
		stats[field] = stats[field] + upd.Delta
		if stats[field] < 0 {
			stats[field] = 0
		}
		if field == "hp" && stats["hp"] == 0 {
			npc.IsAlive = false
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
