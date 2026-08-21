package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"herdrem/internal/protocol"
)

// writeFakeCloudflared creates a fake cloudflared binary that prints a tunnel URL
// and stays alive until killed. This avoids real public tunnel creation (F5).
func writeFakeCloudflared(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "cloudflared")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	// Write a shell script that acts like cloudflared: prints a URL to stderr
	// and then blocks until killed.
	var script string
	if runtime.GOOS == "windows" {
		// Windows: use a batch file
		script = "@echo off\necho https://fake-tunnel.trycloudflare.com 1>&2\n:loop\ngoto loop\n"
	} else {
		script = "#!/bin/sh\necho 'https://fake-tunnel.trycloudflare.com' >&2\nwhile true; do sleep 1; done\n"
	}

	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake cloudflared: %v", err)
	}

	// On non-Windows, the script is directly executable.
	// On Windows, we need to handle .bat execution differently — skip on Windows.
	if runtime.GOOS == "windows" {
		t.Skip("fake cloudflared not supported on Windows in this test")
	}

	return binPath
}

func TestTunnelServiceCreateAndList(t *testing.T) {
	// F5: Use a fake cloudflared binary to avoid real public tunnel creation.
	fakeBin := writeFakeCloudflared(t)

	// Override the cloudflared binary path by setting PATH to include our fake
	originalPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", originalPath) })
	os.Setenv("PATH", filepath.Dir(fakeBin)+":"+originalPath)

	updatedCh := make(chan protocol.TunnelInfo, 5)
	svc := NewTunnelService(8080, func(info protocol.TunnelInfo) {
		updatedCh <- info
	}, nil)

	info, err := svc.CreateTunnel(5173, "token_gate", "12345678")
	if err != nil {
		t.Fatalf("unexpected error creating tunnel: %v", err)
	}

	// F5: Use t.Cleanup for guaranteed cleanup even on t.Fatal
	t.Cleanup(func() {
		_ = svc.CloseTunnel(info.ID)
	})

	if info.Port != 5173 {
		t.Errorf("expected port 5173, got %d", info.Port)
	}
	if info.AuthMode != "token_gate" {
		t.Errorf("expected auth_mode token_gate, got %s", info.AuthMode)
	}

	select {
	case update := <-updatedCh:
		if update.ID != info.ID {
			t.Errorf("expected update for ID %s, got %s", info.ID, update.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tunnel update broadcast")
	}

	tunnels := svc.ListTunnels()
	if len(tunnels) != 1 {
		t.Errorf("expected 1 tunnel, got %d", len(tunnels))
	}

	_ = svc.CloseTunnel(info.ID)
	tunnelsAfter := svc.ListTunnels()
	if len(tunnelsAfter) != 0 {
		t.Errorf("expected 0 tunnels after close, got %d", len(tunnelsAfter))
	}
}
