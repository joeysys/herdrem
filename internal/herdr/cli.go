package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"path"
	"strings"
	"time"
)

// CLIClient implements Client by exec'ing the real herdr binary, locally or
// over SSH for remote hosts.
type CLIClient struct {
	bin    string
	logger *slog.Logger
}

// NewCLIClient creates a CLI-based herdr client.
// If bin is empty, "herdr" is resolved from PATH. The binary is validated
// to exist and be executable, so a bad HERDR_BIN fails fast.
func NewCLIClient(bin string, logger *slog.Logger) (*CLIClient, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if bin == "" {
		bin = "herdr"
	}
	resolved, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("herdr binary not usable (%s): %w", bin, err)
	}
	return &CLIClient{bin: resolved, logger: logger}, nil
}

// isLocal reports whether the host refers to the local machine.
func isLocal(host string) bool {
	return host == "" || host == "local"
}

// buildCmd constructs the exec.Cmd for a herdr invocation on the given host.
// Local: `herdr <args...>`. Remote: `ssh -o BatchMode=yes -o ConnectTimeout=5
// <host> <bin> <args...>`.
func (c *CLIClient) buildCmd(ctx context.Context, host string, args ...string) *exec.Cmd {
	if isLocal(host) {
		return exec.CommandContext(ctx, c.bin, args...)
	}
	// Defense in depth: reject anything ssh would parse as an option. The
	// service layer already validates hosts against the allowlist, but a
	// non-empty host that starts with '-' must never reach ssh's argv.
	if strings.HasPrefix(host, "-") {
		return exec.CommandContext(ctx, "/bin/false")
	}
	// OpenSSH concatenates all args after the host into a single shell
	// command on the remote. Quote each argument so values containing
	// spaces or shell metacharacters (e.g. send-text payloads, pane ids,
	// cwd paths) cannot inject shell syntax or be word-split.
	var remoteCmd strings.Builder
	remoteCmd.WriteString(shellQuote(c.bin))
	for _, a := range args {
		remoteCmd.WriteString(" ")
		remoteCmd.WriteString(shellQuote(a))
	}
	sshArgs := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		host,
		remoteCmd.String(),
	}
	return exec.CommandContext(ctx, "ssh", sshArgs...)
}

// shellQuote wraps s in single quotes so it survives one remote shell parse
// as a single literal argument.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (c *CLIClient) exec(ctx context.Context, host string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := c.buildCmd(ctx, host, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("herdr %s (host=%s): %w (stderr: %s)",
			strings.Join(args, " "), hostLabel(host), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func hostLabel(host string) string {
	if isLocal(host) {
		return "local"
	}
	return host
}

// Alive checks if herdr server is running on the host.
func (c *CLIClient) Alive(ctx context.Context, host string) (bool, error) {
	st, err := c.ServerStatus(ctx, host)
	if err != nil {
		return false, err
	}
	return st.Running, nil
}

// ServerStatus returns the full `status server --json` payload: running plus
// the 0.9+ version/protocol/compatible/socket fields used by the version gate.
func (c *CLIClient) ServerStatus(ctx context.Context, host string) (StatusServerResult, error) {
	out, err := c.exec(ctx, host, "status", "server", "--json")
	if err != nil {
		return StatusServerResult{}, err
	}
	var result StatusServerResult
	if err := json.Unmarshal(out, &result); err != nil {
		return StatusServerResult{}, fmt.Errorf("parse status: %w", err)
	}
	return result, nil
}

// normalizePanes applies the relay-side field fixups shared by ListPanes and
// Snapshot: agent_status→status mapping, host tagging, kind inference, and
// project derivation from cwd.
func normalizePanes(panes []Pane, host string) {
	for i := range panes {
		if panes[i].Status == "" {
			panes[i].Status = panes[i].AgentStatus
		}
		if panes[i].Host == "" {
			panes[i].Host = hostLabel(host)
		}
		// Infer kind: no agent → shell
		if panes[i].Kind == "" {
			if panes[i].Agent == "" {
				panes[i].Kind = "shell"
			} else {
				panes[i].Kind = "agent"
			}
		}
		// Derive project from cwd basename if not set
		if panes[i].Project == "" && panes[i].Cwd != "" {
			panes[i].Project = baseName(panes[i].Cwd)
		}
	}
}

