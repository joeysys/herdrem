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

// historyRig is a relay with the real hub, so broadcasts reach the other
// connected "devices" exactly as in production.
type historyRig struct {
	t     *testing.T
	ctx   context.Context
	url   string
	store *store.HistoryStore
}

func newHistoryRig(t *testing.T, withStore bool) *historyRig {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	svc := New(&herdr.FakeClient{}, nil, nil, nil, nil, h, logger)
	rig := &historyRig{t: t}
	if withStore {
		hs, err := store.NewHistoryStore(t.TempDir() + "/history.json")
		if err != nil {
			t.Fatal(err)
		}
		svc.SetHistory(hs)
		rig.store = hs
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	go h.Run(ctx)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		h.HandleConn(r.Context(), conn, "", svc.HandleMessage)
	}))
	t.Cleanup(srv.Close)
	rig.ctx = ctx
	rig.url = "ws" + strings.TrimPrefix(srv.URL, "http")
	return rig
}

func (r *historyRig) dial() *websocket.Conn {
	r.t.Helper()
	c, _, err := websocket.Dial(r.ctx, r.url, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { c.Close(websocket.StatusNormalClosure, "") })
	// give the hub a beat to register the client before anyone broadcasts
	time.Sleep(50 * time.Millisecond)
	return c
}

func (r *historyRig) send(c *websocket.Conn, body string) {
	r.t.Helper()
	if err := c.Write(r.ctx, websocket.MessageText, []byte(body)); err != nil {
		r.t.Fatal(err)
	}
}

// next reads one message or fails after a short wait.
func (r *historyRig) next(c *websocket.Conn) map[string]any {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(r.ctx, 2*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		r.t.Fatalf("expected a message: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		r.t.Fatal(err)
	}
	return m
}

// silent asserts nothing arrives within a short window.
func (r *historyRig) silent(c *websocket.Conn) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(r.ctx, 250*time.Millisecond)
	defer cancel()
	if _, data, err := c.Read(ctx); err == nil {
		r.t.Fatalf("unexpected message: %s", data)
	}
}

func TestHistoryRecordReachesOtherDevicesNotTheSender(t *testing.T) {
	r := newHistoryRig(t, true)
	phone, laptop := r.dial(), r.dial()

	r.send(phone, `{"type":"history_record","text":"git status","kind":"sh","pane_id":"p1","host":"local","workspace_id":"w1","workspace_label":"herdrem"}`)

	m := r.next(laptop)
	if m["type"] != "history_added" {
		t.Fatalf("laptop got %v", m)
	}
	e := m["entry"].(map[string]any)
	if e["c"] != "git status" || e["h"] != "local" || e["s"] != "w1" || e["l"] != "herdrem" || e["p"] != "p1" {
		t.Fatalf("entry = %v", e)
	}
	r.silent(phone) // the sender already applied its own copy
	if got := r.store.All(); len(got) != 1 || got[0].Text != "git status" {
		t.Fatalf("store = %+v", got)
	}
}

func TestHistoryRecordAgentPromptKeepsAgentMeta(t *testing.T) {
	r := newHistoryRig(t, true)
	a, b := r.dial(), r.dial()
	r.send(a, `{"type":"history_record","text":"refactor the parser","kind":"ag","agent":"claude","title":"api","host":"local","workspace_id":"w1"}`)
	e := r.next(b)["entry"].(map[string]any)
	if e["k"] != "ag" || e["a"] != "claude" || e["w"] != "api" {
		t.Fatalf("entry = %v", e)
	}
}

func TestHistoryRecordRejectionsAreSilent(t *testing.T) {
	r := newHistoryRig(t, true)
	a, b := r.dial(), r.dial()
	for _, body := range []string{
		`{"type":"history_record","text":"export API_TOKEN=abc","kind":"sh"}`,
		`{"type":"history_record","text":" leading space","kind":"sh"}`,
		`{"type":"history_record","text":"","kind":"sh"}`,
	} {
		r.send(a, body)
	}
	r.silent(a) // not even an error: the client applied the same rules
	r.silent(b)
	if len(r.store.All()) != 0 {
		t.Fatal("stored something that should have been rejected")
	}
}

