// NOTE: writer_paint_test.go 验证 Writer 的 paint 工具：占位地址与续写循环、非 paint 工具和 NSFW 配图的拒绝、
// 续写轮中途断流只撤回当前轮、轮数上限、正文里图片引用的规整，以及没有 paint 条件时保持原路径。禁止真实网络。
package agent

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

type writerToolRound struct {
	text  string
	calls []llm.ToolCall
	err   error
}

// writerToolFakeProvider 按轮返回预设的文本和工具调用；rounds 用完后重复 repeat（为空则返回空响应）。
// 内嵌 writerFakeProvider，所以没有 paint 条件时仍能走 ChatStream。
type writerToolFakeProvider struct {
	writerFakeProvider
	tmu      sync.Mutex
	rounds   []writerToolRound
	repeat   *writerToolRound
	idx      int
	reqs     [][]llm.ChatMessage
	reqTools [][]llm.ToolDefinition
}

func (p *writerToolFakeProvider) ChatStreamTools(_ context.Context, _ string, msgs []llm.ChatMessage, tools []llm.ToolDefinition, onText func(string)) (llm.ToolChatResult, error) {
	p.tmu.Lock()
	p.reqs = append(p.reqs, append([]llm.ChatMessage(nil), msgs...))
	p.reqTools = append(p.reqTools, tools)
	var r writerToolRound
	switch {
	case p.idx < len(p.rounds):
		r = p.rounds[p.idx]
	case p.repeat != nil:
		r = *p.repeat
	}
	p.idx++
	p.tmu.Unlock()
	for _, ch := range r.text {
		onText(string(ch))
	}
	return llm.ToolChatResult{Content: r.text, ToolCalls: r.calls}, r.err
}

func (p *writerToolFakeProvider) requestCount() int {
	p.tmu.Lock()
	defer p.tmu.Unlock()
	return len(p.reqs)
}

var _ llm.ToolStreamer = (*writerToolFakeProvider)(nil)

type startRecorder struct{ reqs []ImagePromptRequest }

func (r *startRecorder) start(req ImagePromptRequest) string {
	r.reqs = append(r.reqs, req)
	return IllustrationPlaceholder(len(r.reqs))
}

func paintTestHandles() map[models.AgentRole]agentHandle {
	return map[models.AgentRole]agentHandle{
		models.AgentRolePainter: newPainterTestHandle(models.AgentRolePainter, &recordingImageProvider{}, true, false),
	}
}

func paintCall(id, args string) llm.ToolCall { return fakeToolCall(id, writerPaintToolName, args) }

const paintArgs = `{"prompt":"月下的书房","aspect":"landscape","nsfw":false}`

func runPaintLoop(t *testing.T, ctx context.Context, prov *writerToolFakeProvider, gctx GameContext, handles map[models.AgentRole]agentHandle, rec *startRecorder, onToken func(string)) (*WriterState, error) {
	t.Helper()
	h := newWriterTestHandle(prov)
	state := &WriterState{}
	msgs := []llm.ChatMessage{{Role: "system", Content: "test-writer-system"}, {Role: "user", Content: "继续描述"}}
	tools := []llm.ToolDefinition{writerPaintTool()}
	err := appendWriterStreamTools(ctx, h, prov, state, msgs, tools, gctx, handles, rec.start, false, onToken)
	return state, err
}

