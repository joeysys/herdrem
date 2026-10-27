package poll

import (
	"context"
	"time"

	"herdrem/internal/herdr"
	"herdrem/internal/protocol"
)

// Local status events are not a second polling loop. The socket callback only
// records pane ids in statusDirty. A 300ms merge swaps that set and does one
// local snapshot. TriggerPoll is not involved.

const (
	statusMergeInterval = 300 * time.Millisecond
	statusReconnectWait = time.Second
	localCalibInterval  = 30 * time.Second
)

// SetStatusWatcher receives status transitions after a snapshot has confirmed
// them. from is the previous status. The first time a pane is seen is not a
// transition. The callback runs on the poller goroutine and must not call
// back into pollOnce or refreshLocal.
func (p *Poller) SetStatusWatcher(fn func(pane herdr.Pane, from, to string)) {
	p.statusMu.Lock()
	p.statusWatcher = fn
	p.statusMu.Unlock()
}

func (p *Poller) runStatusEvents(ctx context.Context) {
	src, ok := p.client.(herdr.LocalStatusSource)
	if !ok {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		subCtx, cancel := context.WithCancel(ctx)
		ids := p.localPaneIDs()
		p.statusMu.Lock()
		gen := p.statusGen
		p.subscribedIDs = append([]string(nil), ids...)
		p.statusCancel = cancel
		p.statusMu.Unlock()
		p.statusHealthy.Store(false)

		err := src.SubscribeLocalStatus(subCtx, ids, herdr.StatusHooks{
			OnReady: p.markStatusReady,
			OnEvent: p.markStatusDirty,
		})
		p.statusHealthy.Store(false)
		p.statusMu.Lock()
		resub := p.statusGen != gen
		if p.statusCancel != nil {
			p.statusCancel = nil
		}
		p.statusMu.Unlock()
		cancel()
		if ctx.Err() != nil {
			return
		}
		if resub {
			continue
		}
		p.logger.Debug("local status subscription ended", "err", err)
		timer := time.NewTimer(p.statusRedial())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *Poller) runStatusMerge(ctx context.Context) {
	if _, ok := p.client.(herdr.LocalStatusSource); !ok {
		return
	}
	ticker := time.NewTicker(p.statusMergeWait())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			dirty := p.swapStatusDirty()
			if len(dirty) == 0 || !p.statusHealthy.Load() {
				continue
			}
			p.refreshLocal(ctx)
		}
	}
}

func (p *Poller) statusMergeWait() time.Duration {
	if p.statusMergeEvery > 0 {
		return p.statusMergeEvery
	}
	return statusMergeInterval
}

func (p *Poller) statusRedial() time.Duration {
	if p.statusRedialWait > 0 {
		return p.statusRedialWait
	}
	return statusReconnectWait
}

func (p *Poller) markStatusReady() {
	p.statusHealthy.Store(true)
	p.markStatusDirty(herdr.StatusEvent{Kind: "subscribed", PaneID: "*"})
}

func (p *Poller) markStatusDirty(ev herdr.StatusEvent) {
	p.statusMu.Lock()
	if p.statusDirty == nil {
		p.statusDirty = make(map[string]struct{})
	}
	id := ev.PaneID
	if id == "" {
		id = "*"
	}
	p.statusDirty[id] = struct{}{}
	p.statusMu.Unlock()
}

func (p *Poller) swapStatusDirty() map[string]struct{} {
	p.statusMu.Lock()
	d := p.statusDirty
	p.statusDirty = nil
	p.statusMu.Unlock()
	return d
}

func (p *Poller) localPaneIDs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var ids []string
	for _, host := range p.hosts {
		if !localHost(host) {
			continue
		}
		for _, pane := range p.hostPanes[host] {
			if pane.PaneID != "" {
				ids = append(ids, pane.PaneID)
			}
		}
	}
	return ids
}

func (p *Poller) localHostKey() (string, bool) {
	for _, host := range p.hosts {
		if localHost(host) {
			return host, true
		}
	}
	return "", false
}

// skipLocalSnapshot is true only while the local event connection is up and
// the last local snapshot is younger than the calibration interval. force
// is the TriggerPoll bit for this cycle: a mutation poll must not keep the
// previous panes. The shared poll ticker is unchanged so SSH hosts stay on
// their normal cadence. Caller holds cycleMu.
func (p *Poller) skipLocalSnapshot(host string, force bool) bool {
	if !localHost(host) || force || !p.statusHealthy.Load() {
		return false
	}
	if p.localCalibAt.IsZero() || time.Since(p.localCalibAt) >= localCalibInterval {
		return false
	}
	return true
}

// retainedPanes keeps the last good local panes in the merged list without
// marking them stale and without putting their host in the polled set.
func (p *Poller) retainedPanes(host string) []herdr.Pane {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]herdr.Pane(nil), p.hostPanes[host]...)
}

func (p *Poller) maybeResubscribe(ids []string) {
	if _, ok := p.client.(herdr.LocalStatusSource); !ok {
		return
	}
	p.statusMu.Lock()
	same := sameStringSet(p.subscribedIDs, ids)
	p.statusMu.Unlock()
	if same {
		return
	}
	p.bumpStatusSub()
}

