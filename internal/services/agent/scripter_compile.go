// scripter_compile.go — Compiler stage: takes the story architect's free-text
// StoryOutput (scripter_story.go) and compiles it into a structured
// ScenarioDraft (OneshotResult shape), without inventing new facts. Runs as a
// plain JSON-mode chat (no native tool calling): the target structure
// (submit_compiled_scenario's former schema, dozens of fields with several
// nested arrays) is too large/deep for a single native tool_use call to stay
// reliable — models intermittently drop required top-level fields or
// serialize a nested object field as a string. Plain JsonChat + RepairJSON
// mirrors the proven pattern already used by character.go/npc.go/evaluator.go
// for the same reason; structural/logic issues found afterwards are still
// patched by the existing repairOneshotDraft loop.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

// compilerSystemPrompt 定义编译器角色：只做故事文档到结构化JSON的忠实转换，不创作新事实。
func compilerSystemPrompt() string {
	return `<role>COC7剧本编译器</role>
<task>
你不是剧本创作者，而是格式编译器。<story_document> 是一份已经写定的完整COC7模组成稿，其中的真相、地点、人物、线索、时间线与结局均已确定。你的唯一任务是把这些既定事实忠实地转换成结构化JSON，严禁改写、新增、删减或重新设计任何情节、人物、线索、结局或神话锚点。

【关于故事文档的形式】
成稿是按职业模组的写法组织的，不是按JSON字段分类整理的：它通常沿调查员实际会走的地理或时间顺序排篇，小标题是地名或事件名；人物写在他所在的那一段里，线索写在能拿到它的那一处，检定和后果写在叙事句里（"成功的侦查检定可以发现……"）。文中不会出现"地点清单""NPC清单""线索清单""游玩大纲"这类小节标题，也不会出现"可见信息：""议程：""秘密：""性质：真实"这类要素标签。

所以你要做的是通读全文、理解脉络之后做语义识别，而不是按小节标题或字段标签做匹配：自己判断哪一段描写的是一处调查员会去的地方、哪几句介绍的是一个人、哪一句给出的是一条可获得的信息、哪一段是一种收场。同一处地点的信息可能散落在几个段落里（例如某个人物的秘密在别处才被揭穿），要合并；一个小标题下也可能同时包含地点描写、人物介绍与多条线索，要拆开。绝不能因为文中没有对应的标签或小节就把某个字段留空。

允许的合理补全仅限于：故事文档未写明具体数值时（如NPC属性），按COC7规则书惯例给出合理数值；故事文档地点/NPC无英文标识时，为scene.id生成snake_case标识符。这类补全不得引入新的事实或改变已有事实。

【字段映射规则】
- name：取故事文档标题；若文档未给出明确标题，从文档内具体名词（地名/物件/日期/一句当地话）提炼一个像人类作者起的标题，不用"低语/回响/深渊/阴影/凝视/苏醒/沉睡/诅咒"等滥用词
- content.setting：取自故事文档的表层情境原文或忠实改写，必须保留其中嵌入的具体年月日（如"1923年10月15日"）；该字段同时作为模组对外展示的简介
- content.intro：取自故事文档中调查员到场情境与基本理由；不列出、不推荐、不暗示任何具体行动或下一步
- content.tone_tags：必须逐字等于<diversity_constraints>中的对应值，不得自行替换
- content.invest_focus：通读全文后，用一个简短短语概括故事文档已确立的调查入口（调查员最初从哪一类异常介入），忠实提炼，不得自创文档中不存在的入口
- content.mythos_anchor：必须逐字等于<mythos_anchor>输入
- content.scenes：通读全文，识别出所有调查员会实际走到的地方（无论它是否有独立小标题、无论按什么顺序出现），每处编译为一个scene；scene.id为snake_case英文标识；description要把该地点在文中散落的全部信息合并写全——进门能直接看见和感知到什么、深入查探能发现什么以及需要什么检定、这里可能发生什么危险、从这儿能通往哪些地方；triggers默认["available_from_start"]，仅当文中叙述明确表示需要先获得某个发现或完成某个行动才会来到此处时，才使用条件触发
- content.npcs：通读全文，识别出所有有名有姓、调查员可能接触到的人物（他们通常介绍在其所在地点的段落里，而不是集中成名单），每位编译为一个npc；description要把该人物在文中散落的信息合并写全——他的身份与营生、他想要什么、他正在做什么、他瞒着或不愿说的事、他的标志性小细节、他与其他人的关系；文中若明确写出他隐瞒或保留了某些信息，description须把这一点写明；stats按COC7规则书惯例给出合理属性值（含SAN、HP、MP），文中已给出数值表时直接采用；attitude取自文中写明或可直接判断的初始态度；skills按该人物的职业/角色身份给出3-6项最相关的技能及数值（COC7标准范围，普通人类技能值通常15-75）；spells仅文中明确写明会施法的人物才填写，普通人类留空数组
- content.clues：通读全文，识别出所有调查员能亲自获得的具体发现——一份文件、一句证词、一处痕迹、一个检定结果、一件物品——每条编译为{summary,source,skill_check,on_success,on_failure,nature}。这些发现在文中通常写在叙事句里（"成功的侦查检定会让调查员发现地板上的一张纸条"），没有单独的清单，你需要自己把它们摘出来。nature由你根据文意判定："真实"指调查员可直接查证、指向主线的观察；"隐藏"指揭示神话存在本身是什么的那类发现；"误导"指本身真实、却被文中某人给出了看似合理却错误的解释、从而会把调查引向无关结论的那类发现。source填该发现来自哪个地点、哪个人或哪件物品；skill_check/on_success/on_failure按文中写明的检定名、难度与成败后果对应填写，文中未写明时可留空；至少一条nature="隐藏"的线索，其summary须包含"神话本质"字样并与mythos_anchor强绑定——这个字样是给运行时系统识别用的标记，故事文档里不会出现，由你补上
- content.endings：通读全文，识别出所有互不相同的收场（通常集中在结尾附近，但也可能散见于"如果调查员没有……"这类叙述中），每种编译为{name,trigger,description,san_reward,is_failure}；文中已给出收场名称的直接采用，未命名的按其内容取一个名字；trigger改写为"如果[条件]，则[处境变化]"的条件句结构；san_reward由你依据文中描述的理智冲击轻重程度（是否失控、是否留下永久创伤、能否很快缓过来）对照COC7规则书惯例换算出一个具体骰值（如"恢复1d6"/"损失1d10"）；文中偶尔会直接给出具体数值，此时优先采用，多数情况下文中只有叙事化描述，由你自行判断给出；is_failure标记灾难/失败向的收场；每一种独立的收场都要对应一个ending，不得合并或省略
- content.game_start_slot：从故事文档嵌入的具体时刻推算（0-47，每槽30分钟）；文档未写明具体时刻时取16
- content.map_description：根据故事文档的地点关系概括为文字地图，体现可回访、可交叉验证的调查网络
- content.playthrough_outline：由你依据全文脉络归纳出一份逐场景的游玩流程大纲，供KP按此把握主线。故事文档不会有独立的"游玩大纲"小节——路线、解锁条件与分支是写在正文叙述里的（"一个更简单前往矿井的方法是开卡车沿路上去""如果调查员先去了X，则……"），你需要把它们抽出来串成流程：开场调查员如何进入第一个场景；此后每个场景的进入条件（默认开局即可进入，或需先获得什么发现、完成什么行动）、在此能接触到的人物、能获得的发现、以及从这里通向哪些场景（有分支写清"若…则…"）；最后是哪些条件分别导向哪种收场。大纲中出现的场景、人物、发现与收场必须全部来自本次编译产出的内容，不得引入文档中不存在的东西
- content.timeline：通读全文，识别出所有已发生事件与当天推进的时间节点（它们可能集中在一段时间线里，也可能散见于叙述中），逐条提取为{time,event,phase:past|current}；event须写成中性事实记录句（谁在何时做了什么、什么状态发生了变化）；若故事文档以对话、引语或文学化叙事呈现该时间节点，只转述句式、不引用人物原话，事实内容本身不得改变；文档确实没有可提取的时间节点则留空数组
- content.keeper_appendix：本对象必须给出，不得整体省略。core_truth必填，复述故事文档的核心真相与mythos_anchor对故事的必要性（即为何不可替换）；antagonist_dossier在故事文档写明了施动者时必填——邪教组织的名称伪装/教义/仪式/结构/招募控制/经济据点/历史渊源，或个人施法者的身份掩护/接触契机/法术能力/终极目的，或神话生物的来历/栖身范围/可观察影响/行为驱动，须完整保留，不得压缩为一句话；文档确无独立施动者可写时留空。其余6个运营建议子字段（difficulty_down/difficulty_up/solo_advice/group_advice/horror_tips/theme_guidance）通读全文识别难度调节、单双人团建议或恐怖呈现提示（可能是集中一段，也可能是散在各处对守密人说的话）归拢填入；给守密人的建议在成稿中常以"守密人应当……"这类口语化提示直接插在对应段落里，需要你把它们归拢到对应子字段；文档没有对应内容的子字段留空
- content.mechanics：通读全文，识别出是否描述了可量化追踪的机制（如计数器、行动时钟）（可能是集中一段，也可能是散在各处），提取为{name,type:counter|clock|tracker,description,stages:[{label,effect,trigger}]}；这些机制仅供KP参考，不做自动结算；文档未设计此类机制则留空数组
- reward_concept：本篇的通关奖励概念，由你设计，不是从原文摘抄。故事文档通常不会写"奖励"二字，但它的世界里必然存在与真相相关的实物。通读全文后，从文中已确立的施动者、地点、人物与神话锚点里指认（或据此合成）一件调查员在非失败结局后能带走的实体载体——邪教用于仪式的器物、施法者留下的笔记与抄本、死者遗物中那册来路不明的书、神话生物栖身处的遗存等——写成一句话叙事概念，说清它是什么、原本属于谁或存放在哪、与核心真相或mythos_anchor是什么关系。硬性边界：只能建立在文档已有的人、地、事、物之上，不得为它新增人物、新增地点、改动结局条件或推翻文中已写明的事实（文中已明写被彻底销毁的物件不得用作奖励）；必须是可携带的实体（典籍/手稿/器物），不得写成"知识""顿悟""名望""某人的信任"这类交不到调查员手上的抽象收获；不写具体规则数值、SAN代价与技能加值（那由后续的奖励设计专家依规则书裁定）；若文档本身已明确写出通关奖励，则原样提炼、不另行设计。本字段必须给出，不得留空

【硬性约束】
- 不得编造、合并或删除故事文档中不存在的人名、地名、事件、线索或结局
- 不得改变故事文档已确定的真相、神话锚点、误导线索的设计意图或结局条件
- nature="隐藏"的线索中，神话本质说明出现的法术名/物品名/怪物名/材质名必须与<mythos_anchor>及故事文档一致，不得新造规则书中不存在的元素
- content.setting/content.intro必须保持故事文档表层情境的冷开场语气：中性日常，不剧透真相、不渲染恐怖、不出现惊悚诡异词汇
- content.setting/content.intro优先照抄故事文档表层情境的原句；确需压缩时只做删句，不改写句式、不把多句合并成一个塞满信息的长句
- content.clues中nature="真实"的线索至少2条且互相独立可组合；不得只编译出单一线索链
- 至少一位NPC的description须写明"秘密"或"保留"信息
</task>
<output_format>
只输出一个JSON对象本体，字段结构严格匹配<schema_skeleton>；不要输出任何解释性文字、前后缀说明或代码块围栏标记；content必须是原生嵌套JSON对象，不得整体或部分序列化成字符串。
</output_format>`
}

