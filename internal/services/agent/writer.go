// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

var writerSessionLocks sync.Map

const writerDefaultPrompt = `<system role="writer_agent" game="coc7" lang="zh-CN">
	<identity>
		你是{{NSFW_WRITER_VOICE}}的场景文字编辑，擅长清晰、具体、有节奏的 COC 叙事。根据导演指令续写当前场景。
	</identity>
	<output format="plain_text" no_markdown="true">
		充分发挥想象力, 直接输出玩家可见叙事文字，不加任何前言、标题、解释或格式标记。
	</output>
	<global_config>
		{{NSFW_GLOBAL_CONFIG}}
	</global_config>
	<requirements>
		<rule>使用简体中文，{{NSFW_WRITER_VOICE}}，采用第三人称视角。</rule>
		<rule>人物对话(NPC 与调查员)用引号标注，并让读者分得清是谁在说话。</rule>
		<rule>禁止出现“SAN值”“HP”“技能值”“检定”等游戏术语。</rule>
		<rule>与上文保持连贯，不重复已描述的内容。</rule>
		<rule>导演指令里的每一条独立事实——时间点/时间跨度、每个被提到的NPC的行踪与当前状态、场景或环境变化——都必须在正文里有对应体现；不得为了聚焦某个高潮画面而整条略去其余事实。指令信息量超出单段篇幅时，用简洁的过渡句交代被压缩的部分，而不是直接丢弃。</rule>
		<rule>人物发言(NPC 与调查员)允许同义改写：可以调整措辞、语气词和句式，让它贴合叙事节奏与说话人的性格，但必须保持说话人、原意、立场、承诺和透露的信息量不变；不得借改写增添、删减或反转发言里的事实，也不得为任何人物编造导演指令和上文里都没有的发言。无发言指令时只写场景、动作、环境和 NPC 反应。</rule>
		<rule>导演指令里出现调查员的台词(「」包裹的原话，或明确转述的发言)时，必须在正文里让该调查员开口说出来，不能只写他的动作和神情就略过开口这件事；说完后只写指令或上文已给出的在场反应，并停在轮到对方回应或玩家选择的位置。指令里没有调查员台词时，不替调查员开口，也不补写调查员的回应。</rule>
		<rule>调查员的台词必须做同义转写，不要照抄玩家原句：对照用户消息 character 块里该调查员的 traits(性格)，并参考 app(外貌)，把措辞、语气、用词习惯和句子长短改成这个人会说出口的样子——寡言的人说得短促，学者用词讲究，粗人说话直白，紧张的人可以吞吐停顿。转写只改说法不改内容：说话对象、原意、立场、问题本身、承诺和透露的信息量都保持不变，不加入原句没有的事实、请求、情绪表态或新信息，也不删掉原句里的任何一项诉求。性格资料为空时，按上文已经表现出来的说话方式转写。</rule>
		<rule>玩家行动边界：只能描写导演指令中明确给出的玩家动作/台词，以及工具已确认的结果；禁止补写玩家下一步、心理反应、同意/拒绝/沉默、跟随、移动、拿起/交出物品、攻击、施法、继续搜索、继续交谈或任何未授权后续动作。</rule>
		<rule>每段叙事要形成完整场景拍点：动作开始、环境/对象反馈、动作结束后的可互动状态；可互动状态只能由导演指令或上文已出现的对象构成，必须停在玩家可选择的位置，不得替玩家跨过选择点。</rule>
		<rule>如果导演指令较短，也要基于已给出的行动、地点和上下文补足自然过渡；但不能新增未授权线索、结果、NPC台词、机械收益或玩家行为。</rule>
		<rule>场景转换只能描写导演指令明确要求的移动；若未明确要求移动，只能描述当前地点和导演指令或上文已出现的出口，不能写玩家已经离开或到达新地点，也不得自行增设出口。</rule>
		<rule>指令中出现具体时刻或“过了多久”这类时间跨度时，必须把它转成正文中能被感知的信号——光线/天色变化、环境声响、人物疲惫或状态变化等——让时间流逝清晰可读，不能只靠一个动作衔接就带过不提。</rule>
		<rule>指令交代了某NPC在场景外的行踪（去了哪里、做了什么、为何此刻出现）时，该NPC登场必须体现来历：脚步声由远及近、外观或气息透露刚经历的事、或一句话点出他从何处赶来；禁止让NPC毫无铺垫地凭空出现在玩家面前。</rule>
		<rule>进行详细的描写, 包括环境、人物动作、物件位置、光线、声音、对话反应等</rule>
		<rule>仔细思考每个细节, 将连贯精彩的画面呈现给玩家, 进行想象让人物的动作更生动具体</rule>
		<rule>鼓励把细节写足：每个拍点都展开成有画面感的完整段落，而不是一句概括。逐项写清空间布局与物件位置、光线与影子的走向、声音与气味的来源和变化、人物手部动作/姿态/表情/呼吸等具体小动作、NPC 的语气与停顿，以及环境对动作的物理反馈（门轴的阻力、纸页的质感、地面的湿度）。</rule>
		<rule>细节只能是对导演指令已确认事实的具体化，用来放大画面，不能用来添加剧情：不得借细节新增未授权的线索、结果、NPC台词或玩家行为。</rule>
		<rule>玩家会根据正文决定下一步行动，导演只知道指令里的内容。凡是玩家可能据此行动的东西——新的出口或通道、可拿取或可检查的物品、文字/符号/痕迹等线索、此前未登场的人物、NPC新的动作或意图暗示——只要导演指令和上文都没有，就一律不写。细节只能加在已存在的对象上，写它们的光线、声音、气味、质感、磨损等感官层面，不得构成新的行动入口。</rule>
		<rule>正文篇幅不设上限，通常应写成多个自然段；宁可多写一层感官细节、多补一个过渡镜头，也不要用概括句带过。不要因为担心“写太长”而主动收尾、跳过段落或把该展开的场景压缩成概括句。</rule>
		{{NSFW_WRITER_RULE}}
		<rule>情节发展必须绝对遵循导演指令, 不得自行添加剧情或人物行为</rule>
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

// writerAgentKey 是Writer在AgentTranscript表里的agent_key。NSFW Writer与默认Writer
// 共用同一条历史线(路由只影响本次调用用哪个provider,不影响历史归属),与旧版
// GameSession.WriterHistory不区分NSFW/非NSFW的行为一致。
const writerAgentKey = "writer"

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

// RunWriter 独立生成白字描述,不参与KP主流程成败。nsfw为true且writer_nsfw已配置启用时路由到NSFW Writer。
func RunWriter(ctx context.Context, gctx GameContext, direction string, nsfw bool) (string, error) {
	lock := writerLock(gctx.Session.ID)
	lock.Lock()
	defer lock.Unlock()
	ctx = withWriterGameSessionID(ctx, gctx)

	writerHandle, nsfwMode, state, err := loadWriterState(gctx, nsfw)
	if err != nil {
		return "", err
	}

	if err := appendWriter(ctx, writerHandle, state, direction, gctx, nsfwMode); err != nil {
		return "", err
	}
	return state.Buffer, nil
}

// RunWriterStream 流式生成白字描述,token会直接回调给上层SSE。nsfw为true且writer_nsfw已配置启用时路由到NSFW Writer。
func RunWriterStream(ctx context.Context, gctx GameContext, direction string, nsfw bool, onToken func(string)) (string, error) {
	lock := writerLock(gctx.Session.ID)
	lock.Lock()
	defer lock.Unlock()
	ctx = withWriterGameSessionID(ctx, gctx)

	writerHandle, nsfwMode, state, err := loadWriterState(gctx, nsfw)
	if err != nil {
		return "", err
	}

	err = appendWriterStream(ctx, writerHandle, state, direction, gctx, nsfwMode, onToken)
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

// writerContextWindow 返回本轮选中的 Writer 配置的上下文窗口；未配置或为 0 时回落
// 默认值，保证 Writer 历史始终有裁剪阈值。
func writerContextWindow(h agentHandle) int64 {
	if h.config != nil && h.config.ContextWindow > 0 {
		return int64(h.config.ContextWindow)
	}
	return models.DefaultWriterContextWindow
}

func loadWriterState(gctx GameContext, nsfw bool) (agentHandle, bool, *WriterState, error) {
	handles, err := getCachedAgents(gctx.Session.ID)
	if err != nil {
		return agentHandle{}, false, nil, err
	}
	writerHandle, nsfwMode, err := pickWriterHandle(handles, nsfw)
	if err != nil {
		return agentHandle{}, false, nil, err
	}

	head := buildWriterHead(writerHandle, gctx)
	cm := LoadContext(gctx.Session.ID, writerAgentKey, head, ContextOptions{Window: writerContextWindow(writerHandle)})
	// 老会话transcript为空但GameSession.WriterHistory列有旧数据时，把旧的扁平历史当作
	// seq=0的一轮种子先提交进去，只执行一次，之后像普通历史轮一样被trim掉。
	if cm.IsEmpty() {
		if legacy := loadLegacyWriterHistory(gctx); len(legacy) > 0 {
			cm.Commit(0, legacy)
		}
	}
	return writerHandle, nsfwMode, &WriterState{cm: cm}, nil
}

// loadLegacyWriterHistory 读取旧版按GameSession.WriterHistory整段存的历史，供
// ContextManager为空时做一次性迁移种子；查库失败时回落gctx自带的快照。
func loadLegacyWriterHistory(gctx GameContext) []llm.ChatMessage {
	var session models.GameSession
	if err := models.DB.Select("id", "writer_history").First(&session, gctx.Session.ID).Error; err == nil {
		return chatMsgsToLLM(session.WriterHistory.Data)
	}
	return chatMsgsToLLM(gctx.Session.WriterHistory.Data)
}

// loadWriterTranscriptHistory 读取AgentTranscript(writer)里已提交的全部历史轮，展平成
// 供角色成长(RunCharacterEvolution)复用的[]models.ChatMsg；找不到记录时(如老会话结束
// 前从未触发过Writer)回落旧的GameSession.WriterHistory列，兼容还没迁移过的数据。
func loadWriterTranscriptHistory(sessionID uint) []models.ChatMsg {
	var rec models.AgentTranscript
	if err := models.DB.Where("session_id = ? AND agent_key = ?", sessionID, writerAgentKey).
		First(&rec).Error; err == nil && rec.Version == contextTranscriptVersion && len(rec.Data.Data.Turns) > 0 {
		return llmToChatMsgs(flattenTranscriptTurns(rec.Data.Data.Turns))
	}
	var session models.GameSession
	if err := models.DB.Select("id", "writer_history").First(&session, sessionID).Error; err == nil {
		return session.WriterHistory.Data
	}
	return nil
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

// writerMaxGenerateAttempts 是 Writer 单次续写在遇到拒绝/空内容时的最大尝试次数(含首次)。
const writerMaxGenerateAttempts = 20

// isWriterResponseRejected 判断一次Writer响应是否需要丢弃重试:剥离thinking块后为空,
// 或正文以拒绝前缀开头。
func isWriterResponseRejected(resp string) bool {
	trimmed := strings.TrimSpace(stripThinkingBlock(resp))
	return trimmed == "" || hasWriterRefusalPrefix(trimmed)
}

// appendWriter 根据导演指令调用Writer,并把生成结果追加到本次白字缓冲。
// 响应为空或以拒绝前缀开头时视为无效,丢弃并重新生成,最多尝试 writerMaxGenerateAttempts 次。
func appendWriter(ctx context.Context, h agentHandle, state *WriterState, direction string, gctx GameContext, nsfwMode bool) error {
	if !h.isEnabled() {
		return fmt.Errorf("writer agent 未配置或未启用")
	}
	opening, direction := buildWriterOpening(direction, gctx, nsfwMode)
	msgs := state.cm.Build(opening)
	debugf("Writer", "direction=%s msgs=%d nsfw=%v", direction, len(msgs), nsfwMode)
	cacheKey := h.cacheKey(fmt.Sprintf("%v", gctx.Session.ID))
	// NOTE: Writer走Chat,拿不到返回值里的Usage;通过WithUsageSink把usage送进cm,
	// 既用于会话上下文后台展示的逐轮缓存统计，也让cm感知超阈值需要trim时同步更新msgs。
	ctx = llm.WithUsageSink(ctx, func(u llm.Usage) { msgs = state.cm.Observe(u, msgs) })

	var resp string
	for attempt := 1; attempt <= writerMaxGenerateAttempts; attempt++ {
		var err error
		resp, err = h.provider.Chat(ctx, cacheKey, msgs)
		if err != nil {
			return err
		}
		if !isWriterResponseRejected(resp) {
			break
		}
		debugf("Writer", "response rejected attempt=%d/%d preview=%s", attempt, writerMaxGenerateAttempts, truncateRunes(resp, 100))
		if attempt == writerMaxGenerateAttempts {
			return fmt.Errorf("writer response rejected after %d attempts", writerMaxGenerateAttempts)
		}
	}

	debugf("Writer", "response len=%d preview=%s", len([]rune(resp)), resp)
	appendWriterResponse(state, gctx, msgs, resp, true)
	return nil
}

// appendWriterStream 流式调用Writer;响应为空或以拒绝前缀开头时丢弃重新生成,
// 最多尝试 writerMaxGenerateAttempts 次。拒绝判定完成前的正文通过 writerRefusalGate
// 缓冲,避免拒绝文本在判定前已经流给玩家。
func appendWriterStream(ctx context.Context, h agentHandle, state *WriterState, direction string, gctx GameContext, nsfwMode bool, onToken func(string)) error {
	if !h.isEnabled() {
		return fmt.Errorf("writer agent 未配置或未启用")
	}
	opening, direction := buildWriterOpening(direction, gctx, nsfwMode)
	msgs := state.cm.Build(opening)
	debugf("Writer", "direction=%s msgs=%d nsfw=%v", direction, len(msgs), nsfwMode)
	cacheKey := h.cacheKey(fmt.Sprintf("%v", gctx.Session.ID))
	ctx = llm.WithUsageSink(ctx, func(u llm.Usage) { msgs = state.cm.Observe(u, msgs) })

	var text string
	for attempt := 1; attempt <= writerMaxGenerateAttempts; attempt++ {
		var err error
		text, err = streamWriterOnce(ctx, h, cacheKey, msgs, onToken)
		if err != nil {
			// 传输层错误：把已流出的部分正文写入缓冲(不进history),再把错误返回给上层。
			appendWriterResponse(state, gctx, msgs, text, false)
			return err
		}
		if !isWriterResponseRejected(text) {
			debugf("Writer", "stream response len=%d preview=%s", len([]rune(text)), text)
			appendWriterResponse(state, gctx, msgs, text, true)
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
func streamWriterOnce(ctx context.Context, h agentHandle, cacheKey string, msgs []llm.ChatMessage, onToken func(string)) (string, error) {
	tokenCh, errCh, err := h.provider.ChatStream(ctx, cacheKey, msgs)
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

// buildWriterHead 构造Writer本次调用固定不变的前缀:仅system prompt。NSFW专属的
// explicit_scene_requirements 不再拼进本函数,而是在buildWriterOpening中拼进本轮
// user消息尾部——这样system prompt在本会话内(EnableNSFW不变)始终字节相同,prompt
// cache前缀才能跨轮稳定命中。
func buildWriterHead(h agentHandle, gctx GameContext) []llm.ChatMessage {
	prompt := renderNSFW(writerDefaultPrompt, gctx.Session.EnableNSFW)
	return []llm.ChatMessage{{
		Role:    "system",
		Content: h.systemPrompt(prompt),
	}}
}

// buildWriterOpening 组装本轮发给Writer的唯一一条user消息:人物卡、导演指令与续写
// 要求;nsfwMode为true时在尾部追加色情场景写作要求。发送版即归档版,不再有另外一份
// 归档内容。
func buildWriterOpening(direction string, gctx GameContext, nsfwMode bool) (llm.ChatMessage, string) {
	if direction == "" {
		direction = "继续描述当前场景"
	}

	sb := &strings.Builder{}
	sb.WriteString("<character>")
	for _, p := range gctx.Session.Players {
		card := p.CharacterCard
		line := fmt.Sprintf("<char><name>%s</name><app>%s</app><traits>%s</traits></char>\n", card.Name, card.Appearance, card.Traits)
		sb.WriteString(line)
	}
	sb.WriteString("</character>\n")
	sb.WriteString("<director_instruction>\n")
	sb.WriteString(direction)
	sb.WriteString("\n</director_instruction>\n")
	sb.WriteString("请在上文的基础上续写文章,并保持逻辑、时间、空间上的连贯,把场景细节充分展开")
	if nsfwMode {
		sb.WriteString(",请将描写的重点放在色情场景上重点突出女角色的反应\n")
		sb.WriteString(writerNSFWPromptSuffix)
	}
	return llm.ChatMessage{Role: "user", Content: sb.String()}, direction
}

func buildWriterScenarioToneBlock(gctx GameContext) string {
	content := gctx.Session.Scenario.Content.Data
	if strings.TrimSpace(content.InvestFocus) == "" && len(content.ToneTags) == 0 && strings.TrimSpace(content.Setting) == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<scenario_tone>\n")
	if strings.TrimSpace(gctx.Session.Scenario.Name) != "" {
		sb.WriteString("script: " + strings.TrimSpace(gctx.Session.Scenario.Name) + "\n")
	}
	if strings.TrimSpace(content.Setting) != "" {
		sb.WriteString("setting_summary: " + truncateRunes(content.Setting, 300) + "\n")
	}
	if strings.TrimSpace(content.InvestFocus) != "" {
		sb.WriteString("invest_focus: " + strings.TrimSpace(content.InvestFocus) + "\n")
	}
	if len(content.ToneTags) > 0 {
		sb.WriteString("tone_tags: " + strings.Join(content.ToneTags, ", ") + "\n")
	}
	sb.WriteString("指令：根据这些标签调整文风、节奏、感官焦点，但不得新增未确认线索或玩家行为。\n")
	sb.WriteString("</scenario_tone>\n")
	return sb.String()
}

// appendWriterResponse 把本次交换(发送的msgs + assistant响应)原样提交为新一轮,
// 发送版即归档版。saveHistory为false时(响应被拒绝或传输层错误)不落库,仅更新Buffer。
func appendWriterResponse(state *WriterState, gctx GameContext, msgs []llm.ChatMessage, resp string, saveHistory bool) {
	resp = stripThinkingBlock(resp)
	if saveHistory {
		finalMsgs := append(msgs, llm.ChatMessage{Role: "assistant", Content: resp})
		state.cm.Commit(gctx.Session.TurnRound, finalMsgs)
	}
	if resp == "" {
		return
	}
	// 本次可能有多段Writer输出,段落之间保留空行。
	if state.Buffer != "" {
		state.Buffer += "\n\n"
	}
	state.Buffer += resp
}

// stripThinkingBlock 清理 LLM 输出前导的思考痕迹。
// 形如:
//
//	Thinking...
//	> something reasoning
//	> more reasoning
//	正文...
//
// 规则:若首行以 "Thinking..." 开头则删除该行,并继续删除其后所有以 ">" 开头的行,
// 直到遇到第一个非 ">" 开头的行,之后的内容原样保留。
func stripThinkingBlock(text string) string {
	lines := strings.Split(text, "\n")
	idx := 0
	if idx >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[idx]), "Thinking...") {
		return text
	}
	idx++ // 跳过 Thinking... 行
	for idx < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[idx]), ">") {
		idx++
	}
	return strings.TrimSpace(strings.Join(lines[idx:], "\n"))
}

func trimWriterHistoryForCache(history []llm.ChatMessage, maxRunes int) []llm.ChatMessage {
	if writerHistoryRuneCount(history) <= maxRunes {
		return history
	}

	targetRunes := maxRunes / 2
	keptRunes := 0
	keepFrom := len(history)
	for i := len(history) - 1; i >= 0; i-- {
		keptRunes += len([]rune(history[i].Content))
		if keptRunes > targetRunes {
			break
		}
		keepFrom = i
	}

	// Keep user/assistant exchanges aligned when possible. Writer history is seeded
	// and appended in pairs, so preserving an even boundary avoids orphan messages.
	if keepFrom%2 == 1 {
		keepFrom++
	}
	if keepFrom >= len(history) {
		keepFrom = len(history) - 1
	}
	if keepFrom < 0 {
		keepFrom = 0
	}
	return history[keepFrom:]
}

func writerHistoryRuneCount(history []llm.ChatMessage) int {
	runeCount := 0
	for _, msg := range history {
		runeCount += len([]rune(msg.Content))
	}
	return runeCount
}

const characterEvolutionPrompt = `你是无限流故事的角色成长编辑。根据角色原有的个人经历、性格特征,以及本次冒险的叙事经历,更新角色的个人经历,体现冒险对角色的影响和成长。

