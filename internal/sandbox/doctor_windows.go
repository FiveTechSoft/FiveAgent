//go:build windows

package sandbox

import "time"

// probeBackend tries the AppContainer probe directly so the report can
// carry the exact failure (the HRESULT names the cause).
func probeBackend(root string) []string {
	if _, err := newAppContainer(root, 30*time.Second, 512); err != nil {
		return []string{
			"appcontainer: UNAVAILABLE - " + err.Error(),
			"effective backend: jobobject - DEGRADED: NO network or filesystem isolation (RAM cap, process-tree kill and timeout only)",
		}
	}
	return []string{
		"appcontainer: available (probe profile created and deleted)",
		"effective backend: appcontainer - full isolation (no network, host files unreadable, RAM cap)",
	}
}
