package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeGitToken(t *testing.T) {
	ok := []string{"origin", "main", "feature/a-b", "release_1.2"}
	bad := []string{"", "-o", "--force", "a..b", "a//b", "/abs", "name/", "has space", "refs:heads"}
	for _, s := range ok {
		if !safeGitToken(s) {
			t.Errorf("safeGitToken(%q) = false", s)
		}
	}
	for _, s := range bad {
		if safeGitToken(s) {
			t.Errorf("safeGitToken(%q) = true", s)
		}
	}
}

// trackOrigin publishes dir's main to a new bare repo and sets the upstream.
func trackOrigin(t *testing.T, dir string) string {
	t.Helper()
	bare := t.TempDir()
	git(t, bare, "init", "-q", "--bare", "-b", "main")
	git(t, dir, "remote", "add", "origin", bare)
	git(t, dir, "push", "-q", "-u", "origin", "HEAD")
	return bare
}

func commitFile(t *testing.T, dir, name, body, message string) {
	t.Helper()
	write(t, filepath.Join(dir, name), body)
	git(t, dir, "add", "--", name)
	git(t, dir, "commit", "-q", "-m", message)
}

func head(t *testing.T, dir string) string {
	t.Helper()
	return git(t, dir, "rev-parse", "HEAD")
}

func hasRef(dir, ref string) bool {
	err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", ref).Run()
	return err == nil
}

