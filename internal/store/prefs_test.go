package store

import (
	"encoding/json"
	"os"
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

func TestPrefsStore_TerminalSection(t *testing.T) {
	s, err := NewPrefsStore(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatal()
	}

	// default: unset → zero value
	if got := s.Get().Terminal.PollMs; got != 0 {
		t.Fatalf("default poll_ms = %d, want 0", got)
	}

	// valid set
	if err := s.SetSection("terminal", json.RawMessage(`{"poll_ms":3000}`)); err != nil {
		t.Fatal(err)
	}
	if got := s.Get().Terminal.PollMs; got != 3000 {
		t.Fatalf("poll_ms = %d, want 3000", got)
	}

	// persisted across reload
	s2, err := NewPrefsStore(s.filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get().Terminal.PollMs; got != 3000 {
		t.Fatalf("reloaded poll_ms = %d, want 3000", got)
	}

	// out-of-range values rejected
	for _, bad := range []string{`{"poll_ms":500}`, `{"poll_ms":61000}`, `{"poll_ms":-1000}`} {
		if err := s.SetSection("terminal", json.RawMessage(bad)); err == nil {
			t.Errorf("poll_ms %s should be rejected", bad)
		}
	}
	// 0 is accepted (means "use client default")
	if err := s.SetSection("terminal", json.RawMessage(`{"poll_ms":0}`)); err != nil {
		t.Fatalf("poll_ms=0 should reset to default: %v", err)
	}
	if got := s.Get().Terminal.PollMs; got != 0 {
		t.Fatalf("poll_ms = %d after reset, want 0", got)
	}
}

func TestPrefsStore_TimingSection(t *testing.T) {
	s, err := NewPrefsStore(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}

	// default: unset → zero values
	if got := s.Get().Timing; got.SnapshotPollMs != 0 || got.EnrichGraceMs != 0 {
		t.Fatalf("default timing = %+v, want zero", got)
	}

	// valid set + persisted across reload
	if err := s.SetSection("timing", json.RawMessage(`{"snapshot_poll_ms":5000,"enrich_grace_ms":60000,"host_retry_ms":45000,"reconnect_delay_ms":5000,"grid_preview_ms":10000,"auto_mark_read_ms":15000}`)); err != nil {
		t.Fatal(err)
	}
	s2, err := NewPrefsStore(s.filePath)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Get().Timing
	if got.SnapshotPollMs != 5000 || got.EnrichGraceMs != 60000 || got.HostRetryMs != 45000 ||
		got.ReconnectDelayMs != 5000 || got.GridPreviewMs != 10000 || got.AutoMarkReadMs != 15000 {
		t.Fatalf("reloaded timing = %+v", got)
	}

	// out-of-range values rejected
	for _, bad := range []string{
		`{"snapshot_poll_ms":100}`, `{"snapshot_poll_ms":61000}`,
		`{"enrich_grace_ms":1000}`, `{"enrich_grace_ms":600001}`,
		`{"host_retry_ms":4999}`, `{"reconnect_delay_ms":100}`,
		`{"grid_preview_ms":999}`, `{"snapshot_poll_ms":-5}`,
		`{"auto_mark_read_ms":999}`, `{"auto_mark_read_ms":600001}`,
	} {
		if err := s.SetSection("timing", json.RawMessage(bad)); err == nil {
			t.Errorf("timing %s should be rejected", bad)
		}
	}
	// all-zero is accepted (means "use defaults")
	if err := s.SetSection("timing", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("empty timing should reset to defaults: %v", err)
	}
}

func TestPrefsStore_TimingSection_OnChange(t *testing.T) {
	s, err := NewPrefsStore(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Prefs
	calls := 0
	s.SetOnChange(func(p Prefs) { calls++; got = p })
	if err := s.SetSection("timing", json.RawMessage(`{"snapshot_poll_ms":4000}`)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || got.Timing.SnapshotPollMs != 4000 {
		t.Fatalf("onChange calls=%d timing=%+v", calls, got.Timing)
	}
	// failed validation must not fire the hook
	if err := s.SetSection("timing", json.RawMessage(`{"snapshot_poll_ms":1}`)); err == nil {
		t.Fatal("expected rejection")
	}
	if calls != 1 {
		t.Fatalf("onChange fired on rejected update: calls=%d", calls)
	}
}

// Persisted values bypass SetSection validation — a hand-edited or corrupt
// prefs.json must be normalized on load, not fed raw into the poller (a huge
// snapshot_poll_ms would overflow time.Duration and panic time.NewTicker).
func TestPrefsStore_LoadNormalizesOutOfRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	raw := `{"terminal":{"poll_ms":-1},"timing":{"snapshot_poll_ms":9223372036854775807,"enrich_grace_ms":10,"host_retry_ms":90000,"reconnect_delay_ms":-5,"grid_preview_ms":12000}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewPrefsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.Terminal.PollMs != 0 {
		t.Errorf("terminal.poll_ms = %d, want 0 (normalized)", got.Terminal.PollMs)
	}
	want := TimingPrefs{SnapshotPollMs: 0, EnrichGraceMs: 0, HostRetryMs: 90000, ReconnectDelayMs: 0, GridPreviewMs: 12000}
	if got.Timing != want {
		t.Errorf("timing = %+v, want %+v", got.Timing, want)
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
