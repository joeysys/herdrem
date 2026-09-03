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

	mu         sync.RWMutex
	paneRemote map[string]string // pane_id → host
	latest     []herdr.Pane      // last merged snapshot (for cwd roots)

	// branch cache: cwd → branch name, refreshed every 15s
	branchMu     sync.Mutex
	branchCache  map[string]string    // cwd → branch
	branchExpiry map[string]time.Time // cwd → expiry time
	branchLast   time.Time

	// workspace label cache: workspace_id → label, per host
	wsMu    sync.Mutex
	wsCache map[string]map[string]string // host → {workspace_id → label}
	wsLast  time.Time

	// last active tracking: pane_id → epoch seconds of last observed activity
	activeMu            sync.Mutex
	prevRevisions       map[string]int       // pane_id → last seen revision
	prevSeq             map[string]int       // pane_id → last seen state_change_seq
	lastActive          map[string]int64     // pane_id → epoch seconds
	acknowledgedDone    map[string]bool      // pane_id → done seen by a relay client
	prevValidPathsRevs  map[string]int       // pane_id → last checked revision for valid paths
	prevValidPathsSeq   map[string]int       // pane_id → last checked state change sequence for valid paths
	cachedValidPaths    map[string][]string  // pane_id → cached valid paths
	hasMissingPaths     map[string]bool      // pane_id → whether extracted matches contained missing paths
	validPathsLastCheck map[string]time.Time // pane_id → last valid paths check time

	// incremental terminal content: pane_id → last seen content snapshot
	contentMu    sync.Mutex
	contentCache map[string]string    // pane_id → last full content
	contentRevs  map[string]int       // pane_id → revision of the cached content
	contentSeqs  map[string]int       // pane_id → state_change_seq of the cached content
	contentAt    map[string]time.Time // pane_id → when the cached content was last read
	subscribers  map[string]int64     // pane_id → unix ts of last read_pane request (TTL-based subscription)

	// single-flight guard so a slow pushContentDeltas goroutine is never
	// overlapped by the next poll cycle: the previous run must finish
	// (including its slow reads and cache writes) before a new one starts.
	deltaMu      sync.Mutex
	deltaRunning bool

	// file existence checker (set after construction to avoid circular dep)
	fileSvc   *service.FileService
	fileSvcMu sync.RWMutex

	// trigger channel for immediate poll (e.g. after AckDone)
	trigger chan struct{}

	// persistence: state file path and in-process writer serialization
	statePath   string
	stateFileMu sync.Mutex
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
		branchCache:         make(map[string]string),
		branchExpiry:        make(map[string]time.Time),
		wsCache:             make(map[string]map[string]string),
		prevRevisions:       make(map[string]int),
		prevSeq:             make(map[string]int),
		lastActive:          make(map[string]int64),
		acknowledgedDone:    make(map[string]bool),
		prevValidPathsRevs:  make(map[string]int),
		prevValidPathsSeq:   make(map[string]int),
		cachedValidPaths:    make(map[string][]string),
		hasMissingPaths:     make(map[string]bool),
		validPathsLastCheck: make(map[string]time.Time),
		contentCache:        make(map[string]string),
		contentRevs:         make(map[string]int),
		contentSeqs:         make(map[string]int),
		contentAt:           make(map[string]time.Time),
		subscribers:         make(map[string]int64),
		trigger:             make(chan struct{}, 1),
	}
}

