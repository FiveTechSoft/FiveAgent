//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// bubblewrap runs commands through bwrap(1): new namespaces for
// everything (no network), host filesystem hidden except the read-only
// system directories, and only the user's folder writable at /work.
type bubblewrap struct {
	root    string
	timeout time.Duration
}

func newBubblewrap(root string, timeout time.Duration) (Sandbox, error) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		return nil, fmt.Errorf("sandbox: bubblewrap backend needs bwrap installed (apt install bubblewrap)")
	}
	return &bubblewrap{root: root, timeout: timeout}, nil
}

func (b *bubblewrap) Name() string { return "bubblewrap" }

func (b *bubblewrap) Run(ctx context.Context, userKey string, argv []string) (Result, error) {
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("sandbox: empty command")
	}
	dir, err := userDir(b.root, userKey)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bwrap", append(bwrapArgs(dir), argv...)...)
	return runResult(ctx, cmd)
}

// bwrapArgs builds the argument list, binding /lib64 when it exists.
func bwrapArgs(workDir string) []string {
	args := []string{
		"--unshare-all", "--die-with-parent",
		"--ro-bind", "/usr", "/usr",
		"--ro-bind", "/bin", "/bin",
		"--ro-bind", "/sbin", "/sbin",
		"--ro-bind", "/lib", "/lib",
	}
	if _, err := os.Stat("/lib64"); err == nil {
		args = append(args, "--ro-bind", "/lib64", "/lib64")
	}
	if _, err := os.Stat("/etc/ld.so.cache"); err == nil {
		args = append(args, "--ro-bind", "/etc/ld.so.cache", "/etc/ld.so.cache")
	}
	return append(args,
		"--tmpfs", "/tmp",
		"--proc", "/proc",
		"--bind", workDir, "/work",
		"--chdir", "/work",
		"--setenv", "HOME", "/work",
		"--",
	)
}
