// NOTE: message_render.go 把按 Kind 拆细后的 Message 渲染成喂给 LLM 的纯文本：KP 一次工具调用对应一条消息，
// 正文里不再有 "KP:" 前缀与内嵌的 options/ack/dice 标签，结构化信息在 Meta 里，这里统一加回可读标注。
package agent

import (
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
)

// endGameLogCharBudget 是结算类 LLM 读取聊天记录时的字符预算：消息拆细后条数不再代表剧情量，改按字符截取最近部分。
const endGameLogCharBudget = 120_000

// messageSpeaker 返回一条消息在日志里的说话者标注。image 消息没有可读内容，调用方应先用 renderableMessage 过滤。
func messageSpeaker(m models.Message) string {
	switch m.Kind {
	case models.MessageKindPlayer:
		return playerSpeaker(m)
	case models.MessageKindWriter:
		return "正文"
	case models.MessageKindKP:
		return "KP"
	case models.MessageKindAsk:
		return "KP点名"
	case models.MessageKindEvent:
		return "骰子"
	}
	if m.Role == models.MessageRoleUser {
		return playerSpeaker(m)
	}
	return "KP"
}

func playerSpeaker(m models.Message) string {
	if m.Username != "" {
		return m.Username
	}
	return "玩家"
}

// renderableMessage 报告一条消息是否应该进入给 LLM 的日志：配图没有文字，失败或还没写完的 writer 没有正文，
// event 消息只有含公开骰时才渲染(属性等状态本来就在提示词里)。
func renderableMessage(m models.Message) bool {
	if m.Kind == models.MessageKindEvent {
		return publicDiceLine(m) != ""
	}
	if m.Kind == models.MessageKindImage {
		return false
	}
	if m.Kind == models.MessageKindWriter && (m.Status == models.MessageStatusFailed || strings.TrimSpace(m.Content) == "") {
		return false
	}
	return strings.TrimSpace(m.Content) != ""
}

// renderMessageLine 渲染一条消息为 "[说话者]: 内容"。event 消息只渲染公开骰；旧 kp/ask 消息的骰子结果附在末尾，
// 让结算类 LLM 看得到公开检定。
func renderMessageLine(m models.Message) string {
	if m.Kind == models.MessageKindEvent {
		return fmt.Sprintf("[%s]: %s", messageSpeaker(m), publicDiceLine(m))
	}
	line := fmt.Sprintf("[%s]: %s", messageSpeaker(m), strings.TrimSpace(m.Content))
	if meta := m.Meta.Data; meta != nil && meta.Dice != "" {
		line += fmt.Sprintf(" (骰子: %s)", meta.Dice)
	}
	return line
}

// publicDiceLine 把 event 消息里的公开骰拼成一行，没有公开骰时返回空串。
func publicDiceLine(m models.Message) string {
	meta := m.Meta.Data
	if meta == nil {
		return ""
	}
	var parts []string
	for _, ev := range meta.Events {
		if ev.Type != "dice" || ev.Hidden {
			continue
		}
		s := strings.TrimSpace(formatSingleDiceResult(DiceCheckResult{DiceCheck: DiceCheck{What: ev.Skill, Level: ev.Level}, Roll: ev.Roll, Target: ev.Target, Result: ev.Result, Detail: ev.Detail}))
		if ev.Character != "" {
			s = ev.Character + " " + s
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// BuildSessionChatLog 把消息列表渲染成逐行聊天记录，跳过配图与没有正文的消息。
func BuildSessionChatLog(messages []models.Message) string {
	var sb strings.Builder
	for _, m := range messages {
		if !renderableMessage(m) {
			continue
		}
		sb.WriteString(renderMessageLine(m))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// LoadEndGameMessages 读取结算用的聊天记录：从最新往前累计字符，超过预算就停，再恢复成时间顺序。
func LoadEndGameMessages(sessionID uint) []models.Message {
	var recent []models.Message
	models.DB.Where("session_id = ? AND role != ?", sessionID, models.MessageRoleSystem).
		Order("created_at DESC, id DESC").
		Limit(2000).
		Find(&recent)

	used, keep := 0, 0
	for _, m := range recent {
		used += len([]rune(m.Content))
		if used > endGameLogCharBudget && keep > 0 {
			break
		}
		keep++
	}
	recent = recent[:keep]
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}
	return recent
}
