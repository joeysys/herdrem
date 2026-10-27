package webpush

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"herdrem/internal/herdr"
	"herdrem/internal/store"
)

const (
	// PushTTL is how long a push service may keep an undelivered alert.
	PushTTL = 12 * time.Hour
	// ConfirmDelay is how long a status alert waits before it is sent.
	// The latest snapshot is checked again at send time.
	ConfirmDelay = 10 * time.Second
	// LongRound is the done=long threshold. A finished round notifies only
	// when the pane was seen working at least this long before it finished.
	LongRound = 2 * time.Minute
)

// note is the only JSON a push body carries. No terminal text.
type note struct {
	Title  string `json:"title"`
	Body   string `json:"body"`
	Tag    string `json:"tag"`
	PaneID string `json:"paneId,omitempty"`
}

// Notifier sends Web Push from confirmed status transitions.
type Notifier struct {
	store       *store.WebPushStore
	lookup      func(id string) (herdr.Pane, bool)
	logger      *slog.Logger
	client      *http.Client
	delay       time.Duration
	postTimeout time.Duration // one budget per subscription, not per send
	now         func() time.Time

	mu    sync.Mutex
	since map[string]time.Time
	gen   map[string]uint64
}

// NewNotifier builds a sender. lookup reports the latest snapshot of a pane.
func NewNotifier(st *store.WebPushStore, lookup func(string) (herdr.Pane, bool), logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Notifier{
		store:       st,
		lookup:      lookup,
		logger:      logger,
		client:      newPushClient(),
		delay:       ConfirmDelay,
		postTimeout: 20 * time.Second,
		now:         time.Now,
		since:       make(map[string]time.Time),
		gen:         make(map[string]uint64),
	}
}

// Store is the persisted subscription record.
func (n *Notifier) Store() *store.WebPushStore { return n.store }

// OnTransition is the poller status watcher. The first sight of a pane is
// not passed here. A blocked entry or a round ending schedules one check
// after the confirm delay; a newer transition cancels the older one.
func (n *Notifier) OnTransition(pane herdr.Pane, from, to string) {
	if n == nil || n.store == nil || !n.store.Enabled() || pane.PaneID == "" {
		return
	}
	kind, ran, known, gen := n.observe(pane.PaneID, from, to)
	if kind == "" {
		return
	}
	delay := n.delay
	if delay <= 0 {
		delay = ConfirmDelay
	}
	time.AfterFunc(delay, func() {
		n.fire(pane.PaneID, kind, gen, ran, known)
	})
}

func (n *Notifier) observe(id, from, to string) (kind string, ran time.Duration, known bool, gen uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	// blocked→working is the same round resuming after an approval. Resetting
	// the clock there makes a long task look short, so done=long never fires.
	if working(to) && !working(from) && !blocked(from) {
		n.since[id] = n.clock()
	}
	if enteredBlocked(from, to) {
		kind = "blocked"
	} else if roundEnded(from, to) {
		kind = "done"
		start, ok := n.since[id]
		if ok && !start.IsZero() {
			known = true
			ran = n.clock().Sub(start)
		}
		delete(n.since, id)
	}
	if kind != "" {
		n.gen[id]++
		gen = n.gen[id]
	}
	return kind, ran, known, gen
}

func (n *Notifier) clock() time.Time {
	if n.now != nil {
		return n.now()
	}
	return time.Now()
}

func (n *Notifier) fire(id, kind string, gen uint64, ran time.Duration, known bool) {
	n.mu.Lock()
	if n.gen[id] != gen {
		n.mu.Unlock()
		return
	}
	n.mu.Unlock()
	if n.store == nil || !n.store.Enabled() || n.lookup == nil {
		return
	}
	pane, ok := n.lookup(id)
	if !ok {
		return
	}
	switch kind {
	case "blocked":
		if !blocked(pane.Status) {
			return
		}
	case "done":
		if !strings.EqualFold(pane.Status, "done") {
			return
		}
	default:
		return
	}
	// No shared deadline here. send gives each subscription its own timeout;
	// one slow endpoint must not cancel the others.
	n.send(context.Background(), kind, pane, ran, known)
}

