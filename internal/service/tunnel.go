package service

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"herdrem/internal/protocol"
)

var tryCFRegex = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

// TunnelItem represents one managed cloudflared tunnel process.
type TunnelItem struct {
	Info   protocol.TunnelInfo
	cmd    *exec.Cmd
	cancel func()
	// waited is closed once the monitor goroutine has reaped cmd. Exactly one
	// goroutine ever calls cmd.Wait; Close/Shutdown wait on this channel
	// instead of calling Wait themselves, which would race.
	waited chan struct{}
}

// TunnelPolicy is the server-side control plane for CreateTunnel.
// Zero values mean: default port allowlist, direct disabled, max 8.
type TunnelPolicy struct {
	AllowedPorts map[int]struct{}
	AllowDirect  bool
	MaxActive    int
}

const defaultTunnelMax = 8

var defaultTunnelPorts = []int{5173, 3000, 8080, 4173}

func defaultAllowedPorts() map[int]struct{} {
	set := make(map[int]struct{}, len(defaultTunnelPorts))
	for _, p := range defaultTunnelPorts {
		set[p] = struct{}{}
	}
	return set
}

// TunnelService manages cloudflared dynamic port forwarding tunnels.
type TunnelService struct {
	mu        sync.RWMutex
	tunnels   map[string]*TunnelItem
	relayPort int
	policy    TunnelPolicy
	logger    *slog.Logger
	onUpdate  func(info protocol.TunnelInfo)
}

// NewTunnelService creates a TunnelService. relayPort is the local Relay HTTP port.
func NewTunnelService(relayPort int, onUpdate func(info protocol.TunnelInfo), logger *slog.Logger) *TunnelService {
	if logger == nil {
		logger = slog.Default()
	}
	return &TunnelService{
		tunnels:   make(map[string]*TunnelItem),
		relayPort: relayPort,
		policy: TunnelPolicy{
			AllowedPorts: defaultAllowedPorts(),
			MaxActive:    defaultTunnelMax,
		},
		onUpdate: onUpdate,
		logger:   logger,
	}
}

// SetPolicy replaces the tunnel control-plane policy.
func (ts *TunnelService) SetPolicy(p TunnelPolicy) {
	if p.AllowedPorts == nil {
		p.AllowedPorts = defaultAllowedPorts()
	}
	if p.MaxActive < 1 {
		p.MaxActive = defaultTunnelMax
	}
	ts.mu.Lock()
	ts.policy = p
	ts.mu.Unlock()
}

// DirectAllowed reports whether ungated (direct) tunnels may be created.
func (ts *TunnelService) DirectAllowed() bool {
	if ts == nil {
		return false
	}
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.policy.AllowDirect
}

// CreateTunnel starts a cloudflared tunnel for targetPort.
func (ts *TunnelService) CreateTunnel(targetPort int, authMode string, pin string) (*protocol.TunnelInfo, error) {
	if targetPort <= 0 || targetPort > 65535 {
		return nil, fmt.Errorf("invalid port number: %d", targetPort)
	}

	if authMode == "" {
		authMode = "token_gate"
	}
	if authMode != "token_gate" && authMode != "basic_auth" && authMode != "direct" {
		return nil, fmt.Errorf("invalid auth_mode %q", authMode)
	}
	if authMode == "direct" && !ts.DirectAllowed() {
		return nil, fmt.Errorf("direct tunnels are disabled; set HERDREM_ALLOW_DIRECT_TUNNEL=1 to enable")
	}
	if targetPort < 1024 {
		return nil, fmt.Errorf("port %d is a privileged system port (<1024) and cannot be forwarded", targetPort)
	}
	if !ts.portAllowed(targetPort) {
		return nil, fmt.Errorf("port %d is not in the tunnel allowlist (default 5173,3000,8080,4173; add extras with HERDREM_TUNNEL_PORTS, ranges like 8000-8100 ok)", targetPort)
	}
	// Enforce minimum PIN length for user-provided credentials.
	// The default (empty pin) generates a 128-bit hex token; custom PINs
	// must be at least 8 characters to resist brute force.
	if pin != "" && len(pin) < 8 {
		return nil, fmt.Errorf("custom pin must be at least 8 characters for adequate entropy")
	}

	// Credential generation must fail closed. A silent entropy failure
	// would publish a predictable token on a public gate, so propagate the
	// error and do not start cloudflared.
	idSuffix, err := randomHex(4)
	if err != nil {
		return nil, fmt.Errorf("generate tunnel id: %w", err)
	}
	id := fmt.Sprintf("tun-%d-%s", targetPort, idSuffix)
	token := pin
	if token == "" {
		// Use high-entropy default token instead of 6-digit PIN.
		// A 16-byte hex token (128 bits) makes brute-force infeasible.
		token, err = randomHex(16)
		if err != nil {
			return nil, fmt.Errorf("generate tunnel token: %w", err)
		}
	}

	info := protocol.TunnelInfo{
		ID:         id,
		Port:       targetPort,
		TargetHost: "localhost",
		Status:     "starting",
		AuthMode:   authMode,
		Token:      token,
		CreatedAt:  time.Now().Unix(),
	}

	findExisting := func(items map[string]*TunnelItem) (*protocol.TunnelInfo, error) {
		for _, item := range items {
			if item.Info.Port == targetPort && (item.Info.Status == "active" || item.Info.Status == "starting") {
				// Reject reuse if auth_mode or pin doesn't match.
				if item.Info.AuthMode != authMode || (pin != "" && item.Info.Token != pin) {
					return nil, fmt.Errorf("port %d already has an active tunnel with different auth_mode or credentials", targetPort)
				}
				infoCopy := item.Info
				return &infoCopy, nil
			}
		}
		return nil, nil
	}

	return ts.startTunnel(info, findExisting, authMode == "direct")
}

