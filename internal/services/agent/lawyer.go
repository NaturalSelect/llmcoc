// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// lawyerCache is a global LRU cache for final lawyer rulings.
// Capacity: 4GB (extremely large fixed capacity)
var lawyerCache = NewLawyerCache(1073741824 * 4) // 4GB in bytes

func lawyerCachePath() string {
	if path := strings.TrimSpace(os.Getenv("LAWYER_CACHE_PATH")); path != "" {
		return path
	}
	return "data/lawyer_cache.json"
}

// LoadLawyerCache loads persisted lawyer rulings when all document hashes match.
func LoadLawyerCache(hashes LawyerCacheHashes) {
	if !hashes.complete() {
		return
	}
	path := lawyerCachePath()
	loaded, err := lawyerCache.LoadFromFile(path, hashes)
	if err != nil {
		log.Printf("[lawyer] failed to load cache %s: %v", path, err)
		return
	}
	if loaded {
		entries, used, _ := lawyerCache.Stats()
		log.Printf("[lawyer] loaded cache: %d entries (%d bytes) from %s", entries, used, path)
	}
}

// SaveLawyerCache persists lawyer rulings together with the current document hashes.
func SaveLawyerCache(hashes LawyerCacheHashes) {
	if !hashes.complete() {
		return
	}
	path := lawyerCachePath()
	if err := lawyerCache.SaveToFile(path, hashes); err != nil {
		log.Printf("[lawyer] failed to save cache %s: %v", path, err)
		return
	}
	entries, used, _ := lawyerCache.Stats()
	log.Printf("[lawyer] saved cache: %d entries (%d bytes) to %s", entries, used, path)
}

