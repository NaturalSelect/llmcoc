// NOTE: 用 charm.land/fantasy 的 LanguageModel.Generate/Stream 统一实现 Anthropic 与
// OpenAI 兼容两类 provider 的 Chat/ChatStream/JsonChat/ChatWithTools，取代原来分别手写的
// openAIProvider(聊天部分)/anthropicProvider。两者在 token 用量口径、cache breakpoint、
// 思考/推理回放上的差异被封装在 isAnthropic 分支里，对外行为与旧实现保持一致。
// 画图能力与本文件无关，仍由 openai_provider.go 里精简后的 openAIProvider 提供，见
// fantasyOpenAIProvider 的组合方式。
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	fantasyopenai "charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
)

// defaultReasoningEffort 只用于 OpenAI 兼容分支的默认值；Anthropic 分支的 thinkingLevel
// 留空即表示不开启扩展思考，不做默认值填充(与旧 anthropicProvider 行为一致)。
const defaultReasoningEffort = "high"

const maxRetries = 20

// retryCode4xx 是 5xx/网络错误之外，额外认为值得重试的 4xx 状态码集合，语义与旧实现一致：
// 429 请求过多、400 部分网关对上下文过长返回的错误码、403 网关鉴权/额度问题的错误码、
// 408 请求超时。
var retryCode4xx = map[int]bool{
	429: true,
	400: true,
	403: true,
	408: true,
}

// fantasyProvider 用一个 fantasy.LanguageModel 统一承载 Anthropic 与 OpenAI 兼容两类
// provider 的聊天能力。
type fantasyProvider struct {
	lm          fantasy.LanguageModel
	isAnthropic bool
	model       string
	maxTokens   int
	temperature float32
	// disableTemperature 为 true 时不发送 temperature 参数（用于不支持的模型）。
	disableTemperature bool
	// reasoningEffort 在 Anthropic 分支是 thinking_level(none|low|medium|high|xhigh|max)，
	// 在 OpenAI 兼容分支是 reasoning_effort，原样透传给网关。
	reasoningEffort string
	// NOTE: thinkingBudgetTokens > 0 时 Anthropic 分支改用固定预算思考(thinking.enabled + budget_tokens)
	// 而不是自适应思考，供 claude-haiku-4-5 这类不支持自适应思考的模型使用；是否开启思考仍由
	// reasoningEffort 决定。OpenAI 兼容分支忽略该字段。
	thinkingBudgetTokens int
}

func newFantasyProvider(isAnthropic bool, apiKey, baseURL, model string, maxTokens int, temperature float32, disableTemperature bool, reasoningEffort string, thinkingBudgetTokens int) (*fantasyProvider, error) {
	if maxTokens == 0 {
		maxTokens = 2048
	}
	if !disableTemperature && temperature == 0 {
		temperature = 0.8
	}

	var lm fantasy.LanguageModel
	if isAnthropic {
		opts := []anthropic.Option{anthropic.WithHTTPClient(llmHTTPClient)}
		// NOTE: 只有非空时才显式传 APIKey/BaseURL，留空则让 SDK 使用其默认取值链
		// (ANTHROPIC_API_KEY 环境变量 / 官方 https://api.anthropic.com/ 端点)。
		if apiKey != "" {
			opts = append(opts, anthropic.WithAPIKey(apiKey))
		}
		if baseURL != "" {
			opts = append(opts, anthropic.WithBaseURL(baseURL))
		}
		provider, err := anthropic.New(opts...)
		if err != nil {
			return nil, err
		}
		lm, err = provider.LanguageModel(context.Background(), model)
		if err != nil {
			return nil, err
		}
	} else {
		if reasoningEffort == "" {
			reasoningEffort = defaultReasoningEffort
		}
		opts := []openaicompat.Option{openaicompat.WithAPIKey(apiKey), openaicompat.WithHTTPClient(llmHTTPClient)}
		if baseURL != "" {
			opts = append(opts, openaicompat.WithBaseURL(baseURL))
		}
		provider, err := openaicompat.New(opts...)
		if err != nil {
			return nil, err
		}
		lm, err = provider.LanguageModel(context.Background(), model)
		if err != nil {
			return nil, err
		}
	}

	return &fantasyProvider{
		lm:                   lm,
		isAnthropic:          isAnthropic,
		model:                model,
		maxTokens:            maxTokens,
		temperature:          temperature,
		disableTemperature:   disableTemperature,
		reasoningEffort:      reasoningEffort,
		thinkingBudgetTokens: thinkingBudgetTokens,
	}, nil
}

