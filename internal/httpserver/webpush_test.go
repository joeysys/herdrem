package httpserver

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"herdrem/internal/config"
	"herdrem/internal/hub"
	"herdrem/internal/store"
	"herdrem/internal/webpush"
)

func TestWebPushRoutesStayClosedUntilEnabled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenWebPush(t.TempDir() + "/webpush.json")
	if err != nil {
		t.Fatal(err)
	}
	n := webpush.NewNotifier(st, nil, logger)
	s := New(config.Config{}, hub.New(logger), nil, nil, nil, nil, nil, nil, logger)
	s.SetWebPush(n)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	code, body := pushReq(t, srv, http.MethodGet, "/webpush/status", "")
	if code != 200 || !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("status %d %s", code, body)
	}
	if code, _ := pushReq(t, srv, http.MethodPost, "/webpush/subscribe", `{}`); code != http.StatusNotFound {
		t.Fatalf("subscribe while off = %d", code)
	}
	if code, _ := pushReq(t, srv, http.MethodPost, "/webpush/prefs", `{}`); code != http.StatusNotFound {
		t.Fatalf("prefs while off = %d", code)
	}
	if code, _ := pushReq(t, srv, http.MethodPost, "/webpush/test", `{}`); code != http.StatusNotFound {
		t.Fatalf("test while off = %d", code)
	}

	code, body = pushReq(t, srv, http.MethodPost, "/webpush/enabled", `{"enabled":true}`)
	if code != 200 || !strings.Contains(body, `"publicKey"`) {
		t.Fatalf("enable %d %s", code, body)
	}
	if !st.Enabled() {
		t.Fatal("enable did not persist")
	}
	pub, priv := st.Keys()
	if pub == "" || priv == "" {
		t.Fatal("keys were not stored")
	}

	code, body = pushReq(t, srv, http.MethodPost, "/webpush/enabled", `{"enabled":false}`)
	if code != 200 || !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("disable %d %s", code, body)
	}
	if code, _ := pushReq(t, srv, http.MethodPost, "/webpush/test", `{}`); code != http.StatusNotFound {
		t.Fatal("test stayed registered after disable")
	}
	pub2, _ := st.Keys()
	if pub2 != pub {
		t.Fatal("disabling rotated the vapid key")
	}
}

func TestWebPushRejectsUnstableOriginAndOtherSites(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	const token = "0123456789abcdef"
	st, err := store.OpenWebPush(t.TempDir() + "/webpush.json")
	if err != nil {
		t.Fatal(err)
	}
	s := New(config.Config{Token: token}, hub.New(logger), nil, nil, nil, nil, nil, nil, logger)
	s.SetWebPush(webpush.NewNotifier(st, nil, logger))
	h := s.Routes()

	req := httptest.NewRequest(http.MethodPost, "/webpush/enabled", strings.NewReader(`{"enabled":true}`))
	req.Host = "abc.trycloudflare.com"
	req.Header.Set("Origin", "https://abc.trycloudflare.com")
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "203.0.113.9:4000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "changes") {
		t.Fatalf("unstable %d %s", rec.Code, rec.Body.String())
	}
	if st.Enabled() {
		t.Fatal("unstable origin enabled push")
	}

	req = httptest.NewRequest(http.MethodGet, "/webpush/status", nil)
	req.Host = "127.0.0.1:8766"
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "203.0.113.9:4000"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/webpush/status", nil)
	req.Host = "127.0.0.1:8766"
	req.Header.Set("Origin", "http://127.0.0.1:8766")
	req.RemoteAddr = "203.0.113.9:4000"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token = %d", rec.Code)
	}
}

