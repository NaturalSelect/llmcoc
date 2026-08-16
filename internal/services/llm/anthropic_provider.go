// NOTE: Implements the Anthropic (Claude) LLM provider.
package llm

import (
	"context"
	"errors"
	"fmt"
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
// Debug 级别打印 stop_reason 及 token 用量(含缓存命中数),既用于验证 cache_control
// 断点是否生效,也用于区分"模型真的没输出"和"输出被截断"(stop_reason=max_tokens)。
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

	content, toolCalls := fromAnthropicMessage(&acc)

	log.Debug("anthropic chat done", "model", p.model, "cache_key", cacheKey, "response_len", len([]rune(content)),
		"tool_calls", len(toolCalls), "stop_reason", acc.StopReason, "input_tokens", acc.Usage.InputTokens,
		"output_tokens", acc.Usage.OutputTokens, "cache_read_tokens", acc.Usage.CacheReadInputTokens,
		"cache_creation_tokens", acc.Usage.CacheCreationInputTokens)

	return content, toolCalls, nil
}

func (p *anthropicProvider) chat(ctx context.Context, cacheKey string, messages []ChatMessage, tools []ToolDefinition) (content string, toolCalls []ToolCall, err error) {
	start := time.Now()
	role := roleFromCacheKey(cacheKey)
	defer func() { recordLatency(role, p.model, "chat", time.Since(start), err) }()

	params, err := p.buildParams(messages, tools)
	if err != nil {
		return "", nil, err
	}
	params.Metadata.UserID.Value = cacheKey // 用于 Anthropic 端的用户分流,不影响缓存键

	for attempt := 0; attempt < maxRetries; attempt++ {
		attemptStart := time.Now()
		content, toolCalls, err = p.streamToResult(ctx, cacheKey, params)
		log.Debug("anthropic chat attempt done", "model", p.model, "attempt", attempt+1,
			"elapsed_ms", float64(time.Since(attemptStart).Microseconds())/1000)
		if err == nil || !isAnthropicRetryableError(err) {
			break
		}
		log.Warn("anthropic chat attempt failed, retrying", "attempt", attempt+1, "max_retries", maxRetries, "err", err)
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return "", nil, err
		case <-time.After(8 * time.Second):
		}
	}
	if err != nil {
		err = fmt.Errorf("anthropic chat error: %w", err)
		return "", nil, err
	}
	return content, toolCalls, nil
}

// Chat 与 OpenAI 分支保持相同的对外语义:3 次空响应重试后即使仍失败也返回 nil error
// (既有行为,调用方据此假定 Chat 几乎不返回错误;本次不修正,避免影响所有调用方)。
func (p *anthropicProvider) Chat(ctx context.Context, cacheKey string, messages []ChatMessage) (msg string, err error) {
	for i := 0; i < 3; i++ {
		msg, _, err = p.chat(ctx, cacheKey, messages, nil)
		if err != nil {
			log.Error("anthropic chat error", "err", err)
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
			log.Error("anthropic json chat error", "err", err)
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
			log.Error("anthropic chat with tools error", "err", err)
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
	role := roleFromCacheKey(cacheKey)
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
		log.Warn("anthropic chat stream attempt failed, retrying", "attempt", attempt+1, "max_retries", maxRetries, "err", s.Err())
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
			recordLatency(role, p.model, "stream", time.Since(start), err)
			errCh <- fmt.Errorf("anthropic chat stream receive error: %w", err)
			return
		}
		recordLatency(role, p.model, "stream", time.Since(start), nil)
		log.Debug("anthropic chat stream done", "role", role, "model", p.model,
			"elapsed_ms", float64(time.Since(start).Microseconds())/1000, "response_len", tokenRunes)
		log.Debug("anthropic chat stream cache", "cache_key", cacheKey, "model", p.model,
			"cache_read_tokens", acc.Usage.CacheReadInputTokens, "cache_creation_tokens", acc.Usage.CacheCreationInputTokens)
	}()
	return tokenCh, errCh, nil
}
