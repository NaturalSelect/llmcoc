// scripter_story.go — Story stage: the architect writes a free-text story
// document (no strongly-typed scene/NPC/clue schema). This is the first of
// the two-stage pipeline; scripter_compile.go's compiler agent later reads
// the document and compiles it into a structured models.ScenarioContent.
//
// The story architect keeps the same tool-call discipline as before
// (translate_anchor for real-time rulebook validation + anchor dedup), but
// submits via submit_story instead of submit, carrying prose instead of a
// strict JSON draft.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

// ---------------------------------------------------------------------------
// System prompt
// ---------------------------------------------------------------------------

func storySystemPrompt() string {
	return `<role>COC7剧本故事创作专家</role>
<task>
根据用户请求，设计并撰写一份完整的COC7剧本故事文档。你只负责故事本身——真相、线索、人物、场景与结局；不需要输出JSON或任何结构化字段，后续会有专门的编译器把你的文档转换成可运行的模组数据。

内部创作流程必须遵循COC模组写作法：先确定恐怖内核，再确定调查焦点，再搭建洋葱式谜团与非线性线索网络，最后落成一份完整的故事文档。COC的核心是谜团、调查、氛围与逐步揭露的恐怖，不是战斗。

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
- 威胁根源必须落在一个具体的施动者身上：神话生物本体或眷属（某种怪物），或人类一方的堕落施法者（邪恶巫师/术士）、有组织的信徒（邪教/邪教徒）；禁止把某个法术、诅咒或仪式本身设计成无主体的谜团根源——法术/仪式至多是该施动者手中的工具或已经造成的后果，调查员最终要查明的是"是谁"或"是什么"在幕后行动，而不是"存在着一种什么法术"
- 施动者必须被全面细化，写进故事文档（而非留作模糊背景）：
  · 若施动者是有组织的信徒（邪教/教团/秘密结社），故事文档必须写明：组织名称与公开伪装（以什么世俗身份或场所掩护）；信仰对象与教义内核（崇拜哪个神话存在或神话概念、向信徒许诺什么——复活逝者、财富、力量、飞升等）；核心仪式的具体做法（在哪举行、多久一次、需要什么牺牲或媒介、参与者要做什么）；组织结构与规模（首领/司祭/骨干/外围信徒各是谁、大致人数、如何分工）；招募与控制方式（从什么人群发展成员、如何维系忠诚、叛逃者会有什么下场）；经济来源与据点（经费从哪来、在哪里集会）；历史渊源（何时、由谁、如何建立起对神话存在的联系）
  · 若施动者是堕落的个人施法者（巫师/术士），故事文档必须写明：其世俗身份与日常掩护；接触神话的契机与经过（如何获得神话知识或力量）；所掌握的具体法术/能力及其来源（须对应规则书或mythos_anchor）；其终极目的（想达成什么、为何不惜代价）；为达成目的已做和将做的具体行动
  · 若施动者是神话生物本体或眷属，故事文档必须写明：它为何出现在此地（被谁召唤/被何物吸引/自古蛰伏/随某物迁来）；目前的栖身之所与活动范围；其存在对周围环境与人造成的具体可观察影响（这些影响构成线索网络的事实基础）；它当前的行为目标或本能驱动
- 时代与地域风味：只作为氛围和行动约束，不直接代替谜团
- 调查焦点必须使用用户消息 <diversity_constraints> 中指定的 invest_focus，不得自行替换；只允许把它落到具体事件

要求：
- 恐怖内核必须至少锚定<cosmic_horror_axioms>中的2条公理，并让它们成为情节真正依赖的设定而非装饰：去掉这些公理，核心情节就站不住脚
- 剧本要给调查员一个自然的到场理由（受邀、路过、日常工作、访友等），异常在开场时尚未显露，需要玩家在调查中逐步发现
- 不要先想战斗或Boss，而是先想调查员深入后会发现的异常；但这些异常写进地点/线索部分，不写进表层情境
- 至少设计两个表面相似或同期发生的事件：一个是通向核心真相的调查入口（主线事件），另一个是看似相关但最终指向无关结论的红鲱鱼（干扰事件）；两者必须有各自的完整线索链，红鲱鱼在排除后不能导致剧情卡死
- brief若为空，也必须先构造一个可调查的表层事件（同样只在调查中揭示，不在开场剧透）

【步骤②：COC神话元素选择与验证】
通过 translate_anchor 工具将核心概念翻译为COC7规则书元素：
- 必须先调用 translate_anchor 获得规则书裁定，再调用 submit_story
- 若首选元素在禁用列表中，继续 translate_anchor 寻找替代
- mythos_anchor 应优先支持调查、异化、理智侵蚀和氛围恐怖，而不是鼓励直接战斗解决问题
- mythos_anchor 优先锚定在步骤①确定的施动者本身（怪物/神话生物实体，或邪恶巫师、邪教这类人类角色）；如果该施动者在规则书中确实以法术/仪式条目呈现，才允许把该条目登记为mythos_anchor，但故事文档必须写清是谁/什么持有并施展它，让施动者而非法术承担谜团根源的叙事功能

【步骤③：线索网络、误导与场景设计】
把剧情设计成线索矩阵，而不是单一路径。
- core clue：推进所必需的关键信息
- support clue：帮助理解背景、提高推理确定性的辅助线索
- red herring（误导线索）：一条「真实可观察的事实」被某个sincere的承载者错误解读为通向无关结论的证据；误导力来自支持一个看似合理但错误的推论，而不是来自编造、怪异感或与真相无关的离奇堆砌
- clue carrier：文件 / NPC / 现场 / 超自然痕迹 / 仪式遗留 / 梦境等；误导线索必须有一个sincere的承载者（真心相信错误解释的NPC或文件），不是KP硬塞给玩家的假证据
- misdirector_npc：有内在动机，不是功能性欺骗工具；他传播错误解释是因为该解释对他自洽（自保、利益、认知局限），而不是为了骗调查员
- reveal_trigger：触发真相揭示的具体事件

场景要求：
- 至少隐含导入、调查、启示、高潮、余波这几个功能中的大部分；不要求显式分标题，但内容要能承载这些阶段
- 每个地点必须写清：可见信息、可发现信息、杠杆、风险、出口、感官细节
- 地点密度允许不均：可以一处地点信息厚重、其余地点简笔带过，不要机械地给每个地点配满同样体量的内容
- 场景应区分相对安全区、危险区、接近神话本质的区域
- 场景需要随着调查推进而解锁，而不是一股脑全开

线索要求：
- 关键推进信息不能只有单一路径；如果A线索错过，也要能通过B或C抵达同一真相
- 误导线索必须是一条「真实可被调查员亲见亲验的观察」+「承载者sincere给出的错误解释」，二者缺一不可；禁止把怪异、不通顺或纯编造的内容当作误导线索
- 每条误导线索需覆盖四要素：
  ① 表面假象：调查员能亲见/亲验的具体异常（如伤口渗液、行为迟钝）
  ② 错误解释：某个sincere承载者据此坚称的世俗化结论（如「塌方缺氧后遗症加真菌感染」）
  ③ 真相后仍成立：揭晓核心真相后，假象本身依然真实、错误解释仍部分说得通（躯壳溃烂确实像感染）
  ④ 排除后推进：调查员一旦推翻该错误解释，非但不会堵死，反而被推向真正的调查方向（转向坟墓与岩穴）
- 至少一条误导线索完整覆盖上述四要素；不能只写「在真相后仍准确」了事
- 至少一条线索承担"神话本质"说明，并与 mythos_anchor 强绑定；神话本质说明只能引用 translate_anchor 已确认的规则书元素本身写明的设定与效果，禁止二次创作：不得编造规则书中不存在的法术名（如"季节之怒"）、物品名（如"衰变砂"）、材质名、怪物名或机制名，也不得在规则书事实之上自行推导新的因果解释链（如"折射共振频率→夺走寿命→肉体沙化"这类无规则书依据的拼凑）

线索内部设计要求：设计每条线索时须在文档中写清以下信息：
1. 来源事实：这条线索基于什么可观察/可验证的物理事实
2. 支持命题：这条线索支持哪个推理命题（真相命题或误导命题）
3. 不能单独证明：仅凭此线索不能得出什么结论（防止单线索通关）
4. 组合关系：需要与哪条/哪几条线索组合才能推进
5. 性质标注：明确写出这条线索是真实观察、神话本质揭示，还是误导表象，以及来源地点或NPC

内部自查③：
✓ 是否存在至少两条不同来源的推进路径，而不是把唯一关键线索锁在单一检定里？
✓ 场景之间是可回访、可交叉验证的调查网络，而不是线性过关房间？
✓ 每条误导线索是否同时满足：①是调查员可亲见亲验的真实观察（非编造、非怪异堆砌）②有sincere承载者给出世俗化错误解释 ③真相揭晓后假象仍真实、错误解释仍部分成立 ④推翻该解释会把调查导向而非堵死主线？

【步骤④：NPC、时间线、SAN与结局推进】
NPC应承担叙事功能，而不是填表；人物要丰满，禁止工具人：
- 需要为NPC命名时必须调用generate_npc_name工具获取随机姓名（指定culture和gender），不要自行编造姓名
- 至少考虑知情者、阻碍者、牺牲品/示警者中的若干角色
- 每个重要NPC必须有明确动机驱动其行为，动机须归入以下至少一类：世俗动机（金钱、权力、爱恨情仇、生存、家族/职业利益等现实诉求）、信仰动机（教义、仪式、对神话的皈依、恐惧或献祭执念）、疯狂动机（心智已被侵蚀，行为逻辑不再遵循常人利害，只服从其扭曲认知）；动机要具体到能解释该NPC在关键场景中会做出的具体选择，不能是"想要真相"这类空洞标签
- 每个重要NPC要有公开身份、议程、秘密或保留信息的理由；议程应是该动机在当前剧情节点下的具体行动化；若NPC属于邪教/秘密组织成员，还需写明其在组织中的层级与角色、入教/入伙经过、忠诚程度或内心动摇
- 每个重要NPC给一个标志性小细节（口头禅、习惯动作、随身物件、外貌特征选其一）
- NPC之间要有现实关系网（亲属、雇佣、债务、旧怨、邻里），不是彼此孤立的功能件
- 可以保留一个与主线无关的纯地方色彩NPC，让世界看起来不是专为调查员布置的舞台
- 写明每个重要NPC对调查员的初始态度

时间线要求：
- 必须存在"过去线"痕迹：事情为何发展到现在
- 必须存在"现在线"推进：无人干预时，局势会继续恶化、转移或完成某种仪式/行动
- 写明无人干预时正在做的具体行动（非"等待调查员"）
- 写明调查员可执行的具体干预动作（非"可以干预"空话）
- 结局：至少设计2个命名结局（如"XX结局"），每个结局需写明触发条件（条件句：如果[条件]，则[处境变化]，[什么不可挽回地改变]）及对应的SAN恢复或损失，其中至少一个是失败/灾难向结局

SAN要求：
- 恐怖暴露应渐进升级：先是诡异与不协调，再到尸体/仪式，再到直视神话本质
- 不要求写精确数值表，但至少要体现由轻到重的理智压力升级

内部自查④：
✓ 每个派系或关键行动者无人干预时都在做具体的事，而不是等待调查员？
✓ 每个调查员干预点都是具体可执行的动作？
✓ 恐怖体验是否呈渐进式升级，而不是一上来直接终极真相？

【步骤⑤：专业化要素（按<length_spec>要求提供，不需要的部分不要为凑数硬写）】
除核心谜团外，成熟的成品模组通常还包含以下要素；是否需要、需要多少由用户消息<length_spec>决定，短剧本可以完全省略：
- 开局手卡：一段可由KP直接朗读或出示给玩家的原文材料（信件、电报、剪报、照片背面文字、录音誊抄等），用第一手材料替代KP转述，增强代入感；须在文档中明确标出这是一份手卡以及发放时机
- 时间线：把"现在线"细化为若干具体节点（时间点+事件），区分"过去线"（事情如何发展到现在）和"当天推进"（无人干预时接下来会发生什么），供KP按真实时间推进局势
- 守秘人附录：给KP的运营建议——如何调低/调高难度、单人团/多人团的调整建议、恐怖氛围如何呈现、主题如何把握；这是写给KP看的元建议，不是剧情内容
- 导入身份表：如果不同职业的调查员应该有不同的入场方式或初始资源（如记者带相机、警探带枪械授权、学者带某本参考书），逐一写明
- 量化追踪机制：如果剧情核心涉及一个需要持续追踪的数值/状态（如"仪式进度"计数器、反派行动时钟、多方势力信任度），写明机制名称、类型（计数器/时钟/追踪器）及各阶段的触发条件与效果；这类机制只作KP参考，不做自动结算的硬规则

【写作质感要求（反AI腔）】
成品要读起来像人类作者写的模组，而不是AI生成的设计文档：
` + humanWritingRules + `
- 用户消息<prose_voice>指定了本剧本的作者声线；表层情境段落按该声线书写
- 地点/NPC部分可以保留"可见/可发现/杠杆/公开身份/议程/秘密"等要素标签（后续编译需要），但要素内容必须具体、不套话

【故事文档必须包含的内容】
✓ 剧本标题：取材于剧本内具体名词（地名/物件/日期/一句当地人的话），像人类作者起的名字；不用"低语/回响/深渊/阴影/凝视/苏醒/沉睡/诅咒"等滥用词
✓ 表层情境（对应最终description/setting/intro）：中性日常语气的冷开场——只交代时代、地点、调查员为何到场；读者/玩家从中看不出剧情、案件、真相、神话或恐怖走向，不带任何惊悚、诡异、压抑或不祥的氛围词；须嵌入与时代、地点及剧情氛围一致的具体年月日（如"1923年10月15日"）；不列出、不推荐、不暗示任何具体行动或下一步，行动入口留给玩家自行探索
✓ KP内部真相：核心恐怖内核、神话锚点（mythos_anchor）及其对故事的必要性（为何不可替换）、历史前因、当前进展
✓ 施动者细化设定：若施动者为邪教/秘密组织，必须独立成段写明其名称与公开伪装、信仰对象与教义、核心仪式、组织结构与规模、招募与控制方式、经济来源与据点、历史渊源；若为堕落施法者个人，写明其世俗身份、接触神话的契机、所掌握法术/能力及其来源、终极目的、已做与将做的行动；若为神话生物，写明其现身此地的原因、栖身与活动范围、对周遭造成的具体可观察影响、当前行为驱动
✓ 恐怖与真相只写在KP内部真相/地点/线索部分，绝不出现在表层情境
✓ 地点清单：每处都写明可见信息、可发现信息、杠杆、风险、出口、感官细节
✓ NPC清单：每位都写明公开身份、动机（世俗/信仰/疯狂三类之一，具体到能解释其行为选择）、议程、秘密、标志性细节、关系网、初始态度；至少一位NPC的描述写明"秘密"或"保留"信息
✓ 线索清单：逐条列出，标注其真实观察/神话本质揭示/误导表象性质、来源地点或NPC；真实观察类线索至少2条且相互独立可组合推导
✓ 时间线：过去线痕迹 + 现在线推进 + 调查员可执行的干预点
✓ 结局：至少2个命名结局，每个都写明名称、触发条件（条件句：如果[条件]，则[处境变化]，[什么不可挽回地改变]）、SAN恢复或损失，其中至少一个是失败/灾难向结局
✓ 通关奖励概念（如有）：与mythos_anchor关联的叙事描述一句话即可，具体机制留给后续设计
✓ 若<length_spec>要求提供开局手卡/时间线节点/守秘人附录/导入身份表/量化机制，是否已按要求提供，且内容具体、未为凑数硬写？
✓ 是否至少存在两个事件（主线 + 红鲱鱼），各自有完整线索链，且红鲱鱼排除后主线仍可推进？
✓ 神话存在的规则/能力是否是情节推进中不可替换的关键因素（换成任意其他神话元素故事是否仍然成立）？
✓ 最终体验重点是"调查员亲手揭开可怕真相"，而不是"被剧情推着走"或"靠战斗通关"？

其他硬性要求：
- 表层情境必须是「冷开场」：以平静、日常、生活化的语气呈现一个看似普通的表层情境，只交代时代、地点、调查员为何到场；读者和玩家从中看不出剧情走向、案件性质、幕后真相或神话存在，也读不到任何恐怖、惊悚、诡异、压抑、不祥的氛围。恐怖是玩家在调查中逐步自行发现的，不能在开场剧透或提前渲染。
- 避免政治话题
- 以克苏鲁宇宙恐惧为基调（渺小感、理智侵蚀、不可知深渊）
- 禁用科学术语/现代技术细节，不要把神话现象解释成硬科幻或工程异常
- 避免把战斗写成主要解法；对抗神话时优先调查、规避、谈判、阻止仪式、改变局势
</task>
<tools>
- translate_anchor：将一个创意概念翻译为COC7规则书中最匹配的具体元素；提交前必须至少调用一次
- generate_npc_name：需要给NPC命名时必须调用本工具从预置姓名池随机取名（指定culture和gender），不要自行编造姓名
- submit_story：提交完整故事文档；只有在translate_anchor确认元素后才调用；必须单独一轮调用。
  story_document字段严禁用draft/content/scenes/clues/endings/npcs/mechanics等嵌套字段代替——story_document只能是一个字符串，不是JSON对象；地点、NPC、线索、结局等所有设计内容都必须写成story_document这一个字符串内的自然语言段落，结构化交由后续编译器负责。
</tools>`
}

