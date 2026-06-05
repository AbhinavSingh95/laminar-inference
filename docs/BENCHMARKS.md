# Benchmark Methodology

Benchmark numbers should be treated as evidence only when the workload, machine, configuration, and measurement method are recorded. The benchmark matrix is for local exploration; it is not a universal performance claim.

## Recommended Comparison

Run the same workload against at least these configurations:

| Mode | Gateway config |
| --- | --- |
| No batching baseline | `LAMINAR_MAX_BATCH_SIZE=1` |
| Fixed dynamic batches | `LAMINAR_BATCH_POLICY=fixed LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| Adaptive batches | `LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |
| Adaptive batches, two workers | `LAMINAR_WORKER_COUNT=2 LAMINAR_BATCH_POLICY=adaptive LAMINAR_MAX_BATCH_SIZE=32 LAMINAR_MAX_BATCH_WAIT=10ms` |

For each run, capture:

- machine and OS
- Bazel version
- gateway environment variables
- worker count
- number of requests
- concurrency
- throughput
- p50, p95, and p99 client latency
- average observed batch size
- rejected request count
- worker error count
- per-worker batch and failure counts from `/stats`

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

Start two local workers for manual comparison:

```bash
LAMINAR_WORKER_COUNT=2 LAMINAR_BATCH_POLICY=adaptive ./run.sh
```

## Reporting Template

```text
Machine:
OS:
Bazel:
Gateway config:
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
