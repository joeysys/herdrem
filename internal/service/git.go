package service

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// maxDiffBytes caps how many bytes a single diff response may return.
// A diff can be very large for big changes; cap it so a single request
// cannot tie up the relay or blow up the client.
const maxDiffBytes = 1 << 20 // 1 MiB

// maxLogCommits caps how many commits git_log returns.
const maxLogCommits = 100

// CommitEntry is one row of the git log shown in the commit selector.
type CommitEntry struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Date    string `json:"date"`    // human-readable, e.g. "2026-08-26 14:04"
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
func (fs *FileService) GitDiff(ctx context.Context, host, cwd string, mode GitDiffMode, commit string) (string, error) {
	roots := fs.roots(host)
	if err := isSafePath(cwd, roots); err != nil {
		return "", err
	}
	args := []string{"--no-pager", "diff", "--no-color"}
	switch mode {
	case GitDiffCached:
		args = append(args, "--cached")
	case GitDiffCommit:
		if commit == "" {
			return "", fmt.Errorf("commit ref required for commit mode")
		}
		// `git show` includes the commit header + diff; use --format= to keep
		// the subject line so the user sees what they picked, then the patch.
		args = []string{"--no-pager", "show", "--no-color", "--format=fuller", commit}
	case GitDiffUnstaged, "":
		// plain diff
	default:
		return "", fmt.Errorf("invalid diff mode: %q", mode)
	}
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
	roots := fs.roots(host)
	if err := isSafePath(cwd, roots); err != nil {
		return nil, err
	}
	// %x1f (unit separator) delimits fields; %x1e (record separator) delimits
	// records. Subject is the final field so embedded newlines in the body
	// (rare in a one-line subject) don't break parsing.
	format := "%H%x1f%an%x1f%ad%x1f%s%x1e"
	args := []string{"--no-pager", "log", "--no-color", "-n", fmt.Sprintf("%d", maxLogCommits), "--date=short", "--pretty=format:" + format}
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

// runGit executes `git <args...>` with cwd as the working directory, locally
// or over SSH for remote hosts. The cwd has already been validated against
// the sandbox roots by the caller.
var remoteGitCommand = func(ctx context.Context, host, cwd string, args ...string) *exec.Cmd {
	gitArgs := append([]string{"-C", cwd}, args...)
	if isLocal(host) {
		return exec.CommandContext(ctx, "git", gitArgs...)
	}
	sshArgs := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		host,
		"git",
	}
	sshArgs = append(sshArgs, gitArgs...)
	return exec.CommandContext(ctx, "ssh", sshArgs...)
}

func (fs *FileService) runGit(ctx context.Context, host, cwd string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := remoteGitCommand(ctx, host, cwd, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		// git diff exits with 0; a non-zero exit usually means not a git repo,
		// bad ref, or ssh failure. Surface a concise message.
		if stderrStr == "" {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return nil, fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, stderrStr)
	}
	return stdout.Bytes(), nil
}
