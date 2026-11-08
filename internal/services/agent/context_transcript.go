// context_transcript.go — 跨 agent 共用的跨回合原生消息链管理组件(ContextManager)。
//
// 取代原来 director_history.go 里 Director 专用、"发送版/归档版"两份内容不一致的实现：
// 发送版=归档版，历史轮原样持久化(含 reasoning/ReasoningBlocks)，按 (session_id,
// agent_key) 落在独立的 AgentTranscript 表(具体设计见 models.AgentTranscript 的注释)，
// 供 Director/Writer/Dramaturg/NPC 共用同一套"按整轮裁剪 + 显式断点标记"逻辑。裁剪
// 有两种互斥的策略(见 TrimStrategy)：TrimDropTurns 整轮丢弃(Writer/Dramaturg/NPC，
// 轮内本就是纯文本，没有需要保留的中间结构)；TrimSummarize 不在这里改动历史(Director，
// 历史单元原样累积，只有 LLM 总结才会压缩它，见 director_summary.go)。
//
// 消息布局约定(Build 的返回值)：
//
//	[head...]                      稳定，调用方每次 run() 重新构造，确定性渲染，永不 trim
//	[历史轮1消息][历史轮2消息]...   已提交的历史，只追加，按整轮丢弃或由调用方总结
//	[opening...][循环内追加的消息]  本轮新消息，Commit 时整体归档为新的一轮
//
// head 末尾、最后一个已提交历史轮末尾各打一个 CacheBreakpoint，是 Anthropic prompt
// cache 断点的稳定锚点；本轮最后一条消息由 llm 层的 applyCacheBreakpoints 自动追加，
// 不需要 ContextManager 关心。
package agent

