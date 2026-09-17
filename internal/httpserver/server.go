package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
const previewCapabilityTTL = 10 * time.Minute

// spaCSP is the Content-Security-Policy for the Vue SPA and JSON APIs.
// Preview responses set their own sandbox CSP and must not inherit this.
const spaCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com data:; img-src 'self' data: blob: https:; connect-src 'self' ws: wss: http: https:; frame-src 'self' https:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'self'"

// sessionTTL bounds a minted session's lifetime. The cookie carries
// Max-Age=sessionTTL so it survives PWA container restarts — session-scope
// cookies are dropped by some Android WebAPK/standalone containers when the
// window closes, which re-prompts for the token on every cold start. The
// server-side TTL still bounds an idle session even if a client holds onto it.
const sessionTTL = 24 * time.Hour

// maxSessions/maxPreviewCaps bound the server-side credential maps so an
// authenticated client cannot grow them without limit.
const (
	maxSessions    = 4096
	maxPreviewCaps = 1024
)

type previewCapability struct {
	host      string
	ip        string // minter's client IP — the cap must be used from it
	expiresAt time.Time
}

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
	previewMu   sync.Mutex
	previewCaps map[string]previewCapability
	sessionMu   sync.Mutex
	sessions    map[string]time.Time
}

// paneSnapshotter returns the latest pane list for initial WS snapshots.
// Client connects trigger a poll via the hub's on-connect hook instead of
// here — TriggerPoll fired before hub registration would observe Count()==0.
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
		previewCaps: make(map[string]previewCapability),
		sessions:    make(map[string]time.Time),
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
	mux.HandleFunc("/session-auth", s.handleSessionAuth)
	mux.HandleFunc("/session-logout", s.handleSessionLogout)
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
		if !strings.HasPrefix(r.URL.Path, "/preview/") {
			w.Header().Set("Content-Security-Policy", spaCSP)
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
// GET /file?path=<path>&host=<host> (session cookie, Bearer, or WS subprotocol).
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	if s.files == nil {
		http.Error(w, "files not available", http.StatusServiceUnavailable)
		return
	}
	download := r.URL.Query().Get("download") == "1" || r.URL.Query().Get("download") == "true"
	if !s.authGate(w, r, func() bool {
		return s.relayAuthOK(r) || (download && s.fileFormAuthOK(w, r))
	}) {
		return
	}
	path := r.URL.Query().Get("path")
	host := r.URL.Query().Get("host")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	// Workspace files are mutable: revalidate a reopened image instead of
	// showing the browser's cached copy for up to five minutes.
	w.Header().Set("Cache-Control", "private, no-cache")
	// A workspace file must never render as an active document on the relay
	// origin: the SPA's script-src 'self' would otherwise let a workspace .js
	// load as a first-party <script src="/file?...">. The sandbox CSP covers
	// top-level navigation; flattening non-inline MIME types to text/plain
	// (with nosniff) blocks script/style subresource abuse. Downloads keep
	// the real type so saved files are unaffected.
	w.Header().Set("Content-Security-Policy", "sandbox")
	if !download && !inlineFileMIME(path) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	if download {
		name := filepath.Base(filepath.ToSlash(path))
		w.Header().Set("Content-Disposition", contentDisposition(name))
	}
	if err := s.files.ServeStream(w, r, host, path); err != nil {
		w.Header().Del("Content-Disposition")
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
}

func (s *Server) fileFormAuthOK(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	// Only native URL-encoded forms are needed. Reject multipart before parsing
	// so an unauthenticated upload cannot be spooled during authentication.
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		return false
	}
	token := r.PostForm.Get("token")
	return s.cfg.Token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) == 1
}

// inlineFileMIME reports whether path's extension maps to a MIME type that is
// safe to serve inline on the relay origin — images, audio, video, fonts,
// PDFs and plain text are not scriptable. Everything else (HTML, JS, CSS,
// XML, unknown extensions) is flattened to text/plain by the caller so a
// workspace file cannot become a first-party document or <script> source.
// SVG stays allowed: <img>-loaded SVG cannot script, and top-level
// navigation is covered by the response's sandbox CSP.
func inlineFileMIME(path string) bool {
	mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mt == "" {
		return false
	}
	base, _, _ := strings.Cut(mt, ";")
	switch base = strings.TrimSpace(base); {
	case base == "text/plain", base == "application/pdf":
		return true
	case strings.HasPrefix(base, "image/"), strings.HasPrefix(base, "video/"),
		strings.HasPrefix(base, "audio/"), strings.HasPrefix(base, "font/"):
		return true
	}
	return false
}

