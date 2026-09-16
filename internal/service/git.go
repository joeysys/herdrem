package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// maxDiffBytes caps how many bytes a single diff response may return.
// A diff can be very large for big changes; cap it so a single request
// cannot tie up the relay or blow up the client.
const maxDiffBytes = 1 << 20 // 1 MiB

// maxLogCommits caps how many commits git_log returns.
const maxLogCommits = 100

// commitRe matches a full 40-character lowercase hex object ID. The commit
// selector is populated from `git log --pretty=%H`, so only full SHAs are
// accepted — this prevents option injection (e.g. --output=...) via the
// commit field.
var commitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// CommitEntry is one row of the git log shown in the commit selector.
type CommitEntry struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Date    string `json:"date"` // human-readable, e.g. "2026-08-26 14:04"
	Subject string `json:"subject"`
}

// GitDiffMode selects which diff to produce.
type GitDiffMode string

const (
	GitDiffUnstaged GitDiffMode = "unstaged" // git diff
	GitDiffCached   GitDiffMode = "cached"   // git diff --cached
	GitDiffCommit   GitDiffMode = "commit"   // git show <commit>
)

// GitDiff runs `git diff` (or --cached / show <commit>) inside cwd and
// returns the diff text. cwd must resolve inside the host's allowed roots.
// The diff is limited to files under cwd (via `-- .` pathspec) so the
// preview respects the same sandbox boundary as the file browser.
func (fs *FileService) GitDiff(ctx context.Context, host, cwd string, mode GitDiffMode, commit string) (string, error) {
	if !fs.hostOK(host) {
		return "", fmt.Errorf("unauthorized host %q", host)
	}
	roots := fs.roots(host)
	if err := isSafePath(cwd, roots); err != nil {
		return "", err
	}
	// --no-ext-diff/--no-textconv pin the diff to builtin text diffing: repo
	// or user config could otherwise name a diff.external command or a
	// diff.<driver>.textconv helper, which git would execute.
	args := []string{"--no-pager", "diff", "--no-color", "--no-ext-diff", "--no-textconv"}
	switch mode {
	case GitDiffCached:
		args = append(args, "--cached")
	case GitDiffCommit:
		if commit == "" {
			return "", fmt.Errorf("commit ref required for commit mode")
		}
		if !commitRe.MatchString(commit) {
			return "", fmt.Errorf("invalid commit ref: %q", commit)
		}
		// `git show` includes the commit header + diff; use --format= to keep
		// the subject line so the user sees what they picked, then the patch.
		args = []string{"--no-pager", "show", "--no-color", "--format=fuller", "--no-ext-diff", "--no-textconv", commit}
	case GitDiffUnstaged, "":
		// plain diff
	default:
		return "", fmt.Errorf("invalid diff mode: %q", mode)
	}
	// Limit the diff to files under cwd so the preview stays within the
	// pane's sandbox boundary (consistent with the file browser).
	args = append(args, "--", ".")
	out, err := fs.runGit(ctx, host, cwd, args...)
	if err != nil {
		return "", err
	}
	if len(out) > maxDiffBytes {
		out = out[:maxDiffBytes]
	}
	return string(out), nil
}

// GitLog returns the most recent commits for the repo at cwd, newest first.
// cwd must resolve inside the host's allowed roots.
func (fs *FileService) GitLog(ctx context.Context, host, cwd string) ([]CommitEntry, error) {
	if !fs.hostOK(host) {
		return nil, fmt.Errorf("unauthorized host %q", host)
	}
	roots := fs.roots(host)
	if err := isSafePath(cwd, roots); err != nil {
		return nil, err
	}
	// %x1f (unit separator) delimits fields; %x1e (record separator) delimits
	// records. Subject is the final field so embedded newlines in the body
	// (rare in a one-line subject) don't break parsing.
	format := "%H%x1f%an%x1f%ad%x1f%s%x1e"
	args := []string{"--no-pager", "log", "--no-color", "--no-ext-diff", "--no-textconv", "-n", fmt.Sprintf("%d", maxLogCommits), "--date=short", "--pretty=format:" + format}
	out, err := fs.runGit(ctx, host, cwd, args...)
	if err != nil {
		return nil, err
	}
	return parseGitLog(out), nil
}

