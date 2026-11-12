// NOTE: npc_table.go 给扮演 NPC 的 AI 玩家准备场景卡：游戏内时间、它的位置、同地点的调查员和其他 NPC。
package agent

import (
	"strings"

	"github.com/llmcoc/server/internal/models"
)

const npcSceneAppearanceMax = 60

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
