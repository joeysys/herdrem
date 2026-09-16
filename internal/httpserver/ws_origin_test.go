package httpserver

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"herdrem/internal/auth"
	"herdrem/internal/config"
	"herdrem/internal/hub"
)

// wsOriginServer spins up the real /ws handler so the tests exercise
// websocket.Accept's own origin verification, not a reimplementation of it.
func wsOriginServer(t *testing.T, cfg config.Config) (string, *hub.Hub, context.CancelFunc) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)

	staticFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	s := New(cfg, h, nil, nil, nil, nil, nil, staticFS, logger)
	srv := httptest.NewServer(s.Routes())
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), h, cancel
}

// dialWS attempts a WS upgrade with an explicit Origin and reports whether the
// handshake succeeded, plus the HTTP status when it did not.
func dialWS(t *testing.T, wsURL, origin string) (bool, int) {
	return dialWSWithHost(t, wsURL, origin, "")
}

func dialWSWithHost(t *testing.T, wsURL, origin, host string) (bool, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL+"/ws", &websocket.DialOptions{
		HTTPHeader: map[string][]string{"Origin": {origin}},
		Host:       host,
	})
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		return false, code
	}
	conn.Close(websocket.StatusNormalClosure, "")
	return true, 0
}

// With a configured token, even a same-origin localhost browser must present
// a credential (token or minted session) — the loopback bypass would
// otherwise let any process that can produce a same-origin request drive the
// relay. Without a token configured, the same-origin loopback path stays
// open (dev mode).
func TestWSSameOriginLocalhostRequiresCredentialWithToken(t *testing.T) {
	wsURL, _, cancel := wsOriginServer(t, config.Config{Token: "s3cret"})
	defer cancel()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(wsURL, "ws://"))
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"localhost:" + port, "127.0.0.1:" + port} {
		origin := "http://" + host
		if ok, code := dialWSWithHost(t, wsURL, origin, host); ok {
			t.Fatalf("local same-origin host %q without credential succeeded", host)
		} else if code != http.StatusUnauthorized {
			t.Errorf("local same-origin host %q status = %d, want %d", host, code, http.StatusUnauthorized)
		}
		// The same dial carrying the token must succeed.
		ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, _, err := websocket.Dial(ctx, wsURL+"/ws?token=s3cret", &websocket.DialOptions{
			HTTPHeader: map[string][]string{"Origin": {origin}},
			Host:       host,
		})
		ctxCancel()
		if err != nil {
			t.Fatalf("local same-origin host %q with token failed: %v", host, err)
		}
		conn.Close(websocket.StatusNormalClosure, "")
	}
}

func TestWSSameOriginNonLocalRequiresToken(t *testing.T) {
	wsURL, _, cancel := wsOriginServer(t, config.Config{Token: "s3cret"})
	defer cancel()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(wsURL, "ws://"))
	if err != nil {
		t.Fatal(err)
	}
	host := "192.0.2.10:" + port
	origin := "http://" + host
	if ok, code := dialWSWithHost(t, wsURL, origin, host); ok {
		t.Fatal("non-local same-origin dial without token succeeded")
	} else if code != http.StatusUnauthorized {
		t.Errorf("non-local same-origin status = %d, want %d", code, http.StatusUnauthorized)
	}
}

func TestLocalOriginHeadersFromRemotePeerRequireToken(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "s3cret"}}
	r := httptest.NewRequest("GET", "http://localhost:8766/ws", nil)
	r.Header.Set("Origin", "http://localhost:8766")
	r.RemoteAddr = "192.0.2.10:12345"

	if s.relayAuthOK(r) {
		t.Fatal("remote peer spoofing localhost headers bypassed relay token")
	}
}

func TestIPv6LoopbackHostRequiresCredentialWithToken(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "s3cret"}, sessions: map[string]time.Time{}}
	r := httptest.NewRequest("GET", "http://[::1]:8766/ws", nil)
	r.Header.Set("Origin", "http://[::1]:8766")
	r.RemoteAddr = "[::1]:12345"

	if s.relayAuthOK(r) {
		t.Fatal("IPv6 loopback same-origin request bypassed a configured token")
	}

	// A minted session cookie is the credential that keeps localhost UX
	// token-free after the first login.
	session, err := s.issueSession()
	if err != nil {
		t.Fatal(err)
	}
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	if !s.relayAuthOK(r) {
		t.Fatal("session cookie did not authenticate the IPv6 loopback request")
	}
}

// Cross-origin must still be rejected without a token — otherwise any website
// could drive the user's relay (CSRF).
func TestWSCrossOriginRejectedByDefault(t *testing.T) {
	wsURL, _, cancel := wsOriginServer(t, config.Config{})
	defer cancel()

	if ok, _ := dialWS(t, wsURL, wsURL); !ok {
		t.Error("same-origin dial should succeed")
	}
	ok, code := dialWS(t, wsURL, "http://localhost:8765")
	if ok {
		t.Fatal("cross-origin dial succeeded without a token; relay is open to CSRF")
	}
	if code != http.StatusUnauthorized {
		t.Errorf("cross-origin status = %d, want %d", code, http.StatusUnauthorized)
	}
}

// A configured token that is presented correctly authorizes any origin: the
// token is the CSRF defense, which is what makes the relay switcher and a
// separate Pages deployment work cross-origin.
func TestWSValidTokenAuthorizesAnyOrigin(t *testing.T) {
	wsURL, _, cancel := wsOriginServer(t, config.Config{Token: "s3cret"})
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()
	conn, _, err := websocket.Dial(ctx, wsURL+"/ws?token=s3cret", &websocket.DialOptions{
		HTTPHeader: map[string][]string{"Origin": {"http://localhost:8765"}},
	})
	if err != nil {
		t.Fatalf("cross-origin dial with valid token failed: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")

	// Wrong token cross-origin must not get through.
	if ok, _ := dialWS(t, wsURL, "http://localhost:8765"); ok {
		t.Error("cross-origin dial without the token succeeded")
	}
}

// The browser client carries the token as the herdrem.<base64url> subprotocol
// instead of ?token=, keeping the credential out of the URL. Prove the whole
// path works end to end through the WS upgrade.
func TestWSSubprotocolTokenAuthenticates(t *testing.T) {
	tok := "s3cret token/with=specials?"
	wsURL, _, cancel := wsOriginServer(t, config.Config{Token: tok})
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()
	conn, _, err := websocket.Dial(ctx, wsURL+"/ws", &websocket.DialOptions{
		Subprotocols: []string{"herdrem." + base64.RawURLEncoding.EncodeToString([]byte(tok))},
	})
	if err != nil {
		t.Fatalf("subprotocol-authenticated dial failed: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

// Regression guard for the subtle part of the fix: auth.Check returns true for
// everyone when no token is configured, so keying InsecureSkipVerify off it
// alone would silently disable origin verification on every token-less relay.
func TestWSEmptyTokenDoesNotDisableOriginCheck(t *testing.T) {
	wsURL, _, cancel := wsOriginServer(t, config.Config{Token: ""})
	defer cancel()

	// A bogus token must not buy cross-origin access on a token-less relay.
	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()
	conn, resp, err := websocket.Dial(ctx, wsURL+"/ws?token=anything", &websocket.DialOptions{
		HTTPHeader: map[string][]string{"Origin": {"http://evil.example.com"}},
	})
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("token-less relay accepted a cross-origin dial carrying an arbitrary token")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}
