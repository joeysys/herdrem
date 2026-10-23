package httpserver

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
// window closes, which re-prompts for the token on every cold start.
// Seven idle days covers a phone that opens the installed app once a day
// and is then frozen or killed; the SPA re-mints on connect (and every 30
// minutes while the socket stays up) so an active device rolls this window
// forward. A client that stays idle still expires on this bound.
const sessionTTL = 7 * 24 * time.Hour

// maxSessions/maxPreviewCaps bound the server-side credential maps so an
// authenticated client cannot grow them without limit.
const (
	maxSessions    = 4096
	maxPreviewCaps = 1024
)

// Rejection logging is throttled per client IP: the first rejectLogBurst
// rejections inside a rejectLogWindow are logged, further ones are counted
// and reported once (as "suppressed") on the first rejection of the next
// window. Without it every rejected request writes one Info line, so a
// reconnect loop or a hostile spray would turn the log into an amplification
// channel — the only per-request resource a 401 actually grows.
const (
	rejectLogBurst  = 3
	rejectLogWindow = time.Minute
	maxRejectLogIPs = 4096
)

// rejectLogThrottle dedups authGate rejection logs per client IP.
type rejectLogThrottle struct {
	mu  sync.Mutex
	ips map[string]rejectLogState
}

type rejectLogState struct {
	logged     int
	suppressed int
	windowEnd  time.Time
}

// allow reports whether this rejection should be logged now, and how many
// earlier rejections were suppressed since the last logged line.
func (t *rejectLogThrottle) allow(ip string, now time.Time) (bool, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ips == nil {
		t.ips = make(map[string]rejectLogState)
	}
	st, ok := t.ips[ip]
	if !ok && len(t.ips) >= maxRejectLogIPs {
		for k, v := range t.ips {
			if now.After(v.windowEnd) {
				delete(t.ips, k)
			}
		}
		if len(t.ips) >= maxRejectLogIPs {
			t.ips = make(map[string]rejectLogState, maxRejectLogIPs)
		}
	}
	if !ok || now.After(st.windowEnd) {
		t.ips[ip] = rejectLogState{logged: 1, windowEnd: now.Add(rejectLogWindow)}
		return true, st.suppressed
	}
	st.logged++
	if st.logged > rejectLogBurst {
		st.suppressed++
		t.ips[ip] = st
		return false, 0
	}
	t.ips[ip] = st
	return true, 0
}

type previewCapability struct {
	host      string
	ip        string // minter's client IP — the cap must be used from it
	dir       string // directory scope — the cap only reads files under it
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
	// revoked is the denylist of session ids logged out before their signed
	// expiry. Sessions of one family share a sid (renewals re-sign it), so
	// one deny entry kills every copy of that session. The list persists to
	// revokedFile so a restart cannot forgive a revocation while the signed
	// cookies are still valid; live sessions survive either way.
	revoked     map[string]time.Time
	revokedFile string
	// sessionKey is the per-process HMAC fallback used when no relay token is
	// configured (tokenless relays don't need restart-proof sessions — local
	// same-origin requests authenticate without one). With a token set, the
	// signing key derives from it instead, so sessions survive restarts.
	sessionKey []byte
	rejectLog  rejectLogThrottle
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
	var sessionKey [32]byte
	_, _ = rand.Read(sessionKey[:])
	s := &Server{
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
		revoked:     make(map[string]time.Time),
		sessionKey:  sessionKey[:],
	}
	if cfg.DataDir != "" {
		s.revokedFile = filepath.Join(cfg.DataDir, "revoked_sessions.json")
		s.loadRevoked()
	}
	return s
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
	mux.HandleFunc("/upload", s.handleUpload)
	mux.HandleFunc("/preview/", s.handleFilePreview)
	mux.HandleFunc("/preview-open/", s.handleFilePreview)
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
	if err := s.files.ServeStream(w, r, host, path, ""); err != nil {
		w.Header().Del("Content-Disposition")
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
}

// uploadHeader must accompany every upload. A custom header cannot be set by
// a cross-site form or simple request, so a request carrying it was made by
// script — on this origin, or on another origin that passed the CORS preflight.
const uploadHeader = "X-Herdrem-Upload"

// bearerOK reports whether the request carries the relay token in the
// Authorization header. Unlike auth.Check it ignores cookies: a cookie is sent
// by the browser on its own (ambient), so it can never authorize a
// cross-origin request; a header has to be set by code that knows the token.
func (s *Server) bearerOK(r *http.Request) bool {
	a := r.Header.Get("Authorization")
	if s.cfg.Token == "" || !strings.HasPrefix(a, "Bearer ") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(a, "Bearer ")), []byte(s.cfg.Token)) == 1
}

