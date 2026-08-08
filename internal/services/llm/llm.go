// NOTE: Provides integration with Large Language Models (LLMs).
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
)

// ChatMessage represents a single message in a conversation
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls 在 Role=="assistant" 时携带模型请求的原生工具调用（function calling）。
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID 在 Role=="tool" 时标识该消息是对哪一次工具调用的响应结果。
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ToolDefinition 描述一个可被模型原生调用的函数工具（function calling schema）。
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema；nil 表示无参数
}

// ToolCall 是模型在一次响应中请求的一次原生工具调用。
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // 原始 JSON 文本，由调用方按工具自身的参数结构反序列化
}

// ToolChatResult 是一次原生工具调用对话的返回。Content 为模型的文本部分（可能为空）；
// ToolCalls 为模型请求调用的工具列表（可能为空，表示模型选择直接文本回复而非调用工具）。
type ToolChatResult struct {
	Content   string
	ToolCalls []ToolCall
}

// Provider defines the interface for interacting with various LLM backends.
// cacheKey 用于 prompt cache 隔离,需区分 agent 角色和 NPC 实例,避免跨 agent 缓存污染。
type Provider interface {
	// Chat sends a conversation and returns the full response.
	Chat(ctx context.Context, cacheKey string, messages []ChatMessage) (string, error)
	ChatStream(ctx context.Context, cacheKey string, messages []ChatMessage) (<-chan string, <-chan error, error)
	JsonChat(ctx context.Context, cacheKey string, messages []ChatMessage) (string, error)
	// ChatWithTools 发起一次支持原生 tool calling 的对话；tools 非空时作为 function calling
	// 候选传给模型，模型可选择直接回复文本，或请求调用其中若干工具（通过 ToolChatResult.ToolCalls 返回）。
	ChatWithTools(ctx context.Context, cacheKey string, messages []ChatMessage, tools []ToolDefinition) (ToolChatResult, error)
}

// ImageAspect 是 Director 可选择的语义化画面方向,由 Provider 负责翻译成具体模型的像素尺寸。
type ImageAspect string

const (
	ImageAspectSquare    ImageAspect = "square"    // 方图,默认
	ImageAspectLandscape ImageAspect = "landscape" // 横图
	ImageAspectPortrait  ImageAspect = "portrait"  // 竖图
)

// ImageOptions 承载图片生成的可选参数;零值等价于当前默认行为(方图)。
type ImageOptions struct {
	Aspect ImageAspect
}

type ImageGenerator interface {
	GenerateImage(ctx context.Context, prompt string, opts ImageOptions) (base64Data string, mimeType string, err error)
}

// NewProviderFromConfig creates a provider from a DB-stored LLMProviderConfig.
func NewProviderFromConfig(cfg *models.LLMProviderConfig, modelName string, maxTokens int, temperature float32, disableTemperature bool, reasoningEffort string) Provider {
	return newProviderByType(cfg.Provider, cfg.APIKey, cfg.BaseURL, modelName, maxTokens, temperature, disableTemperature, reasoningEffort)
}

// newProviderByType 按 LLMProviderConfig.Provider 字段分发到具体实现。
// 未识别的类型(包括历史遗留的 "custom"/空字符串)一律回落 OpenAI 兼容实现,
// 保持与既有行为一致。
func newProviderByType(providerType, apiKey, baseURL, model string, maxTokens int, temperature float32, disableTemperature bool, reasoningEffort string) Provider {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "anthropic":
		return newAnthropicProvider(apiKey, baseURL, model, maxTokens, temperature, disableTemperature)
	default:
		return newOpenAIProvider(apiKey, baseURL, model, maxTokens, temperature, disableTemperature, reasoningEffort)
	}
}

// LoadProviderFromDB loads an LLM provider for the given agent role from the database.
// Returns an error if the role has no active config or no active linked provider.
func LoadProviderFromDB(role models.AgentRole) (Provider, error) {
	var cfg models.AgentConfig
	err := models.DB.Preload("ProviderConfig").
		Where("role = ? AND is_active = ?", role, true).
		First(&cfg).Error
	if err != nil {
		return nil, fmt.Errorf("agent %q 未配置,请在管理面板中配置 LLM provider", role)
	}
	if cfg.ProviderConfigID == nil || cfg.ProviderConfig == nil || !cfg.ProviderConfig.IsActive {
		return nil, fmt.Errorf("agent %q 未绑定可用的 LLM provider", role)
	}
	maxTok := cfg.MaxTokens
	if maxTok == 0 {
		maxTok = 1024
	}
	return newProviderByType(cfg.ProviderConfig.Provider, cfg.ProviderConfig.APIKey, cfg.ProviderConfig.BaseURL, cfg.ModelName, maxTok, cfg.Temperature, cfg.DisableTemperature, cfg.ThinkingLevel), nil
}

// StripCodeFence removes markdown code fences from an LLM response.
func StripCodeFence(s string) string {
	findFirst := strings.Index(s, "```")
	if findFirst != -1 {
		s = s[findFirst:]
		if strings.HasPrefix(s, "```json") {
			s = strings.TrimPrefix(s, "```json")
			s = strings.TrimSuffix(s, "```")
			return strings.TrimSpace(s)
		}
		if strings.HasPrefix(s, "```") {
			s = strings.TrimPrefix(s, "```")
			s = strings.TrimSuffix(s, "```")
			return strings.TrimSpace(s)
		}
	}
	return s
}

func JsonArryProtect(s string) string {
	if !strings.HasPrefix(s, "[") {
		s = "[" + s
	}
	if !strings.HasSuffix(s, "]") {
		s = s + "]"
	}
	return s
}

// BuildKPSystemPrompt builds the system prompt for the KP (LLM) from a scenario
func BuildKPSystemPrompt(scenario *models.Scenario, players []models.SessionPlayer) string {
	content := scenario.Content.Data

	playerList := ""
	for _, p := range players {
		card := p.CharacterCard
		playerList += fmt.Sprintf(
			"\n- %s(%s,%s):STR%d CON%d SIZ%d DEX%d APP%d INT%d POW%d EDU%d HP%d/%d SAN%d/%d",
			card.Name, card.Occupation, card.Gender,
			card.Stats.Data.STR, card.Stats.Data.CON, card.Stats.Data.SIZ,
			card.Stats.Data.DEX, card.Stats.Data.APP, card.Stats.Data.INT,
			card.Stats.Data.POW, card.Stats.Data.EDU,
			card.Stats.Data.HP, card.Stats.Data.MaxHP,
			card.Stats.Data.SAN, card.Stats.Data.MaxSAN,
		)
	}

	return fmt.Sprintf(`%s

## 当前剧本
**名称**: %s
**背景设定**: %s

## 在场调查员%s

## KP行为规范
1. 你是克苏鲁神话TRPG(COC 第七版)的主持人(KP),负责推进剧情、扮演NPC、描述场景。
2. 当玩家宣布行动时,若需要骰子检定,请明确告知「请进行XX检定(技能值N)」,系统会自动处理骰子。
3. 保持克苏鲁风格:神秘、压抑、充满未知恐惧,适度展现宇宙恐怖元素。
4. 对话以中文进行,场景描述生动具体,NPC性格鲜明。
5. 当调查员的SAN值、HP或MP发生变化时,以「【SAN -N】」「【HP -N】」的格式标注。
6. 不要替玩家做决策,引导但不强迫剧情走向。
7. 每次回复控制在300字以内,除非场景描述确实需要更多。`, content.SystemPrompt,
		scenario.Name, content.Setting, playerList)
}
