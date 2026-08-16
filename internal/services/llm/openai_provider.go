// NOTE: Implements the OpenAI-compatible LLM provider.
package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const defaultReasoningEffort = "high"

type openAIProvider struct {
	client      *openai.Client
	apiKey      string
	model       string
	maxTokens   int
	temperature float32
	// disableTemperature 为 true 时不发送 temperature 参数（用于不支持的模型）
	disableTemperature bool
	reasoningEffort    string
	baseURL            string
	// imageViaChat 为 true 时 GenerateImage 改走 /chat/completions 而非 /images/generations，
	// 用于只能通过 Chat 接口调用的画图模型/中转网关。
	imageViaChat bool
}

func newOpenAIProvider(apiKey, baseURL, model string, maxTokens int, temperature float32, disableTemperature bool, reasoningEffort string, imageViaChat bool) *openAIProvider {
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
		apiKey:             apiKey,
		model:              model,
		maxTokens:          maxTokens,
		temperature:        temperature,
		disableTemperature: disableTemperature,
		reasoningEffort:    reasoningEffort,
		baseURL:            baseURL,
		imageViaChat:       imageViaChat,
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
		// NOTE: 让最后一个流式 chunk 携带本次请求的 token 用量，配合 finish_reason
		// 一起写入调试日志，用于区分"模型真的没输出"和"输出被截断"。
		StreamOptions: &openai.StreamOptions{IncludeUsage: true},
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
		log.Debug("using session for prompt cache", "session", sessionID, "model", p.model)
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
// finishReason/usage 仅用于调试日志（见 chat()），不参与重试或返回值判断：
// finish_reason=length 意味着输出被 max_tokens 截断而不是模型没有内容可说，
// 与"确实没有生成任何内容/工具调用"是两种不同的空响应成因。
func (p *openAIProvider) streamToString(ctx context.Context, chatReq openai.ChatCompletionRequest) (content string, reasoning string, toolCalls []ToolCall, finishReason string, usage *openai.Usage, err error) {
	stream, err := p.client.CreateChatCompletionStream(ctx, chatReq)
	if err != nil {
		return "", "", nil, "", nil, err
	}
	defer stream.Close()

	var contentSB, reasoningSB strings.Builder
	agg := newToolCallAggregator()
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return contentSB.String(), reasoningSB.String(), agg.finish(), finishReason, usage, nil
		}
		if err != nil {
			return "", "", nil, "", nil, err
		}
		if resp.Usage != nil {
			usage = resp.Usage
		}
		for _, choice := range resp.Choices {
			contentSB.WriteString(choice.Delta.Content)
			reasoningSB.WriteString(choice.Delta.ReasoningContent)
			agg.add(choice.Delta.ToolCalls)
			if choice.FinishReason != "" {
				finishReason = string(choice.FinishReason)
			}
		}
	}
}

func (p *openAIProvider) chat(ctx context.Context, cacheKey string, messages []ChatMessage, json bool, tools []ToolDefinition) (result string, toolCalls []ToolCall, err error) {
	start := time.Now()
	role := roleFromCacheKey(cacheKey)
	defer func() { recordLatency(role, p.model, "chat", time.Since(start), err) }()

	chatReq := p.chatCompletionRequest(ctx, cacheKey, messages, json, tools)
	var reasoning, finishReason string
	var usage *openai.Usage
	for attempt := 0; attempt < maxRetries; attempt++ {
		attemptStart := time.Now()
		result, reasoning, toolCalls, finishReason, usage, err = p.streamToString(ctx, chatReq)
		log.Debug("chat attempt done", "model", p.model, "attempt", attempt+1, "elapsed_ms", float64(time.Since(attemptStart).Microseconds())/1000)
		if err == nil || !isRetryableError(err) {
			break
		}
		log.Warn("chat attempt failed, retrying", "attempt", attempt+1, "max_retries", maxRetries, "err", err)
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return "", nil, err
		case <-time.After(8 * time.Second):
		}
	}
	if err != nil {
		err = fmt.Errorf("LLM chat error: %w", err)
		return "", nil, err
	}
	// NOTE: 提取reasoning_content用于审计日志
	if reasoning != "" {
		log.Debug("llm reasoning", "session", sessionIDFromContext(ctx), "model", p.model,
			"len", len([]rune(reasoning)), "reasoning", truncateForLog(reasoning, 2000))
	}
	log.Debug("chat done", "role", role, "model", p.model, "elapsed_ms", float64(time.Since(start).Microseconds())/1000,
		"response_len", len([]rune(result)), "tool_calls", len(toolCalls), "finish_reason", finishReason, "usage", usage)
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
		log.Warn("chat stream attempt failed, retrying", "attempt", attempt+1, "max_retries", maxRetries, "err", err)
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(8 * time.Second):
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("LLM chat stream error: %w", err)
	}

	role := roleFromCacheKey(cacheKey)
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
				elapsed := time.Since(start)
				recordLatency(role, p.model, "stream", elapsed, nil)
				log.Debug("chat stream done", "role", role, "model", p.model,
					"elapsed_ms", float64(elapsed.Microseconds())/1000, "response_len", tokenRunes)
				return
			}
			if err != nil {
				recordLatency(role, p.model, "stream", time.Since(start), err)
				errCh <- fmt.Errorf("LLM chat stream receive error: %w", err)
				return
			}
			for _, choice := range resp.Choices {
				token := choice.Delta.Content
				// NOTE: 捕获流式reasoning token用于审计
				reasoningToken := choice.Delta.ReasoningContent
				if reasoningToken != "" {
					log.Debug("llm reasoning stream token", "session", sessionIDFromContext(ctx), "model", p.model,
						"token_len", len([]rune(reasoningToken)), "token", reasoningToken)
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
			log.Error("chat error", "err", err)
			continue
		}
		if msg == "" {
			continue
		}
		break
	}
	return msg, nil
}

