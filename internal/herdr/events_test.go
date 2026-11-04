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
		// subscription_event: dotted name, data has no type field.
		_, _ = c.Write([]byte("{\"event\":\"pane.agent_status_changed\",\"data\":{\"pane_id\":\"p1\",\"workspace_id\":\"w1\",\"agent_status\":\"blocked\"}}\n"))
		// event: snake_case lifecycle names, data.type matches.
		_, _ = c.Write([]byte("{\"event\":\"pane_created\",\"data\":{\"type\":\"pane_created\",\"pane\":{\"pane_id\":\"p2\"}}}\n"))
		_, _ = c.Write([]byte("{\"event\":\"pane_closed\",\"data\":{\"type\":\"pane_closed\",\"pane_id\":\"p3\",\"workspace_id\":\"w1\"}}\n"))
		_, _ = c.Write([]byte("{\"event\":\"pane_exited\",\"data\":{\"type\":\"pane_exited\",\"pane_id\":\"p1\",\"workspace_id\":\"w1\"}}\n"))
		// Not a pushed format. Dotted lifecycle names are accepted only so a
		// rename of the event schema cannot starve the status path.
		_, _ = c.Write([]byte("{\"event\":\"pane.created\",\"data\":{\"pane\":{\"pane_id\":\"p4\"}}}\n"))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var gotEvents []StatusEvent
	err = subscribeStatusSocket(ctx, sock, []string{"p1"}, StatusHooks{
		OnReady: func() {},
		OnEvent: func(ev StatusEvent) {
			gotEvents = append(gotEvents, ev)
			if len(gotEvents) == 5 {
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
	if len(gotEvents) != 5 || gotEvents[0].PaneID != "p1" || gotEvents[0].Kind != "pane.agent_status_changed" {
		t.Fatalf("events = %+v", gotEvents)
	}
	if gotEvents[1].Kind != "pane_created" || gotEvents[1].PaneID != "p2" {
		t.Fatalf("created event = %+v", gotEvents[1])
	}
	if gotEvents[2].Kind != "pane_closed" || gotEvents[2].PaneID != "p3" {
		t.Fatalf("closed event = %+v", gotEvents[2])
	}
	if gotEvents[3].Kind != "pane_exited" || gotEvents[3].PaneID != "p1" {
		t.Fatalf("exited event = %+v", gotEvents[3])
	}
	if gotEvents[4].Kind != "pane.created" || gotEvents[4].PaneID != "p4" {
		t.Fatalf("defensive dotted lifecycle event = %+v", gotEvents[4])
	}
}
