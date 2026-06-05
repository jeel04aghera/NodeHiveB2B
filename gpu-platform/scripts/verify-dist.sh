#!/usr/bin/env bash
# verify-dist.sh — fail if any expected prebuilt agent artifact is missing.
#
# The control plane serves these at /dist/agent-<os>-<arch>; a missing one makes
# `curl install.sh | sh` 404 for that platform. Run after `make agent-dist`, in CI,
# and (with --url) against a live deployment to catch a control-plane that shipped
# without the binaries.
#
# Usage:
#   scripts/verify-dist.sh                 # check ./dist on disk
#   scripts/verify-dist.sh --dir path      # check a specific directory
#   scripts/verify-dist.sh --url https://host   # check a running server's /dist
set -euo pipefail

TARGETS="darwin-arm64 darwin-amd64 linux-amd64 linux-arm64"

MODE="dir"
DIR="dist"
URL=""
while [ $# -gt 0 ]; do
  case "$1" in
    --dir) DIR="$2"; MODE="dir"; shift 2 ;;
    --url) URL="${2%/}"; MODE="url"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

missing=0
echo "Verifying agent artifacts ($MODE):"
for t in $TARGETS; do
  if [ "$MODE" = "url" ]; then
    code=$(curl -s -o /dev/null -w "%{http_code}" "$URL/dist/agent-$t" || echo "000")
    if [ "$code" = "200" ]; then
      echo "  ✓ agent-$t (HTTP 200)"
    else
      echo "  ✗ agent-$t — HTTP $code at $URL/dist/agent-$t"
      missing=1
    fi
  else
    f="$DIR/agent-$t"
    if [ -s "$f" ]; then
      echo "  ✓ $f ($(wc -c <"$f" | tr -d ' ') bytes)"
    else
      echo "  ✗ $f — missing or empty"
      missing=1
    fi
  fi
done

if [ "$missing" -ne 0 ]; then
  echo ""
  echo "ERROR: one or more expected agent artifacts are missing." >&2
  echo "Build them with 'make agent-dist' and ensure the deploy image copies ./dist." >&2
  exit 1
fi
echo "All ${TARGETS// /, } artifacts present."
