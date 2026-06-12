# Benchmark Results

Generated: `2026-06-12 22:31:03 AEST`

These numbers are local-machine evidence, not universal performance claims.

| Scenario | Workers | Requests | Concurrency | Success | Throughput (req/s) | p50 (s) | p95 (s) | p99 (s) | Avg Batch | Rejected | Worker Errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| no batching baseline | 1 | 60 | 20 | 60 | 29.80 | 0.6586 | 0.6711 | 0.6728 | 1.00 | 0 | 0 |
| fixed dynamic batching | 1 | 60 | 20 | 60 | 118.70 | 0.1663 | 0.1724 | 0.1734 | 20.00 | 0 | 0 |
| adaptive batching | 1 | 60 | 20 | 60 | 121.18 | 0.1657 | 0.1706 | 0.1719 | 20.00 | 0 | 0 |
| adaptive batching, two workers | 2 | 60 | 20 | 60 | 120.66 | 0.1635 | 0.1691 | 0.1697 | 20.00 | 0 | 0 |
| tiny model, no batching | 1 | 60 | 20 | 60 | 1226.46 | 0.0147 | 0.0155 | 0.0157 | 1.00 | 0 | 0 |
| tiny model, adaptive batching | 1 | 60 | 20 | 60 | 915.23 | 0.0196 | 0.0260 | 0.0270 | 20.00 | 0 | 0 |
| tiny model, adaptive batching, two workers | 2 | 60 | 20 | 60 | 824.43 | 0.0228 | 0.0318 | 0.0322 | 20.00 | 0 | 0 |
| tiny llm, no batching | 1 | 60 | 20 | 60 | 1132.01 | 0.0153 | 0.0192 | 0.0196 | 1.00 | 0 | 0 |
| tiny llm, adaptive batching | 1 | 60 | 20 | 60 | 753.15 | 0.0255 | 0.0287 | 0.0300 | 20.00 | 0 | 0 |
| tiny llm, adaptive batching, two workers | 2 | 60 | 20 | 60 | 766.09 | 0.0239 | 0.0272 | 0.0277 | 20.00 | 0 | 0 |
| onnx runtime, no batching | 1 | 60 | 20 | 60 | 1899.69 | 0.0079 | 0.0092 | 0.0095 | 1.00 | 0 | 0 |
| onnx runtime, adaptive batching | 1 | 60 | 20 | 60 | 1263.80 | 0.0146 | 0.0164 | 0.0169 | 20.00 | 0 | 0 |
| onnx runtime, adaptive batching, two workers | 2 | 60 | 20 | 60 | 1104.18 | 0.0162 | 0.0193 | 0.0197 | 20.00 | 0 | 0 |

## Skipped Scenarios

