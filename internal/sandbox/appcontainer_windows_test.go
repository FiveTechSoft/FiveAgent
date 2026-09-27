//go:build windows

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests prove the AppContainer backend on a real Windows PC:
// go test ./internal/sandbox/ -run AppContainer -v

func appContainerOrSkip(t *testing.T, timeout time.Duration, maxRAM int) Sandbox {
	t.Helper()
	sb, err := newAppContainer(filepath.Join(t.TempDir(), "sb"), timeout, maxRAM)
	if err != nil {
		t.Skipf("AppContainer not available: %v", err)
	}
	return sb
}

func TestAppContainerEcho(t *testing.T) {
	sb := appContainerOrSkip(t, 15*time.Second, 512)
	r, err := sb.Run(context.Background(), "u1", []string{"cmd", "/c", "echo hola"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Stdout, "hola") || r.ExitCode != 0 {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestAppContainerWorkDirWritable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sb")
	sb, err := newAppContainer(root, 15*time.Second, 512)
	if err != nil {
		t.Skip(err)
	}
	r, err := sb.Run(context.Background(), "u1",
		[]string{"cmd", "/c", "echo datos > nota.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 0 {
		t.Fatalf("cannot write in work dir: %+v", r)
	}
	b, err := os.ReadFile(filepath.Join(root, "u1", "nota.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "datos") {
		t.Fatalf("bad content: %q", b)
	}
}

func TestAppContainerCannotReadHostFile(t *testing.T) {
	// Decoy "secret" in the user's temp dir: readable by the user,
	// must NOT be readable by the AppContainer process.
	secret := filepath.Join(t.TempDir(), "fiveagent.yml")
	if err := os.WriteFile(secret, []byte("token: secreto"), 0o600); err != nil {
		t.Fatal(err)
	}
	sb := appContainerOrSkip(t, 15*time.Second, 512)
	r, err := sb.Run(context.Background(), "u1",
		[]string{"cmd", "/c", "type", secret})
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode == 0 || strings.Contains(r.Stdout, "secreto") {
		t.Fatalf("host file leaked into sandbox: %+v", r)
	}
}

func TestAppContainerNoNetwork(t *testing.T) {
	sb := appContainerOrSkip(t, 30*time.Second, 512)
	// curl.exe ships with Windows 10+; an AppContainer with no
	// capabilities must fail to open any connection.
	r, err := sb.Run(context.Background(), "u1",
		[]string{`C:\Windows\System32\curl.exe`, "-m", "10", "-sS", "http://example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode == 0 || strings.Contains(r.Stdout, "Example Domain") {
		t.Fatalf("network should be unreachable inside the sandbox: %+v", r)
	}
}

func TestAppContainerTimeout(t *testing.T) {
	sb := appContainerOrSkip(t, 1*time.Second, 512)
	r, err := sb.Run(context.Background(), "u1",
		[]string{"cmd", "/c", "ping", "-n", "30", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.TimedOut {
		t.Fatalf("expected timeout, got %+v", r)
	}
}

func TestAppContainerRAMCap(t *testing.T) {
	// 256 MiB cap: PowerShell alone fits, but allocating a 600 MB array
	// must fail inside the Job Object.
	sb := appContainerOrSkip(t, 30*time.Second, 256)
	r, err := sb.Run(context.Background(), "u1",
		[]string{"powershell", "-NoProfile", "-Command",
			"$x = New-Object byte[] 600MB; $x[0] = 1; echo ALLOCATED"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.Stdout, "ALLOCATED") {
		t.Fatalf("RAM cap not enforced: %+v", r)
	}
}
