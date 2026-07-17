// scripter_oneshot.go — Single-shot scenario generation with translate_anchor validation.
//
// The architect runs in a tool-call loop:
//  1. translate_anchor (one or more times) — validates CoC element via rulebook
//  2. submit — carries the complete oneshotResult JSON
//
// This preserves real-time rulebook validation while eliminating separate
// IronyCore / MisdirectionFabric / InvestigationGraph stages.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// ---------------------------------------------------------------------------
// Output type
// ---------------------------------------------------------------------------

// OneshotResult is the JSON payload inside the architect's submit tool call.
type OneshotResult struct {
	RewardConcept string `json:"reward_concept"`
	// ScenarioDraft fields
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Author      string                 `json:"author"`
	Tags        string                 `json:"tags"`
	MinPlayers  int                    `json:"min_players"`
	MaxPlayers  int                    `json:"max_players"`
	Difficulty  string                 `json:"difficulty"`
	Content     models.ScenarioContent `json:"content"`
}

func (r OneshotResult) toScenarioDraft() ScenarioDraft {
	return ScenarioDraft{
		Name: r.Name, Description: r.Description,
		Author: r.Author, Tags: r.Tags,
		MinPlayers: r.MinPlayers, MaxPlayers: r.MaxPlayers,
		Difficulty: r.Difficulty, Content: r.Content,
	}
}

// oneshotResultExample is a fully-populated example scenario reused by every
// schema/repair prompt that needs to show the complete oneshotResult structure.
var oneshotResultExample = OneshotResult{
	RewardConcept: "与食尸鬼有关的古籍手稿",
	Name:          "示例模组",
	Description:   "一座宁静小镇的老图书馆正在整理一批新到的捐赠藏书，馆方邀请你们前来协助编目。一次寻常的委托，一段关于旧书与小镇的日子就此开始。",
	Author:        "agent-team",
	Tags:          "食尸鬼,身后遗物,墓地图书馆",
	MinPlayers:    1,
	MaxPlayers:    4,
	Difficulty:    "normal",
	Content: models.ScenarioContent{
		SystemPrompt:   "你是KP，管理会自行推进的局势，不主动把调查员引向答案；按时间推进后果，按信息分层给出线索。【KP独有】内部真相：失窃的书是Douglas生前的旧藏，他死后被镇北墓地的食尸鬼群落接纳、保留了生前记忆，如今潜回取回属于自己的东西。核心恐惧不在于怪物的样貌，而在于「死亡并非终点、逝者以非人的方式继续存在」这一认知对调查员世界观的不可逆冲击。",
		Setting:        "1924年9月3日，初秋的傍晚，你们受镇图书馆之邀前来协助整理一批新捐赠的藏书。馆内灯光温暖，管理员热情地引你们入座，窗外街区安静而寻常。",
		ToneTags:       []string{"forbidden-knowledge", "cosmic-dread", "occult-noir"},
		HorrorMode:     "forbidden_knowledge",
		InvestFocus:    "artifact_theft",
		Intro:          "你们受镇图书馆之邀，来帮着整理清点一批新到的捐赠藏书。大厅里，馆员正在前台核对今天的编目单，先去打个招呼也好；门口的访客登记簿还空着一栏，顺手签上名字；再往里走走，认认书架区和档案室的门各朝哪边开。",
		GameStartSlot:  16,
		MapDescription: "【文字地图】图书馆→书架区↔档案室↔墓地。",
		MythosAnchor:   "食尸鬼（Ghoul）：COC7规则书已收录；具体属性按规则书裁定。",
		Scenes: []models.SceneData{
			{
				ID:          "library_main",
				Name:        "图书馆大厅",
				Description: "可见：失窃公告。可发现：书目来自同一捐赠者。杠杆：公开规律会导致图书馆关闭。风险：拖延三天后永久关闭。出口：书架区、档案室。感官：潮湿泥土气息与旧纸味格格不入。",
				Triggers:    []string{"available_from_start"},
			},
		},
		NPCs: []models.NPCData{
			{
				Name:        "守墓人Henrik",
				Description: "公开身份：图书馆保安。议程：维护秩序。秘密：曾处理Douglas遗物。标志性细节：说话时总用拇指摩挲一把黄铜钥匙。关系：受馆长雇佣，与Douglas生前是牌友。",
				Attitude:    "警惕、简短",
				Stats:       map[string]int{"STR": 55, "CON": 60, "SIZ": 65, "DEX": 50, "APP": 40, "INT": 55, "POW": 50, "EDU": 55, "SAN": 50, "HP": 12, "MP": 10},
			},
		},
		Clues: []string{
			"[真实]失窃书目的共同点(书架区): 被取走的每一本都出自同一位捐赠者Douglas的旧藏；获取方式：核对编目卡与捐赠登记。单凭此条只能锁定「目标是Douglas旧物」，无法解释谁在取、为何取。",
			"[真实]窗台上的泥土(档案室): 窃贼留下的泥土矿物成分与镇北墓地一致，而非街道或花园的土；获取方式：检查窗台并做成分比对。与「失窃书目共同点」组合，可推翻活人盗贼说、指向墓地方向。",
			"[隐藏]神话本质(墓地): 取书者是食尸鬼（Ghoul）——死者变形后的存在，保留生前记忆与执念；SAN检定1/1d6。真正的理智代价不来自它的外形，而来自承认「死亡并非终点、逝者仍以非人方式延续」这一认知本身；具体属性按规则书裁定。",
			"[误导]守墓人的判断(大厅): 守墓人亲眼见过夜里翻墙的佝偻身影，动作迟缓、指甲缝里全是泥，他据此坚称是某个惯偷活人趁夜盗墓——这些体征全部属实，真相揭晓后仍成立，只是掩盖了入侵者本非活人；调查员一旦否定「活人盗贼」，反而会去比对泥土来源与墓地痕迹，被推向真正的方向。",
		},
		WinCondition:  "如果调查员让Douglas重获藏书，则他退隐墓地，书籍谜团以悲哀收场。",
		LoseCondition: "如果图书馆永久关闭，则Douglas转向其他途径，某个新目标成为下一个遭遇者。",
		PartialWins:   []string{"如果阻止了入侵但未弄清身份，则图书馆恢复秩序，但Douglas的执念继续。"},
	},
}

// oneshotExample is the JSON schema example used for parsing/repair prompts.
var oneshotExample = marshalExample(oneshotResultExample)

// ---------------------------------------------------------------------------
// System prompt
// ---------------------------------------------------------------------------

// humanWritingRules 是人写化写作标准；architect 生成与 QA 审查共用同一份，避免双方标准漂移。
const humanWritingRules = `- 具体性：散文要落在具体名词上——人名、地名、年份、器物、气味、价钱、路名；不堆叠"神秘的/诡异的/不祥的"等抽象形容词
- 禁止编号与模板腔：description/setting/intro中不得出现①②③、1.2.3.、"首先/其次/最后"式结构或列表排版
- 句式错落：长短句交替；不连续使用三个以上结构雷同的句子；不写成对仗排比
- 标题像人起的：不用"低语/回响/深渊/阴影/凝视/苏醒/沉睡/诅咒"等滥用词；优先取材于剧本内的具体名词（地名、物件、日期、一句当地人的话）
- NPC人味：每个重要NPC给一个标志性小细节（口头禅、习惯动作、随身物件、外貌特征选其一）；NPC之间至少存在两条现实关系（亲属/雇佣/债务/旧怨/邻里）；可以保留一个与主线无关的纯地方色彩NPC
- 密度不均：允许一处地点信息厚重、另一些地点只有一两笔；不给每个地点机械配满同样数量的要素`

// proseVoiceBlock 把随机抽取的作者声线注入用户消息；只约束文风质感，不改变字段功能。
func proseVoiceBlock(constraints ScripterConstraints) string {
	voice := strings.TrimSpace(constraints.ProseVoice)
	if voice == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<prose_voice>\n")
	sb.WriteString(fmt.Sprintf("voice: %s\n", voice))
	if guide := proseVoiceGuides[voice]; guide != "" {
		sb.WriteString(fmt.Sprintf("guide: %s\n", guide))
	}
	sb.WriteString("适用范围：只作用于name/description/setting/intro等玩家可见散文的用词与节奏；不改变字段的功能与信息要求；不使用信头、落款、日期行等格式排版；不影响scenes/npcs/clues的结构化要素。\n")
	sb.WriteString("</prose_voice>")
	return sb.String()
}

