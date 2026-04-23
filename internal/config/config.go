package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	JWT      JWTConfig      `yaml:"jwt"`
	LLM      LLMConfig      `yaml:"llm"`
	Shop     ShopConfig     `yaml:"shop"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type JWTConfig struct {
	Secret     string `yaml:"secret"`
	ExpireHours int   `yaml:"expire_hours"`
}

type LLMConfig struct {
	Provider    string            `yaml:"provider"` // openai | anthropic | ollama | custom
	BaseURL     string            `yaml:"base_url"`
	APIKey      string            `yaml:"api_key"`
	Model       string            `yaml:"model"`
	MaxTokens   int               `yaml:"max_tokens"`
	Temperature float32           `yaml:"temperature"`
	Providers   map[string]ProviderConfig `yaml:"providers"`
}

type ProviderConfig struct {
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
	Model   string `yaml:"model"`
}

type ShopConfig struct {
	InitialCoins     int `yaml:"initial_coins"`
	InitialCardSlots int `yaml:"initial_card_slots"`
}

var Global Config

func Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, &Global); err != nil {
		return err
	}
	applyEnvOverrides()
	setDefaults()
	return nil
}

func applyEnvOverrides() {
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		Global.LLM.APIKey = v
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		Global.LLM.BaseURL = v
	}
	if v := os.Getenv("LLM_MODEL"); v != "" {
		Global.LLM.Model = v
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		Global.JWT.Secret = v
	}
}

func setDefaults() {
	if Global.Server.Port == 0 {
		Global.Server.Port = 8080
	}
	if Global.Database.Path == "" {
		Global.Database.Path = "data/llmcoc.db"
	}
	if Global.JWT.Secret == "" {
		Global.JWT.Secret = "change-me-in-production"
	}
	if Global.JWT.ExpireHours == 0 {
		Global.JWT.ExpireHours = 168
	}
	if Global.LLM.Model == "" {
		Global.LLM.Model = "gpt-4o"
	}
	if Global.LLM.MaxTokens == 0 {
		Global.LLM.MaxTokens = 2048
	}
	if Global.LLM.Temperature == 0 {
		Global.LLM.Temperature = 0.8
	}
	if Global.Shop.InitialCoins == 0 {
		Global.Shop.InitialCoins = 0
	}
	if Global.Shop.InitialCardSlots == 0 {
		Global.Shop.InitialCardSlots = 3
	}
}
