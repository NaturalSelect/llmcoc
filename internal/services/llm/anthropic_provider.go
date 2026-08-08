// NOTE: Implements the Anthropic (Claude) LLM provider.
package llm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

type anthropicProvider struct {
	client      anthropic.Client
	model       string
	maxTokens   int
	temperature float32
	// disableTemperature 为 true 时不发送 temperature 参数,与 OpenAI 分支语义一致
	disableTemperature bool
}

func newAnthropicProvider(apiKey, baseURL, model string, maxTokens int, temperature float32, disableTemperature bool) *anthropicProvider {
	var opts []option.RequestOption
	// NOTE: 只有非空时才显式传 APIKey/BaseURL,留空则让 SDK 使用其默认取值链
	// (ANTHROPIC_API_KEY 环境变量 / 官方 https://api.anthropic.com/ 端点)。
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	// NOTE: 关闭 SDK 自带的重试,统一由 chat()/ChatStream() 的外层重试循环负责,
	// 避免两层重试相乘导致失败耗时暴涨。
	opts = append(opts, option.WithMaxRetries(0))

	if maxTokens == 0 {
		maxTokens = 2048
	}
	if !disableTemperature && temperature == 0 {
		temperature = 0.8
	}

	return &anthropicProvider{
		client:             anthropic.NewClient(opts...),
		model:              model,
		maxTokens:          maxTokens,
		temperature:        temperature,
		disableTemperature: disableTemperature,
	}
}

// clampTemperature 把 temperature 钳制到 Anthropic 的合法区间 [0,1]
// (后台可能存了 OpenAI 风格的 >1 值,直接透传会被 Anthropic 拒绝)。
func clampTemperature(t float32) float64 {
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return float64(t)
}

func (p *anthropicProvider) buildParams(messages []ChatMessage, tools []ToolDefinition) (anthropic.MessageNewParams, error) {
	system, msgs, err := toAnthropicRequest(messages)
	if err != nil {
		return anthropic.MessageNewParams{}, err
	}
	anthropicTools := toAnthropicTools(tools)
	applyCacheBreakpoints(system, anthropicTools, msgs)

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: int64(p.maxTokens),
		System:    system,
		Messages:  msgs,
	}
	if len(anthropicTools) > 0 {
		params.Tools = anthropicTools
	}
	if !p.disableTemperature {
		params.Temperature = anthropic.Float(clampTemperature(p.temperature))
	}
	return params, nil
}

// isAnthropicRetryableError 判断错误是否值得重试(5xx 或与 OpenAI 分支一致的 4xx 子集)。
func isAnthropicRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode >= 500 || retryCode4xx[apiErr.StatusCode]
	}
	return false
}

// streamToResult 发起一次流式请求并用 SDK 内置的 Message.Accumulate 聚合成完整结果;
// llmDebug 开启时额外打印 cache_key 及缓存命中 token 数,用于验证 cache_control 断点是否生效。
func (p *anthropicProvider) streamToResult(ctx context.Context, cacheKey string, params anthropic.MessageNewParams) (string, []ToolCall, error) {
	stream := p.client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	acc := anthropic.Message{}
	for stream.Next() {
		if err := acc.Accumulate(stream.Current()); err != nil {
			return "", nil, err
		}
	}
	if err := stream.Err(); err != nil {
		return "", nil, err
	}

	if llmDebug {
		log.Printf("[llm-cache] provider=anthropic cache_key=%s model=%s cache_read_tokens=%d cache_creation_tokens=%d",
			cacheKey, p.model, acc.Usage.CacheReadInputTokens, acc.Usage.CacheCreationInputTokens)
	}

	content, toolCalls := fromAnthropicMessage(&acc)
	return content, toolCalls, nil
}

func (p *anthropicProvider) chat(ctx context.Context, cacheKey string, messages []ChatMessage, tools []ToolDefinition) (string, []ToolCall, error) {
	params, err := p.buildParams(messages, tools)
	if err != nil {
		return "", nil, err
	}
	params.Metadata.UserID.Value = cacheKey // 用于 Anthropic 端的用户分流,不影响缓存键

	var content string
	var toolCalls []ToolCall
	for attempt := 0; attempt < maxRetries; attempt++ {
		start := time.Now()
		content, toolCalls, err = p.streamToResult(ctx, cacheKey, params)
		log.Printf("[llm] anthropic chat model %v using %v\n", p.model, time.Since(start))
		if err == nil || !isAnthropicRetryableError(err) {
			break
		}
		log.Printf("[llm] anthropic chat attempt %d/%d failed, retrying in 8s: %v", attempt+1, maxRetries, err)
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(8 * time.Second):
		}
	}
	if err != nil {
		return "", nil, fmt.Errorf("anthropic chat error: %w", err)
	}
	return content, toolCalls, nil
}

