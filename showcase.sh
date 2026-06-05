#!/usr/bin/env bash
#
# Live feature walkthrough for a running Laminar gateway.

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required for this showcase"
  exit 1
fi

if ! curl -fsS "$BASE_URL/health" >/dev/null 2>&1; then
  echo "Gateway is not running. Start it with ./run.sh"
  exit 1
fi

echo "Laminar Inference live showcase"
echo "Base URL: $BASE_URL"
echo ""

echo "[1] Request correlation"
curl -sS -i \
  -X POST "$BASE_URL/predict" \
  -H "Content-Type: application/json" \
  -H "X-Trace-ID: showcase-trace-001" \
  -d '{"prompt":"show trace propagation"}' \
  | sed -n '1,12p'
echo ""

echo "[2] Dynamic batching"
for i in $(seq 1 12); do
  curl -sS \
    -X POST "$BASE_URL/predict" \
    -H "Content-Type: application/json" \
    -H "X-Trace-ID: batch-demo-$i" \
    -d "{\"prompt\":\"batch demo $i\"}" \
    >/dev/null &
done
wait
echo "Submitted 12 concurrent requests"
echo ""

echo "[3] Readiness"
curl -sS "$BASE_URL/ready" | jq .
echo ""

echo "[4] Runtime stats"
curl -sS "$BASE_URL/stats" | jq .
echo ""

echo "[5] Batch policy state"
curl -sS "$BASE_URL/stats" | jq '.runtime.batch_policy'
echo ""

echo "[6] Worker pool state"
curl -sS "$BASE_URL/stats" | jq '.runtime.workers'
echo ""

echo "[7] Metrics snapshot"
curl -sS "$BASE_URL/metrics" | awk '
  /batch_size_distribution_(bucket|sum|count)|request_queue_depth|requests_rejected_total|worker_errors_total|requests_cancelled_total|worker_in_flight_batches|worker_batches_total|worker_failures_total|worker_circuit_open/ {
    if ($1 !~ /^#/) print
  }
'
echo ""

echo "Showcase complete. For benchmark methodology, see docs/BENCHMARKS.md."