// imageModelFamily 归类图片模型支持的参数范围(quality/size 的合法取值因模型而异)。
type imageModelFamily int

const (
	imageModelOther imageModelFamily = iota
	imageModelGptImage
	imageModelDallE3
)

func classifyImageModel(model string) imageModelFamily {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "gpt-image"):
		return imageModelGptImage
	case strings.Contains(m, "dall-e-3"), strings.Contains(m, "dall-e3"):
		return imageModelDallE3
	default:
		return imageModelOther
	}
}

// imageQualityForModel 按模型名选择最高画质参数;不传 quality 时接口会用默认档位
// (dall-e-3 默认 standard、gpt-image-1 默认 auto)，画质会低于预期。
// dall-e-2 不支持 quality 参数,返回空字符串让 omitempty 跳过。
func imageQualityForModel(model string) string {
	switch classifyImageModel(model) {
	case imageModelGptImage:
		return openai.CreateImageQualityHigh
	case imageModelDallE3:
		return openai.CreateImageQualityHD
	default:
		return ""
	}
}

// imageSizeForModel 把 Director 选择的语义化画面方向翻译成具体模型的合法尺寸值。
// 未知模型或未知/空 aspect 一律回落方图 1024x1024——非法尺寸会被 GenerateImage 的
// 30 次重试循环放大成长时间失败,宁可忽略方向也不要发出会被拒绝的尺寸。
func imageSizeForModel(model string, aspect ImageAspect) string {
	family := classifyImageModel(model)
	switch aspect {
	case ImageAspectLandscape:
		switch family {
		case imageModelGptImage:
			return openai.CreateImageSize1536x1024
		case imageModelDallE3:
			return openai.CreateImageSize1792x1024
		}
	case ImageAspectPortrait:
		switch family {
		case imageModelGptImage:
			return openai.CreateImageSize1024x1536
		case imageModelDallE3:
			return openai.CreateImageSize1024x1792
		}
	}
	return openai.CreateImageSize1024x1024
}

func (p *openAIProvider) generateImage(ctx context.Context, prompt string, opts ImageOptions) (string, string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", "", errors.New("image prompt is empty")
	}
	model := strings.TrimSpace(p.model)
	if model == "" {
		return "", "", errors.New("image model is empty")
	}

	if p.imageViaChat {
		return p.generateImageViaChat(ctx, model, prompt)
	}

	resp, err := p.client.CreateImage(ctx, openai.ImageRequest{
		Model:          model,
		Prompt:         prompt,
		N:              1,
		Quality:        imageQualityForModel(model),
		Size:           imageSizeForModel(model, opts.Aspect),
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

func (p *openAIProvider) GenerateImage(ctx context.Context, prompt string, opts ImageOptions) (string, string, error) {
	for i := 0; i < 30; i++ {
		start := time.Now()
		data, mime, err := p.generateImage(ctx, prompt, opts)
		recordLatency("painter", p.model, "image", time.Since(start), err)
		if err != nil {
			log.Error("generate image failed", "attempt", i+1, "err", err)
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
			log.Error("json chat error", "err", err)
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
			log.Error("chat with tools error", "err", err)
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