// fantasyOpenAIProvider 在 fantasyProvider 之上追加画图能力，仅用于 OpenAI 兼容 provider——
// Anthropic 不支持画图，对应分支直接返回裸的 *fantasyProvider，llm.ImageGenerator 类型断言
// 会如预期般失败，与旧 anthropicProvider 从不实现 GenerateImage 的行为一致。
type fantasyOpenAIProvider struct {
	*fantasyProvider
	image *openAIProvider
}

func (p *fantasyOpenAIProvider) GenerateImage(ctx context.Context, prompt string, opts ImageOptions) (string, string, error) {
	return p.image.GenerateImage(ctx, prompt, opts)
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

// anthropicEffortFromLevel 把项目内统一的 thinking_level(none|low|medium|high|xhigh|max)
// 映射为 Anthropic 的 output_config.effort；"none"/空/未识别值返回 false 表示不开启思考。
func anthropicEffortFromLevel(level string) (anthropic.Effort, bool) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "low":
		return anthropic.EffortLow, true
	case "medium":
		return anthropic.EffortMedium, true
	case "high":
		return anthropic.EffortHigh, true
	case "xhigh":
		return anthropic.EffortXHigh, true
	case "max":
		return anthropic.EffortMax, true
	default:
		return "", false
	}
}

// toPrompt 把统一的 []ChatMessage 转换为 fantasy.Prompt，并额外返回输入消息里标记了
// CacheBreakpoint 的消息在输出 prompt 中的下标(过滤掉空内容被跳过的消息后的真实下标)，
// 供 applyCacheBreakpoints 在这些位置打显式断点。tool role 消息不在这里合并——fantasy 的
// Anthropic 实现会用 groupIntoBlocks 把连续的 user/tool 消息自动分组进同一条 Anthropic
// user 消息，效果与旧 toAnthropicRequest 手动合并一致。
func (p *fantasyProvider) toPrompt(messages []ChatMessage) (fantasy.Prompt, []int) {
	prompt := make(fantasy.Prompt, 0, len(messages))
	var explicitBreaks []int
	markIfBreakpoint := func(m ChatMessage) {
		if m.CacheBreakpoint {
			explicitBreaks = append(explicitBreaks, len(prompt)-1)
		}
	}
	for _, m := range messages {
		switch m.Role {
		case "system":
			text := strings.TrimSpace(m.Content)
			if text == "" {
				continue
			}
			prompt = append(prompt, fantasy.Message{
				Role:    fantasy.MessageRoleSystem,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: text}},
			})
			markIfBreakpoint(m)

		case "user":
			text := strings.TrimSpace(m.Content)
			if text == "" {
				continue
			}
			prompt = append(prompt, fantasy.Message{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: text}},
			})
			markIfBreakpoint(m)

		case "assistant":
			parts := make([]fantasy.MessagePart, 0, len(m.ReasoningBlocks)+1+len(m.ToolCalls))
			// NOTE: thinking/redacted_thinking 必须排在 assistant 消息内容最前面，且要带上
			// 签名/加密数据，否则 Anthropic 多轮工具调用校验会拒绝或不认这是同一段推理的延续。
			if p.isAnthropic {
				for _, rb := range m.ReasoningBlocks {
					switch rb.Type {
					case "thinking":
						parts = append(parts, fantasy.ReasoningPart{
							Text: rb.Text,
							ProviderOptions: fantasy.ProviderOptions{
								anthropic.Name: &anthropic.ReasoningOptionMetadata{Signature: rb.Signature},
							},
						})
					case "redacted_thinking":
						parts = append(parts, fantasy.ReasoningPart{
							ProviderOptions: fantasy.ProviderOptions{
								anthropic.Name: &anthropic.ReasoningOptionMetadata{RedactedData: rb.Data},
							},
						})
					}
				}
			} else if m.Reasoning != "" {
				parts = append(parts, fantasy.ReasoningPart{Text: m.Reasoning})
			}
			if text := strings.TrimSpace(m.Content); text != "" {
				parts = append(parts, fantasy.TextPart{Text: text})
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, fantasy.ToolCallPart{
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					Input:      toolCallInputJSON(tc.Arguments),
				})
			}
			// NOTE: 既无思考、文本，也无工具调用的 assistant 消息会产生空 content，直接丢弃。
			if len(parts) == 0 {
				continue
			}
			prompt = append(prompt, fantasy.Message{Role: fantasy.MessageRoleAssistant, Content: parts})
			markIfBreakpoint(m)

		case "tool":
			prompt = append(prompt, fantasy.Message{
				Role: fantasy.MessageRoleTool,
				Content: []fantasy.MessagePart{fantasy.ToolResultPart{
					ToolCallID: m.ToolCallID,
					Output:     fantasy.ToolResultOutputContentText{Text: m.Content},
				}},
			})
			markIfBreakpoint(m)
		}
	}
	return prompt, explicitBreaks
}