// CreateFileTunnel starts a cloudflared tunnel for an HTML or static file preview.
func (ts *TunnelService) CreateFileTunnel(host, filePath, authMode, pin string) (*protocol.TunnelInfo, error) {
	filePath = filepath.Clean(filePath)
	if filePath == "" || filePath == "." {
		return nil, fmt.Errorf("file path cannot be empty")
	}
	if host == "" {
		host = "local"
	}
	if host == "local" {
		if fi, err := os.Stat(filePath); err == nil && fi.IsDir() {
			return nil, fmt.Errorf("cannot create tunnel for directory %q", filePath)
		}
	}

	if authMode == "" {
		authMode = "token_gate"
	}
	if authMode == "direct" {
		return nil, fmt.Errorf("direct auth mode is not allowed for file tunnels; use token_gate or basic_auth")
	}
	if authMode != "token_gate" && authMode != "basic_auth" {
		return nil, fmt.Errorf("invalid auth_mode %q", authMode)
	}
	if pin != "" && len(pin) < 8 {
		return nil, fmt.Errorf("custom pin must be at least 8 characters for adequate entropy")
	}

	idSuffix, err := randomHex(4)
	if err != nil {
		return nil, fmt.Errorf("generate tunnel id: %w", err)
	}
	id := fmt.Sprintf("tun-file-%s", idSuffix)
	token := pin
	if token == "" {
		token, err = randomHex(16)
		if err != nil {
			return nil, fmt.Errorf("generate tunnel token: %w", err)
		}
	}

	info := protocol.TunnelInfo{
		ID:         id,
		FilePath:   filePath,
		Host:       host,
		TargetHost: "localhost",
		Status:     "starting",
		AuthMode:   authMode,
		Token:      token,
		CreatedAt:  time.Now().Unix(),
	}

	findExisting := func(items map[string]*TunnelItem) (*protocol.TunnelInfo, error) {
		for _, item := range items {
			if item.Info.FilePath == filePath && item.Info.Host == host && (item.Info.Status == "active" || item.Info.Status == "starting") {
				if item.Info.AuthMode != authMode || (pin != "" && item.Info.Token != pin) {
					return nil, fmt.Errorf("file %q already has an active tunnel with different auth_mode or credentials", filepath.Base(filePath))
				}
				infoCopy := item.Info
				return &infoCopy, nil
			}
		}
		return nil, nil
	}

	return ts.startTunnel(info, findExisting, false)
}

