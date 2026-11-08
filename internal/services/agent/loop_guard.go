// NOTE: loop_guard.go 是 Director 在不限轮数的 ReAct 循环里的防重复工具调用机制，思路参考
// angela 的 loop_detection（按「工具名+参数+结果」签名在滑动窗口里计数），本项目的落地差异：
//   - 判重粒度是「轮」（一批并发调用），签名对批内调用排序，与并发完成的先后顺序无关；
//   - 只读工具的签名带上结果，有副作用的工具只用参数（HP 等结果每次都不同，不能当作"有进展"）；
//   - 命中分两级：第 3 次追加提醒，第 5 次以 ErrLoopDetected 终止；
//   - 在真正执行前复用只读调用结果，并拒绝跨轮的重复结算，把循环挡在副作用之前。
package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

const (
	loopGuardWindow  = 10
	loopGuardWarnAt  = 3
	loopGuardAbortAt = 5
)

// ErrLoopDetected 表示同一批工具调用在滑动窗口内重复出现过多，本次发言被强制结束。
var ErrLoopDetected = errors.New("director repeated identical tool calls too many times")

// guardCacheable 是可以在「状态未变」时复用上次结果的只读工具。
// NOTE: roll_dice（结果随机）、act_npc（会写 NPC transcript）、generate_image、consult_dramaturg 永不复用。
var guardCacheable = map[ToolCallType]bool{
	ToolQueryClues:         true,
	ToolQueryCharacter:     true,
	ToolQueryNPCCard:       true,
	ToolDescribeCharacters: true,
	ToolCheckRule:          true,
}

// guardSigWithResult 是签名里需要带上结果的工具：它们的结果变化代表有新信息，不算空转。
var guardSigWithResult = map[ToolCallType]bool{
	ToolQueryClues:         true,
	ToolQueryCharacter:     true,
	ToolQueryNPCCard:       true,
	ToolDescribeCharacters: true,
	ToolCheckRule:          true,
	ToolActNPC:             true,
	ToolConsultDramaturg:   true,
}

// guardSettlement 是需要跨轮去重的结算类工具。
var guardSettlement = map[ToolCallType]bool{
	ToolUpdateCharacters: true,
	ToolManageInventory:  true,
}

type loopGuard struct {
	window  []string
	cache   map[string]string // callKey -> 上次成功结果
	settled map[string]bool   // 上次对玩家发言以来已成功执行过的结算调用
	confirm map[string]bool   // 已被拒绝过一次、再原样重发即放行的结算调用
	raw     map[string]string // 本轮 callID -> 工具原始结果（不含复用前缀）
}

func newLoopGuard() *loopGuard {
	return &loopGuard{
		cache:   map[string]string{},
		settled: map[string]bool{},
		confirm: map[string]bool{},
	}
}

// markMessagesProduced 在某一轮产生了玩家可见消息后调用：此后场景已经推进，
// 之前的只读结果可能过期，同样参数的结算也可能是合理的新事件。滑动窗口不清空。
func (g *loopGuard) markMessagesProduced() {
	g.cache = map[string]string{}
	g.settled = map[string]bool{}
	g.confirm = map[string]bool{}
}

// normalizeArgs 把 JSON 参数规范化（键序、空白无关）；解析失败时退回去空白的原文。
func normalizeArgs(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return raw
	}
	return strings.TrimSpace(buf.String())
}

func callKey(name, args string) string {
	return name + "\x00" + normalizeArgs(args)
}

// roundSignature 计算一轮调用的签名。forceResult=true（整批被拒）时所有调用都带上结果，
// 这样原样重发被拒的批次也会被计数。
func roundSignature(calls []llm.ToolCall, results map[string]string, forceResult bool) string {
	parts := make([]string, 0, len(calls))
	for _, c := range calls {
		key := callKey(c.Name, c.Arguments)
		res := results[c.ID]
		if forceResult || guardSigWithResult[ToolCallType(c.Name)] || strings.HasPrefix(res, "SYSTEM REJECT") {
			key += "\x00" + res
		}
		parts = append(parts, key)
	}
	sort.Strings(parts)
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0x01})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// toolResultsByID 从 msgs 末尾连续的 tool 消息里取出本轮每个调用的回执。
func toolResultsByID(msgs []llm.ChatMessage, calls []llm.ToolCall) map[string]string {
	want := make(map[string]bool, len(calls))
	for _, c := range calls {
		want[c.ID] = true
	}
	out := make(map[string]string, len(calls))
	for i := len(msgs) - 1; i >= 0 && msgs[i].Role == "tool"; i-- {
		if want[msgs[i].ToolCallID] {
			out[msgs[i].ToolCallID] = msgs[i].Content
		}
	}
	return out
}

const loopGuardCachedPrefix = "（与前面同参数调用的结果相同，未重新执行，期间状态没有变化）\n"

// directorSideEffect 判断一次成功执行是否可能改变游戏状态（从而让只读缓存失效）。
func directorSideEffect(action ToolCallType) bool {
	return !noSideEffectActions[action] && action != ToolReport
}

