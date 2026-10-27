package herdr

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseWorktreeList(t *testing.T) {
	rows, err := parseWorktreeList([]byte(`{"id":"w","result":{"type":"worktree_list","source":{"repo_key":"k","repo_name":"demo","repo_root":"/demo","source_checkout_path":"/demo","source_workspace_id":"ws"},"worktrees":[{"path":"/demo-wt","branch":"worktree/calm-river-00ab","label":"calm-river-00ab","is_linked_worktree":true,"open_workspace_id":"ws-wt","is_bare":false,"is_detached":false,"is_prunable":false}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Path != "/demo-wt" || rows[0].Branch != "worktree/calm-river-00ab" || !rows[0].Linked || rows[0].OpenWorkspaceID != "ws-wt" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestParseWorktreeResult(t *testing.T) {
	res, err := parseWorktreeResult([]byte(`{"id":"w","result":{"type":"worktree_created","workspace":{"workspace_id":"ws-new"},"tab":{"tab_id":"t"},"root_pane":{"pane_id":"p-new"},"worktree":{"path":"/demo-wt","branch":"worktree/calm-river-00ab","label":"calm-river-00ab","is_linked_worktree":true,"is_bare":false,"is_detached":false,"is_prunable":false}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.WorkspaceID != "ws-new" || res.PaneID != "p-new" || res.Branch != "worktree/calm-river-00ab" || res.AlreadyOpen {
		t.Fatalf("result = %+v", res)
	}
}

func TestParseRPCLine(t *testing.T) {
	err := parseRPCLine([]byte(`{"id":"t3","error":{"code":"workspace_not_found","message":"workspace does-not-exist not found"}}`))
	if err == nil || err.Error() != "workspace does-not-exist not found" {
		t.Fatalf("error = %v", err)
	}
	if err := parseRPCLine([]byte(`{"id":"t","result":{"type":"workspace_moved"}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteSocketCommandIsPythonNotHerdr(t *testing.T) {
	cmd := remoteSocketCommand("/tmp/example.sock", `{"method":"workspace.move","params":{"workspace_id":"ws'1"}}`)
	if !strings.Contains(cmd, "python3 -c ") {
		t.Fatalf("command = %s", cmd)
	}
	if strings.Contains(cmd, "herdr") {
		t.Fatalf("socket reorder must not invoke herdr: %s", cmd)
	}
	if !strings.Contains(cmd, "'/tmp/example.sock'") || !strings.Contains(cmd, `ws'\''1`) {
		t.Fatalf("socket path or payload not quoted: %s", cmd)
	}
}

func TestExecSurfacesJSONErrorAndStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake requires POSIX")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	script := `#!/bin/sh
case "$1" in
  json) echo '{"id":"e","error":{"code":"dirty","message":"fatal: cannot remove dirty worktree"}}'; exit 1 ;;
  text) echo 'fatal: worktree contains modified or untracked files' >&2; exit 1 ;;
esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := NewCLIClient(bin, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.exec(context.Background(), "", "json")
	if err == nil || err.Error() != "fatal: cannot remove dirty worktree" {
		t.Fatalf("json error = %v", err)
	}
	_, err = c.exec(context.Background(), "", "text")
	if err == nil || err.Error() != "fatal: worktree contains modified or untracked files" {
		t.Fatalf("stderr = %v", err)
	}
}

func TestMoveWorkspaceDialsLocalSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket")
	}
	dir := t.TempDir()
	sock := filepath.Join(dir, "herdr.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- "accept " + err.Error()
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		n, _ := conn.Read(buf)
		got <- string(buf[:n])
		_, _ = conn.Write([]byte("{\"id\":\"workspace-move\",\"result\":{\"type\":\"workspace_moved\"}}\n"))
	}()

	bin := filepath.Join(dir, "herdr")
	script := "#!/bin/sh\nif [ \"$1\" = status ]; then echo '{\"running\":true,\"socket\":\"" + sock + "\"}'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := NewCLIClient(bin, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.MoveWorkspace(ctx, "", "ws-1", 3); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-got:
		if !strings.Contains(line, `"method":"workspace.move"`) || !strings.Contains(line, `"insert_index":3`) || !strings.Contains(line, `"workspace_id":"ws-1"`) {
			t.Fatalf("rpc = %s", line)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for socket rpc")
	}
}
