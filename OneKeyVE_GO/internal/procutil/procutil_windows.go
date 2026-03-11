//go:build windows

package procutil

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

const createNoWindow = 0x08000000

const (
	th32csSnapThread    = 0x00000004
	threadSuspendResume = 0x0002
	invalidHandleValue  = ^uintptr(0)
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procCreateToolhelp32Snap = kernel32.NewProc("CreateToolhelp32Snapshot")
	procThread32First        = kernel32.NewProc("Thread32First")
	procThread32Next         = kernel32.NewProc("Thread32Next")
	procOpenThread           = kernel32.NewProc("OpenThread")
	procSuspendThread        = kernel32.NewProc("SuspendThread")
	procResumeThread         = kernel32.NewProc("ResumeThread")
	procCloseHandle          = kernel32.NewProc("CloseHandle")
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