// Chat 与 OpenAI 分支保持相同的对外语义:3 次空响应重试后即使仍失败也返回 nil error
// (既有行为,调用方据此假定 Chat 几乎不返回错误;本次不修正,避免影响所有调用方)。
func (p *anthropicProvider) Chat(ctx context.Context, cacheKey string, messages []ChatMessage) (msg string, err error) {
	for i := 0; i < 3; i++ {
		msg, _, err = p.chat(ctx, cacheKey, messages, nil)
		if err != nil {
			log.Printf("[llm] anthropic Chat error: %v", err)
			continue
		}
		if msg == "" {
			continue
		}
		break
	}
	return msg, nil
}

// JsonChat 依赖 system/user 提示词里"只输出 JSON"的既有约定,不设任何 Anthropic 专属
// JSON 模式参数(Anthropic 无等价的 response_format);返回前套用 StripCodeFence 兜底
// 剥离常见的 ```json 代码块包裹或前导说明文字。
func (p *anthropicProvider) JsonChat(ctx context.Context, cacheKey string, messages []ChatMessage) (string, error) {
	for i := 0; i < 3; i++ {
		msg, _, err := p.chat(ctx, cacheKey, messages, nil)
		if err != nil {
			log.Printf("[llm] anthropic JsonChat error: %v", err)
			continue
		}
		if msg == "" {
			continue
		}
		return StripCodeFence(msg), nil
	}
	return "", ErrEmptyLLMResponse
}

// ChatWithTools 语义对齐 OpenAI 分支:模型既不返回文本也不请求工具调用时视为可重试的
// 空响应,3 次后返回 ErrEmptyLLMResponse。
func (p *anthropicProvider) ChatWithTools(ctx context.Context, cacheKey string, messages []ChatMessage, tools []ToolDefinition) (ToolChatResult, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		content, toolCalls, err := p.chat(ctx, cacheKey, messages, tools)
		if err != nil {
			log.Printf("[llm] anthropic ChatWithTools error: %v", err)
			lastErr = err
			continue
		}
		if content == "" && len(toolCalls) == 0 {
			lastErr = nil
			continue
		}
		return ToolChatResult{Content: content, ToolCalls: toolCalls}, nil
	}
	if lastErr != nil {
		return ToolChatResult{}, fmt.Errorf("anthropic chat with tools error: %w", lastErr)
	}
	return ToolChatResult{}, ErrEmptyLLMResponse
}

func (p *anthropicProvider) ChatStream(ctx context.Context, cacheKey string, messages []ChatMessage) (<-chan string, <-chan error, error) {
	start := time.Now()
	params, err := p.buildParams(messages, nil)
	if err != nil {
		return nil, nil, err
	}

	// NOTE: NewStreaming 不会像 go-openai 那样在握手失败时直接返回 error,错误要等首次
	// Next()/Err() 才能观察到;这里用一次"探测性" Next() 判断是否需要整体重试。
	var stream *ssestream.Stream[anthropic.MessageStreamEventUnion]
	var hasFirst bool
	for attempt := 0; attempt < maxRetries; attempt++ {
		s := p.client.Messages.NewStreaming(ctx, params)
		stream = s
		hasFirst = s.Next()
		if hasFirst || s.Err() == nil || !isAnthropicRetryableError(s.Err()) {
			break
		}
		log.Printf("[llm] anthropic ChatStream attempt %d/%d failed, retrying in 8s: %v", attempt+1, maxRetries, s.Err())
		s.Close()
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(8 * time.Second):
		}
	}
	if !hasFirst && stream.Err() != nil {
		streamErr := stream.Err()
		stream.Close()
		return nil, nil, fmt.Errorf("anthropic chat stream error: %w", streamErr)
	}

	tokenCh := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(tokenCh)
		defer close(errCh)
		defer stream.Close()

		acc := anthropic.Message{}
		var tokenRunes int

		process := func(ev anthropic.MessageStreamEventUnion) bool {
			_ = acc.Accumulate(ev)
			if ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				tokenRunes += len([]rune(ev.Delta.Text))
				select {
				case tokenCh <- ev.Delta.Text:
				case <-ctx.Done():
					return false
				}
			}
			return true
		}

		if hasFirst && !process(stream.Current()) {
			errCh <- ctx.Err()
			return
		}
		for stream.Next() {
			if !process(stream.Current()) {
				errCh <- ctx.Err()
				return
			}
		}
		if err := stream.Err(); err != nil {
			errCh <- fmt.Errorf("anthropic chat stream receive error: %w", err)
			return
		}
		if llmDebug {
			log.Printf("[llm] anthropic ChatStream done model=%s elapsed=%.0fms response_len=%d",
				p.model, float64(time.Since(start).Microseconds())/1000, tokenRunes)
			log.Printf("[llm-cache] provider=anthropic cache_key=%s model=%s cache_read_tokens=%d cache_creation_tokens=%d",
				cacheKey, p.model, acc.Usage.CacheReadInputTokens, acc.Usage.CacheCreationInputTokens)
		}
	}()
	return tokenCh, errCh, nil
}
