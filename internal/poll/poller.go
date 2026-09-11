package poll

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"herdrem/internal/herdr"
	"herdrem/internal/hub"
	"herdrem/internal/protocol"
	"herdrem/internal/service"
)

// Poller periodically polls herdr (per host) and broadcasts agents snapshots.
// It also maintains a pane→host routing map for command dispatch.
type Poller struct {
	client herdr.Client
	hosts  []string        // "local" first, then SSH targets
	kinds  map[string]bool // RELAY_KINDS filter; empty = all
	hub    *hub.Hub
	logger *slog.Logger
	intvl  time.Duration

	mu          sync.RWMutex
	paneRemote  map[string]string       // pane_id → host
	latest      []herdr.Pane            // last merged snapshot (for cwd roots)
	hostPanes   map[string][]herdr.Pane // host → panes from its last successful snapshot (for stale retention)
	hostNextTry map[string]time.Time    // host → next allowed snapshot attempt (failure backoff)

	// branch cache: cwd → branch name, refreshed every 15s
	branchMu     sync.Mutex
	branchCache  map[string]string    // cwd → branch
	branchExpiry map[string]time.Time // cwd → expiry time
	branchLast   time.Time

	// last active tracking: pane_id → epoch seconds of last observed activity
	activeMu            sync.Mutex
	prevRevisions       map[string]int            // pane_id → last seen revision
	prevSeq             map[string]int            // pane_id → last seen state_change_seq
	lastActive          map[string]int64          // pane_id → epoch seconds
	acknowledgedDone    map[string]bool           // pane_id → done seen by a relay client
	prevValidPathsRevs  map[string]int            // pane_id → last checked revision for valid paths
	prevValidPathsSeq   map[string]int            // pane_id → last checked state change sequence for valid paths
	cachedValidPaths    map[string][]string       // pane_id → cached valid paths
	missingPathItems    map[string][]panePathItem // pane_id → candidates that failed the last existence check
	validPathsLastCheck map[string]time.Time      // pane_id → last valid paths check time
	menuCache           map[string]menuEntry      // pane_id → last parsed blocked-menu (keyed by rev+seq+agent)
	menuGen             map[string]int64          // pane_id → generation, bumped by InvalidateMenu to invalidate in-flight reads

	// incremental terminal content: pane_id → last seen content snapshot
	contentMu       sync.Mutex
	contentCache    map[string]string       // pane_id → last full content
	contentRevs     map[string]int          // pane_id → revision of the cached content
	contentSeqs     map[string]int          // pane_id → state_change_seq of the cached content
	contentAt       map[string]time.Time    // pane_id → when the cached content was last read
	contentVerAt    map[string]time.Time    // pane_id → when the cached rev/seq was last confirmed by a snapshot
	subscribers     map[string]int64        // pane_id → unix ts of last read_pane request (TTL-based subscription)
	previewCache    map[string]previewEntry // pane_id → short-lived unversioned preview read
	previewInflight map[string]*previewCall // pane_id → in-flight preview read (singleflight)

	// client-connection driven enrichment gating: path validation, blocked-menu
	// reads, and terminal delta reads are display work — they pause when no
	// client has been connected for enrichmentGrace. pollOnce-only fields.
	lastClientAt   time.Time // last poll cycle that saw hub.Count() > 0
	pathsSkipped   bool      // path validation was skipped last cycle (reconnect → force recheck)
	pathRecheckAll bool      // one-shot: bypass the version-unchanged skip for all panes

	// single-flight guard so a slow pushContentDeltas goroutine is never
	// overlapped by the next poll cycle: the previous run must finish
	// (including its slow reads and cache writes) before a new one starts.
	deltaMu      sync.Mutex
	deltaRunning bool

	// file existence checker (set after construction to avoid circular dep)
	fileSvc   existsChecker
	fileSvcMu sync.RWMutex

	// trigger channel for immediate poll (e.g. after AckDone)
	trigger chan struct{}

	// persistence: state file path and in-process writer serialization
	statePath   string
	stateFileMu sync.Mutex

	metrics pollMetrics
}

// pollMetrics counts upstream calls and cache hits so CPU reductions are
// verifiable from logs rather than assumed (plan §2.3 baseline). Written from
// pollOnce / the delta goroutine; read+reset never — cumulative for the
// process lifetime, sampled by the periodic log line.
type pollMetrics struct {
	mu           sync.Mutex
	cycles       int64 // poll cycles completed
	snapshots    int64 // api snapshot calls attempted
	snapshotErrs int64 // api snapshot calls failed
	paneReads    int64 // ReadPane calls (path scans + menu reads + delta reads)
	pathSkips    int64 // valid-paths cycles skipped via unchanged version
	deltaSkips   int64 // delta cycles skipped via unchanged rev/seq
	seqZeroAgent int   // agent panes whose state_change_seq is still 0 (signal health gauge)
}

// existsChecker is the subset of service.FileService the poller needs —
// narrowed to an interface so tests can count Exists calls.
type existsChecker interface {
	Exists(ctx context.Context, host, path string) bool
}

// menuEntry is a cached blocked-menu parse keyed to the pane's version.
// It is only served while rev+seq+agent still match the current pane, so a
// state change always forces a fresh read — stale approval options are never
// shown just to save CPU.
type menuEntry struct {
	opts   []string
	labels []string
	prompt string
	rev    int
	seq    int
	agent  string
	at     time.Time
}

// previewEntry is a short-lived unversioned preview read. Deliberately
// separate from contentCache: an 8-line tail must never overwrite the
// 10000-line delta baseline.
type previewEntry struct {
	text string
	at   time.Time
}

// previewCall is an in-flight preview read shared by concurrent callers.
type previewCall struct {
	done chan struct{}
	text string
	err  error
}

// New creates a Poller. hosts must include "local"; remotes are SSH targets.
// kinds filters panes by Kind (empty = no filter).
func New(client herdr.Client, hosts []string, kinds map[string]bool, h *hub.Hub, logger *slog.Logger, interval time.Duration) *Poller {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = 3 * time.Second
	}
	if len(hosts) == 0 {
		hosts = []string{"local"}
	}
	return &Poller{
		client:              client,
		hosts:               hosts,
		kinds:               kinds,
		hub:                 h,
		logger:              logger,
		intvl:               interval,
		paneRemote:          make(map[string]string),
		hostPanes:           make(map[string][]herdr.Pane),
		hostNextTry:         make(map[string]time.Time),
		branchCache:         make(map[string]string),
		branchExpiry:        make(map[string]time.Time),
		prevRevisions:       make(map[string]int),
		prevSeq:             make(map[string]int),
		lastActive:          make(map[string]int64),
		acknowledgedDone:    make(map[string]bool),
		prevValidPathsRevs:  make(map[string]int),
		prevValidPathsSeq:   make(map[string]int),
		cachedValidPaths:    make(map[string][]string),
		missingPathItems:    make(map[string][]panePathItem),
		validPathsLastCheck: make(map[string]time.Time),
		menuCache:           make(map[string]menuEntry),
		menuGen:             make(map[string]int64),
		contentCache:        make(map[string]string),
		contentRevs:         make(map[string]int),
		contentSeqs:         make(map[string]int),
		contentAt:           make(map[string]time.Time),
		contentVerAt:        make(map[string]time.Time),
		subscribers:         make(map[string]int64),
		previewCache:        make(map[string]previewEntry),
		previewInflight:     make(map[string]*previewCall),
		trigger:             make(chan struct{}, 1),
	}
}

