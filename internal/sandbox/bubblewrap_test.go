//go:build linux

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bwrapOrSkip(t *testing.T) Sandbox {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	sb, err := newBubblewrap(filepath.Join(t.TempDir(), "sb"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	bwrapViableOrSkip(t, sb)
	return sb
}

// bwrapViableOrSkip preflights the backend with a trivial command:
// bwrap can be installed yet unable to set up its sandbox on a
// restricted host - loopback configuration fails with "RTM_NEWADDR:
// Operation not permitted" where network permissions are locked down
// (observed live on an ARM64 GB10 runner, failing 3 tests in this
// group). That host cannot exercise this backend, so skip with the
// reason logged instead of failing every test.
func bwrapViableOrSkip(t *testing.T, sb Sandbox) {
	t.Helper()
	if r, err := sb.Run(context.Background(), "preflight", []string{"true"}); err != nil {
		t.Skipf("bubblewrap not viable on this host: %v", err)
	} else if r.ExitCode != 0 {
		t.Skipf("bubblewrap not viable on this host: preflight exit %d: %s", r.ExitCode, r.Stderr)
	}
}

func TestBubblewrapEcho(t *testing.T) {
	sb := bwrapOrSkip(t)
	r, err := sb.Run(context.Background(), "u1", []string{"echo", "hola"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(r.Stdout) != "hola" || r.ExitCode != 0 {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestBubblewrapWorkDirIsWritableAndPersists(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sb")
	sb, err := newBubblewrap(root, 5*time.Second)
	if err != nil {
		t.Skip(err)
	}
	bwrapViableOrSkip(t, sb)
	if _, err := sb.Run(context.Background(), "u1", []string{"sh", "-c", "echo datos > nota.txt"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "u1", "nota.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "datos" {
		t.Fatalf("bad content: %q", b)
	}
}

func TestBubblewrapCannotSeeHostFiles(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "fiveagent.yml")
	if err := os.WriteFile(secret, []byte("token: secreto"), 0o600); err != nil {
		t.Fatal(err)
	}
	sb := bwrapOrSkip(t)
	r, err := sb.Run(context.Background(), "u1", []string{"cat", secret})
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode == 0 || strings.Contains(r.Stdout, "secreto") {
		t.Fatalf("host file leaked into sandbox: %+v", r)
	}
}

func TestBubblewrapNoNetwork(t *testing.T) {
	sb := bwrapOrSkip(t)
	r, err := sb.Run(context.Background(), "u1",
		[]string{"bash", "-c", "echo x > /dev/tcp/1.1.1.1/80"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode == 0 {
		t.Fatal("network should be unreachable inside the sandbox")
	}
}

func TestBubblewrapTimeout(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	sb, err := newBubblewrap(t.TempDir(), 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	bwrapViableOrSkip(t, sb)
	r, err := sb.Run(context.Background(), "u1", []string{"sleep", "30"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.TimedOut {
		t.Fatalf("expected timeout, got %+v", r)
	}
}
