package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"herdrem/internal/config"
	"herdrem/internal/herdr"
	"herdrem/internal/hub"
	"herdrem/internal/service"
	"herdrem/internal/store"
)

// historyServer starts the real /ws handler with a service that does (or does
// not) keep the shared input history.
func historyServer(t *testing.T, hs *store.HistoryStore) string {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go h.Run(ctx)
	svc := service.New(&herdr.FakeClient{}, nil, nil, nil, nil, h, logger)
	if hs != nil {
		svc.SetHistory(hs)
	}
	staticFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	s := New(config.Config{}, h, nil, nil, svc, nil, nil, staticFS, logger)
	srv := httptest.NewServer(s.Routes())
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func readMsgs(t *testing.T, wsURL string, n int) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// With no token configured only a same-origin loopback client is let in.
	origin := "http://" + strings.TrimPrefix(wsURL, "ws://")
	conn, _, err := websocket.Dial(ctx, wsURL+"/ws", &websocket.DialOptions{
		HTTPHeader: map[string][]string{"Origin": {origin}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	var out []map[string]any
	for i := 0; i < n; i++ {
		rctx, rcancel := context.WithTimeout(ctx, 400*time.Millisecond)
		_, data, err := conn.Read(rctx)
		rcancel()
		if err != nil {
			break
		}
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func hasFeature(hello map[string]any, f string) bool {
	feats, _ := hello["features"].([]any)
	for _, x := range feats {
		if x == f {
			return true
		}
	}
	return false
}

func TestHelloAdvertisesInputHistoryAndSendsASnapshot(t *testing.T) {
	hs, err := store.NewHistoryStore(t.TempDir() + "/history.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hs.Record(store.HistoryEntry{Text: "git status", Host: "local", Workspace: "w1"}, 1); err != nil {
		t.Fatal(err)
	}
	msgs := readMsgs(t, historyServer(t, hs), 6)
	if len(msgs) == 0 || msgs[0]["type"] != "hello" {
		t.Fatalf("first message = %v", msgs)
	}
	if !hasFeature(msgs[0], "input-history") {
		t.Fatalf("hello features lack input-history: %v", msgs[0]["features"])
	}
	var snap map[string]any
	for _, m := range msgs {
		if m["type"] == "history" {
			snap = m
		}
	}
	if snap == nil {
		t.Fatalf("no history snapshot after hello: %v", msgs)
	}
	es := snap["entries"].([]any)
	if len(es) != 1 || es[0].(map[string]any)["c"] != "git status" || es[0].(map[string]any)["s"] != "w1" {
		t.Fatalf("snapshot entries = %v", es)
	}
}

func TestHelloOmitsInputHistoryWithoutAStore(t *testing.T) {
	msgs := readMsgs(t, historyServer(t, nil), 4)
	if len(msgs) == 0 || msgs[0]["type"] != "hello" {
		t.Fatalf("first message = %v", msgs)
	}
	if hasFeature(msgs[0], "input-history") {
		t.Fatal("input-history advertised without a store: clients would lose their local history")
	}
	for _, m := range msgs {
		if m["type"] == "history" {
			t.Fatal("snapshot sent without a store")
		}
	}
}
