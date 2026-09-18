# Deployment

## systemd (recommended for Linux servers)

### One-command install

Install `herdr` first for the user who will run the relay, then run this from
the repository root:

```bash
./deploy/install.sh
```

The installer builds `herdrem`, installs it, discovers the actual `herdr`
executable from the current user's PATH, persists the selected executable and
both directories for the systemd user manager, and enables `herdrem.service`.
It renders the actual Herdrem path into `ExecStart`, preserves an existing
customized user unit, and never starts or supervises `herdr server`.

If `herdr` is not on PATH, provide its actual path without changing the unit:

```bash
HERDR_BIN=/path/to/herdr ./deploy/install.sh
```

Preview the resolved paths and planned actions without building, writing
configuration, or touching systemd:

```bash
./deploy/install.sh --dry-run
```

Add `--linger` if the relay must keep running after logout:

```bash
./deploy/install.sh --linger
```

The remaining steps describe the equivalent manual installation.

### 1. Install the binaries

This deployment uses a systemd **user** unit. Install Herdrem and Herdr
for the same login user. Their locations are not hardcoded: both commands
must be discoverable on the current shell's PATH during installation; the
installer persists the resolved executable and PATH for the user manager.

```bash
make build
mkdir -p ~/.local/bin
install -m 0755 herdrem ~/.local/bin/herdrem
# Install Herdr separately: https://herdr.dev/docs/install/
command -v herdrem
command -v herdr
```

The relay uses the current user's `HOME` and Herdr Unix socket, so it can
see that user's existing agents. It does not start or supervise `herdr
server`. A Herdr server and the target agents must already run in this
same user context; another user's session is not visible. If `herdr` is
installed outside the current PATH, add that directory to PATH before the
`command -v` checks. For the one-command installer, you can instead pass
`HERDR_BIN=/path/to/herdr`. See the
[Herdr CLI reference](https://herdr.dev/docs/cli-reference/) and
[socket paths](https://herdr.dev/docs/socket-api/) for details.

### 2. Configure the environment file

```bash
mkdir -p ~/.config/herdrem
cp deploy/herdrem.env.example ~/.config/herdrem/herdrem.env
chmod 600 ~/.config/herdrem/herdrem.env

# Generate a secure token and save it to the env file:
TOKEN="$(openssl rand -hex 16)"
sed -i.bak "s/^#\? \?HERDREM_TOKEN=.*/HERDREM_TOKEN=$TOKEN/" ~/.config/herdrem/herdrem.env && rm -f ~/.config/herdrem/herdrem.env.bak
echo "Configured HERDREM_TOKEN: $TOKEN"
unset TOKEN

# Persist the actual install directories for the user unit:
herdrem_bin_dir="$(dirname "$(command -v herdrem)")"
herdr_bin_dir="$(dirname "$(command -v herdr)")"
printf '\nPATH=%s:%s:/usr/local/bin:/usr/bin:/bin\n' "$herdrem_bin_dir" "$herdr_bin_dir" >> ~/.config/herdrem/herdrem.env
unset herdrem_bin_dir herdr_bin_dir
```

The user unit requires this file. The PATH line above is stored in the
file, so it remains available after logout, linger, or reboot. It defaults to
`HERDREM_BIND=127.0.0.1` (loopback only); change to `HERDREM_BIND=0.0.0.0` for LAN access.
If `HERDREM_TOKEN` is set (recommended for any install that is not throwaway
dev), the PWA prompts for it even on `http://localhost:8766`. Leave the token
empty only for local same-origin development. LAN, `--tunnel`, and other
origins always need the token.
You can view the saved token at any time by running:

```bash
grep '^HERDREM_TOKEN=' ~/.config/herdrem/herdrem.env
```

### 3. Install and start the user unit

```bash
mkdir -p ~/.config/systemd/user
cp deploy/herdrem.service ~/.config/systemd/user/herdrem.service
systemctl --user daemon-reload
systemctl --user enable --now herdrem.service
```

To keep the relay running after logout, enable user lingering:

```bash
loginctl enable-linger "$USER"
```

### 4. Verify

```bash
systemctl --user status herdrem
journalctl --user -u herdrem -f
curl http://localhost:8766
```

If the service fails, check the user journal for a missing `herdr` binary,
environment file, or unavailable Herdr socket.

