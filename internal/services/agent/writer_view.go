package agent

import (
	"sort"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// NOTE: Writer 没有独立历史：它的请求 = Writer 自己的 system + Director 的消息链(原样) +
// 每个已完成 write 的 tool 结果后附上的正文 + 末尾一条续写指令。这里只负责组装视图，
// 视图是确定性生成的，不落库。

const (
	writerOutputOpen  = "\n<writer_output>\n"
	writerOutputClose = "\n</writer_output>"
	toolRejectPrefix  = "SYSTEM REJECT"
)

// writerProse 是一条已经生成完成的 Writer 正文，Floor 用于超窗口时按周期丢弃。
type writerProse struct {
	Floor int
	Text  string
}

// loadWriterProse 读取本会话已完成、带调用 ID 的 writer 消息，按调用 ID 建索引。
// 同一个调用 ID 出现多次(网关复用 ID)时保留最新的一条。
func loadWriterProse(sessionID uint) map[string]writerProse {
	var rows []models.Message
	if err := models.DB.
		Select("id", "floor", "content", "meta").
		Where("session_id = ? AND kind = ? AND status = ?", sessionID, models.MessageKindWriter, models.MessageStatusDone).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		alog.Warn("load writer prose failed", "session", sessionID, "err", err)
		return nil
	}
	prose := make(map[string]writerProse, len(rows))
	for _, m := range rows {
		meta := m.Meta.Data
		if meta == nil || meta.CallID == "" {
			continue
		}
		text := stripWriterControlTags(m.Content)
		if text == "" {
			continue
		}
		prose[meta.CallID] = writerProse{Floor: m.Floor, Text: text}
	}
	return prose
}

// buildWriterView 组装发给 Writer 的完整消息序列(不含工具定义)：
//  1. 逐条拷贝 director，第一条 system 的内容换成 Writer 的 system，其余字段(含 CacheBreakpoint)保留；
//  2. keepReasoning 为 false 时清掉推理内容，它们的签名绑定在 Director 的模型上；
//  3. 在对应 write 的 tool 结果后附 <writer_output> 正文，被拒绝的结果不附；
//  4. 估算超出 window 时，从最早的周期开始整周期去掉附带的正文，Director 的消息一条都不删；
//  5. 末尾追加 instruction。
//
// fixedRunes 是消息之外固定占用的字符数(工具定义等)，与 contextEstimatedUsedTokens 无 usage
// 时同口径，按 1 字约 1 token 估算。
func buildWriterView(director []llm.ChatMessage, system string, prose map[string]writerProse,
	keepReasoning bool, window int64, fixedRunes int64, instruction llm.ChatMessage) []llm.ChatMessage {

	msgs := make([]llm.ChatMessage, 0, len(director)+2)
	systemReplaced := false
	for _, m := range director {
		if m.Role == "system" && !systemReplaced {
			m.Content = system
			systemReplaced = true
		}
		if !keepReasoning {
			m.Reasoning = ""
			m.ReasoningBlocks = nil
		}
		msgs = append(msgs, m)
	}
	if !systemReplaced {
		msgs = append([]llm.ChatMessage{{Role: "system", Content: system}}, msgs...)
	}

	attached := attachableProse(msgs, prose)
	dropFloors := writerDropFloors(msgs, attached, window, fixedRunes+int64(len([]rune(instruction.Content))))
	for idx, p := range attached {
		if dropFloors[p.Floor] {
			continue
		}
		msgs[idx].Content += writerOutputOpen + p.Text + writerOutputClose
	}
	return append(msgs, instruction)
}

// attachableProse 返回「消息下标 -> 要附上的正文」：只有 Role 为 tool、结果没有被拒绝、
// 并且调用 ID 能在 prose 里找到的消息才会附。
func attachableProse(msgs []llm.ChatMessage, prose map[string]writerProse) map[int]writerProse {
	attached := map[int]writerProse{}
	if len(prose) == 0 {
		return attached
	}
	for i, m := range msgs {
		if m.Role != "tool" || m.ToolCallID == "" || strings.HasPrefix(m.Content, toolRejectPrefix) {
			continue
		}
		if p, ok := prose[m.ToolCallID]; ok {
			attached[i] = p
		}
	}
	return attached
}

// writerDropFloors 在估算超出窗口时，按周期从小到大返回需要不附正文的周期集合。
// 全部去掉后仍然超窗口时照常返回全部，由调用失败后的现有流程处理。
func writerDropFloors(msgs []llm.ChatMessage, attached map[int]writerProse, window, fixedRunes int64) map[int]bool {
	dropped := map[int]bool{}
	if len(attached) == 0 {
		return dropped
	}
	used := chatMessagesRuneCount(msgs) + fixedRunes
	floorRunes := map[int]int64{}
	for _, p := range attached {
		n := int64(len([]rune(writerOutputOpen + p.Text + writerOutputClose)))
		floorRunes[p.Floor] += n
		used += n
	}
	if !contextOverThreshold(window, used) {
		return dropped
	}
	floors := make([]int, 0, len(floorRunes))
	for f := range floorRunes {
		floors = append(floors, f)
	}
	sort.Ints(floors)
	for _, f := range floors {
		dropped[f] = true
		used -= floorRunes[f]
		if !contextOverThreshold(window, used) {
			break
		}
	}
	return dropped
}

// sameModelConfig 判断 Writer 与 Director 是否用同一个提供商配置和模型：
// 只有同模型时才能把 Director 的推理内容原样带给 Writer。
func sameModelConfig(a, b *models.AgentConfig) bool {
	if a == nil || b == nil || a.ProviderConfigID == nil || b.ProviderConfigID == nil {
		return false
	}
	return *a.ProviderConfigID == *b.ProviderConfigID && a.ModelName == b.ModelName
}