// SetFileService connects the file service for async file-existence checks.
func (p *Poller) SetFileService(fs existsChecker) {
	p.fileSvcMu.Lock()
	p.fileSvc = fs
	p.fileSvcMu.Unlock()
}

// SetStatePath configures a JSON file for persisting poller state across relay
// restarts. Call before Run(). Loads existing state if present.
func (p *Poller) SetStatePath(path string) {
	p.statePath = path
	p.loadState()
}

// persistedState is the JSON shape for the state file.
type persistedState struct {
	LastActive       map[string]int64 `json:"last_active"`
	PrevRevisions    map[string]int   `json:"prev_revisions"`
	PrevSeq          map[string]int   `json:"prev_seq"`
	AcknowledgedDone map[string]bool  `json:"acknowledged_done,omitempty"`
}

func (p *Poller) loadState() {
	if p.statePath == "" {
		return
	}
	b, err := os.ReadFile(p.statePath)
	if err != nil {
		if !os.IsNotExist(err) {
			p.logger.Warn("load poller state", "err", err)
		}
		return
	}
	var ps persistedState
	if err := json.Unmarshal(b, &ps); err != nil {
		p.logger.Warn("parse poller state", "err", err)
		return
	}
	p.activeMu.Lock()
	for k, v := range ps.LastActive {
		p.lastActive[k] = v
	}
	for k, v := range ps.PrevRevisions {
		p.prevRevisions[k] = v
	}
	for k, v := range ps.PrevSeq {
		p.prevSeq[k] = v
	}
	for k, v := range ps.AcknowledgedDone {
		p.acknowledgedDone[k] = v
	}
	p.activeMu.Unlock()
	p.logger.Debug("loaded poller state", "panes", len(ps.LastActive))
}

func (p *Poller) saveState() {
	if p.statePath == "" {
		return
	}
	p.stateFileMu.Lock()
	defer p.stateFileMu.Unlock()

	p.activeMu.Lock()
	ps := persistedState{
		LastActive:       make(map[string]int64, len(p.lastActive)),
		PrevRevisions:    make(map[string]int, len(p.prevRevisions)),
		PrevSeq:          make(map[string]int, len(p.prevSeq)),
		AcknowledgedDone: make(map[string]bool, len(p.acknowledgedDone)),
	}
	for k, v := range p.lastActive {
		ps.LastActive[k] = v
	}
	for k, v := range p.prevRevisions {
		ps.PrevRevisions[k] = v
	}
	for k, v := range p.prevSeq {
		ps.PrevSeq[k] = v
	}
	for k, v := range p.acknowledgedDone {
		ps.AcknowledgedDone[k] = v
	}
	p.activeMu.Unlock()

	b, err := json.Marshal(ps)
	if err != nil {
		p.logger.Warn("marshal poller state", "err", err)
		return
	}
	dir := filepath.Dir(p.statePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		p.logger.Warn("mkdir poller state", "err", err)
		return
	}
	tmp, err := os.CreateTemp(dir, ".tmp-poller-*.json")
	if err != nil {
		p.logger.Warn("create poller state tmp", "err", err)
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		p.logger.Warn("write poller state", "err", err)
		return
	}
	tmp.Close()
	if err := os.Rename(tmpPath, p.statePath); err != nil {
		os.Remove(tmpPath)
		p.logger.Warn("rename poller state", "err", err)
	}
}

// AckDone records that a relay client has seen a completed pane, then triggers
// an immediate poll. Herdr only marks done panes seen when its own UI focuses
// them, so remote clients keep their acknowledgement here.
func (p *Poller) AckDone(paneID string) bool {
	p.mu.RLock()
	valid := false
	for _, pane := range p.latest {
		if pane.PaneID == paneID && pane.Kind == "agent" && strings.EqualFold(pane.Status, "done") {
			valid = true
			break
		}
	}
	p.mu.RUnlock()
	if !valid {
		return false
	}

	p.activeMu.Lock()
	p.acknowledgedDone[paneID] = true
	p.activeMu.Unlock()
	p.saveState()

	// Non-blocking send to trigger immediate poll
	select {
	case p.trigger <- struct{}{}:
	default:
	}
	return true
}

// RemoteForPane implements herdr.PaneRouter.
func (p *Poller) RemoteForPane(paneID string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.paneRemote[paneID]
}

// Snapshot returns a copy of the latest merged pane list for new WS clients.
func (p *Poller) Snapshot() []herdr.Pane {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]herdr.Pane, len(p.latest))
	copy(out, p.latest)
	return out
}

// CwdRoots returns the distinct working directories of panes on a host — the
// allowed roots for the file sandbox.
func (p *Poller) CwdRoots(host string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	want := hostLabel(host)
	seen := make(map[string]bool)
	var roots []string
	for _, pane := range p.latest {
		if pane.Host != want || pane.Cwd == "" || seen[pane.Cwd] {
			continue
		}
		seen[pane.Cwd] = true
		roots = append(roots, pane.Cwd)
	}
	return roots
}

