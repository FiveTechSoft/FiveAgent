package agent

import (
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

func TestSystemPromptDefaultNamesModel(t *testing.T) {
	cfg := &config.Config{
		Model: config.Model{
			BaseURL: "https://api.deepseek.com/v1",
			Name:    "deepseek-chat",
		},
	}
	p := SystemPrompt(cfg)
	if !strings.Contains(p, baseSystemPrompt) {
		t.Errorf("default persona missing: %q", p)
	}
	if !strings.Contains(p, "deepseek-chat") {
		t.Errorf("model name missing: %q", p)
	}
	if !strings.Contains(p, "api.deepseek.com") {
		t.Errorf("endpoint host missing: %q", p)
	}
	if strings.Contains(p, "api.deepseek.com/v1") {
		t.Errorf("host should not include the path: %q", p)
	}
}

func TestSystemPromptCustomKeepsIdentityLine(t *testing.T) {
	cfg := &config.Config{
		SystemPrompt: "You are Paco, a grumpy cat.",
		Model: config.Model{
			BaseURL: "http://localhost:11434/v1",
			Name:    "qwen3",
		},
	}
	p := SystemPrompt(cfg)
	if !strings.HasPrefix(p, "You are Paco, a grumpy cat.") {
		t.Errorf("custom prompt missing: %q", p)
	}
	if strings.Contains(p, "FiveAgent, a helpful") {
		t.Errorf("default persona should be replaced: %q", p)
	}
	if !strings.Contains(p, "qwen3") || !strings.Contains(p, "localhost:11434") {
		t.Errorf("identity line missing: %q", p)
	}
}

func TestSystemPromptWhitespaceOnlyFallsBack(t *testing.T) {
	cfg := &config.Config{
		SystemPrompt: "   ",
		Model:        config.Model{BaseURL: "http://localhost:11434/v1", Name: "qwen3"},
	}
	if p := SystemPrompt(cfg); !strings.Contains(p, baseSystemPrompt) {
		t.Errorf("whitespace-only prompt should fall back to default: %q", p)
	}
}
