package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// File upload: the only path that creates files in a workspace. It is held to
// the same sandbox as reads — the host allowlist and the pane-cwd roots — and
// adds two rules of its own: the destination must be an existing directory,
// and an existing file is never replaced unless the caller asked for it.
//
// Writes are all-or-nothing: the bytes land in a temporary file next to the
// destination and only a complete upload (exactly the announced size) is
// moved into place, so a dropped connection never leaves half a file behind.

// MaxUploadBytes caps one uploaded file. A var so tests can lower it.
var MaxUploadBytes int64 = 100 << 20 // 100 MiB

// Upload errors, mapped to HTTP statuses by the server.
var (
	ErrUploadInvalid    = errors.New("invalid upload request")
	ErrUploadForbidden  = errors.New("destination outside allowed roots")
	ErrUploadNoDir      = errors.New("destination directory not found")
	ErrUploadExists     = errors.New("file already exists")
	ErrUploadTooLarge   = errors.New("file too large")
	ErrUploadIncomplete = errors.New("upload incomplete")
)

// UploadRequest describes one file to store.
type UploadRequest struct {
	Host      string
	Dir       string // absolute directory path
	Name      string // plain file name, no separators
	Size      int64  // exact byte count the body must deliver
	Overwrite bool
}

// UploadResult is what was written.
type UploadResult struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Replaced bool   `json:"replaced"`
}

// validUploadName accepts a single path element that is safe on any POSIX
// filesystem and unambiguous to display: no separators, no NUL or control
// characters, not "." / "..", at most 255 bytes, valid UTF-8.
func validUploadName(name string) error {
	if name == "" || name == "." || name == ".." || len(name) > 255 || !utf8.ValidString(name) {
		return ErrUploadInvalid
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return ErrUploadInvalid
		}
	}
	return nil
}

func tempUploadName() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return ".herdrem-upload-" + hex.EncodeToString(b[:]) + ".part"
}

// Upload stores body as req.Name inside req.Dir.
func (fs *FileService) Upload(ctx context.Context, req UploadRequest, body io.Reader) (UploadResult, error) {
	if err := validUploadName(req.Name); err != nil {
		return UploadResult{}, err
	}
	if req.Size < 0 || req.Dir == "" || !filepath.IsAbs(req.Dir) {
		return UploadResult{}, ErrUploadInvalid
	}
	if req.Size > MaxUploadBytes {
		return UploadResult{}, ErrUploadTooLarge
	}
	if !fs.hostOK(req.Host) {
		return UploadResult{}, ErrUploadForbidden
	}
	roots := fs.roots(req.Host)
	if len(roots) == 0 {
		return UploadResult{}, ErrUploadForbidden
	}
	if isLocal(req.Host) {
		return uploadLocal(req, roots, body)
	}
	return fs.uploadRemote(ctx, req, roots, body)
}

// uploadLocal writes through an os.Root opened at the allowed root that
// contains the destination: every path operation below is confined to that
// root even if a component is (or becomes) a symlink pointing outside it.
func uploadLocal(req UploadRequest, roots []string, body io.Reader) (UploadResult, error) {
	dir := filepath.Clean(req.Dir)
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		if errors.Is(err, iofs.ErrNotExist) {
			return UploadResult{}, ErrUploadNoDir
		}
		return UploadResult{}, err
	}
	base, rel := "", ""
	for _, root := range roots {
		r := filepath.Clean(root)
		if rr, err := filepath.EvalSymlinks(r); err == nil {
			r = rr
		}
		if real == r || strings.HasPrefix(real, r+string(os.PathSeparator)) {
			if rel, err = filepath.Rel(r, real); err == nil {
				base = r
				break
			}
		}
	}
	if base == "" {
		return UploadResult{}, ErrUploadForbidden
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return UploadResult{}, err
	}
	defer root.Close()

	st, err := root.Stat(rel)
	if err != nil {
		if errors.Is(err, iofs.ErrNotExist) {
			return UploadResult{}, ErrUploadNoDir
		}
		return UploadResult{}, err
	}
	if !st.IsDir() {
		return UploadResult{}, ErrUploadNoDir
	}

	dst := filepath.Join(rel, req.Name)
	existing, lerr := root.Lstat(dst)
	existed := lerr == nil
	if existed && (!req.Overwrite || existing.IsDir()) {
		return UploadResult{}, ErrUploadExists
	}

	tmp := filepath.Join(rel, tempUploadName())
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return UploadResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = root.Remove(tmp)
		}
	}()
	n, copyErr := io.Copy(f, io.LimitReader(body, req.Size+1))
	closeErr := f.Close()
	switch {
	case copyErr != nil:
		return UploadResult{}, fmt.Errorf("%w: %v", ErrUploadIncomplete, copyErr)
	case closeErr != nil:
		return UploadResult{}, closeErr
	case n > req.Size:
		return UploadResult{}, ErrUploadTooLarge
	case n < req.Size:
		return UploadResult{}, ErrUploadIncomplete
	}

	if req.Overwrite {
		if err := root.Rename(tmp, dst); err != nil {
			return UploadResult{}, err
		}
		committed = true
	} else {
		// A hard link is an atomic "create only if absent"; filesystems
		// without hard links fall back to check-then-rename.
		if err := root.Link(tmp, dst); err == nil {
			_ = root.Remove(tmp)
			committed = true
		} else if errors.Is(err, iofs.ErrExist) {
			return UploadResult{}, ErrUploadExists
		} else {
			if _, err := root.Lstat(dst); err == nil {
				return UploadResult{}, ErrUploadExists
			}
			if err := root.Rename(tmp, dst); err != nil {
				return UploadResult{}, err
			}
			committed = true
		}
	}
	return UploadResult{Path: filepath.Join(real, req.Name), Size: n, Replaced: existed}, nil
}

