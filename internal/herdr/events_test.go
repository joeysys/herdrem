package herdr

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestSubscribeStatusSocketOmitsPaneUpdated(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "t.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		line, err := bufio.NewReader(c).ReadBytes('\n')
		if err != nil {
			return
		}
		got <- line
		_, _ = c.Write([]byte("{\"id\":\"status-1\",\"result\":{\"type\":\"subscription_started\"}}\n"))
		_, _ = c.Write([]byte("{\"event\":\"pane_agent_status_changed\",\"data\":{\"type\":\"pane_agent_status_changed\",\"pane_id\":\"p1\",\"agent_status\":\"blocked\"}}\n"))
		_, _ = c.Write([]byte("{\"event\":\"pane_created\",\"data\":{\"type\":\"pane_created\",\"pane\":{\"pane_id\":\"p2\"}}}\n"))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var gotEvents []StatusEvent
	err = subscribeStatusSocket(ctx, sock, []string{"p1"}, StatusHooks{
		OnReady: func() {},
		OnEvent: func(ev StatusEvent) {
			gotEvents = append(gotEvents, ev)
			if len(gotEvents) == 2 {
				cancel()
			}
		},
	})
	if ctx.Err() == nil && err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-got:
		for _, want := range []string{
			`"method":"events.subscribe"`,
			`"type":"pane.created"`,
			`"type":"pane.closed"`,
			`"type":"pane.exited"`,
			`"type":"pane.agent_status_changed"`,
			`"pane_id":"p1"`,
		} {
			if !bytes.Contains(req, []byte(want)) {
				t.Fatalf("subscribe request missing %s: %s", want, req)
			}
		}
		if bytes.Contains(req, []byte("pane.updated")) || bytes.Contains(req, []byte("pane_output")) {
			t.Fatalf("subscribe request includes a forbidden type: %s", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no subscribe request")
	}
	if len(gotEvents) != 2 || gotEvents[0].PaneID != "p1" || gotEvents[0].Kind != "pane_agent_status_changed" {
		t.Fatalf("events = %+v", gotEvents)
	}
	if gotEvents[1].Kind != "pane_created" || gotEvents[1].PaneID != "p2" {
		t.Fatalf("created event = %+v", gotEvents[1])
	}
}
