package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"herdrem/internal/herdr"
	"herdrem/internal/hub"
	"herdrem/internal/protocol"
	"herdrem/internal/store"
)

// Service routes incoming WS commands to the appropriate handler.
type Service struct {
	client       herdr.Client
	router       herdr.PaneRouter
	todoStore    *store.Store
	prefsStore   *store.PrefsStore
	historyStore *store.HistoryStore
	commitMu     sync.Mutex
	committing   map[string]bool         // commitKey → a commit is running
	syncing      map[string]string       // commitKey → fetch, pull, or push action in flight
	staging      map[string]bool         // commitKey → stage or discard is running
	lastCommit   map[string]commitRecord // commitKey → the last finished commit
	lastSync     map[string]syncRecord   // commitKey → the last finished sync
	files        *FileService
	tunnels      *TunnelService
	hub          *hub.Hub
	subscriber   ContentSubscriber
	allowedHosts map[string]bool
	logger       *slog.Logger
	bootID       string
	ops          *opCache
}

// ContentSubscriber lets the service tell the poller which panes a client is
// watching, so incremental terminal deltas are only pushed for watched panes.
// Implemented by *poll.Poller; declared here as an interface to avoid a
// service→poll import cycle.
type ContentSubscriber interface {
	Subscribe(paneID string)
	// ContentSnapshot returns one atomically paired full snapshot and revision
	// for a pane, and whether a baseline exists. The service includes the same
	// revision in the full response so clients can validate incremental deltas.
	ContentSnapshot(paneID string) (content string, rev int, ok bool)
	// PreviewSnapshot serves a short pane read for grid previews, reusing the
	// poller's version-confirmed content cache or a short-lived preview cache,
	// and single-flighting concurrent reads of the same pane.
	PreviewSnapshot(ctx context.Context, paneID string, lines int) (string, error)
	// InvalidateMenu drops the poller's cached blocked-menu for a pane; called
	// after a send so the next poll re-reads the post-response screen.
	InvalidateMenu(paneID string)
	// PaneAgent is the agent name last seen for the pane, used to hash a
	// prompt id the same way the menu was issued. Empty if unknown.
	PaneAgent(paneID string) string
	// TriggerPoll asks for a poll cycle now, so a stale card is re-read
	// without waiting out the snapshot interval.
	TriggerPoll()
	// ObserveSnapshot is the cached content, herdr revision, monotonic
	// generation, and process epoch for a pane whose output is being observed,
	// taken together from one lock when that cache was read recently. ok is
	// false when the pane is not observed or the cache is missing or stale.
	// Callers must not pair a direct read with this revision: the next delta
	// extends this content.
	ObserveSnapshot(paneID string) (content string, rev int, gen int, epoch int, ok bool)
	// WidenObserveBaseline re-reads an observed pane out to `lines` and stores
	// that read as the delta baseline under the same lock as gen. truncated is
	// true when the request was cut at the relay cap and the read filled it.
	// ok is false when the pane is not observed or the read failed; the caller
	// must not serve the shorter cache in place of this snapshot.
	WidenObserveBaseline(ctx context.Context, paneID string, lines int) (content string, rev int, gen int, epoch int, truncated bool, ok bool)
}

// maxKeySequence caps one cursor approval. keySequenceGap is the pause
// between those keys so the menu can apply each arrow. Tests set the gap to 0.
const maxKeySequence = 8

var keySequenceGap = 55 * time.Millisecond

// VisualLines counts lines the way TailLines does. A trailing newline
// terminates the last visual line rather than adding an empty one.
func VisualLines(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	if s[len(s)-1] != '\n' {
		n++
	}
	return n
}

// TailLines returns the trailing `lines` lines of `s`. A trailing newline
// terminates the last visual line rather than adding an empty one.
func TailLines(s string, lines int) string {
	if lines <= 0 || s == "" {
		return s
	}
	visual := VisualLines(s)
	if visual <= lines {
		return s
	}
	drop := visual - lines
	n := len(s)
	idx := 0
	seen := 0
	for i := 0; i < n && seen < drop; i++ {
		if s[i] == '\n' {
			seen++
			idx = i + 1
		}
	}
	return s[idx:]
}

