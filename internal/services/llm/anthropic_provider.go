// NOTE: Implements the Anthropic Claude LLM provider.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicProvider struct {
	client      anthropic.Client
	model       anthropic.Model
	maxTokens   int64
	temperature float64
}

func newAnthropicProvider(apiKey, baseURL, model string, maxTokens int, temperature float64) *anthropicProvider {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		baseURL = strings.TrimSuffix(baseURL, "/v1")
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if maxTokens == 0 {
		maxTokens = 2048
	}
	return &anthropicProvider{
		client:      anthropic.NewClient(opts...),
		model:       anthropic.Model(model),
		maxTokens:   int64(maxTokens),
		temperature: temperature,
	}
}

// toAnthropicMessages converts ChatMessage slice into Anthropic MessageParam slice.
// Anthropic requires messages to alternate user/assistant and does not accept a
// "system" role inside the messages array (system is a top-level field).
// System messages are therefore collected separately and returned as the second value.
func (p *anthropicProvider) toAnthropicMessages(msgs []ChatMessage) ([]anthropic.MessageParam, string) {
	var systemParts []string
	var params []anthropic.MessageParam
	for _, m := range msgs {
		switch m.Role {
		case "system":
			systemParts = append(systemParts, m.Content)
		case "assistant":
			params = append(params, anthropic.NewAssistantMessage(
				anthropic.NewTextBlock(m.Content),
			))
		default: // "user" or anything else
			params = append(params, anthropic.NewUserMessage(
				anthropic.NewTextBlock(m.Content),
			))
		}
	}
	return params, strings.Join(systemParts, "\n")
}

// isAnthropicRetryable returns true for transient / rate-limit errors.
func isAnthropicRetryable(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		code := apiErr.StatusCode
		return code == 429 || code >= 500
	}
	return false
}

func (p *anthropicProvider) Chat(ctx context.Context, messages []ChatMessage) (string, error) {
	start := time.Now()
	params, system := p.toAnthropicMessages(messages)

	req := anthropic.MessageNewParams{
		Model:        p.model,
		MaxTokens:    p.maxTokens,
		Messages:     params,
		Thinking:     anthropic.ThinkingConfigParamOfEnabled(1500),
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	}
	req.Temperature.Value = p.temperature
	if system != "" {
		req.System = []anthropic.TextBlockParam{
			{Text: system},
		}
	}

	var resp *anthropic.Message
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err = p.client.Messages.New(ctx, req)
		if err == nil || !isAnthropicRetryable(err) {
			break
		}
		log.Printf("[llm/anthropic] Chat attempt %d/%d failed, retrying in 2s: %v", attempt+1, maxRetries, err)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if err != nil {
		return "", fmt.Errorf("anthropic chat error: %w", err)
	}

	result := extractAnthropicText(resp)
	if llmDebug {
		elapsed := time.Since(start)
		log.Printf("[llm/anthropic] Chat done model=%s elapsed=%.0fms response_len=%d",
			p.model, float64(elapsed.Microseconds())/1000, len([]rune(result)))
	}
	return result, nil
}