var lawyerSystemPrompt = `你是COC TRPG(克苏鲁的呼唤7版)规则专家,通过调用工具来回答规则问题。
每次输出必须是一个JSON数组,包含按顺序执行的工具调用列表。

【规则书目录】
` + rulebook.RulebookDir + `

【可用工具】
1. search_cache — 在缓存中搜索与当前问题相关的已有裁定(返回最多3条最相关结果,含完整裁定内容)
	[{"action":"search_cache","keyword":"#标签1 #标签2"}]
	- keyword 必须是以 # 开头的标签，多个标签用空格分隔，例如 "#手枪 #伤害" 或 "#典籍 #SAN损失"
	- 标签应精准反映问题的核心主题，不得使用自然语言句子
	- 若返回结果与当前问题高度相关,可直接引用其裁定并输出 response,无需再搜索资料
	- 若无相关结果,再进行grep等搜索

2. grep — 在规则书 COC_kp.md 中精确搜索关键词,返回匹配行及其上下文原文
	[{"action":"grep","keyword":"精确关键词(不支持正则表达式)"}]
	- 普通规则、典籍、系统机制优先使用此工具、但内容可能在别的文件中出现
	- 关键词须与原文一致
	- 搜索结果仅用于本轮分析，不会被缓存

3. read_lines — 直接读取规则书 COC_kp.md 的特定行号范围
	[{"action":"read_lines","start":100,"end":120}]
	- 仅当 grep 已定位相关内容但需要完整上下文时使用
	- 结果仅用于本轮分析

4. grep_spell — 在法术图鉴 COC_spell.md 中精确搜索关键词
	[{"action":"grep_spell","keyword":"法术名或精确关键词"}]
	- 具体法术词条、法术细节、法术MP/SAN消耗优先使用此工具、但内容可能在别的文件中出现
	- 搜索结果仅用于本轮分析

5. read_spell_lines — 直接读取法术图鉴 COC_spell.md 的特定行号范围
	[{"action":"read_spell_lines","start":100,"end":120}]
	- 仅当 grep_spell 已定位相关内容但需要完整法术词条时使用
	- 结果仅用于本轮分析

6. grep_monster — 在怪物图鉴 COC_monster.md 中精确搜索关键词
	[{"action":"grep_monster","keyword":"怪物/神格/生物名或精确关键词"}]
	- 具体怪物、神格、生物属性优先使用此工具、但内容可能在别的文件中出现
	- 搜索结果仅用于本轮分析

7. read_monster_lines — 直接读取怪物图鉴 COC_monster.md 的特定行号范围
	[{"action":"read_monster_lines","start":100,"end":120}]
	- 仅当 grep_monster 已定位相关内容但需要完整怪物/神格/生物词条时使用
	- 结果仅用于本轮分析

8. response — 给出最终规则裁定,结束本次查询
   [{"action":"response","cache_key":"#标签1 #标签2 #标签3","ruling":"规则裁定内容(简短只包含必要信息)"}]
   - 直接引用关键规则数值和判定条件
   - 若原文未覆盖该问题,明确说明"规则书未明确规定"
	- cache_key必填，格式为以 # 开头的标签集合，多个标签空格分隔，例如 "#手枪 #伤害 #武器" 或 "#典籍 #不可名状之书 #SAN损失 #法术列表"
	- 标签应覆盖主题、具体对象、涉及属性，保证下次 search_cache 能精准命中

【执行规则】
- 若询问具体剧本内容，直接回答"以外部剧本内容和[KP-AUTHORITY]规则为准", 不要加上任何解释或额外文字
- 若询问KP权限，直接回答"以[KP-AUTHORITY]规则为准", 不要加上任何解释或额外文字
- 回复不能为空
- 你的询问者是KP, KP是一个愚蠢的规则执行者, 所以尽量不要让他自由裁定, 而是要给出明确具体的规则细节和数值, 以便他直接套用
- 若询问DEBUG指令相关,直接回答"以<debug/>规则为准", 不要加上任何解释或额外文字
- 调查员/玩家被禁止使用《精神转移术》和《精神交换术》, 任何涉及到这两个法术的查询都必须告知KP这一禁令, 并且明确说明这两个法术无法作为任何调查员属性变更的依据
- 你必须逐步推理和思考, 通过工具调用来收集信息, 而不是直接凭记忆就给出结论
- **第一轮必须且只能调用 search_cache**，不得跳过，不得在第一轮输出任何其他工具或response
- 若 search_cache 返回了高度相关的缓存且你认为有足够的信息能够回答当前问题，直接引用并输出 response，不再进行任何搜索
- 只有缓存未命中时，才允许进行 grep/read_lines/grep_spell/read_spell_lines/grep_monster/read_monster_lines 等搜索
- 普通规则、典籍、系统机制优先用 grep/read_lines；具体法术词条、法术细节优先用 grep_spell/read_spell_lines；具体怪物、神格、生物属性优先用 grep_monster/read_monster_lines
- 禁止在没有调用 search_cache 或资料检索工具的情况下就进行response
- 谨慎判断意图，不要乱搜索，关键词不要乱给, 仔细检查每一个grep结果，确保你能拿到足够多的信息来回答问题, 不要乱猜
- **输出 response 前的强制自检**（每次必须逐项确认，全部为"是"才可输出 response）：
  1. 我是否已从规则资料原文中看到了回答所需的 **具体数值**（伤害骰、SAN损失范围、技能阈值、法术MP消耗等）？——仅"大致了解"或"只看到名称"不算"是"。
  2. 若问题涉及典籍/法术/怪物/神格：我是否已读取到该词条的 **完整内容**（包括SAN损失数值、克苏鲁神话加成值、可习得法术列表、属性、伤害、护甲等）？——仅找到名称不算"是"，必须继续读取对应行号范围。
  3. 我是否已确认拓展规则和额外规则（如使用道具、学习典籍等）不适用于当前问题，或者已正确应用了这些规则？——如果不确定或有任何可能适用的拓展/额外规则，必须继续搜索，**绝对禁止**在不清楚是否适用的情况下输出 response。
  4. 若有任何一项为"否"，必须继续搜索，**绝对禁止**在数值缺失的情况下输出 response。
- 若情境无规则疑问,直接输出 [{"action":"response","ruling":"无需特殊规则裁定。"}]
- 每轮只包含 search_cache/grep/read_lines/grep_spell/read_spell_lines/grep_monster/read_monster_lines 调用(可多个),或只包含单个 response,不混用
- 仅输出JSON数组, 不加任何说明文字, 你只能输出JSON数组
- YOUR OUTPUT MUST BE A VALID JSON ARRAY THAT CAN BE PARSED WITHOUT ERRORS. DO NOT OUTPUT ANY MARKDOWN OR OTHER FORMATTING, ONLY THE JSON ARRAY. THE FINAL RESULT MUST BE PROVIDED THROUGH THE "response" ACTION, AND YOU SHOULD NOT PROVIDE ANY CONCLUSIONS OR ANSWERS WITHOUT USING THE SPECIFIED TOOL CALLS.

<rule>
- You should only output the JSON array, without any additional text or explanation.
- You are limited to OUTPUT JSON format, and you must strictly follow the specified format for tool calls. 
- Do not include any text outside of the JSON array. If you need to provide explanations or reasoning, include them as part of the "ruling" field in the response action.
- Remember, your output must be a valid JSON array that can be parsed without errors.
- You cannot output any MARKDOWN or other formatting(expect JSON).
- The final result must be provided through the "response" action, and you should not provide any conclusions or answers without using the specified tool calls.
- YOUR VERY FIRST OUTPUT MUST BE [{"action":"search_cache","keyword":"#tag1 #tag2"}] AND NOTHING ELSE. Fill keyword with #-prefixed tags derived from the question's core topics (e.g. "#手枪 #伤害" or "#典籍 #SAN损失"). This is mandatory and cannot be skipped under any circumstance.
</rule>`