// New creates a Service. router, prefs, and files may be nil.
func New(client herdr.Client, router herdr.PaneRouter, todo *store.Store, prefs *store.PrefsStore, files *FileService, h *hub.Hub, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		client: client, router: router, todoStore: todo, prefsStore: prefs, files: files, hub: h, logger: logger,
		bootID: newBootID(), ops: newOpCache(),
	}
}

// SetAllowedHosts sets the set of hosts the relay may act on (e.g.
// "local" plus the configured HERDR_REMOTES). Used to authorize
// client-supplied hosts in pane_create so a client cannot make the relay
// invoke herdr/SSH against an arbitrary destination.
func (s *Service) SetAllowedHosts(hosts []string) {
	set := make(map[string]bool, len(hosts)+1)
	set[""] = true // empty = local
	set["local"] = true
	for _, h := range hosts {
		if h != "" {
			set[h] = true
		}
	}
	s.allowedHosts = set
}

// allowedHost reports whether a client-supplied host is safe to route to.
// Hosts are validated against the configured allowlist, and anything that
// ssh would parse as an option (leading dash) is rejected outright since the
// host string reaches ssh's argument list.
func (s *Service) allowedHost(host string) bool {
	if host == "" || host == "local" {
		return true
	}
	if strings.HasPrefix(host, "-") {
		return false
	}
	if s.allowedHosts == nil {
		return false
	}
	return s.allowedHosts[host]
}

// SetContentSubscriber wires the poller's incremental-content subscription
// hook into the service. Called once at startup; may be nil (incremental
// pushes then stay disabled and clients rely on polling alone).
func (s *Service) SetContentSubscriber(sub ContentSubscriber) {
	s.subscriber = sub
}

// SetTunnels sets the TunnelService reference.
func (s *Service) SetTunnels(tunnels *TunnelService) {
	s.tunnels = tunnels
}

// hostFor resolves the host a pane lives on, for command routing.
func (s *Service) hostFor(paneID string) string {
	if s.router == nil {
		return ""
	}
	return s.router.RemoteForPane(paneID)
}