func (p *anthropicProvider) GenerateCharacter(ctx context.Context, req GenerateCharacterReq) (*GeneratedCharacter, error) {
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

要求:
- 调查员姓名:%s
- 时代背景:%s
- 职业:%s
- 性别:%s
- 玩家背景提示:%s
- 骰子已生成的基础属性:STR=%d CON=%d SIZ=%d DEX=%d APP=%d INT=%d POW=%d EDU=%d

【属性重分配规则】
你可以在不改变以下两组属性总和的前提下,将属性点在组内重新分配,以更符合职业和背景:
  - 第一组(可自由互换):STR、CON、DEX、APP、POW — 当前总和=%d
  - 第二组(可自由互换):SIZ、INT、EDU — 当前总和=%d
  - 约束:每个属性均为5的倍数；STR/CON/DEX/APP/POW 范围 15-90；SIZ/INT/EDU 范围 40-90
  - 若无需调整,原样返回即可

请返回如下JSON格式(所有字段都用中文):
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

	genReq := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 1000,
		System: []anthropic.TextBlockParam{
			{Text: "你是一名克苏鲁神话TRPG专家,只输出JSON,不输出任何其他内容。"},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Thinking: anthropic.ThinkingConfigParamOfEnabled(1500),
	}

	var resp *anthropic.Message
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err = p.client.Messages.New(ctx, genReq)
		if err == nil || !isAnthropicRetryable(err) {
			break
		}
		log.Printf("[llm/anthropic] GenerateCharacter attempt %d/%d failed, retrying in 2s: %v", attempt+1, maxRetries, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if err != nil {
		return nil, fmt.Errorf("anthropic GenerateCharacter error: %w", err)
	}

	content := StripCodeFence(extractAnthropicText(resp))
	var out GeneratedCharacter
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, fmt.Errorf("parse LLM response failed: %w (raw: %s)", err, content)
	}
	return &out, nil
}

func (p *anthropicProvider) AdjustSkills(ctx context.Context, req AdjustSkillsReq) (map[string]int, error) {
	era := req.Era
	if era == "" {
		era = "1920年代"
	}
	occupation := req.Occupation
	if occupation == "" {
		occupation = "调查员"
	}

	var sb strings.Builder
	for k, v := range req.BaseSkills {
		sb.WriteString(fmt.Sprintf("  %s: %d\n", k, v))
	}

	occPoints := req.Stats.EDU * 4
	intPoints := req.Stats.INT * 2

	prompt := fmt.Sprintf(`你是COC第七版规则专家。请根据调查员的职业和背景,合理分配技能加成点,输出调整后的完整技能列表(JSON对象)。

【调查员信息】
- 姓名:%s
- 时代:%s
- 职业:%s
- 背景提示:%s
- 属性:STR=%d CON=%d SIZ=%d DEX=%d APP=%d INT=%d POW=%d EDU=%d

【当前技能基础值】
%s
【技能分配规则】
1. 职业技能点(共 %d 点 = EDU×4):分配给与职业强相关的技能(例如医生必须高医学、急救、心理学等)
2. 兴趣技能点(共 %d 点 = INT×2):分配给调查员个人兴趣或背景相关技能
3. 每项技能最终值(基础值 + 加成点)上限 90
4. 加成点只能加在现有技能列表中的技能上,不得新增技能名称
5. 把所有职业技能点和兴趣技能点完整分配出去,不要剩余

请直接输出完整技能JSON对象(包含所有技能,包括未改动的),格式示例:
{"医学":75,"急救":60,"心理学":50,...}
只输出JSON,不要任何其他文字。`,
		req.Name, era, occupation, req.Background,
		req.Stats.STR, req.Stats.CON, req.Stats.SIZ,
		req.Stats.DEX, req.Stats.APP, req.Stats.INT,
		req.Stats.POW, req.Stats.EDU,
		sb.String(),
		occPoints, intPoints,
	)

	genReq := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 800,
		System: []anthropic.TextBlockParam{
			{Text: "你是COC TRPG规则专家,只输出JSON,不输出任何其他内容。"},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Thinking: anthropic.ThinkingConfigParamOfEnabled(1500),
	}

	var resp *anthropic.Message
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err = p.client.Messages.New(ctx, genReq)
		if err == nil || !isAnthropicRetryable(err) {
			break
		}
		log.Printf("[llm/anthropic] AdjustSkills attempt %d/%d failed, retrying in 2s: %v", attempt+1, maxRetries, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if err != nil {
		return nil, fmt.Errorf("anthropic AdjustSkills error: %w", err)
	}

	content := StripCodeFence(extractAnthropicText(resp))
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

// extractAnthropicText pulls the first text block from a Claude response,
// skipping thinking blocks which are also returned when adaptive thinking is on.
func extractAnthropicText(msg *anthropic.Message) string {
	for _, block := range msg.Content {
		if block.Type == "text" {
			return block.Text
		}
	}
	return ""
}