// Run starts the poll loop. Blocks until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.intvl)
	defer ticker.Stop()

	p.pollOnce(ctx)
	for {
		select {
		case <-ticker.C:
			p.pollOnce(ctx)
		case <-p.trigger:
			p.pollOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// enrichmentGrace is how long display-oriented enrichment (path validation,
// blocked-menu reads, terminal delta reads) keeps running after the last WS
// client disconnects. Base snapshot polling is NOT slowed — pane state keeps
// updating every cycle so reconnecting clients and HTTP routes stay current.
const enrichmentGrace = 30 * time.Second

// hostRetryBackoff bounds how often a failed host's `api snapshot` is
// retried. During backoff the host's last successful panes are retained with
// stale=true so pane routing, cwd roots, and cached state are not treated as
// deleted.
const hostRetryBackoff = 30 * time.Second

func (p *Poller) pollOnce(ctx context.Context) {
	var merged []herdr.Pane
	seen := make(map[string]bool)
	wsLabels := make(map[string]map[string]string) // host → workspace_id → label

	now := time.Now()
	if p.hub != nil && p.hub.Count() > 0 {
		if p.pathsSkipped {
			// First cycle with clients after a paused stretch: force a full
			// path recheck so the reconnecting client does not inherit a stale
			// cached path list.
			p.pathRecheckAll = true
		}
		p.pathsSkipped = false
		p.lastClientAt = now
	}
	enrichOn := p.lastClientAt != (time.Time{}) && now.Sub(p.lastClientAt) <= enrichmentGrace

	for _, host := range p.hosts {
		if t, ok := p.hostNextTry[host]; ok && now.Before(t) {
			merged = append(merged, p.stalePanes(host, seen)...)
			continue
		}
		p.metrics.mu.Lock()
		p.metrics.snapshots++
		p.metrics.mu.Unlock()
		snap, err := p.client.Snapshot(ctx, host)
		if err != nil {
			p.metrics.mu.Lock()
			p.metrics.snapshotErrs++
			p.metrics.mu.Unlock()
			p.logger.Warn("snapshot failed", "host", hostLabel(host), "err", err)
			p.hostNextTry[host] = now.Add(hostRetryBackoff)
			merged = append(merged, p.stalePanes(host, seen)...)
			continue
		}
		delete(p.hostNextTry, host)
		wsLabels[hostLabel(host)] = workspaceLabels(snap.Workspaces)

		var hostPanes []herdr.Pane
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
				p.activeMu.Lock()
				delete(p.menuCache, pane.PaneID)
				p.activeMu.Unlock()
			}
			p.setRemote(pane.PaneID, host)
			seen[pane.PaneID] = true
			merged = append(merged, *pane)
			hostPanes = append(hostPanes, *pane)
		}
		p.mu.Lock()
		p.hostPanes[host] = hostPanes
		p.mu.Unlock()
	}

	stateChanged := p.pruneRemotes(seen)
	stateChanged = p.applyDoneAcknowledgements(merged) || stateChanged

	// Enrich panes with git branch info (cached, refreshed every 15s)
	p.enrichBranches(ctx, merged)

	// Apply workspace labels collected from the same snapshots (no extra call)
	p.applyWorkspaceLabels(merged, wsLabels)

	// Track last active time from pane revisions and agent state
	activeChanged := p.enrichLastActive(merged)
	if activeChanged || stateChanged {
		p.saveState()
	}

	// Confirm or invalidate cached terminal content against the versions just
	// seen upstream — the freshness signal for ContentSnapshot is confirmed
	// versions, not elapsed read time.
	p.confirmContentVersions(merged)

	// Health gauge: agent panes still missing state_change_seq mean the merge
	// is not delivering the signal the skip logic relies on.
	seqZero := 0
	for i := range merged {
		if merged[i].Kind == "agent" && merged[i].StateChangeSeq == 0 {
			seqZero++
		}
	}
	p.metrics.mu.Lock()
	p.metrics.seqZeroAgent = seqZero
	p.metrics.cycles++
	cycles, snapshots, snapshotErrs := p.metrics.cycles, p.metrics.snapshots, p.metrics.snapshotErrs
	paneReads, pathSkips, deltaSkips := p.metrics.paneReads, p.metrics.pathSkips, p.metrics.deltaSkips
	p.metrics.mu.Unlock()
	if cycles%20 == 0 {
		p.logger.Info("poll metrics",
			"cycles", cycles, "snapshots", snapshots, "snapshot_errs", snapshotErrs,
			"pane_reads", paneReads, "path_skips", pathSkips, "delta_skips", deltaSkips,
			"seq_zero_agents", seqZero)
	}

	// Update p.latest with current snapshot before enrichValidPaths so CwdRoots works for safe-path checks
	p.mu.Lock()
	prevSnapshot := p.latest
	p.latest = merged
	p.mu.Unlock()

	if enrichOn {
		// Async file-existence check: scan pane content for file paths and verify
		// they exist on disk.
		p.enrichValidPaths(ctx, merged)

		// Push incremental terminal content deltas for changed panes. Runs
		// asynchronously so a slow remote read (ssh) can never stall the whole
		// poll loop, but is single-flight: if the previous run is still reading,
		// this cycle skips rather than overlapping it.
		if p.tryStartDelta() {
			go func() {
				defer p.deltaDone()
				p.pushContentDeltas(ctx, merged)
			}()
		}
	} else {
		// No connected clients for a while: skip the expensive display work but
		// keep the cached path list on the panes so broadcasts stay consistent.
		p.applyCachedValidPaths(merged)
		p.pathsSkipped = true
	}

	// Only broadcast if the snapshot actually changed — avoids waking up
	// all WS clients when nothing happened.
	p.mu.Lock()
	changed := panesChanged(prevSnapshot, merged)
	p.mu.Unlock()

	if changed {
		p.hub.BroadcastJSON(protocol.AgentsSnapshot{
			Type:   "agents",
			Agents: toAny(merged),
		})
	}
}

// stalePanes returns the retained panes of a host whose snapshot just failed,
// marked stale so clients can tell they are not fresh. The panes stay in
// `seen` so pruneRemotes does not delete their routing and caches.
func (p *Poller) stalePanes(host string, seen map[string]bool) []herdr.Pane {
	p.mu.RLock()
	prev := append([]herdr.Pane(nil), p.hostPanes[host]...)
	p.mu.RUnlock()
	p.mu.Lock()
	for i := range prev {
		p.paneRemote[prev[i].PaneID] = host
		seen[prev[i].PaneID] = true
		prev[i].Stale = true
	}
	p.mu.Unlock()
	return prev
}

// workspaceLabels flattens a snapshot's workspaces into workspace_id → label.
func workspaceLabels(wss []herdr.Workspace) map[string]string {
	m := make(map[string]string, len(wss))
	for _, ws := range wss {
		m[ws.WorkspaceID] = ws.Label
	}
	return m
}

// pathCandidateRe matches potential file paths in terminal text.
// Supports file://, /, ~/, ./, ../, relative paths with slashes (dir/file.ext),
// or filenames with any extension, or build/config filenames.
var pathCandidateRe = regexp.MustCompile(`(?:file://)?(?:(?:/|~/|\./|\.\./|@/)?[\w.+~=@#%$-]+(?:/[\w.+~=@#%$-]+)+|[\w.+~=@#%$-]+\.[a-zA-Z0-9]+|Makefile|Dockerfile|LICENSE|go\.mod|go\.sum|Cargo\.toml)(?::\d+){0,2}`)

var versionOrIPRe = regexp.MustCompile(`^(?:v?\d+\.\d+(?:\.\d+)*|\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})$`)

type panePathItem struct {
	rel string
	dir string
	abs string
}

var softWrapRe = regexp.MustCompile(`([\w.+~=@#%$/-]+)[\r\n]+\s*((?:\.[a-zA-Z0-9]+)|(?:[\w+~=@#%$/-]+\.[a-zA-Z0-9]+))`)

const maxValidPathChecks = 200

var defaultCommonDirs = []string{
	"docs/agents/reviews",
	"docs/agents/exec",
	"docs/agents/notes",
	"docs/agents/research",
	"docs/designs",
	"docs/plans",
	"docs/tasks",
	"docs/adr",
	"docs",
	"web/src",
	"src",
	"internal",
	"cmd",
}

// lineColRe strips trailing :line[:col] suffixes from path candidates.
var lineColRe = regexp.MustCompile(`:\d+`)

// pathCheckWorkers bounds the global fan-out of file-existence checks.
// Previously each pane spawned a goroutine per candidate path — dozens of
// concurrent stat/ssh calls per cycle. A fixed worker pool keeps throughput
// while capping process count.
const pathCheckWorkers = 8

