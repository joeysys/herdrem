package httpserver

import (
	"crypto/subtle"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"herdrem/internal/protocol"
)

const tunnelCookieName = "herdrem_tunnel_auth"

// gateRateLimiter tracks failed auth attempts per IP for tunnel gate endpoints.
// After maxFailedAttempts failures within the window, subsequent attempts are
// rejected until the window expires.
type gateRateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*gateAttemptState
}

type gateAttemptState struct {
	failures   int
	lastFail   time.Time
	blockUntil time.Time
}

const (
	gateMaxFailedAttempts = 5
	gateBlockDuration     = 5 * time.Minute
	gateAttemptWindow     = 10 * time.Minute
	// gateMaxTrackedIPs bounds the attempts map so a spoofed-source flood
	// cannot grow it without limit.
	gateMaxTrackedIPs = 8192
)

func newGateRateLimiter() *gateRateLimiter {
	return &gateRateLimiter{attempts: make(map[string]*gateAttemptState)}
}

// allow checks if the IP is allowed to attempt authentication.
func (rl *gateRateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	st, ok := rl.attempts[ip]
	if !ok {
		return true
	}
	if time.Now().Before(st.blockUntil) {
		return false
	}
	// Reset if window expired
	if time.Since(st.lastFail) > gateAttemptWindow {
		delete(rl.attempts, ip)
		return true
	}
	return true
}

// evictStaleLocked drops entries that are neither blocked nor inside the
// attempt window. Callers must hold rl.mu.
func (rl *gateRateLimiter) evictStaleLocked() {
	now := time.Now()
	for ip, st := range rl.attempts {
		if !now.Before(st.blockUntil) && now.Sub(st.lastFail) > gateAttemptWindow {
			delete(rl.attempts, ip)
		}
	}
}

// recordFailure increments the failure count for an IP and blocks if threshold reached.
func (rl *gateRateLimiter) recordFailure(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	st, ok := rl.attempts[ip]
	if !ok {
		if len(rl.attempts) >= gateMaxTrackedIPs {
			rl.evictStaleLocked()
		}
		if len(rl.attempts) >= gateMaxTrackedIPs {
			// Still full of live entries — refuse to grow unboundedly.
			return
		}
		st = &gateAttemptState{}
		rl.attempts[ip] = st
	}
	// A client whose block already expired starts a fresh failure window —
	// otherwise a single failure after each block re-arms it and a retrying
	// client (e.g. the PWA's WS reconnect loop) stays locked out indefinitely
	// even after the user supplies a valid credential.
	if !st.blockUntil.IsZero() && !time.Now().Before(st.blockUntil) {
		st.failures = 0
	}
	st.failures++
	st.lastFail = time.Now()
	if st.failures >= gateMaxFailedAttempts {
		st.blockUntil = time.Now().Add(gateBlockDuration)
	}
}

// recordSuccess clears the failure count for an IP.
func (rl *gateRateLimiter) recordSuccess(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, ip)
}

// handleTunnelGateByHost serves requests routed by Host header (e.g. Host: tun-5173-abcd.tunnel.local).
// These requests come from cloudflared and do NOT carry the Relay token, so Relay-level
// auth is skipped here — the tunnel gate's own access control (token_gate/basic_auth/direct) applies.
// Verify the request originates from loopback (cloudflared runs locally).
func (s *Server) handleTunnelGateByHost(w http.ResponseWriter, r *http.Request) {
	if s.tunnels == nil {
		http.Error(w, "tunnel service not available", http.StatusServiceUnavailable)
		return
	}

	// Only accept host-routed gate requests from loopback addresses.
	// cloudflared connects to localhost; non-loopback sources are attempting
	// to bypass Relay auth via Host header spoofing.
	if !isLoopback(r.RemoteAddr) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	host := r.Host
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}
	id := strings.TrimSuffix(host, ".tunnel.local")
	subPath := r.URL.Path
	if subPath == "" {
		subPath = "/"
	}

	s.dispatchTunnelGate(w, r, id, subPath)
}

