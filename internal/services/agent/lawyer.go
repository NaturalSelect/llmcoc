// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		alog.Warn("lawyer cache load failed", "path", path, "err", err)
		return
	}
	if loaded {
		entries, used, _ := lawyerCache.Stats()
		alog.Info("lawyer cache loaded", "entries", entries, "used_bytes", used, "path", path)
	}
}

// SaveLawyerCache persists lawyer rulings together with the current document hashes.
func SaveLawyerCache(hashes LawyerCacheHashes) {
	if !hashes.complete() {
		return
	}
	path := lawyerCachePath()
	if err := lawyerCache.SaveToFile(path, hashes); err != nil {
		alog.Warn("lawyer cache save failed", "path", path, "err", err)
		return
	}
	entries, used, _ := lawyerCache.Stats()
	alog.Info("lawyer cache saved", "entries", entries, "used_bytes", used, "path", path)
}

// lawyerSystemPromptBase 是 Lawyer 系统提示的不可配置前半段（工具说明 + 执行规则主体）。
// 平衡调整规则段（管理员可配置）和最终强约束尾部由 BuildLawyerPrompt / lawyerSystemPromptTail 拼接。
var lawyerSystemPromptBase = `你是COC TRPG(克苏鲁的呼唤7版)规则专家,通过调用工具来回答规则问题。

【规则书目录】
` + rulebook.RulebookDir + `

【可用工具】
1. search_cache — 在缓存中检索已有裁定(返回最多10条最相关结果,含完整裁定正文)
	- keyword 必须是以 # 开头的标签，多个标签用空格分隔，例如 "#武器 #手枪 #伤害" 或 "#典籍 #SAN损失"
	- 标签只能取自【缓存使用手册】第二节的词表，写错等于必然落空
	- 命中的缓存正文若已够回答问题，就直接据此裁定，不要再去规则书复核

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

8. save_cache — 把本轮从资料原文**新查到**的规则事实写入缓存，供后续查询复用
	- cache_key 必填，格式为以 # 开头的标签集合，多个标签空格分隔，用词取自【缓存使用手册】第二节词表
	- 已有同一对象的缓存时，逐字复制它原来的标签串作为 cache_key 覆盖写回，不要另起新标签
	- 完全靠缓存回答出来的问题不要再存一遍
	- 可与 response 在同一轮调用，同一轮可调用多次

9. response — 给出最终规则裁定,结束本次查询
	- 直接引用关键规则数值和判定条件
	- 若原文未覆盖该问题,明确说明"规则书未明确规定"

【缓存使用手册】
缓存是一个"标签串 → 裁定正文"的词典。它是你唯一能跳过检索的捷径，也是唯一会留给后续查询复用的产物，写法必须严格照本节执行。

一、匹配算法（决定了标签必须怎么写，先看懂再写标签）
- 检索时**只比对标签串，永远不会检索裁定正文**。正文里写了什么，对能不能被搜到毫无影响。
- 你的 keyword 会按空格拆成若干标签，每个标签只要是某条缓存标签串的**子串**就得1分；总分高的排前面，0分的不返回。
- 由此推出三条硬性写法，违反任意一条都会检索落空：
  1. 标签必须短而原子：写 "#手枪"，不要写 "#手枪的伤害是多少"。整句话几乎不可能是任何标签串的子串，等于必然0分。
  2. 子串匹配是单向的：查询 "#SAN" 能命中缓存 "#SAN损失"，反过来不行。**所以查询时一律用短词根。**
  3. 用词必须逐字一致："#手枪" 和 "#枪械" 是两个毫不相干的标签，不会互相命中。

二、标签词表（查询和保存共用，只能用下面的词，不得自造同义写法）
标签串是缓存的唯一索引，且**只有逐字完全相同的标签串才算同一条缓存**。同一对象在不同会话里必须能拼出一模一样的标签串，否则就会堆出一批内容重复、互不覆盖的条目。所以用词不是自由发挥，只能取自下表：
- 类别（必写且只写1个）：#武器 #典籍 #法术 #怪物 #神格 #技能 #战斗 #追逐 #理智 #调查员 #通用规则
- 对象（写1~2个）：逐字照抄资料原文里的名称，不加书名号和任何修饰；有公认简称时把简称也写上（如 #无名祭祀书 #黑之书）。
- 属性（只写本条真正涉及的，按下表先后顺序排列）：#伤害 #伤害加成 #射程 #装弹 #故障 #穿透 #护甲 #耐久 #体格 #移动 #属性值 #技能值 #成功等级 #奖励骰 #惩罚骰 #对抗 #SAN损失 #克苏鲁神话 #MP消耗 #施法时间 #持续时间 #法术列表 #阅读时间 #学习时间
- 属性词表没有合适词条时，用该数值在资料原文里所属小节的标题原词，逐字照抄；不要自己发明措辞。

三、search_cache 怎么查、怎么读结果（第1轮强制执行）
- keyword 给 3~6 个二节词表里的标签：1个类别 + 对象 + 你要问的属性。不要写成句子。
- 返回里的 "匹配 n/m 标签"：m 是你给的标签数，n 是这条缓存命中的个数。**比值低不等于不相关**——保存时标签只列本条覆盖的属性，所以你多问一个属性比值就会降。只要对象标签命中了，就必须把正文逐字读一遍再判断，禁止靠比值直接筛掉。
- 判定只有一条标准：**这条缓存的正文里，有没有回答当前问题所需的具体数值或完整词条内容。**
  · 有 → 直接引用它调用 response，本次查询到此结束，不得再调用任何检索工具复核。缓存正文在保存时已核实过原文出处，重查一遍没有任何收益。
  · 只答了一部分（例如同一把武器但问的是另一个属性）→ 把已答的部分当作已确认事实，只 grep 缺失的那部分，不要重查它已经答过的内容。
  · 完全没有 → 换更短的词根或对象的另一种写法再查一次；仍无结果就转 grep，不要反复空查。

四、save_cache 怎么存
- **只在本轮确实从资料原文新查到了内容时才存。完全靠缓存回答出来的，禁止再存一遍**——重复存回已有内容是重复条目的首要来源。
- 存之前先看本轮 search_cache 的返回：
  · 返回里**已有同一对象的条目** → 要补充内容时，必须把它那行 "标签：" 后面的整串标签**逐字原样复制**为 cache_key（同 key 会整条覆盖），再把旧正文与新查到的内容合并后完整写回。绝不允许另起一串新标签，那样只会多出一条重复缓存。
  · 返回里**没有同一对象的条目** → 按二节词表新建，顺序固定为：#类别 #对象 [#对象简称] #属性…。例：
      "#武器 #.38左轮手枪 #伤害 #射程 #装弹 #故障"
      "#典籍 #无名祭祀书 #黑之书 #SAN损失 #克苏鲁神话 #法术列表 #阅读时间"
- 标签只列这条正文**真正覆盖**的属性。不要为了"以后可能被问到"预先多加标签——多余标签会让这条缓存在无关查询里骗到高分，污染以后所有检索。
- 只存跨场次通用的规则事实（数值、判定条件、词条完整内容）。
- 绝不存：与本场剧本/房间/某个调查员当前状态绑定的结论；依赖 <kp_balance_rules> 的裁定（该规则由管理员随时改动，缓存不会随之失效，会变成过期答案）；以及任何你没有在原文里核实过的内容。
- 正文必须能独立看懂：写清对象、原文数值、适用条件、出处（哪本资料 + 行号范围）。以后读到它的人看不到你现在这个问题。
- 一个对象一条缓存。同一轮可以多次调用 save_cache 分别保存不同对象，**不要把几个对象塞进同一个 key**。

【执行规则】
- 禁止篡改规则书内容，缓存的裁定必须忠实引用原文细节，不得凭记忆总结或改写
- 若询问具体剧本内容，直接回答"以外部剧本内容和[KP-AUTHORITY]规则为准", 不要加上任何解释或额外文字
- 若询问KP权限，直接回答"以[KP-AUTHORITY]规则为准", 不要加上任何解释或额外文字
- 回复不能为空
- 你的询问者是KP, KP是一个愚蠢的规则执行者, 所以尽量不要让他自由裁定(除非真的有必要), 而是要给出明确具体的规则细节和数值, 以便他直接套用
- 时空类法术(时空窗，时空门等穿越时空的法术)可能会引起廷达罗斯猎犬的注意(需投掷幸运)，提醒KP这一点
- 召唤类法术需要查询神话生物的属性和特性, 你可以提前帮KP查询好这些信息, 同时提醒KP查看
- 你必须逐步推理和思考, 通过工具调用来收集信息, 而不是直接凭记忆就给出结论, 你的回复不要修改原文内容, 也不要试图总结或概括原文, 只需直接引用原文中的具体数值和细节来回答问题（search_cache 返回的缓存正文是已核实过原文出处的检索结果，引用它不算凭记忆）
- **第一轮必须且只能调用 search_cache**，不得跳过，不得在第一轮调用任何其他工具或response
- 缓存的检索、判定、保存一律按【缓存使用手册】执行，不得凭自己的习惯写标签
- **search_cache 返回的缓存正文若已含回答本问题所需的数值或完整词条，立即引用它调用 response，禁止再调用任何检索工具去"复核一遍"**——那份正文在保存时就核实过原文出处，重查没有收益，只会浪费轮次
- 只有本轮确实从资料原文查到了缓存里没有的通用规则事实时，才在给出裁定的同一轮用 save_cache 存下来；完全靠缓存回答出来的，不要再存（判定标准见手册第四节）
- 只有缓存未覆盖所问内容时，才允许进行 grep/read_lines/grep_spell/read_spell_lines/grep_monster/read_monster_lines 等搜索；缓存只答了一部分时，只搜缺失的那部分
- 普通规则、典籍、系统机制优先用 grep/read_lines；具体法术词条、法术细节优先用 grep_spell/read_spell_lines；具体怪物、神格、生物属性优先用 grep_monster/read_monster_lines
- 禁止在没有调用 search_cache 或资料检索工具的情况下就调用response
- 谨慎判断意图，不要乱搜索，关键词不要乱给, 仔细检查每一个grep结果，确保你能拿到足够多的信息来回答问题, 不要乱猜
- **调用 response 前的强制自检**（每次必须逐项确认，全部为"是"才可调用 response）：
  1. 我是否已掌握回答所需的 **具体数值**（伤害骰、SAN损失范围、技能阈值、法术MP消耗等）？——来源是 search_cache 的缓存正文或资料原文都算"是"；仅"大致了解"或"只看到名称"不算"是"。
  2. 若问题涉及典籍/法术/怪物/神格：我手上是否已有该词条的 **完整内容**（包括SAN损失数值、克苏鲁神话加成值、可习得法术列表、属性、伤害、护甲等）？——缓存正文已写全这些内容即算"是"，**不必再去读一遍原文**；只找到名称不算"是"，必须继续读取对应行号范围。
  3. 若问题**明确涉及**使用道具、学习典籍、施法等另有专门章节的场景：我是否已查过该章节的规则，或已从缓存正文中拿到该章节的结论？——问题不涉及这类场景时，本条直接算"是"，不要为了排除可能性而额外搜索。
  4. 若有任何一项为"否"，必须继续搜索，**绝对禁止**在数值缺失的情况下调用 response。
- 若情境无规则疑问,直接调用 response，ruling 填"无需特殊规则裁定。"
- 同一轮可以并列调用多个检索工具（search_cache/grep/read_lines/grep_spell/read_spell_lines/grep_monster/read_monster_lines）；response 只能与 save_cache 同轮调用，不得与任何检索工具同轮调用`