// HandleMessage dispatches a raw WS message to the appropriate handler.
func (s *Service) HandleMessage(ctx context.Context, conn *websocket.Conn, data []byte) {
	msgType := protocol.DecodeType(data)
	switch msgType {
	case "read_pane":
		s.handleReadPane(ctx, conn, data)
	case "send_text":
		s.handleSendText(ctx, conn, data)
	case "send_keys":
		s.handleSendKeys(ctx, conn, data)
	case "respond":
		s.handleRespond(ctx, conn, data)
	case "history_get":
		s.handleHistoryGet(ctx, conn)
	case "history_record":
		s.handleHistoryRecord(ctx, conn, data)
	case "history_remove":
		s.handleHistoryRemove(ctx, conn, data)
	case "history_clear":
		s.handleHistoryClear(ctx, conn, data)
	case "history_favorite":
		s.handleHistoryFavorite(ctx, conn, data)
	case "history_import":
		s.handleHistoryImport(ctx, conn, data)
	case "todo_get":
		s.handleTodoGet(ctx, conn)
	case "todo_set":
		s.handleTodoSet(ctx, conn, data)
	case "file_read":
		s.handleFileRead(ctx, conn, data)
	case "file_list":
		s.handleFileList(ctx, conn, data)
	case "git_diff":
		s.handleGitDiff(ctx, conn, data)
	case "git_log":
		s.handleGitLog(ctx, conn, data)
	case "git_log_view":
		s.handleGitLogView(ctx, conn, data)
	case "git_status":
		s.handleGitStatus(ctx, conn, data)
	case "git_stage":
		s.handleGitStage(ctx, conn, data)
	case "git_commit":
		s.handleGitCommit(ctx, conn, data)
	case "git_sync":
		s.handleGitSync(ctx, conn, data)
	case "git_discard":
		s.handleGitDiscard(ctx, conn, data)
	case "prefs_get":
		s.handlePrefsGet(ctx, conn)
	case "prefs_set":
		s.handlePrefsSet(ctx, conn, data)
	case "kind_override_set":
		s.handleKindOverrideSet(ctx, conn, data)
	case "kind_override_clear":
		s.handleKindOverrideClear(ctx, conn, data)
	case "mark_pane_read":
		s.handleMarkPaneRead(ctx, conn, data)
	case "tunnel_create":
		s.handleTunnelCreate(ctx, conn, data)
	case "tunnel_close":
		s.handleTunnelClose(ctx, conn, data)
	case "tunnels_list":
		s.handleTunnelsList(ctx, conn)
	case "pane_create":
		s.handlePaneCreate(ctx, conn, data)
	case "session_rename":
		s.handleSessionRename(ctx, conn, data)
	case "workspace_move":
		s.handleWorkspaceMove(ctx, conn, data)
	case "session_close":
		s.handleSessionClose(ctx, conn, data)
	case "worktree_list":
		s.handleWorktreeList(ctx, conn, data)
	case "worktree_create":
		s.handleWorktreeCreate(ctx, conn, data)
	case "worktree_open":
		s.handleWorktreeOpen(ctx, conn, data)
	case "worktree_remove":
		s.handleWorktreeRemove(ctx, conn, data)
	default:
		s.sendError(ctx, conn, msgType, "unknown message type")
	}
}

func (s *Service) handleReadPane(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.ReadPane
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "read_pane", "invalid message: "+err.Error())
		return
	}
	if msg.PaneID == "" {
		s.sendError(ctx, conn, "read_pane", "missing pane_id")
		return
	}
	lines := msg.Lines
	if lines <= 0 {
		lines = 30
	}
	// Tell the poller this pane is being watched so it pushes incremental
	// deltas for it. Only terminal reads subscribe; previews stay request/response.
	if msg.Purpose == "term" && s.subscriber != nil {
		s.subscriber.Subscribe(msg.PaneID)
	}
	// A client that declares incremental support may receive append deltas;
	// mark the connection so the hub only routes deltas to capable clients.
	if msg.Incremental {
		s.hub.MarkIncremental(conn)
	}
	var content string
	var err error
	var rev int
	var hasRev bool
	var gen int
	var epoch int
	var replyTruncated bool
	served := false
	// Poll and refresh of an observed pane answer from the cache the next
	// delta extends. Content, rev, and gen come from one lock. A direct read
	// stamped with that rev splits the baseline: the following append is then
	// applied to different bytes and duplicates or drops a line. An ordinary
	// read does not take this path. A refresh whose cache is shorter than the
	// request widens that baseline; the short tail is not served in its place.
	if s.subscriber != nil && (msg.Poll || msg.Refresh) {
		if cached, crev, cgen, cepoch, ok := s.subscriber.ObserveSnapshot(msg.PaneID); ok {
			shortOfRequest := msg.Refresh && VisualLines(cached) < lines
			if shortOfRequest {
				if widened, wrev, wgen, wepoch, truncated, wok := s.subscriber.WidenObserveBaseline(ctx, msg.PaneID, lines); wok {
					content = widened
					rev = wrev
					gen = wgen
					epoch = wepoch
					hasRev = true
					served = true
					replyTruncated = truncated
				}
			} else {
				content = TailLines(cached, lines)
				rev = crev
				gen = cgen
				epoch = cepoch
				hasRev = true
				served = true
			}
		}
	}
	// A refresh of a pane that is not being observed still reads herdr
	// directly, so the reply is not a stale ContentSnapshot.
	if !served && !msg.Refresh && s.subscriber != nil {
		switch msg.Purpose {
		case "term":
			if msg.Poll {
				// Not observed, or the observe cache is stale. Read herdr
				// directly and leave the reply unversioned: streamed output
				// does not move rev/seq, and pairing this read with a cached
				// rev would split the delta baseline.
				break
			}
			content, rev, hasRev = s.subscriber.ContentSnapshot(msg.PaneID)
			if hasRev {
				// Trim the cached snapshot to the requested line count for the
				// initial-open / resync fast path.
				content = TailLines(content, lines)
			}
		case "preview":
			// Grid previews go through the poller's merged-read path: misses
			// share one upstream read per pane (singleflight) and a short-lived
			// cache dedupes the per-card refresh burst. Preview content carries no
			// revision — it is never a delta baseline.
			content, err = s.subscriber.PreviewSnapshot(ctx, msg.PaneID, lines)
			if err != nil {
				s.sendError(ctx, conn, "read_pane", err.Error())
				return
			}
			served = true
		}
	}
	if !served && !hasRev {
		content, err = s.client.ReadPane(ctx, s.hostFor(msg.PaneID), msg.PaneID, lines)
		if err != nil {
			s.sendError(ctx, conn, "read_pane", err.Error())
			return
		}
	}
	reply := protocol.PaneContent{
		Type:    "pane_content",
		PaneID:  msg.PaneID,
		Content: content,
		Lines:   lines,
		Purpose: msg.Purpose,
		Poll:    msg.Poll,
	}
	// The snapshot and revision came from the same poller lock acquisition.
	if hasRev {
		reply.Rev = protocol.IntPtr(rev)
	}
	if gen > 0 {
		reply.Gen = protocol.IntPtr(gen)
	}
	if epoch > 0 {
		reply.Epoch = protocol.IntPtr(epoch)
	}
	reply.Truncated = replyTruncated
	s.sendJSON(ctx, conn, reply)
}

