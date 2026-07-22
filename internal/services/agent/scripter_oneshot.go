// scripter_oneshot.go — OneshotResult type, translator sub-agent, blacklist
// helpers, and the repair/logic-review machinery shared by the story→compile
// pipeline (see scripter_story.go / scripter_compile.go for the two
// generation stages themselves).
//
// runOneshotArchitectLoop remains here because repairOneshotDraft reuses it
// to patch an already-compiled ScenarioDraft (translate_anchor + submit),
// independent of the story stage's own tool loop (runStoryArchitectLoop).
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
		Clues: []models.ClueData{
			{
				Summary:    "被取走的每一本都出自同一位捐赠者Douglas的旧藏",
				Source:     "书架区，核对编目卡与捐赠登记",
				SkillCheck: "图书馆使用",
				OnSuccess:  "锁定「目标是Douglas旧物」，但无法解释谁在取、为何取",
				OnFailure:  "馆员闲聊中主动提起捐赠者名字，调查员仍能获知这一事实",
				Nature:     "真实",
			},
			{
				Summary:    "窗台上的泥土矿物成分与镇北墓地一致，而非街道或花园的土",
				Source:     "档案室，检查窗台并做成分比对",
				SkillCheck: "侦查",
				OnSuccess:  "与「失窃书目的共同点」组合，可推翻活人盗贼说，指向墓地方向",
				OnFailure:  "守墓人闲聊中提到墓地土质特殊，调查员仍可获得同等信息",
				Nature:     "真实",
			},
			{
				Summary:    "取书者是食尸鬼（Ghoul）——死者变形后的存在，保留生前记忆与执念；SAN检定1/1d6",
				Source:     "墓地",
				SkillCheck: "克苏鲁神话",
				OnSuccess:  "真正的理智代价不来自它的外形，而来自承认「死亡并非终点、逝者仍以非人方式延续」这一认知本身；具体属性按规则书裁定",
				Nature:     "隐藏",
			},
			{
				Summary:   "守墓人亲眼见过夜里翻墙的佝偻身影，动作迟缓、指甲缝里全是泥，据此坚称是某个惯偷活人趁夜盗墓",
				Source:    "大厅，守墓人Henrik",
				OnSuccess: "这些体征全部属实，真相揭晓后仍成立；调查员一旦否定「活人盗贼」，反而会去比对泥土来源与墓地痕迹，被推向真正的方向",
				Nature:    "误导",
			},
		},
		Endings: []models.EndingData{
			{
				Name:        "书归其主",
				Trigger:     "如果调查员让Douglas重获藏书，则他退隐墓地",
				Description: "书籍谜团以悲哀收场，图书馆恢复平静",
				SANReward:   "恢复1d4",
			},
			{
				Name:        "永久关闭",
				Trigger:     "如果图书馆永久关闭，则Douglas转向其他途径",
				Description: "某个新目标成为下一个遭遇者，威胁并未真正解除",
				SANReward:   "损失1d6",
				IsFailure:   true,
			},
		},
		Handouts: []models.HandoutData{
			{
				Title:   "捐赠登记簿摘抄",
				Content: "9月1日，Margaret Doyle代已故Douglas Whitfield捐出藏书47册，注明「按其遗愿，供图书馆自由处置」。",
				Timing:  "调查员查阅捐赠登记时",
			},
		},
		Timeline: []models.TimelineEvent{
			{Time: "六周前", Event: "Douglas Whitfield病逝，侄女Margaret按遗愿将藏书捐赠图书馆", Phase: "past"},
			{Time: "开局当晚", Event: "若无人阻止，取书者将在闭馆后潜入书架区，带走下一批目标书籍", Phase: "current"},
		},
		KeeperAppendix: &models.KeeperAppendix{
			DifficultyDown: "让守墓人主动提供泥土线索，缩短调查员定位墓地的时间",
			DifficultyUp:   "取书者提前带走部分证据，迫使调查员更依赖NPC口述重建事实",
			SoloAdvice:     "单人团可让守墓人承担更多主动提示功能",
		},
	},
}

// oneshotExample is the JSON schema example used for parsing/repair prompts.
var oneshotExample = marshalExample(oneshotResultExample)

