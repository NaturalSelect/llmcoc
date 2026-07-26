// NOTE: Implements the OpenAI-compatible LLM provider.
package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// llmDebug controls per-request LLM timing logs (set LLM_DEBUG=1 to enable).
var llmDebug = func() bool {
	return true
}()

const defaultReasoningEffort = "high"

type openAIProvider struct {
	client      *openai.Client
	model       string
	maxTokens   int
	temperature float32
	// disableTemperature 为 true 时不发送 temperature 参数（用于不支持的模型）
	disableTemperature bool
	reasoningEffort    string
	baseURL            string
}

func newOpenAIProvider(apiKey, baseURL, model string, maxTokens int, temperature float32, disableTemperature bool, reasoningEffort string) *openAIProvider {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	if maxTokens == 0 {
		maxTokens = 2048
	}
	// NOTE: 仅在未禁用 temperature 且未指定值时使用默认值
	if !disableTemperature && temperature == 0 {
		temperature = 0.8
	}
	if reasoningEffort == "" {
		reasoningEffort = defaultReasoningEffort
	}
	return &openAIProvider{
		client:             openai.NewClientWithConfig(cfg),
		model:              model,
		maxTokens:          maxTokens,
		temperature:        temperature,
		disableTemperature: disableTemperature,
		reasoningEffort:    reasoningEffort,
		baseURL:            baseURL,
	}
}

func (p *openAIProvider) toOpenAIMessages(msgs []ChatMessage) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, len(msgs))
	for i, m := range msgs {
		out[i] = openai.ChatCompletionMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			ToolCalls:  toOpenAIToolCalls(m.ToolCalls),
		}
	}
	return out
}

func toOpenAITools(tools []ToolDefinition) []openai.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openai.Tool, len(tools))
	for i, t := range tools {
		var params any
		if len(t.Parameters) > 0 {
			params = t.Parameters
		}
		out[i] = openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		}
	}
	return out
}

func toOpenAIToolCalls(calls []ToolCall) []openai.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]openai.ToolCall, len(calls))
	for i, c := range calls {
		out[i] = openai.ToolCall{
			ID:   c.ID,
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      c.Name,
				Arguments: c.Arguments,
			},
		}
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

func (p *openAIProvider) chatCompletionRequest(ctx context.Context, cacheKey string, messages []ChatMessage, json bool, tools []ToolDefinition) openai.ChatCompletionRequest {
	chatReq := openai.ChatCompletionRequest{
		Model:               p.model,
		Messages:            p.toOpenAIMessages(messages),
		MaxCompletionTokens: p.maxTokens,
		ReasoningEffort:     p.reasoningEffort,
	}
	// NOTE: 禁用 temperature 时不设置该参数(部分模型如 o1/o3 不支持)
	if !p.disableTemperature {
		chatReq.Temperature = p.temperature
	}
	if len(tools) > 0 {
		// NOTE: tools 与 json_object 的 response_format 互斥——部分兼容端点同时收到两者会报错，
		// 原生 function calling 场景下模型应通过工具参数返回结构化数据，而非纯文本 JSON。
		chatReq.Tools = toOpenAITools(tools)
	} else if json {
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
		// NOTE: prompt_cache_key 必须按 agent 角色/NPC 实例隔离,避免跨 agent 缓存污染。
		cacheKeyValue := cacheKey
		if cacheKeyValue == "" {
			cacheKeyValue = sessionID
		}
		metadata["prompt_cache_key"] = cacheKeyValue
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

// streamToString 发起流式请求并把所有 delta 拼接为完整文本，用于以流式请求模拟非流式返回。
// NOTE: 部分网关/反向代理对长耗时的非流式请求（尤其是 reasoning_effort=high 的模型）会因
// 响应体迟迟无字节而触发空闲超时；流式请求持续有字节到达，可以规避这类超时，因此
// Chat/JsonChat 内部统一改走流式请求，聚合后再以完整字符串返回，对外行为不变。
func (p *openAIProvider) streamToString(ctx context.Context, chatReq openai.ChatCompletionRequest) (content string, reasoning string, toolCalls []ToolCall, err error) {
	stream, err := p.client.CreateChatCompletionStream(ctx, chatReq)
	if err != nil {
		return "", "", nil, err
	}
	defer stream.Close()

	var contentSB, reasoningSB strings.Builder
	agg := newToolCallAggregator()
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return contentSB.String(), reasoningSB.String(), agg.finish(), nil
		}
		if err != nil {
			return "", "", nil, err
		}
		for _, choice := range resp.Choices {
			contentSB.WriteString(choice.Delta.Content)
			reasoningSB.WriteString(choice.Delta.ReasoningContent)
			agg.add(choice.Delta.ToolCalls)
		}
	}
}

