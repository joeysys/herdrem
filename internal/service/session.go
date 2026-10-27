package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/coder/websocket"
	"herdrem/internal/herdr"
	"herdrem/internal/protocol"
)

// herdr's own generated branch uses these lists (worktree.rs, 0.9.3).
var branchAdjectives = []string{"brave", "calm", "clear", "green", "lucky", "quiet", "rapid", "silver"}
var branchNouns = []string{"river", "cloud", "field", "forest", "harbor", "meadow", "stone", "valley"}

var agentKinds = map[string]struct{}{
	"pi": {}, "claude": {}, "codex": {}, "gemini": {}, "cursor": {}, "devin": {},
	"agy": {}, "cline": {}, "omp": {}, "mastracode": {}, "opencode": {}, "copilot": {},
	"kimi": {}, "kiro": {}, "droid": {}, "amp": {}, "grok": {}, "hermes": {},
	"kilo": {}, "qodercli": {}, "qwen": {}, "letta": {}, "maki": {}, "muse": {},
}

func (s *Service) handleSessionRename(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.SessionRename
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "session_rename", "invalid message: "+err.Error())
		return
	}
	if err := s.checkHost(msg.Host); err != nil {
		s.sendOpErr(ctx, conn, "session_rename", err)
		return
	}
	if err := cleanID(msg.ID); err != nil {
		s.sendOpErr(ctx, conn, "session_rename", err)
		return
	}
	label, err := cleanLabel(msg.Label, msg.Target == "pane" && (msg.Clear || strings.TrimSpace(msg.Label) == ""))
	if err != nil {
		s.sendOpErr(ctx, conn, "session_rename", err)
		return
	}
	switch msg.Target {
	case "workspace":
		err = s.client.RenameWorkspace(ctx, msg.Host, msg.ID, label)
	case "tab":
		err = s.client.RenameTab(ctx, msg.Host, msg.ID, label)
	case "pane":
		clear := msg.Clear || label == ""
		err = s.client.RenamePane(ctx, msg.Host, msg.ID, label, clear)
	default:
		err = &opError{key: "invalid", msg: "invalid target"}
	}
	if err != nil {
		s.sendOpErr(ctx, conn, "session_rename", err)
		return
	}
	s.triggerPoll()
	s.sendJSON(ctx, conn, protocol.SessionOK{Type: "session_ok", Op: "session_rename", ID: msg.ID})
}

func (s *Service) handleWorkspaceMove(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.WorkspaceMove
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "workspace_move", "invalid message: "+err.Error())
		return
	}
	if err := s.checkHost(msg.Host); err != nil {
		s.sendOpErr(ctx, conn, "workspace_move", err)
		return
	}
	if err := cleanID(msg.WorkspaceID); err != nil {
		s.sendOpErr(ctx, conn, "workspace_move", err)
		return
	}
	if msg.InsertIndex < 0 {
		s.sendOpErr(ctx, conn, "workspace_move", &opError{key: "invalid", msg: "invalid insert_index"})
		return
	}
	// insert_index is herdr's pre-removal index. The client adjusts a
	// move-down; the relay does not subtract again.
	if err := s.client.MoveWorkspace(ctx, msg.Host, msg.WorkspaceID, msg.InsertIndex); err != nil {
		s.sendOpErr(ctx, conn, "workspace_move", err)
		return
	}
	s.triggerPoll()
	s.sendJSON(ctx, conn, protocol.SessionOK{Type: "session_ok", Op: "workspace_move", ID: msg.WorkspaceID})
}

func (s *Service) handleSessionClose(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.SessionClose
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "session_close", "invalid message: "+err.Error())
		return
	}
	s.once(ctx, conn, "session_close", msg.OperationID, msg.BootID, "", true, func() (any, error) {
		if err := s.checkHost(msg.Host); err != nil {
			return nil, err
		}
		if err := cleanID(msg.ID); err != nil {
			return nil, err
		}
		var err error
		switch msg.Target {
		case "workspace":
			err = s.client.CloseWorkspace(ctx, msg.Host, msg.ID, msg.Group)
		case "tab":
			err = s.client.CloseTab(ctx, msg.Host, msg.ID)
		case "pane":
			err = s.client.ClosePane(ctx, msg.Host, msg.ID)
		default:
			err = &opError{key: "invalid", msg: "invalid target"}
		}
		if err != nil {
			return nil, err
		}
		s.triggerPoll()
		return protocol.SessionOK{Type: "session_ok", Op: "session_close", ID: msg.ID}, nil
	})
}

