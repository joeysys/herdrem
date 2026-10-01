package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// git status / stage / commit. Same sandbox as git diff: the host must be
// allowed and cwd must resolve inside that host's roots. Status and staging
// are limited to cwd's subtree (pathspec "."), like the diff view. A commit
// necessarily records the whole index, so the status reports how many staged
// files lie outside cwd and the UI warns before committing them.

const (
	maxStatusEntries   = 2000
	maxCommitMessage   = 16 << 10
	maxCommitOutput    = 32 << 10
	maxStagePaths      = 1000
	stageTimeout       = 60 * time.Second
	commitTimeout      = 2 * time.Minute
	commitCmdTimeout   = 5 * time.Minute
	MaxCommitCmdLength = 2000
)

// GitStatusEntry is one changed path, relative to cwd.
type GitStatusEntry struct {
	Path   string `json:"path"`
	Status string `json:"status"`         // M A D R C T U ?
	Orig   string `json:"orig,omitempty"` // rename/copy source
}

// GitStatusResult is the parsed `git status` of cwd's subtree.
type GitStatusResult struct {
	Branch        string           `json:"branch"`
	Detached      bool             `json:"detached,omitempty"`
	Initial       bool             `json:"initial,omitempty"` // no commit yet
	Upstream      string           `json:"upstream,omitempty"`
	Ahead         int              `json:"ahead"`
	Behind        int              `json:"behind"`
	Staged        []GitStatusEntry `json:"staged"`
	Unstaged      []GitStatusEntry `json:"unstaged"`
	Untracked     []GitStatusEntry `json:"untracked"`
	Conflicted    []GitStatusEntry `json:"conflicted"`
	StagedOutside int              `json:"staged_outside"` // staged files outside cwd that a commit would include
	Truncated     bool             `json:"truncated,omitempty"`
}

// GitCommitResult reports a commit (or a custom commit command run).
type GitCommitResult struct {
	Hash    string `json:"hash,omitempty"`
	Subject string `json:"subject,omitempty"`
	Output  string `json:"output,omitempty"` // hooks / command output, capped
}

// ErrGitInvalid marks a malformed request (bad path, empty message…).
var ErrGitInvalid = errors.New("invalid git request")

// GitCommandError carries the output of a failed commit / command so the UI
// can show what the hook or command said.
type GitCommandError struct {
	Err    error
	Output string
}

func (e *GitCommandError) Error() string { return e.Err.Error() }
func (e *GitCommandError) Unwrap() error { return e.Err }

func (fs *FileService) checkGitCwd(host, cwd string) error {
	if !fs.hostOK(host) {
		return fmt.Errorf("unauthorized host %q", host)
	}
	if cwd == "" || !filepath.IsAbs(cwd) {
		return ErrGitInvalid
	}
	return isSafePath(cwd, fs.roots(host))
}

// GitStatus returns the status of cwd's subtree.
func (fs *FileService) GitStatus(ctx context.Context, host, cwd string) (GitStatusResult, error) {
	if err := fs.checkGitCwd(host, cwd); err != nil {
		return GitStatusResult{}, err
	}
	prefixOut, err := fs.runGit(ctx, host, cwd, "rev-parse", "--show-prefix")
	if err != nil {
		return GitStatusResult{}, err
	}
	prefix := strings.TrimRight(string(prefixOut), "\n")
	// --no-optional-locks: a status view must not take the index lock that a
	// concurrent `git commit` in a terminal needs.
	out, err := fs.runGit(ctx, host, cwd, "--no-optional-locks", "status", "--porcelain=v2", "-z", "--branch", "--", ".")
	if err != nil {
		return GitStatusResult{}, err
	}
	res := parseStatusV2(out, prefix)
	if prefix != "" {
		names, err := fs.runGit(ctx, host, cwd, "--no-optional-locks", "diff", "--cached", "--name-only", "-z", "--no-ext-diff", "--no-textconv")
		if err == nil {
			for _, n := range strings.Split(string(names), "\x00") {
				if n != "" && !strings.HasPrefix(n, prefix) {
					res.StagedOutside++
				}
			}
		}
	}
	return res, nil
}

