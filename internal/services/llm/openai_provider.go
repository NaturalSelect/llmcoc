// NOTE: Implements the OpenAI-compatible LLM provider.
package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
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

const defaultReasoningEffort = "high"

type openAIProvider struct {
	client          *openai.Client
	model           string
	maxTokens       int
	temperature     float32
	reasoningEffort string
	baseURL         string
}

func newOpenAIProvider(apiKey, baseURL, model string, maxTokens int, temperature float32, reasoningEffort string) *openAIProvider {
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
	if reasoningEffort == "" {
		reasoningEffort = defaultReasoningEffort
	}
	return &openAIProvider{
		client:          openai.NewClientWithConfig(cfg),
		model:           model,
		maxTokens:       maxTokens,
		temperature:     temperature,
		reasoningEffort: reasoningEffort,
		baseURL:         baseURL,
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

var retryCode4xx = map[int]bool{
	429: true, // Too Many Requests
	400: true, // Bad Request (e.g. context too long)
	403: true, // Forbidden (e.g. invalid API key or insufficient quota)
	408: true, // Request Timeout
}

func (p *openAIProvider) isGeminiRequest() bool {
	m := strings.ToLower(p.model)
	if strings.Contains(m, "gemini") {
		return true
	}
	u := strings.ToLower(p.baseURL)
	return strings.Contains(u, "generativelanguage") || strings.Contains(u, "googleapis") || strings.Contains(u, "aistudio")
}

func sessionIDFromContext(ctx context.Context) string {
	s := ctx.Value("session")
	if s == nil {
		return ""
	}
	if sid, ok := s.(string); ok {
		return sid
	}
	return ""
}

// isRetryableError checks if the error is a 5xx or transient error worth retrying.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.HTTPStatusCode >= 500 || retryCode4xx[apiErr.HTTPStatusCode]
	}
	// Also retry on generic request errors (timeouts, connection resets, etc.)
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		return reqErr.HTTPStatusCode >= 500 || retryCode4xx[reqErr.HTTPStatusCode]
	}
	return false
}

func (p *openAIProvider) chatCompletionRequest(ctx context.Context, messages []ChatMessage, json bool) openai.ChatCompletionRequest {
	chatReq := openai.ChatCompletionRequest{
		Model:           p.model,
		Messages:        p.toOpenAIMessages(messages),
		MaxTokens:       p.maxTokens,
		Temperature:     p.temperature,
		ReasoningEffort: p.reasoningEffort,
	}
	if json {
		chatReq.ResponseFormat = &openai.ChatCompletionResponseFormat{Type: "json_object"}
	}
	sessionID := sessionIDFromContext(ctx)
	if sessionID != "" {
		chatReq.User = sessionID
		metadata := chatReq.Metadata
		if metadata == nil {
			metadata = make(map[string]string)
		}
		log.Printf("[chat] using session id %v for model %v", sessionID, p.model)
		metadata["coc_session_id"] = sessionID
		metadata["prompt_cache_key"] = sessionID
		chatReq.Metadata = metadata
	}
	if p.isGeminiRequest() {
		chatReq.Store = true
		if chatReq.Metadata == nil {
			chatReq.Metadata = make(map[string]string)
		}
		chatReq.Metadata["cache_mode"] = "prefix"
		chatReq.Metadata["cache_vendor"] = "gemini"
	}
	return chatReq
}

func (p *openAIProvider) chat(ctx context.Context, messages []ChatMessage, json bool) (string, error) {
	start := time.Now()
	chatReq := p.chatCompletionRequest(ctx, messages, json)
	var resp openai.ChatCompletionResponse
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		start := time.Now()
		resp, err = p.client.CreateChatCompletion(ctx, chatReq)
		log.Printf("Chat model %v using %v\n", p.model, time.Since(start))
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

func (p *openAIProvider) ChatStream(ctx context.Context, messages []ChatMessage) (<-chan string, <-chan error, error) {
	start := time.Now()
	chatReq := p.chatCompletionRequest(ctx, messages, false)
	var stream *openai.ChatCompletionStream
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		stream, err = p.client.CreateChatCompletionStream(ctx, chatReq)
		if err == nil || !isRetryableError(err) {
			break
		}
		log.Printf("[llm] ChatStream attempt %d/%d failed, retrying in 8s: %v", attempt+1, maxRetries, err)
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(8 * time.Second):
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("LLM chat stream error: %w", err)
	}

	tokenCh := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(tokenCh)
		defer close(errCh)
		defer stream.Close()

		var tokenRunes int
		for {
			resp, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				if llmDebug {
					elapsed := time.Since(start)
					log.Printf("[llm] ChatStream done model=%s elapsed=%.0fms response_len=%d",
						p.model, float64(elapsed.Microseconds())/1000, tokenRunes)
				}
				return
			}
			if err != nil {
				errCh <- fmt.Errorf("LLM chat stream receive error: %w", err)
				return
			}
			for _, choice := range resp.Choices {
				token := choice.Delta.Content
				if token == "" {
					continue
				}
				tokenRunes += len([]rune(token))
				select {
				case tokenCh <- token:
				case <-ctx.Done():
					errCh <- ctx.Err()
					return
				}
			}
		}
	}()
	return tokenCh, errCh, nil
}

func (p *openAIProvider) Chat(ctx context.Context, messages []ChatMessage) (msg string, err error) {
	for i := 0; i < 3; i++ {
		msg, err = p.chat(ctx, messages, false)
		if err != nil {
			log.Printf("[llm] Chat error: %v", err)
			continue
		}
		if msg == "" {
			continue
		}
		break
	}
	return msg, nil
}

func (p *openAIProvider) GenerateImage(ctx context.Context, prompt string, size string) (string, string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", "", errors.New("image prompt is empty")
	}
	model := strings.TrimSpace(p.model)
	if model == "" {
		return "", "", errors.New("image model is empty")
	}
	if strings.TrimSpace(size) == "" {
		size = openai.CreateImageSize1024x1024
	}

	resp, err := p.client.CreateImage(ctx, openai.ImageRequest{
		Model:          model,
		Prompt:         prompt,
		N:              1,
		Size:           size,
		ResponseFormat: openai.CreateImageResponseFormatB64JSON,
	})
	if err != nil {
		return "", "", fmt.Errorf("LLM image error: %w", err)
	}
	if len(resp.Data) == 0 || strings.TrimSpace(resp.Data[0].B64JSON) == "" {
		return "", "", errors.New("LLM returned no image data")
	}
	return resp.Data[0].B64JSON, "image/png", nil
}

var (
	ErrEmptyLLMResponse = errors.New("LLM returned empty response")
)

func (p *openAIProvider) JsonChat(ctx context.Context, messages []ChatMessage) (string, error) {
	for i := 0; i < 3; i++ {
		msg, err := p.chat(ctx, messages, true)
		if err != nil {
			log.Printf("[llm] JsonChat error: %v", err)
			continue
		}
		if msg == "" {
			continue
		}
		return msg, nil
	}
	return "", ErrEmptyLLMResponse
}
