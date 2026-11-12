package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func TestRenderDirectorToolResult_OK(t *testing.T) {
	got := renderDirectorToolResult(fakeToolCall("a", "roll_dice", `{}`), toolOutcome{result: "掷出42"})
	want := "<tool_result name=\"roll_dice\" status=\"ok\">\n掷出42\n</tool_result>"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderDirectorToolResult_RejectFromField(t *testing.T) {
	got := renderDirectorToolResult(fakeToolCall("a", "write", `{}`), toolOutcome{reject: "SYSTEM REJECT: 参数解析失败: x"})
	want := "<tool_result name=\"write\" status=\"rejected\">\n参数解析失败: x\n</tool_result>"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderDirectorToolResult_RejectFromResultPrefix(t *testing.T) {
	for _, sep := range []string{": ", "：", ":", " "} {
		got := renderDirectorToolResult(fakeToolCall("a", "ask_players", `{}`), toolOutcome{result: "SYSTEM REJECT" + sep + "一次只能点名一个人"})
		if !strings.Contains(got, `status="rejected"`) || strings.Contains(got, "SYSTEM REJECT") {
			t.Errorf("sep %q: got %q, want rejected status and no prefix", sep, got)
		}
		if !strings.Contains(got, "\n一次只能点名一个人\n") {
			t.Errorf("sep %q: body lost: %q", sep, got)
		}
	}
}

func TestRenderDirectorToolResult_Cached(t *testing.T) {
	got := renderDirectorToolResult(fakeToolCall("a", "query_clues", `{}`), toolOutcome{result: "<clues/>", cached: true})
	want := "<tool_result name=\"query_clues\" status=\"ok\" cached=\"true\">\n<clues/>\n</tool_result>"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderDirectorToolResult_EscapesNameAttr(t *testing.T) {
	got := renderDirectorToolResult(llm.ToolCall{ID: "a", Name: "a\"<b>&\nc"}, toolOutcome{result: "x"})
	if !strings.HasPrefix(got, `<tool_result name="a&quot;&lt;b&gt;&amp;&#10;c" status="ok">`) {
		t.Fatalf("name attribute not escaped: %q", got)
	}
}

func TestRenderDirectorToolResult_BodyKeptVerbatim(t *testing.T) {
	body := `<chars><c name="约翰" hp="9"/></chars>`
	got := renderDirectorToolResult(fakeToolCall("a", "query_character", `{}`), toolOutcome{result: body})
	if !strings.Contains(got, "\n"+body+"\n") {
		t.Fatalf("XML body must not be escaped: %q", got)
	}
}

func TestIsToolRejected(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"new rejected", `<tool_result name="write" status="rejected">` + "\nx\n</tool_result>", true},
		{"new ok", `<tool_result name="write" status="ok">` + "\nx\n</tool_result>", false},
		{"new ok cached", `<tool_result name="query_clues" status="ok" cached="true">` + "\nx\n</tool_result>", false},
		{"legacy rejected", "SYSTEM REJECT: write.direction 不能为空", true},
		{"legacy ok", "已发布", false},
		{"empty", "", false},
		{"ok body mentions rejected status", `<tool_result name="act_npc" status="ok">` + "\n<ooc>status=\"rejected\"</ooc>\n</tool_result>", false},
		{"ok body starts with SYSTEM REJECT", `<tool_result name="act_npc" status="ok">` + "\nSYSTEM REJECT\n</tool_result>", false},
		{"unterminated open tag", `<tool_result name="write" status="rejected"`, false},
	}
	for _, tc := range cases {
		if got := isToolRejected(tc.content); got != tc.want {
			t.Errorf("%s: isToolRejected(%q) = %v, want %v", tc.name, tc.content, got, tc.want)
		}
	}
}

func TestToolResultBody(t *testing.T) {
	cases := []struct{ name, content, want string }{
		{"ok", "<tool_result name=\"roll_dice\" status=\"ok\">\n掷出42\n</tool_result>", "掷出42"},
		{"multi-line", "<tool_result name=\"a\" status=\"ok\">\n甲\n乙\n</tool_result>", "甲\n乙"},
		{"cached", "<tool_result name=\"a\" status=\"ok\" cached=\"true\">\nx\n</tool_result>", "x"},
		{"rejected", "<tool_result name=\"a\" status=\"rejected\">\n原因\n</tool_result>", "原因"},
		{"legacy untouched", "SYSTEM REJECT: 原因", "SYSTEM REJECT: 原因"},
		{"plain untouched", "已执行。", "已执行。"},
	}
	for _, tc := range cases {
		if got := toolResultBody(tc.content); got != tc.want {
			t.Errorf("%s: toolResultBody(%q) = %q, want %q", tc.name, tc.content, got, tc.want)
		}
	}
}

func TestToolResultBody_RoundTrip(t *testing.T) {
	call := fakeToolCall("a", "act_npc", `{}`)
	for _, o := range []toolOutcome{
		{result: "多行\n正文"},
		{reject: "SYSTEM REJECT: 原因"},
		{result: "x", cached: true},
	} {
		got := toolResultBody(renderDirectorToolResult(call, o))
		want := strings.TrimLeft(strings.TrimPrefix(o.result+o.reject, toolRejectPrefix), ":： \t\r\n")
		if got != want {
			t.Errorf("round trip of %+v = %q, want %q", o, got, want)
		}
	}
}

func TestRunToolLoop_RenderResultAppliesToAllThreePaths(t *testing.T) {
	render := func(call llm.ToolCall, o toolOutcome) string {
		if o.reject != "" {
			return "R[" + call.Name + "]" + o.reject
		}
		return "O[" + call.Name + "]" + o.result
	}
	toolContents := func(msgs []llm.ChatMessage) map[string]string {
		out := map[string]string{}
		for _, m := range msgs {
			if m.Role == "tool" {
				out[m.ToolCallID] = m.Content
			}
		}
		return out
	}

	t.Run("serial path with unknown tool", func(t *testing.T) {
		prov := &sequentialFakeProvider{toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("a", "probe", `{}`), fakeToolCall("b", "ghost", `{}`)}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("c", "finish", `{}`)}},
		}}
		msgs, err := runToolLoop(context.Background(), toolLoopOptions{
			handle: hookTestHandle(prov), stage: "render_test",
			msgs:  []llm.ChatMessage{{Role: "user", Content: "go"}},
			tools: hookTestTools(), maxRounds: 5, dispatch: hookTestDispatch, renderResult: render,
		})
		if err != nil {
			t.Fatalf("runToolLoop: %v", err)
		}
		got := toolContents(msgs)
		if got["a"] != "O[probe]probed" || got["c"] != "O[finish]ok" {
			t.Errorf("dispatched results not rendered: %+v", got)
		}
		if !strings.HasPrefix(got["b"], "R[ghost]SYSTEM REJECT: 未知工具") {
			t.Errorf("unknown tool not rendered: %q", got["b"])
		}
	})

	t.Run("whole batch rejected", func(t *testing.T) {
		prov := &sequentialFakeProvider{toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("a", "probe", `{}`), fakeToolCall("b", "finish", `{}`)}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("c", "finish", `{}`)}},
		}}
		msgs, err := runToolLoop(context.Background(), toolLoopOptions{
			handle: hookTestHandle(prov), stage: "render_test",
			msgs:  []llm.ChatMessage{{Role: "user", Content: "go"}},
			tools: hookTestTools(), maxRounds: 5, dispatch: hookTestDispatch, renderResult: render,
		})
		if err != nil {
			t.Fatalf("runToolLoop: %v", err)
		}
		got := toolContents(msgs)
		if !strings.HasPrefix(got["a"], "R[probe]SYSTEM REJECT") || !strings.HasPrefix(got["b"], "R[finish]SYSTEM REJECT") {
			t.Errorf("batch rejection not rendered per call: %+v", got)
		}
	})

	t.Run("batch dispatch with missing result", func(t *testing.T) {
		prov := &sequentialFakeProvider{toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("a", "probe", `{}`), fakeToolCall("b", "probe", `{}`), fakeToolCall("c", "ghost", `{}`)}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("d", "finish", `{}`)}},
		}}
		msgs, err := runToolLoop(context.Background(), toolLoopOptions{
			handle: hookTestHandle(prov), stage: "render_test",
			msgs:  []llm.ChatMessage{{Role: "user", Content: "go"}},
			tools: hookTestTools(), maxRounds: 5, renderResult: render,
			batchDispatch: func(_ context.Context, calls []llm.ToolCall) []toolOutcome {
				out := []toolOutcome{{result: "first"}}
				if calls[0].Name == "finish" {
					out[0].done = true
				}
				return out
			},
		})
		if err != nil {
			t.Fatalf("runToolLoop: %v", err)
		}
		got := toolContents(msgs)
		if got["a"] != "O[probe]first" {
			t.Errorf("batch result not rendered: %q", got["a"])
		}
		if !strings.HasPrefix(got["b"], "R[probe]SYSTEM REJECT: 工具执行未返回结果") {
			t.Errorf("missing result not rendered: %q", got["b"])
		}
		if !strings.HasPrefix(got["c"], "R[ghost]SYSTEM REJECT: 未知工具") {
			t.Errorf("unknown tool not rendered: %q", got["c"])
		}
	})
}