// SendTest pushes one fixed note to every subscription. It does not wait
// the confirm delay and it does not read a pane.
func (n *Notifier) SendTest(ctx context.Context) error {
	if n.store == nil || !n.store.Enabled() {
		return fmt.Errorf("webpush: disabled")
	}
	subs := n.store.Subscriptions()
	if len(subs) == 0 {
		return fmt.Errorf("webpush: no subscription")
	}
	msg := note{Title: "herdrem", Body: "Push is working", Tag: "herdrem-test"}
	var first error
	sent := 0
	for _, sub := range subs {
		if err := n.post(ctx, sub, msg, "normal"); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		sent++
	}
	if sent == 0 {
		return first
	}
	return nil
}

func (n *Notifier) send(ctx context.Context, kind string, pane herdr.Pane, ran time.Duration, known bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return
	}
	msg := noteFor(kind, pane)
	urgency := "normal"
	if kind == "blocked" {
		urgency = "high"
	}
	timeout := n.postTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	var wg sync.WaitGroup
	for _, sub := range n.store.Subscriptions() {
		if !wants(sub, kind, ran, known) {
			continue
		}
		wg.Add(1)
		go func(sub store.PushSub) {
			defer wg.Done()
			// Fresh timeout. A child of ctx would inherit one shared deadline.
			subCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			if err := n.post(subCtx, sub, msg, urgency); err != nil {
				n.logger.Info("webpush send failed", "err", err)
			}
		}(sub)
	}
	wg.Wait()
}

func wants(sub store.PushSub, kind string, ran time.Duration, known bool) bool {
	switch kind {
	case "blocked":
		return sub.Input
	case "done":
		switch sub.Done {
		case "always":
			return true
		case "long":
			return known && ran >= LongRound
		default:
			return false
		}
	default:
		return false
	}
}

func noteFor(kind string, pane herdr.Pane) note {
	name := pane.Project
	if name == "" {
		name = pane.Agent
	}
	if name == "" {
		name = pane.PaneID
	}
	body := name + " needs input"
	if kind == "done" {
		body = name + " finished a round"
	}
	return note{Title: name, Body: body, Tag: pane.PaneID, PaneID: pane.PaneID}
}

func (n *Notifier) post(ctx context.Context, sub store.PushSub, msg note, urgency string) error {
	pub, priv := n.store.Keys()
	if pub == "" || priv == "" {
		return fmt.Errorf("webpush: missing vapid keys")
	}
	ua, err := decodeB64(sub.P256DH)
	if err != nil {
		return fmt.Errorf("webpush: p256dh: %w", err)
	}
	auth, err := decodeB64(sub.Auth)
	if err != nil {
		return fmt.Errorf("webpush: auth: %w", err)
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	body, err := encrypt(raw, ua, auth)
	if err != nil {
		return err
	}
	aud, err := endpointAud(sub.Endpoint)
	if err != nil {
		return err
	}
	authz, err := vapidHeader(priv, pub, aud, time.Now().Add(12*time.Hour))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", fmt.Sprintf("%d", int(PushTTL.Seconds())))
	req.Header.Set("Urgency", urgency)
	if topic := topicFor(msg.Tag); topic != "" {
		req.Header.Set("Topic", topic)
	}
	req.Header.Set("Authorization", authz)
	req = req.WithContext(context.WithValue(req.Context(), pushDialTarget{}, pushDialTarget{
		scheme: req.URL.Scheme,
		host:   req.URL.Hostname(),
	}))
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		if err := n.store.Delete(sub.Endpoint); err != nil {
			n.logger.Warn("webpush forget subscription", "err", err)
		}
		return fmt.Errorf("webpush: push service returned %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webpush: push service returned %d", resp.StatusCode)
	}
	return nil
}

// pushDialTarget is the URL the dialer is allowed to judge. http to
// localhost, 127.0.0.1, or ::1 stays allowed; every other loopback, private,
// or link-local address is refused, including a public name that resolves there.
type pushDialTarget struct {
	scheme string
	host   string
}

func newPushClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy: nil, // a proxy would skip the address check below
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialPush(ctx, dialer, network, addr)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// lookupPushIP is the only lookup dialPush trusts. Tests replace it so a
// later answer cannot be what the dial uses.
var lookupPushIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

