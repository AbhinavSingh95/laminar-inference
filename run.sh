#!/usr/bin/env bash
#
# Build and run the Laminar worker and gateway locally.

set -euo pipefail

join_by_comma() {
  local IFS=,
  echo "$*"
}

split_csv() {
  local raw="$1"
  local -n out="$2"
  local item
  IFS=',' read -r -a out <<<"$raw"
  for i in "${!out[@]}"; do
    item="${out[$i]}"
    item="${item#"${item%%[![:space:]]*}"}"
    item="${item%"${item##*[![:space:]]}"}"
    out[$i]="$item"
  done
}

WORKER_COUNT="${LAMINAR_WORKER_COUNT:-1}"
if ! [[ "$WORKER_COUNT" =~ ^[0-9]+$ ]] || [[ "$WORKER_COUNT" -lt 1 ]]; then
  echo "LAMINAR_WORKER_COUNT must be a positive integer"
  exit 1
fi

worker_targets=()
worker_listens=()
if [[ -n "${LAMINAR_WORKER_ADDRS:-}" ]]; then
  split_csv "$LAMINAR_WORKER_ADDRS" worker_targets
else
  for ((i = 0; i < WORKER_COUNT; i++)); do
    port=$((50051 + i))
    worker_targets+=("localhost:${port}")
  done
  export LAMINAR_WORKER_ADDRS="$(join_by_comma "${worker_targets[@]}")"
fi

if [[ -n "${LAMINAR_WORKER_LISTEN_ADDRS:-}" ]]; then
  split_csv "$LAMINAR_WORKER_LISTEN_ADDRS" worker_listens
elif [[ "${#worker_targets[@]}" -eq 1 && -n "${LAMINAR_WORKER_LISTEN_ADDR:-}" ]]; then
  worker_listens=("$LAMINAR_WORKER_LISTEN_ADDR")
else
  for target in "${worker_targets[@]}"; do
    worker_listens+=("0.0.0.0:${target##*:}")
  done
fi

if [[ "${#worker_targets[@]}" -ne "${#worker_listens[@]}" ]]; then
  echo "Worker target count (${#worker_targets[@]}) must match listen address count (${#worker_listens[@]})"
  exit 1
fi

export LAMINAR_WORKER_ADDR="${LAMINAR_WORKER_ADDR:-${worker_targets[0]}}"
export LAMINAR_WORKER_ADDRS="$(join_by_comma "${worker_targets[@]}")"

echo "Laminar Inference"
echo "Building worker and gateway..."
bazel build //backend:worker //gateway:gateway

echo ""
worker_pids=()
for listen_addr in "${worker_listens[@]}"; do
  echo "Starting C++ worker on ${listen_addr}"
  ./bazel-bin/backend/worker "$listen_addr" &
  worker_pids+=("$!")
done

cleanup() {
  echo ""
  echo "Stopping services..."
  for pid in "${worker_pids[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
}
trap cleanup EXIT INT TERM

sleep 1
for pid in "${worker_pids[@]}"; do
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "Worker failed to start"
    exit 1
  fi
done

echo "Starting Go gateway on ${LAMINAR_HTTP_ADDR:-:8080}"
echo "Worker targets: ${LAMINAR_WORKER_ADDRS}"
echo ""
./bazel-bin/gateway/gateway_/gateway
