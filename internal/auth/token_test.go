package auth

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckEmptyTokenAllowsAll(t *testing.T) {
	r := httptest.NewRequest("GET", "/ws", nil)
	if !Check(r, "") {
		t.Error("empty expected token should allow (dev mode)")
	}
}

func TestCheckQueryToken(t *testing.T) {
	r := httptest.NewRequest("GET", "/ws?token=secret", nil)
	if !Check(r, "secret") {
		t.Error("correct query token should pass")
	}
	if Check(r, "wrong") {
		t.Error("wrong query token should fail")
	}
}

func TestCheckDoesNotReadRequestBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/file", strings.NewReader("token=secret"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if Check(r, "secret") {
		t.Error("form token should not be checked by common auth")
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if string(body) != "token=secret" {
		t.Errorf("request body = %q, want token=secret", body)
	}
}

func TestCheckNoTokenRejected(t *testing.T) {
	r := httptest.NewRequest("GET", "/ws", nil)
	if Check(r, "secret") {
		t.Error("missing token when expected set should fail")
	}
}

func TestCheckSecWebSocketProtocol(t *testing.T) {
	r := httptest.NewRequest("GET", "/ws", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "secret")
	if !Check(r, "secret") {
		t.Error("correct Sec-WebSocket-Protocol should pass")
	}
}

func TestCheckSecWebSocketProtocolEncoded(t *testing.T) {
	// Browser clients send herdrem.<base64url(token)> so tokens containing
	// subprotocol-illegal characters still authenticate.
	tok := "s3cret token/with=specials?"
	enc := base64.RawURLEncoding.EncodeToString([]byte(tok))
	r := httptest.NewRequest("GET", "/ws", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "herdrem."+enc)
	if !Check(r, tok) {
		t.Error("encoded Sec-WebSocket-Protocol should pass")
	}
	r = httptest.NewRequest("GET", "/ws", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "herdrem."+base64.RawURLEncoding.EncodeToString([]byte("wrong")))
	if Check(r, tok) {
		t.Error("wrong encoded Sec-WebSocket-Protocol should fail")
	}
	// Malformed encodings must not panic or authenticate.
	r = httptest.NewRequest("GET", "/ws", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "herdrem.!!!not-b64")
	if Check(r, tok) {
		t.Error("malformed encoded subprotocol should fail")
	}
}

func TestCheckAuthorizationBearer(t *testing.T) {
	r := httptest.NewRequest("GET", "/session-auth", nil)
	r.Header.Set("Authorization", "Bearer secret")
	if !Check(r, "secret") {
		t.Error("correct Bearer token should pass")
	}
	r = httptest.NewRequest("GET", "/session-auth", nil)
	r.Header.Set("Authorization", "Bearer wrong")
	if Check(r, "secret") {
		t.Error("wrong Bearer token should fail")
	}
	// Non-Bearer schemes must not be treated as token carriers.
	r = httptest.NewRequest("GET", "/session-auth", nil)
	r.Header.Set("Authorization", "Basic c2VjcmV0")
	if Check(r, "secret") {
		t.Error("non-Bearer Authorization should fail")
	}
}

func TestCheckSessionCookie(t *testing.T) {
	r := httptest.NewRequest("GET", "/ws", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "secret"})
	if !Check(r, "secret") {
		t.Error("correct session cookie should pass")
	}
	r = httptest.NewRequest("GET", "/ws", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "wrong"})
	if Check(r, "secret") {
		t.Error("wrong session cookie should fail")
	}
}
