package service

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// gitAvail reports whether git is usable in this environment.
func gitAvail(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
		return false
	}
	return true
}

// initRepo creates a git repo at dir with one committed file, an unstaged
// change, and a staged change, then returns the head commit hash.
func initRepo(t *testing.T, dir string) string {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("config", "commit.gpgsign", "false")
	mustWrite(t, dir+"/a.txt", "initial\n")
	run("add", "a.txt")
	run("commit", "-m", "first commit")
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = dir
	headOut, err := headCmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	head := strings.TrimSpace(string(headOut))

	// staged change
	mustWrite(t, dir+"/b.txt", "staged file\n")
	run("add", "b.txt")
	// unstaged change to a.txt
	mustWrite(t, dir+"/a.txt", "initial\nmodified-unstaged\n")
	return head
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestGitDiff_UnstagedAndCached(t *testing.T) {
	if !gitAvail(t) {
		return
	}
	dir := t.TempDir()
	initRepo(t, dir)
	fs := NewFileService(rootsFor(dir), nil)

	unstaged, err := fs.GitDiff(context.Background(), "local", dir, GitDiffUnstaged, "", "", false)
	if err != nil {
		t.Fatalf("unstaged diff: %v", err)
	}
	if !strings.Contains(unstaged, "modified-unstaged") {
		t.Errorf("unstaged diff missing change:\n%s", unstaged)
	}
	if !strings.Contains(unstaged, "diff --git") {
		t.Errorf("unstaged diff missing diff header:\n%s", unstaged)
	}

	cached, err := fs.GitDiff(context.Background(), "local", dir, GitDiffCached, "", "", false)
	if err != nil {
		t.Fatalf("cached diff: %v", err)
	}
	if !strings.Contains(cached, "staged file") {
		t.Errorf("cached diff missing staged change:\n%s", cached)
	}
	if strings.Contains(cached, "modified-unstaged") {
		t.Errorf("cached diff should not include unstaged changes:\n%s", cached)
	}
}

func TestGitDiff_Commit(t *testing.T) {
	if !gitAvail(t) {
		return
	}
	dir := t.TempDir()
	head := initRepo(t, dir)
	fs := NewFileService(rootsFor(dir), nil)

	out, err := fs.GitDiff(context.Background(), "local", dir, GitDiffCommit, head, "", false)
	if err != nil {
		t.Fatalf("commit diff: %v", err)
	}
	if !strings.Contains(out, "first commit") {
		t.Errorf("commit diff missing commit subject:\n%s", out)
	}
	if !strings.Contains(out, "initial") {
		t.Errorf("commit diff missing file content:\n%s", out)
	}

	// commit mode without a ref must error
	if _, err := fs.GitDiff(context.Background(), "local", dir, GitDiffCommit, "", "", false); err == nil {
		t.Error("commit mode with empty ref should error")
	}
}

func TestGitDiff_Range(t *testing.T) {
	if !gitAvail(t) {
		return
	}
	dir := t.TempDir()
	first := initRepo(t, dir)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("commit", "-m", "second commit")
	secondCmd := exec.Command("git", "rev-parse", "HEAD")
	secondCmd.Dir = dir
	secondOut, err := secondCmd.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	second := strings.TrimSpace(string(secondOut))
	fs := NewFileService(rootsFor(dir), nil)

	out, err := fs.GitDiff(context.Background(), "local", dir, GitDiffRange, first, second, false)
	if err != nil {
		t.Fatalf("range diff: %v", err)
	}
	if !strings.Contains(out, "b.txt") || !strings.Contains(out, "staged file") {
		t.Errorf("range diff missing the second commit's file:\n%s", out)
	}
	if strings.Contains(out, "modified-unstaged") {
		t.Errorf("range diff should compare the two trees, not the worktree:\n%s", out)
	}

	ns, err := fs.GitDiff(context.Background(), "local", dir, GitDiffRange, first, second, true)
	if err != nil {
		t.Fatalf("range name-status: %v", err)
	}
	if ns != "A\tb.txt\n" {
		t.Errorf("range name-status = %q", ns)
	}

	if _, err := fs.GitDiff(context.Background(), "local", dir, GitDiffRange, first, "", false); err == nil {
		t.Error("range mode with one hash should error")
	}
	if _, err := fs.GitDiff(context.Background(), "local", dir, GitDiffRange, first, "--output=/tmp/relay-owned", false); err == nil {
		t.Error("range mode must reject a non-hash second ref")
	}
}

func TestGitDiff_NameStatus(t *testing.T) {
	if !gitAvail(t) {
		return
	}
	dir := t.TempDir()
	head := initRepo(t, dir)
	fs := NewFileService(rootsFor(dir), nil)
	ctx := context.Background()

	unstaged, err := fs.GitDiff(ctx, "local", dir, GitDiffUnstaged, "", "", true)
	if err != nil {
		t.Fatalf("unstaged name-status: %v", err)
	}
	if unstaged != "M\ta.txt\n" {
		t.Errorf("unstaged name-status = %q", unstaged)
	}

	cached, err := fs.GitDiff(ctx, "local", dir, GitDiffCached, "", "", true)
	if err != nil {
		t.Fatalf("cached name-status: %v", err)
	}
	if cached != "A\tb.txt\n" {
		t.Errorf("cached name-status = %q", cached)
	}

	show, err := fs.GitDiff(ctx, "local", dir, GitDiffCommit, head, "", true)
	if err != nil {
		t.Fatalf("commit name-status: %v", err)
	}
	if !strings.Contains(show, "first commit") || !strings.HasSuffix(show, "\nA\ta.txt\n") {
		t.Errorf("commit name-status should keep the header and list a.txt:\n%s", show)
	}
	if strings.Contains(show, "@@") {
		t.Errorf("commit name-status should not carry a patch:\n%s", show)
	}

	// renames keep both paths; non-ASCII names stay readable; paths are
	// relative to a subdirectory cwd like the patch view.
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.Mkdir(dir+"/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, dir+"/sub/ふるい.txt", "same content for rename detection\n")
	run("add", "sub")
	run("commit", "-m", "add sub")
	run("mv", "sub/ふるい.txt", "sub/あたらしい.txt")
	sub, err := fs.GitDiff(ctx, "local", dir+"/sub", GitDiffCached, "", "", true)
	if err != nil {
		t.Fatalf("subdir name-status: %v", err)
	}
	if !strings.HasPrefix(sub, "R100\tふるい.txt\tあたらしい.txt\n") {
		t.Errorf("subdir rename name-status = %q", sub)
	}
}

func TestGitLogView_Switches(t *testing.T) {
	if !gitAvail(t) {
		return
	}
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	initRepo(t, dir)
	run("commit", "-qm", "main two") // b.txt was staged by initRepo
	run("checkout", "-qb", "side")
	mustWrite(t, dir+"/side.txt", "side\n")
	run("add", "side.txt")
	run("commit", "-qm", "side work")
	sideHash := run("rev-parse", "HEAD")
	run("checkout", "-q", "-")
	run("checkout", "-qb", "lonely")
	mustWrite(t, dir+"/lonely.txt", "x\n")
	run("add", "lonely.txt")
	run("commit", "-qm", "unmerged branch")
	run("checkout", "-q", "-")
	run("merge", "-q", "--no-ff", "-m", "merge side", "side")
	// user config that would change the format if not pinned
	run("config", "format.pretty", "short")
	run("config", "log.abbrevCommit", "true")
	run("config", "log.decorate", "no")

	fs := NewFileService(rootsFor(dir), nil)
	ctx := context.Background()
	view := func(o GitLogOptions) string {
		t.Helper()
		out, truncated, err := fs.GitLogView(ctx, "local", dir, o)
		if err != nil {
			t.Fatalf("log view %+v: %v", o, err)
		}
		if truncated {
			t.Fatalf("log view %+v truncated", o)
		}
		return out
	}

	oneline := view(GitLogOptions{Oneline: true})
	if !strings.Contains(oneline, sideHash+" (side) side work") {
		t.Errorf("oneline should carry the full hash:\n%s", oneline)
	}
	if !strings.Contains(oneline, "(HEAD -> ") {
		t.Errorf("oneline should decorate refs despite log.decorate=no:\n%s", oneline)
	}
	if strings.Contains(oneline, "unmerged branch") {
		t.Errorf("without --all the unmerged branch is not reachable:\n%s", oneline)
	}

	full := view(GitLogOptions{})
	if !strings.Contains(full, "commit "+sideHash) || !strings.Contains(full, "Date:") {
		t.Errorf("default should be the medium format with full hashes:\n%s", full)
	}

	if g := view(GitLogOptions{Oneline: true, Graph: true}); !strings.Contains(g, "|\\") && !strings.Contains(g, "|/") {
		t.Errorf("graph should draw the merge:\n%s", g)
	}
	if all := view(GitLogOptions{Oneline: true, All: true}); !strings.Contains(all, "unmerged branch") {
		t.Errorf("--all should list every branch:\n%s", all)
	}
}

func TestGitDiff_OutsideRoots(t *testing.T) {
	if !gitAvail(t) {
		return
	}
	dir := t.TempDir()
	initRepo(t, dir)
	other := t.TempDir()
	fs := NewFileService(rootsFor(other), nil)
	if _, err := fs.GitDiff(context.Background(), "local", dir, GitDiffUnstaged, "", "", false); err == nil {
		t.Error("diff outside allowed roots should be rejected")
	}
}

func TestGitLog(t *testing.T) {
	if !gitAvail(t) {
		return
	}
	dir := t.TempDir()
	initRepo(t, dir)
	fs := NewFileService(rootsFor(dir), nil)

	commits, err := fs.GitLog(context.Background(), "local", dir)
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(commits))
	}
	if commits[0].Subject != "first commit" {
		t.Errorf("subject = %q, want %q", commits[0].Subject, "first commit")
	}
	if commits[0].Hash == "" {
		t.Error("hash is empty")
	}
	if commits[0].Author != "Test" {
		t.Errorf("author = %q, want %q", commits[0].Author, "Test")
	}
}