func TestRunToolLoop_NilRenderResultKeepsRawContent(t *testing.T) {
	prov := &sequentialFakeProvider{toolResponses: []llm.ToolChatResult{
		{ToolCalls: []llm.ToolCall{fakeToolCall("a", "probe", `{}`), fakeToolCall("b", "finish", `{}`)}},
		{ToolCalls: []llm.ToolCall{fakeToolCall("c", "ghost", `{}`)}},
		{ToolCalls: []llm.ToolCall{fakeToolCall("d", "finish", `{}`)}},
	}}
	msgs, err := runToolLoop(context.Background(), toolLoopOptions{
		handle: hookTestHandle(prov), stage: "raw_test",
		msgs:  []llm.ChatMessage{{Role: "user", Content: "go"}},
		tools: hookTestTools(), maxRounds: 5, dispatch: hookTestDispatch,
	})
	if err != nil {
		t.Fatalf("runToolLoop: %v", err)
	}
	for _, m := range msgs {
		if m.Role == "tool" && strings.Contains(m.Content, "<tool_result") {
			t.Errorf("default path must not wrap results: %+v", m)
		}
	}
}

func TestWriterView_NewFormatRejectedWriteGetsNoProse(t *testing.T) {
	director := []llm.ChatMessage{
		{Role: "system", Content: "director-system"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{fakeToolCall("w1", "write", `{"direction":"推门"}`)}},
		{Role: "tool", ToolCallID: "w1", Content: renderDirectorToolResult(fakeToolCall("w1", "write", ""), toolOutcome{result: "已执行。"})},
		{Role: "assistant", ToolCalls: []llm.ToolCall{fakeToolCall("w2", "write", `{"direction":""}`)}},
		{Role: "tool", ToolCallID: "w2", Content: renderDirectorToolResult(fakeToolCall("w2", "write", ""), toolOutcome{result: "SYSTEM REJECT: write.direction 不能为空"})},
	}
	prose := map[string]writerProse{
		"w1": {Floor: 1, Text: "门开了"},
		"w2": {Floor: 1, Text: "不应附在被拒绝的结果上"},
	}
	view := buildWriterView(director, "writer-system", prose, true, 0, 0, writerViewInstruction())
	if !strings.Contains(view[2].Content, "门开了") {
		t.Errorf("ok write should carry prose: %q", view[2].Content)
	}
	if strings.Contains(view[4].Content, "不应附在被拒绝的结果上") {
		t.Errorf("rejected write must not carry prose: %q", view[4].Content)
	}
}

