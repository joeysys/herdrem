package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

func TestCreateFileTunnel(t *testing.T) {
	fakeBin := writeFakeCloudflared(t)
	originalPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", originalPath) })
	os.Setenv("PATH", filepath.Dir(fakeBin)+":"+originalPath)

	svc := NewTunnelService(8080, nil, nil)
	filePath := "/tmp/test.html"
	info, err := svc.CreateFileTunnel("local", filePath, "token_gate", "12345678")
	if err != nil {
		t.Fatalf("unexpected error creating file tunnel: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.CloseTunnel(info.ID)
	})

	if info.FilePath != filePath {
		t.Errorf("expected filePath %s, got %s", filePath, info.FilePath)
	}
	if !strings.HasPrefix(info.ID, "tun-file-") {
		t.Errorf("expected ID prefix tun-file-, got %s", info.ID)
	}
	if info.AuthMode != "token_gate" {
		t.Errorf("expected auth_mode token_gate, got %s", info.AuthMode)
	}

	// Reusing same file returns existing tunnel
	again, err := svc.CreateFileTunnel("local", filePath, "token_gate", "12345678")
	if err != nil {
		t.Fatalf("reuse file tunnel: %v", err)
	}
	if again.ID != info.ID {
		t.Errorf("reuse id = %q, want %q", again.ID, info.ID)
	}
}

func TestCreateFileTunnelRejectsDirectory(t *testing.T) {
	svc := NewTunnelService(8080, nil, nil)
	dir := t.TempDir()
	if _, err := svc.CreateFileTunnel("local", dir, "token_gate", "12345678"); err == nil {
		t.Fatal("expected error creating tunnel for directory, got nil")
	} else if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error = %v, want mention of directory", err)
	}
}

func TestCreateFileTunnelRejectsDirectAuth(t *testing.T) {
	svc := NewTunnelService(8080, nil, nil)
	if _, err := svc.CreateFileTunnel("local", "/tmp/a.html", "direct", "12345678"); err == nil {
		t.Fatal("expected error for direct auth mode on file tunnel, got nil")
	} else if !strings.Contains(err.Error(), "direct auth mode is not allowed") {
		t.Errorf("error = %v, want direct auth rejection", err)
	}
}

func TestConcurrentCreateTunnelDeduplication(t *testing.T) {
	fakeBin := writeFakeCloudflared(t)
	originalPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", originalPath) })
	os.Setenv("PATH", filepath.Dir(fakeBin)+":"+originalPath)

	const concurrency = 16
	for round := 0; round < 5; round++ {
		svc := NewTunnelService(8080, nil, nil)
		results := make([]*protocol.TunnelInfo, concurrency)
		errs := make([]error, concurrency)

		var wg sync.WaitGroup
		wg.Add(concurrency)
		startGate := make(chan struct{})

		for i := 0; i < concurrency; i++ {
			idx := i
			go func() {
				defer wg.Done()
				<-startGate
				info, err := svc.CreateTunnel(5173, "token_gate", "")
				results[idx] = info
				errs[idx] = err
			}()
		}

		close(startGate)
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d goroutine %d failed: %v", round, i, err)
			}
		}

		firstID := results[0].ID
		for i := 1; i < concurrency; i++ {
			if results[i].ID != firstID {
				t.Fatalf("round %d goroutine %d returned ID %s, expected %s", round, i, results[i].ID, firstID)
			}
		}

		tunnels := svc.ListTunnels()
		if len(tunnels) != 1 {
			t.Fatalf("round %d expected exactly 1 active/starting tunnel, got %d", round, len(tunnels))
		}
		_ = svc.CloseTunnel(firstID)
	}
}

func TestConcurrentCreateFileTunnelDeduplication(t *testing.T) {
	fakeBin := writeFakeCloudflared(t)
	originalPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", originalPath) })
	os.Setenv("PATH", filepath.Dir(fakeBin)+":"+originalPath)

	dir := t.TempDir()
	filePath := filepath.Join(dir, "preview.html")
	if err := os.WriteFile(filePath, []byte("<h1>test</h1>"), 0644); err != nil {
		t.Fatal(err)
	}

	const concurrency = 16
	for round := 0; round < 5; round++ {
		svc := NewTunnelService(8080, nil, nil)
		results := make([]*protocol.TunnelInfo, concurrency)
		errs := make([]error, concurrency)

		var wg sync.WaitGroup
		wg.Add(concurrency)
		startGate := make(chan struct{})

		for i := 0; i < concurrency; i++ {
			idx := i
			go func() {
				defer wg.Done()
				<-startGate
				info, err := svc.CreateFileTunnel("local", filePath, "token_gate", "")
				results[idx] = info
				errs[idx] = err
			}()
		}

		close(startGate)
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d goroutine %d failed: %v", round, i, err)
			}
		}

		firstID := results[0].ID
		for i := 1; i < concurrency; i++ {
			if results[i].ID != firstID {
				t.Fatalf("round %d goroutine %d returned ID %s, expected %s", round, i, results[i].ID, firstID)
			}
		}

		tunnels := svc.ListTunnels()
		if len(tunnels) != 1 {
			t.Fatalf("round %d expected exactly 1 active/starting tunnel, got %d", round, len(tunnels))
		}
		_ = svc.CloseTunnel(firstID)
	}
}

