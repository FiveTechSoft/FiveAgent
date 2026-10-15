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
	solved, agentFail, envFail, modelErr := 0, 0, 0, 0
	for _, d := range dirs {
		sbRoot := t.TempDir()
		sb, err := sandbox.New(config.Sandbox{Enabled: true, Root: sbRoot, Timeout: cmdTimeout})
		if err != nil {
			t.Fatalf("no sandbox backend: %v", err)
		}
		// ENV check first: the known-good fix must pass inside this very
		// sandbox. If it does not, the task cannot measure the agent.
		if ok, why := engPreflight(context.Background(), sb, sbRoot, d); !ok {
			envFail++
			t.Logf("ENG %s ENV_FAIL (agent not run): %s", filepath.Base(d), why)
			continue
		}
		// Sandbox key "eng/u1" maps to folder eng_u1; the file tools use the same folder.
		work := filepath.Join(sbRoot, "eng_u1")
		if err := os.RemoveAll(work); err != nil {
			t.Fatal(err)
		}
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
		if err != nil {
			modelErr++
			t.Logf("ENG %s MODEL_ERROR (not counted as agent failure): %v", filepath.Base(d), err)
			continue
		}
		ok, why := engGrade(t, filepath.Join(d, "task"), work)
		if ok {
			solved++
		} else {
			agentFail++
		}
		t.Logf("ENG %s solved=%v why=%q reply=%.120q", filepath.Base(d), ok, why, reply)
	}
	t.Logf("METRIC engineering model=%s sandbox-timeout=%ds solved %d/%d of tasks the agent actually attempted (agent_fail=%d, env_fail=%d, model_error=%d; one attempt per task)", name, cmdTimeout, solved, solved+agentFail, agentFail, envFail, modelErr)
}

// engPreflight proves the environment can pass the task: the golden fix is
// copied over the task inside the sandbox folder and `go test` must exit 0
// there. A failure here is an ENVIRONMENT problem (missing toolchain, missing
// /dev, timeout), never an agent failure.
func engPreflight(ctx context.Context, sb sandbox.Sandbox, root, taskID string) (bool, string) {
	pre := filepath.Join(root, "eng_pre")
	if err := os.RemoveAll(pre); err != nil {
		return false, err.Error()
	}
	if err := os.MkdirAll(pre, 0o700); err != nil {
		return false, err.Error()
	}
	for _, sub := range []string{"task", "golden"} {
		ents, err := os.ReadDir(filepath.Join(taskID, sub))
		if err != nil {
			return false, err.Error()
		}
		for _, e := range ents {
			b, err := os.ReadFile(filepath.Join(taskID, sub, e.Name()))
			if err != nil {
				return false, err.Error()
			}
			if err := os.WriteFile(filepath.Join(pre, e.Name()), b, 0o644); err != nil {
				return false, err.Error()
			}
		}
	}
	res, err := sb.Run(ctx, "eng/pre", []string{"go", "test", "-count=1", "-timeout", "60s", "./..."})
	if err != nil {
		return false, "sandbox error: " + err.Error()
	}
	if res.TimedOut || res.ExitCode != 0 {
		return false, fmt.Sprintf("golden fix does not pass in the sandbox: exit=%d timedOut=%v stdout=%.200q stderr=%.200q", res.ExitCode, res.TimedOut, res.Stdout, res.Stderr)
	}
	return true, ""
}
