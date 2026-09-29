//go:build linux

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Docker backend tests. They run where docker and the alpine image are
// available (CI pre-pulls it); otherwise they skip.

func dockerOrSkip(t *testing.T, timeout time.Duration) Sandbox {
	t.Helper()
	dockerImageOrSkip(t)
	return newDocker(filepath.Join(t.TempDir(), "sb"), "alpine", timeout, 256)
}

// dockerImageOrSkip skips when docker or the alpine fixture is
// missing, and ALSO when the fixture's architecture does not match
// the host: the tests run binaries from the image, and a wrong-arch
// image (e.g. a linux/amd64 alpine on an ARM64 host) fails with
// "exec format error" instead of exercising the sandbox - observed
// live on an ARM64 GB10 runner, where TestDockerTimeout reported
// exec format error rather than a timeout. Skip honestly; pull a
// multi-arch alpine to run the docker battery on ARM.
func dockerImageOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	out, err := exec.Command("docker", "image", "inspect", "alpine",
		"--format", "{{.Os}}/{{.Architecture}}").Output()
	if err != nil {
		t.Skip("alpine image not available")
	}
	if img, host := strings.TrimSpace(string(out)), runtime.GOOS+"/"+runtime.GOARCH; img != host {
		t.Skipf("alpine fixture is %s but the host is %s: wrong-arch fixtures skip (pull a multi-arch alpine)", img, host)
	}
}

func TestDockerEcho(t *testing.T) {
	sb := dockerOrSkip(t, 60*time.Second)
	r, err := sb.Run(context.Background(), "u1", []string{"echo", "hola"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(r.Stdout) != "hola" || r.ExitCode != 0 {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestDockerWorkDirPersists(t *testing.T) {
	dockerImageOrSkip(t)
	root := filepath.Join(t.TempDir(), "sb")
	sb := newDocker(root, "alpine", 60*time.Second, 256)
	r, err := sb.Run(context.Background(), "u1", []string{"sh", "-c", "echo datos > nota.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 0 {
		t.Fatalf("write failed: %+v", r)
	}
	b, err := os.ReadFile(filepath.Join(root, "u1", "nota.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "datos" {
		t.Fatalf("bad content: %q", b)
	}
}

func TestDockerNoNetwork(t *testing.T) {
	sb := dockerOrSkip(t, 60*time.Second)
	r, err := sb.Run(context.Background(), "u1",
		[]string{"wget", "-q", "-T", "5", "-O", "-", "http://example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode == 0 || strings.Contains(r.Stdout, "Example Domain") {
		t.Fatalf("network should be unreachable inside the sandbox: %+v", r)
	}
}

func TestDockerTimeout(t *testing.T) {
	sb := dockerOrSkip(t, 2*time.Second)
	r, err := sb.Run(context.Background(), "u1", []string{"sleep", "60"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.TimedOut {
		t.Fatalf("expected timeout, got %+v", r)
	}
}
