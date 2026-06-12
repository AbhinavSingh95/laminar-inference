# Benchmark Methodology

Benchmark numbers should be treated as evidence only when the workload, machine, configuration, and measurement method are recorded. The benchmark matrix is for local exploration; it is not a universal performance claim.

## Recommended Comparison

Run the same workload against at least these configurations:

| Mode | Config |
| --- | --- |
| No batching baseline | `LAMINAR_MAX_BATCH_SIZE=1` |
| Fixed dynamic batches | `LAMINAR_BATCH_POLICY=fixed LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| Adaptive batches | `LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| Adaptive batches, two workers | `LAMINAR_WORKER_COUNT=2 LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| Tiny model, no batching | `LAMINAR_WORKER_BACKEND=tiny_model LAMINAR_MAX_BATCH_SIZE=1` |
| Tiny model, adaptive batches | `LAMINAR_WORKER_BACKEND=tiny_model LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| Tiny model, adaptive batches, two workers | `LAMINAR_WORKER_BACKEND=tiny_model LAMINAR_WORKER_COUNT=2 LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| Tiny LLM, no batching | `LAMINAR_WORKER_BACKEND=tiny_llm LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS=8 LAMINAR_MAX_BATCH_SIZE=1` |
| Tiny LLM, adaptive batches | `LAMINAR_WORKER_BACKEND=tiny_llm LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS=8 LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| Tiny LLM, adaptive batches, two workers | `LAMINAR_WORKER_BACKEND=tiny_llm LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS=8 LAMINAR_WORKER_COUNT=2 LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| Continuous tiny LLM, no batching | `LAMINAR_WORKER_BACKEND=continuous_tiny_llm LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS=8 LAMINAR_MAX_BATCH_SIZE=1` |
| Continuous tiny LLM, adaptive batches | `LAMINAR_WORKER_BACKEND=continuous_tiny_llm LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS=8 LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| Continuous tiny LLM, adaptive batches, two workers | `LAMINAR_WORKER_BACKEND=continuous_tiny_llm LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS=8 LAMINAR_WORKER_COUNT=2 LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| Continuous tiny LLM, admission overload | `LAMINAR_WORKER_BACKEND=continuous_tiny_llm LAMINAR_ADMISSION_ENABLED=true LAMINAR_ADMISSION_MAX_IN_FLIGHT_TOKENS=40 LAMINAR_BATCH_POLICY=adaptive` |
| ONNX Runtime, no batching | `LAMINAR_WORKER_BACKEND=onnx LAMINAR_MAX_BATCH_SIZE=1` |
| ONNX Runtime, adaptive batches | `LAMINAR_WORKER_BACKEND=onnx LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| ONNX Runtime, adaptive batches, two workers | `LAMINAR_WORKER_BACKEND=onnx LAMINAR_WORKER_COUNT=2 LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| llama.cpp/GGUF, no batching | `LAMINAR_WORKER_BACKEND=llama_cpp LAMINAR_LLAMA_CPP_MODEL=/path/to/model.gguf LAMINAR_MAX_BATCH_SIZE=1` |
| llama.cpp/GGUF, adaptive batches | `LAMINAR_WORKER_BACKEND=llama_cpp LAMINAR_LLAMA_CPP_MODEL=/path/to/model.gguf LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| llama.cpp server, no batching | `LAMINAR_WORKER_BACKEND=llama_server LAMINAR_LLAMA_SERVER_URL=http://127.0.0.1:8081/completion LAMINAR_MAX_BATCH_SIZE=1` |
| llama.cpp server, adaptive batches | `LAMINAR_WORKER_BACKEND=llama_server LAMINAR_LLAMA_SERVER_URL=http://127.0.0.1:8081/completion LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |

For each run, capture:

- machine and OS
- Bazel version
- gateway environment variables
- worker backend
- worker count
- number of requests
- concurrency
- throughput
- p50, p95, and p99 client latency
- average observed batch size
- rejected request count
- admission snapshot: in-flight tokens, accepted tokens, rejected tokens, accepted requests, rejected requests
- worker error count
- per-worker batch and failure counts from `/stats`
- continuous-scheduler metadata for `continuous_tiny_llm`, especially `scheduler.average_decode_batch_size`, `sequence.first_token_step`, `kv_cache.high_watermark_blocks`, `kv_cache.prefix_cache_hits`, and `kv_cache.evictions`
- response metadata for model-backed runs, especially token counts and first-response latency on llama.cpp server
- one representative `/traces?trace_id=...` sample for queue, batch, worker, and backend attribution
- one representative `/traces/otlp?trace_id=...` sample for OTLP/JSON shape and collector-style inspection

## Useful Commands

Start the system with a no-batching baseline:

```bash
LAMINAR_MAX_BATCH_SIZE=1 ./run.sh
```

Start the default dynamic batching configuration:

```bash
LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms ./run.sh
```

Run the local benchmark matrix:

```bash
./benchmark.sh
```

Use a smaller quick run while iterating:

```bash
./benchmark.sh --requests 60 --concurrency 20
```

The report is written to `docs/results/latest.md`.

Run only scenarios whose names match a filter:

```bash
./benchmark.sh --scenario "tiny llm" --requests 20 --concurrency 4
```

Run the real small-LLM lane against a warmed llama.cpp server:

```bash
./scripts/benchmark_real_llm.sh --install --requests 20 --concurrency 4
```

That wrapper installs/checks llama.cpp, starts `llama-server` when needed, runs only the `llama.cpp server` scenarios, writes `docs/results/llama-server-latest.md`, and records runtime metadata next to the report. See `docs/LLM_BENCHMARKING.md` for model selection, local GGUF overrides, and result interpretation.

Inspect a single request trace from a running gateway:

```bash
curl -sS -X POST "http://localhost:8080/predict" \
  -H "Content-Type: application/json" \
  -H "Traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" \
  -d '{"prompt":"trace benchmark sample"}'

