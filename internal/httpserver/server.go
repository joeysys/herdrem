package httpserver

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"herdrem/internal/auth"
	"herdrem/internal/config"
	"herdrem/internal/herdr"
	"herdrem/internal/hub"
	"herdrem/internal/protocol"
	"herdrem/internal/service"
	"herdrem/internal/store"
)

// Server is the HTTP server with WS + static + health.
const previewAuthCookieName = "herdr_preview_auth"

type Server struct {
	cfg         config.Config
	hub         *hub.Hub
	client      herdr.Client
	todoStore   *store.Store
	svc         *service.Service
	files       *service.FileService
	tunnels     *service.TunnelService
	poller      paneSnapshotter
	logger      *slog.Logger
	staticFS    fs.FS // embedded or override
	gateLimiter *gateRateLimiter
}

// paneSnapshotter returns the latest pane list for initial WS snapshots.
type paneSnapshotter interface {
	Snapshot() []herdr.Pane
}

// New creates an HTTP server.
func New(cfg config.Config, h *hub.Hub, client herdr.Client, todo *store.Store, svc *service.Service, files *service.FileService, poller paneSnapshotter, staticFS fs.FS, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg:         cfg,
		hub:         h,
		client:      client,
		todoStore:   todo,
		svc:         svc,
		files:       files,
		poller:      poller,
		logger:      logger,
		staticFS:    staticFS,
		gateLimiter: newGateRateLimiter(),
	}
}

// SetTunnels sets the TunnelService instance for the server.
func (s *Server) SetTunnels(tunnels *service.TunnelService) {
	s.tunnels = tunnels
}

// Routes returns the configured http.Handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/push", s.handlePush)
	mux.HandleFunc("/file", s.handleFile)
	mux.HandleFunc("/preview/", s.handleFilePreview)
	mux.HandleFunc("/preview-auth", s.handlePreviewAuth)
	mux.HandleFunc("/api/tunnel-gate/", s.handleTunnelGate)
	// Static SPA — catch-all
	if s.staticFS != nil {
		mux.Handle("/", fixMimeTypes(http.FileServer(http.FS(s.staticFS))))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.Host, ".tunnel.local") {
			s.handleTunnelGateByHost(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// fixMimeTypes sets correct Content-Type for files that Go's stdlib
// mime table doesn't know about (e.g. .webmanifest → application/manifest+json).
// Without this, http.FileServer serves .webmanifest as text/plain (via content
// sniffing), which causes mobile browsers to reject PWA installation.
func fixMimeTypes(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".webmanifest") {
			w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// handleFile serves static files (HTML, CSS, JS, images, etc.) via HTTP stream.
// GET /file?path=<path>&host=<host>&token=<token>.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	if s.files == nil {
		http.Error(w, "files not available", http.StatusServiceUnavailable)
		return
	}
	if !auth.Check(r, s.cfg.Token) && !(s.staticFS != nil && isSameOrigin(r)) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	path := r.URL.Query().Get("path")
	host := r.URL.Query().Get("host")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	if err := s.files.ServeStream(w, r, host, path); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
}

// handleFilePreview serves files via a path-based URL (/preview/<path>) so that
// relative URLs in HTML documents resolve against the file's directory rather
// than the /file endpoint. This enables HTML previews with linked CSS/JS/images.
// GET /preview/<path>?host=<host>&token=<token>
func (s *Server) handleFilePreview(w http.ResponseWriter, r *http.Request) {
	if s.files == nil {
		http.Error(w, "files not available", http.StatusServiceUnavailable)
		return
	}
	if !s.checkPreviewAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// The host is encoded into the first path segment so relative resource
	// requests keep the remote-host context. Auth is via scoped cookie only —
	// no token in URL to prevent sandboxed scripts from exfiltrating it (F1).
	path := strings.TrimPrefix(r.URL.Path, "/preview/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	hostBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(hostBytes) == 0 {
		http.Error(w, "invalid host", http.StatusBadRequest)
		return
	}
	host := string(hostBytes)
	filePath := "/" + strings.TrimPrefix(parts[1], "/")

	// F2: Force the response into a sandboxed context even if loaded via
	// top-level navigation. This prevents scripts in previewed HTML from
	// executing in the Relay's origin when the cookie allows access.
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups")
	if err := s.files.ServeStream(w, r, host, filePath); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
}

// handlePreviewAuth sets the preview auth cookie. Called via fetch from the SPA
// (same-origin, carries Relay token in query param), NOT from the iframe src.
// This keeps the token out of the iframe URL (F1) while enabling cookie-based
// auth for relative resource requests.
func (s *Server) handlePreviewAuth(w http.ResponseWriter, r *http.Request) {
	// b231a6ac: Do NOT reflect Origin unconditionally — that creates a
	// reflective CORS with credentials. Same-origin fetch (the normal case)
	// needs no CORS headers. Cross-origin is not supported for preview auth.
	if r.Header.Get("Origin") != "" {
		// Only set CORS for same-origin requests (Origin matches Host).
		// This is a no-op for same-origin but prevents cross-origin reflection.
		origin := r.Header.Get("Origin")
		host := r.Host
		if !sameOriginCheck(origin, host) {
			http.Error(w, "Cross-origin preview auth not supported", http.StatusForbidden)
			return
		}
	}

	if s.cfg.Token == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !auth.Check(r, s.cfg.Token) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// N2: Determine Secure flag from X-Forwarded-Proto so HTTP LAN access works.
	// cloudflared sets X-Forwarded-Proto: https; direct HTTP access won't have it.
	secure := r.Header.Get("X-Forwarded-Proto") == "https" || r.TLS != nil
	// 85a62a85: Restore SameSite=Strict when secure (HTTPS) for F2 defense in
	// depth — blocks cross-site top-level navigation from carrying the cookie.
	// Use Lax only for HTTP LAN where Strict would be overly restrictive and
	// CSP sandbox remains the primary defense.
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteStrictMode
	}
	http.SetCookie(w, &http.Cookie{
		Name:     previewAuthCookieName,
		Value:    s.cfg.Token,
		Path:     "/preview/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
	})
	w.WriteHeader(http.StatusNoContent)
}