func TestWriterPaint_PlaceholderFlowAndSeparator(t *testing.T) {
	prov := &writerToolFakeProvider{rounds: []writerToolRound{
		{text: "他推开门。", calls: []llm.ToolCall{paintCall("c1", paintArgs)}},
		{text: "![门后](illust://1)\n\n门后是走廊。"},
	}}
	rec := &startRecorder{}
	var forwarded strings.Builder
	state, err := runPaintLoop(t, context.Background(), prov, GameContext{}, paintTestHandles(), rec, func(s string) { forwarded.WriteString(s) })
	if err != nil {
		t.Fatalf("appendWriterStreamTools: %v", err)
	}

	want := "他推开门。\n\n![门后](illust://1)\n\n门后是走廊。"
	if state.Buffer != want {
		t.Errorf("Buffer = %q, want %q", state.Buffer, want)
	}
	if forwarded.String() != want {
		t.Errorf("forwarded = %q, want %q", forwarded.String(), want)
	}
	if len(rec.reqs) != 1 || rec.reqs[0].Prompt != "月下的书房" || rec.reqs[0].Aspect != "landscape" || rec.reqs[0].NSFW {
		t.Errorf("starter requests = %+v", rec.reqs)
	}
	if got := prov.requestCount(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
	second := prov.reqs[1]
	if len(second) != 4 {
		t.Fatalf("second request has %d messages, want system+user+assistant+tool", len(second))
	}
	if a := second[2]; a.Role != "assistant" || len(a.ToolCalls) != 1 || a.ToolCalls[0].ID != "c1" || a.Content != "他推开门。" {
		t.Errorf("assistant message = %+v", a)
	}
	if tm := second[3]; tm.Role != "tool" || tm.ToolCallID != "c1" || !strings.Contains(tm.Content, "illust://1") {
		t.Errorf("tool message = %+v", tm)
	}
}

func TestWriterPaint_RejectsNonPaintTool(t *testing.T) {
	prov := &writerToolFakeProvider{rounds: []writerToolRound{
		{text: "他推开门。", calls: []llm.ToolCall{fakeToolCall("c1", "write", `{"direction":"x"}`)}},
		{text: "门后是走廊。"},
	}}
	rec := &startRecorder{}
	state, err := runPaintLoop(t, context.Background(), prov, GameContext{}, paintTestHandles(), rec, nil)
	if err != nil {
		t.Fatalf("appendWriterStreamTools: %v", err)
	}
	if len(rec.reqs) != 0 {
		t.Errorf("starter must not be called, got %+v", rec.reqs)
	}
	if tm := prov.reqs[1][3]; !strings.HasPrefix(tm.Content, "SYSTEM REJECT") {
		t.Errorf("tool result = %q, want a rejection", tm.Content)
	}
	if state.Buffer != "他推开门。\n\n门后是走廊。" {
		t.Errorf("Buffer = %q", state.Buffer)
	}
}

func TestWriterPaint_NSFWWithoutNSFWPainterIsRejectedWithoutFallback(t *testing.T) {
	prov := &writerToolFakeProvider{rounds: []writerToolRound{
		{text: "场景。", calls: []llm.ToolCall{paintCall("c1", `{"prompt":"x","nsfw":true}`)}},
		{text: "继续。"},
	}}
	rec := &startRecorder{}
	gctx := GameContext{Session: models.GameSession{EnableNSFW: true}}
	state, err := runPaintLoop(t, context.Background(), prov, gctx, paintTestHandles(), rec, nil)
	if err != nil {
		t.Fatalf("appendWriterStreamTools: %v", err)
	}
	if len(rec.reqs) != 0 {
		t.Errorf("nsfw paint must not fall back to the normal painter, got %+v", rec.reqs)
	}
	if tm := prov.reqs[1][3]; !strings.Contains(tm.Content, "NSFW 配图未启用") {
		t.Errorf("tool result = %q", tm.Content)
	}
	if strings.Contains(state.Buffer, "![") {
		t.Errorf("Buffer must not contain an image, got %q", state.Buffer)
	}
}

func TestWriterPaint_NSFWFlagIgnoredWhenRoomNSFWOff(t *testing.T) {
	prov := &writerToolFakeProvider{rounds: []writerToolRound{
		{text: "场景。", calls: []llm.ToolCall{paintCall("c1", `{"prompt":"x","nsfw":true}`)}},
		{text: "![图](illust://1)"},
	}}
	rec := &startRecorder{}
	if _, err := runPaintLoop(t, context.Background(), prov, GameContext{}, paintTestHandles(), rec, nil); err != nil {
		t.Fatalf("appendWriterStreamTools: %v", err)
	}
	if len(rec.reqs) != 1 || rec.reqs[0].NSFW {
		t.Errorf("room NSFW off must treat nsfw=true as a normal request, got %+v", rec.reqs)
	}
}

func TestWriterPaint_MidStreamEOFRollsBackOnlyCurrentRound(t *testing.T) {
	old := writerStreamRetryDelay
	writerStreamRetryDelay = 0
	t.Cleanup(func() { writerStreamRetryDelay = old })

	prov := &writerToolFakeProvider{rounds: []writerToolRound{
		{text: "他推开门。", calls: []llm.ToolCall{paintCall("c1", paintArgs)}},
		{text: "半截", err: io.ErrUnexpectedEOF},
		{text: "![门后](illust://1)\n\n门后是走廊。"},
	}}
	rec := &startRecorder{}
	var forwarded string
	var keeps []int
	ctx := WithWriterRestart(context.Background(), func(keep int) {
		keeps = append(keeps, keep)
		forwarded = forwarded[:keep]
	})
	state, err := runPaintLoop(t, ctx, prov, GameContext{}, paintTestHandles(), rec, func(s string) { forwarded += s })
	if err != nil {
		t.Fatalf("appendWriterStreamTools: %v", err)
	}

	want := "他推开门。\n\n![门后](illust://1)\n\n门后是走廊。"
	if len(keeps) != 1 || keeps[0] != len("他推开门。") {
		t.Errorf("restart keepBytes = %v, want [%d]", keeps, len("他推开门。"))
	}
	if forwarded != want || state.Buffer != want {
		t.Errorf("forwarded = %q, Buffer = %q, want %q", forwarded, state.Buffer, want)
	}
	if len(rec.reqs) != 1 {
		t.Errorf("the image started in round 1 must not be started again, got %d starts", len(rec.reqs))
	}
}

func TestWriterPaint_NonEOFErrorKeepsPartialText(t *testing.T) {
	boom := io.ErrClosedPipe
	prov := &writerToolFakeProvider{rounds: []writerToolRound{
		{text: "他推开门。", calls: []llm.ToolCall{paintCall("c1", paintArgs)}},
		{text: "半截", err: boom},
	}}
	rec := &startRecorder{}
	state, err := runPaintLoop(t, context.Background(), prov, GameContext{}, paintTestHandles(), rec, nil)
	if err != boom {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if want := "他推开门。\n\n半截\n\n![配图](illust://1)"; state.Buffer != want {
		t.Errorf("Buffer = %q, want %q", state.Buffer, want)
	}
}

func TestWriterPaint_MissingPlaceholderIsAppended(t *testing.T) {
	prov := &writerToolFakeProvider{rounds: []writerToolRound{
		{text: "他推开门。", calls: []llm.ToolCall{paintCall("c1", paintArgs)}},
		{text: "门后是走廊。"},
	}}
	rec := &startRecorder{}
	state, err := runPaintLoop(t, context.Background(), prov, GameContext{}, paintTestHandles(), rec, nil)
	if err != nil {
		t.Fatalf("appendWriterStreamTools: %v", err)
	}
	if want := "他推开门。\n\n门后是走廊。\n\n![配图](illust://1)"; state.Buffer != want {
		t.Errorf("Buffer = %q, want %q", state.Buffer, want)
	}
}

func TestWriterPaint_RoundLimit(t *testing.T) {
	prov := &writerToolFakeProvider{repeat: &writerToolRound{text: "继续。", calls: []llm.ToolCall{paintCall("c", paintArgs)}}}
	rec := &startRecorder{}
	if _, err := runPaintLoop(t, context.Background(), prov, GameContext{}, paintTestHandles(), rec, nil); err != nil {
		t.Fatalf("appendWriterStreamTools: %v", err)
	}
	if got := prov.requestCount(); got != writerMaxToolRounds {
		t.Errorf("requests = %d, want %d", got, writerMaxToolRounds)
	}
	if len(rec.reqs) != writerMaxToolRounds-1 {
		t.Errorf("starts = %d, want %d (the call in the last round is never answered)", len(rec.reqs), writerMaxToolRounds-1)
	}
}

func TestWriterPaint_FirstRoundRefusalIsRetried(t *testing.T) {
	prov := &writerToolFakeProvider{rounds: []writerToolRound{
		{text: "很抱歉，我无法继续。"},
		{text: "他推开了门。"},
	}}
	rec := &startRecorder{}
	var forwarded strings.Builder
	state, err := runPaintLoop(t, context.Background(), prov, GameContext{}, paintTestHandles(), rec, func(s string) { forwarded.WriteString(s) })
	if err != nil {
		t.Fatalf("appendWriterStreamTools: %v", err)
	}
	if forwarded.String() != "他推开了门。" || state.Buffer != "他推开了门。" {
		t.Errorf("forwarded = %q, Buffer = %q, refusal text must never reach the player", forwarded.String(), state.Buffer)
	}
	if got := prov.requestCount(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestNormalizeIllustrationRefs(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		allocated []string
		want      string
	}{
		{"no images", "他推开门。", nil, "他推开门。"},
		{"inline image becomes its own paragraph", "前文![图](illust://1)后文", []string{"illust://1"}, "前文\n\n![图](illust://1)\n\n后文"},
		{"fabricated url removed", "a\n\n![x](https://evil.example/x.png)\n\nb", nil, "a\n\nb"},
		{"unallocated slot removed", "a\n\n![x](illust://9)\n\nb", []string{"illust://1"}, "a\n\nb\n\n![配图](illust://1)"},
		{"duplicate removed", "![a](illust://1)\n\n文\n\n![b](illust://1)", []string{"illust://1"}, "![a](illust://1)\n\n文"},
		{"missing appended in order", "正文", []string{"illust://1", "illust://2"}, "正文\n\n![配图](illust://1)\n\n![配图](illust://2)"},
		{"title is kept", `![x](illust://1 "t")`, []string{"illust://1"}, `![x](illust://1 "t")`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeIllustrationRefs(tt.text, tt.allocated); got != tt.want {
				t.Errorf("normalizeIllustrationRefs(%q, %v) = %q, want %q", tt.text, tt.allocated, got, tt.want)
			}
		})
	}
}

func TestRenderWriterPaint(t *testing.T) {
	off := renderWriterPaint(renderNSFW(writerDefaultPrompt, false), false)
	on := renderWriterPaint(renderNSFW(writerDefaultPrompt, false), true)
	for name, s := range map[string]string{"off": off, "on": on} {
		if strings.Contains(s, "{{WRITER_") {
			t.Errorf("%s prompt leaked a placeholder", name)
		}
	}
	for _, want := range []string{"不要调用任何工具", "代码块、链接和图片；"} {
		if !strings.Contains(off, want) {
			t.Errorf("off prompt lost %q", want)
		}
	}
	if strings.Contains(off, "paint") || strings.Contains(off, "<illustration>") {
		t.Error("off prompt must not mention paint")
	}
	for _, want := range []string{"<illustration>", "唯一可用的工具是 paint", "![一句话说明](地址)", "不要一开篇就配图", "一次只调用一个 paint"} {
		if !strings.Contains(on, want) {
			t.Errorf("on prompt missing %q", want)
		}
	}
	if strings.Contains(on, "不要调用任何工具") {
		t.Error("on prompt must not forbid every tool")
	}
}

func storeWriterPaintHandles(t *testing.T, sessionID uint, prov llm.Provider, withPainter bool) {
	t.Helper()
	handles := map[models.AgentRole]agentHandle{models.AgentRoleWriter: newWriterTestHandle(prov)}
	if withPainter {
		handles[models.AgentRolePainter] = newPainterTestHandle(models.AgentRolePainter, &recordingImageProvider{}, true, false)
	}
	sessionAgents.Store(sessionID, handles)
	t.Cleanup(func() { deleteCachedAgents(sessionID) })
}

func TestRunWriterStream_OffersPaintWhenAvailable(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 301
	if err := models.DB.Create(&models.GameSession{ID: sessionID}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	prov := &writerToolFakeProvider{rounds: []writerToolRound{{text: "他推开了门。"}}}
	storeWriterPaintHandles(t, sessionID, prov, true)

	rec := &startRecorder{}
	ctx := WithWriterIllustrator(context.Background(), rec.start)
	gctx := GameContext{Session: models.GameSession{ID: sessionID}}
	if _, err := RunWriterStream(ctx, gctx, WriterRequest{Direction: "推门"}, func(string) {}); err != nil {
		t.Fatalf("RunWriterStream: %v", err)
	}

	if prov.writerFakeProvider.callCount() != 0 || prov.requestCount() != 1 {
		t.Fatalf("ChatStream calls = %d, ChatStreamTools calls = %d, want 0/1", prov.writerFakeProvider.callCount(), prov.requestCount())
	}
	tools := prov.reqTools[0]
	if len(tools) == 0 || tools[len(tools)-1].Name != writerPaintToolName {
		t.Errorf("paint must be the last declared tool, got %d tools", len(tools))
	}
	if system := prov.reqs[0][0].Content; !strings.Contains(system, "<illustration>") {
		t.Errorf("system prompt should carry the illustration rules:\n%s", system)
	}
	if last := prov.reqs[0][len(prov.reqs[0])-1].Content; !strings.Contains(last, "只有 paint 可以调用") || !strings.Contains(last, "不要一上来就配图") {
		t.Errorf("continuation instruction should allow paint:\n%s", last)
	}
}

func TestRunWriterStream_NoPaintFallsBackToToolChoiceNone(t *testing.T) {
	cases := []struct {
		name        string
		withPainter bool
		illustrator bool
	}{
		{"no illustrator in ctx", true, false},
		{"no painter configured", false, true},
	}
	for i, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			initAgentTestDB(t)
			sessionID := uint(310 + i)
			if err := models.DB.Create(&models.GameSession{ID: sessionID}).Error; err != nil {
				t.Fatalf("create session: %v", err)
			}
			prov := &writerToolFakeProvider{writerFakeProvider: writerFakeProvider{responses: []string{"他推开了门。"}}}
			storeWriterPaintHandles(t, sessionID, prov, tt.withPainter)

			ctx := context.Background()
			if tt.illustrator {
				ctx = WithWriterIllustrator(ctx, (&startRecorder{}).start)
			}
			gctx := GameContext{Session: models.GameSession{ID: sessionID}}
			if _, err := RunWriterStream(ctx, gctx, WriterRequest{Direction: "推门"}, func(string) {}); err != nil {
				t.Fatalf("RunWriterStream: %v", err)
			}

			if prov.requestCount() != 0 || prov.writerFakeProvider.callCount() != 1 {
				t.Fatalf("ChatStreamTools calls = %d, ChatStream calls = %d, want 0/1", prov.requestCount(), prov.writerFakeProvider.callCount())
			}
			for _, tool := range prov.writerFakeProvider.sentTools[0] {
				if tool.Name == writerPaintToolName {
					t.Error("paint must not be declared on the tool_choice=none path")
				}
			}
			if system := prov.writerFakeProvider.msgsAt(0)[0].Content; strings.Contains(system, "<illustration>") || !strings.Contains(system, "不要调用任何工具") {
				t.Errorf("system prompt must stay the original one:\n%s", system)
			}
		})
	}
}
