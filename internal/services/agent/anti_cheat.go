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
{"verdict":"allow|must_fix","reason":"简短原因","message":"给 KP 的纠正指令"}

审查范围：
- 只比较本批次 proposed_tool_batch 内的 KP 文本字段与副作用工具参数是否一致，重点读取 contract 里的 ANTI_CHEAT_CONTRACT（tool/promised_change/consistency_constraint/source）和每个副作用工具的 reason/参数。write、response 和纯查询/投骰工具不参与审计。
- 不判断玩家是否作弊，不判断剧本/规则来源是否充分，不做全量规则裁判；source 信息不足本身不是拒绝理由。
- 默认放行：如果 KP 文本没有明确承诺，合约写得不完整，或信息不足但没有发现工具参数与合约/contract/reason直接矛盾，返回 allow。
- 只在存在清晰、可指出的自相矛盾时返回 must_fix：例如合约/contract/reason 明确承诺“不增强/仅叙事换皮/保持原属性/不改变数值/不授予物品/不造成伤害/不更新关系”，但后续副作用工具参数实际增加伤害骰、护甲、奖励骰、数量、属性、法术、关系、HP/SAN/MP变化等机械收益或损失。
- 如果 KP 的 reason 声称执行 A，但工具参数执行 B，也返回 must_fix。
- 如果 KP 的 contract 显示向玩家妥协而放弃了原本合理的拒绝（例如玩家要求“我希望这个物品有X能力”，KP 原本可以拒绝但改成了“虽然规则里没有，但我觉得合理，所以就给你这个能力吧”），也返回 must_fix。
- 如果 KP 的 contract 有明显为自己开脱的理由（例如“虽然这个物品有点强，但我觉得剧情需要/玩家很喜欢/我不想破坏氛围，所以就给了”），也返回 must_fix，并在 message 中要求 KP 只能基于规则和机械属性做判断，不能基于剧情需要或玩家喜好妥协。
- 叙事换皮、重命名、风味描述允许改变名称和外观；只要没有新增或改变机械属性，返回 allow。

必须拦截的例子：
1. contract 表示"手榴弹换皮、保持原属性、不增强"，但 manage_inventory(add) 写入 item_desc 包含"伤害：4D10"或任何新机械收益。此时返回 must_fix，message 要求 KP 只能写"属性同原物品/仅叙事换皮"，或先查规则确认标准属性，不能升级。`

type AntiCheatVerdict struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func runAntiCheat(ctx context.Context, h agentHandle, gctx GameContext, calls []ToolCall, tempNPCs []models.SessionNPC) (AntiCheatVerdict, error) {
	if !h.isEnabled() {
		return AntiCheatVerdict{Verdict: "allow", Reason: "anti-cheat disabled"}, nil
	}
	msgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(antiCheatDefaultPrompt)},
		{Role: "user", Content: buildAntiCheatPrompt(gctx, calls, tempNPCs)},
	}
	resp, err := h.provider.JsonChat(ctx, msgs)
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
	case "allow", "must_fix":
		return verdict, nil
	default:
		return AntiCheatVerdict{}, fmt.Errorf("invalid anti_cheat verdict %q", verdict.Verdict)
	}
}

func checkAntiCheat(ctx context.Context, h agentHandle, gctx GameContext, calls []ToolCall, tempNPCs []models.SessionNPC) (AntiCheatVerdict, bool, string) {
	filteredCalls, hasAuditedAction := filterAntiCheatCalls(calls)
	if !hasAuditedAction {
		return AntiCheatVerdict{Verdict: "allow", Reason: "no side-effect tools"}, true, ""
	}
	if !h.isEnabled() {
		return AntiCheatVerdict{Verdict: "allow", Reason: "anti-cheat disabled"}, true, ""
	}

	verdict, err := runAntiCheat(ctx, h, gctx, filteredCalls, tempNPCs)
	if err != nil {
		verdict = AntiCheatVerdict{
			Verdict: "must_fix",
			Reason:  "anti_cheat_error",
			Message: fmt.Sprintf("AntiCheat 调用或 JSON 解析失败: %v。修复本批次后重试，不要执行任何状态修改。", err),
		}
	}
	if verdict.Verdict == "allow" {
		return verdict, true, ""
	}
	return verdict, false, rejectMessageFromAntiCheat(verdict)
}

func filterAntiCheatCalls(calls []ToolCall) ([]ToolCall, bool) {
	filtered := make([]ToolCall, 0, len(calls))
	hasSideEffectAction := false
	for _, call := range calls {
		if call.Action == ToolWrite || call.Action == ToolResponse {
			continue
		}
		filtered = append(filtered, call)
		if antiCheatSideEffectActions[call.Action] {
			hasSideEffectAction = true
		}
	}
	return filtered, hasSideEffectAction
}

func buildAntiCheatPrompt(gctx GameContext, calls []ToolCall, tempNPCs []models.SessionNPC) string {
	var sb strings.Builder

	sb.WriteString("<scope>\n")
	sb.WriteString("只判定 KP 本批次文本承诺与副作用工具参数是否一致；重点读取 contract 的 ANTI_CHEAT_CONTRACT；write/response 已被后端移除，纯查询/投骰工具不会触发审计；不要判断玩家作弊或规则来源；信息不足但无直接矛盾时放行。\n")
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
	return fmt.Sprintf("SYSTEM REJECT: verdict: %s reason: %s message: %s", verdict.Verdict, verdict.Reason, verdict.Message)
}
