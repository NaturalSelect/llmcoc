package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/llmcoc/server/internal/services/llm"
)

func TestNormalizeArgs_KeyOrderAndWhitespaceInsensitive(t *testing.T) {
	a := callKey("query_character", `{"character_name":"A",  "x": 1}`)
	b := callKey("query_character", "{\n \"x\":1,\"character_name\":\"A\"}")
	if a != b {
		t.Fatalf("callKey differs: %q vs %q", a, b)
	}
	if callKey("query_character", `{"x":1}`) == callKey("query_character", `{"x":2}`) {
		t.Fatal("different args must not collide")
	}
	if callKey("query_character", `not json `) != callKey("query_character", `not json`) {
		t.Fatal("unparsable args should fall back to trimmed raw text")
	}
}

func TestRoundSignature_IgnoresCallOrderAndSideEffectResults(t *testing.T) {
	c1 := fakeToolCall("1", "check_rule", `{"q":"a"}`)
	c2 := fakeToolCall("2", "act_npc", `{"npc":"n"}`)
	c3 := fakeToolCall("3", "update_characters", `{"hp":-1}`)

	r1 := map[string]string{"1": "rule A", "2": "npc says hi", "3": "HP 10"}
	r2 := map[string]string{"1": "rule A", "2": "npc says hi", "3": "HP 9"}

	s1 := roundSignature([]llm.ToolCall{c1, c2, c3}, r1, false)
	s2 := roundSignature([]llm.ToolCall{c3, c2, c1}, r2, false)
	if s1 != s2 {
		t.Fatal("signature must be independent of call order and of side-effect tool results")
	}

	r3 := map[string]string{"1": "rule B", "2": "npc says hi", "3": "HP 10"}
	if roundSignature([]llm.ToolCall{c1, c2, c3}, r3, false) == s1 {
		t.Fatal("changed read-only result must change the signature")
	}
}

func TestRoundSignature_RejectedBatchCountsResult(t *testing.T) {
	c := fakeToolCall("1", "update_characters", `{"hp":-1}`)
	a := roundSignature([]llm.ToolCall{c}, map[string]string{"1": "SYSTEM REJECT: a"}, true)
	b := roundSignature([]llm.ToolCall{c}, map[string]string{"1": "SYSTEM REJECT: a"}, true)
	if a != b {
		t.Fatal("same rejected batch must repeat the same signature")
	}
	if a == roundSignature([]llm.ToolCall{c}, map[string]string{"1": "SYSTEM REJECT: b"}, true) {
		t.Fatal("different rejection text should differ")
	}
}

// guardHarness 把 loopGuard 接到一个脚本化的 inner dispatch 上，并按 runToolLoop 的方式追加 tool 消息。
type guardHarness struct {
	g        *loopGuard
	executed []string
	results  map[string]string
	mu       sync.Mutex
	msgs     []llm.ChatMessage
}

func newGuardHarness() *guardHarness {
	return &guardHarness{g: newLoopGuard(), results: map[string]string{}}
}

func (h *guardHarness) inner() toolBatchDispatch {
	return func(_ context.Context, calls []llm.ToolCall) []toolOutcome {
		h.mu.Lock()
		defer h.mu.Unlock()
		out := make([]toolOutcome, len(calls))
		for i, c := range calls {
			h.executed = append(h.executed, c.Name)
			res := h.results[c.Name]
			if res == "" {
				res = "ok:" + c.Name
			}
			out[i] = toolOutcome{result: res}
		}
		return out
	}
}

// round 模拟 runToolLoop 一轮：分发 → 追加 tool 消息 → afterDispatch。
func (h *guardHarness) round(calls ...llm.ToolCall) ([]toolOutcome, error) {
	outs := h.g.wrapDispatch(h.inner())(context.Background(), calls)
	h.msgs = append(h.msgs, llm.ChatMessage{Role: "assistant", ToolCalls: calls})
	for i, c := range calls {
		content := outs[i].result
		if outs[i].reject != "" {
			content = outs[i].reject
		}
		h.msgs = append(h.msgs, llm.ChatMessage{Role: "tool", ToolCallID: c.ID, Content: content})
	}
	next, err := h.g.afterDispatch(1, calls, h.msgs, false)
	if next != nil {
		h.msgs = next
	}
	return outs, err
}

func (h *guardHarness) count(name string) int {
	n := 0
	for _, e := range h.executed {
		if e == name {
			n++
		}
	}
	return n
}

