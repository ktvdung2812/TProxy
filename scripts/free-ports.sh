#!/usr/bin/env bash
set -euo pipefail

PORTS=(28120 28122)

for port in "${PORTS[@]}"; do
  pids="$(lsof -t -iTCP:"${port}" -sTCP:LISTEN 2>/dev/null || true)"
  if [[ -z "${pids}" ]]; then
    continue
  fi
  echo "Freeing port ${port} (pid: ${pids//$'\n'/, })"
  # shellcheck disable=SC2086
  kill ${pids} 2>/dev/null || true
done

sleep 1

for port in "${PORTS[@]}"; do
  if lsof -t -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "Port ${port} is still in use" >&2
    exit 1
  fi
done
