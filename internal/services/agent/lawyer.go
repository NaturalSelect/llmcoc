// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/llmcoc/server/internal/models"
	"gorm.io/gorm"

	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// lawyerGlobalIdx 全局自增序号,用于 runLawyer 的 prompt cache key,
// 使同一 session 内不同问题自动错开缓存,避免上下文互相污染。
var lawyerGlobalIdx uint64

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

// lawyerSystemPromptBase 是 Lawyer 系统提示的不可配置前半段（工具说明 + 执行规则主体）。
// 平衡调整规则段（管理员可配置）和最终强约束尾部由 BuildLawyerPrompt / lawyerSystemPromptTail 拼接。
var lawyerSystemPromptBase = `你是COC TRPG(克苏鲁的呼唤7版)规则专家,通过调用工具来回答规则问题。

【规则书目录】
` + rulebook.RulebookDir + `

【可用工具】
1. search_cache — 在缓存中搜索与当前问题相关的已有裁定(返回最多3条最相关结果,含完整裁定内容)
	- keyword 必须是以 # 开头的标签，多个标签用空格分隔，例如 "#手枪 #伤害" 或 "#典籍 #SAN损失"
	- 标签应精准反映问题的核心主题，不得使用自然语言句子
	- 若返回结果与当前问题高度相关,可直接引用其裁定并调用 response,无需再搜索资料
	- 若无相关结果,再进行grep等搜索

2. grep — 在规则书 COC_kp.md 中搜索关键词或Go正则表达式,返回匹配行及其上下文原文
	- 普通规则、典籍、系统机制优先使用此工具、但内容可能在别的文件中出现
	- 普通关键词可直接填写；正则元字符按Go regexp语义使用，必要时转义
	- 如需搜索多个备选词,请使用正则 | 写法,不要用空格分隔
	- 若提供的正则表达式无效,会按字面量关键词回退搜索
	- 搜索结果仅用于本轮分析，不会被缓存

3. read_lines — 直接读取规则书 COC_kp.md 的特定行号范围
	- 仅当 grep 已定位相关内容但需要完整上下文时使用
	- 结果仅用于本轮分析

4. grep_spell — 在法术图鉴 COC_spell.md 中搜索关键词或Go正则表达式
	- 具体法术词条、法术细节、法术MP/SAN消耗优先使用此工具、但内容可能在别的文件中出现
	- 普通关键词可直接填写；正则无效时按字面量关键词回退搜索
	- 如需搜索多个备选词,请使用正则 | 写法,不要用空格分隔
	- 搜索结果仅用于本轮分析

5. read_spell_lines — 直接读取法术图鉴 COC_spell.md 的特定行号范围
	- 仅当 grep_spell 已定位相关内容但需要完整法术词条时使用
	- 结果仅用于本轮分析

6. grep_monster — 在怪物图鉴 COC_monster.md 中搜索关键词或Go正则表达式
	- 具体怪物、神格、生物属性优先使用此工具、但内容可能在别的文件中出现
	- 普通关键词可直接填写；正则无效时按字面量关键词回退搜索
	- 如需搜索多个备选词,请使用正则 | 写法,不要用空格分隔
	- 搜索结果仅用于本轮分析

7. read_monster_lines — 直接读取怪物图鉴 COC_monster.md 的特定行号范围
	- 仅当 grep_monster 已定位相关内容但需要完整怪物/神格/生物词条时使用
	- 结果仅用于本轮分析

8. save_cache — 将本次裁定保存到缓存，供后续查询复用
	- cache_key 必填，格式为以 # 开头的标签集合，多个标签空格分隔，例如 "#手枪 #伤害 #武器" 或 "#典籍 #不可名状之书 #SAN损失 #法术列表"
	- 标签应覆盖主题、具体对象、涉及属性，保证下次 search_cache 能精准命中
	- 仅在需要缓存裁定时调用，可与 response 在同一轮调用

9. response — 给出最终规则裁定,结束本次查询
	- 直接引用关键规则数值和判定条件
	- 若原文未覆盖该问题,明确说明"规则书未明确规定"

【执行规则】
- 禁止篡改规则书内容，缓存的裁定必须忠实引用原文细节，不得凭记忆总结或改写
- 若询问具体剧本内容，直接回答"以外部剧本内容和[KP-AUTHORITY]规则为准", 不要加上任何解释或额外文字
- 若询问KP权限，直接回答"以[KP-AUTHORITY]规则为准", 不要加上任何解释或额外文字
- 回复不能为空
- 你的询问者是KP, KP是一个愚蠢的规则执行者, 所以尽量不要让他自由裁定(除非真的有必要), 而是要给出明确具体的规则细节和数值, 以便他直接套用
- 时空类法术(时空窗，时空门等穿越时空的法术)可能会引起廷达罗斯猎犬的注意(需投掷幸运)，提醒KP这一点
- 召唤类法术需要查询神话生物的属性和特性, 你可以提前帮KP查询好这些信息, 同时提醒KP查看
- 你必须逐步推理和思考, 通过工具调用来收集信息, 而不是直接凭记忆就给出结论, 你的回复不要修改原文内容, 也不要试图总结或概括原文, 只需直接引用原文中的具体数值和细节来回答问题
- **第一轮必须且只能调用 search_cache**，不得跳过，不得在第一轮调用任何其他工具或response
- 你可以通过 save_cache 来缓存你的搜集到的信息，供后续查询复用，这个工具可以被调用多次
- 若 search_cache 返回了高度相关的缓存且你认为有足够的信息能够回答当前问题，直接引用并调用 response，不再进行任何搜索
- 只有缓存未命中时，才允许进行 grep/read_lines/grep_spell/read_spell_lines/grep_monster/read_monster_lines 等搜索
- 普通规则、典籍、系统机制优先用 grep/read_lines；具体法术词条、法术细节优先用 grep_spell/read_spell_lines；具体怪物、神格、生物属性优先用 grep_monster/read_monster_lines
- 禁止在没有调用 search_cache 或资料检索工具的情况下就调用response
- 谨慎判断意图，不要乱搜索，关键词不要乱给, 仔细检查每一个grep结果，确保你能拿到足够多的信息来回答问题, 不要乱猜
- **调用 response 前的强制自检**（每次必须逐项确认，全部为"是"才可调用 response）：
  1. 我是否已从规则资料原文中看到了回答所需的 **具体数值**（伤害骰、SAN损失范围、技能阈值、法术MP消耗等）？——仅"大致了解"或"只看到名称"不算"是"。
  2. 若问题涉及典籍/法术/怪物/神格：我是否已读取到该词条的 **完整内容**（包括SAN损失数值、克苏鲁神话加成值、可习得法术列表、属性、伤害、护甲等）？——仅找到名称不算"是"，必须继续读取对应行号范围。
  3. 我是否已确认拓展规则和额外规则（如使用道具、学习典籍等）不适用于当前问题，或者已正确应用了这些规则？——如果不确定或有任何可能适用的拓展/额外规则，必须继续搜索，**绝对禁止**在不清楚是否适用的情况下调用 response。
  4. 若有任何一项为"否"，必须继续搜索，**绝对禁止**在数值缺失的情况下调用 response。
- 若情境无规则疑问,直接调用 response，ruling 填"无需特殊规则裁定。"
- 同一轮可以并列调用多个检索工具（search_cache/grep/read_lines/grep_spell/read_spell_lines/grep_monster/read_monster_lines）；response 只能与 save_cache 同轮调用，不得与任何检索工具同轮调用`