// isLoopback checks if the remote address is a loopback (127.0.0.1 or ::1).
func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// handleTunnelGate serves /api/tunnel-gate/{id}/... requests with access control.
// This path-based route is for local direct access and requires Relay-level auth
// to prevent unauthenticated local users from bypassing tunnel access control.
func (s *Server) handleTunnelGate(w http.ResponseWriter, r *http.Request) {
	if s.tunnels == nil {
		http.Error(w, "tunnel service not available", http.StatusServiceUnavailable)
		return
	}

	// Relay-level auth: only local same-origin browser access may omit the token.
	// cloudflared-forwarded traffic uses the .tunnel.local host route, not this path.
	if !s.relayAuthGate(w, r) {
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/tunnel-gate/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	subPath := "/"
	if len(parts) > 1 {
		subPath = "/" + parts[1]
	}

	s.dispatchTunnelGate(w, r, id, subPath)
}

func (s *Server) dispatchTunnelGate(w http.ResponseWriter, r *http.Request, id string, subPath string) {
	info, ok := s.tunnels.GetTunnel(id)
	if !ok || info.Status != "active" {
		http.Error(w, fmt.Sprintf("Tunnel %q is inactive or closed", id), http.StatusBadGateway)
		return
	}

	// Access Control Evaluation
	switch info.AuthMode {
	case "basic_auth":
		if !s.checkBasicAuthGate(w, r, info) {
			return
		}
	case "token_gate":
		if !s.checkTokenGate(w, r, info, id, subPath) {
			return // Login page rendered or redirected
		}
	case "direct":
		// Direct pass-through, no auth required
	default:
		// Default to token_gate for safety
		if !s.checkTokenGate(w, r, info, id, subPath) {
			return
		}
	}

	// Handle WebSocket upgrade vs HTTP Reverse Proxy
	if isWebSocketRequest(r) {
		s.proxyWebSocket(w, r, info.Port, subPath, info.AuthMode)
		return
	}

	s.proxyHTTP(w, r, info.Port, subPath, info.AuthMode)
}

func (s *Server) checkBasicAuth(r *http.Request, info *protocol.TunnelInfo) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	if info.Token == "" {
		return false
	}
	// Use constant-time comparison instead of ==.
	userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(info.Token)) == 1
	passMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(info.Token)) == 1
	return userMatch || passMatch
}

// checkBasicAuthGate applies the Basic Auth gate, including rate limiting.
// Basic Auth is just as public-facing as token_gate, so it must share the
// same per-client-IP failure counter — otherwise it is an unlimited
// brute-force oracle for the user-chosen PIN that the UI explicitly allows.
// Returns true when the request may proceed to the proxy.
func (s *Server) checkBasicAuthGate(w http.ResponseWriter, r *http.Request, info *protocol.TunnelInfo) bool {
	clientIP := realClientIP(r)
	if s.gateLimiter != nil && !s.gateLimiter.allow(clientIP) {
		http.Error(w, "Too many failed attempts, try again later", http.StatusTooManyRequests)
		return false
	}
	if !s.checkBasicAuth(r, info) {
		if s.gateLimiter != nil {
			s.gateLimiter.recordFailure(clientIP)
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="Herdr Tunnel Protected Access"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	if s.gateLimiter != nil {
		s.gateLimiter.recordSuccess(clientIP)
	}
	return true
}

func (s *Server) checkTokenGate(w http.ResponseWriter, r *http.Request, info *protocol.TunnelInfo, id string, subPath string) bool {
	// Use real client IP from cloudflared headers, not RemoteAddr (which is
	// always 127.0.0.1 behind cloudflared, causing global self-lock DoS).
	clientIP := realClientIP(r)

	// Rate limit auth attempts
	if s.gateLimiter != nil && !s.gateLimiter.allow(clientIP) {
		http.Error(w, "Too many failed attempts, try again later", http.StatusTooManyRequests)
		return false
	}

	authed := false
	redirectSent := false // explicit flag instead of guessing form field

	// Check query param ?token=...
	tokenQ := r.URL.Query().Get("token")
	if info.Token != "" && subtle.ConstantTimeCompare([]byte(tokenQ), []byte(info.Token)) == 1 {
		s.setTunnelCookie(w, info.Token)
		// Do not proxy a request that still carries the gate token in its
		// URL — the protected app (and its access logs) would see the
		// credential. Redirect to the same URL with the token removed, exactly
		// as the POST login path does. Only safe for GET/HEAD, where a 302
		// preserves semantics; other methods fall through and instead have the
		// token stripped from the forwarded query below.
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if s.gateLimiter != nil {
				s.gateLimiter.recordSuccess(clientIP)
			}
			http.Redirect(w, r, gateCleanURL(r), http.StatusFound)
			return false
		}
		authed = true
	}

	// Check Cookie herdrem_tunnel_auth
	if !authed {
		if cookie, err := r.Cookie(tunnelCookieName); err == nil && info.Token != "" &&
			subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(info.Token)) == 1 {
			authed = true
		}
	}

	// Handle Form POST PIN submission
	if !authed && r.Method == http.MethodPost {
		_ = r.ParseForm()
		submittedPin := r.FormValue("pin")
		submittedToken := r.FormValue("token")
		tokenMatch := info.Token != "" && subtle.ConstantTimeCompare([]byte(submittedToken), []byte(info.Token)) == 1
		pinMatch := info.Token != "" && subtle.ConstantTimeCompare([]byte(submittedPin), []byte(info.Token)) == 1
		if tokenMatch || pinMatch {
			s.setTunnelCookie(w, info.Token)
			http.Redirect(w, r, r.URL.Path, http.StatusFound)
			redirectSent = true
			authed = true
		}
	}

	if authed {
		if s.gateLimiter != nil {
			s.gateLimiter.recordSuccess(clientIP)
		}
		// After redirect, the response is complete — must not
		// continue to proxy onto the same response. Return false unconditionally.
		if redirectSent {
			return false
		}
		return true
	}

	// Record failed attempt for ALL auth paths (GET query, cookie, POST),
	// not just POST. This prevents bypassing rate limiting via GET ?token=guess.
	if s.gateLimiter != nil {
		s.gateLimiter.recordFailure(clientIP)
	}

	// Render PIN Login Form Page
	renderPinPage(w, info, id)
	return false
}

