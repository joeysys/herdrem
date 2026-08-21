package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"herdrem/internal/protocol"
)

// A successful ?token= must redirect to a token-free URL rather than proxy the
// request, so the protected application never sees the gate credential.
func TestTokenGateRedirectsAwayFromTokenURL(t *testing.T) {
	s := &Server{gateLimiter: newGateRateLimiter()}
	info := &protocol.TunnelInfo{Token: "supersecret123", Port: 5173}

	r := httptest.NewRequest("GET", "http://tun.tunnel.local/app?token=supersecret123&keep=1", nil)
	w := httptest.NewRecorder()

	if proceed := s.checkTokenGate(w, r, info, "tun", "/app"); proceed {
		t.Fatal("checkTokenGate returned true after issuing a redirect; caller would proxy onto the same response")
	}
	if w.Code != http.StatusFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusFound)
	}
	if got, want := w.Header().Get("Location"), "/app?keep=1"; got != want {
		t.Errorf("Location = %q, want %q (gate token must be stripped, other params kept)", got, want)
	}
}

// The POST login form posts its credential in a field named "token"; the gate
// must stop after redirecting regardless of which field was used.
func TestTokenGatePostLoginStopsAfterRedirect(t *testing.T) {
	info := &protocol.TunnelInfo{Token: "supersecret123", Port: 5173}
	for _, field := range []string{"token", "pin"} {
		s := &Server{gateLimiter: newGateRateLimiter()}
		r := httptest.NewRequest("POST", "http://tun.tunnel.local/",
			strings.NewReader(field+"=supersecret123"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		if proceed := s.checkTokenGate(w, r, info, "tun", "/"); proceed {
			t.Errorf("field %q: returned true after redirect", field)
		}
		if w.Code != http.StatusFound {
			t.Errorf("field %q: status = %d, want %d", field, w.Code, http.StatusFound)
		}
	}
}

// Gate credentials must never reach the protected application, on either the
// HTTP or the WebSocket path (both call sanitizeGateCredentials).
func TestSanitizeGateCredentials(t *testing.T) {
	req := httptest.NewRequest("GET", "http://x/app?token=supersecret123&keep=1", nil)
	req.AddCookie(&http.Cookie{Name: tunnelCookieName, Value: "supersecret123"})
	req.AddCookie(&http.Cookie{Name: "app_session", Value: "keepme"})
	req.Header.Set("Authorization", "Basic Zm9vOmJhcg==")

	sanitizeGateCredentials(req, "basic_auth")

	if req.URL.Query().Has("token") {
		t.Error("gate token still present in forwarded query")
	}
	if got := req.URL.Query().Get("keep"); got != "1" {
		t.Errorf("application's own query param was dropped: keep=%q", got)
	}
	if _, err := req.Cookie(tunnelCookieName); err == nil {
		t.Error("gate cookie still forwarded to application")
	}
	if _, err := req.Cookie("app_session"); err != nil {
		t.Error("application's own cookie was wrongly dropped")
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("basic_auth Authorization header still forwarded to application")
	}
}

// Only basic_auth consumes Authorization for the gate; other modes must pass
// the application's own Authorization header through untouched.
func TestSanitizeGateCredentialsKeepsAppAuthorization(t *testing.T) {
	req := httptest.NewRequest("GET", "http://x/", nil)
	req.Header.Set("Authorization", "Bearer app-token")

	sanitizeGateCredentials(req, "token_gate")

	if req.Header.Get("Authorization") != "Bearer app-token" {
		t.Error("application's Authorization header was dropped in token_gate mode")
	}
}

// A malicious or careless application must not be able to overwrite the gate
// cookie via its own Set-Cookie.
func TestStripGateSetCookie(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", tunnelCookieName+"=attacker-chosen; Path=/")
	resp.Header.Add("Set-Cookie", "app_pref=dark; Path=/")

	if err := stripGateSetCookie(resp); err != nil {
		t.Fatalf("stripGateSetCookie: %v", err)
	}

	got := resp.Header.Values("Set-Cookie")
	if len(got) != 1 || got[0] != "app_pref=dark; Path=/" {
		t.Errorf("Set-Cookie = %v, want only the application's own cookie", got)
	}
}

// Basic Auth is as public-facing as token_gate and must share its rate limiter.
func TestBasicAuthGateIsRateLimited(t *testing.T) {
	s := &Server{gateLimiter: newGateRateLimiter()}
	info := &protocol.TunnelInfo{Token: "supersecret123", Port: 5173, AuthMode: "basic_auth"}

	var last int
	for i := 0; i < gateMaxFailedAttempts+2; i++ {
		r := httptest.NewRequest("GET", "http://tun.tunnel.local/", nil)
		r.RemoteAddr = "203.0.113.7:1111"
		r.SetBasicAuth("u", "wrong-guess")
		w := httptest.NewRecorder()
		s.checkBasicAuthGate(w, r, info)
		last = w.Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("status after %d failures = %d, want %d",
			gateMaxFailedAttempts+2, last, http.StatusTooManyRequests)
	}
}

// The rate-limit key must not be attacker-controlled: X-Forwarded-For is only
// trusted from loopback (local cloudflared), and then only its last entry.
func TestRealClientIPTrustBoundary(t *testing.T) {
	cases := []struct {
		name, remoteAddr, xff, cfIP, want string
	}{
		{"non-loopback ignores XFF", "203.0.113.9:1234", "1.1.1.1, 2.2.2.2", "", "203.0.113.9"},
		{"loopback takes last XFF entry", "127.0.0.1:5555", "1.1.1.1, 2.2.2.2", "", "2.2.2.2"},
		{"CF-Connecting-IP wins", "127.0.0.1:5555", "1.1.1.1", "198.51.100.4", "198.51.100.4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://x/", nil)
			r.RemoteAddr = c.remoteAddr
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if c.cfIP != "" {
				r.Header.Set("CF-Connecting-IP", c.cfIP)
			}
			if got := realClientIP(r); got != c.want {
				t.Errorf("realClientIP = %q, want %q", got, c.want)
			}
		})
	}
}

// Host-header routing must only be reachable from local cloudflared.
func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8766": true,
		"[::1]:8766":     true,
		"127.0.0.1":      true,
		"203.0.113.9:80": false,
		"192.168.1.5:80": false,
		"":               false,
	}
	for addr, want := range cases {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

// Cross-origin preview auth must be refused outright, never answered with a
// reflected credentialed CORS header.
func TestPreviewAuthRejectsCrossOrigin(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "http://relay.local/preview-auth", nil)
	r.Host = "relay.local"
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()

	s.handlePreviewAuth(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty (no reflection)", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want empty", got)
	}
}
