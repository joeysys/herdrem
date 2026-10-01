package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolate git from the developer's own config (hooks, signing, templates).
func gitEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "T")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "T")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo makes a repo with one commit; the sandbox root is the repo dir.
func newRepo(t *testing.T, commit bool) (*FileService, string) {
	t.Helper()
	gitEnv(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	if commit {
		write(t, filepath.Join(dir, "tracked.txt"), "v1\n")
		write(t, filepath.Join(dir, "sub", "inner.txt"), "v1\n")
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", "init")
	}
	return NewFileService(func(string) []string { return []string{dir} }, nil), dir
}

func paths(es []GitStatusEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Status + " " + e.Path
	}
	return out
}

func TestParseStatusV2(t *testing.T) {
	out := strings.Join([]string{
		"# branch.oid 1234",
		"# branch.head feature",
		"# branch.upstream origin/feature",
		"# branch.ab +2 -1",
		"1 M. N... 100644 100644 100644 aaa bbb sub/staged.go",
		"1 .M N... 100644 100644 100644 aaa bbb sub/worktree.go",
		"1 MM N... 100644 100644 100644 aaa bbb sub/both.go",
		"2 R. N... 100644 100644 100644 aaa bbb R100 sub/new name.go", "sub/old.go",
		"u UU N... 100644 100644 100644 100644 a b c sub/conflict.go",
		"? sub/untracked dir/",
		"",
	}, "\x00")
	r := parseStatusV2([]byte(out), "sub/")
	if r.Branch != "feature" || r.Upstream != "origin/feature" || r.Ahead != 2 || r.Behind != 1 || r.Detached || r.Initial {
		t.Fatalf("branch info = %+v", r)
	}
	if got := strings.Join(paths(r.Staged), "|"); got != "M staged.go|M both.go|R new name.go" {
		t.Fatalf("staged = %s", got)
	}
	if r.Staged[2].Orig != "old.go" {
		t.Fatalf("rename source = %q", r.Staged[2].Orig)
	}
	if got := strings.Join(paths(r.Unstaged), "|"); got != "M worktree.go|M both.go" {
		t.Fatalf("unstaged = %s", got)
	}
	if got := strings.Join(paths(r.Conflicted), "|"); got != "UU conflict.go" {
		t.Fatalf("conflicted = %s", got)
	}
	if got := strings.Join(paths(r.Untracked), "|"); got != "? untracked dir/" {
		t.Fatalf("untracked = %s", got)
	}
}

func TestParseStatusV2InitialAndDetached(t *testing.T) {
	r := parseStatusV2([]byte("# branch.oid (initial)\x00# branch.head (detached)\x00"), "")
	if !r.Initial || !r.Detached {
		t.Fatalf("%+v", r)
	}
	if r.Staged == nil || r.Unstaged == nil || r.Untracked == nil || r.Conflicted == nil {
		t.Fatal("lists must be empty, not null, on the wire")
	}
}