// Exit codes of the remote upload script (1 = resolve/containment failure,
// from remoteResolveScript).
const (
	remoteUploadWriteFail = 2
	remoteUploadNotDir    = 4
	remoteUploadSize      = 5
	remoteUploadExists    = 17
)

// remoteUploadScript resolves the directory, refuses to clobber unless asked,
// receives the bytes on stdin into a temp file, verifies the exact size and
// only then moves the file into place — all in one ssh command, so the
// containment check and the write see the same filesystem state.
func remoteUploadScript(dir string, roots []string, name string, size int64, overwrite bool) string {
	var b strings.Builder
	b.WriteString(remoteResolveScript(filepath.Clean(dir), roots))
	b.WriteString(fmt.Sprintf(` [ -d "$r" ] || exit %d; dst="$r"/%s; existed=0; if [ -e "$dst" ] || [ -L "$dst" ]; then existed=1; fi;`,
		remoteUploadNotDir, shellQuote(name)))
	if overwrite {
		b.WriteString(fmt.Sprintf(` if [ -d "$dst" ] && [ ! -L "$dst" ]; then exit %d; fi;`, remoteUploadExists))
	} else {
		b.WriteString(fmt.Sprintf(` [ "$existed" = 1 ] && exit %d;`, remoteUploadExists))
	}
	b.WriteString(fmt.Sprintf(` tmp=$(mktemp "$r/.herdrem-upload.XXXXXX") || exit %d; trap 'rm -f "$tmp"' EXIT; cat > "$tmp" || exit %d;`,
		remoteUploadWriteFail, remoteUploadWriteFail))
	b.WriteString(fmt.Sprintf(` n=$(wc -c < "$tmp" | tr -d ' '); [ "$n" = %s ] || exit %d; chmod 644 "$tmp" 2>/dev/null;`,
		strconv.FormatInt(size, 10), remoteUploadSize))
	if overwrite {
		b.WriteString(fmt.Sprintf(` mv -f -- "$tmp" "$dst" || exit %d;`, remoteUploadWriteFail))
	} else {
		b.WriteString(fmt.Sprintf(` if ln -- "$tmp" "$dst" 2>/dev/null; then :; elif [ -e "$dst" ] || [ -L "$dst" ]; then exit %d; else mv -- "$tmp" "$dst" || exit %d; fi;`,
			remoteUploadExists, remoteUploadWriteFail))
	}
	b.WriteString(` printf '%s\n' "$r" "$existed"`)
	return b.String()
}

// remoteUploadCommand runs the upload script on host. A var so tests can run
// the script locally instead of over ssh.
var remoteUploadCommand = func(ctx context.Context, host, script string) *exec.Cmd {
	return exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", host, script)
}

func (fs *FileService) uploadRemote(ctx context.Context, req UploadRequest, roots []string, body io.Reader) (UploadResult, error) {
	cmd := remoteUploadCommand(ctx, req.Host, remoteUploadScript(req.Dir, roots, req.Name, req.Size, req.Overwrite))
	// One byte more than announced lets the remote size check catch a body
	// that is too long; a short body (client went away) fails it too.
	counted := &countingReader{r: io.LimitReader(body, req.Size+1)}
	cmd.Stdin = counted
	var stdout boundedWriter
	stdout.limit = 8 << 10
	stderr := &boundedWriter{limit: maxStderrBytes}
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case 1:
				return UploadResult{}, ErrUploadForbidden
			case remoteUploadNotDir:
				return UploadResult{}, ErrUploadNoDir
			case remoteUploadExists:
				return UploadResult{}, ErrUploadExists
			case remoteUploadSize:
				if counted.n > req.Size {
					return UploadResult{}, ErrUploadTooLarge
				}
				return UploadResult{}, ErrUploadIncomplete
			}
		}
		return UploadResult{}, fmt.Errorf("ssh upload %s: %w (%s)", req.Name, err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	dir := filepath.Clean(req.Dir)
	replaced := false
	if len(lines) == 2 {
		dir = lines[0]
		replaced = lines[1] == "1"
	}
	return UploadResult{Path: dir + "/" + req.Name, Size: req.Size, Replaced: replaced}, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
