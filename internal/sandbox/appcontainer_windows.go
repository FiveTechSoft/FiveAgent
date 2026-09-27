//go:build windows

package sandbox

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// appcontainer runs commands inside a Windows AppContainer (the same
// isolation UWP apps get), combined with a Job Object:
//   - filesystem: nothing of the host is visible except system dirs;
//     only the user's folder is granted to the AppContainer SID
//   - network: none (the sandbox is created with no capabilities)
//   - memory: capped by the Job Object; the process tree dies with it
//   - timeout: the job is terminated when it expires
var (
	userenv                       = syscall.NewLazyDLL("userenv.dll")
	advapi32                      = syscall.NewLazyDLL("advapi32.dll")
	procCreateAppContainerProfile = userenv.NewProc("CreateAppContainerProfile")
	procDeleteAppContainerProfile = userenv.NewProc("DeleteAppContainerProfile")
	procGetNamedSecurityInfoW     = advapi32.NewProc("GetNamedSecurityInfoW")
	procSetNamedSecurityInfoW     = advapi32.NewProc("SetNamedSecurityInfoW")
	procSetEntriesInAclW          = advapi32.NewProc("SetEntriesInAclW")
	procLocalFree                 = kernel32.NewProc("LocalFree")
	procInitializeProcThreadAttr  = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttr      = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttr      = kernel32.NewProc("DeleteProcThreadAttributeList")
	procCreateProcessW            = kernel32.NewProc("CreateProcessW")
	procResumeThread              = kernel32.NewProc("ResumeThread")
	procWaitForSingleObject       = kernel32.NewProc("WaitForSingleObject")
	procTerminateJobObject        = kernel32.NewProc("TerminateJobObject")
	procGetExitCodeProcess        = kernel32.NewProc("GetExitCodeProcess")
)

const (
	procThreadAttributeSecurityCapabilities = 0x00020000 | 9 // PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES
	procThreadAttributeHandleList           = 0x00020000 | 2 // PROC_THREAD_ATTRIBUTE_HANDLE_LIST
	extendedStartupinfoPresent              = 0x00080000
	createSuspended                         = 0x00000004
	createNoWindow                          = 0x08000000
	startfUseStdHandles                     = 0x00000100
	handleFlagInherit                       = 0x00000001
	seFileObject                            = 1
	daclSecurityInformation                 = 0x00000004
	grantAccess                             = 1
	subContainersAndObjectsInherit          = 0x3
	trusteeIsSid                            = 0
	trusteeIsUser                           = 1
	waitTimeout                             = 0x00000102
	genericAll                              = 0x10000000
)

type securityCapabilities struct {
	appContainerSid uintptr
	capabilities    uintptr
	capabilityCount uint32
	reserved        uint32
}

type startupInfoExW struct {
	startupInfo     syscall.StartupInfo
	lpAttributeList uintptr
}

type trusteeW struct {
	pMultipleTrustee         uintptr
	multipleTrusteeOperation uint32
	trusteeForm              uint32
	trusteeType              uint32
	_                        uint32 // alignment padding on x64
	ptstrName                uintptr
}

type explicitAccessW struct {
	grfAccessPermissions uint32
	grfAccessMode        uint32
	grfInheritance       uint32
	trustee              trusteeW
}

type appcontainer struct {
	root    string
	timeout time.Duration
	maxRAM  int
}

// newAppContainer probes AppContainer support by creating and deleting
// a throwaway profile; on failure the caller falls back to jobobject.
func newAppContainer(root string, timeout time.Duration, maxRAM int) (Sandbox, error) {
	a := &appcontainer{root: root, timeout: timeout, maxRAM: maxRAM}
	sid, name, err := a.createProfile("probe")
	if err != nil {
		return nil, err
	}
	procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(name)))
	localFree(sid)
	return a, nil
}

func (a *appcontainer) Name() string { return "appcontainer" }

// createProfile makes an AppContainer profile named after the user key
// (stable hash), returning its SID and the UTF-16 name for cleanup.
func (a *appcontainer) createProfile(userKey string) (sid uintptr, name *uint16, err error) {
	h := sha1.Sum([]byte(userKey))
	profileName := "FiveAgent-" + hex.EncodeToString(h[:])[:16]
	name, err = syscall.UTF16PtrFromString(profileName)
	if err != nil {
		return 0, nil, err
	}
	r, _, errNo := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0,
		uintptr(unsafe.Pointer(&sid)))
	if r != 0 { // HRESULT: 0 (S_OK) or 0x800700B7 (already exists) are fine
		if uint32(r) != 0x800700B7 {
			return 0, nil, fmt.Errorf("sandbox: CreateAppContainerProfile: hr=0x%08x (%v)", uint32(r), errNo)
		}
	}
	if sid == 0 {
		return 0, nil, fmt.Errorf("sandbox: CreateAppContainerProfile returned no SID")
	}
	return sid, name, nil
}

