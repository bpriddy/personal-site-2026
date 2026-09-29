#!/usr/bin/env bash
# Installs the dev toolchain into $HOME — no root needed (works over remote
# sessions where a sudo prompt can't be answered). Idempotent; re-run to repair.
#
#   Go         → ~/.local/go
#   Rust       → ~/.rustup, ~/.cargo  (+ wasm32-unknown-unknown)
#   trunk      → ~/.local/bin/trunk
#   C compiler → ~/.local/zig, with ~/.local/bin/cc wrapping `zig cc`
#                (Rust build scripts need a host linker; skip if you have
#                build-essential installed system-wide)
#
# Afterwards, put these on PATH (e.g. in ~/.profile):
#   export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$HOME/.local/go/bin:$PATH"
set -euo pipefail

TRUNK_VERSION=v0.21.14 # keep in sync with the Dockerfile
mkdir -p "$HOME/.local/bin"

if [ ! -x "$HOME/.local/go/bin/go" ]; then
  v=$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -1)
  echo "installing $v"
  curl -fsSL "https://go.dev/dl/$v.linux-amd64.tar.gz" | tar -xz -C "$HOME/.local"
fi

if [ ! -x "$HOME/.cargo/bin/rustup" ]; then
  echo "installing rust"
  curl -fsSL https://sh.rustup.rs | sh -s -- -y --no-modify-path --profile minimal
fi
"$HOME/.cargo/bin/rustup" target add wasm32-unknown-unknown

if ! "$HOME/.local/bin/trunk" --version 2>/dev/null | grep -q "${TRUNK_VERSION#v}"; then
  echo "installing trunk $TRUNK_VERSION"
  curl -fsSL "https://github.com/trunk-rs/trunk/releases/download/${TRUNK_VERSION}/trunk-x86_64-unknown-linux-musl.tar.gz" \
    | tar -xz -C "$HOME/.local/bin"
fi

if ! command -v cc >/dev/null 2>&1; then
  if [ ! -x "$HOME/.local/zig/zig" ]; then
    url=$(curl -fsSL https://ziglang.org/download/index.json | python3 -c '
import json, sys
d = json.load(sys.stdin)
v = max((k for k in d if k != "master"), key=lambda s: [int(x) for x in s.split(".")])
print(d[v]["x86_64-linux"]["tarball"])')
    echo "installing zig ($url)"
    mkdir -p "$HOME/.local/zig"
    curl -fsSL "$url" | tar -xJ -C "$HOME/.local/zig" --strip-components=1
  fi
  printf '#!/bin/sh\n# user-space C compiler/linker (no root): zig'"'"'s bundled clang\nexec "$HOME/.local/zig/zig" cc "$@"\n' > "$HOME/.local/bin/cc"
  chmod +x "$HOME/.local/bin/cc"
fi

echo "done:"
"$HOME/.local/go/bin/go" version
"$HOME/.cargo/bin/rustc" --version
"$HOME/.local/bin/trunk" --version
PATH="$HOME/.local/bin:$PATH" cc --version | head -1
