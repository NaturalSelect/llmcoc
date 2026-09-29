// context_transcript.go — 跨 agent 共用的跨回合原生消息链管理组件(ContextManager)。
//
// 取代原来 director_history.go 里 Director 专用、"发送版/归档版"两份内容不一致的实现：
// 发送版=归档版，历史轮原样持久化(含 reasoning/ReasoningBlocks)，按 (session_id,
// agent_key) 落在独立的 AgentTranscript 表(具体设计见 models.AgentTranscript 的注释)，
// 供 Director/Writer/Dramaturg/NPC 共用同一套"按整轮裁剪 + 显式断点标记"逻辑。裁剪
// 有两种互斥的策略(见 TrimStrategy)：TrimDropTurns 整轮丢弃(Writer/Dramaturg/NPC，
// 轮内本就是纯文本，没有需要保留的中间结构)；TrimCompactDirectorTurns 剥离轮内的
// tool_call/tool 结果、只保留首条 user 消息与本轮最终结果(Director，工具调用本身是
// 只读上下文，丢了不影响后续推理，但完全丢弃整轮会连同结果一起丢掉)。
//
// 消息布局约定(Build 的返回值)：
//
//	[head...]                      稳定，调用方每次 run() 重新构造，确定性渲染，永不 trim
//	[历史轮1消息][历史轮2消息]...   已提交的历史，只追加，按整轮裁剪(丢弃或压缩)
//	[opening...][循环内追加的消息]  本轮新消息，Commit 时整体归档为新的一轮
//
// head 末尾、最后一个已提交历史轮末尾各打一个 CacheBreakpoint，是 Anthropic prompt
// cache 断点的稳定锚点；本轮最后一条消息由 llm 层的 applyCacheBreakpoints 自动追加，
// 不需要 ContextManager 关心。
package agent

