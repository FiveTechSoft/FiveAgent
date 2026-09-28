//go:build windows

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

// jobobject runs commands attached to a Windows Job Object: the whole
// process tree dies with the job, memory is capped, and the working
// directory is the user's folder. NOTE: this backend does not isolate
// the filesystem or the network yet (AppContainer is the next step),
// so on Windows prefer it only for trusted, owner-driven commands.
var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW        = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObj   = kernel32.NewProc("AssignProcessToJobObject")
)

// processAllAccess is PROCESS_ALL_ACCESS (0x1F0FFF); the stdlib
// syscall package does not export the constant.
const processAllAccess = 0x1F0FFF

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	jobObjectLimitProcessMemory       = 0x00000100
)

type jobobjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobobjectExtendedLimitInformation struct {
	BasicLimitInformation jobobjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type jobobject struct {
	root    string
	timeout time.Duration
	maxRAM  int
}

func newJobObject(root string, timeout time.Duration, maxRAM int) (Sandbox, error) {
	return &jobobject{root: root, timeout: timeout, maxRAM: maxRAM}, nil
}

func (j *jobobject) Name() string { return "jobobject" }

func (j *jobobject) Run(ctx context.Context, userKey string, argv []string) (Result, error) {
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("sandbox: empty command")
	}
	dir, err := userDir(j.root, userKey)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, j.timeout)
	defer cancel()

	job, err := createJob(j.maxRAM)
	if err != nil {
		return Result{}, err
	}
	defer syscall.CloseHandle(job)

	var out, errb limitedWriter
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("sandbox: start: %w", err)
	}
	// There is a short window between Start and assignment; closing the
	// job on any later error still reaps the process.
	ph, err := syscall.OpenProcess(processAllAccess, false, uint32(cmd.Process.Pid))
	if err != nil {
		cmd.Process.Kill()
		return Result{}, fmt.Errorf("sandbox: OpenProcess: %w", err)
	}
	defer syscall.CloseHandle(ph)
	if r, _, errNo := procAssignProcessToJobObj.Call(uintptr(job), uintptr(ph)); r == 0 {
		cmd.Process.Kill()
		return Result{}, fmt.Errorf("sandbox: AssignProcessToJobObject: %v", errNo)
	}

	waitErr := cmd.Wait()
	res := Result{Stdout: string(out.buf), Stderr: string(errb.buf)}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		res.ExitCode = -1
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		return res, nil // a timeout is an outcome, not a sandbox failure
	}
	// A non-zero exit code is a normal outcome (Result.ExitCode), not a
	// sandbox failure: the caller needs the captured stdout/stderr
	// (returning the ExitError here silently dropped them - first
	// live-test finding).
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return res, nil
	}
	return res, waitErr
}

// createJob makes a Job Object that kills the whole process tree when
// its handle closes and caps per-process memory at maxRAM MiB.
func createJob(maxRAM int) (syscall.Handle, error) {
	h, _, errNo := procCreateJobObjectW.Call(0, 0)
	if h == 0 {
		return 0, fmt.Errorf("sandbox: CreateJobObject: %v", errNo)
	}
	job := syscall.Handle(h)
	lim := jobobjectExtendedLimitInformation{}
	lim.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose | jobObjectLimitProcessMemory
	lim.ProcessMemoryLimit = uintptr(maxRAM) << 20
	r, _, errNo := procSetInformationJobObject.Call(
		uintptr(job), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&lim)), unsafe.Sizeof(lim))
	if r == 0 {
		syscall.CloseHandle(job)
		return 0, fmt.Errorf("sandbox: SetInformationJobObject: %v", errNo)
	}
	return job, nil
}
