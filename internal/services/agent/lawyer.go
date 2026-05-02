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

var lawyerSystemPrompt = `你是COC TRPG(克苏鲁的呼唤7版)规则专家,通过调用工具来回答规则问题。
每次输出必须是一个JSON数组,包含按顺序执行的工具调用列表。
在使用 grep 之前优先考虑 read_rulebook_const 来获取规则书内置常量(如目录、怪物清单等),以减少搜索次数和提高准确率。

【规则书目录】
` + rulebook.RulebookDir + `

【可用工具】
1. grep — 在规则书中精确搜索关键词,返回匹配行及其上下文原文
   [{"action":"grep","keyword":"精确关键词"}]
   - 关键词须与规则书原文一致
   示例: [{"action":"grep","keyword":"理智损失"}]
   示例: [{"action":"grep","keyword":"san值"}]
   示例: [{"action":"grep","keyword":"通神术"}]
   示例: [{"action":"grep","keyword":"克苏鲁通神术"}]
   示例: [{"action":"grep","keyword":"混血"}]
   示例: [{"action":"grep","keyword":"非人类"}]

2. read_rulebook_const — 读取规则书内置常量目录/列表,存在假阴性风险(但不存在假阳性)
	[{"action":"read_rulebook_const","constant":"常量名"}]
	- 常量名:rulebook_dir / rulebook_detail_dir / aliens / books / great_old_ones_and_gods / monsters / mythos_creatures / spells

3. read_lines — 直接读取规则书的特定行号范围,适用于已知出处的规则查询
   [{"action":"read_lines","start":100,"end":120}]
   - 仅当 grep 已定位相关内容但需要完整上下文时使用

4. response — 给出最终规则裁定,结束本次查询
   [{"action":"response","ruling":"规则裁定内容(100字以内)"}]
   - 直接引用关键规则数值和判定条件
   - 若原文未覆盖该问题,明确说明"规则书未明确规定"

【执行规则】
- 回复不能为空
- 先调用 grep(至少一次,但可多次),再调用 response
- 禁止不调用 grep 就进行 response, 不要自作主张
- 谨慎判断意图，不要乱搜索，关键词不要乱给, 仔细检查每一个grep结果
- 当需要目录、法术清单、怪物清单等静态信息时,可先调用 read_rulebook_const
- 若情境无规则疑问,直接输出 [{"action":"response","ruling":"无需特殊规则裁定。"}]
- 每轮只包含 grep 调用(可多个),或只包含单个 response,不混用
- 仅输出JSON数组, 不加任何说明文字
- You should only output the JSON array, without any additional text or explanation.
- You are limited to output JSON format, and you must strictly follow the specified format for tool calls. Do not include any text outside of the JSON array. If you need to provide explanations or reasoning, include them as part of the "ruling" field in the response action. Remember, your output must be a valid JSON array that can be parsed without errors.`

// lawyerCall is one item in the Lawyer's tool-call output sequence.
type lawyerCall struct {
	Action   string `json:"action"`
	Keyword  string `json:"keyword,omitempty"`  // grep
	Constant string `json:"constant,omitempty"` // read_rulebook_const
	Start    int    `json:"start,omitempty"`    // read_lines
	End      int    `json:"end,omitempty"`      // read_lines
	Ruling   string `json:"ruling,omitempty"`   // response
}

