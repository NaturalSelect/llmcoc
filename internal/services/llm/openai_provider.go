// NOTE: Implements the OpenAI-compatible LLM provider.
package llm

import (
	"context"
	"encoding/json"
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
		return apiErr.HTTPStatusCode >= 500 || apiErr.HTTPStatusCode == 429 || apiErr.HTTPStatusCode == 400
	}
	// Also retry on generic request errors (timeouts, connection resets, etc.)
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		return reqErr.HTTPStatusCode >= 500 || reqErr.HTTPStatusCode == 429 || reqErr.HTTPStatusCode == 400
	}
	return false
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

	name := req.Name
	if name == "" {
		name = "(未指定)"
	}

	gender := req.Gender
	if gender == "" {
		gender = "(未指定)"
	}

	prompt := fmt.Sprintf(`请为克苏鲁神话TRPG(COC第七版)生成一名调查员的详细信息,以JSON格式返回,不要有任何额外文字。

要求：
- 调查员姓名：%s
- 时代背景：%s
- 职业：%s
- 性别：%s
- 玩家背景提示：%s
- 骰子已生成的基础属性：STR=%d CON=%d SIZ=%d DEX=%d APP=%d INT=%d POW=%d EDU=%d

【属性重分配规则】
你可以在不改变以下两组属性总和的前提下,将属性点在组内重新分配,以更符合职业和背景：
  - 第一组(可自由互换)：STR、CON、DEX、APP、POW — 当前总和=%d
  - 第二组(可自由互换)：SIZ、INT、EDU — 当前总和=%d
  - 约束：每个属性均为5的倍数；STR/CON/DEX/APP/POW 范围 15-90；SIZ/INT/EDU 范围 40-90
  - 若无需调整,原样返回即可

请返回如下JSON格式(所有字段都用中文)：
{
  "backstory": "200字以内的背景故事",
  "appearance": "100字以内的外貌描述",
  "traits": "性格特征与信念,50字以内",
  "stats": {"STR":N,"CON":N,"SIZ":N,"DEX":N,"APP":N,"INT":N,"POW":N,"EDU":N}
}`,
		name, era, occupation, gender, req.Background,
		req.Stats.STR, req.Stats.CON, req.Stats.SIZ,
		req.Stats.DEX, req.Stats.APP, req.Stats.INT,
		req.Stats.POW, req.Stats.EDU,
		req.Stats.STR+req.Stats.CON+req.Stats.DEX+req.Stats.APP+req.Stats.POW,
		req.Stats.SIZ+req.Stats.INT+req.Stats.EDU,
	)

	genReq := openai.ChatCompletionRequest{
		Model: p.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "你是一名克苏鲁神话TRPG专家,只输出JSON,不输出任何其他内容。"},
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		MaxTokens:       1000,
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

func (p *openAIProvider) AdjustSkills(ctx context.Context, req AdjustSkillsReq) (map[string]int, error) {
	era := req.Era
	if era == "" {
		era = "1920年代"
	}
	occupation := req.Occupation
	if occupation == "" {
		occupation = "调查员"
	}

	// Build skill list string for prompt
	var sb strings.Builder
	for k, v := range req.BaseSkills {
		sb.WriteString(fmt.Sprintf("  %s: %d\n", k, v))
	}

	// COC7 occupation points = EDU*4, interest points = INT*2
	occPoints := req.Stats.EDU * 4
	intPoints := req.Stats.INT * 2

	prompt := fmt.Sprintf(`你是COC第七版规则专家。请根据调查员的职业和背景,合理分配技能加成点,输出调整后的完整技能列表(JSON对象)。

【调查员信息】
- 姓名：%s
- 时代：%s
- 职业：%s
- 背景提示：%s
- 属性：STR=%d CON=%d SIZ=%d DEX=%d APP=%d INT=%d POW=%d EDU=%d

【当前技能基础值】
%s
【技能分配规则】
1. 职业技能点(共 %d 点 = EDU×4)：分配给与职业强相关的技能(例如医生必须高医学、急救、心理学等)
2. 兴趣技能点(共 %d 点 = INT×2)：分配给调查员个人兴趣或背景相关技能
3. 每项技能最终值(基础值 + 加成点)上限 90
4. 加成点只能加在现有技能列表中的技能上,不得新增技能名称
5. 把所有职业技能点和兴趣技能点完整分配出去,不要剩余

请直接输出完整技能JSON对象(包含所有技能,包括未改动的),格式示例：
{"医学":75,"急救":60,"心理学":50,...}
只输出JSON,不要任何其他文字。`,
		req.Name, era, occupation, req.Background,
		req.Stats.STR, req.Stats.CON, req.Stats.SIZ,
		req.Stats.DEX, req.Stats.APP, req.Stats.INT,
		req.Stats.POW, req.Stats.EDU,
		sb.String(),
		occPoints, intPoints,
	)

	genReq := openai.ChatCompletionRequest{
		Model: p.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "你是COC TRPG规则专家,只输出JSON,不输出任何其他内容。"},
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		MaxTokens:       800,
		Temperature:     0.7,
		ReasoningEffort: defaultReasoningEffort,
	}

	var resp openai.ChatCompletionResponse
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err = p.client.CreateChatCompletion(ctx, genReq)
		if err == nil || !isRetryableError(err) {
			break
		}
		log.Printf("[llm] AdjustSkills attempt %d/%d failed (5xx), retrying in 2s: %v", attempt+1, maxRetries, err)
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
	// extract {...} in case of surrounding text
	if s := strings.Index(content, "{"); s >= 0 {
		if e := strings.LastIndex(content, "}"); e > s {
			content = content[s : e+1]
		}
	}

	var raw map[string]int
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("AdjustSkills parse failed: %w (raw: %s)", err, content)
	}
	return raw, nil
}