// StoryOutput is the story architect's final submission: a free-text story
// document plus the confirmed mythos anchor and optional reward concept.
// It carries no strongly-typed scene/NPC/clue structure — the compiler stage
// is responsible for extracting structure from Document.
type StoryOutput struct {
	Document      string `json:"story_document"`
	MythosAnchor  string `json:"mythos_anchor"`
	RewardConcept string `json:"reward_concept"`
}

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
本剧本必须把以下宇宙公理作为世界观设定的结构性前提，而不是形容词式的气氛装饰：
1. 宇宙无目的性：神话存在不针对人类，人类只是其活动的附带物或原料；恐怖来自"我们不在任何计划之中"。
2. 认知天花板：人类理性有边界，神话真相只能被部分感知，越界即理智受损；真正的恐惧来自"理解"本身，而非血腥场面。
3. 接触不可逆：与神话的任何实质接触都留下永久痕迹（肉体/精神/环境），没有"恢复原状"。
4. 规则即依据：神话存在的一切表现必须直接对应规则书对该元素已写明的设定或能力——不是"它很强所以能做X"，也不是自行推导"规则Y导致了现象X"，而是"规则书本身就写着它会做X"。
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
- 恐怖内核必须至少锚定<cosmic_horror_axioms>中的2条公理，并让它们成为情节真正依赖的设定而非装饰：去掉这些公理，核心情节就站不住脚
- 剧本要给调查员一个自然的到场理由（受邀、路过、日常工作、访友等），异常在开场时尚未显露，需要玩家在调查中逐步发现
- 不要先想战斗或Boss，而是先想调查员深入后会发现的异常；但这些异常写进scenes/clues，不写进开场的setting/intro
- 至少设计两个表面相似或同期发生的事件：一个是通向核心真相的调查入口（主线事件），另一个是看似相关但最终指向无关结论的红鲱鱼（干扰事件）；两者必须有各自的完整线索链，红鲱鱼在排除后不能导致剧情卡死
- brief若为空，也必须先构造一个可调查的表层事件（同样只在调查中揭示，不在开场剧透）

【步骤②：COC神话元素选择与验证】
通过 translate_anchor 工具将核心概念翻译为COC7规则书元素：
- 必须先调用 translate_anchor 获得规则书裁定，再调用 submit
- 若首选元素在禁用列表中，继续 translate_anchor 寻找替代
- mythos_anchor 应优先支持调查、异化、理智侵蚀和氛围恐怖，而不是鼓励直接战斗解决问题

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
	- 神话本质说明必须直接来自该规则书元素本身已写明的设定、能力或效果，禁止在规则书事实之上自行推导新的因果解释或编造"因为A所以B所以C"式的解释链（如"折射共振频率→夺走寿命→肉体沙化"这类无规则书依据的自创推导）

线索内部设计要求（architect内部推理用）：
设计每条线索时必须在内部明确以下五项，并将结果落进结构化字段：
1. 来源事实：这条线索基于什么可观察/可验证的物理事实 → summary + source
2. 支持命题：这条线索支持哪个推理命题（真相命题或误导命题）→ 体现在summary的表述方向
3. 不能单独证明：仅凭此线索不能得出什么结论（防止单线索通关）→ 写进on_success/on_failure的推进限度
4. 组合关系：需要与哪条/哪几条线索组合才能推进 → 可在on_success中点出需要配合的另一条线索
5. 性质标注：明确写出这条线索是真实观察、神话本质揭示，还是误导表象 → 对应 nature 字段
输出格式：每条线索是一个结构化对象 {summary, source, skill_check, on_success, on_failure, nature}；nature 必须是"真实"/"隐藏"/"误导"之一（不再用方括号前缀）；on_failure 写明检定失败时如何不卡关地获得同等或替代信息。nature=误导 的线索必须支持一个表面合理但与真相冲突的替代结论。

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
- ending_signals → endings：至少2个命名结局(name/trigger/description/san_reward/is_failure)，trigger使用条件句结构，胜利与失败结局都要给出san_reward

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
✓ 每条clue的nature字段是"真实"/"隐藏"/"误导"之一；至少一条nature="隐藏"的线索涵盖神话本质并关联mythos_anchor？
✓ [隐藏]神话本质说明中引用的所有法术名、物品名、怪物名、材质名均来自规则书（通过 translate_anchor 已确认），无自创元素？
✓ [隐藏]神话本质的说明是否直接来自规则书元素本身，没有额外编造因果解释或伪科学推导？
✓ 每条[误导]线索是否同时满足：①是调查员可亲见亲验的真实观察（非编造、非怪异堆砌）②有sincere承载者给出世俗化错误解释 ③真相揭晓后假象仍真实、错误解释仍部分成立 ④推翻该解释会把调查导向而非堵死主线？
✓ 是否至少存在两个事件（主线 + 红鲱鱼），各自有完整线索链，且红鲱鱼排除后主线仍可推进？
✓ 关键推进信息是否具备多入口，而不是依赖单一检定成功？
✓ system_prompt含三项KP协议（时间推进/信息分层/不主动引导）+ 核心真相注入？
✓ 每个ending的trigger是否使用条件句（而非二元裁定），且都填写了san_reward？
✓ 所有NPC stats含SAN字段？
✓ 神话存在的规则/能力是否是情节推进中不可替换的关键因素（换成任意其他神话元素故事是否仍然成立）？
✓ 最终体验重点是”调查员亲手揭开可怕真相”，而不是”被剧情推着走”或”靠战斗通关”？

