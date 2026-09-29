// NOTE: writer_test.go 验证 Writer 在响应为空/命中拒绝前缀时丢弃重试的行为,
// 以及流式路径下 writerRefusalGate 不把拒绝文本泄露给玩家。禁止真实网络;使用内联 fake provider。
package agent

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

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

func (p *writerFakeProvider) ChatStream(_ context.Context, _ string, msgs []llm.ChatMessage) (<-chan string, <-chan error, error) {
	text := p.next(msgs)
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

func newWriterNSFWTestHandle(prov llm.Provider, active bool) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleWriterNSFW, IsActive: active},
		enabled:  true,
	}
}

// newTestWriterState 为appendWriter/appendWriterStream的重试/拒绝判定单元测试构造一个
// 全新、未落库的ContextManager;这些测试只关心单次调用内的重试逻辑,不关心跨轮持久化,
// 所以head用固定占位内容即可。
func newTestWriterState(sessionID uint) *WriterState {
	head := []llm.ChatMessage{{Role: "system", Content: "test-writer-system"}}
	return &WriterState{cm: LoadContext(sessionID, writerAgentKey, head, ContextOptions{Window: 100000})}
}

// committedTurns 返回state.cm已提交的历史轮,供测试断言Commit的落库内容。
func committedTurns(state *WriterState) []models.TranscriptTurn {
	return state.cm.data.Turns
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

// ── appendWriter (非流式) ─────────────────────────────────────────────────────

func TestAppendWriter_RetriesOnEmptyThenSucceeds(t *testing.T) {
	initTranslatorTestDB(t)
	prov := &writerFakeProvider{responses: []string{"", "他推开了门。"}}
	h := newWriterTestHandle(prov)
	state := newTestWriterState(1)
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	if err := appendWriter(context.Background(), h, state, "继续描述", gctx, false); err != nil {
		t.Fatalf("appendWriter error: %v", err)
	}
	if state.Buffer != "他推开了门。" {
		t.Errorf("Buffer = %q, want %q", state.Buffer, "他推开了门。")
	}
	turns := committedTurns(state)
	if len(turns) != 1 || len(turns[0].Messages) != 2 || turns[0].Messages[1].Content != "他推开了门。" {
		t.Errorf("已提交的轮 = %+v, want单轮opening+assistant且assistant为最终正文", turns)
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
	state := newTestWriterState(1)
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	if err := appendWriter(context.Background(), h, state, "继续描述", gctx, false); err != nil {
		t.Fatalf("appendWriter error: %v", err)
	}
	if state.Buffer != "她小心翼翼地翻开了那本古书。" {
		t.Errorf("Buffer = %q, 不应包含拒绝文本", state.Buffer)
	}
	for _, turn := range committedTurns(state) {
		for _, m := range turn.Messages {
			if strings.Contains(m.Content, writerRefusalPrefixes[0]) {
				t.Errorf("已提交的轮不应包含拒绝文本: %+v", turn.Messages)
			}
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
	state := newTestWriterState(1)
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	if err := appendWriter(context.Background(), h, state, "继续描述", gctx, false); err != nil {
		t.Fatalf("appendWriter error: %v", err)
	}
	if state.Buffer != "她小心翼翼地翻开了那本古书。" {
		t.Errorf("Buffer = %q, 不应包含拒绝文本", state.Buffer)
	}
	for _, turn := range committedTurns(state) {
		for _, m := range turn.Messages {
			if strings.Contains(m.Content, writerRefusalPrefixes[1]) {
				t.Errorf("已提交的轮不应包含拒绝文本: %+v", turn.Messages)
			}
		}
	}
}

func TestAppendWriter_AllAttemptsRejected_ReturnsErrorAndSkipsHistory(t *testing.T) {
	initTranslatorTestDB(t)
	prov := &writerFakeProvider{} // 序列为空,next() 恒返回 ""
	h := newWriterTestHandle(prov)
	state := newTestWriterState(1)
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	err := appendWriter(context.Background(), h, state, "继续描述", gctx, false)
	if err == nil {
		t.Fatal("全部尝试都被拒绝时应返回错误")
	}
	if turns := committedTurns(state); len(turns) != 0 {
		t.Errorf("被拒绝的响应不应提交为新一轮,got %+v", turns)
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
	state := newTestWriterState(1)
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	var forwarded strings.Builder
	err := appendWriterStream(context.Background(), h, state, "继续描述", gctx, false, func(tok string) {
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
	state := newTestWriterState(1)
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	var forwarded strings.Builder
	err := appendWriterStream(context.Background(), h, state, "继续描述", gctx, false, func(tok string) {
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
	state := newTestWriterState(1)
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	var forwarded strings.Builder
	err := appendWriterStream(context.Background(), h, state, "继续描述", gctx, false, func(tok string) {
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
	state := newTestWriterState(1)
	gctx := GameContext{Session: models.GameSession{ID: 1}}

	var forwarded strings.Builder
	err := appendWriterStream(context.Background(), h, state, "继续描述", gctx, false, func(tok string) {
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

// ── buildWriterHead/buildWriterOpening NSFW后缀 ──────────────────────────────

// TestBuildWriterHeadStableAcrossNSFWMode 验证system prompt(head)不再随每轮nsfwMode
// 变化——只取决于房间级EnableNSFW,保证Writer的prompt cache前缀在NSFW场景切换时不断裂。
func TestBuildWriterHeadStableAcrossNSFWMode(t *testing.T) {
	initTranslatorTestDB(t)
	h := newWriterTestHandle(&writerFakeProvider{})
	gctx := GameContext{Session: models.GameSession{ID: 1, EnableNSFW: true}}

	head := buildWriterHead(h, gctx)
	sysContent := head[0].Content

	if strings.Contains(sysContent, "explicit_scene_requirements") {
		t.Error("system prompt不应再包含explicit_scene_requirements,该后缀现在应拼进本轮user消息尾部")
	}
	if !strings.Contains(sysContent, "官能小说风格") {
		t.Error("房间EnableNSFW已开启时,system prompt应渲染NSFW开态的基础提示词")
	}
}

// TestBuildWriterOpeningNSFWSuffix 验证explicit_scene_requirements后缀现在拼在本轮
// user消息(opening)的尾部,而不是system prompt里。
func TestBuildWriterOpeningNSFWSuffix(t *testing.T) {
	gctx := GameContext{Session: models.GameSession{ID: 1, EnableNSFW: true}}

	openingOff, _ := buildWriterOpening("继续描述", gctx, false)
	openingOn, _ := buildWriterOpening("继续描述", gctx, true)

	if strings.Contains(openingOff.Content, "explicit_scene_requirements") {
		t.Error("非NSFW模式的user消息不应包含explicit_scene_requirements后缀")
	}
	if !strings.Contains(openingOn.Content, "explicit_scene_requirements") {
		t.Error("NSFW模式的user消息应包含explicit_scene_requirements后缀")
	}
}

// TestRunWriter_ContextPrefixStableAcrossTurns 验证Writer跨轮上下文前缀稳定:第2轮
// 发给LLM的msgs,应以"第1轮发送的msgs+其assistant响应"为逐字段相同的前缀(发送版=
// 归档版,不再有"叙事指令:"+direction这份单独的归档内容),且CacheBreakpoint只打在
// head末尾(唯一一条system消息)与上一轮末尾。
func TestRunWriter_ContextPrefixStableAcrossTurns(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 201
	if err := models.DB.Create(&models.GameSession{ID: sessionID}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	prov := &writerFakeProvider{responses: []string{"他推开了门。", "他继续向前走去。"}}
	sessionAgents.Store(uint(sessionID), map[models.AgentRole]agentHandle{
		models.AgentRoleWriter: newWriterTestHandle(prov),
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })

	gctx := GameContext{Session: models.GameSession{ID: sessionID}}
	if _, err := RunWriter(context.Background(), gctx, "继续描述场景一", false); err != nil {
		t.Fatalf("RunWriter turn1: %v", err)
	}
	if _, err := RunWriter(context.Background(), gctx, "继续描述场景二", false); err != nil {
		t.Fatalf("RunWriter turn2: %v", err)
	}
	if got := prov.callCount(); got != 2 {
		t.Fatalf("provider call count = %d, want 2", got)
	}

	turn1Msgs := prov.msgsAt(0)
	turn2Msgs := prov.msgsAt(1)

	wantAssistant := llm.ChatMessage{Role: "assistant", Content: "他推开了门。"}
	wantPrefix := append(append([]llm.ChatMessage(nil), turn1Msgs...), wantAssistant)
	if len(turn2Msgs) < len(wantPrefix) {
		t.Fatalf("turn2Msgs长度%d应不小于期望前缀长度%d", len(turn2Msgs), len(wantPrefix))
	}
	for i, want := range wantPrefix {
		got := turn2Msgs[i]
		got.CacheBreakpoint = false
		want.CacheBreakpoint = false
		if !reflect.DeepEqual(got, want) {
			t.Errorf("turn2Msgs[%d]与期望前缀不一致:\ngot  %+v\nwant %+v", i, got, want)
		}
	}

	// head长度固定为1(仅system);turn1归档的历史长度为2(opening+assistant),所以
	// CacheBreakpoint应恰好落在下标0(head末尾)和下标2(上一轮末尾)。
	for i, m := range turn2Msgs {
		want := i == 0 || i == 2
		if m.CacheBreakpoint != want {
			t.Errorf("turn2Msgs[%d].CacheBreakpoint=%v, want %v", i, m.CacheBreakpoint, want)
		}
	}
}

// ── loadWriterTranscriptHistory (endsession角色成长读取路径) ──────────────────

// TestLoadWriterTranscriptHistory_ReadsFromTranscript 验证endsession.go的角色成长
// 读取路径已经改为从AgentTranscript(writer)取历史,而不是旧的GameSession.WriterHistory列。
func TestLoadWriterTranscriptHistory_ReadsFromTranscript(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 301
	rec := models.AgentTranscript{
		SessionID: sessionID,
		AgentKey:  writerAgentKey,
		Version:   contextTranscriptVersion,
		Data: models.JSONField[models.TranscriptData]{Data: models.TranscriptData{
			NextSeq: 1,
			Turns: []models.TranscriptTurn{{
				Seq: 0,
				Messages: []models.TranscriptMsg{
					{Role: "user", Content: "继续描述场景一"},
					{Role: "assistant", Content: "他推开了门。"},
				},
			}},
		}},
	}
	if err := models.DB.Create(&rec).Error; err != nil {
		t.Fatalf("create transcript: %v", err)
	}

	got := loadWriterTranscriptHistory(sessionID)
	want := []models.ChatMsg{
		{Role: "user", Content: "继续描述场景一"},
		{Role: "assistant", Content: "他推开了门。"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loadWriterTranscriptHistory = %+v, want %+v", got, want)
	}
}

// TestLoadWriterTranscriptHistory_FallsBackToLegacyColumn 验证还没有AgentTranscript
// 记录的老会话(比如结束前从未触发过Writer)时,回落读取旧的GameSession.WriterHistory列。
func TestLoadWriterTranscriptHistory_FallsBackToLegacyColumn(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 302
	legacy := []models.ChatMsg{{Role: "user", Content: "旧数据"}, {Role: "assistant", Content: "旧回复"}}
	session := models.GameSession{ID: sessionID, WriterHistory: models.JSONField[[]models.ChatMsg]{Data: legacy}}
	if err := models.DB.Create(&session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}

	got := loadWriterTranscriptHistory(sessionID)
	if !reflect.DeepEqual(got, legacy) {
		t.Errorf("loadWriterTranscriptHistory = %+v, want %+v", got, legacy)
	}
}
