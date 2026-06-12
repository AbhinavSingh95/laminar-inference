#!/usr/bin/env bash
#
# Run Laminar's real local-LLM benchmark lane against a warmed llama-server.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REQUESTS="${LAMINAR_REAL_LLM_REQUESTS:-20}"
CONCURRENCY="${LAMINAR_REAL_LLM_CONCURRENCY:-4}"
OUTPUT="${LAMINAR_REAL_LLM_OUTPUT:-$ROOT/docs/results/llama-server-latest.md}"
INSTALL=false
KEEP_SERVER="${LAMINAR_KEEP_LLAMA_SERVER:-false}"
STARTED_SERVER=false

usage() {
  cat <<USAGE
Usage: $0 [--install] [--requests N] [--concurrency N] [--output PATH]

Runs the llama.cpp server benchmark scenarios against a warmed local LLM.

Default model:
  \${LAMINAR_LLAMA_HF_MODEL:-bartowski/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M}

Environment:
  LAMINAR_LLAMA_SERVER_URL      Existing /completion endpoint. If unset, this
                                script starts ./scripts/run_llama_server.sh.
  LAMINAR_KEEP_LLAMA_SERVER     true to keep a server started by this script.
  LAMINAR_REAL_LLM_REQUESTS     Default: $REQUESTS
  LAMINAR_REAL_LLM_CONCURRENCY  Default: $CONCURRENCY
  LAMINAR_REAL_LLM_OUTPUT       Default: $OUTPUT
USAGE
}

require_value() {
  local flag="$1"
  if (($# < 2)); then
    echo "$flag requires a value" >&2
    usage >&2
    exit 2
  fi
}

while (($# > 0)); do
  case "$1" in
    --install)
      INSTALL=true
      shift
      ;;
    --requests)
      require_value "$@"
      REQUESTS="$2"
      shift 2
      ;;
    --concurrency)
      require_value "$@"
      CONCURRENCY="$2"
      shift 2
      ;;
    --output)
      require_value "$@"
      OUTPUT="$2"
      shift 2
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

if ! [[ "$REQUESTS" =~ ^[1-9][0-9]*$ ]]; then
  echo "--requests must be a positive integer" >&2
  exit 2
fi

if ! [[ "$CONCURRENCY" =~ ^[1-9][0-9]*$ ]]; then
  echo "--concurrency must be a positive integer" >&2
  exit 2
fi

cleanup() {
  if [[ "$STARTED_SERVER" == true && "$KEEP_SERVER" != true ]]; then
    "$ROOT/scripts/run_llama_server.sh" --stop >/dev/null || true
  fi
}
trap cleanup EXIT

if [[ -z "${LAMINAR_LLAMA_SERVER_URL:-}" || "$INSTALL" == true ]]; then
  if [[ "$INSTALL" == true ]]; then
    "$ROOT/scripts/setup_llama_cpp.sh" --install
  else
    "$ROOT/scripts/setup_llama_cpp.sh"
  fi
else
  echo "using existing llama-server; skipping local llama.cpp tool check"
fi

if [[ -z "${LAMINAR_LLAMA_SERVER_URL:-}" ]]; then
  "$ROOT/scripts/run_llama_server.sh" --background
  STARTED_SERVER=true
  export LAMINAR_LLAMA_SERVER_URL="$("$ROOT/scripts/run_llama_server.sh" --url)"
else
  echo "using existing llama-server: $LAMINAR_LLAMA_SERVER_URL"
fi

mkdir -p "$(dirname "$OUTPUT")"

metadata_file="${OUTPUT%.md}.metadata.json"
cat >"$metadata_file" <<EOF
{
  "timestamp_utc": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
  "model": "${LAMINAR_LLAMA_GGUF_MODEL:-${LAMINAR_LLAMA_HF_MODEL:-bartowski/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M}}",
  "llama_server_url": "$LAMINAR_LLAMA_SERVER_URL",
  "requests": "$REQUESTS",
  "concurrency": "$CONCURRENCY",
  "uname": "$(uname -a | sed 's/"/\\"/g')"
}
EOF

echo "wrote runtime metadata: $metadata_file"
echo "running real LLM benchmark..."
"$ROOT/benchmark.sh" \
  --scenario "llama.cpp server" \
  --requests "$REQUESTS" \
  --concurrency "$CONCURRENCY" \
  --output "$OUTPUT"
