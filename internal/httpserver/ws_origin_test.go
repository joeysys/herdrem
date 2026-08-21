package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
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

	s := New(cfg, h, nil, nil, nil, nil, nil, nil, logger)
	srv := httptest.NewServer(s.Routes())
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), h, cancel
}

// dialWS attempts a WS upgrade with an explicit Origin and reports whether the
// handshake succeeded, plus the HTTP status when it did not.
func dialWS(t *testing.T, wsURL, origin string) (bool, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL+"/ws", &websocket.DialOptions{
		HTTPHeader: map[string][]string{"Origin": {origin}},
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

// Baseline: with no token and no allowlist, cross-origin must still be
// rejected — otherwise any website could drive the user's relay (CSRF).
func TestWSCrossOriginRejectedByDefault(t *testing.T) {
	wsURL, _, cancel := wsOriginServer(t, config.Config{})
	defer cancel()

	if ok, _ := dialWS(t, wsURL, wsURL); !ok {
		t.Error("same-origin dial should succeed")
	}
	ok, code := dialWS(t, wsURL, "http://localhost:8765")
	if ok {
		t.Fatal("cross-origin dial succeeded with no token and no allowlist; relay is open to CSRF")
	}
	if code != 403 {
		t.Errorf("cross-origin status = %d, want 403", code)
	}
}

// HERDR_ALLOWED_ORIGINS lets a trusted origin (e.g. a LAN dev UI) connect
// without a token, while everything else stays rejected.
func TestWSAllowedOriginsPermitsListedOriginOnly(t *testing.T) {
	wsURL, _, cancel := wsOriginServer(t, config.Config{
		AllowedOrigins: "localhost:8765, dev.example.com",
	})
	defer cancel()

	for _, origin := range []string{"http://localhost:8765", "https://dev.example.com"} {
		if ok, code := dialWS(t, wsURL, origin); !ok {
			t.Errorf("allowlisted origin %q rejected (status %d)", origin, code)
		}
	}
	if ok, _ := dialWS(t, wsURL, "http://evil.example.com"); ok {
		t.Error("unlisted origin must stay rejected")
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
	if resp != nil && resp.StatusCode != 403 {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}
