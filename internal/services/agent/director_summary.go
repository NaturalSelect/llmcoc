// NOTE: director_summary.go 实现 Director 历史唯一的压缩手段：把较早的发布单元连同已有总结，用 Director
// 自己的 provider 总结成一个 Kind="summary" 的 turn 放在最前面。历史单元本身从不被确定性地剥离或丢弃，
// 因为单元里有已经生效的结算。切点只落在"拍"的边界上（两条 kp/ask 消息之间），当前周期的单元永远不被总结。
// 总结输入是确定性渲染的纯文本，不发送原生工具消息。
package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const summaryMaxAttempts = 3

// ErrContextFull 表示 floor 进行中，总结之后上下文仍然超过窗口阈值（当前周期本身过长，或总结失败），
// 本次发言需要先结束，下一个周期开始时再总结后续跑。
var ErrContextFull = errors.New("director context is full")

const directorSummaryPrompt = `你是跑团（COC7）KP 的记录员。下面是 KP 在此前若干个发言权周期里做过的事：玩家行动、玩家读到的正文和 KP 的叙事方向、对玩家的回复、点名、掷骰和结算结果。
请把它们总结成一份供 KP 之后继续主持时参考的"故事至今"，必须保留：
1. 事件时间线（按发生顺序，写清谁在什么地点做了什么）；
2. 调查员已经获得的线索和已知信息；
3. NPC 的状态、位置与对调查员的态度；
4. 已经结算的数值和物品变化（生命、理智、物品、时间、关系等）；
5. 尚未解决的悬念、KP 做出的承诺和已经公开的约定；
6. 当前场景、位置，以及最近一次点名的问题。
只写原文里出现过的事实，不要编造，不要加入评价。已有的"故事至今"是更早的总结，要把它与新内容合并成一份，而不是简单拼接。
输出必须放在 <summary> 与 </summary> 标签之间，标签之外不要写任何内容。`

var (
	summaryTagPattern = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
	playerTurnPattern = regexp.MustCompile(`(?s)<player_turn[^>]*>.*?</player_turn>`)
)

// summarizeFunc 把渲染好的纯文本交给 LLM，返回总结正文（已去掉标签）。
type summarizeFunc func(ctx context.Context, input string) (string, error)

// directorSummarizer 返回用 Director 自己的 provider 做总结的实现。
func directorSummarizer(h agentHandle, sessionID uint) summarizeFunc {
	return func(ctx context.Context, input string) (string, error) {
		if h.provider == nil {
			return "", errors.New("director provider unavailable")
		}
		msgs := []llm.ChatMessage{
			{Role: "system", Content: directorSummaryPrompt},
			{Role: "user", Content: input},
		}
		var lastErr error
		for attempt := 1; attempt <= summaryMaxAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			out, err := h.provider.Chat(ctx, h.cacheKey(fmt.Sprint(sessionID))+":summary", msgs)
			if err != nil {
				lastErr = err
				continue
			}
			m := summaryTagPattern.FindStringSubmatch(out)
			if m == nil || strings.TrimSpace(m[1]) == "" {
				lastErr = errors.New("summary missing <summary> block")
				continue
			}
			return strings.TrimSpace(m[1]), nil
		}
		return "", lastErr
	}
}

// renderFloorsForSummary 把一批 turn 确定性渲染成纯文本：已有总结原样放在最前面；其余按 floor 分组。
// 每个单元只取 <player_turn> 块（里面已经包含 <kp_floor>/<asked>/玩家输入），不取 opening 的第二条
// system-reminder、防循环提醒和旧数据里的 <trimmed_tool_calls>；没有任何工具调用的种子轮原样放入。
func renderFloorsForSummary(turns []models.TranscriptTurn) string {
	var b strings.Builder
	curFloor := -1
	for _, t := range turns {
		if t.Kind == "summary" {
			for _, m := range t.Messages {
				b.WriteString(m.Content)
				b.WriteString("\n\n")
			}
			continue
		}
		if t.Floor != curFloor {
			curFloor = t.Floor
			fmt.Fprintf(&b, "## 发言权周期 %d\n", t.Floor)
		}
		seed := !turnHasToolCalls(t)
		for _, m := range t.Messages {
			if m.Role != "user" {
				continue
			}
			if seed {
				b.WriteString(m.Content)
				b.WriteString("\n")
				continue
			}
			if block := playerTurnPattern.FindString(m.Content); block != "" {
				b.WriteString(block)
				b.WriteString("\n")
			}
		}
		b.WriteString(renderUnitOutputs(t.Messages))
	}
	return strings.TrimSpace(b.String())
}

