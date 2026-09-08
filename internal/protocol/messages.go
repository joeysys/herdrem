package protocol

import (
	"encoding/json"

	"herdrem/internal/store"
)

// Version is the protocol version.
const Version = 1

// --- S→C (server-to-client) ---

// Hello is sent on connection success.
type Hello struct {
	Type          string   `json:"type"`
	V             int      `json:"v"`
	ServerVersion string   `json:"server_version"`
	Features      []string `json:"features"`
}

// AgentsSnapshot is the full pane/agent list broadcast.
type AgentsSnapshot struct {
	Type   string        `json:"type"`
	Agents []interface{} `json:"agents"`
}

// BlockedNotify announces a new blocked pane with options.
type BlockedNotify struct {
	Type    string   `json:"type"`
	Pane    string   `json:"pane_id"`
	Agent   string   `json:"agent,omitempty"`
	Project string   `json:"project,omitempty"`
	Options []string `json:"options"`
}

// PaneContent carries terminal output for a pane.
// When Append is true, Content holds only newly-appended bytes since the last
// full snapshot (BaseRev is the revision that snapshot was taken at) and the
// client should append it to its current content. When Append is false,
// Content is the full pane content (initial load, reconnect, or after the
// server lost its diff baseline) and Rev is its revision.
type PaneContent struct {
	Type    string `json:"type"`
	PaneID  string `json:"pane_id"`
	Content string `json:"content"`
	Lines   int    `json:"lines,omitempty"`    // echoed from request
	Purpose string `json:"purpose,omitempty"`  // "term" | "preview"
	Append  bool   `json:"append,omitempty"`   // true = content is incremental delta
	Rev     *int   `json:"rev,omitempty"`      // revision this content represents (nil = unknown; 0 is a real revision)
	BaseRev *int   `json:"base_rev,omitempty"` // for append: revision of the snapshot the delta extends
	Poll    bool   `json:"poll,omitempty"`     // echoed: response is a subscription refresh, not a content replacement
}

// TodoUpdated signals a todo item change.
type TodoUpdated struct {
	Type    string     `json:"type"`
	Todo    store.Todo `json:"todo"`
	Deleted bool       `json:"deleted,omitempty"`
}

// TodosSnapshot carries all todo items.
type TodosSnapshot struct {
	Type  string       `json:"type"`
	Todos []store.Todo `json:"todos"`
}

// PrefsSnapshot carries all prefs (section model).
type PrefsSnapshot struct {
	Type  string      `json:"type"` // "prefs" | "prefs_updated"
	Prefs interface{} `json:"prefs"`
}

// FileContent carries file preview content.
type FileContent struct {
	Type      string `json:"type"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Encoding  string `json:"encoding,omitempty"` // "utf-8" | "base64"
	MimeType  string `json:"mime_type,omitempty"`
	IsText    bool   `json:"is_text,omitempty"`
	IsImage   bool   `json:"is_image,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Size      int    `json:"size,omitempty"`
	ReqID     string `json:"req_id,omitempty"` // echoed from the matching FileRead
}

// FileListing carries directory listing.
type FileListing struct {
	Type    string      `json:"type"`
	Path    string      `json:"path"`
	Entries interface{} `json:"entries"`
	ReqID   string      `json:"req_id,omitempty"` // echoed from the matching FileList
}

// GitDiffContent carries a git diff/show result.
type GitDiffContent struct {
	Type    string `json:"type"` // "git_diff_content"
	Cwd     string `json:"cwd"`
	Mode    string `json:"mode"`             // "unstaged" | "cached" | "commit"
	Commit  string `json:"commit,omitempty"` // echoed when mode == "commit"
	Content string `json:"content"`          // diff text (may be empty)
	ReqID   string `json:"req_id,omitempty"` // echoed from the matching GitDiff
}

