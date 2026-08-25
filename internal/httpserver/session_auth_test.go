package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"herdrem/internal/auth"
	"herdrem/internal/config"
)

func TestSessionAuthSetsHttpOnlyCookie(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}}
	r := httptest.NewRequest("GET", "http://relay.local/session-auth?token=secret", nil)
	r.Host = "relay.local"
	w := httptest.NewRecorder()

	s.handleSessionAuth(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	cookies := w.Result().Cookies()
	var found *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("missing session cookie")
	}
	if found.Value != "secret" || !found.HttpOnly || found.Path != "/" {
		t.Fatalf("cookie = %+v, want HttpOnly Path=/ value=secret", found)
	}
}

func TestSessionAuthRejectsCrossOrigin(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}}
	r := httptest.NewRequest("GET", "http://relay.local/session-auth?token=secret", nil)
	r.Host = "relay.local"
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()

	s.handleSessionAuth(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	if got := w.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("Set-Cookie = %q, want empty", got)
	}
}

func TestRoutesSetsSPACSPOutsidePreview(t *testing.T) {
	s := New(config.Config{}, nil, nil, nil, nil, nil, nil, nil, nil)
	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, r)
	if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
		t.Fatalf("CSP = %q, want SPA policy", got)
	}
}
