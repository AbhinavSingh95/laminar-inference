#!/usr/bin/env bash
#
# Smoke test for a running Laminar gateway and worker.

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
API_URL="$BASE_URL/predict"
STREAM_API_URL="$BASE_URL/predict/stream"
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
  -H "Traceparent: $TRACEPARENT" \
  -d '{"prompt":"single request"}' \
  | jq .
echo ""

echo "[3] Streaming request"
STREAM_TRACE_ID="$(new_trace_id)"
STREAM_PARENT_SPAN_ID="$(new_span_id)"
STREAM_OUTPUT="$(curl -fsS -N \
  -X POST "$STREAM_API_URL" \
  -H "Content-Type: application/json" \
  -H "Traceparent: 00-$STREAM_TRACE_ID-$STREAM_PARENT_SPAN_ID-01" \
  -d '{"prompt":"stream request"}')"
printf "%s\n" "$STREAM_OUTPUT"
if ! grep -q "event: done" <<<"$STREAM_OUTPUT"; then
  echo "Expected streaming response to include a done event"
  exit 1
fi
echo ""

echo "[4] Trace spans"
TRACE_COUNT="$(curl -fsS "$BASE_URL/traces?trace_id=$TRACE_ID" | jq -r '.spans | length')"
if (( TRACE_COUNT < 5 )); then
  echo "Expected at least 5 spans for $TRACE_ID, got $TRACE_COUNT"
  exit 1
fi
curl -fsS "$BASE_URL/traces?trace_id=$TRACE_ID" \
  | jq '.spans[] | {name, span_id, parent_span_id, status, duration_micros, attributes}'
echo ""

echo "[5] OTLP JSON"
OTLP_COUNT="$(curl -fsS "$BASE_URL/traces/otlp?trace_id=$TRACE_ID" | jq -r '[.resourceSpans[].scopeSpans[].spans[]] | length')"
if (( OTLP_COUNT < 5 )); then
  echo "Expected at least 5 OTLP spans for $TRACE_ID, got $OTLP_COUNT"
  exit 1
fi
curl -fsS "$BASE_URL/traces/otlp?trace_id=$TRACE_ID" \
  | jq '.resourceSpans[0].scopeSpans[0].spans[] | {name, traceId, spanId, parentSpanId, kind, status}'
echo ""

echo "[6] Concurrent batch"
for i in $(seq 1 8); do
  batch_trace_id="$(printf "%032x" "$i")"
  batch_parent_span_id="$(printf "%016x" "$i")"
  curl -fsS \
    -X POST "$API_URL" \
    -H "Content-Type: application/json" \
    -H "Traceparent: 00-$batch_trace_id-$batch_parent_span_id-01" \
    -d "{\"prompt\":\"batch request $i\"}" \
    >/dev/null &
done
wait
echo "Completed 8 concurrent requests"
echo ""

echo "[7] Metrics"
curl -fsS "$BASE_URL/metrics" | awk '
  /batch_size_distribution_(bucket|sum|count)|request_duration_seconds_count|worker_errors_total|worker_batches_total|worker_circuit_open|requests_rejected_total|admission_(in_flight_tokens|accepted_tokens_total|rejected_tokens_total|rejected_requests_total)/ {
    if ($1 !~ /^#/) print
  }
'
echo ""

echo "Smoke test passed"