// GitLogResult carries a list of recent commits for the selector.
type GitLogResult struct {
	Type    string      `json:"type"` // "git_log_result"
	Cwd     string      `json:"cwd"`
	Commits interface{} `json:"commits"`
	ReqID   string      `json:"req_id,omitempty"` // echoed from the matching GitLog
}

// ErrorMsg is a generic error response.
type ErrorMsg struct {
	Type    string `json:"type"`
	Op      string `json:"op"`
	Message string `json:"message"`
	Key     string `json:"key,omitempty"`
	ReqID   string `json:"req_id,omitempty"` // echoed when the failing request carried one
}

// --- C→S (client-to-server) ---

// RawMessage is a partially-decoded incoming message.
type RawMessage struct {
	Type string `json:"type"`
}

// DecodeType extracts the "type" field from a JSON message.
func DecodeType(data []byte) string {
	var rm RawMessage
	_ = json.Unmarshal(data, &rm)
	return rm.Type
}

// Respond is a client approval/option command.
type Respond struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	PaneID   string `json:"pane_id"`
	OptIndex int    `json:"opt_index,omitempty"`
	Submit   bool   `json:"submit,omitempty"` // append \n if missing
}

// SendText sends raw text to a pane.
type SendText struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	PaneID string `json:"pane_id"`
	Submit bool   `json:"submit,omitempty"` // append \n if missing
}

// SendKeys sends key sequences to a pane.
type SendKeys struct {
	Type   string `json:"type"`
	Keys   string `json:"keys"`
	PaneID string `json:"pane_id"`
}

// ReadPane requests pane content.
type ReadPane struct {
	Type        string `json:"type"`
	PaneID      string `json:"pane_id"`
	Lines       int    `json:"lines,omitempty"`       // default 30
	Purpose     string `json:"purpose,omitempty"`     // "term" | "preview", echoed back
	Refresh     bool   `json:"refresh,omitempty"`     // force a direct herdr read, bypassing the poller cache
	Incremental bool   `json:"incremental,omitempty"` // client declares it understands append deltas
	Poll        bool   `json:"poll,omitempty"`        // subscription-baseline refresh; response is not used to replace content
}

// TodoGet requests all todo items.
type TodoGet struct {
	Type string `json:"type"`
}

// TodoSet creates or updates a todo. An empty body deletes an existing item.
type TodoSet struct {
	Type    string             `json:"type"`
	ID      string             `json:"id,omitempty"`
	Key     string             `json:"key,omitempty"` // legacy pane-scoped clients; rejected by the server
	Body    string             `json:"body"`
	Binding *store.TodoBinding `json:"binding,omitempty"`
}

// PrefsGet requests all prefs.
type PrefsGet struct {
	Type string `json:"type"`
}

// PrefsSet replaces a whole prefs section.
type PrefsSet struct {
	Type    string          `json:"type"`
	Section string          `json:"section"` // "skills" | "shell_cmds" | "kind_overrides"
	Data    json.RawMessage `json:"data"`
}

// KindOverrideSet marks a pane as agent with a display name.
type KindOverrideSet struct {
	Type         string `json:"type"`
	PaneID       string `json:"pane_id"`
	Host         string `json:"host,omitempty"`
	DisplayAgent string `json:"display_agent,omitempty"`
}

// KindOverrideClear removes a pane's override.
type KindOverrideClear struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id"`
	Host   string `json:"host,omitempty"`
}

// MarkPaneRead marks an agent pane as read/idle.
type MarkPaneRead struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id"`
}

// FileRead requests file content.
type FileRead struct {
	Type  string `json:"type"`
	Path  string `json:"path"`
	Host  string `json:"host,omitempty"`
	ReqID string `json:"req_id,omitempty"` // client-generated; echoed in the response
}

// FileList requests directory listing.
type FileList struct {
	Type  string `json:"type"`
	Path  string `json:"path"`
	Host  string `json:"host,omitempty"`
	ReqID string `json:"req_id,omitempty"` // client-generated; echoed in the response
}

