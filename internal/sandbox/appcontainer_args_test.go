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

func TestProfileNameForAlwaysValid(t *testing.T) {
	keys := []string{
		"probe",
		"whatsapp/34600123456",
		"telegram/123456789",
		`C:\Users\Antonio Linares\FiveAgent`,
		"usuario-con-ñ-y-acentos",
		"",
	}
	seen := map[string]string{}
	for _, k := range keys {
		n := profileNameFor(k)
		if err := validProfileName(n); err != nil {
			t.Errorf("profileNameFor(%q) = %q: %v", k, n, err)
		}
		if len(n) > 64 {
			t.Errorf("profileNameFor(%q) too long: %d", k, len(n))
		}
		if other, dup := seen[n]; dup && other != k {
			t.Errorf("profile name collision: %q and %q both give %q", other, k, n)
		}
		seen[n] = k
	}
}

func TestValidProfileName(t *testing.T) {
	valid := []string{"FiveAgentProbe", "FiveAgent-0123abcdef", "a.b_c-d", "x"}
	for _, n := range valid {
		if err := validProfileName(n); err != nil {
			t.Errorf("validProfileName(%q) should pass: %v", n, err)
		}
	}
	invalid := []string{
		"", "with space", `back\slash`, "slash/path", "con-ñ", "colon:name",
		"this-name-is-way-too-long-to-be-a-valid-appcontainer-profile-name-xx",
	}
	for _, n := range invalid {
		if err := validProfileName(n); err == nil {
			t.Errorf("validProfileName(%q) should fail", n)
		}
	}
}