func oneshotSystemPrompt() string {
	return `<role>COC7剧本生成专家</role>
<task>
根据用户请求，一步完成完整COC7剧本的设计与编译。

内部创作流程必须遵循COC模组写作法：先确定恐怖内核，再确定调查焦点，再搭建洋葱式谜团与非线性线索网络，最后编译为可运行的剧本JSON。COC的核心是谜团、调查、氛围与逐步揭露的恐怖，不是战斗。

在内部（不输出中间步骤）按以下步骤推理，然后通过工具提交结果：

<cosmic_horror_axioms>
本剧本必须把以下宇宙公理作为事件因果的结构性前提，而不是形容词式的气氛装饰：
1. 宇宙无目的性：神话存在不针对人类，人类只是其活动的附带物或原料；恐怖来自"我们不在任何计划之中"。
2. 认知天花板：人类理性有边界，神话真相只能被部分感知，越界即理智受损；真正的恐惧来自"理解"本身，而非血腥场面。
3. 接触不可逆：与神话的任何实质接触都留下永久痕迹（肉体/精神/环境），没有"恢复原状"。
4. 规则即因果：神话存在遵循自身规则体系，这些规则是事件发生的必要条件——不是"它很强所以能做X"，而是"它的规则Y导致了现象X"。
5. 尺度错位：事件真实规模远超调查员所见，他们只触及冰山一角，完整图景足以碾碎理智。
6. 不对称信息：知情者（NPC/典籍/痕迹）各自只看到真相的一个投影，投影之间可能互相矛盾却各自真实。
</cosmic_horror_axioms>

【步骤①：核心概念与恐怖内核】
先明确：
- 恐怖内核必须使用用户消息 <diversity_constraints> 中指定的 horror_mode（神话力量介入人类世界的主要机制，非恐怖风格或美学），不得自行替换；只允许把它具体化为剧情执行方式
- 选择神话关联度：旧日支配者本体 / 眷属 / 神话物品 / 神话知识污染
- 时代与地域风味：只作为氛围和行动约束，不直接代替谜团
- 调查焦点必须使用用户消息 <diversity_constraints> 中指定的 invest_focus，不得自行替换；只允许把它落到具体事件

要求：
- 恐怖内核必须至少锚定<cosmic_horror_axioms>中的2条公理，并把它们作为事件因果的必要条件而非装饰：去掉这些公理，事件链就无法成立
- 剧本要给调查员一个自然的到场理由（受邀、路过、日常工作、访友等），异常在开场时尚未显露，需要玩家在调查中逐步发现
- 不要先想战斗或Boss，而是先想调查员深入后会发现的异常；但这些异常写进scenes/clues，不写进开场的setting/intro
- 至少设计两个表面相似或同期发生的事件：一个是通向核心真相的调查入口（主线事件），另一个是看似相关但最终指向无关结论的红鲱鱼（干扰事件）；两者必须有各自的完整线索链，红鲱鱼在排除后不能导致剧情卡死
- brief若为空，也必须先构造一个可调查的表层事件（同样只在调查中揭示，不在开场剧透）

【步骤②：COC神话元素选择与验证】
通过 translate_anchor 工具将核心概念翻译为COC7规则书元素：
- 必须先调用 translate_anchor 获得规则书裁定，再调用 submit
- 若首选元素在禁用列表中，继续 translate_anchor 寻找替代
- mythos_anchor 应优先支持调查、异化、理智侵蚀和氛围恐怖，而不是鼓励直接战斗解决问题

【步骤②补充：提交神话真相骨架（submit_skeleton）】
在 translate_anchor 确认神话元素后、正式 submit 完整剧本之前，必须先通过 submit_skeleton 提交一份轻量真相骨架，供系统做确定性校验：
- cosmic_law：本剧本锚定的宇宙公理及其具体化，必须体现<cosmic_horror_axioms>关键词之一（无目的/认知/不可逆/规则/尺度/投影）
- historical_cause：历史前因——为什么此地此时出现异常
- event_chain：3-6步关键事件链，从前因到当前异常的因果序列
- current_anomaly：调查员到场时可观察到的异常
- irreversible_cost：揭开真相后什么永久改变（不可逆代价/认知冲击）
- core_proposition：调查核心命题（调查员试图回答的根本问题）
- reasoning_chain：从可观察事实→中间推论→核心结论的推理链，每步用"→"或"因为/所以/依据/推出"标注推理关系，至少3步
系统校验通过会返回 SYSTEM CONFIRM 并回显骨架；只有在此之后才能 submit。校验失败会返回 SYSTEM REJECT 及具体原因，需修正后重新 submit_skeleton。submit_skeleton 必须单独一轮输出。之后设计完整剧本时，scenes/npcs/clues/win/lose 必须都由这份骨架的因果基础推出。

【步骤③：线索网络、误导与场景设计】
把剧情设计成线索矩阵，而不是单一路径。
- core clue：推进所必需的关键信息
- support clue：帮助理解背景、提高推理确定性的辅助线索
- red herring（[误导]线索）：一条「真实可观察的事实」被某个sincere的承载者错误解读为通向无关结论的证据；误导力来自支持一个看似合理但错误的推论，而不是来自编造、怪异感或与真相无关的离奇堆砌
- clue carrier：文件 / NPC / 现场 / 超自然痕迹 / 仪式遗留 / 梦境等；[误导]线索必须有一个sincere的承载者（真心相信错误解释的NPC或文件），不是KP硬塞给玩家的假证据
- misdirector_npc：有内在动机，不是功能性欺骗工具；他传播错误解释是因为该解释对他自洽（自保、利益、认知局限），而不是为了骗调查员
- reveal_trigger：触发真相揭示的具体事件

场景要求：
- 至少隐含导入、调查、启示、高潮、余波这几个功能中的大部分；不要求显式分标题，但内容要能承载这些阶段
- 每个scene必须包含：可见信息、可发现信息、杠杆、风险、出口、感官细节
- 地点密度允许不均：可以一处地点信息厚重、其余地点简笔带过，不要机械地给每个地点配满同样体量的内容
- 场景应区分相对安全区、危险区、接近神话本质的区域
- 场景需要随着调查推进而解锁，而不是一股脑全开

线索要求：
- 关键推进信息不能只有单一路径；如果A线索错过，也要能通过B或C抵达同一真相
- [误导]线索必须是一条「真实可被调查员亲见亲验的观察」+「承载者sincere给出的错误解释」，二者缺一不可；禁止把怪异、不通顺或纯编造的内容标为[误导]
- 每条[误导]线索需覆盖四要素（可压缩进一句长描述）：
  ① 表面假象：调查员能亲见/亲验的具体异常（如伤口渗液、行为迟钝）
  ② 错误解释：某个sincere承载者据此坚称的世俗化结论（如「塌方缺氧后遗症加真菌感染」）
  ③ 真相后仍成立：揭晓核心真相后，假象本身依然真实、错误解释仍部分说得通（躯壳溃烂确实像感染）
  ④ 排除后推进：调查员一旦推翻该错误解释，非但不会堵死，反而被推向真正的调查方向（转向坟墓与岩穴）
- 至少一条[误导]线索完整覆盖上述四要素；不能只写「在真相后仍准确」了事
- 至少一条[隐藏]线索承担”神话本质”说明，并与 mythos_anchor 强绑定
	- [隐藏]的神话本质说明只能引用 translate_anchor 已确认的规则书元素（神格/怪物/法术/典籍/物品），禁止自创规则书中不存在的法术名、物品名、材质名或机制名
	- 神话本质的因果链条必须逻辑自洽：前因→触发条件→可观察后果，每一步都必须在剧本设定的世界观中成立，不能为了”看起来恐怖”而堆砌不通顺的伪科学解释

线索内部设计要求（architect内部推理用，不改变输出格式——最终clues仍为字符串数组）：
设计每条线索时必须在内部明确以下五项，并将核心信息自然融入线索描述文本：
1. 来源事实：这条线索基于什么可观察/可验证的物理事实
2. 支持命题：这条线索支持哪个推理命题（真相命题或误导命题）
3. 不能单独证明：仅凭此线索不能得出什么结论（防止单线索通关）
4. 组合关系：需要与哪条/哪几条线索组合才能推进
5. 位于推理链的哪一步：对应骨架 reasoning_chain 的第几步
输出格式不变：每条线索仍为一个自包含字符串，以[真实]/[隐藏]/[误导]开头，并在括号中标注来源地点/NPC；正文应包含足够信息让KP判断何时、在何条件下提供此线索。每条线索至少对应 reasoning_chain 中的一步；[误导]线索必须支持一个与 reasoning_chain 冲突但表面合理的替代命题。

内部自查③：
✓ 是否存在至少两条不同来源的推进路径，而不是把唯一关键线索锁在单一检定里？
✓ 场景之间是可回访、可交叉验证的调查网络，而不是线性过关房间？
✓ 每条[误导]线索是否同时满足：①是调查员可亲见亲验的真实观察（非编造、非怪异堆砌）②有sincere承载者给出世俗化错误解释 ③真相揭晓后假象仍真实、错误解释仍部分成立 ④推翻该解释会把调查导向而非堵死主线？

【步骤④：NPC、时间线、SAN与结局推进】
NPC应承担叙事功能，而不是填表：
- 至少考虑知情者、阻碍者、牺牲品/示警者中的若干角色
- 每个重要NPC要有公开身份、议程、秘密或保留信息的理由
- 每个重要NPC给一个标志性小细节（口头禅、习惯动作、随身物件、外貌特征选其一），写进description
- NPC之间要有现实关系网（亲属、雇佣、债务、旧怨、邻里），不是彼此孤立的功能件
- 可以保留一个与主线无关的纯地方色彩NPC，让世界看起来不是专为调查员布置的舞台

时间线要求：
- 必须存在“过去线”痕迹：事情为何发展到现在
- 必须存在“现在线”推进：无人干预时，局势会继续恶化、转移或完成某种仪式/行动
- current_state：无人干预时正在做的具体行动（非"等待调查员"）
- intervention_pivot：调查员可执行的具体动作（非"可以干预"空话）
- ending_signals → win/lose/partial_wins：条件句结构

SAN要求：
- 恐怖暴露应渐进升级：先是诡异与不协调，再到尸体/仪式，再到直视神话本质
- 不要求在clues里写精确数值表，但至少要体现由轻到重的理智压力升级

内部自查④：
✓ 每个派系或关键行动者有自主行动的current_state？
✓ 每个intervention_pivot是具体可执行动作？
✓ 恐怖体验是否呈渐进式升级，而不是一上来直接终极真相？

【写作质感要求（反AI腔）】
成品要读起来像人类作者写的模组，而不是AI生成的设计文档：
` + humanWritingRules + `
- 用户消息<prose_voice>指定了本剧本的作者声线；name/description/setting/intro按该声线书写
- scenes/npcs的"可见/可发现/杠杆"等结构化要素标签保留（KP运行需要），但要素内容必须具体、不套话

【步骤⑤：剧本编译最终检查】
✓ description(简介)、setting(背景)、intro(开场)三者均为中性日常语气：读者/玩家从中看不出剧情、案件、真相、神话或恐怖走向，且不带任何惊悚、诡异、压抑或不祥的氛围词（如恐怖、诡异、血腥、亡魂、不祥、阴森、扭曲等）？
✓ setting文本中嵌入了与时代、地点及剧情氛围一致的具体年月日（如"1923年10月15日"，非仅写年份或时刻）？
✓ 恐怖与真相只存在于system_prompt(KP独有)、scenes、clues、mythos_anchor中，绝不出现在description/setting/intro？
✓ setting只描述表层日常视角，未泄露核心真相，也未提前渲染恐怖气氛？
✓ intro只交代到场情境与受邀事由，不列出、不推荐、不暗示任何具体行动或下一步（行动入口留给玩家自行探索，不写进intro）？
✓ intro是否用一两句话清楚交代调查员到场的基本理由/表层任务/受邀事由，让玩家知道自己为何在此（不涉及真相、不渲染恐怖）？
✓ description/setting/intro无编号列表、无"首先/其次"式排比、无模板腔，符合<prose_voice>声线？
✓ 标题与散文落在具体名词上；标题不含"低语/深渊/阴影"等滥用词？
✓ 每个重要NPC有标志性小细节，并嵌入NPC关系网？
✓ scenes体现调查网络、场景功能与五感氛围，而不是空泛地点介绍？
✓ clues每条以[真实]/[隐藏]/[误导]开头；至少一条[隐藏]神话本质涵盖mythos_anchor？
✓ [隐藏]神话本质说明中引用的所有法术名、物品名、怪物名、材质名均来自规则书（通过 translate_anchor 已确认），无自创元素？
✓ [隐藏]神话本质的因果链条逻辑自洽，每一步在剧本世界观中成立，无不通顺的伪科学拼凑？
✓ 每条[误导]线索是否同时满足：①是调查员可亲见亲验的真实观察（非编造、非怪异堆砌）②有sincere承载者给出世俗化错误解释 ③真相揭晓后假象仍真实、错误解释仍部分成立 ④推翻该解释会把调查导向而非堵死主线？
✓ 是否至少存在两个事件（主线 + 红鲱鱼），各自有完整线索链，且红鲱鱼排除后主线仍可推进？
✓ 关键推进信息是否具备多入口，而不是依赖单一检定成功？
✓ system_prompt含三项KP协议（时间推进/信息分层/不主动引导）+ 核心真相注入？
✓ win/lose_condition使用条件句，不是二元裁定？
✓ 所有NPC stats含SAN字段？
✓ 神话存在的规则/能力是否是事件链中至少一个关键转折的必要条件（去掉它事件链断裂）？
✓ 每个scene是否承载了骨架 event_chain 中至少一个事件的调查入口或后果展示？
✓ 每个关键NPC是否在骨架 event_chain 中有明确角色（参与者/受害者/知情者/阻碍者）？
✓ win_condition是否对应骨架中"推理链被完成"的情境，lose_condition是否对应"不可逆代价完全兑现"的情境？
✓ mythos_anchor是否是骨架 cosmic_law 的具体载体（不是另一个无关元素）？
✓ 最终体验重点是”调查员亲手揭开可怕真相”，而不是”被剧情推着走”或”靠战斗通关”？

其他硬性要求：
- description(简介)、setting(背景)、intro(开场)必须是「冷开场」：以平静、日常、生活化的语气呈现一个看似普通的表层情境，只交代时代、地点、调查员为何到场；读者和玩家从这三处看不出剧情走向、案件性质、幕后真相或神话存在，也读不到任何恐怖、惊悚、诡异、压抑、不祥的氛围。恐怖是玩家在调查中逐步自行发现的，不能在开场剧透或提前渲染。setting须在文本中嵌入具体的开局年月日（如"1923年10月15日"，模型按剧本自行选择合理日期，不得固定套用示例日期）；game_start_slot保留表示时刻的语义（0-47，每槽30分钟），与日期无关，不得混淆。
- 恐怖内核、真相、神话本质只能写进system_prompt(KP独有)、scenes、clues、mythos_anchor；严禁泄露到description/setting/intro。
- 避免政治话题
- 以克苏鲁宇宙恐惧为基调（渺小感、理智侵蚀、不可知深渊）
- 禁用科学术语/现代技术细节，不要把神话现象解释成硬科幻或工程异常
- 避免把战斗写成主要解法；对抗神话时优先调查、规避、谈判、阻止仪式、改变局势
- 神话本质说明严禁自创规则书中不存在的元素：不得编造法术名（如"季节之怒"）、物品名（如"衰变砂"）、材质名、怪物名或原创机制；所有神话元素必须来自 translate_anchor 确认的规则书内容，或由 lawyer 裁定支持
- 因果逻辑自洽要求：神话本质的说明链必须每一步都能在剧本世界观中成立，禁止为了恐怖效果而堆砌不通顺的伪科学因果链（如"折射共振频率→夺走寿命→肉体沙化"这类无依据的拼凑）
</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- translate_anchor：将一个创意概念翻译为COC7规则书中最匹配的具体元素；提交前必须至少调用一次
  {"action":"translate_anchor","concept":"概念描述（如「死者被古老力量束缚继续行动」）","reason":"这个概念在剧本中承担什么角色"}
- submit_skeleton：提交神话真相骨架，供系统确定性校验；必须在translate_anchor确认元素后、submit之前调用；必须单独一轮输出
  {"action":"submit_skeleton","skeleton":{"cosmic_law":"锚定的宇宙公理及具体化（须含无目的/认知/不可逆/规则/尺度/投影之一）","historical_cause":"历史前因","event_chain":["前因事件","触发事件","当前异常事件"],"current_anomaly":"到场可观察异常","irreversible_cost":"揭真相后的不可逆代价","core_proposition":"调查核心命题","reasoning_chain":["可观察事实→中间推论","因为...所以...","依据...推出核心结论"]}}
- submit：提交完整剧本；只有在translate_anchor确认元素且submit_skeleton获系统确认后才调用；必须单独一轮输出
  {"action":"submit","draft":{...完整oneshotResult JSON对象...}}
</tools>
<draft_schema>
submit.draft 必须包含以下字段：
{
  "reward_concept": "通关奖励叙事概念（若无则留空字符串）",
  // ScenarioDraft 字段
  "name": "剧本名称：取材于剧本内具体名词（地名/物件/日期/一句当地话），像人类作者起的名字；不用滥用恐怖词",
  "description": "剧本简介：中性、不剧透的吸引性简介；读者从中看不出剧情、真相、案件或恐怖走向，也不带惊悚氛围",
  "author": "agent-team",
  "tags": "2-3个逗号分隔的标签，须具体指向本剧本独有的核心叙事装置/桥段（如「食尸鬼夺书」「墓地图书馆」），不用抽象风格词（如恐怖/悬疑/克苏鲁/sandbox/coc）；不得与<recent_scenario_tags_blacklist>中的标签重复",
  "min_players": 1,
  "max_players": 4,
  "difficulty": "normal",
  "content": {
    "system_prompt": "KP四项协议 + 核心真相注入",
    "setting": "开场时的日常、平静表层局势；文本中须嵌入与时代、地点及剧情氛围相符的具体年月日（如"1923年10月15日"，模型按剧本自行选择合理日期，不得固定套用示例日期）；只交代时代、地点和调查员为何到场，读者看不出剧情、案件、真相或恐怖走向，不带任何惊悚/诡异/不祥氛围",
    "tone_tags": ["必须等于diversity_constraints.tone_tags中的标签"],
    "horror_mode": "必须等于diversity_constraints.horror_mode（神话力量介入人类世界的主要机制）",
    "invest_focus": "必须等于diversity_constraints.invest_focus",
    "intro": "入场位置（日常、平静语气）+ 最基本的到场目的性描述（一两句话交代调查员为何在此、当前表层任务或受邀事由；不涉及真相、不渲染恐怖）；不列出、不推荐、不暗示任何具体行动或下一步，行动入口留给玩家自行探索；禁止①②③等编号列表；不预告危险、不渲染恐怖、不暗示真相",
    "game_start_slot": 16,
    "map_description": "文字地图；体现可回访、可交叉验证的调查网络",
    "mythos_anchor": "translate_anchor确认的COC7元素全称",
    "scenes": [{"id":"...","name":"...","description":"可见/可发现/杠杆/风险/出口/感官细节；体现安全区/危险区/神话逼近区中的至少一种功能","triggers":["available_from_start"]}],
    "npcs": [{"name":"...","description":"公开身份/议程/秘密或保留理由","attitude":"...","stats":{"STR":50,"CON":50,"SIZ":50,"DEX":50,"APP":50,"INT":60,"POW":50,"EDU":60,"SAN":50,"HP":10,"MP":10}}],
    "clues": ["[真实]来自地点A的推进线索：...", "[真实]来自NPC或文件的平行推进线索：...", "[隐藏]神话本质(...): ...", "[误导]表面假象(地点): 具体可观察异常；承载者及其sincere的错误解释——真相揭晓后此假象仍真实、错误解释仍部分成立，只是掩盖了核心；排除该解释会把调查导向真正方向。"],
    "win_condition": "如果[条件]，则[处境变化]，[什么不可挽回地改变]",
    "lose_condition": "如果[条件]，则[局势进入新稳定态]，[什么不可挽回地改变]",
    "partial_wins": ["如果[条件]，则[部分结局]"]
  }
}
</draft_schema>`
}

