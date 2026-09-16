package service

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func rootsFor(dir string) func(string) []string {
	return func(host string) []string {
		if host == "" || host == "local" {
			return []string{dir}
		}
		return nil
	}
}

func TestIsSafePath(t *testing.T) {
	dir := t.TempDir()
	roots := []string{dir}
	if err := isSafePath(filepath.Join(dir, "a.go"), roots); err != nil {
		t.Errorf("inside root should be safe: %v", err)
	}
	if err := isSafePath("/etc/passwd", roots); err == nil {
		t.Error("outside root should be rejected")
	}
	if err := isSafePath(filepath.Join(dir, "..", "escape.go"), roots); err == nil {
		t.Error(".. escape should be rejected")
	}
	if err := isSafePath("", roots); err == nil {
		t.Error("empty path should be rejected")
	}
}

func TestIsSafePath_Symlink(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	os.WriteFile(target, []byte("x"), 0o644)
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink not permitted")
	}
	if err := isSafePath(link, []string{dir}); err == nil {
		t.Error("symlink escaping root should be rejected")
	}
}

func TestGuessMime(t *testing.T) {
	if _, _, img := guessMime("a.png", []byte{0x89, 0x50}); !img {
		t.Error("png should be image")
	}
	mime, isText, _ := guessMime("a.go", []byte("package main\n"))
	if !isText {
		t.Errorf("go source should be text, got %s", mime)
	}
	if _, isText, _ := guessMime("a.bin", []byte{0, 1, 2, 3}); isText {
		t.Error("binary with NUL should not be text")
	}
}

func TestLooksText(t *testing.T) {
	if !looksText([]byte("hello world\n")) {
		t.Error("plain text")
	}
	if looksText([]byte{0xff, 0xfe, 0, 1}) {
		t.Error("binary")
	}
	if !looksText(nil) {
		t.Error("empty is text")
	}
}

func TestFileService_ReadText(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n"), 0o644)
	fs := NewFileService(rootsFor(dir), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := fs.Read(ctx, "", filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Encoding != "utf-8" || res.Content != "package main\n" {
		t.Errorf("res = %+v", res)
	}
	if res.IsImage {
		t.Error("go source should not be image")
	}
}

func TestFileService_ReadMarksTruncatedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, maxFileRead+1), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := NewFileService(rootsFor(dir), nil).Read(context.Background(), "local", path)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.Size != maxFileRead || len(res.Content) != maxFileRead {
		t.Fatalf("large read = truncated:%v size:%d content:%d, want true/%d/%d", res.Truncated, res.Size, len(res.Content), maxFileRead, maxFileRead)
	}
}

func TestOpenWithinRoots_SymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("symlink not permitted")
	}
	fs := NewFileService(rootsFor(dir), nil)
	if _, err := fs.Read(context.Background(), "", link); err == nil {
		t.Fatal("symlink escaping root should be rejected on the opened FD")
	}
	if fs.Exists(context.Background(), "", link) {
		t.Fatal("Exists should reject symlink escape")
	}
}

func TestReadUsesOpenedFDAfterPathSwap(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(inside, []byte("inside-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openWithinRoots(inside, []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(inside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, inside); err != nil {
		t.Skip("symlink not permitted")
	}

	buf := make([]byte, 32)
	n, err := f.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "inside-data" {
		t.Fatalf("read after swap = %q, want original FD content", got)
	}

	resolved, err := fdPath(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := pathInRoots(resolved, []string{dir}); err != nil {
		t.Fatalf("opened FD should still resolve inside root, got %q: %v", resolved, err)
	}
}

func TestFileService_ReadRejectsOutside(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileService(rootsFor(dir), nil)
	ctx := context.Background()
	if _, err := fs.Read(ctx, "", "/etc/hostname"); err == nil {
		t.Error("outside root should be rejected")
	}
}

func TestFileService_List(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	fs := NewFileService(rootsFor(dir), nil)

	entries, err := fs.List(context.Background(), "", dir)
	if err != nil {
		t.Fatal(err)
	}
	var file, sub *FileEntry
	for i := range entries {
		if entries[i].Name == "a.go" {
			file = &entries[i]
		}
		if entries[i].Name == "sub" {
			sub = &entries[i]
		}
	}
	if file == nil || file.Type != "file" {
		t.Errorf("missing file entry: %+v", entries)
	}
	if sub == nil || sub.Type != "dir" {
		t.Errorf("missing dir entry: %+v", entries)
	}
}

func TestToProtocol(t *testing.T) {
	r := &FileReadResult{Path: "/x/a.go", Content: "c", Encoding: "utf-8", MimeType: "text/plain", IsText: true, Truncated: true, Size: 1}
	pc := r.toProtocol("req-1")
	if pc.Type != "file_content" || pc.Encoding != "utf-8" || !pc.IsText || !pc.Truncated || pc.Size != 1 || pc.ReqID != "req-1" {
		t.Errorf("pc = %+v", pc)
	}
}

// remoteResolveScript runs the exact shell snippet the remote file commands
// prepend — resolving $p through symlinks and requiring the result to land
// inside a root. Running it through local sh exercises the same logic the
// remote shell will execute.
func TestRemoteResolveScript(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	// leaf symlink inside the root pointing outside
	if err := os.Symlink(secret, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	// symlinked dir inside the root pointing outside
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	ok := filepath.Join(root, "ok.txt")
	if err := os.WriteFile(ok, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(path string) (string, error) {
		script := remoteResolveScript(path, []string{root}) + ` cat "$r"`
		out, err := exec.Command("sh", "-c", script).CombinedOutput()
		return string(out), err
	}

	if out, err := run(ok); err != nil || strings.TrimSpace(out) != "ok" {
		t.Fatalf("real file should resolve and read: out=%q err=%v", out, err)
	}
	if out, err := run(filepath.Join(root, "link.txt")); err == nil {
		t.Fatalf("leaf symlink escape was not blocked: %q", out)
	}
	if out, err := run(filepath.Join(root, "linkdir", "secret.txt")); err == nil {
		t.Fatalf("symlinked dir escape was not blocked: %q", out)
	}
	if out, err := run(secret); err == nil {
		t.Fatalf("path outside roots was not blocked: %q", out)
	}
}
