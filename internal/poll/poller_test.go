package poll

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"herdrem/internal/herdr"
	"herdrem/internal/hub"
	"herdrem/internal/service"
)

// TestPollerBroadcastsAgentsWithOptions verifies the poll → hub → WS client
// pipeline carries the full Pane payload, including approval options.
func TestPollerBroadcastsAgentsWithOptions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	// ReadContent is a real numbered TUI menu so the poller's menu detection
	// parses it and attaches real options + labels to the blocked pane.
	client := &herdr.FakeClient{
		ReadContent: "Allow write to /home/user/myapp?\n\n❯ 1. Yes\n  2. No",
	}
	p := New(client, nil, nil, h, logger, 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go h.Run(ctx)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		h.HandleConn(ctx, conn, "", nil)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	go p.Run(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		readCtx, rcancel := context.WithTimeout(ctx, 1*time.Second)
		_, data, err := conn.Read(readCtx)
		rcancel()
		if err != nil {
			continue
		}
		var msg map[string]interface{}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg["type"] != "agents" {
			continue
		}
		agents, ok := msg["agents"].([]interface{})
		if !ok || len(agents) != 2 {
			t.Fatalf("expected 2 agents, got %v", msg["agents"])
		}
		for _, a := range agents {
			pane := a.(map[string]interface{})
			if pane["status"] != "blocked" {
				continue
			}
			opts, ok := pane["options"].([]interface{})
			if !ok || len(opts) != 2 || opts[0] != "1" || opts[1] != "2" {
				t.Fatalf("blocked pane missing parsed options: %v", pane)
			}
			labels, ok := pane["option_labels"].([]interface{})
			if !ok || len(labels) != 2 || labels[0] != "Yes" || labels[1] != "No" {
				t.Fatalf("blocked pane missing parsed option_labels: %v", pane)
			}
			return // pass
		}
		t.Fatal("no blocked pane in agents snapshot")
	}
	t.Fatal("no agents snapshot received within deadline")
}

func TestPollerEnrichLastActiveKeepsWorkingAgentsFresh(t *testing.T) {
	p := New(nil, nil, nil, nil, nil, time.Second)
	panes := []herdr.Pane{
		{PaneID: "%agent", Kind: "agent", Status: "working", Revision: 36},
		{PaneID: "%shell", Kind: "shell", Status: "unknown", Revision: 37},
	}

	p.enrichLastActive(panes)
	p.lastActive["%agent"] = 1
	p.lastActive["%shell"] = 1

	if p.enrichLastActive(panes) {
		t.Fatal("working-only refresh should not require a state save")
	}
	if panes[0].LastActive <= 1 {
		t.Fatal("working agent did not refresh last_active with an unchanged revision")
	}
	if panes[1].LastActive != 1 {
		t.Fatal("idle shell should keep last_active when its revision is unchanged")
	}
}

func TestPollerAckDoneStaysIdleUntilNextActivity(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	statePath := filepath.Join(t.TempDir(), "poller.json")
	client := &herdr.FakeClient{Panes: []herdr.Pane{{
		PaneID: "%agent", Kind: "agent", Agent: "claude", Status: "working", AgentStatus: "working",
	}}}
	p := New(client, nil, nil, hub.New(logger), logger, time.Second)
	p.SetStatePath(statePath)

	p.pollOnce(context.Background())
	if p.AckDone("%agent") {
		t.Fatal("working pane acknowledgement was accepted")
	}
	p = New(client, nil, nil, hub.New(logger), logger, time.Second)
	p.SetStatePath(statePath)
	client.Panes[0].Status, client.Panes[0].AgentStatus = "done", "done"
	p.pollOnce(context.Background())
	if got := p.Snapshot()[0].Status; got != "done" {
		t.Fatalf("working pane was acknowledged before completion: %q", got)
	}

	if !p.AckDone("%agent") {
		t.Fatal("done pane acknowledgement was rejected")
	}
	p = New(client, nil, nil, hub.New(logger), logger, time.Second)
	p.SetStatePath(statePath)
	client.Panes[0].Status, client.Panes[0].AgentStatus = "done", "done"
	p.pollOnce(context.Background())
	if got := p.Snapshot()[0].Status; got != "idle" {
		t.Fatalf("acknowledged done pane status = %q, want idle", got)
	}
	client.Panes[0].Status, client.Panes[0].AgentStatus = "done", "done"
	p.pollOnce(context.Background())
	if got := p.Snapshot()[0].Status; got != "idle" {
		t.Fatalf("acknowledged pane reverted to %q before new activity", got)
	}

	client.Panes[0].Status, client.Panes[0].AgentStatus = "working", "working"
	p.pollOnce(context.Background())
	p = New(client, nil, nil, hub.New(logger), logger, time.Second)
	p.SetStatePath(statePath)
	client.Panes[0].Status, client.Panes[0].AgentStatus = "done", "done"
	p.pollOnce(context.Background())
	if got := p.Snapshot()[0].Status; got != "done" {
		t.Fatalf("new completion status = %q, want done", got)
	}
}

