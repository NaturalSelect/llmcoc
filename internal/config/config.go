package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	JWT      JWTConfig      `yaml:"jwt"`
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
	Secret      string `yaml:"secret"`
	ExpireHours int    `yaml:"expire_hours"`
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
	if Global.Shop.InitialCoins == 0 {
		Global.Shop.InitialCoins = 0
	}
	if Global.Shop.InitialCardSlots == 0 {
		Global.Shop.InitialCardSlots = 3
	}
}