// bracketedPaste wraps multi-line text the way a terminal pastes it, so an
// agent or shell with bracketed paste on inserts the line breaks instead of
// taking each one as Enter. Line breaks go as CR, as a terminal sends them,
// and an end marker inside the text is dropped so it cannot end the paste
// early. Single-line text never comes here: an app without bracketed paste
// would show the markers as junk.
func bracketedPaste(text string) string {
	body := strings.ReplaceAll(text, "\x1b[201~", "")
	body = strings.ReplaceAll(body, "\r\n", "\r")
	body = strings.ReplaceAll(body, "\n", "\r")
	return "\x1b[200~" + body + "\x1b[201~"
}

func (s *Service) handleSendText(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.SendText
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "send_text", "invalid message: "+err.Error())
		return
	}
	if msg.PaneID == "" {
		s.failSend(ctx, conn, "send_text", msg.RequestID, "missing pane_id", "invalid")
		return
	}
	if msg.ExpectTail != "" {
		screen, err := s.readVisible(ctx, msg.PaneID)
		if err != nil {
			s.failSend(ctx, conn, "send_text", msg.RequestID, err.Error(), "unavailable")
			return
		}
		if herdr.NormalizeTail(msg.ExpectTail) != herdr.LastNonEmptyLine(screen) {
			s.failSend(ctx, conn, "send_text", msg.RequestID, "Prompt changed, not sent", "stale")
			return
		}
	}
	text := msg.Text
	if msg.Paste && strings.ContainsAny(text, "\r\n") {
		text = bracketedPaste(text)
		if msg.Submit {
			text += "\n"
		}
	} else if msg.Submit && text != "" && text[len(text)-1] != '\n' {
		text += "\n"
	}
	if err := s.client.SendText(ctx, s.hostFor(msg.PaneID), msg.PaneID, text); err != nil {
		// expect_tail marks a secret send. The CLI error string contains the
		// positional text, so the client only hears that the send did not leave.
		message := err.Error()
		if msg.ExpectTail != "" {
			message = "Send failed, not delivered"
		}
		s.failSend(ctx, conn, "send_text", msg.RequestID, message, "")
		return
	}
	s.ackSend(ctx, conn, "send_text", msg.PaneID, msg.RequestID)
	// The screen is about to change — the next poll must re-read the menu
	// rather than serve the pre-response options from cache.
	if s.subscriber != nil {
		s.subscriber.InvalidateMenu(msg.PaneID)
	}
}

