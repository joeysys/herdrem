package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
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
	// Token via query param ?token=… — kept for old bookmarks and for
	// cross-origin subresource URLs (<img>, navigation) that cannot carry
	// headers. First-party code paths use the channels below instead so the
	// long-term token stays out of URLs, history, and proxy logs.
	q := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(q), []byte(expected)) == 1 {
		return true
	}
	// Token via Authorization: Bearer (fetch/XHR clients).
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(a, "Bearer ")), []byte(expected)) == 1 {
			return true
		}
	}
	// Token via HttpOnly session cookie (same-origin SPA).
	if c, err := r.Cookie(SessionCookieName); err == nil {
		if subtle.ConstantTimeCompare([]byte(c.Value), []byte(expected)) == 1 {
			return true
		}
	}
	// Token via Sec-WebSocket-Protocol header (browser WebSocket — its only
	// credential channel besides query). Subprotocol values must be HTTP
	// tchar-safe, so the frontend sends "herdrem.<base64url(token)>"; a raw
	// token value is still accepted for non-browser clients and simple tokens.
	for _, proto := range r.Header.Values("Sec-WebSocket-Protocol") {
		if subtle.ConstantTimeCompare([]byte(proto), []byte(expected)) == 1 {
			return true
		}
		if decoded, ok := decodeWSAuthSubprotocol(proto); ok &&
			subtle.ConstantTimeCompare([]byte(decoded), []byte(expected)) == 1 {
			return true
		}
	}
	return false
}

const wsAuthSubprotocolPrefix = "herdrem."

// decodeWSAuthSubprotocol decodes the "herdrem.<base64url(token)>" form the
// browser WebSocket client sends. RawURL (unpadded) base64 is required: '='
// padding is not a legal subprotocol character.
func decodeWSAuthSubprotocol(proto string) (string, bool) {
	if !strings.HasPrefix(proto, wsAuthSubprotocolPrefix) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(proto, wsAuthSubprotocolPrefix))
	if err != nil {
		return "", false
	}
	return string(raw), true
}
