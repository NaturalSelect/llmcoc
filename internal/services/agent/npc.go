// NOTE: npc.go 只放 NPC 的建卡、销毁和状态辅助；NPC 由 Director 直接扮演，没有独立的 NPC Agent。
package agent

import (
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
)

func npcDisplayState(npc models.SessionNPC) string {
	woundState := strings.TrimSpace(npc.WoundState)
	switch woundState {
	case "major":
		return "重伤"
	case "dying":
		return "濒死/可抢救"
	case "dead":
		return "已死亡"
	}
	if !npc.IsAlive {
		return "已死亡/失能"
	}
	return "存活"
}

func npcCompactState(npc models.SessionNPC) string {
	switch strings.TrimSpace(npc.WoundState) {
	case "major", "dying", "dead":
		return npc.WoundState
	}
	if !npc.IsAlive {
		return "dead"
	}
	return "alive"
}

func createNPC(sessionID uint, card *NPCCard) string {
	if card == nil || strings.TrimSpace(card.Name) == "" {
		return "创建NPC失败:char_card.name 不能为空"
	}

	name := strings.TrimSpace(card.Name)
	race := strings.TrimSpace(card.Race)
	if race == "" {
		race = "人类"
	}
	desc := strings.TrimSpace(card.Description)
	att := strings.TrimSpace(card.Attitude)
	goal := strings.TrimSpace(card.Goal)
	secret := strings.TrimSpace(card.Secret)
	riskPref := strings.TrimSpace(card.RiskPreference)
	if desc == "" {
		desc = "(无描述)"
	}

	var existing models.SessionNPC
	err := models.DB.Where("session_id = ? AND name = ?", sessionID, name).First(&existing).Error
	if err == nil {
		existing.Description = desc
		existing.Race = race
		existing.Occupation = ""
		existing.CthulhuMythosSkill = 0
		existing.Attitude = att
		existing.Goal = goal
		existing.Secret = secret
		existing.RiskPref = riskPref
		existing.WoundState = "none"
		existing.IsAlive = true
		existing.Stats.Data = card.Stats
		existing.Skills.Data = card.Skills
		existing.Spells.Data = card.Spells
		_ = models.DB.Save(&existing).Error
		return fmt.Sprintf("已更新NPC:%s(种族:%s,态度:%s)", name, race, att)
	}

	npc := models.SessionNPC{
		SessionID:   sessionID,
		Name:        name,
		Race:        race,
		Description: desc,
		Attitude:    att,
		Goal:        goal,
		Secret:      secret,
		RiskPref:    riskPref,
		Stats:       models.JSONField[map[string]int]{Data: card.Stats},
		Skills:      models.JSONField[map[string]int]{Data: card.Skills},
		Spells:      models.JSONField[[]string]{Data: card.Spells},
		WoundState:  "none",
		IsAlive:     true,
	}
	if err := models.DB.Create(&npc).Error; err != nil {
		return "创建NPC失败:数据库写入失败"
	}
	if att != "" {
		return fmt.Sprintf("已创建NPC:%s(态度:%s)", name, att)
	}
	return fmt.Sprintf("已创建NPC:%s", name)
}

func destroyNPC(sessionID uint, name string, reason string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "销毁NPC失败:name 不能为空"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "out_of_range"
	}

	if reason == "dead" {
		var npc models.SessionNPC
		if err := models.DB.Where("session_id = ? AND name = ?", sessionID, name).First(&npc).Error; err != nil {
			return fmt.Sprintf("未找到NPC:%s", name)
		}
		stats := npc.Stats.Data
		if stats == nil {
			stats = make(map[string]int)
		}
		stats["HP"] = 0
		delete(stats, "hp")
		npc.Stats.Data = stats
		npc.WoundState = "dead"
		npc.IsAlive = false
		_ = models.DB.Save(&npc).Error
		return fmt.Sprintf("已标记NPC:%s(死亡)", name)
	}

	res := models.DB.Where("session_id = ? AND name = ?", sessionID, name).Delete(&models.SessionNPC{})
	if res.RowsAffected == 0 {
		return fmt.Sprintf("未找到NPC:%s", name)
	}
	return fmt.Sprintf("已销毁NPC:%s", name)
}
