package httpserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"herdrem/internal/config"
	"herdrem/internal/service"
)

// A workspace file served by /file must never become an active document on
// the relay origin: script-src 'self' would otherwise let a .js file be
// included as a first-party script. Non-download responses get a sandbox CSP
// and non-inline MIME types are flattened to text/plain.
func TestHandleFileFlattensActiveMIME(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "evil.js"), []byte("alert(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte("<script>alert(1)</script>"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := service.NewFileService(func(string) []string { return []string{dir} }, nil)
	s := &Server{cfg: config.Config{Token: "secret"}, files: files, gateLimiter: newGateRateLimiter(), sessions: map[string]time.Time{}}

	for _, name := range []string{"evil.js", "page.html"} {
		r := httptest.NewRequest("GET", "/file?path="+url.QueryEscape(filepath.Join(dir, name)), nil)
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		s.handleFile(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", name, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("%s: Content-Type = %q, want text/plain", name, ct)
		}
		if csp := w.Header().Get("Content-Security-Policy"); csp != "sandbox" {
			t.Errorf("%s: CSP = %q, want sandbox", name, csp)
		}
	}
}

// Images keep their real MIME type so <img> previews keep working, and
// downloads keep the inferred type + attachment disposition.
func TestHandleFileKeepsImageMIMEAndDownloadType(t *testing.T) {
	dir := t.TempDir()
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	if err := os.WriteFile(filepath.Join(dir, "x.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "evil.js"), []byte("alert(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := service.NewFileService(func(string) []string { return []string{dir} }, nil)
	s := &Server{cfg: config.Config{Token: "secret"}, files: files, gateLimiter: newGateRateLimiter(), sessions: map[string]time.Time{}}

	r := httptest.NewRequest("GET", "/file?path="+url.QueryEscape(filepath.Join(dir, "x.png")), nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleFile(w, r)
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("png Content-Type = %q, want image/png", ct)
	}

	r = httptest.NewRequest("GET", "/file?path="+url.QueryEscape(filepath.Join(dir, "evil.js"))+"&download=1", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	s.handleFile(w, r)
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/plain") {
		t.Errorf("download Content-Type = %q, want the real script type", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Errorf("download Content-Disposition = %q, want attachment", cd)
	}
}

// A correctly-spelled token in the query string must not authenticate /file
// either — the URL channel is gone on every relay endpoint.
func TestHandleFileRejectsQueryToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := service.NewFileService(func(string) []string { return []string{dir} }, nil)
	s := &Server{cfg: config.Config{Token: "secret"}, files: files, gateLimiter: newGateRateLimiter(), sessions: map[string]time.Time{}}

	r := httptest.NewRequest("GET", "/file?path="+url.QueryEscape(path)+"&token=secret", nil)
	w := httptest.NewRecorder()
	s.handleFile(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("query-token /file status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	// The Bearer channel is the supported one.
	r = httptest.NewRequest("GET", "/file?path="+url.QueryEscape(path), nil)
	r.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	s.handleFile(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("bearer /file status = %d, want 200", w.Code)
	}
}

// Relay endpoints never rate-limit auth failures: the token is a 128-bit
// secret (brute force is not online-feasible), and per-IP blocks wedge
// legitimate clients — a PWA's reconnect loop reads as repeated failures and
// a shared egress IP then locks out every device behind it. The tunnel gate
// keeps its own limiter for the short, public-facing PIN.
func TestRelayAuthGateNeverRateLimits(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}, gateLimiter: newGateRateLimiter(), sessions: map[string]time.Time{}}
	for i := 0; i < gateMaxFailedAttempts+10; i++ {
		r := httptest.NewRequest("GET", "http://relay.local/ws", nil)
		r.Header.Set("Authorization", "Bearer wrong")
		w := httptest.NewRecorder()
		if s.relayAuthGate(w, r) {
			t.Fatal("bad token authenticated")
		}
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, w.Code)
		}
	}
	// A valid credential authenticates immediately after the failure streak.
	r := httptest.NewRequest("GET", "http://relay.local/ws", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	if !s.relayAuthGate(w, r) {
		t.Fatal("valid token rejected after failures")
	}
}

func TestRelayAuthGateExemptsLoopback(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}, gateLimiter: newGateRateLimiter(), sessions: map[string]time.Time{}}
	for i := 0; i < gateMaxFailedAttempts+3; i++ {
		r := httptest.NewRequest("GET", "http://localhost:8766/ws", nil)
		r.Header.Set("Authorization", "Bearer wrong")
		r.RemoteAddr = "127.0.0.1:5555"
		w := httptest.NewRecorder()
		s.relayAuthGate(w, r)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("loopback client was rate-limited on attempt %d", i+1)
		}
	}
}

// Rejection logs are throttled per IP: the first rejectLogBurst rejections in
// a window are logged, the rest are counted and folded into a "suppressed"
// total on the next window's first logged line. Rejections still return 401
// every time — only the log volume is bounded.
func TestRejectLogThrottleBoundsVolume(t *testing.T) {
	var th rejectLogThrottle
	now := time.Now()
	for i := 0; i < rejectLogBurst; i++ {
		if write, suppressed := th.allow("192.0.2.9", now); !write || suppressed != 0 {
			t.Fatalf("attempt %d: write=%v suppressed=%d, want true/0", i+1, write, suppressed)
		}
	}
	for i := 0; i < 7; i++ {
		if write, _ := th.allow("192.0.2.9", now); write {
			t.Fatalf("attempt %d should have been suppressed", rejectLogBurst+i+1)
		}
	}
	// A different IP is not affected by the first IP's throttle state.
	if write, _ := th.allow("203.0.113.7", now); !write {
		t.Fatal("other IP throttled by a different IP's rejections")
	}
	// Next window: the first rejection logs again and reports the suppressed
	// count from the previous window.
	write, suppressed := th.allow("192.0.2.9", now.Add(rejectLogWindow+time.Second))
	if !write || suppressed != 7 {
		t.Fatalf("next window: write=%v suppressed=%d, want true/7", write, suppressed)
	}
}

// A preview capability minted by one client IP must not replay from another.
func TestPreviewCapabilityBindsClientIP(t *testing.T) {
	s := &Server{previewCaps: make(map[string]previewCapability)}
	capability, err := s.issuePreviewCapability("local", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if s.validPreviewCapability(capability, "local", "203.0.113.9") {
		t.Fatal("capability replayed from a different client IP")
	}
	if !s.validPreviewCapability(capability, "local", "192.0.2.1") {
		t.Fatal("capability rejected for its minting IP")
	}
	// Loopback representations are the same client.
	loop, err := s.issuePreviewCapability("local", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !s.validPreviewCapability(loop, "local", "::1") {
		t.Fatal("loopback capability should work across 127.0.0.1/::1")
	}
}
