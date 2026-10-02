//go:build windows

package sandbox

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Battery run 6 (2026-10-02) lost a truthful narration: the audit line
// carried stderr only, and this host's children print on stdout.
// Capturing stderr at all is the ground truth that check compares with.
func TestAppContainerCapturesStderr(t *testing.T) {
	sb := appContainerOrSkip(t, 15*time.Second, 512)
	r, err := sb.Run(context.Background(), "u1", []string{"cmd", "/c", "echo aviso 1>&2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Stderr, "aviso") {
		t.Fatalf("stderr not captured: %+v", r)
	}
}

// WSL's bash.exe writes UTF-16LE (inside an AppContainer it fails with
// "Acceso denegado" + Bash/E_ACCESSDENIED). Without decoding, the model
// gets NUL soup and guesses the error instead of quoting it. Skipped
// when this host has no WSL or no AppContainer profile.
func TestAppContainerDecodesUTF16ChildOutput(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sb")
	sb, err := newAppContainer(root, 15*time.Second, 512)
	if err != nil {
		t.Skipf("AppContainer not available: %v", err)
	}
	r, err := sb.Run(context.Background(), "u1", []string{"bash", "-c", "comando_que_no_existe_xyz123"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode == 0 {
		t.Skipf("bash ran without error on this host: %+v", r)
	}
	if strings.ContainsRune(r.Stdout, 0) {
		t.Fatalf("UTF-16 child output was not decoded: %q", r.Stdout)
	}
	if r.Stdout != "" && !strings.Contains(r.Stdout, "denegado") && !strings.Contains(r.Stdout, "denied") {
		t.Skipf("host's bash answered something else: %q", r.Stdout)
	}
}
