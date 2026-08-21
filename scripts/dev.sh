#!/bin/bash
set -e
echo "🐑 Herdrem — dev mode (hot rebuild)"
echo ""

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BINARY="$ROOT_DIR/herdrem"

cd "$ROOT_DIR"

# Ensure web deps
if [ ! -d web/node_modules ]; then
    echo "▸ Installing web deps…"
    (cd web && npm install)
fi

# Build Go binary once (Go changes still need manual restart)
echo "▸ Building herdrem…"
go build -o "$BINARY" ./cmd/herdrem
echo "✓ Built $BINARY"

# Start Vite watch (rebuilds webdist/ on file change)
echo "▸ Starting Vite watch…"
(cd web && npm run build -- --watch) &
VITE_PID=$!
trap "kill $VITE_PID 2>/dev/null; exit" INT TERM

# Start relay serving from webdist/ directly (no embed needed)
echo "▸ Starting herdrem serve --web --static webdist…"
exec "$BINARY" serve --web --static webdist "$@"