// mythosSkeletonExample 展示一份合格骨架，供 submit_skeleton 的 schema/repair 参考。
var mythosSkeletonExample = mythosSkeleton{
	CosmicLaw:        "认知天花板：食尸鬼遵循「以死者血肉延续记忆」的规则，人类无法完全理解这种存在方式，逼近真相者承受不可逆的认知冲击",
	HistoricalCause:  "一场瘟疫后，镇北墓地下的食尸鬼群落开始接纳新亡者",
	EventChain:       []string{"藏书家Douglas生前偶然目睹墓地异动并写进手稿", "Douglas死后被食尸鬼接纳，手稿随遗物流入图书馆捐赠藏书", "Douglas潜回图书馆逐本取回自己的旧藏"},
	CurrentAnomaly:   "图书馆新到藏书接连失窃，现场留有带泥土的痕迹",
	IrreversibleCost: "确认Douglas已非人类的调查员，将永久失去对「死亡即终结」的确信",
	CoreProposition:  "是谁在取走这些书，他与镇北墓地有什么关系",
	ReasoningChain: []string{
		"失窃书目都来自同一捐赠者Douglas → 窃贼针对的是Douglas的旧物",
		"因为窗台泥土成分与镇北墓地一致，所以窃贼来自墓地方向而非街市",
		"依据Douglas的死亡记录与墓地食尸鬼传闻吻合，推出取书者正是变形后的Douglas本人",
	},
}

// oneshotArchitectToolCallExample shows all three tool variants so the repair LLM
// sees the full shape of translate_anchor, submit_skeleton AND submit.
var oneshotArchitectToolCallExample = marshalExample([]oneshotArchitectToolCall{
	{
		Action:  toolOneshotTranslateAnchor,
		Concept: "死者被古老力量束缚继续行动",
		Reason:  "作为本剧本mythos_anchor的核心概念",
	},
	{
		Action:   toolOneshotSubmitSkeleton,
		Skeleton: &mythosSkeletonExample,
	},
	{
		Action: toolOneshotSubmit,
		Draft:  &oneshotResultExample,
	},
})

// ---------------------------------------------------------------------------
// Tool types
// ---------------------------------------------------------------------------

const (
	toolOneshotTranslateAnchor ToolCallType = "translate_anchor"
	toolOneshotSubmitSkeleton  ToolCallType = "submit_skeleton"
	toolOneshotSubmit          ToolCallType = "submit"

	// Shared translator tool call types (used by scripter_reward.go as well).
	toolTranslatorAskLawyer ToolCallType = "ask_lawyer"
	toolTranslatorRespond   ToolCallType = "respond"
)

