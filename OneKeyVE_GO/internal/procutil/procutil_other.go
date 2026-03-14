//go:build !windows

package procutil

import (
	"os"
	"os/exec"
)

func HideWindow(cmd *exec.Cmd) {
}

func SuspendProcess(process *os.Process) error {
	return nil
}

func ResumeProcess(process *os.Process) error {
	return nil
}

func BindLifetime(process *os.Process) error {
	return nil
}
