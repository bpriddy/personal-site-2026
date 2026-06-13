#!/usr/bin/env bash
# Vercel build: the build image already ships Rust (rustup at /rust) — don't
# reinstall it, just add the wasm target, fetch trunk, and build into dist/.
# Referenced from vercel.json (whose command fields cap at 256 chars).
set -euo pipefail

TRUNK_VERSION=v0.21.14

rustup target add wasm32-unknown-unknown

mkdir -p "$PWD/.build-bin"
curl -fsSL "https://github.com/trunk-rs/trunk/releases/download/${TRUNK_VERSION}/trunk-x86_64-unknown-linux-gnu.tar.gz" \
  | tar -xz -C "$PWD/.build-bin"
export PATH="$PWD/.build-bin:$PATH"

rustc --version
trunk --version
trunk build --release
