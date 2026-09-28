package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

// DoctorReport probes this machine's sandbox capabilities without
// starting the agent. The `fiveagent doctor` subcommand prints it - one
// human-readable line per check. Born from the first live test, where a
// silent AppContainer failure left a PC on the degraded backend.
func DoctorReport(cfg config.Sandbox) []string {
	root := cfg.Root
	if root == "" {
		root = "data/sandbox"
	}
	out := []string{fmt.Sprintf("platform: %s/%s", runtime.GOOS, runtime.GOARCH)}
	out = append(out, probeBackend(root)...)
	dir := filepath.Join(root, "doctor-probe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		out = append(out, fmt.Sprintf("sandbox root %s: NOT writable: %v", root, err))
	} else {
		os.Remove(dir)
		out = append(out, fmt.Sprintf("sandbox root %s: writable", root))
	}
	return out
}
