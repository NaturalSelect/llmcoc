#!/usr/bin/env bash
# start.sh — Build and run the llmcoc server.
#
# Usage:
#   ./start.sh             # production mode
#   ./start.sh --debug     # enable verbose agent + LLM logging, Gin debug mode
#   ./start.sh --dev       # go run instead of build (re-compiles on each start)
#
# Environment variables (can also be set in .env):
#   LLM_API_KEY   — API key for the LLM provider (overrides config.yaml api_key)
#   CONFIG_PATH   — Path to config.yaml (default: ./config.yaml)
#   GIN_MODE      — "release" or "debug" (overridden by --debug flag)
#   AGENT_DEBUG   — "1" to enable per-call agent tracing (set by --debug flag)
#   DB_PATH       — SQLite database path (overrides config.yaml database.path)

set -euo pipefail

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
: "${GIN_MODE:=release}"
: "${AGENT_DEBUG:=0}"

# ── Parse flags ───────────────────────────────────────────────────────────────
MODE="build"   # build | dev
DEBUG=0

for arg in "$@"; do
  case "$arg" in
    --debug)
      DEBUG=1
      GIN_MODE="debug"
      AGENT_DEBUG="1"
      echo "[start.sh] Debug mode enabled (AGENT_DEBUG=1, GIN_MODE=debug)"
      ;;
    --dev)
      MODE="dev"
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

export GIN_MODE
export AGENT_DEBUG
export CONFIG_PATH

# ── Ensure data directory exists ──────────────────────────────────────────────
mkdir -p data
echo "[start.sh] Data directory: $(pwd)/data"

# ── Propagate API key to config env if set ────────────────────────────────────
if [[ -n "${LLM_API_KEY:-}" ]]; then
  echo "[start.sh] LLM_API_KEY is set (length=${#LLM_API_KEY})"
fi

# ── Build and run ─────────────────────────────────────────────────────────────
if [[ "$MODE" == "dev" ]]; then
  echo "[start.sh] Starting server with 'go run ./cmd/server' (CONFIG_PATH=$CONFIG_PATH)"
  exec go run ./cmd/server
else
  BINARY="./bin/llmcoc"
  mkdir -p bin
  echo "[start.sh] Building binary → $BINARY"
  go build -o "$BINARY" ./cmd/server
  echo "[start.sh] Starting server (CONFIG_PATH=$CONFIG_PATH, GIN_MODE=$GIN_MODE)"
  exec "$BINARY"
fi
