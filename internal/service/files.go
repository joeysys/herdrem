package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"herdrem/internal/protocol"
)

// maxFileRead caps how many bytes file_read will return.
const maxFileRead = 1 << 20 // 1 MiB

// FileEntry is one directory entry in a listing.
type FileEntry struct {
	Name  string `json:"name"`
	Type  string `json:"type"` // "file" | "dir" | "link"
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"` // unix seconds, 0 if unknown
}

// FileService serves sandboxed file reads and directory listings, locally or
// over SSH for remote hosts. Roots come from the poller's known pane cwds.
type FileService struct {
	roots   func(host string) []string
	allowed map[string]bool
	logger  *slog.Logger
}

// NewFileService creates a FileService. roots returns allowed cwd roots per host.
func NewFileService(roots func(host string) []string, logger *slog.Logger) *FileService {
	if logger == nil {
		logger = slog.Default()
	}
	return &FileService{roots: roots, logger: logger}
}

// SetAllowedHosts sets the set of remote hosts file operations may target —
// the same allowlist Service applies to pane_create. It is defense in depth:
// roots() already yields nothing for an unknown host, so the check fails
// closed either way.
func (fs *FileService) SetAllowedHosts(hosts []string) {
	set := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		if h != "" {
			set[h] = true
		}
	}
	fs.allowed = set
}

// hostOK reports whether a client-supplied host may be used for file
// operations. Leading-dash hosts are rejected since the string reaches
// ssh's argument list; a nil allowlist means local-only.
func (fs *FileService) hostOK(host string) bool {
	if isLocal(host) {
		return true
	}
	if strings.HasPrefix(host, "-") {
		return false
	}
	return fs.allowed[host]
}

func isLocal(host string) bool { return host == "" || host == "local" }

// isSafePath reports whether path resolves (through symlinks) inside roots.
// Prefer openWithinRoots for reads so the root check applies to the same FD.
func isSafePath(path string, roots []string) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	clean := filepath.Clean(path)
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		// Path may not exist yet for listing a missing dir; fall back to clean.
		real = clean
	}
	return pathInRoots(real, roots)
}

func pathInRoots(real string, roots []string) error {
	real = filepath.Clean(real)
	for _, root := range roots {
		r := filepath.Clean(root)
		if rr, err := filepath.EvalSymlinks(r); err == nil {
			r = rr
		}
		if real == r || strings.HasPrefix(real, r+string(os.PathSeparator)) {
			return nil
		}
	}
	return fmt.Errorf("path %q outside allowed roots", real)
}

// fdPath returns the path of the file already opened in f.
// On Linux this uses /proc/self/fd so the check follows the FD, not a
// later symlink swap of the original path.
func fdPath(f *os.File) (string, error) {
	proc := "/proc/self/fd/" + strconv.Itoa(int(f.Fd()))
	if target, err := os.Readlink(proc); err == nil && target != "" {
		return filepath.Clean(target), nil
	}
	return filepath.EvalSymlinks(f.Name())
}

// openWithinRoots opens path and verifies the opened FD resolves inside roots.
func openWithinRoots(path string, roots []string) (*os.File, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	resolved, err := fdPath(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	if err := pathInRoots(resolved, roots); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// guessMime determines the MIME type and whether the data is text/image.
func guessMime(path string, data []byte) (mime string, isText, isImage bool) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8", true, false
	case ".css":
		return "text/css; charset=utf-8", true, false
	case ".js", ".mjs":
		return "application/javascript; charset=utf-8", true, false
	case ".json":
		return "application/json; charset=utf-8", true, false
	case ".png":
		return "image/png", false, true
	case ".jpg", ".jpeg":
		return "image/jpeg", false, true
	case ".gif":
		return "image/gif", false, true
	case ".webp":
		return "image/webp", false, true
	case ".svg":
		return "image/svg+xml", true, true
	case ".bmp":
		return "image/bmp", false, true
	case ".ico":
		return "image/x-icon", false, true
	}
	// sniff content
	if len(data) > 0 {
		mime = http.DetectContentType(data)
	} else {
		mime = "text/plain; charset=utf-8"
	}
	isImage = strings.HasPrefix(mime, "image/") || ext == ".svg"
	isText = strings.HasPrefix(mime, "text/") || looksText(data)
	if mime == "application/octet-stream" && isText {
		mime = "text/plain; charset=utf-8"
	}
	return mime, isText, isImage
}

// looksText reports whether data is mostly printable (no NUL, high printable ratio).
func looksText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	n := len(data)
	if n > 512 {
		n = 512
	}
	printable := 0
	for _, b := range data[:n] {
		if b == 0 {
			return false
		}
		if b == '\n' || b == '\t' || b == '\r' || (b >= 32 && b < 127) || b >= 0x80 {
			printable++
		}
	}
	return float64(printable)/float64(n) > 0.9
}