func TestWebPushStatusEchoesOnlyTheMatchingSubscription(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenWebPush(t.TempDir() + "/webpush.json")
	if err != nil {
		t.Fatal(err)
	}
	s := New(config.Config{}, hub.New(logger), nil, nil, nil, nil, nil, nil, logger)
	s.SetWebPush(webpush.NewNotifier(st, nil, logger))
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	if code, body := pushReq(t, srv, http.MethodPost, "/webpush/enabled", `{"enabled":true}`); code != 200 {
		t.Fatalf("enable %d %s", code, body)
	}
	p256 := base64.RawURLEncoding.EncodeToString(append([]byte{0x04}, make([]byte, 64)...))
	auth := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	const mine = "https://push.example/mine"
	const other = "https://push.example/other"
	subscribe := func(endpoint string, input bool, done string) string {
		return fmt.Sprintf(`{"endpoint":%q,"p256dh":%q,"auth":%q,"input":%t,"done":%q}`, endpoint, p256, auth, input, done)
	}
	if code, body := pushReq(t, srv, http.MethodPost, "/webpush/subscribe", subscribe(mine, false, "always")); code != http.StatusNoContent {
		t.Fatalf("subscribe mine %d %s", code, body)
	}
	if code, body := pushReq(t, srv, http.MethodPost, "/webpush/subscribe", subscribe(other, true, "off")); code != http.StatusNoContent {
		t.Fatalf("subscribe other %d %s", code, body)
	}

	code, body := pushReqHeader(t, srv, http.MethodGet, "/webpush/status", "", mine)
	if code != 200 || !strings.Contains(body, `"input":false`) || !strings.Contains(body, `"done":"always"`) || strings.Contains(body, `"done":"off"`) || !strings.Contains(body, `"subscribed":true`) {
		t.Fatalf("matching status %d %s", code, body)
	}
	if strings.Contains(body, "push.example") || strings.Contains(body, p256) {
		t.Fatalf("status leaked subscription material: %s", body)
	}

	_, plain := pushReq(t, srv, http.MethodGet, "/webpush/status", "")
	if strings.Contains(plain, `"input"`) || strings.Contains(plain, `"done"`) || strings.Contains(plain, `"subscribed":true`) {
		t.Fatalf("status without endpoint echoed prefs: %s", plain)
	}
	_, wrong := pushReqHeader(t, srv, http.MethodGet, "/webpush/status", "", "https://push.example/missing")
	if strings.Contains(wrong, `"input"`) || strings.Contains(wrong, `"done"`) || strings.Contains(wrong, `"subscribed":true`) {
		t.Fatalf("unknown endpoint echoed prefs: %s", wrong)
	}

	if code, _ := pushReq(t, srv, http.MethodPost, "/webpush/enabled", `{"enabled":false}`); code != 200 {
		t.Fatalf("disable %d", code)
	}
	code, body = pushReqHeader(t, srv, http.MethodPost, "/webpush/enabled", `{"enabled":true}`, mine)
	if code != 200 || !strings.Contains(body, `"input":false`) || !strings.Contains(body, `"done":"always"`) {
		t.Fatalf("re-enable did not return stored prefs %d %s", code, body)
	}
	if strings.Contains(body, mine) {
		t.Fatalf("re-enable leaked the endpoint: %s", body)
	}
}

func TestWebPushTestDoesNotEchoUpstreamError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenWebPush(t.TempDir() + "/webpush.json")
	if err != nil {
		t.Fatal(err)
	}
	s := New(config.Config{}, hub.New(logger), nil, nil, nil, nil, nil, nil, logger)
	s.SetWebPush(webpush.NewNotifier(st, nil, logger))
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()
	if code, body := pushReq(t, srv, http.MethodPost, "/webpush/enabled", `{"enabled":true}`); code != 200 {
		t.Fatalf("enable %d %s", code, body)
	}
	pub, _, err := webpush.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	auth := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	// Closed port on the allowed http loopback exception. The dial fails;
	// the client must not see the dial error.
	body := fmt.Sprintf(`{"endpoint":"http://127.0.0.1:1/push","p256dh":%q,"auth":%q}`, pub, auth)
	if code, got := pushReq(t, srv, http.MethodPost, "/webpush/subscribe", body); code != http.StatusNoContent {
		t.Fatalf("subscribe %d %s", code, got)
	}
	code, got := pushReq(t, srv, http.MethodPost, "/webpush/test", "")
	if code != http.StatusBadGateway {
		t.Fatalf("test %d %s", code, got)
	}
	if !strings.Contains(got, "push was not delivered") || strings.Contains(got, "connection refused") || strings.Contains(got, "127.0.0.1") {
		t.Fatalf("upstream error leaked: %s", got)
	}
}

func pushReqHeader(t *testing.T, srv *httptest.Server, method, path, body, endpoint string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", srv.URL)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-WebPush-Endpoint", endpoint)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func pushReq(t *testing.T, srv *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", srv.URL)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