// grantFolder gives the AppContainer SID full control of dir, merging
// with the existing ACL (children inherit it).
func grantFolder(dir string, sid uintptr) error {
	path, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	var oldDACL, secDesc uintptr
	r, _, errNo := procGetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(path)), seFileObject, daclSecurityInformation,
		0, 0, uintptr(unsafe.Pointer(&oldDACL)), 0, uintptr(unsafe.Pointer(&secDesc)))
	if r != 0 {
		return fmt.Errorf("sandbox: GetNamedSecurityInfo: %v", errNo)
	}
	defer procLocalFree.Call(secDesc)

	ea := explicitAccessW{
		grfAccessPermissions: genericAll,
		grfAccessMode:        grantAccess,
		grfInheritance:       subContainersAndObjectsInherit,
	}
	ea.trustee.trusteeForm = trusteeIsSid
	ea.trustee.trusteeType = trusteeIsUser
	ea.trustee.ptstrName = sid

	var newDACL uintptr
	r, _, errNo = procSetEntriesInAclW.Call(
		1, uintptr(unsafe.Pointer(&ea)), oldDACL, uintptr(unsafe.Pointer(&newDACL)))
	if r != 0 {
		return fmt.Errorf("sandbox: SetEntriesInAcl: %v", errNo)
	}
	defer procLocalFree.Call(newDACL)

	r, _, errNo = procSetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(path)), seFileObject, daclSecurityInformation,
		0, 0, newDACL, 0)
	if r != 0 {
		return fmt.Errorf("sandbox: SetNamedSecurityInfo: %v", errNo)
	}
	return nil
}

func localFree(p uintptr) { procLocalFree.Call(p) }

// makePipe returns an inheritable write end and a parent read end.
func makePipe() (read, write syscall.Handle, err error) {
	sa := syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), InheritHandle: 1}
	if err := syscall.CreatePipe(&read, &write, &sa, 0); err != nil {
		return 0, 0, err
	}
	if err := syscall.SetHandleInformation(read, handleFlagInherit, 0); err != nil {
		syscall.CloseHandle(read)
		syscall.CloseHandle(write)
		return 0, 0, err
	}
	return read, write, nil
}

