//go:build windows

package sandbox

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests run on Windows (go test ./internal/sandbox/ on a Windows PC).

func TestJobObjectEcho(t *testing.T) {
	sb, err := newJobObject(filepath.Join(t.TempDir(), "sb"), 10*time.Second, 512)
	if err != nil {
		t.Fatal(err)
	}
	r, err := sb.Run(context.Background(), "u1", []string{"cmd", "/c", "echo hola"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Stdout, "hola") || r.ExitCode != 0 {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestJobObjectTimeout(t *testing.T) {
	sb, err := newJobObject(t.TempDir(), 500*time.Millisecond, 512)
	if err != nil {
		t.Fatal(err)
	}
	r, err := sb.Run(context.Background(), "u1",
		[]string{"cmd", "/c", "ping", "-n", "30", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.TimedOut {
		t.Fatalf("expected timeout, got %+v", r)
	}
}
