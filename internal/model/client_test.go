package model

import (
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

func TestModelTimeoutDefaults(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Model
		want time.Duration
	}{
		{"explicit wins", config.Model{BaseURL: "http://localhost:11434/v1", Timeout: 42}, 42 * time.Second},
		{"local default 600s", config.Model{BaseURL: "http://localhost:11434/v1"}, 600 * time.Second},
		{"loopback IP default 600s", config.Model{BaseURL: "http://127.0.0.1:8080/v1"}, 600 * time.Second},
		{"remote default 120s", config.Model{BaseURL: "https://api.deepseek.com/v1"}, 120 * time.Second},
		{"empty base_url counts as remote", config.Model{}, 120 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ModelTimeout(c.cfg); got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
}
