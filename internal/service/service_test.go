package service

import (
	"context"
	"encoding/json"
	"errors"
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

func TestWorktreeCreateReplaysOneOperation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := &herdr.FakeClient{}
	svc := New(client, nil, nil, nil, nil, nil, logger)
	body := []byte(`{"type":"worktree_create","workspace_id":"ws1","host":"local","agent":"shell","operation_id":"op-1","boot_id":"` + svc.BootID() + `"}`)
	svc.handleWorktreeCreate(context.Background(), nil, body)
	svc.handleWorktreeCreate(context.Background(), nil, body)
	if client.WorktreeCreateCount() != 1 {
		t.Fatalf("creates = %d, want 1", client.WorktreeCreateCount())
	}
}

func TestStartAgentAcceptsSettledKindAfterMismatch(t *testing.T) {
	prev := agentKindSettleFor
	agentKindSettleFor = 0
	t.Cleanup(func() { agentKindSettleFor = prev })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := &herdr.FakeClient{
		AgentStartErr: errors.New(`{"error":{"code":"agent_kind_mismatch","message":"expected pi, detected kiro"},"id":"cli:agent:start"}`),
		Panes:         []herdr.Pane{{PaneID: "pane-new", Agent: "pi", Kind: "agent"}},
	}
	svc := New(client, nil, nil, nil, nil, nil, logger)
	if err := svc.startAgent(context.Background(), "local", "pane-new", "pi"); err != nil {
		t.Fatal(err)
	}
}

func TestStartAgentKeepsRealKindMismatch(t *testing.T) {
	prev := agentKindSettleFor
	agentKindSettleFor = 0
	t.Cleanup(func() { agentKindSettleFor = prev })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := &herdr.FakeClient{
		AgentStartErr: errors.New("expected pi, detected kiro"),
		Panes:         []herdr.Pane{{PaneID: "pane-new", Agent: "kiro", Kind: "agent"}},
	}
	svc := New(client, nil, nil, nil, nil, nil, logger)
	err := svc.startAgent(context.Background(), "local", "pane-new", "pi")
	if err == nil || !strings.Contains(err.Error(), "detected kiro") {
		t.Fatalf("err = %v, want the kind mismatch", err)
	}
}

func TestStartAgentDoesNotHideOtherStartErrors(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := &herdr.FakeClient{
		AgentStartErr: errors.New("timed out waiting for agent startup"),
		Panes:         []herdr.Pane{{PaneID: "pane-new", Agent: "pi", Kind: "agent"}},
	}
	svc := New(client, nil, nil, nil, nil, nil, logger)
	err := svc.startAgent(context.Background(), "local", "pane-new", "pi")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want the startup timeout", err)
	}
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
	content        string
	rev            int
	ok             bool
	agent          string
	polls          int
	observeContent string
	observeRev     int
	observeGen     int
	observeEpoch   int
	observeOK      bool
	widenContent   string
	widenRev       int
	widenGen       int
	widenEpoch     int
	widenTruncated bool
	widenOK        bool
	widenCalls     int
}

func (f *fakeSubscriber) Subscribe(paneID string) {}
func (f *fakeSubscriber) ContentSnapshot(paneID string) (string, int, bool) {
	return f.content, f.rev, f.ok
}
func (f *fakeSubscriber) PreviewSnapshot(ctx context.Context, paneID string, lines int) (string, error) {
	return TailLines(f.content, lines), nil
}
func (f *fakeSubscriber) InvalidateMenu(paneID string)   {}
func (f *fakeSubscriber) PaneAgent(paneID string) string { return f.agent }
func (f *fakeSubscriber) TriggerPoll()                   { f.polls++ }
func (f *fakeSubscriber) ObserveSnapshot(paneID string) (string, int, int, int, bool) {
	if !f.observeOK {
		return "", 0, 0, 0, false
	}
	return f.observeContent, f.observeRev, f.observeGen, f.observeEpoch, true
}
func (f *fakeSubscriber) WidenObserveBaseline(ctx context.Context, paneID string, lines int) (string, int, int, int, bool, bool) {
	f.widenCalls++
	if f.widenOK {
		return f.widenContent, f.widenRev, f.widenGen, f.widenEpoch, f.widenTruncated, true
	}
	if !f.observeOK {
		return "", 0, 0, 0, false, false
	}
	return f.observeContent, f.observeRev, f.observeGen, f.observeEpoch, false, true
}

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

