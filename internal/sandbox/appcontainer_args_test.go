package sandbox

import (
	"testing"
	"unsafe"
)

// TestProfileArgsShape locks the CreateAppContainerProfile call shape:
// exactly six arguments, nil description/capabilities, zero capability
// count, and the SID-out pointer last. Regression test for the live
// E_INVALIDARG failure (the call used to pass 4 arguments, so the API
// read the SID pointer as the capabilities array and stack garbage as
// the count).
func TestProfileArgsShape(t *testing.T) {
	var sid uintptr
	var name uint16
	args := profileArgs(&name, &sid)
	if len(args) != createAppContainerProfileArgc {
		t.Fatalf("len(args) = %d, want %d (name, display, description, capabilities, count, sidOut)", len(args), createAppContainerProfileArgc)
	}
	if args[0] != uintptr(unsafe.Pointer(&name)) || args[1] != uintptr(unsafe.Pointer(&name)) {
		t.Error("args[0] and args[1] must both point at the profile name")
	}
	for i := 2; i <= 4; i++ {
		if args[i] != 0 {
			t.Errorf("args[%d] = %#x, want 0 (no description, no capabilities, zero count)", i, args[i])
		}
	}
	if args[5] != uintptr(unsafe.Pointer(&sid)) {
		t.Error("args[5] must be the SID-out pointer")
	}
}
