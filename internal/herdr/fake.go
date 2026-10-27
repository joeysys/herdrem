package herdr

import (
	"context"
	"fmt"
	"sync"
)

// FakeClient is a mock HerdrClient for testing.
type FakeClient struct {
	Panes []Pane
	// ReadContent overrides the content returned by ReadPane (e.g. a numbered
	// menu, to exercise menu detection). Empty returns a default prompt.
	ReadContent    string
	lastSentTextMu sync.RWMutex
	lastSentText   string
	sendTextCalls  int
	// SendTextErr, when set, is returned from SendText after the call is recorded.
	SendTextErr error
	sentKeys    []string
	// SendKeysErr, when set, is returned from SendKeys after the key is recorded.
	SendKeysErr error
	// paneSeq is a per-instance counter for generated pane IDs, so separate
	// FakeClient instances do not share/race on one package-level counter.
	paneSeq int
	// SendBlock, when set, stalls the first SendText until it is closed.
	SendBlock       chan struct{}
	closeCalls      int
	removeCalls     int
	worktreeCreates int
	agentStarts     int
	moveCalls       int
	lastMoveIndex   int
	lastCloseGroup  bool
	CloseErr        error
	RemoveErr       error
	WorktreeErr     error
	AgentStartErr   error
}

func (f *FakeClient) Alive(ctx context.Context, host string) (bool, error) {
	return true, nil
}

func (f *FakeClient) ListPanes(ctx context.Context, host string) ([]Pane, error) {
	if f.Panes == nil {
		return []Pane{
			{PaneID: "demo:0.1", Agent: "devin", Project: "herdrem", Cwd: "/home/user/herdrem", Host: "local", Status: "working", Kind: "agent", WorkspaceID: "ws1", WorkspaceLabel: "herdrem"},
			{PaneID: "demo:0.2", Agent: "cursor", Project: "myapp", Cwd: "/home/user/myapp", Host: "local", Status: "blocked", Kind: "agent", WorkspaceID: "ws2", WorkspaceLabel: "myapp", Prompt: "Allow file write?", Options: []string{"y", "n"}, OptionLabels: []string{"Yes, allow", "No, skip"}},
		}, nil
	}
	return f.Panes, nil
}

func (f *FakeClient) ListWorkspaces(ctx context.Context, host string) ([]Workspace, error) {
	return []Workspace{
		{WorkspaceID: "ws1", Label: "herdrem", PaneCount: 1},
		{WorkspaceID: "ws2", Label: "myapp", PaneCount: 1},
	}, nil
}

// Snapshot returns a synthesized api-snapshot-shaped result: the fake's panes
// (with whatever fields the test set directly, including StateChangeSeq) plus
// an agents[] array derived from them, mirroring the real merge contract.
func (f *FakeClient) Snapshot(ctx context.Context, host string) (*Snapshot, error) {
	panes, err := f.ListPanes(ctx, host)
	if err != nil {
		return nil, err
	}
	ws, _ := f.ListWorkspaces(ctx, host)
	var agents []Agent
	for _, p := range panes {
		if p.Kind == "agent" || p.Agent != "" {
			agents = append(agents, Agent{
				PaneID:         p.PaneID,
				Agent:          p.Agent,
				AgentStatus:    p.AgentStatus,
				StateChangeSeq: p.StateChangeSeq,
				Revision:       p.Revision,
				WorkspaceID:    p.WorkspaceID,
				Cwd:            p.Cwd,
			})
		}
	}
	return &Snapshot{Panes: panes, Agents: agents, Workspaces: ws, Version: "fake", Protocol: MinProtocol}, nil
}

// ServerStatus reports a running 0.9-compatible fake server.
func (f *FakeClient) ServerStatus(ctx context.Context, host string) (StatusServerResult, error) {
	ok := true
	return StatusServerResult{Running: true, Version: "fake", Protocol: MinProtocol, Compatible: &ok}, nil
}

func (f *FakeClient) ReadPane(ctx context.Context, host, paneID string, lines int) (string, error) {
	if f.ReadContent != "" {
		return f.ReadContent, nil
	}
	return "$ echo hello\nhello\n$ ", nil
}

