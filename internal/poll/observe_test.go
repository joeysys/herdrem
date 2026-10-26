package poll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"herdrem/internal/herdr"
	"herdrem/internal/hub"
)

// obsFake is a Client that can also observe. ObservePane either fails
// immediately or blocks until ctx is cancelled. It does not speak to herdr.
type obsFake struct {
	*herdr.FakeClient
	mu        sync.Mutex
	starts    int
	exits     int
	reads     int
	readLines []int
	onData    func()
	failFast  bool
	holdRead  chan struct{}
	entered   chan struct{}
	heldOnce  sync.Once
	paneReads []string
}

func (o *obsFake) ObservePane(ctx context.Context, paneID string, onData func()) error {
	o.mu.Lock()
	o.starts++
	o.onData = onData
	fail := o.failFast
	o.mu.Unlock()
	if fail {
		o.mu.Lock()
		o.exits++
		o.mu.Unlock()
		return errors.New("herdr: unknown command observe")
	}
	<-ctx.Done()
	o.mu.Lock()
	o.exits++
	o.mu.Unlock()
	return ctx.Err()
}

func (o *obsFake) ReadPane(ctx context.Context, host, paneID string, lines int) (string, error) {
	o.mu.Lock()
	o.reads++
	o.paneReads = append(o.paneReads, paneID)
	o.readLines = append(o.readLines, lines)
	n := o.reads
	o.mu.Unlock()
	if n == 1 && o.holdRead != nil {
		o.heldOnce.Do(func() { close(o.entered) })
		select {
		case <-o.holdRead:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return o.FakeClient.ReadPane(ctx, host, paneID, lines)
}

func (o *obsFake) readLineCopy() []int {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]int, len(o.readLines))
	copy(out, o.readLines)
	return out
}

func (o *obsFake) startCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.starts
}

func (o *obsFake) exitCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.exits
}

func (o *obsFake) readCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.reads
}

func (o *obsFake) readIDs() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.paneReads...)
}

func (o *obsFake) signal() {
	o.mu.Lock()
	fn := o.onData
	o.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (o *obsFake) waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		idle := o.exits == o.starts
		o.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	t.Errorf("observe processes still running: starts=%d exits=%d", o.starts, o.exits)
}

func newObsPoller(t *testing.T, client herdr.Client, hosts []string) *Poller {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if len(hosts) == 0 {
		hosts = []string{"local"}
	}
	p := New(client, hosts, nil, hub.New(logger), logger, time.Second)
	t.Cleanup(func() {
		p.stopAllObservers()
		if of, ok := client.(*obsFake); ok {
			of.waitIdle(t)
		}
	})
	return p
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(what)
}

func connectHubClient(t *testing.T, h *hub.Hub) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go h.Run(ctx)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		h.HandleConn(ctx, conn, "", nil)
	}))
	t.Cleanup(srv.Close)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	waitFor(t, "hub client did not connect", func() bool { return h.Count() > 0 })
}

func TestObserveStartsOnceForKnownLocalPane(t *testing.T) {
	o := &obsFake{FakeClient: &herdr.FakeClient{}}
	p := newObsPoller(t, o, nil)
	p.setRemote("p", "local")
	p.Subscribe("p")
	p.Subscribe("p")
	waitFor(t, "observe did not start", func() bool { return o.startCount() == 1 })
	time.Sleep(40 * time.Millisecond)
	if got := o.startCount(); got != 1 {
		t.Fatalf("one pane must keep a single observe process, got %d", got)
	}
}

func TestObserveSkipsUnknownSSHAndPlainClient(t *testing.T) {
	o := &obsFake{FakeClient: &herdr.FakeClient{}}
	p := newObsPoller(t, o, nil)
	p.Subscribe("missing")
	p.setRemote("remote", "dev@box")
	p.Subscribe("remote")
	time.Sleep(40 * time.Millisecond)
	if got := o.startCount(); got != 0 {
		t.Fatalf("unknown and ssh panes must stay on poll, started %d", got)
	}

	plain := newObsPoller(t, &herdr.FakeClient{}, nil)
	plain.setRemote("p", "local")
	plain.Subscribe("p")
	plain.observeMu.Lock()
	n := len(plain.observing)
	plain.observeMu.Unlock()
	if n != 0 {
		t.Fatalf("client without observe started %d processes", n)
	}
}

