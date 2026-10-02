package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"herdrem/internal/herdr"
	"herdrem/internal/hub"
	"herdrem/internal/store"
)

type gitRig struct {
	t     *testing.T
	ctx   context.Context
	conn  *websocket.Conn
	svc   *Service
	prefs *store.PrefsStore
	dir   string
}

func newGitRig(t *testing.T) *gitRig {
	t.Helper()
	fs, dir := newRepo(t, true)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	prefs, err := store.NewPrefsStore(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(&herdr.FakeClient{}, nil, nil, prefs, fs, h, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	go h.Run(ctx)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		h.HandleConn(r.Context(), c, "", svc.HandleMessage)
	}))
	t.Cleanup(srv.Close)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(websocket.StatusNormalClosure, "") })
	return &gitRig{t: t, ctx: ctx, conn: c, svc: svc, prefs: prefs, dir: dir}
}

func (r *gitRig) ask(v map[string]any) map[string]any {
	r.t.Helper()
	b, _ := json.Marshal(v)
	if err := r.conn.Write(r.ctx, websocket.MessageText, b); err != nil {
		r.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()
	_, data, err := r.conn.Read(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	return m
}

func TestGitStatusMessageShape(t *testing.T) {
	r := newGitRig(t)
	os.WriteFile(filepath.Join(r.dir, "tracked.txt"), []byte("v2\n"), 0o644)
	m := r.ask(map[string]any{"type": "git_status", "cwd": r.dir, "host": "local", "req_id": "s1"})
	if m["type"] != "git_status_result" || m["req_id"] != "s1" || m["cwd"] != r.dir || m["branch"] != "main" {
		t.Fatalf("got %v", m)
	}
	un := m["unstaged"].([]any)
	if len(un) != 1 || un[0].(map[string]any)["path"] != "tracked.txt" {
		t.Fatalf("unstaged = %v", un)
	}
	if _, ok := m["staged"].([]any); !ok {
		t.Fatal("staged must be an array, not null")
	}
}

func TestGitStageRepliesWithAFreshStatus(t *testing.T) {
	r := newGitRig(t)
	os.WriteFile(filepath.Join(r.dir, "new.txt"), []byte("n\n"), 0o644)
	m := r.ask(map[string]any{"type": "git_stage", "cwd": r.dir, "paths": []string{"new.txt"}, "req_id": "a1"})
	if m["type"] != "git_status_result" || m["req_id"] != "a1" {
		t.Fatalf("got %v", m)
	}
	st := m["staged"].([]any)
	if len(st) != 1 || st[0].(map[string]any)["path"] != "new.txt" {
		t.Fatalf("staged = %v", st)
	}
	m = r.ask(map[string]any{"type": "git_stage", "cwd": r.dir, "paths": []string{"../escape"}, "req_id": "a2"})
	if m["type"] != "error" || m["op"] != "git_stage" || m["key"] != "invalid" || m["req_id"] != "a2" {
		t.Fatalf("bad path: %v", m)
	}
}

func TestGitCommitWithAMessageOverTheSocket(t *testing.T) {
	r := newGitRig(t)
	os.WriteFile(filepath.Join(r.dir, "tracked.txt"), []byte("v2\n"), 0o644)
	r.ask(map[string]any{"type": "git_stage", "cwd": r.dir, "all": true})
	m := r.ask(map[string]any{"type": "git_commit", "cwd": r.dir, "message": "update tracked", "req_id": "c1"})
	if m["type"] != "git_commit_result" || m["ok"] != true || m["subject"] != "update tracked" || m["req_id"] != "c1" {
		t.Fatalf("got %v", m)
	}
	m = r.ask(map[string]any{"type": "git_commit", "cwd": r.dir, "message": "", "req_id": "c2"})
	if m["ok"] != false || m["error"] != "commit message is empty or too long" {
		t.Fatalf("empty message: %v", m)
	}
}

func TestGitCommitCommandComesFromPrefsOnly(t *testing.T) {
	r := newGitRig(t)
	m := r.ask(map[string]any{"type": "git_commit", "cwd": r.dir, "use_command": true, "command": "touch pwned", "req_id": "x"})
	if m["ok"] != false || m["error"] != "no commit command configured" {
		t.Fatalf("got %v", m)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "pwned")); err == nil {
		t.Fatal("a command from the request was executed")
	}

	os.WriteFile(filepath.Join(r.dir, "tracked.txt"), []byte("v2\n"), 0o644)
	if err := r.prefs.SetSection("git", json.RawMessage(`{"commit_cmd":"git commit -qam 'via prefs command'"}`)); err != nil {
		t.Fatal(err)
	}
	m = r.ask(map[string]any{"type": "git_commit", "cwd": r.dir, "use_command": true, "req_id": "y"})
	if m["ok"] != true || m["subject"] != "via prefs command" || m["command"] != "git commit -qam 'via prefs command'" {
		t.Fatalf("got %v", m)
	}
}

func TestGitCommitOneAtATimePerRepo(t *testing.T) {
	r := newGitRig(t)
	r.svc.commitMu.Lock()
	r.svc.committing = map[string]bool{"local\x00" + r.dir: true}
	r.svc.commitMu.Unlock()
	// however the host and path are spelled
	for _, req := range []map[string]any{
		{"type": "git_commit", "cwd": r.dir, "message": "second"},
		{"type": "git_commit", "cwd": r.dir, "host": "local", "message": "second"},
		{"type": "git_commit", "cwd": r.dir + "/", "host": "", "message": "second"},
	} {
		m := r.ask(req)
		if m["ok"] != false || m["error"] != "a commit is already running here" {
			t.Fatalf("%v → %v", req, m)
		}
	}
}

// A page that closed or reopened mid-commit learns about it from the status:
// the commit reply itself only goes to the connection that asked.
func TestGitStatusCarriesTheCommitState(t *testing.T) {
	r := newGitRig(t)
	m := r.ask(map[string]any{"type": "git_status", "cwd": r.dir})
	if _, ok := m["commit_running"]; ok {
		t.Fatalf("idle repo reports a running commit: %v", m)
	}
	if _, ok := m["last_commit"]; ok {
		t.Fatalf("no commit yet, but last_commit = %v", m["last_commit"])
	}

	r.svc.commitMu.Lock()
	r.svc.committing = map[string]bool{commitKey("", r.dir): true}
	r.svc.commitMu.Unlock()
	m = r.ask(map[string]any{"type": "git_status", "cwd": r.dir + "/", "host": "local"})
	if m["commit_running"] != true {
		t.Fatalf("running commit not reported: %v", m)
	}
	r.svc.commitMu.Lock()
	r.svc.committing = nil
	r.svc.commitMu.Unlock()

	os.WriteFile(filepath.Join(r.dir, "tracked.txt"), []byte("v2\n"), 0o644)
	r.ask(map[string]any{"type": "git_stage", "cwd": r.dir, "all": true})
	r.ask(map[string]any{"type": "git_commit", "cwd": r.dir, "message": "kept for later", "req_id": "c9"})
	m = r.ask(map[string]any{"type": "git_status", "cwd": r.dir})
	last, _ := m["last_commit"].(map[string]any)
	if m["commit_running"] != nil || last["req_id"] != "c9" || last["ok"] != true || last["subject"] != "kept for later" {
		t.Fatalf("got %v", m)
	}

	// an old result is not news
	r.svc.commitMu.Lock()
	rec := r.svc.lastCommit[commitKey("", r.dir)]
	rec.at = time.Now().Add(-lastCommitTTL)
	r.svc.lastCommit[commitKey("", r.dir)] = rec
	r.svc.commitMu.Unlock()
	m = r.ask(map[string]any{"type": "git_status", "cwd": r.dir})
	if _, ok := m["last_commit"]; ok {
		t.Fatalf("expired result still reported: %v", m["last_commit"])
	}
}

func TestGitStatusErrorsAreKeyed(t *testing.T) {
	r := newGitRig(t)
	m := r.ask(map[string]any{"type": "git_status", "cwd": t.TempDir(), "req_id": "e"})
	if m["type"] != "error" || m["op"] != "git_status" || m["req_id"] != "e" {
		t.Fatalf("got %v", m)
	}
}
