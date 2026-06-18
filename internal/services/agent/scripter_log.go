package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/llmcoc/server/internal/services/llm"
)

type scripterGenerationLogContextKey struct{}

type scripterGenerationLog struct {
	mu sync.Mutex
	sb strings.Builder
}

func newScripterGenerationLog(sessionID string, req ScenarioCreationRequest) *scripterGenerationLog {
	logbook := &scripterGenerationLog{}
	logbook.sb.WriteString("LLM 剧本生成对话记录\n")
	logbook.sb.WriteString(fmt.Sprintf("生成时间: %s\n", time.Now().Format(time.RFC3339)))
	if strings.TrimSpace(sessionID) != "" {
		logbook.sb.WriteString(fmt.Sprintf("生成会话: %s\n", strings.TrimSpace(sessionID)))
	}
	if reqJSON, err := json.MarshalIndent(req, "", "  "); err == nil {
		logbook.sb.WriteString("\n生成请求:\n")
		logbook.sb.Write(reqJSON)
		logbook.sb.WriteString("\n")
	}
	return logbook
}

func contextWithScripterGenerationLog(ctx context.Context, logbook *scripterGenerationLog) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if logbook == nil {
		return ctx
	}
	return context.WithValue(ctx, scripterGenerationLogContextKey{}, logbook)
}

func scripterGenerationLogFromContext(ctx context.Context) *scripterGenerationLog {
	if ctx == nil {
		return nil
	}
	logbook, _ := ctx.Value(scripterGenerationLogContextKey{}).(*scripterGenerationLog)
	return logbook
}

func (l *scripterGenerationLog) appendExchange(stage string, messages []llm.ChatMessage, response string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sb.WriteString("\n============================================================\n")
	l.sb.WriteString(fmt.Sprintf("阶段: %s\n", firstNonEmpty(stage, "未命名 LLM 调用")))
	l.sb.WriteString(fmt.Sprintf("时间: %s\n", time.Now().Format(time.RFC3339)))
	l.sb.WriteString("\n发送给 LLM 的消息:\n")
	for i, msg := range messages {
		role := firstNonEmpty(msg.Role, "unknown")
		l.sb.WriteString(fmt.Sprintf("\n--- message %d / role=%s ---\n", i+1, role))
		l.writeTextBlock(msg.Content)
	}
	l.sb.WriteString("\n--- assistant response ---\n")
	l.writeTextBlock(response)
}

func (l *scripterGenerationLog) writeTextBlock(text string) {
	l.sb.WriteString(strings.TrimRight(text, "\n"))
	l.sb.WriteString("\n")
}

func (l *scripterGenerationLog) text() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.TrimSpace(l.sb.String())
}

func (r *scripterRoom) generationLogText() string {
	if r == nil || r.generationLog == nil {
		return ""
	}
	return r.generationLog.text()
}

func recordScripterLLMExchange(ctx context.Context, room *scripterRoom, stage string, messages []llm.ChatMessage, response string) {
	var logbook *scripterGenerationLog
	if room != nil {
		logbook = room.generationLog
	}
	if logbook == nil {
		logbook = scripterGenerationLogFromContext(ctx)
	}
	if logbook != nil {
		logbook.appendExchange(stage, messages, response)
	}
}
