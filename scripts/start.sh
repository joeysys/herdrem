#!/bin/bash
set -e
echo "🐑 Herdrem — herdr remote relay"
echo ""

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BINARY="$ROOT_DIR/herdrem"

# Always build web + Go binary (incremental; fast on no-change)
cd "$ROOT_DIR"
if [ -d web/node_modules ]; then
    echo "▸ Building web…"
    (cd web && npm run build)
else
    echo "▸ Installing web deps + building…"
    (cd web && npm install && npm run build)
fi
echo "▸ Building herdrem…"
go build -o "$BINARY" ./cmd/herdrem
echo "✓ Built $BINARY"

# Start relay with web (tunnel opt-in via --tunnel arg)
echo "▸ Starting herdrem serve --web…"
exec "$BINARY" serve --web "$@"
