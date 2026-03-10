//go:build !windows

package procutil

import "os/exec"

func HideWindow(cmd *exec.Cmd) {
}