### Tunnel mode

Install `cloudflared` using the [official Cloudflare instructions](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/), then verify:

```bash
command -v cloudflared && cloudflared --version
```

Edit the user unit and add `--tunnel`:

```bash
systemctl --user edit --full herdrem
# Change ExecStart to:
#   ExecStart=herdrem serve --web --require-herdr --tunnel
```

`HERDREM_TOKEN` is **required** when `--tunnel` is set. If not already set,
generate and save it in `~/.config/herdrem/herdrem.env` before reloading:

```bash
TOKEN="$(openssl rand -hex 16)"
sed -i.bak "s/^#\? \?HERDREM_TOKEN=.*/HERDREM_TOKEN=$TOKEN/" ~/.config/herdrem/herdrem.env && rm -f ~/.config/herdrem/herdrem.env.bak
echo "Tunnel relay token: $TOKEN"
unset TOKEN

systemctl --user daemon-reload
systemctl --user restart herdrem
journalctl --user -u herdrem -f  # look for the wss://*.trycloudflare.com URL
```

See [README](../README.md#configuration) for all env vars.

---

## Docker (Web/PWA only)

Docker serves the static Web/PWA only. It does not run the relay, does not
contain `herdr`, does not use `FakeClient`, and does not need a relay token or
host socket. Install and run the real relay on the host with the systemd user
service above.

### Build and run

```bash
docker compose up -d --build
```

Open `http://<host>:8080`. In the PWA relay switcher, add the separately
installed relay address, for example `ws://<host>:8766`, and enter its
`HERDREM_TOKEN` when prompted. The static Web container has no `/ws`,
`/api`, or file service of its own.

For a browser on another machine, configure the host relay with
`HERDREM_BIND=0.0.0.0` and a token in its systemd environment file. The relay
must be reachable from the browser; the Docker container does not proxy relay
traffic.

When the PWA and relay use different origins, source file previews and relay
control continue to work, but rendered HTML previews require opening the PWA
from the relay's own origin because preview capability minting is same-origin
only.

See [docker-compose.yml](../docker-compose.yml) for the Web-only container.

---

## Security notes

- **Always set `HERDREM_TOKEN`** for anything that is not throwaway local
  dev. Once it is set, localhost needs it too (the PWA will prompt). An empty
  token is accepted only for same-origin `localhost`/`127.0.0.1`/`::1` when
  no token is configured at all.
- The systemd unit defaults to `HERDREM_BIND=127.0.0.1` (loopback only).
  Set `HERDREM_BIND=0.0.0.0` in the env file for LAN access — the relay
  refuses to start with a non-loopback bind and an empty token.
- `--tunnel` also requires `HERDREM_TOKEN`.
- The systemd unit uses `--require-herdr` so it fails loudly if the
  `herdr` CLI is not installed, instead of silently serving fake data.
- The env file is required (not optional) — the user unit fails to start
  without it. It should be `chmod 600` and readable only by the user.
  `~/.config/herdrem` is created `0700`.
- The user unit runs as the current login user, so it can access that
  user's Herdr socket/session.
- **Token persistence**: the token stays in tab memory, never `localStorage`.
  Same-origin login mints an HttpOnly **opaque session** cookie (not the raw
  token). Reloads reuse it; clear site data or a new token revokes it.
  The PWA never puts the token in a URL; query `?token=` is not accepted.
- **Port forwards** (PWA tunnel panel): only `5173,3000,8080,4173` plus
  `HERDREM_TUNNEL_PORTS` (singles or `8000-8100` ranges). Ports `<1024` are
  never forwarded. `direct` (no gate) stays off unless
  `HERDREM_ALLOW_DIRECT_TUNNEL=1`. At most `HERDREM_TUNNEL_MAX` (default 8)
  forwards at once. See [README configuration](../README.md#configuration).
- Failed relay logins always return HTTP 401 — there is no per-IP lockout
  (a reconnecting client must never wedge its own egress IP). HTTP 429 is
  only returned by the tunnel gate's PIN/Basic-Auth limiter.
- **Docker vs host boundary**: Docker serves only the Web/PWA. The host systemd
  service is the only supported relay deployment and is where the real `herdr`
  CLI, user socket, agents, and relay token live.
