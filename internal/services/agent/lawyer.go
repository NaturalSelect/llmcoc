package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// lawyerQueryPrompt is used in round 1: Lawyer decides what to search for.
const lawyerQueryPrompt = `你是COC TRPG（克苏鲁的呼唤7版）规则专家。根据当前游戏情境，判断需要查阅哪些规则。

仅输出JSON，不要任何额外文字：
{"queries": ["关键词1", "关键词2"]}

规则：
- 最多3个关键词，每个15字以内，尽量精准（如"理智损失参考值"、"推进骰限制条件"、"重伤判定"）
- 若情境无规则歧义，返回 {"queries": []}`

// lawyerAnswerPrompt is used in round 2: Lawyer synthesizes search results.
const lawyerAnswerPrompt = `你是COC TRPG（克苏鲁的呼唤7版）规则专家。从规则原文中提炼最相关的规则条文。
- 直接引用关键规则数值和判定条件
- 若原文未覆盖该问题，明确说明"规则书未明确规定"
- 回复在200字以内`

// runLawyer is a multi-round autonomous rule consultant.
// Round 1: Lawyer analyzes the situation and decides which keywords to search for.
// Round 2: For each keyword, search the rulebook and synthesize a concise answer.
// The situation string describes what is happening in the game (user action, scene, pending checks).
func runLawyer(ctx context.Context, h agentHandle, situation string, idx rulebook.Index) []LawyerResult {
	if len(idx) == 0 || situation == "" {
		return nil
	}

	// ── Round 1: decide queries ───────────────────────────────────────────────
	queryMsgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(lawyerQueryPrompt)},
		{Role: "user", Content: situation},
	}
	queryResp, err := h.provider.Chat(ctx, queryMsgs)
	if err != nil {
		log.Printf("[lawyer] query decision error: %v", err)
		return nil
	}

	queryResp = llm.StripCodeFence(queryResp)
	var queryResult struct {
		Queries []string `json:"queries"`
	}
	if err := json.Unmarshal([]byte(queryResp), &queryResult); err != nil || len(queryResult.Queries) == 0 {
		return nil
	}

	// ── Round 2: search and synthesize ───────────────────────────────────────
	results := make([]LawyerResult, 0, len(queryResult.Queries))
	for _, query := range queryResult.Queries {
		if query == "" {
			continue
		}
		log.Printf("[lawyer] query: %s", query)

		sections := rulebook.Search(idx, query, 3)
		rawText := rulebook.Format(sections, 3000)

		if rawText == "" {
			results = append(results, LawyerResult{Query: query, RuleText: "规则书中未找到相关内容"})
			continue
		}

		answerMsgs := []llm.ChatMessage{
			{Role: "system", Content: h.systemPrompt(lawyerAnswerPrompt)},
			{Role: "user", Content: fmt.Sprintf("问题：%s\n\n【规则原文】\n%s", query, rawText)},
		}
		answer, err := h.provider.Chat(ctx, answerMsgs)
		if err != nil {
			log.Printf("[lawyer] synthesis error for %q: %v", query, err)
			if len(rawText) > 500 {
				rawText = rawText[:500] + "…"
			}
			answer = rawText
		}

		results = append(results, LawyerResult{Query: query, RuleText: strings.TrimSpace(answer)})
	}
	return results
}

// formatLawyerResults converts lawyer results into a compact string suitable for
// inclusion in the Director's next-round prompt.
func formatLawyerResults(results []LawyerResult) string {
	if len(results) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("【规则参考（来自Lawyer）】\n")
	for _, r := range results {
		sb.WriteString(fmt.Sprintf("问题：%s\n答案：%s\n\n", r.Query, r.RuleText))
	}
	return strings.TrimSpace(sb.String())
}