- `llama.cpp gguf, no batching`: LAMINAR_LLAMA_CPP_MODEL is not set; provide a local GGUF model path
- `llama.cpp gguf, adaptive batching`: LAMINAR_LLAMA_CPP_MODEL is not set; provide a local GGUF model path
- `llama.cpp server, no batching`: LAMINAR_LLAMA_SERVER_URL is not set; start llama-server and point to /completion
- `llama.cpp server, adaptive batching`: LAMINAR_LLAMA_SERVER_URL is not set; start llama-server and point to /completion

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
    "last_latency": "38.19125ms",
    "last_selected": "2026-06-12T12:30:48.12749Z",
    "total_batches": 60,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "9b3c142b2d0c60c7",
      "spanId": "b48619abae73c1eb",
      "status": {
        "code": 1
      },
      "traceId": "0b949bbcd2518187612ad4cc2dd6f1f9"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "batch.flush",
      "backend.inference",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "Processed: 'trace sample for no batching baseline' (backend=simulator, batch_size=1, latency=25ms)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 18,
      "name": "batch.queue_wait",
      "parent_span_id": "b48619abae73c1eb",
      "span_id": "eb58219cf5d32670",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 31718,
      "name": "worker.grpc",
      "parent_span_id": "4c6ceb679979d5a9",
      "span_id": "c966a4d672a3f93e",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "simulator",
        "request.id": "req-61"
      },
      "duration_micros": 30039,
      "name": "backend.inference",
      "parent_span_id": "c966a4d672a3f93e",
      "span_id": "d88ceeab53c936be",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "31719"
      },
      "duration_micros": 31743,
      "name": "batch.flush",
      "parent_span_id": "eb58219cf5d32670",
      "span_id": "4c6ceb679979d5a9",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "37",
        "trace.flags": "01"
      },
      "duration_micros": 31820,
      "name": "http.predict",
      "parent_span_id": "9b3c142b2d0c60c7",
      "span_id": "b48619abae73c1eb",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "0b949bbcd2518187612ad4cc2dd6f1f9"
}
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
    "last_latency": "149.58725ms",
    "last_selected": "2026-06-12T12:30:49.626165Z",
    "total_batches": 3,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "ea0b63f5a5d79089",
      "spanId": "3a19a1055eb3bcda",
      "status": {
        "code": 1
      },
      "traceId": "7c6da0e7dd2c63f3e81952805603ff58"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "batch.flush",
      "backend.inference",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "Processed: 'trace sample for fixed dynamic batching' (backend=simulator, batch_size=1, latency=25ms)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 11027,
      "name": "batch.queue_wait",
      "parent_span_id": "3a19a1055eb3bcda",
      "span_id": "f120b9a84bbe42b9",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 30689,
      "name": "worker.grpc",
      "parent_span_id": "0c4259619da53e84",
      "span_id": "3661f98700d99ca9",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "simulator",
        "request.id": "req-61"
      },
      "duration_micros": 29331,
      "name": "backend.inference",
      "parent_span_id": "3661f98700d99ca9",
      "span_id": "e32af94c849ba2d0",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "30695"
      },
      "duration_micros": 30714,
      "name": "batch.flush",
      "parent_span_id": "f120b9a84bbe42b9",
      "span_id": "0c4259619da53e84",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "39",
        "trace.flags": "01"
      },
      "duration_micros": 41809,
      "name": "http.predict",
      "parent_span_id": "ea0b63f5a5d79089",
      "span_id": "3a19a1055eb3bcda",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "7c6da0e7dd2c63f3e81952805603ff58"
}
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
  "adjustments": 2,
  "current_wait": "2.5ms",
  "last_batch_size": 20,
  "last_queue_depth": 0,
  "last_success": true,
  "last_worker_latency": "147.767625ms",
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
    "last_latency": "147.697875ms",
    "last_selected": "2026-06-12T12:30:51.242776Z",
    "total_batches": 3,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "92ecb07fc8a48919",
      "spanId": "ba14d9e81ebdde17",
      "status": {
        "code": 1
      },
      "traceId": "7582c19e3f1527bb0903491d6e879d9d"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "batch.flush",
      "backend.inference",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "Processed: 'trace sample for adaptive batching' (backend=simulator, batch_size=1, latency=25ms)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 2865,
      "name": "batch.queue_wait",
      "parent_span_id": "ba14d9e81ebdde17",
      "span_id": "25b71fa8ccd6ba8d",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 31810,
      "name": "worker.grpc",
      "parent_span_id": "a749d324b729bd18",
      "span_id": "2b9242f137af42d0",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "simulator",
        "request.id": "req-61"
      },
      "duration_micros": 30205,
      "name": "backend.inference",
      "parent_span_id": "2b9242f137af42d0",
      "span_id": "efb08fc7c2055387",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "31811"
      },
      "duration_micros": 31862,
      "name": "batch.flush",
      "parent_span_id": "25b71fa8ccd6ba8d",
      "span_id": "a749d324b729bd18",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "34",
        "trace.flags": "01"
      },
      "duration_micros": 34809,
      "name": "http.predict",
      "parent_span_id": "92ecb07fc8a48919",
      "span_id": "ba14d9e81ebdde17",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "7582c19e3f1527bb0903491d6e879d9d"
}
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
  "adjustments": 3,
  "current_wait": "1.25ms",
  "last_batch_size": 20,
  "last_queue_depth": 0,
  "last_success": true,
  "last_worker_latency": "151.242667ms",
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
    "last_latency": "151.175916ms",
    "last_selected": "2026-06-12T12:30:52.840232Z",
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
    "last_latency": "152.642583ms",
    "last_selected": "2026-06-12T12:30:52.679108Z",
    "total_batches": 1,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "d4967dcbde3eac65",
      "spanId": "d4788d7e479a6f35",
      "status": {
        "code": 1
      },
      "traceId": "f61c36ed39f3d35e46e75a8b47a4c222"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "batch.flush",
      "backend.inference",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "Processed: 'trace sample for adaptive batching, two workers' (backend=simulator, batch_size=1, latency=25ms)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 1430,
      "name": "batch.queue_wait",
      "parent_span_id": "d4788d7e479a6f35",
      "span_id": "4ef85edbe0245b17",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 32231,
      "name": "worker.grpc",
      "parent_span_id": "22b602684643b91b",
      "span_id": "347439bdde36382c",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "simulator",
        "request.id": "req-61"
      },
      "duration_micros": 30994,
      "name": "backend.inference",
      "parent_span_id": "347439bdde36382c",
      "span_id": "db5a85bb08d33d8b",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "32236"
      },
      "duration_micros": 32267,
      "name": "batch.flush",
      "parent_span_id": "4ef85edbe0245b17",
      "span_id": "22b602684643b91b",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "47",
        "trace.flags": "01"
      },
      "duration_micros": 33768,
      "name": "http.predict",
      "parent_span_id": "d4967dcbde3eac65",
      "span_id": "d4788d7e479a6f35",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "f61c36ed39f3d35e46e75a8b47a4c222"
}
```

### tiny model, no batching

```json
{
  "LAMINAR_BATCH_POLICY": "fixed",
  "LAMINAR_MAX_BATCH_SIZE": "1",
  "LAMINAR_MAX_BATCH_WAIT": "1ms",
  "LAMINAR_TINY_MODEL_ITERATIONS": "16",
  "LAMINAR_WORKER_BACKEND": "tiny_model"
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
    "address": "localhost:50067",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "543.375\u00b5s",
    "last_selected": "2026-06-12T12:30:54.157334Z",
    "total_batches": 60,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "9a41a6064cfcdb5c",
      "spanId": "96ab536712d6209a",
      "status": {
        "code": 1
      },
      "traceId": "7e39cd5405872d67f39314e9ce785526"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "backend.inference",
      "batch.flush",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "TinyTextModel(backend=tiny_text_model, label=safety_review, confidence=0.254, batch_size=1, iterations=16)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 1,
      "name": "batch.queue_wait",
      "parent_span_id": "96ab536712d6209a",
      "span_id": "f56fec68a2900811",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 588,
      "name": "worker.grpc",
      "parent_span_id": "e7133e957c2306ff",
      "span_id": "53032695b23c05cc",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "tiny_text_model",
        "request.id": "req-61"
      },
      "duration_micros": 405,
      "name": "backend.inference",
      "parent_span_id": "53032695b23c05cc",
      "span_id": "ad81308bad57b82c",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "588"
      },
      "duration_micros": 592,
      "name": "batch.flush",
      "parent_span_id": "f56fec68a2900811",
      "span_id": "e7133e957c2306ff",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "40",
        "trace.flags": "01"
      },
      "duration_micros": 602,
      "name": "http.predict",
      "parent_span_id": "9a41a6064cfcdb5c",
      "span_id": "96ab536712d6209a",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "7e39cd5405872d67f39314e9ce785526"
}
```

### tiny model, adaptive batching

```json
{
  "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
  "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
  "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
  "LAMINAR_ADAPTIVE_TARGET_LATENCY": "10ms",
  "LAMINAR_BATCH_POLICY": "adaptive",
  "LAMINAR_MAX_BATCH_SIZE": "32",
  "LAMINAR_MAX_BATCH_WAIT": "10ms",
  "LAMINAR_TINY_MODEL_ITERATIONS": "16",
  "LAMINAR_WORKER_BACKEND": "tiny_model"
}
```

Workers: `1`

Runtime policy snapshot:

```json
{
  "adjustments": 3,
  "current_wait": "1.25ms",
  "last_batch_size": 20,
  "last_queue_depth": 0,
  "last_success": true,
  "last_worker_latency": "11.743125ms",
  "max_wait": "10ms",
  "min_wait": "1ms",
  "name": "adaptive",
  "queue_high_watermark": 32,
  "target_worker_latency": "10ms"
}
```

Runtime worker snapshot:

```json
[
  {
    "address": "localhost:50071",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "11.711584ms",
    "last_selected": "2026-06-12T12:30:55.268866Z",
    "total_batches": 3,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "af17486db988839e",
      "spanId": "c746be29fcff10ac",
      "status": {
        "code": 1
      },
      "traceId": "1f023ad64a268ad8bddd2d52a054e1bb"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "batch.flush",
      "backend.inference",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "TinyTextModel(backend=tiny_text_model, label=safety_review, confidence=0.254, batch_size=1, iterations=16)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 1440,
      "name": "batch.queue_wait",
      "parent_span_id": "c746be29fcff10ac",
      "span_id": "91475cbf10135b10",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 1398,
      "name": "worker.grpc",
      "parent_span_id": "485ab6efcaafc3cf",
      "span_id": "4191f2dd7be8d28d",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "tiny_text_model",
        "request.id": "req-61"
      },
      "duration_micros": 581,
      "name": "backend.inference",
      "parent_span_id": "4191f2dd7be8d28d",
      "span_id": "26563ad81b3919a6",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "1405"
      },
      "duration_micros": 1419,
      "name": "batch.flush",
      "parent_span_id": "91475cbf10135b10",
      "span_id": "485ab6efcaafc3cf",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "46",
        "trace.flags": "01"
      },
      "duration_micros": 2908,
      "name": "http.predict",
      "parent_span_id": "af17486db988839e",
      "span_id": "c746be29fcff10ac",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "1f023ad64a268ad8bddd2d52a054e1bb"
}
```

### tiny model, adaptive batching, two workers

```json
{
  "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
  "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
  "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
  "LAMINAR_ADAPTIVE_TARGET_LATENCY": "10ms",
  "LAMINAR_BATCH_POLICY": "adaptive",
  "LAMINAR_MAX_BATCH_SIZE": "32",
  "LAMINAR_MAX_BATCH_WAIT": "10ms",
  "LAMINAR_TINY_MODEL_ITERATIONS": "16",
  "LAMINAR_WORKER_BACKEND": "tiny_model"
}
```

Workers: `2`

Runtime policy snapshot:

```json
{
  "adjustments": 3,
  "current_wait": "1.25ms",
  "last_batch_size": 20,
  "last_queue_depth": 0,
  "last_success": true,
  "last_worker_latency": "11.24ms",
  "max_wait": "10ms",
  "min_wait": "1ms",
  "name": "adaptive",
  "queue_high_watermark": 32,
  "target_worker_latency": "10ms"
}
```

Runtime worker snapshot:

```json
[
  {
    "address": "localhost:50075",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "11.203916ms",
    "last_selected": "2026-06-12T12:30:56.403252Z",
    "total_batches": 2,
    "total_failures": 0
  },
  {
    "address": "localhost:50076",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-2",
    "in_flight": 0,
    "last_latency": "13.7655ms",
    "last_selected": "2026-06-12T12:30:56.383931Z",
    "total_batches": 1,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "680287e30b3fc177",
      "spanId": "636357bf167c3111",
      "status": {
        "code": 1
      },
      "traceId": "bcb32f81071b5f6238187c785260d322"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "backend.inference",
      "batch.flush",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "TinyTextModel(backend=tiny_text_model, label=safety_review, confidence=0.254, batch_size=1, iterations=16)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 1437,
      "name": "batch.queue_wait",
      "parent_span_id": "636357bf167c3111",
      "span_id": "a6a4d42c425cd26b",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 1136,
      "name": "worker.grpc",
      "parent_span_id": "d067b16b5125c76d",
      "span_id": "f236e9d02f4dad78",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "tiny_text_model",
        "request.id": "req-61"
      },
      "duration_micros": 490,
      "name": "backend.inference",
      "parent_span_id": "f236e9d02f4dad78",
      "span_id": "05cffd11e5d1384b",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "1139"
      },
      "duration_micros": 1148,
      "name": "batch.flush",
      "parent_span_id": "a6a4d42c425cd26b",
      "span_id": "d067b16b5125c76d",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "59",
        "trace.flags": "01"
      },
      "duration_micros": 2615,
      "name": "http.predict",
      "parent_span_id": "680287e30b3fc177",
      "span_id": "636357bf167c3111",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "bcb32f81071b5f6238187c785260d322"
}
```

### tiny llm, no batching

```json
{
  "LAMINAR_BATCH_POLICY": "fixed",
  "LAMINAR_MAX_BATCH_SIZE": "1",
  "LAMINAR_MAX_BATCH_WAIT": "1ms",
  "LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS": "8",
  "LAMINAR_TINY_LLM_TEMPERATURE": "0.8",
  "LAMINAR_TINY_LLM_TOP_K": "5",
  "LAMINAR_WORKER_BACKEND": "tiny_llm"
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
    "address": "localhost:50079",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "632.334\u00b5s",
    "last_selected": "2026-06-12T12:30:57.538221Z",
    "total_batches": 60,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "856d8994a5fc285f",
      "spanId": "788a318d4c8a9b7e",
      "status": {
        "code": 1
      },
      "traceId": "deec58d000d209f4d62b994a4c122fe6"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "batch.flush",
      "backend.inference",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "TinyLLM(backend=tiny_llm, generated='fast local queue worker evidence worker inference latency', prompt_tokens=8, generated_tokens=8, prefill_micros=109, decode_micros=279, batch_size=1, max_generated_tokens=8, top_k=5, temperature=0.800)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 2,
      "name": "batch.queue_wait",
      "parent_span_id": "788a318d4c8a9b7e",
      "span_id": "fc3f887165c6f923",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 682,
      "name": "worker.grpc",
      "parent_span_id": "d44e39d64a29deec",
      "span_id": "89fceb8a47080d29",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "tiny_llm",
        "request.id": "req-61"
      },
      "duration_micros": 455,
      "name": "backend.inference",
      "parent_span_id": "89fceb8a47080d29",
      "span_id": "d50ebc9ee1ffbf9a",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "683"
      },
      "duration_micros": 688,
      "name": "batch.flush",
      "parent_span_id": "fc3f887165c6f923",
      "span_id": "d44e39d64a29deec",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "38",
        "trace.flags": "01"
      },
      "duration_micros": 700,
      "name": "http.predict",
      "parent_span_id": "856d8994a5fc285f",
      "span_id": "788a318d4c8a9b7e",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "deec58d000d209f4d62b994a4c122fe6"
}
```

### tiny llm, adaptive batching

```json
{
  "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
  "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
  "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
  "LAMINAR_ADAPTIVE_TARGET_LATENCY": "15ms",
  "LAMINAR_BATCH_POLICY": "adaptive",
  "LAMINAR_MAX_BATCH_SIZE": "32",
  "LAMINAR_MAX_BATCH_WAIT": "10ms",
  "LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS": "8",
  "LAMINAR_TINY_LLM_TEMPERATURE": "0.8",
  "LAMINAR_TINY_LLM_TOP_K": "5",
  "LAMINAR_WORKER_BACKEND": "tiny_llm"
}
```

Workers: `1`

Runtime policy snapshot:

```json
{
  "adjustments": 0,
  "current_wait": "10ms",
  "last_batch_size": 20,
  "last_queue_depth": 0,
  "last_success": true,
  "last_worker_latency": "8.935958ms",
  "max_wait": "10ms",
  "min_wait": "1ms",
  "name": "adaptive",
  "queue_high_watermark": 32,
  "target_worker_latency": "15ms"
}
```

Runtime worker snapshot:

```json
[
  {
    "address": "localhost:50083",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "8.901667ms",
    "last_selected": "2026-06-12T12:30:58.68186Z",
    "total_batches": 3,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "1eab367dfc9cebca",
      "spanId": "342165734e7bc9e9",
      "status": {
        "code": 1
      },
      "traceId": "ac2a5e0c0fce6814ec7a686b6f5521be"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "batch.flush",
      "backend.inference",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "TinyLLM(backend=tiny_llm, generated='fast local queue worker evidence worker inference latency', prompt_tokens=8, generated_tokens=8, prefill_micros=161, decode_micros=326, batch_size=1, max_generated_tokens=8, top_k=5, temperature=0.800)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 11040,
      "name": "batch.queue_wait",
      "parent_span_id": "342165734e7bc9e9",
      "span_id": "c0d3777cf58d386a",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 1127,
      "name": "worker.grpc",
      "parent_span_id": "b1e0acec8ed21f3c",
      "span_id": "7701d688effd2698",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "tiny_llm",
        "request.id": "req-61"
      },
      "duration_micros": 543,
      "name": "backend.inference",
      "parent_span_id": "7701d688effd2698",
      "span_id": "633887242bcec20f",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "1130"
      },
      "duration_micros": 1139,
      "name": "batch.flush",
      "parent_span_id": "c0d3777cf58d386a",
      "span_id": "b1e0acec8ed21f3c",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "44",
        "trace.flags": "01"
      },
      "duration_micros": 12223,
      "name": "http.predict",
      "parent_span_id": "1eab367dfc9cebca",
      "span_id": "342165734e7bc9e9",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "ac2a5e0c0fce6814ec7a686b6f5521be"
}
```

### tiny llm, adaptive batching, two workers

```json
{
  "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
  "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
  "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
  "LAMINAR_ADAPTIVE_TARGET_LATENCY": "15ms",
  "LAMINAR_BATCH_POLICY": "adaptive",
  "LAMINAR_MAX_BATCH_SIZE": "32",
  "LAMINAR_MAX_BATCH_WAIT": "10ms",
  "LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS": "8",
  "LAMINAR_TINY_LLM_TEMPERATURE": "0.8",
  "LAMINAR_TINY_LLM_TOP_K": "5",
  "LAMINAR_WORKER_BACKEND": "tiny_llm"
}
```

Workers: `2`

Runtime policy snapshot:

```json
{
  "adjustments": 0,
  "current_wait": "10ms",
  "last_batch_size": 20,
  "last_queue_depth": 0,
  "last_success": true,
  "last_worker_latency": "9.908167ms",
  "max_wait": "10ms",
  "min_wait": "1ms",
  "name": "adaptive",
  "queue_high_watermark": 32,
  "target_worker_latency": "15ms"
}
```

Runtime worker snapshot:

```json
[
  {
    "address": "localhost:50087",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "9.866875ms",
    "last_selected": "2026-06-12T12:30:59.825601Z",
    "total_batches": 2,
    "total_failures": 0
  },
  {
    "address": "localhost:50088",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-2",
    "in_flight": 0,
    "last_latency": "11.193041ms",
    "last_selected": "2026-06-12T12:30:59.800503Z",
    "total_batches": 1,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "5b4a3eb1f0d00f7a",
      "spanId": "aff6e673afd6d791",
      "status": {
        "code": 1
      },
      "traceId": "dafc86e5de3e6718ec5f44dc4387683a"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "batch.flush",
      "backend.inference",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "TinyLLM(backend=tiny_llm, generated='layer latency collector queue safe http onnx causal', prompt_tokens=10, generated_tokens=8, prefill_micros=197, decode_micros=333, batch_size=1, max_generated_tokens=8, top_k=5, temperature=0.800)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 11069,
      "name": "batch.queue_wait",
      "parent_span_id": "aff6e673afd6d791",
      "span_id": "baa8c66aae9e7af0",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 1226,
      "name": "worker.grpc",
      "parent_span_id": "b390d38d99e592e6",
      "span_id": "c48e6db3aca76041",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "tiny_llm",
        "request.id": "req-61"
      },
      "duration_micros": 604,
      "name": "backend.inference",
      "parent_span_id": "c48e6db3aca76041",
      "span_id": "936db0bd854097c6",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "1235"
      },
      "duration_micros": 1242,
      "name": "batch.flush",
      "parent_span_id": "baa8c66aae9e7af0",
      "span_id": "b390d38d99e592e6",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "57",
        "trace.flags": "01"
      },
      "duration_micros": 12384,
      "name": "http.predict",
      "parent_span_id": "5b4a3eb1f0d00f7a",
      "span_id": "aff6e673afd6d791",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "dafc86e5de3e6718ec5f44dc4387683a"
}
```

### onnx runtime, no batching

```json
{
  "LAMINAR_BATCH_POLICY": "fixed",
  "LAMINAR_MAX_BATCH_SIZE": "1",
  "LAMINAR_MAX_BATCH_WAIT": "1ms",
  "LAMINAR_ONNX_MODEL_PATH": "models/logreg_iris.onnx",
  "LAMINAR_WORKER_BACKEND": "onnx"
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
    "address": "localhost:50091",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "264.458\u00b5s",
    "last_selected": "2026-06-12T12:31:00.964018Z",
    "total_batches": 60,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "fb3d0c83b5a143b6",
      "spanId": "6b716433a96fe550",
      "status": {
        "code": 1
      },
      "traceId": "99b3ec9839b274e0009d84f21196debb"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "backend.inference",
      "batch.flush",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "OnnxRuntime(backend=onnx, model=logreg_iris, label=0, batch_size=1, model_batch=3)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 2,
      "name": "batch.queue_wait",
      "parent_span_id": "6b716433a96fe550",
      "span_id": "e5b2517e5605f966",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 264,
      "name": "worker.grpc",
      "parent_span_id": "6ea4863033fa261f",
      "span_id": "71455ad995542b00",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "onnx",
        "request.id": "req-61"
      },
      "duration_micros": 42,
      "name": "backend.inference",
      "parent_span_id": "71455ad995542b00",
      "span_id": "65c7f71fca7eee0b",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "266"
      },
      "duration_micros": 271,
      "name": "batch.flush",
      "parent_span_id": "e5b2517e5605f966",
      "span_id": "6ea4863033fa261f",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "42",
        "trace.flags": "01"
      },
      "duration_micros": 284,
      "name": "http.predict",
      "parent_span_id": "fb3d0c83b5a143b6",
      "span_id": "6b716433a96fe550",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "99b3ec9839b274e0009d84f21196debb"
}
```

### onnx runtime, adaptive batching

```json
{
  "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
  "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
  "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
  "LAMINAR_ADAPTIVE_TARGET_LATENCY": "10ms",
  "LAMINAR_BATCH_POLICY": "adaptive",
  "LAMINAR_MAX_BATCH_SIZE": "32",
  "LAMINAR_MAX_BATCH_WAIT": "10ms",
  "LAMINAR_ONNX_MODEL_PATH": "models/logreg_iris.onnx",
  "LAMINAR_WORKER_BACKEND": "onnx"
}
```

Workers: `1`

Runtime policy snapshot:

```json
{
  "adjustments": 0,
  "current_wait": "10ms",
  "last_batch_size": 20,
  "last_queue_depth": 0,
  "last_success": true,
  "last_worker_latency": "1.0455ms",
  "max_wait": "10ms",
  "min_wait": "1ms",
  "name": "adaptive",
  "queue_high_watermark": 32,
  "target_worker_latency": "10ms"
}
```

Runtime worker snapshot:

```json
[
  {
    "address": "localhost:50095",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "994.375\u00b5s",
    "last_selected": "2026-06-12T12:31:02.063415Z",
    "total_batches": 3,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "20220fad7d1be1aa",
      "spanId": "9f0f36655c67e3aa",
      "status": {
        "code": 1
      },
      "traceId": "0cf4f24ac7b97c8848dcfdd1bbf5f8a3"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "batch.flush",
      "backend.inference",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "OnnxRuntime(backend=onnx, model=logreg_iris, label=0, batch_size=1, model_batch=3)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 10546,
      "name": "batch.queue_wait",
      "parent_span_id": "9f0f36655c67e3aa",
      "span_id": "fc3e70e23d25197e",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 937,
      "name": "worker.grpc",
      "parent_span_id": "4eddcee6f03958c8",
      "span_id": "feb8564f67a1dc2f",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "onnx",
        "request.id": "req-61"
      },
      "duration_micros": 123,
      "name": "backend.inference",
      "parent_span_id": "feb8564f67a1dc2f",
      "span_id": "b97f72254a5aafa1",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "934"
      },
      "duration_micros": 956,
      "name": "batch.flush",
      "parent_span_id": "fc3e70e23d25197e",
      "span_id": "4eddcee6f03958c8",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "48",
        "trace.flags": "01"
      },
      "duration_micros": 11540,
      "name": "http.predict",
      "parent_span_id": "20220fad7d1be1aa",
      "span_id": "9f0f36655c67e3aa",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "0cf4f24ac7b97c8848dcfdd1bbf5f8a3"
}
```

### onnx runtime, adaptive batching, two workers

```json
{
  "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
  "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
  "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
  "LAMINAR_ADAPTIVE_TARGET_LATENCY": "10ms",
  "LAMINAR_BATCH_POLICY": "adaptive",
  "LAMINAR_MAX_BATCH_SIZE": "32",
  "LAMINAR_MAX_BATCH_WAIT": "10ms",
  "LAMINAR_ONNX_MODEL_PATH": "models/logreg_iris.onnx",
  "LAMINAR_WORKER_BACKEND": "onnx"
}
```

Workers: `2`

Runtime policy snapshot:

```json
{
  "adjustments": 0,
  "current_wait": "10ms",
  "last_batch_size": 20,
  "last_queue_depth": 0,
  "last_success": true,
  "last_worker_latency": "1.424375ms",
  "max_wait": "10ms",
  "min_wait": "1ms",
  "name": "adaptive",
  "queue_high_watermark": 32,
  "target_worker_latency": "10ms"
}
```

Runtime worker snapshot:

```json
[
  {
    "address": "localhost:50099",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-1",
    "in_flight": 0,
    "last_latency": "1.37475ms",
    "last_selected": "2026-06-12T12:31:03.199289Z",
    "total_batches": 2,
    "total_failures": 0
  },
  {
    "address": "localhost:50100",
    "circuit_open": false,
    "circuit_state": "closed",
    "consecutive_failures": 0,
    "id": "worker-2",
    "in_flight": 0,
    "last_latency": "2.256625ms",
    "last_selected": "2026-06-12T12:31:03.181881Z",
    "total_batches": 1,
    "total_failures": 0
  }
]
```

Trace sample:

```json
{
  "otlp": {
    "first_span": {
      "kind": 2,
      "name": "http.predict",
      "parentSpanId": "4cfd8abe07aa34a4",
      "spanId": "044503f3aa2f38a8",
      "status": {
        "code": 1
      },
      "traceId": "d2785595b20a651d9f4eeb2156f536e3"
    },
    "resourceSpans": 1,
    "scopeSpans": 1,
    "span_count": 5,
    "span_names": [
      "http.predict",
      "batch.queue_wait",
      "batch.flush",
      "backend.inference",
      "worker.grpc"
    ]
  },
  "response": {
    "id": "req-61",
    "result": "OnnxRuntime(backend=onnx, model=logreg_iris, label=0, batch_size=1, model_batch=3)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 11068,
      "name": "batch.queue_wait",
      "parent_span_id": "044503f3aa2f38a8",
      "span_id": "cf8879779a206eb0",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-61",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 938,
      "name": "worker.grpc",
      "parent_span_id": "e7bd2ba64c2b5880",
      "span_id": "970d6058d7293cdd",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.name": "onnx",
        "request.id": "req-61"
      },
      "duration_micros": 114,
      "name": "backend.inference",
      "parent_span_id": "970d6058d7293cdd",
      "span_id": "bd35049e18aad340",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-61",
        "worker.latency_micros": "943"
      },
      "duration_micros": 955,
      "name": "batch.flush",
      "parent_span_id": "cf8879779a206eb0",
      "span_id": "e7bd2ba64c2b5880",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-61",
        "request.prompt_bytes": "61",
        "trace.flags": "01"
      },
      "duration_micros": 12078,
      "name": "http.predict",
      "parent_span_id": "4cfd8abe07aa34a4",
      "span_id": "044503f3aa2f38a8",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "d2785595b20a651d9f4eeb2156f536e3"
}
```