func (p *openAIProvider) chat(ctx context.Context, cacheKey string, messages []ChatMessage, json bool, tools []ToolDefinition) (string, []ToolCall, error) {
	start := time.Now()
	chatReq := p.chatCompletionRequest(ctx, cacheKey, messages, json, tools)
	var result, reasoning string
	var toolCalls []ToolCall
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		start := time.Now()
		result, reasoning, toolCalls, err = p.streamToString(ctx, chatReq)
		log.Printf("Chat model %v using %v\n", p.model, time.Since(start))
		if err == nil || !isRetryableError(err) {
			break
		}
		log.Printf("[llm] Chat attempt %d/%d failed (5xx), retrying in 8s: %v", attempt+1, maxRetries, err)
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(8 * time.Second):
		}
	}
	if err != nil {
		return "", nil, fmt.Errorf("LLM chat error: %w", err)
	}
	// NOTE: 提取reasoning_content用于审计日志
	if reasoning != "" {
		log.Printf("[llm-reasoning] session=%s model=%s len=%d",
			sessionIDFromContext(ctx), p.model, len([]rune(reasoning)))
		if llmDebug {
			log.Printf("[llm-reasoning-full] %s", reasoning)
		}
	}
	if llmDebug {
		elapsed := time.Since(start)
		log.Printf("[llm] Chat done model=%s elapsed=%.0fms response_len=%d",
			p.model, float64(elapsed.Microseconds())/1000, len([]rune(result)))
	}
	return result, toolCalls, nil
}

func (p *openAIProvider) ChatStream(ctx context.Context, cacheKey string, messages []ChatMessage) (<-chan string, <-chan error, error) {
	start := time.Now()
	chatReq := p.chatCompletionRequest(ctx, cacheKey, messages, false, nil)
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
				// NOTE: 捕获流式reasoning token用于审计
				reasoningToken := choice.Delta.ReasoningContent
				if reasoningToken != "" {
					log.Printf("[llm-reasoning-stream] session=%s model=%s token_len=%d token=%s",
						sessionIDFromContext(ctx), p.model, len([]rune(reasoningToken)), reasoningToken)
				}
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

func (p *openAIProvider) Chat(ctx context.Context, cacheKey string, messages []ChatMessage) (msg string, err error) {
	for i := 0; i < 3; i++ {
		msg, _, err = p.chat(ctx, cacheKey, messages, false, nil)
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

func (p *openAIProvider) generateImage(ctx context.Context, prompt string, size string) (string, string, error) {
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

func (p *openAIProvider) GenerateImage(ctx context.Context, prompt string, size string) (string, string, error) {
	for i := 0; i < 30; i++ {
		data, mime, err := p.generateImage(ctx, prompt, size)
		if err != nil {
			log.Printf("[llm] GenerateImage error: %v", err)
			continue
		}
		if data == "" {
			continue
		}
		return data, mime, nil
	}
	return "", "", errors.New("LLM failed to generate image after 30 attempts")
}

var (
	ErrEmptyLLMResponse = errors.New("LLM returned empty response")
)

func (p *openAIProvider) JsonChat(ctx context.Context, cacheKey string, messages []ChatMessage) (string, error) {
	for i := 0; i < 3; i++ {
		msg, _, err := p.chat(ctx, cacheKey, messages, true, nil)
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

// ChatWithTools 发起一次支持原生 tool calling 的对话；tools 非空时作为 function calling
// 候选传给模型。与 Chat/JsonChat 不同，本方法把"模型选择不调用任何工具、也不返回文本"
// 视为一次可重试的空响应，而不是静默吞掉最终错误——调用方（Scripter 工具循环）需要据此
// 区分"服务调用失败"和"模型确实什么都没返回"。
func (p *openAIProvider) ChatWithTools(ctx context.Context, cacheKey string, messages []ChatMessage, tools []ToolDefinition) (ToolChatResult, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		content, toolCalls, err := p.chat(ctx, cacheKey, messages, false, tools)
		if err != nil {
			log.Printf("[llm] ChatWithTools error: %v", err)
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
		return ToolChatResult{}, fmt.Errorf("LLM chat with tools error: %w", lastErr)
	}
	return ToolChatResult{}, ErrEmptyLLMResponse
}
