package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os/exec"

	"herdrem/internal/config"
	"herdrem/internal/herdr"
)

type doctorCheck struct {
	status, name, detail string
}

// runDoctor performs read-only diagnostics (`herdrem doctor`). It changes no
// state and writes no credentials: herdr presence and per-host version, token
// vs bind address, and cloudflared availability. Prints one line per check
// and returns 1 when any check fails so scripts can gate on it.
func runDoctor(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	envFile := fs.String("env-file", "", "load config env vars from this file (default "+config.DefaultEnvFilePath()+" when present)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(out, "usage: herdrem doctor [-env-file PATH]")
		return 2
	}
	// Same resolution as serve: the file fills variables not already in the
	// process environment, so real env always wins.
	loadedEnv, err := config.LoadEnvFile(*envFile, *envFile != "")
	if err != nil {
		fmt.Fprintf(out, "FAIL  env-file — %v\n", err)
		return 1
	}
	if loadedEnv != "" {
		fmt.Fprintf(out, "note: loaded env file %s\n", loadedEnv)
	}
	cfg := config.FromEnv()

	var checks []doctorCheck
	record := func(status, name, detail string) {
		checks = append(checks, doctorCheck{status, name, detail})
	}

	// 1. herdr binary resolvable (PATH or HERDR_BIN).
	bin := cfg.HerdrBin
	if bin == "" {
		bin = "herdr"
	}
	var client *herdr.CLIClient
	resolved, err := exec.LookPath(bin)
	if err != nil {
		record("FAIL", "herdr binary", fmt.Sprintf("%s not usable: %v", bin, err))
	} else {
		record("PASS", "herdr binary", resolved)
		// LookPath already succeeded, so this cannot fail.
		client, _ = herdr.NewCLIClient(bin, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}

	// 2. herdr server reachable and new enough on every configured host.
	//    Unreachable or not running is a WARN (transient — the relay retries
	//    in the poll loop); a running-but-old server is a FAIL, matching the
	//    serve startup gate.
	if client != nil {
		ctx := context.Background()
		for _, host := range cfg.Hosts() {
			name := "herdr server (" + host + ")"
			st, err := client.ServerStatus(ctx, host)
			if err != nil {
				record("WARN", name, "unreachable: "+err.Error())
				continue
			}
			if !st.Running {
				record("WARN", name, "server not running")
				continue
			}
			if st.Protocol < herdr.MinProtocol || (st.Compatible != nil && !*st.Compatible) {
				record("FAIL", name, fmt.Sprintf("version %s protocol %d; herdr >= 0.9 (protocol >= %d) required", st.Version, st.Protocol, herdr.MinProtocol))
				continue
			}
			record("PASS", name, fmt.Sprintf("version %s, protocol %d", st.Version, st.Protocol))
		}
	}

	// 3. Token vs bind address — the same fail-closed rule serve enforces.
	switch {
	case !cfg.IsLoopbackBind() && cfg.Token == "":
		record("FAIL", "relay auth", fmt.Sprintf("HERDREM_BIND=%s without HERDREM_TOKEN would expose an unauthenticated relay", cfg.Bind))
	case cfg.Token == "":
		record("PASS", "relay auth", "no token needed on loopback bind")
	case len(cfg.Token) < 16:
		record("WARN", "relay auth", "HERDREM_TOKEN is short; relay auth has no failure lockout so a weak token can be guessed online")
	default:
		record("PASS", "relay auth", "token set")
	}

	// 4. cloudflared — only needed for --tunnel quick tunnels.
	if path, err := exec.LookPath("cloudflared"); err != nil {
		record("WARN", "cloudflared", "not in PATH (only needed for --tunnel)")
	} else {
		record("PASS", "cloudflared", path)
	}

	fails := 0
	for _, c := range checks {
		fmt.Fprintf(out, "%s  %s — %s\n", c.status, c.name, c.detail)
		if c.status == "FAIL" {
			fails++
		}
	}
	if fails > 0 {
		fmt.Fprintf(out, "%d check(s) failed\n", fails)
		return 1
	}
	fmt.Fprintln(out, "all checks passed")
	return 0
}