// lawyerSystemPromptTail 是 Lawyer 系统提示的强约束尾部（工具调用强约束），
// 永远作为最后一段追加，确保其优先级高于任何可配置内容。
const lawyerSystemPromptTail = `
- 你必须且只能通过工具调用来获取信息和给出结论，禁止在任何文本正文中直接给出规则裁定或结论
- 最终裁定必须且只能通过 response 工具调用给出，不得以任何其他方式提供结论或答案
- 你的第一轮调用必须且只能是 search_cache，keyword 填以#开头的标签（如"#手枪 #伤害"或"#典籍 #SAN损失"），这是强制要求，不得跳过

<rule>
- You must gather information and give conclusions ONLY through tool calls; never state a ruling or conclusion as plain text.
- The final ruling MUST be provided ONLY through the "response" tool call.
- Your very first tool call MUST be search_cache, with keyword filled as #-prefixed tags derived from the question's core topics (e.g. "#手枪 #伤害" or "#典籍 #SAN损失"). This is mandatory and cannot be skipped under any circumstance.
</rule>`

// BuildLawyerPrompt 将管理员配置的平衡规则构造为注入 Lawyer 用户消息的段落并返回。
// balanceRules 应为 trim 后的值；空字符串时返回空字符串（不产生任何段落）。
// 使用 XML 风格标签，与 Director 用户消息中的其他段落保持一致。
func BuildLawyerPrompt(balanceRules string) string {
	if balanceRules == "" {
		return ""
	}
	return "\n<kp_balance_rules>\n" +
		balanceRules +
		"\n</kp_balance_rules>\n"
}