func TestPollAndRefreshServeObserveSnapshot(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go h.Run(ctx)

	client := &herdr.FakeClient{ReadContent: "live-from-herdr\n"}
	sub := &fakeSubscriber{
		content: "STALE-CACHED\n", rev: 1, ok: true,
		observeContent: "cached-baseline\n", observeRev: 9, observeGen: 3, observeEpoch: 11, observeOK: true,
	}
	svc := New(client, nil, nil, nil, nil, h, logger)
	svc.SetContentSubscriber(sub)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for i := 0; i < 3; i++ {
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

	read := func() map[string]any {
		t.Helper()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"read_pane","pane_id":"demo:0.1","purpose":"term"}`)); err != nil {
		t.Fatal(err)
	}
	ordinary := read()
	if !strings.Contains(ordinary["content"].(string), "STALE-CACHED") || ordinary["rev"] != float64(1) {
		t.Fatalf("ordinary read = %v", ordinary)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"read_pane","pane_id":"demo:0.1","purpose":"term","poll":true}`)); err != nil {
		t.Fatal(err)
	}
	poll := read()
	if poll["content"] != "cached-baseline\n" || poll["rev"] != float64(9) || poll["gen"] != float64(3) || poll["epoch"] != float64(11) {
		t.Fatalf("observed poll = %v", poll)
	}
	if strings.Contains(poll["content"].(string), "live-from-herdr") {
		t.Fatalf("observed poll paired a direct read with the cached rev: %v", poll)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"read_pane","pane_id":"demo:0.1","purpose":"term","refresh":true}`)); err != nil {
		t.Fatal(err)
	}
	refresh := read()
	if refresh["content"] != "cached-baseline\n" || refresh["rev"] != float64(9) || refresh["gen"] != float64(3) || refresh["epoch"] != float64(11) {
		t.Fatalf("observed refresh = %v", refresh)
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

func TestHandleMessage_SendTextPaste(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := &herdr.FakeClient{}
	svc := New(client, nil, nil, nil, nil, hub.New(logger), logger)
	ctx := context.Background()

	cases := []struct{ name, msg, want string }{
		{"multi-line paste, submitted", `{"type":"send_text","pane_id":"p","text":"one\ntwo\r\nthree","paste":true,"submit":true}`, "\x1b[200~one\rtwo\rthree\x1b[201~\n"},
		{"multi-line paste, fill only", `{"type":"send_text","pane_id":"p","text":"a\nb","paste":true}`, "\x1b[200~a\rb\x1b[201~"},
		{"an end marker inside cannot close the paste", `{"type":"send_text","pane_id":"p","text":"a\u001b[201~\nb","paste":true}`, "\x1b[200~a\rb\x1b[201~"},
		{"single line stays plain even with paste", `{"type":"send_text","pane_id":"p","text":"ls","paste":true,"submit":true}`, "ls\n"},
		{"without paste nothing changes", `{"type":"send_text","pane_id":"p","text":"a\nb","submit":true}`, "a\nb\n"},
	}
	for _, c := range cases {
		svc.HandleMessage(ctx, nil, []byte(c.msg))
		if got := client.SentText(); got != c.want {
			t.Errorf("%s: sent %q, want %q", c.name, got, c.want)
		}
	}
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

// prefs_set carries a client-generated req_id; the relay must echo it on the
// prefs_updated broadcast and on errors so the client can correlate acks of
// multi-section saves.
func TestHandleMessage_PrefsSetEchoesReqID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	client := &herdr.FakeClient{}

	prefsStore, err := store.NewPrefsStore(t.TempDir() + "/prefs.json")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(client, nil, nil, prefsStore, nil, h, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go h.Run(ctx)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		// Production path: registers the conn so broadcasts reach it.
		h.HandleConn(ctx, conn, "", svc.HandleMessage)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	read := func() map[string]interface{} {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var reply map[string]interface{}
		if err := json.Unmarshal(data, &reply); err != nil {
			t.Fatal(err)
		}
		return reply
	}

	// Success path: prefs_updated echoes the request's req_id.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"prefs_set","section":"terminal","data":{"poll_ms":6000},"req_id":"save-1"}`)); err != nil {
		t.Fatal(err)
	}
	reply := read()
	if reply["type"] != "prefs_updated" {
		t.Fatalf("expected prefs_updated, got %v", reply)
	}
	if reply["req_id"] != "save-1" {
		t.Fatalf("expected req_id echo save-1, got %v", reply["req_id"])
	}

	// Failure path: the error echoes the req_id of the rejected request.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"prefs_set","section":"timing","data":{"snapshot_poll_ms":7},"req_id":"save-2"}`)); err != nil {
		t.Fatal(err)
	}
	reply = read()
	if reply["type"] != "error" || reply["op"] != "prefs_set" {
		t.Fatalf("expected prefs_set error, got %v", reply)
	}
	if reply["req_id"] != "save-2" {
		t.Fatalf("expected req_id echo save-2, got %v", reply["req_id"])
	}
}

