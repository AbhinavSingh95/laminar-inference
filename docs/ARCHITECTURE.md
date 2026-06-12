# Architecture

Laminar Inference is a compact inference serving control plane. It focuses on the gateway behavior around queueing, batching, cancellation, failure handling, and observability.

## Request Lifecycle

1. A client sends `POST /predict` to the Go gateway.
2. The gateway validates the prompt and propagates W3C `Traceparent` when present, falling back to `X-Trace-ID` or a generated trace ID.
3. The gateway starts an `http.predict` span and the request enters a bounded in-memory queue.
4. The batcher records queue wait, batch flush, worker RPC, and backend inference spans for that request.
5. The batcher flushes when either:
   - `MaxBatchSize` requests are waiting, or
   - `MaxBatchWaitTime` expires for a partial batch.
6. The worker pool selects a healthy worker with the lowest current in-flight batch count.
7. The gateway sends one `ProcessBatch` gRPC call to that worker.
8. The worker executes the selected backend and returns one response per request.
9. The gateway fans out responses to the original HTTP handlers.

For client-facing streaming, a client sends `POST /predict/stream`. That path is intentionally single-request rather than batched: the gateway selects a healthy worker, opens the gRPC `Stream` RPC, and proxies worker events as SSE `token` and `done` events. Non-streaming backends fall back to one final `done` event; the warmed `llama_server` backend emits real token deltas as it reads llama.cpp server SSE chunks.

## Batching Policies

The fixed policy is deterministic dynamic batching:

```text
flush(batch) when len(batch) >= max_batch_size
flush(batch) when oldest request waits max_batch_wait_time
```

The adaptive policy uses the fixed policy as a baseline, then adjusts wait time based on observed worker latency, queue depth, and failures:

```text
decrease wait when worker latency exceeds target
decrease wait when queue depth crosses the high watermark
increase wait when batches are consistently small and latency is healthy
flush half-full batches early when queue pressure is high
```

This policy is intentionally explainable rather than magic. The adaptive state is visible through `/stats`, and the policy behavior is tested directly in `gateway/policy_test.go`.

## Worker Backends

The C++ worker exposes one gRPC service and delegates execution to a pluggable backend selected by `LAMINAR_WORKER_BACKEND`.

| Backend | Purpose |
| --- | --- |
| `simulator` | Deterministic latency model for controlled scheduler tests and simple throughput demonstrations |
| `tiny_model` | Hermetic tiny text model that featurizes prompts, runs fixed-weight batched MLP-style inference, and returns a label plus confidence |
| `tiny_llm` | Hermetic tiny decoder-only LLM path that tokenizes prompts, runs prefill over prompt tokens, performs causal decode steps, and returns generated text plus prefill/decode timing metadata |
| `continuous_tiny_llm` | Hermetic tiny LLM path with a token-step scheduler and KV-cache block allocator simulation for prefill/decode interleaving, prefix-cache sharing, and memory-pressure evidence |
| `onnx` | Runtime-loaded ONNX Runtime backend that executes `models/logreg_iris.onnx` through the ORT C API |
| `llama_cpp` | Optional external runtime adapter that executes a local GGUF model through `llama-cli`, captures output, and enforces timeout/cancellation behavior |
| `llama_server` | Optional warmed-model adapter that calls llama.cpp server's native `/completion` endpoint, passes streaming prompt-cache-aware requests, emits token events, and captures structured server token/timing metadata |

The tiny model, tiny LLM, and continuous tiny LLM are intentionally small. They are not benchmarks for model quality; they exist to make the serving path exercise real CPU inference math, decoder-style generation mechanics, and low-level serving-scheduler behavior without requiring external model downloads or runtime binaries. The ONNX backend adds an actual external inference runtime without making normal builds depend on a checked-in binary. `scripts/setup_onnxruntime.sh` downloads a pinned ONNX Runtime release and verifies its SHA-256 checksum.

The continuous tiny-LLM scheduler lives in `backend/scheduling/continuous_batcher.h` and `backend/scheduling/continuous_batcher.cc`. It models sequence lifecycle as queued prefill, active decode, and completion; each scheduler step has a prefill token budget and a decode sequence budget. Its KV-cache block allocator tracks resident blocks, high watermark, prefix-cache hits and misses, idle prefix eviction, allocation failures, and per-sequence first-token/completion steps. The backend still uses the deterministic tiny LLM for generated text, but attaches scheduler metadata to every response so benchmarks and traces can inspect prefill/decode utilization and memory pressure.

The `llama_cpp` backend is the larger-model bridge. It is deliberately optional: normal builds and tests do not require `llama-cli` or a GGUF file, while benchmark runs can include local LLM evidence when `LAMINAR_LLAMA_CPP_MODEL` and `LAMINAR_LLAMA_CPP_BINARY` are configured. That adapter launches one llama.cpp command per prompt, so it is best understood as a reproducible runtime-integration path rather than a production throughput architecture.

The `llama_server` backend is the warmed-model path. It assumes a separate `llama-server` process has already loaded the GGUF model, then the C++ worker sends HTTP/JSON requests to `/completion` with `stream=true`, `n_predict`, temperature, `cache_prompt`, `return_tokens`, and `timings_per_token`. The worker parses server-sent-event `data:` chunks, emits token events for the gRPC `Stream` RPC, concatenates streamed content, records chunk count, token counters, prompt/generated sizes, and a first-response-byte latency proxy. This avoids process-per-prompt startup cost and makes the project closer to real serving architecture while still keeping the repository free of heavyweight model artifacts.