func TestParseGitLog(t *testing.T) {
	// Two records, second has an empty subject (edge case).
	in := []byte("abc123\x1fAuthor One\x1f2026-08-26\x1fsubject one\x1e" +
		"def456\x1fAuthor Two\x1f2026-08-25\x1f\x1e")
	entries := parseGitLog(in)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Hash != "abc123" || entries[0].Subject != "subject one" {
		t.Errorf("entry 0 mismatch: %+v", entries[0])
	}
	if entries[1].Subject != "" {
		t.Errorf("entry 1 subject should be empty: %+v", entries[1])
	}
}

// TestShellQuote verifies that shellQuote produces strings that are safe to
// embed in a remote shell command, neutralising injection attempts and
// preserving values with spaces, semicolons, and embedded single quotes.
func TestShellQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello", "'hello'"},
		{"a b", "'a b'"},
		{"/repo/x;touch /tmp/pwn;#", "'/repo/x;touch /tmp/pwn;#'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
	}
	for _, c := range cases {
		got := shellQuote(c.in)
		if got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestGitDiff_CommitRejectsOptionInjection verifies that non-SHA commit
// refs (e.g. --output=...) are rejected before reaching git.
func TestGitDiff_CommitRejectsOptionInjection(t *testing.T) {
	if !gitAvail(t) {
		return
	}
	dir := t.TempDir()
	initRepo(t, dir)
	fs := NewFileService(rootsFor(dir), nil)
	bad := []string{
		"--output=/tmp/relay-owned",
		"--format=fuller",
		"HEAD~1",
		"main",
		"abc123", // too short
		"ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ", // not hex
	}
	for _, ref := range bad {
		if _, err := fs.GitDiff(context.Background(), "local", dir, GitDiffCommit, ref, "", false); err == nil {
			t.Errorf("commit ref %q should be rejected", ref)
		}
	}
}

// TestGitDiff_PathspecLimitsToCwd verifies that the `-- .` pathspec limits
// the diff to files under cwd, not the entire repo.
func TestGitDiff_PathspecLimitsToCwd(t *testing.T) {
	if !gitAvail(t) {
		return
	}
	dir := t.TempDir()
	initRepo(t, dir)
	// Create a subdirectory with its own file.
	sub := dir + "/sub"
	os.Mkdir(sub, 0o755)
	mustWrite(t, sub+"/c.txt", "sub file\n")
	// Stage a change in the root and in the subdir.
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("add", "sub/c.txt")

	fs := NewFileService(rootsFor(dir), nil)
	// Diff from the root should include both b.txt and sub/c.txt.
	rootDiff, err := fs.GitDiff(context.Background(), "local", dir, GitDiffCached, "", "", false)
	if err != nil {
		t.Fatalf("root cached diff: %v", err)
	}
	if !strings.Contains(rootDiff, "staged file") {
		t.Errorf("root diff should include b.txt:\n%s", rootDiff)
	}
	if !strings.Contains(rootDiff, "sub file") {
		t.Errorf("root diff should include sub/c.txt:\n%s", rootDiff)
	}

	// Diff from the subdirectory should only include sub/c.txt, not b.txt.
	subDiff, err := fs.GitDiff(context.Background(), "local", sub, GitDiffCached, "", "", false)
	if err != nil {
		t.Fatalf("sub cached diff: %v", err)
	}
	if strings.Contains(subDiff, "staged file") {
		t.Errorf("sub diff should not include b.txt (outside cwd):\n%s", subDiff)
	}
	if !strings.Contains(subDiff, "sub file") {
		t.Errorf("sub diff should include sub/c.txt:\n%s", subDiff)
	}
}

// TestRunGit_BoundedStdout verifies that runGit caps stdout at maxDiffBytes
// and kills the process early instead of buffering unbounded output.
func TestRunGit_BoundedStdout(t *testing.T) {
	if !gitAvail(t) {
		return
	}
	dir := t.TempDir()
	initRepo(t, dir)
	fs := NewFileService(rootsFor(dir), nil)
	// Generate a diff larger than maxDiffBytes by writing a big file.
	// We need to override maxDiffBytes for the test — but since it's a
	// const, we test the bounding indirectly by confirming runGit still
	// works and returns at most maxDiffBytes for a normal diff.
	// Instead, test the bounding mechanism directly with a mock command.
	orig := remoteGitCommand
	defer func() { remoteGitCommand = orig }()
	remoteGitCommand = func(ctx context.Context, host, cwd string, args ...string) *exec.Cmd {
		// Produce 2 MiB of output — well over the 1 MiB cap.
		return exec.CommandContext(ctx, "sh", "-c", "yes hello | head -c $((2*1024*1024))")
	}
	out, err := fs.runGit(context.Background(), "local", dir, "fake")
	if err != nil {
		t.Fatalf("runGit with large output: %v", err)
	}
	if len(out) > maxDiffBytes {
		t.Errorf("runGit returned %d bytes, expected <= %d", len(out), maxDiffBytes)
	}
	if len(out) != maxDiffBytes {
		t.Errorf("runGit should return exactly maxDiffBytes on overflow, got %d", len(out))
	}
}
