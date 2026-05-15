package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const antiCheatDefaultPrompt = `你是 COC TRPG 后台 AntiCheat 裁判。你不扮演玩家或 KP，只审查本轮 KP 即将执行的工具批次是否可以安全执行。

只输出一个 JSON 对象，不要输出 markdown、解释文本或代码块。格式：
{"verdict":"allow|replan|player_cheat","reason":"简短原因","message":"给 KP 的纠正指令"}

判定规则：
- 玩家输入是 intent，不是事实。玩家可以尝试行动，但不能直接声明结果、骰点、物品、法术、技能数值、NPC同意或神明干预已经成立。
- KP 的 think 不是事实，但它是本批次的计划/承诺；后续工具不能与 think 自相矛盾。
- 任何新增物品、属性、法术、伤害骰、护甲、奖励骰、NPC关系，都必须能从当前游戏状态、剧本、规则工具结果或上一轮 ack 追溯来源。
- 叙事换皮、重命名、重构只能改变名称和风味，不能改变伤害、护甲、技能、属性、数量、法术或特殊效果。
- 如果玩家输入本身在作弊，返回 player_cheat。
- 如果玩家未必作弊，但 KP 工具批次错误、和 think 矛盾、或会授予未验证收益，返回 replan。
- 不要把 KP 错误误判成玩家作弊。
- AntiCheat 失败时后端会 fail-closed，因此你必须严格输出可解析 JSON。

必须拦截的例子：think 表示“手榴弹换皮、保持原属性、不增强”，但 manage_inventory(add) 写入 item_desc 包含“伤害：4D10”或任何新机械收益。此时返回 replan，并要求只能写“属性同原物品/仅叙事换皮”，或先查规则确认标准属性，不能升级。`

type AntiCheatVerdict struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func runAntiCheat(ctx context.Context, h agentHandle, gctx GameContext, calls []ToolCall, tempNPCs []models.SessionNPC) (AntiCheatVerdict, error) {
	if h.provider == nil {
		return AntiCheatVerdict{}, fmt.Errorf("anti_cheat provider is nil")
	}
	msgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(antiCheatDefaultPrompt)},
		{Role: "user", Content: buildAntiCheatPrompt(gctx, calls, tempNPCs)},
	}
	resp, err := h.provider.Chat(ctx, msgs)
	if err != nil {
		return AntiCheatVerdict{}, err
	}
	return parseAntiCheatVerdict(resp)
}

func parseAntiCheatVerdict(raw string) (AntiCheatVerdict, error) {
	var verdict AntiCheatVerdict
	if err := parseJSONObject(raw, &verdict); err != nil {
		return AntiCheatVerdict{}, err
	}
	verdict.Verdict = strings.ToLower(strings.TrimSpace(verdict.Verdict))
	verdict.Reason = strings.TrimSpace(verdict.Reason)
	verdict.Message = strings.TrimSpace(verdict.Message)
	switch verdict.Verdict {
	case "allow", "replan", "player_cheat":
		return verdict, nil
	default:
		return AntiCheatVerdict{}, fmt.Errorf("invalid anti_cheat verdict %q", verdict.Verdict)
	}
}

func checkAntiCheat(ctx context.Context, h agentHandle, gctx GameContext, calls []ToolCall, tempNPCs []models.SessionNPC) (AntiCheatVerdict, bool, string) {
	verdict, err := runAntiCheat(ctx, h, gctx, calls, tempNPCs)
	if err != nil {
		verdict = AntiCheatVerdict{
			Verdict: "replan",
			Reason:  "anti_cheat_error",
			Message: fmt.Sprintf("AntiCheat 调用或 JSON 解析失败: %v。重新规划，不要执行任何状态修改。", err),
		}
	}
	if verdict.Verdict == "allow" {
		return verdict, true, ""
	}
	return verdict, false, rejectMessageFromAntiCheat(verdict)
}