## Worker Pool

The gateway supports one or more gRPC worker addresses. `LAMINAR_WORKER_ADDRS` accepts a comma-separated list, and `./run.sh` can start multiple local workers with `LAMINAR_WORKER_COUNT`.

Routing is deliberately small and inspectable:

```text
eligible workers = workers whose per-worker circuit is closed
selected worker = eligible worker with the lowest in-flight batch count
tie breaker = lowest total routed batches, then stable endpoint order
```

Each worker tracks in-flight batches, total batches, total failures, consecutive failures, last latency, last error, and circuit state. This keeps a single failing worker from making the whole gateway unavailable while other workers are healthy.

## Backpressure

The gateway uses a bounded channel for the request queue. If the queue is full, `Submit` returns `queue_full` immediately and the HTTP layer responds with `503`.

When `LAMINAR_ADMISSION_ENABLED=true`, the gateway also applies token-budget admission control before queueing. It estimates each request as `ceil(prompt_bytes * LAMINAR_ADMISSION_ESTIMATED_TOKENS_PER_BYTE) + LAMINAR_ADMISSION_ESTIMATED_OUTPUT_TOKENS`; if admitting that estimate would exceed `LAMINAR_ADMISSION_MAX_IN_FLIGHT_TOKENS`, the request is rejected immediately with `admission_rejected` and HTTP `429`. Accepted token leases are released when a response, cancellation, queue rejection, or worker failure reaches a terminal path.

That trade-off is deliberate: rejecting quickly is usually better than hiding overload behind unbounded memory growth and unpredictable tail latency.

## Cancellation

HTTP request cancellation is honored while queueing and while waiting for a worker response. Cancelled requests are removed before a batch is sent.

The worker RPC itself uses a gateway-owned timeout context rather than the first request's HTTP context. This prevents one caller cancelling its request from cancelling the shared batch RPC for other callers.

## Failure Handling

Each worker has its own consecutive failure counter. Once the threshold is reached for a worker, that worker's circuit opens and routing skips it until the reset window elapses.

The batcher keeps an aggregate readiness guard for the single-worker path and for the case where a worker pool has no available workers. This is not a full production circuit breaker. It is a minimal, inspectable implementation that makes the failure mode explicit and testable.

## Observability

The gateway exposes Prometheus metrics:

- `batch_size_distribution`
- `request_duration_seconds`
- `batch_latency_seconds`
- `worker_errors_total`
- `worker_in_flight_batches`
- `worker_batches_total`
- `worker_failures_total`
- `worker_batch_latency_seconds`
- `worker_circuit_open`
- `requests_cancelled_total`
- `requests_rejected_total`
- `request_queue_depth`
- `admission_in_flight_tokens`
- `admission_accepted_tokens_total`
- `admission_rejected_tokens_total`
- `admission_rejected_requests_total`

The `/stats` endpoint reports runtime configuration and circuit state for quick local inspection.
For adaptive runs, it also reports the current policy wait, target latency, last observed batch size, last queue depth, and number of policy adjustments.
For admission-control runs, it reports in-flight estimated tokens, accepted/rejected token totals, and accepted/rejected request counts.
For multi-worker runs, it reports per-worker load, failure, and circuit state.

The `/traces` endpoint returns recent in-memory spans, and `/traces/otlp` returns the same valid W3C traces as an OTLP/JSON-shaped export envelope with `resourceSpans`, `scopeSpans`, lowerCamelCase fields, hex IDs, and string nanosecond timestamps. When `LAMINAR_OTLP_ENDPOINT` is set, the gateway also starts a background OTLP/HTTP push exporter.

A single request trace contains:

- `http.predict`
- `batch.queue_wait`
- `batch.flush`
- `worker.grpc`
- `backend.inference`

Streaming request traces contain `http.predict.stream`, `worker.grpc.stream`, `backend.inference`, and one `backend.token` span per token event.

The HTTP handler accepts W3C `Traceparent` headers and returns a response `Traceparent` whose span ID is the gateway's HTTP span. The worker returns `backend_name`, `backend_latency_micros`, and a backend metadata map in the protobuf response, so the gateway can attribute backend execution time separately from queueing and gRPC overhead while preserving model-specific evidence. For llama.cpp server runs, the HTTP `/predict` response includes metadata such as `stream`, `stream_chunks`, `tokens_predicted`, `tokens_evaluated`, and `ttft_micros`; the backend span mirrors these as `backend.*` attributes. The streaming endpoint emits SSE payloads with the same metadata shape and records token spans as deltas arrive. For batches containing multiple HTTP requests, Laminar records a logical span chain per request trace rather than attaching all requests to one shared trace. That keeps each trace independently readable while still recording the physical batch size as span attributes.

The push exporter is intentionally fail-soft. Completed spans are enqueued into a bounded buffer, flushed by batch size or time interval, and posted as OTLP/HTTP JSON. Collector errors leave the spans pending for the next flush; queue overflow is counted and dropped rather than blocking request serving.

## Current Worker Model

The default worker backend is a deterministic simulator:

```text
latency = 20ms + (5ms * batch_size)
```

That model captures the shape of amortized fixed overhead. The `tiny_model`, `tiny_llm`, `continuous_tiny_llm`, `onnx`, `llama_cpp`, and `llama_server` backends add real inference paths and scheduler evidence, including a client-facing streaming path for warmed llama.cpp server runs.
