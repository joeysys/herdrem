package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

const sessionExecTimeout = 60 * time.Second

// pythonSocketRPC speaks one NDJSON socket RPC and prints the response line.
// It uses only the python3 standard library. The socket path and the JSON
// payload are argv, not interpolated into the script.
const pythonSocketRPC = `import socket,sys
p,payload=sys.argv[1],sys.argv[2]
s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
s.settimeout(8)
s.connect(p)
data=payload.encode()
if not data.endswith(b"\n"):
    data+=b"\n"
s.sendall(data)
buf=b""
while b"\n" not in buf:
    chunk=s.recv(4096)
    if not chunk:
        break
    buf+=chunk
sys.stdout.write(buf.split(b"\n",1)[0].decode())
`

func (c *CLIClient) RenameWorkspace(ctx context.Context, host, id, label string) error {
	_, err := c.exec(ctx, host, "workspace", "rename", id, label)
	return err
}

func (c *CLIClient) RenameTab(ctx context.Context, host, id, label string) error {
	_, err := c.exec(ctx, host, "tab", "rename", id, label)
	return err
}

func (c *CLIClient) RenamePane(ctx context.Context, host, id, label string, clear bool) error {
	args := []string{"pane", "rename", id}
	if clear {
		args = append(args, "--clear")
	} else {
		args = append(args, label)
	}
	_, err := c.exec(ctx, host, args...)
	return err
}

func (c *CLIClient) CloseWorkspace(ctx context.Context, host, id string, group bool) error {
	args := []string{"workspace", "close", id}
	if group {
		args = append(args, "--group")
	}
	_, err := c.exec(ctx, host, args...)
	return err
}

func (c *CLIClient) CloseTab(ctx context.Context, host, id string) error {
	_, err := c.exec(ctx, host, "tab", "close", id)
	return err
}

func (c *CLIClient) ClosePane(ctx context.Context, host, id string) error {
	_, err := c.exec(ctx, host, "pane", "close", id)
	return err
}

func (c *CLIClient) WorktreeList(ctx context.Context, host, workspaceID string) ([]WorktreeEntry, error) {
	out, err := c.execTimeout(ctx, sessionExecTimeout, host, "worktree", "list", "--workspace", workspaceID)
	if err != nil {
		return nil, err
	}
	return parseWorktreeList(out)
}

func (c *CLIClient) WorktreeCreate(ctx context.Context, host string, opts WorktreeCreateOpts) (WorktreeResult, error) {
	args := []string{"worktree", "create", "--workspace", opts.WorkspaceID, "--no-focus"}
	if opts.Branch != "" {
		args = append(args, "--branch", opts.Branch)
	}
	if opts.Base != "" {
		args = append(args, "--base", opts.Base)
	}
	if opts.Label != "" {
		args = append(args, "--label", opts.Label)
	}
	out, err := c.execTimeout(ctx, sessionExecTimeout, host, args...)
	if err != nil {
		return WorktreeResult{}, err
	}
	return parseWorktreeResult(out)
}

func (c *CLIClient) WorktreeOpen(ctx context.Context, host string, opts WorktreeOpenOpts) (WorktreeResult, error) {
	args := []string{"worktree", "open", "--no-focus"}
	if opts.WorkspaceID != "" {
		args = append(args, "--workspace", opts.WorkspaceID)
	}
	if opts.Path != "" {
		args = append(args, "--path", opts.Path)
	}
	if opts.Branch != "" {
		args = append(args, "--branch", opts.Branch)
	}
	if opts.Label != "" {
		args = append(args, "--label", opts.Label)
	}
	out, err := c.execTimeout(ctx, sessionExecTimeout, host, args...)
	if err != nil {
		return WorktreeResult{}, err
	}
	return parseWorktreeResult(out)
}

func (c *CLIClient) WorktreeRemove(ctx context.Context, host, workspaceID string, force bool) error {
	args := []string{"worktree", "remove", "--workspace", workspaceID}
	if force {
		args = append(args, "--force")
	}
	_, err := c.execTimeout(ctx, sessionExecTimeout, host, args...)
	return err
}

func (c *CLIClient) AgentStart(ctx context.Context, host, paneID, name, kind string) error {
	_, err := c.execTimeout(ctx, sessionExecTimeout, host,
		"agent", "start", name, "--kind", kind, "--pane", paneID, "--timeout", "45000")
	return err
}