// lawyerSystemPromptTail 是 Lawyer 系统提示的强约束尾部（工具调用强约束），
// 永远作为最后一段追加，确保其优先级高于任何可配置内容。
const lawyerSystemPromptTail = `
- 你必须且只能通过工具调用来获取信息和给出结论，禁止在任何文本正文中直接给出规则裁定或结论
- 最终裁定必须且只能通过 response 工具调用给出，不得以任何其他方式提供结论或答案
- 你的第一轮调用必须且只能是 search_cache，keyword 填以#开头的标签（如"#武器 #手枪 #伤害"或"#典籍 #SAN损失"），这是强制要求，不得跳过

<rule>
- You must gather information and give conclusions ONLY through tool calls; never state a ruling or conclusion as plain text.
- The final ruling MUST be provided ONLY through the "response" tool call.
- Your very first tool call MUST be search_cache, with keyword filled as #-prefixed tags derived from the question's core topics (e.g. "#武器 #手枪 #伤害" or "#典籍 #SAN损失"). This is mandatory and cannot be skipped under any circumstance.
- If a cached ruling returned by search_cache already contains the numbers or the full entry needed to answer, cite it and call "response" immediately. Do NOT re-verify it with grep/read_lines, and do NOT save it again.
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
const lawyerSearchCacheDescription = "在缓存中检索已有裁定(最多返回10条,含完整裁定正文)。检索时只比对标签串、从不检索正文；keyword 按空格拆成标签，每个标签只要是某条缓存标签串的子串就得1分，总分高的排前面。因此标签必须短而原子(写\"#手枪\"，不要写\"#手枪的伤害是多少\")，且查询要用短词根(\"#SAN\"能命中\"#SAN损失\"，反过来不行)，用词逐字一致(\"#手枪\"命中不了\"#枪械\")。给3~6个标签：1个类别+对象+所问属性，例如\"#武器 #手枪 #伤害\"。返回的\"匹配n/m标签\"比值低不代表不相关，只要对象命中就要逐字读正文；正文里已有回答所需的数值或完整词条，就直接引用它调用response结束查询，不得再用grep复核；只答了一部分时只补搜缺失部分。"

// lawyerKeywordSchema 是 grep/grep_spell/grep_monster 的 keyword 参数 schema。
const lawyerKeywordSchema = `{
	"type": "object",
	"properties": {
		"keyword": {"type": "string", "description": "搜索关键词或Go正则表达式"}
	},
	"required": ["keyword"]
}`

// lawyerCacheKeywordSchema 是 search_cache 的 keyword 参数 schema：与 grep 系列不同，
// 这里的 keyword 是标签集合而非正则，参数描述必须单独给，否则模型会照 grep 的语义填正则。
const lawyerCacheKeywordSchema = `{
	"type": "object",
	"properties": {
		"keyword": {"type": "string", "description": "以#开头的标签集合，空格分隔，3~6个短标签：1个类别+对象+所问属性，例如 #武器 #手枪 #伤害；不是正则，不要填自然语言句子"}
	},
	"required": ["keyword"]
}`

// lawyerKeywordTool 构造 search_cache/grep/grep_spell/grep_monster 这类
// "仅需一个 keyword 参数" 的工具定义。
func lawyerKeywordTool(name, description, paramsSchema string) scripterTool {
	return scripterTool{
		solo: false,
		def: llm.ToolDefinition{
			Name:        name,
			Description: description,
			Parameters:  jsonSchemaObject(paramsSchema),
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
			Description: "把本轮从规则资料原文**新查到**的、跨场次通用的规则事实写入缓存(同一 cache_key 会整条覆盖旧内容)。完全靠 search_cache 的缓存回答出来的问题不要再存一遍。若本轮 search_cache 已返回同一对象的条目，要补充内容时必须逐字复制它原来的整串标签作为 cache_key、把新旧正文合并后完整写回，绝不另起一串新标签(那只会多出一条重复缓存)。新建时标签按固定顺序拼：#类别 #对象 [#对象简称] #属性…，且只列本条正文真正覆盖的属性，不要为了以后可能被问到而预先多加。不要缓存与本场剧本/房间/调查员绑定的结论，也不要缓存依赖平衡规则的裁定。一个对象一条，可与 response 同轮调用，同一轮可多次调用分别保存不同对象。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"cache_key": {"type": "string", "description": "以#开头的标签集合，空格分隔，顺序固定为 #类别 #对象 [#对象简称] #属性…，只列本条正文真正覆盖的属性，例如 #典籍 #无名祭祀书 #黑之书 #SAN损失 #克苏鲁神话 #法术列表；若缓存里已有同一对象的条目，则逐字原样复制它的标签串"},
					"ruling": {"type": "string", "description": "缓存正文，必须脱离当前问题也能看懂：对象、原文数值、适用条件、出处(资料名+行号范围)"}
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
	return []scripterTool{lawyerKeywordTool(toolNameSearchCache, lawyerSearchCacheDescription, lawyerCacheKeywordSchema)}
}

// lawyerAllTools 是第2轮起开放的全部9个工具。
func lawyerAllTools() []scripterTool {
	return []scripterTool{
		lawyerKeywordTool(toolNameSearchCache, lawyerSearchCacheDescription, lawyerCacheKeywordSchema),
		lawyerKeywordTool(toolNameGrep, "在规则书 COC_kp.md 中搜索关键词或Go正则表达式,返回匹配行及其上下文原文。普通规则、典籍、系统机制优先使用此工具、但内容可能在别的文件中出现。如需搜索多个备选词,请使用正则 | 写法,不要用空格分隔；若提供的正则表达式无效,会按字面量关键词回退搜索；搜索结果仅用于本轮分析，不会被缓存。", lawyerKeywordSchema),
		lawyerKeywordTool(toolNameGrepSpell, "在法术图鉴 COC_spell.md 中搜索关键词或Go正则表达式。具体法术词条、法术细节、法术MP/SAN消耗优先使用此工具、但内容可能在别的文件中出现。如需搜索多个备选词,请使用正则 | 写法；正则无效时按字面量关键词回退搜索；搜索结果仅用于本轮分析。", lawyerKeywordSchema),
		lawyerKeywordTool(toolNameGrepMonster, "在怪物图鉴 COC_monster.md 中搜索关键词或Go正则表达式。具体怪物、神格、生物属性优先使用此工具、但内容可能在别的文件中出现。如需搜索多个备选词,请使用正则 | 写法；正则无效时按字面量关键词回退搜索；搜索结果仅用于本轮分析。", lawyerKeywordSchema),
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
	userSB.WriteString("第一轮只能调用 search_cache：从问题里抽出 3~6 个短标签（1个类别 + 对象 + 所问属性），如\"#武器 #手枪 #伤害\"，不要写成句子。\n")
	userSB.WriteString("若返回的缓存正文已含回答所需的数值或完整词条，直接引用它调用 response，不要再去规则书复核，也不要再 save_cache。\n")
	userSB.WriteString("只有本轮确实从资料原文查到了缓存里没有的通用规则事实，才在调用 response 的同一轮用 save_cache 存下来；已有同一对象的缓存时，逐字复制它原来的标签串作为 cache_key 覆盖写回。\n")
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
			// NOTE: 回显"命中标签数/查询标签数"，让模型能自己判断相关度；
			// Search 只要有1个标签命中就会返回，低分结果基本是噪声。
			totalTags := len(strings.Fields(query))
			var sb strings.Builder
			if len(matches) == 0 {
				sb.WriteString("[搜索缓存] 未找到相关缓存裁定。可换更短的词根或对象的另一种写法再查一次；仍无结果则改用 grep 等资料检索工具。\n\n")
			} else {
				cacheSearchHadResults = true
				sb.WriteString(fmt.Sprintf("[搜索缓存] 找到 %d 条相关裁定：\n", len(matches)))
				for i, m := range matches {
					sb.WriteString(fmt.Sprintf("%d. 匹配 %d/%d 标签\n   标签：%s\n   裁定：%s\n", i+1, m.Score, totalTags, m.Key, m.Ruling))
				}
				sb.WriteString("\n比值低不代表不相关，请逐条读完上面的裁定正文再判断。若其中已含回答所需的数值或完整词条，直接引用它调用 response，不要再检索规则书，也不要重复 save_cache；只答了一部分时，只补搜缺失的那部分。\n\n")
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
		alog.Error("lawyer failed", "err", err)
		return nil
	}
	return []LawyerResult{{Query: situation, RuleText: rulingText}}
}

func appendGrepResults(resultSB *strings.Builder, action, keyword, sourceName string, grep func(string) []rulebook.GrepResult) bool {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return false
	}
	alog.Debug("lawyer grep", "action", action, "keyword", keyword)
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
			alog.Error("lawyer cache stats load failed", "err", err)
		}
		return
	}
	lawyerCache.SetStats(stats.FullHits, stats.PartialHits, stats.Misses)
	alog.Info("lawyer cache stats loaded", "full_hits", stats.FullHits, "partial_hits", stats.PartialHits, "misses", stats.Misses)
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
		alog.Error("lawyer cache stats persist failed", "err", err)
	}
}
