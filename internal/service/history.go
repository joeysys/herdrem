package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/coder/websocket"
	"herdrem/internal/protocol"
	"herdrem/internal/store"
)

// SetHistory wires the shared input-history store. Optional: without it the
// relay does not advertise the "input-history" feature and clients keep their
// history in the browser only.
func (s *Service) SetHistory(h *store.HistoryStore) { s.historyStore = h }

// HasHistory reports whether input history is available (feature flag).
func (s *Service) HasHistory() bool { return s.historyStore != nil }

// SendHistorySnapshot pushes the whole history to one connection (on connect).
func (s *Service) SendHistorySnapshot(ctx context.Context, conn *websocket.Conn) {
	if s.historyStore == nil {
		return
	}
	s.sendJSON(ctx, conn, protocol.HistorySnapshot{Type: "history", Entries: s.historyStore.All()})
}

func (s *Service) handleHistoryGet(ctx context.Context, conn *websocket.Conn) {
	if s.historyStore == nil {
		s.sendError(ctx, conn, "history_get", "history store not available")
		return
	}
	s.SendHistorySnapshot(ctx, conn)
}

func (s *Service) handleHistoryRecord(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.historyStore == nil {
		s.sendError(ctx, conn, "history_record", "history store not available")
		return
	}
	var msg protocol.HistoryRecord
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "history_record", "invalid message: "+err.Error())
		return
	}
	kind := store.HistoryShell
	switch msg.Kind {
	case "ag":
		kind = store.HistoryAgent
	case "sh", "":
	default:
		s.sendError(ctx, conn, "history_record", "invalid kind")
		return
	}
	entry, err := s.historyStore.Record(store.HistoryEntry{
		Text:           msg.Text,
		Kind:           kind,
		Pane:           msg.PaneID,
		Agent:          msg.Agent,
		Title:          msg.Title,
		Host:           msg.Host,
		Workspace:      msg.WorkspaceID,
		WorkspaceLabel: msg.WorkspaceLabel,
	}, time.Now().UnixMilli())
	if err != nil {
		// Rejections (blank, opted out, secret…) are normal and silent: the
		// sender already applied its own copy of the same rules.
		if err != store.ErrHistoryRejected {
			s.sendError(ctx, conn, "history_record", err.Error())
		}
		return
	}
	s.hub.BroadcastJSONExcept(protocol.HistoryAdded{Type: "history_added", Entry: entry}, conn)
}

func (s *Service) handleHistoryRemove(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.historyStore == nil {
		s.sendError(ctx, conn, "history_remove", "history store not available")
		return
	}
	var msg protocol.HistoryRemove
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "history_remove", "invalid message: "+err.Error())
		return
	}
	if msg.Text == "" {
		s.sendError(ctx, conn, "history_remove", "missing text")
		return
	}
	removed, err := s.historyStore.Remove(msg.Kind, msg.Text)
	if err != nil {
		s.sendError(ctx, conn, "history_remove", err.Error())
		return
	}
	if removed {
		s.hub.BroadcastJSONExcept(protocol.HistoryRemoved{Type: "history_removed", Kind: msg.Kind, Text: msg.Text}, conn)
	}
}

func (s *Service) handleHistoryClear(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.historyStore == nil {
		s.sendError(ctx, conn, "history_clear", "history store not available")
		return
	}
	var msg protocol.HistoryClear
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "history_clear", "invalid message: "+err.Error())
		return
	}
	switch msg.Kind {
	case "", "all", "sh", "ag":
	default:
		s.sendError(ctx, conn, "history_clear", "invalid kind")
		return
	}
	scoped := msg.Host != "" || msg.WorkspaceID != ""
	if scoped && (msg.Host == "" || msg.WorkspaceID == "") {
		s.sendError(ctx, conn, "history_clear", "workspace scope requires host and workspace_id")
		return
	}
	kind := msg.Kind
	if kind == "" {
		kind = "all"
	}
	n, err := s.historyStore.Clear(kind, scoped, msg.Host, msg.WorkspaceID)
	if err != nil {
		s.sendError(ctx, conn, "history_clear", err.Error())
		return
	}
	if n > 0 {
		s.hub.BroadcastJSONExcept(protocol.HistoryCleared{Type: "history_cleared", Kind: kind, Host: msg.Host, WorkspaceID: msg.WorkspaceID}, conn)
	}
}

func (s *Service) handleHistoryImport(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.historyStore == nil {
		s.sendError(ctx, conn, "history_import", "history store not available")
		return
	}
	var msg protocol.HistoryImport
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "history_import", "invalid message: "+err.Error())
		return
	}
	accepted, err := s.historyStore.Import(msg.Entries)
	if err != nil {
		s.sendError(ctx, conn, "history_import", err.Error())
		return
	}
	if accepted > 0 {
		// Imports are rare and may change many entries: other devices simply
		// take a fresh snapshot instead of replaying each entry.
		s.hub.BroadcastJSONExcept(protocol.HistorySnapshot{Type: "history", Entries: s.historyStore.All()}, conn)
	}
}
