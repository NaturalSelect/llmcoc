// scripter_story.go — Story stage: the architect writes a free-text story
// document (no strongly-typed scene/NPC/clue schema). This is the first of
// the two-stage pipeline; scripter_compile.go's compiler agent later reads
// the document and compiles it into a structured models.ScenarioContent.
//
// The story architect keeps the same tool-call discipline as before
// (translate_anchor for real-time rulebook validation + anchor dedup, plus
// ask_lawyer for on-demand rule fact checks while drafting scenes/clues),
// but the final story document is never wrapped in a tool call: prose has no
// structure worth encapsulating, so the architect just replies with plain
// text once its research tool calls are done. See runStoryArchitectLoop's
// onPlainText hook.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/llmcoc/server/internal/services/llm"
)

// ---------------------------------------------------------------------------
// System prompt
// ---------------------------------------------------------------------------

func storySystemPrompt() string {
	return `<role>COC7模组作者</role>
<task>
根据用户请求，写出一份完整的COC7模组故事文档——像一位职业模组作者交给出版社的成稿那样：守密人拿到手就能照着跑团的读物。你只负责故事本身：真相、地点、人物、线索、时间线与结局。

不要输出JSON、字段名、要素标签或编号清单。后续有专门的编译器阅读你的成稿，它会自己从叙述里认出哪里是地点、哪里是人物、哪里是线索、哪里是结局，不需要你替它分类整理。你为编译器做的任何"归类"只会让成稿变成半篇小说半张表格的东西。

下面分两部分：第一部分是你在心里做的设计，不写进文档；第二部分是文档本身怎么写。

═══ 第一部分：内部设计（只在心里做，成稿里不出现这些术语与编号） ═══

以下概念——宇宙公理、施动者、线索矩阵、核心线索/辅助线索/误导线索、承载者、恐怖渐进——都是你构思时用的工作语言。成稿中一律不得出现这些词，读者只应该看到它们在情节里生效之后的样子。

<cosmic_horror_axioms>
本剧本必须把以下宇宙公理作为世界观设定的结构性前提，而不是形容词式的气氛装饰：
1. 宇宙无目的性：神话存在不针对人类，人类只是其活动的附带物或原料；恐怖来自"我们不在任何计划之中"。
2. 认知天花板：人类理性有边界，神话真相只能被部分感知，越界即理智受损；真正的恐惧来自"理解"本身，而非血腥场面。
3. 接触不可逆：与神话的任何实质接触都留下永久痕迹（肉体/精神/环境），没有"恢复原状"。
4. 规则即依据：神话存在的一切表现必须直接对应规则书对该元素已写明的设定或能力——不是"它很强所以能做X"，也不是自行推导"规则Y导致了现象X"，而是"规则书本身就写着它会做X"。
5. 尺度错位：事件真实规模远超调查员所见，他们只触及冰山一角，完整图景足以碾碎理智。
6. 不对称信息：知情者（NPC/典籍/痕迹）各自只看到真相的一个投影，投影之间可能互相矛盾却各自真实。
7. 幕后施动者默认优先使用敌对的神话生物本体或眷属，而不是人类堕落者或邪教徒；人类堕落者与邪教徒能否充当幕后主体，以用户消息 <difficulty_spec> 的【威胁规模】为准——该档位未允许时，他们只能是神话力量的代理人，不得被设计成神话力量的根源。
8. 合理设计适当的战斗场景，让调查员有机会直接对抗神话生物本体或眷属，强调其威胁性和紧张感以及面对它们时的绝望感。
</cosmic_horror_axioms>
恐怖内核必须至少锚定其中2条公理，并让它们成为情节真正依赖的设定而非装饰：去掉这些公理，核心情节就站不住脚。但"公理""宇宙无目的性""认知天花板"这些词本身不许出现在成稿里。

【一、恐怖内核与幕后是谁】
- 神话力量以何种方式介入人类世界（邪教仪式、禁忌知识、异族渗透、血脉腐化、猎食、封印苏醒、异界侵入、巫术夺舍等）由你根据 brief 与 <difficulty_spec> 的【威胁规模】自行决定；无论选哪种，成稿中都要有一条一致、可追溯的因果链，不能只当成气氛设定
- 调查焦点必须使用 <diversity_constraints> 中指定的 invest_focus，不得自行替换；只允许把它落到具体事件
- 选择神话关联度：旧日支配者本体 / 眷属 / 神话物品 / 神话知识污染
- 威胁必须有一个具体的主体：某种怪物（神话生物本体或眷属），或人类一方的堕落施法者（邪恶巫师/术士），或有组织的信徒（邪教/邪教徒）。禁止把某个法术、诅咒或仪式本身设计成无主体的谜团根源——法术与仪式至多是这个主体手中的工具或已经造成的后果。调查员最终要查明的是"是谁"或"是什么"在幕后行动，不是"存在着一种什么法术"。这个主体的规模与神话位阶必须落在用户消息 <difficulty_spec> 的【威胁规模】档位内
- 时代与地域风味只作为氛围和行动约束，不直接代替谜团
- 剧本要给调查员一个自然的到场理由（受邀、路过、日常工作、访友等），异常在开场时尚未显露，需要玩家在调查中逐步发现
- 不要先想战斗或Boss，而是先想调查员深入后会撞见的异常
- 至少设计两桩表面相似或同期发生的事件：一桩通向核心真相，另一桩看起来相关但最终指向无关结论；两桩各有完整的一串线索，把后者排除掉之后主线仍然走得通
- brief若为空，也必须先构造一个可调查的表层事件

【二、神话元素验证与取舍】
通过 translate_anchor 工具将核心概念翻译为COC7规则书元素：
- 必须先调用 translate_anchor 获得规则书裁定，后才能动笔写正文
- translate_anchor只负责如实报告：它查到了什么（selected_anchor+content）、这个候选是否命中最近使用元素禁用列表（disabled）；它不会替你判断这个候选适不适合这个故事，也不会替你重试或换概念——要不要重试、换哪个概念、能不能将就用，都是你自己的决定
- 出现以下任一情况就是不合适，必须换一个再查，不要将就着写：disabled=true（规则书里没有可靠匹配，或候选命中了禁用列表，具体原因看content）；content里的解读要靠牵强解释才能对上你想要的效果，或content提到的限制条件把它能用的部分削得几乎剩不下什么；它撑不起恐怖内核要锚定的那2条以上宇宙公理；换成任何其他同类元素都一样能讲这个故事，说明它只是能用，不是该用
- 不要把"换一个"局限在同一个概念下的另一个候选——如果同一概念反复查都撑不起想要的效果，就换掉概念本身（例如从某个具体怪物改向人类堕落施法者，或换到另一个神话分支重新查），只要仍服务于brief与invest_focus
- mythos_anchor 应优先支持调查、异化、理智侵蚀和氛围恐怖，而不是鼓励直接战斗解决问题
- mythos_anchor 优先锚定在幕后那个主体本身；如果它在规则书中确实以法术/仪式条目呈现，才允许把该条目登记为 mythos_anchor，但成稿必须写清是谁在持有并施展它
- 动笔前先问自己："换掉这个元素，我最初想讲的这个故事还成立吗？"如果换了也无所谓，说明它只是贴上去的标签，回 translate_anchor 换一个真正驱动情节的，不要等写完整篇才发现

【三、线索网络与误导】
把剧情设计成一张可以交叉验证的网，不是一条单行道。
- 推进所必需的关键信息不能只有一条路：A错过了，B或C也能到同一个结论
- 每条线索都要落在调查员能亲自看到、听到、挖到、查到的具体东西上；单凭任何一条都得不出全部结论
- 会把人带偏的那条线索，本身必须是真的、调查员可以亲自验证；它之所以误导，是因为有个真心相信自己没错的人（或一份真诚写下的记录）给了它一个听上去合理的世俗解释。真相揭晓之后，这个观察依然成立，那个解释也依然有几分道理；而调查员一旦发现解释不对，不会走进死胡同，反而会被推向该去的方向。禁止把怪异、不通顺或纯编造的内容当作误导
- 给出错误解释的那个人要有自己的理由——自保、利益、认知局限——他不是为了骗调查员才那么说的
- 至少有一处让调查员真正理解他们面对的是什么。这一处只能照 translate_anchor 已确认的规则书元素本身写明的设定与效果来写：不得编造规则书中不存在的法术名、物品名、材质名、怪物名或机制名，也不得在规则书事实之上自行推导新的因果解释链
- 场景要随调查推进而逐步打开，不是一股脑全摆在那儿；要区分相对安全的地方、危险的地方、以及最接近神话本质的地方

内部自查：
✓ 是否存在至少两条不同来源的推进路径，而不是把唯一关键线索锁在单一检定里？
✓ 各处地点之间是可回访、可交叉验证的调查网络，而不是线性过关房间？
✓ 那条会把人带偏的线索，是否同时满足：调查员可亲见亲验、有人真心给出世俗解释、真相后假象与解释都仍部分成立、推翻它会把调查导向而非堵死主线？

【四、人物、时间线、理智与结局】
- 需要给人物起名时必须调用 generate_npc_name 工具（指定culture和gender），不要自行编造姓名
- 至少考虑知情者、阻碍者、牺牲品/示警者中的若干角色
- 每个重要人物的行为都要有说得通的驱动力，落在三类之一：现实诉求（钱、地位、恩怨、活下去、家族或职业利益）、信仰（教义、仪式、皈依、恐惧或献祭执念）、心智已坏（行为逻辑不再遵循常人利害，只服从自己的扭曲认知）。这个驱动力要具体到能解释他在关键时刻做出的具体选择，不能是"想要真相"这种空话
- 人物之间要有现实关系：亲戚、雇佣、欠债、旧怨、邻里，不是彼此孤立的功能件
- 可以留一位与主线完全无关、纯粹是当地风味的人，让这个世界看起来不是专为调查员布置的舞台
- 时间线要同时有"过去"和"现在"：事情是怎么一步步走到今天的；以及在没人干预的情况下，接下来局势会继续恶化、转移或完成某件事。写清楚各方现在正在做的具体动作（不是"等待调查员"），也写清楚调查员能做的具体干预（不是"可以设法阻止"这种空话）
- 恐怖是一层层加上去的：先是说不上来的不对劲，再是尸体、痕迹、无法解释的现象，最后才是直视它本身。不必写精确的理智数值表，但压力要由轻到重
- 至少两种收场，每种都有名字

内部自查：
✓ 每一方在无人干预时都在做具体的事，而不是等着调查员上门？
✓ 每个干预点都是具体可执行的动作？
✓ 恐怖是渐进升级的，而不是一上来就端出终极真相？

【五、可选的专业化内容（按 <length_spec> 决定要不要写，不要为凑数硬写）】
- 事件时间线细化到具体日期节点，供守密人按真实时间推进局势
- 给守密人的运营建议：怎么调低或调高难度、单人团或多人团怎么改、恐怖氛围怎么呈现、主题怎么把握。可以随手写在它该出现的地方（"守密人应当……"），也可以在文末集中一段；不要做成表格或字段
- 不同职业的调查员如果该有不同的入场方式或初始资源（记者带相机、警探带枪械授权、学者带某本参考书），就分别写清楚
- 如果剧情核心涉及一个需要持续追踪的进度（仪式进行到哪一步、对方的行动时钟、几方势力对调查员的信任），写清楚它怎么走、每个阶段什么条件触发、触发之后是什么局面；这类东西只作守密人参考，不是自动结算的硬规则

═══ 第二部分：怎么把它写成一份模组成稿 ═══

【组织方式】
- 按调查员实际会走的顺序排篇：从他们接到委托的地方写起，然后是路上、落脚点、可以走访的几个去处、越走越深的地方，直到摊牌。小标题用它指代的那个地方或那件事的名字（"教堂""墓地""工头房间""向山上去"），不要用"地点""人物""线索"这类分类名当标题
- 人物写在他所在的地方：村长写在客栈那一段，牧师写在教堂那一段。不要把人物抽出来单独汇总成一份名单
- 线索写在能拿到它的那一处：在哪、怎么才能拿到、拿到之后调查员知道了什么。不要把线索抽出来单独汇总成一份清单
- 只有天生跨地点的信息才独立成段：事件时间线、几种收场、以及重要人物与怪物的数值
- 章节之间要交代路怎么走：从这里到下一处有几种方式、各要多久、路上会遇到什么、什么条件下调查员才会想到或才能去那儿。有分岔就在正文里说清楚（"如果调查员先去了X，他们会……；如果他们直接上山，则……"）。这几句路线交代就是这份模组的流程说明，不需要另写一份大纲
- 检定和它的后果写进叙事句里："成功的心理学检定可以发现彼得显然不太喜欢死者""通过困难难度的侦查检定可以发现南侧围墙上有一个三米宽的缺口""失败的调查员会从楼梯上摔下去并受到1D6点伤害"。检定名、难度、成功与失败的结果都在同一句话里自然出现
- 给守密人的提示直接插在它该出现的地方，用对守密人说话的口气："守密人应当用诗意的语言描述这里，温和而不刻意地强调水坝的存在。注意：水坝是解决本模组的关键，务必确保调查员会记得这里"
- 篇幅可以不均：一处地方写得很厚，另几处只有一两笔，这才是成稿该有的样子
- 具体数字贯穿全文：距离多少公里、路上要几小时、村里住多少人、矿井多深、沟壑多宽多深、事情发生在哪年哪月哪日。凡是守密人会被玩家追问的量，都提前写好

【禁止的写法】
- 不写要素标签：任何形如"可见信息：""可发现信息：""杠杆：""风险：""出口：""感官细节：""公开身份：""议程：""秘密：""动机：""关系网：""初始态度：""性质：真实／隐藏／误导""触发条件：""表面假象：""错误解释："的行首标签一律不许出现。这些内容该说的时候用整句话说出来
- 不写设计术语：核心线索、辅助线索、误导线索、红鲱鱼、承载者、线索矩阵、施动者、宇宙公理、洋葱式结构、恐怖渐进、四要素——这些是构思时的工作语言，不是读者该看到的字
- 不写编号清单：正文不出现①②③、1.2.3.、"首先/其次/最后"式的结构。交代零散事实时（当地人分别知道些什么、卡车上带了哪些装备）可以用短列表，但每一条都要是完整的话，不是字段
- 不写"本模组设计了……""这条线索的作用是……"这类元叙述，除非那是明确写给守密人的运营建议

【写完后逐条自问（判据是"读者能不能从文中读出来"，不是"有没有对应的小节"）】
✓ 标题取自文中的具体名词（地名、物件、日期、一句当地人的话），像人起的名字；不用"低语/回响/深渊/阴影/凝视/苏醒/沉睡/诅咒"等滥用词
✓ 开头有一段调查员的导入：谁委托了他们、去哪儿、去做什么，用平静的日常语气写，并写明具体年月日（如"1922年2月17日"）。读这一段的人看不出这里会出事，读不到任何惊悚、诡异、压抑或不祥的字眼，也读不到"接下来该做什么"的建议——行动入口留给玩家自己撞上
✓ 守密人知道而玩家不知道的那部分真相交代清楚了：到底发生了什么、幕后是谁或是什么、它为什么在这儿、事情已经进行到哪一步、还会往哪儿去、以及这个神话存在为什么不可替换。这部分放在明确写给守密人看的段落里，不要混进开头
✓ 幕后那一方写透了：如果是一个组织，它叫什么、对外是什么门面、信什么、许诺给信徒什么、仪式在哪儿怎么做、谁是头目谁是骨干、大概多少人、钱从哪来、怎么发展和控制成员、这一切是什么时候由谁开始的；如果是一个人，他平时是谁、怎么碰上这些东西的、手里有什么本事以及本事从哪来、他到底想要什么、为此已经做了什么、还打算做什么；如果是一个神话存在，它为什么在这儿、藏在哪、活动到什么范围、它的存在给周围留下了哪些看得见摸得着的影响、它现在想干什么。这些通过叙述交代——一份账本、一段回忆、一处现场痕迹、一个人的证词都可以——不要写成条目
✓ 调查员会走到的每一处：进门第一眼看见什么、闻到听到什么；愿意深查的人能挖出什么、需要什么检定；这里可能出什么事；从这儿还能往哪儿去
✓ 每个重要人物：他靠什么谋生、他想要什么、他正在做什么、他瞒着什么或不愿意说什么、他见到调查员是什么态度；给他一个记得住的小地方（一句口头禅、一个习惯动作、一件随身物、一处长相）。至少要有一个人明确瞒着什么
✓ 关键信息不止一条路可以拿到；至少有两条互相独立、需要拼起来才能推出真相的发现
✓ 至少有一处让调查员真正理解他们面对的是什么，且完全照规则书已写明的设定与效果来写
✓ 至少有一条会把人带偏的线索，满足前面第三节写的全部条件
✓ 除主线之外还有第二桩看起来相关的事，有自己完整的一串线索，指向一个与真相无关的结论；排除掉它之后主线仍然走得通
✓ 路怎么走在正文里说清楚了：从开头怎么进入第一处，从每一处还能去哪儿，什么条件下才能进入下一处，做了什么会导向哪种收场
✓ 时间线写成中性的事实记录（谁在什么时候做了什么、什么状态变了），带具体日期；不写成对话，不用引号引用人物原话
✓ 至少两种收场，每种都有名字，都写清楚怎样会走到那里、走到之后什么被永久改变了、调查员的理智是恢复还是继续损失；其中至少一种是失败或灾难
✓ 若 <length_spec> 要求提供时间线节点、给守密人的建议、职业入场差异或可追踪的进度机制，都已按要求写到，且内容具体、没有为凑数硬写
✓ 换掉这个神话存在，故事还成立吗？如果成立，说明它只是装饰，要重写
✓ 最后的体验是"调查员自己把可怕的真相挖了出来"，不是"被剧情推着走"，也不是"打赢了一场仗"

【写作质感（反AI腔）】
成稿要读起来像人类作者写的模组，而不是AI生成的设计文档：
` + humanWritingRules + `

【其他硬性要求】
- 开头必须是冷开场：以平静、日常、生活化的语气呈现一个看似普通的表层情境，只交代时代、地点、调查员为何到场；读者和玩家从中看不出剧情走向、案件性质、幕后真相或神话存在，也读不到任何恐怖、惊悚、诡异、压抑、不祥的氛围。恐怖是玩家在调查中逐步自行发现的，不能在开场剧透或提前渲染
- 避免政治话题
- 以克苏鲁宇宙恐惧为基调（渺小感、理智侵蚀、不可知深渊）
- 禁用科学术语/现代技术细节，不要把神话现象解释成硬科幻或工程异常
- 避免把战斗写成主要解法；对抗神话时优先调查、规避、谈判、阻止仪式、改变局势
</task>
<tools>
- translate_anchor：将一个创意概念翻译为COC7规则书中最匹配的具体元素；正式写作前必须至少调用一次
- ask_lawyer：向COC7规则书专家提出具体规则书问题，核验故事中出现的技能检定、法术效果、怪物能力、机制细节等是否符合规则书事实；写作过程中可随时多次调用
- generate_npc_name：需要给人物起名时必须调用本工具从预置姓名池随机取名（指定culture和gender），不要自行编造姓名
- get_writing_example：获取一份职业模组成稿作为写作参考，学习组织篇章、控制信息密度、把检定与线索写进叙事句的手法；必须在正式动笔前调用一次。参考成稿的具体人名、地名、机构名、情节与神话设定与你要写的剧本无关，禁止照搬
</tools>
<submit>
完成上面的工具调用后，不要调用任何工具来"提交"故事——直接在下一条回复中输出完整故事文档正文本身，就是一段普通的自然语言回复，不要用JSON、代码块或字段名包裹，不要出现story_document/draft/content/scenes/clues/endings/npcs/mechanics等字段名。这一整段回复就是最终交付的成稿。
</submit>`
}