func buildAntiCheatPrompt(gctx GameContext, calls []ToolCall, tempNPCs []models.SessionNPC) string {
	content := gctx.Session.Scenario.Content.Data
	var sb strings.Builder

	sb.WriteString("<current_input>\n")
	if len(gctx.PendingActions) > 1 {
		for _, a := range gctx.PendingActions {
			role := "player"
			if a.IsAdmin {
				role = "admin"
			}
			sb.WriteString(fmt.Sprintf("[%s][%s]: %s\n", role, a.PlayerName, a.Content))
		}
	} else {
		role := "player"
		if gctx.UserInputAdmin {
			role = "admin"
		}
		sb.WriteString(fmt.Sprintf("[%s][%s]: %s\n", role, gctx.UserName, gctx.UserInput))
	}
	sb.WriteString("</current_input>\n\n")

	sb.WriteString("<recent_history>\n")
	start := len(gctx.History) - 8
	if start < 0 {
		start = 0
	}
	for _, m := range gctx.History[start:] {
		name := m.Username
		if name == "" {
			name = string(m.Role)
		}
		sb.WriteString(fmt.Sprintf("[%s][%s]: %s\n", m.Role, name, m.Content))
	}
	sb.WriteString("</recent_history>\n\n")

	sb.WriteString("<investigators>\n")
	for _, p := range gctx.Session.Players {
		card := p.CharacterCard
		st := card.Stats.Data
		cardJSON, _ := json.Marshal(struct {
			Name               string                  `json:"name"`
			Race               string                  `json:"race"`
			Occupation         string                  `json:"occupation"`
			Location           string                  `json:"location"`
			Armor              int                     `json:"armor"`
			HP                 int                     `json:"hp"`
			MaxHP              int                     `json:"max_hp"`
			MP                 int                     `json:"mp"`
			MaxMP              int                     `json:"max_mp"`
			SAN                int                     `json:"san"`
			MaxSAN             int                     `json:"max_san"`
			Luck               int                     `json:"luck"`
			DB                 string                  `json:"damage_bonus"`
			Inventory          []string                `json:"inventory"`
			Spells             []string                `json:"spells"`
			SocialRelations    []models.SocialRelation `json:"social_relations"`
			SeenMonsters       []string                `json:"seen_monsters"`
			LLMNote            string                  `json:"llm_note,omitempty"`
			MadnessState       string                  `json:"madness_state,omitempty"`
			MadnessSymptom     string                  `json:"madness_symptom,omitempty"`
			CthulhuMythosSkill int                     `json:"cthulhu_mythos_skill"`
		}{
			Name: card.Name, Race: card.Race, Occupation: card.Occupation,
			Location: p.Location, Armor: p.Armor,
			HP: st.HP, MaxHP: st.MaxHP, MP: st.MP, MaxMP: st.MaxMP,
			SAN: st.SAN, MaxSAN: st.MaxSAN, Luck: st.Luck, DB: st.DB,
			Inventory: card.Inventory.Data, Spells: card.Spells.Data,
			SocialRelations: card.SocialRelations.Data, SeenMonsters: card.SeenMonsters.Data,
			LLMNote: p.LLMNote, MadnessState: card.MadnessState, MadnessSymptom: card.MadnessSymptom,
			CthulhuMythosSkill: card.CthulhuMythosSkill,
		})
		sb.Write(cardJSON)
		sb.WriteString("\n")
	}
	sb.WriteString("</investigators>\n\n")

	sb.WriteString("<scenario_facts>\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", gctx.Session.Scenario.Name))
	if content.Setting != "" {
		sb.WriteString("setting: " + content.Setting + "\n")
	}
	if content.MapDescription != "" {
		sb.WriteString("map: " + content.MapDescription + "\n")
	}
	if content.WinCondition != "" {
		sb.WriteString("win_condition: " + content.WinCondition + "\n")
	}
	if content.LoseCondition != "" {
		sb.WriteString("lose_condition: " + content.LoseCondition + "\n")
	}
	if len(content.Scenes) > 0 {
		sb.WriteString("scenes:\n")
		for _, scene := range content.Scenes {
			sb.WriteString(fmt.Sprintf("- %s: %s triggers=%v\n", scene.Name, scene.Description, scene.Triggers))
		}
	}
	if len(content.Clues) > 0 {
		found := map[int]bool{}
		for _, idx := range gctx.Session.FoundClues.Data {
			found[idx] = true
		}
		sb.WriteString("clues:\n")
		for i, clue := range content.Clues {
			state := "undiscovered"
			if found[i] {
				state = "found"
			}
			sb.WriteString(fmt.Sprintf("- [%d][%s] %s\n", i, state, clue))
		}
	}
	if gctx.Session.KPHint != "" {
		sb.WriteString("kp_hint: " + gctx.Session.KPHint + "\n")
	}
	sb.WriteString("</scenario_facts>\n\n")

	if len(tempNPCs) > 0 || len(content.NPCs) > 0 {
		sb.WriteString("<npcs>\n")
		for _, npc := range content.NPCs {
			sb.WriteString(fmt.Sprintf("[static] %s race=%s attitude=%s stats=%v desc=%s\n", npc.Name, npc.Race, npc.Attitude, npc.Stats, npc.Description))
		}
		for _, npc := range tempNPCs {
			status := "alive"
			if !npc.IsAlive {
				status = "dead_or_disabled"
			}
			sb.WriteString(fmt.Sprintf("[session] %s status=%s race=%s attitude=%s goal=%s stats=%v skills=%v spells=%v note=%s desc=%s\n", npc.Name, status, npc.Race, npc.Attitude, npc.Goal, npc.Stats.Data, npc.Skills.Data, npc.Spells.Data, npc.LLMNote, npc.Description))
		}
		sb.WriteString("</npcs>\n\n")
	}

	sb.WriteString("<proposed_tool_batch>\n")
	callsJSON, err := json.MarshalIndent(calls, "", "  ")
	if err != nil {
		sb.WriteString(fmt.Sprintf("ERROR marshaling calls: %v", err))
	} else {
		sb.Write(callsJSON)
	}
	sb.WriteString("\n</proposed_tool_batch>\n")
	return sb.String()
}

func rejectMessageFromAntiCheat(verdict AntiCheatVerdict) string {
	return fmt.Sprintf("SYSTEM REJECT: anti_cheat verdict=%s reason=%s message=%s", verdict.Verdict, verdict.Reason, verdict.Message)
}
