package agent

import (
	"context"
	"fmt"
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
