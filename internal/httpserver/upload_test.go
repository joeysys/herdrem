package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"herdrem/internal/config"
	"herdrem/internal/hub"
	"herdrem/internal/service"
)

type uploadRig struct {
	srv  *httptest.Server
	root string
}

func newUploadRig(t *testing.T, cfg config.Config) *uploadRig {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(logger)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go h.Run(ctx)
	root := t.TempDir()
	files := service.NewFileService(func(string) []string { return []string{root} }, logger)
	staticFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	s := New(cfg, h, nil, nil, nil, files, nil, staticFS, logger)
	srv := httptest.NewServer(s.Routes())
	t.Cleanup(srv.Close)
	return &uploadRig{srv: srv, root: root}
}

type upOpts struct {
	cookie    string
	method    string
	name      string
	dir       string
	body      string
	size      int // -1 = len(body)
	origin    string
	noHeader  bool
	bearer    string
	overwrite bool
}

func (r *uploadRig) do(t *testing.T, o upOpts) (*http.Response, string) {
	t.Helper()
	if o.method == "" {
		o.method = http.MethodPost
	}
	if o.dir == "" {
		o.dir = r.root
	}
	size := len(o.body)
	if o.size >= 0 && o.size != 0 {
		size = o.size
	}
	q := url.Values{"host": {"local"}, "dir": {o.dir}, "name": {o.name}, "size": {strconv.Itoa(size)}}
	if o.overwrite {
		q.Set("overwrite", "1")
	}
	req, _ := http.NewRequest(o.method, r.srv.URL+"/upload?"+q.Encode(), strings.NewReader(o.body))
	if o.origin == "" {
		o.origin = r.srv.URL // same origin
	}
	req.Header.Set("Origin", o.origin)
	if !o.noHeader {
		req.Header.Set("X-Herdrem-Upload", "1")
	}
	if o.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+o.bearer)
	}
	if o.cookie != "" {
		req.AddCookie(&http.Cookie{Name: "herdrem_auth", Value: o.cookie})
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestUploadEndpointStoresAFile(t *testing.T) {
	r := newUploadRig(t, config.Config{})
	resp, body := r.do(t, upOpts{name: "note.txt", body: "hello"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var res struct {
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		Replaced bool   `json:"replaced"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	if res.Size != 5 || filepath.Base(res.Path) != "note.txt" || res.Replaced {
		t.Fatalf("result = %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(r.root, "note.txt")); string(got) != "hello" {
		t.Fatalf("content = %q", got)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("upload response must not be cached")
	}
}

func TestUploadEndpointGuardsAgainstOtherSites(t *testing.T) {
	r := newUploadRig(t, config.Config{})
	if resp, _ := r.do(t, upOpts{name: "a", body: "x", origin: "https://evil.example"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", resp.StatusCode)
	}
	if resp, _ := r.do(t, upOpts{name: "a", body: "x", noHeader: true}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing header: %d", resp.StatusCode)
	}
	if resp, _ := r.do(t, upOpts{name: "a", body: "x", method: http.MethodGet}); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(r.root, "a")); err == nil {
		t.Fatal("a rejected request wrote a file")
	}
}

func TestUploadEndpointNeedsTheTokenWhenOneIsSet(t *testing.T) {
	r := newUploadRig(t, config.Config{Token: "s3cret"})
	if resp, _ := r.do(t, upOpts{name: "a", body: "x"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no credential: %d", resp.StatusCode)
	}
	if resp, _ := r.do(t, upOpts{name: "a", body: "x", bearer: "wrong"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", resp.StatusCode)
	}
	if resp, body := r.do(t, upOpts{name: "a", body: "x", bearer: "s3cret"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("valid token: %d %s", resp.StatusCode, body)
	}
}

func TestUploadEndpointStatusCodes(t *testing.T) {
	r := newUploadRig(t, config.Config{})
	os.WriteFile(filepath.Join(r.root, "taken"), []byte("orig"), 0o644)
	cases := []struct {
		name string
		o    upOpts
		want int
	}{
		{"conflict", upOpts{name: "taken", body: "new"}, http.StatusConflict},
		{"overwrite", upOpts{name: "taken", body: "new", overwrite: true}, http.StatusOK},
		{"bad name", upOpts{name: "../x", body: "x"}, http.StatusBadRequest},
		{"outside roots", upOpts{name: "x", body: "x", dir: t.TempDir()}, http.StatusForbidden},
		{"missing dir", upOpts{name: "x", body: "x", dir: filepath.Join(r.root, "nope")}, http.StatusNotFound},
		{"length mismatch", upOpts{name: "m", body: "abc", size: 10}, http.StatusBadRequest},
	}
	for _, c := range cases {
		if resp, body := r.do(t, c.o); resp.StatusCode != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, resp.StatusCode, c.want, body)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(r.root, "taken")); string(got) != "new" {
		t.Fatalf("overwrite did not land: %q", got)
	}
}

func TestUploadEndpointRejectsOversize(t *testing.T) {
	old := service.MaxUploadBytes
	service.MaxUploadBytes = 3
	t.Cleanup(func() { service.MaxUploadBytes = old })
	r := newUploadRig(t, config.Config{})
	if resp, _ := r.do(t, upOpts{name: "big", body: "12345"}); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestHelloAdvertisesFileUpload(t *testing.T) {
	r := newUploadRig(t, config.Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(r.srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL+"/ws", &websocket.DialOptions{
		HTTPHeader: map[string][]string{"Origin": {r.srv.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var hello map[string]any
	json.Unmarshal(data, &hello)
	if !hasFeature(hello, "file-upload") {
		t.Fatalf("features = %v", hello["features"])
	}
}

// --- cross-origin (an app served from another origin, with the relay token) ---

const otherOrigin = "https://app.example"

func preflight(t *testing.T, r *uploadRig, origin string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodOptions, r.srv.URL+"/upload?name=x", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type,x-herdrem-upload")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestUploadCrossOriginPreflightAllowsTheHeadersButNoCredentials(t *testing.T) {
	r := newUploadRig(t, config.Config{Token: "s3cret"})
	resp := preflight(t, r, otherOrigin)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
	h := resp.Header
	if h.Get("Access-Control-Allow-Origin") != otherOrigin {
		t.Fatalf("ACAO = %q", h.Get("Access-Control-Allow-Origin"))
	}
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials must never be allowed cross-origin")
	}
	allowed := strings.ToLower(h.Get("Access-Control-Allow-Headers"))
	for _, want := range []string{"authorization", "content-type", "x-herdrem-upload"} {
		if !strings.Contains(allowed, want) {
			t.Fatalf("allow-headers %q lacks %s", allowed, want)
		}
	}
	if h.Get("Access-Control-Allow-Methods") != "POST" {
		t.Fatalf("allow-methods = %q", h.Get("Access-Control-Allow-Methods"))
	}
}

func TestUploadCrossOriginPreflightRefusedWithoutAToken(t *testing.T) {
	r := newUploadRig(t, config.Config{})
	resp := preflight(t, r, otherOrigin)
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("token-less relay answered a cross-origin preflight: %d %q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestUploadCrossOriginWithTheTokenWorksAndIsReadable(t *testing.T) {
	r := newUploadRig(t, config.Config{Token: "s3cret"})
	resp, body := r.do(t, upOpts{name: "from-pages.txt", body: "hi", origin: otherOrigin, bearer: "s3cret"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != otherOrigin {
		t.Fatal("the app could not read the result")
	}
	if got, _ := os.ReadFile(filepath.Join(r.root, "from-pages.txt")); string(got) != "hi" {
		t.Fatalf("content = %q", got)
	}
}

func TestUploadCrossOriginErrorsAreReadableToo(t *testing.T) {
	r := newUploadRig(t, config.Config{Token: "s3cret"})
	os.WriteFile(filepath.Join(r.root, "taken"), []byte("x"), 0o644)
	resp, _ := r.do(t, upOpts{name: "taken", body: "y", origin: otherOrigin, bearer: "s3cret"})
	if resp.StatusCode != http.StatusConflict || resp.Header.Get("Access-Control-Allow-Origin") != otherOrigin {
		t.Fatalf("conflict: %d %q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}
	resp, _ = r.do(t, upOpts{name: "a", body: "y", origin: otherOrigin, bearer: "wrong"})
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("Access-Control-Allow-Origin") != otherOrigin {
		t.Fatalf("unauthorized: %d %q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}
}

// The heart of it: a site that does not know the token cannot ride on a
// cookie, even a valid session (or legacy raw-token) cookie.
func TestUploadCrossOriginNeverAcceptsCookies(t *testing.T) {
	r := newUploadRig(t, config.Config{Token: "s3cret"})
	resp, _ := r.do(t, upOpts{name: "csrf.txt", body: "x", origin: "https://evil.example", cookie: "s3cret"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cookie-authenticated cross-origin upload: status %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(r.root, "csrf.txt")); err == nil {
		t.Fatal("cross-origin cookie upload wrote a file")
	}
}

func TestUploadCrossOriginRefusedOnATokenlessRelay(t *testing.T) {
	r := newUploadRig(t, config.Config{})
	resp, body := r.do(t, upOpts{name: "x", body: "x", origin: otherOrigin})
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "HERDREM_TOKEN") {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("token-less relay must not grant CORS")
	}
}

func TestUploadCrossOriginStillNeedsTheMarkerHeader(t *testing.T) {
	r := newUploadRig(t, config.Config{Token: "s3cret"})
	resp, _ := r.do(t, upOpts{name: "x", body: "x", origin: otherOrigin, bearer: "s3cret", noHeader: true})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestUploadFromAScriptWithoutOriginUsesTheToken(t *testing.T) {
	r := newUploadRig(t, config.Config{Token: "s3cret"})
	// do() always sets Origin; build a curl-like request by hand.
	q := url.Values{"host": {"local"}, "dir": {r.root}, "name": {"cli.txt"}, "size": {"3"}}
	req, _ := http.NewRequest(http.MethodPost, r.srv.URL+"/upload?"+q.Encode(), strings.NewReader("cli"))
	req.Header.Set("X-Herdrem-Upload", "1")
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("no Origin, no CORS header")
	}
}