// ---------------------------------------------------------------------------
// Story validation — deterministic checks, no LLM call.
// ---------------------------------------------------------------------------

// validateStoryDocument 对 architect 提交的故事文档做确定性结构校验（不调用LLM）。
func validateStoryDocument(story StoryOutput) []string {
	var issues []string
	if length := len([]rune(strings.TrimSpace(story.Document))); length < 500 {
		msg := fmt.Sprintf("story_document 过短（当前%d字），需完整覆盖表层情境/KP内部真相/地点/NPC/线索/时间线/结局各部分", length)
		if length == 0 {
			// NOTE: 0字通常意味着模型把内容写进了draft/content等嵌套字段而不是story_document本身，
			// 这些字段会被静默丢弃；直接点破该失败模式，避免下一轮重复同样的错误。
			msg += "；如果你把故事内容写进了draft/content/clues等嵌套字段，这是错误用法——submit_story只有story_document/mythos_anchor/reward_concept三个顶层字段，story_document必须是完整故事正文本身（纯文本字符串），不能是JSON对象"
		}
		issues = append(issues, msg)
	}
	if strings.TrimSpace(story.MythosAnchor) == "" {
		issues = append(issues, "mythos_anchor 为空，须填入translate_anchor确认的COC7元素全称")
	}
	return issues
}

