# Benchmark Results

Generated: `2026-06-05 12:47:00 AEST`

These numbers are local-machine evidence, not universal performance claims.

| Scenario | Workers | Requests | Concurrency | Success | Throughput (req/s) | p50 (s) | p95 (s) | p99 (s) | Avg Batch | Rejected | Worker Errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| no batching baseline | 1 | 60 | 20 | 60 | 29.94 | 0.6604 | 0.6754 | 0.6775 | 1.00 | 0 | 0 |
| fixed dynamic batching | 1 | 60 | 20 | 60 | 122.33 | 0.1629 | 0.1650 | 0.1653 | 20.00 | 0 | 0 |
| adaptive batching | 1 | 60 | 20 | 60 | 117.89 | 0.1704 | 0.1743 | 0.1756 | 20.00 | 0 | 0 |
| adaptive batching, two workers | 2 | 60 | 20 | 60 | 120.32 | 0.1630 | 0.1726 | 0.1733 | 20.00 | 0 | 0 |

## Scenario Configuration

### no batching baseline

```json
{
  "LAMINAR_BATCH_POLICY": "fixed",
  "LAMINAR_MAX_BATCH_SIZE": "1",
  "LAMINAR_MAX_BATCH_WAIT": "1ms"
}
```

Workers: `1`

Runtime policy snapshot:

```json
{
  "max_wait": "1ms",
  "name": "fixed"
}
```

Runtime worker snapshot:

```json
[
  {
    "address": "localhost:50051",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "33.34975ms",
    "last_selected": "2026-06-05T02:46:56.215318Z",
    "total_batches": 60,
    "total_failures": 0
  }
]
```

### fixed dynamic batching

```json
{
  "LAMINAR_BATCH_POLICY": "fixed",
  "LAMINAR_MAX_BATCH_SIZE": "32",
  "LAMINAR_MAX_BATCH_WAIT": "10ms"
}
```

Workers: `1`

Runtime policy snapshot:

```json
{
  "max_wait": "10ms",
  "name": "fixed"
}
```

Runtime worker snapshot:

```json
[
  {
    "address": "localhost:50055",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "148.239666ms",
    "last_selected": "2026-06-05T02:46:57.663636Z",
    "total_batches": 3,
    "total_failures": 0
  }
]
```

### adaptive batching

```json
{
  "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
  "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
  "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
  "LAMINAR_ADAPTIVE_TARGET_LATENCY": "150ms",
  "LAMINAR_BATCH_POLICY": "adaptive",
  "LAMINAR_MAX_BATCH_SIZE": "32",
  "LAMINAR_MAX_BATCH_WAIT": "10ms"
}
```

Workers: `1`

Runtime policy snapshot:

```json
{
  "adjustments": 1,
  "current_wait": "5ms",
  "last_batch_size": 20,
  "last_queue_depth": 0,
  "last_success": true,
  "last_worker_latency": "156.60675ms",
  "max_wait": "10ms",
  "min_wait": "1ms",
  "name": "adaptive",
  "queue_high_watermark": 32,
  "target_worker_latency": "150ms"
}
```

Runtime worker snapshot:

```json
[
  {
    "address": "localhost:50059",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "156.579833ms",
    "last_selected": "2026-06-05T02:46:59.231007Z",
    "total_batches": 3,
    "total_failures": 0
  }
]
```

### adaptive batching, two workers

```json
{
  "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
  "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
  "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
  "LAMINAR_ADAPTIVE_TARGET_LATENCY": "150ms",
  "LAMINAR_BATCH_POLICY": "adaptive",
  "LAMINAR_MAX_BATCH_SIZE": "32",
  "LAMINAR_MAX_BATCH_WAIT": "10ms"
}
```

Workers: `2`

Runtime policy snapshot:

```json
{
  "adjustments": 2,
  "current_wait": "2.5ms",
  "last_batch_size": 20,
  "last_queue_depth": 0,
  "last_success": true,
  "last_worker_latency": "152.649458ms",
  "max_wait": "10ms",
  "min_wait": "1ms",
  "name": "adaptive",
  "queue_high_watermark": 32,
  "target_worker_latency": "150ms"
}
```

Runtime worker snapshot:

```json
[
  {
    "address": "localhost:50063",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "152.63325ms",
    "last_selected": "2026-06-05T02:47:00.813258Z",
    "total_batches": 2,
    "total_failures": 0
  },
  {
    "address": "localhost:50064",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-2",
    "in_flight": 0,
    "last_latency": "152.787959ms",
    "last_selected": "2026-06-05T02:47:00.648235Z",
    "total_batches": 1,
    "total_failures": 0
  }
]
```
