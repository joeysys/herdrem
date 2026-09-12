package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"herdrem/internal/herdr"
	"herdrem/internal/hub"
	"herdrem/internal/store"
)

func setupTestService(t *testing.T) (*Service, *httptest.Server) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	client := &herdr.FakeClient{}

	tmpDir := t.TempDir()
	todoStore, err := store.New(tmpDir + "/todo.json")
	if err != nil {
		t.Fatal(err)
	}

	svc := New(client, nil, todoStore, nil, nil, h, logger)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go h.Run(ctx)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Fatal(err)
		}
		svc.HandleMessage(ctx, conn, nil) // won't be called directly in this test
	}))
	t.Cleanup(srv.Close)

	return svc, srv
}

func TestHandleMessage_ReadPane(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	client := &herdr.FakeClient{}
	svc := New(client, nil, nil, nil, nil, h, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go h.Run(ctx)

	// Create a real WS connection
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		// Read one message and dispatch
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		svc.HandleMessage(ctx, conn, data)
		// Keep conn open briefly for client to read response
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Send read_pane
	msg := `{"type":"read_pane","pane_id":"demo:0.1"}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		t.Fatal(err)
	}

	// Read response
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var reply map[string]interface{}
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatal(err)
	}
	if reply["type"] != "pane_content" {
		t.Errorf("expected pane_content, got %v", reply["type"])
	}
	if reply["pane_id"] != "demo:0.1" {
		t.Errorf("expected pane_id demo:0.1, got %v", reply["pane_id"])
	}
}

func TestHandleMessage_TodoSetAndGet(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	client := &herdr.FakeClient{}

	tmpDir := t.TempDir()
	todoStore, err := store.New(tmpDir + "/todo.json")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(client, nil, todoStore, nil, nil, h, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go h.Run(ctx)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		// Handle multiple messages
		for i := 0; i < 4; i++ {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			svc.HandleMessage(ctx, conn, data)
		}
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// todo_set
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"todo_set","body":"test value","binding":{"host":"local","workspace_id":"ws1","workspace_label":"demo"}}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var updated map[string]interface{}
	json.Unmarshal(data, &updated)
	if updated["type"] != "todo_updated" {
		t.Errorf("expected todo_updated, got %v", updated["type"])
	}
	todo := updated["todo"].(map[string]interface{})
	id, _ := todo["id"].(string)
	if id == "" {
		t.Error("expected server-generated todo id")
	}

	// Rebind without changing identity.
	rebind := `{"type":"todo_set","id":"` + id + `","body":"test value","binding":{"host":"local","workspace_id":"ws2"}}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(rebind)); err != nil {
		t.Fatal(err)
	}
	_, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(data, &updated)
	rebound := updated["todo"].(map[string]interface{})
	if rebound["id"] != id || rebound["created_at"] != todo["created_at"] {
		t.Errorf("rebind changed identity: %#v", rebound)
	}

	// Delete by stable ID.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"todo_set","id":"`+id+`","body":""}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(data, &updated)
	if updated["deleted"] != true {
		t.Errorf("expected deleted update, got %#v", updated)
	}

	// todo_get
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"todo_get"}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var snap map[string]interface{}
	json.Unmarshal(data, &snap)
	if snap["type"] != "todos" {
		t.Errorf("expected todos, got %v", snap["type"])
	}
	todos := snap["todos"].([]interface{})
	if len(todos) != 0 {
		t.Errorf("expected empty snapshot after delete, got %#v", todos)
	}
}

func TestHandleMessage_UnknownType(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	client := &herdr.FakeClient{}
	svc := New(client, nil, nil, nil, nil, h, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go h.Run(ctx)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		svc.HandleMessage(ctx, conn, data)
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"bogus"}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var reply map[string]interface{}
	json.Unmarshal(data, &reply)
	if reply["type"] != "error" {
		t.Errorf("expected error, got %v", reply["type"])
	}
	if reply["op"] != "bogus" {
		t.Errorf("expected op=bogus, got %v", reply["op"])
	}
}

