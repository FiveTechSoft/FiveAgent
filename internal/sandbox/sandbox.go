// Package sandbox runs commands for the agent inside an isolated,
// per-user environment. What is enforced depends on the backend:
//   - bubblewrap (Linux): no network, host filesystem hidden except
//     read-only system dirs, only the user's folder writable, timeout.
//   - docker (any OS with Docker): no network, RAM/CPU caps, only the
//     user's folder mounted writable, timeout.
//   - jobobject (Windows): process tree killed with the job, RAM cap,
//     per-user working directory, timeout. Filesystem and network
//     isolation are NOT enforced yet (AppContainer is the next step).
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

// Result is one command's outcome.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
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
			backend = "jobobject"
		default: // darwin and others: docker until a native backend lands
			if _, err := exec.LookPath("docker"); err == nil {
				backend = "docker"
			} else {
				return nil, fmt.Errorf("sandbox: on %s only the docker backend is available for now", runtime.GOOS)
			}
		}
	}
	switch backend {
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
	r := Result{Stdout: string(out.buf), Stderr: string(errb.buf)}
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
