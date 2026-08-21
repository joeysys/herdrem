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

# Run the relay (serves PWA on :8766)
./herdrem serve --web

# Or dev mode (hot web rebuild via Vite watch)
make dev
```

Then open `http://localhost:8766` in a browser on your phone or another device (same LAN), or expose via Cloudflare tunnel:

```bash
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
- **Token auth** — optional shared secret (`HERDREM_TOKEN`); when unset, auth is disabled (dev mode). Tunnels auto-generate a 128-bit token for the tunnel gate.
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
| `HERDREM_TOKEN` | _(empty = auth disabled)_ | Shared secret for relay auth |
| `HERDR_BIN` | `herdr` | Path to herdr binary |
| `HERDR_REMOTES` | — | Comma-separated SSH remotes (optional) |
| `HERDREM_KINDS` | — | Comma-separated agent kind filter (optional) |
| `HERDREM_DATA_DIR` | `~/.config/herdrem` | Data directory |
| `HERDREM_STATIC` | — | Override embedded static dir (debug) |
| `HERDR_ALLOWED_ORIGINS` | _(empty = same-origin only)_ | Comma-separated browser origins allowed to open a WebSocket cross-origin |

### Cross-origin WebSocket

The relay enforces a same-origin check on the `/ws` upgrade, so a UI served from
one host cannot drive a relay on another host by default — including via the
built-in relay switcher, which is cross-origin by nature. Two ways to authorize it:

- **Set `HERDREM_TOKEN`** on the target relay and enter that token in the
  relay switcher. A valid token authorizes any origin, because the token itself
  is the CSRF defense. This is the right choice for tunnels and public deployments.
- **Set `HERDR_ALLOWED_ORIGINS`** on the target relay, e.g.
  `HERDR_ALLOWED_ORIGINS=localhost:8765`. Useful for a token-less trusted LAN or
  dev setup. Entries are host patterns (`localhost:8765`, `*.example.com`); an
  entry containing `://` is matched against `scheme://host`.

With neither set, only same-origin connections are accepted. Do not disable this
casually: a relay reachable cross-origin without a token can be driven by any
website the user happens to visit.

## License

See [LICENSE](LICENSE) file for details.

## Deployment

For production deployments (systemd, Docker, docker-compose), see
[deploy/README.md](deploy/README.md).