func (s *Service) handleSendKeys(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.SendKeys
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "send_keys", "invalid message: "+err.Error())
		return
	}
	if msg.PaneID == "" {
		s.failSend(ctx, conn, "send_keys", msg.RequestID, "missing pane_id", "invalid")
		return
	}
	keys := []string{msg.Keys}
	if len(msg.Sequence) > 0 {
		if len(msg.Sequence) > maxKeySequence {
			s.failSend(ctx, conn, "send_keys", msg.RequestID, "too many keys", "invalid")
			return
		}
		keys = make([]string, 0, len(msg.Sequence))
		for _, k := range msg.Sequence {
			if strings.TrimSpace(k) == "" {
				s.failSend(ctx, conn, "send_keys", msg.RequestID, "empty key", "invalid")
				return
			}
			keys = append(keys, k)
		}
	}
	if msg.PromptID != "" {
		stale, err := s.promptStale(ctx, msg.PaneID, msg.PromptID)
		if err != nil {
			s.failSend(ctx, conn, "send_keys", msg.RequestID, err.Error(), "unavailable")
			return
		}
		if stale {
			s.rejectStalePrompt(ctx, conn, "send_keys", msg.PaneID, msg.RequestID)
			return
		}
	}
	for i, k := range keys {
		if err := s.client.SendKeys(ctx, s.hostFor(msg.PaneID), msg.PaneID, k); err != nil {
			s.failSend(ctx, conn, "send_keys", msg.RequestID, err.Error(), "")
			return
		}
		if i+1 == len(keys) || keySequenceGap <= 0 {
			continue
		}
		timer := time.NewTimer(keySequenceGap)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.failSend(ctx, conn, "send_keys", msg.RequestID, ctx.Err().Error(), "")
			return
		case <-timer.C:
		}
	}
	s.ackSend(ctx, conn, "send_keys", msg.PaneID, msg.RequestID)
	if s.subscriber != nil {
		s.subscriber.InvalidateMenu(msg.PaneID)
	}
}

// promptStale re-reads the visible screen and reports whether promptID still
// matches. A read error is returned to the caller; it is not treated as stale.
func (s *Service) promptStale(ctx context.Context, paneID, promptID string) (bool, error) {
	screen, err := s.readVisible(ctx, paneID)
	if err != nil {
		return false, err
	}
	agent := ""
	if s.subscriber != nil {
		agent = s.subscriber.PaneAgent(paneID)
	}
	return herdr.PromptID(screen, agent) != promptID, nil
}

func (s *Service) rejectStalePrompt(ctx context.Context, conn *websocket.Conn, op, paneID, requestID string) {
	if s.subscriber != nil {
		s.subscriber.InvalidateMenu(paneID)
		s.subscriber.TriggerPoll()
	}
	s.failSend(ctx, conn, op, requestID, "Prompt changed, please confirm again", "stale")
}

// readVisible reads the same trailing window menu detection hashes.
func (s *Service) readVisible(ctx context.Context, paneID string) (string, error) {
	return s.client.ReadPane(ctx, s.hostFor(paneID), paneID, herdr.VisibleScreenLines)
}

// failSend reports a send_text / send_keys / respond failure. requestID is
// echoed when the client asked for send-ack. A nil conn is a test path that
// only checks whether herdr was called.
func (s *Service) failSend(ctx context.Context, conn *websocket.Conn, op, requestID, message, key string) {
	if conn == nil {
		return
	}
	s.sendJSON(ctx, conn, protocol.ErrorMsg{
		Type:      "error",
		Op:        op,
		Message:   message,
		Key:       key,
		RequestID: requestID,
	})
}