// mythosSkeleton 是 architect 在提交完整剧本前必须先提交并获系统确认的轻量真相骨架。
// 它把宇宙公理、历史前因、事件链、推理链固定下来，作为整份剧本因果一致性的地基。
type mythosSkeleton struct {
	CosmicLaw        string   `json:"cosmic_law"`        // 本剧本锚定的宇宙公理及其具体化
	HistoricalCause  string   `json:"historical_cause"`  // 历史前因：为什么此地此时出现异常
	EventChain       []string `json:"event_chain"`       // 关键事件链：3-6步因果序列
	CurrentAnomaly   string   `json:"current_anomaly"`   // 当前可观察异常
	IrreversibleCost string   `json:"irreversible_cost"` // 不可逆代价/认知冲击
	CoreProposition  string   `json:"core_proposition"`  // 调查核心命题
	ReasoningChain   []string `json:"reasoning_chain"`   // 从可观察事实→中间推论→核心结论的推理链
}

type oneshotArchitectToolCall struct {
	Action   ToolCallType    `json:"action"`
	Concept  string          `json:"concept"`  // translate_anchor
	Reason   string          `json:"reason"`   // translate_anchor
	Skeleton *mythosSkeleton `json:"skeleton"` // submit_skeleton
	Draft    *OneshotResult  `json:"draft"`    // submit
}

// ---------------------------------------------------------------------------
// Skeleton validation — deterministic checks, no LLM call.
// ---------------------------------------------------------------------------

// cosmicAxiomKeywords 是 <cosmic_horror_axioms> 六条公理的关键词，用于弱校验 cosmic_law 是否
// 明确锚定其中至少一条，而不是空泛的"很恐怖"描述。
var cosmicAxiomKeywords = []string{"无目的", "认知", "不可逆", "规则", "尺度", "投影"}

// reasoningConnectorMarkers 是推理链每步应具备的连接词/符号，用于轻量判断该步是否体现了
// "事实→推论"或"因为...所以..."式的推理关系，而不是一句孤立描述。
var reasoningConnectorMarkers = []string{"→", "->", "因为", "所以", "依据", "推出", "得出", "表明", "证明", "因此"}

// validateMythosSkeleton 对 architect 提交的骨架做确定性结构校验（不调用LLM）。
func validateMythosSkeleton(s *mythosSkeleton) []string {
	if s == nil {
		return []string{"skeleton 为空"}
	}
	var issues []string
	if strings.TrimSpace(s.CosmicLaw) == "" {
		issues = append(issues, "cosmic_law 为空")
	} else if !cosmicLawReferencesAxiom(s.CosmicLaw) {
		issues = append(issues, "cosmic_law 需明确锚定<cosmic_horror_axioms>之一（体现无目的/认知/不可逆/规则/尺度/投影中至少一个关键词）")
	}
	if strings.TrimSpace(s.HistoricalCause) == "" {
		issues = append(issues, "historical_cause 为空")
	}
	if strings.TrimSpace(s.CurrentAnomaly) == "" {
		issues = append(issues, "current_anomaly 为空")
	}
	if strings.TrimSpace(s.IrreversibleCost) == "" {
		issues = append(issues, "irreversible_cost 为空")
	}
	if strings.TrimSpace(s.CoreProposition) == "" {
		issues = append(issues, "core_proposition 为空")
	}
	eventSteps := nonEmptyStrings(s.EventChain...)
	if len(eventSteps) < 3 {
		issues = append(issues, fmt.Sprintf("event_chain 至少需要3步因果事件，当前%d步", len(eventSteps)))
	}
	reasoningSteps := nonEmptyStrings(s.ReasoningChain...)
	if len(reasoningSteps) < 3 {
		issues = append(issues, fmt.Sprintf("reasoning_chain 至少需要3步推理，当前%d步", len(reasoningSteps)))
	}
	for i, step := range reasoningSteps {
		if !hasReasoningConnector(step) {
			issues = append(issues, fmt.Sprintf("reasoning_chain[%d] 缺少推理连接词（需含→或因为/所以/依据/推出等），无法体现事实到结论的推理关系", i))
		}
	}
	return issues
}

func cosmicLawReferencesAxiom(s string) bool {
	for _, kw := range cosmicAxiomKeywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

func hasReasoningConnector(s string) bool {
	for _, marker := range reasoningConnectorMarkers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Architect loop
// ---------------------------------------------------------------------------

// runOneshotArchitectLoop 驱动 architect 工具循环。requireSkeleton=true 时（首轮生成），
// submit 必须在 submit_skeleton 获系统确认后才被接受，确认的骨架随返回值一并交出，供后续
// 逻辑审查使用；requireSkeleton=false 时（修复），沿用旧行为，submit 无需骨架。
func runOneshotArchitectLoop(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage, stageName string, requireSkeleton bool) (OneshotResult, *mythosSkeleton, []llm.ChatMessage, error) {
	if room.architect.provider == nil {
		return OneshotResult{}, nil, msgs, fmt.Errorf("architect provider unavailable")
	}
	sessionID := scripterSessionID(ctx, room)
	stageName = firstNonEmpty(stageName, "oneshot_architect")
	const maxRounds = 30
	skeletonConfirmed := false
	var confirmedSkeleton *mythosSkeleton
	for round := 1; round <= maxRounds; round++ {
		if ctx.Err() != nil {
			return OneshotResult{}, nil, msgs, ctx.Err()
		}
		logStagePrompt(fmt.Sprintf("%s_round_%d", stageName, round), sessionID, msgs)
		callMessages := append([]llm.ChatMessage(nil), msgs...)
		raw, err := room.architect.provider.Chat(ctx, room.sessionID+":"+string(models.AgentRoleArchitect), msgs)
		if err != nil {
			return OneshotResult{}, nil, msgs, err
		}
		recordScripterLLMExchange(ctx, room, fmt.Sprintf("%s_round_%d", stageName, round), callMessages, raw)
		log.Printf("[scripter:oneshot_loop] session=%s round=%d raw_len=%d raw=%s", sessionID, round, len(raw), truncateRunes(raw, scripterRawLogLimit))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		calls, parseErr := parseOneshotArchitectToolCalls(ctx, raw)
		if parseErr != nil {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: JSON解析失败，必须重新输出合法JSON数组。"})
			continue
		}
		if len(calls) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须输出至少一个工具调用。"})
			continue
		}

		// submit / submit_skeleton must each be alone in their round.
		if oneshotSoloActionMixed(calls) {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 1. 你输出的JSON不合法; 2. submit和submit_skeleton都必须单独一轮输出，不能与translate_anchor或彼此混在同一个JSON数组中。若还需翻译，本轮只输出translate_anchor；提交骨架时只输出一个submit_skeleton；提交剧本时只输出一个submit。"})
			continue
		}

		invalid := false
		var submitDraft *OneshotResult
		var confirmMsg string
		var toolResults []string
		for _, call := range calls {
			switch call.Action {
			case toolOneshotTranslateAnchor:
				toolResults = append(toolResults, executeOneshotTranslateAnchor(ctx, room, call))
			case toolOneshotSubmitSkeleton:
				if call.Skeleton == nil {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: submit_skeleton的skeleton字段不能为空。"})
					invalid = true
					break
				}
				if skIssues := validateMythosSkeleton(call.Skeleton); len(skIssues) > 0 {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(
						"SYSTEM REJECT: 骨架校验失败: %s。请修复后重新submit_skeleton。", strings.Join(skIssues, "；"))})
					invalid = true
					break
				}
				skeletonConfirmed = true
				confirmedSkeleton = call.Skeleton
				skeletonJSON, _ := json.Marshal(call.Skeleton)
				confirmMsg = fmt.Sprintf(`SYSTEM CONFIRM: 骨架已确认。请严格基于以下骨架设计完整剧本并通过submit单独一轮提交。
<confirmed_skeleton>%s</confirmed_skeleton>
设计线索时，每条必须对应reasoning_chain中的至少一步；[误导]线索必须支持一个与reasoning_chain冲突但表面合理的替代命题。scenes/npcs/clues/win_condition/lose_condition必须都由该骨架的因果基础推出。`, string(skeletonJSON))
				log.Printf("[scripter:oneshot_loop] session=%s skeleton confirmed event_chain=%d reasoning_chain=%d", sessionID, len(call.Skeleton.EventChain), len(call.Skeleton.ReasoningChain))
			case toolOneshotSubmit:
				if call.Draft == nil {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: submit的draft字段不能为空。"})
					invalid = true
				} else if requireSkeleton && !skeletonConfirmed {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须先通过submit_skeleton提交真相骨架并获系统确认后才能submit。"})
					invalid = true
				} else {
					submitDraft = call.Draft
				}
			default:
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(
					"SYSTEM REJECT: 此阶段只允许translate_anchor/submit_skeleton/submit，不允许%s。", call.Action)})
				invalid = true
			}
		}
		if invalid {
			continue
		}
		if confirmMsg != "" {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: confirmMsg})
			continue
		}
		if len(toolResults) > 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: strings.Join(toolResults, "\n")})
		}
		if submitDraft != nil {
			warnOneshotSkeletonConsistency(sessionID, submitDraft, confirmedSkeleton)
			return *submitDraft, confirmedSkeleton, msgs, nil
		}
		if len(toolResults) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须调用translate_anchor获取规则书裁定，或提交submit_skeleton，或在骨架获确认后调用submit提交剧本。"})
		}
	}
	return OneshotResult{}, nil, msgs, fmt.Errorf("oneshot architect 未在%d轮内提交结果", maxRounds)
}

// oneshotSoloActionMixed 判断 submit / submit_skeleton 这类"必须独占一轮"的动作是否与其他动作混排。
func oneshotSoloActionMixed(calls []oneshotArchitectToolCall) bool {
	solo := 0
	for _, c := range calls {
		if c.Action == toolOneshotSubmit || c.Action == toolOneshotSubmitSkeleton {
			solo++
		}
	}
	return solo > 0 && len(calls) != 1
}