// mergeSnapshotAgents folds the agents[] array of a snapshot back onto its
// panes[] by pane_id: state_change_seq (the whole point of the snapshot call —
// PaneInfo no longer carries it in 0.9) plus the agent name/status as a
// fallback for panes that omit them.
func mergeSnapshotAgents(snap *Snapshot) {
	if len(snap.Agents) == 0 {
		return
	}
	byPane := make(map[string]*Agent, len(snap.Agents))
	for i := range snap.Agents {
		byPane[snap.Agents[i].PaneID] = &snap.Agents[i]
	}
	for i := range snap.Panes {
		a, ok := byPane[snap.Panes[i].PaneID]
		if !ok {
			continue
		}
		snap.Panes[i].StateChangeSeq = a.StateChangeSeq
		if snap.Panes[i].Agent == "" {
			snap.Panes[i].Agent = a.Agent
		}
		if snap.Panes[i].AgentStatus == "" {
			snap.Panes[i].AgentStatus = a.AgentStatus
		}
	}
}

// parseSnapshot decodes the `herdr api snapshot` envelope and enforces the
// protocol gate: a response without protocol >= MinProtocol is not a usable
// 0.9 snapshot (old herdr, command missing, or unexpected shape).
func parseSnapshot(out []byte) (*Snapshot, error) {
	var envelope struct {
		Result struct {
			Snapshot *Snapshot `json:"snapshot"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return nil, fmt.Errorf("parse api snapshot: %w", err)
	}
	snap := envelope.Result.Snapshot
	if snap == nil || snap.Protocol < MinProtocol {
		return nil, fmt.Errorf("api snapshot: herdr >= 0.9 (protocol >= %d) required, got protocol %d", MinProtocol, snapProtocol(snap))
	}
	return snap, nil
}

func snapProtocol(snap *Snapshot) int {
	if snap == nil {
		return 0
	}
	return snap.Protocol
}

// Snapshot fetches the single-call full snapshot (`herdr api snapshot`) for a
// host and merges agents[].state_change_seq back onto the panes.
func (c *CLIClient) Snapshot(ctx context.Context, host string) (*Snapshot, error) {
	out, err := c.exec(ctx, host, "api", "snapshot")
	if err != nil {
		return nil, err
	}
	snap, err := parseSnapshot(out)
	if err != nil {
		return nil, err
	}
	mergeSnapshotAgents(snap)
	normalizePanes(snap.Panes, host)
	return snap, nil
}

// ListPanes returns all panes for a host, tagging each with its host.
// herdr 0.9 `pane list` emits a JSON envelope shaped like
// {"id":"cli:pane:list","result":{"panes":[...],"type":"pane_list"}}.
// Each pane uses `agent_status` (not `status`); we map it onto Pane.Status.
// Panes without an agent are classified as Kind="shell".
// Note: pane list no longer carries state_change_seq (moved to AgentInfo in
// 0.9) — callers that need it must use Snapshot instead.
func (c *CLIClient) ListPanes(ctx context.Context, host string) ([]Pane, error) {
	out, err := c.exec(ctx, host, "pane", "list")
	if err != nil {
		return nil, err
	}
	panes, err := parsePaneList(out)
	if err != nil {
		return nil, err
	}
	normalizePanes(panes, host)
	return panes, nil
}

// parsePaneList decodes the `herdr pane list` envelope.
func parsePaneList(out []byte) ([]Pane, error) {
	var envelope struct {
		Result struct {
			Panes []Pane `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return nil, fmt.Errorf("parse pane list: %w", err)
	}
	return envelope.Result.Panes, nil
}

// ListWorkspaces returns all workspaces for a host.
func (c *CLIClient) ListWorkspaces(ctx context.Context, host string) ([]Workspace, error) {
	out, err := c.exec(ctx, host, "workspace", "list")
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Result struct {
			Workspaces []Workspace `json:"workspaces"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return nil, fmt.Errorf("parse workspace list: %w", err)
	}
	return envelope.Result.Workspaces, nil
}

// ReadPane returns the current content of a pane (raw ANSI preserved).
func (c *CLIClient) ReadPane(ctx context.Context, host, paneID string, lines int) (string, error) {
	if lines <= 0 {
		lines = 30
	}
	out, err := c.exec(ctx, host, "pane", "read", paneID, "--lines", fmt.Sprintf("%d", lines), "--format", "ansi")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SendText sends raw text to a pane.
// herdr's subcommand is `send-text` with positional PANE_ID and TEXT args
// (no `--text` flag, no `send` subcommand).
func (c *CLIClient) SendText(ctx context.Context, host, paneID, text string) error {
	_, err := c.exec(ctx, host, "pane", "send-text", paneID, text)
	return err
}

// SendKeys sends key sequences to a pane.
func (c *CLIClient) SendKeys(ctx context.Context, host, paneID, keys string) error {
	_, err := c.exec(ctx, host, "pane", "send-keys", paneID, keys)
	return err
}

// StatusServer is an alias for Alive (interface compatibility).
func (c *CLIClient) StatusServer(ctx context.Context, host string) (bool, error) {
	return c.Alive(ctx, host)
}



// PaneSplit splits an existing pane and returns the new pane's ID.
// If fromPaneID is empty, the current pane is split.
func (c *CLIClient) PaneSplit(ctx context.Context, host, fromPaneID, direction, cwd string) (string, error) {
	args := []string{"pane", "split"}
	if fromPaneID != "" {
		args = append(args, fromPaneID)
	} else {
		args = append(args, "--current")
	}
	if direction == "" {
		direction = "right"
	}
	args = append(args, "--direction", direction)
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	out, err := c.exec(ctx, host, args...)
	if err != nil {
		return "", err
	}
	var envelope struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return "", fmt.Errorf("parse pane split: %w", err)
	}
	if envelope.Result.Pane.PaneID == "" {
		return "", fmt.Errorf("pane split: empty pane_id in response")
	}
	return envelope.Result.Pane.PaneID, nil
}

// WorkspaceCreate creates a new workspace with a root shell pane and returns the new pane's ID.
func (c *CLIClient) WorkspaceCreate(ctx context.Context, host, label, cwd string) (string, error) {
	args := []string{"workspace", "create"}
	if label != "" {
		args = append(args, "--label", label)
	}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	out, err := c.exec(ctx, host, args...)
	if err != nil {
		return "", err
	}
	var envelope struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return "", fmt.Errorf("parse workspace create: %w", err)
	}
	if envelope.Result.RootPane.PaneID == "" {
		return "", fmt.Errorf("workspace create: empty pane_id in response")
	}
	return envelope.Result.RootPane.PaneID, nil
}

// TabCreate creates a new tab (with a root shell pane) in the given workspace
// and returns the new pane's ID.
func (c *CLIClient) TabCreate(ctx context.Context, host, workspaceID, cwd string) (string, error) {
	args := []string{"tab", "create", "--workspace", workspaceID}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	out, err := c.exec(ctx, host, args...)
	if err != nil {
		return "", err
	}
	var envelope struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return "", fmt.Errorf("parse tab create: %w", err)
	}
	if envelope.Result.RootPane.PaneID == "" {
		return "", fmt.Errorf("tab create: empty pane_id in response")
	}
	return envelope.Result.RootPane.PaneID, nil
}

// baseName returns the last path component, handling trailing slashes.
func baseName(p string) string {
	if p == "" {
		return ""
	}
	b := path.Base(p)
	if b == "/" || b == "." {
		return ""
	}
	return b
}