func TestHistoryRecordInvalidKindIsAnError(t *testing.T) {
	r := newHistoryRig(t, true)
	a := r.dial()
	r.send(a, `{"type":"history_record","text":"ls","kind":"nope"}`)
	m := r.next(a)
	if m["type"] != "error" {
		t.Fatalf("got %v", m)
	}
}

func TestHistoryGetReturnsTheSnapshot(t *testing.T) {
	r := newHistoryRig(t, true)
	_, _ = r.store.Record(store.HistoryEntry{Text: "one"}, 1)
	_, _ = r.store.Record(store.HistoryEntry{Text: "two", Kind: store.HistoryAgent}, 2)
	c := r.dial()
	r.send(c, `{"type":"history_get"}`)
	m := r.next(c)
	if m["type"] != "history" {
		t.Fatalf("got %v", m)
	}
	es := m["entries"].([]any)
	if len(es) != 2 || es[0].(map[string]any)["c"] != "two" {
		t.Fatalf("entries = %v", es)
	}
}

func TestHistoryRemoveAndClearBroadcast(t *testing.T) {
	r := newHistoryRig(t, true)
	_, _ = r.store.Record(store.HistoryEntry{Text: "keep", Host: "local", Workspace: "a"}, 1)
	_, _ = r.store.Record(store.HistoryEntry{Text: "drop", Host: "local", Workspace: "a"}, 2)
	_, _ = r.store.Record(store.HistoryEntry{Text: "other ws", Host: "local", Workspace: "b"}, 3)
	a, b := r.dial(), r.dial()

	r.send(a, `{"type":"history_remove","kind":"sh","text":"drop"}`)
	m := r.next(b)
	if m["type"] != "history_removed" || m["text"] != "drop" {
		t.Fatalf("got %v", m)
	}

	r.send(a, `{"type":"history_clear","kind":"all","host":"local","workspace_id":"a"}`)
	m = r.next(b)
	if m["type"] != "history_cleared" || m["workspace_id"] != "a" {
		t.Fatalf("got %v", m)
	}
	if got := r.store.All(); len(got) != 1 || got[0].Text != "other ws" {
		t.Fatalf("store = %+v", got)
	}
}

func TestHistoryClearScopeNeedsBothHostAndWorkspace(t *testing.T) {
	r := newHistoryRig(t, true)
	_, _ = r.store.Record(store.HistoryEntry{Text: "x"}, 1)
	a := r.dial()
	r.send(a, `{"type":"history_clear","kind":"all","workspace_id":"only-ws"}`)
	if m := r.next(a); m["type"] != "error" {
		t.Fatalf("got %v", m)
	}
	if len(r.store.All()) != 1 {
		t.Fatal("a half-specified scope must not clear anything")
	}
}

func TestHistoryImportGivesOtherDevicesAFreshSnapshot(t *testing.T) {
	r := newHistoryRig(t, true)
	a, b := r.dial(), r.dial()
	r.send(a, `{"type":"history_import","entries":[{"c":"old cmd","t":10,"n":3},{"c":"export TOKEN=x","t":11,"n":1}]}`)
	m := r.next(b)
	if m["type"] != "history" {
		t.Fatalf("got %v", m)
	}
	es := m["entries"].([]any)
	if len(es) != 1 || es[0].(map[string]any)["c"] != "old cmd" || es[0].(map[string]any)["n"].(float64) != 3 {
		t.Fatalf("entries = %v", es)
	}
}

func TestHistoryWithoutAStoreReportsUnavailable(t *testing.T) {
	r := newHistoryRig(t, false)
	a := r.dial()
	r.send(a, `{"type":"history_get"}`)
	if m := r.next(a); m["type"] != "error" {
		t.Fatalf("got %v", m)
	}
}

func TestHasHistoryReflectsTheStore(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(&herdr.FakeClient{}, nil, nil, nil, nil, hub.New(logger), logger)
	if svc.HasHistory() {
		t.Fatal("no store wired: feature must be off")
	}
	hs, _ := store.NewHistoryStore(t.TempDir() + "/h.json")
	svc.SetHistory(hs)
	if !svc.HasHistory() {
		t.Fatal("store wired: feature must be on")
	}
}
