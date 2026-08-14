// scripter_conversation.go — 消息链复用原语：把一次 Scripter 生成/修复调用积累的
// 完整对话历史（system/user/assistant/tool 全部消息）在同一 agentHandle、同一职责
// 角色的连续调用之间保留下来，供 QA 修复轮次续接同一条对话而不是每轮重建，
// 详见 repairStoryDocument / repairOneshotDraft。
package agent

import (
	"context"

	"github.com/llmcoc/server/internal/services/llm"
)

// scripterConversation 持有一条可能跨越多次生成/修复调用的消息链。draftIdxs 记录
// 历次"成稿"（story 文档全文 / oneshot JSON 全文）在 msgs 中的下标，供
// supersedePriorDrafts 把除最新一版外的历史正文原地替换为占位符，避免每轮修复都把
// 整份历史正文重新叠进上下文。logged 是 recordScripterLLMExchange 已经写入生成日志
// 的消息前缀长度，避免同一条链被多次调用复用时把历史消息重复记入生成日志。
type scripterConversation struct {
	msgs      []llm.ChatMessage
	logged    int
	draftIdxs []int
}

// newScripterConversation 以给定的初始消息（通常是 system+user）建立一条新链。
func newScripterConversation(msgs ...llm.ChatMessage) *scripterConversation {
	return &scripterConversation{msgs: append([]llm.ChatMessage(nil), msgs...)}
}

// append 把消息追加到链尾。
func (c *scripterConversation) append(msgs ...llm.ChatMessage) {
	c.msgs = append(c.msgs, msgs...)
}

// markDraft 把当前链尾（最新一条消息，通常是刚提交成功的成稿正文）记为一次成稿。
func (c *scripterConversation) markDraft() {
	if len(c.msgs) == 0 {
		return
	}
	c.draftIdxs = append(c.draftIdxs, len(c.msgs)-1)
}

// scripterDraftSupersededPlaceholder 替换历史成稿正文时使用的占位符。
const scripterDraftSupersededPlaceholder = "（本版正文已被后续修订取代，此处省略；以对话中最新一版完整正文为准）"

// supersedePriorDrafts 把 draftIdxs 中除最后一个之外的历史成稿正文原地替换为占位符，
// 保证链里任意时刻只有一份完整正文，避免多轮修复后上下文里堆叠多份历史全文。
func (c *scripterConversation) supersedePriorDrafts() {
	if len(c.draftIdxs) <= 1 {
		return
	}
	for _, idx := range c.draftIdxs[:len(c.draftIdxs)-1] {
		if idx < 0 || idx >= len(c.msgs) {
			continue
		}
		c.msgs[idx].Content = scripterDraftSupersededPlaceholder
	}
}

// runeLen 统计链中全部消息内容（含工具调用参数）的字符数，用于判断链是否超过复用
// 上限、需要降级为重建一条新链。
func (c *scripterConversation) runeLen() int {
	total := 0
	for _, m := range c.msgs {
		total += len([]rune(m.Content))
		for _, tc := range m.ToolCalls {
			total += len([]rune(tc.Arguments))
		}
	}
	return total
}

// reset 丢弃当前链的全部历史，以给定消息重新开始；用于链为空或超过长度上限时降级为
// 重建一条新链（即改造前的行为）。
func (c *scripterConversation) reset(msgs ...llm.ChatMessage) {
	c.msgs = append([]llm.ChatMessage(nil), msgs...)
	c.logged = 0
	c.draftIdxs = nil
}

// branch 返回一条以当前链内容为起点的独立分支：分支上的 append 不会影响原链，原链
// 后续的 append 也不会因为共享底层数组容量而覆盖分支已经写入的内容——必须使用三
// 索引切片表达式（full slice expression）把分支的容量钉死在当前长度，否则两条链会
// 共享同一底层数组产生别名，互相覆盖对方后续追加的内容。
func (c *scripterConversation) branch() *scripterConversation {
	n := len(c.msgs)
	return &scripterConversation{msgs: c.msgs[:n:n], logged: c.logged}
}

// record 把本轮请求（链中相对上次记录新增的消息）与响应写入生成日志，并推进 logged
// 游标；供不经过 runToolLoop 的纯 JsonChat 调用点（如 runOneshotSubmitPhase）复用与
// runToolLoop 一致的"只记新增消息"语义。
func (c *scripterConversation) record(ctx context.Context, room *scripterRoom, stage string, response string) {
	newMessages := append([]llm.ChatMessage(nil), c.msgs[c.logged:]...)
	recordScripterLLMExchange(ctx, room, stage, newMessages, response)
	c.logged = len(c.msgs)
}
