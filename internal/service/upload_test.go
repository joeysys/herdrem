package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func uploadFS(t *testing.T) (*FileService, string) {
	t.Helper()
	root := t.TempDir()
	fs := NewFileService(func(host string) []string {
		if host == "" || host == "local" || host == "box" {
			return []string{root}
		}
		return nil
	}, nil)
	fs.SetAllowedHosts([]string{"box"})
	return fs, root
}

func up(t *testing.T, fs *FileService, host, dir, name, body string, overwrite bool) (UploadResult, error) {
	t.Helper()
	return fs.Upload(context.Background(), UploadRequest{
		Host: host, Dir: dir, Name: name, Size: int64(len(body)), Overwrite: overwrite,
	}, strings.NewReader(body))
}

func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".herdrem-upload") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestUploadLocalWritesTheFile(t *testing.T) {
	fs, root := uploadFS(t)
	res, err := up(t, fs, "local", root, "photo.jpg", "jpegbytes", false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "photo.jpg"))
	if string(got) != "jpegbytes" {
		t.Fatalf("content = %q", got)
	}
	if res.Size != 9 || res.Replaced || filepath.Base(res.Path) != "photo.jpg" {
		t.Fatalf("result = %+v", res)
	}
	st, _ := os.Stat(filepath.Join(root, "photo.jpg"))
	if st.Mode().Perm()&0o111 != 0 {
		t.Fatalf("uploaded file is executable: %v", st.Mode())
	}
	noTempFiles(t, root)
}

func TestUploadLocalIntoSubdirAndEmptyFile(t *testing.T) {
	fs, root := uploadFS(t)
	sub := filepath.Join(root, "docs", "in")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := up(t, fs, "", sub, "empty.txt", "", false); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(sub, "empty.txt")); err != nil || st.Size() != 0 {
		t.Fatalf("empty upload: %v %v", st, err)
	}
}

func TestUploadLocalNeverClobbersWithoutOverwrite(t *testing.T) {
	fs, root := uploadFS(t)
	p := filepath.Join(root, "a.txt")
	os.WriteFile(p, []byte("original"), 0o644)
	if _, err := up(t, fs, "local", root, "a.txt", "new", false); !errors.Is(err, ErrUploadExists) {
		t.Fatalf("err = %v, want ErrUploadExists", err)
	}
	if got, _ := os.ReadFile(p); string(got) != "original" {
		t.Fatalf("file was clobbered: %q", got)
	}
	noTempFiles(t, root)

	res, err := up(t, fs, "local", root, "a.txt", "replaced", true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Replaced {
		t.Fatal("expected Replaced")
	}
	if got, _ := os.ReadFile(p); string(got) != "replaced" {
		t.Fatalf("overwrite failed: %q", got)
	}
}

func TestUploadLocalNeverReplacesADirectory(t *testing.T) {
	fs, root := uploadFS(t)
	os.Mkdir(filepath.Join(root, "sub"), 0o755)
	if _, err := up(t, fs, "local", root, "sub", "x", true); !errors.Is(err, ErrUploadExists) {
		t.Fatalf("err = %v", err)
	}
}

func TestUploadLocalRejectsBadNames(t *testing.T) {
	fs, root := uploadFS(t)
	for _, name := range []string{"", ".", "..", "a/b", "../x", `a\b`, "x\x00y", "line\nbreak", "tab\tx", strings.Repeat("n", 256), "bad\xffutf8"} {
		if _, err := up(t, fs, "local", root, name, "x", false); !errors.Is(err, ErrUploadInvalid) {
			t.Errorf("name %q: err = %v, want ErrUploadInvalid", name, err)
		}
	}
	// ordinary names that only look unusual are fine
	for _, name := range []string{".env.example", "-dash", "with space.txt", "名前.png"} {
		if _, err := up(t, fs, "local", root, name, "x", false); err != nil {
			t.Errorf("name %q: %v", name, err)
		}
	}
}

func TestUploadLocalStaysInsideTheRoots(t *testing.T) {
	fs, root := uploadFS(t)
	outside := t.TempDir()

	if _, err := up(t, fs, "local", outside, "x", "x", false); !errors.Is(err, ErrUploadForbidden) {
		t.Fatalf("outside dir: %v", err)
	}
	// a symlinked directory inside the root that points outside it
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := up(t, fs, "local", link, "x", "x", false); !errors.Is(err, ErrUploadForbidden) {
		t.Fatalf("symlinked dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "x")); err == nil {
		t.Fatal("wrote outside the root")
	}
	// "dir/.." style input that cleans to outside
	if _, err := up(t, fs, "local", filepath.Join(root, ".."), "x", "x", false); !errors.Is(err, ErrUploadForbidden) {
		t.Fatalf("parent traversal: %v", err)
	}
}

func TestUploadLocalOverwriteOfASymlinkReplacesTheLinkNotItsTarget(t *testing.T) {
	fs, root := uploadFS(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("keep"), 0o644)
	os.Symlink(outside, filepath.Join(root, "link.txt"))
	if _, err := up(t, fs, "local", root, "link.txt", "new", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(outside); string(got) != "keep" {
		t.Fatalf("symlink target outside the root was written: %q", got)
	}
	if fi, _ := os.Lstat(filepath.Join(root, "link.txt")); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("expected the link to be replaced by a regular file")
	}
}

func TestUploadLocalDestinationMustBeAnExistingDirectory(t *testing.T) {
	fs, root := uploadFS(t)
	if _, err := up(t, fs, "local", filepath.Join(root, "missing"), "x", "x", false); !errors.Is(err, ErrUploadNoDir) {
		t.Fatalf("missing dir: %v", err)
	}
	os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o644)
	if _, err := up(t, fs, "local", filepath.Join(root, "file"), "x", "x", false); !errors.Is(err, ErrUploadNoDir) {
		t.Fatalf("file as dir: %v", err)
	}
	if _, err := up(t, fs, "local", "relative/dir", "x", "x", false); !errors.Is(err, ErrUploadInvalid) {
		t.Fatalf("relative dir: %v", err)
	}
}

func TestUploadLocalOnlyCommitsTheAnnouncedSize(t *testing.T) {
	fs, root := uploadFS(t)
	short, err := fs.Upload(context.Background(), UploadRequest{Host: "local", Dir: root, Name: "short", Size: 10}, strings.NewReader("abc"))
	if !errors.Is(err, ErrUploadIncomplete) {
		t.Fatalf("short body: %v %+v", err, short)
	}
	if _, err := fs.Upload(context.Background(), UploadRequest{Host: "local", Dir: root, Name: "long", Size: 2}, strings.NewReader("abcdef")); !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("long body: %v", err)
	}
	for _, n := range []string{"short", "long"} {
		if _, err := os.Stat(filepath.Join(root, n)); err == nil {
			t.Fatalf("%s was committed", n)
		}
	}
	noTempFiles(t, root)
}

