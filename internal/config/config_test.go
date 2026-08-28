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
