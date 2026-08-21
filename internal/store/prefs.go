package store

import (
	"encoding/json"
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

// Prefs holds user preferences in the section model (skills / shell_cmds /
// kind_overrides / prefix_by_agent), matching the original relay prefs_store.
type Prefs struct {
	Skills        []Skill                 `json:"skills,omitempty"`
	AgentCmds     []string                `json:"agent_cmds,omitempty"`
	ShellCmds     []string                `json:"shell_cmds,omitempty"`
	KindOverrides map[string]KindOverride `json:"kind_overrides,omitempty"`
	PrefixByAgent map[string]string       `json:"prefix_by_agent,omitempty"`
}

// PrefsStore is a JSON file-backed Prefs store.
type PrefsStore struct {
	mu       sync.RWMutex
	filePath string
	data     Prefs
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
	return json.Unmarshal(b, &s.data)
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

// SetSection replaces a whole section ("skills" | "shell_cmds" | "kind_overrides").
// data must be the JSON-marshalable value for that section.
func (s *PrefsStore) SetSection(section string, data json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	default:
		return &InvalidSectionError{Section: section}
	}
	return s.save()
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
