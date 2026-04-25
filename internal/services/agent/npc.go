package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const npcDefaultPrompt = `你正在扮演COC TRPG中的一个NPC角色。你会收到该NPC的性格设定、当前情境和调查员的行动。
请给出该NPC在这一轮的具体反应。

【关键准则】
- KP（守秘人）通过引导问题描述当前情景，你应理解并按照KP的意图进行角色扮演
- 当KP给出明确的场景指引或逻辑约束时，优先遵守该约束而非自由创意
- 例如：若KP说"调查员试图做X但失败了"，你应该接受这个结果，而非推翻或挑战
- 保持NPC的性格和知识范围，但在故事逻辑上听从KP的导向

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
- 你只知道NPC自身的信息和当前情境，不了解整体剧情走向
- 你会持续扮演同一个NPC，保持前后反应一致
- 当KP明确指示时，跟随KP的故事逻辑而非创意发展`

// npcAgentStates keeps per-session, per-npc conversation memory so each NPC
// behaves like an independent long-lived agent.
var npcAgentStates sync.Map // key: "sessionID:npcName" -> []llm.ChatMessage

func npcStateKey(sessionID uint, npcName string) string {
	return fmt.Sprintf("%d:%s", sessionID, npcName)
}

func loadNPCState(sessionID uint, npcName string) []llm.ChatMessage {
	// Prefer in-memory cache for current process performance.
	key := npcStateKey(sessionID, npcName)
	if v, ok := npcAgentStates.Load(key); ok {
		if hist, ok2 := v.([]llm.ChatMessage); ok2 {
			return hist
		}
	}

	// Fallback to persistent context on SessionNPC.
	var npc models.SessionNPC
	if err := models.DB.Where("session_id = ? AND name = ?", sessionID, npcName).First(&npc).Error; err == nil {
		if len(npc.AgentCtx.Data) > 0 {
			history := chatMsgsToLLM(npc.AgentCtx.Data)
			npcAgentStates.Store(key, history)
			return history
		}
	}

	// If no live context exists, try compact memory from prior non-death destroy.
	var mem models.SessionNPCMemory
	if err := models.DB.Where("session_id = ? AND name = ?", sessionID, npcName).First(&mem).Error; err == nil {
		if strings.TrimSpace(mem.MemorySummary) != "" {
			seed := []llm.ChatMessage{{
				Role:    "assistant",
				Content: "【NPC记忆】" + mem.MemorySummary,
			}}
			npcAgentStates.Store(key, seed)
			return seed
		}
	}
	return nil
}

func saveNPCState(sessionID uint, npcName string, history []llm.ChatMessage) {
	if len(history) > 16 {
		history = history[len(history)-16:]
	}
	key := npcStateKey(sessionID, npcName)
	npcAgentStates.Store(key, history)

	// Persist to DB so temp NPC survives process restarts.
	_ = models.DB.Model(&models.SessionNPC{}).
		Where("session_id = ? AND name = ?", sessionID, npcName).
		Update("agent_ctx", models.JSONField[[]models.ChatMsg]{Data: llmToChatMsgs(history)}).Error
}

func clearNPCState(sessionID uint, npcName string) {
	npcAgentStates.Delete(npcStateKey(sessionID, npcName))
}

func createNPC(sessionID uint, card *NPCCard) string {
	if card == nil || strings.TrimSpace(card.Name) == "" {
		return "创建NPC失败：char_card.name 不能为空"
	}

	name := strings.TrimSpace(card.Name)
	desc := strings.TrimSpace(card.Description)
	att := strings.TrimSpace(card.Attitude)
	if desc == "" {
		desc = "（无描述）"
	}

	var existing models.SessionNPC
	err := models.DB.Where("session_id = ? AND name = ?", sessionID, name).First(&existing).Error
	if err == nil {
		existing.Description = desc
		existing.IsAlive = true
		existing.Stats.Data = card.Stats
		existing.Skills.Data = card.Skills
		_ = models.DB.Save(&existing).Error
		// Re-seed from compact memory if available.
		seedNPCFromMemory(sessionID, name)
		return fmt.Sprintf("已更新NPC：%s（态度：%s）", name, att)
	}

	npc := models.SessionNPC{
		SessionID:   sessionID,
		Name:        name,
		Description: desc,
		Stats:       models.JSONField[map[string]int]{Data: card.Stats},
		Skills:      models.JSONField[map[string]int]{Data: card.Skills},
		IsAlive:     true,
	}
	if err := models.DB.Create(&npc).Error; err != nil {
		return "创建NPC失败：数据库写入失败"
	}
	// Re-seed from compact memory if available.
	seedNPCFromMemory(sessionID, name)
	if att != "" {
		return fmt.Sprintf("已创建NPC：%s（态度：%s）", name, att)
	}
	return fmt.Sprintf("已创建NPC：%s", name)
}

