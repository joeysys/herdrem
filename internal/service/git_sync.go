package service

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// fetch, fast-forward pull, and push of the current branch to its existing
// upstream. The client picks the action; the remote and branch come from the
// repository's own tracking config. Nothing the client sends is placed in
// git's argv except that fixed action.

const syncTimeout = 2 * time.Minute

type tracking struct {
	branch   string
	remote   string
	merge    string // refs/heads/<branch>
	upstream string
	ahead    int
	behind   int
	detached bool
}

// sameBranch is the "simple" push/pull rule: the upstream ref is this branch
// on its remote, not some other name.
func (tr tracking) sameBranch() bool {
	return tr.branch != "" && !tr.detached &&
		tr.merge == "refs/heads/"+tr.branch &&
		safeGitToken(tr.remote) && safeGitToken(tr.branch)
}

// safeGitToken rejects anything that could be read as a flag or a ref trick.
func safeGitToken(s string) bool {
	if s == "" || len(s) > 256 || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") {
		return false
	}
	if strings.Contains(s, "..") || strings.Contains(s, "//") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '/':
		default:
			return false
		}
	}
	return true
}

// GitSync runs fetch, pull, or push. action is "fetch", "pull", or "push".
func (fs *FileService) GitSync(ctx context.Context, host, cwd, action string) (string, error) {
	switch action {
	case "fetch":
		return fs.gitFetch(ctx, host, cwd)
	case "pull":
		return fs.gitPull(ctx, host, cwd)
	case "push":
		return fs.gitPush(ctx, host, cwd)
	default:
		return "", ErrGitInvalid
	}
}

func (fs *FileService) gitFetch(ctx context.Context, host, cwd string) (string, error) {
	if err := fs.checkGitCwd(host, cwd); err != nil {
		return "", err
	}
	tr, err := fs.tracking(ctx, host, cwd)
	if err != nil {
		return "", err
	}
	if tr.remote == "" {
		return "", fmt.Errorf("no upstream")
	}
	if !safeGitToken(tr.remote) {
		return "", fmt.Errorf("%w: upstream remote name", ErrGitInvalid)
	}
	// Only the tracking remote, so a fetch cannot be aimed somewhere else.
	return fs.runGitCaptured(ctx, syncTimeout, host, cwd,
		"-c", "fetch.recurseSubmodules=no",
		"fetch", "--no-tags", tr.remote,
	)
}

func (fs *FileService) gitPull(ctx context.Context, host, cwd string) (string, error) {
	if err := fs.checkGitCwd(host, cwd); err != nil {
		return "", err
	}
	tr, err := fs.tracking(ctx, host, cwd)
	if err != nil {
		return "", err
	}
	if !tr.sameBranch() {
		return "", fmt.Errorf("pull needs this branch to track its own upstream")
	}
	if tr.ahead > 0 {
		return "", fmt.Errorf("branch has unpushed commits")
	}
	if tr.behind == 0 {
		return "", fmt.Errorf("already up to date")
	}
	clean, err := fs.worktreeClean(ctx, host, cwd)
	if err != nil {
		return "", err
	}
	if !clean {
		return "", fmt.Errorf("working tree is not clean")
	}
	// --ff-only and --no-rebase pin the result: user config cannot turn this
	// into a merge commit or a rebase.
	return fs.runGitCaptured(ctx, syncTimeout, host, cwd,
		"-c", "pull.rebase=false",
		"-c", "fetch.recurseSubmodules=no",
		"pull", "--ff-only", "--no-rebase", "--no-tags",
		tr.remote, tr.branch,
	)
}

func (fs *FileService) gitPush(ctx context.Context, host, cwd string) (string, error) {
	if err := fs.checkGitCwd(host, cwd); err != nil {
		return "", err
	}
	tr, err := fs.tracking(ctx, host, cwd)
	if err != nil {
		return "", err
	}
	if tr.detached {
		return "", fmt.Errorf("detached HEAD")
	}
	if tr.remote == "" || tr.merge == "" {
		return "", fmt.Errorf("no upstream")
	}
	if !tr.sameBranch() {
		return "", fmt.Errorf("upstream is not this branch")
	}
	if tr.behind > 0 {
		return "", fmt.Errorf("branch is behind its upstream")
	}
	if tr.ahead == 0 {
		return "", fmt.Errorf("nothing to push")
	}
	// The refspec is the branch name twice, resolved here — not taken from
	// the client. followTags and recurseSubmodules are pinned off so config
	// cannot widen what gets pushed. No --force.
	return fs.runGitCaptured(ctx, syncTimeout, host, cwd,
		"-c", "push.followTags=false",
		"-c", "push.recurseSubmodules=no",
		"push", tr.remote, tr.branch,
	)
}

