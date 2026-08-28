package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"herdrem/internal/config"
	"herdrem/internal/herdr"
	"herdrem/internal/httpserver"
	"herdrem/internal/hub"
	"herdrem/internal/poll"
	"herdrem/internal/protocol"
	"herdrem/internal/service"
	"herdrem/internal/store"
	webdist "herdrem/webdist"
)

func main() {
	// Skip subcommand "serve" if present — flag.Parse stops at first non-flag arg
	var subcmd string
	rawArgs := os.Args[1:]
	if len(rawArgs) > 0 && !strings.HasPrefix(rawArgs[0], "-") {
		subcmd = rawArgs[0]
		os.Args = append(os.Args[:1], rawArgs[1:]...)
	}
	_ = subcmd // "serve" is the default/only command for now

	// Flags
	web := flag.Bool("web", false, "serve embedded SPA")
	tunnel := flag.Bool("tunnel", false, "launch cloudflared quick tunnel")
	requireHerdr := flag.Bool("require-herdr", false, "exit instead of falling back to fake client when herdr CLI is unavailable (production)")
	staticDir := flag.String("static", "", "override embedded static dir (debug)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.FromEnv()
	if *staticDir != "" {
		cfg.StaticDir = *staticDir
	}
	cfg.Web = *web
	cfg.Tunnel = *tunnel
	cfg.RequireHerdr = *requireHerdr

	logger.Info("starting Herdrem", "addr", cfg.Addr(), "web", cfg.Web, "tunnel", cfg.Tunnel, "data_dir", cfg.DataDir)

	// --tunnel requires a token — never expose an unauthenticated relay to the public internet
	if cfg.Tunnel && cfg.Token == "" {
		logger.Error("--tunnel requires HERDREM_TOKEN to be set (refusing to expose unauthenticated relay)")
		os.Exit(1)
	}

	// Fail-closed: a non-loopback bind without a token exposes an unauthenticated
	// relay (WS, terminal input, approvals, file/command access) to the network.
	if !cfg.IsLoopbackBind() && cfg.Token == "" {
		logger.Error("HERDREM_TOKEN is required when binding to a non-loopback address (refusing to expose unauthenticated relay)",
			"bind", cfg.Bind, "hint", "set HERDREM_BIND=127.0.0.1 for local-only, or set HERDREM_TOKEN")
		os.Exit(1)
	}

	// Ensure data dir exists
	_ = os.MkdirAll(cfg.DataDir, 0o755)

	// Stores
	todoStore, err := store.New(cfg.DataDir + "/todo.json")
	if err != nil {
		logger.Error("todo store init", "err", err)
	}
	prefsStore, err := store.NewPrefsStore(cfg.DataDir + "/prefs.json")
	if err != nil {
		logger.Error("prefs store init", "err", err)
	}

	// Hub
	h := hub.New(logger)

	// Herdr client — real CLI when available, fake otherwise (dev).
	// In production (--require-herdr), a missing CLI is a fatal error so
	// the relay doesn't silently serve fake data.
	var client herdr.Client
	if cli, err := herdr.NewCLIClient(cfg.HerdrBin, logger); err != nil {
		if cfg.RequireHerdr {
			logger.Error("herdr CLI unavailable and --require-herdr is set (refusing to start with fake client)",
				"err", err, "hint", "install herdr and set HERDR_BIN, or remove --require-herdr for dev mode")
			os.Exit(1)
		}
		logger.Warn("herdr CLI unavailable, using fake client", "err", err)
		client = &herdr.FakeClient{}
	} else {
		logger.Info("using herdr CLI", "bin", cfg.HerdrBin, "hosts", cfg.Hosts())
		client = cli
	}

	// Poller (per-host + kinds filter; also serves as the pane router)
	poller := poll.New(client, cfg.Hosts(), cfg.Kinds(), h, logger, 0)
	poller.SetStatePath(cfg.DataDir + "/poller_state.json")

	// File service (sandboxed reads/listings; roots from the poller's panes)
	fileSvc := service.NewFileService(poller.CwdRoots, logger)
	poller.SetFileService(fileSvc)

	// Static FS
	var staticFS fs.FS
	if cfg.Web {
		if cfg.StaticDir != "" {
			staticFS = os.DirFS(cfg.StaticDir)
			logger.Info("using static dir override", "path", cfg.StaticDir)
		} else {
			staticFS, err = fs.Sub(webdist.All, ".")
			if err != nil {
				logger.Error("embed static", "err", err)
			} else {
				logger.Info("serving embedded web")
			}
		}
	}

	// Service (command dispatch; poller routes commands to the right host)
	svc := service.New(client, poller, todoStore, prefsStore, fileSvc, h, logger)
	svc.SetContentSubscriber(poller)
	svc.SetAllowedHosts(cfg.Hosts())

	// HTTP server
	srv := httpserver.New(cfg, h, client, todoStore, svc, fileSvc, poller, staticFS, logger)
	portInt, err := cfg.ParsePort()
	if err != nil {
		logger.Error("invalid port", "port", cfg.Port, "err", err)
		os.Exit(1)
	}

	// Tunnel Service for dynamic port forwarding
	tunnelSvc := service.NewTunnelService(portInt, func(info protocol.TunnelInfo) {
		h.BroadcastJSON(protocol.TunnelUpdated{Type: "tunnel_updated", Tunnel: info})
	}, logger)
	svc.SetTunnels(tunnelSvc)
	srv.SetTunnels(tunnelSvc)

	httpSrv := &http.Server{
		Addr:    cfg.Addr(),
		Handler: srv.Routes(),
	}

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start hub
	go h.Run(ctx)

	// Start poller
	go poller.Run(ctx)

	// Tunnel error channel — if --tunnel is requested and cloudflared fails, cancel server
	tunnelErrCh := make(chan error, 1)
	if cfg.Tunnel {
		go startTunnel(ctx, cfg.Port, logger, tunnelErrCh)
	}

	// HTTP server error channel — listen failure must exit non-zero
	httpErrCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr())
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			httpErrCh <- err
		}
	}()

	// Wait for signal, HTTP error, or tunnel error
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	exitCode := 0
	select {
	case <-sigCh:
		logger.Info("shutting down (signal)…")
	case err := <-httpErrCh:
		logger.Error("http server fatal error", "err", err)
		exitCode = 1
	case err := <-tunnelErrCh:
		logger.Error("tunnel fatal error", "err", err)
		exitCode = 1
	}
	cancel()
	_ = httpSrv.Shutdown(context.Background())
	tunnelSvc.Shutdown() // F4: terminate all cloudflared processes on exit
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

// startTunnel launches cloudflared quick tunnel and prints the wss:// URL.
// On failure (cloudflared missing or exits), sends error to errCh so main can exit.
func startTunnel(ctx context.Context, port string, logger *slog.Logger, errCh chan<- error) {
	if _, err := exec.LookPath("cloudflared"); err != nil {
		logger.Error("cloudflared not found in PATH",
			"hint", "install: https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/")
		errCh <- fmt.Errorf("cloudflared not found: %w", err)
		return
	}
	cmd := exec.CommandContext(ctx, "cloudflared", "tunnel", "--url", "http://localhost:"+port)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	logger.Info("starting cloudflared quick tunnel")
	if err := cmd.Run(); err != nil {
		logger.Error("cloudflared exited", "err", err)
		errCh <- fmt.Errorf("cloudflared exited: %w", err)
	}
}
