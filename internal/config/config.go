// NOTE: Package config loads and parses the application's configuration from YAML and environment variables.
package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	JWT      JWTConfig      `yaml:"jwt"`
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
}