func TestExpectTailCheckedBeforeSend(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := &herdr.FakeClient{ReadContent: "hello\n[sudo] password for testuser:\n"}
	svc := New(client, nil, nil, nil, nil, hub.New(logger), logger)
	ctx := context.Background()

	svc.HandleMessage(ctx, nil, []byte(`{"type":"send_text","pane_id":"p","text":"s3cret","submit":true,"expect_tail":"[sudo] password for testuser:"}`))
	if client.SendCount() != 1 || client.SentText() != "s3cret\n" {
		t.Fatalf("match: count=%d text=%q", client.SendCount(), client.SentText())
	}

	client.ReadContent = "$ "
	svc.HandleMessage(ctx, nil, []byte(`{"type":"send_text","pane_id":"p","text":"s3cret","submit":true,"expect_tail":"[sudo] password for testuser:"}`))
	if client.SendCount() != 1 {
		t.Fatalf("stale tail must not send, count=%d", client.SendCount())
	}

	svc.HandleMessage(ctx, nil, []byte(`{"type":"send_text","pane_id":"p","text":"ls","submit":true}`))
	if client.SendCount() != 2 || client.SentText() != "ls\n" {
		t.Fatalf("no expect_tail stays fire-and-forget, count=%d text=%q", client.SendCount(), client.SentText())
	}
}

func TestSendTextRequestIDAck(t *testing.T) {
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

	read := func() map[string]any {
		t.Helper()
		rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
		defer rcancel()
		_, data, err := conn.Read(rctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"send_text","pane_id":"p","text":"hi","submit":true,"request_id":"r1"}`)); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got["type"] != "sent" || got["op"] != "send_text" || got["request_id"] != "r1" {
		t.Fatalf("ack = %v", got)
	}
	if client.SentText() != "hi\n" {
		t.Fatalf("sent text = %q", client.SentText())
	}

	client.SendTextErr = errSent
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"send_text","pane_id":"p","text":"nope","submit":true,"request_id":"r2"}`)); err != nil {
		t.Fatal(err)
	}
	got = read()
	if got["type"] != "error" || got["op"] != "send_text" || got["request_id"] != "r2" {
		t.Fatalf("error ack = %v", got)
	}
}

