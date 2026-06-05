# Laminar Inference

Dynamic and adaptive batching gateway for inference workloads, implemented as a small Bazel monorepo with a Go HTTP gateway, load-aware worker pool, C++ gRPC worker simulator, protobuf service contract, Prometheus metrics, deterministic scheduler tests, and a reproducible local benchmark matrix.

This project is intentionally scoped as an inference-systems lab. The C++ worker is a deterministic latency simulator, not a model runtime. The goal is to make batching, queueing, cancellation, worker failure, and observability behavior easy to inspect and test.

## Why This Exists

Modern inference services often trade a small amount of queueing delay for much higher accelerator utilization. Laminar demonstrates that control plane:

- accept individual HTTP requests
- group them into bounded batches by size or time
- route each gRPC batch to a healthy worker
- fan out worker responses to the original callers
- expose latency, batch-size, worker, error, and queue metrics
- handle cancellation, overload, and worker failures explicitly

## Architecture

```text
HTTP clients
    |
    v
+-------------------+       gRPC        +----------------------+
| Go gateway         | ---------------> | C++ worker simulator  |
| - bounded queue    |                  +----------------------+
| - dynamic batcher  |       gRPC        +----------------------+
| - worker pool      | ---------------> | C++ worker simulator  |
| - Prometheus       |                  +----------------------+
+-------------------+
    |
    v
Prometheus metrics
```

Core paths:

- [gateway/main.go](gateway/main.go) contains the HTTP server, batch scheduler, backpressure, readiness, and worker client boundary.
- [gateway/worker_pool.go](gateway/worker_pool.go) routes batches across workers and isolates per-worker failures.
- [backend/main.cc](backend/main.cc) implements the C++ gRPC worker and deterministic batch latency model.
- [proto/inference.proto](proto/inference.proto) defines the cross-language contract.
- [gateway/main_test.go](gateway/main_test.go) and [gateway/worker_pool_test.go](gateway/worker_pool_test.go) test scheduler and routing behavior without live processes.

## What It Demonstrates

- Dynamic batching by max batch size and max wait time
- Adaptive batching that reacts to queue pressure, worker latency, and failures
- Least-loaded multi-worker routing with per-worker circuit breakers
- Polyglot build orchestration with Bazel, Go, C++, gRPC, and protobuf
- Bounded queue backpressure instead of unbounded request accumulation
- Cancellation isolation so one cancelled caller cannot poison a whole batch
- Circuit breaker behavior for repeated worker failures
- Request correlation through `trace_id` propagation
- Prometheus metrics for request latency, worker latency, per-worker load, batch size, queue depth, rejected requests, and cancellations
- Reproducible unit tests for concurrency-sensitive scheduler behavior

## Quick Start

Prerequisites:

- Bazel 8.5 or newer
- macOS or Linux with a C++17 toolchain

Build everything:

```bash
bazel build //...
```

Run the worker and gateway:

```bash
./run.sh
```

Run the gateway with two local worker simulators:

```bash
LAMINAR_WORKER_COUNT=2 ./run.sh
```

In another terminal:

```bash
curl -X POST http://localhost:8080/predict \
  -H "Content-Type: application/json" \
  -H "X-Trace-ID: demo-001" \
  -d '{"prompt":"Explain dynamic batching"}'
```

Useful endpoints:

- `POST /predict` submits an inference request
- `GET /health` checks gateway liveness
- `GET /ready` reports circuit-breaker readiness
- `GET /stats` shows runtime config, batch policy state, and per-worker state
- `GET /metrics` exposes Prometheus metrics

## Tests

Run all Bazel tests:

```bash
bazel test //...
```

The gateway tests cover:

- flush when `MaxBatchSize` is reached
- flush when `MaxBatchWaitTime` expires
- request cancellation before and during worker RPCs
- bounded queue overload behavior
- circuit breaker rejection after repeated worker failures
- least-loaded worker routing under concurrent batches
- per-worker failure isolation

The shell smoke test exercises the live HTTP/gRPC path:

```bash
./test.sh
```

## Configuration

The gateway is configured through environment variables:

| Variable | Default | Meaning |
| --- | --- | --- |
| `LAMINAR_HTTP_ADDR` | `:8080` | Gateway listen address |
| `LAMINAR_WORKER_ADDR` | `localhost:50051` | Worker gRPC address |
| `LAMINAR_WORKER_ADDRS` | unset | Comma-separated worker gRPC addresses. Overrides `LAMINAR_WORKER_ADDR` when set |
| `LAMINAR_BATCH_POLICY` | `fixed` | `fixed` or `adaptive` |
| `LAMINAR_MAX_BATCH_SIZE` | `32` | Max requests per batch |
| `LAMINAR_MAX_BATCH_WAIT` | `10ms` | Max time to wait before flushing a partial batch |
| `LAMINAR_ADAPTIVE_MIN_WAIT` | `1ms` | Shortest wait used by adaptive batching |
| `LAMINAR_ADAPTIVE_MAX_WAIT` | `10ms` | Longest wait used by adaptive batching |
| `LAMINAR_ADAPTIVE_TARGET_LATENCY` | `150ms` | Worker-latency target for adaptive wait adjustments |
| `LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK` | `32` | Queue depth where adaptive batching becomes latency-biased |
| `LAMINAR_QUEUE_SIZE` | `1000` | Bounded gateway queue capacity |
| `LAMINAR_WORKER_REQUEST_TIMEOUT` | `30s` | Deadline for one worker batch RPC |
| `LAMINAR_CIRCUIT_THRESHOLD` | `5` | Consecutive worker failures before opening the circuit |
| `LAMINAR_CIRCUIT_RESET_AFTER` | `30s` | Cooldown before probing the worker again |

`./run.sh` also supports `LAMINAR_WORKER_COUNT` for starting multiple local worker simulators on adjacent ports.

Example:

```bash
LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms ./run.sh
```

## Benchmarking

Generate a local benchmark matrix comparing no batching, fixed dynamic batching, adaptive batching, and adaptive batching with two workers:

```bash
./benchmark.sh
```

The script writes [docs/results/latest.md](docs/results/latest.md). Treat benchmark numbers as machine-local unless captured with the methodology in [docs/BENCHMARKS.md](docs/BENCHMARKS.md). A credible comparison should report at least throughput, p50/p95/p99 latency, average batch size, rejection rate, and the exact gateway configuration used.

## Current Limits

This is not yet a production inference server. The worker simulates accelerator behavior with `base_latency + per_request_latency * batch_size`; it does not execute a model. The next meaningful upgrades are:

- add a real model backend path such as ONNX Runtime or llama.cpp
- add OpenTelemetry spans across HTTP and gRPC
- add queue persistence or admission-control policies for more realistic overload experiments

## License

MIT. See [LICENSE](LICENSE).
