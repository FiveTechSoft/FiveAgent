package evals

import (
	"context"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
)

// TestSandboxGoToolchainProbe records (never fails on) whether `go test`
// runs inside the sandbox backend of THIS machine. Engineering tasks need it.
func TestSandboxGoToolchainProbe(t *testing.T) {
	sb, err := sandbox.New(config.Sandbox{Enabled: true, Root: t.TempDir(), Timeout: 120})
	if err != nil {
		t.Skipf("no sandbox backend: %v", err)
	}
	res, err := sb.Run(context.Background(), "eng/u1", []string{"go", "version"})
	t.Logf("PROBE backend=%s go version: res=%+v err=%v", sb.Name(), res, err)
}

// TestSandboxGoTestProbe records whether `go test` itself works in the sandbox on a golden task.
func TestSandboxGoTestProbe(t *testing.T) {
	root := t.TempDir()
	sb, err := sandbox.New(config.Sandbox{Enabled: true, Root: root, Timeout: 120})
	if err != nil {
		t.Skipf("no sandbox backend: %v", err)
	}
	if _, err := sb.Run(context.Background(), "eng/u1", []string{"true"}); err != nil {
		t.Skipf("sandbox cannot run: %v", err)
	}
	if v, err := sb.Run(context.Background(), "eng/u1", []string{"go", "version"}); err != nil || v.ExitCode != 0 || !strings.Contains(v.Stdout, "go version") {
		t.Skipf("go is not runnable inside the %s sandbox on this machine (toolchain outside the mounted system dirs): %+v %v", sb.Name(), v, err)
	}
	work := root + "/eng_u1"
	copyTree(t, "engineering/t01_sum/task", work)
	res, err := sb.Run(context.Background(), "eng/u1", []string{"go", "test", "./..."})
	if sb.Name() == "bubblewrap" && !strings.Contains(res.Stdout, "--- FAIL: TestSum") {
		t.Fatalf("go test must reach the seeded failure inside bubblewrap (missing /dev regression): %+v", res)
	}
	t.Logf("PROBE backend=%s go test (expected FAIL exit 1 from the seeded bug): exit=%d err=%v stdout=%.300q stderr=%.300q", sb.Name(), res.ExitCode, err, res.Stdout, res.Stderr)
}

// TestBubblewrapHasDevNull is the toolchain-independent regression for the
// missing /dev: a shell redirect to /dev/null must work inside bubblewrap.
func TestBubblewrapHasDevNull(t *testing.T) {
	sb, err := sandbox.New(config.Sandbox{Enabled: true, Root: t.TempDir(), Timeout: 60})
	if err != nil || sb.Name() != "bubblewrap" {
		t.Skip("bubblewrap backend not in use")
	}
	if res, err := sb.Run(context.Background(), "eng/u1", []string{"true"}); err != nil || res.ExitCode != 0 {
		t.Skipf("bubblewrap cannot run here: %+v %v", res, err)
	}
	res, err := sb.Run(context.Background(), "eng/u1", []string{"sh", "-c", "echo ok > /dev/null && echo devnull-ok"})
	if err != nil || !strings.Contains(res.Stdout, "devnull-ok") {
		t.Fatalf("/dev/null missing inside bubblewrap: %+v %v", res, err)
	}
}
