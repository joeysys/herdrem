//go:build unix

package service

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// killGroupOnCancel starts cmd in its own process group and, when its context
// ends, kills the whole group instead of only the direct child (exec's
// default), so descendants cannot outlive the timeout.
func killGroupOnCancel(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