func (s *Service) handleWorktreeList(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.WorktreeList
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "worktree_list", "invalid message: "+err.Error())
		return
	}
	if err := s.checkHost(msg.Host); err != nil {
		s.sendOpErr(ctx, conn, "worktree_list", err)
		return
	}
	if err := cleanID(msg.WorkspaceID); err != nil {
		s.sendOpErr(ctx, conn, "worktree_list", err)
		return
	}
	rows, err := s.client.WorktreeList(ctx, msg.Host, msg.WorkspaceID)
	if err != nil {
		s.sendOpErr(ctx, conn, "worktree_list", err)
		return
	}
	entries := make([]protocol.WorktreeEntryMsg, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, protocol.WorktreeEntryMsg{
			Path: row.Path, Branch: row.Branch, Label: row.Label,
			OpenWorkspaceID: row.OpenWorkspaceID, Linked: row.Linked,
		})
	}
	s.sendJSON(ctx, conn, protocol.Worktrees{
		Type: "worktrees", WorkspaceID: msg.WorkspaceID, Host: msg.Host, Entries: entries,
	})
}

func (s *Service) handleWorktreeCreate(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.WorktreeCreate
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "worktree_create", "invalid message: "+err.Error())
		return
	}
	// requireID is false so an old client with no operation_id still runs.
	// A repeated id shares one checkout instead of minting another branch.
	s.once(ctx, conn, "worktree_create", msg.OperationID, msg.BootID, "", false, func() (any, error) {
		if err := s.checkHost(msg.Host); err != nil {
			return nil, err
		}
		if err := cleanID(msg.WorkspaceID); err != nil {
			return nil, err
		}
		branch, err := cleanBranch(msg.Branch)
		if err != nil {
			return nil, err
		}
		label := strings.TrimSpace(msg.Label)
		if label == "" {
			label = branch
			if i := strings.LastIndex(label, "/"); i >= 0 {
				label = label[i+1:]
			}
		}
		label, err = cleanLabel(label, false)
		if err != nil {
			return nil, err
		}
		if msg.Base != "" {
			if _, err := cleanBranch(msg.Base); err != nil {
				return nil, &opError{key: "invalid", msg: "invalid base"}
			}
		}
		if err := validateAgent(msg.Agent); err != nil {
			return nil, err
		}
		res, err := s.client.WorktreeCreate(ctx, msg.Host, herdr.WorktreeCreateOpts{
			WorkspaceID: msg.WorkspaceID, Branch: branch, Base: msg.Base, Label: label,
		})
		if err != nil {
			return nil, err
		}
		if err := s.startAgent(ctx, msg.Host, res.PaneID, msg.Agent); err != nil {
			s.triggerPoll()
			return nil, err
		}
		s.triggerPoll()
		return protocol.WorktreeReady{
			Type: "worktree_ready", Op: "worktree_create",
			WorkspaceID: res.WorkspaceID, PaneID: res.PaneID, Branch: res.Branch, Path: res.Path, Host: msg.Host,
		}, nil
	})
}

func (s *Service) handleWorktreeOpen(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.WorktreeOpen
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "worktree_open", "invalid message: "+err.Error())
		return
	}
	if err := s.checkHost(msg.Host); err != nil {
		s.sendOpErr(ctx, conn, "worktree_open", err)
		return
	}
	if msg.WorkspaceID != "" {
		if err := cleanID(msg.WorkspaceID); err != nil {
			s.sendOpErr(ctx, conn, "worktree_open", err)
			return
		}
	}
	if err := cleanPath(msg.Path); err != nil {
		s.sendOpErr(ctx, conn, "worktree_open", err)
		return
	}
	if msg.Branch != "" {
		if _, err := cleanBranch(msg.Branch); err != nil {
			s.sendOpErr(ctx, conn, "worktree_open", err)
			return
		}
	}
	label := strings.TrimSpace(msg.Label)
	if label != "" {
		var err error
		label, err = cleanLabel(label, false)
		if err != nil {
			s.sendOpErr(ctx, conn, "worktree_open", err)
			return
		}
	}
	res, err := s.client.WorktreeOpen(ctx, msg.Host, herdr.WorktreeOpenOpts{
		WorkspaceID: msg.WorkspaceID, Path: msg.Path, Branch: msg.Branch, Label: label,
	})
	if err != nil {
		s.sendOpErr(ctx, conn, "worktree_open", err)
		return
	}
	s.triggerPoll()
	s.sendJSON(ctx, conn, protocol.WorktreeReady{
		Type: "worktree_ready", Op: "worktree_open",
		WorkspaceID: res.WorkspaceID, PaneID: res.PaneID, Branch: res.Branch, Path: res.Path,
		AlreadyOpen: res.AlreadyOpen, Host: msg.Host,
	})
}