func destroyNPC(sessionID uint, name string, reason string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "销毁NPC失败：name 不能为空"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "out_of_range"
	}

	state := loadNPCState(sessionID, name)
	if reason == "dead" {
		// Death means no continuation memory; clear any old memory snapshot.
		_ = models.DB.Where("session_id = ? AND name = ?", sessionID, name).Delete(&models.SessionNPCMemory{}).Error
	} else {
		// Non-death destroy (e.g. out_of_range): compact context into long-term memory.
		summary := compactNPCMemory(state)
		if summary != "" {
			var mem models.SessionNPCMemory
			err := models.DB.Where("session_id = ? AND name = ?", sessionID, name).First(&mem).Error
			if err == nil {
				mem.MemorySummary = summary
				_ = models.DB.Save(&mem).Error
			} else {
				_ = models.DB.Create(&models.SessionNPCMemory{
					SessionID:     sessionID,
					Name:          name,
					MemorySummary: summary,
				}).Error
			}
		}
	}

	res := models.DB.Where("session_id = ? AND name = ?", sessionID, name).Delete(&models.SessionNPC{})
	clearNPCState(sessionID, name)
	if res.RowsAffected == 0 {
		return fmt.Sprintf("未找到NPC：%s", name)
	}
	if reason == "dead" {
		return fmt.Sprintf("已销毁NPC：%s（死亡）", name)
	}
	return fmt.Sprintf("已销毁NPC：%s（记忆已压缩保存）", name)
}

func actNPC(
	ctx context.Context,
	h agentHandle,
	gctx GameContext,
	npcName string,
	question string,
	tempNPCs []models.SessionNPC,
) (NPCAction, error) {
	return runNPC(ctx, h, gctx, npcName, question, tempNPCs)
}

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
	question string,
	tempNPCs []models.SessionNPC,
) (NPCAction, error) {
	log.Printf("[npc] acting: %s", npcName)
	debugf("NPC", "name=%q question=%s", npcName, question)

	// Build NPC profile from DB/scenario lookup (profile only, no scenario background).
	npcProfile := buildNPCProfile(npcName, gctx, tempNPCs)

	if question == "" {
		question = fmt.Sprintf("调查员行动：[%s] %s。你要做什么？", gctx.UserName, gctx.UserInput)
	}

	// System prompt + NPC profile as static context.
	msgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(npcDefaultPrompt)},
		{Role: "user", Content: "NPC资料：\n" + npcProfile},
	}
	// Each NPC owns independent dialogue history in this session.
	npcHistory := loadNPCState(gctx.Session.ID, npcName)
	msgs = append(msgs, npcHistory...)

	// Current question as the final user message.
	msgs = append(msgs, llm.ChatMessage{
		Role:    "user",
		Content: "KP提问：" + question + "\n\n请给出该NPC本轮的行动和对话。",
	})

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

	// Persist per-NPC memory so each NPC behaves like a dedicated agent.
	assistantMemo := fmt.Sprintf("行动：%s\n对话：%s", action.Action, action.Dialogue)
	npcHistory = append(npcHistory,
		llm.ChatMessage{Role: "user", Content: question},
		llm.ChatMessage{Role: "assistant", Content: assistantMemo},
	)
	saveNPCState(gctx.Session.ID, npcName, npcHistory)

	debugf("NPC", "name=%q action=%s dialogue=%s", npcName, action.Action, action.Dialogue)
	return action, nil
}

func compactNPCMemory(history []llm.ChatMessage) string {
	if len(history) == 0 {
		return ""
	}
	start := 0
	if len(history) > 8 {
		start = len(history) - 8
	}
	var sb strings.Builder
	sb.WriteString("近期互动摘要：")
	for _, m := range history[start:] {
		role := "NPC"
		if m.Role == "user" {
			role = "KP"
		}
		line := strings.TrimSpace(m.Content)
		if len([]rune(line)) > 80 {
			line = string([]rune(line)[:80]) + "…"
		}
		if line != "" {
			sb.WriteString(" [" + role + "]" + line)
		}
	}
	text := sb.String()
	if len([]rune(text)) > 400 {
		text = string([]rune(text)[:400]) + "…"
	}
	return text
}

func seedNPCFromMemory(sessionID uint, npcName string) {
	clearNPCState(sessionID, npcName)
	var mem models.SessionNPCMemory
	if err := models.DB.Where("session_id = ? AND name = ?", sessionID, npcName).First(&mem).Error; err != nil {
		return
	}
	summary := strings.TrimSpace(mem.MemorySummary)
	if summary == "" {
		return
	}
	history := []llm.ChatMessage{{Role: "assistant", Content: "【NPC记忆】" + summary}}
	saveNPCState(sessionID, npcName, history)
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