import (
	"fmt"
	"strings"

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

// compactTranscriptTurns 是 TrimCompactDirectorTurns 策略(Director)的实现：从最旧的
// 轮开始逐轮压缩(compactDirectorTurn)，直到估算用量降到窗口的一半(低水位)或没有更多
// 轮可压缩为止；永远跳过最后一轮(与 trimTranscriptTurns 一样至少保留最近 1 轮不动)，
// 已经压缩过的轮(Compacted=true)和没有工具调用可剥离的轮(如老会话的纯文本种子轮)
// 直接跳过、不重复处理，也不贡献任何用量下降。与 trimTranscriptTurns 不同的是本函数
// 从不丢弃轮——所有历史轮(压缩后)永远保留，压缩只减少轮内消息数量，不减少轮数。
// tokensPerRune/lowWater 的口径与 trimTranscriptTurns 完全一致。
func compactTranscriptTurns(turns []models.TranscriptTurn, window int64, usedTokens int64, totalRunes int64) ([]models.TranscriptTurn, int) {
	if window <= 0 || len(turns) <= 1 {
		return turns, 0
	}
	tokensPerRune := 1.0
	if usedTokens > 0 && totalRunes > 0 {
		tokensPerRune = float64(usedTokens) / float64(totalRunes)
	}
	lowWater := float64(window) / 2
	estimated := float64(usedTokens)

	out := append([]models.TranscriptTurn(nil), turns...)
	n := 0
	for i := 0; i < len(out)-1 && estimated > lowWater; i++ {
		if out[i].Compacted {
			continue
		}
		compacted, ok := compactDirectorTurn(out[i])
		if !ok {
			continue
		}
		estimated -= float64(transcriptTurnRunes(out[i])-transcriptTurnRunes(compacted)) * tokensPerRune
		out[i] = compacted
		n++
	}
	if n == 0 {
		return turns, 0
	}
	return out, n
}

// compactDirectorTurn 把一个 Director 历史轮改写为[首条消息][剥离标记][本轮结果]共
// 3 条消息：首条消息原样保留(轮的起点，通常是 buildKPTurnOpening 产出的第一条 user
// 消息，即"state+<player_turn>"；其后的第二条 user 消息如<system-reminder>连同中间
// 全部 tool_call/tool 消息一起被丢弃)；剥离标记记录被删掉的工具调用统计(见
// renderTrimmedToolCallsMarker)；本轮结果是从轮内 response/write/end_game 三种工具
// 调用的参数里提取出的、这一轮实际生效的产出(见 renderDirectorTurnResult)，不是
// "发送给模型的原始消息"，只用于给 Director 自己在后续轮次里回顾"上一轮做过什么"。
// ok=false 表示该轮没有任何 ToolCalls(如老会话的纯文本种子轮)，不需要也不应该压缩，
// 调用方应原样保留。
func compactDirectorTurn(t models.TranscriptTurn) (models.TranscriptTurn, bool) {
	if len(t.Messages) == 0 {
		return models.TranscriptTurn{}, false
	}
	hasToolCalls := false
	for _, m := range t.Messages {
		if len(m.ToolCalls) > 0 {
			hasToolCalls = true
			break
		}
	}
	if !hasToolCalls {
		return models.TranscriptTurn{}, false
	}

	first := t.Messages[0]
	first.Reasoning = ""
	first.ReasoningBlocks = nil
	marker := models.TranscriptMsg{Role: "user", Content: renderTrimmedToolCallsMarker(t.Messages)}
	result := models.TranscriptMsg{Role: "assistant", Content: renderDirectorTurnResult(t.Messages)}
	return models.TranscriptTurn{
		Seq:       t.Seq,
		Round:     t.Round,
		Compacted: true,
		Messages:  []models.TranscriptMsg{first, marker, result},
	}, true
}

// renderTrimmedToolCallsMarker 统计一轮内(被剥离前)带 ToolCalls 的 assistant 消息数
// (rounds)与工具调用总数(calls)，以及按工具名的计数——按在轮内首次出现的顺序排列，
// 保证渲染结果确定性、跨次调用字节稳定。calls 计入全部工具调用，不区分调用最终是
// 成功执行还是被 SYSTEM REJECT 拒绝(拒绝的调用同样占用了一轮，值得在统计里体现)。
func renderTrimmedToolCallsMarker(msgs []models.TranscriptMsg) string {
	rounds := 0
	calls := 0
	var order []string
	counts := map[string]int{}
	for _, m := range msgs {
		if len(m.ToolCalls) == 0 {
			continue
		}
		rounds++
		for _, tc := range m.ToolCalls {
			calls++
			if _, seen := counts[tc.Name]; !seen {
				order = append(order, tc.Name)
			}
			counts[tc.Name]++
		}
	}
	parts := make([]string, len(order))
	for i, name := range order {
		parts[i] = fmt.Sprintf("%s×%d", name, counts[name])
	}
	return fmt.Sprintf(`<trimmed_tool_calls rounds="%d" calls="%d">%s</trimmed_tool_calls>`, rounds, calls, strings.Join(parts, ", "))
}

// renderDirectorTurnResult 从一轮内的 response/write/end_game 调用参数里提取这一轮
// 实际生效的产出：按消息出现顺序遍历所有 ToolCalls，同类型调用后出现的覆盖先出现
// 的(等价于"最后一次成功调用生效")；调用结果消息(role=tool)以"SYSTEM REJECT"开头
// 视为被拒绝、不采纳(不影响 renderTrimmedToolCallsMarker 的计数，只影响这里是否
// 采纳其参数)；decodeDirectorToolCall 解码失败的调用同样跳过。三块都为空时说明这一
// 轮没有产生任何生效结果(如硬失败提前中断)，返回一个占位说明。
func renderDirectorTurnResult(msgs []models.TranscriptMsg) string {
	results := make(map[string]string, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" {
			results[m.ToolCallID] = m.Content
		}
	}

	var reply, direction, endSummary string
	var options, ack []string
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if strings.HasPrefix(results[tc.ID], "SYSTEM REJECT") {
				continue
			}
			switch ToolCallType(tc.Name) {
			case ToolResponse:
				if parsed, err := decodeDirectorToolCall(llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}); err == nil {
					reply, options, ack = parsed.Reply, parsed.Options, parsed.Ack
				}
			case ToolWrite:
				if parsed, err := decodeDirectorToolCall(llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}); err == nil {
					direction = parsed.Direction
				}
			case ToolEndGame:
				if parsed, err := decodeDirectorToolCall(llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}); err == nil {
					endSummary = parsed.EndSummary
				}
			}
		}
	}

	if reply == "" && direction == "" && endSummary == "" {
		return "<turn_result>（本轮未产生结果）</turn_result>"
	}
	var b strings.Builder
	b.WriteString("<turn_result>\n")
	if reply != "" {
		b.WriteString("<response>\n")
		b.WriteString(reply)
		b.WriteString("\n")
		if len(options) > 0 {
			b.WriteString("<options>" + strings.Join(options, " | ") + "</options>\n")
		}
		if len(ack) > 0 {
			b.WriteString("<ack>" + strings.Join(ack, ";") + "</ack>\n")
		}
		b.WriteString("</response>\n")
	}
	if direction != "" {
		b.WriteString("<write>" + direction + "</write>\n")
	}
	if endSummary != "" {
		b.WriteString("<end_game>" + endSummary + "</end_game>\n")
	}
	b.WriteString("</turn_result>")
	return b.String()
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
	// TrimCompactDirectorTurns 剥离最旧轮内部的 tool_call/tool 结果，只保留该轮首条
	// user 消息与最终结果(compactTranscriptTurns)，永不整轮丢弃——Director 的轮内
	// 工具调用本身是只读上下文，但轮的最终结果(response/write/end_game 的产出)
	// 不能像 Writer/Dramaturg/NPC 那样随便丢弃整轮。
	TrimCompactDirectorTurns
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
	// historyLen 里"的前缀长度——TrimDropTurns 丢弃整轮会同步减少它；
	// TrimCompactDirectorTurns 压缩轮不改变轮数，不需要调整它，但压缩后每轮的消息数
	// 变了，historyLen 必须重新展开计算，不能像丢弃那样简单减去消息数。
	// turnStart=headLen+historyLen(在最近一次 Build 调用时刻)，标记 Build 返回值里
	// opening 的起始下标，据此在 Commit 时定位"这次调用真正新产生的消息"，不受循环内
	// 是否发生过裁剪影响。
	headLen      int
	historyLen   int
	historyTurns int
	turnStart    int
	lastUsage    llm.Usage
	stat         models.TurnCacheStat
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