// SetFileService connects the file service for async file-existence checks.
func (p *Poller) SetFileService(fs *service.FileService) {
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

func (p *Poller) pollOnce(ctx context.Context) {
	var merged []herdr.Pane
	seen := make(map[string]bool)

	for _, host := range p.hosts {
		panes, err := p.client.ListPanes(ctx, host)
		if err != nil {
			p.logger.Warn("poll failed", "host", hostLabel(host), "err", err)
			continue
		}
		for i := range panes {
			pane := &panes[i]
			if !p.kindAllowed(pane.Kind) {
				continue
			}
			if pane.Host == "" {
				pane.Host = hostLabel(host)
			}
			if isBlocked(pane.Status) {
				p.attachMenu(ctx, host, pane)
			} else {
				pane.Options = nil
				pane.OptionLabels = nil
			}
			p.setRemote(pane.PaneID, host)
			seen[pane.PaneID] = true
			merged = append(merged, *pane)
		}
	}

	stateChanged := p.pruneRemotes(seen)
	stateChanged = p.applyDoneAcknowledgements(merged) || stateChanged

	// Enrich panes with git branch info (cached, refreshed every 15s)
	p.enrichBranches(ctx, merged)

	// Enrich panes with workspace labels
	p.enrichWorkspaceLabels(ctx, merged)

	// Track last active time from pane revisions and agent state
	activeChanged := p.enrichLastActive(merged)
	if activeChanged || stateChanged {
		p.saveState()
	}

	// Update p.latest with current snapshot before enrichValidPaths so CwdRoots works for safe-path checks
	p.mu.Lock()
	prevSnapshot := p.latest
	p.latest = merged
	p.mu.Unlock()

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

	// Only broadcast if the snapshot actually changed — avoids waking up
	// all WS clients every 3s when nothing happened.
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

// enrichValidPaths reads pane content, extracts file paths, and checks which
// exist on disk. Re-checks missing paths periodically even if revision is unchanged
// (e.g. for files created by agents right after printing their path).
// Batch + concurrent with a 2s timeout.
func (p *Poller) enrichValidPaths(ctx context.Context, panes []herdr.Pane) {
	p.fileSvcMu.RLock()
	fs := p.fileSvc
	p.fileSvcMu.RUnlock()
	if fs == nil {
		return
	}

	type result struct {
		idx        int
		valid      []string
		totalPaths int
	}

	type paneCheck struct {
		idx   int
		host  string
		cwd   string
		items []panePathItem
	}
	var checks []paneCheck

	p.activeMu.Lock()
	now := time.Now()
	reLineCol := regexp.MustCompile(`:\d+`)
	for i := range panes {
		pane := &panes[i]
		prevRev, seen := p.prevValidPathsRevs[pane.PaneID]
		prevStateSeq := p.prevValidPathsSeq[pane.PaneID]
		cachedValid := p.cachedValidPaths[pane.PaneID]
		lastCheck := p.validPathsLastCheck[pane.PaneID]
		missing := p.hasMissingPaths[pane.PaneID]

		// Skip if neither content version changed; re-check missing paths every 5s.
		if seen && pane.Revision == prevRev && pane.StateChangeSeq == prevStateSeq {
			if !missing || now.Sub(lastCheck) < 5*time.Second {
				pane.ValidPaths = cachedValid
				continue
			}
		}

		// Read pane content — match the frontend's read size (500 lines) so
		// all visible file paths are checked, not just the last 50 lines.
		content, err := p.client.ReadPane(ctx, pane.Host, pane.PaneID, 500)
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
			p.prevValidPathsRevs[pane.PaneID] = pane.Revision
			p.prevValidPathsSeq[pane.PaneID] = pane.StateChangeSeq
			p.validPathsLastCheck[pane.PaneID] = now
			p.hasMissingPaths[pane.PaneID] = false
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
			clean := reLineCol.ReplaceAllString(raw, "")
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
			p.prevValidPathsRevs[pane.PaneID] = pane.Revision
			p.prevValidPathsSeq[pane.PaneID] = pane.StateChangeSeq
			p.validPathsLastCheck[pane.PaneID] = now
			p.hasMissingPaths[pane.PaneID] = false
			continue
		}
		checks = append(checks, paneCheck{idx: i, host: pane.Host, cwd: pane.Cwd, items: items})
	}
	p.activeMu.Unlock()

	if len(checks) == 0 {
		return
	}

	// Concurrent existence check with timeout
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	results := make(chan result, len(checks))
	var wg sync.WaitGroup
	for _, c := range checks {
		wg.Add(1)
		go func(c paneCheck) {
			defer wg.Done()
			// Check paths concurrently within a pane for speed
			type pathResult struct {
				item   panePathItem
				exists bool
			}
			prCh := make(chan pathResult, len(c.items))
			var pwg sync.WaitGroup
			for _, item := range c.items {
				pwg.Add(1)
				go func(item panePathItem) {
					defer pwg.Done()
					prCh <- pathResult{item: item, exists: fs.Exists(checkCtx, c.host, item.abs)}
				}(item)
			}
			pwg.Wait()
			close(prCh)
			valid := []string{}
			seenValid := make(map[string]bool)
			for pr := range prCh {
				if pr.exists {
					if !seenValid[pr.item.abs] {
						seenValid[pr.item.abs] = true
						valid = append(valid, pr.item.abs)
					}
					if pr.item.rel != "" && !seenValid[pr.item.rel] {
						seenValid[pr.item.rel] = true
						valid = append(valid, pr.item.rel)
					}
					if pr.item.dir != "" && !seenValid[pr.item.dir] {
						seenValid[pr.item.dir] = true
						valid = append(valid, pr.item.dir)
					}
				}
			}
			results <- result{idx: c.idx, valid: valid, totalPaths: len(c.items)}
		}(c)
	}
	wg.Wait()
	close(results)

	p.activeMu.Lock()
	checkNow := time.Now()
	for r := range results {
		panes[r.idx].ValidPaths = r.valid
		p.cachedValidPaths[panes[r.idx].PaneID] = r.valid
		p.prevValidPathsRevs[panes[r.idx].PaneID] = panes[r.idx].Revision
		p.prevValidPathsSeq[panes[r.idx].PaneID] = panes[r.idx].StateChangeSeq
		p.validPathsLastCheck[panes[r.idx].PaneID] = checkNow
		p.hasMissingPaths[panes[r.idx].PaneID] = len(r.valid) < r.totalPaths
	}
	p.activeMu.Unlock()
}

// panesChanged compares two pane slices by a lightweight fingerprint
// (pane_id + status + last_active + branch + options). Order-insensitive.
func panesChanged(prev, cur []herdr.Pane) bool {
	if len(prev) != len(cur) {
		return true
	}
	fp := func(p herdr.Pane) string {
		return p.PaneID + ":" + p.Status + ":" + strconv.FormatInt(p.LastActive, 10) + ":" + p.Branch + ":" + strconv.Itoa(len(p.Options)) + ":" + strconv.Itoa(len(p.ValidPaths))
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

// attachMenu reads a blocked pane and parses its on-screen menu so clients get
// real approve buttons (options + labels).
func (p *Poller) attachMenu(ctx context.Context, host string, pane *herdr.Pane) {
	content, err := p.client.ReadPane(ctx, host, pane.PaneID, 30)
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
			delete(p.hasMissingPaths, id)
			delete(p.validPathsLastCheck, id)
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
			delete(p.subscribers, id)
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

// enrichWorkspaceLabels fills in WorkspaceLabel for each pane by fetching
// `herdr workspace list` per host. Cached with a 30s TTL.
func (p *Poller) enrichWorkspaceLabels(ctx context.Context, panes []herdr.Pane) {
	p.wsMu.Lock()
	defer p.wsMu.Unlock()

	now := time.Now()
	if now.Sub(p.wsLast) > 30*time.Second {
		p.wsCache = make(map[string]map[string]string)
		p.wsLast = now
	}

	// Collect hosts that need workspace label refresh
	hostsNeeded := make(map[string]bool)
	for i := range panes {
		h := panes[i].Host
		if h == "" {
			h = "local"
		}
		if _, ok := p.wsCache[h]; !ok {
			hostsNeeded[h] = true
		}
	}

	for h := range hostsNeeded {
		wss, err := p.client.ListWorkspaces(ctx, h)
		if err != nil {
			p.logger.Debug("workspace list failed", "host", h, "err", err)
			p.wsCache[h] = make(map[string]string) // cache empty to avoid retry
			continue
		}
		m := make(map[string]string)
		for _, ws := range wss {
			m[ws.WorkspaceID] = ws.Label
		}
		p.wsCache[h] = m
	}

	// Apply labels to panes
	for i := range panes {
		h := panes[i].Host
		if h == "" {
			h = "local"
		}
		if labels, ok := p.wsCache[h]; ok {
			if label, ok := labels[panes[i].WorkspaceID]; ok && label != "" {
				panes[i].WorkspaceLabel = label
			}
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

// snapshotStaleAfter is how old the poller's cached full snapshot may be and
// still be served to a read_pane. Polls run every ~3s while a client is
// subscribed; if the cache is older than this (client was away, subscription
// expired), the snapshot may be stale and callers should read directly from
// herdr instead of serving yesterday's content.
const snapshotStaleAfter = 10 * time.Second

// ContentSnapshot returns an atomically paired cached full snapshot and
// revision for a pane, and whether it is fresh enough to serve. When the cache
// is stale (older than snapshotStaleAfter) or absent, ok=false and the caller
// should read directly from herdr rather than serve old content.
func (p *Poller) ContentSnapshot(paneID string) (string, int, bool) {
	p.contentMu.Lock()
	defer p.contentMu.Unlock()
	content, contentOK := p.contentCache[paneID]
	rev, revOK := p.contentRevs[paneID]
	if !contentOK || !revOK {
		return "", 0, false
	}
	if at, ok := p.contentAt[paneID]; ok && time.Since(at) > snapshotStaleAfter {
		return "", 0, false
	}
	return content, rev, true
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
			continue
		}
		p.contentMu.Unlock()

		// Read a generous line window. The pane's real content is usually well
		// under this, so the read returns the full scrollback and the prefix
		// diff produces true append deltas; only once a pane's output grows
		// past the window (very long sessions) does it fall back to full
		// snapshots, which is no worse than the previous behavior.
		content, err := p.client.ReadPane(ctx, pane.Host, pid, 10000)
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
