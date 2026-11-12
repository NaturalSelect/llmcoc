// NOTE: npc_table.go 给扮演 NPC 的 AI 玩家准备“坐在桌边能看到的东西”：场景卡和自上次发言以来的公开牌桌记录。
package agent

import (
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
)

const (
	// NOTE: 单次最多回看的消息条数与渲染字符预算，防止 NPC 久未发言时一次塞进整局记录。
	npcTableLogFetchLimit = 200
	npcTableLogRunes      = 4000
	npcSceneAppearanceMax = 60
)

// buildNPCScene 渲染场景卡：游戏内时间、该 NPC 的位置、同一位置上存活的调查员和其他存活 NPC。
// NPC 没有位置记录时不列名单，谁在场以 KP 的描述为准。
func buildNPCScene(gctx GameContext, npcName string, tempNPCs []models.SessionNPC) string {
	var sb strings.Builder
	sb.WriteString("时间:" + describeGameTime(gctx.Session.GameStartTime, gctx.Session.GameTime) + "\n")

	loc := ""
	for _, npc := range tempNPCs {
		if npc.Name == npcName {
			loc = strings.TrimSpace(npc.Location)
			break
		}
	}
	if loc == "" {
		sb.WriteString("你的角色所在位置:未记录，谁在场以KP的描述为准\n")
		return sb.String()
	}
	sb.WriteString("你的角色所在位置:" + loc + "\n")

	var pcs []string
	for _, pl := range gctx.Session.Players {
		card := pl.CharacterCard
		if card.Name == "" || card.WoundState == "dead" || strings.TrimSpace(pl.Location) != loc {
			continue
		}
		entry := card.Name
		if app := []rune(strings.TrimSpace(card.Appearance)); len(app) > 0 {
			if len(app) > npcSceneAppearanceMax {
				app = append(app[:npcSceneAppearanceMax], '…')
			}
			entry += "(" + string(app) + ")"
		}
		pcs = append(pcs, entry)
	}
	var npcs []string
	for _, npc := range tempNPCs {
		if npc.Name == npcName || npcCompactState(npc) == "dead" || strings.TrimSpace(npc.Location) != loc {
			continue
		}
		npcs = append(npcs, npc.Name)
	}
	if len(pcs) > 0 {
		sb.WriteString("同处此地的调查员:" + strings.Join(pcs, "、") + "\n")
	}
	if len(npcs) > 0 {
		sb.WriteString("同处此地的其他NPC:" + strings.Join(npcs, "、") + "\n")
	}
	return sb.String()
}

// loadNPCTableLog 读取 id>after 的公开消息并渲染成牌桌记录，返回文本和新的已读水位。
// 遇到第一条还在生成的 writer 就停下，它和之后的内容留到下一次；DB 出错时当作没有新记录。
func loadNPCTableLog(sessionID, after uint) (string, uint) {
	var recent []models.Message
	if err := models.DB.Where("session_id = ? AND id > ? AND role != ?", sessionID, after, models.MessageRoleSystem).
		Order("id DESC").
		Limit(npcTableLogFetchLimit).
		Find(&recent).Error; err != nil {
		return "", after
	}

	seen := after
	var lines []string
	for i := len(recent) - 1; i >= 0; i-- {
		m := recent[i]
		if m.Kind == models.MessageKindWriter && m.Status == models.MessageStatusPending {
			break
		}
		seen = m.ID
		if renderableMessage(m) {
			lines = append(lines, renderMessageLine(m))
		}
	}

	used, start := 0, len(lines)
	for start > 0 {
		n := len([]rune(lines[start-1])) + 1
		if used+n > npcTableLogRunes && start < len(lines) {
			break
		}
		used += n
		start--
	}
	if len(lines) == 0 {
		return "", seen
	}
	var sb strings.Builder
	if start > 0 {
		sb.WriteString(fmt.Sprintf("(更早的%d条记录已省略)\n", start))
	}
	for _, l := range lines[start:] {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	return sb.String(), seen
}
