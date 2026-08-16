// NOTE: writer_test.go 验证 Writer 在响应为空/命中拒绝前缀时丢弃重试的行为,
// 以及流式路径下 writerRefusalGate 不把拒绝文本泄露给玩家。禁止真实网络;使用内联 fake provider。
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// ── fake provider ──────────────────────────────────────────────────────────

// writerFakeProvider 按序返回预设的 Chat/ChatStream 响应,序列耗尽后返回空字符串。
// chunkSize 控制 ChatStream 把响应切成多大的 rune 块喂给 tokenCh,默认逐字符,
// 用于覆盖 writerRefusalGate 跨多次 feed 累积判定的场景。
type writerFakeProvider struct {
	mu        sync.Mutex
	responses []string
	respIdx   int
	calls     int
	chunkSize int
}

// next 返回下一个预设响应,并计入一次调用;序列耗尽后持续返回空字符串。
func (p *writerFakeProvider) next() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.respIdx >= len(p.responses) {
		return ""
	}
	r := p.responses[p.respIdx]
	p.respIdx++
	return r
}

func (p *writerFakeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *writerFakeProvider) Chat(_ context.Context, _ string, _ []llm.ChatMessage) (string, error) {
	return p.next(), nil
}

func (p *writerFakeProvider) ChatStream(_ context.Context, _ string, _ []llm.ChatMessage) (<-chan string, <-chan error, error) {
	text := p.next()
	chunk := p.chunkSize
	if chunk <= 0 {
		chunk = 1
	}
	tokenCh := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(tokenCh)
		runes := []rune(text)
		for i := 0; i < len(runes); i += chunk {
			end := i + chunk
			if end > len(runes) {
				end = len(runes)
			}
			tokenCh <- string(runes[i:end])
		}
		errCh <- nil
	}()
	return tokenCh, errCh, nil
}

func (p *writerFakeProvider) JsonChat(_ context.Context, _ string, _ []llm.ChatMessage) (string, error) {
	return "", nil
}

func (p *writerFakeProvider) ChatWithTools(_ context.Context, _ string, _ []llm.ChatMessage, _ []llm.ToolDefinition) (llm.ToolChatResult, error) {
	return llm.ToolChatResult{}, nil
}

var _ llm.Provider = (*writerFakeProvider)(nil)

func newWriterTestHandle(prov llm.Provider) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleWriter, IsActive: true},
		enabled:  true,
	}
}

// ── isWriterResponseRejected ─────────────────────────────────────────────────

func TestIsWriterResponseRejected(t *testing.T) {
	cases := []struct {
		name string
		resp string
		want bool
	}{
		{"空字符串", "", true},
		{"仅空白", "   \n\t", true},
		{"精确拒绝前缀", "I cannot fulfill this request.", true},
		{"拒绝前缀带后续说明", "I cannot fulfill this request. It violates policy.", true},
		{"前缀不完整不算拒绝", "I cannot fulfill this request", false},
		{"中文拒绝前缀", "我无法完成您的请求。", true},
		{"中文拒绝前缀带后续说明", "我无法完成您的请求。这违反了相关政策。", true},
		{"中文拒绝前缀不完整不算拒绝", "我无法完成您的请求", false},
		{"另一种中文拒绝前缀", "很抱歉，我无法根据指令生成相关内容。", true},
		{"另一种中文拒绝前缀不完整不算拒绝", "很抱歉，我无法根据指令生成", false},
		{"正常中文正文", "他推开了吱呀作响的木门。", false},
		{"thinking块之后是拒绝前缀", "Thinking...\n> reasoning here\n\nI cannot fulfill this request.", true},
		{"thinking块之后是中文拒绝前缀", "Thinking...\n> reasoning here\n\n我无法完成您的请求。", true},
		{"thinking块之后是正常正文", "Thinking...\n> reasoning here\n\n他走进了房间。", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWriterResponseRejected(tc.resp); got != tc.want {
				t.Errorf("isWriterResponseRejected(%q) = %v, want %v", tc.resp, got, tc.want)
			}
		})
	}
}

// ── writerRefusalGate ─────────────────────────────────────────────────────────

// feedAll 把 text 逐字符喂给 gate,拼接所有 feed 返回值,模拟真实流式场景。
func feedAllToGate(g *writerRefusalGate, text string) string {
	var out strings.Builder
	for _, r := range text {
		out.WriteString(g.feed(string(r)))
	}
	return out.String()
}

func TestWriterRefusalGate_ShortNonMatchingFlushedAtEOF(t *testing.T) {
	var g writerRefusalGate
	mid := feedAllToGate(&g, "好的") // 远短于拒绝前缀长度,不会在feed阶段判定
	if mid != "" {
		t.Fatalf("feed阶段不应提前放行,got %q", mid)
	}
	if got := g.eof(); got != "好的" {
		t.Errorf("eof() = %q, want %q", got, "好的")
	}
}

