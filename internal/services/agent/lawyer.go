// NOTE: Defines AI agent roles and their interactions.
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

var lawyerSystemPrompt = `你是COC TRPG（克苏鲁的呼唤7版）规则专家，通过调用工具来回答规则问题。
每次输出必须是一个JSON数组，包含按顺序执行的工具调用列表。

【规则书目录】
` + rulebook.RulebookDir + `

【可用工具】
1. search — 检索规则书原文
   {"action":"search","keyword":"精确关键词（15字以内）"}
   参考上方目录选择合适的关键词进行搜索。
   示例：{"action":"search","keyword":"理智损失参考值"}
   示例：{"action":"search","keyword":"推进骰限制条件"}

2. read_rulebook_const — 读取规则书内置常量目录/列表，存在假阴性风险（但不存在假阳性）
	{"action":"read_rulebook_const","constant":"常量名"}
	- 常量名：rulebook_dir / rulebook_detail_dir / aliens / books / great_old_ones_and_gods / monsters / mythos_creatures / spells

3. answer — 给出最终规则裁定，结束本次查询
   {"action":"answer","ruling":"规则裁定内容（200字以内）"}
   - 直接引用关键规则数值和判定条件
   - 若原文未覆盖该问题，明确说明"规则书未明确规定"

【执行规则】
- 先调用 search（至少一次，但可多次），再调用 answer
- 当需要目录、法术清单、怪物清单等静态信息时，可先调用 read_rulebook_const
- 若情境无规则疑问，直接输出 [{"action":"answer","ruling":"无需特殊规则裁定。"}]
- 每轮只包含 search 调用（可多个），或只包含单个 answer，不混用
- 仅输出JSON数组，不加任何说明文字`

// lawyerCall is one item in the Lawyer's tool-call output sequence.
type lawyerCall struct {
	Action   string `json:"action"`
	Keyword  string `json:"keyword,omitempty"`  // search
	Constant string `json:"constant,omitempty"` // read_rulebook_const
	Ruling   string `json:"ruling,omitempty"`   // answer
}

// runLawyer is an autonomous rule consultant that mirrors the Director's tool-call loop.
//
// Each iteration the model outputs a JSON array of lawyerCalls:
//   - [search, search, …] → execute searches, feed results back as user message, loop
//   - [answer]            → return ruling
//
// The conversation grows naturally (system → user → assistant → user → …) so the model
// always sees its own prior decisions alongside the search evidence.
func runLawyer(ctx context.Context, h agentHandle, situation string, idx rulebook.Index) []LawyerResult {
	if len(idx) == 0 || situation == "" {
		return nil
	}

	debugf("Lawyer", "question=%s", situation)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(lawyerSystemPrompt)},
		{Role: "user", Content: situation},
	}

	const maxIter = 4
	for iter := 0; iter < maxIter; iter++ {
		if ctx.Err() != nil {
			return nil
		}

		raw, err := h.provider.Chat(ctx, msgs)
		if err != nil {
			log.Printf("[lawyer] iter %d LLM error: %v", iter, err)
			return nil
		}
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		stripped := llm.StripCodeFence(raw)
		var calls []lawyerCall
		if err := json.Unmarshal([]byte(stripped), &calls); err != nil {
			// Try to extract array from surrounding text.
			if s := strings.Index(stripped, "["); s >= 0 {
				if e := strings.LastIndex(stripped, "]"); e > s {
					_ = json.Unmarshal([]byte(stripped[s:e+1]), &calls)
				}
			}
		}
		if len(calls) == 0 {
			log.Printf("[lawyer] iter %d: no parseable calls, aborting", iter)
			return nil
		}

		// ── answer: return ruling ─────────────────────────────────────────────
		for _, c := range calls {
			if c.Action == "answer" && c.Ruling != "" {
				debugf("Lawyer", "iter=%d answer ruling=%s", iter+1, c.Ruling)
				return []LawyerResult{{
					Query:    situation,
					RuleText: strings.TrimSpace(c.Ruling),
				}}
			}
		}

		// ── search/read_rulebook_const: execute and feed results back ─────────
		var resultSB strings.Builder
		for _, c := range calls {
			switch c.Action {
			case "search":
				if c.Keyword == "" {
					continue
				}
				log.Printf("[lawyer] search: %s", c.Keyword)
				debugf("Lawyer", "iter=%d search keyword=%q", iter+1, c.Keyword)
				sections := rulebook.Search(idx, c.Keyword, 5)
				text := rulebook.Format(sections, 2000)
				if text == "" {
					text = "（规则书中未找到相关内容）"
				}
				resultSB.WriteString(fmt.Sprintf("【search:%s】\n%s\n\n", c.Keyword, text))
				debugf("Search", "Search keyword: %v result: %v", c.Keyword, text)
			case "read_rulebook_const":
				if c.Constant == "" {
					continue
				}
				text := rulebook.ReadConstant(c.Constant)
				resultSB.WriteString(fmt.Sprintf("【read_rulebook_const:%s】\n%s\n\n", c.Constant, text))
			}
		}
		if resultSB.Len() == 0 {
			// No valid search calls and no answer — give up.
			return nil
		}
		msgs = append(msgs, llm.ChatMessage{
			Role:    "user",
			Content: "搜索结果如下，请据此给出规则裁定：\n\n" + resultSB.String(),
		})
	}

	log.Printf("[lawyer] max iterations reached without answer")
	return nil
}

// formatLawyerResults converts lawyer results into a compact string for the Director.
func formatLawyerResults(results []LawyerResult) string {
	if len(results) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("【规则参考（来自Lawyer）】\n")
	for _, r := range results {
		sb.WriteString(r.RuleText)
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}
