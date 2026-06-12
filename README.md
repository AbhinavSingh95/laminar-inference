<p align="center">
  <img src="docs/assets/laminar-inference-logo.svg" alt="Laminar Inference logo" width="760">
</p>

<p align="center">
  <a href="https://bazel.build/"><img alt="Build: Bazel" src="https://img.shields.io/badge/build-Bazel-43a047?style=flat-square"></a>
  <a href="https://go.dev/"><img alt="Gateway: Go" src="https://img.shields.io/badge/gateway-Go-00ADD8?style=flat-square"></a>
  <a href="https://isocpp.org/"><img alt="Worker: C++17" src="https://img.shields.io/badge/worker-C%2B%2B17-00599C?style=flat-square"></a>
  <a href="https://grpc.io/"><img alt="Protocol: gRPC" src="https://img.shields.io/badge/protocol-gRPC-244c5a?style=flat-square"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-111827?style=flat-square"></a>
</p>

## About

Laminar Inference is a small inference-serving systems lab. It implements a Go HTTP gateway, a C++ gRPC worker, dynamic/adaptive batching, multi-worker routing, backpressure, token-budget admission control, tracing, metrics, streaming responses, and several model/runtime backends including ONNX Runtime and llama.cpp.

The goal is not to be a drop-in production server. The goal is to make the hard parts of inference serving visible, testable, and benchmarkable.

## What This Shows

- Request batching with fixed and adaptive policies
- Least-loaded multi-worker routing with per-worker circuit breakers
- Bounded queue backpressure and token-budget admission control
- C++ worker backends for simulator, tiny model, tiny LLM, continuous tiny LLM, ONNX Runtime, llama.cpp CLI, and warmed llama.cpp server
- Continuous batching and KV-cache block simulation for LLM-style serving internals
- Client-facing streaming through `POST /predict/stream`
- Request tracing with W3C `Traceparent`, local trace inspection, and OTLP/JSON export
- Prometheus metrics for requests, batches, workers, queues, admission, cancellations, and failures
- Reproducible local benchmark matrix with runtime config, trace samples, and model metadata

## Architecture

```text
HTTP clients
    |
    v
+-------------------------+
| Go gateway              |
| - HTTP API              |
| - fixed/adaptive batch  |
| - token admission       |
| - worker pool routing   |
| - metrics and traces    |
+-----------+-------------+
            |
            | gRPC ProcessBatch / Stream
            v
+-------------------------+        optional HTTP/CLI/runtime calls
| C++ worker              | -----> ONNX Runtime
| - backend interface     | -----> llama-cli / GGUF
| - simulator             | -----> llama-server /completion
| - tiny model / tiny LLM |
| - continuous scheduler  |
+-------------------------+
```

Core implementation paths:

- [gateway/main.go](gateway/main.go): HTTP server, batching, backpressure, admission, readiness, and metrics
- [gateway/worker_pool.go](gateway/worker_pool.go): least-loaded worker routing and per-worker circuit state
- [gateway/tracing.go](gateway/tracing.go): W3C trace context, in-memory spans, and OTLP/JSON shape
- [gateway/otlp_exporter.go](gateway/otlp_exporter.go): optional background OTLP/HTTP export
- [backend/main.cc](backend/main.cc): C++ gRPC worker service
- [backend/inference_backend.cc](backend/inference_backend.cc): worker backend implementations
- [backend/scheduling/continuous_batcher.h](backend/scheduling/continuous_batcher.h): continuous batching and KV-cache simulation API
- [proto/inference.proto](proto/inference.proto): cross-language gRPC contract

## Backends

| Backend | Purpose |
| --- | --- |
| `simulator` | Deterministic latency model for fast scheduler and gateway tests |
| `tiny_model` | Hermetic fixed-weight text classifier-style inference path |
| `tiny_llm` | Hermetic decoder-only LLM path with tokenization, prefill, decode, and generation metadata |
| `continuous_tiny_llm` | Tiny LLM plus token-step scheduling, decode utilization, prefix cache, and KV-cache block metrics |
| `onnx` | Optional ONNX Runtime backend using the checked-in `models/logreg_iris.onnx` smoke model |
| `llama_cpp` | Optional local GGUF command adapter through `llama-cli` |
| `llama_server` | Optional warmed model path through llama.cpp server's native `/completion` endpoint |

The tiny backends are intentionally small and hermetic. They make serving mechanics testable without requiring external model downloads. The llama.cpp server path is the real local LLM benchmark path.

## Quick Start

Prerequisites:

- Bazel 8.5+
- Go toolchain managed through Bazel
- C++17 toolchain
- macOS or Linux

Build and test:

```bash
bazel build //...
bazel test //...
```

Run the default simulator worker and gateway:

```bash
./run.sh
```

Send a request:

```bash
curl -sS -X POST http://localhost:8080/predict \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Explain dynamic batching"}' | jq .
```

Run the live smoke test against a running gateway:

```bash
./test.sh
```

## Common Runs

Run with adaptive batching:

