#!/usr/bin/env bash
#
# Live feature walkthrough for a running Laminar gateway.

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
new_trace_id() {
  od -An -N16 -tx1 /dev/urandom | tr -d ' \n'
}

new_span_id() {
  od -An -N8 -tx1 /dev/urandom | tr -d ' \n'
}

TRACE_ID="${TRACE_ID:-$(new_trace_id)}"
PARENT_SPAN_ID="${PARENT_SPAN_ID:-$(new_span_id)}"
TRACEPARENT="00-$TRACE_ID-$PARENT_SPAN_ID-01"

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
  -H "Traceparent: $TRACEPARENT" \
  -d '{"prompt":"show trace propagation"}' \
  | sed -n '1,12p'
echo ""

echo "[2] Request trace"
curl -sS "$BASE_URL/traces?trace_id=$TRACE_ID" \
  | jq '.spans[] | {name, span_id, parent_span_id, status, duration_micros, attributes}'
echo ""

echo "[3] OTLP JSON export"
curl -sS "$BASE_URL/traces/otlp?trace_id=$TRACE_ID" \
  | jq '.resourceSpans[0].scopeSpans[0].spans[] | {name, traceId, spanId, parentSpanId, kind, status}'
echo ""

echo "[4] Streaming inference"
stream_trace_id="$(new_trace_id)"
stream_parent_span_id="$(new_span_id)"
curl -sS -N \
  -X POST "$BASE_URL/predict/stream" \
  -H "Content-Type: application/json" \
  -H "Traceparent: 00-$stream_trace_id-$stream_parent_span_id-01" \
  -d '{"prompt":"show streaming inference"}'
echo ""
curl -sS "$BASE_URL/traces?trace_id=$stream_trace_id" \
  | jq '.spans[] | {name, span_id, parent_span_id, status, duration_micros, attributes}'
echo ""

echo "[5] Dynamic batching"
for i in $(seq 1 12); do
  batch_trace_id="$(printf "%032x" "$i")"
  batch_parent_span_id="$(printf "%016x" "$i")"
  curl -sS \
    -X POST "$BASE_URL/predict" \
    -H "Content-Type: application/json" \
    -H "Traceparent: 00-$batch_trace_id-$batch_parent_span_id-01" \
    -d "{\"prompt\":\"batch demo $i\"}" \
    >/dev/null &
done
wait
echo "Submitted 12 concurrent requests"
echo ""

echo "[6] Readiness"
curl -sS "$BASE_URL/ready" | jq .
echo ""

echo "[7] Runtime stats"
curl -sS "$BASE_URL/stats" | jq .
echo ""

echo "[8] Batch policy state"
curl -sS "$BASE_URL/stats" | jq '.runtime.batch_policy'
echo ""

echo "[9] Worker pool state"
curl -sS "$BASE_URL/stats" | jq '.runtime.workers'
echo ""

echo "[10] Metrics snapshot"
curl -sS "$BASE_URL/metrics" | awk '
  /batch_size_distribution_(bucket|sum|count)|request_queue_depth|requests_rejected_total|worker_errors_total|requests_cancelled_total|worker_in_flight_batches|worker_batches_total|worker_failures_total|worker_circuit_open|admission_(in_flight_tokens|accepted_tokens_total|rejected_tokens_total|rejected_requests_total)/ {
    if ($1 !~ /^#/) print
  }
'
echo ""

echo "Showcase complete. For benchmark methodology, see docs/BENCHMARKS.md."
