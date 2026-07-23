#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/npm"

if [[ -z "${NPM_TOKEN:-}" ]]; then
  echo "Set NPM_TOKEN first (granular token with publish + bypass 2FA enabled)."
  echo "Example: NPM_TOKEN=npm_xxx ./scripts/publish-npm.sh"
  exit 1
fi

TMP_NPMRC="$(mktemp)"
trap 'rm -f "$TMP_NPMRC"' EXIT
printf '%s\n' "//registry.npmjs.org/:_authToken=${NPM_TOKEN}" >"$TMP_NPMRC"

echo "Publishing as $(npm whoami --userconfig "$TMP_NPMRC")..."
npm publish --userconfig "$TMP_NPMRC" --access public
echo "Done: https://www.npmjs.com/package/@ktvdung1606/tproxy"
