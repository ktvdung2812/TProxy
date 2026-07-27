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

build_web=true
for arg in "$@"; do
  case "$arg" in
    --backend-only) build_web=false ;;
  esac
done

if $build_web; then
  npm --prefix web run build
fi

go build -o bin/tproxy ./cmd/tproxy

# Dev stack: Go backend on 28122, Vite on 28120. Never kill 28120 in this mode.
if lsof -t -iTCP:28122 -sTCP:LISTEN >/dev/null 2>&1; then
  echo "dev mode: restarting backend on 28122 (keeping Vite on 28120)"
  kill "$(lsof -t -iTCP:28122 -sTCP:LISTEN)" 2>/dev/null || true
  sleep 1
  export TPROXY_DEV_BACKEND_PORT="${TPROXY_DEV_BACKEND_PORT:-28122}"
  export TPROXY_PUBLIC_PORT="${TPROXY_PUBLIC_PORT:-28120}"
  export TPROXY_SKIP_TUNNEL_AUTO=1
  exec bash scripts/dev-backend.sh
fi

echo "production mode: restarting tproxy on 28120"
kill "$(lsof -t -iTCP:28120 -sTCP:LISTEN)" 2>/dev/null || true
sleep 1
export TPROXY_API_KEY="${TPROXY_API_KEY:-change-me}"
exec ./bin/tproxy --config config.yaml
