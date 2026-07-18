# tproxy deploy bundle

This directory is a single-server deployment bundle for `tproxy`.

## Contents

- `bin/tproxy-linux-amd64` and `bin/tproxy-linux-arm64`: Linux server binaries.
- `config.yaml`: runtime config for server use. It binds `0.0.0.0:28120` and stores SQLite data in `/data/tproxy.db`.
- `.env.example`: required environment variables and provider secret placeholders.
- `docker-compose.yml`: Docker deployment using the project Dockerfile.
- `start.sh`: direct binary launcher that auto-selects amd64 or arm64.
- `systemd/tproxy.service`: optional systemd service for `/opt/tproxy`.
- `RECOVERY.md`: backup, integrity-check and restore commands.

## Prepare

```bash
cp .env.example .env
./bin/tproxy-linux-amd64 --print-master-key
```

Put the generated key in `TPROXY_MASTER_KEY`, replace `TPROXY_API_KEY`, and replace `TPROXY_MANAGEMENT_SECRET`.

If you expose the dashboard/admin API remotely, set in `config.yaml`:

```yaml
server:
  allow-remote-management: true
```

Use TLS/reverse proxy in front of the service. For browser OAuth providers on a public server, set each provider `oauth.redirect-url` to:

```text
https://your-domain.example/api/admin/oauth/callback
```

## Run with Docker

```bash
cd deploy
cp .env.example .env
mkdir -p data
docker compose up -d --build
docker compose logs -f tproxy
```

Dashboard and API are served from the same port:

```text
http://SERVER_IP:28120/dashboard/
http://SERVER_IP:28120/v1
http://SERVER_IP:28120/healthz
```

## Run as a binary

Copy this directory to the server, for example `/opt/tproxy`, then:

```bash
cd /opt/tproxy
cp .env.example .env
chmod +x start.sh bin/tproxy-linux-*
mkdir -p /data
./start.sh
```

Optional systemd install:

```bash
sudo useradd --system --home /opt/tproxy --shell /usr/sbin/nologin tproxy
sudo mkdir -p /opt/tproxy /data
sudo cp -R . /opt/tproxy/
sudo chown -R tproxy:tproxy /opt/tproxy /data
sudo cp /opt/tproxy/systemd/tproxy.service /etc/systemd/system/tproxy.service
sudo systemctl daemon-reload
sudo systemctl enable --now tproxy
sudo systemctl status tproxy
```

## Operations

Health check:

```bash
curl http://127.0.0.1:28120/healthz
```

Consistent SQLite backup:

```bash
./bin/tproxy-linux-amd64 --config config.yaml --backup-database backups/tproxy-$(date +%Y%m%d-%H%M%S).db
```

See `RECOVERY.md` for restore and integrity-check steps.
