package httpserver

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"herdrem/internal/config"
	"herdrem/internal/service"
)

func TestPreviewCapabilityBindsHostAndExpires(t *testing.T) {
	s := &Server{previewCaps: make(map[string]previewCapability)}
	capability, err := s.issuePreviewCapability("local", "192.0.2.1", "/srv")
	if err != nil {
		t.Fatal(err)
	}
	if !s.validPreviewCapability(capability, "local", "192.0.2.1", "/srv/a.html") {
		t.Fatal("fresh capability should be valid for its host")
	}
	if s.validPreviewCapability(capability, "remote", "192.0.2.1", "/srv/a.html") {
		t.Fatal("capability should not be valid for another host")
	}

	s.previewMu.Lock()
	s.previewCaps[capability] = previewCapability{host: "local", ip: "192.0.2.1", dir: "/srv", expiresAt: time.Now().Add(-time.Second)}
	s.previewMu.Unlock()
	if s.validPreviewCapability(capability, "local", "192.0.2.1", "/srv/a.html") {
		t.Fatal("expired capability should be rejected")
	}
}

func TestPreviewAuthReturnsCapability(t *testing.T) {
	root := t.TempDir()
	files := service.NewFileService(func(string) []string { return []string{root} }, nil)
	s := &Server{cfg: config.Config{Token: "secret"}, files: files, previewCaps: make(map[string]previewCapability)}
	r := httptest.NewRequest("GET", "http://relay.local/preview-auth?host=local&dir="+root, nil)
	r.Header.Set("Authorization", "Bearer secret")
	r.Host = "relay.local"
	w := httptest.NewRecorder()

	s.handlePreviewAuth(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("Set-Cookie = %q, capability flow should not mint a cookie", got)
	}
	var body struct {
		Capability    string `json:"capability"`
		RunCapability string `json:"run_capability"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Capability == "" || !s.validPreviewCapability(body.Capability, "local", "192.0.2.1", filepath.Join(root, "a.html")) {
		t.Fatal("response did not contain a valid local capability")
	}
	// The dir-scoped run capability must validate siblings and reject
	// outside-dir and dotfile paths.
	if body.RunCapability == "" {
		t.Fatal("dir-scoped run_capability missing")
	}
	if !s.validPreviewCapability(body.RunCapability, "local", "192.0.2.1", filepath.Join(root, "a.html")) {
		t.Fatal("run capability should validate a sibling file")
	}
	if s.validPreviewCapability(body.RunCapability, "local", "192.0.2.1", filepath.Join(root, "..", "x")) {
		t.Fatal("run capability should reject paths outside its dir")
	}
	if s.validPreviewCapability(body.RunCapability, "local", "192.0.2.1", filepath.Join(root, ".env")) {
		t.Fatal("run capability must reject dotfiles")
	}
}

// A dir outside the host's roots (including "/") must not be minted into a
// capability — it would degenerate the scope into every allowed root.
func TestPreviewAuthRejectsDirOutsideRoots(t *testing.T) {
	root := t.TempDir()
	files := service.NewFileService(func(string) []string { return []string{root} }, nil)
	s := &Server{cfg: config.Config{Token: "secret"}, files: files, previewCaps: make(map[string]previewCapability)}

	for _, dir := range []string{"/", filepath.Dir(root), "relative/path"} {
		r := httptest.NewRequest("GET", "http://relay.local/preview-auth?host=local&dir="+dir, nil)
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		s.handlePreviewAuth(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("dir=%q status = %d, want %d", dir, w.Code, http.StatusBadRequest)
		}
	}
}

// A missing dir mints a legacy host-scoped capability: older frontends keep
// working for strict previews, but such a capability is ineligible for run
// mode because its scope is not directory-bound.
func TestPreviewAuthMissingDirMintsLegacyCapability(t *testing.T) {
	root := t.TempDir()
	files := service.NewFileService(func(string) []string { return []string{root} }, nil)
	s := &Server{cfg: config.Config{Token: "secret"}, files: files, previewCaps: make(map[string]previewCapability)}
	r := httptest.NewRequest("GET", "http://relay.local/preview-auth?host=local", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handlePreviewAuth(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var body struct {
		Capability    string `json:"capability"`
		RunCapability string `json:"run_capability"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.RunCapability != "" {
		t.Fatalf("run_capability = %q, want absent without dir", body.RunCapability)
	}
	scope, ok := s.previewScopeDir(body.Capability, "local", "192.0.2.1", filepath.Join(root, "a.html"))
	if !ok {
		t.Fatal("legacy capability should validate")
	}
	if scope != "" {
		t.Fatalf("legacy capability scope = %q, want unscoped", scope)
	}
}

// Remote hosts cannot be resolved on this filesystem, so the capability
// layer applies the lexical dir check; remoteResolveScriptScoped is the
// authoritative gate. A path that happens to exist locally must not change
// the remote verdict.
func TestPreviewScopeDirRemoteUsesLexicalCheck(t *testing.T) {
	s := &Server{previewCaps: make(map[string]previewCapability)}
	capability, err := s.issuePreviewCapability("remote", "192.0.2.1", "/srv/site")
	if err != nil {
		t.Fatal(err)
	}
	scope, ok := s.previewScopeDir(capability, "remote", "192.0.2.1", "/srv/site/a.html")
	if !ok || scope != "/srv/site" {
		t.Fatalf("remote scope = %q ok=%v, want /srv/site", scope, ok)
	}
	if _, ok := s.previewScopeDir(capability, "remote", "192.0.2.1", "/srv/other/a.html"); ok {
		t.Fatal("remote lexical check should reject outside-dir path")
	}
}

func TestHandleFilePreviewUsesCapabilityPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.html")
	if err := os.WriteFile(path, []byte("<link rel=stylesheet href=style.css>"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := service.NewFileService(func(string) []string { return []string{dir} }, nil)
	s := &Server{
		cfg:         config.Config{Token: "secret"},
		files:       files,
		previewCaps: make(map[string]previewCapability),
	}
	capability, err := s.issuePreviewCapability("local", "192.0.2.1", dir)
	if err != nil {
		t.Fatal(err)
	}
	host := base64.RawURLEncoding.EncodeToString([]byte("local"))
	previewPath := strings.TrimPrefix(path, "/")
	r := httptest.NewRequest("GET", "/preview/"+capability+"/"+host+"/"+previewPath, nil)
	w := httptest.NewRecorder()
	s.handleFilePreview(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body, err := io.ReadAll(w.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "style.css") {
		t.Fatalf("body = %q, want preview content", body)
	}
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"sandbox;",
		"default-src 'none'",
		"connect-src 'none'",
		"form-action 'none'",
		"base-uri 'none'",
		// 'self' never matches inside an opaque-origin sandbox, so the
		// handler emits the request's own scheme://host explicitly.
		"img-src http://example.com data:",
	} {
		if !strings.Contains(csp, want) {
			t.Fatalf("Content-Security-Policy = %q, missing %q", csp, want)
		}
	}
	// The preview must be fully static: any sandbox escape token
	// (allow-scripts/allow-forms/allow-popups) or script-src re-opens a
	// channel that can read the capability URL and ride it out via frame
	// self-navigation, images, forms, or popups.
	for _, bad := range []string{"allow-scripts", "allow-forms", "allow-popups", "script-src"} {
		if strings.Contains(csp, bad) {
			t.Fatalf("Content-Security-Policy = %q, still contains %q", csp, bad)
		}
	}
	if strings.Contains(csp, "img-src http:") && !strings.Contains(csp, "img-src http://example.com") {
		t.Fatalf("Content-Security-Policy = %q, img-src not pinned to self origin", csp)
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private, no-store", got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}

	r = httptest.NewRequest("GET", "/preview/"+host+"/"+previewPath, nil)
	w = httptest.NewRecorder()
	s.handleFilePreview(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing capability status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestPreviewCapabilityScopedToDirectory(t *testing.T) {
	s := &Server{previewCaps: make(map[string]previewCapability)}
	capability, err := s.issuePreviewCapability("local", "192.0.2.1", "/srv/site")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"/srv/site/index.html":  true,
		"/srv/site/css/app.css": true,
		"/srv/other/secret.env": false,
		"/srv/site/../root.env": false,
		"/srv":                  false,
	} {
		if got := s.validPreviewCapability(capability, "local", "192.0.2.1", path); got != want {
			t.Errorf("validPreviewCapability(dir=/srv/site, path=%s) = %v, want %v", path, got, want)
		}
	}
}

// Opted-in ?run=1 loads get `sandbox allow-scripts`: scripts run but the
// response still forces an opaque origin — for iframes and top-level loads
// alike, so a new tab can execute JS without gaining relay origin. Default
// loads and legacy (dir-less) capabilities keep the strict policy.
func TestFilePreviewCSPRunMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>x</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := service.NewFileService(func(string) []string { return []string{dir} }, nil)
	s := &Server{
		cfg:         config.Config{Token: "secret"},
		files:       files,
		previewCaps: make(map[string]previewCapability),
	}
	capability, err := s.issuePreviewCapability("local", "192.0.2.1", dir)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.issuePreviewCapability("local", "192.0.2.1", "")
	if err != nil {
		t.Fatal(err)
	}
	host := base64.RawURLEncoding.EncodeToString([]byte("local"))
	path := strings.TrimPrefix(filepath.Join(dir, "index.html"), "/")
	url := "/preview/" + capability + "/" + host + "/" + path
	legacyURL := "/preview/" + legacy + "/" + host + "/" + path

	for _, tc := range []struct {
		name    string
		u       string
		dest    string
		wantCSP string // "strict" | "run"
	}{
		{"run iframe", url + "?run=1", "iframe", "run"},
		{"run new tab", url + "?run=1", "document", "run"},
		{"run no dest header", url + "?run=1", "", "run"},
		{"default iframe", url, "iframe", "strict"},
		{"default document", url, "document", "strict"},
		// A legacy capability is unscoped, so it never qualifies for run
		// mode regardless of fetch destination.
		{"legacy cap iframe", legacyURL + "?run=1", "iframe", "strict"},
		{"legacy cap document", legacyURL + "?run=1", "document", "strict"},
	} {
		r := httptest.NewRequest("GET", tc.u, nil)
		if tc.dest != "" {
			r.Header.Set("Sec-Fetch-Dest", tc.dest)
		}
		w := httptest.NewRecorder()
		s.handleFilePreview(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", tc.name, w.Code, http.StatusOK)
		}
		csp := w.Header().Get("Content-Security-Policy")
		switch tc.wantCSP {
		case "strict":
			if !strings.Contains(csp, "default-src 'none'") {
				t.Fatalf("%s: CSP = %q, want strict sandbox policy", tc.name, csp)
			}
		case "run":
			for _, want := range []string{
				"sandbox allow-scripts",
				"default-src 'none'",
				"connect-src http://example.com",
				"img-src http://example.com data:",
				"script-src 'unsafe-inline' 'unsafe-eval' http://example.com data:",
				"style-src 'unsafe-inline' http://example.com data:",
				"form-action 'none'",
				"frame-src 'none'",
			} {
				if !strings.Contains(csp, want) {
					t.Fatalf("%s: CSP = %q, missing %q", tc.name, csp, want)
				}
			}
			if strings.Contains(csp, "allow-same-origin") {
				t.Fatalf("%s: CSP = %q, must never allow same-origin", tc.name, csp)
			}
			if strings.Contains(csp, " http: ") || strings.Contains(csp, " https: ") {
				t.Fatalf("%s: CSP = %q, must not allow arbitrary http: or https: schemes for scripts or styles", tc.name, csp)
			}
		}
	}
}

// The default static preview uses the legacy host-scoped capability, so a
// document referencing ../ resources keeps working; only the dir-scoped run
// capability rejects outside-dir paths (the script-readable URL must stay
// narrow).
func TestFilePreviewLegacyCapServesOutsideDir(t *testing.T) {
	root := t.TempDir()
	site := filepath.Join(root, "site")
	if err := os.Mkdir(site, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "shared.css"), []byte("body{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := service.NewFileService(func(string) []string { return []string{root} }, nil)
	s := &Server{
		cfg:         config.Config{Token: "secret"},
		files:       files,
		previewCaps: make(map[string]previewCapability),
	}
	legacy, _ := s.issuePreviewCapability("local", "192.0.2.1", "")
	run, _ := s.issuePreviewCapability("local", "192.0.2.1", site)
	host := base64.RawURLEncoding.EncodeToString([]byte("local"))
	path := strings.TrimPrefix(filepath.Join(root, "shared.css"), "/")

	r := httptest.NewRequest("GET", "/preview/"+legacy+"/"+host+"/"+path, nil)
	w := httptest.NewRecorder()
	s.handleFilePreview(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy cap ../ status = %d, want 200 (default preview keeps root-wide reach)", w.Code)
	}

	r = httptest.NewRequest("GET", "/preview/"+run+"/"+host+"/"+path, nil)
	w = httptest.NewRecorder()
	s.handleFilePreview(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("dir cap ../ status = %d, want 401", w.Code)
	}
}