其他硬性要求：
- description(简介)、setting(背景)、intro(开场)必须是「冷开场」：以平静、日常、生活化的语气呈现一个看似普通的表层情境，只交代时代、地点、调查员为何到场；读者和玩家从这三处看不出剧情走向、案件性质、幕后真相或神话存在，也读不到任何恐怖、惊悚、诡异、压抑、不祥的氛围。恐怖是玩家在调查中逐步自行发现的，不能在开场剧透或提前渲染。setting须在文本中嵌入具体的开局年月日（如"1923年10月15日"，模型按剧本自行选择合理日期，不得固定套用示例日期）；game_start_slot保留表示时刻的语义（0-47，每槽30分钟），与日期无关，不得混淆。
- 恐怖内核、真相、神话本质只能写进system_prompt(KP独有)、scenes、clues、mythos_anchor；严禁泄露到description/setting/intro。
- 避免政治话题
- 以克苏鲁宇宙恐惧为基调（渺小感、理智侵蚀、不可知深渊）
- 禁用科学术语/现代技术细节，不要把神话现象解释成硬科幻或工程异常
- 避免把战斗写成主要解法；对抗神话时优先调查、规避、谈判、阻止仪式、改变局势
- 神话本质说明严禁自创规则书中不存在的元素：不得编造法术名（如"季节之怒"）、物品名（如"衰变砂"）、材质名、怪物名或原创机制；所有神话元素必须来自 translate_anchor 确认的规则书内容，或由 lawyer 裁定支持
- 神话本质必须直接来源规则书：说明只能复述或直接对应 translate_anchor 已确认的规则书元素本身写明的设定与效果，禁止在此基础上自行推导新的因果解释或编造伪科学解释链（如"折射共振频率→夺走寿命→肉体沙化"这类无规则书依据的拼凑）
</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- translate_anchor：将一个创意概念翻译为COC7规则书中最匹配的具体元素；提交前必须至少调用一次
  {"action":"translate_anchor","concept":"概念描述（如「死者被古老力量束缚继续行动」）","reason":"这个概念在剧本中承担什么角色"}