// setTunnelCookie sets the tunnel auth cookie with Secure attribute.
func (s *Server) setTunnelCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     tunnelCookieName,
		Value:    token,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
		Secure:   true, // Always set Secure — tunnel URLs are HTTPS via cloudflared
	})
}

// realClientIP extracts the real client IP from cloudflared proxy headers.
// RemoteAddr is always 127.0.0.1 behind cloudflared, so use CF-Connecting-IP
// or X-Forwarded-For to distinguish actual clients for rate limiting.
// Both headers are client-controlled before the proxy rewrites them, so they
// are only trusted when the immediate peer is loopback (a local proxy);
// a direct remote client must not be able to pick its rate-limit key.
// XFF takes the LAST item (added by the proxy), not the first.
func realClientIP(r *http.Request) string {
	if isLoopback(r.RemoteAddr) {
		// CF-Connecting-IP is set by Cloudflare edge; trusting it only for
		// loopback peers keeps direct clients from spoofing it.
		if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
			return strings.TrimSpace(ip)
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			last := strings.TrimSpace(parts[len(parts)-1])
			if last != "" {
				return last
			}
		}
	}
	// Fallback to RemoteAddr for direct (non-proxied) access
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// gateCleanURL returns the request's path plus its query with the gate `token`
// parameter removed, for redirecting away from a credential-bearing URL.
func gateCleanURL(r *http.Request) string {
	q := r.URL.Query()
	q.Del("token")
	if len(q) == 0 {
		return r.URL.Path
	}
	return r.URL.Path + "?" + q.Encode()
}

// sanitizeGateCredentials removes the gate's own credentials from a request
// that is about to be proxied to the protected application. The app is
// not a trusted party: it must never see the gate token, the gate cookie, or
// the Authorization header that basic_auth consumed.
//
// It mutates the request in place, so it must be given the outbound request.
func sanitizeGateCredentials(req *http.Request, authMode string) {
	// Gate token in the query string.
	if q := req.URL.Query(); q.Has("token") {
		q.Del("token")
		req.URL.RawQuery = q.Encode()
	}

	// Gate cookie: re-serialize the Cookie header without it.
	if cookies := req.Cookies(); len(cookies) > 0 {
		kept := make([]*http.Cookie, 0, len(cookies))
		for _, c := range cookies {
			if c.Name != tunnelCookieName {
				kept = append(kept, c)
			}
		}
		if len(kept) != len(cookies) {
			req.Header.Del("Cookie")
			for _, c := range kept {
				req.AddCookie(c)
			}
		}
	}

	// Authorization was consumed by the basic_auth gate; do not forward it.
	if authMode == "basic_auth" {
		req.Header.Del("Authorization")
	}
}

// stripGateSetCookie drops any attempt by the protected application to
// overwrite the gate cookie.
func stripGateSetCookie(resp *http.Response) error {
	values := resp.Header.Values("Set-Cookie")
	if len(values) == 0 {
		return nil
	}
	kept := make([]string, 0, len(values))
	for _, v := range values {
		name := v
		if idx := strings.Index(name, "="); idx != -1 {
			name = name[:idx]
		}
		if strings.TrimSpace(name) == tunnelCookieName {
			continue
		}
		kept = append(kept, v)
	}
	if len(kept) == len(values) {
		return nil
	}
	resp.Header.Del("Set-Cookie")
	for _, v := range kept {
		resp.Header.Add("Set-Cookie", v)
	}
	return nil
}

