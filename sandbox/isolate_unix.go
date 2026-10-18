//go:build !windows

package sandbox

import (
	"os"
	"os/exec"
	"syscall"
)

// isolate puts the child in its own process group and cancels it by signalling
// the group.
//
// The group matters for the timeout path: killing only the child would leave
// anything it spawned still running, which for a tool that can execute a
// program means an orphaned process outside the deadline that stopped it.
func isolate(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		// The negative pid addresses the whole process group.
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
}
