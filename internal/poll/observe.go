package poll

import (
	"context"
	"errors"
	"time"

	"herdrem/internal/herdr"
)

// paneObserver is the optional herdr capability used by scheme C. FakeClient
// and every other Client stay unchanged; the poller type-asserts this.
// ObservePane blocks until ctx is cancelled or the process ends. onData fires
// when stdout yields bytes. Those bytes are not delivered anywhere else.
type paneObserver interface {
	ObservePane(ctx context.Context, paneID string, onData func()) error
}

// observeRun is one local `herdr terminal session observe` process.
// gen lets a late exit ignore a newer run that reused the pane id.
type observeRun struct {
	gen    uint64
	cancel context.CancelFunc
}

const (
	maxObservers       = 8
	observeBackoff     = 30 * time.Second
	observeReadTimeout = 20 * time.Second
)

// observeBeat coalesces stdout signals for one pane into a single pane read.
// Tests shrink it. Production stays at the plan's short beat.
var observeBeat = 200 * time.Millisecond

// localHost matches herdr's local target ("" or "local"). A missing paneRemote
// entry is not local; callers check the ok result from paneHost first.
func localHost(host string) bool {
	return host == "" || host == "local"
}

func (p *Poller) paneHost(paneID string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	host, ok := p.paneRemote[paneID]
	return host, ok
}

// ensureObserve starts one shared observe process for a subscribed local pane.
// SSH, an unknown host, a client without the command, a full cap, and a
// backoff window all leave the pane on the existing read_pane poll. No error
// is surfaced.
func (p *Poller) ensureObserve(paneID string) {
	obs, ok := p.client.(paneObserver)
	if !ok || paneID == "" {
		return
	}
	host, known := p.paneHost(paneID)
	if !known || !localHost(host) {
		return
	}
	if !p.activeSubscription(paneID) {
		return
	}

	p.observeMu.Lock()
	if _, running := p.observing[paneID]; running {
		p.observeMu.Unlock()
		return
	}
	if until, waiting := p.observeRetry[paneID]; waiting && time.Now().Before(until) {
		p.observeMu.Unlock()
		return
	}
	if len(p.observing) >= maxObservers {
		p.observeMu.Unlock()
		return
	}
	p.observeGen++
	gen := p.observeGen
	ctx, cancel := context.WithCancel(context.Background())
	p.observing[paneID] = &observeRun{gen: gen, cancel: cancel}
	delete(p.observeRetry, paneID)
	p.observeMu.Unlock()

	p.logger.Debug("terminal observe start", "pane", paneID)
	go func() {
		err := obs.ObservePane(ctx, paneID, func() { p.noteObserve(paneID) })
		p.finishObserve(paneID, gen, err)
	}()
}

func (p *Poller) finishObserve(paneID string, gen uint64, err error) {
	p.observeMu.Lock()
	run, ok := p.observing[paneID]
	if !ok || run.gen != gen {
		p.observeMu.Unlock()
		return
	}
	delete(p.observing, paneID)
	if t := p.observeTimers[paneID]; t != nil {
		t.Stop()
		delete(p.observeTimers, paneID)
	}
	delete(p.observePending, paneID)
	unexpected := err != nil && !errors.Is(err, context.Canceled)
	warn := false
	if unexpected {
		p.observeRetry[paneID] = time.Now().Add(observeBackoff)
		if !p.observeWarned[paneID] {
			p.observeWarned[paneID] = true
			warn = true
		}
	}
	p.observeMu.Unlock()
	if !unexpected {
		return
	}
	if warn {
		p.logger.Warn("terminal observe unavailable; this pane stays on poll", "pane", paneID, "err", err)
		return
	}
	p.logger.Debug("terminal observe exited", "pane", paneID, "err", err)
}

