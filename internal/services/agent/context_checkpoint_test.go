// NOTE: 覆盖 Director 按发布单元落库（Checkpoint / CommitTail / NextFloor）与周期中途的就地总结（CompactInFloor）。
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func ckCall(id, name, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: name, Arguments: args}
}

func ckAssistant(calls ...llm.ToolCall) llm.ChatMessage {
	return llm.ChatMessage{Role: "assistant", ToolCalls: calls}
}

func ckTool(id, content string) llm.ChatMessage {
	return llm.ChatMessage{Role: "tool", ToolCallID: id, Content: content}
}

func ckHead() []llm.ChatMessage {
	return []llm.ChatMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "scenario"}}
}

func TestCheckpoint_AppendsUnitsWithFloorAndUnitNumbers(t *testing.T) {
	initAgentTestDB(t)
	const sid = uint(31)

	cm := LoadContext(sid, "director", ckHead(), ContextOptions{})
	if cm.NextFloor() != 1 {
		t.Fatalf("NextFloor() = %d, want 1", cm.NextFloor())
	}
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "<player_turn floor=1>开场</player_turn>"})
	msgs = append(msgs, ckAssistant(ckCall("a", "write", `{"direction":"d1"}`), ckCall("b", "response", `{"reply":"r1"}`)),
		ckTool("a", "已记录"), ckTool("b", "已发给玩家"))
	cm.Checkpoint(1, msgs)

	msgs = append(msgs, ckAssistant(ckCall("c", "ask_players", `{"targets":["A"],"question":"?"}`)), ckTool("c", "已点名"))
	cm.Checkpoint(1, msgs)

	data := loadTranscript(t, sid, "director")
	if len(data.Turns) != 2 {
		t.Fatalf("应有 2 个单元, got %d", len(data.Turns))
	}
	if data.Turns[0].Floor != 1 || data.Turns[0].Unit != 1 || data.Turns[1].Floor != 1 || data.Turns[1].Unit != 2 {
		t.Errorf("Floor/Unit 编号错误: %+v / %+v", data.Turns[0], data.Turns[1])
	}
	if data.Turns[0].Messages[0].Role != "user" || data.Turns[1].Messages[0].Role != "assistant" {
		t.Errorf("第 1 单元以 opening 开头，第 2 单元以 assistant 开头")
	}
	if data.NextFloor != 2 {
		t.Errorf("NextFloor = %d, want 2", data.NextFloor)
	}
	if data.NextSeq != 2 || len(data.Stats) != 2 {
		t.Errorf("NextSeq=%d stats=%d, want 2/2", data.NextSeq, len(data.Stats))
	}

	// 周期内第二次 Checkpoint 不再增加 NextFloor
	cm2 := LoadContext(sid, "director", ckHead(), ContextOptions{})
	if cm2.NextFloor() != 2 {
		t.Fatalf("下一个 floor 应为 2, got %d", cm2.NextFloor())
	}
}

func TestCheckpoint_NextFloorPrefixIsByteIdenticalToLastSent(t *testing.T) {
	initAgentTestDB(t)
	const sid = uint(32)
	head := ckHead()

	cm1 := LoadContext(sid, "director", head, ContextOptions{})
	msgs := cm1.Build(llm.ChatMessage{Role: "user", Content: "<player_turn floor=1>开场</player_turn>"})
	msgs = append(msgs, llm.ChatMessage{
		Role: "assistant", ToolCalls: []llm.ToolCall{ckCall("a", "response", `{"reply":"r1"}`)},
		ReasoningBlocks: []llm.ReasoningBlock{{Type: "thinking", Text: "t", Signature: "sig"}},
	}, ckTool("a", "已发给玩家"))
	cm1.Checkpoint(1, msgs)
	msgs = append(msgs, ckAssistant(ckCall("b", "ask_players", `{"targets":["A"],"question":"?"}`)), ckTool("b", "已点名"))
	cm1.Checkpoint(1, msgs)

	cm2 := LoadContext(sid, "director", head, ContextOptions{})
	next := cm2.Build(llm.ChatMessage{Role: "user", Content: "<player_turn floor=2>回应</player_turn>"})
	if len(next) != len(msgs)+1 {
		t.Fatalf("len(next) = %d, want %d", len(next), len(msgs)+1)
	}
	if !msgsEqualIgnoringBreakpoint(next[:len(msgs)], msgs) {
		t.Errorf("下一个 floor 的前缀必须与上一个 floor 最后发送的内容逐字段一致:\ngot  %+v\nwant %+v", next[:len(msgs)], msgs)
	}
}