import (
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// contextTranscriptVersion 是 TranscriptData 的结构版本号；LoadContext 读到不匹配的
// Version 时视为空 transcript 重新开始，不做迁移，避免升级后旧结构数据被错误解析。
const contextTranscriptVersion = 1

// maxTranscriptStats 是 TranscriptData.Stats 保留的最大条数，只给后台按会话展示
// 逐轮缓存命中率用，是纯审计数据，超出部分从最旧的开始丢弃。
const maxTranscriptStats = 50

// contextReserveThreshold/contextReserveTokens/contextReserveRatio 复刻 Angela
// (charm.land/fantasy 宿主项目) internal/config/config.go 的 ReserveFor 规则：
// 窗口大于 200k 时预留固定 20k token，否则预留窗口的 20%——固定预留在小窗口下会
// 吞掉大部分可用空间，所以小窗口改用比例。
const (
	contextReserveThreshold int64   = 200_000
	contextReserveTokens    int64   = 20_000
	contextReserveRatio     float64 = 0.2
)

// contextReserve 返回窗口 window 应该预留出来、不计入可用额度的 token 数。
func contextReserve(window int64) int64 {
	if window > contextReserveThreshold {
		return contextReserveTokens
	}
	return int64(float64(window) * contextReserveRatio)
}

// contextOverThreshold 判断 used(最近一次调用的估算用量)是否已经逼近 window 上限、
// 需要触发一次整体 trim。window<=0 表示未配置阈值，永不触发。used/window 的单位由
// 调用方决定(token 或 rune)，两者只要口径一致即可。
func contextOverThreshold(window int64, used int64) bool {
	if window <= 0 {
		return false
	}
	return used >= window-contextReserve(window)
}

// contextEstimatedUsedTokens 返回应该拿去和 Window 比较的 token 数：网关返回了 usage
// 时直接用 usage.ContextTokens()(prompt+output)；网关未返回 usage(全零，部分中转网关
// 如此)时退化为按"1 rune ≈ 1 token"估算 msgs 的总字符数，保证阈值判断在没有 usage 的
// 网关上依然生效。
func contextEstimatedUsedTokens(usage llm.Usage, msgs []llm.ChatMessage) int64 {
	if usage.PromptTokens > 0 || usage.OutputTokens > 0 {
		return usage.ContextTokens()
	}
	return chatMessagesRuneCount(msgs)
}

// chatMessagesRuneCount 统计一组消息(含工具调用参数)的总字符数，用于估算 token 密度、
// 或在 usage 缺失时直接作为用量。
func chatMessagesRuneCount(msgs []llm.ChatMessage) int64 {
	var n int64
	for _, m := range msgs {
		n += int64(len([]rune(m.Content)))
		for _, tc := range m.ToolCalls {
			n += int64(len([]rune(tc.Arguments)))
		}
	}
	return n
}

// transcriptTurnRunes 统计一个历史回合(含其中的工具调用参数)的总字符数。
func transcriptTurnRunes(t models.TranscriptTurn) int64 {
	var n int64
	for _, m := range t.Messages {
		n += int64(len([]rune(m.Content)))
		for _, tc := range m.ToolCalls {
			n += int64(len([]rune(tc.Arguments)))
		}
	}
	return n
}

// trimTranscriptTurns 是 TrimDropTurns 策略(Writer/Dramaturg/NPC)的实现：从最旧的
// 整轮开始批量丢弃，直到估算的总用量降到窗口的一半(低
// 水位)，至少保留最近 1 轮；按回合(而不是按消息)裁剪保证不会拆散某一轮内部的
// tool_call 与对应 tool 结果的配对。tokensPerRune = usedTokens/totalRunes 是"当前这次
// 调用"的 token/字符密度，用于把只有字符数可数的历史回合换算成估算 token 数。
// 网关未返回 usage 时 usedTokens 退化为 totalRunes，tokensPerRune 自然为 1。
// 调用方应仅在 contextOverThreshold 判定超过阈值时才调用本函数；未超过阈值时
// estimated 不会跌破 lowWater，循环体不会执行，函数本身也可以安全地无条件调用。
func trimTranscriptTurns(turns []models.TranscriptTurn, window int64, usedTokens int64, totalRunes int64) ([]models.TranscriptTurn, int) {
	if window <= 0 || len(turns) <= 1 {
		return turns, 0
	}
	tokensPerRune := 1.0
	if usedTokens > 0 && totalRunes > 0 {
		tokensPerRune = float64(usedTokens) / float64(totalRunes)
	}
	lowWater := float64(window) / 2
	estimated := float64(usedTokens)

	dropped := 0
	for estimated > lowWater && dropped < len(turns)-1 {
		estimated -= float64(transcriptTurnRunes(turns[dropped])) * tokensPerRune
		dropped++
	}
	if dropped == 0 {
		return turns, 0
	}
	return turns[dropped:], dropped
}

// llmMsgToTranscript/transcriptMsgToLLM 在 llm.ChatMessage 与持久化用的
// models.TranscriptMsg 之间转换，字段一一对应、不丢弃 Reasoning/ReasoningBlocks——
// 与旧 llmToDirectorMsgs 不同，这里必须做到发送版=归档版，历史轮原样存档才能保证
// prompt cache 前缀跨轮稳定。

func llmMsgToTranscript(m llm.ChatMessage) models.TranscriptMsg {
	tm := models.TranscriptMsg{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, Reasoning: m.Reasoning}
	if len(m.ToolCalls) > 0 {
		tm.ToolCalls = make([]models.TranscriptToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			tm.ToolCalls[i] = models.TranscriptToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}
		}
	}
	if len(m.ReasoningBlocks) > 0 {
		tm.ReasoningBlocks = make([]models.TranscriptReasoningBlock, len(m.ReasoningBlocks))
		for i, rb := range m.ReasoningBlocks {
			tm.ReasoningBlocks[i] = models.TranscriptReasoningBlock{Type: rb.Type, Text: rb.Text, Signature: rb.Signature, Data: rb.Data}
		}
	}
	return tm
}