func TestWriterRefusalGate_MatchingPrefixFullySuppressed(t *testing.T) {
	var g writerRefusalGate
	out := feedAllToGate(&g, writerRefusalPrefixes[0]+" because of policy reasons that keep going on.")
	if out != "" {
		t.Errorf("命中拒绝前缀后不应转发任何内容,got %q", out)
	}
	if got := g.eof(); got != "" {
		t.Errorf("eof() after suppress = %q, want empty", got)
	}
}

func TestWriterRefusalGate_MatchingChinesePrefixFullySuppressed(t *testing.T) {
	var g writerRefusalGate
	out := feedAllToGate(&g, writerRefusalPrefixes[1]+"这违反了相关政策。")
	if out != "" {
		t.Errorf("命中中文拒绝前缀后不应转发任何内容,got %q", out)
	}
	if got := g.eof(); got != "" {
		t.Errorf("eof() after suppress = %q, want empty", got)
	}
}

func TestWriterRefusalGate_LongNonMatchingForwardedInFull(t *testing.T) {
	var g writerRefusalGate
	text := "I cannot see the door clearly through the fog, so he steps closer to look again."
	out := feedAllToGate(&g, text)
	out += g.eof()
	if out != text {
		t.Errorf("未命中拒绝前缀的长文本应完整转发,got %q, want %q", out, text)
	}
}

func TestWriterRefusalGate_ForwardsImmediatelyAfterDecision(t *testing.T) {
	var g writerRefusalGate
	if g.state != wrgPeek {
		t.Fatalf("初始状态应为 peek")
	}
	feedAllToGate(&g, "safe content long enough to cross the threshold length easily")
	if g.state != wrgForward {
		t.Fatalf("未命中拒绝前缀后状态应转入 forward,got %d", g.state)
	}
	if got := g.feed("more"); got != "more" {
		t.Errorf("forward状态下应直通,got %q", got)
	}
}

// ── appendWriter (非流式) ─────────────────────────────────────────────────────

func TestAppendWriter_RetriesOnEmptyThenSucceeds(t *testing.T) {
	initTranslatorTestDB(t)
	prov := &writerFakeProvider{responses: []string{"", "他推开了门。"}}
	h := newWriterTestHandle(prov)
	state := &WriterState{}
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	if err := appendWriter(context.Background(), h, state, "继续描述", gctx); err != nil {
		t.Fatalf("appendWriter error: %v", err)
	}
	if state.Buffer != "他推开了门。" {
		t.Errorf("Buffer = %q, want %q", state.Buffer, "他推开了门。")
	}
	if len(state.History) != 2 || state.History[1].Content != "他推开了门。" {
		t.Errorf("History = %+v, want单对user/assistant且assistant为最终正文", state.History)
	}
	if got := prov.callCount(); got != 2 {
		t.Errorf("provider call count = %d, want 2", got)
	}
}

func TestAppendWriter_RetriesOnRefusalThenSucceeds(t *testing.T) {
	initTranslatorTestDB(t)
	prov := &writerFakeProvider{responses: []string{
		"I cannot fulfill this request. blocked by policy",
		"她小心翼翼地翻开了那本古书。",
	}}
	h := newWriterTestHandle(prov)
	state := &WriterState{}
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	if err := appendWriter(context.Background(), h, state, "继续描述", gctx); err != nil {
		t.Fatalf("appendWriter error: %v", err)
	}
	if state.Buffer != "她小心翼翼地翻开了那本古书。" {
		t.Errorf("Buffer = %q, 不应包含拒绝文本", state.Buffer)
	}
	for _, m := range state.History {
		if strings.Contains(m.Content, writerRefusalPrefixes[0]) {
			t.Errorf("History不应包含拒绝文本: %+v", state.History)
		}
	}
}

func TestAppendWriter_RetriesOnChineseRefusalThenSucceeds(t *testing.T) {
	initTranslatorTestDB(t)
	prov := &writerFakeProvider{responses: []string{
		"我无法完成您的请求。这违反了相关政策",
		"她小心翼翼地翻开了那本古书。",
	}}
	h := newWriterTestHandle(prov)
	state := &WriterState{}
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	if err := appendWriter(context.Background(), h, state, "继续描述", gctx); err != nil {
		t.Fatalf("appendWriter error: %v", err)
	}
	if state.Buffer != "她小心翼翼地翻开了那本古书。" {
		t.Errorf("Buffer = %q, 不应包含拒绝文本", state.Buffer)
	}
	for _, m := range state.History {
		if strings.Contains(m.Content, writerRefusalPrefixes[1]) {
			t.Errorf("History不应包含拒绝文本: %+v", state.History)
		}
	}
}

