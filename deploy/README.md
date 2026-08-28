# Deployment

## systemd (recommended for Linux servers)

### 1. Install the binaries

This deployment uses a systemd **user** unit. Install Herdrem and Herdr
for the same login user. Their locations are not hardcoded: both commands
must be available on the PATH of the systemd user manager.

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
`command -v` checks and import it. See the
[Herdr CLI reference](https://herdr.dev/docs/cli-reference/) and
[socket paths](https://herdr.dev/docs/socket-api/) for details.

### 2. Configure the environment file

```bash
mkdir -p ~/.config/herdrem
cp deploy/herdrem.env.example ~/.config/herdrem/herdrem.env
chmod 600 ~/.config/herdrem/herdrem.env
# Set HERDREM_TOKEN for LAN or tunnel access.
# Persist the actual install directories for the user unit:
herdrem_bin_dir="$(dirname "$(command -v herdrem)")"
herdr_bin_dir="$(dirname "$(command -v herdr)")"
printf '\nPATH=%s:%s:/usr/local/bin:/usr/bin:/bin\n' "$herdrem_bin_dir" "$herdr_bin_dir" >> ~/.config/herdrem/herdrem.env
unset herdrem_bin_dir herdr_bin_dir
```

The user unit requires this file. The PATH line above is stored in the
file, so it remains available after logout, linger, or reboot. It defaults to
`HERDREM_BIND=127.0.0.1` (loopback only), which is safe without a token.

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

`HERDREM_TOKEN` is **required** when `--tunnel` is set. Set it in
`~/.config/herdrem/herdrem.env` before reloading:

```bash
systemctl --user daemon-reload
systemctl --user restart herdrem
journalctl --user -u herdrem -f  # look for the wss://*.trycloudflare.com URL
```

See [README](../README.md#configuration) for all env vars.

---

## Docker

### Build

```bash
docker build -t herdrem .
```

### Run with a token (required for the container's non-loopback bind)

```bash
docker run -d \
  --name herdrem \
  -p 127.0.0.1:8766:8766 \
  -e HERDREM_BIND=0.0.0.0 \
  -e HERDREM_TOKEN="$(openssl rand -hex 16)" \
  --restart unless-stopped \
  herdrem
```

### Run with persistent data

```bash
docker run -d \
  --name herdrem \
  -p 127.0.0.1:8766:8766 \
  -e HERDREM_BIND=0.0.0.0 \
  -e HERDREM_TOKEN="$(openssl rand -hex 16)" \
  -v herdrem-data:/var/lib/herdrem \
  --restart unless-stopped \
  herdrem
```

### docker-compose

```bash
HERDREM_TOKEN="$(openssl rand -hex 16)" docker compose up -d
```

See [docker-compose.yml](../docker-compose.yml) for the full configuration.

---

## Security notes

- **Always set `HERDREM_TOKEN`** for any non-loopback exposure.
- The systemd unit defaults to `HERDREM_BIND=127.0.0.1` (loopback only).
  Set `HERDREM_BIND=0.0.0.0` in the env file for LAN access — the relay
  refuses to start with a non-loopback bind and an empty token.
- The systemd unit uses `--require-herdr` so it fails loudly if the
  `herdr` CLI is not installed, instead of silently serving fake data.
- The env file is required (not optional) — the user unit fails to start
  without it. It should be `chmod 600` and readable only by the user.
- The user unit runs as the current login user, so it can access that
  user's Herdr socket/session.
- For tunnel deployments, the token is also the CSRF defense for
  cross-origin WebSocket.
