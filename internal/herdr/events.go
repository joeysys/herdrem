package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// StatusEvent is one local lifecycle or agent-status push. The socket reader
// delivers it by callback. Callers must not queue the event on a channel: a
// busy pane can emit faster than a consumer, and a full channel would stall
// the socket.
type StatusEvent struct {
	Kind   string
	PaneID string
}

// StatusHooks receives the subscription ack and later events. OnEvent runs
// on the read goroutine and must not block.
type StatusHooks struct {
	OnReady func()
	OnEvent func(StatusEvent)
}

// LocalStatusSource is an optional capability. Snapshot polling stays the
// fallback when a client does not implement it. FakeClient does not.
type LocalStatusSource interface {
	// SubscribeLocalStatus dials the local herdr socket and holds one
	// events.subscribe connection until ctx is cancelled or the socket drops.
	// The subscription is lifecycle events plus pane.agent_status_changed for
	// each pane id. It is the first request on that connection.
	SubscribeLocalStatus(ctx context.Context, paneIDs []string, hooks StatusHooks) error
}

// SubscribeLocalStatus looks up the local socket, then subscribes. Remote
// hosts are not supported here.
func (c *CLIClient) SubscribeLocalStatus(ctx context.Context, paneIDs []string, hooks StatusHooks) error {
	st, err := c.ServerStatus(ctx, "")
	if err != nil {
		return err
	}
	if st.Socket == "" || strings.ContainsAny(st.Socket, "\r\n") {
		return fmt.Errorf("herdr socket path is empty")
	}
	return subscribeStatusSocket(ctx, st.Socket, paneIDs, hooks)
}

type statusSub struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id,omitempty"`
}

// statusSubscribeBody is the one request written on the connection.
// pane.updated is intentionally absent: it fires on every revision.
func statusSubscribeBody(paneIDs []string) ([]byte, error) {
	subs := []statusSub{
		{Type: "pane.created"},
		{Type: "pane.closed"},
		{Type: "pane.exited"},
	}
	seen := make(map[string]bool, len(paneIDs))
	for _, id := range paneIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		subs = append(subs, statusSub{Type: "pane.agent_status_changed", PaneID: id})
	}
	body, err := json.Marshal(map[string]any{
		"id":     "status-1",
		"method": "events.subscribe",
		"params": map[string]any{"subscriptions": subs},
	})
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func subscribeStatusSocket(ctx context.Context, socketPath string, paneIDs []string, hooks StatusHooks) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("events.subscribe: %w", err)
	}
	defer conn.Close()
	// Closing the socket is what unblocks ReadBytes when ctx ends. A second
	// subscribe is not sent on this connection; the caller redials.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	body, err := statusSubscribeBody(paneIDs)
	if err != nil {
		return err
	}
	if _, err := conn.Write(body); err != nil {
		return fmt.Errorf("events.subscribe: %w", err)
	}
	r := bufio.NewReader(conn)
	ack, err := r.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("events.subscribe: %w", err)
	}
	if err := subscriptionStarted(ack); err != nil {
		return err
	}
	if hooks.OnReady != nil {
		hooks.OnReady()
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := r.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("events.subscribe: %w", err)
		}
		ev, ok := parseStatusEvent(line)
		if !ok || hooks.OnEvent == nil {
			continue
		}
		hooks.OnEvent(ev)
	}
}

func subscriptionStarted(line []byte) error {
	var resp struct {
		Result struct {
			Type string `json:"type"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("events.subscribe: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return fmt.Errorf("events.subscribe: %s", resp.Error.Message)
	}
	if resp.Result.Type != "subscription_started" {
		return fmt.Errorf("events.subscribe: unexpected ack")
	}
	return nil
}

func parseStatusEvent(line []byte) (StatusEvent, bool) {
	var env struct {
		Event string `json:"event"`
		Data  struct {
			PaneID string `json:"pane_id"`
			Pane   struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &env); err != nil || env.Event == "" {
		return StatusEvent{}, false
	}
	switch env.Event {
	case "pane_created", "pane_closed", "pane_exited", "pane_agent_status_changed":
	default:
		return StatusEvent{}, false
	}
	id := env.Data.PaneID
	if id == "" {
		id = env.Data.Pane.PaneID
	}
	return StatusEvent{Kind: env.Event, PaneID: id}, true
}