// parseStatusV2 parses `git status --porcelain=v2 -z --branch`. Paths come
// relative to the repository root; prefix (cwd relative to the root) is
// stripped so the client sees paths relative to cwd.
func parseStatusV2(out []byte, prefix string) GitStatusResult {
	res := GitStatusResult{Staged: []GitStatusEntry{}, Unstaged: []GitStatusEntry{}, Untracked: []GitStatusEntry{}, Conflicted: []GitStatusEntry{}}
	rel := func(p string) string { return strings.TrimPrefix(p, prefix) }
	count := 0
	add := func(list *[]GitStatusEntry, e GitStatusEntry) {
		if count >= maxStatusEntries {
			res.Truncated = true
			return
		}
		count++
		*list = append(*list, e)
	}
	recs := strings.Split(string(out), "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if rec == "" {
			continue
		}
		switch {
		case strings.HasPrefix(rec, "# branch.oid "):
			res.Initial = strings.TrimPrefix(rec, "# branch.oid ") == "(initial)"
		case strings.HasPrefix(rec, "# branch.head "):
			res.Branch = strings.TrimPrefix(rec, "# branch.head ")
			res.Detached = res.Branch == "(detached)"
		case strings.HasPrefix(rec, "# branch.upstream "):
			res.Upstream = strings.TrimPrefix(rec, "# branch.upstream ")
		case strings.HasPrefix(rec, "# branch.ab "):
			for _, f := range strings.Fields(strings.TrimPrefix(rec, "# branch.ab ")) {
				n, _ := strconv.Atoi(f[1:])
				if f[0] == '+' {
					res.Ahead = n
				} else if f[0] == '-' {
					res.Behind = n
				}
			}
		case rec[0] == '1' || rec[0] == '2':
			n := 9
			if rec[0] == '2' {
				n = 10
			}
			f := strings.SplitN(rec, " ", n)
			if len(f) < n || len(f[1]) != 2 {
				continue
			}
			path, orig := rel(f[n-1]), ""
			if rec[0] == '2' && i+1 < len(recs) {
				orig = rel(recs[i+1])
				i++
			}
			x, y := f[1][0], f[1][1]
			if x != '.' {
				add(&res.Staged, GitStatusEntry{Path: path, Status: string(x), Orig: orig})
			}
			if y != '.' {
				add(&res.Unstaged, GitStatusEntry{Path: path, Status: string(y)})
			}
		case rec[0] == 'u':
			f := strings.SplitN(rec, " ", 11)
			if len(f) == 11 {
				add(&res.Conflicted, GitStatusEntry{Path: rel(f[10]), Status: f[1]})
			}
		case rec[0] == '?':
			add(&res.Untracked, GitStatusEntry{Path: rel(strings.TrimPrefix(rec, "? ")), Status: "?"})
		}
	}
	return res
}

// validStagePath accepts a path relative to cwd that stays inside it.
func validStagePath(p string) bool {
	if p == "" || len(p) > 4096 || strings.ContainsRune(p, 0) || filepath.IsAbs(p) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// literalPathspecs turns paths into ":(literal)" pathspecs, so names with
// glob characters (*, ?, [) stage exactly that file.
func literalPathspecs(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = ":(literal)" + p
	}
	return out
}

// GitStage adds (or, with unstage, removes from the index) the given paths —
// or everything under cwd with all.
func (fs *FileService) GitStage(ctx context.Context, host, cwd string, paths []string, all, unstage bool) error {
	if err := fs.checkGitCwd(host, cwd); err != nil {
		return err
	}
	if !all {
		if len(paths) == 0 || len(paths) > maxStagePaths {
			return ErrGitInvalid
		}
		for _, p := range paths {
			if !validStagePath(p) {
				return ErrGitInvalid
			}
		}
	}
	specs := []string{"."}
	if !all {
		specs = literalPathspecs(paths)
	}
	if !unstage {
		args := []string{"add"}
		if all {
			args = append(args, "-A") // new, modified and deleted files alike
		}
		_, err := fs.runGitT(ctx, stageTimeout, host, cwd, append(append(args, "--"), specs...)...)
		return err
	}
	_, err := fs.runGitT(ctx, stageTimeout, host, cwd, append([]string{"restore", "--staged", "--"}, specs...)...)
	if err != nil && strings.Contains(err.Error(), "HEAD") {
		// No commit yet: there is no HEAD to restore from, so drop the
		// entries from the index instead (the files stay on disk).
		_, err = fs.runGitT(ctx, stageTimeout, host, cwd, append([]string{"rm", "--cached", "-r", "-q", "--"}, specs...)...)
	}
	return err
}

