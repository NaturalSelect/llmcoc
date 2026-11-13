// NOTE: writer_test.go 验证 Writer 在响应为空/命中拒绝前缀时丢弃重试的行为,
// 以及流式路径下 writerRefusalGate 不把拒绝文本泄露给玩家。禁止真实网络;使用内联 fake provider。
package agent

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// ── fake provider ──────────────────────────────────────────────────────────

// writerFakeProvider 按序返回预设的 Chat/ChatStream 响应,序列耗尽后返回空字符串。
// chunkSize 控制 ChatStream 把响应切成多大的 rune 块喂给 tokenCh,默认逐字符,
// 用于覆盖 writerRefusalGate 跨多次 feed 累积判定的场景。sentMsgs 记录每次调用
// 实际收到的 msgs 快照,供前缀稳定性测试比对。
type writerFakeProvider struct {
	mu        sync.Mutex
	responses []string
	respIdx   int
	calls     int
	chunkSize int
	sentMsgs  [][]llm.ChatMessage
	// sentTools 记录每次 ChatStream 收到的 historyTools。
	sentTools [][]llm.ToolDefinition
	// streamErrs[i] 非 nil 时，第 i 次 ChatStream 调用先流出 responses[i] 再带着这个错误结束，模拟流中途断开。
	streamErrs []error
}

// next 返回下一个预设响应,并计入一次调用;序列耗尽后持续返回空字符串。
func (p *writerFakeProvider) next(msgs []llm.ChatMessage) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.sentMsgs = append(p.sentMsgs, append([]llm.ChatMessage(nil), msgs...))
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

// msgsAt 返回第i次调用(0-indexed)实际收到的msgs快照。
func (p *writerFakeProvider) msgsAt(i int) []llm.ChatMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sentMsgs[i]
}

func (p *writerFakeProvider) Chat(_ context.Context, _ string, msgs []llm.ChatMessage) (string, error) {
	return p.next(msgs), nil
}

func (p *writerFakeProvider) ChatStream(_ context.Context, _ string, msgs []llm.ChatMessage, historyTools []llm.ToolDefinition) (<-chan string, <-chan error, error) {
	text := p.next(msgs)
	p.mu.Lock()
	p.sentTools = append(p.sentTools, historyTools)
	var streamErr error
	if i := p.calls - 1; i < len(p.streamErrs) {
		streamErr = p.streamErrs[i]
	}
	p.mu.Unlock()
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
		errCh <- streamErr
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

func newWriterNSFWTestHandle(prov llm.Provider, active bool) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleWriterNSFW, IsActive: active},
		enabled:  true,
	}
}

// callAppendWriterStream 以固定的最小请求调用 appendWriterStream:这些测试只关心单次调用内的
// 重试/拒绝判定,不关心上下文内容。
func callAppendWriterStream(ctx context.Context, h agentHandle, state *WriterState, onToken func(string)) error {
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "test-writer-system"},
		{Role: "user", Content: "继续描述"},
	}
	gctx := GameContext{Session: models.GameSession{ID: 1}}
	return appendWriterStream(ctx, h, state, msgs, nil, gctx, false, onToken)
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
		{"另一种中文拒绝前缀不完整不算拒绝", "很抱", false},
		{"正常中文正文", "他推开了吱呀作响的木门。", false},
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

