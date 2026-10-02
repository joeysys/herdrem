package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/coder/websocket"
	"herdrem/internal/protocol"
)

// gitStatusMsg is the git_status_result message: the service result's fields
// are promoted to the top level next to type / cwd / req_id. CommitRunning and
// LastCommit carry the repo's commit state, so a page that was closed or
// reopened while a commit ran still sees it and its result (the commit reply
// itself only goes to the connection that asked for it).
type gitStatusMsg struct {
	Type  string `json:"type"`
	Cwd   string `json:"cwd"`
	ReqID string `json:"req_id,omitempty"`
	GitStatusResult
	CommitRunning bool                      `json:"commit_running,omitempty"`
	LastCommit    *protocol.GitCommitResult `json:"last_commit,omitempty"`
}

// lastCommitTTL bounds how long a finished commit's result is reported with
// the status: long enough to come back to a closed page, short enough that an
// old result does not read as news.
const lastCommitTTL = 10 * time.Minute

type commitRecord struct {
	result protocol.GitCommitResult
	at     time.Time
}

// commitKey identifies a repository directory. "" and "local" both mean this
// machine: one key, or a second commit could slip past the guard by spelling
// the host differently.
func commitKey(host, cwd string) string {
	if isLocal(host) {
		host = "local"
	}
	return host + "\x00" + filepath.Clean(cwd)
}

// commitState reports whether a commit is running in the repo and the result
// of the last one, if it finished within lastCommitTTL.
func (s *Service) commitState(host, cwd string) (bool, *protocol.GitCommitResult) {
	key := commitKey(host, cwd)
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	var last *protocol.GitCommitResult
	if rec, ok := s.lastCommit[key]; ok && time.Since(rec.at) < lastCommitTTL {
		r := rec.result
		last = &r
	}
	return s.committing[key], last
}

// finishCommit records the result and releases the repo in one step, so a
// status taken in between never shows the commit as neither running nor done.
func (s *Service) finishCommit(key string, result protocol.GitCommitResult) {
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	if s.lastCommit == nil {
		s.lastCommit = map[string]commitRecord{}
	}
	s.lastCommit[key] = commitRecord{result: result, at: time.Now()}
	delete(s.committing, key)
}

func gitErrCode(err error) string {
	switch {
	case errors.Is(err, ErrGitInvalid):
		return "invalid"
	case isPermErr(err):
		return "permission"
	}
	return "git_error"
}

func (s *Service) sendGitStatus(ctx context.Context, conn *websocket.Conn, host, cwd, reqID, op string) {
	res, err := s.files.GitStatus(ctx, host, cwd)
	if err != nil {
		s.sendErrorKeyReq(ctx, conn, op, err.Error(), gitErrCode(err), reqID)
		return
	}
	msg := gitStatusMsg{Type: "git_status_result", Cwd: cwd, ReqID: reqID, GitStatusResult: res}
	msg.CommitRunning, msg.LastCommit = s.commitState(host, cwd)
	s.sendJSON(ctx, conn, msg)
}

func (s *Service) handleGitStatus(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.files == nil {
		s.sendErrorKeyReq(ctx, conn, "git_status", "files not available", "unavailable", "")
		return
	}
	var msg protocol.GitStatus
	if err := json.Unmarshal(data, &msg); err != nil || msg.Cwd == "" {
		s.sendErrorKeyReq(ctx, conn, "git_status", "invalid message", "invalid", msg.ReqID)
		return
	}
	s.sendGitStatus(ctx, conn, msg.Host, msg.Cwd, msg.ReqID, "git_status")
}

// handleGitStage runs in its own goroutine: staging a large tree must not
// stall this connection's other messages (the hub dispatches them in order).
func (s *Service) handleGitStage(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.files == nil {
		s.sendErrorKeyReq(ctx, conn, "git_stage", "files not available", "unavailable", "")
		return
	}
	var msg protocol.GitStage
	if err := json.Unmarshal(data, &msg); err != nil || msg.Cwd == "" {
		s.sendErrorKeyReq(ctx, conn, "git_stage", "invalid message", "invalid", msg.ReqID)
		return
	}
	go func() {
		// Detached from the connection: a phone going to sleep mid-way must not
		// kill git half-way through. The reply is simply dropped if it left.
		ctx := context.WithoutCancel(ctx)
		if err := s.files.GitStage(ctx, msg.Host, msg.Cwd, msg.Paths, msg.All, msg.Unstage); err != nil {
			s.sendErrorKeyReq(ctx, conn, "git_stage", err.Error(), gitErrCode(err), msg.ReqID)
			return
		}
		s.sendGitStatus(ctx, conn, msg.Host, msg.Cwd, msg.ReqID, "git_stage")
	}()
}

// handleGitCommit commits in its own goroutine (hooks and a custom command can
// take minutes) and allows one commit at a time per repository directory, so
// a double tap cannot commit twice.
func (s *Service) handleGitCommit(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.files == nil {
		s.sendErrorKeyReq(ctx, conn, "git_commit", "files not available", "unavailable", "")
		return
	}
	var msg protocol.GitCommit
	if err := json.Unmarshal(data, &msg); err != nil || msg.Cwd == "" {
		s.sendErrorKeyReq(ctx, conn, "git_commit", "invalid message", "invalid", msg.ReqID)
		return
	}
	reply := protocol.GitCommitResult{Type: "git_commit_result", Cwd: msg.Cwd, ReqID: msg.ReqID}
	command := ""
	if msg.UseCommand {
		if s.prefsStore != nil {
			command = s.prefsStore.Get().Git.CommitCmd
		}
		if command == "" {
			reply.Error = "no commit command configured"
			s.sendJSON(ctx, conn, reply)
			return
		}
		reply.Command = command
	}
	key := commitKey(msg.Host, msg.Cwd)
	s.commitMu.Lock()
	if s.committing == nil {
		s.committing = map[string]bool{}
	}
	if s.committing[key] {
		s.commitMu.Unlock()
		reply.Error = "a commit is already running here"
		s.sendJSON(ctx, conn, reply)
		return
	}
	s.committing[key] = true
	s.commitMu.Unlock()

	go func() {
		ctx := context.WithoutCancel(ctx) // see handleGitStage; commits have their own timeouts
		var res GitCommitResult
		var err error
		if msg.UseCommand {
			res, err = s.files.RunCommitCommand(ctx, msg.Host, msg.Cwd, command)
		} else {
			res, err = s.files.GitCommit(ctx, msg.Host, msg.Cwd, msg.Message)
		}
		reply.Hash, reply.Subject, reply.Output = res.Hash, res.Subject, res.Output
		if err != nil {
			reply.Error = err.Error()
			if errors.Is(err, ErrGitInvalid) && !msg.UseCommand {
				reply.Error = "commit message is empty or too long"
			}
		} else {
			reply.OK = true
		}
		s.finishCommit(key, reply)
		s.sendJSON(ctx, conn, reply)
	}()
}
