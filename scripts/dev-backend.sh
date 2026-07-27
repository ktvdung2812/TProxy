#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ -f .env.run ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env.run
  set +a
fi

if [[ ! -f config.yaml ]]; then
  cp config.example.yaml config.yaml
  echo "Created config.yaml from config.example.yaml"
fi

BACKEND_PORT="${TPROXY_DEV_BACKEND_PORT:-28122}"
PUBLIC_PORT="${TPROXY_PUBLIC_PORT:-28120}"

awk -v backend_port="${BACKEND_PORT}" '
  BEGIN { in_server = 0 }
  /^server:[[:space:]]*$/ { in_server = 1; print; next }
  in_server && /^[^[:space:]]/ { in_server = 0 }
  in_server && /^[[:space:]]+port:[[:space:]]*/ {
    sub(/port:[[:space:]]*[0-9]+/, "port: " backend_port)
    in_server = 0
  }
  { print }
' config.yaml > .config.dev.yaml

export TPROXY_PUBLIC_PORT="${PUBLIC_PORT}"
export TPROXY_SKIP_TUNNEL_AUTO=1

echo "tproxy dev backend → http://127.0.0.1:${BACKEND_PORT}"
echo "public entry (dashboard + API) → http://127.0.0.1:${PUBLIC_PORT}"
exec go run ./cmd/tproxy --config .config.dev.yaml