func (a *appcontainer) Run(ctx context.Context, userKey string, argv []string) (Result, error) {
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("sandbox: empty command")
	}
	dir, err := userDir(a.root, userKey)
	if err != nil {
		return Result{}, err
	}

	sid, profileName, err := a.createProfile(userKey)
	if err != nil {
		return Result{}, err
	}
	defer procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(profileName)))
	defer localFree(sid)

	if err := grantFolder(dir, sid); err != nil {
		return Result{}, err
	}

	job, err := createJob(a.maxRAM)
	if err != nil {
		return Result{}, err
	}
	defer syscall.CloseHandle(job)

	stdoutR, stdoutW, err := makePipe()
	if err != nil {
		return Result{}, err
	}
	defer syscall.CloseHandle(stdoutR)
	stderrR, stderrW, err := makePipe()
	if err != nil {
		syscall.CloseHandle(stdoutW)
		return Result{}, err
	}
	defer syscall.CloseHandle(stderrR)

	// Attribute list: run as AppContainer + inherit only the two pipe
	// write handles. Buffer is []uint64 to keep it 8-byte aligned.
	var attrSize uintptr
	procInitializeProcThreadAttr.Call(0, 2, 0, uintptr(unsafe.Pointer(&attrSize)))
	attrBuf := make([]uint64, (attrSize+7)/8)
	attrList := uintptr(unsafe.Pointer(&attrBuf[0]))
	r, _, errNo := procInitializeProcThreadAttr.Call(attrList, 2, 0, uintptr(unsafe.Pointer(&attrSize)))
	if r == 0 {
		syscall.CloseHandle(stdoutW)
		syscall.CloseHandle(stderrW)
		return Result{}, fmt.Errorf("sandbox: InitializeProcThreadAttributeList: %v", errNo)
	}
	defer procDeleteProcThreadAttr.Call(attrList)

	sc := securityCapabilities{appContainerSid: sid}
	r, _, errNo = procUpdateProcThreadAttr.Call(
		attrList, 0, procThreadAttributeSecurityCapabilities,
		uintptr(unsafe.Pointer(&sc)), unsafe.Sizeof(sc), 0, 0)
	if r == 0 {
		syscall.CloseHandle(stdoutW)
		syscall.CloseHandle(stderrW)
		return Result{}, fmt.Errorf("sandbox: UpdateProcThreadAttribute(security): %v", errNo)
	}
	handles := []syscall.Handle{stdoutW, stderrW}
	r, _, errNo = procUpdateProcThreadAttr.Call(
		attrList, 0, procThreadAttributeHandleList,
		uintptr(unsafe.Pointer(&handles[0])), uintptr(len(handles))*unsafe.Sizeof(handles[0]), 0, 0)
	if r == 0 {
		syscall.CloseHandle(stdoutW)
		syscall.CloseHandle(stderrW)
		return Result{}, fmt.Errorf("sandbox: UpdateProcThreadAttribute(handles): %v", errNo)
	}

	var si startupInfoExW
	si.startupInfo.Cb = uint32(unsafe.Sizeof(si))
	si.startupInfo.Flags = startfUseStdHandles
	si.startupInfo.StdOutput = stdoutW
	si.startupInfo.StdErr = stderrW
	si.lpAttributeList = attrList
	var pi syscall.ProcessInformation

	cmdline, err := syscall.UTF16PtrFromString(joinCmdline(argv))
	if err != nil {
		syscall.CloseHandle(stdoutW)
		syscall.CloseHandle(stderrW)
		return Result{}, err
	}
	dirPtr, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		syscall.CloseHandle(stdoutW)
		syscall.CloseHandle(stderrW)
		return Result{}, err
	}
	r, _, errNo = procCreateProcessW.Call(
		0, uintptr(unsafe.Pointer(cmdline)), 0, 0, 1,
		createSuspended|extendedStartupinfoPresent|createNoWindow,
		0, uintptr(unsafe.Pointer(dirPtr)),
		uintptr(unsafe.Pointer(&si)), uintptr(unsafe.Pointer(&pi)))
	if r == 0 {
		syscall.CloseHandle(stdoutW)
		syscall.CloseHandle(stderrW)
		return Result{}, fmt.Errorf("sandbox: CreateProcess: %v", errNo)
	}
	defer syscall.CloseHandle(pi.Process)
	defer syscall.CloseHandle(pi.Thread)

	if r, _, errNo := procAssignProcessToJobObj.Call(uintptr(job), uintptr(pi.Process)); r == 0 {
		syscall.CloseHandle(stdoutW)
		syscall.CloseHandle(stderrW)
		return Result{}, fmt.Errorf("sandbox: AssignProcessToJobObject: %v", errNo)
	}
	if r, _, errNo := procResumeThread.Call(uintptr(pi.Thread)); int32(r) == -1 {
		syscall.CloseHandle(stdoutW)
		syscall.CloseHandle(stderrW)
		return Result{}, fmt.Errorf("sandbox: ResumeThread: %v", errNo)
	}

	// Parent ends of the child's pipes must close so reads see EOF.
	syscall.CloseHandle(stdoutW)
	syscall.CloseHandle(stderrW)

	outCh := make(chan []byte, 2)
	go func() { b, _ := readAll(stdoutR); outCh <- b }()
	go func() { b, _ := readAll(stderrR); outCh <- b }()

	timeout := a.timeout
	if d, ok := ctx.Deadline(); ok {
		if rem := time.Until(d); rem < timeout {
			timeout = rem
		}
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	ms := uint32(timeout / time.Millisecond)
	wr, _, _ := procWaitForSingleObject.Call(uintptr(pi.Process), uintptr(ms))
	timedOut := uint32(wr) == waitTimeout
	if timedOut {
		procTerminateJobObject.Call(uintptr(job), 1)
		procWaitForSingleObject.Call(uintptr(pi.Process), 5000)
	}

	stdout := <-outCh
	stderr := <-outCh

	exitCode := -1
	var code uint32
	if r, _, _ := procGetExitCodeProcess.Call(uintptr(pi.Process), uintptr(unsafe.Pointer(&code))); r != 0 {
		exitCode = int(code)
	}
	res := Result{Stdout: clip(string(stdout)), Stderr: clip(string(stderr)), ExitCode: exitCode, TimedOut: timedOut}
	return res, nil
}

// joinCmdline builds a Windows command line, quoting arguments with spaces.
func joinCmdline(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		if strings.ContainsAny(a, " \t\"") {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		quoted[i] = a
	}
	return strings.Join(quoted, " ")
}

func readAll(h syscall.Handle) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		var n uint32
		err := syscall.ReadFile(h, tmp, &n, nil)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil || n == 0 {
			return buf, nil
		}
	}
}

// clip caps a stream at maxOutput bytes.
func clip(s string) string {
	if len(s) > maxOutput {
		return s[:maxOutput]
	}
	return s
}

// keep exec imported: used by the jobobject fallback in the same package.
var _ = exec.Command
