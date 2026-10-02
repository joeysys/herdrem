//go:build unix

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A timed-out commit command must not leave its descendants running: they
// hold the output pipe, which kept cmd.Run (and the repo's commit guard)
// blocked until they exited on their own.
func TestRunCapturedKillsTheWholeGroupOnTimeout(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cmd := remoteShellCommand(ctx, "local", dir, "sleep 30 & echo $! > bg.pid; sleep 30")

	start := time.Now()
	if _, err := runCaptured(cmd); err == nil {
		t.Fatal("expected the timed-out command to fail")
	}
	// WaitDelay alone would return after ~5s; the group kill returns at once.
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("runCaptured returned after %s; descendants kept it blocked", d)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "bg.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("background process %d survived the timeout", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
