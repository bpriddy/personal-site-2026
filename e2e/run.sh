#!/usr/bin/env bash
# End-to-end browser tests: builds and starts the main site (:8090) and the
# user-content service (:8091), runs Playwright, and always stops both.
#
# Usage: e2e/run.sh [options] [-- playwright args...]
#   --skip-build-frontends  don't run scripts/build-frontends.sh (reuse build/frontends)
#   --slow                  also run @slow tests (token expiry, ~61s)
#   --phase NAME            run only this phase (repeatable): site, particle-stream, broken
#   --feasibility           only the standalone WebGPU feasibility test (no servers)
#
# The main site's FRONTEND_ROTATION picks the front end, so the suite runs in
# phases, restarting the servers with a different rotation each time:
#   site             FRONTEND_ROTATION=builtin/site             all specs
#   particle-stream  FRONTEND_ROTATION=builtin/particle-stream  ready + navigation
#   broken           FRONTEND_ROTATION=builtin/e2e-broken       fallback
# If FRONTEND_ROTATION is already set in the environment, it runs a single
# "custom" phase with that rotation and all specs.
#
# Other env: E2E_WEBGPU_ADAPTER=gpu|swiftshader (default gpu), E2E_WORKERS,
# E2E_MAIN_PORT / E2E_UC_PORT (default 8090 / 8091; origins follow).
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$HOME/.local/go/bin:$PATH"

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$E2E_DIR/.." && pwd)"
RUN_DIR="$E2E_DIR/.run"
BIN="$RUN_DIR/bin"
LOGS="$RUN_DIR/logs"

MAIN_PORT="${E2E_MAIN_PORT:-8090}"
UC_PORT="${E2E_UC_PORT:-8091}"
export APP_ENV=dev
export FRONTENDS_DIR="${FRONTENDS_DIR:-$ROOT/build/frontends}"
export MAIN_ORIGIN="http://localhost:$MAIN_PORT"
export USERCONTENT_ORIGIN="http://127.0.0.1:$UC_PORT"
export E2E_MAIN_ORIGIN="$MAIN_ORIGIN" E2E_USERCONTENT_ORIGIN="$USERCONTENT_ORIGIN"

build_frontends=1
phases=()
feasibility=0
pw_args=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-build-frontends) build_frontends=0 ;;
    --slow) export E2E_SLOW=1 ;;
    --phase) phases+=("$2"); shift ;;
    --feasibility) feasibility=1 ;;
    --) shift; pw_args=("$@"); break ;;
    -h|--help) sed -n "2,22p" "$0" | grep "^#"; exit 0 ;;
    *) echo "run.sh: unknown option $1 (see --help)" >&2; exit 2 ;;
  esac
  shift
done

die() { echo; echo "run.sh: FAILED: $*" >&2; exit 1; }
step() { echo; echo "==> $*"; }

# --- Playwright + browser (user-space; no system deps needed on the dev box)
step "Playwright"
cd "$E2E_DIR"
[[ -d node_modules/@playwright/test ]] || npm ci
npx playwright install chromium >/dev/null || die "npx playwright install chromium"

if [[ $feasibility == 1 ]]; then
  exec npx playwright test tests/webgpu-feasibility.spec.ts "${pw_args[@]}"
fi

