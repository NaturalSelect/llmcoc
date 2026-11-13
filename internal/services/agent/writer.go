// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

var writerSessionLocks sync.Map

const writerDefaultPrompt = `<system role="writer_agent" game="coc7" lang="zh-CN">
	<identity>
		你是{{NSFW_WRITER_VOICE}}的 COC 角色扮演(role play)文本写手，擅长清晰、具体、有节奏的叙事。你的唯一任务是根据最后一条消息里的导演指令，写出一段 role play 文本，并且只输出这段文本。你不是 KP：不裁定规则、不调用工具、不点名玩家、不提问、不给推荐行动。
	</identity>
	<context>
		<rule>之前的对话是 KP(导演)的完整工作记录：模组资料、周期状态、KP 的工具调用与结果(规则裁定、暗骰、NPC 私下反应等)，以及你之前写出的正文(多数在系统追加的 <system-reminder> 提醒里，旧记录里附在 write 或 narrate/ask 的工具结果后，都用 <writer_output> 标签框出来，只是为了让你分清哪一段是正文，标签不属于正文)。记录里的工具都已执行完，{{WRITER_TOOL_RULE}}，也不要续写 KP 的工作：不写 ask_players、narrate、write 之类的调用及其参数(点名、提问、推荐行动)，也不模仿工具结果的格式。</rule>
		<rule>下面规则里说的“上文”，只指玩家已经读到的内容：你之前写出的正文，以及 KP 发给玩家的 narrate/ask 文字。</rule>
		<rule>记录里玩家不知道的信息(模组秘密、暗骰结果、NPC 的隐藏意图、规则原文)不得写进正文，也不得暗示；只写最后一条消息里导演指令要求的内容。</rule>
	</context>
	<output format="markdown">
		只输出这一段 role play 文本本身，不加任何前言、标题、解释或收尾说明。
		<rule>正文按小说排版用 Markdown 输出，段与段之间空一行。</rule>
		<rule>只允许这几种标记：*斜体* 用于内心低语、外文、书名或幻听到的声音；**粗体** 只用于极少数需要读者一眼看到的关键词，一段里最多一处；> 引用块用于信件、日记、碑文、墙上字迹等人物读到的文字；单独一行 --- 用于场景或时间跳转的分隔，它的前后都要空一行。</rule>
		<rule>不要使用标题、列表、表格、代码块、链接{{WRITER_IMAGE_BAN}}；对话照常写在段落里，不要用引用块包裹对话。</rule>
		<rule>输出里不得出现任何尖括号标签，包括 <writer_output>、<tool_result>、<ask_players>、<narrate>、<write>、<director_instruction>，也不要给正文套上任何标签；只输出正文本身。你只写 role play 文本，不生成 ask_players 点名或提问、不列推荐行动、不写给玩家的任何话；KP 对玩家的提问由系统另行展示，不要在正文末尾复述或改写它。</rule>
	</output>{{WRITER_PAINT_SECTION}}
	<global_config>
		{{NSFW_GLOBAL_CONFIG}}
	</global_config>
	<requirements>
		<rule>使用简体中文，{{NSFW_WRITER_VOICE}}，全文固定第三人称：用调查员的名字或“他/她”指代，整段正文里不得用“你”指代调查员，也不要在段末向读者发问。</rule>
		<rule>正文里提到的地点、道具、图纸、文件必须在指令或上文里已经出现；指令里调查员换了位置、拿到或看到了新东西时，用一句过渡写明他在哪里、东西从哪来，不要让人物和物件无交代地出现。</rule>
		<rule>人物对话(NPC 与调查员)用引号标注，并让读者分得清是谁在说话。</rule>
		<rule>禁止出现“SAN值”“HP”“技能值”“检定”等游戏术语。</rule>
		<rule>与上文保持连贯，不重复已描述的内容。</rule>
		<rule>导演指令里的每一条独立事实——时间点/时间跨度、每个被提到的NPC的行踪与当前状态、场景或环境变化——都必须在正文里有对应体现；不得为了聚焦某个高潮画面而整条略去其余事实。指令信息量超出单段篇幅时，用简洁的过渡句交代被压缩的部分，而不是直接丢弃。</rule>
		<rule>人物发言(NPC 与调查员)允许同义改写：可以调整措辞、语气词和句式，让它贴合叙事节奏与说话人的性格，但必须保持说话人、原意、立场、承诺和透露的信息量不变；不得借改写增添、删减或反转发言里的事实，也不得为任何人物编造导演指令和上文里都没有的发言。无发言指令时只写场景、动作、环境和 NPC 反应。</rule>
		<rule>指令里出现 NPC 对玩家提问的回答(无论是原话还是转述)时，必须写成 NPC 开口的对白，一个问题对应一句回答，说清说话人；不得把几个回答压成一句叙述概括带过，也不得只写“她交代了一切”这类不含具体内容的话。</rule>
		<rule>导演指令里出现调查员的台词(「」包裹的原话，或明确转述的发言)时，必须在正文里让该调查员开口说出来，不能只写他的动作和神情就略过开口这件事；说完后只写指令或上文已给出的在场反应，并停在轮到对方回应或玩家选择的位置。指令里没有调查员台词时，不替调查员开口，也不补写调查员的回应。</rule>
		<rule>调查员的台词必须做同义转写，不要照抄玩家原句：对照用户消息 character 块里该调查员的 traits(性格)，并参考 app(外貌)和 eq(当前穿戴或手持的装备，描写时可以引用)，把措辞、语气、用词习惯和句子长短改成这个人会说出口的样子——寡言的人说得短促，学者用词讲究，粗人说话直白，紧张的人可以吞吐停顿。转写只改说法不改内容：说话对象、原意、立场、问题本身、承诺和透露的信息量都保持不变，不加入原句没有的事实、请求、情绪表态或新信息，也不删掉原句里的任何一项诉求。性格资料为空时，按上文已经表现出来的说话方式转写。</rule>
		<rule>玩家行动边界：只能描写导演指令中明确给出的玩家动作/台词，以及工具已确认的结果；禁止补写玩家下一步、心理反应、同意/拒绝/沉默、跟随、移动、拿起/交出物品、攻击、施法、继续搜索、继续交谈或任何未授权后续动作。</rule>
		<rule>每段叙事要形成完整场景拍点：动作开始、环境/对象反馈、动作结束后的可互动状态；可互动状态只能由导演指令或上文已出现的对象构成，必须停在玩家可选择的位置，不得替玩家跨过选择点。</rule>
		<rule>导演指令是一份任务单，按“任务/必写/可补/禁止/停在”分段，你在它划定的框架内工作，不多做也不少做。必写：逐条写进正文，一条不能少，也不能改变其中的事实、结果和说话人。可补：只在这个范围内补足过渡、动作细节和感官描写，范围没写时，补的是必写项所涉对象的过渡、动作和感官层面。禁止：正文里一律不得出现。停在：写完必写项就收住，停在这里给出的状态。除此之外，正文里发生的事件、出现的人和物、说出的话都必须能在必写项里找到；可以引用玩家已经读到的内容(此前的正文、KP 发给玩家的消息)来保持连贯；不能新增未授权线索、结果、NPC台词、机械收益或玩家行为。指令没有分段时，把它的每一行都当作必写项。</rule>
		<rule>只写最后一条消息里 &lt;director_instruction&gt; 的内容。对话记录里 KP 的其他 write 调用及其 direction(包括同一轮里 call_id 不同的调用)、KP 的思考和后续安排，都不是这一段要写的内容：前面已经写出的，不要重写；还没写的，也不要提前写。</rule>
		<rule>场景转换只能描写导演指令明确要求的移动；若未明确要求移动，只能描述当前地点和导演指令或上文已出现的出口，不能写玩家已经离开或到达新地点，也不得自行增设出口。</rule>
		<rule>指令中出现具体时刻或“过了多久”这类时间跨度时，必须把它转成正文中能被感知的信号——光线/天色变化、环境声响、人物疲惫或状态变化等——让时间流逝清晰可读，不能只靠一个动作衔接就带过不提。</rule>
		<rule>指令交代了某NPC在场景外的行踪（去了哪里、做了什么、为何此刻出现）时，该NPC登场必须体现来历：脚步声由远及近、外观或气息透露刚经历的事、或一句话点出他从何处赶来；禁止让NPC毫无铺垫地凭空出现在玩家面前。</rule>
		<rule>把每条必写项写成有画面感的完整段落，而不是一句概括。细节包括环境、人物动作、物件位置、光线与影子的走向、声音与气味的来源和变化、人物手部动作/姿态/表情/呼吸等具体小动作、NPC 的语气与停顿，以及环境对动作的物理反馈（门轴的阻力、纸页的质感、地面的湿度）。</rule>
		<rule>细节只能是对必写项已确认事实的具体化，用来放大画面，不能用来添加剧情：不得借细节新增未授权的事件、线索、结果、NPC台词或玩家行为。写完一条再写下一条，不要为了让场景更精彩而补出指令里没有的情节、转折或新人物。</rule>
		<rule>玩家会根据正文决定下一步行动。凡是玩家可能据此行动的东西——新的出口或通道、可拿取或可检查的物品、文字/符号/痕迹等线索、此前未登场的人物、NPC新的动作或意图暗示——只要导演指令和上文都没有，就一律不写。细节只能加在已存在的对象上，写它们的光线、声音、气味、质感、磨损等感官层面，不得构成新的行动入口。</rule>
		<rule>具体数字只来自导演指令：数量、温度、时长、距离、人数、金额、伤害点数这类精确数字，指令给了就原样使用，指令没给就不要自己编。拿不准的量用可感知的程度表达(烫得吓人、两三步远、一小堆)，不要写成“39.4度”“七英尺”“三个骨包”；同一件事在正文里前后出现时数字必须一致，正文里也不要出现与指令或上文相矛盾的数量和时刻。</rule>
		<rule>失败就写成失败：指令里说某个尝试没成功、没有达成、被挡住或出了岔子时，正文必须把“没有达成”写出来(血仍在渗，绷带没压住；锁芯纹丝不动；脚步声没有被甩掉)。不得用“勉强”“总算”“多少起了点作用”“险些没成”这类措辞把失败改写成部分成功，也不得替它补上指令没给的收益。只有指令明确写了达成了哪一部分，才能写那一部分，且不得超出指令。成功同理，只写指令确认的那件事，不顺手附赠别的好处。</rule>
		<rule>人物首次出场要有交代：指令或上文里出现一个上文从未出现过的人名时，不要把他当成读者早已认识的人直接写进场景；用一两句只依据指令的话带出他是谁、此刻在做什么、从哪里来。指令没有给出身份时，只写看得见听得见的外在表现和他被称呼的名字，不编造职业、关系或来历。</rule>
		<rule>时刻与天色以导演指令为准：指令给了钟点或时间段，正文里的光线、天色、环境声响、人物疲惫程度必须与之相符，不自行换算成别的钟点，也不写成与之矛盾的昼夜；指令没有给时间时，沿用上文已有的光线与天色，不擅自推进时间。</rule>
		<rule>正文篇幅不设上限，通常应写成多个自然段；宁可把每条必写项的感官细节和过渡写足，也不要用概括句带过。不要因为担心“写太长”而主动收尾、跳过必写项或把该展开的场景压缩成概括句；篇幅用来展开必写项，不用来增加必写项之外的内容。</rule>
		{{NSFW_WRITER_RULE}}
		<rule>情节发展必须绝对遵循导演指令，指令里没有的事件、人物行为和对白不得出现，必写项也不得漏写；写完最后一条必写项就停，停在指令给出的“停在”状态。</rule>
	</requirements>
	<style>
		<rule>怪物或异常真正出现时，先用一两个正常细节建立基线，再写异常打破基线，突出反差。</rule>
		<rule>避免无病呻吟和空泛心理描写。不要频繁写“某种不安”“难以言说”“仿佛有什么东西”等没有具体对象的句子。</rule>
		<rule>人物台词里适当使用“哈”“嗯”“啊”“呃”“唉”“哼”“喂”等语气词，以及“……”表示的停顿，让说话像真人开口：笑时带“哈”，迟疑时“呃……”，惊讶时“啊！”，叹气时“唉”。语气词要贴合说话人的性格与当下情绪，一句里最多一个，不要每句都加。语气词只能加在导演指令或上文已有的发言里，不能单独构成一句新发言；不得借语气词改变原意，比如在原话之外加一个“嗯”就表示了同意，或替调查员表达了指令里没有的情绪。</rule>
		<rule>暴力、血腥、性暗示只在导演指令需要时使用；不要为了风格主动添加。</rule>
		<rule>保证信息的完整传达和逻辑连贯：宁可写得更长、文笔不够精炼，也不能省略指令里的时间推进、NPC行踪、状态变化信息；漏掉这些视为不合格输出。</rule>
		<rule>整体基调贴近H.P.洛夫克拉夫特(Lovecraft)风格: 用精确、近乎档案记录般冷静克制的笔法描写具体细节(比例、角度、材质、构造、气味成分、声音频率)，让精确本身透出不祥——反差来自“越描述越清楚，却越发现说不通”，而不是靠空泛情绪词堆砌。</rule>
		<rule>感官线索按气味/声响→触感→视觉的顺序逐层堆叠、层层递进，不要一次性把最骇人的画面摆在开头。</rule>
	</style>
</system>
`

