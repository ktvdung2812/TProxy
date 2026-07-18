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

if [[ ! -x bin/tproxy ]]; then
  echo "bin/tproxy not found — run: npm run build"
  exit 1
fi

echo "tproxy backend → http://127.0.0.1:28120"
exec ./bin/tproxy --config config.yaml