func TestSummary_NewFormatRejectedCallsAreSkippedAndBodyUnwrapped(t *testing.T) {
	wrap := func(name, result string) string {
		return renderDirectorToolResult(fakeToolCall("x", name, ""), toolOutcome{result: result})
	}
	unit := models.TranscriptTurn{Floor: 1, Unit: 1, Messages: []models.TranscriptMsg{
		{Role: "assistant", ToolCalls: []models.TranscriptToolCall{
			{ID: "a", Name: "narrate", Arguments: `{"reply":"被拒绝的回复"}`},
			{ID: "b", Name: "roll_dice", Arguments: `{}`},
		}},
		{Role: "tool", ToolCallID: "a", Content: wrap("narrate", "SYSTEM REJECT: options")},
		{Role: "tool", ToolCallID: "b", Content: wrap("roll_dice", "掷出57")},
	}}
	out := renderUnitOutputs(unit.Messages)
	if strings.Contains(out, "被拒绝的回复") {
		t.Errorf("rejected narrate must be skipped:\n%s", out)
	}
	if !strings.Contains(out, "[掷骰] 掷出57") || strings.Contains(out, "tool_result") {
		t.Errorf("dice body should be unwrapped:\n%s", out)
	}
	if turnClosesBeat(unit) {
		t.Error("a rejected narrate must not close the beat")
	}

	ok := models.TranscriptTurn{Floor: 1, Unit: 2, Messages: []models.TranscriptMsg{
		{Role: "assistant", ToolCalls: []models.TranscriptToolCall{{ID: "n", Name: "narrate", Arguments: `{"reply":"好"}`}}},
		{Role: "tool", ToolCallID: "n", Content: wrap("narrate", "已执行。")},
	}}
	if !turnClosesBeat(ok) {
		t.Error("an accepted narrate must close the beat")
	}
}