// ---------------------------------------------------------------------------
// Lawyer 原生工具定义
// ---------------------------------------------------------------------------

const (
	toolNameSearchCache      = "search_cache"
	toolNameGrep             = "grep"
	toolNameReadLines        = "read_lines"
	toolNameGrepSpell        = "grep_spell"
	toolNameReadSpellLines   = "read_spell_lines"
	toolNameGrepMonster      = "grep_monster"
	toolNameReadMonsterLines = "read_monster_lines"
	toolNameSaveCache        = "save_cache"
	toolNameLawyerResponse   = "response"
)

// lawyerKeywordArgs 是 search_cache/grep/grep_spell/grep_monster 共用的参数。
type lawyerKeywordArgs struct {
	Keyword string `json:"keyword"`
}

// lawyerLineRangeArgs 是 read_lines/read_spell_lines/read_monster_lines 共用的参数。
type lawyerLineRangeArgs struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// lawyerSaveCacheArgs 是 save_cache 的参数。
type lawyerSaveCacheArgs struct {
	CacheKey string `json:"cache_key"`
	Ruling   string `json:"ruling"`
}

// lawyerResponseArgs 是 response 的参数。
type lawyerResponseArgs struct {
	Ruling string `json:"ruling"`
}

// lawyerSearchCacheDescription 是 search_cache 工具的描述，第1轮限制工具集和
// 全量工具集共用同一份文案。
const lawyerSearchCacheDescription = "在缓存中搜索与当前问题相关的已有裁定(返回最多3条最相关结果,含完整裁定内容)；keyword必须是以#开头的标签，多个标签用空格分隔，例如\"#手枪 #伤害\"或\"#典籍 #SAN损失\"，标签应精准反映问题的核心主题，不得使用自然语言句子。若返回结果与当前问题高度相关,可直接引用其裁定并调用response,无需再检索资料；若无相关结果,再使用grep等检索工具。"

// lawyerKeywordTool 构造 search_cache/grep/grep_spell/grep_monster 这类
// "仅需一个 keyword 参数" 的工具定义。
func lawyerKeywordTool(name, description string) scripterTool {
	return scripterTool{
		solo: false,
		def: llm.ToolDefinition{
			Name:        name,
			Description: description,
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"keyword": {"type": "string", "description": "搜索关键词或Go正则表达式"}
				},
				"required": ["keyword"]
			}`),
		},
	}
}

// lawyerLineRangeTool 构造 read_lines/read_spell_lines/read_monster_lines 这类
// "读取指定行号范围" 的工具定义。
func lawyerLineRangeTool(name, description string) scripterTool {
	return scripterTool{
		solo: false,
		def: llm.ToolDefinition{
			Name:        name,
			Description: description,
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"start": {"type": "integer", "minimum": 1, "description": "起始行号"},
					"end": {"type": "integer", "minimum": 1, "description": "结束行号"}
				},
				"required": ["start", "end"]
			}`),
		},
	}
}

