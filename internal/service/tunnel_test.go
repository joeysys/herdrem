package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"herdrem/internal/protocol"
)

// writeFakeCloudflared creates a fake cloudflared binary that prints a tunnel URL
// and stays alive until killed. This avoids real public tunnel creation.
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
	// Use a fake cloudflared binary to avoid real public tunnel creation.
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

	// Use t.Cleanup for guaranteed cleanup even on t.Fatal
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

func TestCreateTunnelRejectsPortOutsideAllowlist(t *testing.T) {
	svc := NewTunnelService(8080, nil, nil)
	if _, err := svc.CreateTunnel(22, "token_gate", "12345678"); err == nil {
		t.Fatal("expected error for port 22")
	} else if !strings.Contains(err.Error(), "privileged") {
		t.Errorf("error = %v, want privileged-port mention", err)
	}
	if _, err := svc.CreateTunnel(9999, "token_gate", "12345678"); err == nil {
		t.Fatal("expected error for 9999")
	} else if !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("error = %v, want allowlist mention", err)
	}
}

func TestCreateTunnelRejectsPrivilegedEvenIfAllowlisted(t *testing.T) {
	svc := NewTunnelService(8080, nil, nil)
	svc.SetPolicy(TunnelPolicy{AllowedPorts: map[int]struct{}{80: {}, 443: {}}, MaxActive: 8})
	if _, err := svc.CreateTunnel(80, "token_gate", "12345678"); err == nil {
		t.Fatal("expected privileged-port rejection")
	}
}

func TestCreateTunnelRejectsDirectByDefault(t *testing.T) {
	svc := NewTunnelService(8080, nil, nil)
	if _, err := svc.CreateTunnel(5173, "direct", ""); err == nil {
		t.Fatal("expected error for direct mode")
	} else if !strings.Contains(err.Error(), "HERDREM_ALLOW_DIRECT_TUNNEL") {
		t.Errorf("error = %v, want env-flag mention", err)
	}
}

func TestCreateTunnelAllowsDirectWhenEnabled(t *testing.T) {
	fakeBin := writeFakeCloudflared(t)
	originalPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", originalPath) })
	os.Setenv("PATH", filepath.Dir(fakeBin)+":"+originalPath)

	svc := NewTunnelService(8080, nil, nil)
	svc.SetPolicy(TunnelPolicy{
		AllowedPorts: map[int]struct{}{5173: {}},
		AllowDirect:  true,
		MaxActive:    8,
	})
	info, err := svc.CreateTunnel(5173, "direct", "")
	if err != nil {
		t.Fatalf("direct with policy: %v", err)
	}
	t.Cleanup(func() { _ = svc.CloseTunnel(info.ID) })
	if info.AuthMode != "direct" {
		t.Errorf("auth_mode = %q, want direct", info.AuthMode)
	}
}

func TestCreateTunnelCapsConcurrent(t *testing.T) {
	fakeBin := writeFakeCloudflared(t)
	originalPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", originalPath) })
	os.Setenv("PATH", filepath.Dir(fakeBin)+":"+originalPath)

	svc := NewTunnelService(8080, nil, nil)
	svc.SetPolicy(TunnelPolicy{
		AllowedPorts: map[int]struct{}{5173: {}, 3000: {}, 8080: {}},
		MaxActive:    2,
	})
	a, err := svc.CreateTunnel(5173, "token_gate", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.CloseTunnel(a.ID) })
	b, err := svc.CreateTunnel(3000, "token_gate", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.CloseTunnel(b.ID) })
	if _, err := svc.CreateTunnel(8080, "token_gate", "12345678"); err == nil {
		t.Fatal("expected cap error")
	} else if !strings.Contains(err.Error(), "too many tunnels") {
		t.Errorf("error = %v, want cap mention", err)
	}
	// Reusing an existing port must not count as a new slot.
	again, err := svc.CreateTunnel(5173, "token_gate", "12345678")
	if err != nil {
		t.Fatalf("reuse existing: %v", err)
	}
	if again.ID != a.ID {
		t.Errorf("reuse id = %q, want %q", again.ID, a.ID)
	}
}