func TestUploadRejectsOversizeAndUnknownHosts(t *testing.T) {
	fs, root := uploadFS(t)
	old := MaxUploadBytes
	MaxUploadBytes = 4
	t.Cleanup(func() { MaxUploadBytes = old })
	if _, err := up(t, fs, "local", root, "big", "12345", false); !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("oversize: %v", err)
	}
	if _, err := up(t, fs, "evil", root, "x", "x", false); !errors.Is(err, ErrUploadForbidden) {
		t.Fatalf("unknown host: %v", err)
	}
	if _, err := up(t, fs, "-oProxyCommand=x", root, "x", "x", false); !errors.Is(err, ErrUploadForbidden) {
		t.Fatalf("option-like host: %v", err)
	}
}

// --- remote: run the very same script with sh instead of ssh ---

func withLocalRemote(t *testing.T) {
	t.Helper()
	orig := remoteUploadCommand
	remoteUploadCommand = func(ctx context.Context, host, script string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", script)
	}
	t.Cleanup(func() { remoteUploadCommand = orig })
}

func TestUploadRemoteWritesAndReports(t *testing.T) {
	withLocalRemote(t)
	fs, root := uploadFS(t)
	res, err := up(t, fs, "box", root, "it's here.txt", "remote bytes", false)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "it's here.txt")); string(got) != "remote bytes" {
		t.Fatalf("content = %q", got)
	}
	if res.Replaced || !strings.HasSuffix(res.Path, "/it's here.txt") {
		t.Fatalf("result = %+v", res)
	}
	st, _ := os.Stat(filepath.Join(root, "it's here.txt"))
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
	noTempFiles(t, root)
}

func TestUploadRemoteConflictAndOverwrite(t *testing.T) {
	withLocalRemote(t)
	fs, root := uploadFS(t)
	os.WriteFile(filepath.Join(root, "a"), []byte("orig"), 0o644)
	if _, err := up(t, fs, "box", root, "a", "new", false); !errors.Is(err, ErrUploadExists) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a")); string(got) != "orig" {
		t.Fatalf("clobbered: %q", got)
	}
	res, err := up(t, fs, "box", root, "a", "new", true)
	if err != nil || !res.Replaced {
		t.Fatalf("overwrite: %+v %v", res, err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a")); string(got) != "new" {
		t.Fatalf("not replaced: %q", got)
	}
	os.Mkdir(filepath.Join(root, "d"), 0o755)
	if _, err := up(t, fs, "box", root, "d", "x", true); !errors.Is(err, ErrUploadExists) {
		t.Fatalf("dir overwrite: %v", err)
	}
	noTempFiles(t, root)
}

func TestUploadRemoteOnlyCommitsTheAnnouncedSize(t *testing.T) {
	withLocalRemote(t)
	fs, root := uploadFS(t)
	if _, err := fs.Upload(context.Background(), UploadRequest{Host: "box", Dir: root, Name: "short", Size: 10}, strings.NewReader("abc")); !errors.Is(err, ErrUploadIncomplete) {
		t.Fatalf("short: %v", err)
	}
	if _, err := fs.Upload(context.Background(), UploadRequest{Host: "box", Dir: root, Name: "long", Size: 2}, bytes.NewReader([]byte("abcdef"))); !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("long: %v", err)
	}
	for _, n := range []string{"short", "long"} {
		if _, err := os.Stat(filepath.Join(root, n)); err == nil {
			t.Fatalf("%s was committed", n)
		}
	}
	noTempFiles(t, root)
}

func TestUploadRemoteStaysInsideTheRoots(t *testing.T) {
	withLocalRemote(t)
	fs, root := uploadFS(t)
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(root, "escape"))
	if _, err := up(t, fs, "box", filepath.Join(root, "escape"), "x", "x", false); !errors.Is(err, ErrUploadForbidden) {
		t.Fatalf("symlink escape: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "x")); err == nil {
		t.Fatal("wrote outside the root")
	}
	if _, err := up(t, fs, "box", filepath.Join(root, "missing"), "x", "x", false); err == nil {
		t.Fatal("missing dir accepted")
	}
	os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o644)
	if _, err := up(t, fs, "box", filepath.Join(root, "f"), "x", "x", false); !errors.Is(err, ErrUploadNoDir) {
		t.Fatalf("file as dir: %v", err)
	}
}