func (p *Poller) bumpStatusSub() {
	p.statusMu.Lock()
	p.statusGen++
	cancel := p.statusCancel
	p.statusMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func sameStringSet(a, b []string) bool {
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

// refreshLocal is the one local snapshot a coalesce tick is allowed to do.
// It does not snapshot other hosts and does not call TriggerPoll. agents is
// broadcast only when a status string or the pane set changed.
func (p *Poller) refreshLocal(ctx context.Context) {
	if !p.statusHealthy.Load() {
		return
	}
	host, ok := p.localHostKey()
	if !ok {
		return
	}
	p.cycleMu.Lock()
	defer p.cycleMu.Unlock()
	if !p.statusHealthy.Load() {
		return
	}
	p.metrics.mu.Lock()
	p.metrics.snapshots++
	p.metrics.mu.Unlock()
	snap, err := p.client.Snapshot(ctx, host)
	if err != nil {
		p.metrics.mu.Lock()
		p.metrics.snapshotErrs++
		p.metrics.mu.Unlock()
		p.logger.Warn("local status refresh failed", "err", err)
		return
	}
	now := time.Now()
	p.localCalibAt = now
	before := p.statusByID()

	enrichOn := p.lastClientAt != (time.Time{}) && now.Sub(p.lastClientAt) <= p.enrichGraceDur()
	seen := make(map[string]bool)
	localPanes := p.ingest(ctx, host, snap, enrichOn, seen)
	p.mu.Lock()
	p.hostPanes[host] = append([]herdr.Pane(nil), localPanes...)
	var remote []herdr.Pane
	for _, pane := range p.latest {
		if localHost(pane.Host) {
			continue
		}
		remote = append(remote, pane)
	}
	p.mu.Unlock()

	merged := append([]herdr.Pane(nil), localPanes...)
	merged = append(merged, remote...)
	_ = p.pruneRemotes(map[string]bool{host: true}, seen)
	_ = p.applyDoneAcknowledgements(merged)
	p.enrichBranches(ctx, merged)
	p.applyWorkspaceMeta(merged, map[string]map[string]wsMeta{
		hostLabel(host): workspaceMeta(snap.Workspaces),
	}, map[string]map[string]string{
		hostLabel(host): tabLabelIndex(snap.Tabs),
	})
	activeChanged := p.enrichLastActive(merged)
	if activeChanged {
		p.saveState()
	}
	p.confirmContentVersions(merged)
	p.mu.Lock()
	p.latest = merged
	p.mu.Unlock()
	if agentsNeedBroadcast(before, merged) && p.hub != nil {
		p.hub.BroadcastJSON(protocol.AgentsSnapshot{
			Type:   "agents",
			Agents: toAny(merged),
		})
	}
	p.deliverStatusChanges(before, merged)
	p.syncObservers()
	ids := make([]string, 0, len(localPanes))
	for _, pane := range localPanes {
		ids = append(ids, pane.PaneID)
	}
	p.maybeResubscribe(ids)
}

func (p *Poller) ingest(ctx context.Context, host string, snap *herdr.Snapshot, enrichOn bool, seen map[string]bool) []herdr.Pane {
	var hostPanes []herdr.Pane
	if snap == nil {
		return hostPanes
	}
	for i := range snap.Panes {
		pane := &snap.Panes[i]
		if !p.kindAllowed(pane.Kind) {
			continue
		}
		if pane.Host == "" {
			pane.Host = hostLabel(host)
		}
		if isBlocked(pane.Status) {
			if enrichOn {
				p.attachMenu(ctx, host, pane)
			} else {
				p.applyMenuCache(pane)
			}
		} else {
			pane.Options = nil
			pane.OptionLabels = nil
			pane.PromptID = ""
			p.activeMu.Lock()
			delete(p.menuCache, pane.PaneID)
			p.activeMu.Unlock()
		}
		p.setRemote(pane.PaneID, host)
		seen[pane.PaneID] = true
		hostPanes = append(hostPanes, *pane)
	}
	return hostPanes
}

func (p *Poller) statusByID() map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	m := make(map[string]string, len(p.latest))
	for _, pane := range p.latest {
		m[pane.PaneID] = pane.Status
	}
	return m
}

// agentsNeedBroadcast is the event-path rule: a status string changed, or a
// pane was created or closed. A revision-only snapshot does not broadcast.
func agentsNeedBroadcast(before map[string]string, panes []herdr.Pane) bool {
	if len(before) != len(panes) {
		return true
	}
	seen := make(map[string]bool, len(panes))
	for _, pane := range panes {
		prev, ok := before[pane.PaneID]
		if !ok || prev != pane.Status {
			return true
		}
		seen[pane.PaneID] = true
	}
	return len(seen) != len(before)
}

func (p *Poller) deliverStatusChanges(before map[string]string, panes []herdr.Pane) {
	p.statusMu.Lock()
	fn := p.statusWatcher
	p.statusMu.Unlock()
	if fn == nil {
		return
	}
	for i := range panes {
		if panes[i].Stale {
			continue
		}
		prev, ok := before[panes[i].PaneID]
		if !ok || prev == "" || prev == panes[i].Status {
			continue
		}
		fn(panes[i], prev, panes[i].Status)
	}
}
