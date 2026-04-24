package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const npcDefaultPrompt = `你正在扮演COC TRPG中的一个NPC角色。你会收到该NPC的性格设定、当前情境和玩家最近的行动。
请给出该NPC在这一轮的具体反应。

仅输出JSON，不要任何额外文字：
{
  "npc_name": "NPC名称",
  "action": "NPC的行动描述（50字以内）",
  "dialogue": "NPC说的话，若NPC沉默则为空字符串"
}

注意：
- 保持NPC性格一致性
- 若NPC已死亡或无法行动，action填"无法行动"，dialogue为空
- 对话要符合NPC的语气和知识背景
- 你只知道NPC自身的信息和当前情境，不了解整体剧情走向`

// runNPC makes one NPC act based on its own profile and the context brief provided by the KP.
// The NPC agent does NOT receive scenario information — it only gets:
//   - The NPC's own profile (name, description, attitude, stats)
//   - A brief context from the KP (npcCtx) describing the immediate situation
//   - Recent conversation history for reactive dialogue
func runNPC(
	ctx context.Context,
	h agentHandle,
	gctx GameContext,
	npcName string,
	npcCtx string,
	tempNPCs []models.SessionNPC,
) (NPCAction, error) {
	log.Printf("[npc] acting: %s", npcName)
	debugf("NPC", "name=%q ctx=%s", npcName, npcCtx)

	// Build NPC profile from DB/scenario lookup (profile only, no scenario background).
	npcProfile := buildNPCProfile(npcName, gctx, tempNPCs)
	recent := tailMessages(gctx.History, 6)
	histSummary := buildHistorySummary(recent)

	situationLine := fmt.Sprintf("当前玩家行动：[%s]: %s", gctx.UserName, gctx.UserInput)
	if npcCtx != "" {
		situationLine = fmt.Sprintf("当前情境：%s\n玩家行动：[%s]: %s", npcCtx, gctx.UserName, gctx.UserInput)
	}

	userPrompt := fmt.Sprintf(
		"NPC资料：\n%s\n\n近期对话：\n%s\n\n%s\n\n请给出该NPC本轮的行动和对话。",
		npcProfile,
		histSummary,
		situationLine,
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(npcDefaultPrompt)},
		{Role: "user", Content: userPrompt},
	}

	resp, err := h.provider.Chat(ctx, msgs)
	if err != nil {
		return NPCAction{NPCName: npcName, Action: "保持沉默", Dialogue: ""}, fmt.Errorf("npc LLM error: %w", err)
	}

	resp = llm.StripCodeFence(resp)
	var action NPCAction
	if err := json.Unmarshal([]byte(resp), &action); err != nil {
		log.Printf("[npc] JSON parse error for %s: %v", npcName, err)
		return NPCAction{NPCName: npcName, Action: strings.TrimSpace(resp), Dialogue: ""}, nil
	}
	if action.NPCName == "" {
		action.NPCName = npcName
	}
	debugf("NPC", "name=%q action=%s dialogue=%s", npcName, action.Action, action.Dialogue)
	return action, nil
}

// buildNPCProfile returns a text description of an NPC for use in prompts.
func buildNPCProfile(name string, gctx GameContext, tempNPCs []models.SessionNPC) string {
	// Check scenario static NPCs first.
	content := gctx.Session.Scenario.Content.Data
	for _, npc := range content.NPCs {
		if npc.Name == name {
			desc := npc.Description
			if len(desc) > 200 {
				desc = desc[:200] + "…"
			}
			profile := fmt.Sprintf("姓名：%s\n描述：%s\n态度：%s", npc.Name, desc, npc.Attitude)
			if len(npc.Stats) > 0 {
				var statParts []string
				for k, v := range npc.Stats {
					statParts = append(statParts, fmt.Sprintf("%s:%d", k, v))
				}
				profile += "\n属性：" + strings.Join(statParts, " ")
			}
			return profile
		}
	}

	// Check temporary NPC cards.
	for _, npc := range tempNPCs {
		if npc.Name == name {
			alive := "存活"
			if !npc.IsAlive {
				alive = "已死亡/失能"
			}
			desc := npc.Description
			if len(desc) > 200 {
				desc = desc[:200] + "…"
			}
			profile := fmt.Sprintf("姓名：%s（临时NPC，%s）\n描述：%s", npc.Name, alive, desc)
			if len(npc.Stats.Data) > 0 {
				var statParts []string
				for k, v := range npc.Stats.Data {
					statParts = append(statParts, fmt.Sprintf("%s:%d", k, v))
				}
				profile += "\n属性：" + strings.Join(statParts, " ")
			}
			return profile
		}
	}

	// Unknown NPC – use name only.
	return fmt.Sprintf("姓名：%s\n（无详细资料）", name)
}