// toolCallInputJSON 把工具调用的原始 JSON 参数文本转换为 fantasy ToolCallPart.Input 需要的
// 字符串形式；参数为空或不是合法 JSON 时回落成空对象。
func toolCallInputJSON(rawArgs string) string {
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return "{}"
	}
	return trimmed
}

// toFantasyTools 把工具定义转换为 fantasy 的 FunctionTool 列表。
func toFantasyTools(tools []ToolDefinition) []fantasy.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]fantasy.Tool, len(tools))
	for i, t := range tools {
		out[i] = fantasy.FunctionTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: toFantasyInputSchema(t.Parameters),
		}
	}
	return out
}

// toFantasyInputSchema 从 ToolDefinition.Parameters(JSON Schema)构造 FunctionTool.InputSchema。
// NOTE: required 字段必须解到具体的 []string 类型——fantasy 的 Anthropic 实现读取
// InputSchema["required"] 时用的是 `req.([]string)` 类型断言，若这里图省事直接
// json.Unmarshal 进 map[string]any，required 会变成 []any 导致断言失败、required 字段被
// 静默丢弃。openai 兼容分支则会把整个 map 原样当 JSON Schema 发出去，所以额外带上 type。
func toFantasyInputSchema(params json.RawMessage) map[string]any {
	schema := struct {
		Type       string   `json:"type"`
		Properties any      `json:"properties"`
		Required   []string `json:"required"`
	}{Type: "object", Properties: map[string]any{}}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &schema)
	}
	if schema.Type == "" {
		schema.Type = "object"
	}
	return map[string]any{
		"type":       schema.Type,
		"properties": schema.Properties,
		"required":   schema.Required,
	}
}

// anthropicCacheControlOptions 构造一个 ephemeral cache breakpoint 的 provider options。
func anthropicCacheControlOptions() fantasy.ProviderOptions {
	return anthropic.NewProviderCacheControlOptions(&anthropic.ProviderCacheControlOptions{
		CacheControl: anthropic.CacheControl{Type: "ephemeral"},
	})
}