// writerNSFWPromptSuffix 只在实际路由到 NSFW Writer 时追加;共享规则仍来自
// writerDefaultPrompt,避免两份提示词各自漂移。拼进本轮user消息尾部(而不是system),
// 保证system prompt跨轮字节不变,prompt cache前缀才能稳定命中。
const writerNSFWPromptSuffix = `<explicit_scene_requirements>
	<rule>本段被导演标记为色情/性描写场景,必须正面完整描写,禁止淡出、跳过、省略过程或用一句概括带过。</rule>
	<rule>使用具体直接的语言写动作、姿态、触感、声音、气味与情绪反应,按时间顺序推进,不要只铺氛围。</rule>
	<rule>参与者的意愿、情绪和身体反应要写清楚,并与人物性格、当前处境、伤势状态保持一致。</rule>
	<rule>本段仍受上文全部约束:不得越过导演指令新增玩家行为,不得替玩家做出选择,不得出现游戏术语。</rule>
	<rule>请将描写重点放在色情内容上,特别是性器官交互的细节值得使用更大的篇幅书写。</rule>
	<rule>本段结束时同样要停在玩家可选择的位置。</rule>
</explicit_scene_requirements>
`

func writerLock(sessionID uint) *sync.Mutex {
	lock, _ := writerSessionLocks.LoadOrStore(sessionID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func withWriterGameSessionID(ctx context.Context, gctx GameContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if gctx.Session.ID == 0 {
		return ctx
	}
	return context.WithValue(ctx, "session", fmt.Sprintf("%v", gctx.Session.ID))
}

// RunWriterStream 流式生成白字描述,token会直接回调给上层SSE。nsfw为true且writer_nsfw已配置启用时路由到NSFW Writer。
// Writer 没有独立历史:请求 = Writer system + req.Director 的消息链 + 一条续写指令。
func RunWriterStream(ctx context.Context, gctx GameContext, req WriterRequest, onToken func(string)) (string, error) {
	lock := writerLock(gctx.Session.ID)
	lock.Lock()
	defer lock.Unlock()
	ctx = withWriterGameSessionID(ctx, gctx)

	handles, err := getCachedAgents(gctx.Session.ID)
	if err != nil {
		return "", err
	}
	h, nsfwMode, err := pickWriterHandle(handles, req.NSFW)
	if err != nil {
		return "", err
	}

	// NOTE: 提供 paint 需要三个条件同时满足：上层挂了配图启动器、Writer 的 provider 支持流式工具调用、
	// 至少有一个可用的 painter。否则保持原来的路径：工具定义只为让消息链里的 tool_calls/tool 结果合法而声明,请求固定 tool_choice=none。
	start := WriterIllustratorFrom(ctx)
	streamer, canStream := h.provider.(llm.ToolStreamer)
	paintOK := start != nil && canStream && writerPaintAvailable(handles, gctx.Session.EnableNSFW)

	tools, _, _ := buildToolState(directorTools(handles[models.AgentRoleDramaturg].isEnabled()))
	if paintOK {
		// NOTE: paint 追加在 Director 工具列表之后，保持前缀稳定。
		tools = append(tools, writerPaintTool())
	}
	instruction := buildWriterInstruction(req, gctx, nsfwMode, paintOK)
	// NOTE: Director 不等 Writer，快照里没有排在前面、刚写完的 writer 的正文；队列串行保证它们此刻已经结束，
	// 在指令之前补上同一格式的提醒，Writer 才接得上前一条。快照里已经有的（id <= ProseAfter）不重复。
	director := req.Director
	if req.MessageID > 0 {
		if gap := loadFinishedWriters(gctx.Session.ID, req.ProseAfter, req.MessageID); len(gap) > 0 {
			director = append(append([]llm.ChatMessage(nil), director...), renderWriterNotice(gap))
		}
	}
	msgs := buildWriterView(director, writerSystemPrompt(h, nsfwMode, paintOK),
		sameModelConfig(h.config, handles[models.AgentRoleDirector].config), instruction)

	state := &WriterState{}
	if paintOK {
		err = appendWriterStreamTools(ctx, h, streamer, state, msgs, tools, gctx, handles, start, nsfwMode, onToken)
	} else {
		err = appendWriterStream(ctx, h, state, msgs, tools, gctx, nsfwMode, onToken)
	}
	return state.Buffer, err
}

// pickWriterHandle 选择本轮 Writer:仅当本轮被标记为 NSFW 且 writer_nsfw 已配置并启用
// 时才路由过去,否则一律回落默认 Writer。第二个返回值表示是否按 NSFW 模式组装提示词。
func pickWriterHandle(handles map[models.AgentRole]agentHandle, nsfw bool) (agentHandle, bool, error) {
	if nsfw {
		if h := handles[models.AgentRoleWriterNSFW]; h.isEnabled() {
			return h, true, nil
		}
	}
	h := handles[models.AgentRoleWriter]
	if !h.isEnabled() {
		return agentHandle{}, false, fmt.Errorf("writer agent 未配置或未启用")
	}
	return h, false, nil
}

// writerRefusalPrefixes 命中其中任一前缀视为模型拒绝生成正文,与空内容一样需要丢弃重试。
var writerRefusalPrefixes = []string{
	"I cannot fulfill this request.",
	"我无法完成您的请求。",
	"很抱歉",
}

// writerRefusalPrefixMaxLen 是所有拒绝前缀中最长的字节长度,writerRefusalGate
// 需要缓冲到这个长度才能对全部前缀做出判定。
var writerRefusalPrefixMaxLen = func() int {
	max := 0
	for _, p := range writerRefusalPrefixes {
		if len(p) > max {
			max = len(p)
		}
	}
	return max
}()

// hasWriterRefusalPrefix 判断 s 是否以任一拒绝前缀开头。
func hasWriterRefusalPrefix(s string) bool {
	for _, p := range writerRefusalPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// writerMaxGenerateAttempts 是 Writer 单次续写在遇到拒绝/空内容/流中断时的最大尝试次数(含首次)。
const writerMaxGenerateAttempts = 20

// NOTE: 流中断后重新请求前的等待，避免对刚断开的网关立刻重试；测试里改小。
var writerStreamRetryDelay = 2 * time.Second

type writerRestartKey struct{}

// NOTE: WithWriterRestart 挂一个回调，流式 Writer 丢弃半截正文重新请求前调用，上层据此撤回已广播的部分正文。
// keepBytes 是要保留的已转发正文字节数：整段重来时为 0，续写轮中途断流时只撤回当前轮，保留此前各轮已经写出的正文。
func WithWriterRestart(ctx context.Context, fn func(keepBytes int)) context.Context {
	return context.WithValue(ctx, writerRestartKey{}, fn)
}

func notifyWriterRestart(ctx context.Context, keepBytes int) {
	if fn, _ := ctx.Value(writerRestartKey{}).(func(int)); fn != nil {
		fn(keepBytes)
	}
}

// NOTE: Writer 偶尔会照着历史里的 <writer_output>、KP 的工具调用和 <tool_result> 把标签原样写进正文。
// 工具调用块(含里面的点名问题)和 <tool_result>(里面是只有 KP 能看的信息)整段丢弃，问题由系统另行展示；<writer_output> 只剥掉标签、保留正文。
var (
	writerToolBlockPattern  = regexp.MustCompile(`(?is)<(?:ask_players|narrate|write|director_instruction|tool_result)\b[^>]*>.*?(?:</(?:ask_players|narrate|write|director_instruction|tool_result)>|\z)`)
	writerWrapperTagPattern = regexp.MustCompile(`(?i)</?writer_output\b[^>]*>`)
)

// stripWriterControlTags 去掉 Writer 输出里的控制标签，返回收尾去空白后的正文。
func stripWriterControlTags(text string) string {
	if !strings.Contains(text, "<") {
		return strings.TrimSpace(text)
	}
	text = writerToolBlockPattern.ReplaceAllString(text, "")
	text = writerWrapperTagPattern.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

// isWriterResponseRejected 判断一次Writer响应是否需要丢弃重试:去除首尾空白后为空,
// 或正文以拒绝前缀开头。
func isWriterResponseRejected(resp string) bool {
	trimmed := strings.TrimSpace(resp)
	return trimmed == "" || hasWriterRefusalPrefix(trimmed)
}

// appendWriterStream 流式调用Writer;响应为空或以拒绝前缀开头时丢弃重新生成,
// 最多尝试 writerMaxGenerateAttempts 次。拒绝判定完成前的正文通过 writerRefusalGate
// 缓冲,避免拒绝文本在判定前已经流给玩家。msgs 是已经组好的完整请求,tools 只声明不调用。
func appendWriterStream(ctx context.Context, h agentHandle, state *WriterState, msgs []llm.ChatMessage, tools []llm.ToolDefinition, gctx GameContext, nsfwMode bool, onToken func(string)) error {
	if !h.isEnabled() {
		return fmt.Errorf("writer agent 未配置或未启用")
	}
	debugf("Writer", "msgs=%d tools=%d nsfw=%v", len(msgs), len(tools), nsfwMode)
	cacheKey := h.cacheKey(fmt.Sprintf("%v", gctx.Session.ID))

	var text string
	for attempt := 1; attempt <= writerMaxGenerateAttempts; attempt++ {
		var err error
		text, err = streamWriterOnce(ctx, h, cacheKey, msgs, tools, onToken)
		text = stripWriterControlTags(text)
		if err != nil {
			// NOTE: 流在响应完成前被关闭(EOF)时，半截正文整段丢弃重新请求，不能和重试的正文拼在一起。
			if llm.IsUnexpectedEOF(err) && ctx.Err() == nil && attempt < writerMaxGenerateAttempts {
				alog.Warn("writer stream interrupted, retrying", "session", gctx.Session.ID, "attempt", attempt,
					"max_attempts", writerMaxGenerateAttempts, "partial_len", len([]rune(text)), "err", err)
				notifyWriterRestart(ctx, 0)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(writerStreamRetryDelay):
				}
				continue
			}
			// 其他传输层错误：把已流出的部分正文写入缓冲,再把错误返回给上层。
			appendWriterBuffer(state, text)
			return err
		}
		if !isWriterResponseRejected(text) {
			debugf("Writer", "stream response len=%d preview=%s", len([]rune(text)), text)
			appendWriterBuffer(state, text)
			return nil
		}
		debugf("Writer", "stream response rejected attempt=%d/%d preview=%s", attempt, writerMaxGenerateAttempts, truncateRunes(text, 100))
		if attempt == writerMaxGenerateAttempts {
			return fmt.Errorf("writer stream response rejected after %d attempts", writerMaxGenerateAttempts)
		}
	}
	return nil
}

// streamWriterOnce 执行一次Writer流式请求,通过 writerRefusalGate 缓冲正文头部,
// 返回完整原始正文(未做拒绝判定,由调用方决定保存还是丢弃重试)。
func streamWriterOnce(ctx context.Context, h agentHandle, cacheKey string, msgs []llm.ChatMessage, tools []llm.ToolDefinition, onToken func(string)) (string, error) {
	tokenCh, errCh, err := h.provider.ChatStream(ctx, cacheKey, msgs, tools)
	if err != nil {
		return "", err
	}

	// NOTE: token 经拒绝前缀 gate 后才转发,onToken 只收到确认非拒绝的正文;
	// resp 累积原始全文用于拒绝判定和history保存。
	var resp strings.Builder
	var gate writerRefusalGate
	for token := range tokenCh {
		resp.WriteString(token)
		if onToken == nil {
			continue
		}
		if out := gate.feed(token); out != "" {
			onToken(out)
		}
	}
	if onToken != nil {
		if out := gate.eof(); out != "" {
			onToken(out)
		}
	}
	streamErr := <-errCh
	return strings.TrimSpace(resp.String()), streamErr
}

// NOTE: writerRefusalGate 在确认正文不是拒绝前缀(writerRefusalPrefixes)之前缓冲内容、
// 不转发;一旦缓冲长度达到最长前缀长度即可判定:命中任一前缀则转入丢弃状态(整段视为拒绝,
// 后续内容也不转发),未命中则一次性放行缓冲内容并转入直通状态。
type writerRefusalGate struct {
	buf   strings.Builder
	state int // 0=peeking, 1=forwarding(直通), 2=suppressing(丢弃)
}

const (
	wrgPeek     = 0
	wrgForward  = 1
	wrgSuppress = 2
)

func (g *writerRefusalGate) feed(chunk string) string {
	switch g.state {
	case wrgForward:
		return chunk
	case wrgSuppress:
		return ""
	}
	g.buf.WriteString(chunk)
	if g.buf.Len() < writerRefusalPrefixMaxLen {
		return ""
	}
	content := g.buf.String()
	g.buf.Reset()
	if hasWriterRefusalPrefix(content) {
		g.state = wrgSuppress
		return ""
	}
	g.state = wrgForward
	return content
}

// eof 处理流结束时仍处于peek状态(总长度不足最长前缀长度)的残留内容:多前缀长度不一,
// 残留内容仍可能完整命中较短的前缀,因此放行前必须再做一次拒绝判定,命中则丢弃。
func (g *writerRefusalGate) eof() string {
	if g.state != wrgPeek {
		return ""
	}
	content := g.buf.String()
	g.buf.Reset()
	if hasWriterRefusalPrefix(content) {
		g.state = wrgSuppress
		return ""
	}
	return content
}

// NOTE: siteSettingInt 读取 SiteSetting 并解析为 int，解析失败或空值时返回 fallback。
// 与 handlers 包的同名函数逻辑一致，因包隔离各自维护。
func siteSettingInt(key string, fallback int) int {
	s := models.GetSiteSetting(key, "")
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}

// writerSystemPrompt 返回 Writer 本次调用的 system 提示词。基础提示词按本次调用的 nsfwMode
// 渲染(同一 handle 下字节固定);NSFW 专属的 explicit_scene_requirements 不放这里,
// 而是在 buildWriterInstruction 里拼进末尾的 user 消息。房间开启 NSFW 但本次 write
// 标记为 false 时,必须仍用全年龄提示词。paintOK 为 true 时提示词里换成允许调用 paint 的版本。
func writerSystemPrompt(h agentHandle, nsfwMode, paintOK bool) string {
	return h.systemPrompt(renderWriterPaint(renderNSFW(writerDefaultPrompt, nsfwMode), paintOK))
}

// buildWriterInstruction 组装请求末尾的续写指令:人物卡、导演指令与续写要求;
// nsfwMode 为 true 时在尾部追加色情场景写作要求。
func buildWriterInstruction(req WriterRequest, gctx GameContext, nsfwMode, paintOK bool) llm.ChatMessage {
	direction := req.Direction
	if direction == "" {
		direction = "继续描述当前场景"
	}

	sb := &strings.Builder{}
	sb.WriteString("<character>")
	for _, p := range gctx.Session.Players {
		card := p.CharacterCard
		line := fmt.Sprintf("<char><name>%s</name><app>%s</app><eq>%s</eq><traits>%s</traits></char>\n", card.Name, card.Appearance, equippedLabels(card.Inventory.Data), card.Traits)
		sb.WriteString(line)
	}
	sb.WriteString("</character>\n")
	if req.CallID != "" {
		sb.WriteString(fmt.Sprintf("<director_instruction call_id=%q>\n", req.CallID))
	} else {
		sb.WriteString("<director_instruction>\n")
	}
	sb.WriteString(direction)
	sb.WriteString("\n</director_instruction>\n")
	toolClause := writerInstructionToolClauseOff
	if paintOK {
		toolClause = writerInstructionToolClauseOn
	}
	sb.WriteString("这是 KP 刚刚交给你的导演指令(对应上文 call_id 相同的调用)。你的任务是在这份任务单的框架内完成它：把必写项按顺序全部写进一段 role play 文本,在已有正文的基础上保持逻辑、时间、空间上的连贯,只在可补范围内补细节,不增加任务单之外的事件、人物和对白,也不要漏写必写项,不要写上文里其他 write 调用的内容。只输出这段 role play 文本本身:" + toolClause + ",不要生成 ask_players、narrate 等调用,不要点名、提问或给推荐行动,不要输出任何标签")
	if nsfwMode {
		sb.WriteString(",请将描写的重点放在色情场景上重点突出女角色的反应\n")
		sb.WriteString(writerNSFWPromptSuffix)
	}
	return llm.ChatMessage{Role: "user", Content: sb.String()}
}

// appendWriterBuffer 把本次生成的正文追加进白字缓冲,空串忽略。
func appendWriterBuffer(state *WriterState, resp string) {
	if resp == "" {
		return
	}
	// 本次可能有多段Writer输出,段落之间保留空行。
	if state.Buffer != "" {
		state.Buffer += "\n\n"
	}
	state.Buffer += resp
}