func TestPollerEnrichValidPaths(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)

	tmpDir := t.TempDir()
	existingFile := "app.go"
	os.WriteFile(tmpDir+"/"+existingFile, []byte("package main"), 0644)

	client := &herdr.FakeClient{
		ReadContent: "checking app.go and missing.py",
	}
	p := New(client, nil, nil, h, logger, 50*time.Millisecond)
	fileSvc := service.NewFileService(func(host string) []string { return []string{tmpDir} }, logger)
	p.SetFileService(fileSvc)

	panes := []herdr.Pane{
		{PaneID: "%1", Host: "local", Cwd: tmpDir, Revision: 1},
	}

	p.enrichLastActive(panes)
	p.enrichValidPaths(context.Background(), panes)

	if len(panes[0].ValidPaths) == 0 || !strings.HasSuffix(panes[0].ValidPaths[0], "app.go") {
		t.Fatalf("expected valid_paths to contain app.go, got: %v", panes[0].ValidPaths)
	}

	// Test non-matching content returns empty non-nil slice
	client.ReadContent = "no paths here"
	panes[0].Revision = 2
	p.enrichLastActive(panes)
	p.enrichValidPaths(context.Background(), panes)

	if panes[0].ValidPaths == nil || len(panes[0].ValidPaths) != 0 {
		t.Fatalf("expected valid_paths to be empty slice [], got: %v", panes[0].ValidPaths)
	}
}

func TestPollerEnrichValidPathsDirectoryContextAndLineUnwrap(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)

	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "docs", "agents", "reviews")
	os.MkdirAll(subDir, 0755)

	os.WriteFile(filepath.Join(subDir, "turn-fix-demo-1.md"), []byte("doc1"), 0644)
	os.WriteFile(filepath.Join(subDir, "turn-claim-demo-0.md"), []byte("doc2"), 0644)
	os.WriteFile(filepath.Join(subDir, "review-demo-wrap.md"), []byte("doc3"), 0644)

	client := &herdr.FakeClient{
		ReadContent: `Consumed docs: docs/agents/reviews/turn-fix-demo-1.md、turn-claim-demo-0.md、review-demo-w
  rap.md`,
	}
	p := New(client, nil, nil, h, logger, 50*time.Millisecond)
	fileSvc := service.NewFileService(func(host string) []string { return []string{tmpDir} }, logger)
	p.SetFileService(fileSvc)

	panes := []herdr.Pane{
		{PaneID: "%1", Host: "local", Cwd: tmpDir, Revision: 1},
	}

	p.enrichValidPaths(context.Background(), panes)

	validMap := make(map[string]bool)
	for _, v := range panes[0].ValidPaths {
		validMap[v] = true
	}

	if !validMap["turn-claim-demo-0.md"] || !validMap["docs/agents/reviews/turn-claim-demo-0.md"] {
		t.Fatalf("expected turn-claim-demo-0.md to be probed and resolved, got: %v", panes[0].ValidPaths)
	}

	if !validMap["review-demo-wrap.md"] && !validMap["docs/agents/reviews/review-demo-wrap.md"] {
		t.Fatalf("expected soft line-wrapped review-demo-wrap.md to be resolved, got: %v", panes[0].ValidPaths)
	}
}

