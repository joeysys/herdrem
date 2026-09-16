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
		r := httptest.NewRequest("GET", "/file?path="+url.QueryEscape(filepath.Join(dir, name))+"&token=secret", nil)
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

	r := httptest.NewRequest("GET", "/file?path="+url.QueryEscape(filepath.Join(dir, "x.png"))+"&token=secret", nil)
	w := httptest.NewRecorder()
	s.handleFile(w, r)
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("png Content-Type = %q, want image/png", ct)
	}

	r = httptest.NewRequest("GET", "/file?path="+url.QueryEscape(filepath.Join(dir, "evil.js"))+"&token=secret&download=1", nil)
	w = httptest.NewRecorder()
	s.handleFile(w, r)
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/plain") {
		t.Errorf("download Content-Type = %q, want the real script type", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Errorf("download Content-Disposition = %q, want attachment", cd)
	}
}

// Repeated auth failures from a non-loopback client are throttled; loopback
// clients stay exempt so a local user can never lock themselves out.
func TestRelayAuthGateRateLimitsRemoteFailures(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}, gateLimiter: newGateRateLimiter(), sessions: map[string]time.Time{}}
	for i := 0; i < gateMaxFailedAttempts; i++ {
		r := httptest.NewRequest("GET", "http://relay.local/ws?token=wrong", nil)
		w := httptest.NewRecorder()
		if s.relayAuthGate(w, r) {
			t.Fatal("bad token authenticated")
		}
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, w.Code)
		}
	}
	// Next failure trips the block — the limiter answers before re-checking.
	r := httptest.NewRequest("GET", "http://relay.local/ws?token=wrong", nil)
	w := httptest.NewRecorder()
	if s.relayAuthGate(w, r) {
		t.Fatal("blocked request authenticated")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}
}

func TestRelayAuthGateExemptsLoopback(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}, gateLimiter: newGateRateLimiter(), sessions: map[string]time.Time{}}
	for i := 0; i < gateMaxFailedAttempts+3; i++ {
		r := httptest.NewRequest("GET", "http://localhost:8766/ws?token=wrong", nil)
		r.RemoteAddr = "127.0.0.1:5555"
		w := httptest.NewRecorder()
		s.relayAuthGate(w, r)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("loopback client was rate-limited on attempt %d", i+1)
		}
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
