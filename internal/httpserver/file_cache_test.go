package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"herdrem/internal/config"
	"herdrem/internal/service"
)

func TestFilePreviewRevalidatesMutableFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "image.png")
	if err := os.WriteFile(path, []byte("old-image"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	files := service.NewFileService(func(string) []string { return []string{dir} }, logger)
	server := New(config.Config{}, nil, nil, nil, nil, files, nil, nil, logger)
	req := httptest.NewRequest(http.MethodGet, "/file?path="+url.QueryEscape(path), nil)
	res := httptest.NewRecorder()

	server.Routes().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if got := res.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Errorf("Cache-Control = %q, want private, no-cache", got)
	}
	modified := res.Header().Get("Last-Modified")
	if modified == "" {
		t.Fatal("missing Last-Modified for conditional revalidation")
	}

	if err := os.WriteFile(path, []byte("new-image"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedAt := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/file?path="+url.QueryEscape(path), nil)
	req.Header.Set("If-Modified-Since", modified)
	res = httptest.NewRecorder()
	server.Routes().ServeHTTP(res, req)

	if res.Code != http.StatusOK || res.Body.String() != "new-image" {
		t.Errorf("revalidated response = (%d, %q), want (200, new-image)", res.Code, res.Body.String())
	}
}
