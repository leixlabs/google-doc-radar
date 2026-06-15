#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")"

echo "Building google-doc-radar-server..."
CGO_ENABLED=1 go build -o gdoc-radar-server .

echo "Installing binary to ~/.local/bin/..."
mkdir -p ~/.local/bin
cp gdoc-radar-server ~/.local/bin/

echo "Installing launchd service..."
PLIST="leix.gdoc.radar.server.plist"
cp "$PLIST" ~/Library/LaunchAgents/
launchctl unload ~/Library/LaunchAgents/"$PLIST" 2>/dev/null || true
launchctl load ~/Library/LaunchAgents/"$PLIST"

echo "Done. Server will start on port 9906."