func llmMsgsToTranscript(msgs []llm.ChatMessage) []models.TranscriptMsg {
	out := make([]models.TranscriptMsg, len(msgs))
	for i, m := range msgs {
		out[i] = llmMsgToTranscript(m)
	}
	return out
}

func transcriptMsgToLLM(m models.TranscriptMsg) llm.ChatMessage {
	cm := llm.ChatMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, Reasoning: m.Reasoning}
	if len(m.ToolCalls) > 0 {
		cm.ToolCalls = make([]llm.ToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			cm.ToolCalls[i] = llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}
		}
	}
	if len(m.ReasoningBlocks) > 0 {
		cm.ReasoningBlocks = make([]llm.ReasoningBlock, len(m.ReasoningBlocks))
		for i, rb := range m.ReasoningBlocks {
			cm.ReasoningBlocks[i] = llm.ReasoningBlock{Type: rb.Type, Text: rb.Text, Signature: rb.Signature, Data: rb.Data}
		}
	}
	return cm
}

// flattenTranscriptTurns 把按回合切分的历史展开成一条连续的 llm.ChatMessage 序列，
// 供拼进本轮请求的 msgs。
func flattenTranscriptTurns(turns []models.TranscriptTurn) []llm.ChatMessage {
	total := 0
	for _, t := range turns {
		total += len(t.Messages)
	}
	out := make([]llm.ChatMessage, 0, total)
	for _, t := range turns {
		for _, m := range t.Messages {
			out = append(out, transcriptMsgToLLM(m))
		}
	}
	return out
}

// ContextOptions 配置 ContextManager 的用量阈值与裁剪策略。Window 是模型上下文窗口
// 的 token 数，Window<=0 表示历史无限增长、永不裁剪。所有 agent 统一按 token 判断：
// Director 从 ChatWithTools 的返回值拿 Usage，Writer/Dramaturg/NPC 通过
// llm.WithUsageSink 拿 Usage；网关不返回 usage 时退化为按总字符数估算。Strategy 决定
// 超限后具体怎么裁剪，零值 TrimDropTurns 是 Writer/Dramaturg/NPC 使用的默认值。
type ContextOptions struct {
	Window   int64
	Strategy TrimStrategy
}

// TrimStrategy 选择 ContextManager 超过用量阈值后的裁剪方式。
type TrimStrategy int

const (
	// TrimDropTurns 从最旧的整轮开始整体丢弃(trimTranscriptTurns)，是 Writer/
	// Dramaturg/NPC 使用的默认策略——它们的轮本就是纯文本对话，没有需要单独保留的
	// 中间结构，丢弃整轮最简单也最省空间。
	TrimDropTurns TrimStrategy = iota
	// TrimSummarize 是 Director 的策略：Observe/Commit 不改动历史，只报告上下文是否已满，
	// 由调用方在拍的边界上调用 compactHistory 做 LLM 总结。Director 的单元里有已经生效的
	// 结算，确定性地剥离或丢弃它们都会让 KP 忘掉已发生的事。
	TrimSummarize
)

// ContextManager 管理单个 (session_id, agent_key) 的跨回合原生消息链：Build 拼出本次
// 调用要发送的完整消息序列并打好 prompt cache 断点，Observe 在工具循环每一轮结束后
// 累计用量、按阈值整体丢弃最旧的历史轮，Commit 在本轮结束时把新产生的消息追加为一轮
// 并落库。同一个 ContextManager 实例只应在一次 run() 内使用一轮 Build→Observe*→Commit。
type ContextManager struct {
	sessionID uint
	agentKey  string
	head      []llm.ChatMessage
	opts      ContextOptions
	data      models.TranscriptData

	// headLen/historyLen 是 Build 时 [head...][历史轮...] 各自贡献的消息条数；
	// historyTurns 是历史轮的条数(不是消息数)，标记 c.data.Turns 里"已经反映在
	// historyLen 里"的前缀长度——TrimDropTurns 丢弃整轮、总结替换前缀都会改变它，
	// 改变后必须经 rebuild 重新展开计算 historyLen，不能简单减去消息数。
	// turnStart=headLen+historyLen(在最近一次 Build 调用时刻)，标记 Build 返回值里
	// opening 的起始下标，据此在 Commit 时定位"这次调用真正新产生的消息"，不受循环内
	// 是否发生过裁剪影响。
	headLen      int
	historyLen   int
	historyTurns int
	turnStart    int
	lastUsage    llm.Usage
	stat         models.TurnCacheStat

	// floor/unit 只给 Director 的按拍落库用：floor 是本次 Build 时领到的发言权周期编号，
	// unit 是本周期内已落库的发布单元数。
	floor int
	unit  int
}

