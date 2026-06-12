# Engineering Roadmap

This roadmap is ordered by reviewer impact: first make the system correct and measurable, then deepen the inference-specific work.

## Completed

### Credible Core

- Keep the repository buildable with `bazel build //...`.
- Cover scheduler behavior with deterministic `bazel test //...` tests.
- Expose queue, latency, batch-size, rejection, and failure metrics.
- Keep the simulator as a deterministic baseline backend.

### Benchmark Evidence

- Add a reproducible local harness that restarts the gateway with different batching configurations. Completed in `scripts/benchmark_matrix.py`.
- Compare no batching, fixed dynamic batching, adaptive batching, multi-worker routing, model-backed modes, and admission overload.
- Publish p50, p95, p99, throughput, average batch size, rejection rate, runtime snapshots, and trace samples.
- Save representative benchmark output under `docs/results/`.

### Adaptive Batching

- Add a policy interface for batching decisions. Completed in `gateway/policy.go`.
- Implement a latency-aware adaptive policy that adjusts wait time based on observed queue depth and worker latency.
- Test policy transitions with deterministic tests. Completed in `gateway/policy_test.go`.
- Compare adaptive batching against fixed dynamic batching with the benchmark matrix.

### Multi-Worker Serving

- Add worker registration and health tracking. Completed in `gateway/worker_pool.go`.
- Route batches by least-loaded worker with deterministic tie-breaking.
- Add failure isolation per worker. Completed in `gateway/worker_pool_test.go`.
- Expose per-worker metrics and `/stats` snapshots.
- Compare single-worker and two-worker adaptive batching in the benchmark matrix.

### Real Inference Paths

- Add a pluggable worker backend. Completed in `backend/inference_backend.cc`.
- Add a hermetic tiny text model backend.
- Add a hermetic decoder-only tiny LLM backend with tokenization, prefill timing, causal decode timing, generation controls, and deterministic tests.
- Add ONNX Runtime as an optional runtime-loaded backend.
- Add optional llama.cpp/GGUF integration through `llama-cli`.
- Add a warmed llama.cpp server backend through native `/completion`.
- Add client-facing streaming token responses with `POST /predict/stream`.

### Distributed Observability

- Add request-scoped tracing across HTTP, queueing, batching, worker RPC, and backend inference. Completed with `gateway/tracing.go`.
- Propagate W3C `Traceparent` through the HTTP boundary.
- Export spans in an OTLP/JSON-shaped envelope through `/traces/otlp`.
- Push spans to an OTLP/HTTP endpoint in the background with bounded buffering and retry-on-next-flush behavior.
- Record token-level trace spans for streaming inference events.

### LLM Serving Internals

- Add a reproducible real small-LLM benchmark lane with llama.cpp setup, server lifecycle, filtered benchmark scenarios, and runtime metadata.
- Add continuous batching and KV-cache simulation in `backend/scheduling/continuous_batcher.*`.
- Add `LAMINAR_WORKER_BACKEND=continuous_tiny_llm` with per-sequence first-token/completion step metadata, decode batch utilization, KV-cache high watermark, prefix-cache hit/miss counters, and benchmark matrix scenarios.
- Add token-budget admission control with early `admission_rejected` responses, admission metrics, `/stats` snapshots, and overload benchmark evidence.

## Next Up

- Add fault-injection scenarios for slow workers, cancellation storms, backend timeouts, and KV-cache exhaustion.
- Add priority/deadline-aware scheduling for interactive vs batch requests.
- Add an example OpenTelemetry Collector config for users who want external trace ingestion.
- Add a two-llama-server benchmark recipe for multi-worker real-model serving.