要求:
- 保留角色的核心身份,但反映冒险带来的变化
- 个人经历可以追加新的经历
- 篇幅与原有内容相近,不要过度冗长,一般仅追加一两句话即可,如果过长请考虑总结
- 总篇幅在200字以内
- 仅输出JSON,不要任何额外文字:
{"new_backstory": "更新后的个人经历(200字以内)"}
`

// CharacterEvolutionResult is the writer agent output for a single character's evolution.
type CharacterEvolutionResult struct {
	NewBackstory string `json:"new_backstory"`
}

var evolutionExample = func() string {
	data, err := json.Marshal(CharacterEvolutionResult{})
	if err != nil {
		return ""
	}
	return string(data)
}()

// RunCharacterEvolution uses the Writer agent to generate an updated backstory and traits
// for the given character card, based on the session's WriterHistory.
// The full WriterHistory is reused as conversation context (all messages are already cached
// by the provider from the game session). Only the final evolution request is a new message.
// Returns an error if the Writer agent is not configured or the LLM call fails.
func RunCharacterEvolution(ctx context.Context, card *models.CharacterCard, writerHistory []models.ChatMsg) (CharacterEvolutionResult, error) {
	if len(writerHistory) == 0 {
		return CharacterEvolutionResult{NewBackstory: card.Backstory}, nil
	}

	handle, err := loadSingleAgent(models.AgentRoleEvaluator)
	if err != nil {
		return CharacterEvolutionResult{}, fmt.Errorf("evaluator agent 未配置: %w", err)
	}

	// Copy WriterHistory as-is — all messages hit the provider's prompt cache.
	msgs := make([]llm.ChatMessage, 0, len(writerHistory)+2)
	msgs = append(msgs, llm.ChatMessage{
		Role:    "system",
		Content: handle.systemPrompt(characterEvolutionPrompt),
	})
	for _, m := range writerHistory {
		msgs = append(msgs, llm.ChatMessage{Role: m.Role, Content: m.Content})
	}
	// Append the evolution request as the only new (non-cached) message.
	msgs = append(msgs, llm.ChatMessage{
		Role: "user",
		Content: fmt.Sprintf(
			"根据以上叙事,更新角色【%s】的背景故事(100字)。\n原背景故事:%s\n\n仅输出JSON:{\"new_backstory\": \"...\"}",
			card.Name, card.Backstory,
		),
	})

	resp, err := handle.provider.Chat(ctx, handle.cacheKey(sessionIDFromContextValue(ctx)), msgs)
	if err != nil {
		return CharacterEvolutionResult{}, fmt.Errorf("character evolution LLM error: %w", err)
	}

	var result CharacterEvolutionResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		for i := 0; i < 30; i++ {
			resp, err = RepairJSON(ctx, resp, err, evolutionExample)
			if err == nil {
				err = json.Unmarshal([]byte(resp), &result)
				if err == nil {
					break
				}
			}
			alog.Warn("character evolution JSON parse retry", "character", card.Name, "attempt", i+1, "err", err)
		}
		if err != nil {
			alog.Error("character evolution JSON parse failed", "character", card.Name, "err", err)
			return CharacterEvolutionResult{}, fmt.Errorf("character evolution JSON parse error: %w", err)
		}
	}

	return result, nil
}