// enrichValidPaths reads pane content, extracts file paths, and checks which
// exist on disk. Version-unchanged panes reuse the cached result; panes with
// previously-missing candidates re-check only those missing items after a
// short expiry instead of re-reading and re-parsing 500 lines. Existence
// checks run through a fixed worker pool.
func (p *Poller) enrichValidPaths(ctx context.Context, panes []herdr.Pane) {
	p.fileSvcMu.RLock()
	fs := p.fileSvc
	p.fileSvcMu.RUnlock()
	if fs == nil {
		return
	}

	// paneCheck is one pane's existence-check batch. missingOnly means items
	// are the previously-failed candidates being re-probed (no fresh parse).
	type paneCheck struct {
		idx         int
		host        string
		items       []panePathItem
		missingOnly bool
	}
	var checks []paneCheck

	p.activeMu.Lock()
	now := time.Now()
	force := p.pathRecheckAll
	p.pathRecheckAll = false
	for i := range panes {
		pane := &panes[i]
		prevRev, seen := p.prevValidPathsRevs[pane.PaneID]
		prevStateSeq := p.prevValidPathsSeq[pane.PaneID]
		cachedValid := p.cachedValidPaths[pane.PaneID]
		lastCheck := p.validPathsLastCheck[pane.PaneID]
		missingItems := p.missingPathItems[pane.PaneID]

		// Version unchanged: reuse the cached list. When some candidates were
		// missing last time, re-probe just those after a 5s expiry (files an
		// agent creates right after printing their path) — no re-read, no
		// regex, no re-check of already-valid paths.
		if !force && seen && pane.Revision == prevRev && pane.StateChangeSeq == prevStateSeq {
			if len(missingItems) == 0 || now.Sub(lastCheck) < 5*time.Second {
				pane.ValidPaths = cachedValid
				p.metrics.mu.Lock()
				p.metrics.pathSkips++
				p.metrics.mu.Unlock()
				continue
			}
			checks = append(checks, paneCheck{idx: i, host: pane.Host, items: missingItems, missingOnly: true})
			continue
		}

		// Read pane content — match the frontend's read size (500 lines) so
		// all visible file paths are checked, not just the last 50 lines.
		content, err := p.client.ReadPane(ctx, pane.Host, pane.PaneID, 500)
		p.metrics.mu.Lock()
		p.metrics.paneReads++
		p.metrics.mu.Unlock()
		if err != nil {
			pane.ValidPaths = cachedValid
			continue
		}
		text := herdr.StripANSI(content)
		unwrapped := softWrapRe.ReplaceAllString(text, "$1$2\n$1$2")
		matches := pathCandidateRe.FindAllString(text, -1)
		matches = append(matches, pathCandidateRe.FindAllString(unwrapped, -1)...)

		if len(matches) == 0 {
			valid := []string{}
			pane.ValidPaths = valid
			p.cachedValidPaths[pane.PaneID] = valid
			p.missingPathItems[pane.PaneID] = nil
			p.prevValidPathsRevs[pane.PaneID] = pane.Revision
			p.prevValidPathsSeq[pane.PaneID] = pane.StateChangeSeq
			p.validPathsLastCheck[pane.PaneID] = now
			continue
		}

		// Extract directory contexts from matches and defaults
		dirSeen := make(map[string]bool)
		var dirContexts []string
		for _, d := range defaultCommonDirs {
			if !dirSeen[d] {
				dirSeen[d] = true
				dirContexts = append(dirContexts, d)
			}
		}
		for _, m := range matches {
			if strings.Contains(m, "/") {
				dir := filepath.Dir(strings.TrimPrefix(m, "file://"))
				dir = strings.TrimPrefix(dir, "./")
				if dir != "." && dir != "/" && dir != "" && !dirSeen[dir] {
					dirSeen[dir] = true
					dirContexts = append(dirContexts, dir)
				}
			}
		}

		// Deduplicate and resolve relative paths against cwd and directory contexts.
		// Two-pass: (1) add every match's primary candidate first so the cap
		// never drops a direct path in favour of a speculative dir-context
		// probe; (2) for bare filenames only, fill remaining budget with
		// dir-context probes. This keeps the newest terminal paths (often at
		// the bottom of the pane) from being truncated by expansion of earlier
		// bare filenames.
		seenPath := make(map[string]bool)
		var items []panePathItem
		var bareFilenames []string
		for _, m := range matches {
			if strings.HasPrefix(m, "http://") || strings.HasPrefix(m, "https://") {
				continue
			}
			raw := strings.TrimPrefix(m, "file://")
			raw = strings.TrimPrefix(raw, "./")
			clean := lineColRe.ReplaceAllString(raw, "")
			clean = strings.Trim(clean, " \t\r\n'\"`()[]{}<>:;,：「」『』《》【】、，。")
			if clean == "" || versionOrIPRe.MatchString(clean) {
				continue
			}

			// Pass 1 — primary candidate for every match.
			abs := clean
			if !strings.HasPrefix(clean, "/") && !strings.HasPrefix(clean, "~") {
				if pane.Cwd != "" {
					abs = filepath.Join(pane.Cwd, clean)
				}
			}
			if !seenPath[abs] {
				seenPath[abs] = true
				items = append(items, panePathItem{rel: clean, abs: abs})
			}

			// Collect bare filenames (no slash, not absolute/home) for pass 2.
			if !strings.HasPrefix(clean, "/") && !strings.HasPrefix(clean, "~") && !strings.Contains(clean, "/") {
				basename := filepath.Base(clean)
				if !seenPath[basename] {
					seenPath[basename] = true
					bareFilenames = append(bareFilenames, basename)
				}
			}
		}

		// Pass 2 — dir-context probes for bare filenames, until the cap is reached.
		// Primary candidates (pass 1) are not capped: they are bounded by the
		// number of regex matches in the 500-line read and each is a single
		// stat() call. Only the speculative dir-context expansion is capped.
		for _, filename := range bareFilenames {
			if len(items) >= maxValidPathChecks {
				break
			}
			for _, dir := range dirContexts {
				if len(items) >= maxValidPathChecks {
					break
				}
				ctxRel := filepath.Join(dir, filename)
				ctxAbs := filepath.Join(pane.Cwd, ctxRel)
				if !seenPath[ctxAbs] {
					seenPath[ctxAbs] = true
					items = append(items, panePathItem{rel: filename, dir: ctxRel, abs: ctxAbs})
				}
			}
		}
		if len(items) == 0 {
			valid := []string{}
			pane.ValidPaths = valid
			p.cachedValidPaths[pane.PaneID] = valid
			p.missingPathItems[pane.PaneID] = nil
			p.prevValidPathsRevs[pane.PaneID] = pane.Revision
			p.prevValidPathsSeq[pane.PaneID] = pane.StateChangeSeq
			p.validPathsLastCheck[pane.PaneID] = now
			continue
		}
		checks = append(checks, paneCheck{idx: i, host: pane.Host, items: items})
	}
	p.activeMu.Unlock()

	if len(checks) == 0 {
		return
	}

	// Bounded existence check: a fixed worker pool drains a flattened job
	// list across all panes, so N panes × M candidates can never exceed
	// pathCheckWorkers concurrent stat/ssh calls.
	type existsJob struct {
		check int
		item  panePathItem
	}
	type existsRes struct {
		check int
		item  panePathItem
		ok    bool
	}
	total := 0
	for _, c := range checks {
		total += len(c.items)
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	jobs := make(chan existsJob)
	results := make(chan existsRes, total)
	workers := pathCheckWorkers
	if total < workers {
		workers = total
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				results <- existsRes{check: j.check, item: j.item, ok: fs.Exists(checkCtx, checks[j.check].host, j.item.abs)}
			}
		}()
	}
	for ci, c := range checks {
		for _, item := range c.items {
			jobs <- existsJob{check: ci, item: item}
		}
	}
	close(jobs)
	wg.Wait()
	close(results)

	// Group results per check, keyed by abs (items are unique per check).
	perCheck := make([]map[string]bool, len(checks))
	for r := range results {
		if perCheck[r.check] == nil {
			perCheck[r.check] = make(map[string]bool)
		}
		perCheck[r.check][r.item.abs] = r.ok
	}

	p.activeMu.Lock()
	checkNow := time.Now()
	for ci, c := range checks {
		pane := &panes[c.idx]
		var stillMissing []panePathItem
		valid := []string{}
		seenValid := make(map[string]bool)
		for _, item := range c.items {
			if !perCheck[ci][item.abs] {
				stillMissing = append(stillMissing, item)
				continue
			}
			for _, s := range []string{item.abs, item.rel, item.dir} {
				if s != "" && !seenValid[s] {
					seenValid[s] = true
					valid = append(valid, s)
				}
			}
		}
		if c.missingOnly {
			// Re-probe of previously-missing candidates only: merge the newly
			// found paths into the cached list; do not re-derive it.
			if len(valid) > 0 {
				merged := append(append([]string{}, p.cachedValidPaths[pane.PaneID]...), valid...)
				p.cachedValidPaths[pane.PaneID] = merged
				pane.ValidPaths = merged
			} else {
				pane.ValidPaths = p.cachedValidPaths[pane.PaneID]
			}
			p.missingPathItems[pane.PaneID] = stillMissing
			p.validPathsLastCheck[pane.PaneID] = checkNow
			continue
		}
		pane.ValidPaths = valid
		p.cachedValidPaths[pane.PaneID] = valid
		p.missingPathItems[pane.PaneID] = stillMissing
		p.prevValidPathsRevs[pane.PaneID] = pane.Revision
		p.prevValidPathsSeq[pane.PaneID] = pane.StateChangeSeq
		p.validPathsLastCheck[pane.PaneID] = checkNow
	}
	p.activeMu.Unlock()
}

