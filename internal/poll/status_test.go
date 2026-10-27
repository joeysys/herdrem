package poll

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"herdrem/internal/herdr"
	"herdrem/internal/hub"
)

// snapClient serves a different pane list per host and counts snapshots.
type snapClient struct {
	herdr.FakeClient
	mu    sync.Mutex
	panes map[string][]herdr.Pane
	calls map[string]int
}

func (s *snapClient) Snapshot(ctx context.Context, host string) (*herdr.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[host]++
	panes := append([]herdr.Pane(nil), s.panes[host]...)
	return &herdr.Snapshot{Panes: panes, Protocol: herdr.MinProtocol, Version: "fake"}, nil
}

func (s *snapClient) setStatus(host, id, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.panes[host] {
		if s.panes[host][i].PaneID == id {
			s.panes[host][i].Status = status
			s.panes[host][i].AgentStatus = status
		}
	}
}

func (s *snapClient) callCount(host string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[host]
}

type burstSource struct {
	snapClient
	release chan struct{}
	subs    atomic.Int32
	events  int
}

func (b *burstSource) SubscribeLocalStatus(ctx context.Context, paneIDs []string, hooks herdr.StatusHooks) error {
	b.subs.Add(1)
	if hooks.OnReady != nil {
		hooks.OnReady()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.release:
	}
	for i := 0; i < b.events; i++ {
		if hooks.OnEvent != nil {
			hooks.OnEvent(herdr.StatusEvent{Kind: "pane_agent_status_changed", PaneID: "L"})
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestStatusEventsCoalesceToOneLocalSnapshot(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	src := &burstSource{
		release: make(chan struct{}),
		events:  30,
	}
	src.panes = map[string][]herdr.Pane{
		"local": {{PaneID: "L", Status: "working", Kind: "agent", Agent: "claude", Host: "local"}},
		"dev":   {{PaneID: "R", Status: "working", Kind: "agent", Agent: "claude", Host: "dev"}},
	}
	src.calls = map[string]int{}
	p := New(src, []string{"local", "dev"}, nil, hub.New(logger), logger, time.Hour)
	p.statusMergeEvery = 25 * time.Millisecond
	p.statusRedialWait = time.Hour

	var mu sync.Mutex
	var transitions []string
	p.SetStatusWatcher(func(pane herdr.Pane, from, to string) {
		if pane.PaneID != "L" {
			return
		}
		mu.Lock()
		transitions = append(transitions, from+"->"+to)
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && src.subs.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if src.subs.Load() == 0 {
		t.Fatal("did not subscribe")
	}
	time.Sleep(80 * time.Millisecond)
	settled := src.callCount("local")
	time.Sleep(40 * time.Millisecond)
	if src.callCount("local") != settled {
		t.Fatal("local snapshots still moving before the burst")
	}
	remoteBefore := src.callCount("dev")
	src.setStatus("local", "L", "blocked")
	close(src.release)

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if src.callCount("local") > settled {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := src.callCount("local") - settled
	if got != 1 {
		t.Fatalf("local snapshots after 30 events = %d, want 1", got)
	}
	if src.callCount("dev") != remoteBefore {
		t.Fatalf("coalesce snapshotted remote: %d -> %d", remoteBefore, src.callCount("dev"))
	}
	if p.RemoteForPane("R") != "dev" {
		t.Fatal("local refresh dropped the remote pane")
	}
	var status string
	for _, pane := range p.Snapshot() {
		if pane.PaneID == "L" {
			status = pane.Status
		}
	}
	if status != "blocked" {
		t.Fatalf("status = %q, want blocked within 1s", status)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(transitions) != 1 || transitions[0] != "working->blocked" {
		t.Fatalf("transitions = %v", transitions)
	}
}

type dropSource struct {
	snapClient
	subs atomic.Int32
	drop chan struct{}
}

func (d *dropSource) SubscribeLocalStatus(ctx context.Context, paneIDs []string, hooks herdr.StatusHooks) error {
	n := d.subs.Add(1)
	if hooks.OnReady != nil {
		hooks.OnReady()
	}
	if n == 1 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-d.drop:
			return context.Canceled
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestStatusDropRestoresSnapshotPollingAndRedials(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	src := &dropSource{drop: make(chan struct{})}
	src.panes = map[string][]herdr.Pane{
		"local": {{PaneID: "L", Status: "working", Kind: "agent", Agent: "claude", Host: "local"}},
	}
	src.calls = map[string]int{}
	p := New(src, []string{"local"}, nil, hub.New(logger), logger, 40*time.Millisecond)
	p.statusMergeEvery = 20 * time.Millisecond
	p.statusRedialWait = 300 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !p.statusHealthy.Load() {
		time.Sleep(5 * time.Millisecond)
	}
	if !p.statusHealthy.Load() {
		t.Fatal("event connection did not become healthy")
	}
	time.Sleep(80 * time.Millisecond)
	settled := src.callCount("local")
	time.Sleep(90 * time.Millisecond)
	if src.callCount("local") != settled {
		t.Fatalf("healthy connection still snapshotted local on the fast tick (%d -> %d)", settled, src.callCount("local"))
	}
	src.setStatus("local", "L", "blocked")
	close(src.drop)

	deadline = time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) && src.callCount("local") == settled {
		time.Sleep(10 * time.Millisecond)
	}
	if src.callCount("local") == settled {
		t.Fatal("dropped event connection did not return to snapshot polling")
	}
	var status string
	for _, pane := range p.Snapshot() {
		if pane.PaneID == "L" {
			status = pane.Status
		}
	}
	if status != "blocked" {
		t.Fatalf("status after drop = %q", status)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && src.subs.Load() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if src.subs.Load() < 2 {
		t.Fatalf("reconnects = %d, want >= 2", src.subs.Load())
	}
}

func TestRemotePollKeepsLocalPanes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	src := &snapClient{
		panes: map[string][]herdr.Pane{
			"local": {{PaneID: "L", Status: "working", Kind: "agent", Host: "local"}},
			"dev":   {{PaneID: "R", Status: "idle", Kind: "shell", Host: "dev"}},
		},
		calls: map[string]int{},
	}
	p := New(src, []string{"local", "dev"}, nil, hub.New(logger), logger, time.Hour)
	p.pollOnce(context.Background())
	if p.RemoteForPane("L") != "local" || p.RemoteForPane("R") != "dev" {
		t.Fatalf("routes L=%q R=%q", p.RemoteForPane("L"), p.RemoteForPane("R"))
	}
	p.statusHealthy.Store(true)
	p.localCalibAt = time.Now()
	src.mu.Lock()
	src.panes["dev"] = nil
	src.mu.Unlock()
	before := src.callCount("local")
	p.pollOnce(context.Background())
	if p.RemoteForPane("L") != "local" {
		t.Fatal("remote poll dropped the local pane")
	}
	if p.RemoteForPane("R") != "" {
		t.Fatal("remote pane removed upstream was kept")
	}
	if src.callCount("local") != before {
		t.Fatalf("local snapshots %d -> %d while events were healthy", before, src.callCount("local"))
	}
	var still bool
	for _, pane := range p.Snapshot() {
		if pane.PaneID == "L" && !pane.Stale {
			still = true
		}
	}
	if !still {
		t.Fatal("local pane missing from the merged list or marked stale")
	}
}

func TestTriggerPollRefreshesLocalLabelWhileEventsHealthy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	src := &snapClient{
		panes: map[string][]herdr.Pane{
			"local": {{PaneID: "L", Label: "old", Status: "working", Kind: "agent", Host: "local"}},
		},
		calls: map[string]int{},
	}
	p := New(src, []string{"local"}, nil, hub.New(logger), logger, time.Hour)
	p.pollOnce(context.Background())
	p.statusHealthy.Store(true)
	p.localCalibAt = time.Now()

	src.mu.Lock()
	src.panes["local"][0].Label = "renamed"
	src.mu.Unlock()

	before := src.callCount("local")
	p.pollOnce(context.Background())
	if src.callCount("local") != before {
		t.Fatalf("ticker poll snapshotted local while events were healthy (%d -> %d)", before, src.callCount("local"))
	}
	if labelOf(p, "L") != "old" {
		t.Fatalf("skipped poll updated label to %q", labelOf(p, "L"))
	}

	// session_rename / workspace_move call TriggerPoll. Run then pollOnce.
	p.TriggerPoll()
	p.pollOnce(context.Background())
	if src.callCount("local") != before+1 {
		t.Fatalf("trigger snapshots %d -> %d, want %d", before, src.callCount("local"), before+1)
	}
	if labelOf(p, "L") != "renamed" {
		t.Fatalf("label after trigger = %q", labelOf(p, "L"))
	}
}

func labelOf(p *Poller, id string) string {
	for _, pane := range p.Snapshot() {
		if pane.PaneID == id {
			return pane.Label
		}
	}
	return ""
}

func TestAgentsNeedBroadcastIgnoresRevision(t *testing.T) {
	before := map[string]string{"L": "working"}
	if agentsNeedBroadcast(before, []herdr.Pane{{PaneID: "L", Status: "working", Revision: 99}}) {
		t.Fatal("revision-only change would broadcast")
	}
	if !agentsNeedBroadcast(before, []herdr.Pane{{PaneID: "L", Status: "blocked"}}) {
		t.Fatal("status change would not broadcast")
	}
	if !agentsNeedBroadcast(before, []herdr.Pane{{PaneID: "L", Status: "working"}, {PaneID: "N", Status: "idle"}}) {
		t.Fatal("created pane would not broadcast")
	}
	if !agentsNeedBroadcast(before, []herdr.Pane{}) {
		t.Fatal("closed pane would not broadcast")
	}
}
