package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const antiCheatDefaultPrompt = `你是 COC TRPG 后台 AntiCheat 裁判。你只审查 KP 本批次“说的计划/理由”和“准备执行的工具调用”是否自洽。

只输出一个 JSON 对象，不要输出 markdown、解释文本或代码块：
{"verdict":"allow|replan","reason":"简短原因","message":"给 KP 的纠正指令"}

审查范围：
- 只比较本批次 proposed_tool_batch 内的 KP 文本字段与工具参数是否一致，例如 think、reason 与 manage_inventory/update_*/manage_spell/manage_relation 等工具的参数。write 和 response 不参与审计。
- 不判断玩家是否作弊，不判断剧本/规则来源是否充分，不做全量规则裁判。
- 如果 KP 文本没有明确承诺，或只是笼统计划，且工具参数没有明显反向执行，返回 allow。
- 如果 KP 明确承诺“不增强/仅叙事换皮/保持原属性/不改变数值/不授予物品/不造成伤害/不更新关系”等，但后续工具参数实际增加伤害骰、护甲、奖励骰、数量、属性、法术、关系、HP/SAN/MP变化等机械收益或损失，返回 replan。
- 如果 KP 的 reason/ack/reply 声称执行 A，但工具参数执行 B，也返回 replan。
- 叙事换皮、重命名、风味描述允许改变名称和外观；只要没有新增或改变机械属性，返回 allow。

必须拦截的例子：think 表示“手榴弹换皮、保持原属性、不增强”，但 manage_inventory(add) 写入 item_desc 包含“伤害：4D10”或任何新机械收益。此时返回 replan，message 要求 KP 只能写“属性同原物品/仅叙事换皮”，或先查规则确认标准属性，不能升级。`

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
	case "allow", "replan":
		return verdict, nil
	default:
		return AntiCheatVerdict{}, fmt.Errorf("invalid anti_cheat verdict %q", verdict.Verdict)
	}
}

func checkAntiCheat(ctx context.Context, h agentHandle, gctx GameContext, calls []ToolCall, tempNPCs []models.SessionNPC) (AntiCheatVerdict, bool, string) {
	filteredCalls, hasAuditedAction := filterAntiCheatCalls(calls)
	if !hasAuditedAction {
		return AntiCheatVerdict{Verdict: "allow", Reason: "no audited tools"}, true, ""
	}

	verdict, err := runAntiCheat(ctx, h, gctx, filteredCalls, tempNPCs)
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

func filterAntiCheatCalls(calls []ToolCall) ([]ToolCall, bool) {
	filtered := make([]ToolCall, 0, len(calls))
	hasAuditedAction := false
	for _, call := range calls {
		if call.Action == ToolWrite || call.Action == ToolResponse {
			continue
		}
		filtered = append(filtered, call)
		if call.Action != ToolThink {
			hasAuditedAction = true
		}
	}
	return filtered, hasAuditedAction
}

func buildAntiCheatPrompt(gctx GameContext, calls []ToolCall, tempNPCs []models.SessionNPC) string {
	var sb strings.Builder

	sb.WriteString("<scope>\n")
	sb.WriteString("只判定 KP 本批次文本承诺与工具参数是否一致；write/response 已被后端移除，不参与审计；不要判断玩家作弊或规则来源。\n")
	sb.WriteString("</scope>\n\n")

	sb.WriteString("<current_input_context>\n")
	if len(gctx.PendingActions) > 1 {
		for _, a := range gctx.PendingActions {
			sb.WriteString(fmt.Sprintf("[%s]: %s\n", a.PlayerName, a.Content))
		}
	} else {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", gctx.UserName, gctx.UserInput))
	}
	sb.WriteString("</current_input_context>\n\n")

	sb.WriteString("<recent_kp_ack_context>\n")
	start := len(gctx.History) - 4
	if start < 0 {
		start = 0
	}
	for _, m := range gctx.History[start:] {
		if m.Role == models.MessageRoleAssistant {
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
	}
	sb.WriteString("</recent_kp_ack_context>\n\n")

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