- submit：提交完整剧本；只有在translate_anchor确认元素后才调用；必须单独一轮输出
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
    "clues": [
      {"summary":"来自地点A的推进线索（自包含事实）","source":"地点/NPC/文件","skill_check":"推荐检定技能，可留空","on_success":"检定成功获得的信息或效果","on_failure":"检定失败时如何不卡关地获得同等或替代信息","nature":"真实"},
      {"summary":"来自NPC或文件的平行推进线索","source":"...","nature":"真实"},
      {"summary":"神话本质说明，只引用translate_anchor已确认的规则书元素","source":"...","nature":"隐藏"},
      {"summary":"表面假象：具体可观察异常；承载者及其sincere的错误解释——真相揭晓后此假象仍真实、错误解释仍部分成立，只是掩盖了核心；排除该解释会把调查导向真正方向","source":"...","nature":"误导"}
    ],
    "endings": [
      {"name":"结局名(取材于剧本具体名词)","trigger":"如果[条件]，则[处境变化]，[什么不可挽回地改变]","description":"结局叙事(可选)","san_reward":"如\"恢复1d6\"","is_failure":false},
      {"name":"...","trigger":"如果[条件]，则[局势进入新稳定态]，[什么不可挽回地改变]","san_reward":"如\"损失1d6\"","is_failure":true}
    ],
    "handouts": [{"title":"手卡标题(可选字段，无合适手卡可整体省略)","content":"可直接朗读给玩家的正文","timing":"发放时机"}],
    "timeline": [{"time":"六周前","event":"过去线痕迹事件(可选字段，无必要可整体省略)","phase":"past"},{"time":"开局当晚","event":"无人干预时的当前推进","phase":"current"}],
    "keeper_appendix": {"difficulty_down":"降低难度建议(可选整体省略)","difficulty_up":"提高难度建议","solo_advice":"单人团建议","group_advice":"多人团建议"},
    "entry_identities": [{"profession":"职业名(可选整体省略)","init_resource":"初始资源","recommend_clues":"推荐开局线索"}],
    "mechanics": [{"name":"机制名(可选整体省略，仅供KP参考不做自动结算)","type":"counter|clock|tracker","description":"机制说明","stages":[{"label":"阶段标签","effect":"该阶段效果","trigger":"推进条件"}]}]
  }
}
</draft_schema>`
}

// oneshotArchitectToolCallExample shows both tool variants so the repair LLM
// sees the full shape of translate_anchor AND submit.
var oneshotArchitectToolCallExample = marshalExample([]oneshotArchitectToolCall{
	{
		Action:  toolOneshotTranslateAnchor,
		Concept: "死者被古老力量束缚继续行动",
		Reason:  "作为本剧本mythos_anchor的核心概念",
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
	toolOneshotSubmit          ToolCallType = "submit"
	toolStorySubmit            ToolCallType = "submit_story"

	// Shared translator tool call types (used by scripter_reward.go as well).
	toolTranslatorAskLawyer ToolCallType = "ask_lawyer"
	toolTranslatorRespond   ToolCallType = "respond"
)

type oneshotArchitectToolCall struct {
	Action  ToolCallType   `json:"action"`
	Concept string         `json:"concept"` // translate_anchor
	Reason  string         `json:"reason"`  // translate_anchor
	Draft   *OneshotResult `json:"draft"`   // submit
}

// storyArchitectToolCall is the story architect's tool-call payload:
// translate_anchor (shared semantics with oneshotArchitectToolCall) plus
// submit_story, which carries the free-text story document instead of a
// strongly-typed draft.
type storyArchitectToolCall struct {
	Action        ToolCallType `json:"action"`
	Concept       string       `json:"concept"`        // translate_anchor
	Reason        string       `json:"reason"`         // translate_anchor
	StoryDocument string       `json:"story_document"` // submit_story
	MythosAnchor  string       `json:"mythos_anchor"`  // submit_story
	RewardConcept string       `json:"reward_concept"` // submit_story
}

// ---------------------------------------------------------------------------
// Architect loop
// ---------------------------------------------------------------------------

// runOneshotArchitectLoop 驱动 architect 工具循环：translate_anchor（可多次）+ submit（独占一轮）。
// 目前仅由 repairOneshotDraft 复用，用于在已编译草案上做 translate_anchor 校验 + 结构修复。
func runOneshotArchitectLoop(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage, stageName string) (OneshotResult, []llm.ChatMessage, error) {
	if room.architect.provider == nil {
		return OneshotResult{}, msgs, fmt.Errorf("architect provider unavailable")
	}
	sessionID := scripterSessionID(ctx, room)
	stageName = firstNonEmpty(stageName, "oneshot_architect")
	const maxRounds = 30
	for round := 1; round <= maxRounds; round++ {
		if ctx.Err() != nil {
			return OneshotResult{}, msgs, ctx.Err()
		}
		logStagePrompt(fmt.Sprintf("%s_round_%d", stageName, round), sessionID, msgs)
		callMessages := append([]llm.ChatMessage(nil), msgs...)
		raw, err := room.architect.provider.Chat(ctx, room.sessionID+":"+string(models.AgentRoleArchitect), msgs)
		if err != nil {
			return OneshotResult{}, msgs, err
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

		// submit must be alone in its round.
		if oneshotSoloActionMixed(calls) {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 1. 你输出的JSON不合法; 2. submit必须单独一轮输出，不能与translate_anchor混在同一个JSON数组中。若还需翻译，本轮只输出translate_anchor；提交剧本时只输出一个submit。"})
			continue
		}

		invalid := false
		var submitDraft *OneshotResult
		var toolResults []string
		for _, call := range calls {
			switch call.Action {
			case toolOneshotTranslateAnchor:
				toolResults = append(toolResults, executeOneshotTranslateAnchor(ctx, room, call))
			case toolOneshotSubmit:
				if call.Draft == nil {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: submit的draft字段不能为空。"})
					invalid = true
				} else {
					submitDraft = call.Draft
				}
			default:
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(
					"SYSTEM REJECT: 此阶段只允许translate_anchor/submit，不允许%s。", call.Action)})
				invalid = true
			}
		}
		if invalid {
			continue
		}
		if len(toolResults) > 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: strings.Join(toolResults, "\n")})
		}
		if submitDraft != nil {
			return *submitDraft, msgs, nil
		}
		if len(toolResults) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须调用translate_anchor获取规则书裁定，或提交submit提交剧本。"})
		}
	}
	return OneshotResult{}, msgs, fmt.Errorf("oneshot architect 未在%d轮内提交结果", maxRounds)
}

// oneshotSoloActionMixed 判断 submit 这类"必须独占一轮"的动作是否与其他动作混排。
func oneshotSoloActionMixed(calls []oneshotArchitectToolCall) bool {
	solo := 0
	for _, c := range calls {
		if c.Action == toolOneshotSubmit {
			solo++
		}
	}
	return solo > 0 && len(calls) != 1
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
// Repair — patches an already-compiled ScenarioDraft against issue lists
// raised by validateDraftCompatibility / runStoryQAReview / runLogicReview.
// ---------------------------------------------------------------------------

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
请修复上述问题并重新调用translate_anchor验证神话元素，然后通过submit提交修复后的完整剧本JSON。逐条针对must_fix修复到位，除修复所需外不要改动其他内容；不要更换已确认的神话元素（mythos_anchor）；不得改变diversity_constraints中的horror_mode/invest_focus/tone_tags；若需修复tags，须避开<recent_scenario_tags_blacklist>中的所有标签。`,
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

	result, _, err := runOneshotArchitectLoop(ctx, room, msgs, "oneshot_repair_architect")
	if err != nil {
		return ScenarioDraft{}, fmt.Errorf("oneshot repair failed: %w", err)
	}

	draft := result.toScenarioDraft()
	log.Printf("[scripter:oneshot_repair] session=%s done name=%q scenes=%d npcs=%d clues=%d",
		sessionID, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	return draft, nil
}

