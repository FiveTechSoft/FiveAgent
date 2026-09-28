//go:build windows

package sandbox

import (
	"time"
	"unsafe"
)

// probeBackend tries the AppContainer probe directly so the report can
// carry the exact failure (the HRESULT names the cause). It prints the
// derived profile name and, on failure, retries with a fixed trivial
// name: that separates "our derived name is invalid" from "this
// machine cannot create AppContainer profiles".
func probeBackend(root string) []string {
	probeName := profileNameFor("probe")
	out := []string{"appcontainer probe profile name: " + probeName}
	if err := validProfileName(probeName); err != nil {
		out = append(out, "probe name INVALID: "+err.Error())
	}
	_, err := newAppContainer(root, 30*time.Second, 512)
	if err == nil {
		return append(out,
			"appcontainer: available (probe profile created and deleted)",
			"effective backend: appcontainer - full isolation (no network, host files unreadable, RAM cap)")
	}
	out = append(out, "appcontainer: UNAVAILABLE - "+err.Error())
	if sid, name, err2 := createProfileNamed("FiveAgentProbe"); err2 == nil {
		procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(name)))
		localFree(sid)
		out = append(out, "second probe with fixed name \"FiveAgentProbe\": WORKS - the derived probe name was the problem")
	} else {
		out = append(out, "second probe with fixed name \"FiveAgentProbe\": also fails ("+err2.Error()+") - the name is not the cause")
	}
	return append(out,
		"effective backend: jobobject - DEGRADED: NO network or filesystem isolation (RAM cap, process-tree kill and timeout only)")
}
