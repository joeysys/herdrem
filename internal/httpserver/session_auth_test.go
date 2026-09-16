package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"herdrem/internal/auth"
	"herdrem/internal/config"
)

func TestSessionAuthSetsHttpOnlyCookie(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}, sessions: map[string]time.Time{}}
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
	if !found.HttpOnly || found.Path != "/" {
		t.Fatalf("cookie = %+v, want HttpOnly Path=/", found)
	}
	// The cookie must carry an opaque session id, never the relay token.
	if found.Value == "" || found.Value == "secret" {
		t.Fatalf("cookie value %q must be an opaque session id, not the token", found.Value)
	}
	// The session must authenticate follow-up requests on its own.
	r2 := httptest.NewRequest("GET", "http://relay.local/ws", nil)
	r2.AddCookie(found)
	if !s.relayAuthOK(r2) {
		t.Fatal("session cookie did not authenticate a follow-up request")
	}
}

func TestSessionLogoutRevokesSession(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}, sessions: map[string]time.Time{}}
	r := httptest.NewRequest("GET", "http://relay.local/session-auth?token=secret", nil)
	w := httptest.NewRecorder()
	s.handleSessionAuth(w, r)
	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			session = c
		}
	}
	if session == nil {
		t.Fatal("missing session cookie")
	}

	lr := httptest.NewRequest("POST", "http://relay.local/session-logout", nil)
	lr.Header.Set("Sec-Fetch-Site", "same-origin")
	lr.AddCookie(session)
	lw := httptest.NewRecorder()
	s.handleSessionLogout(lw, lr)
	if lw.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d", lw.Code, http.StatusNoContent)
	}

	r2 := httptest.NewRequest("GET", "http://relay.local/ws", nil)
	r2.AddCookie(session)
	if s.relayAuthOK(r2) {
		t.Fatal("revoked session still authenticates")
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