// ackSend writes the sent receipt after herdr has returned. No request id
// means an old client: success stays silent.
func (s *Service) ackSend(ctx context.Context, conn *websocket.Conn, op, paneID, requestID string) {
	if conn == nil || requestID == "" {
		return
	}
	s.sendJSON(ctx, conn, protocol.Sent{
		Type:      "sent",
		Op:        op,
		PaneID:    paneID,
		RequestID: requestID,
	})
}

// isNumericChoice reports whether s represents a numeric option selection (e.g. "1", "2").
// Interactive TUI select menus (Ink/Bubbletea) trigger immediately on the digit keypress.
// Appending a newline leaves an unconsumed Enter in the terminal's stdin buffer, which
// leaks into subsequent input prompts and inserts an empty blank line.
func isNumericChoice(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *Service) handleRespond(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.Respond
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "respond", "invalid message: "+err.Error())
		return
	}
	s.once(ctx, conn, "respond", msg.OperationID, msg.BootID, msg.RequestID, false, func() (any, error) {
		if msg.PaneID == "" {
			return nil, &opError{key: "invalid", msg: "missing pane_id"}
		}
		if msg.PromptID != "" {
			stale, err := s.promptStale(ctx, msg.PaneID, msg.PromptID)
			if err != nil {
				return nil, &opError{key: "unavailable", msg: err.Error()}
			}
			if stale {
				if s.subscriber != nil {
					s.subscriber.InvalidateMenu(msg.PaneID)
					s.subscriber.TriggerPoll()
				}
				return nil, &opError{key: "stale", msg: "Prompt changed, please confirm again"}
			}
		}
		text := msg.Text
		if msg.Submit {
			if text != "" && text[len(text)-1] != '\n' {
				text += "\n"
			}
		} else if isNumericChoice(text) {
			// TUI numbered select choices act on the digit keypress directly;
			// normalizing surrounding whitespace prevents an unconsumed Enter from
			// spilling into the next input prompt as a blank line.
			text = strings.TrimSpace(text)
		} else if text != "" && text[len(text)-1] != '\n' {
			// Line-buffered shell prompts (e.g. y/n) require Enter to submit.
			text += "\n"
		}
		if err := s.client.SendText(ctx, s.hostFor(msg.PaneID), msg.PaneID, text); err != nil {
			return nil, err
		}
		if s.subscriber != nil {
			s.subscriber.InvalidateMenu(msg.PaneID)
		}
		if msg.RequestID == "" {
			return nil, nil
		}
		return protocol.Sent{Type: "sent", Op: "respond", PaneID: msg.PaneID, RequestID: msg.RequestID}, nil
	})
}

func (s *Service) handleTodoGet(ctx context.Context, conn *websocket.Conn) {
	if s.todoStore == nil {
		s.sendError(ctx, conn, "todo_get", "todo store not available")
		return
	}
	snap := protocol.TodosSnapshot{
		Type:  "todos",
		Todos: s.todoStore.All(),
	}
	s.sendJSON(ctx, conn, snap)
}

