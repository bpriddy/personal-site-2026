#!/usr/bin/env bash
# Vercel build: install Rust + trunk, then build the site into dist/.
# Referenced from vercel.json (whose command fields cap at 256 chars).
set -euo pipefail

TRUNK_VERSION=v0.21.14

curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
  | sh -s -- -y --profile minimal --default-toolchain stable --target wasm32-unknown-unknown

export PATH="$HOME/.cargo/bin:$PATH"

curl -fsSL "https://github.com/trunk-rs/trunk/releases/download/${TRUNK_VERSION}/trunk-x86_64-unknown-linux-gnu.tar.gz" \
  | tar -xz -C "$HOME/.cargo/bin"

trunk --version
trunk build --release
