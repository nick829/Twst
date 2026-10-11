#!/usr/bin/env bash
# Installs Go into the server user's directory; does not require root/apt.
set -euo pipefail

GO_VERSION="1.25.0"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) GO_ARCH="amd64" ;;
  aarch64|arm64) GO_ARCH="arm64" ;;
  *) echo "Unsupported CPU architecture: $ARCH" >&2; exit 1 ;;
esac

ROOT="${HOME:-/home/container}"
TOOLS="$ROOT/.tools"
mkdir -p "$TOOLS"
TARBALL="/tmp/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
URL="https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required in the Debian image to bootstrap Go." >&2
  exit 1
fi

echo "Downloading Go ${GO_VERSION} for linux-${GO_ARCH}..."
curl -fL "$URL" -o "$TARBALL"
rm -rf "$TOOLS/go"
tar -C "$TOOLS" -xzf "$TARBALL"
rm -f "$TARBALL"
export PATH="$TOOLS/go/bin:$PATH"
cd "${SERVER_DIR:-$ROOT}"

echo "Go installed: $(go version)"
if [ ! -f go.mod ]; then
  echo "ERROR: go.mod not found in $(pwd). Upload/extract the project into the server root." >&2
  exit 1
fi

go mod tidy
go build -o server .
echo "Build complete: $(pwd)/server"