// ---------------------------------------------------------------------------
// Story validation — deterministic checks, no LLM call.
// ---------------------------------------------------------------------------

// validateStoryDocument 对 architect 提交的故事文档做确定性结构校验（不调用LLM）。
func validateStoryDocument(story StoryOutput) []string {
	var issues []string
	if length := len([]rune(strings.TrimSpace(story.Document))); length < 500 {
		msg := fmt.Sprintf("故事文档过短（当前%d字），需要一份完整成稿：导入、守密人知道的真相、调查员会走到的各处地方与其中的人和线索、时间线、结局", length)
		if length == 0 {
			msg += "；不需要调用任何工具来提交，直接在回复正文里输出完整故事文档本身（纯文本，不是JSON）"
		}
		issues = append(issues, msg)
	}
	if strings.TrimSpace(story.MythosAnchor) == "" {
		issues = append(issues, "尚未确认mythos_anchor：须先调用translate_anchor并得到status为found的结论，再输出故事文档")
	}
	return issues
}

// ---------------------------------------------------------------------------
// Architect loop
// ---------------------------------------------------------------------------

// runStoryArchitectLoop 驱动故事 architect 工具循环：translate_anchor（可多次）+
// generate_npc_name/get_writing_example，完成后模型直接以不带 tool_calls 的一条
// 普通回复输出完整故事文档正文——不封装成 submit_story 工具调用参数，因为故事文档
// 本身就是自由文本，没有需要封装的结构。mythos_anchor 从 translate_anchor 最近一次
// status="found"的结论中自动记录，不要求模型在结尾重复提交。
// initialAnchor 非空时作为锚点初始值（repair场景传入已确认的旧锚点，允许修复措辞
// 问题时不必重新调用translate_anchor）。
func runStoryArchitectLoop(ctx context.Context, room *scripterRoom, msgs []llm.ChatMessage, stageName string, initialAnchor string) (StoryOutput, error) {
	stageName = firstNonEmpty(stageName, "story_architect")

	tools := []scripterTool{
		translateAnchorTool("将一个创意概念翻译为COC7规则书中最匹配的具体元素；正式写作前必须至少调用一次"),
		askLawyerTool("向COC7规则书专家提出一个具体规则书问题，用于核验故事中出现的技能检定、法术效果、怪物能力、机制细节等是否符合规则书事实；可多次调用"),
		generateNPCNameTool(),
		getWritingExampleTool(),
	}

	sessionID := scripterSessionID(ctx, room)
	confirmedAnchor := strings.TrimSpace(initialAnchor)
	dispatch := func(ctx context.Context, call llm.ToolCall) toolOutcome {
		switch call.Name {
		case toolNameTranslateAnchor:
			var args translateAnchorArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: translate_anchor参数不是合法JSON，请重新调用。"}
			}
			text, conclusion := executeOneshotTranslateAnchor(ctx, room, args.Concept, args.Reason)
			if conclusion != nil && !conclusion.Disabled && strings.TrimSpace(conclusion.SelectedAnchor) != "" {
				confirmedAnchor = strings.TrimSpace(conclusion.SelectedAnchor)
				log.Printf("[scripter:story_loop] session=%s confirmed anchor=%q", sessionID, confirmedAnchor)
			}
			return toolOutcome{result: text}
		case toolNameAskLawyer:
			var args askLawyerArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: ask_lawyer参数不是合法JSON，请重新调用。"}
			}
			return toolOutcome{result: storyAskLawyer(ctx, room, args.Question)}
		case toolNameGenerateNPCName:
			return dispatchGenerateNPCName(ctx, room, call)
		case toolNameGetWritingExample:
			return toolOutcome{result: executeGetWritingExample(ctx, room)}
		default:
			return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 此阶段只允许translate_anchor/ask_lawyer/generate_npc_name/get_writing_example，不允许%s。", call.Name)}
		}
	}

	var submitted *StoryOutput
	onPlainText := func(content string) (bool, string) {
		story := StoryOutput{Document: strings.TrimSpace(content), MythosAnchor: confirmedAnchor}
		if issues := validateStoryDocument(story); len(issues) > 0 {
			return false, fmt.Sprintf(
				"SYSTEM REJECT: 故事文档校验失败: %s。不需要调用任何工具，直接在下一条回复中重新输出完整故事文档正文。",
				strings.Join(issues, "；"))
		}
		submitted = &story
		log.Printf("[scripter:story_loop] session=%s submitted doc_len=%d anchor=%q", sessionID, len([]rune(story.Document)), truncateRunes(story.MythosAnchor, 80))
		return true, ""
	}

	const maxRounds = 30
	err := runToolLoop(ctx, toolLoopOptions{
		room:        room,
		handle:      room.architect,
		stage:       stageName,
		msgs:        msgs,
		tools:       tools,
		maxRounds:   maxRounds,
		dispatch:    dispatch,
		onPlainText: onPlainText,
	})
	if err != nil {
		return StoryOutput{}, err
	}
	return *submitted, nil
}