// ---------------------------------------------------------------------------
// Shared review issue-list schema — used by runStoryQAReview (scripter_story.go)
// and runLogicReview below.
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

// ---------------------------------------------------------------------------
// Logic review — 用闲置的 QA agent 审查因果逻辑与神话一致性，问题清单喂给修复循环。
// 与 runStoryQAReview 平行但职责不同：这里只审逻辑可达性与编译忠实度，不审文风。
// ---------------------------------------------------------------------------

// logicReviewSystemPrompt 定义逻辑审查员的检查清单：因果可达性、推理路径、神话锚点必要性。
func logicReviewSystemPrompt() string {
	return `<role>剧本逻辑审查员</role>
<task>以<story_document>为唯一真相源，审查编译后COC剧本结构化数据的事实忠实度、因果逻辑与推理可达性。不审文风、不审用词。</task>
<checklist>
1. 事实忠实度：scenes/npcs/clues/endings中的人名、地名、因果关系、结局条件是否与<story_document>逐一对应，编译时有没有新增、删减或篡改任何事实？
2. 异常→线索→结论 可达性：从故事文本描述的当前异常出发，沿clues是否能到达故事文本的核心真相？是否存在至少两条独立路径？
3. NPC知情边界：每个NPC知道什么、不知道什么是否与故事文本一致？NPC不应知道超出其在故事中接触范围的信息
4. 误导排除后仍可推进：去掉所有nature="误导"的线索后，仅靠nature="真实"/"隐藏"的线索是否仍能推导到故事文本描述的真相？
5. 神话锚点必要性：mythos_anchor是否是故事文本中不可替换的关键因素（换成其他神话元素故事是否仍然成立）？
6. 洛氏恐怖强度：剧本是否体现了认知冲击、尺度错位、不可逆代价中的至少两项？而非仅靠血腥或惊吓桥段？
7. 结局条件因果：每个ending的trigger是否与故事文本描述的对应结局条件一致，且从不同终止状态逻辑推出？
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
		"name":          draft.Name,
		"system_prompt": draft.Content.SystemPrompt,
		"mythos_anchor": draft.Content.MythosAnchor,
		"mythos_core":   draft.Content.MythosCore,
		"scenes":        scenes,
		"npcs":          npcs,
		"clues":         draft.Content.Clues,
		"endings":       draft.Content.Endings,
	}
}

// runLogicReview 以 storyDoc 为真相源，返回因果逻辑与编译忠实度整改清单；storyDoc为空或
// 审查不可用/失败时返回nil（非致命，跳过即可）。
func runLogicReview(ctx context.Context, room *scripterRoom, draft *ScenarioDraft, storyDoc string) []string {
	if room == nil || room.qa.provider == nil || draft == nil || strings.TrimSpace(storyDoc) == "" {
		return nil
	}
	sessionID := scripterSessionID(ctx, room)
	payloadJSON, err := json.Marshal(buildLogicReviewPayload(draft))
	if err != nil {
		log.Printf("[scripter:logic_review] session=%s marshal payload failed: %v", sessionID, err)
		return nil
	}
	userMsg := fmt.Sprintf(`<story_document>%s</story_document>
<draft_for_review>%s</draft_for_review>
请按checklist审查以上剧本的因果逻辑、推理可达性与对故事文本的忠实度，输出问题清单JSON。`,
		storyDoc, string(payloadJSON))
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
		draft.Content.Clues = []models.ClueData{
			{Summary: "公开异常(调查入口): 一个无法普通解释的局势已经开始", Nature: "真实", Source: "到达现场并主动询问或检查"},
			{Summary: "佐证细节(深入调查): 与公开异常相互印证的独立事实，须两条线索合并才能确认事态走向", Nature: "真实", Source: "深入调查或与相关人员交流"},
			{Summary: "表象线索(初步调查): 支持错误推断的表象证据；表面合理但只能解释一部分", Nature: "误导", Source: "初步调查"},
		}
		log.Printf("[scripter:normalize] session=%s generated default clues count=3", sessionID)
	}
	for i := range draft.Content.Clues {
		draft.Content.Clues[i].Summary = strings.TrimSpace(draft.Content.Clues[i].Summary)
		if strings.TrimSpace(draft.Content.Clues[i].Nature) == "" {
			draft.Content.Clues[i].Nature = "真实"
		}
	}
	// 提取标注"神话本质"的[隐藏]线索 → MythosCore；判定条件须与 hasMythosEssenceClue
	// （scripter.go）保持一致——必须同时满足 nature=隐藏 且 summary 含"神话本质"，
	// 否则可能误删恰好提到该字样的[真实]/[误导]线索，拉低真实线索计数。
	var filteredClues []models.ClueData
	for _, clue := range draft.Content.Clues {
		if strings.TrimSpace(clue.Nature) == "隐藏" && strings.Contains(clue.Summary, "神话本质") {
			if strings.TrimSpace(draft.Content.MythosCore) == "" {
				draft.Content.MythosCore = clue.Summary
			} else {
				draft.Content.MythosCore += "；" + clue.Summary
			}
			log.Printf("[scripter:normalize] session=%s extracted mythos_core=%q", sessionID, truncateRunes(clue.Summary, 200))
		} else {
			filteredClues = append(filteredClues, clue)
		}
	}
	draft.Content.Clues = filteredClues
	if strings.TrimSpace(draft.Content.MythosCore) == "" && strings.TrimSpace(draft.Content.MythosAnchor) != "" {
		draft.Content.MythosCore = fmt.Sprintf("神话本质(核心发现): %s；到达终止节点并触发揭示后承担理智代价。", draft.Content.MythosAnchor)
		log.Printf("[scripter:normalize] session=%s synthesized mythos_core from anchor", sessionID)
	}
	if len(draft.Content.Endings) == 0 {
		draft.Content.Endings = []models.EndingData{
			{Name: "余波固化", Trigger: "调查员让关键事实公开并改变至少一个派系时间线", Description: "局势以较低代价固化，但神话锚点的余波仍保留。", SANReward: "恢复1d6"},
			{Name: "新的稳定态", Trigger: "关键时间线终点到达且调查员没有改变任何派系行动", Description: "局势进入新的稳定态，某人或某地不可挽回地改变。", IsFailure: true, SANReward: "损失1d10"},
		}
		log.Printf("[scripter:normalize] session=%s filled default endings count=2", sessionID)
	}
}
