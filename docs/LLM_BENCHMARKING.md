# Real LLM Benchmarking

This runbook benchmarks Laminar against a warmed `llama-server` process and a real small instruction model. The goal is credible local serving evidence without committing large model artifacts to the repository.

## Default Model

The default benchmark model is `bartowski/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M`.

Why this default:

- It is a GGUF build intended for llama.cpp.
- The upstream Qwen2.5 0.5B Instruct model is small enough for laptop-class local benchmarking.
- The model license is Apache-2.0 on the Hugging Face model cards.
- The GGUF model page documents direct `llama-server -hf ...` usage, which lets this repo avoid checking in model binaries.

References:

- [llama.cpp build documentation](https://github.com/ggml-org/llama.cpp/blob/master/docs/build.md)
- [bartowski/Qwen2.5-0.5B-Instruct-GGUF](https://huggingface.co/bartowski/Qwen2.5-0.5B-Instruct-GGUF)
- [Qwen/Qwen2.5-0.5B-Instruct](https://huggingface.co/Qwen/Qwen2.5-0.5B-Instruct)

## One-Command Path

Install llama.cpp if needed, start a warmed server, run only the `llama.cpp server` benchmark scenarios, write a Markdown report, and stop the server afterward:

```bash
./scripts/benchmark_real_llm.sh --install --requests 20 --concurrency 4
```

The report is written to `docs/results/llama-server-latest.md` by default. Runtime metadata is written next to it as `docs/results/llama-server-latest.metadata.json`.

If llama.cpp is already installed:

```bash
./scripts/benchmark_real_llm.sh --requests 20 --concurrency 4
```

Keep the server running after the benchmark:

```bash
LAMINAR_KEEP_LLAMA_SERVER=true ./scripts/benchmark_real_llm.sh --requests 20 --concurrency 4
```

## Manual Server Path

Start the warmed server:

```bash
./scripts/run_llama_server.sh --background
```

Run the benchmark lane against that server:

```bash
export LAMINAR_LLAMA_SERVER_URL="$(./scripts/run_llama_server.sh --url)"
./benchmark.sh --scenario "llama.cpp server" --requests 20 --concurrency 4 \
  --output docs/results/llama-server-latest.md
```

Inspect or stop the server:

```bash
./scripts/run_llama_server.sh --status
./scripts/run_llama_server.sh --stop
```

## Existing Server Path

Use an already-running local or remote `llama-server` by pointing Laminar at its native `/completion` endpoint:

```bash
LAMINAR_LLAMA_SERVER_URL=http://127.0.0.1:18081/completion \
./scripts/benchmark_real_llm.sh --requests 20 --concurrency 4
```

When `LAMINAR_LLAMA_SERVER_URL` is already set, the wrapper skips the local llama.cpp tool check unless `--install` is also provided.

## Local GGUF Override

Use a local model file instead of the Hugging Face `-hf` shortcut:

```bash
LAMINAR_LLAMA_GGUF_MODEL=models/local/model.gguf \
./scripts/benchmark_real_llm.sh --requests 20 --concurrency 4
```

`models/local/` and `*.gguf` are ignored by git so model binaries stay out of the repository.

## Useful Controls

| Variable | Default | Meaning |
| --- | --- | --- |
| `LAMINAR_LLAMA_HF_MODEL` | `bartowski/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M` | Hugging Face GGUF spec passed to `llama-server -hf` |
| `LAMINAR_LLAMA_GGUF_MODEL` | unset | Local `.gguf` path. Overrides the Hugging Face model |
| `LAMINAR_LLAMA_SERVER_PORT` | `18081` | Local server port used by `scripts/run_llama_server.sh` |
| `LAMINAR_LLAMA_SERVER_CONTEXT` | `2048` | Context size passed to `llama-server -c` |
| `LAMINAR_LLAMA_SERVER_THREADS` | unset | Optional CPU thread count passed with `-t` |
| `LAMINAR_LLAMA_SERVER_GPU_LAYERS` | unset | Optional GPU layer count passed with `-ngl` |
| `LAMINAR_REAL_LLM_REQUESTS` | `20` | Default request count for the real-LLM wrapper |
| `LAMINAR_REAL_LLM_CONCURRENCY` | `4` | Default concurrency for the real-LLM wrapper |
| `LAMINAR_REAL_LLM_OUTPUT` | `docs/results/llama-server-latest.md` | Default report path |

## Reading The Results

For a strong benchmark write-up, include:

- machine, OS, CPU/GPU, memory, and thermal constraints
- exact model and quantization
- request count and concurrency
- fixed vs adaptive batching comparison
- p50, p95, and p99 client latency
- throughput in successful requests per second
- average observed batch size
- rejected request and worker error counts
- response metadata such as `tokens_predicted`, `tokens_evaluated`, `stream_chunks`, and `ttft_micros`
- one trace sample showing `http.predict`, `batch.queue_wait`, `batch.flush`, `worker.grpc`, `backend.inference`, and token spans when streaming is exercised

Interpretation matters more than a single high number. The strongest story is where batching improves throughput, how much tail latency it adds, and what the system does at saturation.

## Smaller Fallback

If the Qwen2.5 0.5B GGUF is too heavy for a machine, `HuggingFaceTB/SmolLM2-135M-Instruct` is another Apache-2.0 small instruction model to consider. Use an available GGUF conversion or local `.gguf` file through `LAMINAR_LLAMA_GGUF_MODEL`.

Reference:

- [HuggingFaceTB/SmolLM2-135M-Instruct](https://huggingface.co/HuggingFaceTB/SmolLM2-135M-Instruct)