func contentDisposition(name string) string {
	fallback := strings.Map(func(r rune) rune {
		if r >= 0x20 && r <= 0x7e && r != '"' && r != '\\' {
			return r
		}
		return '_'
	}, name)
	if fallback == "" {
		fallback = "download"
	}
	return `attachment; filename="` + fallback + `"; filename*=UTF-8''` + encodeRFC5987(name)
}

func encodeRFC5987(s string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		b := s[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') || strings.ContainsRune("!#$&+-.^_`|~", rune(b)) {
			out.WriteByte(b)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[b>>4])
		out.WriteByte(hex[b&0x0f])
	}
	return out.String()
}

// handleFilePreview serves files via a path-based URL (/preview/<cap>/<path>) so that
// relative URLs in HTML documents resolve against the file's directory rather
// than the /file endpoint. This enables HTML previews with linked CSS/JS/images.
// The short-lived capability keeps the relay token out of the sandboxed iframe.
func (s *Server) handleFilePreview(w http.ResponseWriter, r *http.Request) {
	if s.files == nil {
		http.Error(w, "files not available", http.StatusServiceUnavailable)
		return
	}
	// The capability is in the path because an opaque-origin sandbox cannot send
	// SameSite cookies with relative subresource requests. It is short-lived and
	// host-bound, so leaking it cannot expose the relay token or another host.
	path := strings.TrimPrefix(r.URL.Path, "/preview/")
	parts := strings.SplitN(path, "/", 3)
	if s.cfg.Token == "" {
		parts = strings.SplitN(path, "/", 2)
	}
	if (s.cfg.Token != "" && len(parts) != 3) || (s.cfg.Token == "" && len(parts) != 2) {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	capability := ""
	hostPart := parts[0]
	filePart := parts[1]
	if s.cfg.Token != "" {
		capability, hostPart, filePart = parts[0], parts[1], parts[2]
	}
	hostBytes, err := base64.RawURLEncoding.DecodeString(hostPart)
	if err != nil || len(hostBytes) == 0 {
		http.Error(w, "invalid host", http.StatusBadRequest)
		return
	}
	host := string(hostBytes)
	if s.cfg.Token != "" && !s.validPreviewCapability(capability, host, realClientIP(r)) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	filePath := "/" + strings.TrimPrefix(filePart, "/")

	// Force the response into a sandboxed context even if loaded via
	// top-level navigation. The preview is fully static — bare `sandbox`
	// grants no capabilities, so no script may run. That is what makes the
	// capability in the URL safe: any credential a script could read
	// (location.href, document.baseURI, element.src) is exfiltratable via
	// frame self-navigation, which no CSP directive covers, so the only
	// complete fix is removing script execution. Static markup cannot read
	// its own URL and cannot embed a per-view minted capability it does
	// not know. The deny-by-default CSP stays as defense in depth:
	// 'self' never matches inside an opaque-origin document, so the
	// request's own scheme://host is emitted explicitly to keep relative
	// subresources (CSS/images/fonts) loading under this origin + data:.
	proto := "http"
	if r.TLS != nil || (isLoopback(r.RemoteAddr) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")) {
		proto = "https"
	}
	self := proto + "://" + r.Host
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; "+
		"style-src 'unsafe-inline' "+self+"; "+
		"img-src "+self+" data:; media-src "+self+" data:; font-src "+self+" data:; "+
		"connect-src 'none'; form-action 'none'; base-uri 'none'")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if err := s.files.ServeStream(w, r, host, filePath); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
}

