package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSandboxEnabledByDefault(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := dir + "/" + name
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := "model:\n  provider: openai-compatible\n  base_url: http://x\n  name: m\n"

	c, err := Load(write("a.yml", base))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Sandbox.Enabled {
		t.Error("sandbox must default to enabled when the section is absent")
	}

	c, err = Load(write("b.yml", base+"sandbox:\n  backend: auto\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Sandbox.Enabled {
		t.Error("sandbox must default to enabled when enabled key is absent")
	}

	c, err = Load(write("c.yml", base+"sandbox:\n  enabled: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Sandbox.Enabled {
		t.Error("explicit enabled: false must win")
	}
}

func TestLoadExpandsEnv(t *testing.T) {
	t.Setenv("FIVEAGENT_TEST_SECRET", "s3cret")
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "fiveagent.yml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// ${VAR} expands to its value.
	cfg, err := Load(write("model:\n  base_url: http://x\n  name: m\nmemory:\n  postgres: postgres://u:${FIVEAGENT_TEST_SECRET}@db:5432/fiveagent\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "postgres://u:s3cret@db:5432/fiveagent"; cfg.Memory.Postgres != want {
		t.Errorf("env not expanded: got %q, want %q", cfg.Memory.Postgres, want)
	}

	// An unset variable expands to the empty string, silently.
	cfg, err = Load(write("model:\n  base_url: http://x\n  name: m\nmemory:\n  postgres: postgres://u:$FIVEAGENT_TEST_MISSING@db:5432/fiveagent\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "postgres://u:@db:5432/fiveagent"; cfg.Memory.Postgres != want {
		t.Errorf("unset var: got %q, want %q", cfg.Memory.Postgres, want)
	}
}

func TestLoadNumThread(t *testing.T) {
	yml := `
model:
  base_url: http://localhost:11434/v1
  name: qwen3
  num_thread: 8
coder:
  name: qwen3-coder
`
	path := filepath.Join(t.TempDir(), "fiveagent.yml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.NumThread != 8 {
		t.Fatalf("model.num_thread: got %d, want 8", cfg.Model.NumThread)
	}
	if cfg.Coder.NumThread != 8 {
		t.Fatalf("coder.num_thread not inherited: got %d, want 8", cfg.Coder.NumThread)
	}
}

func TestLoadNumThreadRequiresV1BaseURL(t *testing.T) {
	yml := `
model:
  base_url: http://localhost:11434
  name: qwen3
  num_thread: 8
`
	path := filepath.Join(t.TempDir(), "fiveagent.yml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected a loud config error, got nil")
	}
	if !strings.Contains(err.Error(), "num_thread") {
		t.Fatalf("error must name num_thread: %v", err)
	}
}