func TestExpectTailSendFailureHidesText(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	client := &herdr.FakeClient{
		ReadContent: "[sudo] password for testuser:\n",
		SendTextErr: errString("herdr pane send-text p s3cret-hunter: boom"),
	}
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
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	body := `{"type":"send_text","pane_id":"p","text":"s3cret-hunter","submit":true,"request_id":"r1","expect_tail":"[sudo] password for testuser:"}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(body)); err != nil {
		t.Fatal(err)
	}
	rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
	defer rcancel()
	_, data, err := conn.Read(rctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s3cret-hunter") {
		t.Fatalf("reply leaked secret: %s", data)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["type"] != "error" || got["message"] != "Send failed, not delivered" {
		t.Fatalf("reply = %v", got)
	}
}

var errSent = errString("herdr send failed")

type errString string

func (e errString) Error() string { return string(e) }

func TestRespondPromptID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	screen := "Allow?\n1. Yes\n2. No\n✢ Tempering… (1s · ↓ 10 tokens)\n"
	client := &herdr.FakeClient{ReadContent: screen}
	sub := &fakeSubscriber{agent: "claude"}
	svc := New(client, nil, nil, nil, nil, h, logger)
	svc.SetContentSubscriber(sub)
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

	read := func() map[string]any {
		t.Helper()
		rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
		defer rcancel()
		_, data, err := conn.Read(rctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	id := herdr.PromptID(screen, "claude")
	// Only the working line's clock moved.
	client.ReadContent = "Allow?\n1. Yes\n2. No\n✻ Tempering… (8s · ↑ 1.2k tokens)\n"
	body := `{"type":"respond","pane_id":"p","text":"1","prompt_id":"` + id + `","request_id":"ok"}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(body)); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got["type"] != "sent" || got["request_id"] != "ok" {
		t.Fatalf("fresh card ack = %v", got)
	}
	if client.SentText() != "1" || client.SendCount() != 1 {
		t.Fatalf("sent %q count %d", client.SentText(), client.SendCount())
	}

	client.ReadContent = "Different prompt?\n1. Yes\n2. No\n"
	body = `{"type":"respond","pane_id":"p","text":"1","prompt_id":"` + id + `","request_id":"old"}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(body)); err != nil {
		t.Fatal(err)
	}
	got = read()
	if got["type"] != "error" || got["op"] != "respond" || got["key"] != "stale" || got["request_id"] != "old" {
		t.Fatalf("stale = %v", got)
	}
	if client.SendCount() != 1 {
		t.Fatalf("stale respond must not send keys, count=%d", client.SendCount())
	}
	if sub.polls != 1 {
		t.Fatalf("stale respond polls=%d, want 1", sub.polls)
	}

	// Old client: no prompt_id, still sends.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"respond","pane_id":"p","text":"2"}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(300 * time.Millisecond)
	defer deadline.Stop()
	for client.SendCount() != 2 {
		select {
		case <-deadline.C:
			t.Fatalf("old client count=%d text=%q", client.SendCount(), client.SentText())
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	if client.SentText() != "2" {
		t.Fatalf("old client sent %q", client.SentText())
	}
}

func TestSendKeysSequenceChecksPromptOnce(t *testing.T) {
	prev := keySequenceGap
	keySequenceGap = 0
	t.Cleanup(func() { keySequenceGap = prev })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	screen := "Allow?\n1. Yes\n2. No\n"
	client := &herdr.FakeClient{ReadContent: screen}
	sub := &fakeSubscriber{agent: "cursor"}
	svc := New(client, nil, nil, nil, nil, h, logger)
	svc.SetContentSubscriber(sub)
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

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	read := func() map[string]any {
		t.Helper()
		rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
		defer rcancel()
		_, data, err := conn.Read(rctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	id := herdr.PromptID(screen, "cursor")
	body := `{"type":"send_keys","pane_id":"p","sequence":["Down","Down","Enter"],"prompt_id":"` + id + `","request_id":"ok"}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(body)); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got["type"] != "sent" || got["op"] != "send_keys" || got["request_id"] != "ok" {
		t.Fatalf("sequence ack = %v", got)
	}
	if strings.Join(client.SentKeys(), ",") != "Down,Down,Enter" {
		t.Fatalf("keys = %v", client.SentKeys())
	}

	client.ReadContent = "Different prompt?\n1. Yes\n2. No\n"
	body = `{"type":"send_keys","pane_id":"p","sequence":["Enter"],"prompt_id":"` + id + `","request_id":"old"}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(body)); err != nil {
		t.Fatal(err)
	}
	got = read()
	if got["type"] != "error" || got["op"] != "send_keys" || got["key"] != "stale" || got["request_id"] != "old" {
		t.Fatalf("stale = %v", got)
	}
	if strings.Join(client.SentKeys(), ",") != "Down,Down,Enter" {
		t.Fatalf("stale sequence must not send keys, got %v", client.SentKeys())
	}
	if sub.polls != 1 {
		t.Fatalf("stale polls=%d, want 1", sub.polls)
	}

	client.SendKeysErr = errSent
	fresh := herdr.PromptID(client.ReadContent, "cursor")
	body = `{"type":"send_keys","pane_id":"p","sequence":["Down","Enter"],"prompt_id":"` + fresh + `","request_id":"fail"}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(body)); err != nil {
		t.Fatal(err)
	}
	got = read()
	if got["type"] != "error" || got["request_id"] != "fail" {
		t.Fatalf("mid-sequence = %v", got)
	}
	if strings.Join(client.SentKeys(), ",") != "Down,Down,Enter,Down" {
		t.Fatalf("must stop after the failing key, got %v", client.SentKeys())
	}
}

