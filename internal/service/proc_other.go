//go:build !unix

package service

import "os/exec"

// killGroupOnCancel is a no-op without Unix process groups: the context still
// kills the direct child, and runCaptured's WaitDelay bounds the wait.
func killGroupOnCancel(cmd *exec.Cmd) {}