func isWebSocketRequest(r *http.Request) bool {
	containsHeader := func(name, value string) bool {
		for _, v := range strings.Split(r.Header.Get(name), ",") {
			if strings.EqualFold(strings.TrimSpace(v), value) {
				return true
			}
		}
		return false
	}
	return containsHeader("Connection", "upgrade") && containsHeader("Upgrade", "websocket")
}

func (s *Server) proxyHTTP(w http.ResponseWriter, r *http.Request, targetPort int, subPath string, authMode string) {
	targetHost := fmt.Sprintf("localhost:%d", targetPort)
	revProxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.Header.Set("X-Forwarded-Host", req.Host)
			req.Header.Set("X-Forwarded-Proto", "https")
			req.Host = targetHost
			req.URL.Scheme = "http"
			req.URL.Host = targetHost
			req.URL.Path = subPath
			req.URL.RawQuery = r.URL.RawQuery
			// Strip the gate's own credentials before they reach the app.
			sanitizeGateCredentials(req, authMode)
		},
		ModifyResponse: stripGateSetCookie,
	}
	revProxy.ServeHTTP(w, r)
}

func (s *Server) proxyWebSocket(w http.ResponseWriter, r *http.Request, targetPort int, subPath string, authMode string) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "webserver doesn't support hijacking", http.StatusInternalServerError)
		return
	}

	clientConn, bufrw, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	targetAddr := fmt.Sprintf("127.0.0.1:%d", targetPort)
	backendConn, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
	if err != nil {
		_, _ = bufrw.WriteString("HTTP/1.1 502 Bad Gateway\r\n\r\n")
		_ = bufrw.Flush()
		return
	}
	defer backendConn.Close()

	// Rewrite Host and Path to bypass local dev server host checks
	r.Header.Set("X-Forwarded-Host", r.Host)
	r.Host = fmt.Sprintf("localhost:%d", targetPort)
	r.URL.Path = subPath
	// The WebSocket path must apply the same credential scrubbing as the
	// HTTP path — the upgrade request carries the same query, cookie and
	// Authorization header.
	sanitizeGateCredentials(r, authMode)

	// Write the modified HTTP upgrade request to the backend.
	if err := r.Write(backendConn); err != nil {
		return
	}

	// Flush any data the HTTP server may have buffered in bufrw's writer
	// (e.g., if a response was started before hijack — unlikely for WS but
	// defensive).
	if err := bufrw.Flush(); err != nil {
		return
	}

	// Pipe bidirectional traffic. When either direction completes or errors,
	// close both connections to unblock the other goroutine.
	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(backendConn, bufrw)
		errCh <- err
		// Signal EOF to backend by closing write side (best-effort on TCP).
		if tcp, ok := backendConn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	go func() {
		_, err := io.Copy(clientConn, backendConn)
		errCh <- err
		if tcp, ok := clientConn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()

	<-errCh
}

func renderPinPage(w http.ResponseWriter, info *protocol.TunnelInfo, id string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Herdr Tunnel Access Protection</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #0f172a; color: #f8fafc; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; }
    .card { background: #1e293b; border: 1px solid #334155; border-radius: 12px; padding: 32px; width: 100%%; max-width: 380px; box-shadow: 0 20px 25px -5px rgba(0,0,0,0.5); text-align: center; }
    h2 { font-size: 20px; font-weight: 700; margin: 0 0 8px; color: #38bdf8; }
    p { font-size: 13px; color: #94a3b8; margin: 0 0 24px; line-height: 1.5; }
    input { width: 100%%; box-sizing: border-box; background: #0f172a; border: 1px solid #475569; border-radius: 6px; padding: 10px 12px; font-family: monospace; font-size: 15px; color: #f8fafc; margin-bottom: 16px; outline: none; text-align: center; }
    input:focus { border-color: #38bdf8; }
    button { width: 100%%; background: #0284c7; border: none; border-radius: 6px; padding: 10px; font-size: 14px; font-weight: 600; color: #ffffff; cursor: pointer; }
    button:hover { background: #0369a1; }
    .badge { display: inline-block; background: #334155; color: #cbd5e1; font-family: monospace; font-size: 11px; padding: 2px 8px; border-radius: 4px; margin-bottom: 16px; }
  </style>
</head>
<body>
  <div class="card">
    <div class="badge">Port %d</div>
    <h2>Herdr Tunnel Gate</h2>
    <p>This Cloudflare tunnel is protected by Herdr Remote. Please enter your PIN or Access Token to continue.</p>
    <form method="POST">
      <input type="password" name="token" placeholder="Enter Token or PIN" autofocus required />
      <button type="submit">Verify & Access</button>
    </form>
  </div>
</body>
</html>`, info.Port)
	_, _ = w.Write([]byte(html))
}
