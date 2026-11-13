package agent

import (
	"github.com/llmcoc/server/internal/services/llm"
)

// NOTE: 新会话里 Writer 的正文以提醒消息追加进 Director 历史（见 writer_notice.go）。
// 这个机制上线前的旧会话，transcript 里的 write 结果没有正文；首次进入时一次性按调用 ID 补进旧的 tool 结果，
// 并把水位推到已写完的连续前缀的末尾，之后历史只追加、不再改写。

// initProseHistory 在 transcript 第一次被这个机制使用时做一次回填并初始化水位，返回是否改动了历史。
func (c *ContextManager) initProseHistory() bool {
	if c.data.ProseInHistory {
		return false
	}
	ns := loadFinishedWriters(c.sessionID, 0, 0)
	changed := c.backfillWriterProse(proseByCallID(ns))
	for _, n := range ns {
		c.data.WriterSeen = max(c.data.WriterSeen, n.MessageID)
	}
	c.data.ProseInHistory = true
	// NOTE: transcript 还是空的新会话不在这里落库（首轮硬失败不能留下记录），标志和水位随第一个单元一起保存；
	// 没保存之前周期就失败了，下次进来重新初始化，结果相同。
	if changed || len(c.data.Turns) > 0 {
		c.save()
	}
	return changed
}

// backfillWriterProse 把 prose 里的正文补进 transcript 中还没有正文的 tool 结果，返回是否改动；不落库，由调用方统一保存。
// summary 单元已经是总结文本，不动；被拒绝的结果、已经带正文的结果由 attachWriterOutput 跳过。
func (c *ContextManager) backfillWriterProse(prose map[string]string) bool {
	if len(prose) == 0 {
		return false
	}
	changed := false
	for ti := range c.data.Turns {
		turn := &c.data.Turns[ti]
		if turn.Kind == "summary" {
			continue
		}
		for mi := range turn.Messages {
			m := &turn.Messages[mi]
			if m.Role != "tool" || m.ToolCallID == "" {
				continue
			}
			text, ok := prose[m.ToolCallID]
			if !ok {
				continue
			}
			if next, attached := attachWriterOutput(m.Content, text); attached {
				m.Content = next
				changed = true
			}
		}
	}
	return changed
}

// advanceWriterSeen 让水位追上这批消息里的提醒；只增不减。
func (c *ContextManager) advanceWriterSeen(msgs []llm.ChatMessage) {
	c.data.WriterSeen = max(c.data.WriterSeen, maxNoticeMessageID(msgs))
}

// WriterSeen 返回当前水位，供周期开始时初始化投递进度。
func (c *ContextManager) WriterSeen() uint { return c.data.WriterSeen }