// stopObserve cancels the pane's process if one is running. Safe to call when
// nothing is running. The cancel runs after observeMu is released so onData
// can take that lock.
func (p *Poller) stopObserve(paneID string) {
	p.observeMu.Lock()
	run := p.observing[paneID]
	delete(p.observing, paneID)
	if t := p.observeTimers[paneID]; t != nil {
		t.Stop()
		delete(p.observeTimers, paneID)
	}
	delete(p.observePending, paneID)
	p.observeMu.Unlock()
	if run != nil {
		run.cancel()
	}
}

func (p *Poller) stopAllObservers() {
	p.observeMu.Lock()
	ids := make([]string, 0, len(p.observing))
	for id := range p.observing {
		ids = append(ids, id)
	}
	p.observeMu.Unlock()
	for _, id := range ids {
		p.stopObserve(id)
	}
}

// syncObservers matches running processes to live local subscriptions.
// Expired subscribers, SSH panes, and panes whose host is no longer known
// lose their process. Freed cap slots go to waiting local subscribers.
func (p *Poller) syncObservers() {
	if _, ok := p.client.(paneObserver); !ok {
		return
	}
	ttl := p.subscriberTTL()
	now := time.Now()
	p.contentMu.Lock()
	live := make(map[string]bool, len(p.subscribers))
	for id, ts := range p.subscribers {
		if now.Sub(time.Unix(ts, 0)) > ttl {
			delete(p.subscribers, id)
			continue
		}
		live[id] = true
	}
	p.contentMu.Unlock()

	p.observeMu.Lock()
	running := make([]string, 0, len(p.observing))
	for id := range p.observing {
		running = append(running, id)
	}
	p.observeMu.Unlock()

	for _, id := range running {
		host, known := p.paneHost(id)
		if live[id] && known && localHost(host) {
			continue
		}
		p.stopObserve(id)
		if !live[id] {
			p.observeMu.Lock()
			delete(p.observeRetry, id)
			delete(p.observeWarned, id)
			p.observeMu.Unlock()
		}
	}
	for id := range live {
		p.ensureObserve(id)
	}
}

// noteObserve arms one short timer per burst of stdout. Further bytes in that
// window do not schedule another read.
func (p *Poller) noteObserve(paneID string) {
	p.observeMu.Lock()
	defer p.observeMu.Unlock()
	if _, running := p.observing[paneID]; !running || p.observePending[paneID] {
		return
	}
	p.observePending[paneID] = true
	p.observeTimers[paneID] = time.AfterFunc(observeBeat, func() {
		p.fireObserve(paneID)
	})
}

func (p *Poller) fireObserve(paneID string) {
	p.observeMu.Lock()
	delete(p.observePending, paneID)
	delete(p.observeTimers, paneID)
	_, still := p.observing[paneID]
	p.observeMu.Unlock()
	if !still {
		return
	}
	if !p.activeSubscription(paneID) {
		p.stopObserve(paneID)
		return
	}
	p.kickDelta(paneID)
}

// kickDeltaPoll is the poll loop's single-flight delta pass. A pass already
// in flight keeps these panes for one follow-up read. Observe signals queue
// themselves separately with kickDelta and stay forced.
func (p *Poller) kickDeltaPoll(ctx context.Context, panes []herdr.Pane) {
	p.deltaMu.Lock()
	if p.deltaRunning {
		if p.deltaPollWait == nil {
			p.deltaPollWait = make(map[string]herdr.Pane)
		}
		for _, pane := range panes {
			if pane.PaneID == "" {
				continue
			}
			p.deltaPollWait[pane.PaneID] = pane
		}
		p.deltaMu.Unlock()
		return
	}
	p.deltaRunning = true
	p.deltaMu.Unlock()
	cp := append([]herdr.Pane(nil), panes...)
	go p.runDeltas(ctx, cp, nil)
}

