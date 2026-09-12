package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Skill is a quick-input text template with a display label.
type Skill struct {
	Label    string `json:"label"`
	Template string `json:"template"`
}

// KindOverride marks a pane as a given kind/agent display.
type KindOverride struct {
	Kind         string `json:"kind"`
	DisplayAgent string `json:"display_agent"`
	UpdatedAt    string `json:"updated_at"`
}

// TerminalPrefs holds terminal-view preferences.
type TerminalPrefs struct {
	// PollMs is the term view's subscription-refresh interval in milliseconds.
	// 0 means the client default (6000). Explicit values must be in
	// [TermPollMinMs, TermPollMaxMs] — the floor keeps herdr CLI reads cheap.
	PollMs int `json:"poll_ms,omitempty"`
}

// Bounds for TerminalPrefs.PollMs when explicitly set. Exported because the
// poller clamps defensively at the consumption boundary too — a hand-edited
// or corrupt prefs.json must not be able to inject extreme timers (a huge
// value would overflow time.Duration and panic time.NewTicker).
const (
	TermPollMinMs = 1000
	TermPollMaxMs = 60000
)

// TimingPrefs holds relay/frontend timing knobs. Every field is milliseconds;
// 0 (or absent) means "use the built-in default" — defaults live at the
// consumers (poller / web client), not here.
type TimingPrefs struct {
	// SnapshotPollMs is the base agents-snapshot tick: one `api snapshot` exec
	// per host per tick. Default 3000. Range [500, 60000].
	SnapshotPollMs int `json:"snapshot_poll_ms,omitempty"`
	// EnrichGraceMs is how long path/menu/delta enrichment keeps running after
	// the last WS client disconnects. Default 30000. Range [5000, 600000].
	EnrichGraceMs int `json:"enrich_grace_ms,omitempty"`
	// HostRetryMs is the backoff before retrying a host whose snapshot failed.
	// Default 30000. Range [5000, 600000].
	HostRetryMs int `json:"host_retry_ms,omitempty"`
	// ReconnectDelayMs is the browser WS reconnect delay. Consumed client-side.
	// Default 2000. Range [200, 30000].
	ReconnectDelayMs int `json:"reconnect_delay_ms,omitempty"`
	// GridPreviewMs is the grid-card preview refresh interval. Stored for the
	// future wall/monitor feature; the grid preview UI is currently disabled
	// so nothing consumes it yet. Default 8000. Range [1000, 60000].
	GridPreviewMs int `json:"grid_preview_ms,omitempty"`
}

// Bounds for TimingPrefs fields when explicitly set. Exported for the same
// defensive-clamp reason as TermPollMinMs/TermPollMaxMs.
const (
	SnapshotPollMinMs   = 500
	SnapshotPollMaxMs   = 60000
	EnrichGraceMinMs    = 5000
	EnrichGraceMaxMs    = 600000
	HostRetryMinMs      = 5000
	HostRetryMaxMs      = 600000
	ReconnectDelayMinMs = 200
	ReconnectDelayMaxMs = 30000
	GridPreviewMinMs    = 1000
	GridPreviewMaxMs    = 60000
)

// normMs keeps 0 ("use default") and in-range values; anything else resets to
// 0. Validation (SetSection) rejects out-of-range input; normalization (load,
// SetTiming) silently repairs persisted/corrupt values.
func normMs(v, min, max int) int {
	if v == 0 || (v >= min && v <= max) {
		return v
	}
	return 0
}

// Normalize clears every out-of-range timing field to 0 ("use default").
func (v *TimingPrefs) Normalize() {
	v.SnapshotPollMs = normMs(v.SnapshotPollMs, SnapshotPollMinMs, SnapshotPollMaxMs)
	v.EnrichGraceMs = normMs(v.EnrichGraceMs, EnrichGraceMinMs, EnrichGraceMaxMs)
	v.HostRetryMs = normMs(v.HostRetryMs, HostRetryMinMs, HostRetryMaxMs)
	v.ReconnectDelayMs = normMs(v.ReconnectDelayMs, ReconnectDelayMinMs, ReconnectDelayMaxMs)
	v.GridPreviewMs = normMs(v.GridPreviewMs, GridPreviewMinMs, GridPreviewMaxMs)
}

// Normalize repairs out-of-range persisted values (load path).
func (p *Prefs) Normalize() {
	p.Terminal.PollMs = normMs(p.Terminal.PollMs, TermPollMinMs, TermPollMaxMs)
	p.Timing.Normalize()
}

// Prefs holds user preferences in the section model (skills / shell_cmds /
// kind_overrides / prefix_by_agent / terminal), matching the original relay
// prefs_store.
type Prefs struct {
	Skills        []Skill                 `json:"skills,omitempty"`
	AgentCmds     []string                `json:"agent_cmds,omitempty"`
	ShellCmds     []string                `json:"shell_cmds,omitempty"`
	KindOverrides map[string]KindOverride `json:"kind_overrides,omitempty"`
	PrefixByAgent map[string]string       `json:"prefix_by_agent,omitempty"`
	Terminal      TerminalPrefs           `json:"terminal,omitempty"`
	Timing        TimingPrefs             `json:"timing,omitempty"`
}