// ---------------------------------------------------------------------------
// Architect loop
// ---------------------------------------------------------------------------

// runStoryArchitectLoop 驱动故事 architect 工具循环：translate_anchor（可多次）+ submit_story（独占一轮）。
func runStoryArchitectLoop(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage, stageName string) (StoryOutput, error) {
	stageName = firstNonEmpty(stageName, "story_architect")

	tools := []scripterTool{
		translateAnchorTool("将一个创意概念翻译为COC7规则书中最匹配的具体元素；提交前必须至少调用一次"),
		generateNPCNameTool(),
		{
			solo: true,
			def: llm.ToolDefinition{
				Name:        toolNameSubmitStory,
				Description: "提交完整故事文档；只有在translate_anchor确认元素后才调用；必须单独一轮调用",
				Parameters: jsonSchemaObject(`{
					"type": "object",
					"properties": {
						"story_document": {"type": "string", "description": "完整故事文档正文（纯文本自然语言，可用小标题分段，不要输出JSON、代码块或字段名）；地点/NPC/线索/结局等所有设计内容都必须写成这一个字符串内的自然语言段落"},
						"mythos_anchor": {"type": "string", "description": "translate_anchor确认的COC7元素全称"},
						"reward_concept": {"type": "string", "description": "通关奖励叙事概念（若无则留空字符串）"}
					},
					"required": ["story_document", "mythos_anchor", "reward_concept"]
				}`),
			},
		},
	}

	sessionID := scripterSessionID(ctx, room)
	var submitted *StoryOutput
	dispatch := func(ctx context.Context, call llm.ToolCall) toolOutcome {
		switch call.Name {
		case toolNameTranslateAnchor:
			var args translateAnchorArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: translate_anchor参数不是合法JSON，请重新调用。"}
			}
			return toolOutcome{result: executeOneshotTranslateAnchor(ctx, room, args.Concept, args.Reason)}
		case toolNameGenerateNPCName:
			return dispatchGenerateNPCName(ctx, room, call)
		case toolNameSubmitStory:
			var args struct {
				StoryDocument string `json:"story_document"`
				MythosAnchor  string `json:"mythos_anchor"`
				RewardConcept string `json:"reward_concept"`
			}
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: submit_story参数不是合法JSON，请重新调用。"}
			}
			story := StoryOutput{
				Document:      args.StoryDocument,
				MythosAnchor:  args.MythosAnchor,
				RewardConcept: args.RewardConcept,
			}
			if issues := validateStoryDocument(story); len(issues) > 0 {
				return toolOutcome{reject: fmt.Sprintf(
					"SYSTEM REJECT: 故事文档校验失败: %s。请修复后重新submit_story。", strings.Join(issues, "；"))}
			}
			submitted = &story
			log.Printf("[scripter:story_loop] session=%s submitted doc_len=%d anchor=%q", sessionID, len([]rune(story.Document)), truncateRunes(story.MythosAnchor, 80))
			return toolOutcome{result: "已收到，故事文档已提交。", done: true}
		default:
			return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 此阶段只允许translate_anchor/generate_npc_name/submit_story，不允许%s。", call.Name)}
		}
	}

	const maxRounds = 30
	if err := runScripterToolLoop(ctx, room, room.architect, stageName, msgs, tools, maxRounds, dispatch); err != nil {
		return StoryOutput{}, err
	}
	return *submitted, nil
}