// kickDelta forces one pane read. If a pass is already reading, the pane is
// queued and read again when that pass returns, still forced. A pane with no
// known host is not read.
func (p *Poller) kickDelta(paneID string) {
	if paneID == "" || p.hub == nil {
		return
	}
	panes := p.panesForDelta([]string{paneID})
	if len(panes) == 0 {
		return
	}
	p.deltaMu.Lock()
	if p.deltaRunning {
		if p.deltaForce == nil {
			p.deltaForce = make(map[string]bool)
		}
		p.deltaForce[paneID] = true
		p.deltaMu.Unlock()
		return
	}
	p.deltaRunning = true
	p.deltaMu.Unlock()
	go p.runDeltas(context.Background(), panes, map[string]bool{paneID: true})
}

// runDeltas owns deltaRunning until it exits. Observe follow-ups use their
// own timeout so a cancelled poll context cannot drop a signal, and so a
// stuck read cannot hold the single-flight slot for the process lifetime.
func (p *Poller) runDeltas(ctx context.Context, panes []herdr.Pane, force map[string]bool) {
	followUp := force != nil
	for {
		runCtx := ctx
		var cancel context.CancelFunc
		if followUp {
			runCtx, cancel = context.WithTimeout(context.Background(), observeReadTimeout)
		}
		p.pushContentDeltasOpt(runCtx, panes, force)
		if cancel != nil {
			cancel()
		}

		queuedForce, waited, ok := p.takeDeltaFollowUp()
		if !ok {
			return
		}
		seen := make(map[string]bool, len(waited)+len(queuedForce))
		next := make([]herdr.Pane, 0, len(waited)+len(queuedForce))
		for _, pane := range waited {
			if pane.PaneID == "" || seen[pane.PaneID] {
				continue
			}
			seen[pane.PaneID] = true
			next = append(next, pane)
		}
		extra := make([]string, 0)
		for id := range queuedForce {
			if !seen[id] {
				extra = append(extra, id)
			}
		}
		if len(extra) > 0 {
			next = append(next, p.panesForDelta(extra)...)
		}
		panes = next
		// Only observe ids are forced. Poll panes keep the rev/seq skip.
		force = queuedForce
		followUp = true
	}
}

// takeDeltaFollowUp claims the panes queued while a pass was in flight.
// ok is false when nothing is waiting; deltaRunning is cleared in that case.
// The caller must not hold deltaMu.
func (p *Poller) takeDeltaFollowUp() (force map[string]bool, waited []herdr.Pane, ok bool) {
	p.deltaMu.Lock()
	defer p.deltaMu.Unlock()
	if len(p.deltaForce) == 0 && len(p.deltaPollWait) == 0 {
		p.deltaRunning = false
		return nil, nil, false
	}
	force = p.deltaForce
	p.deltaForce = nil
	waited = make([]herdr.Pane, 0, len(p.deltaPollWait))
	for _, pane := range p.deltaPollWait {
		waited = append(waited, pane)
	}
	p.deltaPollWait = nil
	return force, waited, true
}

// newContentEpoch is a non-zero id for this poller process. It rides with gen
// so a restarted relay, whose gen starts again at 1, is not treated as a stale
// snapshot by a client that still holds the previous process's gen.
func newContentEpoch() int {
	epoch := int(time.Now().UnixNano() & 0x7fffffff)
	if epoch == 0 {
		return 1
	}
	return epoch
}

// ObserveSnapshot returns the cached content, herdr revision, monotonic
// content generation, and process epoch for a pane with a live observe
// process, when that cache was read within contentVerFreshFor. Content, rev,
// and gen come from one contentMu hold — the same baseline the next delta
// extends. ok is false when the pane is not observed or the cache is missing
// or stale; callers must not pair a direct read with a cached revision.
// observeMu is dropped before contentMu. A cache that has no generation yet
// is assigned 1 under that lock.
func (p *Poller) ObserveSnapshot(paneID string) (content string, rev int, gen int, epoch int, ok bool) {
	if !p.observingPane(paneID) {
		return "", 0, 0, 0, false
	}
	p.contentMu.Lock()
	defer p.contentMu.Unlock()
	content, contentOK := p.contentCache[paneID]
	rev, revOK := p.contentRevs[paneID]
	if !contentOK || !revOK || time.Since(p.contentAt[paneID]) > contentVerFreshFor {
		return "", 0, 0, 0, false
	}
	gen = p.contentGen[paneID]
	if gen == 0 {
		gen = 1
		p.contentGen[paneID] = 1
	}
	return content, rev, gen, p.contentEpoch, true
}

