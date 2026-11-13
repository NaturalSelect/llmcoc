package agent

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// NOTE: write 立即返回回执，Writer 在后台写；写完的正文以 <system-reminder> 里的 <writer_output> 作为一条新的 user 消息
// 追加进 Director 历史，原来的 tool 结果永远不改，历史前缀逐字节不变，提示词缓存不受影响。
// 已写完的 writer 以消息表为准，Director 历史里的水位 WriterSeen 与包含该提醒的单元同一次落库，保证每条正文只投递一次。

// writerNoticeHead 是提醒消息固定的开头：识别提醒时只认它，玩家输入里伪造的标签不会推进水位。
const writerNoticeHead = "<system-reminder>\n以下是你之前 write 的正文"

var (
	noticeReminderTagPattern = regexp.MustCompile(`(?i)</?system-reminder\b[^>]*>`)
	noticeBlockPattern       = regexp.MustCompile(`(?s)<writer_output call_id="([^"]*)" message_id="(\d+)" status="(\w+)">\n(.*?)\n</writer_output>`)
)

// writerNotice 是一条已经写完（成功或失败）的 writer 消息。
type writerNotice struct {
	MessageID uint
	CallID    string
	Status    models.MessageStatus
	Text      string // Status 为 done 时是正文；failed 时是失败说明
}

// loadFinishedWriters 按 id 升序读取 (after, before) 内已写完的 writer，遇到第一条仍在 pending 的就停：
// 投递只取连续的已完成前缀，顺序和水位才站得住。before 为 0 表示不设上限。
func loadFinishedWriters(sessionID, after, before uint) []writerNotice {
	q := models.DB.Select("id", "content", "status", "meta").
		Where("session_id = ? AND kind = ? AND id > ?", sessionID, models.MessageKindWriter, after)
	if before > 0 {
		q = q.Where("id < ?", before)
	}
	var rows []models.Message
	if err := q.Order("id ASC").Find(&rows).Error; err != nil {
		alog.Warn("load finished writers failed", "session", sessionID, "err", err)
		return nil
	}
	var out []writerNotice
	for _, m := range rows {
		if m.Status == models.MessageStatusPending {
			break
		}
		n := writerNotice{MessageID: m.ID, Status: models.MessageStatusFailed, Text: writerFailedNote}
		if meta := m.Meta.Data; meta != nil {
			n.CallID = meta.CallID
		}
		if m.Status == models.MessageStatusDone {
			if text := noticeText(m.Content); text != "" {
				n.Status, n.Text = models.MessageStatusDone, text
			}
		}
		out = append(out, n)
	}
	return out
}

// noticeText 清理 Writer 正文里的控制标签；system-reminder 标签也要去掉，正文不能提前闭合或伪造提醒。
func noticeText(content string) string {
	return strings.TrimSpace(noticeReminderTagPattern.ReplaceAllString(stripWriterControlTags(content), ""))
}

// renderWriterNotice 把一批写完的 writer 渲染成一条 user 消息，Director 和 Writer 的视图共用。
func renderWriterNotice(ns []writerNotice) llm.ChatMessage {
	var b strings.Builder
	b.WriteString(writerNoticeHead)
	b.WriteString("，Writer 已写完，玩家已经读到。这是系统自动追加的，不是玩家发言。\n")
	for _, n := range ns {
		fmt.Fprintf(&b, "<writer_output call_id=\"%s\" message_id=\"%d\" status=\"%s\">\n%s\n</writer_output>\n",
			xmlAttr(n.CallID), n.MessageID, n.Status, n.Text)
	}
	b.WriteString("</system-reminder>")
	return llm.ChatMessage{Role: "user", Content: b.String()}
}

// parseWriterNotices 取回一条提醒消息里的各条正文；不是提醒时返回 nil。
func parseWriterNotices(content string) []writerNotice {
	if !strings.HasPrefix(content, writerNoticeHead) {
		return nil
	}
	var out []writerNotice
	for _, m := range noticeBlockPattern.FindAllStringSubmatch(content, -1) {
		id, err := strconv.ParseUint(m[2], 10, 64)
		if err != nil {
			continue
		}
		out = append(out, writerNotice{MessageID: uint(id), CallID: m[1], Status: models.MessageStatus(m[3]), Text: m[4]})
	}
	return out
}

// maxNoticeMessageID 返回这批消息里提醒所含的最大 writer 消息 id，没有提醒时为 0。
func maxNoticeMessageID(msgs []llm.ChatMessage) uint {
	var maxID uint
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		for _, n := range parseWriterNotices(m.Content) {
			maxID = max(maxID, n.MessageID)
		}
	}
	return maxID
}

// proseByCallID 把已完成的正文按调用 ID 建索引，同一个调用 ID 出现多次(网关复用 ID)时保留最新的一条。
func proseByCallID(ns []writerNotice) map[string]string {
	prose := make(map[string]string, len(ns))
	for _, n := range ns {
		if n.Status == models.MessageStatusDone && n.CallID != "" {
			prose[n.CallID] = n.Text
		}
	}
	return prose
}