func TestGitSyncPushFetchPullAndDiscard(t *testing.T) {
	fs, dir := newRepo(t, true)
	bare := trackOrigin(t, dir)
	ctx := context.Background()

	commitFile(t, dir, "tracked.txt", "v2\n", "second")
	if _, err := fs.GitSync(ctx, "local", dir, "push"); err != nil {
		t.Fatalf("push: %v", err)
	}
	if head(t, dir) != git(t, bare, "rev-parse", "main") {
		t.Fatal("push did not update origin/main")
	}
	if _, err := fs.GitSync(ctx, "local", dir, "push"); err == nil || !strings.Contains(err.Error(), "nothing to push") {
		t.Fatalf("second push = %v", err)
	}

	// Another branch is ahead locally and push.default would publish every
	// matching branch. Push must still move only the current branch.
	git(t, dir, "checkout", "-q", "-b", "other")
	commitFile(t, dir, "other.txt", "o1\n", "other")
	git(t, dir, "push", "-q", "origin", "other")
	otherAtPush := head(t, dir)
	commitFile(t, dir, "other.txt", "o2\n", "other again")
	git(t, dir, "checkout", "-q", "main")
	commitFile(t, dir, "tracked.txt", "v3\n", "third")
	git(t, dir, "config", "push.default", "matching")
	if _, err := fs.GitSync(ctx, "local", dir, "push"); err != nil {
		t.Fatalf("push with push.default=matching: %v", err)
	}
	if git(t, bare, "rev-parse", "main") != head(t, dir) {
		t.Fatal("main was not pushed")
	}
	if git(t, bare, "rev-parse", "other") != otherAtPush {
		t.Fatal("push published the other branch")
	}

	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("git", "clone", "-q", bare, other).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	// Local and remote each add a commit on top of the same main.
	commitFile(t, dir, "tracked.txt", "v4\n", "local only")
	commitFile(t, other, "tracked.txt", "remote\n", "from the other side")
	git(t, other, "push", "-q", "origin", "HEAD")

	if _, err := fs.GitSync(ctx, "local", dir, "fetch"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "tracked.txt")); string(got) != "v4\n" {
		t.Fatalf("fetch changed the worktree: %q", got)
	}
	st, err := fs.GitStatus(ctx, "local", dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Behind == 0 || st.Ahead == 0 {
		t.Fatalf("after fetch ahead=%d behind=%d, want both", st.Ahead, st.Behind)
	}
	// The histories diverged (local v3, remote "remote"), so neither side may run.
	if _, err := fs.GitSync(ctx, "local", dir, "push"); err == nil || !strings.Contains(err.Error(), "behind") {
		t.Fatalf("diverged push = %v", err)
	}
	if _, err := fs.GitSync(ctx, "local", dir, "pull"); err == nil || !strings.Contains(err.Error(), "unpushed") {
		t.Fatalf("diverged pull = %v", err)
	}

	// Land on the remote, then let it move once more. A dirty tree is refused;
	// a clean one fast-forwards.
	git(t, dir, "reset", "-q", "--hard", "origin/main")
	commitFile(t, other, "tracked.txt", "further\n", "further")
	git(t, other, "push", "-q", "origin", "HEAD")
	if _, err := fs.GitSync(ctx, "local", dir, "fetch"); err != nil {
		t.Fatalf("fetch update: %v", err)
	}
	write(t, filepath.Join(dir, "tracked.txt"), "dirty\n")
	if _, err := fs.GitSync(ctx, "local", dir, "pull"); err == nil || !strings.Contains(err.Error(), "not clean") {
		t.Fatalf("dirty pull = %v", err)
	}
	git(t, dir, "checkout", "-q", "--", "tracked.txt")
	if _, err := fs.GitSync(ctx, "local", dir, "pull"); err != nil {
		t.Fatalf("ff pull: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "tracked.txt")); string(got) != "further\n" {
		t.Fatalf("pull left %q", got)
	}
}

func TestGitDiscardRestoresWorktreeOnly(t *testing.T) {
	fs, dir := newRepo(t, true)
	ctx := context.Background()
	path := filepath.Join(dir, "tracked.txt")

	write(t, path, "v2\n")
	git(t, dir, "add", "--", "tracked.txt")
	write(t, path, "v3\n")
	if err := fs.GitDiscard(ctx, "local", dir, "tracked.txt"); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "v2\n" {
		t.Fatalf("worktree = %q, want the staged v2", got)
	}
	if git(t, dir, "diff", "--cached", "--name-only") != "tracked.txt" {
		t.Fatal("discard also unstaged the file")
	}

	write(t, filepath.Join(dir, "new.txt"), "x\n")
	if err := fs.GitDiscard(ctx, "local", dir, "new.txt"); err == nil {
		t.Fatal("discarded an untracked file")
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal("untracked file was removed")
	}
	if err := fs.GitDiscard(ctx, "local", dir, "../escape"); err == nil {
		t.Fatal("discard accepted a path outside cwd")
	}
	if _, err := fs.GitSync(ctx, "local", dir, "rebase"); err == nil {
		t.Fatal("unknown action was accepted")
	}

	// Discard from a subdirectory workspace (F1).
	sub := filepath.Join(dir, "sub")
	write(t, filepath.Join(sub, "inner.txt"), "v2-unstaged\n")
	if err := fs.GitDiscard(ctx, "local", sub, "inner.txt"); err != nil {
		t.Fatalf("discard in subdir: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(sub, "inner.txt")); string(got) != "v1\n" {
		t.Fatalf("sub/inner.txt = %q, want v1", got)
	}
}

func TestGitFetchWithoutUpstreamFails(t *testing.T) {
	fs, dir := newRepo(t, true)
	ctx := context.Background()
	if _, err := fs.GitSync(ctx, "local", dir, "fetch"); err == nil || !strings.Contains(err.Error(), "no upstream") {
		t.Fatalf("fetch without upstream = %v, want 'no upstream'", err)
	}
}

func TestGitSyncHandlerIgnoresClientRemote(t *testing.T) {
	r := newGitRig(t)
	bare := trackOrigin(t, r.dir)
	evil := t.TempDir()
	git(t, evil, "init", "-q", "--bare", "-b", "main")
	git(t, r.dir, "remote", "add", "evil", evil)
	commitFile(t, r.dir, "tracked.txt", "v2\n", "second")

	m := r.ask(map[string]any{
		"type": "git_sync", "cwd": r.dir, "action": "push", "remote": "evil", "req_id": "p1",
	})
	if m["type"] != "git_sync_result" || m["ok"] != true || m["action"] != "push" || m["req_id"] != "p1" {
		t.Fatalf("push result: %v", m)
	}
	if !hasRef(bare, "main") || git(t, bare, "rev-parse", "main") != head(t, r.dir) {
		t.Fatal("origin did not receive the push")
	}
	if hasRef(evil, "refs/heads/main") && git(t, evil, "rev-parse", "main") == head(t, r.dir) {
		t.Fatal("client-supplied remote was pushed to")
	}

	m = r.ask(map[string]any{"type": "git_sync", "cwd": r.dir, "action": "push --force", "req_id": "bad"})
	if m["type"] != "error" || m["op"] != "git_sync" || m["key"] != "invalid" || m["req_id"] != "bad" {
		t.Fatalf("bad action: %v", m)
	}

	os.WriteFile(filepath.Join(r.dir, "tracked.txt"), []byte("dirty\n"), 0o644)
	m = r.ask(map[string]any{"type": "git_discard", "cwd": r.dir, "path": "tracked.txt", "req_id": "d1"})
	if m["type"] != "git_status_result" || m["req_id"] != "d1" {
		t.Fatalf("discard: %v", m)
	}
	if got, _ := os.ReadFile(filepath.Join(r.dir, "tracked.txt")); string(got) != "v2\n" {
		t.Fatalf("discard left %q", got)
	}
	m = r.ask(map[string]any{"type": "git_discard", "cwd": r.dir, "path": "../escape", "req_id": "d2"})
	if m["type"] != "error" || m["op"] != "git_discard" || m["key"] != "invalid" {
		t.Fatalf("bad discard: %v", m)
	}
}
