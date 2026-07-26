// NOTE: 原生 tool calling 的流式响应聚合。OpenAI 流式协议中，一次工具调用会被拆成多个
// chunk：id/type/function.name 只在该 index 首次出现的 chunk 里给出，之后的 chunk 只携带
// function.arguments 的增量分片，需要按 index 分组累加后再反序列化为完整参数。
package llm

import (
	"log"

	openai "github.com/sashabaranov/go-openai"
)

// toolCallAggregator 把流式 chunk 中的 Delta.ToolCalls 分片按 index 分组累加，
// 还原出完整的工具调用列表。
type toolCallAggregator struct {
	order   []int
	byIdx   map[int]*ToolCall
	lastIdx int
	hasLast bool
	// nextSynIdx 用于 Index 字段缺失时的退化路径（部分兼容端点在单工具调用场景省略
	// Index）；取负数索引，保证不与真实 index（恒 >= 0）冲突。
	nextSynIdx int
}

func newToolCallAggregator() *toolCallAggregator {
	return &toolCallAggregator{byIdx: make(map[int]*ToolCall), nextSynIdx: -1}
}

// add 消费一个 chunk 里的工具调用分片。
func (a *toolCallAggregator) add(deltas []openai.ToolCall) {
	for _, d := range deltas {
		idx := a.resolveIndex(d)
		entry, exists := a.byIdx[idx]
		if !exists {
			entry = &ToolCall{}
			a.byIdx[idx] = entry
			a.order = append(a.order, idx)
		}
		// id/name 只在非空时写入：同一工具调用的后续分片这两个字段通常为空，
		// 不能用空值覆盖首个分片已写入的值。
		if d.ID != "" {
			entry.ID = d.ID
		}
		if d.Function.Name != "" {
			entry.Name = d.Function.Name
		}
		// arguments 是分片字段，必须逐片追加而非覆盖。
		entry.Arguments += d.Function.Arguments
		a.lastIdx = idx
		a.hasLast = true
	}
}

// resolveIndex 返回该分片归属的聚合索引。Index 字段缺失时退化为按 ID 是否变化判断：
// 同一工具调用的后续分片 ID 通常为空（并入上一条），出现新的非空 ID 则视为新工具调用。
func (a *toolCallAggregator) resolveIndex(d openai.ToolCall) int {
	if d.Index != nil {
		return *d.Index
	}
	if !a.hasLast {
		idx := a.nextSynIdx
		a.nextSynIdx--
		return idx
	}
	if d.ID != "" {
		if last, ok := a.byIdx[a.lastIdx]; ok && last.ID != "" && last.ID != d.ID {
			idx := a.nextSynIdx
			a.nextSynIdx--
			return idx
		}
	}
	return a.lastIdx
}

// finish 按分片首次出现的顺序返回聚合完成的工具调用列表；丢弃 name 始终为空的残缺条目
// （通常意味着上游提前中断或协议不兼容）。
func (a *toolCallAggregator) finish() []ToolCall {
	result := make([]ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		entry := a.byIdx[idx]
		if entry.Name == "" {
			log.Printf("[llm] tool call aggregator: dropping incomplete entry with empty name (id=%q args_len=%d)", entry.ID, len(entry.Arguments))
			continue
		}
		result = append(result, *entry)
	}
	return result
}
