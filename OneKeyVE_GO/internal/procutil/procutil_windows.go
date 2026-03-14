//go:build windows

package procutil

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

const createNoWindow = 0x08000000

const (
	th32csSnapThread             = 0x00000004
	threadSuspendResume          = 0x0002
	invalidHandleValue           = ^uintptr(0)
	jobInfoClassExtendedLimit    = 9
	jobObjectLimitKillOnJobClose = 0x00002000
	processTerminate             = 0x0001
	processSetQuota              = 0x0100
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procCreateToolhelp32Snap = kernel32.NewProc("CreateToolhelp32Snapshot")
	procThread32First        = kernel32.NewProc("Thread32First")
	procThread32Next         = kernel32.NewProc("Thread32Next")
	procOpenThread           = kernel32.NewProc("OpenThread")
	procOpenProcess          = kernel32.NewProc("OpenProcess")
	procSuspendThread        = kernel32.NewProc("SuspendThread")
	procResumeThread         = kernel32.NewProc("ResumeThread")
	procCloseHandle          = kernel32.NewProc("CloseHandle")
	procCreateJobObject      = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJob    = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob   = kernel32.NewProc("AssignProcessToJobObject")

	lifetimeJob struct {
		once   sync.Once
		handle syscall.Handle
		err    error
	}
)

type threadEntry32 struct {
	Size           uint32
	Usage          uint32
	ThreadID       uint32
	OwnerProcessID uint32
	BasePri        int32
	DeltaPri       int32
	Flags          uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectBasicLimitInformation struct {
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

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func HideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

func SuspendProcess(process *os.Process) error {
	return forEachThread(process, func(thread syscall.Handle) error {
		r1, _, err := procSuspendThread.Call(uintptr(thread))
		if r1 == invalidHandleValue {
			return err
		}
		return nil
	})
}

func ResumeProcess(process *os.Process) error {
	return forEachThread(process, func(thread syscall.Handle) error {
		for {
			r1, _, err := procResumeThread.Call(uintptr(thread))
			if r1 == invalidHandleValue {
				return err
			}
			if r1 <= 1 {
				return nil
			}
		}
	})
}

func BindLifetime(process *os.Process) error {
	if process == nil {
		return nil
	}

	handle, err := lifetimeJobHandle()
	if err != nil {
		return err
	}

	processHandle, _, openErr := procOpenProcess.Call(processTerminate|processSetQuota, 0, uintptr(process.Pid))
	if processHandle == 0 {
		if openErr != syscall.Errno(0) {
			return openErr
		}
		return syscall.EINVAL
	}
	defer procCloseHandle.Call(processHandle)

	r1, _, callErr := procAssignProcessToJob.Call(uintptr(handle), processHandle)
	if r1 != 0 {
		return nil
	}

	// Some launch environments already place the process into a job object that
	// disallows reassignment. Keep rendering available even if the safety bind
	// cannot be applied there.
	if callErr == syscall.ERROR_ACCESS_DENIED {
		return nil
	}
	if callErr != syscall.Errno(0) {
		return callErr
	}
	return syscall.EINVAL
}

func lifetimeJobHandle() (syscall.Handle, error) {
	lifetimeJob.once.Do(func() {
		handle, _, err := procCreateJobObject.Call(0, 0)
		if handle == 0 {
			if err != syscall.Errno(0) {
				lifetimeJob.err = err
			} else {
				lifetimeJob.err = syscall.EINVAL
			}
			return
		}

		info := jobObjectExtendedLimitInformation{}
		info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
		r1, _, callErr := procSetInformationJob.Call(
			handle,
			jobInfoClassExtendedLimit,
			uintptr(unsafe.Pointer(&info)),
			unsafe.Sizeof(info),
		)
		if r1 == 0 {
			procCloseHandle.Call(handle)
			if callErr != syscall.Errno(0) {
				lifetimeJob.err = callErr
			} else {
				lifetimeJob.err = syscall.EINVAL
			}
			return
		}

		lifetimeJob.handle = syscall.Handle(handle)
	})

	return lifetimeJob.handle, lifetimeJob.err
}

func forEachThread(process *os.Process, fn func(syscall.Handle) error) error {
	if process == nil {
		return nil
	}

	snapshot, _, err := procCreateToolhelp32Snap.Call(th32csSnapThread, 0)
	if snapshot == invalidHandleValue {
		return err
	}
	defer procCloseHandle.Call(snapshot)

	entry := threadEntry32{Size: uint32(unsafe.Sizeof(threadEntry32{}))}
	ret, _, firstErr := procThread32First.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	if ret == 0 {
		if firstErr != syscall.Errno(0) {
			return firstErr
		}
		return nil
	}

	for {
		if entry.OwnerProcessID == uint32(process.Pid) {
			thread, _, openErr := procOpenThread.Call(threadSuspendResume, 0, uintptr(entry.ThreadID))
			if thread != 0 {
				handle := syscall.Handle(thread)
				callErr := fn(handle)
				procCloseHandle.Call(thread)
				if callErr != nil {
					return callErr
				}
			} else if openErr != syscall.Errno(0) {
				return openErr
			}
		}

		ret, _, nextErr := procThread32Next.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
		if ret == 0 {
			if nextErr != syscall.Errno(0) && nextErr != syscall.ERROR_NO_MORE_FILES {
				return nextErr
			}
			return nil
		}
	}
}
