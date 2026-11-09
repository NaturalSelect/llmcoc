// NOTE: game_events.go 把工具结算翻译成玩家可见的 models.GameEvent。只处理调查员；NPC 的变化、
// 暗骰的技能与点数、时间推进的理由、遭遇的参与者都不进事件，避免通过前端或接口泄露给玩家。
package agent

import (
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
)

// investigatorIndex 返回调查员在 players 里的下标，找不到(含 NPC)时返回 -1。
func investigatorIndex(players []models.SessionPlayer, name string) int {
	if name == "" {
		return -1
	}
	for i := range players {
		if players[i].CharacterCard.Name == name {
			return i
		}
	}
	return -1
}

func intPtr(v int) *int { return &v }

func statEvent(name, field string, from, to int) models.GameEvent {
	return models.GameEvent{Type: "stat", Character: name, Field: field, From: intPtr(from), To: intPtr(to)}
}

// diceEvent 生成一次骰子事件；暗骰只保留 hidden 标记，公开骰的对象不是调查员时不写角色名。
// 没有检定名的骰子(伤害、损失等)带上表达式，否则玩家只看到一个孤零零的点数。
func diceEvent(r DiceCheckResult, players []models.SessionPlayer) models.GameEvent {
	if r.Hidden {
		return models.GameEvent{Type: "dice", Hidden: true}
	}
	ev := models.GameEvent{Type: "dice", Skill: r.What, Roll: r.Roll, Level: r.Level}
	if strings.TrimSpace(r.What) == "" {
		ev.Expr = strings.TrimSpace(r.DiceExpr)
	}
	if investigatorIndex(players, r.Character) >= 0 {
		ev.Character = r.Character
	}
	return ev
}

// cardStatEvents 比对一张调查员卡执行前后的数值与伤势，按固定顺序产出变化。
func cardStatEvents(before, after models.CharacterCard) []models.GameEvent {
	name := after.Name
	b, a := before.Stats.Data, after.Stats.Data
	var out []models.GameEvent
	fields := []struct {
		field    string
		from, to int
	}{
		{"hp", b.HP, a.HP},
		{"max_hp", b.MaxHP, a.MaxHP},
		{"san", b.SAN, a.SAN},
		{"max_san", b.MaxSAN, a.MaxSAN},
		{"mp", b.MP, a.MP},
		{"max_mp", b.MaxMP, a.MaxMP},
		{"pow", b.POW, a.POW},
		{"cthulhu_mythos", before.CthulhuMythosSkill, after.CthulhuMythosSkill},
		{"str", b.STR, a.STR},
		{"con", b.CON, a.CON},
		{"siz", b.SIZ, a.SIZ},
		{"dex", b.DEX, a.DEX},
		{"app", b.APP, a.APP},
		{"int", b.INT, a.INT},
		{"edu", b.EDU, a.EDU},
	}
	for _, f := range fields {
		if f.from != f.to {
			out = append(out, statEvent(name, f.field, f.from, f.to))
		}
	}
	if woundState(before.WoundState) != woundState(after.WoundState) {
		out = append(out, models.GameEvent{Type: "wound", Character: name, State: woundState(after.WoundState)})
	}
	if before.IsUnconscious != after.IsUnconscious {
		// NOTE: 濒死/死亡本身就意味着昏迷，不再重复一条昏迷事件。
		if after.IsUnconscious {
			if s := woundState(after.WoundState); s != "dying" && s != "dead" {
				out = append(out, models.GameEvent{Type: "wound", Character: name, State: "unconscious"})
			}
		} else {
			out = append(out, models.GameEvent{Type: "wound", Character: name, State: "conscious"})
		}
	}
	return out
}

func woundState(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// listEntry 是一个列表条目的快照：Key 用于匹配同一条目，Label 是给玩家看的文字。
type listEntry struct {
	Key   string
	Label string
}

// listDiffEvents 按 Key 比对两份快照：新增 add、消失 remove、Label 变了 change(Detail 为"旧 → 新")。
func listDiffEvents(name, category string, before, after []listEntry) []models.GameEvent {
	old := make(map[string]string, len(before))
	for _, e := range before {
		old[e.Key] = e.Label
	}
	seen := make(map[string]bool, len(after))
	var out []models.GameEvent
	for _, e := range after {
		seen[e.Key] = true
		prev, ok := old[e.Key]
		switch {
		case !ok:
			out = append(out, models.GameEvent{Type: "item", Character: name, Category: category, Op: "add", Name: e.Label})
		case prev != e.Label:
			out = append(out, models.GameEvent{Type: "item", Character: name, Category: category, Op: "change", Name: e.Key, Detail: fmt.Sprintf("%s → %s", prev, e.Label)})
		}
	}
	for _, e := range before {
		if !seen[e.Key] {
			out = append(out, models.GameEvent{Type: "item", Character: name, Category: category, Op: "remove", Name: e.Label})
		}
	}
	return out
}

func inventoryEntries(card models.CharacterCard) []listEntry {
	out := make([]listEntry, 0, len(card.Inventory.Data))
	for _, it := range card.Inventory.Data {
		out = append(out, listEntry{Key: inventoryItemBaseName(it), Label: it})
	}
	return out
}

func stringEntries(list []string) []listEntry {
	out := make([]listEntry, 0, len(list))
	for _, s := range list {
		out = append(out, listEntry{Key: s, Label: s})
	}
	return out
}

func assetEntries(card models.CharacterCard) []listEntry {
	out := make([]listEntry, 0, len(card.Assets.Data))
	for _, a := range card.Assets.Data {
		label := a.Name
		if a.Category != "" {
			label = fmt.Sprintf("%s(%s)", a.Name, a.Category)
		}
		out = append(out, listEntry{Key: a.Name, Label: label})
	}
	return out
}

func relationEntries(p models.SessionPlayer) []listEntry {
	rels := effectiveRelations(p.CharacterCard.SocialRelations.Data, p.SessionRelations.Data)
	out := make([]listEntry, 0, len(rels))
	for _, r := range rels {
		label := r.Name
		if r.Relationship != "" {
			label = fmt.Sprintf("%s(%s)", r.Name, r.Relationship)
		}
		out = append(out, listEntry{Key: r.Name, Label: label})
	}
	return out
}

// listChangeEvents 在 apply 前后对调查员的某个列表做快照比对；找不到调查员时直接执行、不产出事件。
func listChangeEvents(players []models.SessionPlayer, name, category string, snap func(models.SessionPlayer) []listEntry, apply func()) []models.GameEvent {
	idx := investigatorIndex(players, name)
	if idx < 0 {
		apply()
		return nil
	}
	before := snap(players[idx])
	apply()
	return listDiffEvents(name, category, before, snap(players[idx]))
}