// MoveWorkspace calls workspace.move on the herdr socket. There is no CLI
// for it. Local uses a unix dial. A remote host runs python3 against the
// socket path from `status server` on that host. send-text does not use this.
func (c *CLIClient) MoveWorkspace(ctx context.Context, host, id string, insertIndex int) error {
	if strings.HasPrefix(host, "-") {
		return fmt.Errorf("invalid host")
	}
	st, err := c.ServerStatus(ctx, host)
	if err != nil {
		return err
	}
	if st.Socket == "" || strings.ContainsAny(st.Socket, "\r\n") {
		return fmt.Errorf("herdr socket path is empty")
	}
	body, err := json.Marshal(map[string]any{
		"id":     "workspace-move",
		"method": "workspace.move",
		"params": map[string]any{
			"workspace_id": id,
			"insert_index": insertIndex,
		},
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var line []byte
	if isLocal(host) {
		line, err = rpcUnix(ctx, st.Socket, body)
	} else {
		line, err = c.rpcSSH(ctx, host, st.Socket, body)
	}
	if err != nil {
		return err
	}
	return parseRPCLine(line)
}

func rpcUnix(ctx context.Context, socketPath string, payload []byte) ([]byte, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("workspace.move: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		payload = append(payload, '\n')
	}
	if _, err := conn.Write(payload); err != nil {
		return nil, fmt.Errorf("workspace.move: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("workspace.move: %w", err)
	}
	return line, nil
}

func (c *CLIClient) rpcSSH(ctx context.Context, host, socketPath string, payload []byte) ([]byte, error) {
	remote := remoteSocketCommand(socketPath, string(payload))
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", host, remote)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("%s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("workspace.move: %w", err)
	}
	return out, nil
}

// remoteSocketCommand is the remote shell command for one socket RPC.
// It is not prefixed with herdr. python3 is required on the remote; if it
// is missing, reorder fails and rename or worktree still use the herdr CLI.
func remoteSocketCommand(socketPath, payload string) string {
	return "python3 -c " + shellQuote(pythonSocketRPC) + " " + shellQuote(socketPath) + " " + shellQuote(payload)
}

func parseWorktreeList(out []byte) ([]WorktreeEntry, error) {
	if msg := rpcErrorMessage(out); msg != "" {
		return nil, fmt.Errorf("%s", msg)
	}
	var envelope struct {
		Result struct {
			Worktrees []WorktreeEntry `json:"worktrees"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return nil, fmt.Errorf("parse worktree list: %w", err)
	}
	if envelope.Result.Worktrees == nil {
		return []WorktreeEntry{}, nil
	}
	return envelope.Result.Worktrees, nil
}

func parseWorktreeResult(out []byte) (WorktreeResult, error) {
	if msg := rpcErrorMessage(out); msg != "" {
		return WorktreeResult{}, fmt.Errorf("%s", msg)
	}
	var envelope struct {
		Result struct {
			Workspace struct {
				WorkspaceID string `json:"workspace_id"`
			} `json:"workspace"`
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
			Worktree struct {
				Path   string `json:"path"`
				Branch string `json:"branch"`
			} `json:"worktree"`
			AlreadyOpen bool `json:"already_open"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return WorktreeResult{}, fmt.Errorf("parse worktree: %w", err)
	}
	res := WorktreeResult{
		WorkspaceID: envelope.Result.Workspace.WorkspaceID,
		PaneID:      envelope.Result.RootPane.PaneID,
		Branch:      envelope.Result.Worktree.Branch,
		Path:        envelope.Result.Worktree.Path,
		AlreadyOpen: envelope.Result.AlreadyOpen,
	}
	if res.WorkspaceID == "" {
		return WorktreeResult{}, fmt.Errorf("worktree: empty workspace_id")
	}
	return res, nil
}

func rpcErrorMessage(out []byte) string {
	out = bytesTrim(out)
	if len(out) == 0 || out[0] != '{' {
		return ""
	}
	var env struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(out, &env) != nil || env.Error == nil {
		return ""
	}
	if env.Error.Message != "" {
		return env.Error.Message
	}
	return env.Error.Code
}

func parseRPCLine(line []byte) error {
	if msg := rpcErrorMessage(line); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		return fmt.Errorf("parse workspace.move: %w", err)
	}
	if len(env.Result) == 0 {
		return fmt.Errorf("workspace.move: empty result")
	}
	return nil
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
