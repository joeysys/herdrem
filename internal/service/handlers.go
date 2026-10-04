package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/coder/websocket"
	"herdrem/internal/protocol"
)

// kindOverrideKey builds the prefs key for a pane override.
func kindOverrideKey(host, paneID string) string {
	if host == "" {
		host = "local"
	}
	return host + ":" + paneID
}

func (s *Service) handleFileRead(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.files == nil {
		s.sendErrorKeyReq(ctx, conn, "file_read", "files not available", "unavailable", "")
		return
	}
	var msg protocol.FileRead
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendErrorKeyReq(ctx, conn, "file_read", "invalid message: "+err.Error(), "invalid", "")
		return
	}
	if msg.Path == "" {
		s.sendErrorKeyReq(ctx, conn, "file_read", "missing path", "invalid", msg.ReqID)
		return
	}
	res, err := s.files.Read(ctx, msg.Host, msg.Path)
	if err != nil {
		code := "not_found"
		if isPermErr(err) {
			code = "permission"
		} else if errors.Is(err, errIsDir) {
			code = "is_dir"
		}
		s.sendErrorKeyReq(ctx, conn, "file_read", err.Error(), code, msg.ReqID)
		return
	}
	s.sendJSON(ctx, conn, res.toProtocol(msg.ReqID))
}

func (s *Service) handleFileList(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.files == nil {
		s.sendErrorKeyReq(ctx, conn, "file_list", "files not available", "unavailable", "")
		return
	}
	var msg protocol.FileList
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendErrorKeyReq(ctx, conn, "file_list", "invalid message: "+err.Error(), "invalid", "")
		return
	}
	if msg.Path == "" {
		s.sendErrorKeyReq(ctx, conn, "file_list", "missing path", "invalid", msg.ReqID)
		return
	}
	entries, err := s.files.List(ctx, msg.Host, msg.Path)
	if err != nil {
		code := "not_found"
		if isPermErr(err) {
			code = "permission"
		}
		s.sendErrorKeyReq(ctx, conn, "file_list", err.Error(), code, msg.ReqID)
		return
	}
	s.sendJSON(ctx, conn, protocol.FileListing{
		Type:    "file_listing",
		Path:    msg.Path,
		Entries: entries,
		ReqID:   msg.ReqID,
	})
}

func (s *Service) handlePrefsGet(ctx context.Context, conn *websocket.Conn) {
	if s.prefsStore == nil {
		s.sendErrorKey(ctx, conn, "prefs_get", "prefs not available", "unavailable")
		return
	}
	s.sendJSON(ctx, conn, protocol.PrefsSnapshot{
		Type:  "prefs",
		Prefs: s.prefsStore.Get(),
	})
}

func (s *Service) handlePrefsSet(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.prefsStore == nil {
		s.sendErrorKey(ctx, conn, "prefs_set", "prefs not available", "unavailable")
		return
	}
	var msg protocol.PrefsSet
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendErrorKey(ctx, conn, "prefs_set", "invalid message: "+err.Error(), "invalid")
		return
	}
	if msg.Section == "" {
		s.sendErrorKeyReq(ctx, conn, "prefs_set", "missing section", "invalid", msg.ReqID)
		return
	}
	if err := s.prefsStore.SetSection(msg.Section, msg.Data); err != nil {
		s.sendErrorKeyReq(ctx, conn, "prefs_set", err.Error(), "invalid", msg.ReqID)
		return
	}
	s.hub.BroadcastJSON(protocol.PrefsSnapshot{
		Type:  "prefs_updated",
		Prefs: s.prefsStore.Get(),
		ReqID: msg.ReqID,
	})
}

func (s *Service) handleKindOverrideSet(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.prefsStore == nil {
		s.sendErrorKey(ctx, conn, "kind_override_set", "prefs not available", "unavailable")
		return
	}
	var msg protocol.KindOverrideSet
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendErrorKey(ctx, conn, "kind_override_set", "invalid message: "+err.Error(), "invalid")
		return
	}
	if msg.PaneID == "" {
		s.sendErrorKey(ctx, conn, "kind_override_set", "missing pane_id", "invalid")
		return
	}
	displayAgent := msg.DisplayAgent
	if displayAgent == "" {
		displayAgent = "agent"
	}
	key := kindOverrideKey(msg.Host, msg.PaneID)
	if err := s.prefsStore.SetKindOverride(key, "agent", displayAgent); err != nil {
		s.sendErrorKey(ctx, conn, "kind_override_set", err.Error(), "invalid")
		return
	}
	s.hub.BroadcastJSON(protocol.PrefsSnapshot{
		Type:  "prefs_updated",
		Prefs: s.prefsStore.Get(),
	})
}

func (s *Service) handleKindOverrideClear(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.prefsStore == nil {
		s.sendErrorKey(ctx, conn, "kind_override_clear", "prefs not available", "unavailable")
		return
	}
	var msg protocol.KindOverrideClear
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendErrorKey(ctx, conn, "kind_override_clear", "invalid message: "+err.Error(), "invalid")
		return
	}
	if msg.PaneID == "" {
		s.sendErrorKey(ctx, conn, "kind_override_clear", "missing pane_id", "invalid")
		return
	}
	key := kindOverrideKey(msg.Host, msg.PaneID)
	if err := s.prefsStore.ClearKindOverride(key); err != nil {
		s.sendErrorKey(ctx, conn, "kind_override_clear", err.Error(), "invalid")
		return
	}
	s.hub.BroadcastJSON(protocol.PrefsSnapshot{
		Type:  "prefs_updated",
		Prefs: s.prefsStore.Get(),
	})
}