func parseGitLog(out []byte) []CommitEntry {
	trimmed := bytes.TrimRight(out, "\x1e\n")
	if len(trimmed) == 0 {
		return nil
	}
	records := bytes.Split(trimmed, []byte{0x1e})
	entries := make([]CommitEntry, 0, len(records))
	for _, rec := range records {
		rec = bytes.TrimSpace(rec)
		if len(rec) == 0 {
			continue
		}
		fields := bytes.SplitN(rec, []byte{0x1f}, 4)
		if len(fields) != 4 {
			continue
		}
		entries = append(entries, CommitEntry{
			Hash:    string(fields[0]),
			Author:  string(fields[1]),
			Date:    string(fields[2]),
			Subject: string(fields[3]),
		})
	}
	return entries
}

// cancelOnFullWriter wraps boundedWriter and cancels the context once the
// limit is reached, so a process producing unbounded output is killed early
// instead of keeping the relay's pipe buffer full. It always reports a full
// write so the producing process is never blocked on a write we chose not
// to store.
type cancelOnFullWriter struct {
	boundedWriter
	cancel   context.CancelFunc
	overflow bool
}

func (w *cancelOnFullWriter) Write(p []byte) (int, error) {
	n, _ := w.boundedWriter.Write(p)
	if w.boundedWriter.buf.Len() >= w.boundedWriter.limit && !w.overflow {
		w.overflow = true
		w.cancel()
	}
	return n, nil
}

// gitUnsafeEnv lists environment variables that could alter diff plumbing or
// spawn helper processes; they are scrubbed from git invocations. Repo or
// user config exec vectors are additionally pinned off via --no-ext-diff and
// --no-textconv on every git command.
var gitUnsafeEnv = []string{
	"GIT_EXTERNAL_DIFF",
	"GIT_DIFF_OPTS",
	"GIT_PAGER",
	"PAGER",
	"GIT_CONFIG_COUNT",
	"GIT_CONFIG_PARAMETERS",
}

// gitSafeEnv returns the process environment without variables that could
// redirect git's diff/config machinery.
func gitSafeEnv() []string {
	blocked := make(map[string]bool, len(gitUnsafeEnv))
	for _, n := range gitUnsafeEnv {
		blocked[n] = true
	}
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if blocked[name] || strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// runGit executes `git <args...>` with cwd as the working directory, locally
// or over SSH for remote hosts. The cwd has already been validated against
// the sandbox roots by the caller. stdout and stderr are capped at
// maxDiffBytes / maxStderrBytes respectively; if stdout overflows, the
// process is killed early to avoid unbounded memory use.
var remoteGitCommand = func(ctx context.Context, host, cwd string, args ...string) *exec.Cmd {
	if isLocal(host) {
		gitArgs := append([]string{"-C", cwd}, args...)
		cmd := exec.CommandContext(ctx, "git", gitArgs...)
		cmd.Env = gitSafeEnv()
		return cmd
	}
	// For SSH remotes, OpenSSH concatenates all args after the host into a
	// single shell command string. Each argument must be shell-quoted to
	// prevent injection and word-splitting on the remote side. `env -u`
	// scrubs the same diff/config env knobs on the remote.
	var remoteCmd strings.Builder
	for _, n := range gitUnsafeEnv {
		remoteCmd.WriteString("env -u ")
		remoteCmd.WriteString(n)
		remoteCmd.WriteString(" ")
	}
	remoteCmd.WriteString("git ")
	remoteCmd.WriteString(shellQuote("-C"))
	remoteCmd.WriteString(" ")
	remoteCmd.WriteString(shellQuote(cwd))
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

func (fs *FileService) runGit(ctx context.Context, host, cwd string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := remoteGitCommand(ctx, host, cwd, args...)

	stdout := &cancelOnFullWriter{
		boundedWriter: boundedWriter{limit: maxDiffBytes + 1},
		cancel:        cancel,
	}
	stderr := &boundedWriter{limit: maxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	waitErr := cmd.Run()

	if stdout.overflow {
		return stdout.buf.Bytes()[:maxDiffBytes], nil
	}

	if waitErr != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		// git diff exits with 0; a non-zero exit usually means not a git repo,
		// bad ref, or ssh failure. Surface a concise message.
		if stderrStr == "" {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), waitErr)
		}
		return nil, fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), waitErr, stderrStr)
	}
	return stdout.buf.Bytes(), nil
}
