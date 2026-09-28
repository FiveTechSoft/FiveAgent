package sandbox

import (
	"testing"
	"unicode/utf16"
	"unsafe"
)

// escapeSinkName and escapeSinkSid pin TestProfileArgsShape's fixtures
// on the heap (see the test for why).
var (
	escapeSinkName *uint16
	escapeSinkSid  *uintptr
)

// TestProfileArgsShape locks the CreateAppContainerProfile call shape:
// exactly six arguments, a non-empty description, nil capabilities,
// zero capability count, and the SID-out pointer last. Regression test
// for the live E_INVALIDARG failure: the call used to pass 4 arguments
// (SID pointer read as capabilities), and later passed a NULL/empty
// description, which Windows 11 also rejects with 0x80070057.
func TestProfileArgsShape(t *testing.T) {
	// Force the fixtures to escape to the heap: converting an
	// unsafe.Pointer to uintptr does NOT make its referent escape, so
	// stack-allocated fixtures can MOVE between the uintptr conversion
	// inside profileArgs and the comparisons below (Go stacks grow and
	// shrink on GC). A moved stack made this test fail intermittently
	// (CI ubuntu/windows jobs, and locally with -count>=20) while the
	// production call was correct. Boxing in a heap struct keeps the
	// addresses stable for the whole test.
	box := &struct {
		name uint16
		sid  uintptr
	}{}
	// Pin the fixture pointers in package-level sinks: escape analysis
	// does not count unsafe.Pointer -> uintptr conversions as escaping,
	// so even a heap-looking local can stay on the goroutine stack and
	// MOVE between the conversion inside profileArgs and the checks
	// below (reproduced locally with -count>=20 and on CI runners).
	escapeSinkName = &box.name
	escapeSinkSid = &box.sid
	args := profileArgs(escapeSinkName, escapeSinkSid)
	if len(args) != createAppContainerProfileArgc {
		t.Fatalf("len(args) = %d, want %d (name, display, description, capabilities, count, sidOut)", len(args), createAppContainerProfileArgc)
	}
	if args[0] != uintptr(unsafe.Pointer(escapeSinkName)) || args[1] != uintptr(unsafe.Pointer(escapeSinkName)) {
		t.Error("args[0] and args[1] must both point at the profile name")
	}
	if args[2] != uintptr(unsafe.Pointer(profileDescription)) {
		t.Errorf("args[2] = %#x, want the profileDescription pointer", args[2])
	}
	if desc := utf16ToString(profileDescription); desc == "" {
		t.Error("profileDescription decodes to an empty string, want non-empty")
	}
	if args[3] != 0 || args[4] != 0 {
		t.Errorf("args[3]=%#x args[4]=%#x, want 0, 0 (no capabilities, zero count)", args[3], args[4])
	}
	if args[5] != uintptr(unsafe.Pointer(escapeSinkSid)) {
		t.Error("args[5] must be the SID-out pointer")
	}
}

// utf16ToString decodes a NUL-terminated UTF-16 string.
func utf16ToString(p *uint16) string {
	if p == nil {
		return ""
	}
	var u []uint16
	for q := unsafe.Pointer(p); ; q = unsafe.Pointer(uintptr(q) + 2) {
		v := *(*uint16)(q)
		if v == 0 {
			break
		}
		u = append(u, v)
	}
	return string(utf16.Decode(u))
}

func TestProfileNameForAlwaysValid(t *testing.T) {
	keys := []string{
		"probe",
		"whatsapp/34600123456",
		"telegram/123456789",
		`C:\Users\Test User\FiveAgent`,
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
