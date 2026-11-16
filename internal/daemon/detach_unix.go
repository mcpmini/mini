//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// A daemon started by a terminal command must outlive a Ctrl-C there, which signals the
// terminal's whole process group, and the terminal closing.
func detachFromTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