// applyCachedValidPaths copies the last computed valid-path lists onto panes
// when validation is paused (no connected clients), so a broadcast snapshot
// does not silently lose the field.
func (p *Poller) applyCachedValidPaths(panes []herdr.Pane) {
	p.activeMu.Lock()
	defer p.activeMu.Unlock()
	for i := range panes {
		if v, ok := p.cachedValidPaths[panes[i].PaneID]; ok {
			panes[i].ValidPaths = v
		}
	}
}

// panesChanged compares two pane slices by a lightweight fingerprint
// (pane_id + status + last_active + branch + options). Order-insensitive.
func panesChanged(prev, cur []herdr.Pane) bool {
	if len(prev) != len(cur) {
		return true
	}
	fp := func(p herdr.Pane) string {
		return p.PaneID + ":" + p.Status + ":" + strconv.FormatInt(p.LastActive, 10) + ":" + p.Branch + ":" + p.WorkspaceLabel + ":" + p.Prompt + ":" + strings.Join(p.Options, ",") + ":" + strings.Join(p.OptionLabels, "|") + ":" + strconv.Itoa(len(p.ValidPaths)) + ":" + strconv.FormatBool(p.Stale)
	}
	seen := make(map[string]struct{}, len(cur))
	for _, p := range cur {
		seen[fp(p)] = struct{}{}
	}
	for _, p := range prev {
		if _, ok := seen[fp(p)]; !ok {
			return true
		}
	}
	return false
}

// menuCacheTTL is the bounded backstop for menu reuse: while a pane's
// rev/seq/agent match the cache key the parsed menu is served without a
// re-read, and this TTL forces a periodic refresh anyway in case herdr ever
// changes the screen without bumping a version counter.
const menuCacheTTL = 30 * time.Second

// attachMenu reads a blocked pane and parses its on-screen menu so clients get
// real approve buttons (options + labels). Results are cached keyed by
// pane_id + revision + state_change_seq + agent: an unchanged version means
// the screen is identical, so re-reading it every poll is pure cost.
func (p *Poller) attachMenu(ctx context.Context, host string, pane *herdr.Pane) {
	p.activeMu.Lock()
	if e, ok := p.menuCache[pane.PaneID]; ok &&
		e.rev == pane.Revision && e.seq == pane.StateChangeSeq && e.agent == pane.Agent &&
		time.Since(e.at) < menuCacheTTL {
		pane.Options = e.opts
		pane.OptionLabels = e.labels
		if pane.Prompt == "" {
			pane.Prompt = e.prompt
		}
		p.activeMu.Unlock()
		return
	}
	// Snapshot the pane's generation before the read: InvalidateMenu bumps it,
	// so a read that raced a send must not be written back — its content
	// predates the response even though the rev/seq key still matches.
	gen := p.menuGen[pane.PaneID]
	p.activeMu.Unlock()

	content, err := p.client.ReadPane(ctx, host, pane.PaneID, 30)
	p.metrics.mu.Lock()
	p.metrics.paneReads++
	p.metrics.mu.Unlock()
	if err != nil {
		p.logger.Debug("menu read failed", "pane", pane.PaneID, "err", err)
		return
	}
	text := herdr.StripANSI(content)
	opts, labels := herdr.DetectMenu(text, pane.Agent)
	pane.Options = opts
	pane.OptionLabels = labels
	if pane.Prompt == "" {
		pane.Prompt = promptExcerpt(text)
	}

	p.activeMu.Lock()
	if p.menuGen[pane.PaneID] == gen {
		p.menuCache[pane.PaneID] = menuEntry{
			opts: opts, labels: labels, prompt: pane.Prompt,
			rev: pane.Revision, seq: pane.StateChangeSeq, agent: pane.Agent, at: time.Now(),
		}
	}
	p.activeMu.Unlock()
}

// applyMenuCache serves the cached menu when enrichment is paused (no
// connected clients): correct options remain on the pane for the next
// broadcast, but nothing is read from herdr. A version-mismatched or absent
// entry leaves the pane untouched rather than risking stale options.
func (p *Poller) applyMenuCache(pane *herdr.Pane) {
	p.activeMu.Lock()
	defer p.activeMu.Unlock()
	e, ok := p.menuCache[pane.PaneID]
	if !ok || e.rev != pane.Revision || e.seq != pane.StateChangeSeq || e.agent != pane.Agent {
		return
	}
	pane.Options = e.opts
	pane.OptionLabels = e.labels
	if pane.Prompt == "" {
		pane.Prompt = e.prompt
	}
}

// InvalidateMenu drops the cached menu for a pane and bumps its generation,
// so any menu read already in flight is discarded instead of repopulating the
// cache with pre-send content. Called after send_text / send_keys / respond —
// the screen is about to change, so the next poll must re-read rather than
// serve the pre-response menu.
func (p *Poller) InvalidateMenu(paneID string) {
	p.activeMu.Lock()
	delete(p.menuCache, paneID)
	p.menuGen[paneID]++
	p.activeMu.Unlock()
}