func (s *Service) handleMarkPaneRead(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.MarkPaneRead
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendErrorKey(ctx, conn, "mark_pane_read", "invalid message: "+err.Error(), "invalid")
		return
	}
	if msg.PaneID == "" {
		s.sendErrorKey(ctx, conn, "mark_pane_read", "missing pane_id", "invalid")
		return
	}
	type pollTrigger interface {
		AckDone(paneID string) bool
	}
	r, ok := s.router.(pollTrigger)
	if !ok {
		s.sendErrorKey(ctx, conn, "mark_pane_read", "pane acknowledgement unavailable", "unavailable")
		return
	}
	if !r.AckDone(msg.PaneID) {
		s.sendErrorKey(ctx, conn, "mark_pane_read", "pane is not a completed agent", "invalid")
	}
}

func isPermErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "outside allowed roots") || strings.Contains(msg, "permission denied")
}

func (s *Service) handleGitDiff(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.files == nil {
		s.sendErrorKeyReq(ctx, conn, "git_diff", "files not available", "unavailable", "")
		return
	}
	var msg protocol.GitDiff
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendErrorKeyReq(ctx, conn, "git_diff", "invalid message: "+err.Error(), "invalid", "")
		return
	}
	if msg.Cwd == "" {
		s.sendErrorKeyReq(ctx, conn, "git_diff", "missing cwd", "invalid", msg.ReqID)
		return
	}
	mode := GitDiffMode(msg.Mode)
	if mode == "" {
		mode = GitDiffUnstaged
	}
	if msg.Format != "" && msg.Format != "name-status" {
		s.sendErrorKeyReq(ctx, conn, "git_diff", "invalid diff format: "+msg.Format, "invalid", msg.ReqID)
		return
	}
	content, err := s.files.GitDiff(ctx, msg.Host, msg.Cwd, mode, msg.Commit, msg.Format == "name-status")
	if err != nil {
		code := "git_error"
		if isPermErr(err) {
			code = "permission"
		}
		s.sendErrorKeyReq(ctx, conn, "git_diff", err.Error(), code, msg.ReqID)
		return
	}
	s.sendJSON(ctx, conn, protocol.GitDiffContent{
		Type:    "git_diff_content",
		Cwd:     msg.Cwd,
		Mode:    string(mode),
		Commit:  msg.Commit,
		Format:  msg.Format,
		Content: content,
		ReqID:   msg.ReqID,
	})
}

func (s *Service) handleGitLogView(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.files == nil {
		s.sendErrorKeyReq(ctx, conn, "git_log_view", "files not available", "unavailable", "")
		return
	}
	var msg protocol.GitLogView
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendErrorKeyReq(ctx, conn, "git_log_view", "invalid message: "+err.Error(), "invalid", "")
		return
	}
	if msg.Cwd == "" {
		s.sendErrorKeyReq(ctx, conn, "git_log_view", "missing cwd", "invalid", msg.ReqID)
		return
	}
	content, truncated, err := s.files.GitLogView(ctx, msg.Host, msg.Cwd, GitLogOptions{
		Oneline:     msg.Oneline,
		Graph:       msg.Graph,
		All:         msg.All,
		FirstParent: msg.FirstParent,
		NoMerges:    msg.NoMerges,
	})
	if err != nil {
		code := "git_error"
		if isPermErr(err) {
			code = "permission"
		}
		s.sendErrorKeyReq(ctx, conn, "git_log_view", err.Error(), code, msg.ReqID)
		return
	}
	s.sendJSON(ctx, conn, protocol.GitLogViewResult{
		Type:      "git_log_view_result",
		Cwd:       msg.Cwd,
		Content:   content,
		Limit:     maxLogViewCommits,
		Truncated: truncated,
		ReqID:     msg.ReqID,
	})
}

func (s *Service) handleGitLog(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.files == nil {
		s.sendErrorKeyReq(ctx, conn, "git_log", "files not available", "unavailable", "")
		return
	}
	var msg protocol.GitLog
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendErrorKeyReq(ctx, conn, "git_log", "invalid message: "+err.Error(), "invalid", "")
		return
	}
	if msg.Cwd == "" {
		s.sendErrorKeyReq(ctx, conn, "git_log", "missing cwd", "invalid", msg.ReqID)
		return
	}
	commits, err := s.files.GitLog(ctx, msg.Host, msg.Cwd)
	if err != nil {
		code := "git_error"
		if isPermErr(err) {
			code = "permission"
		}
		s.sendErrorKeyReq(ctx, conn, "git_log", err.Error(), code, msg.ReqID)
		return
	}
	s.sendJSON(ctx, conn, protocol.GitLogResult{
		Type:    "git_log_result",
		Cwd:     msg.Cwd,
		Commits: commits,
		ReqID:   msg.ReqID,
	})
}
