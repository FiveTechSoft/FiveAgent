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
	return sb
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
	r, err := sb.Run(context.Background(), "u1", []string{"sleep", "30"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.TimedOut {
		t.Fatalf("expected timeout, got %+v", r)
	}
}
