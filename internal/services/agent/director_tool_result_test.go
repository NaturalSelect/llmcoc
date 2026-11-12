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

func TestRenderDirectorToolResult_NPCAttr(t *testing.T) {
	cases := []struct {
		name string
		call llm.ToolCall
		out  toolOutcome
		want string
	}{
		{"act_npc", fakeToolCall("a", "act_npc", `{"npc_name":"老板","question":"q"}`), toolOutcome{result: "x"},
			"<tool_result name=\"act_npc\" npc=\"老板\" status=\"ok\">\nx\n</tool_result>"},
		{"update_npc_card", fakeToolCall("a", "update_npc_card", `{"npc_name":" 老板 ","changes":["HP -1"]}`), toolOutcome{result: "x"},
			"<tool_result name=\"update_npc_card\" npc=\"老板\" status=\"ok\">\nx\n</tool_result>"},
		{"create_npc uses char_card.name", fakeToolCall("a", "create_npc", `{"char_card":{"name":"醉汉","race":"人类"}}`), toolOutcome{result: "x"},
			"<tool_result name=\"create_npc\" npc=\"醉汉\" status=\"ok\">\nx\n</tool_result>"},
		{"rejected keeps npc before status", fakeToolCall("a", "act_npc", `{"npc_name":"老板"}`), toolOutcome{reject: "SYSTEM REJECT: 不行"},
			"<tool_result name=\"act_npc\" npc=\"老板\" status=\"rejected\">\n不行\n</tool_result>"},
		{"cached after status", fakeToolCall("a", "query_npc_card", `{"npc_name":"老板"}`), toolOutcome{result: "x", cached: true},
			"<tool_result name=\"query_npc_card\" npc=\"老板\" status=\"ok\" cached=\"true\">\nx\n</tool_result>"},
		{"empty name has no attr", fakeToolCall("a", "query_npc_card", `{"npc_name":""}`), toolOutcome{result: "x"},
			"<tool_result name=\"query_npc_card\" status=\"ok\">\nx\n</tool_result>"},
		{"bad arguments has no attr", fakeToolCall("a", "act_npc", `not json`), toolOutcome{result: "x"},
			"<tool_result name=\"act_npc\" status=\"ok\">\nx\n</tool_result>"},
		{"non-npc tool ignores npc_name", fakeToolCall("a", "roll_dice", `{"npc_name":"老板"}`), toolOutcome{result: "x"},
			"<tool_result name=\"roll_dice\" status=\"ok\">\nx\n</tool_result>"},
		{"create_npc without char_card", fakeToolCall("a", "create_npc", `{}`), toolOutcome{result: "x"},
			"<tool_result name=\"create_npc\" status=\"ok\">\nx\n</tool_result>"},
	}
	for _, tc := range cases {
		if got := renderDirectorToolResult(tc.call, tc.out); got != tc.want {
			t.Errorf("%s:\ngot  %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestRenderDirectorToolResult_NPCAttrEscapedAndRoundTrips(t *testing.T) {
	got := renderDirectorToolResult(fakeToolCall("a", "act_npc", `{"npc_name":"a\"<b>&"}`), toolOutcome{result: "正文"})
	if !strings.HasPrefix(got, `<tool_result name="act_npc" npc="a&quot;&lt;b&gt;&amp;" status="ok">`) {
		t.Fatalf("npc attribute not escaped: %q", got)
	}
	if isToolRejected(got) || toolResultBody(got) != "正文" {
		t.Errorf("round trip broke: rejected=%v body=%q", isToolRejected(got), toolResultBody(got))
	}
	rejected := renderDirectorToolResult(fakeToolCall("a", "act_npc", `{"npc_name":"老板"}`), toolOutcome{reject: "SYSTEM REJECT: 不行"})
	if !isToolRejected(rejected) || toolResultBody(rejected) != "不行" {
		t.Errorf("rejected round trip broke: rejected=%v body=%q", isToolRejected(rejected), toolResultBody(rejected))
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

func TestAttachWriterOutput_EnvelopeAndLegacyFormats(t *testing.T) {
	envelope := renderDirectorToolResult(fakeToolCall("w1", "write", ""), toolOutcome{result: "已发布。"})
	got, ok := attachWriterOutput(envelope, "门开了")
	if !ok {
		t.Fatal("an ok envelope result should take prose")
	}
	if want := "<tool_result name=\"write\" status=\"ok\">\n已发布。\n<writer_output>\n门开了\n</writer_output>\n</tool_result>"; got != want {
		t.Errorf("envelope =\n%q\nwant\n%q", got, want)
	}
	if isToolRejected(got) || toolResultBody(got) != "已发布。\n<writer_output>\n门开了\n</writer_output>" {
		t.Errorf("attaching prose must not change how the envelope is parsed: rejected=%v body=%q", isToolRejected(got), toolResultBody(got))
	}
	if text, ok := writerOutputOf(got); !ok || text != "门开了" {
		t.Errorf("writerOutputOf = %q, %v", text, ok)
	}

	legacy, ok := attachWriterOutput("已发布", "门开了")
	if !ok || legacy != "已发布\n<writer_output>\n门开了\n</writer_output>" {
		t.Errorf("legacy = %q, %v", legacy, ok)
	}
}

func TestAttachWriterOutput_SkipsRejectedDuplicateAndEmpty(t *testing.T) {
	rejected := renderDirectorToolResult(fakeToolCall("w2", "write", ""), toolOutcome{result: "SYSTEM REJECT: write.direction 不能为空"})
	if got, ok := attachWriterOutput(rejected, "不应附上"); ok || got != rejected {
		t.Errorf("a rejected envelope must stay untouched, got %q, %v", got, ok)
	}
	if got, ok := attachWriterOutput("SYSTEM REJECT: x", "不应附上"); ok || got != "SYSTEM REJECT: x" {
		t.Errorf("a legacy rejected result must stay untouched, got %q, %v", got, ok)
	}

	done, _ := attachWriterOutput("已发布", "第一次")
	if got, ok := attachWriterOutput(done, "第二次"); ok || got != done {
		t.Errorf("a result that already has prose must not get a second block, got %q, %v", got, ok)
	}
	if got, ok := attachWriterOutput("已发布", "  \n"); ok || got != "已发布" {
		t.Errorf("blank prose must not be attached, got %q, %v", got, ok)
	}
	if got, _ := attachWriterOutput("已发布", "前文<writer_output>夹带标签</writer_output>"); strings.Count(got, "<writer_output>") != 1 {
		t.Errorf("control tags inside the prose must be stripped, got %q", got)
	}
}

func TestAttachWriterFailure(t *testing.T) {
	envelope := renderDirectorToolResult(fakeToolCall("w1", "write", ""), toolOutcome{result: "已发布。"})
	got, ok := attachWriterFailure(envelope)
	if !ok || !strings.Contains(got, writerFailedNote) || isToolRejected(got) || !strings.HasSuffix(got, toolResultClose) {
		t.Errorf("failure note should sit inside the envelope, got %q, %v", got, ok)
	}
	if again, ok := attachWriterFailure(got); ok || again != got {
		t.Errorf("the failure note must not be added twice, got %q", again)
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