func TestWriterRefusalGate_MatchingSorryChinesePrefixFullySuppressed(t *testing.T) {
	var g writerRefusalGate
	out := feedAllToGate(&g, writerRefusalPrefixes[2]+"相关内容。")
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

// ── appendWriterStream (流式) ──────────────────────────────────────────────────

func TestAppendWriterStream_SuppressesRefusalThenSucceeds(t *testing.T) {
	initSiteSettingTestDB(t)
	prov := &writerFakeProvider{
		chunkSize: 1, // 逐字符喂给gate,充分覆盖累积判定路径
		responses: []string{
			"I cannot fulfill this request. blocked by policy",
			"他缓缓地走向那扇门。",
		},
	}
	h := newWriterTestHandle(prov)
	state := &WriterState{}

	var forwarded strings.Builder
	err := callAppendWriterStream(context.Background(), h, state, func(tok string) {
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
	initSiteSettingTestDB(t)
	prov := &writerFakeProvider{
		chunkSize: 1, // 逐字符喂给gate,充分覆盖多字节前缀的累积判定路径
		responses: []string{
			"我无法完成您的请求。这违反了相关政策",
			"他缓缓地走向那扇门。",
		},
	}
	h := newWriterTestHandle(prov)
	state := &WriterState{}

	var forwarded strings.Builder
	err := callAppendWriterStream(context.Background(), h, state, func(tok string) {
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
	initSiteSettingTestDB(t)
	prov := &writerFakeProvider{
		chunkSize: 4,
		responses: []string{"", "调查员点亮了手中的煤油灯。"},
	}
	h := newWriterTestHandle(prov)
	state := &WriterState{}

	var forwarded strings.Builder
	err := callAppendWriterStream(context.Background(), h, state, func(tok string) {
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
	initSiteSettingTestDB(t)
	prov := &writerFakeProvider{chunkSize: 1} // 序列为空,next() 恒返回 ""
	h := newWriterTestHandle(prov)
	state := &WriterState{}

	var forwarded strings.Builder
	err := callAppendWriterStream(context.Background(), h, state, func(tok string) {
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

// ── pickWriterHandle ──────────────────────────────────────────────────────────

// 流中途 EOF:半截正文不进 Buffer 和 history,通知上层撤回已转发的部分,重新请求后只保留完整的那一份。
func TestAppendWriterStream_MidStreamEOFDiscardsPartialAndRetries(t *testing.T) {
	initSiteSettingTestDB(t)
	old := writerStreamRetryDelay
	writerStreamRetryDelay = 0
	t.Cleanup(func() { writerStreamRetryDelay = old })

	prov := &writerFakeProvider{
		chunkSize:  2,
		responses:  []string{"他缓缓地走向", "他缓缓地走向那扇门。"},
		streamErrs: []error{fantasy.WrapTransportError(io.ErrUnexpectedEOF)},
	}
	h := newWriterTestHandle(prov)
	state := &WriterState{}

	var restarts int
	var forwarded strings.Builder
	ctx := WithWriterRestart(context.Background(), func(int) {
		restarts++
		forwarded.Reset()
	})
	err := callAppendWriterStream(ctx, h, state, func(tok string) {
		forwarded.WriteString(tok)
	})
	if err != nil {
		t.Fatalf("appendWriterStream error: %v", err)
	}
	if restarts != 1 {
		t.Errorf("restart callback count = %d, want 1", restarts)
	}
	if forwarded.String() != "他缓缓地走向那扇门。" {
		t.Errorf("forwarded after restart = %q, want only the complete text", forwarded.String())
	}
	if state.Buffer != "他缓缓地走向那扇门。" {
		t.Errorf("Buffer = %q, want only the complete text without the partial", state.Buffer)
	}
	if got := prov.callCount(); got != 2 {
		t.Errorf("provider call count = %d, want 2", got)
	}
}

// 没有挂回调时(例如非队列路径)重试照常进行,不会 panic。
func TestAppendWriterStream_MidStreamEOFWithoutRestartCallback(t *testing.T) {
	initSiteSettingTestDB(t)
	old := writerStreamRetryDelay
	writerStreamRetryDelay = 0
	t.Cleanup(func() { writerStreamRetryDelay = old })

	prov := &writerFakeProvider{
		chunkSize:  3,
		responses:  []string{"半截", "完整的一段。"},
		streamErrs: []error{io.ErrUnexpectedEOF},
	}
	h := newWriterTestHandle(prov)
	state := &WriterState{}

	if err := callAppendWriterStream(context.Background(), h, state, func(string) {}); err != nil {
		t.Fatalf("appendWriterStream error: %v", err)
	}
	if state.Buffer != "完整的一段。" {
		t.Errorf("Buffer = %q, want %q", state.Buffer, "完整的一段。")
	}
}

// EOF 一直不好时,用满尝试次数后把错误交给上层,已流出的部分正文保留在 Buffer 里。
func TestAppendWriterStream_MidStreamEOFExhaustsAttempts(t *testing.T) {
	initSiteSettingTestDB(t)
	old := writerStreamRetryDelay
	writerStreamRetryDelay = 0
	t.Cleanup(func() { writerStreamRetryDelay = old })

	errs := make([]error, writerMaxGenerateAttempts)
	resps := make([]string, writerMaxGenerateAttempts)
	for i := range errs {
		errs[i] = io.ErrUnexpectedEOF
		resps[i] = "半截"
	}
	prov := &writerFakeProvider{chunkSize: 3, responses: resps, streamErrs: errs}
	h := newWriterTestHandle(prov)
	state := &WriterState{}

	var restarts int
	ctx := WithWriterRestart(context.Background(), func(int) { restarts++ })
	err := callAppendWriterStream(ctx, h, state, func(string) {})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want unexpected EOF after exhausting attempts", err)
	}
	if got := prov.callCount(); got != writerMaxGenerateAttempts {
		t.Errorf("provider call count = %d, want %d", got, writerMaxGenerateAttempts)
	}
	if restarts != writerMaxGenerateAttempts-1 {
		t.Errorf("restart callback count = %d, want %d (no restart after the last attempt)", restarts, writerMaxGenerateAttempts-1)
	}
	if state.Buffer != "半截" {
		t.Errorf("Buffer = %q, want the last partial text kept for the caller", state.Buffer)
	}
}

// 非 EOF 的流错误不重试,保持原行为:部分正文进 Buffer,错误交给上层。
func TestAppendWriterStream_NonEOFStreamErrorIsNotRetried(t *testing.T) {
	initSiteSettingTestDB(t)
	boom := errors.New("boom")
	prov := &writerFakeProvider{
		chunkSize:  3,
		responses:  []string{"半截"},
		streamErrs: []error{boom},
	}
	h := newWriterTestHandle(prov)
	state := &WriterState{}

	var restarts int
	ctx := WithWriterRestart(context.Background(), func(int) { restarts++ })
	err := callAppendWriterStream(ctx, h, state, func(string) {})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if got := prov.callCount(); got != 1 {
		t.Errorf("provider call count = %d, want 1", got)
	}
	if restarts != 0 {
		t.Errorf("restart callback count = %d, want 0", restarts)
	}
	if state.Buffer != "半截" {
		t.Errorf("Buffer = %q, want partial text kept", state.Buffer)
	}
}

// 重试等待期间 ctx 被取消,立即返回 ctx 错误而不是继续请求。
func TestAppendWriterStream_MidStreamEOFStopsOnContextCancel(t *testing.T) {
	initSiteSettingTestDB(t)
	old := writerStreamRetryDelay
	writerStreamRetryDelay = time.Hour
	t.Cleanup(func() { writerStreamRetryDelay = old })

	prov := &writerFakeProvider{
		chunkSize:  3,
		responses:  []string{"半截", "不应该被请求"},
		streamErrs: []error{io.ErrUnexpectedEOF},
	}
	h := newWriterTestHandle(prov)
	state := &WriterState{}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithWriterRestart(ctx, func(int) { cancel() })
	err := callAppendWriterStream(ctx, h, state, func(string) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := prov.callCount(); got != 1 {
		t.Errorf("provider call count = %d, want 1 (no request after cancel)", got)
	}
}

func TestPickWriterHandle(t *testing.T) {
	prov := &writerFakeProvider{}
	writerHandle := newWriterTestHandle(prov)
	nsfwHandle := newWriterNSFWTestHandle(prov, true)
	disabledNSFWHandle := newWriterNSFWTestHandle(prov, false)
	disabledWriterHandle := agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleWriter, IsActive: false},
		enabled:  true,
	}

	cases := []struct {
		name         string
		handles      map[models.AgentRole]agentHandle
		nsfw         bool
		wantRole     models.AgentRole
		wantNSFWMode bool
		wantErr      bool
	}{
		{
			name:         "非NSFW场景使用默认writer",
			handles:      map[models.AgentRole]agentHandle{models.AgentRoleWriter: writerHandle, models.AgentRoleWriterNSFW: nsfwHandle},
			nsfw:         false,
			wantRole:     models.AgentRoleWriter,
			wantNSFWMode: false,
		},
		{
			name:         "NSFW场景且writer_nsfw已启用则路由过去",
			handles:      map[models.AgentRole]agentHandle{models.AgentRoleWriter: writerHandle, models.AgentRoleWriterNSFW: nsfwHandle},
			nsfw:         true,
			wantRole:     models.AgentRoleWriterNSFW,
			wantNSFWMode: true,
		},
		{
			name:         "NSFW场景但writer_nsfw未配置则回落默认writer",
			handles:      map[models.AgentRole]agentHandle{models.AgentRoleWriter: writerHandle},
			nsfw:         true,
			wantRole:     models.AgentRoleWriter,
			wantNSFWMode: false,
		},
		{
			name:         "NSFW场景但writer_nsfw被禁用则回落默认writer",
			handles:      map[models.AgentRole]agentHandle{models.AgentRoleWriter: writerHandle, models.AgentRoleWriterNSFW: disabledNSFWHandle},
			nsfw:         true,
			wantRole:     models.AgentRoleWriter,
			wantNSFWMode: false,
		},
		{
			name:    "默认writer未启用时返回错误",
			handles: map[models.AgentRole]agentHandle{models.AgentRoleWriter: disabledWriterHandle},
			nsfw:    false,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, nsfwMode, err := pickWriterHandle(tc.handles, tc.nsfw)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("pickWriterHandle error: %v", err)
			}
			if got.roleName() != string(tc.wantRole) {
				t.Errorf("role = %q, want %q", got.roleName(), tc.wantRole)
			}
			if nsfwMode != tc.wantNSFWMode {
				t.Errorf("nsfwMode = %v, want %v", nsfwMode, tc.wantNSFWMode)
			}
		})
	}
}

// ── Writer system / 指令 / 请求组装 ──────────────────────────────────────────

// TestWriterSystemPromptFollowsNSFWMode 验证system prompt按本次调用的nsfwMode渲染,
// 而不是房间级EnableNSFW:房间开启NSFW但本次write标记为false时,默认Writer必须拿到全年龄提示词。
func TestWriterSystemPromptFollowsNSFWMode(t *testing.T) {
	initSiteSettingTestDB(t)
	h := newWriterTestHandle(&writerFakeProvider{})

	on := writerSystemPrompt(h, true, false)
	off := writerSystemPrompt(h, false, false)

	for _, sys := range []string{on, off} {
		if strings.Contains(sys, "explicit_scene_requirements") {
			t.Error("system prompt不应包含explicit_scene_requirements,该后缀应拼进末尾的续写指令")
		}
		if strings.Contains(sys, "{{NSFW") {
			t.Errorf("system prompt leaked placeholder:\n%s", sys)
		}
	}
	if !strings.Contains(on, "官能小说风格") || strings.Contains(on, "洛氏小说风格") {
		t.Error("nsfwMode=true时应渲染NSFW开态的基础提示词")
	}
	if !strings.Contains(off, "洛氏小说风格") || strings.Contains(off, "官能小说风格") {
		t.Error("nsfwMode=false时应渲染全年龄的基础提示词,即使房间已开启NSFW")
	}
}

// TestBuildWriterInstruction 验证续写指令:携带调用ID、NSFW后缀只在NSFW模式出现。
func TestBuildWriterInstruction(t *testing.T) {
	gctx := GameContext{Session: models.GameSession{ID: 1, EnableNSFW: true}}

	off := buildWriterInstruction(WriterRequest{Direction: "继续描述", CallID: "call_7"}, gctx, false, false)
	on := buildWriterInstruction(WriterRequest{Direction: "继续描述"}, gctx, true, false)

	if off.Role != "user" {
		t.Errorf("instruction role = %q, want user", off.Role)
	}
	if !strings.Contains(off.Content, `<director_instruction call_id="call_7">`) {
		t.Errorf("instruction should carry the call id:\n%s", off.Content)
	}
	if strings.Contains(off.Content, "explicit_scene_requirements") {
		t.Error("非NSFW模式的续写指令不应包含explicit_scene_requirements后缀")
	}
	if !strings.Contains(on.Content, "<director_instruction>") || strings.Contains(on.Content, "call_id=") {
		t.Errorf("没有调用ID时不应写call_id属性:\n%s", on.Content)
	}
	if !strings.Contains(on.Content, "explicit_scene_requirements") {
		t.Error("NSFW模式的续写指令应包含explicit_scene_requirements后缀")
	}
	for _, c := range []llm.ChatMessage{off, on} {
		for _, want := range []string{"role play 文本", "不要生成 ask_players", "不要点名、提问或给推荐行动", "不要输出任何标签"} {
			if !strings.Contains(c.Content, want) {
				t.Errorf("续写指令应要求只输出 role play 文本，缺少 %q:\n%s", want, c.Content)
			}
		}
	}
}

// TestRunWriterStream_RoomNSFWNonNSFWCallUsesAllAgesPrompt 验证房间开启NSFW、本次write标记为
// false时,实际发给默认Writer的system prompt是全年龄版本。
func TestRunWriterStream_RoomNSFWNonNSFWCallUsesAllAgesPrompt(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 202
	if err := models.DB.Create(&models.GameSession{ID: sessionID, EnableNSFW: true}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	prov := &writerFakeProvider{responses: []string{"他推开了门。"}}
	sessionAgents.Store(uint(sessionID), map[models.AgentRole]agentHandle{
		models.AgentRoleWriter: newWriterTestHandle(prov),
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })

	gctx := GameContext{Session: models.GameSession{ID: sessionID, EnableNSFW: true}}
	if _, err := RunWriterStream(context.Background(), gctx, WriterRequest{Direction: "推门"}, func(string) {}); err != nil {
		t.Fatalf("RunWriterStream: %v", err)
	}
	system := prov.msgsAt(0)[0].Content
	if !strings.Contains(system, "洛氏小说风格") || strings.Contains(system, "官能小说风格") {
		t.Errorf("room NSFW + nsfw=false must render the all-ages Writer prompt, got:\n%s", system)
	}
}

// TestRunWriterStream_SendsDirectorContextAndTools 验证Writer的请求:system换成Writer自己的,
// Director的消息链(含tool_calls/tool结果,以及tool结果里已有的<writer_output>正文)原样保留,不再读库补正文,
// 末尾是续写指令,并且声明了Director的全部工具。
func TestRunWriterStream_SendsDirectorContextAndTools(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 201
	if err := models.DB.Create(&models.GameSession{ID: sessionID}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	// NOTE: 库里有 call_2 的正文，但消息链里 call_2 的结果没有——视图不能再去库里补。
	if err := models.DB.Create(&models.Message{
		SessionID: sessionID, Role: models.MessageRoleAssistant, Kind: models.MessageKindWriter,
		Status: models.MessageStatusDone, Floor: 1, Content: "库里的正文不应出现在请求里",
		Meta: models.JSONField[*models.MessageMeta]{Data: &models.MessageMeta{CallID: "call_2"}},
	}).Error; err != nil {
		t.Fatalf("create writer message: %v", err)
	}
	prov := &writerFakeProvider{responses: []string{"他继续向前走去。"}}
	sessionAgents.Store(uint(sessionID), map[models.AgentRole]agentHandle{
		models.AgentRoleWriter: newWriterTestHandle(prov),
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })

	director := []llm.ChatMessage{
		{Role: "system", Content: "director-system"},
		{Role: "user", Content: "<scenario>模组</scenario>", CacheBreakpoint: true},
		{Role: "assistant", ToolCalls: []llm.ToolCall{fakeToolCall("call_1", "write", `{"direction":"推门"}`)}},
		{Role: "tool", ToolCallID: "call_1", Content: "已发布\n<writer_output>\n他推开了门。\n</writer_output>"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{fakeToolCall("call_2", "write", `{"direction":"继续走"}`)}},
		{Role: "tool", ToolCallID: "call_2", Content: "已发布"},
	}
	gctx := GameContext{Session: models.GameSession{ID: sessionID}}
	req := WriterRequest{Direction: "继续走", CallID: "call_2", Director: director}
	if _, err := RunWriterStream(context.Background(), gctx, req, func(string) {}); err != nil {
		t.Fatalf("RunWriterStream: %v", err)
	}

	msgs := prov.msgsAt(0)
	if len(msgs) != len(director)+1 {
		t.Fatalf("msgs = %d, want director(%d)+instruction", len(msgs), len(director))
	}
	if msgs[0].Role != "system" || msgs[0].Content == "director-system" {
		t.Errorf("msgs[0] should be the Writer system prompt, got %+v", msgs[0])
	}
	if !msgs[1].CacheBreakpoint || msgs[1].Content != "<scenario>模组</scenario>" {
		t.Errorf("scenario message must be kept byte-for-byte with its cache breakpoint, got %+v", msgs[1])
	}
	if len(msgs[2].ToolCalls) != 1 || msgs[2].ToolCalls[0].ID != "call_1" {
		t.Errorf("assistant tool_calls must be preserved, got %+v", msgs[2])
	}
	if want := "已发布\n<writer_output>\n他推开了门。\n</writer_output>"; msgs[3].Content != want {
		t.Errorf("tool result of call_1 = %q, want the prose already in the chain %q", msgs[3].Content, want)
	}
	if msgs[5].Content != "已发布" {
		t.Errorf("tool result of call_2 must stay as the director left it, got %q", msgs[5].Content)
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" || !strings.Contains(last.Content, `call_id="call_2"`) || !strings.Contains(last.Content, "继续走") {
		t.Errorf("last message should be the continuation instruction, got %+v", last)
	}

	prov.mu.Lock()
	defer prov.mu.Unlock()
	if got, want := len(prov.sentTools[0]), len(directorTools(false)); got != want || got == 0 {
		t.Errorf("declared history tools = %d, want the full director tool list (%d)", got, want)
	}
}

// TestWriterPromptAllowsParaphraseAndRequiresInvestigatorSpeech 验证Writer提示词允许对人物发言
// 做同义改写(但不得编造发言)，并要求导演指令里的调查员台词必须在正文里开口说出。
func TestWriterPromptAllowsParaphraseAndRequiresInvestigatorSpeech(t *testing.T) {
	for _, want := range []string{
		"允许同义改写",
		"不得借改写增添、删减或反转发言里的事实",
		"必须在正文里让该调查员开口说出来",
		"调查员的台词必须做同义转写",
		"转写只改说法不改内容",
		"适当使用“哈”“嗯”“啊”",
		"不能单独构成一句新发言",
	} {
		if !strings.Contains(writerDefaultPrompt, want) {
			t.Errorf("writer prompt should contain %q", want)
		}
	}
	if strings.Contains(writerDefaultPrompt, "原话直接引用") {
		t.Error("writer prompt should no longer require quoting speech verbatim")
	}
}

// TestWriteToolAsksDirectorToCarryInvestigatorSpeech 验证write工具说明要求导演把玩家声明的
// 调查员台词搬进direction，导演系统提示词也没有把这类台词一并禁掉。
func TestWriteToolAsksDirectorToCarryInvestigatorSpeech(t *testing.T) {
	desc := writeTool().def.Description
	for _, want := range []string{"【调查员台词】", "只会把direction里写明的内容写成正文", "必须搬进direction"} {
		if !strings.Contains(desc, want) {
			t.Errorf("write tool description should contain %q", want)
		}
	}
	if !strings.Contains(kpSystemPrompt, "玩家本轮已用「」声明的台词不属于虚构") {
		t.Error("kp system prompt should exempt player-declared speech from the write no-fabrication rule")
	}
}

// TestWriterPromptForbidsEchoingControlTags 验证 Writer 提示词明确禁止输出尖括号标签，并说明
// <writer_output> 只是历史里的分隔标记。测试反馈里每一轮正文都带着 <writer_output> 和 <ask_players> 漏给了玩家。
func TestWriterPromptForbidsEchoingControlTags(t *testing.T) {
	for _, want := range []string{
		"输出里不得出现任何尖括号标签",
		"<ask_players>",
		"标签不属于正文",
		"角色扮演(role play)文本写手",
		"只输出这一段 role play 文本本身",
		"不生成 ask_players 点名或提问、不列推荐行动",
		"不点名玩家、不提问、不给推荐行动",
	} {
		if !strings.Contains(writerDefaultPrompt, want) {
			t.Errorf("writer prompt should contain %q", want)
		}
	}
	if strings.Contains(writerDefaultPrompt, "由你之前写出的 <writer_output> 正文") {
		t.Error("writer prompt should no longer present <writer_output> as the name of the prose it writes")
	}
}

// TestWriterPromptKeepsNarrativeConsistent 验证 Writer 提示词不让它编造精确数字、不把失败写成半成功、
// 新人名要带出来历、时刻以导演指令为准。这几条对应测试反馈里正文与摘要数字矛盾、
// 急救失败却写成“勉强止住血”、从未介绍过的人物突然出现、状态栏与叙述时间不一致。
func TestWriterPromptKeepsNarrativeConsistent(t *testing.T) {
	for _, want := range []string{
		"具体数字只来自导演指令",
		"指令没给就不要自己编",
		"失败就写成失败",
		"不得用“勉强”“总算”",
		"人物首次出场要有交代",
		"时刻与天色以导演指令为准",
	} {
		if !strings.Contains(writerDefaultPrompt, want) {
			t.Errorf("writer prompt should contain %q", want)
		}
	}
}

// TestStripWriterControlTags 验证 Writer 把控制标签写进正文时被兜底剥掉：
// 反馈里每一轮都带着整段 <writer_output> 加 <ask_players> 漏给了玩家。
func TestStripWriterControlTags(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"没有标签原样返回", "他推开了门。", "他推开了门。"},
		{"首尾空白被去掉", "\n 他推开了门。 \n", "他推开了门。"},
		{"writer_output 只剥标签保留正文", "<writer_output>\n他推开了门。\n</writer_output>", "他推开了门。"},
		{"ask_players 整段丢弃", "他推开了门。\n<ask_players>你要怎么做？</ask_players>", "他推开了门。"},
		{"两种标签同时出现", "<writer_output>雨落下了。</writer_output><ask_players>你站在二楼走廊上，打算做什么？</ask_players>", "雨落下了。"},
		{"没有闭合的 ask_players 丢到结尾", "雨落下了。\n<ask_players>你站在走廊上", "雨落下了。"},
		{"标签带属性", "雨落下了。<ask_players targets=\"甲\">怎么办？</ask_players>", "雨落下了。"},
		{"大小写不敏感", "雨落下了。<ASK_PLAYERS>怎么办？</ASK_PLAYERS>", "雨落下了。"},
		{"正文里的普通尖括号不动", "他比划着“3 < 5”的手势。", "他比划着“3 < 5”的手势。"},
		{"tool_result 整段丢弃", "他推开了门。\n<tool_result name=\"roll_dice\" status=\"ok\">\n<dice roll=\"42\" hidden=\"true\"/>\n</tool_result>", "他推开了门。"},
		{"没有闭合的 tool_result 丢到结尾", "他推开了门。\n<tool_result name=\"act_npc\" status=\"ok\">\n<ooc visibility=\"kp_only\">秘密", "他推开了门。"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripWriterControlTags(tc.in); got != tc.want {
				t.Errorf("stripWriterControlTags(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestAppendWriterStream_StripsControlTagsFromBuffer 验证流式路径最终落到缓冲里的正文已经剥掉控制标签；
// 只有标签、没有正文的响应按空内容处理，丢弃重试。
func TestAppendWriterStream_StripsControlTagsFromBuffer(t *testing.T) {
	initSiteSettingTestDB(t)
	prov := &writerFakeProvider{
		chunkSize: 4,
		responses: []string{
			"<ask_players>你要怎么做？</ask_players>",
			"<writer_output>雨落在窗上。</writer_output>\n<ask_players>你要怎么做？</ask_players>",
		},
	}
	h := newWriterTestHandle(prov)
	state := &WriterState{}

	if err := callAppendWriterStream(context.Background(), h, state, nil); err != nil {
		t.Fatalf("appendWriterStream error: %v", err)
	}
	if state.Buffer != "雨落在窗上。" {
		t.Errorf("buffer = %q, want tags stripped", state.Buffer)
	}
	if got := prov.callCount(); got != 2 {
		t.Errorf("provider call count = %d, want 2 (tags-only response must be retried)", got)
	}
}

// TestWriterPromptStaysWithinDirection 验证 Writer 提示词把导演指令当任务单：必写不少、范围外不多，
// 不再鼓励“充分发挥想象力”，明确只写最后一条指令、不写其他 write 的内容，续写指令里也有同样的约束。
func TestWriterPromptStaysWithinDirection(t *testing.T) {
	for _, want := range []string{
		"任务/必写/可补/禁止/停在",
		"不多做也不少做",
		"一条不能少",
		"都必须能在必写项里找到",
		"指令没有分段时，把它的每一行都当作必写项",
		"只写最后一条消息里 &lt;director_instruction&gt; 的内容",
		"前面已经写出的，不要重写；还没写的，也不要提前写",
		"不得借细节新增未授权的事件",
		"必写项也不得漏写",
		"篇幅用来展开必写项，不用来增加必写项之外的内容",
	} {
		if !strings.Contains(writerDefaultPrompt, want) {
			t.Errorf("writer prompt should contain %q", want)
		}
	}
	for _, banned := range []string{"充分发挥想象力", "进行想象让人物的动作更生动具体", "宁可多写一层感官细节", "导演指令是要点式的"} {
		if strings.Contains(writerDefaultPrompt, banned) {
			t.Errorf("writer prompt should no longer contain %q", banned)
		}
	}
	gctx := GameContext{Session: models.GameSession{ID: 1}}
	inst := buildWriterInstruction(WriterRequest{Direction: "任务：写推门\n必写：\n- 他推门", CallID: "call_1"}, gctx, false, false)
	for _, want := range []string{"任务单的框架内", "必写项按顺序全部写进", "只在可补范围内补细节", "不增加任务单之外的事件、人物和对白", "不要漏写必写项", "不要写上文里其他 write 调用的内容"} {
		if !strings.Contains(inst.Content, want) {
			t.Errorf("writer instruction should contain %q:\n%s", want, inst.Content)
		}
	}
}

// TestFallbackWriterDirectionUsesTaskSheet 验证自动补写的兜底指令和 write 的 direction 用同一套分段，
// KP 消息放在必写下面，且清掉内部标签；空消息不补写。
func TestFallbackWriterDirectionUsesTaskSheet(t *testing.T) {
	got := fallbackWriterDirection("门开了。")
	for _, want := range []string{"任务：", "必写：\n门开了。", "可补：", "禁止：", "停在："} {
		if !strings.Contains(got, want) {
			t.Errorf("fallback direction should contain %q:\n%s", want, got)
		}
	}
	if fallbackWriterDirection("  ") != "" {
		t.Error("empty KP message should not produce a fallback direction")
	}
}

// TestWriterPromptKeepsThirdPersonAndExplainsNewProps 验证 Writer 提示词固定第三人称、不向读者发问，
// 并要求位置与道具的变化有过渡。对应反馈里正文中途从“林望舒”跳成“你”、凭空冒出一张引水图。
func TestWriterPromptKeepsThirdPersonAndExplainsNewProps(t *testing.T) {
	for _, want := range []string{
		"全文固定第三人称",
		"不得用“你”指代调查员",
		"不要在段末向读者发问",
		"用一句过渡写明他在哪里、东西从哪来",
		"一个问题对应一句回答",
	} {
		if !strings.Contains(writerDefaultPrompt, want) {
			t.Errorf("writer prompt should contain %q", want)
		}
	}
}
