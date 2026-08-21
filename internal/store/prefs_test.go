package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestPrefsStore_Sections(t *testing.T) {
	s, err := NewPrefsStore(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}

	// skills
	skills := []Skill{{Label: "Review", Template: "{prefix} review this"}}
	data, _ := json.Marshal(skills)
	if err := s.SetSection("skills", data); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if len(got.Skills) != 1 || got.Skills[0].Label != "Review" {
		t.Errorf("skills = %+v", got.Skills)
	}

	// shell_cmds
	cmds, _ := json.Marshal([]string{"git status", "ls -la"})
	if err := s.SetSection("shell_cmds", cmds); err != nil {
		t.Fatal(err)
	}
	if len(s.Get().ShellCmds) != 2 {
		t.Errorf("shell_cmds = %+v", s.Get().ShellCmds)
	}

	// invalid section
	if err := s.SetSection("bogus", cmds); err == nil {
		t.Error("expected invalid section error")
	}
}

func TestPrefsStore_KindOverrides(t *testing.T) {
	s, err := NewPrefsStore(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetKindOverride("local:h:0.1", "agent", "claude"); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	ov, ok := got.KindOverrides["local:h:0.1"]
	if !ok || ov.DisplayAgent != "claude" || ov.Kind != "agent" {
		t.Errorf("override = %+v", got.KindOverrides)
	}
	if ov.UpdatedAt == "" {
		t.Error("expected updated_at set")
	}
	if err := s.ClearKindOverride("local:h:0.1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get().KindOverrides["local:h:0.1"]; ok {
		t.Error("override should be cleared")
	}
}

func TestPrefsStore_Persists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	s1, _ := NewPrefsStore(path)
	s1.SetSection("shell_cmds", json.RawMessage(`["make build"]`))

	s2, err := NewPrefsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Get().ShellCmds) != 1 || s2.Get().ShellCmds[0] != "make build" {
		t.Errorf("reloaded = %+v", s2.Get().ShellCmds)
	}
}

func TestPrefsStore_GetReturnsCopy(t *testing.T) {
	s, _ := NewPrefsStore(filepath.Join(t.TempDir(), "prefs.json"))
	s.SetKindOverride("k", "agent", "a")
	got := s.Get()
	got.KindOverrides["k"] = KindOverride{DisplayAgent: "MUTATED"}
	if s.Get().KindOverrides["k"].DisplayAgent == "MUTATED" {
		t.Error("Get returned shared map")
	}
}
