package agent

import (
	"github.com/llmcoc/server/internal/models"
)

// NOTE: Writer 的正文随 write 的 tool 结果进入 Director 历史。周期被打断时没等到的 Writer、
// 以及这个机制上线前的旧会话，transcript 里的 tool 结果没有正文；每个周期开始前按调用 ID
// 从消息表补上，之后 Writer / NPC 的视图只需要原样复用 Director 的消息链。

// loadWriterProse 读取本会话已完成、带调用 ID 的 writer 消息，按调用 ID 建索引。
// 同一个调用 ID 出现多次(网关复用 ID)时保留最新的一条。
func loadWriterProse(sessionID uint) map[string]string {
	var rows []models.Message
	if err := models.DB.
		Select("id", "content", "meta").
		Where("session_id = ? AND kind = ? AND status = ?", sessionID, models.MessageKindWriter, models.MessageStatusDone).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		alog.Warn("load writer prose failed", "session", sessionID, "err", err)
		return nil
	}
	prose := make(map[string]string, len(rows))
	for _, m := range rows {
		meta := m.Meta.Data
		if meta == nil || meta.CallID == "" {
			continue
		}
		text := stripWriterControlTags(m.Content)
		if text == "" {
			continue
		}
		prose[meta.CallID] = text
	}
	return prose
}

// backfillWriterProse 把 prose 里的正文补进 transcript 中还没有正文的 tool 结果，有改动就落库，返回是否改动。
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
	if changed {
		c.save()
	}
	return changed
}
