#!/usr/bin/env bash
set -euo pipefail
ROOT="${SERVER_DIR:-${HOME:-/home/container}}"
cd "$ROOT"
export PATH="$ROOT/.tools/go/bin:$PATH"
if ! command -v go >/dev/null 2>&1; then
  echo "ERROR: Go is not installed. Run install-go.sh from Script Install first." >&2
  exit 127
fi
if [ ! -f go.mod ]; then
  echo "ERROR: go.mod is missing from $ROOT" >&2
  exit 1
fi
# Build at startup too, so source changes are picked up automatically.
go mod tidy
go build -o server .
exec ./server