// applyCacheBreakpoints 在 Anthropic 请求上标注 cache_control 断点(最多 4 个，Anthropic
// 单次请求上限)。调用方(通过 ChatMessage.CacheBreakpoint，见 toPrompt)显式标记了断点
// 位置时——用于 ContextManager 这类需要跨调用保持前缀字节稳定的场景——只在 system 最后
// 一条、显式标记(最多取最后 2 个)、以及 prompt 末尾一条上打点，tools 不再单独打点
// (system 断点已覆盖 tools：Anthropic 的请求前缀顺序是 tools→system→messages)。没有
// 显式标记时(Scripter/Lawyer 等一次性多轮工具循环的旧调用方式)保持原有启发式：system
// 最后一条+tools 最后一个+prompt 末尾最多两个"连续 user/tool 消息段"各自的最后一条，
// 行为不变。fantasy 会把连续的 user/tool 消息自动分组成一条 Anthropic user 消息
// (groupIntoBlocks)，所以启发式分支里按"下一条是否仍是 user/tool"判断一条消息是否是
// 其所在分组的最后一条，等价于旧实现按"合并后的 Anthropic 消息列表"定位。
func applyCacheBreakpoints(prompt fantasy.Prompt, tools []fantasy.Tool, explicitBreaks []int) {
	lastSystem := -1
	for i, m := range prompt {
		if m.Role == fantasy.MessageRoleSystem {
			lastSystem = i
		}
	}
	if lastSystem >= 0 {
		prompt[lastSystem].ProviderOptions = anthropicCacheControlOptions()
	}

	if len(explicitBreaks) > 0 {
		start := 0
		if len(explicitBreaks) > 2 {
			start = len(explicitBreaks) - 2
		}
		for _, idx := range explicitBreaks[start:] {
			if idx >= 0 && idx < len(prompt) {
				prompt[idx].ProviderOptions = anthropicCacheControlOptions()
			}
		}
		if last := len(prompt) - 1; last >= 0 {
			prompt[last].ProviderOptions = anthropicCacheControlOptions()
		}
		return
	}

	if len(tools) > 0 {
		if ft, ok := tools[len(tools)-1].(fantasy.FunctionTool); ok {
			ft.ProviderOptions = anthropicCacheControlOptions()
			tools[len(tools)-1] = ft
		}
	}

	marked := 0
	for i := len(prompt) - 1; i >= 0 && marked < 2; i-- {
		role := prompt[i].Role
		if role != fantasy.MessageRoleUser && role != fantasy.MessageRoleTool {
			continue
		}
		if i+1 < len(prompt) {
			nextRole := prompt[i+1].Role
			if nextRole == fantasy.MessageRoleUser || nextRole == fantasy.MessageRoleTool {
				continue // 不是所在分组的最后一条，跳过
			}
		}
		prompt[i].ProviderOptions = anthropicCacheControlOptions()
		marked++
	}
}

// buildProviderOptions 构造一次调用的 provider 专属选项：Anthropic 分支处理扩展思考
// (自适应 effort 或固定 budget)与用户分流 metadata；OpenAI 兼容分支处理 reasoning_effort、session 级 prompt cache key，
// 以及 tools 与 json_object 互斥的 response_format。
func (p *fantasyProvider) buildProviderOptions(ctx context.Context, cacheKey string, jsonMode, hasTools bool) fantasy.ProviderOptions {
	if p.isAnthropic {
		opts := &anthropic.ProviderOptions{}
		if effort, ok := anthropicEffortFromLevel(p.reasoningEffort); ok {
			if p.thinkingBudgetTokens > 0 {
				// NOTE: fantasy 在 Effort 非空时强制走自适应思考，固定预算模式必须不带 Effort。
				opts.Thinking = &anthropic.ThinkingProviderOption{BudgetTokens: int64(p.thinkingBudgetTokens)}
			} else {
				opts.Effort = &effort
			}
		}
		if cacheKey != "" {
			// 用于 Anthropic 端的用户分流，不影响缓存键。
			opts.ExtraBody = map[string]any{"metadata": map[string]string{"user_id": cacheKey}}
		}
		return anthropic.NewProviderOptions(opts)
	}

	opts := &openaicompat.ProviderOptions{}
	if p.reasoningEffort != "" {
		effort := fantasyopenai.ReasoningEffort(p.reasoningEffort)
		opts.ReasoningEffort = &effort
	}

	extraBody := map[string]any{}
	metadata := map[string]string{}
	if sessionID := sessionIDFromContext(ctx); sessionID != "" {
		opts.User = &sessionID
		log.Debug("using session for prompt cache", "session", sessionID, "model", p.model)
		// NOTE: prompt_cache_key 必须按 agent 角色/NPC 实例隔离，避免跨 agent 缓存污染。
		cacheKeyValue := cacheKey
		if cacheKeyValue == "" {
			cacheKeyValue = sessionID
		}
		metadata["prompt_cache_key"] = cacheKeyValue
	}
	if len(metadata) > 0 {
		extraBody["metadata"] = metadata
	}
	if !hasTools && jsonMode {
		// NOTE: tools 与 json_object 的 response_format 互斥，原生 function calling 场景下
		// 模型应通过工具参数返回结构化数据，而非纯文本 JSON。
		extraBody["response_format"] = map[string]string{"type": "json_object"}
	}
	if len(extraBody) > 0 {
		opts.ExtraBody = extraBody
	}
	return openaicompat.NewProviderOptions(opts)
}