func TestCheckpoint_NoNewMessagesIsNoop(t *testing.T) {
	initAgentTestDB(t)
	cm := LoadContext(33, "director", ckHead(), ContextOptions{})
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "open"})
	cm.Checkpoint(1, msgs)
	if !cm.IsEmpty() {
		t.Error("没有任何新消息时 Checkpoint 不应落库")
	}
}

func TestCommitTail_KeepsOnlyCompleteRoundsAfterLastCheckpoint(t *testing.T) {
	initAgentTestDB(t)
	const sid = uint(34)
	cm := LoadContext(sid, "director", ckHead(), ContextOptions{})
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "open"})
	msgs = append(msgs, ckAssistant(ckCall("a", "response", `{"reply":"r"}`)), ckTool("a", "已发给玩家"))
	cm.Checkpoint(1, msgs)

	// 尾巴：一轮完整的结算 + 一轮被取消、tool 结果不齐的调用
	msgs = append(msgs,
		ckAssistant(ckCall("b", "update_characters", `{"hp":-1}`)), ckTool("b", "已更新"),
		ckAssistant(ckCall("c", "manage_inventory", `{}`), ckCall("d", "roll_dice", `{}`)), ckTool("c", "已执行"))
	cm.CommitTail(1, msgs)

	data := loadTranscript(t, sid, "director")
	if len(data.Turns) != 2 {
		t.Fatalf("应有 1 个单元 + 1 个 tail, got %d", len(data.Turns))
	}
	tail := data.Turns[1]
	if tail.Kind != "tail" || tail.Floor != 1 || tail.Unit != 2 {
		t.Errorf("tail 元信息错误: %+v", tail)
	}
	if len(tail.Messages) != 2 || tail.Messages[0].ToolCalls[0].Name != "update_characters" {
		t.Errorf("tail 只应保留完整的那一轮: %+v", tail.Messages)
	}
	assertValidToolSequence(t, flattenTailForCheck(data.Turns))
}

func flattenTailForCheck(turns []models.TranscriptTurn) []models.TranscriptMsg {
	var out []models.TranscriptMsg
	for _, tr := range turns {
		out = append(out, tr.Messages...)
	}
	return out
}

func TestCommitTail_NoCompleteRoundIsNoop(t *testing.T) {
	initAgentTestDB(t)
	cm := LoadContext(35, "director", ckHead(), ContextOptions{})
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "open"})
	// 只有 opening，第一轮被取消：硬失败，不落库
	msgs = append(msgs, ckAssistant(ckCall("a", "query_clues", `{}`)))
	cm.CommitTail(1, msgs)
	if !cm.IsEmpty() {
		t.Error("没有完整轮时 CommitTail 不应落库")
	}

	// 只有 opening 和一个纯提醒，也不算进展
	cm2 := LoadContext(35, "director", ckHead(), ContextOptions{})
	msgs2 := cm2.Build(llm.ChatMessage{Role: "user", Content: "open"})
	cm2.CommitTail(1, msgs2)
	if !cm2.IsEmpty() {
		t.Error("只有 opening 不应落库")
	}
}