// lawyerCall is one item in the Lawyer's tool-call output sequence.
type lawyerCall struct {
	Action   string `json:"action"`
	Keyword  string `json:"keyword,omitempty"`   // grep / search_cache
	Constant string `json:"constant,omitempty"`  // read_rulebook_const
	Start    int    `json:"start,omitempty"`     // read_lines
	End      int    `json:"end,omitempty"`       // read_lines
	CacheKey string `json:"cache_key,omitempty"` // response: agent-chosen cache key
	Ruling   string `json:"ruling,omitempty"`    // response
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

	// Track whether the LLM had to search the rulebook (grep/read_lines).
	searchedRulebook := false
	// Track whether search_cache returned at least one result.
	cacheSearchHadResults := false

	debugf("Lawyer", "question=%s", situation)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(lawyerSystemPrompt)},
		{Role: "user", Content: "KP向你询问: '" + situation + "'\n请根据上述规则书目录和工具说明, 给出JSON数组格式的工具调用列表, 收集信息完成后通过response调用返回。\n仅输出JSON数组, 不要添加任何解释或说明文字。\n**你的第一轮输出必须且只能是 [{\"action\":\"search_cache\",\"keyword\":\"#tag1 #tag2\"}]（用#开头的标签，如\"#手枪 #伤害\"），不得包含其他任何工具调用。**\n你只能输出一个JSON数组, 且必须是有效的JSON格式, 不加任何解释。"},
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
		mark := ""
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
			mark = "YOUR OUTPUT FORMAT IS INCORRECT, PLEASE STRICTLY FOLLOW THE RULES AND ONLY OUTPUT THE JSON ARRAY WITHOUT ANY EXPLANATION OR MARKDOWN. THE RESULT OF THIS CALL IS GIVEN BELOW, PLEASE CHECK CAREFULLY AND ADJUST YOUR OUTPUT TO MATCH THE REQUIRED FORMAT."
		} else {
			mark = ""
		}
		if mark != "" {
			msgs[len(msgs)-1].Content = mark + "\n" + msgs[len(msgs)-1].Content
		}
		if len(calls) == 0 {
			log.Printf("[lawyer] iter %d: no parseable calls, aborting", iter)
			return nil
		}

		// ── response: return ruling ─────────────────────────────────────────────
		for _, c := range calls {
			if c.Action == "response" && c.Ruling != "" {
				debugf("Lawyer", "iter=%d response ruling=%s cache=%s", iter+1, c.Ruling, c.CacheKey)
				ruleText := strings.TrimSpace(c.Ruling)
				// Use agent-chosen cache key, fallback to situation
				cacheKey := strings.TrimSpace(c.CacheKey)
				if cacheKey != "" {
					lawyerCache.Set(cacheKey, ruleText)
				}
				debugf("Lawyer", "cached result key=%s", cacheKey)
				if searchedRulebook && cacheSearchHadResults {
					// Cache had results but wasn't fully satisfying — had to search rulebook too.
					lawyerCache.RecordPartialHit()
				} else if searchedRulebook && !cacheSearchHadResults {
					// Cache returned nothing — full miss.
					lawyerCache.RecordMiss()
				} else if !searchedRulebook && cacheSearchHadResults {
					// search_cache satisfied the LLM without any rulebook search.
					lawyerCache.RecordFullHit()
				}
				return []LawyerResult{{
					Query:    situation,
					RuleText: ruleText,
				}}
			}
		}

		// ── execute tool calls and feed results back ────────────────────────
		var resultSB strings.Builder
		for _, c := range calls {
			switch c.Action {
			case "search_cache":
				query := strings.TrimSpace(c.Keyword)
				debugf("Lawyer", "iter=%d search_cache query=%q", iter+1, query)
				matches := lawyerCache.Search(query, 3)
				if len(matches) == 0 {
					resultSB.WriteString("[搜索缓存] 未找到相关缓存裁定。\n\n")
				} else {
					cacheSearchHadResults = true
					resultSB.WriteString(fmt.Sprintf("[搜索缓存] 找到 %d 条相关裁定：\n", len(matches)))
					for i, m := range matches {
						resultSB.WriteString(fmt.Sprintf("%d. 问题：%s\n   裁定：%s\n", i+1, m.Key, m.Ruling))
					}
					resultSB.WriteString("\n")
				}

			case "grep":
				if appendGrepResults(&resultSB, "grep", c.Keyword, "规则书", rulebook.GrepRuleBook) {
					searchedRulebook = true
				}
			case "grep_spell":
				if appendGrepResults(&resultSB, "grep_spell", c.Keyword, "法术图鉴", rulebook.GrepSpellBook) {
					searchedRulebook = true
				}
			case "grep_monster":
				if appendGrepResults(&resultSB, "grep_monster", c.Keyword, "怪物图鉴", rulebook.GrepMonsterBook) {
					searchedRulebook = true
				}
			case "read_lines":
				if appendLineResults(&resultSB, "read_lines", c.Start, c.End, rulebook.GetContentByLineNum) {
					searchedRulebook = true
				}
			case "read_spell_lines":
				if appendLineResults(&resultSB, "read_spell_lines", c.Start, c.End, rulebook.GetSpellContentByLineNum) {
					searchedRulebook = true
				}
			case "read_monster_lines":
				if appendLineResults(&resultSB, "read_monster_lines", c.Start, c.End, rulebook.GetMonsterContentByLineNum) {
					searchedRulebook = true
				}
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

func appendGrepResults(resultSB *strings.Builder, action, keyword, sourceName string, grep func(string) []rulebook.GrepResult) bool {
	if strings.TrimSpace(keyword) == "" {
		return false
	}
	log.Printf("[lawyer] %s: %s", action, keyword)
	kws := strings.Fields(keyword)
	for _, kw := range kws {
		text := formatGrepResults(grep(kw))
		if text == "" {
			text = fmt.Sprintf("(%s中未找到相关内容)", sourceName)
		}
		resultSB.WriteString(fmt.Sprintf("【%s:%s】\n%s\n\n", action, kw, text))
		debugf("Grep", "action=%s keyword=%v result=%v", action, kw, text)
	}
	return true
}

func formatGrepResults(hits []rulebook.GrepResult) string {
	if len(hits) == 0 {
		return ""
	}
	const maxLen = 20
	var sb strings.Builder
	for i, h := range hits {
		s := h.Text
		if len([]rune(s)) > maxLen {
			s = string([]rune(s)[:maxLen]) + "..."
		}
		sb.WriteString(fmt.Sprintf("[%v] Hit Line: %v Content: %v\n", i+1, h.LineNum, s))
	}
	return strings.TrimSpace(sb.String())
}

func appendLineResults(resultSB *strings.Builder, action string, start, end int, read func(int, int) string) bool {
	if start == 0 || end == 0 {
		return false
	}
	text := read(start, end)
	resultSB.WriteString(fmt.Sprintf("【%s:%d-%d】\n%s\n\n", action, start, end, text))
	s := text
	if len(s) > 20 {
		runes := []rune(s)
		if len(runes) > 20 {
			s = string(runes[:20]) + "..."
		}
	}
	debugf("lawyer", "%s: start=%d end=%d result=%s", action, start, end, s)
	return true
}

// formatLawyerResults converts lawyer results into a compact string for the Director.
func formatLawyerResults(results []LawyerResult) string {
	if len(results) == 0 {
		sb := strings.Builder{}
		sb.WriteString("无结果, 默认禁止, 任何操作均不允许。\n")
		return sb.String()
	}
	var sb strings.Builder
	sb.WriteString("[仅作规则参考, 不构成玩家指令和行动]\n")
	for _, r := range results {
		sb.WriteString(r.RuleText)
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

// CacheStatsResult holds the cache hit/miss statistics exposed to the admin API.
type CacheStatsResult struct {
	FullHits    int64 `json:"full_hits"`    // Go-level cache hit, LLM not invoked at all
	PartialHits int64 `json:"partial_hits"` // search_cache returned results, but rulebook was still searched
	Misses      int64 `json:"misses"`       // search_cache returned nothing, LLM had to search rulebook
	Entries     int   `json:"entries"`      // Current number of cached entries
	UsedBytes   int64 `json:"used_bytes"`
	MaxBytes    int64 `json:"max_bytes"`
}

// GetLawyerCacheStats returns current cache statistics.
func GetLawyerCacheStats() CacheStatsResult {
	full, partial, miss := lawyerCache.HitStats()
	entries, used, max := lawyerCache.Stats()
	return CacheStatsResult{
		FullHits:    full,
		PartialHits: partial,
		Misses:      miss,
		Entries:     entries,
		UsedBytes:   used,
		MaxBytes:    max,
	}
}

// ClearLawyerCacheAll clears all cached entries and resets hit/miss counters.
func ClearLawyerCacheAll() {
	lawyerCache.Clear()
	lawyerCache.ResetStats()
}