func TestAllowedHost(t *testing.T) {
	s := &Service{}
	// No allowlist configured → only local/empty allowed.
	if !s.allowedHost("") {
		t.Error("empty host should be allowed (local)")
	}
	if !s.allowedHost("local") {
		t.Error("local should be allowed")
	}
	if s.allowedHost("evil.example.com") {
		t.Error("unconfigured host must be rejected when no allowlist is set")
	}

	// With allowlist: only listed remotes allowed.
	s.SetAllowedHosts([]string{"build-server", "relay-2"})
	if !s.allowedHost("build-server") {
		t.Error("configured remote should be allowed")
	}
	if s.allowedHost("evil.example.com") {
		t.Error("non-listed remote must be rejected")
	}
	if s.allowedHost("local") == false {
		t.Error("local must always be allowed")
	}
}

func TestAllowedHostRejectsOptionLikeHost(t *testing.T) {
	s := &Service{}
	s.SetAllowedHosts([]string{"build-server"})
	// host starting with '-' would reach ssh argv as an option; the service
	// must reject it even if it is listed (defense in depth).
	if s.allowedHost("-oProxyCommand=x") {
		t.Error("option-like host must be rejected")
	}
}

func TestNormalizePaneMode(t *testing.T) {
	// Regression for the mode-echo fix: an empty client mode must be
	// normalized to "shell" so the PaneCreated echo is concrete (matches the
	// TS union and docs) instead of an empty string.
	if got := normalizePaneMode(""); got != "shell" {
		t.Errorf("normalizePaneMode(empty) = %q, want shell", got)
	}
	if got := normalizePaneMode("workspace"); got != "workspace" {
		t.Errorf("normalizePaneMode(workspace) = %q", got)
	}
	if got := normalizePaneMode("tab"); got != "tab" {
		t.Errorf("normalizePaneMode(tab) = %q", got)
	}
	if got := normalizePaneMode("shell"); got != "shell" {
		t.Errorf("normalizePaneMode(shell) = %q", got)
	}
}

// fakeSubscriber implements ContentSubscriber with a controllable snapshot so
// handleReadPane's cache-vs-refresh decision can be tested without importing
// the poll package (which would create a test import cycle).
type fakeSubscriber struct {
	content string
	rev     int
	ok      bool
}

func (f *fakeSubscriber) Subscribe(paneID string) {}
func (f *fakeSubscriber) ContentSnapshot(paneID string) (string, int, bool) {
	return f.content, f.rev, f.ok
}
func (f *fakeSubscriber) PreviewSnapshot(ctx context.Context, paneID string, lines int) (string, error) {
	return TailLines(f.content, lines), nil
}
func (f *fakeSubscriber) InvalidateMenu(paneID string) {}

// Regression for the "Refresh shows yesterday's content" bug: a refresh
// read_pane must bypass the poller's cached snapshot even when a fresh cache
// exists, so the client always gets authoritative herdr content.
func TestHandleReadPaneRefreshBypassesCache(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go h.Run(ctx)

	// Fake client whose ReadPane returns authoritative content.
	client := &herdr.FakeClient{ReadContent: "fresh-from-herdr\n"}
	// Subscriber returns a DIFFERENT cached snapshot.
	sub := &fakeSubscriber{content: "STALE-CACHED\n", rev: 1, ok: true}

	svc := New(client, nil, nil, nil, nil, h, logger)
	svc.SetContentSubscriber(sub)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		// Handle up to 2 messages (ordinary read + refresh read).
		for i := 0; i < 2; i++ {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			svc.HandleMessage(ctx, conn, data)
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Ordinary read: serves the subscriber's cached snapshot.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"read_pane","pane_id":"demo:0.1","purpose":"term"}`)); err != nil {
		t.Fatal(err)
	}
	_, data, _ := conn.Read(ctx)
	var reply map[string]interface{}
	_ = json.Unmarshal(data, &reply)
	if reply["type"] != "pane_content" {
		t.Fatalf("expected pane_content, got %v", reply)
	}
	if !strings.Contains(reply["content"].(string), "STALE-CACHED") {
		t.Fatalf("ordinary read should serve cache, got %v", reply["content"])
	}

	// Refresh read: bypasses the cache and reads herdr directly.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"read_pane","pane_id":"demo:0.1","purpose":"term","refresh":true}`)); err != nil {
		t.Fatal(err)
	}
	_, data2, _ := conn.Read(ctx)
	var reply2 map[string]interface{}
	_ = json.Unmarshal(data2, &reply2)
	if reply2["type"] != "pane_content" {
		t.Fatalf("refresh expected pane_content, got %v", reply2)
	}
	if !strings.Contains(reply2["content"].(string), "fresh-from-herdr") {
		t.Fatalf("refresh read should bypass cache, got %v", reply2["content"])
	}
}