// LoadContext 按 (session_id, agent_key) 读取已持久化的 transcript；未找到记录或
// Version 不匹配时从空 transcript 开始。head 是该 agent 本次调用固定不变的前缀(如
// system prompt + 确定性渲染的场景/人设消息)，由调用方在每次 run() 时重新构造并传入，
// 不持久化——历史里的消息只包含 Build 的 opening 之后新产生的部分。
func LoadContext(sessionID uint, agentKey string, head []llm.ChatMessage, opts ContextOptions) *ContextManager {
	cm := &ContextManager{sessionID: sessionID, agentKey: agentKey, head: head, opts: opts}
	var rec models.AgentTranscript
	if err := models.DB.Where("session_id = ? AND agent_key = ?", sessionID, agentKey).
		First(&rec).Error; err == nil && rec.Version == contextTranscriptVersion {
		cm.data = rec.Data.Data
	}
	return cm
}

// NextSeq 返回下一次调用应该使用的回合序号，供调用方写进本轮 opening 消息里(如
// Director 的 <player_turn seq=N>)标识"最新一轮"。不能用 GameSession.TurnRound 代替：
// 同一个 TurnRound 内可能有多次调用(如战斗/追逐一个回合内多名参与者各自触发一次
// run())，seq 必须严格递增且每次调用唯一。
func (c *ContextManager) NextSeq() int {
	return c.data.NextSeq
}

// NextFloor 返回下一个发言权周期（floor）的编号，从 1 开始。Director 的 opening 用它标识
// "最新一个周期"；周期内第一个发布单元落库时才真正占用该编号，硬失败的周期不会消耗编号。
func (c *ContextManager) NextFloor() int {
	if c.data.NextFloor < 1 {
		return 1
	}
	return c.data.NextFloor
}

// IsEmpty 报告 transcript 是否还没有任何已提交的历史轮，供调用方判断是否需要把旧数据
// (如老会话按纯文本 transcript 存的历史)播种为 seq=0 的一轮，且只在为空时播种一次。
func (c *ContextManager) IsEmpty() bool {
	return len(c.data.Turns) == 0
}

// Build 拼出本次调用要发送的完整消息序列：head + 已提交的历史轮(原样展开，含
// reasoning) + opening(本轮新消息)，并在 head 末尾、最后一个已提交历史轮末尾打上
// CacheBreakpoint——这两个位置跨调用字节不变，是 Anthropic prompt cache 断点的稳定
// 锚点；历史为空时只标 head。
func (c *ContextManager) Build(opening ...llm.ChatMessage) []llm.ChatMessage {
	c.headLen = len(c.head)
	c.historyTurns = len(c.data.Turns)
	historyMsgs := flattenTranscriptTurns(c.data.Turns)
	c.historyLen = len(historyMsgs)

	msgs := make([]llm.ChatMessage, 0, c.headLen+c.historyLen+len(opening))
	msgs = append(msgs, c.head...)
	if c.headLen > 0 {
		msgs[c.headLen-1].CacheBreakpoint = true
	}
	msgs = append(msgs, historyMsgs...)
	if c.historyLen > 0 {
		msgs[len(msgs)-1].CacheBreakpoint = true
	}

	c.turnStart = len(msgs)
	c.lastUsage = llm.Usage{}
	c.stat = models.TurnCacheStat{}
	c.floor = c.NextFloor()
	c.unit = 0
	return append(msgs, opening...)
}