func (s *Service) handleWorktreeRemove(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.WorktreeRemove
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "worktree_remove", "invalid message: "+err.Error())
		return
	}
	s.once(ctx, conn, "worktree_remove", msg.OperationID, msg.BootID, "", true, func() (any, error) {
		if err := s.checkHost(msg.Host); err != nil {
			return nil, err
		}
		if err := cleanID(msg.WorkspaceID); err != nil {
			return nil, err
		}
		if err := s.client.WorktreeRemove(ctx, msg.Host, msg.WorkspaceID, msg.Force); err != nil {
			return nil, err
		}
		s.triggerPoll()
		return protocol.SessionOK{Type: "session_ok", Op: "worktree_remove", ID: msg.WorkspaceID}, nil
	})
}

func validateAgent(kind string) error {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || kind == "shell" {
		return nil
	}
	if _, ok := agentKinds[kind]; !ok {
		return &opError{key: "invalid", msg: "unsupported agent"}
	}
	return nil
}

func (s *Service) startAgent(ctx context.Context, host, paneID, kind string) error {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if err := validateAgent(kind); err != nil {
		return err
	}
	if kind == "" || kind == "shell" {
		return nil
	}
	if paneID == "" {
		return fmt.Errorf("worktree was created but the agent did not start: missing pane id")
	}
	name, err := newAgentName()
	if err != nil {
		return fmt.Errorf("worktree was created but the agent did not start: %w", err)
	}
	if err := s.client.AgentStart(ctx, host, paneID, name, kind); err != nil {
		return fmt.Errorf("worktree was created but the agent did not start: %w", err)
	}
	return nil
}

func (s *Service) checkHost(host string) error {
	if !s.allowedHost(host) || strings.HasPrefix(host, "-") {
		return &opError{key: "invalid", msg: "unauthorized host"}
	}
	return nil
}

func (s *Service) sendOpErr(ctx context.Context, conn *websocket.Conn, op string, err error) {
	s.writeRaw(ctx, conn, s.opPayload(op, "", nil, err))
}

func (s *Service) triggerPoll() {
	if s.subscriber != nil {
		s.subscriber.TriggerPoll()
	}
}

func cleanID(id string) error {
	if id == "" || len(id) > 200 || strings.ContainsAny(id, "\r\n") || strings.HasPrefix(id, "-") {
		return &opError{key: "invalid", msg: "invalid id"}
	}
	return nil
}

func cleanLabel(label string, allowEmpty bool) (string, error) {
	label = strings.TrimSpace(label)
	if strings.ContainsAny(label, "\r\n") {
		return "", &opError{key: "invalid", msg: "label contains a newline"}
	}
	if label == "" && !allowEmpty {
		return "", &opError{key: "invalid", msg: "empty label"}
	}
	if len(label) > 200 {
		return "", &opError{key: "invalid", msg: "label is too long"}
	}
	return label, nil
}

func cleanPath(p string) error {
	if strings.TrimSpace(p) == "" || len(p) > 4096 || strings.ContainsAny(p, "\r\n") {
		return &opError{key: "invalid", msg: "invalid path"}
	}
	return nil
}

// cleanBranch returns a generated branch when branch is empty.
// A supplied name cannot contain a newline, a space, a leading dash, or "..".
func cleanBranch(branch string) (string, error) {
	if strings.TrimSpace(branch) == "" {
		return newBranch()
	}
	if len(branch) > 200 || strings.ContainsAny(branch, "\r\n ") || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") {
		return "", &opError{key: "invalid", msg: "invalid branch"}
	}
	return branch, nil
}

func newBranch() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	seed := binary.BigEndian.Uint64(b[:])
	adj := branchAdjectives[seed%uint64(len(branchAdjectives))]
	noun := branchNouns[(seed/uint64(len(branchAdjectives)))%uint64(len(branchNouns))]
	return fmt.Sprintf("worktree/%s-%s-%04x", adj, noun, seed&0xffff), nil
}

func newAgentName() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("wt%x", b), nil
}