func TestIsNumericChoice(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1", true},
		{"2", true},
		{"9", true},
		{"10", true},
		{" 1 \n", true},
		{"", false},
		{"   ", false},
		{"y", false},
		{"n", false},
		{"1a", false},
		{"a1", false},
	}
	for _, tt := range tests {
		if got := isNumericChoice(tt.input); got != tt.want {
			t.Errorf("isNumericChoice(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestHandleMessage_Respond(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	client := &herdr.FakeClient{}
	svc := New(client, nil, nil, nil, nil, h, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go h.Run(ctx)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			svc.HandleMessage(ctx, conn, data)
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	waitForSentText := func(want string) {
		t.Helper()
		deadline := time.NewTimer(time.Second)
		ticker := time.NewTicker(time.Millisecond)
		defer deadline.Stop()
		defer ticker.Stop()
		for {
			if got := client.SentText(); got == want {
				return
			}
			select {
			case <-ticker.C:
			case <-deadline.C:
				t.Fatalf("expected LastSentText to be %q, got %q", want, client.SentText())
			}
		}
	}

	// 1. Numeric choice: should NOT append \n so interactive TUI select doesn't leak \n to next input
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"respond","pane_id":"demo:0.1","text":"1"}`)); err != nil {
		t.Fatal(err)
	}
	waitForSentText("1")

	// 2. Numeric choice with surrounding whitespace: should send only the digits
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"respond","pane_id":"demo:0.1","text":" 2 \n"}`)); err != nil {
		t.Fatal(err)
	}
	waitForSentText("2")

	// 3. Non-numeric choice (e.g. "y"): should append \n for line-buffered shell prompts
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"respond","pane_id":"demo:0.1","text":"y"}`)); err != nil {
		t.Fatal(err)
	}
	waitForSentText("y\n")

	// 4. Shell approval explicitly requests Enter even for numeric labels.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"respond","pane_id":"demo:0.1","text":"3","submit":true}`)); err != nil {
		t.Fatal(err)
	}
	waitForSentText("3\n")
}

// Regression for "terminal freezes while an agent streams": a term poll read
// must bypass the versioned baseline. herdr's pane revision and
// state_change_seq only move on metadata/state changes — not on streamed
// output — so serving the confirmed baseline with a rev makes the client
// skip its merge+repaint path and the view freezes until the next state
// transition. Poll reads return fresh unversioned tails instead.
func TestHandleReadPanePollBypassesCache(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go h.Run(ctx)

	client := &herdr.FakeClient{ReadContent: "fresh-from-herdr\n"}
	sub := &fakeSubscriber{content: "STALE-CACHED\n", rev: 1, ok: true}

	svc := New(client, nil, nil, nil, nil, h, logger)
	svc.SetContentSubscriber(sub)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		svc.HandleMessage(ctx, conn, data)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"read_pane","pane_id":"demo:0.1","purpose":"term","poll":true}`)); err != nil {
		t.Fatal(err)
	}
	_, data, _ := conn.Read(ctx)
	var reply map[string]interface{}
	_ = json.Unmarshal(data, &reply)
	if reply["type"] != "pane_content" {
		t.Fatalf("expected pane_content, got %v", reply)
	}
	if !strings.Contains(reply["content"].(string), "fresh-from-herdr") {
		t.Fatalf("poll read should bypass cache and read directly, got %v", reply["content"])
	}
	if _, hasRev := reply["rev"]; hasRev {
		t.Fatalf("poll response must be unversioned so the client merges the tail, got rev=%v", reply["rev"])
	}
}
