package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeFakeHerdr creates a fake herdr binary that outputs fixed fixtures,
// so the exec+parse pipeline can be tested without a real herdr install.
func writeFakeHerdr(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake requires POSIX")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	script := `#!/bin/sh
case "$1 $2" in
  "status server") echo '{"running":true}' ;;
  "pane list") echo '{"id":"cli:pane:list","result":{"panes":[{"pane_id":"h:0.1","agent":"claude","cwd":"/demo","agent_status":"working","terminal_title_stripped":"claude: demo"},{"pane_id":"h:0.2","agent":"cursor","cwd":"/demo","agent_status":"blocked","terminal_title_stripped":"cursor: demo"}],"type":"pane_list"}}' ;;
  "pane read") echo '$ echo hello
hello
$ ' ;;
  "pane send-text") exit 0 ;;
  "pane send-keys") exit 0 ;;
  *) echo "unknown: $@" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestCLIClient_Local(t *testing.T) {
	bin := writeFakeHerdr(t)
	c, err := NewCLIClient(bin, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	alive, err := c.Alive(ctx, "")
	if err != nil || !alive {
		t.Fatalf("Alive = %v, %v", alive, err)
	}

	panes, err := c.ListPanes(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(panes))
	}
	if panes[0].PaneID != "h:0.1" || panes[0].Host != "local" {
		t.Errorf("panes[0] = %+v", panes[0])
	}
	if panes[1].Status != "blocked" {
		t.Errorf("panes[1] status = %v", panes[1].Status)
	}

	content, err := c.ReadPane(ctx, "", "h:0.1", 30)
	if err != nil || content == "" {
		t.Errorf("ReadPane = %q, %v", content, err)
	}

	if err := c.SendText(ctx, "", "h:0.1", "y\n"); err != nil {
		t.Errorf("SendText: %v", err)
	}
	if err := c.SendKeys(ctx, "", "h:0.1", "Enter"); err != nil {
		t.Errorf("SendKeys: %v", err)
	}
}

func TestCLIClient_NotFound(t *testing.T) {
	_, err := NewCLIClient(filepath.Join(t.TempDir(), "no-such-herdr"), nil)
	if err == nil {
		t.Fatal("expected error for missing herdr binary")
	}
}

func TestBuildCmd_LocalVsSSH(t *testing.T) {
	c := &CLIClient{bin: "/usr/bin/herdr"}

	local := c.buildCmd(context.Background(), "", "pane", "list")
	if local.Args[0] != "/usr/bin/herdr" {
		t.Errorf("local cmd[0] = %v, want /usr/bin/herdr", local.Args[0])
	}

	ssh := c.buildCmd(context.Background(), "user@remote", "pane", "list")
	if ssh.Args[0] != "ssh" {
		t.Errorf("ssh cmd[0] = %v, want ssh", ssh.Args[0])
	}
	joined := strings.Join(ssh.Args, " ")
	for _, want := range []string{"BatchMode=yes", "ConnectTimeout=5", "user@remote", "/usr/bin/herdr", "pane", "list"} {
		if !strings.Contains(joined, want) {
			t.Errorf("ssh args missing %q: %v", want, ssh.Args)
		}
	}
}

// Remote args must be single-quoted inside the one command string ssh
// concatenates — a pane id or send-text payload containing shell
// metacharacters must not re-parse on the remote.
func TestBuildCmdSSHQuotesRemoteArgs(t *testing.T) {
	c := &CLIClient{bin: "/usr/bin/herdr"}
	ssh := c.buildCmd(context.Background(), "user@remote", "pane", "send-text", "p1", "hi; rm -rf $HOME & `id`")
	remote := ssh.Args[len(ssh.Args)-1]
	for _, want := range []string{"'/usr/bin/herdr'", "'pane'", "'send-text'", `'hi; rm -rf $HOME & ` + "`id`" + `'`} {
		if !strings.Contains(remote, want) {
			t.Errorf("remote command missing quoted %q: %q", want, remote)
		}
	}
	// The remote command is a single ssh argv item — word-splitting or
	// operator injection cannot leak out of it.
	if ssh.Args[len(ssh.Args)-2] != "user@remote" {
		t.Errorf("ssh arg before remote command = %q, want host", ssh.Args[len(ssh.Args)-2])
	}
}

func TestSendTextErrorRedactsPayload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake requires POSIX")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := NewCLIClient(bin, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = c.SendText(context.Background(), "", "p1", "s3cret-hunter")
	if err == nil {
		t.Fatal("expected send-text failure")
	}
	if strings.Contains(err.Error(), "s3cret-hunter") {
		t.Fatalf("error leaked payload: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("error = %v, want <redacted>", err)
	}
}

func TestHostLabel(t *testing.T) {
	if hostLabel("") != "local" || hostLabel("local") != "local" {
		t.Error("local host should map to 'local'")
	}
	if hostLabel("user@h") != "user@h" {
		t.Error("remote host should pass through")
	}
}

func TestCLIClient_ParsesScroll(t *testing.T) {
	bin := writeFakeHerdrScroll(t)
	c, err := NewCLIClient(bin, nil)
	if err != nil {
		t.Fatal(err)
	}
	panes, err := c.ListPanes(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) < 2 {
		t.Fatalf("got %d panes, want >= 2", len(panes))
	}
	// pane 0 is a TUI (scroll max_offset 0), pane 1 has scrollback.
	if panes[0].Scroll == nil || panes[0].Scroll.MaxOffsetFromBottom != 0 {
		t.Errorf("TUI pane scroll = %+v, want max_offset 0", panes[0].Scroll)
	}
	if panes[1].Scroll == nil || panes[1].Scroll.MaxOffsetFromBottom != 100 {
		t.Errorf("scrollback pane scroll = %+v, want max_offset 100", panes[1].Scroll)
	}
}

func writeFakeHerdrScroll(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	script := `#!/bin/sh
case "$1 $2" in
  "pane list") echo '{"id":"cli:pane:list","result":{"panes":[
    {"pane_id":"tui:0.1","agent":"grok","cwd":"/demo","agent_status":"working","scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":44}},
    {"pane_id":"sc:0.2","agent":"codex","cwd":"/demo","agent_status":"idle","scroll":{"max_offset_from_bottom":100,"offset_from_bottom":5,"viewport_rows":44}}
  ]},"type":"pane_list"}' ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestObservePaneSignalsWithoutGridOrAttach(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake requires POSIX")
	}
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "herdr")
	script := `#!/bin/sh
printf '%s\n' "$1" "$2" "$3" "$4" "$5" > "$HERDR_ARGV_FILE"
printf 'x'
exec sleep 30
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_ARGV_FILE", argv)
	c, err := NewCLIClient(bin, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan struct{})
	var once sync.Once
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.ObservePane(ctx, "pane-1", func() {
			once.Do(func() { close(got) })
		})
	}()
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("observe produced no signal")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("after cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("observe did not return after cancel")
	}

	body, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "attach") || strings.Contains(text, "--cols") || strings.Contains(text, "--rows") {
		t.Fatalf("observe argv must not resize or attach: %q", text)
	}
	gotArgs := strings.Split(strings.TrimRight(text, "\n"), "\n")
	want := []string{"terminal", "session", "observe", "pane-1"}
	if len(gotArgs) != len(want) {
		t.Fatalf("argv lines = %q", gotArgs)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Fatalf("argv = %q, want %q", gotArgs, want)
		}
	}
}
