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

	unstaged, err := fs.GitDiff(context.Background(), "local", dir, GitDiffUnstaged, "")
	if err != nil {
		t.Fatalf("unstaged diff: %v", err)
	}
	if !strings.Contains(unstaged, "modified-unstaged") {
		t.Errorf("unstaged diff missing change:\n%s", unstaged)
	}
	if !strings.Contains(unstaged, "diff --git") {
		t.Errorf("unstaged diff missing diff header:\n%s", unstaged)
	}

	cached, err := fs.GitDiff(context.Background(), "local", dir, GitDiffCached, "")
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

	out, err := fs.GitDiff(context.Background(), "local", dir, GitDiffCommit, head)
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
	if _, err := fs.GitDiff(context.Background(), "local", dir, GitDiffCommit, ""); err == nil {
		t.Error("commit mode with empty ref should error")
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
	if _, err := fs.GitDiff(context.Background(), "local", dir, GitDiffUnstaged, ""); err == nil {
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