func (s *Service) handleTodoSet(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.todoStore == nil {
		s.sendError(ctx, conn, "todo_set", "todo store not available")
		return
	}
	var msg protocol.TodoSet
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "todo_set", "invalid message: "+err.Error())
		return
	}
	if msg.Key != "" {
		s.sendError(ctx, conn, "todo_set", "outdated pane-scoped todo message")
		return
	}
	if len(msg.Body) > 64*1024 {
		s.sendError(ctx, conn, "todo_set", "body too large")
		return
	}
	if msg.ID != "" && len(msg.ID) != 32 {
		s.sendError(ctx, conn, "todo_set", "invalid id")
		return
	}
	if msg.Binding != nil && (msg.Binding.Host == "" || msg.Binding.WorkspaceID == "") {
		s.sendError(ctx, conn, "todo_set", "binding requires host and workspace_id")
		return
	}
	if msg.Binding != nil && (len(msg.Binding.Host) > 1024 || len(msg.Binding.WorkspaceID) > 1024 || len(msg.Binding.WorkspaceLabel) > 4096) {
		s.sendError(ctx, conn, "todo_set", "binding too large")
		return
	}
	if msg.Body == "" {
		if msg.ID == "" {
			s.sendError(ctx, conn, "todo_set", "missing id")
			return
		}
		if _, ok := s.todoStore.Get(msg.ID); !ok {
			s.sendError(ctx, conn, "todo_set", "todo not found")
			return
		}
		if err := s.todoStore.Delete(msg.ID); err != nil {
			s.sendError(ctx, conn, "todo_set", err.Error())
			return
		}
		updated := protocol.TodoUpdated{Type: "todo_updated", Todo: store.Todo{ID: msg.ID}, Deleted: true}
		s.sendJSON(ctx, conn, updated)
		s.hub.BroadcastJSONExcept(updated, conn)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	todo := store.Todo{ID: msg.ID, Body: msg.Body, Binding: msg.Binding, UpdatedAt: now}
	if msg.ID == "" {
		id, err := newTodoID()
		if err != nil {
			s.sendError(ctx, conn, "todo_set", "could not create todo id")
			return
		}
		todo.ID = id
		todo.CreatedAt = now
	} else if existing, ok := s.todoStore.Get(msg.ID); ok {
		todo.CreatedAt = existing.CreatedAt
	} else {
		s.sendError(ctx, conn, "todo_set", "todo not found")
		return
	}
	if err := s.todoStore.Set(todo); err != nil {
		s.sendError(ctx, conn, "todo_set", err.Error())
		return
	}
	updated := protocol.TodoUpdated{Type: "todo_updated", Todo: todo}
	s.sendJSON(ctx, conn, updated)
	// Broadcast to all other clients (exclude sender to avoid duplicate)
	s.hub.BroadcastJSONExcept(updated, conn)
}

func newTodoID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", id), nil
}

func (s *Service) sendError(ctx context.Context, conn *websocket.Conn, op, message string) {
	s.sendJSON(ctx, conn, protocol.ErrorMsg{
		Type:    "error",
		Op:      op,
		Message: message,
	})
}

// sendErrorKey sends an error with a machine-readable code (protocol §6 enum).
func (s *Service) sendErrorKey(ctx context.Context, conn *websocket.Conn, op, message, key string) {
	s.sendJSON(ctx, conn, protocol.ErrorMsg{
		Type:    "error",
		Op:      op,
		Message: message,
		Key:     key,
	})
}

// sendErrorKeyReq is sendErrorKey with a request id echo for request-scoped ops.
func (s *Service) sendErrorKeyReq(ctx context.Context, conn *websocket.Conn, op, message, key, reqID string) {
	s.sendJSON(ctx, conn, protocol.ErrorMsg{
		Type:    "error",
		Op:      op,
		Message: message,
		Key:     key,
		ReqID:   reqID,
	})
}

// SendPrefsSnapshot pushes the current prefs to a single connection (used on connect).
func (s *Service) SendPrefsSnapshot(ctx context.Context, conn *websocket.Conn) {
	if s.prefsStore == nil {
		return
	}
	s.sendJSON(ctx, conn, protocol.PrefsSnapshot{
		Type:  "prefs",
		Prefs: s.prefsStore.Get(),
	})
}

func (s *Service) sendJSON(ctx context.Context, conn *websocket.Conn, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		s.logger.Error("marshal error", "err", err)
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := conn.Write(writeCtx, websocket.MessageText, data); err != nil {
		s.logger.Error("write error", "err", err)
	}
}