# any listener at all (not just HTTP) counts: never test against a foreign server
port_busy() {
  (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null && return 0
  (exec 3<>"/dev/tcp/localhost/$1") 2>/dev/null
}
check_ports() {
  port_busy "$MAIN_PORT" && die "port $MAIN_PORT is already in use by another process; free it or set E2E_MAIN_PORT"
  port_busy "$UC_PORT" && die "port $UC_PORT is already in use by another process; free it or set E2E_UC_PORT"
  return 0
}
check_ports # fail fast, before building

# --- build the system under test
cd "$ROOT"
if [[ $build_frontends == 1 ]]; then
  step "scripts/build-frontends.sh"
  [[ -x scripts/build-frontends.sh ]] ||
    die "scripts/build-frontends.sh is missing (the front-end build hasn't landed yet); pass --skip-build-frontends to use an existing $FRONTENDS_DIR"
  scripts/build-frontends.sh || die "scripts/build-frontends.sh exited non-zero"
fi
for ref in builtin/site builtin/particle-stream; do
  [[ -f "$FRONTENDS_DIR/$ref/index.html" ]] ||
    die "$FRONTENDS_DIR/$ref/index.html is missing (run scripts/build-frontends.sh)"
done
# test fixture: a front end that throws on load (tests/fallback.spec.ts)
mkdir -p "$FRONTENDS_DIR/builtin/e2e-broken"
cp "$E2E_DIR/fixtures/e2e-broken/index.html" "$FRONTENDS_DIR/builtin/e2e-broken/index.html"

step "go build ./cmd/server ./cmd/usercontent"
mkdir -p "$BIN" "$LOGS"
go build -o "$BIN/server" ./cmd/server || die "go build ./cmd/server"
[[ -d cmd/usercontent ]] ||
  die "cmd/usercontent doesn't exist yet (the user-content service hasn't landed); nothing to test against"
go build -o "$BIN/usercontent" ./cmd/usercontent || die "go build ./cmd/usercontent"

# --- servers
pids=()
stop_servers() {
  for pid in "${pids[@]:-}"; do
    [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
  done
  for pid in "${pids[@]:-}"; do
    [[ -n "$pid" ]] && wait "$pid" 2>/dev/null || true
  done
  pids=()
}
trap stop_servers EXIT
trap 'exit 130' INT TERM


wait_healthy() { # name url pid log
  for _ in $(seq 1 100); do
    if curl -sf --max-time 1 "$2" >/dev/null; then return 0; fi
    if ! kill -0 "$3" 2>/dev/null; then
      echo "--- $4"; tail -n 30 "$4" >&2
      die "$1 exited during startup (log: $4)"
    fi
    sleep 0.2
  done
  echo "--- $4"; tail -n 30 "$4" >&2
  die "$1 not healthy at $2 after 20s (log: $4)"
}

start_servers() { # phase rotation
  local phase=$1
  export FRONTEND_ROTATION=$2
  check_ports
  PORT=$MAIN_PORT MAIN_ORIGIN="$MAIN_ORIGIN" USERCONTENT_ORIGIN="$USERCONTENT_ORIGIN" "$BIN/server" >"$LOGS/main-$phase.log" 2>&1 &
  pids+=($!)
  PORT=$UC_PORT MAIN_ORIGIN="$MAIN_ORIGIN" USERCONTENT_ORIGIN="$USERCONTENT_ORIGIN" "$BIN/usercontent" >"$LOGS/usercontent-$phase.log" 2>&1 &
  pids+=($!)
  wait_healthy "main site" "$MAIN_ORIGIN/healthz" "${pids[0]}" "$LOGS/main-$phase.log"
  wait_healthy "user-content" "$USERCONTENT_ORIGIN/healthz" "${pids[1]}" "$LOGS/usercontent-$phase.log"
}

declare -A ROTATION_OF=(
  [site]=builtin/site
  [particle-stream]=builtin/particle-stream
  [broken]=builtin/e2e-broken
)
declare -A SPECS_OF=(
  [site]=""
  [particle-stream]="tests/builtins-ready.spec.ts tests/navigation.spec.ts"
  [broken]="tests/fallback.spec.ts"
  [custom]=""
)
if [[ -n "${FRONTEND_ROTATION:-}" ]]; then
  ROTATION_OF[custom]=$FRONTEND_ROTATION
  phases=(custom)
elif [[ ${#phases[@]} -eq 0 ]]; then
  phases=(site particle-stream broken)
fi

failed=()
for phase in "${phases[@]}"; do
  [[ -n "${ROTATION_OF[$phase]:-}" ]] || die "unknown phase $phase (site, particle-stream, broken)"
  step "phase $phase: FRONTEND_ROTATION=${ROTATION_OF[$phase]}"
  start_servers "$phase" "${ROTATION_OF[$phase]}"
  cd "$E2E_DIR"
  # shellcheck disable=SC2086
  if ! PLAYWRIGHT_HTML_OUTPUT_DIR="playwright-report/$phase" npx playwright test ${SPECS_OF[$phase]} --output "test-results/$phase" "${pw_args[@]}"; then
    failed+=("$phase")
    echo "run.sh: phase $phase failed; server logs in $LOGS/*-$phase.log" >&2
  fi
  stop_servers
done

echo
if [[ ${#failed[@]} -gt 0 ]]; then
  echo "run.sh: FAILED phases: ${failed[*]} (reports: e2e/playwright-report/<phase>)" >&2
  exit 1
fi
echo "run.sh: all phases passed"