```bash
LAMINAR_BATCH_POLICY=adaptive ./run.sh
```

Run two local workers behind the gateway:

```bash
LAMINAR_WORKER_COUNT=2 LAMINAR_BATCH_POLICY=adaptive ./run.sh
```

Run the continuous tiny-LLM scheduler backend:

```bash
LAMINAR_WORKER_BACKEND=continuous_tiny_llm \
LAMINAR_BATCH_POLICY=adaptive \
./run.sh
```

Run with token-budget admission control:

```bash
LAMINAR_ADMISSION_ENABLED=true \
LAMINAR_ADMISSION_MAX_IN_FLIGHT_TOKENS=256 \
LAMINAR_ADMISSION_ESTIMATED_OUTPUT_TOKENS=32 \
LAMINAR_BATCH_POLICY=adaptive \
./run.sh
```

Run ONNX Runtime:

```bash
./scripts/setup_onnxruntime.sh
LAMINAR_WORKER_BACKEND=onnx ./run.sh
```

Run against a warmed llama.cpp server:

```bash
LAMINAR_WORKER_BACKEND=llama_server \
LAMINAR_LLAMA_SERVER_URL=http://127.0.0.1:18081/completion \
LAMINAR_LLAMA_SERVER_MAX_TOKENS=64 \
./run.sh
```

## Real Local LLM Benchmark

The easiest real-model path uses llama.cpp server and a small Qwen2.5 0.5B GGUF model:

```bash
./scripts/benchmark_real_llm.sh --install --requests 20 --concurrency 4
```

This script:

- installs/checks llama.cpp tools
- starts a warmed `llama-server` when needed
- runs only the `llama.cpp server` benchmark scenarios
- writes a Markdown report to `docs/results/llama-server-latest.md`
- writes runtime metadata to `docs/results/llama-server-latest.metadata.json`
- keeps model binaries out of the repository

More detail: [docs/LLM_BENCHMARKING.md](docs/LLM_BENCHMARKING.md).

## Benchmarking

Run the full local benchmark matrix:

```bash
./benchmark.sh
```

Run a focused continuous-scheduler benchmark:

```bash
./benchmark.sh --scenario "continuous tiny llm" --requests 60 --concurrency 20
```

Run the overload/admission-control scenario:

```bash
./benchmark.sh --scenario "admission control overload" --requests 60 --concurrency 20
```

Benchmark reports include throughput, p50/p95/p99 latency, average batch size, rejection count, runtime policy snapshots, admission snapshots, worker snapshots, and trace samples.

Reference docs:

- [docs/BENCHMARKS.md](docs/BENCHMARKS.md): methodology and scenario guide
- [docs/results/latest.md](docs/results/latest.md): latest benchmark matrix output
- [docs/results/llama-server-latest.md](docs/results/llama-server-latest.md): latest real llama.cpp server result

Benchmark numbers are local-machine evidence, not universal performance claims.

## Observability

Useful endpoints:

| Endpoint | Description |
| --- | --- |
| `GET /health` | Gateway liveness |
| `GET /ready` | Readiness based on worker circuit state |
| `GET /stats` | Runtime config, batch policy, admission state, and worker snapshots |
| `GET /metrics` | Prometheus metrics |
| `GET /traces?trace_id=...` | Local request span snapshot |
| `GET /traces/otlp?trace_id=...` | OTLP/JSON-shaped span export |
| `POST /predict` | Non-streaming inference request |
| `POST /predict/stream` | Server-sent event stream with `token` and `done` events |

Run a local fake OTLP collector:

```bash
python3 scripts/fake_otlp_collector.py --port 4318
LAMINAR_OTLP_ENDPOINT=http://localhost:4318/v1/traces ./run.sh
```

## Repository Layout

```text
backend/                 C++ worker and inference backends
backend/scheduling/      Continuous batching and KV-cache simulation
gateway/                 Go HTTP gateway, batching, routing, tracing, admission
proto/                   gRPC/protobuf contract
models/                  Small checked-in ONNX smoke model
scripts/                 Benchmark and runtime setup helpers
third_party/onnxruntime/ Optional ONNX Runtime headers/build glue
docs/                    Architecture, benchmarking, LLM setup, roadmap, results
docs/assets/             README artwork and project assets
```

## Documentation

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): detailed system architecture
- [docs/BENCHMARKS.md](docs/BENCHMARKS.md): benchmark methodology
- [docs/LLM_BENCHMARKING.md](docs/LLM_BENCHMARKING.md): real local LLM setup
- [docs/ROADMAP.md](docs/ROADMAP.md): completed milestones and next systems work

## Current Limits

Laminar is a focused systems project, not a hardened production inference platform. The most important remaining upgrades are:

- fault-injection scenarios for slow workers, timeouts, cancellation storms, and KV-cache exhaustion
- priority/deadline-aware scheduling
- a documented two-llama-server multi-worker benchmark recipe
- optional OpenTelemetry Collector configuration for external trace ingestion

## License

Released under the [MIT License](LICENSE).