func TestCommitTail_DoesNotDoubleCountAfterCheckpoint(t *testing.T) {
	initAgentTestDB(t)
	const sid = uint(36)
	cm := LoadContext(sid, "director", ckHead(), ContextOptions{})
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "open"})
	msgs = append(msgs, ckAssistant(ckCall("a", "response", `{"reply":"r"}`)), ckTool("a", "已发给玩家"))
	cm.Checkpoint(1, msgs)
	cm.CommitTail(1, msgs) // 没有任何新消息
	data := loadTranscript(t, sid, "director")
	if len(data.Turns) != 1 {
		t.Errorf("Checkpoint 之后没有新消息时 CommitTail 不应再追加, got %d 个单元", len(data.Turns))
	}
}

func TestCompletedTailEnd(t *testing.T) {
	m := func(msgs ...llm.ChatMessage) []llm.ChatMessage { return msgs }
	user := llm.ChatMessage{Role: "user", Content: "u"}

	cases := []struct {
		name string
		msgs []llm.ChatMessage
		from int
		want int
	}{
		{"empty", m(user), 1, 1},
		{"complete", m(user, ckAssistant(ckCall("a", "x", "{}")), ckTool("a", "ok")), 1, 3},
		{"missing result", m(user, ckAssistant(ckCall("a", "x", "{}"), ckCall("b", "y", "{}")), ckTool("a", "ok")), 1, 1},
		{"complete then partial", m(user, ckAssistant(ckCall("a", "x", "{}")), ckTool("a", "ok"), ckAssistant(ckCall("b", "y", "{}"))), 1, 3},
		{"plain reminder kept", m(user, ckAssistant(ckCall("a", "x", "{}")), ckTool("a", "ok"), user), 1, 4},
	}
	for _, c := range cases {
		if got := completeTailEnd(c.msgs, c.from); got != c.want {
			t.Errorf("%s: completeTailEnd = %d, want %d", c.name, got, c.want)
		}
	}
}

// ckBeat 往 msgs 追加一拍：一个 write 单元和一个 response 单元，并各自 Checkpoint。
func ckBeat(cm *ContextManager, msgs []llm.ChatMessage, tag string) []llm.ChatMessage {
	msgs = append(msgs,
		ckAssistant(ckCall("q"+tag, "query_clues", `{}`)), ckTool("q"+tag, "线索"+tag),
		ckAssistant(ckCall("w"+tag, "write", `{"direction":"d`+tag+`"}`)), ckTool("w"+tag, "已记录"))
	cm.Checkpoint(1, msgs)
	msgs = append(msgs,
		ckAssistant(ckCall("u"+tag, "update_characters", `{}`)), ckTool("u"+tag, "HP变化"+tag),
		ckAssistant(ckCall("p"+tag, "response", `{"reply":"r`+tag+`"}`)), ckTool("p"+tag, "已发给玩家"))
	cm.Checkpoint(1, msgs)
	return msgs
}

