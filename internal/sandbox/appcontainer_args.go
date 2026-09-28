package sandbox

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"unicode/utf16"
	"unsafe"
)

// createAppContainerProfileArgc is the exact argument count of the Win32
// CreateAppContainerProfile call: app container name, display name,
// description, capabilities array, capability count, out SID pointer.
// Passing fewer arguments lets the API read stack garbage as the
// capability count and SID-out pointer - that was the live E_INVALIDARG
// (0x80070057) failure on a standard Windows PC.
const createAppContainerProfileArgc = 6

// profileDescription is pszDescription. The API documents it as
// optional, but Windows 11 (build 22621) returns E_INVALIDARG
// (0x80070057) for a NULL or empty description - verified live with an
// independent P/Invoke matrix probe: NULL -> 0x80070057, "" -> 0x80070057,
// "D" -> S_OK. So it must be a non-empty string on every call.
var profileDescription = utf16z("FiveAgent sandbox profile")

// utf16z returns a pointer to s as a NUL-terminated UTF-16 string. The
// backing array is package-level and never freed, so the pointer stays
// valid for the whole process (syscall reads it during the call).
func utf16z(s string) *uint16 {
	u := append(utf16.Encode([]rune(s)), 0)
	return &u[0]
}

// profileArgs builds the argument list for CreateAppContainerProfile.
// Description is a non-empty constant (see profileDescription) and
// capabilities are nil with a zero capability count: a sandbox profile
// must get NO capabilities (no network access). The last argument
// receives the created profile's SID.
func profileArgs(name *uint16, sidOut *uintptr) []uintptr {
	return []uintptr{
		uintptr(unsafe.Pointer(name)),               // pszAppContainerName
		uintptr(unsafe.Pointer(name)),               // pszDisplayName
		uintptr(unsafe.Pointer(profileDescription)), // pszDescription: never NULL/empty
		0,                               // pCapabilities: none
		0,                               // dwCapabilityCount: zero
		uintptr(unsafe.Pointer(sidOut)), // ppSidAppContainerSid
	}
}

// profileNameFor derives the AppContainer profile name for a user key:
// a short prefix plus a hex digest, so any user key (phone numbers,
// paths, unicode) lands inside the documented name rules.
func profileNameFor(userKey string) string {
	h := sha1.Sum([]byte(userKey))
	return "FiveAgent-" + hex.EncodeToString(h[:])[:16]
}

// profileNameRule documents the CreateAppContainerProfile name rules:
// max 64 chars, letters/digits/dot/underscore/hyphen only - anything
// else makes the API return E_INVALIDARG.
var profileNameRule = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// validProfileName checks a profile name against the documented rules.
func validProfileName(name string) error {
	if !profileNameRule.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: max 64 chars, only A-Z a-z 0-9 . _ -", name)
	}
	return nil
}
