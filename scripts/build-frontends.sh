#!/usr/bin/env bash
# Builds the built-in front ends as release bundles with relative asset paths
# into the dev FRONTENDS_DIR layout (docs/frontend-protocol.md, "Refs and files"):
#
#   site/                          → build/frontends/builtin/site/
#   experiments/particle-stream/   → build/frontends/builtin/particle-stream/
#
# Needs Rust + wasm32-unknown-unknown + trunk (scripts/dev-setup.sh).
set -euo pipefail

export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$HOME/.local/go/bin:$PATH"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out="$root/build/frontends/builtin"

build() { # <crate dir> <ref name>
  local src="$root/$1" dist="$out/$2"
  echo "==> builtin/$2 ($1)"
  rm -rf "$dist"
  mkdir -p "$dist"
  (cd "$src" && trunk build --release --public-url ./ --dist "$dist")
  # a front end must load its assets relatively and include the host API
  grep -q '<script src="/site-host.js"></script>' "$dist/index.html" ||
    { echo "error: $dist/index.html does not include /site-host.js" >&2; exit 1; }
  if grep -Eo '(src|href)="/[^"]*"' "$dist/index.html" | grep -v '"/site-host.js"'; then
    echo "error: $dist/index.html has root-absolute asset paths (above)" >&2
    exit 1
  fi
}

build site site
build experiments/particle-stream particle-stream

echo "built front ends in $out"