// startTunnel holds ts.mu continuously from checking existing tunnels to inserting the new item,
// completely eliminating TOCTOU races between concurrent creation requests.
func (ts *TunnelService) startTunnel(
	info protocol.TunnelInfo,
	findExisting func(map[string]*TunnelItem) (*protocol.TunnelInfo, error),
	isDirect bool,
) (*protocol.TunnelInfo, error) {
	ts.mu.Lock()

	if findExisting != nil {
		existing, err := findExisting(ts.tunnels)
		if err != nil {
			ts.mu.Unlock()
			return nil, err
		}
		if existing != nil {
			ts.mu.Unlock()
			return existing, nil
		}
	}

	live := 0
	for _, item := range ts.tunnels {
		if item.Info.Status == "active" || item.Info.Status == "starting" {
			live++
		}
	}
	maxActive := ts.policy.MaxActive
	if maxActive < 1 {
		maxActive = defaultTunnelMax
	}
	if live >= maxActive {
		ts.mu.Unlock()
		return nil, fmt.Errorf("too many tunnels (max %d); close one first or raise HERDREM_TUNNEL_MAX", maxActive)
	}

	// Check cloudflared binary
	binPath, err := exec.LookPath("cloudflared")
	if err != nil {
		info.Status = "error"
		info.Error = "cloudflared binary not found in PATH. Please install cloudflared first."
		ts.logger.Error("cloudflared binary missing", "id", info.ID, "err", err)
		ts.mu.Unlock()
		if ts.onUpdate != nil {
			ts.onUpdate(info)
		}
		return &info, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	var cmd *exec.Cmd
	if isDirect {
		// Direct mode: point cloudflared directly to targetPort
		targetURL := fmt.Sprintf("http://localhost:%d", info.Port)
		cmd = exec.CommandContext(ctx, binPath, "tunnel", "--url", targetURL)
	} else {
		// Gate mode: point cloudflared to Relay port with custom Host header for tunnel ID identification
		relayURL := fmt.Sprintf("http://localhost:%d", ts.relayPort)
		hostHeader := fmt.Sprintf("%s.tunnel.local", info.ID)
		cmd = exec.CommandContext(ctx, binPath, "tunnel", "--url", relayURL, "--http-host-header", hostHeader)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		info.Status = "error"
		info.Error = fmt.Sprintf("failed to attach stderr: %v", err)
		ts.mu.Unlock()
		return &info, nil
	}

	item := &TunnelItem{
		Info:   info,
		cmd:    cmd,
		cancel: cancel,
		waited: make(chan struct{}),
	}
	ts.tunnels[info.ID] = item
	infoCopy := item.Info
	ts.mu.Unlock()

	if ts.onUpdate != nil {
		ts.onUpdate(infoCopy)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		ts.mu.Lock()
		item.Info.Status = "error"
		item.Info.Error = fmt.Sprintf("failed to start cloudflared: %v", err)
		errInfo := item.Info
		ts.mu.Unlock()
		// Never started, so nothing will ever reap it; unblock any waiter.
		close(item.waited)
		if ts.onUpdate != nil {
			ts.onUpdate(errInfo)
		}
		return &errInfo, nil
	}

	// Read stderr in background to parse Public URL. This goroutine also owns
	// the single cmd.Wait() for this process.
	go ts.monitorTunnelStderr(info.ID, item, stderr)

	return &infoCopy, nil
}

func (ts *TunnelService) monitorTunnelStderr(id string, item *TunnelItem, stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		if url := tryCFRegex.FindString(line); url != "" {
			var updateInfo *protocol.TunnelInfo
			ts.mu.Lock()
			item, ok := ts.tunnels[id]
			if ok && item.Info.Status == "starting" {
				item.Info.PublicURL = url
				item.Info.Status = "active"
				ts.logger.Info("cloudflared tunnel established", "id", id, "url", url, "port", item.Info.Port, "file", item.Info.FilePath)
				infoCopy := item.Info
				updateInfo = &infoCopy
			}
			ts.mu.Unlock()

			if updateInfo != nil && ts.onUpdate != nil {
				ts.onUpdate(*updateInfo)
			}
		}
	}

	// stderr EOF means the process is finishing. This goroutine is the sole
	// owner of cmd.Wait() for this process — reaping here covers the common
	// "cloudflared exited on its own" path, not just explicit Close/Shutdown.
	waitErr := item.cmd.Wait()
	close(item.waited)

	var updateInfo *protocol.TunnelInfo
	ts.mu.Lock()
	if cur, ok := ts.tunnels[id]; ok {
		if cur.Info.Status == "starting" || cur.Info.Status == "active" {
			cur.Info.Status = "error"
			if cur.Info.Error == "" {
				if waitErr != nil {
					cur.Info.Error = fmt.Sprintf("cloudflared process exited unexpectedly: %v", waitErr)
				} else {
					cur.Info.Error = "cloudflared process exited unexpectedly"
				}
			}
			infoCopy := cur.Info
			updateInfo = &infoCopy
		}
	}
	ts.mu.Unlock()

	if updateInfo != nil && ts.onUpdate != nil {
		ts.onUpdate(*updateInfo)
	}
}

