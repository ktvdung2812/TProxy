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

echo "tproxy backend → http://127.0.0.1:28120"
exec go run ./cmd/tproxy --config config.yaml
