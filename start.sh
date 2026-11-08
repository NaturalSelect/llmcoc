#!/usr/bin/env bash
# start.sh — Build and run the llmcoc server.
#
# Usage:
#   ./start.sh             # production mode
#   ./start.sh --debug     # enable verbose agent + LLM logging (LOG_LEVEL=debug)
#   ./start.sh --dev       # go run instead of build (re-compiles on each start)
#   ./start.sh --no-web    # skip the frontend build and reuse cmd/server/dist/app
#
# The frontend (frontend/, Vue + Vite) is built into cmd/server/dist/app before the Go
# build, so Node >= 20.19 (or >= 22.12) and npm are required unless --no-web is given.
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
BUILD_WEB=1

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
    --no-web)
      BUILD_WEB=0
      echo "[start.sh] Skipping frontend build (--no-web)"
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

# ── Build frontend ────────────────────────────────────────────────────────────
# 产物输出到 cmd/server/dist/app，由 Go 二进制嵌入；没有产物时服务只会返回 503 提示
if [[ "$BUILD_WEB" == "1" ]]; then
  if ! command -v npm >/dev/null 2>&1; then
    echo "[start.sh] npm not found: install Node.js (>= 20.19 or >= 22.12), or pass --no-web to reuse an existing build" >&2
    exit 1
  fi
  if [[ ! -d frontend/node_modules ]]; then
    echo "[start.sh] Installing frontend dependencies (npm ci)"
    (cd frontend && npm ci)
  fi
  echo "[start.sh] Building frontend → cmd/server/dist/app"
  (cd frontend && npm run build)
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
  echo "[start.sh] Starting server (CONFIG_PATH=$CONFIG_PATH, LOG_LEVEL=$LOG_LEVEL)"
  exec "$BINARY"
fi