// errIsDir reports that a file_read target is a directory. The handler maps it
// to the "is_dir" key so the client opens the directory panel instead of an
// error preview.
var errIsDir = errors.New("is a directory")

// FileReadResult is the outcome of reading a file.
type FileReadResult struct {
	Path      string
	Content   string // utf-8 text or base64 for images
	Encoding  string // "utf-8" | "base64"
	MimeType  string
	IsText    bool
	IsImage   bool
	Truncated bool
	Size      int
}

// Read reads a file within the sandbox (local or remote).
func (fs *FileService) Read(ctx context.Context, host, path string) (*FileReadResult, error) {
	if !fs.hostOK(host) {
		return nil, fmt.Errorf("unauthorized host %q", host)
	}
	roots := fs.roots(host)
	var data []byte
	var err error
	if isLocal(host) {
		data, err = readLocalWithinRoots(path, roots)
	} else {
		if err := isSafePath(path, roots); err != nil {
			return nil, err
		}
		data, err = readRemote(ctx, host, path, roots)
	}
	if err != nil {
		return nil, err
	}
	truncated := len(data) > maxFileRead
	if truncated {
		data = data[:maxFileRead]
	}
	mime, isText, isImage := guessMime(path, data)
	res := &FileReadResult{
		Path:      path,
		MimeType:  mime,
		IsText:    isText,
		IsImage:   isImage,
		Truncated: truncated,
		Size:      len(data),
	}
	if isImage {
		res.Encoding = "base64"
		res.Content = base64.StdEncoding.EncodeToString(data)
	} else {
		res.Encoding = "utf-8"
		res.Content = string(data)
	}
	return res, nil
}

// ServeStream streams a file to http.ResponseWriter using http.ServeContent (local) or byte stream (remote).
// Safe-path checked. Bypasses 1 MiB maxFileRead truncation.
func (fs *FileService) ServeStream(w http.ResponseWriter, r *http.Request, host, path string) error {
	if !fs.hostOK(host) {
		return fmt.Errorf("unauthorized host %q", host)
	}
	roots := fs.roots(host)
	if !isLocal(host) {
		if err := isSafePath(path, roots); err != nil {
			return err
		}
		// Stream the remote file straight through. The previous version
		// ran `base64` over SSH and held both the encoded buffer and the
		// decoded copy in memory (~2.3x file size, unbounded), so a single
		// large asset — which HTML previews fetch automatically via relative
		// URLs — could OOM the Relay. `cat` + io.Copy keeps memory at the
		// copy-buffer size regardless of file size.
		return fs.serveRemoteStream(w, r, host, path, roots)
	}

	f, err := openWithinRoots(path, roots)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}

	// Let http.ServeContent handle MIME inference via mime.TypeByExtension
	// (using filepath.Base(path)) and Range requests. Setting X-Content-Type-Options
	// prevents MIME sniffing in browsers; ServeContent will set Content-Type
	// only if it can infer it, so we provide a fallback for unknown extensions.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "private, max-age=300")
	}
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
	return nil
}

// maxStreamBytes bounds a single streamed remote response. Streaming keeps
// memory constant, but an unbounded response still ties up the connection and
// the remote SSH session, so cap it explicitly. A var rather than a const
// so tests can lower it without producing a gigabyte of data.
var maxStreamBytes int64 = 256 << 20 // 256 MiB

// maxStderrBytes bounds how much of the remote process's stderr is retained
// for error reporting. Unbounded buffering here is its own memory sink.
const maxStderrBytes = 8 << 10

// remoteResolveScript emits a shell snippet that resolves the client-supplied
// path through symlinks on the remote and verifies the result stays inside
// one of roots, exiting 1 otherwise. The check runs remotely inside the same
// ssh command as the subsequent action, so it sees the remote filesystem —
// the local isSafePath pre-check cannot observe remote symlinks.
// Resolution is POSIX-portable (cd -P + leaf readlink) because BSD remotes
// lack `realpath`/`readlink -f`.
func remoteResolveScript(path string, roots []string) string {
	var b strings.Builder
	b.WriteString("p=")
	b.WriteString(shellQuote(path))
	b.WriteString(`; while :; do d=$(dirname "$p"); b=$(basename "$p"); rd=$(cd -P "$d" 2>/dev/null && pwd -P) || exit 1; r="$rd/$b"; if [ -L "$r" ]; then p=$(readlink "$r") || exit 1; case "$p" in /*) ;; *) p="$rd/$p";; esac; else break; fi; done`)
	b.WriteString(`; ok=0; for R in`)
	for _, root := range roots {
		b.WriteString(" ")
		b.WriteString(shellQuote(filepath.Clean(root)))
	}
	b.WriteString(`; do RR=$(cd -P "$R" 2>/dev/null && pwd -P) || RR=$R; case "$r" in "$RR"|"$RR"/*) ok=1;; esac; done; [ "$ok" = 1 ] || { echo "path outside allowed roots" >&2; exit 1; };`)
	return b.String()
}

