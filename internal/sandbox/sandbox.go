// Package sandbox runs commands for the agent inside an isolated,
// per-user environment. What is enforced depends on the backend:
//   - bubblewrap (Linux): no network, host filesystem hidden except
//     read-only system dirs, only the user's folder writable, timeout.
//   - docker (any OS with Docker): no network, RAM/CPU caps, only the
//     user's folder mounted writable, timeout.
//   - appcontainer (Windows): AppContainer + Job Object: host files not
//     readable, no network, RAM cap, process tree killed with the job,
//     per-user writable folder, timeout. CI-tested on windows runners;
//     pending live verification on a real PC. jobobject (older
//     fallback) enforces only the RAM cap, process-tree kill and
//     timeout - NO filesystem or network isolation (live-verified on a
//     real Windows PC 2026-09-28: commands run, but the machine is NOT
//     isolated). The fallback logs a loud WARNING; `fiveagent doctor`
//     reports the exact AppContainer probe error.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

// Result is one command's outcome.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

// decodeUTF16 normalizes a stream a Windows child wrote as UTF-16LE.
// WSL's bash.exe does exactly that: inside an AppContainer it fails
// with "Acceso denegado." plus "Código de error: Bash/E_ACCESSDENIED"
// interleaved with NUL bytes (battery run 6, 2026-10-02). The model
// was handed that NUL soup, guessed an error instead of quoting it,
// and the battery scored the guess as a hallucination. Output that is
// not UTF-16 comes back untouched.
func decodeUTF16(s string) string {
	b := []byte(s)
	if len(b) < 4 {
		return s
	}
	zeros := 0
	for i := 1; i < len(b); i += 2 {
		if b[i] == 0 {
			zeros++
		}
	}
	if zeros*4 < len(b) { // fewer than a quarter of the odd bytes are NUL: not UTF-16
		return s
	}
	if b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(u))
}

// Sandbox executes commands in isolation.
type Sandbox interface {
	// Run executes argv with the user's folder as the only writable
	// place. userKey (e.g. "whatsapp/34600123456") selects that folder.
	Run(ctx context.Context, userKey string, argv []string) (Result, error)
	// Name is the backend name, for logs ("bubblewrap", "jobobject", "docker").
	Name() string
}

// New picks the configured backend ("auto" chooses per OS).
func New(cfg config.Sandbox) (Sandbox, error) {
	root := cfg.Root
	if root == "" {
		root = "data/sandbox"
	}
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	maxRAM := cfg.MaxRAM
	if maxRAM <= 0 {
		maxRAM = 512
	}
	image := cfg.Image
	if image == "" {
		image = "alpine"
	}
	backend := cfg.Backend
	if backend == "" || backend == "auto" {
		switch runtime.GOOS {
		case "linux":
			if _, err := exec.LookPath("bwrap"); err == nil {
				backend = "bubblewrap"
			} else if _, err := exec.LookPath("docker"); err == nil {
				backend = "docker"
			} else {
				return nil, fmt.Errorf("sandbox: no backend available: install bubblewrap (recommended) or docker")
			}
		case "windows":
			backend = "appcontainer"
		default: // darwin and others: docker until a native backend lands
			if _, err := exec.LookPath("docker"); err == nil {
				backend = "docker"
			} else {
				return nil, fmt.Errorf("sandbox: on %s only the docker backend is available for now", runtime.GOOS)
			}
		}
	}
	switch backend {
	case "appcontainer":
		sb, err := newAppContainer(root, timeout, maxRAM)
		if err != nil {
			// No AppContainer support: degrade to the weaker Job
			// Objects backend instead of failing - but say it LOUDLY,
			// because the degradation silently drops the network and
			// filesystem isolation (found in the first live test).
			log.Printf("sandbox: WARNING: AppContainer unavailable (%v); falling back to jobobject: NO network or filesystem isolation, only RAM cap / process-tree kill / timeout. Run 'fiveagent doctor' for details", err)
			return newJobObject(root, timeout, maxRAM)
		}
		return sb, nil
	case "bubblewrap":
		return newBubblewrap(root, timeout)
	case "docker":
		return newDocker(root, image, timeout, maxRAM), nil
	case "jobobject":
		return newJobObject(root, timeout, maxRAM)
	default:
		return nil, fmt.Errorf("sandbox: unknown backend %q", backend)
	}
}

// userDir returns the writable folder for a user key, creating it.
// Characters outside [a-zA-Z0-9._-] become "_".
func userDir(root, key string) (string, error) {
	var sb strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("sandbox: empty user key")
	}
	dir := filepath.Join(root, sb.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// capture limits how much output we keep per stream.
const maxOutput = 64 * 1024

type limitedWriter struct {
	buf []byte
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if len(w.buf) < maxOutput {
		n := maxOutput - len(w.buf)
		if n > len(p) {
			n = len(p)
		}
		w.buf = append(w.buf, p[:n]...)
	}
	return len(p), nil
}

// runResult runs cmd and packs the outcome, honouring the ctx timeout.
func runResult(ctx context.Context, cmd *exec.Cmd) (Result, error) {
	var out, errb limitedWriter
	cmd.Stdout = &out
	cmd.Stderr = &errb
	runErr := cmd.Run()
	// A non-zero exit code is a normal outcome (Result.ExitCode), not a
	// sandbox failure; only start/IO failures are errors.
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		runErr = nil
	}
	r := Result{Stdout: decodeUTF16(string(out.buf)), Stderr: decodeUTF16(string(errb.buf))}
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		r.ExitCode = -1
	}
	if ctx.Err() == context.DeadlineExceeded {
		r.TimedOut = true
		runErr = nil // a timeout is an outcome, not a sandbox failure
	}
	return r, runErr
}