curl -sS "http://localhost:8080/traces?trace_id=4bf92f3577b34da6a3ce929d0e0e4736" \
  | jq '.spans[] | {name, span_id, parent_span_id, status, duration_micros, attributes}'

curl -sS "http://localhost:8080/traces/otlp?trace_id=4bf92f3577b34da6a3ce929d0e0e4736" \
  | jq '.resourceSpans[0].scopeSpans[0].spans[] | {name, traceId, spanId, parentSpanId, kind, status}'
```

Inspect client-facing SSE streaming from a running gateway:

```bash
curl -N -sS -X POST "http://localhost:8080/predict/stream" \
  -H "Content-Type: application/json" \
  -H "Traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" \
  -d '{"prompt":"stream benchmark sample"}'
```

Start two local workers for manual comparison:

```bash
LAMINAR_WORKER_COUNT=2 LAMINAR_BATCH_POLICY=adaptive ./run.sh
```

Start the tiny model backend:

```bash
LAMINAR_WORKER_BACKEND=tiny_model LAMINAR_BATCH_POLICY=adaptive ./run.sh
```

Start the tiny decoder-only LLM backend:

```bash
LAMINAR_WORKER_BACKEND=tiny_llm LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS=8 LAMINAR_BATCH_POLICY=adaptive ./run.sh
```

Start the continuous tiny-LLM scheduler backend:

```bash
LAMINAR_WORKER_BACKEND=continuous_tiny_llm \
LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS=8 \
LAMINAR_CONTINUOUS_MAX_PREFILL_TOKENS_PER_STEP=32 \
LAMINAR_CONTINUOUS_MAX_DECODE_SEQUENCES_PER_STEP=8 \
LAMINAR_CONTINUOUS_KV_CACHE_BLOCKS=512 \
LAMINAR_CONTINUOUS_KV_BLOCK_TOKENS=16 \
LAMINAR_CONTINUOUS_PREFIX_CACHE_TOKENS=2 \
LAMINAR_BATCH_POLICY=adaptive \
./run.sh
```

Benchmark only the continuous scheduler scenarios:

```bash
./benchmark.sh --scenario "continuous tiny llm" --requests 60 --concurrency 20
```

Use this mode to inspect token-step serving behavior without external model downloads. Response metadata records scheduler steps, decode batch utilization, per-sequence first-token step, KV-cache high watermark, prefix-cache hits/misses, and evictions.

Benchmark the overload path with token-budget admission control:

```bash
./benchmark.sh --scenario "admission control overload" --requests 60 --concurrency 20
```

This scenario uses a deliberately small token budget. A good result is not zero rejections; a good result shows early, explicit `admission_rejected` responses, bounded in-flight token load, no worker errors, and recovery after pressure drops.

Install and start the ONNX Runtime backend:

```bash
./scripts/setup_onnxruntime.sh
LAMINAR_WORKER_BACKEND=onnx LAMINAR_BATCH_POLICY=adaptive ./run.sh
```

Start a local llama.cpp/GGUF backend:

```bash
LAMINAR_WORKER_BACKEND=llama_cpp \
LAMINAR_LLAMA_CPP_BINARY=/path/to/llama-cli \
LAMINAR_LLAMA_CPP_MODEL=/path/to/model.gguf \
LAMINAR_LLAMA_CPP_MAX_TOKENS=32 \
LAMINAR_BATCH_POLICY=adaptive \
./run.sh
```

Include llama.cpp scenarios in the benchmark matrix:

```bash
LAMINAR_LLAMA_CPP_BINARY=/path/to/llama-cli \
LAMINAR_LLAMA_CPP_MODEL=/path/to/model.gguf \
./benchmark.sh --requests 20 --concurrency 4
```

The llama.cpp scenarios are skipped when the binary or GGUF model is not configured. The current adapter starts a llama.cpp process per prompt, so benchmark it as external-runtime integration evidence rather than as the final warmed-model throughput path.

Start against a warmed llama.cpp server:

```bash
LAMINAR_WORKER_BACKEND=llama_server \
LAMINAR_LLAMA_SERVER_URL=http://127.0.0.1:8081/completion \
LAMINAR_LLAMA_SERVER_MAX_TOKENS=32 \
LAMINAR_LLAMA_SERVER_STREAM=true \
LAMINAR_LLAMA_SERVER_TIMINGS_PER_TOKEN=true \
LAMINAR_BATCH_POLICY=adaptive \
./run.sh
```

Include warmed llama.cpp server scenarios in the benchmark matrix:

```bash
LAMINAR_LLAMA_SERVER_URL=http://127.0.0.1:8081/completion \
LAMINAR_LLAMA_SERVER_STREAM=true \
LAMINAR_LLAMA_SERVER_TIMINGS_PER_TOKEN=true \
./benchmark.sh --requests 20 --concurrency 4
```

The llama.cpp server scenarios are skipped when `LAMINAR_LLAMA_SERVER_URL` is not configured or the server `/health` endpoint is not reachable. This path is the better benchmark for warmed-model serving because model loading happens outside each request. Response samples should include `metadata` with fields such as `stream`, `stream_chunks`, `tokens_predicted`, `tokens_evaluated`, and `ttft_micros`; the trace sample should mirror these on the `backend.inference` span.

Run with a local OTLP/HTTP collector stub:

```bash
python3 scripts/fake_otlp_collector.py --port 4318
LAMINAR_OTLP_ENDPOINT=http://localhost:4318/v1/traces LAMINAR_BATCH_POLICY=adaptive ./run.sh
```

## Reporting Template

```text
Machine:
OS:
Bazel:
Gateway config:
Worker backend:
Worker count:
Worker latency model:
Requests:
Concurrency:
Throughput:
p50:
p95:
p99:
Average batch size:
Rejected requests:
Worker errors:
Per-worker stats:
Notes:
```

## Reviewer Guidance

A strong result is not simply "batching is faster." A strong result explains the trade-off: where throughput improves, how much latency is added, what happens at saturation, and when backpressure starts.
