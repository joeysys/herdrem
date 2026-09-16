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
	capability, err := s.issuePreviewCapability("local", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if !s.validPreviewCapability(capability, "local", "192.0.2.1") {
		t.Fatal("fresh capability should be valid for its host")
	}
	if s.validPreviewCapability(capability, "remote", "192.0.2.1") {
		t.Fatal("capability should not be valid for another host")
	}

	s.previewMu.Lock()
	s.previewCaps[capability] = previewCapability{host: "local", ip: "192.0.2.1", expiresAt: time.Now().Add(-time.Second)}
	s.previewMu.Unlock()
	if s.validPreviewCapability(capability, "local", "192.0.2.1") {
		t.Fatal("expired capability should be rejected")
	}
}

func TestPreviewAuthReturnsCapability(t *testing.T) {
	s := &Server{cfg: config.Config{Token: "secret"}, previewCaps: make(map[string]previewCapability)}
	r := httptest.NewRequest("GET", "http://relay.local/preview-auth?host=local&token=secret", nil)
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
		Capability string `json:"capability"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Capability == "" || !s.validPreviewCapability(body.Capability, "local", "192.0.2.1") {
		t.Fatal("response did not contain a valid local capability")
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
	capability, err := s.issuePreviewCapability("local", "192.0.2.1")
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
	if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Fatalf("Content-Security-Policy = %q, want sandbox", got)
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