// buildCall 组装一次完整的 fantasy.Call。thinking 开启时(仅 Anthropic)跳过 Temperature——
// Anthropic 扩展思考与自定义 temperature 互斥，开启思考后传 temperature 会被 API 拒绝。
func (p *fantasyProvider) buildCall(ctx context.Context, cacheKey string, messages []ChatMessage, jsonMode bool, tools []ToolDefinition) fantasy.Call {
	prompt, explicitBreaks := p.toPrompt(messages)
	fantasyTools := toFantasyTools(tools)

	thinkingActive := false
	if p.isAnthropic {
		if _, ok := anthropicEffortFromLevel(p.reasoningEffort); ok {
			thinkingActive = true
		}
		applyCacheBreakpoints(prompt, fantasyTools, explicitBreaks)
	}

	maxTokens := int64(p.maxTokens)
	call := fantasy.Call{
		Prompt:          prompt,
		MaxOutputTokens: &maxTokens,
		Tools:           fantasyTools,
		ProviderOptions: p.buildProviderOptions(ctx, cacheKey, jsonMode, len(tools) > 0),
	}
	if !thinkingActive && !p.disableTemperature {
		temp := float64(p.temperature)
		call.Temperature = &temp
	}
	return call
}

// usageFromFantasy 把 fantasy 的 Usage 转换成项目内统一的 Usage。fantasy 的 InputTokens 在
// 两个 provider 实现里都不含缓存命中的部分(Anthropic 的 input_tokens 本身如此；OpenAI 分支
// 由 fantasy 用 PromptTokens-CachedTokens 计算得出)，需要把三者相加才符合 PromptTokens
// "完整 prompt 大小"这个统一约定。CacheCreationTokens 只有 Anthropic 会填充，OpenAI 分支恒
// 为 0，相加无副作用。
func usageFromFantasy(u fantasy.Usage) Usage {
	return Usage{
		PromptTokens:        u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens,
		OutputTokens:        u.OutputTokens,
		CacheReadTokens:     u.CacheReadTokens,
		CacheCreationTokens: u.CacheCreationTokens,
	}
}

// anthropicReasoningMetadata 从 StreamPart/Content 的 ProviderMetadata 里提取 Anthropic 的
// 思考签名/加密数据；非 Anthropic 分支或未携带该 metadata 时返回 nil。
func anthropicReasoningMetadata(pm fantasy.ProviderMetadata) *anthropic.ReasoningOptionMetadata {
	if pm == nil {
		return nil
	}
	meta, ok := pm[anthropic.Name].(*anthropic.ReasoningOptionMetadata)
	if !ok {
		return nil
	}
	return meta
}

// streamResult 是 consumeStream 聚合出的一次完整响应。
type streamResult struct {
	content      string
	reasoning    string
	blocks       []ReasoningBlock
	toolCalls    []ToolCall
	usage        Usage
	finishReason string
}

