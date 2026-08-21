package auth

import (
	"net/http/httptest"
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