// thresholdParams 返回这次判断用的窗口与已用量：Window>0 时用 usage 换算 token 数与
// Window 比较；Window<=0 时 ok=false，调用方不做任何 trim 判断。
func (c *ContextManager) thresholdParams(msgs []llm.ChatMessage, usage llm.Usage) (window int64, used int64, totalRunes int64, ok bool) {
	if c.opts.Window <= 0 {
		return 0, 0, 0, false
	}
	return c.opts.Window, contextEstimatedUsedTokens(usage, msgs), chatMessagesRuneCount(msgs), true
}

// trimIfNeeded 是 Observe/Commit 共用的裁剪实现：TrimDropTurns 策略下判断是否超阈值，
// 超过就用 trimTranscriptTurns 整轮丢弃最旧的历史，再经 rebuild 重新展开历史消息。
// 返回替换后的 msgs 与被丢弃的轮数；未触发裁剪时原样返回 msgs、n=0。
// historyLen==0 时没有可裁剪的历史，TrimSummarize 策略从不在这里改动历史，都直接跳过。
func (c *ContextManager) trimIfNeeded(msgs []llm.ChatMessage, usage llm.Usage) ([]llm.ChatMessage, int) {
	if c.historyLen == 0 || c.opts.Strategy == TrimSummarize {
		return msgs, 0
	}
	window, used, totalRunes, ok := c.thresholdParams(msgs, usage)
	if !ok || !contextOverThreshold(window, used) {
		return msgs, 0
	}

	newTurns, n := trimTranscriptTurns(c.data.Turns, window, used, totalRunes)
	if n == 0 {
		return msgs, 0
	}
	c.historyTurns -= n
	c.stat.TrimmedTurns += n
	c.data.Turns = newTurns
	return c.rebuild(msgs), n
}

// rebuild 把 [headLen:headLen+historyLen] 这段历史消息整体替换成 c.data.Turns[:historyTurns]
// 重新展开的结果，msgs[turnStart:] 之后的本轮消息原样保留，并补打历史末尾的断点、更新
// historyLen/turnStart。丢弃整轮和总结替换前缀之后轮内消息数都会变，只有整段重建才能保证
// 正确性，不能像"减去消息数"那样在原 msgs 上做局部拼接。
func (c *ContextManager) rebuild(msgs []llm.ChatMessage) []llm.ChatMessage {
	hist := flattenTranscriptTurns(c.data.Turns[:c.historyTurns])
	out := make([]llm.ChatMessage, 0, c.headLen+len(hist)+(len(msgs)-c.turnStart))
	out = append(out, msgs[:c.headLen]...)
	out = append(out, hist...)
	out = append(out, msgs[c.turnStart:]...)
	if len(hist) > 0 {
		out[c.headLen+len(hist)-1].CacheBreakpoint = true
	}
	c.historyLen = len(hist)
	c.turnStart = c.headLen + len(hist)
	return out
}

// currentFloorStart 返回 c.data.Turns 里本周期(c.floor)第一个非总结单元的下标，没有时返回 len。
// 只在 Build 之后有意义；本周期的单元永远不能被总结，因为 KP 还在这个周期里工作。
func (c *ContextManager) currentFloorStart() int {
	for i, t := range c.data.Turns {
		if t.Kind != "summary" && t.Floor == c.floor {
			return i
		}
	}
	return len(c.data.Turns)
}

// Observe 是 toolLoopOptions.afterCall 的实现：每轮 ChatWithTools 成功返回后调用一次，
// 累计本次 run() 内到目前为止的 usage；TrimDropTurns 策略下超过阈值时从最旧的已提交
// 历史轮开始整体丢弃，返回去掉了被丢弃历史轮的 msgs。
func (c *ContextManager) Observe(usage llm.Usage, msgs []llm.ChatMessage) []llm.ChatMessage {
	out, _ := c.observe(usage, msgs)
	return out
}