// wrapDispatch 在真正执行前处理三件事：复用只读调用结果、同批相同只读调用只执行一次、
// 拒绝跨轮重复结算。inner 只会收到需要真正执行的调用子集，返回值按原下标还原。
func (g *loopGuard) wrapDispatch(inner toolBatchDispatch) toolBatchDispatch {
	return func(ctx context.Context, calls []llm.ToolCall) []toolOutcome {
		g.raw = map[string]string{}
		outcomes := make([]toolOutcome, len(calls))
		var run []int
		firstIdx := map[string]int{}
		dupOf := map[int]int{}
		batchSettle := map[string]bool{}

		for i, c := range calls {
			action := ToolCallType(c.Name)
			key := callKey(c.Name, c.Arguments)
			switch {
			case guardCacheable[action]:
				if res, ok := g.cache[key]; ok {
					outcomes[i] = toolOutcome{result: loopGuardCachedPrefix + res}
					g.raw[c.ID] = res
					continue
				}
				if j, ok := firstIdx[key]; ok {
					dupOf[i] = j
					continue
				}
				firstIdx[key] = i
			case guardSettlement[action]:
				if g.settled[key] || batchSettle[key] {
					if g.confirm[key] {
						delete(g.confirm, key)
					} else {
						g.confirm[key] = true
						outcomes[i] = toolOutcome{reject: fmt.Sprintf(
							"SYSTEM REJECT: 本次 %s 的参数与你自上一次对玩家发言以来已经执行过的调用完全相同，疑似同一事件被结算了两次，本次未执行。若确实需要再结算一次，请原样再发一次相同的调用；否则请忽略。", c.Name)}
						continue
					}
				}
				batchSettle[key] = true
			}
			run = append(run, i)
		}

		if len(run) > 0 {
			sub := make([]llm.ToolCall, len(run))
			for k, i := range run {
				sub[k] = calls[i]
			}
			subOut := inner(ctx, sub)
			sideEffect := false
			for k, i := range run {
				o := toolOutcome{reject: "SYSTEM REJECT: 工具执行未返回结果，请重试。"}
				if k < len(subOut) {
					o = subOut[k]
				}
				outcomes[i] = o
				if o.reject != "" {
					continue
				}
				action := ToolCallType(calls[i].Name)
				g.raw[calls[i].ID] = o.result
				if directorSideEffect(action) {
					sideEffect = true
				}
				if guardSettlement[action] {
					g.settled[callKey(calls[i].Name, calls[i].Arguments)] = true
				}
			}
			if sideEffect {
				// NOTE: 同批里状态已被改写，本批的查询结果可能是改写前读到的，不能当作之后的复用来源。
				g.cache = map[string]string{}
			} else {
				for _, i := range run {
					c := calls[i]
					if guardCacheable[ToolCallType(c.Name)] && outcomes[i].reject == "" {
						g.cache[callKey(c.Name, c.Arguments)] = outcomes[i].result
					}
				}
			}
		}

		for i, j := range dupOf {
			outcomes[i] = outcomes[j]
			outcomes[i].done = false
			g.raw[calls[i].ID] = g.raw[calls[j].ID]
		}
		return outcomes
	}
}

// afterDispatch 是接到 toolLoopOptions.afterDispatch 的钩子：记录本轮签名，
// 第 loopGuardWarnAt 次追加提醒，第 loopGuardAbortAt 次返回 ErrLoopDetected。
func (g *loopGuard) afterDispatch(_ int, calls []llm.ToolCall, msgs []llm.ChatMessage, rejected bool) ([]llm.ChatMessage, error) {
	results := toolResultsByID(msgs, calls)
	if !rejected {
		for id, res := range g.raw {
			results[id] = res
		}
	}
	g.raw = nil

	sig := roundSignature(calls, results, rejected)
	g.window = append(g.window, sig)
	if len(g.window) > loopGuardWindow {
		g.window = g.window[len(g.window)-loopGuardWindow:]
	}
	n := 0
	for _, s := range g.window {
		if s == sig {
			n++
		}
	}

	switch {
	case n >= loopGuardAbortAt:
		return nil, ErrLoopDetected
	case n == loopGuardWarnAt:
		reminder := fmt.Sprintf(
			"<system-reminder>你已经是第 %d 次发出完全相同的工具调用（%s），并得到相同的结果或拒绝。不要继续重复：请阅读上面的工具结果并修正参数；信息已经足够时，直接用 response 回复，或用 ask_players 点名调查员。再重复将被强制终止这次发言。</system-reminder>",
			n, strings.Join(distinctCallNames(calls), "、"))
		return append(msgs, llm.ChatMessage{Role: "user", Content: reminder}), nil
	}
	return nil, nil
}

func distinctCallNames(calls []llm.ToolCall) []string {
	seen := map[string]bool{}
	var names []string
	for _, c := range calls {
		if !seen[c.Name] {
			seen[c.Name] = true
			names = append(names, c.Name)
		}
	}
	return names
}
