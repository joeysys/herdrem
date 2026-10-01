<p align="center">
  <img src="web/public/brand-mark.png" alt="herdrem" width="128">
</p>

# herdrem

Monitor and approve [herdr](https://herdr.dev) agents from your phone or desktop browser — no SSH required.

A single Go binary (`Herdrem`) serves a Vue 3 PWA and a WebSocket relay that bridges your local [herdr](https://github.com/herdrdev/herdr) agents to any browser, on any device.

## Features

- **Web PWA** — approve blocked agents with one tap; works as a mobile PWA and a desktop web app
- **Live terminal** — stream agent output, send input over WebSocket
- **File browser** — navigate, preview, and edit files remotely
- **Cloudflare tunnels** — one-flag public exposure with token/basic-auth/PIN gates
- **HTML preview** — sandboxed iframe preview of HTML files with CSP hardening
- **Token auth** — when `HERDREM_TOKEN` is unset (dev), only a same-origin page on `localhost` / `127.0.0.1` / `::1` may connect without a token. Once the token is set, **every** connection needs it, including localhost.
- **15 themes** — default, modern, herdr, e-ink, sand, clay, dune, nord, rose, dracula, kanagawa, midnight, dark, light, modern-dark

## Screenshots

<!-- Screenshots pending — reserved slots:
<p align="center">
  <img src="assets/screenshots/desktop.png" alt="Desktop web" width="720">
</p>
<p align="center">
  <img src="assets/screenshots/mobile.png" alt="Mobile PWA" width="300">
</p>
-->

## Quick Start

```bash
# Build web + Go binary
make build

# Run the relay locally (loopback only)
./herdrem serve --web

# Or dev mode (hot web rebuild via Vite watch)
make dev
```

For same-LAN access from a phone or another machine, opt in to a non-loopback bind and set a token.
Generate the token, print it once so you can enter it in the browser, and start the relay:

```bash
export HERDREM_TOKEN="$(openssl rand -hex 16)"
echo "Relay token: $HERDREM_TOKEN"
HERDREM_BIND=0.0.0.0 ./herdrem serve --web
```

Open `http://<lan-ip>:8766` in a browser on the phone or another device and enter the displayed token when the PWA
prompts for it. The token is required even though the page is served by the
relay, because the LAN address is not an exempt local host. Note that the relay
does not print the token in its startup logs.

Or expose via Cloudflare tunnel (also requires a token):

```bash
export HERDREM_TOKEN="$(openssl rand -hex 16)"
echo "Relay token: $HERDREM_TOKEN"
./herdrem serve --web --tunnel
```

## Deployment

On a Linux host, the installer script sets everything up in one step — it
builds `herdrem` (or uses the bundled binary), writes the env file, and
installs and enables a systemd user service with user lingering so the relay
survives logout and reboot:

```bash
./deploy/install.sh            # from a source checkout
./deploy/install.sh --binary   # from a binary bundle (herdrem next to deploy/)
./deploy/install.sh --dry-run  # preview the actions without changing anything
```

**Without systemd**, the relay runs as a foreground process: `scripts/start.sh`
builds web + Go and execs `herdrem serve --web`, and `scripts/dev.sh` does the
same with a Vite watch. Configuration comes from the process environment plus
`~/.config/herdrem/herdrem.env`, which the binary loads automatically — see
[Configuration](#configuration). Keeping it running across logouts without
systemd is up to you (`tmux`, `nohup`, a supervisor of choice, etc.).

Docker is only supported for serving the static Web/PWA; it does not run the
relay. See [deploy/README.md](deploy/README.md) for the remaining options
(`--no-linger`, `HERDR_BIN`), the equivalent manual steps, and tunnel mode.

## Configuration

All config is environment-based (see `internal/config/config.go`). The binary
reads `HERDREM_*`/`HERDR_*` variables from its process environment, and also
loads `~/.config/herdrem/herdrem.env` itself when the file exists
(dotenv-style: `KEY=VALUE`, `#` comments, quotes allowed). File values only
fill in variables that aren't already set, so a real environment always wins —
the same precedence systemd applies between `Environment=` and
`EnvironmentFile=`, which is why the installed unit keeps working unchanged.

```bash
./herdrem serve --web                       # auto-loads ~/.config/herdrem/herdrem.env
./herdrem serve --web --env-file /etc/herdrem.env  # explicit path (must exist)
HERDREM_PORT=9000 ./herdrem serve --web     # env vars still override the file
```

| Env | Default | Description |
|-----|---------|-------------|
| `HERDREM_PORT` | `8766` | Listen port |
| `HERDREM_BIND` | `127.0.0.1` | Listen address; non-loopback binds require a token |
| `HERDREM_TOKEN` | _(empty = localhost same-origin only)_ | Shared secret. Empty is allowed only for loopback same-origin with no token configured; if this is set, localhost needs it too |
| `HERDR_BIN` | `herdr` | Path to herdr binary |
| `HERDR_REMOTES` | — | Comma-separated SSH remotes (optional) |
| `HERDREM_KINDS` | — | Comma-separated agent kind filter (optional) |
| `HERDREM_DATA_DIR` | `~/.config/herdrem` | Data directory |
| `HERDREM_STATIC` | — | Override embedded static dir (debug) |
| `HERDREM_TUNNEL_PORTS` | — | Extra allowed ports/ranges on top of `5173,3000,8080,4173` (e.g. `8000-8100,9000`). Privileged ports (`<1024`) and malformed tokens are ignored |
| `HERDREM_ALLOW_DIRECT_TUNNEL` | off | Set to `1` to allow ungated (`direct`) port tunnels |
| `HERDREM_TUNNEL_MAX` | `8` | Cap on concurrent starting+active port tunnels |

### Relay authentication

- **No token** (local dev): only a same-origin page on `localhost` /
  `127.0.0.1` / `::1` may connect.
- **Token set**: every connection needs it — including localhost. Non-loopback
  binds and `--tunnel` refuse to start without one; generate it with
  `openssl rand -hex 16` (short tokens trigger a startup warning).

The PWA prompts for the token on first connect — enter it in the login field
or relay settings; `?token=` URLs are not accepted. The token is stored
per-relay in tab `sessionStorage` (never `localStorage`): it survives reloads
but dies with the tab, and a 401 or deleting/changing the relay drops it.
Same-origin login also mints an HttpOnly session cookie (not the token) with a
7-day idle limit. The PWA renews that cookie on each connect and every 30
minutes while the socket stays up, so a phone that opens the installed app
daily does not re-prompt; a week without a successful connect does.

### Port forwarding (Cloudflare quick tunnels)

From the PWA tunnel panel (or `tunnel_create` over the relay WebSocket), the
relay can publish a local HTTP port as `https://*.trycloudflare.com`.

| Rule | Default |
|------|---------|
| Allowed ports | `5173`, `3000`, `8080`, `4173` |
| Extra ports | `HERDREM_TUNNEL_PORTS` — singles or ranges (`8000-8100,9000`). Malformed tokens are ignored. Privileged ports (`<1024`) are never forwarded |
| `direct` (no PIN/Basic gate) | Off. Set `HERDREM_ALLOW_DIRECT_TUNNEL=1` to show/enable it. Anyone with the URL then has full access to that port |
| Concurrent tunnels | 8 (`HERDREM_TUNNEL_MAX`) |

`token_gate` (default) and `basic_auth` still sit in front of the published
port. `--tunnel` on `herdrem serve` is the **relay itself**, not these
per-port forwards; it still requires `HERDREM_TOKEN`.

## Development

```bash
# Frontend
cd web && npm install && npm run dev    # Vite dev server

# Backend
go build ./cmd/herdrem                  # Build binary
go test ./...                           # Run tests

# Full dev loop (Vite watch + Go binary serving webdist/)
make dev
```

## Architecture

```
cmd/herdrem/main.go      Entry point — flags, config, server bootstrap
internal/
  config/                Env-based config
  herdr/                 herdr CLI/protocol client
  httpserver/            HTTP server, WebSocket hub, tunnel gate, file preview
  hub/                   Pub/sub hub for agent events
  poll/                  herdr poll loop
  protocol/              Wire protocol types
  service/               Tunnel service (cloudflared), agent service
  store/                 In-memory state store
  auth/                  Token + PIN auth (constant-time compare)
web/                     Vue 3 + Pinia + Vite PWA
webdist/                 Embedded build output (go:embed)
scripts/                 dev.sh (hot reload), start.sh (one-shot)
deploy/                  systemd unit, env template, deployment guide
```

## License

See [LICENSE](LICENSE) file for details.

## Acknowledgements

Inspired by [herdr-remote](https://github.com/dcolinmorgan/herdr-remote).
