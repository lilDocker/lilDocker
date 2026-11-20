#!/bin/sh
set -e
cd "$(dirname "$0")/app"

if ! command -v go >/dev/null 2>&1; then
  echo "Go was not found. Install Go 1.24 or newer from https://go.dev/dl and run this again." >&2
  exit 1
fi

if [ "$(id -u)" -eq 0 ]; then
  dest=/usr/local/bin
else
  dest="$HOME/.local/bin"
fi
mkdir -p "$dest"

echo "Building lilDocker..."
go build -o "$dest/lilDocker" ./cmd
echo "Installed: $dest/lilDocker"

case ":$PATH:" in
  *":$dest:"*) ;;
  *) echo "Add $dest to your PATH:  export PATH=\"$dest:\$PATH\"" ;;
esac
echo "Run:  lilDocker version"