// storyAskLawyer 是 story architect 阶段 ask_lawyer 工具的执行逻辑，供写作过程中
// 随时核验技能检定、法术效果、怪物能力等规则书事实；与reward_agent/oneshot_translator
// 的同名实现一致，各agent各自持有一份小包装，不额外抽共享层。
func storyAskLawyer(ctx context.Context, room *scripterRoom, question string) string {
	sessionID := scripterSessionID(ctx, room)
	question = strings.TrimSpace(question)
	if question == "" {
		return `<ask_lawyer_result error="question字段为空"/>`
	}
	log.Printf("[scripter:story_loop] session=%s ask_lawyer question=%q", sessionID, truncateRunes(question, 300))
	if room.lawyer.provider == nil {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="lawyer_unavailable">规则书专家不可用；不得声称已核验具体规则书事实。</ask_lawyer_result>`, question)
	}
	results := runLawyer(ctx, room.lawyer, question)
	if len(results) == 0 {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="no_result">规则书中未找到相关裁定；可换用更具体的提问方式重新提问，或在正文中避免依赖该未核验的规则细节。</ask_lawyer_result>`, question)
	}
	return fmt.Sprintf(`<ask_lawyer_result question=%q status="found">%s</ask_lawyer_result>`, question, formatLawyerResults(results))
}

