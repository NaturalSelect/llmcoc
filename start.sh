#!/usr/bin/env bash
# start.sh — Build and run the llmcoc server.
#
# Usage:
#   ./start.sh             # production mode
#   ./start.sh --debug     # enable verbose agent + LLM logging (LOG_LEVEL=debug)
#   ./start.sh --dev       # go run instead of build (re-compiles on each start)
#
# Environment variables (can also be set in .env):
#   CONFIG_PATH   — Path to config.yaml (default: ./config.yaml)
#   LOG_LEVEL     — "debug"|"info"|"warn"|"error" 日志级别 (default: debug；--debug/--dev 强制 debug)
#   DB_PATH       — SQLite database path (overrides config.yaml database.path)

set -euo pipefail
trap '' PIPE

# ── Load .env if present ──────────────────────────────────────────────────────
if [[ -f .env ]]; then
  echo "[start.sh] Loading environment from .env"
  # Export non-comment, non-empty lines
  set -a
  # shellcheck disable=SC1090
  source .env
  set +a
fi

# ── Defaults ──────────────────────────────────────────────────────────────────
: "${CONFIG_PATH:=./config.yaml}"
: "${LOG_LEVEL:=debug}"

# ── Parse flags ───────────────────────────────────────────────────────────────
MODE="build"   # build | dev
DEBUG=0

for arg in "$@"; do
  case "$arg" in
    --debug)
      DEBUG=1
      LOG_LEVEL="debug"
      echo "[start.sh] Debug mode enabled (LOG_LEVEL=debug)"
      ;;
    --dev)
      DEBUG=1
      MODE="dev"
      LOG_LEVEL="debug"
      echo "[start.sh] Dev mode: using 'go run'"
      ;;
    --help|-h)
      grep '^#' "$0" | sed 's/^# \?//'
      exit 0
      ;;
    *)
      echo "[start.sh] Unknown argument: $arg" >&2
      exit 1
      ;;
  esac
done

export LOG_LEVEL
export CONFIG_PATH

# ── Ensure data directory exists ──────────────────────────────────────────────
mkdir -p data
echo "[start.sh] Data directory: $(pwd)/data"

# ── Build and run ─────────────────────────────────────────────────────────────
if [[ "$MODE" == "dev" ]]; then
  echo "[start.sh] Starting server with 'go run ./cmd/server' (CONFIG_PATH=$CONFIG_PATH)"
  exec go run ./cmd/server
else
  BINARY="./bin/llmcoc"
  mkdir -p bin
  echo "[start.sh] Building binary → $BINARY"
  go build -o "$BINARY" ./cmd/server
  echo "[start.sh] Starting server (CONFIG_PATH=$CONFIG_PATH, LOG_LEVEL=$LOG_LEVEL)"
  exec "$BINARY"
fi