func (f *FakeClient) SendText(ctx context.Context, host, paneID, text string) error {
	f.lastSentTextMu.Lock()
	f.sendTextCalls++
	n := f.sendTextCalls
	f.lastSentText = text
	err := f.SendTextErr
	block := f.SendBlock
	f.lastSentTextMu.Unlock()
	if block != nil && n == 1 {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

// SendCount returns how many times SendText has been called.
func (f *FakeClient) SendCount() int {
	f.lastSentTextMu.RLock()
	defer f.lastSentTextMu.RUnlock()
	return f.sendTextCalls
}

// SentText returns the last text passed to SendText.
func (f *FakeClient) SentText() string {
	f.lastSentTextMu.RLock()
	defer f.lastSentTextMu.RUnlock()
	return f.lastSentText
}

func (f *FakeClient) SendKeys(ctx context.Context, host, paneID, keys string) error {
	f.lastSentTextMu.Lock()
	f.sentKeys = append(f.sentKeys, keys)
	err := f.SendKeysErr
	f.lastSentTextMu.Unlock()
	return err
}

// SentKeys returns the keys passed to SendKeys, in order.
func (f *FakeClient) SentKeys() []string {
	f.lastSentTextMu.RLock()
	defer f.lastSentTextMu.RUnlock()
	out := make([]string, len(f.sentKeys))
	copy(out, f.sentKeys)
	return out
}

func (f *FakeClient) StatusServer(ctx context.Context, host string) (bool, error) {
	return true, nil
}

func (f *FakeClient) PaneSplit(ctx context.Context, host, fromPaneID, direction, cwd string) (string, error) {
	f.paneSeq++
	return fmt.Sprintf("fake:%d.%d", f.paneSeq, f.paneSeq), nil
}

func (f *FakeClient) WorkspaceCreate(ctx context.Context, host, label, cwd string) (string, error) {
	f.paneSeq++
	return fmt.Sprintf("fake:ws%d.%d", f.paneSeq, f.paneSeq), nil
}

func (f *FakeClient) TabCreate(ctx context.Context, host, workspaceID, cwd string) (string, error) {
	f.paneSeq++
	return fmt.Sprintf("fake:tab%d.%d", f.paneSeq, f.paneSeq), nil
}

func (f *FakeClient) RenameWorkspace(ctx context.Context, host, id, label string) error { return nil }
func (f *FakeClient) RenameTab(ctx context.Context, host, id, label string) error       { return nil }
func (f *FakeClient) RenamePane(ctx context.Context, host, id, label string, clear bool) error {
	return nil
}

func (f *FakeClient) CloseWorkspace(ctx context.Context, host, id string, group bool) error {
	f.lastSentTextMu.Lock()
	f.closeCalls++
	f.lastCloseGroup = group
	err := f.CloseErr
	f.lastSentTextMu.Unlock()
	return err
}

func (f *FakeClient) CloseTab(ctx context.Context, host, id string) error {
	f.lastSentTextMu.Lock()
	f.closeCalls++
	err := f.CloseErr
	f.lastSentTextMu.Unlock()
	return err
}

func (f *FakeClient) ClosePane(ctx context.Context, host, id string) error {
	f.lastSentTextMu.Lock()
	f.closeCalls++
	err := f.CloseErr
	f.lastSentTextMu.Unlock()
	return err
}

// CloseCount is how many close calls have run.
func (f *FakeClient) CloseCount() int {
	f.lastSentTextMu.RLock()
	defer f.lastSentTextMu.RUnlock()
	return f.closeCalls
}

func (f *FakeClient) MoveWorkspace(ctx context.Context, host, id string, insertIndex int) error {
	f.lastSentTextMu.Lock()
	f.moveCalls++
	f.lastMoveIndex = insertIndex
	f.lastSentTextMu.Unlock()
	return nil
}

// MoveCount is how many MoveWorkspace calls have run.
func (f *FakeClient) MoveCount() int {
	f.lastSentTextMu.RLock()
	defer f.lastSentTextMu.RUnlock()
	return f.moveCalls
}

func (f *FakeClient) WorktreeList(ctx context.Context, host, workspaceID string) ([]WorktreeEntry, error) {
	return []WorktreeEntry{{
		Path: "/tmp/example", Branch: "worktree/calm-river-00ab", Label: "calm-river-00ab", Linked: true,
	}}, nil
}

// WorktreeCreateCount is how many WorktreeCreate calls have run.
func (f *FakeClient) WorktreeCreateCount() int {
	f.lastSentTextMu.RLock()
	defer f.lastSentTextMu.RUnlock()
	return f.worktreeCreates
}

func (f *FakeClient) WorktreeCreate(ctx context.Context, host string, opts WorktreeCreateOpts) (WorktreeResult, error) {
	f.lastSentTextMu.Lock()
	f.worktreeCreates++
	err := f.WorktreeErr
	f.lastSentTextMu.Unlock()
	if err != nil {
		return WorktreeResult{}, err
	}
	return WorktreeResult{WorkspaceID: "ws-new", PaneID: "pane-new", Branch: opts.Branch, Path: "/tmp/example"}, nil
}

func (f *FakeClient) WorktreeOpen(ctx context.Context, host string, opts WorktreeOpenOpts) (WorktreeResult, error) {
	if f.WorktreeErr != nil {
		return WorktreeResult{}, f.WorktreeErr
	}
	return WorktreeResult{WorkspaceID: "ws-open", PaneID: "pane-open", Path: opts.Path, AlreadyOpen: true}, nil
}

func (f *FakeClient) WorktreeRemove(ctx context.Context, host, workspaceID string, force bool) error {
	f.lastSentTextMu.Lock()
	f.removeCalls++
	err := f.RemoveErr
	f.lastSentTextMu.Unlock()
	return err
}

// RemoveCount is how many WorktreeRemove calls have run.
func (f *FakeClient) RemoveCount() int {
	f.lastSentTextMu.RLock()
	defer f.lastSentTextMu.RUnlock()
	return f.removeCalls
}

func (f *FakeClient) AgentStart(ctx context.Context, host, paneID, name, kind string) error {
	f.lastSentTextMu.Lock()
	f.agentStarts++
	err := f.AgentStartErr
	f.lastSentTextMu.Unlock()
	return err
}
