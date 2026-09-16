package config

import (
	"testing"
)

func TestAddr_EmptyBind(t *testing.T) {
	// An explicitly empty Bind means all interfaces; FromEnv defaults to loopback.
	c := Config{Port: "8766", Bind: ""}
	if got := c.Addr(); got != ":8766" {
		t.Errorf("Addr() = %q, want \":8766\"", got)
	}
}

func TestAddr_LoopbackBind(t *testing.T) {
	c := Config{Port: "8766", Bind: "127.0.0.1"}
	if got := c.Addr(); got != "127.0.0.1:8766" {
		t.Errorf("Addr() = %q, want \"127.0.0.1:8766\"", got)
	}
}

func TestAddr_IPv6Bind(t *testing.T) {
	c := Config{Port: "8766", Bind: "::1"}
	if got := c.Addr(); got != "[::1]:8766" {
		t.Errorf("Addr() = %q, want \"[::1]:8766\"", got)
	}
}

func TestAddr_BracketedIPv6Bind(t *testing.T) {
	c := Config{Port: "8766", Bind: "[::1]"}
	if got := c.Addr(); got != "[::1]:8766" {
		t.Errorf("Addr() = %q, want \"[::1]:8766\"", got)
	}
}

func TestIsLoopbackBind(t *testing.T) {
	cases := []struct {
		bind string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"::1", true},
		{"[::1]", true},
		{"localhost", true},
		{"0.0.0.0", false},
		{"", false},
		{"192.168.1.1", false},
	}
	for _, c := range cases {
		cfg := Config{Bind: c.bind}
		if got := cfg.IsLoopbackBind(); got != c.want {
			t.Errorf("IsLoopbackBind(%q) = %v, want %v", c.bind, got, c.want)
		}
	}
}

func TestFromEnv_Bind(t *testing.T) {
	t.Setenv("HERDREM_BIND", "127.0.0.1")
	cfg := FromEnv()
	if cfg.Bind != "127.0.0.1" {
		t.Errorf("Bind = %q, want \"127.0.0.1\"", cfg.Bind)
	}
}

func TestFromEnv_DefaultBind(t *testing.T) {
	t.Setenv("HERDREM_BIND", "")
	if cfg := FromEnv(); cfg.Bind != "127.0.0.1" {
		t.Errorf("Bind = %q, want \"127.0.0.1\"", cfg.Bind)
	}
}

func TestAllowedTunnelPorts_DefaultAndExtras(t *testing.T) {
	cfg := Config{}
	got := cfg.AllowedTunnelPorts()
	for _, p := range DefaultTunnelPorts {
		if _, ok := got[p]; !ok {
			t.Errorf("default port %d missing", p)
		}
	}
	if _, ok := got[22]; ok {
		t.Error("port 22 must not be in the default allowlist")
	}

	cfg.TunnelPortsExtra = "22, 99999, bogus, 3306"
	got = cfg.AllowedTunnelPorts()
	if _, ok := got[22]; ok {
		t.Error("privileged port 22 must be discarded")
	}
	if _, ok := got[3306]; !ok {
		t.Error("extra 3306 missing")
	}
	if _, ok := got[99999]; ok {
		t.Error("invalid extra port 99999 should be ignored")
	}
}

func TestAllowedTunnelPorts_RangesAndJunk(t *testing.T) {
	cfg := Config{TunnelPortsExtra: "8000-8100,9000-10000, 80-90, 8100-8000, 8A00, 8000-81OO, 9000-10000-11000, +5174, 0x1F40"}
	got := cfg.AllowedTunnelPorts()
	for _, p := range []int{8000, 8050, 8100, 9000, 9500, 10000} {
		if _, ok := got[p]; !ok {
			t.Errorf("range port %d missing", p)
		}
	}
	if _, ok := got[7999]; ok {
		t.Error("7999 is outside 8000-8100")
	}
	if _, ok := got[80]; ok {
		t.Error("privileged 80 from range 80-90 must be dropped")
	}
	if _, ok := got[90]; ok {
		t.Error("privileged 90 must be dropped")
	}
	// 80-90 is entirely privileged → adds nothing; reversed/malformed tokens discarded.
	if _, ok := got[8100]; !ok {
		t.Error("8100 from first range should remain")
	}

	cfg.TunnelPortsExtra = "80-2000"
	got = cfg.AllowedTunnelPorts()
	if _, ok := got[80]; ok {
		t.Error("80 in mixed range must be dropped")
	}
	if _, ok := got[1024]; !ok {
		t.Error("1024 from mixed range 80-2000 should be kept")
	}
	if _, ok := got[2000]; !ok {
		t.Error("2000 from mixed range should be kept")
	}
}

func TestEnvTunnelPolicy(t *testing.T) {
	t.Setenv("HERDREM_ALLOW_DIRECT_TUNNEL", "")
	t.Setenv("HERDREM_TUNNEL_MAX", "")
	cfg := FromEnv()
	if cfg.AllowDirectTunnel {
		t.Error("direct tunnels must be off by default")
	}
	if cfg.MaxTunnels() != DefaultTunnelMax {
		t.Errorf("TunnelMax = %d, want %d", cfg.MaxTunnels(), DefaultTunnelMax)
	}

	t.Setenv("HERDREM_ALLOW_DIRECT_TUNNEL", "1")
	t.Setenv("HERDREM_TUNNEL_MAX", "3")
	cfg = FromEnv()
	if !cfg.AllowDirectTunnel {
		t.Error("HERDREM_ALLOW_DIRECT_TUNNEL=1 should enable direct")
	}
	if cfg.MaxTunnels() != 3 {
		t.Errorf("TunnelMax = %d, want 3", cfg.MaxTunnels())
	}

	t.Setenv("HERDREM_TUNNEL_MAX", "0")
	cfg = FromEnv()
	if cfg.MaxTunnels() != DefaultTunnelMax {
		t.Errorf("invalid max 0 should fall back to %d, got %d", DefaultTunnelMax, cfg.MaxTunnels())
	}
}