// uploadCORS lets an app served from another origin (a Pages/web-only
// deployment, a relay switcher) upload with the relay token. Same policy as
// the WebSocket, where a valid token authorizes any origin: the token is the
// CSRF defense. Credentials (cookies) are never allowed cross-origin, so a
// site that does not know the token gets nothing, and a token-less relay
// (local dev, loopback same-origin only) does not take cross-origin uploads.
func (s *Server) uploadCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || s.cfg.Token == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Add("Vary", "Origin")
}

// handleUpload stores one file in a workspace directory.
// POST /upload?host=<host>&dir=<abs dir>&name=<file name>&size=<bytes>[&overwrite=1]
// with the raw file bytes as the body. The token is never accepted from a
// form field or the URL.
//
//   - same origin: session cookie or Bearer, like every other relay endpoint;
//   - any other origin (or no Origin, e.g. curl): Bearer token only.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	sameOrigin := isSameOrigin(r)
	if !sameOrigin {
		s.uploadCORS(w, r)
	}
	if r.Method == http.MethodOptions {
		// CORS preflight. It carries no credentials, so it cannot be
		// authenticated; it only tells the browser the real request may carry
		// our headers. That request is then authenticated on its own.
		if sameOrigin || s.cfg.Token == "" || r.Header.Get("Origin") == "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, "+uploadHeader)
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.files == nil {
		http.Error(w, "files not available", http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get(uploadHeader) != "1" {
		http.Error(w, "missing "+uploadHeader+" header", http.StatusForbidden)
		return
	}
	if sameOrigin {
		if !s.relayAuthGate(w, r) {
			return
		}
	} else {
		if s.cfg.Token == "" {
			http.Error(w, "cross-origin upload needs a relay token (HERDREM_TOKEN)", http.StatusForbidden)
			return
		}
		if !s.authGate(w, r, func() bool { return s.bearerOK(r) }) {
			return
		}
	}
	q := r.URL.Query()
	size, err := strconv.ParseInt(q.Get("size"), 10, 64)
	if err != nil || size < 0 {
		http.Error(w, "invalid size", http.StatusBadRequest)
		return
	}
	if size > service.MaxUploadBytes {
		http.Error(w, service.ErrUploadTooLarge.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if r.ContentLength >= 0 && r.ContentLength != size {
		http.Error(w, "body length does not match size", http.StatusBadRequest)
		return
	}
	// Read at most one byte more than announced: enough to detect a lie.
	r.Body = http.MaxBytesReader(w, r.Body, size+1)
	overwrite := q.Get("overwrite") == "1" || q.Get("overwrite") == "true"
	res, err := s.files.Upload(r.Context(), service.UploadRequest{
		Host:      q.Get("host"),
		Dir:       q.Get("dir"),
		Name:      q.Get("name"),
		Size:      size,
		Overwrite: overwrite,
	}, r.Body)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, service.ErrUploadInvalid):
			status = http.StatusBadRequest
		case errors.Is(err, service.ErrUploadForbidden):
			status = http.StatusForbidden
		case errors.Is(err, service.ErrUploadNoDir):
			status = http.StatusNotFound
		case errors.Is(err, service.ErrUploadExists):
			status = http.StatusConflict
		case errors.Is(err, service.ErrUploadTooLarge):
			status = http.StatusRequestEntityTooLarge
		case errors.Is(err, service.ErrUploadIncomplete):
			status = http.StatusBadRequest
		}
		if status == http.StatusInternalServerError {
			s.logger.Warn("upload failed", "host", q.Get("host"), "dir", q.Get("dir"), "err", err)
			http.Error(w, "upload failed", status)
			return
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(res)
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
	//
	// Open mode is also carried in the path prefix (/preview-open/) rather than
	// a query parameter: in-document relative links and iframe navigations
	// resolve against the path, so a query flag would be silently dropped on
	// the first click inside a multi-page site and the page would fall back to
	// the static sandbox. A path prefix is preserved by relative resolution.
	openPath := strings.HasPrefix(r.URL.Path, "/preview-open/")
	path := strings.TrimPrefix(r.URL.Path, "/preview/")
	if openPath {
		path = strings.TrimPrefix(r.URL.Path, "/preview-open/")
	}
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
	filePath := "/" + strings.TrimPrefix(filePart, "/")
	scopeDir := ""
	if s.cfg.Token != "" {
		dir, ok := s.previewScopeDir(capability, host, realClientIP(r), filePath)
		if !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		scopeDir = dir
	}

	// Opt-in script execution requires ?run=1 AND a directory-scoped
	// capability. The opaque origin is enforced by the response CSP itself —
	// `sandbox allow-scripts` applies to any browsing context, iframe or
	// top-level — so a new tab running JS is no more privileged than the
	// inline run frame, and no Sec-Fetch-Dest sniffing is needed. A running
	// script can read its own URL and therefore the capability, which is why
	// the capability is scoped to the file's directory (see
	// handlePreviewAuth) and that scope is re-checked against the opened
	// file in ServeStream.
	//
	// `sandbox allow-scripts` lets the document execute scripts but keeps it
	// in an opaque origin — the response enforces the boundary, so an
	// embedder that drops (or widens) the iframe's sandbox attribute cannot
	// turn this into relay-origin execution. No allow-same-origin, ever.
	//
	// Default loads get a fully static, sandboxed response: bare `sandbox`
	// grants no capabilities, so no script may run and static markup cannot
	// read its own URL — defense in depth even when the iframe sandbox
	// attribute already blocks execution. The deny-by-default CSP:
	//
	// 'self' never matches inside an opaque-origin document, so the
	// request's own scheme://host is emitted explicitly to keep relative
	// subresources (CSS/images/fonts) loading under this origin + data:.
	//
	// Run mode narrows all subresource channels: script/style/img/media/font
	// and fetch/XHR/WebSocket only reach this relay origin (sibling files in
	// the scoped dir — cross-origin, so no credentials ride along) or data:.
	// default-src 'none' closes the fallback for unlisted directives
	// (manifest-src, prefetch-src, worker-src, ...), so no fetch-like
	// channel can send off-origin at all. What remains unpreventable by CSP
	// is top-level document navigation. What bounds a leaked run capability
	// is the capability itself: scoped to the file's directory, dotfiles
	// refused, bound to the minter's IP, 10-minute TTL.
	run := r.URL.Query().Get("run") == "1" && (s.cfg.Token == "" || scopeDir != "")
	// /preview-open/ is the "open in a new tab with scripts & remote resources" mode:
	// same scoped capability and opaque origin as run mode, but remote
	// subresource channels (script/style/img/media/font/frame/worker over
	// https:) are allowed so CDN-backed pages (mermaid, echarts, ...) can render
	// in an isolated origin — the in-app sandbox can never satisfy those.
	//
	// Honest cost: this re-opens the silent-beacon channel that run mode
	// deliberately closes — a script can embed its own DOM plus the
	// dir-scoped capability in a remote subresource URL without any visible
	// navigation. What still bounds the blast radius: the document stays an
	// opaque origin (no relay cookies, no relay API reads — CORS never
	// grants this origin), connect-src stays pinned to this origin (no
	// readable fetch channel back, and no ACAO is emitted so sibling file
	// contents stay unreadable), and the capability is dir-scoped, refuses
	// dotfiles, is bound to the minter's IP, and expires in 10 minutes. The
	// accepted residual — silent leak of page DOM + capability, usable only
	// by a same-egress-IP party inside the TTL — is recorded in
	// docs/designs/html-preview-and-tunnel-forwarding.md.
	//
	// Sandbox flags stay minimal on purpose: `allow-scripts` only, same as
	// run mode. Forms, popups, top navigation escapes and downloads are not
	// needed for CDN-backed rendering, so they are not granted.
	//
	// Local files only: remote hosts have no local filesystem to fall back
	// to and keep the sandboxed preview/tunnel path.
	//
	// Authorization boundary: this server does not enforce user consent.
	// Any request carrying a valid run capability receives the executing
	// tier (?run=1 or /preview-open/) — including older clients that minted
	// the capability and navigated to it directly. The consent step
	// ("▶ Run scripts" in the web UI) is enforced client-side only; the
	// server-side bound is the capability itself (dir scope, dotfile
	// refusal, minter-IP binding, 10-minute TTL) plus the CSP above.
	openMode := openPath && (host == "" || host == "local") &&
		(s.cfg.Token == "" || scopeDir != "")
	proto := "http"
	if r.TLS != nil || (isLoopback(r.RemoteAddr) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")) {
		proto = "https"
	}
	self := proto + "://" + r.Host
	if openMode {
		w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; "+
			"script-src 'unsafe-inline' 'unsafe-eval' https: "+self+" data:; "+
			"style-src 'unsafe-inline' https: "+self+" data:; "+
			"img-src https: "+self+" data: blob:; media-src https: "+self+" data: blob:; "+
			"font-src https: "+self+" data:; connect-src "+self+"; "+
			"frame-src https: "+self+"; worker-src https: "+self+" blob:; "+
			"form-action 'none'; object-src 'none'; base-uri 'none'")
	} else if run {
		w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; "+
			"script-src 'unsafe-inline' 'unsafe-eval' "+self+" data:; "+
			"style-src 'unsafe-inline' "+self+" data:; "+
			"img-src "+self+" data:; media-src "+self+" data:; font-src "+self+" data:; "+
			"connect-src "+self+"; frame-src 'none'; object-src 'none'; "+
			"form-action 'none'; base-uri 'none'")
	} else {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; "+
			"style-src 'unsafe-inline' "+self+"; "+
			"img-src "+self+" data:; media-src "+self+" data:; font-src "+self+" data:; "+
			"connect-src 'none'; form-action 'none'; base-uri 'none'")
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if err := s.files.ServeStream(w, r, host, filePath, scopeDir); err != nil {
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
	// Two capabilities are minted: `capability` is legacy host-scoped and
	// drives the default static preview (a sandboxed document cannot run
	// scripts, so it cannot leak the token in its URL — and ../ resources
	// keep working). `run_capability` is scoped to the file's directory —
	// a script running in the opt-in mode can read its own URL and leak it,
	// so only the narrow token may ever reach a script-executing document.
	// dir must be absolute and inside one of the host's roots — dir=/ would
	// otherwise degenerate the scope into every allowed root. A missing dir
	// mints only the legacy capability (older clients); those never qualify
	// for run mode (see handleFilePreview).
	capability, err := s.issuePreviewCapability(host, realClientIP(r), "")
	if err != nil {
		http.Error(w, "preview capability unavailable", http.StatusInternalServerError)
		return
	}
	resp := map[string]string{"capability": capability}
	if raw := r.URL.Query().Get("dir"); raw != "" {
		dir := filepath.Clean(raw)
		if dir == "." || !filepath.IsAbs(dir) {
			http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
			return
		}
		if !s.files.DirWithinRoots(host, dir) {
			http.Error(w, "dir outside allowed roots", http.StatusBadRequest)
			return
		}
		runCapability, err := s.issuePreviewCapability(host, realClientIP(r), dir)
		if err != nil {
			http.Error(w, "preview capability unavailable", http.StatusInternalServerError)
			return
		}
		resp["run_capability"] = runCapability
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
	// A request already holding a live session re-signs the same sid: every
	// slide generation is one session family, so revoking the sid kills all
	// of its copies (older cookies included) at once.
	sid, _ := s.sessionSID(r)
	sessionID, err := s.issueSession(sid)
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
		if sid, exp, ok := s.parseSession(c.Value); ok {
			s.sessionMu.Lock()
			if s.revoked == nil {
				s.revoked = make(map[string]time.Time)
			}
			// Evict expired denies — they can never match a live cookie. If the
			// list is still full, drop the soonest-expiring entry instead of
			// silently skipping: a logout must always land, or the dropped
			// revoke would resurrect a copied cookie.
			now := time.Now()
			for k, e := range s.revoked {
				if !e.After(now) {
					delete(s.revoked, k)
				}
			}
			if len(s.revoked) >= maxSessions {
				oldestKey, oldestAt, first := "", time.Time{}, true
				for k, e := range s.revoked {
					if first || e.Before(oldestAt) {
						oldestKey, oldestAt, first = k, e, false
					}
				}
				delete(s.revoked, oldestKey)
			}
			// The deny needs no longer than the cookie's own signed expiry.
			s.revoked[sid] = time.Unix(exp, 0)
			s.saveRevokedLocked()
			s.sessionMu.Unlock()
		}
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

func (s *Server) issuePreviewCapability(host, ip, dir string) (string, error) {
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
	s.previewCaps[token] = previewCapability{host: host, ip: ip, dir: dir, expiresAt: now.Add(previewCapabilityTTL)}
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

func (s *Server) validPreviewCapability(token, host, ip, filePath string) bool {
	_, ok := s.previewScopeDir(token, host, ip, filePath)
	return ok
}

// previewScopeDir validates the capability and returns its directory scope.
// A valid legacy capability (minted without dir) returns ""; callers must
// treat that as unscoped-for-read but ineligible for run mode.
func (s *Server) previewScopeDir(token, host, ip, filePath string) (string, bool) {
	now := time.Now()
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	capability, ok := s.previewCaps[token]
	if !ok || !capability.expiresAt.After(now) || capability.host != host || !sameClientIP(capability.ip, ip) {
		if ok && !capability.expiresAt.After(now) {
			delete(s.previewCaps, token)
		}
		return "", false
	}
	// Remote paths do not exist on this filesystem, so only the lexical
	// check applies here; remoteResolveScriptScoped is the authoritative
	// gate at serve time. Local paths get full symlink resolution.
	if host == "" || host == "local" {
		if !service.PathWithinDir(capability.dir, filePath) {
			return "", false
		}
	} else if !service.PathWithinDirLexical(capability.dir, filePath) {
		return "", false
	}
	// A scoped capability must never open dotfiles — the run-mode document
	// can leak its capability URL, and same-egress-IP holders must not get
	// .env/.git. Same rule the file tunnel applies.
	if capability.dir != "" && service.HasDotSegment(filePath) {
		return "", false
	}
	return capability.dir, true
}

// sessionCookieVersion marks the signed-cookie format so a future change can
// reject old values instead of mis-parsing them.
const sessionCookieVersion = "v1"

// sessionKeyFor returns the HMAC key signing session cookies. With a
// configured relay token the key derives from it — the token is already the
// root credential, and deriving keeps sessions valid across restarts (and
// dies with the token on rotation). Tokenless relays fall back to the
// per-process key generated in New.
func (s *Server) sessionKeyFor() []byte {
	if s.cfg.Token == "" {
		return s.sessionKey
	}
	sum := sha256.Sum256([]byte("herdrem-session-v1:" + s.cfg.Token))
	return sum[:]
}

// sessionSig computes the HMAC binding a session id to its expiry.
func (s *Server) sessionSig(sid string, exp int64) string {
	mac := hmac.New(sha256.New, s.sessionKeyFor())
	fmt.Fprintf(mac, "%s.%s.%d", sessionCookieVersion, sid, exp)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// issueSession mints a self-authenticating session cookie value:
// v1.<sid>.<expiry>.<hmac>. The server keeps no per-session state — the
// signature is the credential — so sessions survive restarts. Sessions
// stand in for the relay token: they are revocable via /session-logout
// (denylist persisted under DataDir) and expire at the signed sessionTTL
// timestamp. A non-empty sid re-signs that session family (slide renewal);
// an empty sid mints a fresh family.
func (s *Server) issueSession(sid string) (string, error) {
	if sid == "" {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		sid = hex.EncodeToString(raw[:])
	}
	exp := time.Now().Add(sessionTTL).Unix()
	return fmt.Sprintf("%s.%s.%d.%s", sessionCookieVersion, sid, exp, s.sessionSig(sid, exp)), nil
}

// parseSession validates a session cookie value and returns its sid and
// expiry when the signature and expiry check out. A session that verifies is
// authentic even if this process never saw it minted — restart-survival is
// the point.
func (s *Server) parseSession(value string) (string, int64, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 4 || parts[0] != sessionCookieVersion || parts[1] == "" {
		return "", 0, false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || exp <= time.Now().Unix() {
		return "", 0, false
	}
	if subtle.ConstantTimeCompare([]byte(parts[3]), []byte(s.sessionSig(parts[1], exp))) != 1 {
		return "", 0, false
	}
	return parts[1], exp, true
}

// sessionSID returns the sid of the request's live session cookie: valid
// signature, unexpired, and not on the logout denylist.
func (s *Server) sessionSID(r *http.Request) (string, bool) {
	c, err := r.Cookie(auth.SessionCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	sid, _, ok := s.parseSession(c.Value)
	if !ok {
		return "", false
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	_, denied := s.revoked[sid]
	return sid, !denied
}

// validSession reports whether the request carries a live session cookie.
func (s *Server) validSession(r *http.Request) bool {
	_, ok := s.sessionSID(r)
	return ok
}

// loadRevoked restores the persisted logout denylist. Entries whose deny
// window already lapsed are dropped on load — their cookies are invalid
// regardless. Missing/corrupt files just start empty.
func (s *Server) loadRevoked() {
	b, err := os.ReadFile(s.revokedFile)
	if err != nil {
		return
	}
	var raw map[string]int64
	if json.Unmarshal(b, &raw) != nil {
		return
	}
	now := time.Now()
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	for sid, exp := range raw {
		if t := time.Unix(exp, 0); t.After(now) {
			s.revoked[sid] = t
		}
	}
}

// saveRevokedLocked persists the denylist (caller holds sessionMu). The file
// holds only opaque sids and expiry timestamps — no secrets. Best-effort: a
// failed write leaves the in-memory deny standing, matching the pre-persist
// behavior where a restart forgives revocations.
func (s *Server) saveRevokedLocked() {
	if s.revokedFile == "" {
		return
	}
	raw := make(map[string]int64, len(s.revoked))
	for sid, exp := range s.revoked {
		raw[sid] = exp.Unix()
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return
	}
	dir := filepath.Dir(s.revokedFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".tmp-revoked-*.json")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Chmod(tmpPath, 0o600)
	if err := os.Rename(tmpPath, s.revokedFile); err != nil && s.logger != nil {
		s.logger.Warn("persist revoked sessions failed", "err", err)
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.relayAuthGate(w, r) {
		return
	}
	// Session freshness is client-driven: the SPA re-mints via /session-auth
	// while connected, so a stateless signed cookie needs no server-side
	// slide (and a genuinely idle client still expires on schedule).
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
		ServerVersion: "0.1.0",
		Features: []string{
			"agents", "terminal", "todo", "blocked", "multi-host",
			"files", "prefs", "kind-override", "mark-read", "http-file", "tunnels",
			"pane-create", "incremental-terminal", "git-diff", "git-commit", "git-sync", "git-name-status", "git-log-view", "git-range", "send-text-paste",
		},
	}
	if s.tunnels != nil && s.tunnels.DirectAllowed() {
		hello.Features = append(hello.Features, "tunnel-direct")
	}
	if s.files != nil {
		hello.Features = append(hello.Features, "file-upload", "preview-run", "preview-open")
	}
	if s.svc != nil && s.svc.HasHistory() {
		hello.Features = append(hello.Features, "input-history")
	}
	if data, err := json.Marshal(hello); err == nil {
		_ = conn.Write(ctx, websocket.MessageText, data)
	}

	// Send initial todo + prefs + agents snapshots
	s.sendTodoSnapshot(ctx, conn)
	if s.svc != nil {
		s.svc.SendPrefsSnapshot(ctx, conn)
		s.svc.SendHistorySnapshot(ctx, conn)
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

// authGate evaluates an auth predicate for relay endpoints. Per-IP failure
// rate limiting was removed: the relay token is a 128-bit secret that cannot
// be brute-forced online, and the limiter only wedged legitimate clients
// (a PWA's reconnect loop reads as "repeated failures"; a shared egress IP
// then blocks every device behind it). Generic request flooding is left to
// the edge/deployment layer — relay auth never emits 429. The tunnel gate
// keeps its own limiter because its user-chosen PIN is short and public-facing.
func (s *Server) authGate(w http.ResponseWriter, r *http.Request, ok func() bool) bool {
	if ok() {
		return true
	}
	s.logRejection(r)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
	return false
}

// logRejection emits the diagnostic line for a rejected relay auth attempt.
// Volume is throttled per client IP (rejectLogThrottle) so a failing
// reconnect loop or a hostile spray cannot amplify the log; the HTTP
// response is unaffected.
func (s *Server) logRejection(r *http.Request) {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	ip := realClientIP(r)
	write, suppressed := s.rejectLog.allow(ip, time.Now())
	if !write {
		return
	}
	_, hasCookie := r.Cookie(auth.SessionCookieName)
	attrs := []any{
		"path", r.URL.Path,
		"ip", ip,
		"bearer", strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "),
		"cookie", hasCookie,
		"subprotocols", len(r.Header.Values("Sec-WebSocket-Protocol")),
		"origin", r.Header.Get("Origin"),
		"ua", r.Header.Get("User-Agent"),
	}
	if suppressed > 0 {
		attrs = append(attrs, "suppressed", suppressed)
	}
	logger.Info("[DEBUG-a4f2] auth gate: rejected", attrs...)
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