func TestObserveCapsAtEightAndFillsAFreedSlot(t *testing.T) {
	o := &obsFake{FakeClient: &herdr.FakeClient{}}
	p := newObsPoller(t, o, nil)
	for i := 0; i < maxObservers+1; i++ {
		id := fmt.Sprintf("p%d", i)
		p.setRemote(id, "local")
		p.Subscribe(id)
	}
	waitFor(t, "cap was not filled", func() bool { return o.startCount() >= maxObservers })
	time.Sleep(40 * time.Millisecond)
	if got := o.startCount(); got != maxObservers {
		t.Fatalf("observe cap = %d, started %d", maxObservers, got)
	}

	p.observeMu.Lock()
	var running string
	for id := range p.observing {
		running = id
		break
	}
	p.observeMu.Unlock()
	if running == "" {
		t.Fatal("no running observe to stop")
	}
	p.stopObserve(running)
	p.syncObservers()
	waitFor(t, "freed slot was not taken", func() bool { return o.startCount() == maxObservers+1 })
}

func TestObserveCoalescesBurstIntoOneForcedRead(t *testing.T) {
	prev := observeBeat
	observeBeat = 30 * time.Millisecond
	t.Cleanup(func() { observeBeat = prev })

	o := &obsFake{FakeClient: &herdr.FakeClient{ReadContent: "old\nnew\n"}}
	p := newObsPoller(t, o, nil)
	connectHubClient(t, p.hub)

	const id = "p"
	p.mu.Lock()
	p.paneRemote[id] = "local"
	p.latest = []herdr.Pane{{
		PaneID: id, Host: "local", Revision: 3, StateChangeSeq: 4,
		Scroll: &herdr.ScrollInfo{MaxOffsetFromBottom: 20},
	}}
	p.mu.Unlock()
	p.contentMu.Lock()
	p.contentCache[id] = "old\n"
	p.contentRevs[id] = 3
	p.contentSeqs[id] = 4
	p.contentMu.Unlock()
	p.Subscribe(id)
	waitFor(t, "observe did not start", func() bool { return o.startCount() == 1 })

	pane := herdr.Pane{PaneID: id, Host: "local", Revision: 3, StateChangeSeq: 4}
	p.pushContentDeltas(context.Background(), []herdr.Pane{pane})
	if got := o.readCount(); got != 0 {
		t.Fatalf("unchanged rev/seq must not read without a signal, got %d", got)
	}

	for i := 0; i < 5; i++ {
		o.signal()
	}
	waitFor(t, "forced read did not happen", func() bool { return o.readCount() == 1 })
	time.Sleep(3 * observeBeat)
	if got := o.readCount(); got != 1 {
		t.Fatalf("burst must coalesce to one read, got %d", got)
	}
	p.contentMu.Lock()
	cached := p.contentCache[id]
	p.contentMu.Unlock()
	if cached != "old\nnew\n" {
		t.Fatalf("forced read cached %q", cached)
	}

	o.signal()
	waitFor(t, "second burst did not read", func() bool { return o.readCount() == 2 })
}

func TestObserveForceReadRunsAfterInFlightDelta(t *testing.T) {
	o := &obsFake{
		FakeClient: &herdr.FakeClient{ReadContent: "screen\n"},
		holdRead:   make(chan struct{}),
		entered:    make(chan struct{}),
	}
	p := newObsPoller(t, o, nil)
	connectHubClient(t, p.hub)

	const id = "p"
	pane := herdr.Pane{
		PaneID: id, Host: "local", Revision: 5, StateChangeSeq: 5,
		Scroll: &herdr.ScrollInfo{MaxOffsetFromBottom: 10},
	}
	p.mu.Lock()
	p.paneRemote[id] = "local"
	p.latest = []herdr.Pane{pane}
	p.mu.Unlock()
	p.contentMu.Lock()
	p.subscribers[id] = time.Now().Unix()
	p.contentMu.Unlock()

	p.kickDeltaPoll(context.Background(), []herdr.Pane{pane})
	select {
	case <-o.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight read did not start")
	}
	// Same rev as the read that is in flight. Without a queued force, the
	// follow-up would skip and the signal would be lost.
	p.kickDelta(id)
	close(o.holdRead)
	waitFor(t, "queued observe signal was not read", func() bool { return o.readCount() == 2 })
}