// PrefsStore is a JSON file-backed Prefs store.
type PrefsStore struct {
	mu       sync.RWMutex
	filePath string
	data     Prefs
	onChange func(Prefs) // invoked after a successful SetSection (unlocked)
}

// NewPrefsStore creates or loads a PrefsStore from the given file path.
func NewPrefsStore(filePath string) (*PrefsStore, error) {
	s := &PrefsStore{filePath: filePath}
	if err := s.load(); err != nil {
		return nil, err
	}
	if s.data.KindOverrides == nil {
		s.data.KindOverrides = make(map[string]KindOverride)
	}
	if s.data.PrefixByAgent == nil {
		s.data.PrefixByAgent = map[string]string{"codex": "$", "default": "/"}
	}
	return s, nil
}

func (s *PrefsStore) load() error {
	b, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return err
	}
	// Persisted values never passed through SetSection validation — a
	// hand-edited file can carry out-of-range timers. Normalize rather than
	// fail so the relay still starts with sane defaults.
	s.data.Normalize()
	return nil
}

func (s *PrefsStore) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-prefs-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.filePath)
}

// Get returns a copy of the current prefs.
func (s *PrefsStore) Get() Prefs {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.data
	out.KindOverrides = make(map[string]KindOverride, len(s.data.KindOverrides))
	for k, v := range s.data.KindOverrides {
		out.KindOverrides[k] = v
	}
	out.PrefixByAgent = make(map[string]string, len(s.data.PrefixByAgent))
	for k, v := range s.data.PrefixByAgent {
		out.PrefixByAgent[k] = v
	}
	return out
}

// SetOnChange registers a callback invoked with a copy of the full prefs after
// every successful SetSection. It runs after the store lock is released, so it
// may call Get(). Used to push timing knobs into the poller without prefs
// consumers subscribing to the WS broadcast.
func (s *PrefsStore) SetOnChange(fn func(Prefs)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// SetSection replaces a whole section ("skills" | "shell_cmds" | "kind_overrides").
// data must be the JSON-marshalable value for that section.
func (s *PrefsStore) SetSection(section string, data json.RawMessage) error {
	s.mu.Lock()
	if err := s.setSectionLocked(section, data); err != nil {
		s.mu.Unlock()
		return err
	}
	err := s.save()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if fn := s.onChangeFn(); fn != nil {
		fn(s.Get())
	}
	return nil
}

func (s *PrefsStore) onChangeFn() func(Prefs) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.onChange
}

// setSectionLocked validates and applies a section update. Caller holds s.mu.
func (s *PrefsStore) setSectionLocked(section string, data json.RawMessage) error {
	switch section {
	case "skills":
		var v []Skill
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.data.Skills = v
	case "shell_cmds":
		var v []string
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.data.ShellCmds = v
	case "agent_cmds":
		var v []string
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.data.AgentCmds = v
	case "kind_overrides":
		var v map[string]KindOverride
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.data.KindOverrides = v
	case "prefix_by_agent":
		var v map[string]string
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.data.PrefixByAgent = v
	case "terminal":
		var v TerminalPrefs
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		if v.PollMs != 0 && (v.PollMs < TermPollMinMs || v.PollMs > TermPollMaxMs) {
			return fmt.Errorf("terminal.poll_ms must be 0 or in [%d, %d] (got %d)", TermPollMinMs, TermPollMaxMs, v.PollMs)
		}
		s.data.Terminal = v
	case "timing":
		var v TimingPrefs
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		if err := v.validate(); err != nil {
			return err
		}
		s.data.Timing = v
	default:
		return &InvalidSectionError{Section: section}
	}
	return nil
}

// validate enforces the per-key bounds: 0 means "use default", explicit values
// must be in range.
func (v TimingPrefs) validate() error {
	checks := []struct {
		name string
		ms   int
		min  int
		max  int
	}{
		{"timing.snapshot_poll_ms", v.SnapshotPollMs, SnapshotPollMinMs, SnapshotPollMaxMs},
		{"timing.enrich_grace_ms", v.EnrichGraceMs, EnrichGraceMinMs, EnrichGraceMaxMs},
		{"timing.host_retry_ms", v.HostRetryMs, HostRetryMinMs, HostRetryMaxMs},
		{"timing.reconnect_delay_ms", v.ReconnectDelayMs, ReconnectDelayMinMs, ReconnectDelayMaxMs},
		{"timing.grid_preview_ms", v.GridPreviewMs, GridPreviewMinMs, GridPreviewMaxMs},
	}
	for _, c := range checks {
		if c.ms != 0 && (c.ms < c.min || c.ms > c.max) {
			return fmt.Errorf("%s must be 0 or in [%d, %d] (got %d)", c.name, c.min, c.max, c.ms)
		}
	}
	return nil
}

// SetKindOverride sets an override for host:paneID.
func (s *PrefsStore) SetKindOverride(key, kind, displayAgent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.KindOverrides[key] = KindOverride{
		Kind:         kind,
		DisplayAgent: displayAgent,
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	return s.save()
}

// ClearKindOverride removes the override for key.
func (s *PrefsStore) ClearKindOverride(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.KindOverrides, key)
	return s.save()
}

// InvalidSectionError is returned for an unknown prefs section.
type InvalidSectionError struct{ Section string }

func (e *InvalidSectionError) Error() string { return "invalid section: " + e.Section }
