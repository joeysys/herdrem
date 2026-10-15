package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newHistory(t *testing.T) (*HistoryStore, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "history.json")
	h, err := NewHistoryStore(p)
	if err != nil {
		t.Fatal(err)
	}
	return h, p
}

func texts(es []HistoryEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Text
	}
	return out
}

func TestHistoryRecordDedupesWithinAWorkspaceAndCounts(t *testing.T) {
	h, _ := newHistory(t)
	e := HistoryEntry{Text: "git status", Host: "local", Workspace: "w1", Pane: "p1"}
	if _, err := h.Record(e, 100); err != nil {
		t.Fatal(err)
	}
	got, err := h.Record(HistoryEntry{Text: "git status", Host: "local", Workspace: "w1", Pane: "p2"}, 200)
	if err != nil {
		t.Fatal(err)
	}
	if got.Count != 2 || got.Time != 200 || got.Pane != "p2" {
		t.Fatalf("merged entry = %+v", got)
	}
	if n := len(h.All()); n != 1 {
		t.Fatalf("entries = %d, want 1", n)
	}
}

func TestHistorySameTextInTwoWorkspacesIsTwoEntries(t *testing.T) {
	h, _ := newHistory(t)
	_, _ = h.Record(HistoryEntry{Text: "make test", Host: "local", Workspace: "a"}, 1)
	_, _ = h.Record(HistoryEntry{Text: "make test", Host: "local", Workspace: "b"}, 2)
	_, _ = h.Record(HistoryEntry{Text: "make test", Host: "other", Workspace: "a"}, 3)
	if n := len(h.All()); n != 3 {
		t.Fatalf("entries = %d, want 3", n)
	}
}