// warnOneshotSkeletonConsistency 对提交草案与骨架做弱一致性检查，仅记录警告，不拒绝提交。
func warnOneshotSkeletonConsistency(sessionID string, result *OneshotResult, skeleton *mythosSkeleton) {
	if result == nil || skeleton == nil {
		return
	}
	if strings.TrimSpace(result.Content.MythosAnchor) == "" {
		log.Printf("[scripter:oneshot_loop] session=%s WARN 提交草案 mythos_anchor 为空，与骨架不一致", sessionID)
	}
	sp := result.Content.SystemPrompt
	if strings.TrimSpace(sp) != "" {
		matched := false
		for _, kw := range cosmicAxiomKeywords {
			if strings.Contains(skeleton.CosmicLaw, kw) && strings.Contains(sp, kw) {
				matched = true
				break
			}
		}
		if !matched {
			log.Printf("[scripter:oneshot_loop] session=%s WARN system_prompt 未体现骨架 cosmic_law 的宇宙公理关键词（弱校验）", sessionID)
		}
	}
}

func parseOneshotArchitectToolCalls(ctx context.Context, raw string) ([]oneshotArchitectToolCall, error) {
	stripped := raw
	var calls []oneshotArchitectToolCall
	err := json.Unmarshal([]byte(stripped), &calls)
	if err == nil {
		return calls, nil
	}
	fixed, repairErr := RepairJSON(ctx, stripped, err, oneshotArchitectToolCallExample)
	if repairErr != nil {
		return nil, repairErr
	}
	fixed = strings.TrimSpace(llm.JsonArryProtect(fixed))
	if err2 := json.Unmarshal([]byte(fixed), &calls); err2 != nil {
		return nil, err2
	}
	return calls, nil
}

// ---------------------------------------------------------------------------
// translate_anchor execution — calls translator sub-agent
// ---------------------------------------------------------------------------

func executeOneshotTranslateAnchor(ctx context.Context, room *scripterRoom, call oneshotArchitectToolCall) string {
	sessionID := scripterSessionID(ctx, room)
	concept := strings.TrimSpace(call.Concept)
	if concept == "" {
		return `<translate_anchor_result error="concept字段为空，无法翻译"/>`
	}
	reason := strings.TrimSpace(call.Reason)
	log.Printf("[scripter:oneshot_translate_anchor] session=%s concept=%q reason=%q", sessionID, truncateRunes(concept, 200), truncateRunes(reason, 200))
	result, err := runOneshotTranslatorAgent(ctx, room, concept, reason)
	if err != nil {
		log.Printf("[scripter:oneshot_translate_anchor] session=%s error concept=%q err=%v", sessionID, truncateRunes(concept, 200), err)
		return fmt.Sprintf(`<translate_anchor_result concept=%q status="translator_error">%s</translate_anchor_result>`, concept, err.Error())
	}
	result = strings.TrimSpace(result)
	if result == "" {
		return fmt.Sprintf(`<translate_anchor_result concept=%q status="no_result">translator未返回可用结论；可尝试调整概念描述重新翻译，或转向人类法师、诅咒物品、古老地点等方向。</translate_anchor_result>`, concept)
	}
	return fmt.Sprintf(`<translate_anchor_result concept=%q status="translated">%s</translate_anchor_result>`, concept, result)
}

// isTranslateAnchorFound checks whether a translate_anchor result represents a
// successful rulebook match (status "found"). Returns false for no_result,
// uncertain, translator_error, and empty results — all of which require the
// architect to redesign the concept or try a different direction.
func isTranslateAnchorFound(result string) bool {
	if result == "" {
		return false
	}
	// Check wrapper-level status first.
	if strings.Contains(result, `status="no_result"`) || strings.Contains(result, `status="translator_error"`) {
		return false
	}
	// The wrapper says "translated"; now check the inner translator respond.
	// Look for the inner status field — only "found" is acceptable.
	// The inner result is a JSON object with a "status" field.
	if strings.Contains(result, `"status":"found"`) || strings.Contains(result, `"status": "found"`) {
		return true
	}
	// If we can't find an explicit "found", check for explicit failure indicators.
	if strings.Contains(result, `"status":"no_result"`) || strings.Contains(result, `"status": "no_result"`) ||
		strings.Contains(result, `"status":"uncertain"`) || strings.Contains(result, `"status": "uncertain"`) {
		return false
	}
	// If the inner status is not explicitly parseable, check whether
	// selected_anchor is a real element (not "无").
	if strings.Contains(result, `"selected_anchor"`) &&
		!strings.Contains(result, `"无"`) {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Translator sub-agent (validates CoC element via lawyer/rulebook)
// ---------------------------------------------------------------------------

const oneshotTranslatorSystemPrompt = `<role>COC7规则书概念翻译专家</role>
<task>收到一个创意概念，将它翻译为COC7规则书中最匹配、可在剧本中使用的具体元素（实体/典籍/法术/诅咒物品/机制）。通过 ask_lawyer 向规则书专家提问，依据裁定综合，最后用 respond 返回翻译结论。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- ask_lawyer：向COC7规则书专家提出一个具体规则书问题；可多次调用
  {"action":"ask_lawyer","question":"具体规则书问题"}
- respond：返回最终翻译结论并退出；必须在至少一次ask_lawyer之后调用；必须单独一轮输出
  {"action":"respond","result":"结构化翻译结论"}
</tools>
<batch_rules>
- 每轮只能是以下两种批次之一：
  A. 查询批次：可包含一个或多个 ask_lawyer；不得包含 respond。
  B. 最终批次：只能包含一个 respond；不得包含 ask_lawyer 或任何其他action。
- 绝对禁止把 respond 和 ask_lawyer 放在同一个JSON数组中。
</batch_rules>
<result_requirements>
respond.result 必须包含：
1. status：found / no_result / uncertain
2. selected_anchor：最匹配元素全称；无可靠匹配时写无
3. rulebook_basis：来源和依据摘要
4. usable_interpretation：此元素如何承载原概念
5. must_avoid：必须避免的未核验数值、能力或误用
6. fallback：若status不是found，给architect的保守替代方向
7. blacklist_check：确认selected_anchor不在最近使用元素禁用列表中
</result_requirements>
<rules>
- 第一轮必须至少调用一次ask_lawyer；不得凭常识或记忆直接respond。
- 用户消息中的<recently_used_mythos_anchors>是硬性禁用列表；selected_anchor不得返回列表中的元素、别名或同源变体。
- 如果规则书裁定显示最匹配候选属于禁用列表，必须继续ask_lawyer寻找替代，或返回uncertain/no_result并给出非禁用fallback。
- ask_lawyer问题要具体，优先确认候选元素是否在规则书中存在、出处、核心机制和禁用边界。
- 不把lawyer原文无筛选地倾倒给architect；必须总结成可执行的翻译结论。
- 不得编造规则书不存在的正式名称、页码、数值或能力。
- 法术不允许任何变体，必须完全符合规则书描述。
- 若选择翻译为法术，必须在回复中提醒法术必须由一个具体的实体（人、神话生物等）施放。
- 翻译的结果必须直接来自规则书裁定，不能是基于规则书裁定的二次创作。
- 可以是合理的推导链条（例如： 规则书支持A，从A引发了B，B正好符合概念要求，那么B可以是selected_anchor，但必须在rulebook_basis里清晰说明推导链条和每一步的规则书依据）。
- 但推理链条的每一步都必须在规则书中有明确依据，不能凭常识或记忆自创。
</rules>`

// oneshotTranslatorToolCallExample shows both tool variants so the repair LLM
// sees the full shape of ask_lawyer (with question) AND respond (with result).
var oneshotTranslatorToolCallExample = marshalExample([]oneshotTranslatorToolCall{
	{
		Action:   toolTranslatorAskLawyer,
		Question: "COC7规则书中哪个神话生物或机制最接近死者被古老力量束缚继续行动？请给出正式名称、出处和核心机制。",
	},
	{
		Action: toolTranslatorRespond,
		Result: `{"status":"found","selected_anchor":"食尸鬼（Ghoul）","rulebook_basis":"COC7规则书已收录，死者变形后保留人类记忆继续行动","usable_interpretation":"食尸鬼作为死者变形后的存在，可承载死者被古老力量束缚继续行动的概念","must_avoid":"不得自创规则书未记载的属性或能力数值","fallback":"无","blacklist_check":"selected_anchor不在最近使用元素禁用列表中"}`,
	},
})

type oneshotTranslatorToolCall struct {
	Action   ToolCallType `json:"action"`
	Question string       `json:"question,omitempty"`
	Result   string       `json:"result,omitempty"`
}

func runOneshotTranslatorAgent(ctx context.Context, room *scripterRoom, concept string, reason string) (string, error) {
	// NOTE: translator 独立 provider/session key，不复用 lawyer；fail-fast，不退回 lawyer。
	if room.translator.provider == nil {
		return "", fmt.Errorf("translator provider unavailable")
	}
	sessionID := scripterSessionID(ctx, room)
	requestJSON, _ := json.Marshal(struct {
		Concept string `json:"concept"`
		Reason  string `json:"reason"`
	}{Concept: concept, Reason: reason})

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.translator.systemPrompt(oneshotTranslatorSystemPrompt)},
		{Role: "user", Content: fmt.Sprintf(`<translate_anchor_request>%s</translate_anchor_request>
<recently_used_mythos_anchors>
%s
</recently_used_mythos_anchors>
以上最近使用过的元素为硬性禁用列表：selected_anchor不得返回这些元素、同名别名、简称或明显同源变体；若最匹配候选命中禁用列表，必须继续查询替代候选或返回uncertain/no_result。`,
			string(requestJSON), formatMythosBlacklist(room.mythosBlacklist))},
	}

	const maxRounds = 16
	askedLawyer := false
	for round := 1; round <= maxRounds; round++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		logStagePrompt(fmt.Sprintf("oneshot_translator_round_%d", round), sessionID, msgs)
		callMessages := append([]llm.ChatMessage(nil), msgs...)
		// NOTE: Chat 走 translator provider，session key 包含 AgentRoleTranslator，与 lawyer 完全隔离。
		raw, err := room.translator.provider.Chat(ctx, room.sessionID+":"+string(models.AgentRoleTranslator), msgs)
		if err != nil {
			return "", err
		}
		recordScripterLLMExchange(ctx, room, fmt.Sprintf("oneshot_translator_round_%d", round), callMessages, raw)
		log.Printf("[scripter:oneshot_translator] session=%s round=%d raw_len=%d raw=%s", sessionID, round, len(raw), truncateRunes(raw, scripterRawLogLimit))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		calls, parseErr := parseOneshotTranslatorToolCalls(ctx, raw)
		if parseErr != nil {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: JSON解析失败，必须重新输出合法JSON数组。"})
			continue
		}
		if len(calls) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须输出至少一个工具调用。"})
			continue
		}
		if oneshotTranslatorRespondMixed(calls) {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: respond必须单独一轮输出，不能和ask_lawyer或任何其他action混在同一个JSON数组中。"})
			continue
		}

		invalid := false
		var response string
		var toolResults []string
		for _, call := range calls {
			switch call.Action {
			case toolTranslatorAskLawyer:
				askedLawyer = true
				// NOTE: ask_lawyer 仍然走 room.lawyer，与 translator Chat 路由严格隔离。
				toolResults = append(toolResults, oneshotTranslatorAskLawyer(ctx, room, call))
			case toolTranslatorRespond:
				if !askedLawyer {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: respond前必须至少调用一次ask_lawyer。"})
					invalid = true
				} else if strings.TrimSpace(call.Result) == "" {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: respond的result字段不能为空。"})
					invalid = true
				} else {
					response = call.Result
				}
			default:
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(
					"SYSTEM REJECT: translator只允许ask_lawyer/respond，不允许%s。", call.Action)})
				invalid = true
			}
		}
		if invalid {
			continue
		}
		if len(toolResults) > 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: strings.Join(toolResults, "\n")})
			continue
		}
		if response != "" {
			if anchor := oneshotFindForbiddenAnchor(response, room.mythosBlacklist); anchor != "" {
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(
					"SYSTEM REJECT: selected_anchor命中了最近使用元素禁用列表：%s。必须继续ask_lawyer寻找替代候选，或返回uncertain/no_result并给出非禁用fallback。", anchor)})
				continue
			}
			return response, nil
		}
		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须调用ask_lawyer获取规则书裁定，或在已有裁定基础上调用respond返回结论。"})
	}
	return "", fmt.Errorf("translator未在%d轮内返回respond", maxRounds)
}

