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

For same-LAN access from a phone or another machine, opt in to a non-loopback bind and set a token:

```bash
HERDREM_BIND=0.0.0.0 HERDREM_TOKEN="$(openssl rand -hex 16)" ./herdrem serve --web
```

Open `http://<lan-ip>:8766` in a browser on the phone or another device and enter the token when the PWA
prompts for it. The token is required even though the page is served by the
relay, because the LAN address is not an exempt local host.

Or expose via Cloudflare tunnel (also requires a token):

```bash
HERDREM_TOKEN="$(openssl rand -hex 16)" ./herdrem serve --web --tunnel
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
- **Token auth** — only same-origin requests addressed to `localhost`, `127.0.0.1`, or `::1` may omit `HERDREM_TOKEN`; all other relay connections require it.
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
| `HERDREM_TOKEN` | _(empty = local same-origin only)_ | Shared secret required for non-local or cross-origin relay access |
| `HERDR_BIN` | `herdr` | Path to herdr binary |
| `HERDR_REMOTES` | — | Comma-separated SSH remotes (optional) |
| `HERDREM_KINDS` | — | Comma-separated agent kind filter (optional) |
| `HERDREM_DATA_DIR` | `~/.config/herdrem` | Data directory |
| `HERDREM_STATIC` | — | Override embedded static dir (debug) |

### Relay authentication

The embedded PWA may connect without a token only when the browser page and relay
are same-origin and the relay host is `localhost`, `127.0.0.1`, or `::1`. Connections via
a LAN IP, hostname, tunnel, separate UI, or non-browser client must present the
configured `HERDREM_TOKEN`. The PWA asks for it on first connection; the
relay switcher also accepts it when connecting to a non-local relay.

## License

See [LICENSE](LICENSE) file for details.

## Deployment

For production deployments (systemd, Docker, docker-compose), see
[deploy/README.md](deploy/README.md).
