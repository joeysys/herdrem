package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Input history: shell commands and prompts typed to agents through the web
// app. Kept on the relay so every device that uses this relay shares it; each
// entry remembers the workspace it was entered in so clients can scope their
// search (pane / workspace / whole relay).

// History kinds. Shell is stored as the empty string so the wire/disk form of
// a shell entry has no "k" key (matching what browsers cached before the relay
// kept history).
const (
	HistoryShell = ""
	HistoryAgent = "ag"
)

// Limits. Per-kind caps keep a flood of prompts from evicting commands.
const (
	MaxShellLen     = 500
	MaxAgentLen     = 4000
	MaxShellEntries = 1500
	MaxAgentEntries = 600
	maxHistoryMeta  = 80  // agent type, pane title
	maxHistoryID    = 256 // host, workspace id, pane id
	maxHistoryLabel = 256 // workspace label
	// MaxHistoryImport bounds one history_import message.
	MaxHistoryImport = 500
)

// HistoryEntry is one remembered input.
type HistoryEntry struct {
	Text           string `json:"c"`
	Time           int64  `json:"t"` // last used, ms since epoch
	Count          int    `json:"n"`
	Pane           string `json:"p,omitempty"`
	Kind           string `json:"k,omitempty"` // "ag" for agent prompts; absent = shell
	Agent          string `json:"a,omitempty"` // agent type (prompts only)
	Title          string `json:"w,omitempty"` // pane title at submit time (prompts only)
	Host           string `json:"h,omitempty"`
	Workspace      string `json:"s,omitempty"`
	WorkspaceLabel string `json:"l,omitempty"`
	Fav            bool   `json:"f,omitempty"` // kept when the history is cleared
}

// Credential-looking text is never stored: this mirrors the browser-side
// filter so a client that skips it (or an old cache being imported) cannot
// get a password or token onto the relay's disk.
var historySecretRE = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`(?:pass(?:word|wd)?|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)\s*[=:]`,
	`--(?:password|passwd|token|secret|api-key|apikey)\b`,
	`\bauthorization\s*:`,
	`\bbearer\s+\S`,
	`://[^\s/@:]+:[^\s/@]+@`,
	`-----BEGIN [A-Z ]*PRIVATE KEY-----`,
}, "|"))

// ErrHistoryRejected means the text is not something to remember (blank,
// opted out with a leading space, too long, multi-line shell, or a secret).
var ErrHistoryRejected = errors.New("history entry rejected")

func normKind(k string) string {
	if k == HistoryAgent {
		return HistoryAgent
	}
	return HistoryShell
}

func clipRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// cleanHistoryEntry validates and normalises an entry. A leading space opts
// out (HISTCONTROL=ignorespace), so it is rejected rather than trimmed.
func cleanHistoryEntry(e HistoryEntry) (HistoryEntry, error) {
	e.Kind = normKind(e.Kind)
	if e.Text == "" {
		return e, ErrHistoryRejected
	}
	if r, _ := utf8.DecodeRuneInString(e.Text); unicode.IsSpace(r) {
		return e, ErrHistoryRejected
	}
	e.Text = strings.TrimRightFunc(e.Text, unicode.IsSpace)
	if e.Text == "" {
		return e, ErrHistoryRejected
	}
	limit := MaxShellLen
	if e.Kind == HistoryAgent {
		limit = MaxAgentLen
	} else if strings.ContainsAny(e.Text, "\r\n") {
		return e, ErrHistoryRejected
	}
	if utf8.RuneCountInString(e.Text) > limit {
		return e, ErrHistoryRejected
	}
	if historySecretRE.MatchString(e.Text) {
		return e, ErrHistoryRejected
	}
	if e.Count < 1 {
		e.Count = 1
	}
	e.Pane = clipRunes(e.Pane, maxHistoryID)
	e.Host = clipRunes(e.Host, maxHistoryID)
	e.Workspace = clipRunes(e.Workspace, maxHistoryID)
	e.WorkspaceLabel = clipRunes(e.WorkspaceLabel, maxHistoryLabel)
	if e.Kind == HistoryAgent {
		e.Agent = clipRunes(e.Agent, maxHistoryMeta)
		e.Title = clipRunes(e.Title, maxHistoryMeta)
	} else {
		e.Agent, e.Title = "", ""
	}
	return e, nil
}

// sameSlot: an entry is the same remembered input when kind, text and
// workspace all match — the same command in two workspaces is two entries, so
// each workspace keeps its own recent list.
func sameSlot(a, b HistoryEntry) bool {
	return a.Kind == b.Kind && a.Text == b.Text && a.Host == b.Host && a.Workspace == b.Workspace
}

// HistoryStore is a JSON file-backed input history, newest first.
type HistoryStore struct {
	mu       sync.RWMutex
	filePath string
	entries  []HistoryEntry
}

// NewHistoryStore loads (or creates) the history at filePath. A corrupt file
// is set aside as <file>.corrupt-<unix> and the history starts empty, rather
// than failing startup or silently overwriting it.
func NewHistoryStore(filePath string) (*HistoryStore, error) {
	s := &HistoryStore{filePath: filePath}
	b, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var raw []HistoryEntry
	if err := json.Unmarshal(b, &raw); err != nil {
		_ = os.Rename(filePath, fmt.Sprintf("%s.corrupt-%d", filePath, time.Now().Unix()))
		return s, nil
	}
	for _, e := range raw {
		if c, err := cleanHistoryEntry(e); err == nil {
			s.entries = append(s.entries, c)
		}
	}
	s.sortAndCapLocked()
	return s, nil
}