// sameOriginCheck verifies that the Origin header matches the request Host.
func sameOriginCheck(origin, host string) bool {
	// Parse origin to extract host portion
	originHost := origin
	if idx := strings.Index(origin, "://"); idx != -1 {
		originHost = origin[idx+3:]
	}
	// Strip path from origin host
	if idx := strings.Index(originHost, "/"); idx != -1 {
		originHost = originHost[:idx]
	}
	return originHost == host
}

func (s *Server) checkPreviewAuth(r *http.Request) bool {
	if s.cfg.Token == "" {
		return true
	}
	// Only accept cookie auth for /preview/ — NOT query param token.
	// This prevents token from appearing in iframe URLs (F1).
	cookie, err := r.Cookie(previewAuthCookieName)
	return err == nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(s.cfg.Token)) == 1
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	// When serving embedded SPA, allow same-origin WS without token.
	// The page itself was served by this server, so the user already has access.
	if s.staticFS != nil && auth.Check(r, s.cfg.Token) {
		// token matches (or token is empty) — proceed
	} else if s.staticFS != nil && isSameOrigin(r) {
		// same-origin request to embedded SPA — skip token
	} else if !auth.Check(r, s.cfg.Token) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Origin policy for the upgrade itself. coder/websocket enforces a
	// same-origin check inside Accept, independently of the token gate above,
	// so cross-origin clients are rejected there unless we widen it here.
	//
	// Two ways to be authorized cross-origin:
	//
	//   - A real token was configured AND presented. The token is then the
	//     CSRF defense (an attacker's page cannot guess it), so the origin
	//     check is redundant and we skip it. This is what makes the relay
	//     switcher and a separate Pages deployment work.
	//   - The origin is on the configured HERDR_ALLOWED_ORIGINS allowlist,
	//     for token-less trusted setups (e.g. a LAN dev UI).
	//
	// With neither, the default same-origin check stands. Note this must test
	// cfg.Token != "" explicitly: auth.Check returns true for *everyone* when
	// no token is configured, so keying off it alone would drop the origin
	// check on every token-less relay and let any website drive it.
	tokenAuthed := s.cfg.Token != "" && auth.Check(r, s.cfg.Token)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: tokenAuthed,
		OriginPatterns:     s.cfg.Origins(),
	})
	if err != nil {
		s.logger.Error("ws accept error", "err", err)
		return
	}
	defer conn.Close(websocket.StatusInternalError, "")

	ctx := r.Context()

	// Send hello
	hello := protocol.Hello{
		Type:          "hello",
		V:             protocol.Version,
		ServerVersion: "herdrem-m3",
		Features: []string{
			"agents", "terminal", "todo", "blocked", "multi-host",
			"files", "prefs", "kind-override", "mark-read", "http-file", "tunnels",
			"pane-create", "incremental-terminal",
		},
	}
	if data, err := json.Marshal(hello); err == nil {
		_ = conn.Write(ctx, websocket.MessageText, data)
	}

	// Send initial todo + prefs + agents snapshots
	s.sendTodoSnapshot(ctx, conn)
	if s.svc != nil {
		s.svc.SendPrefsSnapshot(ctx, conn)
	}
	s.sendAgentsSnapshot(ctx, conn)

	// Handle connection lifecycle with command dispatch
	var onMsg hub.MessageHandler
	if s.svc != nil {
		onMsg = s.svc.HandleMessage
	}
	s.hub.HandleConn(ctx, conn, s.cfg.Token, onMsg)
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !auth.Check(r, s.cfg.Token) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Limit body size to prevent unbounded memory allocation
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB
	var msg json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	// Only allow agent_event type — prevent forging server-authoritative state
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(msg, &probe); err != nil || probe.Type != "agent_event" {
		http.Error(w, `{"error":"only agent_event allowed"}`, http.StatusForbidden)
		return
	}
	// Broadcast to all WS clients
	s.hub.Broadcast(msg)
	w.Write([]byte(`{"ok":true}`))
}