// WidenObserveBaseline re-reads an observed pane out to `lines` and stores
// that read as the delta baseline. The herdr call happens outside contentMu.
// The window is raised before the read so a concurrent delta read is not
// narrower, and a narrower read already in flight will not replace this one.
// truncated is set only when the request was cut at maxBaselineLines and the
// read filled that cap. ok is false when the pane is not observed, the cache
// is stale, or the read fails.
func (p *Poller) WidenObserveBaseline(ctx context.Context, paneID string, lines int) (content string, rev int, gen int, epoch int, truncated bool, ok bool) {
	if !p.observingPane(paneID) {
		return "", 0, 0, 0, false, false
	}
	if lines < 1 {
		lines = 1
	}
	if lines > maxBaselineLines {
		lines = maxBaselineLines
		truncated = true
	}
	p.contentMu.Lock()
	cached := p.contentCache[paneID]
	cachedLines := visualLines(cached)
	fresh := time.Since(p.contentAt[paneID]) <= contentVerFreshFor
	rev, revOK := p.contentRevs[paneID]
	if fresh && revOK && cachedLines >= lines && p.contentReadLines[paneID] >= lines {
		gen = p.contentGen[paneID]
		if gen == 0 {
			gen = 1
			p.contentGen[paneID] = 1
		}
		epoch = p.contentEpoch
		p.contentMu.Unlock()
		return cached, rev, gen, epoch, false, true
	}
	if p.contentReadLines[paneID] < lines {
		p.contentReadLines[paneID] = lines
	}
	p.contentMu.Unlock()

	read, err := p.client.ReadPane(ctx, p.RemoteForPane(paneID), paneID, lines)
	if err != nil || !p.observingPane(paneID) {
		return "", 0, 0, 0, false, false
	}
	p.contentMu.Lock()
	defer p.contentMu.Unlock()
	rev, revOK = p.contentRevs[paneID]
	if !revOK || time.Since(p.contentAt[paneID]) > contentVerFreshFor {
		return "", 0, 0, 0, false, false
	}
	prev := p.contentCache[paneID]
	baseGen := p.contentGen[paneID]
	gen = baseGen
	if read != prev {
		if baseGen == 0 {
			gen = 1
		} else {
			gen = baseGen + 1
		}
		p.contentGen[paneID] = gen
	}
	if gen == 0 {
		gen = 1
		p.contentGen[paneID] = 1
	}
	p.contentCache[paneID] = read
	p.contentAt[paneID] = time.Now()
	if truncated && visualLines(read) < lines {
		truncated = false
	}
	return read, rev, gen, p.contentEpoch, truncated, true
}

func (p *Poller) observingPane(paneID string) bool {
	p.observeMu.Lock()
	defer p.observeMu.Unlock()
	_, ok := p.observing[paneID]
	return ok
}

func (p *Poller) panesForDelta(ids []string) []herdr.Pane {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]herdr.Pane, 0, len(ids))
	for _, id := range ids {
		found := false
		for i := range p.latest {
			if p.latest[i].PaneID == id {
				out = append(out, p.latest[i])
				found = true
				break
			}
		}
		if found {
			continue
		}
		host, ok := p.paneRemote[id]
		if !ok {
			continue
		}
		label := host
		if localHost(host) {
			label = "local"
		}
		out = append(out, herdr.Pane{PaneID: id, Host: label})
	}
	return out
}
