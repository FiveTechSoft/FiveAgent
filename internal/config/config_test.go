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
