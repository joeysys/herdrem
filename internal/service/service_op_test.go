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
)

func TestRespondOperationIDRunsOnce(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := &herdr.FakeClient{SendBlock: make(chan struct{})}
	svc := New(client, nil, nil, nil, nil, hub.New(logger), logger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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

	dial := func() *websocket.Conn {
		t.Helper()
		wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
		return conn
	}
	c1, c2 := dial(), dial()
	body := []byte(`{"type":"respond","pane_id":"p","text":"y","request_id":"r1","operation_id":"op-1","boot_id":"` + svc.BootID() + `"}`)
	type readResult struct {
		msg map[string]any
		err error
	}
	results := make(chan readResult, 2)
	readOne := func(conn *websocket.Conn) {
		rctx, rcancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer rcancel()
		_, data, err := conn.Read(rctx)
		if err != nil {
			results <- readResult{err: err}
			return
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			results <- readResult{err: err}
			return
		}
		results <- readResult{msg: msg}
	}
	go readOne(c1)
	go readOne(c2)
	if err := c1.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
	if err := c2.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for client.SendCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	if client.SendCount() != 1 {
		t.Fatalf("SendText calls = %d, want 1 while the first call is in flight", client.SendCount())
	}
	close(client.SendBlock)
	deadline = time.Now().Add(2 * time.Second)
	for client.SendCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if client.SendCount() != 1 {
		t.Fatalf("SendText calls = %d, want 1 after both requests finished", client.SendCount())
	}
	if client.SentText() != "y\n" {
		t.Fatalf("sent = %q", client.SentText())
	}
	for i := 0; i < 2; i++ {
		select {
		case res := <-results:
			if res.err != nil {
				t.Fatal(res.err)
			}
			if res.msg["type"] != "sent" || res.msg["op"] != "respond" {
				t.Fatalf("reply = %v", res.msg)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for respond reply")
		}
	}
}

func TestRespondStaleBootDoesNotSend(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := &herdr.FakeClient{}
	svc := New(client, nil, nil, nil, nil, hub.New(logger), logger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
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
	body := []byte(`{"type":"respond","pane_id":"p","text":"y","operation_id":"op-old","boot_id":"old-boot"}`)
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	if msg["key"] != "stale_boot" || msg["op"] != "respond" {
		t.Fatalf("msg = %v", msg)
	}
	if client.SendCount() != 0 {
		t.Fatalf("SendText calls = %d", client.SendCount())
	}
}

func TestSessionCloseRequiresOperationID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := &herdr.FakeClient{}
	svc := New(client, nil, nil, nil, nil, hub.New(logger), logger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
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
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"session_close","target":"workspace","id":"ws-1","host":"local","group":true}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	if msg["key"] != "invalid" || client.CloseCount() != 0 {
		t.Fatalf("msg = %v closes = %d", msg, client.CloseCount())
	}
	body := `{"type":"session_close","target":"workspace","id":"ws-1","host":"local","group":true,"operation_id":"c1","boot_id":"` + svc.BootID() + `"}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if client.CloseCount() != 1 {
		t.Fatalf("closes = %d", client.CloseCount())
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if client.CloseCount() != 1 {
		t.Fatalf("replay closes = %d", client.CloseCount())
	}
}