// GitDiscard restores one tracked path's unstaged worktree changes. Staged
// content stays in the index; untracked and conflicted paths are refused.
func (fs *FileService) GitDiscard(ctx context.Context, host, cwd, path string) error {
	if err := fs.checkGitCwd(host, cwd); err != nil {
		return err
	}
	if !validStagePath(path) || strings.HasSuffix(path, "/") {
		return ErrGitInvalid
	}
	prefixOut, err := fs.runGit(ctx, host, cwd, "rev-parse", "--show-prefix")
	if err != nil {
		return err
	}
	prefix := strings.TrimRight(string(prefixOut), "\n")
	out, err := fs.runGit(ctx, host, cwd, "--no-optional-locks", "status", "--porcelain=v2", "-z", "--", ":(literal)"+path)
	if err != nil {
		return err
	}
	st := parseStatusV2(out, prefix)
	if len(st.Conflicted) > 0 || len(st.Untracked) > 0 || len(st.Unstaged) != 1 || st.Unstaged[0].Path != path {
		return fmt.Errorf("%w: no unstaged change to discard", ErrGitInvalid)
	}
	_, err = fs.runGitT(ctx, stageTimeout, host, cwd, "restore", "--worktree", "--", ":(literal)"+path)
	return err
}

func (fs *FileService) tracking(ctx context.Context, host, cwd string) (tracking, error) {
	st, err := fs.GitStatus(ctx, host, cwd)
	if err != nil {
		return tracking{}, err
	}
	tr := tracking{upstream: st.Upstream, ahead: st.Ahead, behind: st.Behind, detached: st.Detached}
	if st.Detached || st.Branch == "" || st.Branch == "(detached)" {
		tr.detached = true
		return tr, nil
	}
	if !safeGitToken(st.Branch) {
		return tracking{}, fmt.Errorf("%w: branch name", ErrGitInvalid)
	}
	tr.branch = st.Branch
	remote, ok, err := fs.gitConfigGet(ctx, host, cwd, "branch."+st.Branch+".remote")
	if err != nil {
		return tracking{}, err
	}
	if ok {
		tr.remote = remote
	}
	merge, ok, err := fs.gitConfigGet(ctx, host, cwd, "branch."+st.Branch+".merge")
	if err != nil {
		return tracking{}, err
	}
	if ok {
		tr.merge = merge
	}
	return tr, nil
}

func (fs *FileService) gitConfigGet(ctx context.Context, host, cwd, key string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := remoteGitCommand(ctx, host, cwd, "config", "--local", "--get", key)
	out, err := runCaptured(cmd)
	if err == nil {
		return strings.TrimSpace(out), true, nil
	}
	if ctx.Err() != nil {
		return "", false, ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return "", false, nil
	}
	return "", false, fmt.Errorf("git config --get %s: %w", key, err)
}

// worktreeClean reports whether the whole repository — not just cwd — has
// nothing staged, unstaged, or untracked.
func (fs *FileService) worktreeClean(ctx context.Context, host, cwd string) (bool, error) {
	out, err := fs.runGit(ctx, host, cwd, "--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all", "-z")
	if err != nil {
		return false, err
	}
	return strings.Trim(string(out), "\x00\n") == "", nil
}

func (fs *FileService) runGitCaptured(ctx context.Context, timeout time.Duration, host, cwd string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := runCaptured(remoteGitCommand(ctx, host, cwd, args...))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			err = fmt.Errorf("timed out after %s", timeout)
		}
		return output, &GitCommandError{Err: fmt.Errorf("git: %w", err), Output: output}
	}
	return output, nil
}
