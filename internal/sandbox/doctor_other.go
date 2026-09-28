//go:build !windows

package sandbox

import (
	"os/exec"
	"runtime"
)

// probeBackend mirrors the backend auto-selection in New.
func probeBackend(root string) []string {
	var out []string
	bwrap := ""
	if p, err := exec.LookPath("bwrap"); err == nil {
		bwrap = p
	}
	docker := ""
	if p, err := exec.LookPath("docker"); err == nil {
		docker = p
	}
	switch runtime.GOOS {
	case "linux":
		if bwrap != "" {
			out = append(out, "bubblewrap: available ("+bwrap+")")
		} else {
			out = append(out, "bubblewrap: NOT found - install it (apt install bubblewrap)")
		}
	}
	if docker != "" {
		out = append(out, "docker: available ("+docker+")")
	} else {
		out = append(out, "docker: NOT found")
	}
	switch {
	case runtime.GOOS == "linux" && bwrap != "":
		out = append(out, "effective backend: bubblewrap - full isolation (no network, host fs hidden)")
	case docker != "":
		out = append(out, "effective backend: docker - full isolation (no network, RAM/CPU caps)")
	default:
		out = append(out, "effective backend: NONE - the sandbox would be disabled on this machine")
	}
	return out
}
