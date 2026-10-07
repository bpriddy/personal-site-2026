#!/usr/bin/env bash
# Bakes the site's published content into builtin/stream (frontends/stream):
# writes frontends/stream/baked/site.js (window.__BAKED = <site.json>), which
# trunk inlines into index.html, so the front end fetches no content at
# runtime. Rebuild the front end after baking (scripts/build-frontends.sh).
#
#   scripts/bake-stream.sh [source]   source: a site.json URL or file
#                                     (default https://benpriddy.com/api/site.json)
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
src="${1:-https://benpriddy.com/api/site.json}"
out="$root/frontends/stream/baked/site.js"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
if [[ -f "$src" ]]; then cp "$src" "$tmp"; else curl -sfL --max-time 30 "$src" -o "$tmp"; fi
python3 - "$tmp" "$out" <<'PY'
import json, sys
site = json.load(open(sys.argv[1]))
if site.get("contractVersion") != 1 or not isinstance(site.get("pages"), list):
    sys.exit("bake-stream: not a site.json (contractVersion 1)")
body = json.dumps(site, ensure_ascii=False, separators=(",", ":"), sort_keys=True)
# safe inside an inline <script>: no "</" can close it
body = body.replace("</", "<\\/").replace("\u2028", "\\u2028").replace("\u2029", "\\u2029")
open(sys.argv[2], "w").write("// baked by scripts/bake-stream.sh; do not edit\nwindow.__BAKED=" + body + ";\n")
PY
echo "baked $(wc -c <"$out") bytes into ${out#$root/}"