func TestPollerEnrichValidPathsRefreshesWhenStateChangeSeqChanges(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	tmpDir := t.TempDir()
	for _, name := range []string{"old.md", "new.md"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	client := &herdr.FakeClient{ReadContent: "old.md"}
	p := New(client, nil, nil, h, logger, 50*time.Millisecond)
	p.SetFileService(service.NewFileService(func(host string) []string { return []string{tmpDir} }, logger))
	panes := []herdr.Pane{{PaneID: "%1", Host: "local", Cwd: tmpDir, Revision: 1, StateChangeSeq: 1}}

	p.enrichValidPaths(context.Background(), panes)
	client.ReadContent = "new.md"
	panes[0].StateChangeSeq = 2
	p.enrichValidPaths(context.Background(), panes)

	for _, path := range panes[0].ValidPaths {
		if path == "new.md" {
			return
		}
	}
	t.Fatalf("expected state_change_seq update to refresh valid_paths, got: %v", panes[0].ValidPaths)
}

func TestPollerEnrichValidPathsCapPreservesNewestRelativePaths(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "docs", "agents", "reviews")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create 250 bare-filename .md files in the reviews dir (each will trigger
	// a dir-context probe, which previously consumed the 200-item budget).
	var bareFiles []string
	for i := 0; i < 250; i++ {
		name := fmt.Sprintf("bare-%03d.md", i)
		if err := os.WriteFile(filepath.Join(subDir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		bareFiles = append(bareFiles, name)
	}

	// The important file appears at the END of the pane content (after all
	// bare filenames). Previously it was truncated by the 200-item cap.
	important := "docs/agents/reviews/turn-verify-agent-design-fresh-20260807-1421-5.md"
	if err := os.WriteFile(filepath.Join(tmpDir, important), []byte("important"), 0644); err != nil {
		t.Fatal(err)
	}

	// Build pane content: 250 bare filenames first, then the important path.
	var content strings.Builder
	for _, name := range bareFiles {
		fmt.Fprintf(&content, "  • %s\n", name)
	}
	fmt.Fprintf(&content, "Output docs:\n  • %s\n", important)

	client := &herdr.FakeClient{ReadContent: content.String()}
	p := New(client, nil, nil, h, logger, 50*time.Millisecond)
	p.SetFileService(service.NewFileService(func(host string) []string { return []string{tmpDir} }, logger))
	panes := []herdr.Pane{{PaneID: "%1", Host: "local", Cwd: tmpDir, Revision: 1}}

	p.enrichValidPaths(context.Background(), panes)

	found := false
	for _, v := range panes[0].ValidPaths {
		if v == important {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s to survive the cap and appear in valid_paths (got %d entries)", important, len(panes[0].ValidPaths))
	}
}

func TestPollerEnrichValidPathsSoftWrapBeforeExtension(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)

	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "src", "store")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "menus.store.ts"), []byte("export {}"), 0644); err != nil {
		t.Fatal(err)
	}

	client := &herdr.FakeClient{ReadContent: "src/store/menus.store\n  .ts"}
	p := New(client, nil, nil, h, logger, 50*time.Millisecond)
	p.SetFileService(service.NewFileService(func(host string) []string { return []string{tmpDir} }, logger))
	panes := []herdr.Pane{{PaneID: "%1", Host: "local", Cwd: tmpDir, Revision: 1}}

	p.enrichValidPaths(context.Background(), panes)

	for _, path := range panes[0].ValidPaths {
		if path == "src/store/menus.store.ts" {
			return
		}
	}
	t.Fatalf("expected wrapped path to be resolved, got: %v", panes[0].ValidPaths)
}

func TestDiffContent(t *testing.T) {
	tests := []struct {
		name       string
		prev, cur  string
		wantDelta  string
		wantAppend bool
	}{
		{
			name:       "append only",
			prev:       "line1\nline2\n",
			cur:        "line1\nline2\nline3\n",
			wantDelta:  "line3\n",
			wantAppend: true,
		},
		{
			name:       "append preserves ansi",
			prev:       "\x1b[31mred\x1b[0m\n",
			cur:        "\x1b[31mred\x1b[0m\n\x1b[32mgreen\x1b[0m\n",
			wantDelta:  "\x1b[32mgreen\x1b[0m\n",
			wantAppend: true,
		},
		{
			name:       "no previous snapshot forces full",
			prev:       "",
			cur:        "anything",
			wantDelta:  "anything",
			wantAppend: false,
		},
		{
			name:       "window shift forces full",
			prev:       "old line one\nold line two\n",
			cur:        "brand new content\ncompletely different\n",
			wantDelta:  "brand new content\ncompletely different\n",
			wantAppend: false,
		},
		{
			name:       "content rewritten in place forces full",
			prev:       "old text",
			cur:        "new text",
			wantDelta:  "new text",
			wantAppend: false,
		},
		{
			name:       "lone carriage return in delta forces full",
			prev:       "progress: 10%\n",
			cur:        "progress: 10%\nprogress: 20%\r\x1b[K",
			wantDelta:  "progress: 10%\nprogress: 20%\r\x1b[K",
			wantAppend: false,
		},
		{
			name:       "identical content yields empty delta append",
			prev:       "same\n",
			cur:        "same\n",
			wantDelta:  "",
			wantAppend: true,
		},
		{
			name:       "crlf output appends cleanly",
			prev:       "line1\r\n",
			cur:        "line1\r\nline2\r\n",
			wantDelta:  "line2\r\n",
			wantAppend: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delta, appendMode := diffContent(tt.prev, tt.cur)
			if delta != tt.wantDelta {
				t.Errorf("diffContent delta = %q, want %q", delta, tt.wantDelta)
			}
			if appendMode != tt.wantAppend {
				t.Errorf("diffContent append = %v, want %v", appendMode, tt.wantAppend)
			}
		})
	}
}