// observe 与 Observe 相同，额外报告"上下文是否仍然逼近窗口上限"：TrimDropTurns 下是丢弃之后
// 按剩余字符数折算的估算值仍然超过阈值或没有更多可丢弃的轮；TrimSummarize 下历史不会被改动，
// 超过阈值就报告已满，由调用方决定是否总结。Window<=0 时恒为 false。
func (c *ContextManager) observe(usage llm.Usage, msgs []llm.ChatMessage) ([]llm.ChatMessage, bool) {
	c.lastUsage = usage
	c.stat.Calls++
	c.stat.PromptTokens += usage.PromptTokens
	c.stat.OutputTokens += usage.OutputTokens
	c.stat.CacheReadTokens += usage.CacheReadTokens
	c.stat.CacheCreationTokens += usage.CacheCreationTokens

	window, used, totalRunes, ok := c.thresholdParams(msgs, usage)
	out, n := c.trimIfNeeded(msgs, usage)
	if !ok || !contextOverThreshold(window, used) {
		return out, false
	}
	if n == 0 {
		return out, true
	}
	estimated := used
	if totalRunes > 0 {
		estimated = int64(float64(used) * float64(chatMessagesRuneCount(out)) / float64(totalRunes))
	}
	return out, contextOverThreshold(window, estimated)
}

// Commit 在本轮 run() 结束时调用：把 finalMsgs[turnStart:]原样(含 reasoning)追加为
// 新的一轮持久化，再做一次收尾阈值检查，最后写入 Stats 并落库。round 通常是
// GameSession.TurnRound，只用于展示，不参与 trim 判断；调用方需要自行保证只在"至少
// 取得过一轮进展"时才调用(硬失败不应该 Commit，与旧协议一致)。
func (c *ContextManager) Commit(round int, finalMsgs []llm.ChatMessage) {
	newTurnMsgs := finalMsgs[c.turnStart:]
	seq := c.data.NextSeq
	c.data.NextSeq++
	c.data.Turns = append(c.data.Turns, models.TranscriptTurn{
		Seq:      seq,
		Round:    round,
		Messages: llmMsgsToTranscript(newTurnMsgs),
	})

	c.trimIfNeeded(finalMsgs, c.lastUsage)

	c.stat.Seq = seq
	c.stat.Round = round
	c.data.Stats = append(c.data.Stats, c.stat)
	if len(c.data.Stats) > maxTranscriptStats {
		c.data.Stats = c.data.Stats[len(c.data.Stats)-maxTranscriptStats:]
	}
	c.save()
}

// Checkpoint 在 Director 的某一轮产生了玩家可见消息后调用：把 msgs[turnStart:] 作为一个发布单元
// 追加并落库，使 transcript 与消息表一一对应，进程中途退出也只会丢最后一拍。msgs 必须以完整的
// tool 结果结尾（afterDispatch 钩子满足这一点）。落库后 turnStart 前移到 len(msgs)，此前的单元对
// 之后的 Observe 而言就是"已提交的历史"，可以被总结。不做阈值检查，留给下一次 Observe。
func (c *ContextManager) Checkpoint(round int, msgs []llm.ChatMessage) {
	if !hasAssistantRound(msgs[min(c.turnStart, len(msgs)):]) {
		return
	}
	c.appendUnit(round, msgs, len(msgs), "")
}

// hasAssistantRound 报告 msgs 里是否至少有一条 assistant 消息；只有 opening 或提醒的尾巴不算进展。
func hasAssistantRound(msgs []llm.ChatMessage) bool {
	for _, m := range msgs {
		if m.Role == "assistant" {
			return true
		}
	}
	return false
}