// ---------------------------------------------------------------------------
// get_writing_example execution — serves a reference manuscript for style only
// ---------------------------------------------------------------------------

// storyWritingExamplePath 是参考成稿在仓库根目录下的文件名；进程按相对路径读取，
// 与 main.go 加载 COC_kp.md 等规则资料的方式一致（服务从仓库根目录启动）。
const storyWritingExamplePath = "example_story.md"

var (
	storyWritingExampleOnce    sync.Once
	storyWritingExampleContent string
	storyWritingExampleErr     error
)

// loadStoryWritingExample 懒加载参考成稿全文，只读一次后常驻内存供后续调用复用。
func loadStoryWritingExample() (string, error) {
	storyWritingExampleOnce.Do(func() {
		data, err := os.ReadFile(storyWritingExamplePath)
		if err != nil {
			storyWritingExampleErr = err
			return
		}
		storyWritingExampleContent = string(data)
	})
	return storyWritingExampleContent, storyWritingExampleErr
}

// executeGetWritingExample 是 get_writing_example 工具的执行逻辑。文件缺失时不阻塞
// 生成流程，只返回说明，architect 应继续按<task>创作要求写作。
func executeGetWritingExample(ctx context.Context, room *scripterRoom) string {
	sessionID := scripterSessionID(ctx, room)
	content, err := loadStoryWritingExample()
	if err != nil {
		log.Printf("[scripter:get_writing_example] session=%s load error=%v", sessionID, err)
		return fmt.Sprintf("参考成稿读取失败（%v），本次不提供参考，请直接按<task>中的创作要求继续写作。", err)
	}
	log.Printf("[scripter:get_writing_example] session=%s served len=%d", sessionID, len([]rune(content)))
	return "以下是一份职业模组成稿，仅供学习组织篇章、控制信息密度、把检定与线索写进叙事句的手法；" +
		"其中具体的人名、地名、机构名、情节与神话设定与你要写的剧本无关，禁止照搬：\n\n" + content
}

