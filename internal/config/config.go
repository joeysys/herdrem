package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all relay configuration from env / flags.
type Config struct {
	Port        string
	Token       string
	HerdrBin    string
	Remotes     string // comma-separated SSH targets
	KindsFilter string // comma-separated RELAY_KINDS filter
	DataDir     string
	StaticDir   string // optional override for embed (debug)
	Web         bool   // serve embedded SPA
	Tunnel      bool   // launch cloudflared quick tunnel
	// AllowedOrigins lists extra browser origins permitted to open a WS
	// connection cross-origin (comma-separated HERDR_ALLOWED_ORIGINS).
	// The relay's own host is always allowed; this only widens it, e.g. a
	// dev UI on localhost:8765 driving a LAN relay via the relay switcher.
	AllowedOrigins string
}

// FromEnv builds Config from HERDREM_*/HERDR_* environment variables.
func FromEnv() Config {
	return Config{
		Port:        envOr("HERDREM_PORT", "8766"),
		Token:       os.Getenv("HERDREM_TOKEN"),
		HerdrBin:    os.Getenv("HERDR_BIN"),
		Remotes:     os.Getenv("HERDR_REMOTES"),
		KindsFilter: os.Getenv("HERDREM_KINDS"),
		DataDir:     envOr("HERDREM_DATA_DIR", defaultDataDir()),
		StaticDir:   os.Getenv("HERDREM_STATIC"),

		AllowedOrigins: os.Getenv("HERDR_ALLOWED_ORIGINS"),
	}
}

// Origins returns the configured cross-origin allowlist for WS upgrades
// (empty = same-origin only, unless the request carries a valid token).
// Entries are matched by coder/websocket's OriginPatterns: a bare host
// ("localhost:8765") is matched against the origin host, and an entry
// containing "://" is matched against "scheme://host".
func (c Config) Origins() []string { return splitList(c.AllowedOrigins) }

// Addr returns the listen address.
func (c Config) Addr() string { return ":" + c.Port }

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
