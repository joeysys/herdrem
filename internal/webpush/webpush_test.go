package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"herdrem/internal/herdr"
	"herdrem/internal/store"
)

func TestRFC8291AppendixA(t *testing.T) {
	// RFC 8291 Appendix A. derive is checked against the published ECDH
	// secret, content-encryption key, and nonce — a round trip through
	// this package cannot see a symmetric HKDF mistake.
	asPriv, err := decodeB64("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw")
	if err != nil {
		t.Fatal(err)
	}
	uaPub, err := decodeB64("BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	if err != nil {
		t.Fatal(err)
	}
	asPub, err := decodeB64("BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8")
	if err != nil {
		t.Fatal(err)
	}
	auth, err := decodeB64("BTBZMqHH6r4Tts7J_aSIgg")
	if err != nil {
		t.Fatal(err)
	}
	salt, err := decodeB64("DGv6ra1nlYgDCS1FRnbzlw")
	if err != nil {
		t.Fatal(err)
	}
	asKey, err := ecdh.P256().NewPrivateKey(asPriv)
	if err != nil {
		t.Fatal(err)
	}
	uaKey, err := ecdh.P256().NewPublicKey(uaPub)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := asKey.ECDH(uaKey)
	if err != nil {
		t.Fatal(err)
	}
	if b64(shared) != "kyrL1jIIOHEzg3sM2ZWRHDRB62YACZhhSlknJ672kSs" {
		t.Fatalf("ecdh_secret = %s", b64(shared))
	}
	cek, nonce, err := derive(shared, auth, salt, uaPub, asPub)
	if err != nil {
		t.Fatal(err)
	}
	if b64(cek) != "oIhVW04MRdy2XN9CiKLxTg" {
		t.Fatalf("cek = %s", b64(cek))
	}
	if b64(nonce) != "4h_95klXJ5E_qnoN" {
		t.Fatalf("nonce = %s", b64(nonce))
	}
}