func TestDeltaPollQueuedBehindInFlightRead(t *testing.T) {
	o := &obsFake{
		FakeClient: &herdr.FakeClient{ReadContent: "screen\n"},
		holdRead:   make(chan struct{}),
		entered:    make(chan struct{}),
	}
	p := newObsPoller(t, o, nil)
	connectHubClient(t, p.hub)

	paneA := herdr.Pane{PaneID: "a", Host: "local", Revision: 1, StateChangeSeq: 1}
	paneB := herdr.Pane{PaneID: "b", Host: "local", Revision: 2, StateChangeSeq: 2}
	p.mu.Lock()
	p.paneRemote["a"] = "local"
	p.paneRemote["b"] = "local"
	p.latest = []herdr.Pane{paneA, paneB}
	p.mu.Unlock()
	p.contentMu.Lock()
	p.subscribers["a"] = time.Now().Unix()
	p.subscribers["b"] = time.Now().Unix()
	p.contentMu.Unlock()

	p.kickDeltaPoll(context.Background(), []herdr.Pane{paneA})
	select {
	case <-o.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight read did not start")
	}
	p.kickDeltaPoll(context.Background(), []herdr.Pane{paneB})
	close(o.holdRead)
	waitFor(t, "queued poll pane was not read", func() bool {
		for _, id := range o.readIDs() {
			if id == "b" {
				return true
			}
		}
		return false
	})
}

func TestObserveSnapshotOnlyWhileObserving(t *testing.T) {
	o := &obsFake{FakeClient: &herdr.FakeClient{}}
	p := newObsPoller(t, o, nil)
	p.setRemote("p", "local")
	p.contentMu.Lock()
	p.contentCache["p"] = "screen\n"
	p.contentRevs["p"] = 4
	p.contentAt["p"] = time.Now()
	p.contentMu.Unlock()
	if _, _, _, _, ok := p.ObserveSnapshot("p"); ok {
		t.Fatal("snapshot without an observe process")
	}
	p.Subscribe("p")
	waitFor(t, "observe did not start", func() bool { return o.startCount() == 1 })
	content, rev, gen, epoch, ok := p.ObserveSnapshot("p")
	if !ok || content != "screen\n" || rev != 4 || gen != 1 || epoch == 0 {
		t.Fatalf("ObserveSnapshot = %q, %d, %d, %d, %v", content, rev, gen, epoch, ok)
	}
	p.contentMu.Lock()
	p.contentAt["p"] = time.Now().Add(-contentVerFreshFor - time.Second)
	p.contentMu.Unlock()
	if _, _, _, _, ok := p.ObserveSnapshot("p"); ok {
		t.Fatal("stale contentAt must not be served")
	}
}

// connectIncremental registers one client and marks it able to receive deltas.
func connectIncremental(t *testing.T, h *hub.Hub) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go h.Run(ctx)
	var (
		mu         sync.Mutex
		serverConn *websocket.Conn
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		serverConn = conn
		mu.Unlock()
		h.HandleConn(ctx, conn, "", nil)
	}))
	t.Cleanup(srv.Close)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	waitFor(t, "hub client did not connect", func() bool { return h.Count() > 0 })
	mu.Lock()
	sc := serverConn
	mu.Unlock()
	if sc == nil {
		t.Fatal("server conn was not captured")
	}
	h.MarkIncremental(sc)
	return conn
}