// consumeStream 完整消费一个 fantasy.StreamResponse，聚合成结构化结果；onText 非 nil 时会
// 在每个文本增量到达的同时实时回调，供 ChatStream 转发。工具调用在两个 provider 实现里都是
// 由 fantasy 内部完成分片聚合后、以单个 StreamPartTypeToolCall 事件整体给出，这里不需要再
// 自己做分片聚合（原 openai_tool_calls.go 的职责已被 fantasy 接管）。
func (p *fantasyProvider) consumeStream(stream fantasy.StreamResponse, onText func(string)) (streamResult, error) {
	var res streamResult
	var textSB, reasoningSB strings.Builder

	// NOTE: Anthropic 的推理文本(reasoning_delta)和签名(signature_delta/reasoning_end)
	// 分别在不同的 stream part 到达，用 part.ID(对应 content block 序号)配对；跨多个思考块
	// 时只保留有完整签名/加密数据的块，避免拼出一个无法回放校验的残缺 ReasoningBlock。
	var reasoningOrder []string
	reasoningTexts := map[string]*strings.Builder{}
	reasoningMetas := map[string]*anthropic.ReasoningOptionMetadata{}
	trackReasoning := func(part fantasy.StreamPart) {
		if _, ok := reasoningTexts[part.ID]; !ok {
			reasoningTexts[part.ID] = &strings.Builder{}
			reasoningOrder = append(reasoningOrder, part.ID)
		}
		if part.Delta != "" {
			reasoningTexts[part.ID].WriteString(part.Delta)
		}
		if meta := anthropicReasoningMetadata(part.ProviderMetadata); meta != nil {
			reasoningMetas[part.ID] = meta
		}
	}

	var streamErr error
	for part := range stream {
		switch part.Type {
		case fantasy.StreamPartTypeTextDelta:
			if part.Delta == "" {
				continue
			}
			textSB.WriteString(part.Delta)
			if onText != nil {
				onText(part.Delta)
			}
		case fantasy.StreamPartTypeReasoningDelta:
			if p.isAnthropic {
				trackReasoning(part)
			} else {
				reasoningSB.WriteString(part.Delta)
			}
		case fantasy.StreamPartTypeReasoningEnd:
			if p.isAnthropic {
				trackReasoning(part)
			}
		case fantasy.StreamPartTypeToolCall:
			res.toolCalls = append(res.toolCalls, ToolCall{ID: part.ID, Name: part.ToolCallName, Arguments: part.ToolCallInput})
		case fantasy.StreamPartTypeFinish:
			res.usage = usageFromFantasy(part.Usage)
			res.finishReason = string(part.FinishReason)
		case fantasy.StreamPartTypeError:
			streamErr = part.Error
		}
	}

	res.content = textSB.String()
	if p.isAnthropic {
		for _, id := range reasoningOrder {
			meta := reasoningMetas[id]
			if meta == nil {
				continue
			}
			switch {
			case meta.Signature != "":
				res.blocks = append(res.blocks, ReasoningBlock{Type: "thinking", Text: reasoningTexts[id].String(), Signature: meta.Signature})
			case meta.RedactedData != "":
				res.blocks = append(res.blocks, ReasoningBlock{Type: "redacted_thinking", Data: meta.RedactedData})
			}
		}
	} else {
		res.reasoning = reasoningSB.String()
	}
	return res, streamErr
}

// isRetryableFantasyError 判断错误是否值得整体重试(换一次新连接)：fantasy 自带的
// ProviderError.IsRetryable() 覆盖 408/409/429/5xx/传输层错误，额外并入项目一直以来
// 认为值得重试的 400/403(见 retryCode4xx 注释)。
func isRetryableFantasyError(err error) bool {
	if err == nil {
		return false
	}
	var perr *fantasy.ProviderError
	if errors.As(err, &perr) {
		return perr.IsRetryable() || retryCode4xx[perr.StatusCode]
	}
	return fantasy.IsTransportError(err)
}