func (s *Service) handleTunnelCreate(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.tunnels == nil {
		s.sendError(ctx, conn, "tunnel_create", "tunnel service not available")
		return
	}
	var msg protocol.TunnelCreate
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "tunnel_create", "invalid message: "+err.Error())
		return
	}
	var info *protocol.TunnelInfo
	var err error
	if msg.FilePath != "" {
		host := msg.Host
		if host == "" {
			host = "local"
		}
		if s.files != nil && !s.files.IsFile(ctx, host, msg.FilePath) {
			s.sendError(ctx, conn, "tunnel_create", "file not found, is a directory, or outside sandbox roots")
			return
		}
		info, err = s.tunnels.CreateFileTunnel(host, msg.FilePath, msg.AuthMode, msg.Pin)
	} else {
		info, err = s.tunnels.CreateTunnel(msg.Port, msg.AuthMode, msg.Pin)
	}
	if err != nil {
		s.sendError(ctx, conn, "tunnel_create", err.Error())
		return
	}
	s.sendJSON(ctx, conn, protocol.TunnelUpdated{Type: "tunnel_updated", Tunnel: *info})
}

func (s *Service) handleTunnelClose(ctx context.Context, conn *websocket.Conn, data []byte) {
	if s.tunnels == nil {
		s.sendError(ctx, conn, "tunnel_close", "tunnel service not available")
		return
	}
	var msg protocol.TunnelClose
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "tunnel_close", "invalid message: "+err.Error())
		return
	}
	if err := s.tunnels.CloseTunnel(msg.ID); err != nil {
		s.sendError(ctx, conn, "tunnel_close", err.Error())
		return
	}
}

func (s *Service) handleTunnelsList(ctx context.Context, conn *websocket.Conn) {
	if s.tunnels == nil {
		s.sendJSON(ctx, conn, protocol.TunnelsSnapshot{Type: "tunnels", Tunnels: []protocol.TunnelInfo{}})
		return
	}
	s.sendJSON(ctx, conn, protocol.TunnelsSnapshot{Type: "tunnels", Tunnels: s.tunnels.ListTunnels()})
}

func (s *Service) handlePaneCreate(ctx context.Context, conn *websocket.Conn, data []byte) {
	var msg protocol.PaneCreate
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(ctx, conn, "pane_create", "invalid message: "+err.Error())
		return
	}
	s.once(ctx, conn, "pane_create", msg.OperationID, msg.BootID, "", false, func() (any, error) {
		host := msg.Host
		if host == "" && msg.FromPaneID != "" {
			host = s.hostFor(msg.FromPaneID)
		}
		// Security: only route to configured/local hosts. A client-supplied host
		// string also reaches ssh's argument list, so reject anything that could
		// be parsed as an option (leading dash) even if it matches a name.
		if !s.allowedHost(host) || strings.HasPrefix(host, "-") {
			return nil, &opError{key: "invalid", msg: "unauthorized host"}
		}
		var paneID string
		var err error
		switch msg.Mode {
		case "workspace":
			paneID, err = s.client.WorkspaceCreate(ctx, host, msg.Label, msg.Cwd)
		case "tab":
			if msg.WorkspaceID == "" {
				return nil, &opError{key: "invalid", msg: "missing workspace_id for tab mode"}
			}
			paneID, err = s.client.TabCreate(ctx, host, msg.WorkspaceID, msg.Cwd)
		case "shell", "":
			paneID, err = s.client.PaneSplit(ctx, host, msg.FromPaneID, msg.Direction, msg.Cwd)
		default:
			return nil, &opError{key: "invalid", msg: "invalid mode: " + msg.Mode}
		}
		if err != nil {
			return nil, err
		}
		s.triggerPoll()
		return protocol.PaneCreated{
			Type:   "pane_created",
			PaneID: paneID,
			Mode:   normalizePaneMode(msg.Mode),
			Host:   host,
		}, nil
	})
}

// normalizePaneMode returns a concrete mode string for echo: the client's
// mode, or "shell" when empty (the wire default for pane_create).
func normalizePaneMode(mode string) string {
	if mode == "" {
		return "shell"
	}
	return mode
}
