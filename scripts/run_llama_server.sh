#!/usr/bin/env bash
#
# Start, stop, or inspect a local llama.cpp server for Laminar benchmarks.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE_DIR="${LAMINAR_LLAMA_STATE_DIR:-$ROOT/.cache/laminar/llama-server}"
PID_FILE="$STATE_DIR/llama-server.pid"
LOG_FILE="$STATE_DIR/llama-server.log"

LLAMA_SERVER_BIN="${LAMINAR_LLAMA_SERVER_BIN:-llama-server}"
HOST="${LAMINAR_LLAMA_SERVER_HOST:-127.0.0.1}"
PORT="${LAMINAR_LLAMA_SERVER_PORT:-18081}"
HF_MODEL="${LAMINAR_LLAMA_HF_MODEL:-bartowski/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M}"
LOCAL_MODEL="${LAMINAR_LLAMA_GGUF_MODEL:-}"
CTX_SIZE="${LAMINAR_LLAMA_SERVER_CONTEXT:-2048}"
THREADS="${LAMINAR_LLAMA_SERVER_THREADS:-}"
GPU_LAYERS="${LAMINAR_LLAMA_SERVER_GPU_LAYERS:-}"
START_TIMEOUT_SECONDS="${LAMINAR_LLAMA_SERVER_START_TIMEOUT:-180}"

health_url="http://$HOST:$PORT/health"
completion_url="http://$HOST:$PORT/completion"

usage() {
  cat <<USAGE
Usage: $0 [--background|--foreground|--stop|--status|--url]

Starts llama-server with a small real instruction model by default.

Environment:
  LAMINAR_LLAMA_SERVER_BIN       llama-server binary path/name
  LAMINAR_LLAMA_HF_MODEL         HF GGUF model spec
                                 Default: $HF_MODEL
  LAMINAR_LLAMA_GGUF_MODEL       Local .gguf path. Overrides HF model when set
  LAMINAR_LLAMA_SERVER_HOST      Default: $HOST
  LAMINAR_LLAMA_SERVER_PORT      Default: $PORT
  LAMINAR_LLAMA_SERVER_CONTEXT   Default: $CTX_SIZE
  LAMINAR_LLAMA_SERVER_THREADS   Optional llama-server thread count
  LAMINAR_LLAMA_SERVER_GPU_LAYERS Optional GPU layer count
  LAMINAR_LLAMA_STATE_DIR        Default: $STATE_DIR
USAGE
}

mode="foreground"
while (($# > 0)); do
  case "$1" in
    --background)
      mode="background"
      shift
      ;;
    --foreground)
      mode="foreground"
      shift
      ;;
    --stop)
      mode="stop"
      shift
      ;;
    --status)
      mode="status"
      shift
      ;;
    --url)
      mode="url"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

mkdir -p "$STATE_DIR"

is_running() {
  [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" >/dev/null 2>&1
}

health_ready() {
  curl -fsS "$health_url" >/dev/null 2>&1
}

build_command() {
  command=("$LLAMA_SERVER_BIN" "--host" "$HOST" "--port" "$PORT" "-c" "$CTX_SIZE")
  if [[ -n "$LOCAL_MODEL" ]]; then
    command+=("-m" "$LOCAL_MODEL")
  else
    command+=("-hf" "$HF_MODEL")
  fi
  if [[ -n "$THREADS" ]]; then
    command+=("-t" "$THREADS")
  fi
  if [[ -n "$GPU_LAYERS" ]]; then
    command+=("-ngl" "$GPU_LAYERS")
  fi
}

wait_until_ready() {
  local deadline=$((SECONDS + START_TIMEOUT_SECONDS))
  until health_ready; do
    if ((SECONDS >= deadline)); then
      echo "llama-server did not become healthy within ${START_TIMEOUT_SECONDS}s" >&2
      echo "log: $LOG_FILE" >&2
      exit 1
    fi
    sleep 1
  done
}

case "$mode" in
  url)
    echo "$completion_url"
    ;;
  status)
    if health_ready; then
      echo "llama-server is healthy at $health_url"
      echo "completion url: $completion_url"
    elif is_running; then
      echo "llama-server process is running but health is not ready"
      echo "pid: $(cat "$PID_FILE")"
      echo "log: $LOG_FILE"
      exit 1
    else
      echo "llama-server is not running"
      exit 1
    fi
    ;;
  stop)
    if is_running; then
      pid="$(cat "$PID_FILE")"
      echo "stopping llama-server pid=$pid"
      kill "$pid"
      for _ in {1..20}; do
        if ! kill -0 "$pid" >/dev/null 2>&1; then
          break
        fi
        sleep 0.2
      done
      if kill -0 "$pid" >/dev/null 2>&1; then
        echo "llama-server did not stop after SIGTERM; sending SIGKILL"
        kill -9 "$pid" >/dev/null 2>&1 || true
      fi
      rm -f "$PID_FILE"
    else
      echo "llama-server is not running"
    fi
    ;;
  background)
    if health_ready; then
      echo "llama-server already healthy at $health_url"
      echo "completion url: $completion_url"
      exit 0
    fi
    if is_running; then
      echo "llama-server process is already running but health is not ready" >&2
      echo "pid: $(cat "$PID_FILE")" >&2
      echo "log: $LOG_FILE" >&2
      echo "stop it with: $0 --stop" >&2
      exit 1
    fi
    if ! command -v "$LLAMA_SERVER_BIN" >/dev/null 2>&1 && [[ ! -x "$LLAMA_SERVER_BIN" ]]; then
      echo "llama-server binary not found: $LLAMA_SERVER_BIN" >&2
      echo "run ./scripts/setup_llama_cpp.sh --install or set LAMINAR_LLAMA_SERVER_BIN" >&2
      exit 1
    fi
    if [[ -n "$LOCAL_MODEL" && ! -f "$LOCAL_MODEL" ]]; then
      echo "local GGUF model not found: $LOCAL_MODEL" >&2
      exit 1
    fi
    build_command
    echo "starting llama-server in background"
    echo "model: ${LOCAL_MODEL:-$HF_MODEL}"
    echo "log: $LOG_FILE"
    "${command[@]}" >"$LOG_FILE" 2>&1 &
    echo "$!" >"$PID_FILE"
    wait_until_ready
    echo "llama-server healthy at $health_url"
    echo "completion url: $completion_url"
    ;;
  foreground)
    if ! command -v "$LLAMA_SERVER_BIN" >/dev/null 2>&1 && [[ ! -x "$LLAMA_SERVER_BIN" ]]; then
      echo "llama-server binary not found: $LLAMA_SERVER_BIN" >&2
      echo "run ./scripts/setup_llama_cpp.sh --install or set LAMINAR_LLAMA_SERVER_BIN" >&2
      exit 1
    fi
    if [[ -n "$LOCAL_MODEL" && ! -f "$LOCAL_MODEL" ]]; then
      echo "local GGUF model not found: $LOCAL_MODEL" >&2
      exit 1
    fi
    build_command
    echo "starting llama-server in foreground"
    echo "model: ${LOCAL_MODEL:-$HF_MODEL}"
    echo "completion url: $completion_url"
    exec "${command[@]}"
    ;;
esac
