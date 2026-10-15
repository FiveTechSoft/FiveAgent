package evals

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
)

type fakeSB struct {
	res sandbox.Result
	err error
}

func (fakeSB) Name() string { return "fake" }
func (f fakeSB) Run(context.Context, string, []string) (sandbox.Result, error) {
	return f.res, f.err
}

// A broken environment must be classified as ENV, with the reason kept.
func TestEngineeringPreflightClassifiesEnvironment(t *testing.T) {
	task := "engineering/t01_sum"
	if ok, why := engPreflight(context.Background(), fakeSB{}, t.TempDir(), task); !ok {
		t.Fatalf("exit 0 must pass preflight: %s", why)
	}
	broken := fakeSB{res: sandbox.Result{ExitCode: 1, Stderr: "go: error obtaining buildID: open /dev/null: no such file or directory"}}
	ok, why := engPreflight(context.Background(), broken, t.TempDir(), task)
	if ok || !strings.Contains(why, "/dev/null") {
		t.Fatalf("broken env must fail preflight and keep the reason, got ok=%v why=%q", ok, why)
	}
	if ok, _ := engPreflight(context.Background(), fakeSB{res: sandbox.Result{TimedOut: true}}, t.TempDir(), task); ok {
		t.Fatal("a timeout is an environment problem")
	}
	if ok, _ := engPreflight(context.Background(), fakeSB{err: errors.New("boom")}, t.TempDir(), task); ok {
		t.Fatal("a sandbox error is an environment problem")
	}
}

// With the real sandbox of this machine the golden fix of every task must
// pass inside it. Skipped when Go is not runnable in the sandbox (CI
// Ubuntu), because then no task can be measured there.
func TestEngineeringPreflightRealSandbox(t *testing.T) {
	root := t.TempDir()
	sb, err := sandbox.New(config.Sandbox{Enabled: true, Root: root, Timeout: 120})
	if err != nil {
		t.Skipf("no sandbox backend: %v", err)
	}
	if v, err := sb.Run(context.Background(), "eng/pre", []string{"go", "version"}); err != nil || v.ExitCode != 0 || !strings.Contains(v.Stdout, "go version") {
		t.Skipf("go is not runnable inside the %s sandbox here", sb.Name())
	}
	dirs, _ := filepath.Glob("engineering/t*")
	for _, d := range dirs {
		if ok, why := engPreflight(context.Background(), sb, root, d); !ok {
			t.Errorf("%s: %s", filepath.Base(d), why)
		}
	}
}