// GitDiff requests a git diff for a working directory.
type GitDiff struct {
	Type   string `json:"type"` // "git_diff"
	Cwd    string `json:"cwd"`  // repo path (must be inside sandbox roots)
	Host   string `json:"host,omitempty"`
	Mode   string `json:"mode"`             // "unstaged" | "cached" | "commit"
	Commit string `json:"commit,omitempty"` // required when mode == "commit"
	ReqID  string `json:"req_id,omitempty"` // client-generated; echoed in the response
}

// GitLog requests the recent commit list for a working directory.
type GitLog struct {
	Type  string `json:"type"` // "git_log"
	Cwd   string `json:"cwd"`
	Host  string `json:"host,omitempty"`
	ReqID string `json:"req_id,omitempty"`
}

// AgentEvent is an optional push event.
type AgentEvent struct {
	Type   string `json:"type"`
	Event  string `json:"event"`
	PaneID string `json:"pane_id,omitempty"`
}

// TunnelInfo represents one forwarded port tunnel state.
type TunnelInfo struct {
	ID         string `json:"id"`
	Port       int    `json:"port"`
	TargetHost string `json:"target_host,omitempty"`
	PublicURL  string `json:"public_url"`
	Status     string `json:"status"`    // "starting" | "active" | "error" | "closed"
	AuthMode   string `json:"auth_mode"` // "token_gate" | "basic_auth" | "direct"
	Token      string `json:"token,omitempty"`
	Error      string `json:"error,omitempty"`
	CreatedAt  int64  `json:"created_at"` // unix timestamp (seconds)
}

// TunnelCreate requests creating a new port tunnel.
type TunnelCreate struct {
	Type     string `json:"type"`
	Port     int    `json:"port"`
	AuthMode string `json:"auth_mode,omitempty"` // "token_gate" | "basic_auth" | "direct"
	Pin      string `json:"pin,omitempty"`
}

// TunnelClose requests terminating a port tunnel.
type TunnelClose struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// TunnelsList requests listing all active port tunnels.
type TunnelsList struct {
	Type string `json:"type"`
}

// PaneCreate requests creating a new shell or agent pane.
// Mode is "shell" (split an existing pane), "workspace" (create a new workspace),
// or "tab" (create a new tab in an existing workspace).
// FromPaneID is the pane to split (required for "shell" mode).
// WorkspaceID is the workspace to create a tab in (required for "tab" mode).
// Direction is "right" or "down" (shell mode only, defaults to "right").
// Cwd and Label are optional overrides.
type PaneCreate struct {
	Type        string `json:"type"`
	Mode        string `json:"mode"`                   // "shell" | "workspace" | "tab"
	FromPaneID  string `json:"pane_id,omitempty"`      // pane to split (shell mode)
	WorkspaceID string `json:"workspace_id,omitempty"` // workspace for tab mode
	Host        string `json:"host,omitempty"`         // target host
	Direction   string `json:"direction,omitempty"`    // "right" | "down" (shell mode)
	Cwd         string `json:"cwd,omitempty"`          // working directory override
	Label       string `json:"label,omitempty"`        // workspace label (workspace mode)
}

// PaneCreated confirms a new pane was created, carrying the new pane's ID.
type PaneCreated struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id"`
	Mode   string `json:"mode"` // "shell" | "workspace" | "tab" (normalized; empty → "shell")
	Host   string `json:"host,omitempty"`
}

// TunnelsSnapshot carries all port tunnels.
type TunnelsSnapshot struct {
	Type    string       `json:"type"`
	Tunnels []TunnelInfo `json:"tunnels"`
}

// TunnelUpdated signals a single tunnel state change.
type TunnelUpdated struct {
	Type   string     `json:"type"`
	Tunnel TunnelInfo `json:"tunnel"`
}

// IntPtr returns a pointer to v, for *int fields so that revision 0 survives
// JSON serialization (herdr revisions start at zero).
func IntPtr(v int) *int {
	return &v
}
