package sandbox

import "unsafe"

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