// renderUnitOutputs 渲染一个单元里产生玩家可见消息的调用、掷骰与 NPC 反应，以及同一批的状态更新结果。
// 被拒绝的调用不采纳；check_rule/query_*/consult_dramaturg 只是查询，不渲染。
func renderUnitOutputs(msgs []models.TranscriptMsg) string {
	results := make(map[string]string, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" {
			results[m.ToolCallID] = m.Content
		}
	}
	var b strings.Builder
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if isToolRejected(results[tc.ID]) {
				continue
			}
			call := llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}
			parsed, err := decodeDirectorToolCall(call)
			prose, hasProse := writerOutputOf(results[tc.ID])
			res := strings.TrimSpace(toolResultBody(results[tc.ID]))
			// NOTE: 正文是玩家实际读到的内容，比 direction 更接近事实；自动补写的正文挂在来源 narrate/ask 的结果上。
			if hasProse {
				if i := strings.Index(res, writerOutputOpen); i >= 0 {
					res = strings.TrimSpace(res[:i])
				}
			}
			switch ToolCallType(tc.Name) {
			case ToolWrite:
				switch {
				case hasProse:
					fmt.Fprintf(&b, "[正文] %s\n", prose)
				case err == nil && parsed.Direction != "":
					fmt.Fprintf(&b, "[叙事方向] %s\n", parsed.Direction)
				}
			case ToolNarrate, ToolLegacyResponse:
				if hasProse {
					fmt.Fprintf(&b, "[正文] %s\n", prose)
				}
				if err == nil && parsed.Reply != "" {
					fmt.Fprintf(&b, "[KP回复] %s\n", parsed.Reply)
				}
			case ToolAskPlayers:
				if hasProse {
					fmt.Fprintf(&b, "[正文] %s\n", prose)
				}
				if err == nil {
					fmt.Fprintf(&b, "[点名 %s] %s\n", strings.Join(parsed.Targets, "、"), parsed.Question)
				}
			case ToolEndGame:
				if err == nil && parsed.EndSummary != "" {
					fmt.Fprintf(&b, "[结局] %s\n", parsed.EndSummary)
				}
			case ToolRollDice:
				if res != "" {
					fmt.Fprintf(&b, "[掷骰] %s\n", res)
				}
			case ToolActNPC:
				if err == nil && res != "" {
					fmt.Fprintf(&b, "[NPC %s] %s\n", parsed.NPCName, res)
				}
			default:
				if res != "" && directorSideEffect(ToolCallType(tc.Name)) {
					fmt.Fprintf(&b, "[结算 %s] %s\n", tc.Name, res)
				}
			}
		}
	}
	return b.String()
}

