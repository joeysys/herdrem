package hub

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// BroadcastJSONIncremental must only reach clients that declared
// incremental support; BroadcastJSON reaches everyone.
func TestBroadcastIncrementalTargetsDeclaredClients(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	var mu sync.Mutex
	gotA, gotB := []string{}, []string{}

	var serverConns []*websocket.Conn
	var cmu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		cmu.Lock()
		serverConns = append(serverConns, conn)
		cmu.Unlock()
		h.HandleConn(ctx, conn, "", nil)
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	// Client A: will be marked incremental. Client B: not.
	connA, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connA.Close(websocket.StatusNormalClosure, "")
	connB, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connB.Close(websocket.StatusNormalClosure, "")
	time.Sleep(300 * time.Millisecond) // let clients register

	// Mark the FIRST server-side connection (A) incremental, like the service
	// does when a client declares support via read_pane.
	cmu.Lock()
	if len(serverConns) < 1 {
		cmu.Unlock()
		t.Fatal("no server conns registered")
	}
	serverA := serverConns[0]
	cmu.Unlock()
	h.MarkIncremental(serverA)

	// Read pump for A
	go func() {
		for {
			_, data, err := connA.Read(ctx)
			if err != nil {
				return
			}
			mu.Lock()
			gotA = append(gotA, string(data))
			mu.Unlock()
		}
	}()
	go func() {
		for {
			_, data, err := connB.Read(ctx)
			if err != nil {
				return
			}
			mu.Lock()
			gotB = append(gotB, string(data))
			mu.Unlock()
		}
	}()

	time.Sleep(200 * time.Millisecond)

	// Incremental broadcast → only A.
	h.BroadcastJSONIncremental(map[string]string{"type": "delta"})
	// Regular broadcast → both.
	h.BroadcastJSON(map[string]string{"type": "full"})

	time.Sleep(400 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	aJoined := strings.Join(gotA, " ")
	bJoined := strings.Join(gotB, " ")
	if !strings.Contains(aJoined, "delta") {
		t.Errorf("incremental client A should receive delta, got %v", gotA)
	}
	if strings.Contains(bJoined, "delta") {
		t.Errorf("legacy client B must NOT receive delta, got %v", gotB)
	}
	if !strings.Contains(bJoined, "full") {
		t.Errorf("regular broadcast should reach B, got %v", gotB)
	}
}

// The on-connect hook must fire after the client is registered in the hub,
// so a TriggerPoll invoked from it observes Count() > 0.
func TestOnConnectFiresAfterRegistration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	var mu sync.Mutex
	countAtFire := -1
	fired := make(chan struct{})
	h.SetOnConnect(func() {
		mu.Lock()
		countAtFire = h.Count()
		mu.Unlock()
		close(fired)
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		h.HandleConn(ctx, conn, "", nil)
	}))
	defer srv.Close()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("onConnect never fired")
	}
	mu.Lock()
	defer mu.Unlock()
	if countAtFire != 1 {
		t.Fatalf("onConnect observed Count()=%d, want 1 (client must be registered first)", countAtFire)
	}
}
