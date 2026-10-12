package evals

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// TestEngineeringLive runs the engineering tasks through the real agent
// (read_file/edit_file/write_file/run_command in the sandbox) against a
// model server, and reports solved/total. Skipped unless
// FIVEAGENT_EVAL_ENG_LIVE=1. Env: FIVEAGENT_EVAL_BASE_URL,
// FIVEAGENT_EVAL_MODEL, FIVEAGENT_EVAL_API_KEY (optional),
// FIVEAGENT_ENG_TIMEOUT (seconds per command, default 120).
//
// Not run in CI: the number it prints is only meaningful for the model
// and sandbox backend it names, and the output states both. Nothing here
// is a result until someone has run it.
func TestEngineeringLive(t *testing.T) {
	if os.Getenv("FIVEAGENT_EVAL_ENG_LIVE") != "1" {
		t.Skip("set FIVEAGENT_EVAL_ENG_LIVE=1 and FIVEAGENT_EVAL_BASE_URL/MODEL (needs a model server)")
	}
	base, name := os.Getenv("FIVEAGENT_EVAL_BASE_URL"), os.Getenv("FIVEAGENT_EVAL_MODEL")
	if base == "" || name == "" {
		t.Fatal("FIVEAGENT_EVAL_BASE_URL and FIVEAGENT_EVAL_MODEL are required")
	}
	cmdTimeout := 120
	if v := os.Getenv("FIVEAGENT_ENG_TIMEOUT"); v != "" {
		if _, err := fmt.Sscan(v, &cmdTimeout); err != nil {
			t.Fatal(err)
		}
	}
	dirs, _ := filepath.Glob("engineering/t*")
	solved := 0
	for _, d := range dirs {
		sbRoot := t.TempDir()
		sb, err := sandbox.New(config.Sandbox{Enabled: true, Root: sbRoot, Timeout: cmdTimeout})
		if err != nil {
			t.Fatalf("no sandbox backend: %v", err)
		}
		// Sandbox key "eng/u1" maps to folder eng_u1; the file tools use the same folder.
		work := filepath.Join(sbRoot, "eng_u1")
		if err := os.MkdirAll(work, 0o700); err != nil {
			t.Fatal(err)
		}
		copyTree(t, filepath.Join(d, "task"), work)
		cfg := &config.Config{Model: config.Model{BaseURL: base, Name: name, APIKey: os.Getenv("FIVEAGENT_EVAL_API_KEY")}}
		ws := tools.Workspace{Root: sbRoot, Fixed: "eng_u1"}
		reg := tools.NewRegistry(tools.ReadFile{WS: ws}, tools.EditFile{WS: ws}, tools.WriteFile{WS: ws}, tools.RunCommand{SB: sb})
		st, err := memory.OpenJSON(filepath.Join(t.TempDir(), "h.json"))
		if err != nil {
			t.Fatal(err)
		}
		a := agent.New(model.NewOpenAICompat(cfg.Model), st, reg, agent.SystemPrompt(cfg))
		ctx, cancel := context.WithTimeout(tools.WithRequestInfo(context.Background(), "eng", "u1"), 10*time.Minute)
		reply, err := a.Handle(ctx, "eng", "u1", "The Go module in your folder has a failing test. Run `go test ./...`, find the bug in the non-test code and fix it. Do not modify any *_test.go file. Stop when the tests pass.")
		cancel()
		ok := err == nil && engSolved(t, filepath.Join(d, "task"), work)
		if ok {
			solved++
		}
		t.Logf("ENG %s solved=%v err=%v reply=%.120q", filepath.Base(d), ok, err, reply)
	}
	t.Logf("METRIC engineering model=%s sandbox-timeout=%ds solved %d/%d (one attempt per task)", name, cmdTimeout, solved, len(dirs))
}
