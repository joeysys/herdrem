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
	// RequireHerdr makes the relay exit instead of falling back to the fake
	// client when the herdr CLI is unavailable. Set for production deployments
	// so a missing herdr binary is loud, not silent.
	RequireHerdr bool
}

// FromEnv builds Config from HERDREM_*/HERDR_* environment variables.
func FromEnv() Config {
	return Config{
		Port:        envOr("HERDREM_PORT", "8766"),
		Bind:        envOr("HERDREM_BIND", "127.0.0.1"),
		Token:       os.Getenv("HERDREM_TOKEN"),
		HerdrBin:    os.Getenv("HERDR_BIN"),
		Remotes:     os.Getenv("HERDR_REMOTES"),
		KindsFilter: os.Getenv("HERDREM_KINDS"),
		DataDir:     envOr("HERDREM_DATA_DIR", defaultDataDir()),
		StaticDir:   os.Getenv("HERDREM_STATIC"),
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
