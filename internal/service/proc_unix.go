//go:build unix

package service

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// killGroupOnCancel starts cmd in its own process group and, when its context
// ends, sends SIGTERM to the process group so git and other tools can clean up
// lockfiles and temp files before exit, escalating to SIGKILL after a grace
// period if descendants remain.
func killGroupOnCancel(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		go func(pgid int) {
			time.Sleep(500 * time.Millisecond)
			if err := syscall.Kill(-pgid, 0); err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
		}(cmd.Process.Pid)
		return err
	}
}