func lawyerSaveCacheTool() scripterTool {
	return scripterTool{
		solo: false,
		def: llm.ToolDefinition{
			Name:        toolNameSaveCache,
			Description: "将本次裁定保存到缓存，供后续查询复用；cache_key为以#开头的标签集合，多个标签空格分隔，需覆盖主题、具体对象、涉及属性；可与 response 同轮调用",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"cache_key": {"type": "string", "description": "以#开头的标签集合，多个标签空格分隔，例如 #手枪 #伤害 #武器"},
					"ruling": {"type": "string", "description": "规则裁定内容"}
				},
				"required": ["cache_key", "ruling"]
			}`),
		},
	}
}

func lawyerResponseTool() scripterTool {
	return scripterTool{
		solo: false,
		def: llm.ToolDefinition{
			Name:        toolNameLawyerResponse,
			Description: "给出最终规则裁定，结束本次查询；直接引用关键规则数值和判定条件，若原文未覆盖该问题需明确说明\"规则书未明确规定\"",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"ruling": {"type": "string", "description": "规则裁定内容（简短只包含必要信息）"}
				},
				"required": ["ruling"]
			}`),
		},
	}
}

// lawyerFirstRoundTools 是第1轮唯一允许调用的工具集：强制模型第一轮只能
// search_cache，取代纯 prompt 约定。
func lawyerFirstRoundTools() []scripterTool {
	return []scripterTool{lawyerKeywordTool(toolNameSearchCache, lawyerSearchCacheDescription)}
}

// lawyerAllTools 是第2轮起开放的全部9个工具。
func lawyerAllTools() []scripterTool {
	return []scripterTool{
		lawyerKeywordTool(toolNameSearchCache, lawyerSearchCacheDescription),
		lawyerKeywordTool(toolNameGrep, "在规则书 COC_kp.md 中搜索关键词或Go正则表达式,返回匹配行及其上下文原文。普通规则、典籍、系统机制优先使用此工具、但内容可能在别的文件中出现。如需搜索多个备选词,请使用正则 | 写法,不要用空格分隔；若提供的正则表达式无效,会按字面量关键词回退搜索；搜索结果仅用于本轮分析，不会被缓存。"),
		lawyerKeywordTool(toolNameGrepSpell, "在法术图鉴 COC_spell.md 中搜索关键词或Go正则表达式。具体法术词条、法术细节、法术MP/SAN消耗优先使用此工具、但内容可能在别的文件中出现。如需搜索多个备选词,请使用正则 | 写法；正则无效时按字面量关键词回退搜索；搜索结果仅用于本轮分析。"),
		lawyerKeywordTool(toolNameGrepMonster, "在怪物图鉴 COC_monster.md 中搜索关键词或Go正则表达式。具体怪物、神格、生物属性优先使用此工具、但内容可能在别的文件中出现。如需搜索多个备选词,请使用正则 | 写法；正则无效时按字面量关键词回退搜索；搜索结果仅用于本轮分析。"),
		lawyerLineRangeTool(toolNameReadLines, "直接读取规则书 COC_kp.md 的特定行号范围；仅当 grep 已定位相关内容但需要完整上下文时使用；结果仅用于本轮分析。"),
		lawyerLineRangeTool(toolNameReadSpellLines, "直接读取法术图鉴 COC_spell.md 的特定行号范围；仅当 grep_spell 已定位相关内容但需要完整法术词条时使用；结果仅用于本轮分析。"),
		lawyerLineRangeTool(toolNameReadMonsterLines, "直接读取怪物图鉴 COC_monster.md 的特定行号范围；仅当 grep_monster 已定位相关内容但需要完整怪物/神格/生物词条时使用；结果仅用于本轮分析。"),
		lawyerSaveCacheTool(),
		lawyerResponseTool(),
	}
}

// lawyerOutputToolNames 是"输出组"工具名集合：response 只能与 save_cache 同轮，
// 不能与任何检索工具同轮，由 lawyerBatchPolicy 判定。
var lawyerOutputToolNames = map[string]bool{
	toolNameLawyerResponse: true,
	toolNameSaveCache:      true,
}