// sortAndCapLocked orders newest first and enforces the per-kind caps.
// Favorites are exempt: clearing keeps them, so the cap must not drop them.
func (s *HistoryStore) sortAndCapLocked() {
	sort.SliceStable(s.entries, func(i, j int) bool { return s.entries[i].Time > s.entries[j].Time })
	seen := map[string]int{}
	kept := make([]HistoryEntry, 0, len(s.entries))
	var rest []HistoryEntry
	for _, e := range s.entries {
		if e.Fav {
			kept = append(kept, e)
			continue
		}
		rest = append(rest, e)
	}
	for _, e := range rest {
		max := MaxShellEntries
		if e.Kind == HistoryAgent {
			max = MaxAgentEntries
		}
		if seen[e.Kind] >= max {
			continue
		}
		seen[e.Kind]++
		kept = append(kept, e)
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Time > kept[j].Time })
	s.entries = kept
}

func (s *HistoryStore) saveLocked() error {
	b, err := json.Marshal(s.entries)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Atomic write: temp file in the same dir (0600), then rename.
	tmp, err := os.CreateTemp(dir, ".tmp-history-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.filePath)
}

// All returns a copy of every entry, newest first.
func (s *HistoryStore) All() []HistoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]HistoryEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

// mergeLocked folds e into the list; the caller has already cleaned it.
func (s *HistoryStore) mergeLocked(e HistoryEntry, keepCount bool) HistoryEntry {
	for i, old := range s.entries {
		if !sameSlot(old, e) {
			continue
		}
		merged := old
		if keepCount { // import: never lose what we already know
			if e.Count > merged.Count {
				merged.Count = e.Count
			}
			if e.Time > merged.Time {
				merged.Time = e.Time
				merged.Pane, merged.Agent, merged.Title, merged.WorkspaceLabel = e.Pane, e.Agent, e.Title, e.WorkspaceLabel
			}
		} else {
			merged.Count = old.Count + 1
			merged.Time = e.Time
			merged.Pane = e.Pane
			if e.Agent != "" {
				merged.Agent = e.Agent
			}
			if e.Title != "" {
				merged.Title = e.Title
			}
			if e.WorkspaceLabel != "" {
				merged.WorkspaceLabel = e.WorkspaceLabel
			}
		}
		merged.Fav = old.Fav || e.Fav
		s.entries[i] = merged
		return merged
	}
	s.entries = append(s.entries, e)
	return e
}

// Record remembers one submitted input and returns the stored (merged) entry.
// now is ms since epoch.
func (s *HistoryStore) Record(e HistoryEntry, now int64) (HistoryEntry, error) {
	e.Time = now
	e.Count = 1
	c, err := cleanHistoryEntry(e)
	if err != nil {
		return HistoryEntry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	merged := s.mergeLocked(c, false)
	s.sortAndCapLocked()
	if err := s.saveLocked(); err != nil {
		return HistoryEntry{}, err
	}
	return merged, nil
}

// Import merges entries cached by a client from before the relay kept
// history. Invalid entries are skipped; it returns how many were accepted.
func (s *HistoryStore) Import(in []HistoryEntry) (int, error) {
	if len(in) > MaxHistoryImport {
		in = in[:MaxHistoryImport]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	accepted := 0
	for _, e := range in {
		c, err := cleanHistoryEntry(e)
		if err != nil {
			continue
		}
		if c.Time <= 0 {
			c.Time = time.Now().UnixMilli()
		}
		s.mergeLocked(c, true)
		accepted++
	}
	if accepted == 0 {
		return 0, nil
	}
	s.sortAndCapLocked()
	return accepted, s.saveLocked()
}

// Remove forgets an input everywhere (every workspace), so a command removed
// because it should not have been saved is really gone.
func (s *HistoryStore) Remove(kind, text string) (bool, error) {
	kind = normKind(kind)
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.entries[:0]
	removed := false
	for _, e := range s.entries {
		if e.Kind == kind && e.Text == text {
			removed = true
			continue
		}
		kept = append(kept, e)
	}
	s.entries = kept
	if !removed {
		return false, nil
	}
	return true, s.saveLocked()
}

// Clear forgets entries of a kind ("all", "sh" or "ag"). With scoped set,
// only that workspace's entries go; otherwise the whole relay's.
func (s *HistoryStore) Clear(kind string, scoped bool, host, workspace string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.entries[:0]
	removed := 0
	for _, e := range s.entries {
		kindMatch := kind == "" || kind == "all" || e.Kind == normKind(kind)
		scopeMatch := !scoped || (e.Host == host && e.Workspace == workspace)
		if kindMatch && scopeMatch && !e.Fav {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	s.entries = kept
	if removed == 0 {
		return 0, nil
	}
	return removed, s.saveLocked()
}

// SetFavorite stars or unstars an input. everywhere updates every workspace's
// copy of that text; otherwise only the host+workspace slot.
func (s *HistoryStore) SetFavorite(kind, text, host, workspace string, everywhere, fav bool) (int, error) {
	kind = normKind(kind)
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for i, e := range s.entries {
		if e.Kind != kind || e.Text != text {
			continue
		}
		if !everywhere && (e.Host != host || e.Workspace != workspace) {
			continue
		}
		if e.Fav == fav {
			continue
		}
		e.Fav = fav
		s.entries[i] = e
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.saveLocked()
}
