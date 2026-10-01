package httpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"herdrem/internal/auth"
	"herdrem/internal/config"
)

func TestSessionAuthSetsHttpOnlyCookie(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}, revoked: map[string]time.Time{}}
	r := httptest.NewRequest("GET", "http://relay.local/session-auth", nil)
	r.Header.Set("Authorization", "Bearer secret")
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
	// The cookie must be persistent, not session-scoped: some PWA containers
	// drop session cookies on close, which would re-prompt for the token.
	// Seven idle days is the product bound — a daily PWA open must not expire
	// overnight, and a week without a successful connect still must.
	if sessionTTL != 7*24*time.Hour {
		t.Fatalf("sessionTTL = %s, want 7 days idle", sessionTTL)
	}
	if found.MaxAge != int(sessionTTL.Seconds()) {
		t.Fatalf("cookie MaxAge = %d, want %d", found.MaxAge, int(sessionTTL.Seconds()))
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
	s := &Server{cfg: config.Config{Token: "secret"}, revoked: map[string]time.Time{}}
	r := httptest.NewRequest("GET", "http://relay.local/session-auth", nil)
	r.Header.Set("Authorization", "Bearer secret")
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

// mint is a helper: /session-auth with the relay token, returning the cookie.
func mint(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest("GET", "http://relay.local/session-auth", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleSessionAuth(w, r)
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	t.Fatal("missing session cookie")
	return nil
}

// logout is a helper: POST /session-logout carrying the session cookie.
func logout(t *testing.T, s *Server, session *http.Cookie) {
	t.Helper()
	lr := httptest.NewRequest("POST", "http://relay.local/session-logout", nil)
	lr.Header.Set("Sec-Fetch-Site", "same-origin")
	lr.AddCookie(session)
	lw := httptest.NewRecorder()
	s.handleSessionLogout(lw, lr)
	if lw.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d", lw.Code, http.StatusNoContent)
	}
}

func TestSessionRenewalKeepsFamilyAndLogoutKillsAllCopies(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}, revoked: map[string]time.Time{}}
	first := mint(t, s)

	// A slide renewal presents only the live cookie — it must keep the sid
	// so the whole family dies when any copy is logged out.
	rr := httptest.NewRequest("GET", "http://relay.local/session-auth", nil)
	rr.AddCookie(first)
	rw := httptest.NewRecorder()
	s.handleSessionAuth(rw, rr)
	var renewed *http.Cookie
	for _, c := range rw.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			renewed = c
		}
	}
	if renewed == nil {
		t.Fatal("missing renewed session cookie")
	}
	sid1, _, _ := s.parseSession(first.Value)
	sid2, _, _ := s.parseSession(renewed.Value)
	if sid1 == "" || sid1 != sid2 {
		t.Fatalf("renewal changed sid %q -> %q; logout cannot kill the family", sid1, sid2)
	}

	logout(t, s, renewed)

	for name, c := range map[string]*http.Cookie{"original": first, "renewed": renewed} {
		r := httptest.NewRequest("GET", "http://relay.local/ws", nil)
		r.AddCookie(c)
		if s.relayAuthOK(r) {
			t.Fatalf("%s cookie still authenticates after family logout", name)
		}
	}
}

func TestSessionLogoutSurvivesRestart(t *testing.T) {
	// The denylist persists under DataDir: a restarted process must not
	// forgive a revoked session while its signed cookie is still valid.
	dir := t.TempDir()
	s1 := New(config.Config{Token: "secret", DataDir: dir}, nil, nil, nil, nil, nil, nil, nil, nil)
	session := mint(t, s1)
	logout(t, s1, session)

	s2 := New(config.Config{Token: "secret", DataDir: dir}, nil, nil, nil, nil, nil, nil, nil, nil)
	r := httptest.NewRequest("GET", "http://relay.local/ws", nil)
	r.AddCookie(session)
	if s2.relayAuthOK(r) {
		t.Fatal("revoked session was forgiven by a restart")
	}
}

func TestSessionLogoutAtDenylistCapacity(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}, revoked: map[string]time.Time{}}
	// Fill the denylist to the cap — a logout must still land rather than
	// silently returning 204 while skipping the revoke.
	for i := 0; i < maxSessions; i++ {
		s.revoked[fmt.Sprintf("sid-%d", i)] = time.Now().Add(sessionTTL)
	}
	session := mint(t, s)
	logout(t, s, session)

	r := httptest.NewRequest("GET", "http://relay.local/ws", nil)
	r.AddCookie(session)
	if s.relayAuthOK(r) {
		t.Fatal("logout was skipped because the denylist was full")
	}
}

func TestSessionSurvivesRestart(t *testing.T) {
	// A signed session is self-authenticating: a different Server instance
	// with the same configured token must accept it — that IS a restart.
	s1 := &Server{cfg: config.Config{Token: "secret"}, revoked: map[string]time.Time{}}
	session := mint(t, s1)

	s2 := &Server{cfg: config.Config{Token: "secret"}, revoked: map[string]time.Time{}}
	r := httptest.NewRequest("GET", "http://relay.local/ws", nil)
	r.AddCookie(session)
	if !s2.relayAuthOK(r) {
		t.Fatal("session minted before restart did not authenticate after restart")
	}
}

func TestSessionRejectsTamperedAndExpired(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}, revoked: map[string]time.Time{}}
	session := mint(t, s)

	// Forged signature: same shape, wrong HMAC.
	forged := &http.Cookie{Name: auth.SessionCookieName, Value: session.Value[:len(session.Value)-2] + "zz"}
	r := httptest.NewRequest("GET", "http://relay.local/ws", nil)
	r.AddCookie(forged)
	if s.relayAuthOK(r) {
		t.Fatal("tampered session signature authenticated")
	}

	// Expired session: correctly signed but with a past expiry — mint one by
	// hand since issueSession always signs TTL ahead.
	exp := time.Now().Add(-time.Hour).Unix()
	stale := fmt.Sprintf("v1.%s.%d.%s", "deadbeef", exp, s.sessionSig("deadbeef", exp))
	r2 := httptest.NewRequest("GET", "http://relay.local/ws", nil)
	r2.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: stale})
	if s.relayAuthOK(r2) {
		t.Fatal("expired session authenticated")
	}

	// Token rotation must invalidate old sessions (the key derives from it).
	s3 := &Server{cfg: config.Config{Token: "rotated"}, revoked: map[string]time.Time{}}
	r3 := httptest.NewRequest("GET", "http://relay.local/ws", nil)
	r3.AddCookie(session)
	if s3.relayAuthOK(r3) {
		t.Fatal("session minted under old token authenticated after rotation")
	}
}

func TestSessionAuthRejectsCrossOrigin(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}}
	r := httptest.NewRequest("GET", "http://relay.local/session-auth", nil)
	r.Host = "relay.local"
	r.Header.Set("Authorization", "Bearer secret")
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