func TestLoopGuard_ReadOnlyCacheHitAndInvalidation(t *testing.T) {
	h := newGuardHarness()
	q := func(id string) llm.ToolCall { return fakeToolCall(id, "query_clues", `{"keyword":"门"}`) }

	o1, _ := h.round(q("a"))
	o2, _ := h.round(q("b"))
	if h.count("query_clues") != 1 {
		t.Fatalf("query_clues executed %d times, want 1 (second reused)", h.count("query_clues"))
	}
	if !o2[0].cached || o2[0].result != o1[0].result {
		t.Fatalf("cached outcome = %+v, want cached=true with the original result %q", o2[0], o1[0].result)
	}
	if o1[0].cached {
		t.Fatalf("first execution must not be marked cached: %+v", o1[0])
	}

	// 有副作用的工具成功执行后，缓存失效
	h.round(fakeToolCall("c", "update_characters", `{"hp":-1}`))
	h.round(q("d"))
	if h.count("query_clues") != 2 {
		t.Fatalf("query_clues executed %d times after a side effect, want 2", h.count("query_clues"))
	}

	// 产生消息后同样失效
	h.g.markMessagesProduced()
	h.round(q("e"))
	if h.count("query_clues") != 3 {
		t.Fatalf("query_clues executed %d times after messages, want 3", h.count("query_clues"))
	}
}

func TestLoopGuard_RollDiceAndActNPCNeverReused(t *testing.T) {
	h := newGuardHarness()
	dice := func(id string) llm.ToolCall { return fakeToolCall(id, "roll_dice", `{"dice":"1d100"}`) }
	npc := func(id string) llm.ToolCall { return fakeToolCall(id, "act_npc", `{"npc":"n"}`) }
	h.round(dice("a"))
	h.round(dice("b"))
	h.round(npc("c"))
	h.round(npc("d"))
	if h.count("roll_dice") != 2 || h.count("act_npc") != 2 {
		t.Fatalf("roll_dice=%d act_npc=%d, both want 2", h.count("roll_dice"), h.count("act_npc"))
	}
}

func TestLoopGuard_SameBatchDuplicateReadOnlyExecutesOnce(t *testing.T) {
	h := newGuardHarness()
	outs, _ := h.round(
		fakeToolCall("a", "query_character", `{"character_name":"A"}`),
		fakeToolCall("b", "query_character", `{"character_name":"A"}`),
		fakeToolCall("c", "query_character", `{"character_name":"B"}`),
	)
	if h.count("query_character") != 2 {
		t.Fatalf("executed %d, want 2 (A once, B once)", h.count("query_character"))
	}
	if outs[0].result != outs[1].result || outs[0].reject != "" || outs[1].reject != "" {
		t.Fatalf("duplicate must share the result: %+v %+v", outs[0], outs[1])
	}
}

func TestLoopGuard_FailedReadOnlyIsNotCached(t *testing.T) {
	g := newLoopGuard()
	calls := 0
	inner := func(_ context.Context, cs []llm.ToolCall) []toolOutcome {
		calls++
		return []toolOutcome{{reject: "SYSTEM REJECT: 参数解析失败"}}
	}
	d := g.wrapDispatch(inner)
	c := []llm.ToolCall{fakeToolCall("a", "query_clues", `{}`)}
	d(context.Background(), c)
	d(context.Background(), c)
	if calls != 2 {
		t.Fatalf("inner called %d times, want 2 (rejections are not cached)", calls)
	}
}

func TestLoopGuard_CrossRoundSettlementDedupAndConfirm(t *testing.T) {
	h := newGuardHarness()
	set := func(id string) llm.ToolCall {
		return fakeToolCall(id, "update_characters", `{"character":"A","hp":-2}`)
	}

	h.round(set("a"))
	outs, _ := h.round(set("b"))
	if outs[0].reject == "" || !strings.Contains(outs[0].reject, "原样再发") {
		t.Fatalf("second identical settlement must be rejected with a confirm hint, got %+v", outs[0])
	}
	if h.count("update_characters") != 1 {
		t.Fatalf("executed %d, want 1", h.count("update_characters"))
	}

	outs, _ = h.round(set("c"))
	if outs[0].reject != "" {
		t.Fatalf("resending unchanged must be let through, got %+v", outs[0])
	}
	if h.count("update_characters") != 2 {
		t.Fatalf("executed %d, want 2 after confirmation", h.count("update_characters"))
	}

	// 参数不同不受影响
	outs, _ = h.round(fakeToolCall("d", "update_characters", `{"character":"A","hp":-3}`))
	if outs[0].reject != "" {
		t.Fatalf("different args must not be rejected: %+v", outs[0])
	}
}