func TestDiffContentWindowShift(t *testing.T) {
	// Build a pane content that exceeds the rolling window so the read shifts.
	// prev: 40 lines; cur = prev[5:] (5 scrolled out) + 3 new lines.
	prevLines := make([]string, 40)
	for i := range prevLines {
		prevLines[i] = fmt.Sprintf("line%03d", i)
	}
	prev := strings.Join(prevLines, "\n") + "\n"
	newLines := []string{"new1", "new2", "new3"}
	cur := strings.Join(append(prevLines[5:], newLines...), "\n") + "\n"

	delta, appendMode := diffContent(prev, cur)
	if !appendMode {
		t.Fatalf("expected append mode for window shift, got full")
	}
	if delta != "new1\nnew2\nnew3\n" {
		t.Errorf("delta = %q, want %q", delta, "new1\nnew2\nnew3\n")
	}
}

func TestDiffContentTrueDiscontinuity(t *testing.T) {
	prev := "old content\ncompletely different\n"
	cur := "brand new session\nnothing in common\n"
	delta, appendMode := diffContent(prev, cur)
	if appendMode {
		t.Fatalf("expected full mode for discontinuity, got append (delta=%q)", delta)
	}
	if delta != cur {
		t.Errorf("full delta = %q, want %q", delta, cur)
	}
}

func TestDiffContentShiftedWithAnsi(t *testing.T) {
	// ANSI-styled lines survive the shift and the new lines carry their own ANSI.
	prevLines := make([]string, 30)
	for i := range prevLines {
		prevLines[i] = fmt.Sprintf("\x1b[31mline%02d\x1b[0m", i)
	}
	prev := strings.Join(prevLines, "\n") + "\n"
	curLines := append(prevLines[2:], "\x1b[33myellow\x1b[0m")
	cur := strings.Join(curLines, "\n") + "\n"

	delta, appendMode := diffContent(prev, cur)
	if !appendMode {
		t.Fatalf("expected append for shifted ANSI content, got full")
	}
	if delta != "\x1b[33myellow\x1b[0m\n" {
		t.Errorf("delta = %q, want %q", delta, "\x1b[33myellow\x1b[0m\n")
	}
}

func TestShiftedAppendPrefersLongestOverlap(t *testing.T) {
	// cur's head could match prev at several shifts; the longest consecutive
	// run must win (that is the real window overlap, not a coincidental match).
	prevLines := make([]string, 40)
	for i := range prevLines {
		prevLines[i] = fmt.Sprintf("l%03d", i)
	}
	// prevLines[3] = "l003" also equals line 3 of cur after shift 3. Add a
	// coincidental early match: make prevLines[1] == prevLines[3] so both k=1
	// and k=3 are candidates; k=3 gives the longer run.
	prevLines[1] = prevLines[3]
	prev := strings.Join(prevLines, "\n") + "\n"
	curLines := append(append([]string{}, prevLines[3:]...), "tailA", "tailB")
	cur := strings.Join(curLines, "\n") + "\n"

	delta, ok := shiftedAppend(prev, cur)
	if !ok {
		t.Fatal("expected align found")
	}
	if delta != "tailA\ntailB\n" {
		t.Errorf("delta = %q, want %q", delta, "tailA\ntailB\n")
	}
}

