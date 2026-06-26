package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type writerContextProvider struct {
	chatCtx    context.Context
	streamCtx  context.Context
	chatResp   string
	streamResp string
}

func (p *writerContextProvider) Chat(ctx context.Context, messages []llm.ChatMessage) (string, error) {
	p.chatCtx = ctx
	return p.chatResp, nil
}

func (p *writerContextProvider) ChatStream(ctx context.Context, messages []llm.ChatMessage) (<-chan string, <-chan error, error) {
	p.streamCtx = ctx
	tokenCh := make(chan string, 1)
	errCh := make(chan error, 1)
	tokenCh <- p.streamResp
	errCh <- nil
	close(tokenCh)
	close(errCh)
	return tokenCh, errCh, nil
}

func (p *writerContextProvider) JsonChat(ctx context.Context, messages []llm.ChatMessage) (string, error) {
	return p.Chat(ctx, messages)
}

func initWriterTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&models.User{},
		&models.CharacterCard{},
		&models.Scenario{},
		&models.ScenarioGenerationLog{},
		&models.GameSession{},
		&models.SessionPlayer{},
	); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prev
		_ = sqlDB.Close()
	})
}

func cacheWriterProviderForTest(t *testing.T, sessionID uint, provider llm.Provider) {
	t.Helper()
	sessionAgents.Store(sessionID, map[models.AgentRole]agentHandle{
		models.AgentRoleWriter: {provider: provider, enabled: true},
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })
}

func writerGameContextForTest(sessionID uint) GameContext {
	return GameContext{Session: models.GameSession{
		ID: sessionID,
		Players: []models.SessionPlayer{{
			CharacterCard: models.CharacterCard{
				Name:       "调查员",
				Appearance: "灰色风衣",
				Traits:     "谨慎",
			},
		}},
	}}
}

func TestRunWriterStreamInjectsSessionIDFromGameContext(t *testing.T) {
	initWriterTestDB(t)
	const sessionID uint = 424201
	provider := &writerContextProvider{streamResp: "月光落在门厅。"}
	cacheWriterProviderForTest(t, sessionID, provider)

	ctx := context.WithValue(context.Background(), "session", "stale-session")
	text, err := RunWriterStream(ctx, writerGameContextForTest(sessionID), "描述门厅", nil)
	if err != nil {
		t.Fatalf("RunWriterStream failed: %v", err)
	}
	if text != provider.streamResp {
		t.Fatalf("stream text=%q, want %q", text, provider.streamResp)
	}
	if provider.streamCtx == nil {
		t.Fatal("provider did not receive context")
	}
	want := fmt.Sprintf("%v", sessionID)
	if got := provider.streamCtx.Value("session"); got != want {
		t.Fatalf("provider session ctx=%v, want %q", got, want)
	}
}

func TestRunWriterInjectsSessionIDFromGameContext(t *testing.T) {
	initWriterTestDB(t)
	const sessionID uint = 424202
	provider := &writerContextProvider{chatResp: "壁炉里只剩灰烬。"}
	cacheWriterProviderForTest(t, sessionID, provider)

	text, err := RunWriter(context.Background(), writerGameContextForTest(sessionID), "描述壁炉")
	if err != nil {
		t.Fatalf("RunWriter failed: %v", err)
	}
	if text != provider.chatResp {
		t.Fatalf("writer text=%q, want %q", text, provider.chatResp)
	}
	if provider.chatCtx == nil {
		t.Fatal("provider did not receive context")
	}
	want := fmt.Sprintf("%v", sessionID)
	if got := provider.chatCtx.Value("session"); got != want {
		t.Fatalf("provider session ctx=%v, want %q", got, want)
	}
}

// NOTE: multiTokenProvider 将完整响应拆为多个 token 推送,用于测试流式 thinking 过滤。
type multiTokenProvider struct {
	tokens []string
	ctx    context.Context
}

func (p *multiTokenProvider) Chat(ctx context.Context, messages []llm.ChatMessage) (string, error) {
	p.ctx = ctx
	return strings.Join(p.tokens, ""), nil
}

func (p *multiTokenProvider) ChatStream(ctx context.Context, messages []llm.ChatMessage) (<-chan string, <-chan error, error) {
	p.ctx = ctx
	tokenCh := make(chan string, len(p.tokens))
	errCh := make(chan error, 1)
	go func() {
		for _, tok := range p.tokens {
			tokenCh <- tok
		}
		close(tokenCh)
		errCh <- nil
		close(errCh)
	}()
	return tokenCh, errCh, nil
}

func (p *multiTokenProvider) JsonChat(ctx context.Context, messages []llm.ChatMessage) (string, error) {
	return p.Chat(ctx, messages)
}

func TestStreamThinkingFilter_NoThinking(t *testing.T) {
	// NOTE: 没有 thinking 块,全部直接转发。无换行时 feed 不立即输出,eof flush。
	var f streamThinkingFilter
	got := f.feed("月光落在门厅。")
	if got != "" {
		t.Fatalf("feed=%q, want empty (still peeking)", got)
	}
	got = f.eof()
	if got != "月光落在门厅。" {
		t.Fatalf("eof=%q, want %q", got, "月光落在门厅。")
	}
}

