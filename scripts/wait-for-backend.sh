#!/usr/bin/env bash
set -euo pipefail

URL="${TPROXY_HEALTH_URL:-http://127.0.0.1:28122/healthz}"
ATTEMPTS="${TPROXY_HEALTH_ATTEMPTS:-120}"

for ((attempt = 1; attempt <= ATTEMPTS; attempt++)); do
  if curl -sf "$URL" >/dev/null 2>&1; then
    echo "backend ready → $URL"
    exit 0
  fi
  sleep 0.25
done

echo "backend did not become ready at $URL" >&2
exit 1