// CommitTail 在 floor 结束、被打断或出错时调用：最后一个单元之后可能还有已经执行完的轮（例如
// 状态结算后被取消），这些副作用已经生效，必须留在 KP 的记忆里。只落库其中"完整"的前缀——
// 带 tool_calls 的 assistant 消息必须有齐全的 tool 结果，被截断的最后一轮直接丢弃。
// 没有任何完整的轮可落（包括只有 opening 的硬失败）时什么都不做，与旧协议的硬失败语义一致。
func (c *ContextManager) CommitTail(round int, msgs []llm.ChatMessage) {
	cut := completeTailEnd(msgs, c.turnStart)
	if cut <= c.turnStart {
		return
	}
	// NOTE: 只有 opening（以及提醒）没有任何 assistant 轮的尾巴不算进展，不落库。
	if !hasAssistantRound(msgs[c.turnStart:cut]) {
		return
	}
	c.appendUnit(round, msgs, cut, "tail")
	c.trimIfNeeded(msgs[:cut], c.lastUsage)
	c.save()
}

// completeTailEnd 返回 msgs[from:] 中最后一个"完整"位置：遍历到带 tool_calls 的 assistant 消息时，
// 要求紧随其后的 tool 消息覆盖全部 tool_call_id，否则从该 assistant 消息处截断。
func completeTailEnd(msgs []llm.ChatMessage, from int) int {
	end := from
	for i := from; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			need := make(map[string]bool, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				need[tc.ID] = true
			}
			j := i + 1
			for ; j < len(msgs) && msgs[j].Role == "tool"; j++ {
				delete(need, msgs[j].ToolCallID)
			}
			if len(need) > 0 {
				return end
			}
			i = j - 1
			end = j
			continue
		}
		end = i + 1
	}
	return end
}

// appendUnit 把 msgs[turnStart:cut] 追加为一个发布单元并落库，同时推进偏移量与 Stats。
func (c *ContextManager) appendUnit(round int, msgs []llm.ChatMessage, cut int, kind string) {
	seq := c.data.NextSeq
	c.data.NextSeq++
	c.unit++
	if c.unit == 1 {
		c.data.NextFloor = c.floor + 1
	}
	c.data.Turns = append(c.data.Turns, models.TranscriptTurn{
		Seq:      seq,
		Round:    round,
		Floor:    c.floor,
		Unit:     c.unit,
		Kind:     kind,
		Messages: llmMsgsToTranscript(msgs[c.turnStart:cut]),
	})
	c.historyTurns++
	c.historyLen += cut - c.turnStart
	c.turnStart = cut

	c.stat.Seq = seq
	c.stat.Round = round
	c.stat.LastContextTokens = c.lastUsage.ContextTokens()
	c.data.Stats = append(c.data.Stats, c.stat)
	if len(c.data.Stats) > maxTranscriptStats {
		c.data.Stats = c.data.Stats[len(c.data.Stats)-maxTranscriptStats:]
	}
	c.stat = models.TurnCacheStat{}
	c.save()
}

// DeleteContext 删除(session_id, agent_key)对应的整条持久化transcript记录，用于NPC
// 重新创建(seedNPCFromMemory)时彻底丢弃旧的存活上下文，只保留(若有)压缩后的长期记忆。
func DeleteContext(sessionID uint, agentKey string) {
	if err := models.DB.Where("session_id = ? AND agent_key = ?", sessionID, agentKey).
		Delete(&models.AgentTranscript{}).Error; err != nil {
		alog.Error("delete agent transcript failed", "session", sessionID, "agent_key", agentKey, "err", err)
	}
}

// save 按 (session_id, agent_key) upsert 整个 TranscriptData。
func (c *ContextManager) save() {
	err := models.DB.Where(models.AgentTranscript{SessionID: c.sessionID, AgentKey: c.agentKey}).
		Assign(models.AgentTranscript{
			Version: contextTranscriptVersion,
			Data:    models.JSONField[models.TranscriptData]{Data: c.data},
		}).
		FirstOrCreate(&models.AgentTranscript{}).Error
	if err != nil {
		alog.Error("save agent transcript failed", "session", c.sessionID, "agent_key", c.agentKey, "err", err)
	}
}