// handlePreviewAuth mints a short-lived, host-bound capability. Called via
// fetch from the SPA; non-local relays carry the Relay token, NOT the iframe
// src. The capability is safe to put in the iframe path: it grants only
// preview reads and never authenticates relay APIs.
func (s *Server) handlePreviewAuth(w http.ResponseWriter, r *http.Request) {
	// Do NOT reflect Origin unconditionally — that creates a
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

	if !s.relayAuthGate(w, r) {
		return
	}
	if s.cfg.Token == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	host := r.URL.Query().Get("host")
	if host == "" {
		host = "local"
	}
	capability, err := s.issuePreviewCapability(host, realClientIP(r))
	if err != nil {
		http.Error(w, "preview capability unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"capability": capability})
}

// handleSessionAuth sets the HttpOnly relay session cookie. Same-origin SPA
// fetch only — cross-origin is refused so the relay token cannot leak via CORS.
func (s *Server) handleSessionAuth(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		origin := r.Header.Get("Origin")
		if !sameOriginCheck(origin, r.Host) {
			http.Error(w, "Cross-origin session auth not supported", http.StatusForbidden)
			return
		}
	}
	if !s.relayAuthGate(w, r) {
		return
	}
	if s.cfg.Token == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// X-Forwarded-Proto is only meaningful from a trusted local proxy —
	// a remote client must not be able to force the Secure attribute on
	// (self-DoS) or influence the SameSite tier.
	secure := r.TLS != nil || (isLoopback(r.RemoteAddr) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteStrictMode
	}
	// The cookie carries an opaque, revocable session id — never the raw
	// relay token — so a leaked or retired session cannot be replayed as
	// the long-term credential and can be killed via /session-logout.
	sessionID, err := s.issueSession()
	if err != nil {
		http.Error(w, "session unavailable", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionLogout revokes the presented session and clears the cookie.
// Same-origin only — a cross-site POST must not be able to kill the user's
// session.
func (s *Server) handleSessionLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isSameOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if c, err := r.Cookie(auth.SessionCookieName); err == nil && c.Value != "" {
		s.sessionMu.Lock()
		delete(s.sessions, c.Value)
		s.sessionMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
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

func (s *Server) issuePreviewCapability(host, ip string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	now := time.Now()
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	if s.previewCaps == nil {
		s.previewCaps = make(map[string]previewCapability)
	}
	// ponytail: bounded expiry sweep keeps this small without a cleanup goroutine.
	for key, capability := range s.previewCaps {
		if !capability.expiresAt.After(now) {
			delete(s.previewCaps, key)
		}
	}
	// Hard bound: minting is authenticated, but the map must not grow
	// without limit. Evict the soonest-expiring entry when full.
	if len(s.previewCaps) >= maxPreviewCaps {
		oldestKey, oldestAt, first := "", time.Time{}, true
		for key, capability := range s.previewCaps {
			if first || capability.expiresAt.Before(oldestAt) {
				oldestKey, oldestAt, first = key, capability.expiresAt, false
			}
		}
		delete(s.previewCaps, oldestKey)
	}
	s.previewCaps[token] = previewCapability{host: host, ip: ip, expiresAt: now.Add(previewCapabilityTTL)}
	return token, nil
}

// sameClientIP reports whether two client IPs refer to the same client.
// Loopback representations (127.0.0.1 vs ::1) are interchangeable — the
// local operator is already trusted; the binding exists to stop a remote
// party replaying a leaked capability.
func sameClientIP(a, b string) bool {
	if a == b {
		return true
	}
	pa, pb := net.ParseIP(a), net.ParseIP(b)
	return pa != nil && pb != nil && pa.IsLoopback() && pb.IsLoopback()
}

func (s *Server) validPreviewCapability(token, host, ip string) bool {
	now := time.Now()
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	capability, ok := s.previewCaps[token]
	if !ok || !capability.expiresAt.After(now) || capability.host != host || !sameClientIP(capability.ip, ip) {
		if ok && !capability.expiresAt.After(now) {
			delete(s.previewCaps, token)
		}
		return false
	}
	return true
}

// issueSession mints an opaque session id for the session cookie. Sessions
// stand in for the relay token: they are revocable via /session-logout and
// expire after sessionTTL of inactivity.
func (s *Server) issueSession() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	now := time.Now()
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]time.Time)
	}
	for k, exp := range s.sessions {
		if !exp.After(now) {
			delete(s.sessions, k)
		}
	}
	if len(s.sessions) >= maxSessions {
		oldestKey, oldestAt, first := "", time.Time{}, true
		for k, exp := range s.sessions {
			if first || exp.Before(oldestAt) {
				oldestKey, oldestAt, first = k, exp, false
			}
		}
		delete(s.sessions, oldestKey)
	}
	s.sessions[id] = now.Add(sessionTTL)
	return id, nil
}

// validSession reports whether the request carries a live session cookie.
// A successful check slides the expiry forward so an active browser session
// never hits the TTL mid-work.
func (s *Server) validSession(r *http.Request) bool {
	c, err := r.Cookie(auth.SessionCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	now := time.Now()
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	exp, ok := s.sessions[c.Value]
	if !ok {
		return false
	}
	if !exp.After(now) {
		delete(s.sessions, c.Value)
		return false
	}
	s.sessions[c.Value] = now.Add(sessionTTL)
	return true
}

// sessionSlideInterval paces session renewal for open WS connections —
// comfortably below sessionTTL so an active session never lapses.
const sessionSlideInterval = time.Hour

// slideSession renews a session's expiry while the connection that
// authenticated with it stays open. An already-expired session is not
// resurrected.
func (s *Server) slideSession(ctx context.Context, id string) {
	t := time.NewTicker(sessionSlideInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.sessionMu.Lock()
		if exp, ok := s.sessions[id]; ok && exp.After(time.Now()) {
			s.sessions[id] = time.Now().Add(sessionTTL)
		}
		s.sessionMu.Unlock()
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.relayAuthGate(w, r) {
		return
	}
	// Slide the session that authenticated this connection for as long as
	// it stays open — WS traffic does not re-authenticate, so without this
	// the idle TTL would expire under an active client. The slide dies with
	// the connection, so a genuinely idle client still expires on schedule.
	if c, err := r.Cookie(auth.SessionCookieName); err == nil && c.Value != "" {
		go s.slideSession(r.Context(), c.Value)
	}
	// Origin policy for the upgrade itself. coder/websocket enforces a
	// same-origin check inside Accept, independently of the token gate above.
	//
	// A valid configured token authorizes cross-origin access, so the origin
	// check is redundant and can be skipped for the relay switcher/tunnels.
	tokenAuthed := s.cfg.Token != "" && auth.Check(r, s.cfg.Token)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: tokenAuthed,
		// The browser carries the token as a Sec-WebSocket-Protocol value;
		// a WHATWG-conformant client fails the handshake when an offered
		// subprotocol is not echoed in the 101, even though auth already
		// passed above. Echo back whichever offered value authenticated.
		Subprotocols: auth.MatchSubprotocols(r, s.cfg.Token),
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
			"pane-create", "incremental-terminal", "git-diff",
		},
	}
	if s.tunnels != nil && s.tunnels.DirectAllowed() {
		hello.Features = append(hello.Features, "tunnel-direct")
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
	if !s.relayAuthGate(w, r) {
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

// relayAuthOK permits only the explicitly local browser exception without a
// token, and only when no token is configured at all (dev mode). With
// HERDREM_TOKEN set — the documented contract — every request needs a
// credential: the raw token (query / Sec-WebSocket-Protocol / legacy cookie
// value) or a live minted session cookie.
func (s *Server) relayAuthOK(r *http.Request) bool {
	if s.cfg.Token == "" {
		return isLocalSameOrigin(r)
	}
	if auth.Check(r, s.cfg.Token) {
		return true
	}
	return s.validSession(r)
}

// authGate wraps an auth predicate with the shared failure rate limiter so a
// remote client brute-forcing the relay token on any authenticated endpoint
// is throttled after repeated failures. Loopback clients are exempt — a
// local user must never lock themselves out of their own relay (e.g. a stale
// token triggering a WS reconnect loop).
func (s *Server) authGate(w http.ResponseWriter, r *http.Request, ok func() bool) bool {
	ip := realClientIP(r)
	tracked := ip != "" && !isLoopbackIP(ip)
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	if tracked && s.gateLimiter != nil && !s.gateLimiter.allow(ip) {
		logger.Info("[DEBUG-a4f2] auth gate: rate-limited", "path", r.URL.Path, "ip", ip)
		http.Error(w, "too many authentication failures", http.StatusTooManyRequests)
		return false
	}
	if ok() {
		if tracked && s.gateLimiter != nil {
			s.gateLimiter.recordSuccess(ip)
		}
		return true
	}
	if tracked && s.gateLimiter != nil {
		s.gateLimiter.recordFailure(ip)
	}
	_, hasCookie := r.Cookie(auth.SessionCookieName)
	logger.Info("[DEBUG-a4f2] auth gate: rejected",
		"path", r.URL.Path, "ip", ip,
		"bearer", strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "),
		"cookie", hasCookie,
		"subprotocols", len(r.Header.Values("Sec-WebSocket-Protocol")),
		"origin", r.Header.Get("Origin"),
		"ua", r.Header.Get("User-Agent"),
	)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
	return false
}

// relayAuthGate is authGate bound to the standard relay credential check.
func (s *Server) relayAuthGate(w http.ResponseWriter, r *http.Request) bool {
	return s.authGate(w, r, func() bool { return s.relayAuthOK(r) })
}

// isLoopbackIP reports whether a bare IP string is loopback.
func isLoopbackIP(ip string) bool {
	p := net.ParseIP(strings.TrimSpace(ip))
	return p != nil && p.IsLoopback()
}

// isSameOrigin checks if the request's Origin header matches the Host.
func isSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Resource requests such as <img> may omit Origin; Fetch Metadata still
		// identifies a browser same-origin request without trusting Host alone.
		return r.Header.Get("Sec-Fetch-Site") == "same-origin"
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

// isLocalSameOrigin is the only token-free browser path: the request must be
// same-origin, arrive from a loopback peer, and be addressed to localhost,
// exactly 127.0.0.1, or ::1. The peer check prevents a remote client from
// spoofing the Host and Origin headers to look local.
func isLocalSameOrigin(r *http.Request) bool {
	if !isLoopback(r.RemoteAddr) || !isSameOrigin(r) {
		return false
	}
	host := r.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	} else {
		host = strings.Trim(host, "[]")
	}
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}
