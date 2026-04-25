package llm

import (
	"context"
	"encoding/json"
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
		return apiErr.HTTPStatusCode >= 500
	}
	// Also retry on generic request errors (timeouts, connection resets, etc.)
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		return reqErr.HTTPStatusCode >= 500
	}
	return false
}

func (p *openAIProvider) ChatStream(ctx context.Context, messages []ChatMessage) (<-chan string, error) {
	ch := make(chan string, 64)

	req := openai.ChatCompletionRequest{
		Model:           p.model,
		Messages:        p.toOpenAIMessages(messages),
		MaxTokens:       p.maxTokens,
		Temperature:     p.temperature,
		ReasoningEffort: defaultReasoningEffort,
		Stream:          true,
	}

	var stream *openai.ChatCompletionStream
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		stream, err = p.client.CreateChatCompletionStream(ctx, req)
		if err == nil || !isRetryableError(err) {
			break
		}
		log.Printf("[llm] ChatStream attempt %d/%d failed (5xx), retrying in 2s: %v", attempt+1, maxRetries, err)
		select {
		case <-ctx.Done():
			close(ch)
			return ch, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	if err != nil {
		close(ch)
		return ch, fmt.Errorf("LLM stream error: %w", err)
	}

	go func() {
		defer close(ch)
		defer stream.Close()
		for {
			resp, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				ch <- fmt.Sprintf("[ERROR] %s", err.Error())
				return
			}
			if len(resp.Choices) > 0 {
				delta := resp.Choices[0].Delta.Content
				if delta != "" {
					ch <- delta
				}
			}
		}
	}()

	return ch, nil
}

func (p *openAIProvider) Chat(ctx context.Context, messages []ChatMessage) (string, error) {
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
		log.Printf("[llm] Chat attempt %d/%d failed (5xx), retrying in 2s: %v", attempt+1, maxRetries, err)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
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

func (p *openAIProvider) GenerateCharacter(ctx context.Context, req GenerateCharacterReq) (*GeneratedCharacter, error) {
	era := req.Era
	if era == "" {
		era = "1920年代"
	}
	occupation := req.Occupation
	if occupation == "" {
		occupation = "调查员"
	}

	prompt := fmt.Sprintf(`请为克苏鲁神话TRPG（COC第七版）生成一名调查员的详细信息，以JSON格式返回，不要有任何额外文字。

要求：
- 时代背景：%s
- 职业：%s
- 玩家背景提示：%s
- 基础属性已生成：STR=%d CON=%d SIZ=%d DEX=%d APP=%d INT=%d POW=%d EDU=%d

请返回如下JSON格式（所有字段都用中文）：
{
  "backstory": "200字以内的背景故事",
  "appearance": "100字以内的外貌描述",
  "traits": "性格特征与信念，50字以内"
}`,
		era, occupation, req.Background,
		req.Stats.STR, req.Stats.CON, req.Stats.SIZ,
		req.Stats.DEX, req.Stats.APP, req.Stats.INT,
		req.Stats.POW, req.Stats.EDU,
	)

	genReq := openai.ChatCompletionRequest{
		Model: p.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "你是一名克苏鲁神话TRPG专家，只输出JSON，不输出任何其他内容。"},
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		MaxTokens:       800,
		Temperature:     0.9,
		ReasoningEffort: defaultReasoningEffort,
	}
	var resp openai.ChatCompletionResponse
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err = p.client.CreateChatCompletion(ctx, genReq)
		if err == nil || !isRetryableError(err) {
			break
		}
		log.Printf("[llm] GenerateCharacter attempt %d/%d failed (5xx), retrying in 2s: %v", attempt+1, maxRetries, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if err != nil {
		return nil, err
	}

	content := StripCodeFence(resp.Choices[0].Message.Content)

	var out GeneratedCharacter
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, fmt.Errorf("parse LLM response failed: %w (raw: %s)", err, content)
	}
	return &out, nil
}
