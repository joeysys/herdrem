package herdr

import (
	"os"
	"testing"
)

// TestParseSnapshotContract decodes a captured herdr 0.9.0 `api snapshot`
// response (testdata/api-snapshot.json) and verifies the merge that restores
// state_change_seq onto panes — the field pane list no longer returns in 0.9.
func TestParseSnapshotContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/api-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := parseSnapshot(raw)
	if err != nil {
		t.Fatalf("parseSnapshot: %v", err)
	}
	if snap.Protocol < MinProtocol {
		t.Fatalf("protocol = %d, want >= %d", snap.Protocol, MinProtocol)
	}
	if snap.Version == "" {
		t.Fatal("version missing from snapshot")
	}
	if len(snap.Panes) == 0 || len(snap.Agents) == 0 || len(snap.Workspaces) == 0 {
		t.Fatalf("snapshot missing panes/agents/workspaces: %d/%d/%d",
			len(snap.Panes), len(snap.Agents), len(snap.Workspaces))
	}
	// Contract check: herdr 0.9 panes[] must NOT carry state_change_seq — it
	// lives on agents[] only. If a future herdr adds it back this test still
	// holds (merge is a no-op for identical values), but the agents[] side
	// must always provide it.
	agentSeqs := map[string]int{}
	for _, a := range snap.Agents {
		agentSeqs[a.PaneID] = a.StateChangeSeq
	}

	mergeSnapshotAgents(snap)
	normalizePanes(snap.Panes, "local")

	for _, p := range snap.Panes {
		seq, isAgent := agentSeqs[p.PaneID]
		if !isAgent {
			continue
		}
		if p.StateChangeSeq != seq {
			t.Errorf("pane %s state_change_seq = %d, want merged %d", p.PaneID, p.StateChangeSeq, seq)
		}
		if p.Kind != "agent" {
			t.Errorf("pane %s kind = %q, want agent", p.PaneID, p.Kind)
		}
		if p.Host != "local" {
			t.Errorf("pane %s host = %q, want local", p.PaneID, p.Host)
		}
	}
	// At least one merged pane must have a non-zero seq — otherwise the
	// fixture drifted from the real schema.
	merged := false
	for _, p := range snap.Panes {
		if p.StateChangeSeq > 0 {
			merged = true
			break
		}
	}
	if !merged {
		t.Fatal("no pane received a non-zero state_change_seq — merge broken or fixture stale")
	}
}

// TestParseSnapshotRejectsOldProtocol ensures a snapshot below protocol 22 is
// a hard error — the relay cannot run correctly on herdr < 0.9.
func TestParseSnapshotRejectsOldProtocol(t *testing.T) {
	_, err := parseSnapshot([]byte(`{"id":"x","result":{"snapshot":{"protocol":21,"version":"0.8.0","panes":[]}}}`))
	if err == nil {
		t.Fatal("expected protocol gate to reject protocol 21")
	}
}

// TestParseSnapshotRejectsMissingSnapshot covers the pre-0.9 shape where the
// command exists but returns no snapshot key.
func TestParseSnapshotRejectsMissingSnapshot(t *testing.T) {
	_, err := parseSnapshot([]byte(`{"id":"x","result":{"type":"api_snapshot"}}`))
	if err == nil {
		t.Fatal("expected error for missing snapshot payload")
	}
}

// TestParsePaneListContract keeps the pane list decode path honest against a
// captured 0.9 envelope (debug/test path only — runtime uses api snapshot).
func TestParsePaneListContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/pane-list.json")
	if err != nil {
		t.Fatal(err)
	}
	panes, err := parsePaneList(raw)
	if err != nil {
		t.Fatalf("parsePaneList: %v", err)
	}
	if len(panes) == 0 {
		t.Fatal("pane list fixture decoded to zero panes")
	}
	for _, p := range panes {
		if p.PaneID == "" {
			t.Error("pane missing pane_id")
		}
	}
}
