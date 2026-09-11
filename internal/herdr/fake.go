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
	// paneSeq is a per-instance counter for generated pane IDs, so separate
	// FakeClient instances do not share/race on one package-level counter.
	paneSeq int
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
	f.lastSentText = text
	f.lastSentTextMu.Unlock()
	return nil
}

// SentText returns the last text passed to SendText.
func (f *FakeClient) SentText() string {
	f.lastSentTextMu.RLock()
	defer f.lastSentTextMu.RUnlock()
	return f.lastSentText
}

func (f *FakeClient) SendKeys(ctx context.Context, host, paneID, keys string) error {
	return nil
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
