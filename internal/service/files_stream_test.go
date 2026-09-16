package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// withRemoteCat swaps the remote streaming command for a local one and restores
// it afterwards.
func withRemoteCat(t *testing.T, build func(ctx context.Context, host, path string, roots []string) *exec.Cmd) {
	t.Helper()
	orig := remoteCatCommand
	remoteCatCommand = build
	t.Cleanup(func() { remoteCatCommand = orig })
}

func withStreamCap(t *testing.T, cap int64) {
	t.Helper()
	orig := maxStreamBytes
	maxStreamBytes = cap
	t.Cleanup(func() { maxStreamBytes = orig })
}

// A producer that never stops writing must not wedge the request when the size
// cap is hit. This is the regression guard for the shutdown-ordering deadlock:
// if cmd.Wait() is ever called before the context is cancelled, the child stays
// blocked on a full stdout pipe and this test hangs instead of failing fast.
func TestServeRemoteStreamCapDoesNotDeadlock(t *testing.T) {
	withStreamCap(t, 64<<10)
	withRemoteCat(t, func(ctx context.Context, host, path string, roots []string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "yes hello")
	})

	fs := NewFileService(func(string) []string { return []string{"/"} }, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/preview/x", nil)

	done := make(chan error, 1)
	go func() { done <- fs.serveRemoteStream(w, r, "remote", "/big.bin", []string{"/"}) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveRemoteStream returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveRemoteStream did not return within 10s — shutdown ordering regressed (Wait before cancel)")
	}

	if got := int64(w.Body.Len()); got != maxStreamBytes {
		t.Errorf("body length = %d, want the cap %d", got, maxStreamBytes)
	}
}

// A client that disappears mid-stream must also not wedge the request.
func TestServeRemoteStreamAbortDoesNotDeadlock(t *testing.T) {
	withStreamCap(t, 1<<30) // effectively uncapped; the abort path is what matters
	withRemoteCat(t, func(ctx context.Context, host, path string, roots []string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "yes hello")
	})

	fs := NewFileService(func(string) []string { return []string{"/"} }, nil)
	r := httptest.NewRequest("GET", "/preview/x", nil)

	done := make(chan error, 1)
	go func() { done <- fs.serveRemoteStream(failingWriter{}, r, "remote", "/big.bin", []string{"/"}) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveRemoteStream returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveRemoteStream did not return within 10s after a write error")
	}
}

// A normal, complete file streams through and reaps cleanly.
func TestServeRemoteStreamWholeFile(t *testing.T) {
	withStreamCap(t, 1<<20)
	withRemoteCat(t, func(ctx context.Context, host, path string, roots []string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf '<h1>hi</h1>'")
	})

	fs := NewFileService(func(string) []string { return []string{"/"} }, nil)
	w := httptest.NewRecorder()
	w.Header().Set("Cache-Control", "private, no-cache")
	r := httptest.NewRequest("GET", "/preview/x", nil)

	if err := fs.serveRemoteStream(w, r, "remote", "/page.html", []string{"/"}); err != nil {
		t.Fatalf("serveRemoteStream: %v", err)
	}
	if got := w.Body.String(); got != "<h1>hi</h1>" {
		t.Errorf("body = %q", got)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff header")
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Errorf("Cache-Control = %q, want caller policy", got)
	}
}

// A failing remote command with no output must surface as an error, not a 200
// with an empty body.
func TestServeRemoteStreamMissingFileErrors(t *testing.T) {
	withRemoteCat(t, func(ctx context.Context, host, path string, roots []string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'cat: no such file' >&2; exit 1")
	})

	fs := NewFileService(func(string) []string { return []string{"/"} }, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/preview/x", nil)

	err := fs.serveRemoteStream(w, r, "remote", "/missing.html", []string{"/"})
	if err == nil {
		t.Fatal("expected an error for a failing remote command")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error should carry remote stderr, got: %v", err)
	}
}

// stderr retention is bounded regardless of how much the remote process emits.
func TestBoundedWriterCaps(t *testing.T) {
	b := &boundedWriter{limit: 16}
	n, err := b.Write([]byte(strings.Repeat("x", 1000)))
	if err != nil || n != 1000 {
		t.Fatalf("Write = (%d, %v), want (1000, nil) so the producer never blocks", n, err)
	}
	if got := len(b.String()); got != 16 {
		t.Errorf("retained %d bytes, want 16", got)
	}
}

var errClientGone = errors.New("client gone")

type failingWriter struct{}

func (failingWriter) Header() http.Header       { return http.Header{} }
func (failingWriter) WriteHeader(int)           {}
func (failingWriter) Write([]byte) (int, error) { return 0, errClientGone }