// ---------------------------------------------------------------------------
// Top-level story generation / repair
// ---------------------------------------------------------------------------

func generateStoryDocument(ctx context.Context, room *scripterRoom, constraints ScripterConstraints) (StoryOutput, error) {
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
<scenario_title_blacklist>%s</scenario_title_blacklist>
<recent_scenario_tags_blacklist>
%s
</recent_scenario_tags_blacklist>
以上为近期模组已用过的核心叙事标签：本次剧本标题与核心叙事装置应避开这些标签所指向的桥段。
<length_spec>
%s
</length_spec>
<difficulty_spec>
%s
</difficulty_spec>
请设计并撰写完整的COC7剧本故事文档。`,
		string(reqJSON), string(constraintsJSON),
		diversityConstraintsBlock(constraints),
		proseVoiceBlock(constraints),
		formatMythosBlacklist(room.mythosBlacklist),
		formatScenarioTitleBlacklist(room.titleSamples),
		formatScenarioTagsBlacklist(room.tagsBlacklist),
		lengthSpec(room.req.TargetLength)+"\n线索最终会直接展示给玩家，但真实/隐藏/误导的性质标注会被隐藏；因此误导性线索在表面上必须与真实线索无法区分——它必须是真实可验证的观察，误导力来自支持错误结论，而非靠编造或怪异感蒙混。",
		difficultySpec(room.req.Difficulty),
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(storySystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	logStagePrompt("story", sessionID, msgs)

	result, err := runStoryArchitectLoop(ctx, room, msgs, "story_architect")
	if err != nil {
		return StoryOutput{}, err
	}

	log.Printf("[scripter:story] session=%s done anchor=%q doc_len=%d",
		sessionID, truncateRunes(result.MythosAnchor, 80), len([]rune(result.Document)))
	logScripterArtifact("Story Output", sessionID, result)

	return result, nil
}

func repairStoryDocument(ctx context.Context, room *scripterRoom, constraints ScripterConstraints, previous StoryOutput, issues []string) (StoryOutput, error) {
	sessionID := scripterSessionID(ctx, room)
	reqJSON, _ := json.Marshal(room.req)
	constraintsJSON, _ := json.Marshal(constraints)

	userMsg := fmt.Sprintf(
		`<request_json>%s</request_json>
<constraints>%s</constraints>
%s
%s
<previous_story_document>%s</previous_story_document>
<previous_mythos_anchor>%s</previous_mythos_anchor>
<must_fix>
%s
</must_fix>
请修复上述问题并通过submit_story重新提交完整故事文档。逐条针对must_fix修复到位，除修复所需外不要改动其他内容；除非must_fix明确要求，否则不要更换已确认的神话元素（mythos_anchor）；不得改变diversity_constraints中的horror_mode/invest_focus/tone_tags所指向的核心设定。`,
		string(reqJSON), string(constraintsJSON),
		diversityConstraintsBlock(constraints),
		proseVoiceBlock(constraints),
		previous.Document,
		previous.MythosAnchor,
		strings.Join(issues, "\n"),
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(storySystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	logStagePrompt("story_repair", sessionID, msgs)

	result, err := runStoryArchitectLoop(ctx, room, msgs, "story_repair_architect")
	if err != nil {
		return StoryOutput{}, fmt.Errorf("story repair failed: %w", err)
	}
	if strings.TrimSpace(result.MythosAnchor) == "" {
		result.MythosAnchor = previous.MythosAnchor
	}
	if strings.TrimSpace(result.RewardConcept) == "" {
		result.RewardConcept = previous.RewardConcept
	}
	log.Printf("[scripter:story_repair] session=%s done doc_len=%d", sessionID, len([]rune(result.Document)))
	return result, nil
}

// ---------------------------------------------------------------------------
// QA humanization review — reviews the raw story text for "AI voice"
// ---------------------------------------------------------------------------

// storyQAReviewSystemPrompt 与故事 architect 共用 humanWritingRules，保证审查标准一致。
func storyQAReviewSystemPrompt() string {
	return `<role>剧本人写化审查员</role>
<task>审查COC剧本故事文档是否带有"AI腔"，输出必须整改的问题清单。只审文字质感与人味，不审剧情设计、规则正确性或结构完整性。</task>
<standards>
` + humanWritingRules + `
</standards>
<scope>
- 编号/模板腔禁令只适用于面向玩家的表层情境段落（对应最终description/setting/intro）
- KP内部真相、地点、NPC、线索等结构化内容段落可以保留"可见/可发现/杠杆/公开身份/议程/秘密"等要素标签，不要因此报问题；但要素内容若空泛套话（如"异常的气味""神秘的声音"）应报问题
- 表层情境部分必须保持日常、平静、无恐怖氛围、不剧透真相；若违反必须报告（这是硬约束）
- <prose_voice>是本剧本的作者声线，仅当散文明显是说明文/设计文档腔时才报问题，不苛求声线完美贴合
</scope>`
}

// runStoryQAReview 返回人写化整改清单；storyDoc为空或审查不可用/失败时返回nil（非致命，跳过即可）。
func runStoryQAReview(ctx context.Context, room *scripterRoom, storyDoc string, constraints ScripterConstraints) []string {
	if room == nil || room.qa.provider == nil || strings.TrimSpace(storyDoc) == "" {
		return nil
	}
	sessionID := scripterSessionID(ctx, room)
	userMsg := fmt.Sprintf(`%s
<story_document>%s</story_document>
请按standards审查以上故事文档，通过report_issues工具提交问题清单。`,
		proseVoiceBlock(constraints), storyDoc)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.qa.systemPrompt(storyQAReviewSystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	result, err := runReportIssuesTool(ctx, room.qa, "qa_humanize", msgs,
		"提交本次审查发现的问题清单；没有问题时提交空数组")
	if err != nil {
		log.Printf("[scripter:qa_humanize] session=%s review failed: %v (skipping)", sessionID, err)
		return nil
	}
	issues := make([]string, 0, len(result))
	for _, issue := range result {
		if issue = strings.TrimSpace(issue); issue != "" {
			issues = append(issues, "[人写化] "+issue)
		}
	}
	if len(issues) > 8 {
		issues = issues[:8]
	}
	return issues
}
