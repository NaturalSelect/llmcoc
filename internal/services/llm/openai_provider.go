// NOTE: Implements the OpenAI-compatible LLM provider.
package llm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// llmDebug controls per-request LLM timing logs (set LLM_DEBUG=1 to enable).
var llmDebug = func() bool {
	v := strings.ToLower(os.Getenv("AGENT_DEBUG"))
	return v == "1" || v == "true" || v == "yes"
}()

const defaultReasoningEffort = "xhigh"

type openAIProvider struct {
	client      *openai.Client
	model       string
	maxTokens   int
	temperature float32
}

func newOpenAIProvider(apiKey, baseURL, model string, maxTokens int, temperature float32) *openAIProvider {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	if maxTokens == 0 {
		maxTokens = 2048
	}
	if temperature == 0 {
		temperature = 0.8
	}
	return &openAIProvider{
		client:      openai.NewClientWithConfig(cfg),
		model:       model,
		maxTokens:   maxTokens,
		temperature: temperature,
	}
}

func (p *openAIProvider) toOpenAIMessages(msgs []ChatMessage) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, len(msgs))
	for i, m := range msgs {
		out[i] = openai.ChatCompletionMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

const maxRetries = 20

// isRetryableError checks if the error is a 5xx or transient error worth retrying.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.HTTPStatusCode >= 500 || apiErr.HTTPStatusCode == 429 || apiErr.HTTPStatusCode == 400 || apiErr.HTTPStatusCode == 403
	}
	// Also retry on generic request errors (timeouts, connection resets, etc.)
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		return reqErr.HTTPStatusCode >= 500 || reqErr.HTTPStatusCode == 429 || reqErr.HTTPStatusCode == 400 || reqErr.HTTPStatusCode == 403
	}
	return false
}

func (p *openAIProvider) chat(ctx context.Context, messages []ChatMessage) (string, error) {
	start := time.Now()
	chatReq := openai.ChatCompletionRequest{
		Model:           p.model,
		Messages:        p.toOpenAIMessages(messages),
		MaxTokens:       p.maxTokens,
		Temperature:     p.temperature,
		ReasoningEffort: defaultReasoningEffort,
	}
	var resp openai.ChatCompletionResponse
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err = p.client.CreateChatCompletion(ctx, chatReq)
		if err == nil || !isRetryableError(err) {
			break
		}
		log.Printf("[llm] Chat attempt %d/%d failed (5xx), retrying in 8s: %v", attempt+1, maxRetries, err)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(8 * time.Second):
		}
	}
	if err != nil {
		return "", fmt.Errorf("LLM chat error: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("LLM returned no choices")
	}
	result := resp.Choices[0].Message.Content
	if llmDebug {
		elapsed := time.Since(start)
		log.Printf("[llm] Chat done model=%s elapsed=%.0fms response_len=%d",
			p.model, float64(elapsed.Microseconds())/1000, len([]rune(result)))
	}
	return result, nil
}

func (p *openAIProvider) Chat(ctx context.Context, messages []ChatMessage) (msg string, err error) {
	for i := 0; i < maxRetries; i++ {
		msg, err = p.chat(ctx, messages)
		if err != nil {
			log.Printf("[llm] Chat error: %v", err)
			return "", err
		}
		if msg == "" {
			continue
		}
		break
	}
	return msg, nil
}