func (p *Poller) kindAllowed(kind string) bool {
	if len(p.kinds) == 0 {
		return true
	}
	return p.kinds[kind]
}

func (p *Poller) setRemote(paneID, host string) {
	p.mu.Lock()
	p.paneRemote[paneID] = host
	p.mu.Unlock()
}

func (p *Poller) pruneRemotes(seen map[string]bool) bool {
	p.mu.Lock()
	for id := range p.paneRemote {
		if !seen[id] {
			delete(p.paneRemote, id)
		}
	}
	p.mu.Unlock()

	p.activeMu.Lock()
	changed := false
	for id := range p.prevRevisions {
		if !seen[id] {
			changed = true
			delete(p.prevRevisions, id)
			delete(p.prevSeq, id)
			delete(p.lastActive, id)
			delete(p.acknowledgedDone, id)
			delete(p.prevValidPathsRevs, id)
			delete(p.prevValidPathsSeq, id)
			delete(p.cachedValidPaths, id)
			delete(p.missingPathItems, id)
			delete(p.validPathsLastCheck, id)
			delete(p.menuCache, id)
			delete(p.menuGen, id)
		}
	}
	p.activeMu.Unlock()

	p.contentMu.Lock()
	for id := range p.contentCache {
		if !seen[id] {
			delete(p.contentCache, id)
			delete(p.contentRevs, id)
			delete(p.contentSeqs, id)
			delete(p.contentAt, id)
			delete(p.contentVerAt, id)
			delete(p.subscribers, id)
			delete(p.previewCache, id)
		}
	}
	p.contentMu.Unlock()
	return changed
}

func (p *Poller) applyDoneAcknowledgements(panes []herdr.Pane) bool {
	p.activeMu.Lock()
	defer p.activeMu.Unlock()

	changed := false
	for i := range panes {
		pane := &panes[i]
		if !strings.EqualFold(pane.Status, "done") {
			if p.acknowledgedDone[pane.PaneID] {
				delete(p.acknowledgedDone, pane.PaneID)
				changed = true
			}
			continue
		}
		if !p.acknowledgedDone[pane.PaneID] {
			continue
		}
		pane.Status = "idle"
		pane.AgentStatus = "idle"
	}
	return changed
}

func isBlocked(status string) bool {
	s := strings.ToLower(status)
	return strings.Contains(s, "block") || strings.Contains(s, "wait") || strings.Contains(s, "confirm")
}

// promptExcerpt returns the tail of a pane read for notifications/cards.
func promptExcerpt(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	excerpt := strings.Join(lines, "\n")
	if len(excerpt) > 500 {
		excerpt = excerpt[:500]
	}
	return excerpt
}

func hostLabel(host string) string {
	if host == "" || host == "local" {
		return "local"
	}
	return host
}

func toAny(panes []herdr.Pane) []interface{} {
	out := make([]interface{}, len(panes))
	for i, p := range panes {
		out[i] = p
	}
	return out
}

// enrichBranches fills in the Branch field for each pane by running
// `git -C <cwd> branch --show-current`. Results are cached per-cwd with a
// 15-second TTL to avoid hammering git on every poll cycle.
func (p *Poller) enrichBranches(ctx context.Context, panes []herdr.Pane) {
	p.branchMu.Lock()
	defer p.branchMu.Unlock()

	now := time.Now()
	const ttl = 15 * time.Second

	// Collect unique cwds that need branch lookup (expired or missing)
	needed := make(map[string]bool)
	for i := range panes {
		cwd := panes[i].Cwd
		if cwd == "" {
			continue
		}
		if exp, ok := p.branchExpiry[cwd]; !ok || now.After(exp) {
			if !needed[cwd] {
				needed[cwd] = true
			}
		}
	}

	// Fetch expired/missing branches only
	for cwd := range needed {
		branch := gitBranch(ctx, cwd)
		p.branchCache[cwd] = branch // cache even if empty
		p.branchExpiry[cwd] = now.Add(ttl)
	}

	// Apply to panes
	for i := range panes {
		if panes[i].Cwd == "" {
			continue
		}
		if br, ok := p.branchCache[panes[i].Cwd]; ok && br != "" {
			panes[i].Branch = br
		}
	}
}