// remoteCatCommand builds the command that streams a remote file to stdout.
// Indirected through a var so tests can substitute a local process.
var remoteCatCommand = func(ctx context.Context, host, path string, roots []string) *exec.Cmd {
	return exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
		host, remoteResolveScript(path, roots)+` cat -- "$r"`)
}

// boundedWriter keeps at most limit bytes and silently discards the rest. It
// always reports a full write so the producing process is never blocked on a
// stderr write we chose not to store.
type boundedWriter struct {
	buf   bytes.Buffer
	limit int
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	if remain := b.limit - b.buf.Len(); remain > 0 {
		if len(p) > remain {
			b.buf.Write(p[:remain])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *boundedWriter) String() string { return b.buf.String() }

// serveRemoteStream pipes a remote file to the response with constant memory.
// Errors that occur before any byte is written are returned so the caller can
// still produce a proper HTTP error; once the body has started, a failure can
// only truncate the response, which is logged.
//
// Shutdown ordering is the subtle part. When we stop draining stdout — because
// the cap was hit or the client went away — the remote process is left blocked
// writing into a full pipe. Calling cmd.Wait() in that state waits for a
// process that can never exit, so the context must be cancelled (killing the
// child) *before* reaping. Getting this backwards deadlocks the request.
func (fs *FileService) serveRemoteStream(w http.ResponseWriter, r *http.Request, host, path string, roots []string) error {
	ctx, cancel := context.WithCancel(r.Context())

	cmd := remoteCatCommand(ctx, host, path, roots)
	stderr := &boundedWriter{limit: maxStderrBytes}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("ssh stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("ssh cat %s: %w", path, err)
	}

	waited := false
	reap := func() error {
		if waited {
			return nil
		}
		waited = true
		return cmd.Wait()
	}
	// Safety net for early returns: cancel first, then reap. Both are
	// idempotent, so the explicit shutdowns below win when they run first.
	defer func() {
		cancel()
		_ = reap()
	}()

	// Peek enough for content sniffing before committing to a status code, so
	// a missing/unreadable file still yields an error response rather than a
	// 200 with an empty body.
	br := bufio.NewReaderSize(stdout, 32<<10)
	head, peekErr := br.Peek(512)
	if len(head) == 0 {
		// stdout is already at EOF here, so the child is not blocked on a full
		// pipe: reap without cancelling to observe its true exit status, which
		// is what distinguishes "missing file" from "empty file".
		if waitErr := reap(); waitErr != nil {
			return fmt.Errorf("ssh cat %s: %w (%s)", path, waitErr, strings.TrimSpace(stderr.String()))
		}
		if peekErr != nil && peekErr != io.EOF {
			return fmt.Errorf("read remote %s: %w", path, peekErr)
		}
		// Genuinely empty file.
	}

	// A caller-provided Content-Type wins (e.g. /file flattens active MIME
	// types to text/plain); otherwise infer from extension + sniffed content.
	if w.Header().Get("Content-Type") == "" {
		mime, _, _ := guessMime(path, head)
		w.Header().Set("Content-Type", mime)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "private, max-age=300")
	}

	written, copyErr := io.Copy(w, io.LimitReader(br, maxStreamBytes))

	switch {
	case copyErr != nil:
		// Client hung up or the pipe broke; the child may still be writing.
		cancel()
		_ = reap()
		fs.logger.Warn("remote stream aborted", "host", host, "path", path,
			"written", written, "err", copyErr)

	case written >= maxStreamBytes:
		// Cap reached. The child is very likely still producing into a pipe we
		// will never read again, so kill before reaping. A file that is exactly
		// maxStreamBytes long is complete but still reported here; the response
		// is correct either way.
		cancel()
		_ = reap()
		fs.logger.Warn("remote stream hit size cap", "host", host, "path", path,
			"cap", maxStreamBytes)

	default:
		// Whole file copied and stdout reached EOF, so the child has finished
		// writing. Reap without cancelling and surface a non-zero exit — the
		// response is already committed, but a silent truncation must not go
		// unrecorded.
		if waitErr := reap(); waitErr != nil {
			fs.logger.Warn("remote read failed after response started",
				"host", host, "path", path, "written", written,
				"err", waitErr, "stderr", strings.TrimSpace(stderr.String()))
		}
	}
	return nil
}

// Exists checks if a file exists within the sandbox (local or remote).
// Does not read the file content — just a stat. Safe-path checked.
func (fs *FileService) Exists(ctx context.Context, host, path string) bool {
	if !fs.hostOK(host) {
		return false
	}
	roots := fs.roots(host)
	if isLocal(host) {
		f, err := openWithinRoots(path, roots)
		if err != nil {
			return false
		}
		f.Close()
		return true
	}
	if err := isSafePath(path, roots); err != nil {
		return false
	}
	// remote: resolve through symlinks and check containment, then test -e
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=3",
		host, remoteResolveScript(path, roots)+` test -e "$r"`)
	return cmd.Run() == nil
}