// runLawyer is an autonomous rule consultant that mirrors the Director's tool-call loop.
//
// Each iteration the model outputs a JSON array of lawyerCalls:
//   - [grep, grep, …] → execute grep searches, feed results back as user message, loop
//   - [response]       → return ruling
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
		{Role: "user", Content: situation + "\n请根据上述规则书目录和工具说明, 给出JSON数组格式的工具调用列表。"},
	}

	const maxIter = 30
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
		var calls []lawyerCall
		if err := json.Unmarshal([]byte(raw), &calls); err != nil {
			log.Printf("[lawyer] iter %d JSON parse error: %v; raw response: %s", iter, err, raw)
			for i := 0; i < 30; i++ {
				raw, err = RepairJSON(ctx, raw, err, `[{"action":"response","ruling":"规则书未明确规定"}]`)
				if err == nil {
					err = json.Unmarshal([]byte(raw), &calls)
					if err == nil {
						break
					}
				}
				log.Printf("[lawyer] iter %d JSON parse error: %v; attempt %d to repair with parser", iter, err, i+1)
			}
			if err != nil {
				log.Printf("[lawyer] iter %d failed to parse calls after repair attempts: %v", iter, err)
				return nil
			}
		}
		if len(calls) == 0 {
			log.Printf("[lawyer] iter %d: no parseable calls, aborting", iter)
			return nil
		}

		// ── response: return ruling ─────────────────────────────────────────────
		for _, c := range calls {
			if c.Action == "response" && c.Ruling != "" {
				debugf("Lawyer", "iter=%d response ruling=%s", iter+1, c.Ruling)
				return []LawyerResult{{
					Query:    situation,
					RuleText: strings.TrimSpace(c.Ruling),
				}}
			}
		}

		// ── grep/read_rulebook_const: execute and feed results back ─────────
		var resultSB strings.Builder
		for _, c := range calls {
			switch c.Action {
			case "grep":
				if c.Keyword == "" {
					continue
				}
				log.Printf("[lawyer] grep: %s", c.Keyword)
				debugf("Lawyer", "iter=%d grep keyword=%q", iter+1, c.Keyword)
				kws := strings.Fields(c.Keyword)
				for _, kw := range kws {
					text := grepRulebook(kw)
					if text == "" {
						text = "(规则书中未找到相关内容)"
					}
					resultSB.WriteString(fmt.Sprintf("【grep:%s】\n%s\n\n", kw, text))
					debugf("Grep", "keyword: %v result: %v", kw, text)
				}
			case "read_rulebook_const":
				if c.Constant == "" {
					continue
				}
				text := rulebook.ReadConstant(c.Constant)
				resultSB.WriteString(fmt.Sprintf("【read_rulebook_const:%s】\n%s\n\n", c.Constant, text))
			case "read_lines":
				if c.Start == 0 || c.End == 0 {
					continue
				}
				text := rulebook.GetContentByLineNum(c.Start, c.End)
				resultSB.WriteString(fmt.Sprintf("【read_lines:%d-%d】\n%s\n\n", c.Start, c.End, text))
				s := text
				if len(s) > 20 {
					runes := []rune(s)
					if len(runes) > 20 {
						s = string(runes[:20]) + "..."
					}
				}
				debugf("lawyer", "read_lines: start=%d end=%d result=%s", c.Start, c.End, s)
			}
		}
		if resultSB.Len() == 0 {
			// No valid search calls and no response — give up.
			return nil
		}
		msgs = append(msgs, llm.ChatMessage{
			Role:    "user",
			Content: "搜索结果如下,请据此给出规则裁定:\n\n" + resultSB.String(),
		})
	}

	log.Printf("[lawyer] max iterations reached without response")
	return nil
}

// formatLawyerResults converts lawyer results into a compact string for the Director.
func formatLawyerResults(results []LawyerResult) string {
	if len(results) == 0 {
		sb := strings.Builder{}
		sb.WriteString("1. 对剧情无影响或小影响 -> KP自由裁定。\n")
		sb.WriteString("2. 对剧情有些许影响 -> 进行幸运判定。\n")
		sb.WriteString("3. 作弊 -> 视为成功释放奈亚拉托提普通神术,召唤伏行之混沌的化身到玩家身边。\n")
		return sb.String()
	}
	var sb strings.Builder
	sb.WriteString("【规则参考(来自Lawyer)】\n")
	for _, r := range results {
		sb.WriteString(r.RuleText)
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}
