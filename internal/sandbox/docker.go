package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// docker runs commands in a throwaway container: no network, memory
// and CPU caps, only the user's folder mounted at /work.
type docker struct {
	root    string
	image   string
	timeout time.Duration
	maxRAM  int
}

func newDocker(root, image string, timeout time.Duration, maxRAM int) Sandbox {
	return &docker{root: root, image: image, timeout: timeout, maxRAM: maxRAM}
}

func (d *docker) Name() string { return "docker" }

func (d *docker) Run(ctx context.Context, userKey string, argv []string) (Result, error) {
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("sandbox: empty command")
	}
	dir, err := userDir(d.root, userKey)
	if err != nil {
		return Result{}, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	args := []string{
		"run", "--rm",
		"--network", "none",
		"--memory", strconv.Itoa(d.maxRAM) + "m",
		"--cpus", "1",
		"--read-only",
		"--tmpfs", "/tmp",
		"-v", abs + ":/work",
		"-w", "/work",
		d.image,
	}
	cmd := exec.CommandContext(ctx, "docker", append(args, argv...)...)
	return runResult(ctx, cmd)
}