// remoteIsDirExit is the sentinel exit code the remote read command uses to
// report "path is a directory" — deterministic across remote shells, unlike
// matching stderr text.
const remoteIsDirExit = 42

func readRemote(ctx context.Context, host, path string, roots []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
		host, remoteResolveScript(path, roots)+fmt.Sprintf(` [ -d "$r" ] && exit %d; base64 < "$r"`, remoteIsDirExit))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == remoteIsDirExit {
			return nil, errIsDir
		}
		return nil, fmt.Errorf("ssh base64 %s: %w (%s)", path, err, strings.TrimSpace(stderr.String()))
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(stdout.String()))
}

// List returns the entries of a directory within the sandbox (local or remote).
func (fs *FileService) List(ctx context.Context, host, path string) ([]FileEntry, error) {
	if !fs.hostOK(host) {
		return nil, fmt.Errorf("unauthorized host %q", host)
	}
	roots := fs.roots(host)
	if isLocal(host) {
		return listLocalWithinRoots(path, roots)
	}
	if err := isSafePath(path, roots); err != nil {
		return nil, err
	}
	return listRemote(ctx, host, path, roots)
}

func readLocalWithinRoots(path string, roots []string) ([]byte, error) {
	f, err := openWithinRoots(path, roots)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errIsDir
	}
	return io.ReadAll(f)
}

func listLocalWithinRoots(path string, roots []string) ([]FileEntry, error) {
	f, err := openWithinRoots(path, roots)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ents, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var out []FileEntry
	for _, e := range ents {
		fe := FileEntry{Name: e.Name(), Type: "file"}
		if e.IsDir() {
			fe.Type = "dir"
		} else if e.Type()&os.ModeSymlink != 0 {
			fe.Type = "link"
		}
		if info, err := e.Info(); err == nil {
			if fe.Type == "file" {
				fe.Size = info.Size()
			}
			fe.Mtime = info.ModTime().Unix()
		}
		out = append(out, fe)
	}
	return out, nil
}

func listRemote(ctx context.Context, host, path string, roots []string) ([]FileEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// emit "type\tsize\tmtime\tname" per line via a portable shell one-liner,
	// after resolving the dir and checking containment on the remote side.
	sh := remoteResolveScript(path, roots) + ` cd "$r" 2>/dev/null && for f in .* *; do [ "$f" = "." ] || [ "$f" = ".." ] || [ -e "$f" ] || continue; if [ -d "$f" ]; then t=dir; s=0; elif [ -L "$f" ]; then t=link; s=0; else t=file; s=$(wc -c <"$f" 2>/dev/null || echo 0); fi; m=$(stat -c '%Y' "$f" 2>/dev/null || echo 0); printf '%s\t%s\t%s\t%s\n' "$t" "$s" "$m" "$f"; done`
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", host, "sh", "-c", sh)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ssh ls %s: %w (%s)", path, err, strings.TrimSpace(stderr.String()))
	}
	var out []FileEntry
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) != 4 {
			continue
		}
		var size, mtime int64
		fmt.Sscanf(parts[1], "%d", &size)
		fmt.Sscanf(parts[2], "%d", &mtime)
		out = append(out, FileEntry{Type: parts[0], Size: size, Mtime: mtime, Name: parts[3]})
	}
	return out, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// toProtocol converts a FileReadResult to the wire FileContent message.
func (r *FileReadResult) toProtocol(reqID string) protocol.FileContent {
	return protocol.FileContent{
		Type:      "file_content",
		Path:      r.Path,
		Content:   r.Content,
		Encoding:  r.Encoding,
		MimeType:  r.MimeType,
		IsText:    r.IsText,
		IsImage:   r.IsImage,
		Truncated: r.Truncated,
		Size:      r.Size,
		ReqID:     reqID,
	}
}
