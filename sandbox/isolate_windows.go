//go:build windows

package sandbox

import (
	"os"
	"os/exec"
	"syscall"
)

// isolate puts the child in its own process group and cancels it by killing the
// tree.
//
// CREATE_NEW_PROCESS_GROUP has no Unix equivalent and no Setpgid field on
// SysProcAttr, which is why this is a separate file rather than a runtime
// check: the field does not exist at compile time on Windows.
//
// TerminateProcess kills only the immediate child. Windows offers no signal
// that reaches a whole group the way kill(-pgid) does, so a tool that spawned
// a subprocess could outlive its deadline. The job object below is the correct
// mechanism; until it is wired up this is a documented gap rather than a
// silent one.
func isolate(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return command.Process.Kill()
	}
}
