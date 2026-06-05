#!/usr/bin/env bash
#
# Smoke test for a running Laminar gateway and worker.

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
API_URL="$BASE_URL/predict"

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required for this smoke test"
  exit 1
fi

echo "Laminar Inference smoke test"
echo "Base URL: $BASE_URL"
echo ""

echo "[1] Health"
curl -fsS "$BASE_URL/health" | jq .
curl -fsS "$BASE_URL/ready" | jq .
echo ""

echo "[2] Single request"
curl -fsS \
  -X POST "$API_URL" \
  -H "Content-Type: application/json" \
  -H "X-Trace-ID: smoke-single" \
  -d '{"prompt":"single request"}' \
  | jq .
echo ""

echo "[3] Concurrent batch"
for i in $(seq 1 8); do
  curl -fsS \
    -X POST "$API_URL" \
    -H "Content-Type: application/json" \
    -H "X-Trace-ID: smoke-batch-$i" \
    -d "{\"prompt\":\"batch request $i\"}" \
    >/dev/null &
done
wait
echo "Completed 8 concurrent requests"
echo ""

echo "[4] Metrics"
curl -fsS "$BASE_URL/metrics" | awk '
  /batch_size_distribution_(bucket|sum|count)|request_duration_seconds_count|worker_errors_total|worker_batches_total|worker_circuit_open|requests_rejected_total/ {
    if ($1 !~ /^#/) print
  }
'
echo ""

echo "Smoke test passed"
