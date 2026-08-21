package herdr

import "context"

// Pane represents a single tmux pane / agent session.
type Pane struct {
	PaneID                string      `json:"pane_id"`
	Agent                 string      `json:"agent,omitempty"`
	DisplayAgent          string      `json:"display_agent,omitempty"` // after kind_override
	Project               string      `json:"project,omitempty"`
	Cwd                   string      `json:"cwd,omitempty"`
	Host                  string      `json:"host,omitempty"`
	Status                string      `json:"status,omitempty"`
	AgentStatus           string      `json:"agent_status,omitempty"` // herdr 0.7+ field; mapped onto Status
	WorkspaceID           string      `json:"workspace_id,omitempty"`
	WorkspaceLabel        string      `json:"workspace_label,omitempty"`
	Kind                  string      `json:"kind,omitempty"` // "agent" | "shell"
	TerminalTitle         string      `json:"terminal_title,omitempty"`
	TerminalTitleStripped string      `json:"terminal_title_stripped,omitempty"`
	Branch                string      `json:"branch,omitempty"`
	Prompt                string      `json:"prompt,omitempty"`
	Options               []string    `json:"options,omitempty"`          // approval payloads
	OptionLabels          []string    `json:"option_labels,omitempty"`    // approval display labels
	LastActive            int64       `json:"last_active,omitempty"`      // epoch seconds of last content change
	Revision              int         `json:"revision,omitempty"`         // herdr pane revision (content version)
	StateChangeSeq        int         `json:"state_change_seq,omitempty"` // herdr state change sequence (more granular than revision)
	ValidPaths            []string    `json:"valid_paths"`                // file paths in last content that exist on disk
	Scroll                *ScrollInfo `json:"scroll,omitempty"`           // terminal scrollback/offset info
}

// ScrollInfo carries herdr's terminal scroll state. max_offset_from_bottom
// is 0 for full-screen TUI panes (no scrollback), which tells the relay that
// the pane redraws in place and incremental append is not meaningful.
type ScrollInfo struct {
	MaxOffsetFromBottom int `json:"max_offset_from_bottom,omitempty"`
	OffsetFromBottom    int `json:"offset_from_bottom,omitempty"`
	ViewportRows        int `json:"viewport_rows,omitempty"`
}

// Workspace represents a herdr workspace (from `herdr workspace list`).
type Workspace struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
	PaneCount   int    `json:"pane_count"`
	TabCount    int    `json:"tab_count"`
	AgentStatus string `json:"agent_status"`
	Focused     bool   `json:"focused"`
	ActiveTabID string `json:"active_tab_id"`
}

// StatusServerResult is the parsed output of `herdr status server --json`.
type StatusServerResult struct {
	Running bool `json:"running"`
}

// Client is the interface for all herdr CLI interactions.
// host is "" or "local" for the local machine, or an SSH target for remotes.
type Client interface {
	// Alive checks if herdr server is running on the given host.
	Alive(ctx context.Context, host string) (bool, error)
	// ListPanes returns all panes for a host.
	ListPanes(ctx context.Context, host string) ([]Pane, error)
	// ListWorkspaces returns all workspaces for a host.
	ListWorkspaces(ctx context.Context, host string) ([]Workspace, error)
	// ReadPane returns the current content of a pane (last `lines` lines).
	ReadPane(ctx context.Context, host, paneID string, lines int) (string, error)
	// SendText sends raw text to a pane.
	SendText(ctx context.Context, host, paneID, text string) error
	// SendKeys sends key sequences to a pane.
	SendKeys(ctx context.Context, host, paneID, keys string) error
	// StatusServer checks if herdr server is running.
	StatusServer(ctx context.Context, host string) (bool, error)
	// PaneSplit splits an existing pane and returns the new pane's ID.
	// If fromPaneID is empty, the current pane is split.
	// direction is "right" or "down". cwd overrides the new pane's working directory.
	PaneSplit(ctx context.Context, host, fromPaneID, direction, cwd string) (string, error)
	// WorkspaceCreate creates a new workspace with a root shell pane and returns the new pane's ID.
	// label and cwd are optional.
	WorkspaceCreate(ctx context.Context, host, label, cwd string) (string, error)
	// TabCreate creates a new tab (with a root shell pane) in the given workspace
	// and returns the new pane's ID. workspaceID is required.
	TabCreate(ctx context.Context, host, workspaceID, cwd string) (string, error)
}

// PaneRouter resolves which host a pane lives on, for command routing.
type PaneRouter interface {
	RemoteForPane(paneID string) string
}
