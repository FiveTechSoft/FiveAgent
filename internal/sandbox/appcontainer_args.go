package sandbox

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"unsafe"
)

// createAppContainerProfileArgc is the exact argument count of the Win32
// CreateAppContainerProfile call: app container name, display name,
// description, capabilities array, capability count, out SID pointer.
// Passing fewer arguments lets the API read stack garbage as the
// capability count and SID-out pointer - that was the live E_INVALIDARG
// (0x80070057) failure on a standard Windows PC.
const createAppContainerProfileArgc = 6

// profileArgs builds the argument list for CreateAppContainerProfile.
// Description and capabilities are nil and the capability count is
// zero: a sandbox profile must get NO capabilities (no network access).
// The last argument receives the created profile's SID.
func profileArgs(name *uint16, sidOut *uintptr) []uintptr {
	return []uintptr{
		uintptr(unsafe.Pointer(name)),   // pszAppContainerName
		uintptr(unsafe.Pointer(name)),   // pszDisplayName
		0,                               // pszDescription (optional)
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
