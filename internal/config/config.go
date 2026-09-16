package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Config holds all relay configuration from env / flags.
type Config struct {
	Port        string
	Bind        string // listen address (host part); default = loopback
	Token       string
	HerdrBin    string
	Remotes     string // comma-separated SSH targets
	KindsFilter string // comma-separated RELAY_KINDS filter
	DataDir     string
	StaticDir   string // optional override for embed (debug)
	Web         bool   // serve embedded SPA
	Tunnel      bool   // launch cloudflared quick tunnel
	// PPROFAddr enables Go pprof endpoints on a dedicated loopback-only
	// listener (e.g. "127.0.0.1:6060"). Empty = disabled; never merged into
	// the public-facing mux.
	PPROFAddr string
	// RequireHerdr makes the relay exit instead of falling back to the fake
	// client when the herdr CLI is unavailable. Set for production deployments
	// so a missing herdr binary is loud, not silent.
	RequireHerdr bool
	// TunnelPortsExtra is HERDREM_TUNNEL_PORTS: extra allowed forward ports
	// on top of DefaultTunnelPorts.
	TunnelPortsExtra string
	// AllowDirectTunnel is HERDREM_ALLOW_DIRECT_TUNNEL: when true, clients
	// may create ungated (direct) quick tunnels. Off by default.
	AllowDirectTunnel bool
	// TunnelMax is HERDREM_TUNNEL_MAX: cap on concurrent starting+active
	// tunnels. Zero means DefaultTunnelMax.
	TunnelMax int
}

// DefaultTunnelPorts is the built-in allowlist for port-forward tunnels.
// Extra ports are added via HERDREM_TUNNEL_PORTS.
var DefaultTunnelPorts = []int{5173, 3000, 8080, 4173}

// DefaultTunnelMax is the cap on concurrent starting+active tunnels.
const DefaultTunnelMax = 8

// minUnprivilegedPort is the first port that does not require root on Unix
// (TCP/UDP ports 1–1023 are system/service ports). Tunnel forwards never
// include these, even if listed in HERDREM_TUNNEL_PORTS.
const minUnprivilegedPort = 1024

// FromEnv builds Config from HERDREM_*/HERDR_* environment variables.
func FromEnv() Config {
	return Config{
		Port:              envOr("HERDREM_PORT", "8766"),
		Bind:              envOr("HERDREM_BIND", "127.0.0.1"),
		Token:             os.Getenv("HERDREM_TOKEN"),
		HerdrBin:          os.Getenv("HERDR_BIN"),
		Remotes:           os.Getenv("HERDR_REMOTES"),
		KindsFilter:       os.Getenv("HERDREM_KINDS"),
		DataDir:           envOr("HERDREM_DATA_DIR", defaultDataDir()),
		StaticDir:         os.Getenv("HERDREM_STATIC"),
		PPROFAddr:         os.Getenv("HERDREM_PPROF_ADDR"),
		TunnelPortsExtra:  os.Getenv("HERDREM_TUNNEL_PORTS"),
		AllowDirectTunnel: envTruthy("HERDREM_ALLOW_DIRECT_TUNNEL"),
		TunnelMax:         envIntOr("HERDREM_TUNNEL_MAX", DefaultTunnelMax),
	}
}

// Addr returns the listen address.
func (c Config) Addr() string { return net.JoinHostPort(strings.Trim(c.Bind, "[]"), c.Port) }

// IsLoopbackBind reports whether the relay is bound to a loopback address,
// meaning it is not reachable from the network.
func (c Config) IsLoopbackBind() bool {
	if c.Bind == "localhost" {
		return true
	}
	return net.ParseIP(strings.Trim(c.Bind, "[]")).IsLoopback()
}

// Hosts returns the poll host list: always "local" first, then SSH remotes.
func (c Config) Hosts() []string {
	hosts := []string{"local"}
	for _, r := range splitList(c.Remotes) {
		hosts = append(hosts, r)
	}
	return hosts
}

// Kinds returns the RELAY_KINDS filter set (empty = no filter).
func (c Config) Kinds() map[string]bool {
	set := make(map[string]bool)
	for _, k := range splitList(c.KindsFilter) {
		set[k] = true
	}
	return set
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envTruthy(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func envIntOr(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

// AllowedTunnelPorts is the default whitelist plus HERDREM_TUNNEL_PORTS extras.
// Extra tokens may be a single port (`9000`) or an inclusive range
// (`8000-8100`). Malformed tokens (letters, extra dashes, reversed ranges,
// non-decimal) are discarded. Privileged system ports (<1024) are dropped
// even when they appear inside a range.
func (c Config) AllowedTunnelPorts() map[int]struct{} {
	set := make(map[int]struct{}, len(DefaultTunnelPorts)+4)
	for _, p := range DefaultTunnelPorts {
		set[p] = struct{}{}
	}
	for _, part := range splitList(c.TunnelPortsExtra) {
		lo, hi, ok := parseTunnelPortToken(part)
		if !ok {
			continue
		}
		if lo < minUnprivilegedPort {
			lo = minUnprivilegedPort
		}
		if hi > 65535 {
			hi = 65535
		}
		for p := lo; p <= hi; p++ {
			set[p] = struct{}{}
		}
	}
	return set
}

// parseTunnelPortToken accepts "9000" or "8000-8100". Anything else — letters,
// signs, hex, extra hyphens, reversed ranges — is invalid.
func parseTunnelPortToken(part string) (lo, hi int, ok bool) {
	part = strings.TrimSpace(part)
	if part == "" {
		return 0, 0, false
	}
	hyphens := strings.Count(part, "-")
	if hyphens > 1 {
		return 0, 0, false
	}
	if hyphens == 0 {
		n, ok := parseDecimalPort(part)
		if !ok {
			return 0, 0, false
		}
		return n, n, true
	}
	a, b, _ := strings.Cut(part, "-")
	lo, ok1 := parseDecimalPort(a)
	hi, ok2 := parseDecimalPort(b)
	if !ok1 || !ok2 || lo > hi {
		return 0, 0, false
	}
	return lo, hi, true
}

func parseDecimalPort(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return n, true
}

// MaxTunnels returns the concurrent-tunnel cap, substituting the default
// when unset.
func (c Config) MaxTunnels() int {
	if c.TunnelMax < 1 {
		return DefaultTunnelMax
	}
	return c.TunnelMax
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Sprintf("/tmp/herdrem-%d", os.Getuid())
	}
	return home + "/.config/herdrem"
}

// ParsePort validates and returns the port as int.
func (c Config) ParsePort() (int, error) {
	p, err := strconv.Atoi(c.Port)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("invalid port %q", c.Port)
	}
	return p, nil
}