func TestEncryptRoundTrip(t *testing.T) {
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := []byte("0123456789abcdef")
	body, err := encrypt([]byte(`{"title":"n","body":"needs input"}`), ua.PublicKey().Bytes(), auth)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decrypt(body, ua.Bytes(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != `{"title":"n","body":"needs input"}` {
		t.Fatalf("plain = %s", plain)
	}
}

func TestVAPIDHeaderVerifies(t *testing.T) {
	pub, priv, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	h, err := vapidHeader(priv, pub, "https://push.example", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "vapid t=") || !strings.Contains(h, ",k="+pub) {
		t.Fatalf("header = %s", h)
	}
	token := strings.TrimPrefix(strings.Split(h, ",")[0], "vapid t=")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token = %s", token)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("sig: %v len %d", err, len(sig))
	}
	key, err := vapidKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(&key.PublicKey, sum[:], r, s) {
		t.Fatal("signature did not verify")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !strings.Contains(string(payload), "https://push.example") {
		t.Fatalf("payload = %s", payload)
	}
}

func TestOriginStable(t *testing.T) {
	ok, _ := OriginStable("http", "localhost:8766")
	if !ok {
		t.Fatal("localhost should be stable")
	}
	ok, _ = OriginStable("http", "127.0.0.1:8766")
	if !ok {
		t.Fatal("127.0.0.1 should be stable")
	}
	ok, reason := OriginStable("https", "abc.trycloudflare.com")
	if ok || !strings.Contains(reason, "changes") {
		t.Fatalf("quick tunnel stable=%v reason=%q", ok, reason)
	}
	ok, reason = OriginStable("https", "203.0.113.5")
	if ok || !strings.Contains(reason, "IP") {
		t.Fatalf("ip stable=%v reason=%q", ok, reason)
	}
	ok, reason = OriginStable("http", "box.example")
	if ok || !strings.Contains(reason, "https") {
		t.Fatalf("http name stable=%v reason=%q", ok, reason)
	}
	ok, reason = OriginStable("https", "box.ts.net")
	if !ok {
		t.Fatalf("ts.net rejected: %s", reason)
	}
}

func TestTopicHashesUnsafePaneID(t *testing.T) {
	if topicFor("herdrem-test") != "herdrem-test" {
		t.Fatal("safe tag was rewritten")
	}
	got := topicFor("demo:0.1")
	if got == "demo:0.1" || len(got) != 32 {
		t.Fatalf("topic = %q", got)
	}
}

type pushHit struct {
	body    []byte
	urgency string
	ttl     string
	topic   string
	authz   string
}

func TestBlockedPushConfirmsAndOmitsTerminalText(t *testing.T) {
	ua, auth, hits, srv := pushServer(t, http.StatusCreated)
	defer srv.Close()
	n := testNotifier(t, srv.URL+"/p", ua, auth, true, "long")
	n.delay = 20 * time.Millisecond
	var status atomic.Value
	status.Store("blocked")
	n.lookup = func(string) (herdr.Pane, bool) {
		return herdr.Pane{
			PaneID: "demo:0.1", Project: "herdrem", Status: status.Load().(string),
			Prompt: "SECRET TERMINAL LINE",
		}, true
	}
	n.OnTransition(herdr.Pane{PaneID: "demo:0.1", Status: "blocked"}, "working", "blocked")
	status.Store("idle")
	time.Sleep(60 * time.Millisecond)
	if len(hits()) != 0 {
		t.Fatal("cleared pane was still pushed")
	}
	status.Store("blocked")
	n.OnTransition(herdr.Pane{PaneID: "demo:0.1", Status: "blocked"}, "working", "blocked")
	waitHits(t, hits, 1)
	got := hits()[0]
	if got.urgency != "high" || got.ttl != "43200" {
		t.Fatalf("urgency/ttl = %s %s", got.urgency, got.ttl)
	}
	if got.topic == "demo:0.1" || len(got.topic) != 32 {
		t.Fatalf("topic = %q", got.topic)
	}
	if !strings.HasPrefix(got.authz, "vapid ") {
		t.Fatalf("auth = %s", got.authz)
	}
	plain, err := decrypt(got.body, ua.Bytes(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "SECRET") {
		t.Fatalf("terminal text in push body: %s", plain)
	}
	var fields map[string]any
	if err := json.Unmarshal(plain, &fields); err != nil {
		t.Fatal(err)
	}
	for k := range fields {
		switch k {
		case "title", "body", "tag", "paneId":
		default:
			t.Fatalf("unexpected field %s", k)
		}
	}
	if fields["paneId"] != "demo:0.1" || fields["tag"] != "demo:0.1" {
		t.Fatalf("note = %s", plain)
	}
	if !strings.Contains(fields["body"].(string), "needs input") {
		t.Fatalf("body = %v", fields["body"])
	}
}

func TestDoneLongKeepsClockAcrossBlocked(t *testing.T) {
	ua, auth, hits, srv := pushServer(t, http.StatusCreated)
	defer srv.Close()
	n := testNotifier(t, srv.URL+"/p", ua, auth, false, "long")
	n.delay = 15 * time.Millisecond
	start := time.Now()
	var status atomic.Value
	status.Store("working")
	n.lookup = func(string) (herdr.Pane, bool) {
		return herdr.Pane{PaneID: "p", Project: "app", Status: status.Load().(string)}, true
	}
	n.now = func() time.Time { return start }
	n.OnTransition(herdr.Pane{PaneID: "p"}, "idle", "working")
	n.now = func() time.Time { return start.Add(10 * time.Minute) }
	status.Store("blocked")
	n.OnTransition(herdr.Pane{PaneID: "p"}, "working", "blocked")
	n.now = func() time.Time { return start.Add(10*time.Minute + 30*time.Second) }
	status.Store("working")
	n.OnTransition(herdr.Pane{PaneID: "p"}, "blocked", "working")
	n.now = func() time.Time { return start.Add(10*time.Minute + 50*time.Second) }
	status.Store("done")
	n.OnTransition(herdr.Pane{PaneID: "p"}, "working", "done")
	waitHits(t, hits, 1)
	plain, err := decrypt(hits()[0].body, ua.Bytes(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plain), "finished a round") {
		t.Fatalf("plain = %s", plain)
	}
}

func TestDoneLongSkipsShortRounds(t *testing.T) {
	ua, auth, hits, srv := pushServer(t, http.StatusCreated)
	defer srv.Close()
	n := testNotifier(t, srv.URL+"/p", ua, auth, true, "long")
	n.delay = 15 * time.Millisecond
	start := time.Now()
	n.now = func() time.Time { return start }
	n.lookup = func(string) (herdr.Pane, bool) {
		return herdr.Pane{PaneID: "p", Project: "app", Status: "done"}, true
	}
	n.OnTransition(herdr.Pane{PaneID: "p", Status: "working"}, "idle", "working")
	n.now = func() time.Time { return start.Add(30 * time.Second) }
	n.OnTransition(herdr.Pane{PaneID: "p", Status: "done"}, "working", "done")
	time.Sleep(50 * time.Millisecond)
	if len(hits()) != 0 {
		t.Fatal("short round was pushed")
	}

	n.now = func() time.Time { return start }
	n.OnTransition(herdr.Pane{PaneID: "p", Status: "working"}, "idle", "working")
	n.now = func() time.Time { return start.Add(3 * time.Minute) }
	n.OnTransition(herdr.Pane{PaneID: "p", Status: "done"}, "working", "done")
	waitHits(t, hits, 1)
	plain, err := decrypt(hits()[0].body, ua.Bytes(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if hits()[0].urgency != "normal" {
		t.Fatalf("urgency = %s", hits()[0].urgency)
	}
	if !strings.Contains(string(plain), "finished a round") {
		t.Fatalf("plain = %s", plain)
	}
}

func TestPushServiceGoneDeletesSubscription(t *testing.T) {
	ua, auth, _, srv := pushServer(t, http.StatusGone)
	defer srv.Close()
	n := testNotifier(t, srv.URL+"/p", ua, auth, true, "always")
	if err := n.SendTest(context.Background()); err == nil {
		t.Fatal("expected push failure")
	}
	if subs := n.store.Subscriptions(); len(subs) != 0 {
		t.Fatalf("subscription kept after 410: %+v", subs)
	}
}

func TestPushDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/landed", http.StatusFound)
	}))
	defer srv.Close()
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := []byte("0123456789abcdef")
	n := testNotifier(t, srv.URL+"/p", ua, auth, true, "always")
	if err := n.SendTest(context.Background()); err == nil {
		t.Fatal("redirect was treated as success")
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, redirect was followed", hits.Load())
	}
	if len(n.store.Subscriptions()) != 1 {
		t.Fatal("redirect deleted the subscription")
	}
}

func TestDisabledNotifierDoesNotSend(t *testing.T) {
	ua, auth, hits, srv := pushServer(t, http.StatusCreated)
	defer srv.Close()
	n := testNotifier(t, srv.URL+"/p", ua, auth, true, "always")
	if err := n.store.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	n.delay = 10 * time.Millisecond
	n.lookup = func(string) (herdr.Pane, bool) {
		return herdr.Pane{PaneID: "p", Status: "blocked"}, true
	}
	n.OnTransition(herdr.Pane{PaneID: "p"}, "working", "blocked")
	time.Sleep(40 * time.Millisecond)
	if len(hits()) != 0 {
		t.Fatal("disabled notifier sent")
	}
}

func pushServer(t *testing.T, code int) (*ecdh.PrivateKey, []byte, func() []pushHit, *httptest.Server) {
	t.Helper()
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := []byte("0123456789abcdef")
	var mu sync.Mutex
	var items []pushHit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		items = append(items, pushHit{
			body: b, urgency: r.Header.Get("Urgency"), ttl: r.Header.Get("TTL"),
			topic: r.Header.Get("Topic"), authz: r.Header.Get("Authorization"),
		})
		mu.Unlock()
		w.WriteHeader(code)
	}))
	return ua, auth, func() []pushHit {
		mu.Lock()
		defer mu.Unlock()
		out := make([]pushHit, len(items))
		copy(out, items)
		return out
	}, srv
}

