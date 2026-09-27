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
	// Timeout caps one model call, in seconds. Default: 120, or 600 for
	// local endpoints (localhost), where large models load into RAM on
	// first use and easily exceed two minutes.
	Timeout int `yaml:"timeout,omitempty"`
}

// Channel is one messaging channel's settings. The fields used depend on
// the adapter: telegram uses bot_token; whatsapp (official Cloud API) uses
// access_token, phone_number_id, verify_token and listen_addr.
type Channel struct {
	Enabled  bool   `yaml:"enabled"`
	BotToken string `yaml:"bot_token,omitempty"`
	// WhatsApp Cloud API
	AccessToken   string `yaml:"access_token,omitempty"`
	PhoneNumberID string `yaml:"phone_number_id,omitempty"`
	VerifyToken   string `yaml:"verify_token,omitempty"`
	AppSecret     string `yaml:"app_secret,omitempty"`  // enables X-Hub-Signature-256 checks
	ListenAddr    string `yaml:"listen_addr,omitempty"` // webhook listen address, default :8080
	// AllowedSenders limits who the bot answers: WhatsApp phone numbers as
	// they arrive (e.g. "34600123456"), Telegram chat IDs (e.g. "123456789").
	// Empty means allow everyone (a warning is logged at startup).
	AllowedSenders []string `yaml:"allowed_senders,omitempty"`
}

// Memory holds the storage settings. JSON is a file-backed store for local
// prototypes; Postgres for real deployments. If JSON is set, it wins.
type Memory struct {
	Postgres string `yaml:"postgres"`
	JSON     string `yaml:"json,omitempty"`
}

// Sandbox holds the per-user isolated execution settings. Disabled by
// default; when enabled, the agent gets a run_command tool whose
// commands run inside a per-user sandbox (see internal/sandbox).
type Sandbox struct {
	Enabled bool   `yaml:"enabled"`
	Backend string `yaml:"backend,omitempty"`    // auto | bubblewrap | jobobject | docker
	Root    string `yaml:"root,omitempty"`       // default data/sandbox
	Timeout int    `yaml:"timeout,omitempty"`    // seconds per command, default 30
	MaxRAM  int    `yaml:"max_ram_mb,omitempty"` // docker / jobobject only, default 512
	Image   string `yaml:"image,omitempty"`      // docker only, default alpine
}

// Config is the root of fiveagent.yml.
type Config struct {
	Model    Model              `yaml:"model"`
	Channels map[string]Channel `yaml:"channels"`
	Memory   Memory             `yaml:"memory"`
	Sandbox  Sandbox            `yaml:"sandbox,omitempty"`
	// SystemPrompt overrides the agent's built-in persona. Optional; the
	// model identity line is always appended (see agent.SystemPrompt).
	SystemPrompt string `yaml:"system_prompt,omitempty"`
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
