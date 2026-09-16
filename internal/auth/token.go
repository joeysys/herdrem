package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// SessionCookieName is the HttpOnly cookie used by same-origin requests
// (WS upgrade, /file, /session-auth). It carries either a minted session id
// or, for legacy/self-issued values, the raw token.
const SessionCookieName = "herdrem_auth"

// Check validates the token against the expected value.
// If expected is empty, auth is disabled (dev mode).
func Check(r *http.Request, expected string) bool {
	if expected == "" {
		return true
	}
	// Token via Authorization: Bearer (fetch/XHR clients). Query ?token= is
	// deliberately not accepted: the long-term secret must not live in URLs,
	// history, or proxy logs. (The tunnel gate's PIN query is a separate
	// per-tunnel credential handled in tunnel_gate.go.)
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
	// credential channel). Subprotocol values must be HTTP
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

// MatchSubprotocols returns the client-offered Sec-WebSocket-Protocol values
// that carry expected — the raw token or the herdrem.<base64url> form. The
// WS upgrade must echo one back: a browser (and any WHATWG-conformant
// client) that offered subprotocols and receives none in the 101 response
// fails the handshake, regardless of whether the credential authenticated.
func MatchSubprotocols(r *http.Request, expected string) []string {
	if expected == "" {
		return nil
	}
	var out []string
	for _, proto := range r.Header.Values("Sec-WebSocket-Protocol") {
		if subtle.ConstantTimeCompare([]byte(proto), []byte(expected)) == 1 {
			out = append(out, proto)
			continue
		}
		if decoded, ok := decodeWSAuthSubprotocol(proto); ok &&
			subtle.ConstantTimeCompare([]byte(decoded), []byte(expected)) == 1 {
			out = append(out, proto)
		}
	}
	return out
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