func TestLoopGuard_SettlementDedupResetsAfterMessages(t *testing.T) {
	h := newGuardHarness()
	set := fakeToolCall("a", "manage_inventory", `{"character":"A","item":"刀","operate":"add"}`)
	h.round(set)
	h.g.markMessagesProduced()
	set.ID = "b"
	outs, _ := h.round(set)
	if outs[0].reject != "" {
		t.Fatalf("new beat may settle the same call again: %+v", outs[0])
	}
}

func TestLoopGuard_WarnAtThirdAndAbortAtFifth(t *testing.T) {
	h := newGuardHarness()
	// roll_dice 不会被复用，用它制造"执行了但签名相同"的空转：签名只看参数
	call := func(i int) llm.ToolCall { return fakeToolCall(fmt.Sprintf("r%d", i), "roll_dice", `{"dice":"1d100"}`) }

	for i := 1; i <= 2; i++ {
		if _, err := h.round(call(i)); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}
	// roll_dice 不在 guardSigWithResult 里，签名只看参数，第 3 次触发提醒
	if _, err := h.round(call(3)); err != nil {
		t.Fatalf("round 3: %v", err)
	}
	last := h.msgs[len(h.msgs)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "<system-reminder>") || !strings.Contains(last.Content, "roll_dice") {
		t.Fatalf("round 3 must append a reminder, last msg = %+v", last)
	}
	if _, err := h.round(call(4)); err != nil {
		t.Fatalf("round 4: %v", err)
	}
	if _, err := h.round(call(5)); !errors.Is(err, ErrLoopDetected) {
		t.Fatalf("round 5 err = %v, want ErrLoopDetected", err)
	}
}

func TestLoopGuard_ReadOnlyCachedRepeatsStillCountTowardsLoop(t *testing.T) {
	h := newGuardHarness()
	q := func(i int) llm.ToolCall {
		return fakeToolCall(fmt.Sprintf("q%d", i), "query_clues", `{"keyword":"门"}`)
	}
	var err error
	for i := 1; i <= 5; i++ {
		_, err = h.round(q(i))
	}
	if !errors.Is(err, ErrLoopDetected) {
		t.Fatalf("5 identical cached rounds must abort, err = %v", err)
	}
	if h.count("query_clues") != 1 {
		t.Fatalf("executed %d times, want 1", h.count("query_clues"))
	}
}

func TestLoopGuard_RejectedBatchRepeatsAreCounted(t *testing.T) {
	g := newLoopGuard()
	calls := []llm.ToolCall{fakeToolCall("a", "update_characters", `{"hp":1}`), fakeToolCall("b", "narrate", `{"reply":"x"}`)}
	var msgs []llm.ChatMessage
	var err error
	for i := 1; i <= 5; i++ {
		msgs = append(msgs,
			llm.ChatMessage{Role: "tool", ToolCallID: "a", Content: "SYSTEM REJECT: batch"},
			llm.ChatMessage{Role: "tool", ToolCallID: "b", Content: "SYSTEM REJECT: batch"})
		var next []llm.ChatMessage
		next, err = g.afterDispatch(i, calls, msgs, true)
		if next != nil {
			msgs = next
		}
		if i < 5 && err != nil {
			t.Fatalf("round %d: unexpected %v", i, err)
		}
	}
	if !errors.Is(err, ErrLoopDetected) {
		t.Fatalf("5 identical rejected batches must abort, err = %v", err)
	}
}

func TestLoopGuard_DifferentRoundsDoNotTrigger(t *testing.T) {
	h := newGuardHarness()
	for i := 0; i < 12; i++ {
		args := fmt.Sprintf(`{"keyword":"k%d"}`, i)
		if _, err := h.round(fakeToolCall(fmt.Sprintf("q%d", i), "query_clues", args)); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}
	for _, m := range h.msgs {
		if m.Role == "user" {
			t.Fatalf("unexpected reminder: %q", m.Content)
		}
	}
}

func TestLoopGuard_WindowSlides(t *testing.T) {
	h := newGuardHarness()
	repeat := func(i int) llm.ToolCall { return fakeToolCall(fmt.Sprintf("r%d", i), "roll_dice", `{"dice":"1d6"}`) }
	// 重复 4 次（会提醒但不终止），再塞入 10 个不同的调用把窗口冲掉，之后再重复 4 次不应终止
	for i := 0; i < 4; i++ {
		if _, err := h.round(repeat(i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < loopGuardWindow; i++ {
		if _, err := h.round(fakeToolCall(fmt.Sprintf("d%d", i), "roll_dice", fmt.Sprintf(`{"dice":"1d%d"}`, i+20))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 10; i < 14; i++ {
		if _, err := h.round(repeat(i)); err != nil {
			t.Fatalf("round %d: window should have slid, got %v", i, err)
		}
	}
}