func TestStreamThinkingFilter_ThinkingSingleToken(t *testing.T) {
	// NOTE: 整个 thinking+正文 在一个 token 里。
	var f streamThinkingFilter
	got := f.feed("Thinking...\n> reasoning\n正文内容")
	if got != "" {
		t.Fatalf("feed=%q, want empty (still buffering)", got)
	}
	got = f.eof()
	if got != "正文内容" {
		t.Fatalf("eof=%q, want %q", got, "正文内容")
	}
}

func TestStreamThinkingFilter_ThinkingMultiToken(t *testing.T) {
	// NOTE: thinking 块跨多个 token,验证逐步累积后只输出正文。
	var f streamThinkingFilter
	tokens := []string{"Think", "ing...\n", "> reason", "ing text\n", "正文从这里开始"}
	var collected strings.Builder
	for _, tok := range tokens {
		if emit := f.feed(tok); emit != "" {
			collected.WriteString(emit)
		}
	}
	if emit := f.eof(); emit != "" {
		collected.WriteString(emit)
	}
	want := "正文从这里开始"
	if collected.String() != want {
		t.Fatalf("collected=%q, want %q", collected.String(), want)
	}
}

func TestStreamThinkingFilter_NoNewlinePeekEOF(t *testing.T) {
	// NOTE: 整段输出没有换行且不是 Thinking...,eof 应 flush。
	var f streamThinkingFilter
	got := f.feed("普通正文没有换行")
	if got != "" {
		t.Fatalf("feed=%q, want empty (still peeking)", got)
	}
	got = f.eof()
	if got != "普通正文没有换行" {
		t.Fatalf("eof=%q, want %q", got, "普通正文没有换行")
	}
}

func TestStreamThinkingFilter_PeekLimitExceeded(t *testing.T) {
	// NOTE: 累积超过 peekLimit 仍无换行,强制 flush。
	var f streamThinkingFilter
	f.peekLimit = 10
	got := f.feed("这是一段很长的普通正文超过了限制")
	if got != "这是一段很长的普通正文超过了限制" {
		t.Fatalf("feed=%q, want full content", got)
	}
	// NOTE: 此时已是 pass-through,后续 token 直通。
	got2 := f.feed("继续输出")
	if got2 != "继续输出" {
		t.Fatalf("feed=%q, want %q", got2, "继续输出")
	}
}

func TestStreamThinkingFilter_MultipleGTLines(t *testing.T) {
	// NOTE: 多行 > 推理,全部跳过,只输出正文。
	var f streamThinkingFilter
	input := "Thinking...\n> line1\n> line2\n> line3\n正文开始\n第二行正文\n"
	// NOTE: 一次性 feed,模拟单 token。
	got := f.feed(input)
	// NOTE: processBuf 会处理完整行,不完整尾部留 buffer;这里末尾有换行所以全部处理。
	want := "正文开始\n第二行正文\n"
	if got != want {
		t.Fatalf("feed=%q, want %q", got, want)
	}
}

func TestStreamThinkingFilter_SkipTailAtEOF(t *testing.T) {
	// NOTE: 跳块模式下流结束,正文最后一行没有换行收尾。
	var f streamThinkingFilter
	tokens := []string{"Thinking...\n> reason\n正文结尾没有换行"}
	var collected strings.Builder
	for _, tok := range tokens {
		if emit := f.feed(tok); emit != "" {
			collected.WriteString(emit)
		}
	}
	if emit := f.eof(); emit != "" {
		collected.WriteString(emit)
	}
	want := "正文结尾没有换行"
	if collected.String() != want {
		t.Fatalf("collected=%q, want %q", collected.String(), want)
	}
}

func TestStreamThinkingFilter_OnlyThinkingBlock(t *testing.T) {
	// NOTE: 整段输出只有 thinking 块,没有正文。
	var f streamThinkingFilter
	tokens := []string{"Thinking...\n> only reasoning\n> more reasoning\n"}
	var collected strings.Builder
	for _, tok := range tokens {
		if emit := f.feed(tok); emit != "" {
			collected.WriteString(emit)
		}
	}
	if emit := f.eof(); emit != "" {
		collected.WriteString(emit)
	}
	if collected.String() != "" {
		t.Fatalf("collected=%q, want empty", collected.String())
	}
}

func TestRunWriterStream_FilterThinkingBlock(t *testing.T) {
	// NOTE: 端到端测试:RunWriterStream 的 onToken 应只收到正文,buffer 仍含 thinking。
	initWriterTestDB(t)
	const sessionID uint = 424203
	provider := &multiTokenProvider{
		tokens: []string{"Thinking...\n", "> some reasoning\n", "正文内容在这里"},
	}
	cacheWriterProviderForTest(t, sessionID, provider)

	var onTokenOutput strings.Builder
	text, err := RunWriterStream(
		context.Background(),
		writerGameContextForTest(sessionID),
		"描述场景",
		func(token string) { onTokenOutput.WriteString(token) },
	)
	if err != nil {
		t.Fatalf("RunWriterStream failed: %v", err)
	}
	// NOTE: onToken 应只收到正文部分。
	onTokenStr := onTokenOutput.String()
	if onTokenStr != "正文内容在这里" {
		t.Fatalf("onToken output=%q, want %q", onTokenStr, "正文内容在这里")
	}
	// NOTE: text 是 stripThinkingBlock 后的结果,也应只有正文。
	if text != "正文内容在这里" {
		t.Fatalf("RunWriterStream text=%q, want %q", text, "正文内容在这里")
	}
}