func TestObserveSnapshotThenAppendDoesNotDupOrDrop(t *testing.T) {
	const id = "p"
	const baseline = "alpha\n"
	const next = "alpha\nbeta\n"
	o := &obsFake{FakeClient: &herdr.FakeClient{ReadContent: next}}
	p := newObsPoller(t, o, nil)
	conn := connectIncremental(t, p.hub)

	pane := herdr.Pane{
		PaneID: id, Host: "local", Revision: 7, StateChangeSeq: 1,
		Scroll: &herdr.ScrollInfo{MaxOffsetFromBottom: 20},
	}
	p.mu.Lock()
	p.paneRemote[id] = "local"
	p.latest = []herdr.Pane{pane}
	p.mu.Unlock()
	p.contentMu.Lock()
	p.contentCache[id] = baseline
	p.contentRevs[id] = 7
	p.contentSeqs[id] = 1
	p.contentAt[id] = time.Now()
	p.contentMu.Unlock()
	p.Subscribe(id)
	waitFor(t, "observe did not start", func() bool { return o.startCount() == 1 })

	content, rev, gen, epoch, ok := p.ObserveSnapshot(id)
	if !ok || content != baseline || rev != 7 || gen != 1 || epoch == 0 {
		t.Fatalf("refresh baseline = %q rev=%d gen=%d epoch=%d ok=%v", content, rev, gen, epoch, ok)
	}

	p.pushContentDeltasOpt(context.Background(), []herdr.Pane{pane}, map[string]bool{id: true})

	readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		t.Fatal(err)
	}
	var msg struct {
		Content string `json:"content"`
		Append  bool   `json:"append"`
		Rev     *int   `json:"rev"`
		BaseRev *int   `json:"base_rev"`
		Gen     *int   `json:"gen"`
		BaseGen *int   `json:"base_gen"`
		Epoch   *int   `json:"epoch"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	if !msg.Append || msg.Content != "beta\n" || msg.Rev == nil || *msg.Rev != 7 || msg.BaseRev == nil || *msg.BaseRev != 7 {
		t.Fatalf("delta = %+v", msg)
	}
	if msg.Gen == nil || *msg.Gen != 2 || msg.BaseGen == nil || *msg.BaseGen != 1 || msg.Epoch == nil || *msg.Epoch != epoch {
		t.Fatalf("delta generation = %+v, snapshot epoch %d", msg, epoch)
	}
	applied := content + msg.Content
	if applied != next || strings.Count(applied, "alpha\n") != 1 || strings.Count(applied, "beta\n") != 1 {
		t.Fatalf("refresh+delta = %q, want %q", applied, next)
	}
}

func TestObserveStopsWhenSubscriptionExpires(t *testing.T) {
	o := &obsFake{FakeClient: &herdr.FakeClient{}}
	p := newObsPoller(t, o, nil)
	p.setRemote("p", "local")
	p.Subscribe("p")
	waitFor(t, "observe did not start", func() bool { return o.startCount() == 1 })

	p.contentMu.Lock()
	p.subscribers["p"] = time.Now().Add(-2 * time.Hour).Unix()
	p.contentMu.Unlock()
	if p.activeSubscription("p") {
		t.Fatal("expired subscription still active")
	}
	waitFor(t, "observe survived subscription expiry", func() bool { return o.exitCount() == 1 })
}

func TestObserveBackoffAfterUnexpectedExit(t *testing.T) {
	o := &obsFake{FakeClient: &herdr.FakeClient{}, failFast: true}
	p := newObsPoller(t, o, nil)
	p.setRemote("p", "local")
	p.Subscribe("p")
	waitFor(t, "failed observe did not record a backoff", func() bool {
		p.observeMu.Lock()
		defer p.observeMu.Unlock()
		_, retry := p.observeRetry["p"]
		return retry && len(p.observing) == 0 && o.startCount() == 1
	})
	p.Subscribe("p")
	time.Sleep(40 * time.Millisecond)
	if got := o.startCount(); got != 1 {
		t.Fatalf("backoff must not respawn on the next subscribe, started %d", got)
	}
	p.observeMu.Lock()
	p.observeRetry["p"] = time.Now().Add(-time.Second)
	p.observeMu.Unlock()
	p.ensureObserve("p")
	waitFor(t, "observe did not retry after backoff", func() bool { return o.startCount() == 2 })
}

func TestFinishObserveIgnoresStaleGeneration(t *testing.T) {
	p := newObsPoller(t, &herdr.FakeClient{}, nil)
	_, cancel := context.WithCancel(context.Background())
	p.observeMu.Lock()
	p.observing["p"] = &observeRun{gen: 2, cancel: cancel}
	p.observeMu.Unlock()

	p.finishObserve("p", 1, errors.New("old process exited"))

	p.observeMu.Lock()
	_, running := p.observing["p"]
	_, retry := p.observeRetry["p"]
	p.observeMu.Unlock()
	if !running || retry {
		t.Fatalf("stale exit changed the live run: running=%v retry=%v", running, retry)
	}
}

func TestPollOnceObservesSubscribedLocalPaneOnly(t *testing.T) {
	local := &obsFake{FakeClient: &herdr.FakeClient{Panes: []herdr.Pane{
		{PaneID: "local-pane", Kind: "shell"},
	}}}
	p := newObsPoller(t, local, []string{"local"})
	p.contentMu.Lock()
	p.subscribers["local-pane"] = time.Now().Unix()
	p.contentMu.Unlock()
	p.pollOnce(context.Background())
	waitFor(t, "poll did not start observe", func() bool { return local.startCount() == 1 })
	p.pollOnce(context.Background())
	time.Sleep(40 * time.Millisecond)
	if got := local.startCount(); got != 1 {
		t.Fatalf("second poll restarted observe, starts=%d", got)
	}

	remote := &obsFake{FakeClient: &herdr.FakeClient{Panes: []herdr.Pane{
		{PaneID: "ssh-pane", Kind: "shell"},
	}}}
	rp := newObsPoller(t, remote, []string{"dev@box"})
	rp.contentMu.Lock()
	rp.subscribers["ssh-pane"] = time.Now().Unix()
	rp.contentMu.Unlock()
	rp.pollOnce(context.Background())
	time.Sleep(40 * time.Millisecond)
	if got := remote.startCount(); got != 0 {
		t.Fatalf("ssh poll started observe, starts=%d", got)
	}
}

func TestWidenObserveBaselineReadsRequestedLines(t *testing.T) {
	const id = "p"
	o := &obsFake{FakeClient: &herdr.FakeClient{ReadContent: "a\nb\nc\n"}}
	p := newObsPoller(t, o, nil)
	connectHubClient(t, p.hub)
	p.setRemote(id, "local")
	p.contentMu.Lock()
	p.contentCache[id] = "a\n"
	p.contentRevs[id] = 4
	p.contentGen[id] = 2
	p.contentAt[id] = time.Now()
	p.contentMu.Unlock()
	p.Subscribe(id)
	waitFor(t, "observe did not start", func() bool { return o.startCount() == 1 })

	content, rev, gen, epoch, truncated, ok := p.WidenObserveBaseline(context.Background(), id, 12000)
	if !ok || truncated || content != "a\nb\nc\n" || rev != 4 || gen != 3 || epoch == 0 {
		t.Fatalf("widen = %q rev=%d gen=%d epoch=%d truncated=%v ok=%v", content, rev, gen, epoch, truncated, ok)
	}
	if lines := o.readLineCopy(); len(lines) != 1 || lines[0] != 12000 {
		t.Fatalf("read lines = %v, want [12000]", lines)
	}

	pane := herdr.Pane{
		PaneID: id, Host: "local", Revision: 5, StateChangeSeq: 1,
		Scroll: &herdr.ScrollInfo{MaxOffsetFromBottom: 20},
	}
	p.mu.Lock()
	p.latest = []herdr.Pane{pane}
	p.mu.Unlock()
	p.pushContentDeltasOpt(context.Background(), []herdr.Pane{pane}, nil)
	lines := o.readLineCopy()
	if len(lines) != 2 || lines[1] != 12000 {
		t.Fatalf("later delta read lines = %v, want [12000 12000]", lines)
	}

	capped, _, _, _, cappedTrunc, cappedOK := p.WidenObserveBaseline(context.Background(), id, maxBaselineLines+10)
	if !cappedOK || cappedTrunc || capped != "a\nb\nc\n" {
		t.Fatalf("short pane over the cap = %q truncated=%v ok=%v", capped, cappedTrunc, cappedOK)
	}
	p.contentMu.Lock()
	window := p.contentReadLines[id]
	p.contentMu.Unlock()
	if window != maxBaselineLines {
		t.Fatalf("window = %d, want %d", window, maxBaselineLines)
	}
}