func dialPush(ctx context.Context, dialer *net.Dialer, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	allowLoopback := false
	if t, ok := ctx.Value(pushDialTarget{}).(pushDialTarget); ok {
		allowLoopback = strings.EqualFold(t.scheme, "http") && httpPushLoopback(t.host)
	}
	ips, err := lookupPushIP(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("webpush: endpoint has no addresses")
	}
	for _, ip := range ips {
		if !pushIPDenied(ip.IP) {
			continue
		}
		if allowLoopback && ip.IP.IsLoopback() {
			continue
		}
		return nil, fmt.Errorf("webpush: endpoint address is not allowed")
	}
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	// Dial the addresses just checked. Dialing host would resolve again, and
	// that second answer can be a private or loopback address (DNS rebinding).
	// TLS ServerName stays the request URL host; http.Transport does not take
	// it from this dial address.
	safe := *dialer
	prevCtx := dialer.ControlContext
	prev := dialer.Control
	safe.ControlContext = func(ctx context.Context, network, address string, c syscall.RawConn) error {
		h, _, splitErr := net.SplitHostPort(address)
		if splitErr != nil {
			return splitErr
		}
		ip := net.ParseIP(h)
		if pushIPDenied(ip) && !(allowLoopback && ip != nil && ip.IsLoopback()) {
			return fmt.Errorf("webpush: endpoint address is not allowed")
		}
		if prevCtx != nil {
			return prevCtx(ctx, network, address, c)
		}
		if prev != nil {
			return prev(network, address, c)
		}
		return nil
	}
	var first error
	for _, ip := range ips {
		conn, dialErr := safe.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		if first == nil {
			first = dialErr
		}
	}
	return nil, first
}

func httpPushLoopback(host string) bool {
	switch strings.ToLower(strings.Trim(host, "[]")) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func pushIPDenied(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

func endpointAud(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("webpush: bad endpoint")
	}
	return u.Scheme + "://" + u.Host, nil
}

var topicOK = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// topicFor is the Topic header. Push services allow 32 characters. A pane id
// that does not fit is hashed; the notification tag in the body stays the
// real pane id.
func topicFor(tag string) string {
	if topicOK.MatchString(tag) {
		return tag
	}
	if tag == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(tag))
	return hex.EncodeToString(sum[:])[:32]
}

func working(status string) bool {
	return strings.EqualFold(status, "working")
}

func blocked(status string) bool {
	s := strings.ToLower(status)
	return strings.Contains(s, "block") || strings.Contains(s, "wait") || strings.Contains(s, "confirm")
}

func enteredBlocked(from, to string) bool {
	return blocked(to) && !blocked(from)
}

func roundEnded(from, to string) bool {
	return strings.EqualFold(to, "done") && (working(from) || blocked(from))
}

// OriginStable reports whether a browser origin may enable Web Push.
// localhost is stable on any scheme. Anything else must be https, a
// hostname, and not a trycloudflare quick-tunnel name.
func OriginStable(scheme, host string) (bool, string) {
	name := host
	if h, _, err := splitHost(host); err == nil {
		name = h
	}
	name = strings.Trim(name, "[]")
	low := strings.ToLower(name)
	if low == "localhost" || low == "127.0.0.1" || low == "::1" {
		return true, ""
	}
	if !strings.EqualFold(scheme, "https") {
		return false, "push needs https or localhost"
	}
	if ip := parseIP(name); ip {
		return false, "push needs a fixed hostname, not an IP address"
	}
	if low == "trycloudflare.com" || strings.HasSuffix(low, ".trycloudflare.com") {
		return false, "this quick tunnel address changes when the tunnel restarts"
	}
	return true, ""
}

func splitHost(host string) (string, string, error) {
	// net.SplitHostPort rejects a host without a port. Callers treat that
	// as "the whole string is the host".
	if !strings.Contains(host, ":") {
		return "", "", fmt.Errorf("no port")
	}
	// IPv6 without a port contains colons but no port separator that
	// SplitHostPort accepts unless it is bracketed.
	i := strings.LastIndex(host, ":")
	if i < 0 {
		return "", "", fmt.Errorf("no port")
	}
	// Avoid importing net in a way that treats bare IPv6 as host:port.
	if strings.Count(host, ":") > 1 && !strings.HasPrefix(host, "[") {
		return "", "", fmt.Errorf("no port")
	}
	return host[:i], host[i+1:], nil
}

func parseIP(name string) bool {
	if strings.Contains(name, ":") {
		return true
	}
	parts := strings.Split(name, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 3 {
			return false
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
			n = n*10 + int(c-'0')
		}
		if n > 255 {
			return false
		}
	}
	return true
}