// gitBranch returns the current git branch for a directory, or "" on error.
func gitBranch(ctx context.Context, cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	cmd := exec.CommandContext(ctx, "git", "-C", abs, "branch", "--show-current")
	cmd.Cancel = func() error { return cmd.Process.Kill() }
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// applyWorkspaceLabels fills in WorkspaceLabel for each pane from the label
// maps collected in the same poll cycle's api snapshot — no extra herdr call.
// Panes on hosts whose snapshot failed keep the label from their retained
// (stale) copy.
func (p *Poller) applyWorkspaceLabels(panes []herdr.Pane, wsLabels map[string]map[string]string) {
	for i := range panes {
		h := panes[i].Host
		if h == "" {
			h = "local"
		}
		labels, ok := wsLabels[h]
		if !ok {
			continue
		}
		if label, ok := labels[panes[i].WorkspaceID]; ok && label != "" {
			panes[i].WorkspaceLabel = label
		}
	}
}

// enrichLastActive tracks pane activity by detecting changes in revision or
// state_change_seq. Working agents stay active because herdr may leave both
// counters unchanged while an agent is running. Returns true if any timestamp
// changed.
func (p *Poller) enrichLastActive(panes []herdr.Pane) bool {
	p.activeMu.Lock()
	defer p.activeMu.Unlock()

	now := time.Now().Unix()
	changed := false
	for i := range panes {
		pid := panes[i].PaneID
		rev := panes[i].Revision
		seq := panes[i].StateChangeSeq
		prev, seen := p.prevRevisions[pid]
		working := panes[i].Kind == "agent" && strings.EqualFold(panes[i].Status, "working")
		stateChanged := !seen || rev != prev || seq != p.prevSeq[pid]
		if stateChanged || working {
			p.lastActive[pid] = now
			p.prevRevisions[pid] = rev
			p.prevSeq[pid] = seq
			changed = changed || stateChanged
		}
		if ts, ok := p.lastActive[pid]; ok {
			panes[i].LastActive = ts
		}
	}
	return changed
}

// Subscribe marks a pane as watched so the poller reads its content for
// delta computation. The subscription is a TTL: the client refreshes it with
// every read_pane (the frontend polls every 6s), and it expires automatically
// a while after the last request, so disconnected clients leave no permanent
// state behind. Note: subscription gates whether the poller reads/pushes a
// pane's terminal traffic at all — it does not per-connection filter
// delivery. Both deltas and the fallback full snapshots are routed through
// BroadcastJSONIncremental, so they reach only incremental-capable clients;
// older clients are served via the direct read_pane response path.
func (p *Poller) Subscribe(paneID string) {
	p.contentMu.Lock()
	p.subscribers[paneID] = time.Now().Unix()
	p.contentMu.Unlock()
}

// contentVerFreshFor is the periodic forced-probe backstop: a cached content
// snapshot stays servable as long as successful polls keep confirming its
// rev/seq against upstream. If confirmations stop for this long (polling
// stalled, subscriptions expired), the caller falls back to a direct read.
const contentVerFreshFor = 30 * time.Second

// confirmContentVersions reconciles the content cache against the versions a
// successful snapshot just reported. A match refreshes contentVerAt (fresh);
// a mismatch zeroes it (the cached baseline is provably behind and is
// invalidated immediately, not after a timeout). Panes on stale-marked hosts
// confirm nothing.
func (p *Poller) confirmContentVersions(panes []herdr.Pane) {
	p.contentMu.Lock()
	defer p.contentMu.Unlock()
	now := time.Now()
	for i := range panes {
		pane := &panes[i]
		if pane.Stale {
			continue
		}
		pid := pane.PaneID
		if _, ok := p.contentCache[pid]; !ok {
			continue
		}
		if pane.Revision == p.contentRevs[pid] && pane.StateChangeSeq == p.contentSeqs[pid] {
			p.contentVerAt[pid] = now
		} else {
			p.contentVerAt[pid] = time.Time{}
		}
	}
}

// ContentSnapshot returns an atomically paired cached full snapshot and
// revision for a pane, and whether it is fresh enough to serve. Freshness is
// version-based: the cache is served while recent polls keep confirming the
// pane's upstream rev/seq still match the cached read. When the upstream
// version has drifted or confirmations have stopped for contentVerFreshFor,
// ok=false and the caller should read directly from herdr.
func (p *Poller) ContentSnapshot(paneID string) (string, int, bool) {
	p.contentMu.Lock()
	defer p.contentMu.Unlock()
	content, contentOK := p.contentCache[paneID]
	rev, revOK := p.contentRevs[paneID]
	if !contentOK || !revOK {
		return "", 0, false
	}
	if time.Since(p.contentVerAt[paneID]) > contentVerFreshFor {
		return "", 0, false
	}
	return content, rev, true
}

// previewCacheTTL is the short lifetime of an unversioned preview read — long
// enough to dedupe a burst of grid-card refreshes, short enough that a
// changed screen is never served stale.
const previewCacheTTL = 5 * time.Second

// PreviewSnapshot serves a short pane read for grid-card previews while
// minimizing upstream reads:
//  1. A version-confirmed contentCache baseline is tailed in place (free).
//  2. A previewCache entry younger than previewCacheTTL is reused.
//  3. Concurrent misses for the same pane share one ReadPane (singleflight).
//
// Preview reads are deliberately kept OUT of contentCache — a small tail
// window must never overwrite the large delta baseline.
func (p *Poller) PreviewSnapshot(ctx context.Context, paneID string, lines int) (string, error) {
	if content, _, ok := p.ContentSnapshot(paneID); ok {
		return service.TailLines(content, lines), nil
	}

	p.contentMu.Lock()
	if e, ok := p.previewCache[paneID]; ok && time.Since(e.at) < previewCacheTTL {
		text := e.text
		p.contentMu.Unlock()
		return service.TailLines(text, lines), nil
	}
	if call, ok := p.previewInflight[paneID]; ok {
		p.contentMu.Unlock()
		select {
		case <-call.done:
			return service.TailLines(call.text, lines), call.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	call := &previewCall{done: make(chan struct{})}
	p.previewInflight[paneID] = call
	p.contentMu.Unlock()

	content, err := p.client.ReadPane(ctx, p.RemoteForPane(paneID), paneID, lines)
	p.metrics.mu.Lock()
	p.metrics.paneReads++
	p.metrics.mu.Unlock()

	p.contentMu.Lock()
	call.text, call.err = content, err
	close(call.done)
	delete(p.previewInflight, paneID)
	if err == nil {
		p.previewCache[paneID] = previewEntry{text: content, at: time.Now()}
	}
	p.contentMu.Unlock()
	return service.TailLines(content, lines), err
}

// TriggerPoll requests an immediate poll cycle (e.g. on first client connect
// or after an acknowledgement), without waiting for the ticker.
func (p *Poller) TriggerPoll() {
	select {
	case p.trigger <- struct{}{}:
	default:
	}
}

// subscriberTTL is how long a pane stays subscribed after the last read_pane.
// Longer than the frontend's 6s poll so a healthy client never drops out.
const subscriberTTL = 30 * time.Second

// activeSubscription reports whether a pane currently has a live subscriber
// and prunes expired entries opportunistically.
func (p *Poller) activeSubscription(paneID string) bool {
	p.contentMu.Lock()
	defer p.contentMu.Unlock()
	ts, ok := p.subscribers[paneID]
	if !ok {
		return false
	}
	if time.Since(time.Unix(ts, 0)) > subscriberTTL {
		delete(p.subscribers, paneID)
		return false
	}
	return true
}

// diffContent decides how to transmit the new pane content given the previous
// snapshot. It handles three cases:
//
//  1. Pure append: cur = prev + suffix. Delta is the suffix.
//  2. Window shift: herdr returns a rolling ~1000-line window, so once a pane
//     grows past the window, cur = (a suffix of prev) + newLines. The new
//     lines are recovered by aligning cur's head against prev (finding the
//     shift k where prev[k:] best matches cur). The client's content stays a
//     superset, so splicing is correct.
//  3. True discontinuity (pane rebuilt, content replaced, or interactive-shell
//     echo reshuffling): no reliable overlap. Delta is the full content and
//     Append=false.
//
// Carriage returns that are not part of CRLF (in-place redraws: progress bars,
// spinners) can't be represented by a suffix append, so cases 1/2 fall back to
// full whenever the recovered delta contains one.
func diffContent(prev, cur string) (delta string, appendMode bool) {
	if prev != "" && strings.HasPrefix(cur, prev) {
		delta = cur[len(prev):]
		if !hasLoneCR(delta) {
			return delta, true
		}
		return cur, false
	}

	// Window shift: recover the appended lines by aligning cur's head with prev.
	if prev != "" {
		if d, ok := shiftedAppend(prev, cur); ok && !hasLoneCR(d) {
			return d, true
		}
	}
	return cur, false
}

// minAlignRun is the minimum number of consecutively matching lines required
// to trust a window-shift alignment.
const minAlignRun = 20

// maxShiftSearch bounds how far into prev shiftedAppend scans for the shift
// point. A rolling window drops only a few lines per poll, so k is small; a
// large bound would degrade to O(n²) on repetitive content.
const maxShiftSearch = 256

// shiftedAppend aligns cur's head against prev to recover the lines appended
// after the overlap. herdr's read is a rolling ~1000-line window, so when a
// pane exceeds it, cur = prev[k:] + newLines for some small shift k (the
// number of lines scrolled out). We find k by testing every candidate where
// cur[0] matches prev[k] and keeping the one with the longest consecutive
// match; the appended delta is cur after that run.
//
// ok=false when no trustworthy overlap exists (true discontinuity, or an
// interactive shell whose echo/redraw reshuffles the window non-append-style).
func shiftedAppend(prev, cur string) (delta string, ok bool) {
	prevLines := splitLines(prev)
	curLines := splitLines(cur)
	if len(prevLines) < minAlignRun || len(curLines) < minAlignRun {
		return "", false
	}

	// A valid window shift is cur = prev[k:] + newLines: the matched run must
	// extend ALL the way to prev's end (i == len(prevLines)). If the run stops
	// early, the overlap region itself changed (a partial line completed, a
	// mid-window line rewritten) and emitting the tail as "append" would
	// duplicate content the client already holds.
	//
	// k is bounded: a rolling window only drops a small number of lines per
	// poll (herdr scrolls ~viewport rows), so scanning far into prev is wasted
	// work on repetitive content.
	bestK := -1
	bestRun := 0
	maxK := len(prevLines)
	if maxK > maxShiftSearch {
		maxK = maxShiftSearch
	}
	for k := 1; k < maxK; k++ {
		if prevLines[k] != curLines[0] {
			continue
		}
		i, j := k, 0
		for i < len(prevLines) && j < len(curLines) && prevLines[i] == curLines[j] {
			i++
			j++
		}
		// Require the run to consume prev entirely; only then is prev[k:] the
		// unmodified overlap and curLines[j:] the new lines.
		if i == len(prevLines) && j > bestRun {
			bestRun = j
			bestK = k
		}
	}
	if bestK < 0 || bestRun < minAlignRun {
		return "", false
	}

	// Reconstruct the new lines preserving cur's trailing newline: splitLines
	// dropped it, so re-append it if cur actually ended with one.
	newLines := curLines[bestRun:]
	if len(newLines) == 0 {
		return "", false
	}
	delta = strings.Join(newLines, "\n")
	if strings.HasSuffix(cur, "\n") {
		delta += "\n"
	}
	return delta, true
}

// splitLines splits content into lines, dropping a single trailing empty line
// from a trailing newline so line counts are intuitive.
func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}
	return lines
}

// hasLoneCR reports whether s contains a \r that is not part of a \r\n pair.
func hasLoneCR(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' && (i+1 >= len(s) || s[i+1] != '\n') {
			return true
		}
	}
	return false
}