// ---------------------------------------------------------------------------
// Top-level story generation / repair
// ---------------------------------------------------------------------------

func generateStoryDocument(ctx context.Context, room *scripterRoom, constraints ScripterConstraints) (StoryOutput, error) {
	sessionID := scripterSessionID(ctx, room)

	userMsg := fmt.Sprintf(
		`
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
		scenarioRequestBlock(room.req, constraints),
		diversityConstraintsBlock(constraints),
		formatMythosBlacklist(room.mythosBlacklist),
		formatScenarioTitleBlacklist(room.titleSamples),
		formatScenarioTagsBlacklist(room.tagsBlacklist),
		lengthSpec(room.req.TargetLength)+"\n会把人带偏的那条线索，在玩家眼里必须和真线索长得一模一样：它本身是调查员可以亲自验证的真实观察，之所以误导，是因为有人给了它一个听上去合理却错误的解释——不能靠编造、怪异或语焉不详来蒙混。",
		difficultySpec(room.req.Difficulty),
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(storySystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	logStagePrompt("story", sessionID, msgs)

	result, err := runStoryArchitectLoop(ctx, room, msgs, "story_architect", "")
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

	userMsg := fmt.Sprintf(
		`%s
%s
<previous_story_document>%s</previous_story_document>
<previous_mythos_anchor>%s</previous_mythos_anchor>
<must_fix>
%s
</must_fix>
请直接在下一条回复中输出修复后的完整故事文档正文，不需要调用任何工具来提交。逐条针对must_fix修复到位，除修复所需外不要改动其他内容；除非must_fix明确要求，否则不要更换已确认的神话元素（mythos_anchor）；不得改变diversity_constraints中的invest_focus/tone_tags所指向的核心设定。`,
		scenarioRequestBlock(room.req, constraints),
		diversityConstraintsBlock(constraints),
		previous.Document,
		previous.MythosAnchor,
		strings.Join(issues, "\n"),
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(storySystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	logStagePrompt("story_repair", sessionID, msgs)

	result, err := runStoryArchitectLoop(ctx, room, msgs, "story_repair_architect", previous.MythosAnchor)
	if err != nil {
		return StoryOutput{}, fmt.Errorf("story repair failed: %w", err)
	}
	if strings.TrimSpace(result.MythosAnchor) == "" {
		result.MythosAnchor = previous.MythosAnchor
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
- 反编号、反模板腔的要求适用于整份文档：地点、人物、线索、结局段落若写成行首要素标签（"可见信息：""议程：""秘密：""性质：真实"）、①②③编号或字段清单，都必须报问题
- 文中若出现设计术语（核心线索、误导线索、红鲱鱼、承载者、线索矩阵、施动者、宇宙公理、四要素），必须报问题——这些是作者构思时的工作语言，不该出现在成稿里
- 内容空泛套话（"异常的气味""神秘的声音"）要报问题；与空泛同级的问题是堆砌：一句话并列三个以上互不相关的具体细节（专名、属性、动作），细节间无因果/感知/情绪连接，读起来像道具清单或人物卡直译——应报问题，判定标准见standards中"一段一焦点""细节要挂钩"
- 交代零散事实用的短列表（当地人分别知道些什么、卡车上带了哪些装备）是职业模组的常见写法，只要每条都是完整的句子就不要报问题
- 检定与后果写进叙事句（"成功的侦查检定可以发现……""失败的调查员会……"）是正确写法，不要报问题
- 开头的导入段必须保持日常、平静、无恐怖氛围、不剧透真相；若违反必须报告（这是硬约束）
</scope>`
}

// runStoryQAReview 返回人写化整改清单；storyDoc为空或审查不可用/失败时返回nil（非致命，跳过即可）。
func runStoryQAReview(ctx context.Context, room *scripterRoom, storyDoc string) []string {
	if room == nil || room.qa.provider == nil || strings.TrimSpace(storyDoc) == "" {
		return nil
	}
	sessionID := scripterSessionID(ctx, room)
	userMsg := fmt.Sprintf(`<story_document>%s</story_document>
请按standards审查以上故事文档，通过report_issues工具提交问题清单。`,
		storyDoc)
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
