package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
)

// ChatMessage represents a single message in a conversation
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// GenerateCharacterReq is the input for AI character generation
type GenerateCharacterReq struct {
	Name       string
	Occupation string
	Background string
	Era        string
	Stats      models.CharacterStats
}

// GeneratedCharacter is the output from AI character generation
type GeneratedCharacter struct {
	Backstory  string                 `json:"backstory"`
	Appearance string                 `json:"appearance"`
	Traits     string                 `json:"traits"`
	Stats      *models.CharacterStats `json:"stats,omitempty"` // LLM-adjusted attributes (optional)
}

// AdjustSkillsReq is the input for AI skill adjustment
type AdjustSkillsReq struct {
	Name       string
	Occupation string
	Background string
	Era        string
	Stats      models.CharacterStats
	BaseSkills map[string]int // current skill values (all skills)
}

// Provider defines the interface for LLM providers
type Provider interface {
	// ChatStream sends a conversation and streams tokens to a channel (for real-time output)
	ChatStream(ctx context.Context, messages []ChatMessage) (<-chan string, error)
	// Chat sends a conversation and returns the full response (for agent pipeline steps)
	Chat(ctx context.Context, messages []ChatMessage) (string, error)
	// GenerateCharacter uses AI to fill in character details
	GenerateCharacter(ctx context.Context, req GenerateCharacterReq) (*GeneratedCharacter, error)
	// AdjustSkills uses AI to redistribute skill points to fit the character's occupation and background
	AdjustSkills(ctx context.Context, req AdjustSkillsReq) (map[string]int, error)
}

// NewProviderFromConfig creates a provider from a DB-stored LLMProviderConfig.
func NewProviderFromConfig(cfg *models.LLMProviderConfig, modelName string, maxTokens int, temperature float32) Provider {
	return newOpenAIProvider(cfg.APIKey, cfg.BaseURL, modelName, maxTokens, temperature)
}

// LoadProviderFromDB loads an LLM provider for the given agent role from the database.
// Returns an error if the role has no active config or no active linked provider.
func LoadProviderFromDB(role models.AgentRole) (Provider, error) {
	var cfg models.AgentConfig
	err := models.DB.Preload("ProviderConfig").
		Where("role = ? AND is_active = ?", role, true).
		First(&cfg).Error
	if err != nil {
		return nil, fmt.Errorf("agent %q 未配置，请在管理面板中配置 LLM provider", role)
	}
	if cfg.ProviderConfigID == nil || cfg.ProviderConfig == nil || !cfg.ProviderConfig.IsActive {
		return nil, fmt.Errorf("agent %q 未绑定可用的 LLM provider", role)
	}
	maxTok := cfg.MaxTokens
	if maxTok == 0 {
		maxTok = 1024
	}
	return newOpenAIProvider(cfg.ProviderConfig.APIKey, cfg.ProviderConfig.BaseURL, cfg.ModelName, maxTok, cfg.Temperature), nil
}

// StripCodeFence removes markdown code fences from an LLM response.
func StripCodeFence(s string) string {
	start := 0
	end := len(s)
	for i := 0; i < len(s)-2; i++ {
		if s[i] == '`' && s[i+1] == '`' && s[i+2] == '`' {
			if start == 0 {
				for j := i + 3; j < len(s); j++ {
					if s[j] == '\n' {
						start = j + 1
						break
					}
				}
			} else {
				end = i
				break
			}
		}
	}
	if start > 0 {
		return s[start:end]
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
			"\n- %s（%s，%s）：STR%d CON%d SIZ%d DEX%d APP%d INT%d POW%d EDU%d HP%d/%d SAN%d/%d",
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
1. 你是克苏鲁神话TRPG（COC 第七版）的主持人（KP），负责推进剧情、扮演NPC、描述场景。
2. 当玩家宣布行动时，若需要骰子检定，请明确告知「请进行XX检定（技能值N）」，系统会自动处理骰子。
3. 保持克苏鲁风格：神秘、压抑、充满未知恐惧，适度展现宇宙恐怖元素。
4. 对话以中文进行，场景描述生动具体，NPC性格鲜明。
5. 当调查员的SAN值、HP或MP发生变化时，以「【SAN -N】」「【HP -N】」的格式标注。
6. 不要替玩家做决策，引导但不强迫剧情走向。
7. 每次回复控制在300字以内，除非场景描述确实需要更多。`, content.SystemPrompt,
		scenario.Name, content.Setting, playerList)
}
