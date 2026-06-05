# Architecture

Laminar Inference is a compact inference serving control plane. It focuses on the gateway behavior around queueing, batching, cancellation, failure handling, and observability.

## Request Lifecycle

1. A client sends `POST /predict` to the Go gateway.
2. The gateway validates the prompt and assigns or propagates an `X-Trace-ID`.
3. The request enters a bounded in-memory queue.
4. The batcher flushes when either:
   - `MaxBatchSize` requests are waiting, or
   - `MaxBatchWaitTime` expires for a partial batch.
5. The worker pool selects a healthy worker with the lowest current in-flight batch count.
6. The gateway sends one `ProcessBatch` gRPC call to that worker.
7. The worker simulates batch inference latency and returns one response per request.
8. The gateway fans out responses to the original HTTP handlers.

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

## Worker Pool

The gateway supports one or more gRPC worker addresses. `LAMINAR_WORKER_ADDRS` accepts a comma-separated list, and `./run.sh` can start multiple local worker simulators with `LAMINAR_WORKER_COUNT`.

Routing is deliberately small and inspectable:

```text
eligible workers = workers whose per-worker circuit is closed
selected worker = eligible worker with the lowest in-flight batch count
tie breaker = lowest total routed batches, then stable endpoint order
```

Each worker tracks in-flight batches, total batches, total failures, consecutive failures, last latency, last error, and circuit state. This keeps a single failing worker from making the whole gateway unavailable while other workers are healthy.

## Backpressure

The gateway uses a bounded channel for the request queue. If the queue is full, `Submit` returns `queue_full` immediately and the HTTP layer responds with `503`.

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

The `/stats` endpoint reports runtime configuration and circuit state for quick local inspection.
For adaptive runs, it also reports the current policy wait, target latency, last observed batch size, last queue depth, and number of policy adjustments.
For multi-worker runs, it reports per-worker load, failure, and circuit state.

## Current Worker Model

The C++ worker is a deterministic simulator:

```text
latency = 20ms + (5ms * batch_size)
```

That model captures the shape of amortized fixed overhead, but it is not a substitute for a real model runtime. The next major architecture step is to add a pluggable worker backend and run at least one real inference path.
