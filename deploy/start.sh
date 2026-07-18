#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ -f "${ROOT}/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "${ROOT}/.env"
  set +a
fi

case "$(uname -m)" in
  x86_64|amd64)
    BIN="${ROOT}/bin/tproxy-linux-amd64"
    ;;
  aarch64|arm64)
    BIN="${ROOT}/bin/tproxy-linux-arm64"
    ;;
  *)
    echo "Unsupported Linux architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

if [[ ! -x "${BIN}" ]]; then
  echo "Missing executable binary: ${BIN}" >&2
  exit 1
fi

exec "${BIN}" --config "${ROOT}/config.yaml"
