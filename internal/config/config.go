// Package config loads fiveagent.yml: model endpoint, channels and storage.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Model selects the chat model. Any OpenAI-compatible endpoint works:
// Ollama, llama.cpp, vLLM, OpenAI, DeepSeek, ...
type Model struct {
	Provider string `yaml:"provider"` // "openai-compatible"
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Name     string `yaml:"name"`
}

// Channel is one messaging channel's settings.
type Channel struct {
	Enabled  bool   `yaml:"enabled"`
	BotToken string `yaml:"bot_token,omitempty"`
}

// Memory holds the storage settings.
type Memory struct {
	Postgres string `yaml:"postgres"`
}

// Config is the root of fiveagent.yml.
type Config struct {
	Model    Model              `yaml:"model"`
	Channels map[string]Channel `yaml:"channels"`
	Memory   Memory             `yaml:"memory"`
}

// Load reads path (default ./fiveagent.yml).
func Load(path string) (*Config, error) {
	if path == "" {
		path = "fiveagent.yml"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Model.BaseURL == "" || c.Model.Name == "" {
		return nil, fmt.Errorf("model.base_url and model.name are required")
	}
	return &c, nil
}