func TestCompactInFloor_SummarizesEarlierFloorsAndKeepsCurrentFloor(t *testing.T) {
	initAgentTestDB(t)
	const sid = uint(37)
	// 前两个周期已经落库
	prev := LoadContext(sid, "director", ckHead(), ContextOptions{})
	for f := 1; f <= 2; f++ {
		m := prev.Build(llm.ChatMessage{Role: "user", Content: fmt.Sprintf("<player_turn floor=%d>行动%d</player_turn>", f, f)})
		ckBeat(prev, m, fmt.Sprint(f))
	}

	cm := LoadContext(sid, "director", ckHead(), ContextOptions{Window: 3_000, Strategy: TrimSummarize})
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "<player_turn floor=3>行动3</player_turn>"})
	msgs = ckBeat(cm, msgs, "3")
	// NOTE: 尾巴巨大，保留本周期和尾巴就超过低水位(1500)，所以切点会取本周期之前最大的拍边界；
	// 同时总结后估算值仍低于阈值(2400)。没有 usage 时 token 密度按 1 rune = 1 token。
	bigTail := strings.Repeat("卡", 2_000)
	msgs = append(msgs, ckAssistant(ckCall("z", "query_character", `{}`)), ckTool("z", bigTail))

	var gotInput string
	out, stillFull := cm.CompactInFloor(context.Background(), func(_ context.Context, in string) (string, error) {
		gotInput = in
		return "前两个周期的摘要", nil
	}, msgs, llm.Usage{})

	if stillFull {
		t.Fatalf("总结后估算值应已低于阈值")
	}
	if cm.data.Turns[0].Kind != "summary" || !strings.Contains(cm.data.Turns[0].Messages[0].Content, `floors="1-2"`) {
		t.Fatalf("前缀应是只覆盖 floor 1-2 的总结: %+v", cm.data.Turns[0])
	}
	for _, turn := range cm.data.Turns[1:] {
		if turn.Floor != 3 {
			t.Errorf("总结之后只应剩本周期的单元: %+v", turn)
		}
	}
	if len(cm.data.Turns) != 3 {
		t.Fatalf("应为 1 个总结 + 本周期 2 个单元, got %d", len(cm.data.Turns))
	}
	if strings.Contains(gotInput, "r3") || strings.Contains(gotInput, "行动3") {
		t.Errorf("本周期的内容不能进入总结输入:\n%s", gotInput)
	}
	for _, want := range []string{"行动1", "[KP回复] r1", "[结算 update_characters] HP变化2"} {
		if !strings.Contains(gotInput, want) {
			t.Errorf("总结输入缺少 %q:\n%s", want, gotInput)
		}
	}

	if out[len(out)-1].Content != bigTail {
		t.Errorf("未落库的尾巴必须原样保留在末尾: %+v", len(out[len(out)-1].Content))
	}
	if !strings.HasPrefix(out[len(cm.head)].Content, "<story_so_far") {
		t.Errorf("head 之后应紧跟总结: %+v", out[len(cm.head)])
	}
	if !out[len(cm.head)-1].CacheBreakpoint {
		t.Errorf("head 末尾应有断点")
	}
	hist := flattenTranscriptTurns(cm.data.Turns)
	if cm.historyLen != len(hist) || cm.turnStart != len(cm.head)+len(hist) || cm.historyTurns != len(cm.data.Turns) {
		t.Errorf("偏移量与重建结果不一致: historyLen=%d turnStart=%d historyTurns=%d", cm.historyLen, cm.turnStart, cm.historyTurns)
	}
	if !out[cm.turnStart-1].CacheBreakpoint {
		t.Errorf("新历史末尾应有断点")
	}
	var tms []models.TranscriptMsg
	for _, m := range out[len(cm.head):] {
		tms = append(tms, llmMsgToTranscript(m))
	}
	assertValidToolSequence(t, tms)

	// 之后的 Checkpoint 仍然以 turnStart 为起点，不会重复落库已有内容
	out = append(out, ckAssistant(ckCall("a", "ask_players", `{"targets":["A"],"question":"?"}`)), ckTool("a", "已点名"))
	cm.Checkpoint(1, out)
	saved := loadTranscript(t, sid, "director")
	last := saved.Turns[len(saved.Turns)-1]
	// 未落库的查询轮 z 在总结时原样保留，之后和 ask 一起落成本周期的第 3 个单元；已落库的单元不会重复。
	if last.Floor != 3 || last.Unit != 3 || len(last.Messages) != 4 ||
		last.Messages[0].ToolCalls[0].Name != "query_character" || last.Messages[2].ToolCalls[0].Name != "ask_players" {
		t.Errorf("总结后的 Checkpoint 应只追加 z+ask 这一个单元: floor=%d unit=%d msgs=%d", last.Floor, last.Unit, len(last.Messages))
	}
	if n := len(saved.Turns); n != 4 {
		t.Errorf("落库的单元应为 总结 + 本周期 3 个单元, got %d", n)
	}
	if saved.Turns[0].Kind != "summary" {
		t.Errorf("总结应已落库: %+v", saved.Turns[0])
	}
}