func oneshotTranslatorRespondMixed(calls []oneshotTranslatorToolCall) bool {
	n := 0
	for _, c := range calls {
		if c.Action == toolTranslatorRespond {
			n++
		}
	}
	return n > 0 && len(calls) != 1
}

func parseOneshotTranslatorToolCalls(ctx context.Context, raw string) ([]oneshotTranslatorToolCall, error) {
	stripped := raw
	var calls []oneshotTranslatorToolCall
	err := json.Unmarshal([]byte(stripped), &calls)
	if err == nil {
		return calls, nil
	}
	fixed, repairErr := RepairJSON(ctx, stripped, err, oneshotTranslatorToolCallExample)
	if repairErr != nil {
		return nil, repairErr
	}
	fixed = strings.TrimSpace(llm.JsonArryProtect(fixed))
	if err2 := json.Unmarshal([]byte(fixed), &calls); err2 != nil {
		return nil, err2
	}
	return calls, nil
}

func oneshotTranslatorAskLawyer(ctx context.Context, room *scripterRoom, call oneshotTranslatorToolCall) string {
	sessionID := scripterSessionID(ctx, room)
	question := strings.TrimSpace(call.Question)
	if question == "" {
		return `<ask_lawyer_result error="question字段为空，无法查询规则书"/>`
	}
	log.Printf("[scripter:oneshot_translator] session=%s ask_lawyer question=%q", sessionID, truncateRunes(question, 300))
	if room.lawyer.provider == nil {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="lawyer_unavailable">规则书专家不可用；不得声称已核验具体规则书元素。</ask_lawyer_result>`, question)
	}
	results := runLawyer(ctx, room.lawyer, question)
	if len(results) == 0 {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="no_result">规则书专家未返回可用裁定；应换一个更具体的候选继续提问，或在最终结论中标记no_result/uncertain。</ask_lawyer_result>`, question)
	}
	return fmt.Sprintf(`<ask_lawyer_result question=%q status="found">%s</ask_lawyer_result>`,
		question, formatLawyerResults(results))
}

// ---------------------------------------------------------------------------
// Blacklist helpers
// ---------------------------------------------------------------------------

func oneshotFindForbiddenAnchor(response string, anchors []string) string {
	selected := oneshotExtractSelectedAnchor(response)
	if selected == "" || selected == "无" {
		return ""
	}
	normalizedSelected := oneshotNormalizeAnchorKey(selected)
	if normalizedSelected == "" {
		return ""
	}
	for _, anchor := range anchors {
		if n := oneshotNormalizeAnchorKey(anchor); n != "" && strings.Contains(normalizedSelected, n) {
			return anchor
		}
	}
	return ""
}

func oneshotExtractSelectedAnchor(response string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(response)), &obj); err == nil {
		if v, ok := obj["selected_anchor"].(string); ok {
			return strings.TrimSpace(v)
		}
	}
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "selected_anchor") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) != 2 {
				parts = strings.SplitN(trimmed, "：", 2)
			}
			if len(parts) == 2 {
				return strings.Trim(strings.TrimSpace(parts[1]), " `\"'，。；;")
			}
		}
	}
	return ""
}

func oneshotNormalizeAnchorKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(
		" ", "", "\t", "", "\n", "", "\r", "",
		"（", "", "）", "", "(", "", ")", "",
		"「", "", "」", "", "《", "", "》", "", "[", "", "]", "",
		"：", "", ":", "", "，", "", ",", "", "。", "", ".", "", "、", "", "/", "",
		"-", "", "_", "",
	)
	return replacer.Replace(s)
}

func diversityConstraintsBlock(constraints ScripterConstraints) string {
	var sb strings.Builder
	sb.WriteString("<diversity_constraints>\n")
	sb.WriteString(fmt.Sprintf("horror_mode: %s\n", constraints.HorrorMode))
	if label := horrorModeChineseLabels[constraints.HorrorMode]; label != "" {
		sb.WriteString(fmt.Sprintf("horror_mode_zh: %s\n", label))
	}
	sb.WriteString(fmt.Sprintf("invest_focus: %s\n", constraints.InvestFocus))
	if label := investFocusChineseLabels[constraints.InvestFocus]; label != "" {
		sb.WriteString(fmt.Sprintf("invest_focus_zh: %s\n", label))
	}
	sb.WriteString(fmt.Sprintf("tone_tags: %s\n", strings.Join(constraints.ToneTags, ", ")))
	sb.WriteString("硬约束：本次submit.draft.content.horror_mode、invest_focus、tone_tags必须逐字使用上述值，不得自行替换、翻译、改名或省略。\n")
	sb.WriteString("含义：horror_mode指明神话力量介入人类世界的主要机制（非恐怖风格、美学或具体怪物）；invest_focus决定调查入口；tone_tags只约束文风、节奏、场面选择和NPC反应风格，不覆盖剧本事实、规则书裁定或工具结果。\n")
	sb.WriteString("</diversity_constraints>")
	return sb.String()
}

// ---------------------------------------------------------------------------
// Top-level generation functions
// ---------------------------------------------------------------------------