// compilerSchemaTemplate 是编译阶段的字段注解式骨架：给出完整字段结构与类型/枚举占位，
// 不填充任何剧情内容。此前用完整示例（oneshotExample，一整份食尸鬼故事）作schema，
// 模型会模仿示例中的叙事词汇，造成跨剧本内容同质；骨架式只传达结构，不传染内容。
const compilerSchemaTemplate = `{
  "reward_concept": "string：本篇通关奖励的一句话叙事概念，由你依据文档已确立的施动者/地点/人物/mythos_anchor设计并指认一件可携带的实体载体（典籍/手稿/器物），说明它是什么、原属谁或存于何处、与核心真相的关系；原文未明写也必须设计，但不得新增人物地点、不得改动结局、不写规则数值与SAN代价；不得留空",
  "name": "string：故事文档标题；无明确标题时从文档内具体名词提炼，不用低语/回响/深渊/阴影/凝视/苏醒/沉睡/诅咒等滥用词",
  "author": "agent-team",
  "tags": "string：2-3个逗号分隔标签，指向本剧本独有的核心叙事装置/桥段，不用抽象风格词；须避开<recent_scenario_tags_blacklist>",
  "min_players": 1,
  "max_players": 4,
  "difficulty": "string：如 normal",
  "content": {
    "setting": "string：优先照抄表层情境原句，压缩只删不改写；必须保留文档中嵌入的具体年月日；同时作为模组简介展示",
    "tone_tags": ["必须逐字等于<diversity_constraints>.tone_tags"],
    "invest_focus": "string：从故事文档中概括出调查入口（调查员最初从哪一类异常介入），忠实提炼，不得自创",
    "intro": "string：优先照抄表层情境原句，压缩只删不改写；调查员到场情境与基本理由；不列出、不推荐、不暗示任何具体行动或下一步",
    "game_start_slot": "int：0-47，每槽30分钟；从文档嵌入的时刻推算，未写明时取16",
    "map_description": "string：按地点关系概括的文字地图，体现可回访、可交叉验证的调查网络",
    "playthrough_outline": "string：由编译器依据全文脉络归纳的逐场景流程大纲，每个场景写明进入条件/可接触人物/可得发现/通向哪里与分支",
    "mythos_anchor": "必须逐字等于<mythos_anchor>输入",
    "scenes": [{"id": "snake_case英文标识", "name": "string", "description": "合并该地点在文中散落的全部信息：直接可见与可感知的、深查才能发现的（含所需检定）、可能发生的危险、通往哪些地方", "triggers": ["默认available_from_start；仅文档明确写出解锁条件时才用条件触发"]}],
    "npcs": [{"name": "string", "description": "合并该人物在文中散落的全部信息：身份与营生、他想要什么、正在做什么、瞒着或不愿说的事、标志性细节、与他人的关系", "attitude": "string：文档写明的初始态度", "stats": {"STR": 50, "CON": 50, "SIZ": 50, "DEX": 50, "APP": 50, "INT": 60, "POW": 50, "EDU": 60, "SAN": 50, "HP": 10, "MP": 10}, "skills": {"按职业身份3-6项最相关技能": 50}, "spells": ["仅文档明确写明会施法者才填，普通人类留空数组"]}],
    "clues": [{"summary": "string：写清这条发现是什么、从哪来、调查员由此知道了什么", "source": "string", "skill_check": "string，可留空", "on_success": "string，可留空", "on_failure": "string，可留空", "nature": "真实|隐藏|误导 三选一"}],
    "endings": [{"name": "string", "trigger": "保持如果[条件]，则[处境变化]的条件句结构", "description": "string", "san_reward": "string：如恢复1d6/损失1d6，文档未写明时按结局性质给出", "is_failure": "bool：标记灾难/失败向结局"}],
    "timeline": [{"time": "string", "event": "string：中性事实记录句，不含引号引用的人物原话", "phase": "past|current"}],
    "keeper_appendix": {"core_truth": "string：必填，KP核心真相与mythos_anchor必要性", "antagonist_dossier": "string：施动者（邪教/施法者/神话生物）细化设定，不得压缩为一句话；文档无独立施动者可留空", "difficulty_down": "string", "difficulty_up": "string", "solo_advice": "string", "group_advice": "string", "horror_tips": "string", "theme_guidance": "string"},
    "mechanics": [{"name": "string", "type": "counter|clock|tracker", "description": "string", "stages": [{"label": "string", "effect": "string", "trigger": "string"}]}]
  }
}`

