<p align="center">
  <img src="web/public/brand-mark.png" alt="herdrem" width="128">
</p>

# herdrem

Monitor and approve [herdr](https://herdr.dev) agents from your phone or desktop browser — no SSH required.

A single Go binary (`Herdrem`) serves a Vue 3 PWA and a WebSocket relay that bridges your local herdr agents to any browser, on any device.

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

## Configuration

All config is environment-based (see `internal/config/config.go`):

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

- **No token configured** (local dev): a same-origin page opened at `localhost`,
  `127.0.0.1`, or `::1` may connect without a token.
- **Token configured** (LAN, `--tunnel`, or you set `HERDREM_TOKEN` even on
  loopback): every connection must present it — including localhost. The PWA
  prompts on first connect; a 401 from `/session-auth` also surfaces the prompt
  after boot timeout.

The token is kept in `sessionStorage` keyed by relay URL (never
`localStorage`): it survives page reloads in the tab — including relay
restarts — and dies with the tab. Same-origin JavaScript can read it, so XSS
hygiene still matters; deleting the relay entry, changing its URL, or a 401
rejection drops the stored token. The PWA does not put it in the WebSocket
or `/session-auth` URL (it uses a WebSocket subprotocol and
`Authorization: Bearer`). Query `?token=` is not accepted — paste the token
into the relay switcher field instead. Same-origin login mints an HttpOnly
**opaque session** cookie (not the raw token) so a reload can reconnect;
`/session-logout` or clearing site data revokes it. Tokens entered on one
page origin do not follow the PWA to another origin — a relay opened from
its own origin needs the token entered there once. File downloads use a
`POST` form field, which is also why a file/image preview across origins
(no cookie, no header channel) requires opening the PWA from the relay's
own origin.

Relay auth failures always return HTTP 401 — relay endpoints never
rate-limit or block client IPs (the token's entropy makes online brute force
infeasible, and per-IP blocks wedge legitimate clients behind a shared
egress). Use a high-entropy token (`openssl rand -hex 16`); a startup
warning fires for short ones. HTTP 429 is still emitted by the tunnel gate's
PIN/Basic-Auth limiter, which guards short user-chosen credentials.

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

## License

See [LICENSE](LICENSE) file for details.

## Deployment

Install the relay on the host with the systemd user service. Docker is only
supported for serving the static Web/PWA; it does not run the relay. See
[deploy/README.md](deploy/README.md).
