package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeContextConfig(t *testing.T) {
	for _, tc := range []struct {
		label, yml string
		bad        bool
		want       int
	}{
		{"ctx selects native", "model:\n  name: fixture\n  base_url: http://localhost:11434/v1\n  num_ctx: 8192\ncoder:\n  name: coder\n", false, 8192},
		{"unset", "model:\n  name: fixture\n  base_url: http://fixture/v1\n", false, 0},
		{"wrong suffix", "model:\n  name: fixture\n  base_url: http://fixture\n  num_ctx: 8192\n", true, 0},
		{"negative", "model:\n  name: fixture\n  base_url: http://fixture/v1\n  num_ctx: -1\n", true, 0},
		{"conflict", "model:\n  name: fixture\n  base_url: http://fixture/v1\n  num_ctx: 8192\n  chat_template_kwargs: {enable_thinking: false}\n", true, 0},
	} {
		t.Run(tc.label, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(p, []byte(tc.yml), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(p)
			if tc.bad {
				if err == nil {
					t.Fatal("invalid config accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Model.NumCtx != tc.want {
				t.Fatal("context option lost")
			}
			if cfg.Coder.Name != "" && cfg.Coder.NumCtx != tc.want {
				t.Fatal("same-endpoint coder did not inherit")
			}
		})
	}
}