// compileStoryToModule 把故事 architect 的自由文本 StoryOutput 编译为结构化 ScenarioDraft。
// compiler 未配置时 fallback 到 architect provider。编译走纯文本 JsonChat（不使用原生
// tool calling）：目标结构字段多、content 内嵌7个数组，模型在单次原生工具调用里维持这种
// 深度嵌套时曾观测到两类失败——必填顶层字段（如name）留空、或把content整体序列化成字符串
// 而非原生对象。JsonChat 是这个代码库里同类"大段结构化JSON"生成的既有做法（character.go/
// npc.go/evaluator.go 等都这样做），JSON语法错误交给 RepairJSON（复用 AgentRoleParser
// 的既有修复循环），内容层面的必填校验（name/reward_concept）失败时则把上次输出连同具体的
// 修正要求重新发回去，让compiler带着上下文重新生成一次。
// 第二个返回值是 compiler 从故事文档中提炼的 reward_concept（可能为空），供调用方决定是否
// 触发 reward_agent；reward_concept 不属于 ScenarioDraft 的持久化字段，只在编译产出这一刻使用。
func compileStoryToModule(ctx context.Context, room *scripterRoom, story StoryOutput, constraints ScripterConstraints) (ScenarioDraft, string, error) {
	compiler := room.compiler
	if compiler.provider == nil {
		compiler = room.architect
	}
	if compiler.provider == nil {
		return ScenarioDraft{}, "", fmt.Errorf("compiler/architect provider unavailable")
	}
	sessionID := scripterSessionID(ctx, room)

	userMsg := fmt.Sprintf(
		`<story_document>%s</story_document>
<mythos_anchor>%s</mythos_anchor>
%s
<recent_scenario_tags_blacklist>
%s
</recent_scenario_tags_blacklist>
<schema_skeleton>%s</schema_skeleton>
请将以上故事文档编译为结构化剧本JSON，严格遵循schema_skeleton的字段结构；tags须避开recent_scenario_tags_blacklist中的标签。`,
		story.Document, story.MythosAnchor,
		diversityConstraintsBlock(constraints),
		formatScenarioTagsBlacklist(room.tagsBlacklist),
		compilerSchemaTemplate,
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: compiler.systemPrompt(compilerSystemPrompt())},
		{Role: "user", Content: userMsg},
	}

	cacheKey := compiler.cacheKey(sessionID)
	const maxBusinessRetries = 3
	rewardRetried := false

	for attempt := 1; attempt <= maxBusinessRetries; attempt++ {
		stage := fmt.Sprintf("compile_attempt_%d", attempt)
		logStagePrompt(stage, sessionID, msgs)
		resp, err := compiler.provider.JsonChat(ctx, cacheKey, msgs)
		if err != nil {
			return ScenarioDraft{}, "", fmt.Errorf("compile failed: %w", err)
		}
		recordScripterLLMExchange(ctx, nil, stage, msgs, resp)
		log.Printf("[scripter:compile] session=%s attempt=%d resp_len=%d", sessionID, attempt, len([]rune(resp)))

		var result OneshotResult
		if err := json.Unmarshal([]byte(resp), &result); err != nil {
			fixed, repairErr := RepairJSON(ctx, resp, err, compilerSchemaTemplate)
			if repairErr != nil {
				return ScenarioDraft{}, "", fmt.Errorf("compile failed: JSON修复失败: %w", repairErr)
			}
			if err := json.Unmarshal([]byte(fixed), &result); err != nil {
				return ScenarioDraft{}, "", fmt.Errorf("compile failed: JSON修复后仍无法解析: %w", err)
			}
		}

		if strings.TrimSpace(result.Name) == "" {
			msgs = append(msgs,
				llm.ChatMessage{Role: "assistant", Content: resp},
				llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: name字段不能为空。请连同完整剧本JSON重新输出一次，其余字段保持与上次一致。"},
			)
			continue
		}
		if strings.TrimSpace(result.RewardConcept) == "" && !rewardRetried {
			rewardRetried = true
			msgs = append(msgs,
				llm.ChatMessage{Role: "assistant", Content: resp},
				llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: reward_concept 不能为空。请从故事文档已有的施动者、地点、人物与 mythos_anchor 中指认或合成一件调查员通关后能带走的实体载体（典籍/手稿/器物），写成一句话叙事概念：它是什么、原属谁或存于何处、与核心真相的关系；不得新增人物或地点、不得改动结局、不写规则数值。请连同完整剧本JSON重新输出一次，其余字段保持与上次一致。"},
			)
			continue
		}
		if strings.TrimSpace(result.RewardConcept) == "" {
			log.Printf("[scripter:compile] session=%s reward_concept 重试后仍为空，交由触发点兜底", sessionID)
		}

		draft := result.toScenarioDraft()
		// NOTE: mythos_anchor 已由 story 阶段 translate_anchor 确认，编译阶段强制覆盖，防止LLM篡改。
		draft.Content.MythosAnchor = story.MythosAnchor
		log.Printf("[scripter:compile] session=%s done name=%q scenes=%d npcs=%d clues=%d",
			sessionID, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
		logScripterArtifact("Compiled ScenarioDraft", sessionID, draft)
		return draft, strings.TrimSpace(result.RewardConcept), nil
	}

	return ScenarioDraft{}, "", fmt.Errorf("compile failed: 连续%d次内容校验未通过", maxBusinessRetries)
}