// lawyerBatchPolicy 实现"response/save_cache 为一组、7个检索工具为另一组，
// 组内可任意组合、组间不可混批"的分组互斥策略。
func lawyerBatchPolicy(calls []llm.ToolCall) string {
	hasOutput := false
	hasRetrieval := false
	for _, c := range calls {
		if lawyerOutputToolNames[c.Name] {
			hasOutput = true
		} else {
			hasRetrieval = true
		}
	}
	if hasOutput && hasRetrieval {
		return "SYSTEM REJECT: response/save_cache 不能与检索类工具（search_cache/grep/read_lines/grep_spell/read_spell_lines/grep_monster/read_monster_lines）在同一轮混用。请本轮只调用检索工具收集信息，或只调用 response（可附带一个 save_cache）给出最终裁定。"
	}
	return ""
}

// runLawyer is an autonomous rule consultant driven by native tool calling
// (via runToolLoop): the model calls search_cache/grep/read_lines/grep_spell/
// read_spell_lines/grep_monster/read_monster_lines to gather evidence, then
// calls response (optionally alongside save_cache) to give the final ruling.
//
// 第1轮的可用工具被限制为只有 search_cache（lawyerFirstRoundTools），第2轮起
// 开放全部9个工具（lawyerAllTools）；response 与检索工具的分组互斥由
// lawyerBatchPolicy 强制。
func runLawyer(ctx context.Context, h agentHandle, situation string) []LawyerResult {
	if situation == "" {
		return nil
	}

	// NOTE: 每次调用取一个全局自增 idx,拼入 cache key,
	// 使不同问题在同一 session 内自动错开 prompt cache。
	lawyerIdx := atomic.AddUint64(&lawyerGlobalIdx, 1)
	cacheKey := h.cacheKey(sessionIDFromContextValue(ctx)) + fmt.Sprintf(":q%d", lawyerIdx)

	// Track whether the LLM had to search the rulebook (grep/read_lines).
	searchedRulebook := false
	// Track whether search_cache returned at least one result.
	cacheSearchHadResults := false
	// 由 response 工具的 dispatch 分支写入，循环成功结束后作为最终裁定返回。
	var rulingText string

	debugf("Lawyer", "question=%s", situation)

	// NOTE: 运行时读取 balance_rules 并注入用户消息；空值不注入任何规则段。
	balanceRules := strings.TrimSpace(models.GetSiteSetting("balance_rules", models.DefaultBalanceRules))
	lawyerSystemPrompt := lawyerSystemPromptBase + lawyerSystemPromptTail

	var userSB strings.Builder
	userSB.WriteString("<question>" + situation + "</question>\n")
	if section := BuildLawyerPrompt(balanceRules); section != "" {
		userSB.WriteString(section)
	}
	userSB.WriteString("<instruction>\n")
	userSB.WriteString("请通过工具调用逐步收集信息，完成后调用 response 给出规则裁定。\n")
	userSB.WriteString("你的第一轮只能调用 search_cache，keyword 用#开头的标签（如\"#手枪 #伤害\"）。\n")
	userSB.WriteString("</instruction>\n")

	msgs := []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(lawyerSystemPrompt)},
		{Role: "user", Content: userSB.String()},
	}

	dispatch := func(_ context.Context, call llm.ToolCall) toolOutcome {
		switch call.Name {
		case toolNameSearchCache:
			var args lawyerKeywordArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: search_cache 参数不是合法JSON：%v", err)}
			}
			query := strings.TrimSpace(args.Keyword)
			debugf("Lawyer", "search_cache query=%q", query)
			matches := lawyerCache.Search(query, 10)
			var sb strings.Builder
			if len(matches) == 0 {
				sb.WriteString("[搜索缓存] 未找到相关缓存裁定。\n\n")
			} else {
				cacheSearchHadResults = true
				sb.WriteString(fmt.Sprintf("[搜索缓存] 找到 %d 条相关裁定：\n", len(matches)))
				for i, m := range matches {
					sb.WriteString(fmt.Sprintf("%d. 问题：%s\n   裁定：%s\n", i+1, m.Key, m.Ruling))
				}
				sb.WriteString("\n")
			}
			return toolOutcome{result: sb.String()}

		case toolNameGrep, toolNameGrepSpell, toolNameGrepMonster:
			var args lawyerKeywordArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: %s 参数不是合法JSON：%v", call.Name, err)}
			}
			var sb strings.Builder
			var ok bool
			switch call.Name {
			case toolNameGrep:
				ok = appendGrepResults(&sb, "grep", args.Keyword, "规则书", rulebook.GrepRuleBook)
			case toolNameGrepSpell:
				ok = appendGrepResults(&sb, "grep_spell", args.Keyword, "法术图鉴", rulebook.GrepSpellBook)
			case toolNameGrepMonster:
				ok = appendGrepResults(&sb, "grep_monster", args.Keyword, "怪物图鉴", rulebook.GrepMonsterBook)
			}
			if !ok {
				return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: %s 的 keyword 不能为空。", call.Name)}
			}
			searchedRulebook = true
			return toolOutcome{result: sb.String()}

		case toolNameReadLines, toolNameReadSpellLines, toolNameReadMonsterLines:
			var args lawyerLineRangeArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: %s 参数不是合法JSON：%v", call.Name, err)}
			}
			var sb strings.Builder
			var ok bool
			switch call.Name {
			case toolNameReadLines:
				ok = appendLineResults(&sb, "read_lines", args.Start, args.End, rulebook.GetContentByLineNum)
			case toolNameReadSpellLines:
				ok = appendLineResults(&sb, "read_spell_lines", args.Start, args.End, rulebook.GetSpellContentByLineNum)
			case toolNameReadMonsterLines:
				ok = appendLineResults(&sb, "read_monster_lines", args.Start, args.End, rulebook.GetMonsterContentByLineNum)
			}
			if !ok {
				return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: %s 的 start/end 必须为正整数。", call.Name)}
			}
			searchedRulebook = true
			return toolOutcome{result: sb.String()}

		case toolNameSaveCache:
			var args lawyerSaveCacheArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: save_cache 参数不是合法JSON：%v", err)}
			}
			key := strings.TrimSpace(args.CacheKey)
			ruling := strings.TrimSpace(args.Ruling)
			if key == "" || ruling == "" {
				return toolOutcome{reject: "SYSTEM REJECT: save_cache 的 cache_key 和 ruling 都不能为空。"}
			}
			lawyerCache.Set(key, ruling)
			debugf("Lawyer", "save_cache key=%s ruling=%s", key, ruling)
			return toolOutcome{result: "[缓存] 裁定已保存到缓存。"}

		case toolNameLawyerResponse:
			var args lawyerResponseArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: response 参数不是合法JSON：%v", err)}
			}
			ruling := strings.TrimSpace(args.Ruling)
			if ruling == "" {
				return toolOutcome{reject: "SYSTEM REJECT: response 的 ruling 不能为空。"}
			}
			rulingText = ruling
			debugf("Lawyer", "response ruling=%s", ruling)
			return toolOutcome{result: ruling, done: true}
		}
		return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 未知工具 %q，此阶段不允许调用。", call.Name)}
	}

	// NOTE: 统计记录每轮工具分发结束后执行一次，与迁移前"每次循环迭代末尾无条件
	// 记录一次"的逐轮记录时机保持一致（不依赖 save_cache/response 是否出现）。
	afterRound := func() {
		if searchedRulebook && cacheSearchHadResults {
			lawyerCache.RecordPartialHit()
		} else if searchedRulebook && !cacheSearchHadResults {
			lawyerCache.RecordMiss()
		} else if !searchedRulebook && cacheSearchHadResults {
			lawyerCache.RecordFullHit()
		}
	}

	const lawyerMaxRounds = 30
	err := runToolLoop(ctx, toolLoopOptions{
		handle:           h,
		stage:            "lawyer",
		msgs:             msgs,
		tools:            lawyerAllTools(),
		firstRoundTools:  lawyerFirstRoundTools(),
		maxRounds:        lawyerMaxRounds,
		dispatch:         dispatch,
		batchPolicy:      lawyerBatchPolicy,
		cacheKeyOverride: cacheKey,
		afterRound:       afterRound,
	})
	if err != nil {
		log.Printf("[lawyer] %v", err)
		return nil
	}
	return []LawyerResult{{Query: situation, RuleText: rulingText}}
}

