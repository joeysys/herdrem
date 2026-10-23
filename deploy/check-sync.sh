#!/usr/bin/env bash
# check-sync.sh — verify that the embedded template copies inside
# install.sh still match the canonical on-disk files. Run from anywhere:
#   ./deploy/check-sync.sh   or   make check-install
set -euo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT

# Extract the embedded_service / embedded_env function bodies and source
# only those, then compare their output to the canonical files.
sed -n '/^embedded_service()/,/^}/p;/^embedded_env()/,/^}/p' \
    "$here/install.sh" >"$tmp/embedded.sh"
( . "$tmp/embedded.sh" && embedded_service ) >"$tmp/herdrem.service"
( . "$tmp/embedded.sh" && embedded_env ) >"$tmp/herdrem.env.example"

diff -u "$here/herdrem.service" "$tmp/herdrem.service"
diff -u "$here/herdrem.env.example" "$tmp/herdrem.env.example"

printf 'embedded templates match deploy/herdrem.service and herdrem.env.example\n'