func TestGitStatusOnARealRepo(t *testing.T) {
	fs, dir := newRepo(t, true)
	write(t, filepath.Join(dir, "tracked.txt"), "v2\n")      // unstaged
	write(t, filepath.Join(dir, "new.txt"), "n\n")           // untracked
	write(t, filepath.Join(dir, "sub", "inner.txt"), "v2\n") // will be staged
	git(t, dir, "add", "sub/inner.txt")
	r, err := fs.GitStatus(context.Background(), "local", dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Branch != "main" {
		t.Fatalf("branch = %q", r.Branch)
	}
	if got := strings.Join(paths(r.Staged), "|"); got != "M sub/inner.txt" {
		t.Fatalf("staged = %s", got)
	}
	if got := strings.Join(paths(r.Unstaged), "|"); got != "M tracked.txt" {
		t.Fatalf("unstaged = %s", got)
	}
	if got := strings.Join(paths(r.Untracked), "|"); got != "? new.txt" {
		t.Fatalf("untracked = %s", got)
	}
}

func TestGitStatusFromASubdirectoryIsScopedAndCountsOutsideStaging(t *testing.T) {
	fs, dir := newRepo(t, true)
	sub := filepath.Join(dir, "sub")
	write(t, filepath.Join(dir, "tracked.txt"), "outside staged\n")
	git(t, dir, "add", "tracked.txt")
	write(t, filepath.Join(sub, "inner.txt"), "v2\n")
	fs.roots = func(string) []string { return []string{sub} } // pane cwd is the subdir
	r, err := fs.GitStatus(context.Background(), "local", sub)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(paths(r.Unstaged), "|"); got != "M inner.txt" {
		t.Fatalf("paths should be relative to cwd: %s", got)
	}
	if len(r.Staged) != 0 || r.StagedOutside != 1 {
		t.Fatalf("staged=%v outside=%d", r.Staged, r.StagedOutside)
	}
}

func TestGitStatusRejectsCwdOutsideRootsAndNonRepos(t *testing.T) {
	fs, _ := newRepo(t, true)
	if _, err := fs.GitStatus(context.Background(), "local", t.TempDir()); err == nil {
		t.Fatal("cwd outside the roots was accepted")
	}
	plain := t.TempDir()
	fs.roots = func(string) []string { return []string{plain} }
	if _, err := fs.GitStatus(context.Background(), "local", plain); err == nil {
		t.Fatal("a non-repo produced a status")
	}
}

func TestGitStageSelectedAllAndUnstage(t *testing.T) {
	fs, dir := newRepo(t, true)
	ctx := context.Background()
	write(t, filepath.Join(dir, "a*.txt"), "glob name\n")
	write(t, filepath.Join(dir, "ab.txt"), "would match the glob\n")
	if err := fs.GitStage(ctx, "local", dir, []string{"a*.txt"}, false, false); err != nil {
		t.Fatal(err)
	}
	r, _ := fs.GitStatus(ctx, "local", dir)
	if got := strings.Join(paths(r.Staged), "|"); got != "A a*.txt" {
		t.Fatalf("a glob-looking name must stage only itself: %s", got)
	}

	os.Remove(filepath.Join(dir, "tracked.txt"))
	if err := fs.GitStage(ctx, "local", dir, nil, true, false); err != nil {
		t.Fatal(err)
	}
	r, _ = fs.GitStatus(ctx, "local", dir)
	if got := strings.Join(paths(r.Staged), "|"); got != "A a*.txt|A ab.txt|D tracked.txt" {
		t.Fatalf("add all should take new and deleted files: %s", got)
	}
	if len(r.Unstaged)+len(r.Untracked) != 0 {
		t.Fatalf("left unstaged: %+v %+v", r.Unstaged, r.Untracked)
	}

	if err := fs.GitStage(ctx, "local", dir, []string{"ab.txt"}, false, true); err != nil {
		t.Fatal(err)
	}
	r, _ = fs.GitStatus(ctx, "local", dir)
	if got := strings.Join(paths(r.Untracked), "|"); got != "? ab.txt" {
		t.Fatalf("unstaged file should be untracked again: %s", got)
	}
	if err := fs.GitStage(ctx, "local", dir, nil, true, true); err != nil {
		t.Fatal(err)
	}
	r, _ = fs.GitStatus(ctx, "local", dir)
	if len(r.Staged) != 0 {
		t.Fatalf("unstage all left %v", r.Staged)
	}
	if _, err := os.Stat(filepath.Join(dir, "a*.txt")); err != nil {
		t.Fatal("unstaging must not touch the file on disk")
	}
}

func TestGitUnstageWorksBeforeTheFirstCommit(t *testing.T) {
	fs, dir := newRepo(t, false)
	ctx := context.Background()
	write(t, filepath.Join(dir, "first.txt"), "x\n")
	if err := fs.GitStage(ctx, "local", dir, []string{"first.txt"}, false, false); err != nil {
		t.Fatal(err)
	}
	if err := fs.GitStage(ctx, "local", dir, []string{"first.txt"}, false, true); err != nil {
		t.Fatalf("unstage with no HEAD: %v", err)
	}
	r, _ := fs.GitStatus(ctx, "local", dir)
	if !r.Initial || len(r.Staged) != 0 || len(r.Untracked) != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestGitStageRejectsPathsLeavingCwd(t *testing.T) {
	fs, dir := newRepo(t, true)
	for _, p := range []string{"", "../x", "a/../../x", "/etc/passwd", "x\x00y"} {
		if err := fs.GitStage(context.Background(), "local", dir, []string{p}, false, false); !errors.Is(err, ErrGitInvalid) {
			t.Errorf("%q: %v", p, err)
		}
	}
	if err := fs.GitStage(context.Background(), "local", dir, nil, false, false); !errors.Is(err, ErrGitInvalid) {
		t.Errorf("no paths and not all: %v", err)
	}
}

func TestGitCommitWithAMessage(t *testing.T) {
	fs, dir := newRepo(t, true)
	ctx := context.Background()
	write(t, filepath.Join(dir, "tracked.txt"), "v2\n")
	git(t, dir, "add", "tracked.txt")
	msg := "fix: quotes \"and\" $(not a subshell) `x`\n\nbody line"
	res, err := fs.GitCommit(ctx, "local", dir, msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hash) != 40 || res.Subject != `fix: quotes "and" $(not a subshell) `+"`x`" {
		t.Fatalf("result = %+v", res)
	}
	if got := git(t, dir, "log", "-1", "--format=%B"); got != msg {
		t.Fatalf("message stored = %q", got)
	}
}

func TestGitCommitFailuresCarryTheOutput(t *testing.T) {
	fs, dir := newRepo(t, true)
	ctx := context.Background()
	if _, err := fs.GitCommit(ctx, "local", dir, "   "); !errors.Is(err, ErrGitInvalid) {
		t.Fatalf("empty message: %v", err)
	}
	_, err := fs.GitCommit(ctx, "local", dir, "nothing staged")
	var ce *GitCommandError
	if !errors.As(err, &ce) || !strings.Contains(ce.Output, "nothing") {
		t.Fatalf("nothing to commit: %v", err)
	}
	// a failing pre-commit hook's output reaches the user
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	write(t, hook, "#!/bin/sh\necho 'lint failed: x.go:1' >&2\nexit 1\n")
	os.Chmod(hook, 0o755)
	write(t, filepath.Join(dir, "tracked.txt"), "v3\n")
	git(t, dir, "add", "tracked.txt")
	_, err = fs.GitCommit(ctx, "local", dir, "blocked by hook")
	if !errors.As(err, &ce) || !strings.Contains(ce.Output, "lint failed") {
		t.Fatalf("hook output missing: %v", err)
	}
}

func TestRunCommitCommand(t *testing.T) {
	fs, dir := newRepo(t, true)
	ctx := context.Background()
	write(t, filepath.Join(dir, "tracked.txt"), "v2\n")
	git(t, dir, "add", "tracked.txt")

	res, err := fs.RunCommitCommand(ctx, "local", dir, `echo "running in $(pwd)"; git commit -q -m "from the command"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hash) != 40 || res.Subject != "from the command" {
		t.Fatalf("result = %+v", res)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(res.Output, real) && !strings.Contains(res.Output, dir) {
		t.Fatalf("command did not run in the repo dir: %q", res.Output)
	}

	// ran fine, committed nothing: output, no hash
	res, err = fs.RunCommitCommand(ctx, "local", dir, "echo just a message suggestion")
	if err != nil || res.Hash != "" || res.Output != "just a message suggestion" {
		t.Fatalf("no-commit run: %+v %v", res, err)
	}

	// failing command: error + output
	_, err = fs.RunCommitCommand(ctx, "local", dir, "echo nope >&2; exit 3")
	var ce *GitCommandError
	if !errors.As(err, &ce) || ce.Output != "nope" {
		t.Fatalf("failing command: %v", err)
	}

	// non-interactive: anything reading stdin gets EOF immediately
	res, err = fs.RunCommitCommand(ctx, "local", dir, `read x || echo "stdin closed"`)
	if err != nil || res.Output != "stdin closed" {
		t.Fatalf("stdin: %+v %v", res, err)
	}

	if _, err := fs.RunCommitCommand(ctx, "local", dir, "  "); !errors.Is(err, ErrGitInvalid) {
		t.Fatalf("empty command: %v", err)
	}
	if _, err := fs.RunCommitCommand(ctx, "local", t.TempDir(), "true"); err == nil {
		t.Fatal("ran outside the roots")
	}
}