// chat 是 Chat/JsonChat/ChatWithTools 共用的内部实现：始终走 Stream 而非 Generate 并在内部
// 聚合成完整结果——部分网关/反向代理对长耗时的非流式请求（尤其是 reasoning_effort=high 的
// 模型）会因响应体迟迟无字节而触发空闲超时；流式请求持续有字节到达，可以规避这类超时。
// 重试整段请求(换新连接)最多 maxRetries 次，只处理连接/传输错误，不处理"空响应"——那是
// 调用方 Chat/JsonChat/ChatWithTools 各自的职责。
func (p *fantasyProvider) chat(ctx context.Context, cacheKey string, messages []ChatMessage, jsonMode bool, tools []ToolDefinition) (content, reasoning string, blocks []ReasoningBlock, toolCalls []ToolCall, usage Usage, err error) {
	start := time.Now()
	role := roleFromCacheKey(cacheKey)
	defer func() { recordLatency(role, p.model, "chat", time.Since(start), err) }()

	call := p.buildCall(ctx, cacheKey, messages, jsonMode, tools)

	var res streamResult
	for attempt := 0; attempt < maxRetries; attempt++ {
		attemptStart := time.Now()
		var stream fantasy.StreamResponse
		stream, err = p.lm.Stream(ctx, call)
		if err == nil {
			res, err = p.consumeStream(stream, nil)
		}
		log.Debug("chat attempt done", "model", p.model, "attempt", attempt+1,
			"elapsed_ms", float64(time.Since(attemptStart).Microseconds())/1000)
		if err == nil || !isRetryableFantasyError(err) {
			break
		}
		log.Warn("chat attempt failed, retrying", "attempt", attempt+1, "max_retries", maxRetries, "err", err)
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return "", "", nil, nil, Usage{}, err
		case <-time.After(8 * time.Second):
		}
	}
	if err != nil {
		err = fmt.Errorf("LLM chat error: %w", err)
		return "", "", nil, nil, Usage{}, err
	}

	if res.reasoning != "" {
		log.Debug("llm reasoning", "session", sessionIDFromContext(ctx), "model", p.model,
			"len", len([]rune(res.reasoning)), "reasoning", truncateForLog(res.reasoning, 2000))
	}
	recordUsage(role, p.model, "chat", res.usage)
	logUsage(role, p.model, res.usage)
	if sink := usageSinkFromContext(ctx); sink != nil {
		sink(res.usage)
	}
	log.Debug("chat done", "role", role, "model", p.model, "elapsed_ms", float64(time.Since(start).Microseconds())/1000,
		"response_len", len([]rune(res.content)), "tool_calls", len(res.toolCalls), "finish_reason", res.finishReason, "usage", res.usage)
	return res.content, res.reasoning, res.blocks, res.toolCalls, res.usage, nil
}