func TestCompactInFloor_NothingToSummarizeReportsFull(t *testing.T) {
	initAgentTestDB(t)
	cm := LoadContext(38, "director", ckHead(), ContextOptions{Window: 1_000, Strategy: TrimSummarize})
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "<player_turn floor=1>x</player_turn>"})
	msgs = ckBeat(cm, msgs, "1")

	out, stillFull := cm.CompactInFloor(context.Background(), func(context.Context, string) (string, error) {
		t.Fatal("本周期之前没有历史时不应调用总结")
		return "", nil
	}, msgs, llm.Usage{PromptTokens: 5_000})
	if !stillFull {
		t.Errorf("没有可总结的内容时应报告仍然已满")
	}
	if len(out) != len(msgs) {
		t.Errorf("msgs 不应被改写")
	}
}

func TestCompactInFloor_SummaryFailureReportsFullAndKeepsHistory(t *testing.T) {
	initAgentTestDB(t)
	const sid = uint(39)
	prev := LoadContext(sid, "director", ckHead(), ContextOptions{})
	ckBeat(prev, prev.Build(llm.ChatMessage{Role: "user", Content: "<player_turn floor=1>a</player_turn>"}), "1")

	cm := LoadContext(sid, "director", ckHead(), ContextOptions{Window: 1_000, Strategy: TrimSummarize})
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "<player_turn floor=2>b</player_turn>"})
	before := len(cm.data.Turns)

	out, stillFull := cm.CompactInFloor(context.Background(), func(context.Context, string) (string, error) {
		return "", errors.New("gateway down")
	}, msgs, llm.Usage{PromptTokens: 5_000})
	if !stillFull || len(out) != len(msgs) || len(cm.data.Turns) != before {
		t.Errorf("总结失败应保持原样并报告已满: full=%v turns=%d->%d", stillFull, before, len(cm.data.Turns))
	}
	if countKind(cm.data.Turns, "summary") != 0 {
		t.Errorf("不应写入总结单元")
	}
}

func TestBeatBoundaryAt(t *testing.T) {
	unit := func(floor int, calls ...string) models.TranscriptTurn {
		turn := models.TranscriptTurn{Floor: floor}
		var tcs []models.TranscriptToolCall
		for i, name := range calls {
			tcs = append(tcs, models.TranscriptToolCall{ID: fmt.Sprintf("%s%d", name, i), Name: name, Arguments: `{}`})
		}
		turn.Messages = []models.TranscriptMsg{{Role: "assistant", ToolCalls: tcs}}
		for _, tc := range tcs {
			turn.Messages = append(turn.Messages, models.TranscriptMsg{Role: "tool", ToolCallID: tc.ID, Content: "ok"})
		}
		return turn
	}
	rejected := unit(1, "response")
	rejected.Messages[1].Content = "SYSTEM REJECT: empty"

	turns := []models.TranscriptTurn{
		unit(1, "write"),    // 0: 拍中间
		unit(1, "response"), // 1: 收尾
		unit(1, "write"),    // 2: 拍中间
		rejected,            // 3: 被拒绝的 response 不算收尾
		unit(1, "write"),    // 4: 同一周期的后续单元
		unit(2, "write"),    // 5: 换了周期
	}
	// b 表示在 turns[b-1] 与 turns[b] 之间切开
	cases := map[int]bool{
		1: false, // write 后面还要接 response
		2: true,  // response 收尾了一拍
		3: false, // write 后面还要接回复
		4: false, // 被拒绝的 response 不算，且后面还是同一周期
		5: true,  // 换周期必然是边界
		6: true,  // 末尾
	}
	for b, want := range cases {
		if got := beatBoundaryAt(turns, b); got != want {
			t.Errorf("beatBoundaryAt(%d) = %v, want %v", b, got, want)
		}
	}
	if beatBoundaryAt(turns, 0) || beatBoundaryAt(turns, 7) {
		t.Errorf("越界下标不是边界")
	}
}

func flattenText(msgs []models.TranscriptMsg) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}
