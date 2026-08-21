# Deployment

## systemd (recommended for Linux servers)

### 1. Build and install the binary

```bash
make build
sudo install -m 0755 herdrem /usr/local/bin/herdrem
```

### 2. Create a dedicated user and data directory

```bash
sudo useradd -r -s /usr/sbin/nologin -d /var/lib/herdrem herdrem
sudo mkdir -p /var/lib/herdrem
sudo chown herdrem:herdrem /var/lib/herdrem
```

### 3. Install the systemd unit

```bash
sudo mkdir -p /etc/herdrem
sudo cp deploy/herdrem.env.example /etc/herdrem/herdrem.env
sudo chmod 600 /etc/herdrem/herdrem.env
# Edit /etc/herdrem/herdrem.env and set HERDREM_TOKEN
sudo openssl rand -hex 16  # generate a token

sudo cp deploy/herdrem.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now herdrem
```

### 4. Verify

```bash
systemctl status herdrem
journalctl -u herdrem -f
curl http://localhost:8766
```

### Tunnel mode

To expose the relay publicly via Cloudflare, add `--tunnel` to `ExecStart`:

```ini
ExecStart=/usr/local/bin/herdrem serve --web --tunnel
```

`HERDREM_TOKEN` is **required** when `--tunnel` is set — the relay refuses
to start without it. See [README](../README.md#configuration) for all env vars.

---

## Docker

### Build

```bash
docker build -t herdrem .
```

### Run (local only, no auth)

```bash
docker run -p 8766:8766 herdrem
```

### Run with token and persistent data

```bash
docker run -d \
  --name herdrem \
  -p 8766:8766 \
  -e HERDREM_TOKEN=$(openssl rand -hex 16) \
  -v herdrem-data:/var/lib/herdrem \
  --restart unless-stopped \
  herdrem
```

### docker-compose

```bash
docker compose up -d
```

See [docker-compose.yml](docker-compose.yml) for the full configuration.

---

## Security notes

- **Always set `HERDREM_TOKEN`** for any non-loopback exposure.
- The systemd unit runs under a dedicated `herdrem` user with `ProtectSystem=strict` and `NoNewPrivileges`.
- The env file should be `chmod 600` and owned by root — it contains the relay token.
- For tunnel deployments, the token is also the CSRF defense for cross-origin WebSocket.