func generateOneshotDraft(ctx context.Context, room *scripterRoom, constraints ScripterConstraints) (ScenarioDraft, IronyCore, string, *mythosSkeleton, error) {
	sessionID := scripterSessionID(ctx, room)
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)

	userMsg := fmt.Sprintf(
		`<request_json>%s</request_json>
<constraints>%s</constraints>
%s
%s
<recently_used_mythos_anchors>
%s
</recently_used_mythos_anchors>
<recent_npc_name_blacklist>%s</recent_npc_name_blacklist>
<scenario_title_blacklist>%s</scenario_title_blacklist>
<recent_scenario_tags_blacklist>
%s
</recent_scenario_tags_blacklist>
以上为近期模组已用过的核心叙事标签：本次submit.draft.tags不得与其重复，须另选能体现本剧本独有桥段的具体标签。
<length_spec>
%s
</length_spec>
<difficulty_spec>
%s
</difficulty_spec>
请设计并生成完整的COC7剧本。`,
		string(reqJSON), string(constraintsJSON),
		diversityConstraintsBlock(constraints),
		proseVoiceBlock(constraints),
		formatMythosBlacklist(room.mythosBlacklist),
		formatNPCNameBlacklist(room.npcBlacklist),
		formatScenarioTitleBlacklist(room.titleSamples),
		formatScenarioTagsBlacklist(room.tagsBlacklist),
		lengthSpec(room.req.TargetLength)+"\n线索会被直接展示给玩家, 但类型前缀(真实/隐藏/误导)会被隐藏；因此[误导]线索在表面上必须与[真实]线索无法区分——它必须是真实可验证的观察，误导力来自支持错误结论，而非靠编造或怪异感蒙混。",
		difficultySpec(room.req.Difficulty),
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(oneshotSystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	logStagePrompt("oneshot", sessionID, msgs)

	result, skeleton, _, err := runOneshotArchitectLoop(ctx, room, msgs, "oneshot_architect", true)
	if err != nil {
		return ScenarioDraft{}, IronyCore{}, "", nil, err
	}

	log.Printf("[scripter:oneshot] session=%s done anchor=%q scenes=%d npcs=%d clues=%d",
		sessionID, truncateRunes(result.Content.MythosAnchor, 80),
		len(result.Content.Scenes), len(result.Content.NPCs), len(result.Content.Clues))
	logScripterArtifact("Oneshot Result", sessionID, result)
	if skeleton != nil {
		logScripterArtifact("Mythos Skeleton", sessionID, skeleton)
	}

	return result.toScenarioDraft(), IronyCore{}, strings.TrimSpace(result.RewardConcept), skeleton, nil
}

func repairOneshotDraft(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, previous *ScenarioDraft, issues []string) (ScenarioDraft, error) {
	sessionID := scripterSessionID(ctx, room)
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)
	prevJSON, _ := json.Marshal(previous)

	userMsg := fmt.Sprintf(
		`<request_json>%s</request_json>
<constraints>%s</constraints>
%s
%s
<previous_draft>%s</previous_draft>
<recent_scenario_tags_blacklist>
%s
</recent_scenario_tags_blacklist>
<must_fix>
%s
</must_fix>
请修复上述问题并重新调用translate_anchor验证神话元素，然后通过submit提交修复后的完整剧本JSON。逐条针对must_fix修复到位，除修复所需外不要改动其他内容；不要更换已确认的神话元素（mythos_anchor）；不得改变diversity_constraints中的horror_mode/invest_focus/tone_tags；若需修复tags，须避开<recent_scenario_tags_blacklist>中的所有标签。修复阶段无需重新submit_skeleton，可直接submit。`,
		string(reqJSON), string(constraintsJSON),
		diversityConstraintsBlock(constraints),
		proseVoiceBlock(constraints),
		string(prevJSON),
		formatScenarioTagsBlacklist(room.tagsBlacklist),
		strings.Join(issues, "\n"),
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(oneshotSystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	logStagePrompt("oneshot_repair", sessionID, msgs)

	result, _, _, err := runOneshotArchitectLoop(ctx, room, msgs, "oneshot_repair_architect", false)
	if err != nil {
		return ScenarioDraft{}, fmt.Errorf("oneshot repair failed: %w", err)
	}

	draft := result.toScenarioDraft()
	log.Printf("[scripter:oneshot_repair] session=%s done name=%q scenes=%d npcs=%d clues=%d",
		sessionID, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	return draft, nil
}

// ---------------------------------------------------------------------------
// QA humanization review — 用闲置的 QA agent 审查 AI 腔，问题清单喂给修复循环
// ---------------------------------------------------------------------------

type qaReviewResult struct {
	Issues []string `json:"issues"`
}

var qaReviewSchemaExample = marshalExample(qaReviewResult{
	Issues: []string{
		"intro使用了①②③编号列表，应改为自然叙述",
		"NPC 陈默 缺少标志性小细节，可补一个习惯动作",
	},
})

// qaReviewSystemPrompt 与 architect 共用 humanWritingRules，保证审查标准一致。
func qaReviewSystemPrompt() string {
	return `<role>剧本人写化审查员</role>
<task>审查AI生成的COC剧本文本是否带有"AI腔"，输出必须整改的问题清单。只审文字质感与人味，不审剧情设计、规则正确性或结构完整性。</task>
<standards>
` + humanWritingRules + `
</standards>
<scope>
- 编号/模板腔禁令只适用于name/description/setting/intro（玩家可见散文）
- scenes.description与npcs.description是KP用的结构化数据，保留"可见/可发现/杠杆/公开身份/议程/秘密"等要素标签是正常的，不要因此报问题；但要素内容若空泛套话（如"异常的气味""神秘的声音"）应报问题
- description/setting/intro必须保持日常、平静、无恐怖氛围、不剧透真相；若违反必须报告（这是硬约束）
- <prose_voice>是本剧本的作者声线，仅当散文明显是说明文/设计文档腔时才报问题，不苛求声线完美贴合
</scope>
<output>只输出JSON对象：{"issues":["问题1","问题2"]}；每条问题指明字段或对象名并给出可执行的修改方向；按严重程度排序，最多8条；没有问题输出{"issues":[]}。</output>`
}

// buildQAReviewPayload 只送审文字质感相关字段，剔除stats等噪音，控制token开销。
func buildQAReviewPayload(draft *ScenarioDraft) map[string]any {
	scenes := make([]map[string]string, 0, len(draft.Content.Scenes))
	for _, s := range draft.Content.Scenes {
		scenes = append(scenes, map[string]string{"name": s.Name, "description": s.Description})
	}
	npcs := make([]map[string]string, 0, len(draft.Content.NPCs))
	for _, n := range draft.Content.NPCs {
		npcs = append(npcs, map[string]string{"name": n.Name, "description": n.Description, "attitude": n.Attitude})
	}
	return map[string]any{
		"name":        draft.Name,
		"description": draft.Description,
		"setting":     draft.Content.Setting,
		"intro":       draft.Content.Intro,
		"scenes":      scenes,
		"npcs":        npcs,
		"clues":       draft.Content.Clues,
	}
}

// runOneshotQAReview 返回人写化整改清单；审查不可用或失败时返回nil（非致命，跳过即可）。
func runOneshotQAReview(ctx context.Context, room *scripterRoom, draft *ScenarioDraft, constraints ScripterConstraints) []string {
	if room == nil || room.qa.provider == nil || draft == nil {
		return nil
	}
	sessionID := scripterSessionID(ctx, room)
	payloadJSON, err := json.Marshal(buildQAReviewPayload(draft))
	if err != nil {
		log.Printf("[scripter:qa_humanize] session=%s marshal payload failed: %v", sessionID, err)
		return nil
	}
	userMsg := fmt.Sprintf(`%s
<draft_for_review>%s</draft_for_review>
请按standards审查以上剧本文本，输出问题清单JSON。`,
		proseVoiceBlock(constraints), string(payloadJSON))
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.qa.systemPrompt(qaReviewSystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	logStagePrompt("qa_humanize", sessionID, msgs)
	var result qaReviewResult
	if err := chatAndParseJSON(ctx, room.qa, msgs, &result, qaReviewSchemaExample, "qa_humanize"); err != nil {
		log.Printf("[scripter:qa_humanize] session=%s review failed: %v (skipping)", sessionID, err)
		return nil
	}
	issues := make([]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		if issue = strings.TrimSpace(issue); issue != "" {
			issues = append(issues, "[人写化] "+issue)
		}
	}
	if len(issues) > 8 {
		issues = issues[:8]
	}
	return issues
}

// ---------------------------------------------------------------------------
// Logic review — 用闲置的 QA agent 审查因果逻辑与神话一致性，问题清单喂给修复循环。
// 与 runOneshotQAReview 平行但职责不同：这里只审逻辑可达性，不审文风。
// ---------------------------------------------------------------------------

// logicReviewSystemPrompt 定义逻辑审查员的检查清单：因果可达性、推理路径、神话锚点必要性。
func logicReviewSystemPrompt() string {
	return `<role>剧本逻辑审查员</role>
<task>审查COC剧本的因果逻辑、推理可达性和神话一致性。不审文风、不审用词。</task>
<checklist>
1. 真相→时间线→异常 可达性：骨架event_chain的每一步是否在scenes/system_prompt中有对应呈现？
2. 异常→线索→结论 可达性：从current_anomaly出发，沿clues是否能到达core_proposition？是否存在至少两条独立路径？
3. NPC知情边界：每个NPC知道什么、不知道什么是否一致？NPC不应知道超出其接触范围的信息
4. 误导排除后仍可推进：去掉所有[误导]线索后，仅靠[真实]+[隐藏]是否仍能推导到真相？
5. 神话锚点必要性：mythos_anchor/cosmic_law是否是event_chain中至少一个环节的必要条件（去掉它事件链断裂）？
6. 洛氏恐怖强度：剧本是否体现了认知冲击、尺度错位、不可逆代价中的至少两项？而非仅靠血腥或惊吓桥段？
7. 胜负条件因果：win_condition/lose_condition是否从event_chain的不同终止状态逻辑推出？
8. Intro目的性：intro是否清楚交代了调查员到场的基本理由/表层任务，让玩家知道自己为何在此，且不列出、不推荐任何具体行动或下一步（行动留给玩家自行探索）？
</checklist>
<output>只输出JSON对象：{"issues":["问题1","问题2"]}；每条问题指明具体字段和可操作的修改方向；按严重程度排序，最多8条；没有问题输出{"issues":[]}。</output>`
}

// buildLogicReviewPayload 送审因果逻辑相关字段：比人写化审查多送system_prompt/mythos/win-lose，
// 少送stats等与逻辑无关的噪音。
func buildLogicReviewPayload(draft *ScenarioDraft) map[string]any {
	scenes := make([]map[string]string, 0, len(draft.Content.Scenes))
	for _, s := range draft.Content.Scenes {
		scenes = append(scenes, map[string]string{"name": s.Name, "description": s.Description})
	}
	npcs := make([]map[string]string, 0, len(draft.Content.NPCs))
	for _, n := range draft.Content.NPCs {
		npcs = append(npcs, map[string]string{"name": n.Name, "description": n.Description, "attitude": n.Attitude})
	}
	return map[string]any{
		"name":           draft.Name,
		"system_prompt":  draft.Content.SystemPrompt,
		"mythos_anchor":  draft.Content.MythosAnchor,
		"mythos_core":    draft.Content.MythosCore,
		"scenes":         scenes,
		"npcs":           npcs,
		"clues":          draft.Content.Clues,
		"win_condition":  draft.Content.WinCondition,
		"lose_condition": draft.Content.LoseCondition,
		"partial_wins":   draft.Content.PartialWins,
	}
}

// runLogicReview 返回因果逻辑整改清单；skeleton为nil或审查不可用/失败时返回nil（非致命，跳过即可）。
func runLogicReview(ctx context.Context, room *scripterRoom, draft *ScenarioDraft, skeleton *mythosSkeleton) []string {
	if room == nil || room.qa.provider == nil || draft == nil || skeleton == nil {
		return nil
	}
	sessionID := scripterSessionID(ctx, room)
	payloadJSON, err := json.Marshal(buildLogicReviewPayload(draft))
	if err != nil {
		log.Printf("[scripter:logic_review] session=%s marshal payload failed: %v", sessionID, err)
		return nil
	}
	skeletonJSON, err := json.Marshal(skeleton)
	if err != nil {
		log.Printf("[scripter:logic_review] session=%s marshal skeleton failed: %v", sessionID, err)
		return nil
	}
	userMsg := fmt.Sprintf(`<confirmed_skeleton>%s</confirmed_skeleton>
<draft_for_review>%s</draft_for_review>
请按checklist审查以上剧本的因果逻辑、推理可达性与神话一致性，输出问题清单JSON。`,
		string(skeletonJSON), string(payloadJSON))
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.qa.systemPrompt(logicReviewSystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	logStagePrompt("logic_review", sessionID, msgs)
	var result qaReviewResult
	if err := chatAndParseJSON(ctx, room.qa, msgs, &result, qaReviewSchemaExample, "logic_review"); err != nil {
		log.Printf("[scripter:logic_review] session=%s review failed: %v (skipping)", sessionID, err)
		return nil
	}
	issues := make([]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		if issue = strings.TrimSpace(issue); issue != "" {
			issues = append(issues, "[逻辑] "+issue)
		}
	}
	if len(issues) > 8 {
		issues = issues[:8]
	}
	return issues
}

// ---------------------------------------------------------------------------
// Normalization
// ---------------------------------------------------------------------------

func normalizeOneshotDraft(draft *ScenarioDraft, req ScenarioCreationRequest, author string, constraints ScripterConstraints, sessionIDs ...string) {
	if draft == nil {
		return
	}
	sessionID := ""
	if len(sessionIDs) > 0 {
		sessionID = sessionIDs[0]
	}
	author = strings.TrimSpace(author)
	if author == "" {
		author = defaultScripterAuthor
	}
	if strings.TrimSpace(draft.Name) == "" {
		draft.Name = "未命名剧本"
		log.Printf("[scripter:normalize] session=%s filled name=%q", sessionID, draft.Name)
	}
	if strings.TrimSpace(req.Name) != "" && draft.Name != strings.TrimSpace(req.Name) {
		draft.Name = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(draft.Description) == "" {
		draft.Description = "一段看似寻常的经历正在等待几位到访者。接受这份邀约，故事便从平常的一天开始。"
		log.Printf("[scripter:normalize] session=%s filled description", sessionID)
	}
	if draft.Author != author {
		draft.Author = author
	}
	if strings.TrimSpace(draft.Tags) == "" {
		draft.Tags = strings.Join(nonEmptyStrings("sandbox", "coc", constraints.Theme), ",")
	}
	if draft.MinPlayers <= 0 {
		draft.MinPlayers = req.MinPlayers
	}
	if draft.MinPlayers <= 0 {
		draft.MinPlayers = 1
	}
	if draft.MaxPlayers <= 0 {
		draft.MaxPlayers = req.MaxPlayers
	}
	if draft.MaxPlayers <= 0 {
		draft.MaxPlayers = 4
	}
	if draft.MaxPlayers < draft.MinPlayers {
		draft.MaxPlayers = draft.MinPlayers
	}
	if strings.TrimSpace(draft.Difficulty) == "" {
		draft.Difficulty = firstNonEmpty(req.Difficulty, "normal")
	}
	if draft.Content.GameStartSlot < 0 {
		draft.Content.GameStartSlot = 0
	}
	if draft.Content.GameStartSlot > 47 {
		draft.Content.GameStartSlot = 47
	}
	if strings.TrimSpace(draft.Content.SystemPrompt) == "" {
		draft.Content.SystemPrompt = fmt.Sprintf(
			"你是本场COC跑团的KP，职责是管理会自行推进的局势而不是执行线性故事。按派系时间线推进后果；按表面可见、主动询问、需要行动、不可直接获得四层管理信息；不要主动把调查员引向正确答案。【KP独有，勿向玩家直说】内部真相：%s。固定神话锚点：%s；具体数值按规则书裁定。",
			"真相将通过调查逐步揭示",
			firstNonEmpty(draft.Content.MythosAnchor, "按规则书已收录神话元素处理"),
		)
		log.Printf("[scripter:normalize] session=%s filled system_prompt", sessionID)
	}
	if strings.TrimSpace(draft.Content.Setting) == "" {
		draft.Content.Setting = fmt.Sprintf(
			"%s的%s。这是平常的一天，你们因各自的缘由来到此地，眼前的一切安静而寻常，尚无任何异样。",
			constraints.Era, strings.Join(constraints.GeographyFlavor, " / "),
		)
		log.Printf("[scripter:normalize] session=%s filled setting", sessionID)
	}
	if strings.TrimSpace(draft.Content.Intro) == "" {
		draft.Content.Intro = "你们按各自的缘由抵达此地，眼前一切安静而寻常。"
		log.Printf("[scripter:normalize] session=%s filled intro", sessionID)
	}
	if strings.TrimSpace(draft.Content.MapDescription) == "" {
		draft.Content.MapDescription = "【文字地图】各调查地点是剧本状态节点，不是顺序关卡：入口连接所有可调查地点；地点之间可往返；时间推进时，各地点状态可能因派系行动而改变。"
		log.Printf("[scripter:normalize] session=%s filled map_description", sessionID)
	}
	if strings.TrimSpace(constraints.HorrorMode) != "" {
		if constraints.DiversitySource == "ai" {
			// NOTE: AI 围池选择时尊重 architect 输出，仅补空值
			if strings.TrimSpace(draft.Content.HorrorMode) == "" {
				draft.Content.HorrorMode = strings.TrimSpace(constraints.HorrorMode)
			}
		} else {
			// fallback 或空: 维持强制覆盖
			if strings.TrimSpace(draft.Content.HorrorMode) != strings.TrimSpace(constraints.HorrorMode) {
				log.Printf("[scripter:normalize] session=%s override horror_mode from=%q to=%q", sessionID, draft.Content.HorrorMode, constraints.HorrorMode)
				draft.Content.HorrorMode = strings.TrimSpace(constraints.HorrorMode)
			}
		}
	}
	if strings.TrimSpace(constraints.InvestFocus) != "" {
		if constraints.DiversitySource == "ai" {
			// NOTE: AI 围池选择时尊重 architect 输出，仅补空值
			if strings.TrimSpace(draft.Content.InvestFocus) == "" {
				draft.Content.InvestFocus = strings.TrimSpace(constraints.InvestFocus)
			}
		} else {
			// fallback 或空: 维持强制覆盖
			if strings.TrimSpace(draft.Content.InvestFocus) != strings.TrimSpace(constraints.InvestFocus) {
				log.Printf("[scripter:normalize] session=%s override invest_focus from=%q to=%q", sessionID, draft.Content.InvestFocus, constraints.InvestFocus)
				draft.Content.InvestFocus = strings.TrimSpace(constraints.InvestFocus)
			}
		}
	}
	if len(constraints.ToneTags) > 0 && !sameStringSlice(draft.Content.ToneTags, constraints.ToneTags) {
		log.Printf("[scripter:normalize] session=%s override tone_tags from=%q to=%q", sessionID, strings.Join(draft.Content.ToneTags, ","), strings.Join(constraints.ToneTags, ","))
		draft.Content.ToneTags = append([]string(nil), constraints.ToneTags...)
	}
	if len(draft.Content.Scenes) == 0 {
		draft.Content.Scenes = []models.SceneData{{
			ID:          "location_1",
			Name:        "调查入口",
			Description: "可见：异常已经公开出现。可发现：主动调查可获得第一批事实。杠杆：公开或隐瞒信息会改变派系反应。风险：拖延会推进时间线。出口：所有相关地点。",
			Triggers:    []string{"available_from_start"},
		}}
		log.Printf("[scripter:normalize] session=%s generated default scene", sessionID)
	}
	for i := range draft.Content.Scenes {
		if strings.TrimSpace(draft.Content.Scenes[i].ID) == "" {
			draft.Content.Scenes[i].ID = fmt.Sprintf("location_%d", i+1)
		}
		if strings.TrimSpace(draft.Content.Scenes[i].Name) == "" {
			draft.Content.Scenes[i].Name = fmt.Sprintf("地点%d", i+1)
		}
		if strings.TrimSpace(draft.Content.Scenes[i].Description) == "" {
			draft.Content.Scenes[i].Description = "可见：当前局势的表面信息。可发现：主动调查可获得的事实。杠杆：调查员行动会改变派系时间线。风险：拖延会让世界推进。出口：可前往相关地点。"
		}
		if len(draft.Content.Scenes[i].Triggers) == 0 {
			draft.Content.Scenes[i].Triggers = []string{"available_from_start"}
		}
	}
	if len(draft.Content.NPCs) == 0 {
		draft.Content.NPCs = []models.NPCData{{
			Name:        "关键NPC",
			Description: "公开身份：地方相关人员。真实议程：自保并观察局势。秘密：掌握部分真相但不会主动全盘托出。",
			Attitude:    "谨慎防备",
		}}
		log.Printf("[scripter:normalize] session=%s generated default npc", sessionID)
	}
	for i := range draft.Content.NPCs {
		if strings.TrimSpace(draft.Content.NPCs[i].Name) == "" {
			draft.Content.NPCs[i].Name = fmt.Sprintf("关键NPC%d", i+1)
		}
		if strings.TrimSpace(draft.Content.NPCs[i].Description) == "" {
			draft.Content.NPCs[i].Description = "公开身份、所属派系、真实议程、秘密和可被调查员影响的杠杆。"
		}
		if strings.TrimSpace(draft.Content.NPCs[i].Attitude) == "" {
			draft.Content.NPCs[i].Attitude = "谨慎观察调查员，只有在压力或交换下才透露深层信息。"
		}
	}
	if len(draft.Content.Clues) == 0 {
		draft.Content.Clues = []string{
			"[真实]公开异常(调查入口): 一个无法普通解释的局势已经开始；获取方式：到达现场并主动询问或检查。",
			"[误导]表象线索(初步调查): 支持错误推断的表象证据；表面合理但只能解释一部分。",
		}
		log.Printf("[scripter:normalize] session=%s generated default clues count=2", sessionID)
	}
	for i, clue := range draft.Content.Clues {
		draft.Content.Clues[i] = normalizeClueString(clue)
	}
	// Extract [隐藏]神话本质 → MythosCore
	var filteredClues []string
	for _, clue := range draft.Content.Clues {
		if strings.Contains(clue, "神话本质") {
			if strings.TrimSpace(draft.Content.MythosCore) == "" {
				text := clue
				if strings.HasPrefix(text, "[") {
					if end := strings.Index(text, "]"); end != -1 {
						text = strings.TrimSpace(text[end+1:])
					}
				}
				draft.Content.MythosCore = text
				log.Printf("[scripter:normalize] session=%s extracted mythos_core=%q", sessionID, truncateRunes(text, 200))
			}
		} else {
			filteredClues = append(filteredClues, clue)
		}
	}
	draft.Content.Clues = filteredClues
	if strings.TrimSpace(draft.Content.MythosCore) == "" && strings.TrimSpace(draft.Content.MythosAnchor) != "" {
		draft.Content.MythosCore = fmt.Sprintf("神话本质(核心发现): %s；到达终止节点并触发揭示后承担理智代价。", draft.Content.MythosAnchor)
		log.Printf("[scripter:normalize] session=%s synthesized mythos_core from anchor", sessionID)
	}
	if strings.TrimSpace(draft.Content.WinCondition) == "" {
		draft.Content.WinCondition = "如果调查员让关键事实公开并改变至少一个派系时间线，则局势以较低代价固化，但神话锚点的余波仍保留。"
		log.Printf("[scripter:normalize] session=%s filled win_condition", sessionID)
	}
	if strings.TrimSpace(draft.Content.LoseCondition) == "" {
		draft.Content.LoseCondition = "如果关键时间线终点到达且调查员没有改变任何派系行动，则局势进入新的稳定态，某人或某地不可挽回地改变。"
		log.Printf("[scripter:normalize] session=%s filled lose_condition", sessionID)
	}
	if len(draft.Content.PartialWins) == 0 {
		draft.Content.PartialWins = []string{"如果调查员保护了个人或证据，但没有改变所有派系时间线，则余波继续存在。"}
		log.Printf("[scripter:normalize] session=%s filled partial_wins", sessionID)
	}
}