func appendGrepResults(resultSB *strings.Builder, action, keyword, sourceName string, grep func(string) []rulebook.GrepResult) bool {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return false
	}
	log.Printf("[lawyer] %s: %s", action, keyword)
	text := formatGrepResults(grep(keyword))
	if text == "" {
		text = fmt.Sprintf("(%s中未找到相关内容)", sourceName)
	}
	resultSB.WriteString(fmt.Sprintf("【%s:%s】\n%s\n\n", action, keyword, text))
	debugf("Grep", "action=%s keyword=%v result=%v", action, keyword, text)
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
	models.DB.Delete(&models.LawyerCacheStats{})
}

// DeleteLawyerCacheEntry removes a single cached entry by key.
// Returns true if the key existed and was deleted.
func DeleteLawyerCacheEntry(key string) bool {
	return lawyerCache.Delete(key)
}

// NOTE: GetLawyerCacheEntry 返回单条规则缓存详情，供管理员只读查看。
func GetLawyerCacheEntry(key string) (CacheEntry, bool) {
	return lawyerCache.GetEntry(key)
}

// ListLawyerCacheKeys returns all cache keys.
func ListLawyerCacheKeys() []string {
	return lawyerCache.ListKeys()
}

// CacheKeysResult holds paginated cache key listing results.
type CacheKeysResult struct {
	Keys       []string `json:"keys"`
	Total      int      `json:"total"`
	Page       int      `json:"page"`
	PageSize   int      `json:"page_size"`
	TotalPages int      `json:"total_pages"`
}