func (s *Server) sendTodoSnapshot(ctx context.Context, conn *websocket.Conn) {
	if s.todoStore == nil {
		return
	}
	snap := protocol.TodosSnapshot{
		Type:  "todos",
		Todos: s.todoStore.All(),
	}
	if data, err := json.Marshal(snap); err == nil {
		_ = conn.Write(ctx, websocket.MessageText, data)
	}
}

func (s *Server) sendAgentsSnapshot(ctx context.Context, conn *websocket.Conn) {
	if s.poller == nil {
		return
	}
	panes := s.poller.Snapshot()
	snap := protocol.AgentsSnapshot{
		Type:   "agents",
		Agents: toAny(panes),
	}
	if data, err := json.Marshal(snap); err == nil {
		_ = conn.Write(ctx, websocket.MessageText, data)
	}
}

// toAny converts a pane slice to []interface{} for JSON marshaling.
func toAny(panes []herdr.Pane) []interface{} {
	out := make([]interface{}, len(panes))
	for i, p := range panes {
		out[i] = p
	}
	return out
}

// isSameOrigin checks if the request's Origin header matches the Host
// (i.e., the SPA loaded from this server is connecting back to itself).
func isSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false // non-browser clients must use token auth
	}
	// Parse origin host and compare with request host
	// Origin is like "http://localhost:8766" or "https://example.com"
	host := r.Host
	// Strip scheme from origin
	originHost := origin
	if i := strings.Index(origin, "://"); i >= 0 {
		originHost = origin[i+3:]
	}
	return originHost == host
}
