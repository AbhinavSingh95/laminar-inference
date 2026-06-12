# Benchmark Results

Generated: `2026-06-12 22:50:57 AEST`

These numbers are local-machine evidence, not universal performance claims.

| Scenario | Workers | Requests | Concurrency | Success | Throughput (req/s) | p50 (s) | p95 (s) | p99 (s) | Avg Batch | Rejected | Worker Errors |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| llama.cpp server, no batching | 1 | 20 | 4 | 20 | 7.72 | 0.4919 | 0.5072 | 0.6076 | 1.00 | 0 | 0 |
| llama.cpp server, adaptive batching | 1 | 20 | 4 | 20 | 7.64 | 0.5081 | 0.5914 | 0.5920 | 4.00 | 0 | 0 |

## Scenario Configuration

### llama.cpp server, no batching

```json
{
  "LAMINAR_BATCH_POLICY": "fixed",
  "LAMINAR_LLAMA_SERVER_CACHE_PROMPT": "true",
  "LAMINAR_LLAMA_SERVER_MAX_TOKENS": "32",
  "LAMINAR_LLAMA_SERVER_TEMPERATURE": "0.2",
  "LAMINAR_LLAMA_SERVER_TIMEOUT_MS": "120000",
  "LAMINAR_LLAMA_SERVER_URL": "http://127.0.0.1:18081/completion",
  "LAMINAR_MAX_BATCH_SIZE": "1",
  "LAMINAR_MAX_BATCH_WAIT": "1ms",
  "LAMINAR_WORKER_BACKEND": "llama_server"
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
    "last_latency": "126.400542ms",
    "last_selected": "2026-06-12T12:50:53.704594Z",
    "total_batches": 20,
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
      "parentSpanId": "4ea7a607ef5d3617",
      "spanId": "e1cf6f0d6a833b9a",
      "status": {
        "code": 1
      },
      "traceId": "30de0816e9783388e4dc53f880fbb448"
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
    "id": "req-21",
    "metadata": {
      "cache_prompt": "true",
      "generated_chars": "158",
      "prompt_chars": "46",
      "stream": "true",
      "stream_chunks": "33",
      "timings_per_token": "true",
      "tokens_evaluated": "9",
      "tokens_predicted": "32",
      "ttft_micros": "628"
    },
    "result": "LlamaServer(backend=llama_server, generated='I am trying to build a server using the llama.cpp library in C++ that can handle multiple requests. I want to be able to handle multiple requests concurrently', prompt_chars=46, generated_chars=158, duration_micros=231260, batch_size=1, endpoint='127.0.0.1:18081/completion', max_tokens=32, temperature=0.200, cache_prompt=true, stream=true, timings_per_token=true, stream_chunks=33, ttft_micros=628, tokens_predicted=32, tokens_evaluated=9)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 5,
      "name": "batch.queue_wait",
      "parent_span_id": "e1cf6f0d6a833b9a",
      "span_id": "5a643f856db88d90",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-21",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 231977,
      "name": "worker.grpc",
      "parent_span_id": "3799414c6a6d64a9",
      "span_id": "f97e99ea7c1b1235",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.cache_prompt": "true",
        "backend.generated_chars": "158",
        "backend.name": "llama_server",
        "backend.prompt_chars": "46",
        "backend.stream": "true",
        "backend.stream_chunks": "33",
        "backend.timings_per_token": "true",
        "backend.tokens_evaluated": "9",
        "backend.tokens_predicted": "32",
        "backend.ttft_micros": "628",
        "request.id": "req-21"
      },
      "duration_micros": 231454,
      "name": "backend.inference",
      "parent_span_id": "f97e99ea7c1b1235",
      "span_id": "298934b8fa74a2e5",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-21",
        "worker.latency_micros": "231978"
      },
      "duration_micros": 231991,
      "name": "batch.flush",
      "parent_span_id": "5a643f856db88d90",
      "span_id": "3799414c6a6d64a9",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-21",
        "request.prompt_bytes": "46",
        "trace.flags": "01"
      },
      "duration_micros": 232018,
      "name": "http.predict",
      "parent_span_id": "4ea7a607ef5d3617",
      "span_id": "e1cf6f0d6a833b9a",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "30de0816e9783388e4dc53f880fbb448"
}
```

### llama.cpp server, adaptive batching