func testNotifier(t *testing.T, endpoint string, ua *ecdh.PrivateKey, auth []byte, input bool, done string) *Notifier {
	t.Helper()
	st, err := store.OpenWebPush(t.TempDir() + "/webpush.json")
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetKeys(pub, priv); err != nil {
		t.Fatal(err)
	}
	if err := st.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := st.Upsert(store.PushSub{
		Endpoint: endpoint,
		P256DH:   b64(ua.PublicKey().Bytes()),
		Auth:     b64(auth),
		Input:    input,
		Done:     done,
	}); err != nil {
		t.Fatal(err)
	}
	n := NewNotifier(st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return n
}

func TestSendGivesEachSubscriptionItsOwnDeadline(t *testing.T) {
	var fastAt atomic.Int64
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fastAt.Store(time.Now().UnixNano())
		w.WriteHeader(http.StatusCreated)
	}))
	defer fast.Close()
	release := make(chan struct{})
	free := sync.OnceFunc(func() { close(release) })
	defer free()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer slow.Close()
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := []byte("0123456789abcdef")
	n := testNotifier(t, slow.URL+"/p", ua, auth, true, "always")
	if err := n.store.Upsert(store.PushSub{
		Endpoint: fast.URL + "/p",
		P256DH:   b64(ua.PublicKey().Bytes()),
		Auth:     b64(auth),
		Input:    true,
		Done:     "always",
	}); err != nil {
		t.Fatal(err)
	}
	n.postTimeout = 300 * time.Millisecond
	start := time.Now()
	done := make(chan struct{})
	go func() {
		n.send(context.Background(), "blocked", herdr.Pane{PaneID: "p", Project: "app"}, 0, false)
		close(done)
	}()
	deadline := time.Now().Add(150 * time.Millisecond)
	for fastAt.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fastAt.Load() == 0 {
		t.Fatal("fast subscription waited behind the slow endpoint")
	}
	if waited := time.Duration(fastAt.Load() - start.UnixNano()); waited > 150*time.Millisecond {
		t.Fatalf("fast hit took %s", waited)
	}
	free()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("send did not return after the slow endpoint timed out")
	}
}