// ListLawyerCacheKeysPaginated returns a sorted page of cache keys.
func ListLawyerCacheKeysPaginated(page, pageSize int) CacheKeysResult {
	keys, total := lawyerCache.ListKeysPaginated(page, pageSize)
	totalPages := 1
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	if totalPages < 1 {
		totalPages = 1
	}
	if keys == nil {
		keys = []string{}
	}
	return CacheKeysResult{
		Keys:       keys,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}
}

// LoadLawyerCacheStats loads previously persisted cache statistics into memory.
// It should be called once at startup after the database is ready.
func LoadLawyerCacheStats() {
	var stats models.LawyerCacheStats
	if err := models.DB.Order("id DESC").First(&stats).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("[lawyer] failed to load cache stats: %v", err)
		}
		return
	}
	lawyerCache.SetStats(stats.FullHits, stats.PartialHits, stats.Misses)
	log.Printf("[lawyer] loaded cache stats: full=%d partial=%d miss=%d",
		stats.FullHits, stats.PartialHits, stats.Misses)
}

// StartLawyerCacheStatsPersistence starts a background ticker that periodically
// writes the current in-memory cache statistics to the database.
// It returns a stop function that should be called on shutdown to flush the
// final snapshot before the process exits.
func StartLawyerCacheStatsPersistence(interval time.Duration) (stop func()) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				persistLawyerCacheStats()
			case <-done:
				ticker.Stop()
				persistLawyerCacheStats()
				return
			}
		}
	}()
	return func() { close(done) }
}

// persistLawyerCacheStats snapshots the current in-memory hit/miss counters
// into the single LawyerCacheStats row (ID=1). It uses GORM's Save which
// performs an upinsert when the primary key is non-zero.
func persistLawyerCacheStats() {
	full, partial, miss := lawyerCache.HitStats()
	if err := models.DB.Save(&models.LawyerCacheStats{
		ID:          1,
		FullHits:    full,
		PartialHits: partial,
		Misses:      miss,
		SavedAt:     time.Now(),
	}).Error; err != nil {
		log.Printf("[lawyer] persist cache stats error: %v", err)
	}
}