func (p *fantasyProvider) Chat(ctx context.Context, cacheKey string, messages []ChatMessage) (string, error) {
	var msg string
	var err error
	for i := 0; i < 3; i++ {
		msg, _, _, _, _, err = p.chat(ctx, cacheKey, messages, false, nil)
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

func (p *fantasyProvider) JsonChat(ctx context.Context, cacheKey string, messages []ChatMessage) (string, error) {
	for i := 0; i < 3; i++ {
		msg, _, _, _, _, err := p.chat(ctx, cacheKey, messages, true, nil)
		if err != nil {
			log.Error("json chat error", "err", err)
			continue
		}
		if msg == "" {
			continue
		}
		return StripCodeFence(msg), nil
	}
	return "", ErrEmptyLLMResponse
}

// ChatWithTools 发起一次支持原生 tool calling 的对话；模型既不返回文本也不请求工具调用时
// 视为一次可重试的空响应，3 次后返回 ErrEmptyLLMResponse。
func (p *fantasyProvider) ChatWithTools(ctx context.Context, cacheKey string, messages []ChatMessage, tools []ToolDefinition) (ToolChatResult, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		content, reasoning, blocks, toolCalls, usage, err := p.chat(ctx, cacheKey, messages, false, tools)
		if err != nil {
			log.Error("chat with tools error", "err", err)
			lastErr = err
			continue
		}
		if content == "" && len(toolCalls) == 0 {
			lastErr = nil
			continue
		}
		return ToolChatResult{Content: content, ToolCalls: toolCalls, ReasoningBlocks: blocks, Reasoning: reasoning, Usage: usage}, nil
	}
	if lastErr != nil {
		return ToolChatResult{}, fmt.Errorf("LLM chat with tools error: %w", lastErr)
	}
	return ToolChatResult{}, ErrEmptyLLMResponse
}

// ChatStream 复刻旧实现的"探测首个事件"语义：只有连接尚未产出任何事件就失败时才整体重试
// (换一次新连接)；一旦已经开始消费流(哪怕只收到一个非文本事件)后再失败，就不再重试，直接
// 把错误交给 errCh——重试会导致已转发内容前面再拼一段重复内容。
func (p *fantasyProvider) ChatStream(ctx context.Context, cacheKey string, messages []ChatMessage) (<-chan string, <-chan error, error) {
	start := time.Now()
	role := roleFromCacheKey(cacheKey)
	call := p.buildCall(ctx, cacheKey, messages, false, nil)

	var next func() (fantasy.StreamPart, bool)
	var stop func()
	var first fantasy.StreamPart
	var hasFirst bool
	for attempt := 0; attempt < maxRetries; attempt++ {
		s, err := p.lm.Stream(ctx, call)
		if err != nil {
			return nil, nil, fmt.Errorf("LLM chat stream error: %w", err)
		}
		n, st := iter.Pull(s)
		part, ok := n()
		if ok && part.Type == fantasy.StreamPartTypeError {
			if isRetryableFantasyError(part.Error) && attempt < maxRetries-1 {
				st()
				log.Warn("chat stream attempt failed, retrying", "attempt", attempt+1, "max_retries", maxRetries, "err", part.Error)
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-time.After(8 * time.Second):
				}
				continue
			}
			st()
			return nil, nil, fmt.Errorf("LLM chat stream error: %w", part.Error)
		}
		next, stop, first, hasFirst = n, st, part, ok
		break
	}
	if next == nil {
		return nil, nil, fmt.Errorf("LLM chat stream error: exhausted %d retries", maxRetries)
	}

	tokenCh := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(tokenCh)
		defer close(errCh)
		defer stop()

		var tokenRunes int
		// process 返回 true 表示应该终止消费(出错或 ctx 取消)。
		process := func(part fantasy.StreamPart) bool {
			switch part.Type {
			case fantasy.StreamPartTypeTextDelta:
				if part.Delta == "" {
					return false
				}
				tokenRunes += len([]rune(part.Delta))
				select {
				case tokenCh <- part.Delta:
				case <-ctx.Done():
					errCh <- ctx.Err()
					return true
				}
			case fantasy.StreamPartTypeReasoningDelta:
				// NOTE: 捕获流式 reasoning 增量用于审计，纯日志，不影响返回值。
				if part.Delta != "" {
					log.Debug("llm reasoning stream token", "session", sessionIDFromContext(ctx), "model", p.model,
						"token_len", len([]rune(part.Delta)), "token", part.Delta)
				}
			case fantasy.StreamPartTypeError:
				recordLatency(role, p.model, "stream", time.Since(start), part.Error)
				errCh <- fmt.Errorf("LLM chat stream receive error: %w", part.Error)
				return true
			case fantasy.StreamPartTypeFinish:
				usage := usageFromFantasy(part.Usage)
				recordUsage(role, p.model, "stream", usage)
				logUsage(role, p.model, usage)
				if sink := usageSinkFromContext(ctx); sink != nil {
					sink(usage)
				}
			}
			return false
		}

		if hasFirst && process(first) {
			return
		}
		for {
			part, ok := next()
			if !ok {
				break
			}
			if process(part) {
				return
			}
		}
		elapsed := time.Since(start)
		recordLatency(role, p.model, "stream", elapsed, nil)
		log.Debug("chat stream done", "role", role, "model", p.model,
			"elapsed_ms", float64(elapsed.Microseconds())/1000, "response_len", tokenRunes)
	}()
	return tokenCh, errCh, nil
}