func TestPushDialRejectsPrivateAndLoopback(t *testing.T) {
	dialer := &net.Dialer{Timeout: time.Second}
	deny := []struct {
		scheme, host, addr string
	}{
		{"https", "10.1.2.3", "10.1.2.3:443"},
		{"https", "127.0.0.1", "127.0.0.1:9"},
		{"https", "169.254.169.254", "169.254.169.254:443"},
		{"https", "192.168.0.8", "192.168.0.8:443"},
	}
	for _, tc := range deny {
		ctx := context.WithValue(context.Background(), pushDialTarget{}, pushDialTarget{scheme: tc.scheme, host: tc.host})
		_, err := dialPush(ctx, dialer, "tcp", tc.addr)
		if err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("%s %s: %v", tc.scheme, tc.addr, err)
		}
	}
	ctx := context.WithValue(context.Background(), pushDialTarget{}, pushDialTarget{scheme: "http", host: "127.0.0.1"})
	_, err := dialPush(ctx, dialer, "tcp", "127.0.0.1:1")
	if err != nil && strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("http loopback exception was refused: %v", err)
	}
}

func TestDialPushUsesCheckedAddressNotASecondLookup(t *testing.T) {
	var calls atomic.Int32
	orig := lookupPushIP
	lookupPushIP = func(context.Context, string) ([]net.IPAddr, error) {
		n := calls.Add(1)
		if n == 1 {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	t.Cleanup(func() { lookupPushIP = orig })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		c, acceptErr := ln.Accept()
		if acceptErr == nil {
			c.Close()
			accepted <- struct{}{}
		}
	}()

	var dialed string
	dialer := &net.Dialer{
		Timeout: 200 * time.Millisecond,
		ControlContext: func(_ context.Context, _, address string, _ syscall.RawConn) error {
			dialed = address
			return errors.New("stopped before connect")
		},
	}
	ctx := context.WithValue(context.Background(), pushDialTarget{}, pushDialTarget{scheme: "https", host: "rebind.test"})
	_, err = dialPush(ctx, dialer, "tcp", "rebind.test:443")
	if err == nil || !strings.Contains(err.Error(), "stopped before connect") {
		t.Fatalf("dialPush err=%v, want the pinned dial to stop before connect", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("lookups=%d, want 1", calls.Load())
	}
	if dialed != "203.0.113.10:443" {
		t.Fatalf("dialed %q, want the address from the first lookup", dialed)
	}
	select {
	case <-accepted:
		t.Fatal("loopback listener accepted a connection")
	default:
	}
}

func waitHits(t *testing.T, hits func() []pushHit, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(hits()) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("got %d pushes, want %d", len(hits()), n)
}