// CloseTunnel terminates a managed tunnel.
func (ts *TunnelService) CloseTunnel(id string) error {
	ts.mu.Lock()
	item, ok := ts.tunnels[id]
	if !ok {
		ts.mu.Unlock()
		return fmt.Errorf("tunnel %q not found", id)
	}

	item.Info.Status = "closed"
	infoCopy := item.Info
	if item.cancel != nil {
		item.cancel()
	}
	if item.cmd != nil && item.cmd.Process != nil {
		_ = item.cmd.Process.Kill()
	}

	delete(ts.tunnels, id)
	ts.mu.Unlock()

	// Do not call Wait here — the monitor goroutine owns it. Just wait for
	// it to finish reaping, bounded so a wedged child cannot block the caller.
	waitReaped(item, reapTimeout)

	ts.logger.Info("cloudflared tunnel closed", "id", id, "port", infoCopy.Port)
	if ts.onUpdate != nil {
		ts.onUpdate(infoCopy)
	}

	return nil
}

// Shutdown closes all active tunnels and reaps child processes.
// Called when the Relay server is shutting down.
func (ts *TunnelService) Shutdown() {
	ts.mu.Lock()
	ids := make([]string, 0, len(ts.tunnels))
	items := make([]*TunnelItem, 0, len(ts.tunnels))
	for id, item := range ts.tunnels {
		ids = append(ids, id)
		items = append(items, item)
		item.Info.Status = "closed"
		if item.cancel != nil {
			item.cancel()
		}
		if item.cmd != nil && item.cmd.Process != nil {
			_ = item.cmd.Process.Kill()
		}
	}
	ts.tunnels = make(map[string]*TunnelItem)
	ts.mu.Unlock()

	// The monitor goroutine reaps; wait for it rather than calling Wait
	// concurrently from here.
	for _, item := range items {
		waitReaped(item, reapTimeout)
	}

	for _, id := range ids {
		ts.logger.Info("cloudflared tunnel closed on shutdown", "id", id)
	}
}

// reapTimeout bounds how long Close/Shutdown will wait for the monitor
// goroutine to finish reaping a killed child.
const reapTimeout = 3 * time.Second

// waitReaped blocks until the item's monitor goroutine has called cmd.Wait,
// or until timeout. A nil channel (item never started) returns immediately.
func waitReaped(item *TunnelItem, timeout time.Duration) {
	if item == nil || item.waited == nil {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-item.waited:
	case <-timer.C:
	}
}

// GetTunnel returns info for a tunnel by ID.
func (ts *TunnelService) GetTunnel(id string) (*protocol.TunnelInfo, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	item, ok := ts.tunnels[id]
	if !ok {
		return nil, false
	}
	// Return a copy to prevent callers from racing with concurrent map mutations.
	infoCopy := item.Info
	return &infoCopy, true
}

func (ts *TunnelService) portAllowed(port int) bool {
	if port < 1024 || port > 65535 {
		return false
	}
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	if len(ts.policy.AllowedPorts) == 0 {
		_, ok := defaultAllowedPorts()[port]
		return ok
	}
	_, ok := ts.policy.AllowedPorts[port]
	return ok
}

// ListTunnels returns all current tunnel states.
func (ts *TunnelService) ListTunnels() []protocol.TunnelInfo {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	var list []protocol.TunnelInfo
	for _, item := range ts.tunnels {
		list = append(list, item.Info)
	}
	return list
}

// randomHex returns bytesLen cryptographically random bytes as hex.
// The error is returned rather than swallowed — these values are used as
// public-gate credentials, so an entropy failure must fail closed.
func randomHex(bytesLen int) (string, error) {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