func TestRefreshWidensShortObserveCache(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go h.Run(ctx)

	client := &herdr.FakeClient{ReadContent: "LIVE\n"}
	sub := &fakeSubscriber{
		observeContent: "short\n", observeRev: 9, observeGen: 3, observeEpoch: 11, observeOK: true,
		widenContent: "one\ntwo\nthree\n", widenRev: 9, widenGen: 4, widenEpoch: 11,
		widenTruncated: true, widenOK: true,
	}
	svc := New(client, nil, nil, nil, nil, h, logger)
	svc.SetContentSubscriber(sub)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for i := 0; i < 2; i++ {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			svc.HandleMessage(ctx, conn, data)
		}
	}))
	defer srv.Close()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	read := func() map[string]any {
		t.Helper()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"read_pane","pane_id":"p","purpose":"term","poll":true,"lines":80}`)); err != nil {
		t.Fatal(err)
	}
	poll := read()
	if poll["content"] != "short\n" || sub.widenCalls != 0 {
		t.Fatalf("poll must keep the short tail and not widen: %v calls=%d", poll, sub.widenCalls)
	}
	if _, ok := poll["truncated"]; ok {
		t.Fatalf("poll truncated = %v", poll["truncated"])
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"read_pane","pane_id":"p","purpose":"term","refresh":true,"lines":80}`)); err != nil {
		t.Fatal(err)
	}
	refresh := read()
	if refresh["content"] != "one\ntwo\nthree\n" || refresh["gen"] != float64(4) || refresh["rev"] != float64(9) || refresh["truncated"] != true {
		t.Fatalf("refresh = %v", refresh)
	}
	if strings.Contains(refresh["content"].(string), "LIVE") {
		t.Fatalf("refresh served a direct read: %v", refresh["content"])
	}
	if sub.widenCalls != 1 {
		t.Fatalf("widen calls = %d", sub.widenCalls)
	}
}

func TestHandleMessage_ReqEcho(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	client := &herdr.FakeClient{}

	tmpDir := t.TempDir()
	todoStore, err := store.New(tmpDir + "/todo.json")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(client, nil, todoStore, nil, nil, h, logger)
	svc.SetTunnels(NewTunnelService(0, nil, logger))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go h.Run(ctx)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		if r.URL.Query().Get("bystander") == "1" {
			// Registered to the hub so BroadcastJSONExcept reaches it.
			h.HandleConn(ctx, conn, "", nil)
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
	bystander, _, err := websocket.Dial(ctx, wsURL+"?bystander=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bystander.Close(websocket.StatusNormalClosure, "")
	for i := 0; i < 50 && h.Count() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if h.Count() == 0 {
		t.Fatal("bystander never registered")
	}

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	readMsg := func(c *websocket.Conn) map[string]interface{} {
		t.Helper()
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	// Create: the initiator's copy echoes req_id; the broadcast must not.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"todo_set","req_id":"r-create","body":"echo me","binding":{"host":"local","workspace_id":"ws1"}}`)); err != nil {
		t.Fatal(err)
	}
	m := readMsg(conn)
	if m["type"] != "todo_updated" || m["req_id"] != "r-create" {
		t.Fatalf("initiator update missing req_id echo: %#v", m)
	}
	id := m["todo"].(map[string]interface{})["id"].(string)
	bm := readMsg(bystander)
	if bm["type"] != "todo_updated" {
		t.Fatalf("bystander missed broadcast: %#v", bm)
	}
	if rid, present := bm["req_id"]; present && rid != "" {
		t.Fatalf("broadcast leaked req_id %v", rid)
	}

	// Delete: same echo rules.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"todo_set","req_id":"r-del","id":"`+id+`","body":""}`)); err != nil {
		t.Fatal(err)
	}
	m = readMsg(conn)
	if m["req_id"] != "r-del" || m["deleted"] != true {
		t.Fatalf("delete echo wrong: %#v", m)
	}
	bm = readMsg(bystander)
	if rid, present := bm["req_id"]; present && rid != "" {
		t.Fatalf("delete broadcast leaked req_id %v", rid)
	}

	// Error: validation failures echo req_id to the initiator.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"todo_set","req_id":"r-err","id":"missing","body":""}`)); err != nil {
		t.Fatal(err)
	}
	m = readMsg(conn)
	if m["type"] != "error" || m["op"] != "todo_set" || m["req_id"] != "r-err" {
		t.Fatalf("todo error did not echo req_id: %#v", m)
	}

	// tunnel_create: failures echo req_id the same way.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"tunnel_create","req_id":"r-tun","port":99999,"auth_mode":"token_gate"}`)); err != nil {
		t.Fatal(err)
	}
	m = readMsg(conn)
	if m["type"] != "error" || m["op"] != "tunnel_create" || m["req_id"] != "r-tun" {
		t.Fatalf("tunnel error did not echo req_id: %#v", m)
	}
}