func TestAppendWriter_AllAttemptsRejected_ReturnsErrorAndSkipsHistory(t *testing.T) {
	initTranslatorTestDB(t)
	prov := &writerFakeProvider{} // 序列为空,next() 恒返回 ""
	h := newWriterTestHandle(prov)
	state := &WriterState{}
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	err := appendWriter(context.Background(), h, state, "继续描述", gctx)
	if err == nil {
		t.Fatal("全部尝试都被拒绝时应返回错误")
	}
	if len(state.History) != 0 {
		t.Errorf("被拒绝的响应不应写入History,got %+v", state.History)
	}
	if state.Buffer != "" {
		t.Errorf("被拒绝的响应不应写入Buffer,got %q", state.Buffer)
	}
	if got := prov.callCount(); got != writerMaxGenerateAttempts {
		t.Errorf("provider call count = %d, want %d", got, writerMaxGenerateAttempts)
	}
}

// ── appendWriterStream (流式) ──────────────────────────────────────────────────

func TestAppendWriterStream_SuppressesRefusalThenSucceeds(t *testing.T) {
	initTranslatorTestDB(t)
	prov := &writerFakeProvider{
		chunkSize: 1, // 逐字符喂给gate,充分覆盖累积判定路径
		responses: []string{
			"I cannot fulfill this request. blocked by policy",
			"他缓缓地走向那扇门。",
		},
	}
	h := newWriterTestHandle(prov)
	state := &WriterState{}
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	var forwarded strings.Builder
	err := appendWriterStream(context.Background(), h, state, "继续描述", gctx, func(tok string) {
		forwarded.WriteString(tok)
	})
	if err != nil {
		t.Fatalf("appendWriterStream error: %v", err)
	}
	if forwarded.String() != "他缓缓地走向那扇门。" {
		t.Errorf("forwarded = %q, 不应包含拒绝文本,也不应缺内容", forwarded.String())
	}
	if state.Buffer != "他缓缓地走向那扇门。" {
		t.Errorf("Buffer = %q, want %q", state.Buffer, "他缓缓地走向那扇门。")
	}
	if got := prov.callCount(); got != 2 {
		t.Errorf("provider call count = %d, want 2", got)
	}
}

func TestAppendWriterStream_SuppressesChineseRefusalThenSucceeds(t *testing.T) {
	initTranslatorTestDB(t)
	prov := &writerFakeProvider{
		chunkSize: 1, // 逐字符喂给gate,充分覆盖多字节前缀的累积判定路径
		responses: []string{
			"我无法完成您的请求。这违反了相关政策",
			"他缓缓地走向那扇门。",
		},
	}
	h := newWriterTestHandle(prov)
	state := &WriterState{}
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	var forwarded strings.Builder
	err := appendWriterStream(context.Background(), h, state, "继续描述", gctx, func(tok string) {
		forwarded.WriteString(tok)
	})
	if err != nil {
		t.Fatalf("appendWriterStream error: %v", err)
	}
	if forwarded.String() != "他缓缓地走向那扇门。" {
		t.Errorf("forwarded = %q, 不应包含拒绝文本,也不应缺内容", forwarded.String())
	}
	if state.Buffer != "他缓缓地走向那扇门。" {
		t.Errorf("Buffer = %q, want %q", state.Buffer, "他缓缓地走向那扇门。")
	}
	if got := prov.callCount(); got != 2 {
		t.Errorf("provider call count = %d, want 2", got)
	}
}

func TestAppendWriterStream_EmptyThenSucceeds(t *testing.T) {
	initTranslatorTestDB(t)
	prov := &writerFakeProvider{
		chunkSize: 4,
		responses: []string{"", "调查员点亮了手中的煤油灯。"},
	}
	h := newWriterTestHandle(prov)
	state := &WriterState{}
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	var forwarded strings.Builder
	err := appendWriterStream(context.Background(), h, state, "继续描述", gctx, func(tok string) {
		forwarded.WriteString(tok)
	})
	if err != nil {
		t.Fatalf("appendWriterStream error: %v", err)
	}
	if forwarded.String() != "调查员点亮了手中的煤油灯。" {
		t.Errorf("forwarded = %q, want %q", forwarded.String(), "调查员点亮了手中的煤油灯。")
	}
}

func TestAppendWriterStream_AllAttemptsRejected_ReturnsErrorAndForwardsNothing(t *testing.T) {
	initTranslatorTestDB(t)
	prov := &writerFakeProvider{chunkSize: 1} // 序列为空,next() 恒返回 ""
	h := newWriterTestHandle(prov)
	state := &WriterState{}
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	var forwarded strings.Builder
	err := appendWriterStream(context.Background(), h, state, "继续描述", gctx, func(tok string) {
		forwarded.WriteString(tok)
	})
	if err == nil {
		t.Fatal("全部尝试都被拒绝时应返回错误")
	}
	if forwarded.Len() != 0 {
		t.Errorf("被拒绝的流不应转发任何token,got %q", forwarded.String())
	}
	if got := prov.callCount(); got != writerMaxGenerateAttempts {
		t.Errorf("provider call count = %d, want %d", got, writerMaxGenerateAttempts)
	}
}