// GitCommit commits the index with message (passed on stdin, never argv).
func (fs *FileService) GitCommit(ctx context.Context, host, cwd, message string) (GitCommitResult, error) {
	if err := fs.checkGitCwd(host, cwd); err != nil {
		return GitCommitResult{}, err
	}
	if strings.TrimSpace(message) == "" || len(message) > maxCommitMessage {
		return GitCommitResult{}, ErrGitInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, commitTimeout)
	defer cancel()
	cmd := remoteGitCommand(ctx, host, cwd, "commit", "-F", "-")
	cmd.Stdin = strings.NewReader(message)
	output, err := runCaptured(cmd)
	if err != nil {
		return GitCommitResult{Output: output}, &GitCommandError{Err: fmt.Errorf("git commit: %w", err), Output: output}
	}
	res := fs.headSummary(ctx, host, cwd)
	res.Output = output
	return res, nil
}

// remoteShellCommand runs a user-configured command with sh in cwd. A var so
// tests can stub the ssh hop.
var remoteShellCommand = func(ctx context.Context, host, cwd, command string) *exec.Cmd {
	if isLocal(host) {
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		return cmd
	}
	script := "cd " + shellQuote(cwd) + " && GIT_TERMINAL_PROMPT=0 sh -c " + shellQuote(command)
	return exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", host, script)
}

// RunCommitCommand runs the configured commit command in cwd. It is
// non-interactive: stdin is empty, git cannot prompt, and it is killed after
// commitCmdTimeout. The command comes from relay prefs, never from the
// request that triggers it.
func (fs *FileService) RunCommitCommand(ctx context.Context, host, cwd, command string) (GitCommitResult, error) {
	if err := fs.checkGitCwd(host, cwd); err != nil {
		return GitCommitResult{}, err
	}
	command = strings.TrimSpace(command)
	if command == "" || len(command) > MaxCommitCmdLength || strings.ContainsRune(command, 0) {
		return GitCommitResult{}, ErrGitInvalid
	}
	before := fs.headSummary(ctx, host, cwd).Hash
	ctx, cancel := context.WithTimeout(ctx, commitCmdTimeout)
	defer cancel()
	output, err := runCaptured(remoteShellCommand(ctx, host, cwd, command))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			err = fmt.Errorf("timed out after %s", commitCmdTimeout)
		}
		return GitCommitResult{Output: output}, &GitCommandError{Err: fmt.Errorf("commit command: %w", err), Output: output}
	}
	res := fs.headSummary(context.Background(), host, cwd)
	if res.Hash == before {
		// It ran fine but made no commit (e.g. nothing staged, or the
		// command only prepared a message): report the output, not a hash.
		res = GitCommitResult{}
	}
	res.Output = output
	return res, nil
}

func (fs *FileService) headSummary(ctx context.Context, host, cwd string) GitCommitResult {
	out, err := fs.runGit(ctx, host, cwd, "log", "-1", "--format=%H%x00%s")
	if err != nil {
		return GitCommitResult{}
	}
	hash, subject, _ := strings.Cut(strings.TrimRight(string(out), "\n"), "\x00")
	return GitCommitResult{Hash: hash, Subject: subject}
}

// runCaptured runs cmd with stdout and stderr interleaved into one capped
// buffer (hook and command output reads best in the order it was written).
func runCaptured(cmd *exec.Cmd) (string, error) {
	out := &boundedWriter{limit: maxCommitOutput}
	cmd.Stdout = out
	cmd.Stderr = out // same writer: exec serialises the two streams
	err := cmd.Run()
	return strings.TrimRight(out.String(), "\n"), err
}
