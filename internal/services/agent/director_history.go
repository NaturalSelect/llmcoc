// director_history.go — Director 跨回合原生消息链的持久化与阈值整体 trim。
//
// 在此之前，Director 的 run() 每回合结束都会把 runToolLoop 里积累的完整原生消息链
// (assistant 的 tool_calls、每次工具调用对应的 tool 结果)整个丢弃，下一回合只剩
// formatHistoryTranscript 生成的一段 HIST(RO) 纯文本——等价于"每次都 trim"。本文件
// 把这条链按回合(DirectorTurn)持久化到 GameSession.DirectorHistory，跨回合原样
// 接续；历史默认无限增长，只有当 AgentConfig.ContextWindow 配置了非零值、且最近一次
// 调用的 token 用量已经逼近该窗口时，才从最旧的整轮开始批量丢弃、一次性降到窗口的
// 一半(低水位)，而不是每次都裁一点——两次 trim 之间的多轮请求前缀保持稳定，能持续
// 命中 prompt cache。
package agent

import (
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// directorReserveThreshold/directorReserveTokens/directorReserveRatio 复刻 Angela
// (charm.land/fantasy 宿主项目) internal/config/config.go 的 ReserveFor 规则：
// 窗口大于 200k 时预留固定 20k token，否则预留窗口的 20%——固定预留在小窗口下会
// 吞掉大部分可用空间，所以小窗口改用比例。
const (
	directorReserveThreshold int64   = 200_000
	directorReserveTokens    int64   = 20_000
	directorReserveRatio     float64 = 0.2
)

// directorReserve 返回窗口 window 应该预留出来、不计入可用额度的 token 数。
func directorReserve(window int64) int64 {
	if window > directorReserveThreshold {
		return directorReserveTokens
	}
	return int64(float64(window) * directorReserveRatio)
}

// directorOverThreshold 判断 used(最近一次调用的估算 token 用量)是否已经逼近窗口
// 上限、需要触发一次整体 trim。window<=0 表示未配置阈值，永不触发。
func directorOverThreshold(window int64, used int64) bool {
	if window <= 0 {
		return false
	}
	return used >= window-directorReserve(window)
}

// directorEstimatedUsedTokens 返回应该拿去和 window 比较的 token 数：网关返回了
// usage 时直接用 usage.ContextTokens()(prompt+output)；网关未返回 usage(全零，部分
// 中转网关如此)时退化为按"1 rune ≈ 1 token"估算 msgs 的总字符数，保证阈值判断在
// 没有 usage 的网关上依然生效。
func directorEstimatedUsedTokens(usage llm.Usage, msgs []llm.ChatMessage) int64 {
	if usage.PromptTokens > 0 || usage.OutputTokens > 0 {
		return usage.ContextTokens()
	}
	return chatMessagesRuneCount(msgs)
}

// chatMessagesRuneCount 统计一组消息(含工具调用参数)的总字符数，用于估算 token 密度
// 或在 usage 缺失时兜底估算用量。
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

// directorTurnRunes 统计一个历史回合(含其中的工具调用参数)的总字符数。
func directorTurnRunes(t models.DirectorTurn) int64 {
	var n int64
	for _, m := range t.Messages {
		n += int64(len([]rune(m.Content)))
		for _, tc := range m.ToolCalls {
			n += int64(len([]rune(tc.Arguments)))
		}
	}
	return n
}

// loadDirectorHistory 读取 Director 自己的跨回合原生消息链，与 Dramaturg/Writer 的
// 历史读取方式一致：优先查库避免 gctx 携带的是过期快照，查库失败时回落 gctx 自带的数据。
func loadDirectorHistory(gctx GameContext) []models.DirectorTurn {
	var session models.GameSession
	if err := models.DB.Select("id", "director_history").First(&session, gctx.Session.ID).Error; err == nil {
		return session.DirectorHistory.Data
	}
	return gctx.Session.DirectorHistory.Data
}

func saveDirectorHistory(sessionID uint, turns []models.DirectorTurn) {
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", sessionID).
		Update("director_history", models.JSONField[[]models.DirectorTurn]{Data: turns})
}

// flattenDirectorTurns 把按回合切分的历史展开成一条连续的 llm.ChatMessage 序列，
// 供拼进本轮请求的 msgs。
func flattenDirectorTurns(turns []models.DirectorTurn) []llm.ChatMessage {
	total := 0
	for _, t := range turns {
		total += len(t.Messages)
	}
	out := make([]llm.ChatMessage, 0, total)
	for _, t := range turns {
		for _, m := range t.Messages {
			out = append(out, directorMsgToLLM(m))
		}
	}
	return out
}

func directorMsgToLLM(m models.DirectorMsg) llm.ChatMessage {
	msg := llm.ChatMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
	if len(m.ToolCalls) > 0 {
		msg.ToolCalls = make([]llm.ToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			msg.ToolCalls[i] = llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}
		}
	}
	return msg
}

// llmToDirectorMsgs 把一段 llm.ChatMessage 转换成归档用的 DirectorMsg，丢弃
// ReasoningBlocks/Reasoning——Anthropic 扩展思考签名和 OpenAI 兼容推理模型的
// reasoning_content 只在当轮工具循环内需要原样回传给 API，跨回合持久化没有意义，
// 反而会让存储膨胀。
func llmToDirectorMsgs(msgs []llm.ChatMessage) []models.DirectorMsg {
	out := make([]models.DirectorMsg, len(msgs))
	for i, m := range msgs {
		dm := models.DirectorMsg{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		if len(m.ToolCalls) > 0 {
			dm.ToolCalls = make([]models.DirectorToolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				dm.ToolCalls[j] = models.DirectorToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}
			}
		}
		out[i] = dm
	}
	return out
}

// trimDirectorTurns 从最旧的整轮开始批量丢弃，直到估算的总用量降到窗口的一半
// (低水位)，至少保留最近 1 轮；按回合(而不是按消息)裁剪保证不会拆散某一轮内部的
// tool_call 与对应 tool 结果的配对。tokensPerRune = usedTokens/totalRunes 是"当前
// 这次调用"的 token/字符密度，用于把只有字符数可数的历史回合换算成估算 token 数；
// usedTokens/totalRunes 应该是同一次调用的 directorEstimatedUsedTokens 与
// chatMessagesRuneCount 结果——usage 缺失时二者相等，密度自然退化为 1，与
// "1 rune ≈ 1 token" 的兜底估算保持一致。
// 调用方应仅在 directorOverThreshold 判定超过阈值时才调用本函数；未超过阈值时
// estimated 不会跌破 lowWater，循环体不会执行，函数本身也可以安全地无条件调用。
func trimDirectorTurns(turns []models.DirectorTurn, window int64, usedTokens int64, totalRunes int64) ([]models.DirectorTurn, int) {
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
		estimated -= float64(directorTurnRunes(turns[dropped])) * tokensPerRune
		dropped++
	}
	if dropped == 0 {
		return turns, 0
	}
	return turns[dropped:], dropped
}