// trimIfNeeded 是 Observe/Commit 共用的裁剪实现：判断是否超阈值，按 ContextOptions.
// Strategy 分派到 trimTranscriptTurns(整轮丢弃)或 compactTranscriptTurns(压缩
// Director 轮内工具调用)，再把 [headLen:headLen+historyLen] 这段历史消息整体替换成
// 裁剪后的 c.data.Turns[:historyTurns] 重新展开的结果——两种策略下轮内消息数都可能
// 变化(丢弃是变少到 0，压缩是变成固定 3 条)，只有整段重建才能保证正确性，不能像
// "减去消息数"那样在原 msgs 上做局部拼接。返回替换后的 msgs 与本次裁剪触及的轮数；
// 未触发裁剪时原样返回 msgs、n=0。historyLen==0 时没有可裁剪的历史，直接跳过。
func (c *ContextManager) trimIfNeeded(msgs []llm.ChatMessage, usage llm.Usage) ([]llm.ChatMessage, int) {
	if c.historyLen == 0 {
		return msgs, 0
	}
	window, used, totalRunes, ok := c.thresholdParams(msgs, usage)
	if !ok || !contextOverThreshold(window, used) {
		return msgs, 0
	}

	var newTurns []models.TranscriptTurn
	var n int
	switch c.opts.Strategy {
	case TrimCompactDirectorTurns:
		newTurns, n = compactTranscriptTurns(c.data.Turns, window, used, totalRunes)
		if n > 0 {
			c.stat.CompactedTurns += n
		}
	default:
		newTurns, n = trimTranscriptTurns(c.data.Turns, window, used, totalRunes)
		if n > 0 {
			c.historyTurns -= n
			c.stat.TrimmedTurns += n
		}
	}
	if n == 0 {
		return msgs, 0
	}
	c.data.Turns = newTurns

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
	return out, n
}

// Observe 是 toolLoopOptions.afterCall 的实现：每轮 ChatWithTools 成功返回后调用一次，
// 累计本次 run() 内到目前为止的 usage，超过 ContextOptions 配置的阈值时从最旧的已提交
// 历史轮开始整体丢弃，返回去掉了被丢弃历史轮的 msgs。
func (c *ContextManager) Observe(usage llm.Usage, msgs []llm.ChatMessage) []llm.ChatMessage {
	c.lastUsage = usage
	c.stat.Calls++
	c.stat.PromptTokens += usage.PromptTokens
	c.stat.OutputTokens += usage.OutputTokens
	c.stat.CacheReadTokens += usage.CacheReadTokens
	c.stat.CacheCreationTokens += usage.CacheCreationTokens

	out, _ := c.trimIfNeeded(msgs, usage)
	return out
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
