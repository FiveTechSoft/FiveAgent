package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSystemPrompt(t *testing.T) {
	yml := `
model:
  base_url: http://localhost:11434/v1
  name: qwen3
system_prompt: "You are Paco."
`
	path := filepath.Join(t.TempDir(), "fiveagent.yml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SystemPrompt != "You are Paco." {
		t.Errorf("system_prompt not parsed: %q", cfg.SystemPrompt)
	}
}

func TestLoadSystemPromptOptional(t *testing.T) {
	yml := "model:\n  base_url: http://localhost:11434/v1\n  name: qwen3\n"
	path := filepath.Join(t.TempDir(), "fiveagent.yml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SystemPrompt != "" {
		t.Errorf("system_prompt should default to empty: %q", cfg.SystemPrompt)
	}
}

func TestLoadCoderInheritsModel(t *testing.T) {
	yml := `
model:
  provider: openai-compatible
  base_url: https://api.deepseek.com/v1
  api_key: sk-test
  name: deepseek-chat
coder:
  name: deepseek-coder
`
	path := filepath.Join(t.TempDir(), "fiveagent.yml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Coder.BaseURL != cfg.Model.BaseURL {
		t.Errorf("coder.base_url not inherited: %q", cfg.Coder.BaseURL)
	}
	if cfg.Coder.Provider != cfg.Model.Provider {
		t.Errorf("coder.provider not inherited: %q", cfg.Coder.Provider)
	}
	if cfg.Coder.APIKey != cfg.Model.APIKey {
		t.Errorf("coder.api_key not inherited: %q", cfg.Coder.APIKey)
	}
	if cfg.Coder.Name != "deepseek-coder" {
		t.Errorf("coder.name overwritten: %q", cfg.Coder.Name)
	}
}

func TestLoadCoderKeepsExplicitValues(t *testing.T) {
	yml := `
model:
  base_url: https://api.deepseek.com/v1
  api_key: sk-test
  name: deepseek-chat
coder:
  base_url: http://localhost:11434/v1
  api_key: ""
  name: qwen3
`
	path := filepath.Join(t.TempDir(), "fiveagent.yml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Coder.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("coder.base_url overwritten: %q", cfg.Coder.BaseURL)
	}
	// Explicit empty api_key still inherits: there is no way to say
	// "no key" once the parent has one; document the behavior.
	if cfg.Coder.APIKey != "sk-test" {
		t.Errorf("coder.api_key: got %q, want inherited sk-test", cfg.Coder.APIKey)
	}
}