func TestHistoryShellAndAgentAreSeparateKinds(t *testing.T) {
	h, _ := newHistory(t)
	_, _ = h.Record(HistoryEntry{Text: "status", Kind: HistoryShell}, 1)
	ag, err := h.Record(HistoryEntry{Text: "status", Kind: HistoryAgent, Agent: "claude", Title: "api"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ag.Kind != "ag" || ag.Agent != "claude" || ag.Title != "api" {
		t.Fatalf("agent entry = %+v", ag)
	}
	if n := len(h.All()); n != 2 {
		t.Fatalf("entries = %d, want 2", n)
	}
	sh := h.All()[1]
	if sh.Kind != "" || sh.Agent != "" {
		t.Fatalf("shell entry carries agent data: %+v", sh)
	}
}

func TestHistoryRejectsWhatShouldNotBeRemembered(t *testing.T) {
	h, _ := newHistory(t)
	cases := map[string]HistoryEntry{
		"blank":           {Text: ""},
		"whitespace":      {Text: "   "},
		"leading space":   {Text: " ls"},
		"shell multiline": {Text: "echo a\necho b"},
		"shell too long":  {Text: strings.Repeat("x", MaxShellLen+1)},
		"agent too long":  {Text: strings.Repeat("x", MaxAgentLen+1), Kind: HistoryAgent},
		"password flag":   {Text: "mysql --password hunter2"},
		"token assign":    {Text: "export API_TOKEN=abc"},
		"bearer":          {Text: `curl -H "Authorization: Bearer abc" x`},
		"url creds":       {Text: "git clone https://u:pw@host/r.git"},
		"private key":     {Text: "-----BEGIN RSA PRIVATE KEY-----", Kind: HistoryAgent},
		"secret in agent": {Text: "use password: hunter2 for the db", Kind: HistoryAgent},
	}
	for name, e := range cases {
		if _, err := h.Record(e, 1); err != ErrHistoryRejected {
			t.Errorf("%s: err = %v, want ErrHistoryRejected", name, err)
		}
	}
	if n := len(h.All()); n != 0 {
		t.Fatalf("stored %d rejected entries", n)
	}
}

func TestHistoryAcceptsLongMultilineAgentPromptsAndAmbiguousWords(t *testing.T) {
	h, _ := newHistory(t)
	for _, e := range []HistoryEntry{
		{Text: "line one\nline two", Kind: HistoryAgent},
		{Text: strings.Repeat("x", MaxShellLen+1), Kind: HistoryAgent},
		{Text: "cat bypass.txt"},
		{Text: "echo passenger"},
	} {
		if _, err := h.Record(e, 1); err != nil {
			t.Errorf("%q: %v", e.Text, err)
		}
	}
}

func TestHistoryClipsMetadata(t *testing.T) {
	h, _ := newHistory(t)
	e, err := h.Record(HistoryEntry{Text: "fix it", Kind: HistoryAgent, Agent: strings.Repeat("a", 500), Title: strings.Repeat("t", 500), WorkspaceLabel: strings.Repeat("l", 1000)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(e.Agent)) != maxHistoryMeta || len([]rune(e.Title)) != maxHistoryMeta || len([]rune(e.WorkspaceLabel)) != maxHistoryLabel {
		t.Fatalf("metadata not clipped: %d %d %d", len(e.Agent), len(e.Title), len(e.WorkspaceLabel))
	}
}

func TestHistoryCapsEachKindOnItsOwn(t *testing.T) {
	h, _ := newHistory(t)
	_, _ = h.Record(HistoryEntry{Text: "keep me"}, 1)
	for i := 0; i < MaxAgentEntries+25; i++ {
		_, err := h.Record(HistoryEntry{Text: "prompt " + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + itoa(i), Kind: HistoryAgent}, int64(10+i))
		if err != nil {
			t.Fatal(err)
		}
	}
	ag, sh := 0, 0
	for _, e := range h.All() {
		if e.Kind == HistoryAgent {
			ag++
		} else {
			sh++
		}
	}
	if ag != MaxAgentEntries || sh != 1 {
		t.Fatalf("agent=%d shell=%d, want %d and 1", ag, sh, MaxAgentEntries)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}

func TestHistoryIsNewestFirstAndPersists(t *testing.T) {
	h, p := newHistory(t)
	_, _ = h.Record(HistoryEntry{Text: "one", Host: "local", Workspace: "w"}, 1)
	_, _ = h.Record(HistoryEntry{Text: "two", Kind: HistoryAgent, Agent: "claude"}, 2)
	_, _ = h.Record(HistoryEntry{Text: "three"}, 3)
	if got := texts(h.All()); strings.Join(got, ",") != "three,two,one" {
		t.Fatalf("order = %v", got)
	}
	again, err := NewHistoryStore(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(again.All()); strings.Join(got, ",") != "three,two,one" {
		t.Fatalf("reloaded order = %v", got)
	}
	if again.All()[1].Agent != "claude" {
		t.Fatalf("reloaded lost agent: %+v", again.All()[1])
	}
}

func TestHistoryFileIsPrivate(t *testing.T) {
	h, p := newHistory(t)
	_, _ = h.Record(HistoryEntry{Text: "ls"}, 1)
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("history file mode = %v, want owner-only", st.Mode().Perm())
	}
}

func TestHistoryCorruptFileIsSetAsideNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "history.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := NewHistoryStore(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.All()) != 0 {
		t.Fatal("expected empty history")
	}
	matches, _ := filepath.Glob(p + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("corrupt file not preserved: %v", matches)
	}
}

func TestHistoryLoadDropsInvalidRows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "history.json")
	body := `[{"c":"ok","t":5,"n":2},{"c":"","t":4},{"c":"export TOKEN=x","t":3},{"c":" lead","t":2}]`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := NewHistoryStore(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(h.All()); strings.Join(got, ",") != "ok" {
		t.Fatalf("loaded %v, want [ok]", got)
	}
	if h.All()[0].Count != 2 {
		t.Fatalf("count lost: %+v", h.All()[0])
	}
}

func TestHistoryRemoveForgetsEveryWorkspaceButOnlyThatKind(t *testing.T) {
	h, _ := newHistory(t)
	_, _ = h.Record(HistoryEntry{Text: "x", Workspace: "a"}, 1)
	_, _ = h.Record(HistoryEntry{Text: "x", Workspace: "b"}, 2)
	_, _ = h.Record(HistoryEntry{Text: "x", Kind: HistoryAgent}, 3)
	ok, err := h.Remove("sh", "x")
	if err != nil || !ok {
		t.Fatalf("remove = %v, %v", ok, err)
	}
	all := h.All()
	if len(all) != 1 || all[0].Kind != HistoryAgent {
		t.Fatalf("remaining = %+v", all)
	}
	if ok, _ := h.Remove("sh", "nope"); ok {
		t.Fatal("removing an unknown entry reported success")
	}
}

func TestHistoryClearByKindAndWorkspace(t *testing.T) {
	h, _ := newHistory(t)
	_, _ = h.Record(HistoryEntry{Text: "a1", Host: "local", Workspace: "a"}, 1)
	_, _ = h.Record(HistoryEntry{Text: "b1", Host: "local", Workspace: "b"}, 2)
	_, _ = h.Record(HistoryEntry{Text: "a-prompt", Kind: HistoryAgent, Host: "local", Workspace: "a"}, 3)

	if n, _ := h.Clear("all", true, "local", "a"); n != 2 {
		t.Fatalf("scoped clear removed %d, want 2", n)
	}
	if got := texts(h.All()); strings.Join(got, ",") != "b1" {
		t.Fatalf("remaining = %v", got)
	}
	_, _ = h.Record(HistoryEntry{Text: "p", Kind: HistoryAgent}, 4)
	if n, _ := h.Clear("ag", false, "", ""); n != 1 {
		t.Fatalf("kind clear removed %d, want 1", n)
	}
	if n, _ := h.Clear("all", false, "", ""); n != 1 {
		t.Fatalf("relay-wide clear removed %d, want 1", n)
	}
	if len(h.All()) != 0 {
		t.Fatal("expected empty")
	}
}

func TestHistoryClearKeepsFavorites(t *testing.T) {
	h, _ := newHistory(t)
	_, _ = h.Record(HistoryEntry{Text: "keep", Host: "local", Workspace: "a"}, 1)
	_, _ = h.Record(HistoryEntry{Text: "drop", Host: "local", Workspace: "a"}, 2)
	if n, err := h.SetFavorite("", "keep", "local", "a", false, true); err != nil || n != 1 {
		t.Fatalf("favorite n=%d err=%v", n, err)
	}
	if _, err := h.Record(HistoryEntry{Text: "keep", Host: "local", Workspace: "a"}, 3); err != nil {
		t.Fatal(err)
	}
	got := h.All()[0]
	if got.Text != "keep" || !got.Fav || got.Count != 2 {
		t.Fatalf("re-record lost the star: %+v", got)
	}
	if n, _ := h.Clear("all", true, "local", "a"); n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	left := h.All()
	if len(left) != 1 || left[0].Text != "keep" || !left[0].Fav {
		t.Fatalf("remaining = %+v", left)
	}
	if n, _ := h.SetFavorite("", "keep", "local", "a", false, false); n != 1 || h.All()[0].Fav {
		t.Fatal("unfavorite failed")
	}
}

func TestHistoryImportMergesWithoutLosingWhatIsKnown(t *testing.T) {
	h, _ := newHistory(t)
	_, _ = h.Record(HistoryEntry{Text: "ls", Host: "local", Workspace: "w"}, 500)
	_, _ = h.Record(HistoryEntry{Text: "ls", Host: "local", Workspace: "w"}, 600) // count 2

	n, err := h.Import([]HistoryEntry{
		{Text: "ls", Time: 100, Count: 1, Host: "local", Workspace: "w"}, // older + lower count: no regression
		{Text: "old cmd", Time: 50, Count: 3},                            // no workspace: relay-wide only
		{Text: "export TOKEN=x", Time: 60, Count: 1},                     // secret: skipped
		{Text: "", Time: 70},                                             // blank: skipped
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("accepted %d, want 2", n)
	}
	var ls, old *HistoryEntry
	for _, e := range h.All() {
		e := e
		switch e.Text {
		case "ls":
			ls = &e
		case "old cmd":
			old = &e
		}
	}
	if ls == nil || ls.Count != 2 || ls.Time != 600 {
		t.Fatalf("ls regressed: %+v", ls)
	}
	if old == nil || old.Count != 3 || old.Workspace != "" {
		t.Fatalf("imported entry wrong: %+v", old)
	}
}

func TestHistoryImportIsBounded(t *testing.T) {
	h, _ := newHistory(t)
	var in []HistoryEntry
	for i := 0; i < MaxHistoryImport+100; i++ {
		in = append(in, HistoryEntry{Text: "cmd " + itoa(i), Time: int64(i + 1), Count: 1})
	}
	n, err := h.Import(in)
	if err != nil {
		t.Fatal(err)
	}
	if n != MaxHistoryImport {
		t.Fatalf("accepted %d, want %d", n, MaxHistoryImport)
	}
}