```json
{
  "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
  "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
  "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
  "LAMINAR_ADAPTIVE_TARGET_LATENCY": "5s",
  "LAMINAR_BATCH_POLICY": "adaptive",
  "LAMINAR_LLAMA_SERVER_CACHE_PROMPT": "true",
  "LAMINAR_LLAMA_SERVER_MAX_TOKENS": "32",
  "LAMINAR_LLAMA_SERVER_TEMPERATURE": "0.2",
  "LAMINAR_LLAMA_SERVER_TIMEOUT_MS": "120000",
  "LAMINAR_LLAMA_SERVER_URL": "http://127.0.0.1:18081/completion",
  "LAMINAR_MAX_BATCH_SIZE": "32",
  "LAMINAR_MAX_BATCH_WAIT": "10ms",
  "LAMINAR_WORKER_BACKEND": "llama_server"
}
```

Workers: `1`

Runtime policy snapshot:

```json
{
  "adjustments": 0,
  "current_wait": "10ms",
  "last_batch_size": 4,
  "last_queue_depth": 0,
  "last_success": true,
  "last_worker_latency": "494.093416ms",
  "max_wait": "10ms",
  "min_wait": "1ms",
  "name": "adaptive",
  "queue_high_watermark": 32,
  "target_worker_latency": "5s"
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
    "last_latency": "494.07075ms",
    "last_selected": "2026-06-12T12:50:57.251206Z",
    "total_batches": 5,
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
      "parentSpanId": "a62cbf95532a49d6",
      "spanId": "fa01ca9f639fbcc7",
      "status": {
        "code": 1
      },
      "traceId": "de98479ebd6090e101fb1602d8cb8a51"
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
    "id": "req-21",
    "metadata": {
      "cache_prompt": "true",
      "generated_chars": "129",
      "prompt_chars": "52",
      "stream": "true",
      "stream_chunks": "33",
      "timings_per_token": "true",
      "tokens_evaluated": "9",
      "tokens_predicted": "32",
      "ttft_micros": "531"
    },
    "result": "LlamaServer(backend=llama_server, generated='I\\'m trying to implement adaptive batching in a server-side LLM (Language Model) using the LLM framework. I\\'ve been using the `llm', prompt_chars=52, generated_chars=129, duration_micros=129652, batch_size=1, endpoint='127.0.0.1:18081/completion', max_tokens=32, temperature=0.200, cache_prompt=true, stream=true, timings_per_token=true, stream_chunks=33, ttft_micros=531, tokens_predicted=32, tokens_evaluated=9)"
  },
  "spans": [
    {
      "attributes": {
        "queue.capacity": "1000",
        "queue.depth": "0"
      },
      "duration_micros": 11071,
      "name": "batch.queue_wait",
      "parent_span_id": "fa01ca9f639fbcc7",
      "span_id": "13cf368dc9dc667f",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.size": "1",
        "request.id": "req-21",
        "rpc.method": "ProcessBatch",
        "rpc.system": "grpc"
      },
      "duration_micros": 130610,
      "name": "worker.grpc",
      "parent_span_id": "31ec2bfa4c06fe20",
      "span_id": "d4d43ae282459f25",
      "status": "ok"
    },
    {
      "attributes": {
        "backend.cache_prompt": "true",
        "backend.generated_chars": "129",
        "backend.name": "llama_server",
        "backend.prompt_chars": "52",
        "backend.stream": "true",
        "backend.stream_chunks": "33",
        "backend.timings_per_token": "true",
        "backend.tokens_evaluated": "9",
        "backend.tokens_predicted": "32",
        "backend.ttft_micros": "531",
        "request.id": "req-21"
      },
      "duration_micros": 129870,
      "name": "backend.inference",
      "parent_span_id": "d4d43ae282459f25",
      "span_id": "785f76cb70f9d3e6",
      "status": "ok"
    },
    {
      "attributes": {
        "batch.original_size": "1",
        "batch.size": "1",
        "request.id": "req-21",
        "worker.latency_micros": "130614"
      },
      "duration_micros": 130629,
      "name": "batch.flush",
      "parent_span_id": "13cf368dc9dc667f",
      "span_id": "31ec2bfa4c06fe20",
      "status": "ok"
    },
    {
      "attributes": {
        "http.method": "POST",
        "http.route": "/predict",
        "http.status_code": "200",
        "request.id": "req-21",
        "request.prompt_bytes": "52",
        "trace.flags": "01"
      },
      "duration_micros": 141787,
      "name": "http.predict",
      "parent_span_id": "a62cbf95532a49d6",
      "span_id": "fa01ca9f639fbcc7",
      "status": "ok"
    }
  ],
  "status": 200,
  "trace_id": "de98479ebd6090e101fb1602d8cb8a51"
}
```
