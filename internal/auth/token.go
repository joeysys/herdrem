package auth

import (
	"crypto/subtle"
	"net/http"
)

// SessionCookieName is the HttpOnly cookie that carries the relay token for
// same-origin requests (WS upgrade, /file, /session-auth).
const SessionCookieName = "herdrem_auth"

// Check validates the token against the expected value.
// If expected is empty, auth is disabled (dev mode).
func Check(r *http.Request, expected string) bool {
	if expected == "" {
		return true
	}
	// Token via query param ?token=…
	q := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(q), []byte(expected)) == 1 {
		return true
	}
	// Token via HttpOnly session cookie (same-origin SPA).
	if c, err := r.Cookie(SessionCookieName); err == nil {
		if subtle.ConstantTimeCompare([]byte(c.Value), []byte(expected)) == 1 {
			return true
		}
	}
	// Token via Sec-WebSocket-Protocol header (browser-friendly)
	for _, proto := range r.Header.Values("Sec-WebSocket-Protocol") {
		if subtle.ConstantTimeCompare([]byte(proto), []byte(expected)) == 1 {
			return true
		}
	}
	return false
}