// A full-screen TUI pane (scrollback 0) is identified so the relay pushes a
// full snapshot instead of attempting an incremental append against a redrawn
// screen.
func TestClassifyPaneContent(t *testing.T) {
	// scrollback metadata present with offset > 0 → incremental is safe
	scrollback := herdr.Pane{PaneID: "codex:1", Scroll: &herdr.ScrollInfo{MaxOffsetFromBottom: 500}}
	if classifyPaneContent(&scrollback) != contentIncremental {
		t.Error("scrollback pane should be incremental")
	}
	// scroll max_offset 0 → full-screen TUI → full
	tui := herdr.Pane{PaneID: "grok:1", Scroll: &herdr.ScrollInfo{MaxOffsetFromBottom: 0}}
	if classifyPaneContent(&tui) != contentFull {
		t.Error("scroll max_offset 0 should be full (TUI)")
	}
	// NO scroll metadata (older herdr schema) → full is the safe fallback
	// because we cannot prove the pane is not a TUI
	noScroll := herdr.Pane{PaneID: "plain:1"}
	if classifyPaneContent(&noScroll) != contentFull {
		t.Error("pane without scroll info must fall back to full")
	}
	if classifyPaneContent(nil) != contentFull {
		t.Error("nil pane should be full")
	}
}

// The full-content guard must bypass diffContent: two screens that are pure
// suffixes would otherwise be delivered as an append, corrupting the view.
func TestFullGuardBypassesDiffContent(t *testing.T) {
	// precondition: a screen that grows by a suffix WOULD be a valid append
	delta, appendMode := diffContent("screen\n", "screen\nstatus: 1\n")
	if !appendMode || delta != "status: 1\n" {
		t.Fatalf("precondition: expected suffix append, got append=%v delta=%q", appendMode, delta)
	}
	// classifyPaneContent must be full for a scroll-0 pane regardless of content
	pane := herdr.Pane{PaneID: "tui:1", Scroll: &herdr.ScrollInfo{MaxOffsetFromBottom: 0}}
	if classifyPaneContent(&pane) != contentFull {
		t.Fatal("expected full for TUI")
	}
}

// A partial line completed in the overlap (streaming half-line) is NOT a
// clean window shift — emitting it as append would duplicate the partial line
// on the client. Must fall back to full.
func TestShiftedAppendPartialLineCompletion(t *testing.T) {
	prevLines := make([]string, 60)
	for i := range prevLines {
		prevLines[i] = fmt.Sprintf("line%03d", i)
	}
	prevLines[59] = "hello wor" // partial
	prev := strings.Join(prevLines, "\n") + "\n"
	cur := strings.Join(append(append([]string{}, prevLines[3:59]...), "hello world", "next"), "\n") + "\n"
	delta, appendMode := diffContent(prev, cur)
	if appendMode {
		t.Fatalf("partial-line completion must be FULL, got append delta=%q", delta)
	}
}

// A mid-overlap line rewrite is also not a clean shift → full.
func TestShiftedAppendMidOverlapChanged(t *testing.T) {
	prevLines := make([]string, 60)
	for i := range prevLines {
		prevLines[i] = fmt.Sprintf("line%03d", i)
	}
	prev := strings.Join(prevLines, "\n") + "\n"
	curLines := append([]string{}, prevLines[3:]...)
	curLines[40-3] = "CHANGED"
	curLines = append(curLines, "new1")
	cur := strings.Join(curLines, "\n") + "\n"
	delta, appendMode := diffContent(prev, cur)
	if appendMode {
		t.Fatalf("mid-overlap rewrite must be FULL, got append delta=%q", delta)
	}
}

// A window shift whose new tail has NO trailing newline must not inject one
// (that would drift the client's content one \n past the server baseline).
func TestShiftedAppendNoTrailingNewline(t *testing.T) {
	prevLines := make([]string, 40)
	for i := range prevLines {
		prevLines[i] = fmt.Sprintf("line%03d", i)
	}
	prev := strings.Join(prevLines, "\n") + "\n"
	curLines := append([]string{}, prevLines[3:]...)
	curLines = append(curLines, "partial-no-newline")
	cur := strings.Join(curLines, "\n") // NO trailing \n
	delta, appendMode := diffContent(prev, cur)
	if !appendMode {
		t.Fatalf("expected append for clean shift, got full")
	}
	if delta != "partial-no-newline" {
		t.Errorf("delta = %q, want %q (no trailing newline)", delta, "partial-no-newline")
	}
}