// turnHasToolCalls 报告单元里是否有带工具调用的 assistant 消息；没有的是老会话播种的纯文本历史。
func turnHasToolCalls(t models.TranscriptTurn) bool {
	for _, m := range t.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// beatBoundaryAt 报告在 turns[:b] 与 turns[b:] 之间切开是否落在"拍"的边界上（1<=b<=len(turns)）。
// 一拍是两条 kp/ask 消息之间的所有单元，总结只能整拍整拍地处理，否则同一拍的 write 与 narrate 会被拆开。
func beatBoundaryAt(turns []models.TranscriptTurn, b int) bool {
	if b < 1 || b > len(turns) {
		return false
	}
	prev := turns[b-1]
	if b == len(turns) || prev.Kind == "summary" || !turnHasToolCalls(prev) || turns[b].Floor != prev.Floor {
		return true
	}
	return turnClosesBeat(prev)
}

// turnClosesBeat 报告单元里是否有未被拒绝的 narrate/ask_players/end_game，即这个单元收尾了一拍。
func turnClosesBeat(t models.TranscriptTurn) bool {
	results := make(map[string]string)
	for _, m := range t.Messages {
		if m.Role == "tool" {
			results[m.ToolCallID] = m.Content
		}
	}
	for _, m := range t.Messages {
		for _, tc := range m.ToolCalls {
			switch ToolCallType(tc.Name) {
			case ToolNarrate, ToolLegacyResponse, ToolAskPlayers, ToolEndGame:
				if !isToolRejected(results[tc.ID]) {
					return true
				}
			}
		}
	}
	return false
}

// compactHistory 把 c.data.Turns[:b] 总结成一个 summary 单元，b 是 (0,limit] 内的拍边界：
// 优先取"保留的部分 + fixedTokens 不超过窗口一半"的最小 b（低水位，与 trimTranscriptTurns 一致），
// 没有满足的就取最大的边界。tokensPerRune 把保留部分的字符数折算成 token，fixedTokens 是 head 和
// 未落库尾巴的估算量。没有可切的边界、或可总结的部分只有一个旧总结时什么都不做。
func (c *ContextManager) compactHistory(ctx context.Context, summarize summarizeFunc, limit int, tokensPerRune float64, fixedTokens int64) (bool, error) {
	if summarize == nil || c.opts.Window <= 0 {
		return false, nil
	}
	limit = min(limit, len(c.data.Turns))
	lowWater := float64(c.opts.Window) / 2

	cut := 0
	for b := 1; b <= limit; b++ {
		if !beatBoundaryAt(c.data.Turns, b) {
			continue
		}
		cut = b
		var keptRunes int64
		for _, t := range c.data.Turns[b:] {
			keptRunes += transcriptTurnRunes(t)
		}
		if float64(fixedTokens)+float64(keptRunes)*tokensPerRune <= lowWater {
			break
		}
	}
	if cut == 0 || (cut == 1 && c.data.Turns[0].Kind == "summary") {
		return false, nil
	}

	text, err := summarize(ctx, renderFloorsForSummary(c.data.Turns[:cut]))
	if err != nil {
		return false, err
	}
	first, last := summaryFloorRange(c.data.Turns[:cut])
	summary := models.TranscriptTurn{
		Seq:  c.data.Turns[0].Seq,
		Kind: "summary",
		Messages: []models.TranscriptMsg{{
			Role:    "user",
			Content: fmt.Sprintf("<story_so_far floors=\"%d-%d\">\n%s\n</story_so_far>", first, last, text),
		}},
		Compacted: true,
	}
	rest := append([]models.TranscriptTurn(nil), c.data.Turns[cut:]...)
	c.data.Turns = append([]models.TranscriptTurn{summary}, rest...)
	c.stat.CompactedTurns += cut
	c.save()
	return true, nil
}

// MaybeCompact 在新周期的 Build 之前调用：估算用量超过阈值时，把较早的拍总结掉，直到估算降到窗口的一半。
// Window<=0 时永不触发。总结失败只记日志并保持原样。返回是否替换了历史。
func (c *ContextManager) MaybeCompact(ctx context.Context, summarize summarizeFunc) bool {
	window := c.opts.Window
	if window <= 0 || summarize == nil {
		return false
	}
	used := c.estimatedHistoryTokens()
	if !contextOverThreshold(window, used) {
		return false
	}
	headRunes := chatMessagesRuneCount(c.head)
	tokensPerRune := 1.0
	if total := c.historyRunes() + headRunes; total > 0 {
		tokensPerRune = float64(used) / float64(total)
	}
	changed, err := c.compactHistory(ctx, summarize, len(c.data.Turns), tokensPerRune, int64(float64(headRunes)*tokensPerRune))
	if err != nil {
		alog.Error("director summary failed", "session", c.sessionID, "err", err)
		return false
	}
	if changed {
		c.resetLastContextTokens(tokensPerRune, headRunes)
	}
	return changed
}

// CompactInFloor 在周期进行中上下文已满时调用：只总结本周期之前的拍，本周期的单元和未落库的尾巴原样保留，
// 返回按新历史重建的 msgs。usage 是刚完成的那次调用的用量，用来估算 token 密度。
// 没有可总结的、总结失败、或重建后估算值仍然超过阈值时 stillFull 为 true，调用方应结束本周期。
func (c *ContextManager) CompactInFloor(ctx context.Context, summarize summarizeFunc, msgs []llm.ChatMessage, usage llm.Usage) (out []llm.ChatMessage, stillFull bool) {
	window := c.opts.Window
	if window <= 0 || summarize == nil {
		return msgs, true
	}
	totalRunes := chatMessagesRuneCount(msgs)
	tokensPerRune := 1.0
	if totalRunes > 0 {
		tokensPerRune = float64(contextEstimatedUsedTokens(usage, msgs)) / float64(totalRunes)
	}
	var fixedRunes int64
	fixedRunes += chatMessagesRuneCount(msgs[:c.headLen])
	fixedRunes += chatMessagesRuneCount(msgs[c.turnStart:])

	changed, err := c.compactHistory(ctx, summarize, c.currentFloorStart(), tokensPerRune, int64(float64(fixedRunes)*tokensPerRune))
	if err != nil {
		alog.Error("director in-floor summary failed", "session", c.sessionID, "err", err)
		return msgs, true
	}
	if !changed {
		return msgs, true
	}
	// NOTE: 总结只替换前缀，本周期已落库的单元仍在 Turns 末尾，所以 historyTurns 就是 Turns 的长度。
	c.historyTurns = len(c.data.Turns)
	out = c.rebuild(msgs)
	c.resetLastContextTokens(tokensPerRune, 0)
	return out, contextOverThreshold(window, int64(float64(chatMessagesRuneCount(out))*tokensPerRune))
}

// resetLastContextTokens 用总结后的估算值覆盖最近一条 Stats 的 LastContextTokens，否则下一个周期开始时
// estimatedHistoryTokens 会读到总结前的旧值而重复触发总结。
func (c *ContextManager) resetLastContextTokens(tokensPerRune float64, headRunes int64) {
	n := len(c.data.Stats)
	if n == 0 {
		return
	}
	est := int64(float64(c.historyRunes()+headRunes) * tokensPerRune)
	c.data.Stats[n-1].LastContextTokens = est
	c.save()
}

func summaryFloorRange(turns []models.TranscriptTurn) (first, last int) {
	first = -1
	for _, t := range turns {
		if t.Kind == "summary" {
			continue
		}
		if first < 0 || t.Floor < first {
			first = t.Floor
		}
		if t.Floor > last {
			last = t.Floor
		}
	}
	if first < 0 {
		first = 0
	}
	return first, last
}

// historyRunes 统计已提交历史的总字符数。
func (c *ContextManager) historyRunes() int64 {
	var n int64
	for _, t := range c.data.Turns {
		n += transcriptTurnRunes(t)
	}
	return n
}

// estimatedHistoryTokens 估算下一次调用的上下文用量：优先取最近一条 Stats 的 prompt+output，
// 取不到（网关无 usage 或还没有记录）时退回历史加 head 的字符数。
func (c *ContextManager) estimatedHistoryTokens() int64 {
	if n := len(c.data.Stats); n > 0 {
		if s := c.data.Stats[n-1]; s.LastContextTokens > 0 {
			return s.LastContextTokens
		}
	}
	return c.historyRunes() + chatMessagesRuneCount(c.head)
}