// paneContentMode classifies how a pane's content should be delivered.
//
//   - contentFull: always send the full snapshot. Used for full-screen TUI
//     agents (scrollback 0, redraw in place) and for panes whose scroll
//     metadata is absent — without scroll info we cannot tell a TUI from a
//     scrollback pane, and splicing an incremental delta onto a redrawn TUI
//     screen would corrupt it, so full is the safe fallback.
//   - contentIncremental: the pane has real scrollback, so append/window-shift
//     deltas are safe.
type paneContentMode int

const (
	contentIncremental paneContentMode = iota
	contentFull
)

// classifyPaneContent decides whether incremental deltas are safe for a pane.
// Only panes with explicit scrollback metadata (scroll present and
// max_offset_from_bottom > 0) are incremental; TUI panes (offset 0) and panes
// without scroll metadata (older herdr) fall back to full snapshots.
func classifyPaneContent(p *herdr.Pane) paneContentMode {
	if p == nil || p.Scroll == nil {
		// No scroll info: cannot distinguish TUI from scrollback — full is safe.
		return contentFull
	}
	if p.Scroll.MaxOffsetFromBottom == 0 {
		return contentFull // full-screen TUI
	}
	return contentIncremental
}

// tryStartDelta atomically claims the single-flight slot for a delta push.
// Returns false if a previous run is still in flight (its reads and cache
// writes would otherwise race with this one and could reorder rev/content).
func (p *Poller) tryStartDelta() bool {
	p.deltaMu.Lock()
	defer p.deltaMu.Unlock()
	if p.deltaRunning {
		return false
	}
	p.deltaRunning = true
	return true
}

// deltaDone releases the single-flight slot.
func (p *Poller) deltaDone() {
	p.deltaMu.Lock()
	p.deltaRunning = false
	p.deltaMu.Unlock()
}

// pushContentDeltas watches pane revisions and pushes incremental terminal
// content to subscribed, incremental-capable clients. For each pane whose
// revision/seq changed since the last poll, it reads the content (a generous
// window that covers the full scrollback for typical sessions) and diffs it
// against the cached snapshot: pure-suffix growth and clean window shifts
// yield append deltas; anything else (in-place redraw, TUI, unknown pane
// type) falls back to a full snapshot. Runs asynchronously from pollOnce so a
// slow remote read cannot stall the poll loop.
func (p *Poller) pushContentDeltas(ctx context.Context, panes []herdr.Pane) {
	// No connected clients → nothing to push; skip the content reads entirely.
	if p.hub.Count() == 0 {
		return
	}
	for i := range panes {
		pane := &panes[i]
		pid := pane.PaneID
		rev := pane.Revision
		seq := pane.StateChangeSeq

		// Only push panes a client is actively watching.
		if !p.activeSubscription(pid) {
			continue
		}

		// Full-screen TUI agents (grok, newer codex/claude) redraw a fixed
		// viewport in place (scrollback 0); their content is a single screen
		// and every change replaces the whole view, so incremental append is
		// meaningless. Panes without scroll metadata (older herdr) are also
		// delivered as full snapshots since we cannot prove they are not TUIs
		full := classifyPaneContent(pane) == contentFull

		p.contentMu.Lock()
		prevContent, havePrev := p.contentCache[pid]
		prevRev := p.contentRevs[pid]
		prevSeq := p.contentSeqs[pid]
		if havePrev && rev == prevRev && seq == prevSeq {
			p.contentMu.Unlock()
			p.metrics.mu.Lock()
			p.metrics.deltaSkips++
			p.metrics.mu.Unlock()
			continue
		}
		p.contentMu.Unlock()

		// Read a generous line window. The pane's real content is usually well
		// under this, so the read returns the full scrollback and the prefix
		// diff produces true append deltas; only once a pane's output grows
		// past the window (very long sessions) does it fall back to full
		// snapshots, which is no worse than the previous behavior.
		content, err := p.client.ReadPane(ctx, pane.Host, pid, 10000)
		p.metrics.mu.Lock()
		p.metrics.paneReads++
		p.metrics.mu.Unlock()
		if err != nil {
			p.logger.Debug("delta read failed", "pane", pid, "err", err)
			continue
		}

		var delta string
		appendMode := false
		if !full {
			delta, appendMode = diffContent(prevContent, content)
		} else {
			// TUI or unknown pane type: always full snapshot (append would
			// corrupt the redrawn screen).
			delta = content
		}
		p.logger.Debug("delta computed", "pane", pid, "rev", rev, "prev_rev", prevRev, "append", appendMode, "full", full, "delta_bytes", len(delta), "prev_len", len(prevContent), "cur_len", len(content))

		p.contentMu.Lock()
		p.contentCache[pid] = content
		p.contentRevs[pid] = rev
		p.contentSeqs[pid] = seq
		p.contentAt[pid] = time.Now()
		p.contentMu.Unlock()

		if appendMode && delta == "" {
			// Revision bumped but no visible change in the read window
			// (e.g. cursor-only updates); nothing to send.
			continue
		}
		p.hub.BroadcastJSONIncremental(protocol.PaneContent{
			Type:    "pane_content",
			PaneID:  pid,
			Content: delta,
			Append:  appendMode,
			Rev:     protocol.IntPtr(rev),
			BaseRev: protocol.IntPtr(prevRev),
		})
	}
}
