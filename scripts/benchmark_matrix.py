#!/usr/bin/env python3
"""Run a local batching benchmark matrix and write a Markdown report.

The harness intentionally uses only the Python standard library so it can run
on a fresh workstation after Bazel is installed.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import contextlib
import dataclasses
import hashlib
import json
import os
import pathlib
import signal
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


ROOT = pathlib.Path(__file__).resolve().parents[1]
RESULTS_DIR = ROOT / "docs" / "results"


@dataclasses.dataclass(frozen=True)
class Scenario:
    name: str
    env: dict[str, str]
    worker_count: int = 1
    requires_onnx: bool = False
    requires_llama_cpp: bool = False
    requires_llama_server: bool = False


@dataclasses.dataclass
class RunResult:
    scenario: Scenario
    env: dict[str, str]
    request_count: int
    concurrency: int
    worker_count: int
    success_count: int
    status_counts: dict[str, int]
    wall_time_seconds: float
    throughput_success_rps: float
    p50_seconds: float
    p95_seconds: float
    p99_seconds: float
    average_batch_size: float
    rejected_requests: float
    worker_errors: float
    stats: dict[str, object]
    trace_sample: dict[str, object]


SCENARIOS = [
    Scenario(
        name="no batching baseline",
        env={
            "LAMINAR_BATCH_POLICY": "fixed",
            "LAMINAR_MAX_BATCH_SIZE": "1",
            "LAMINAR_MAX_BATCH_WAIT": "1ms",
        },
    ),
    Scenario(
        name="fixed dynamic batching",
        env={
            "LAMINAR_BATCH_POLICY": "fixed",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
        },
    ),
    Scenario(
        name="adaptive batching",
        env={
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "150ms",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
    Scenario(
        name="adaptive batching, two workers",
        worker_count=2,
        env={
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "150ms",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
    Scenario(
        name="tiny model, no batching",
        env={
            "LAMINAR_WORKER_BACKEND": "tiny_model",
            "LAMINAR_TINY_MODEL_ITERATIONS": "16",
            "LAMINAR_BATCH_POLICY": "fixed",
            "LAMINAR_MAX_BATCH_SIZE": "1",
            "LAMINAR_MAX_BATCH_WAIT": "1ms",
        },
    ),
    Scenario(
        name="tiny model, adaptive batching",
        env={
            "LAMINAR_WORKER_BACKEND": "tiny_model",
            "LAMINAR_TINY_MODEL_ITERATIONS": "16",
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "10ms",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
    Scenario(
        name="tiny model, adaptive batching, two workers",
        worker_count=2,
        env={
            "LAMINAR_WORKER_BACKEND": "tiny_model",
            "LAMINAR_TINY_MODEL_ITERATIONS": "16",
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "10ms",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
    Scenario(
        name="tiny llm, no batching",
        env={
            "LAMINAR_WORKER_BACKEND": "tiny_llm",
            "LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS": "8",
            "LAMINAR_TINY_LLM_TOP_K": "5",
            "LAMINAR_TINY_LLM_TEMPERATURE": "0.8",
            "LAMINAR_BATCH_POLICY": "fixed",
            "LAMINAR_MAX_BATCH_SIZE": "1",
            "LAMINAR_MAX_BATCH_WAIT": "1ms",
        },
    ),
    Scenario(
        name="tiny llm, adaptive batching",
        env={
            "LAMINAR_WORKER_BACKEND": "tiny_llm",
            "LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS": "8",
            "LAMINAR_TINY_LLM_TOP_K": "5",
            "LAMINAR_TINY_LLM_TEMPERATURE": "0.8",
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "15ms",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
    Scenario(
        name="tiny llm, adaptive batching, two workers",
        worker_count=2,
        env={
            "LAMINAR_WORKER_BACKEND": "tiny_llm",
            "LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS": "8",
            "LAMINAR_TINY_LLM_TOP_K": "5",
            "LAMINAR_TINY_LLM_TEMPERATURE": "0.8",
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "15ms",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
    Scenario(
        name="continuous tiny llm, no batching",
        env={
            "LAMINAR_WORKER_BACKEND": "continuous_tiny_llm",
            "LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS": "8",
            "LAMINAR_TINY_LLM_TOP_K": "5",
            "LAMINAR_TINY_LLM_TEMPERATURE": "0.8",
            "LAMINAR_CONTINUOUS_MAX_PREFILL_TOKENS_PER_STEP": "32",
            "LAMINAR_CONTINUOUS_MAX_DECODE_SEQUENCES_PER_STEP": "8",
            "LAMINAR_CONTINUOUS_KV_CACHE_BLOCKS": "512",
            "LAMINAR_CONTINUOUS_KV_BLOCK_TOKENS": "16",
            "LAMINAR_CONTINUOUS_PREFIX_CACHE_TOKENS": "2",
            "LAMINAR_BATCH_POLICY": "fixed",
            "LAMINAR_MAX_BATCH_SIZE": "1",
            "LAMINAR_MAX_BATCH_WAIT": "1ms",
        },
    ),
    Scenario(
        name="continuous tiny llm, adaptive batching",
        env={
            "LAMINAR_WORKER_BACKEND": "continuous_tiny_llm",
            "LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS": "8",
            "LAMINAR_TINY_LLM_TOP_K": "5",
            "LAMINAR_TINY_LLM_TEMPERATURE": "0.8",
            "LAMINAR_CONTINUOUS_MAX_PREFILL_TOKENS_PER_STEP": "32",
            "LAMINAR_CONTINUOUS_MAX_DECODE_SEQUENCES_PER_STEP": "8",
            "LAMINAR_CONTINUOUS_KV_CACHE_BLOCKS": "512",
            "LAMINAR_CONTINUOUS_KV_BLOCK_TOKENS": "16",
            "LAMINAR_CONTINUOUS_PREFIX_CACHE_TOKENS": "2",
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "15ms",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
    Scenario(
        name="continuous tiny llm, adaptive batching, two workers",
        worker_count=2,
        env={
            "LAMINAR_WORKER_BACKEND": "continuous_tiny_llm",
            "LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS": "8",
            "LAMINAR_TINY_LLM_TOP_K": "5",
            "LAMINAR_TINY_LLM_TEMPERATURE": "0.8",
            "LAMINAR_CONTINUOUS_MAX_PREFILL_TOKENS_PER_STEP": "32",
            "LAMINAR_CONTINUOUS_MAX_DECODE_SEQUENCES_PER_STEP": "8",
            "LAMINAR_CONTINUOUS_KV_CACHE_BLOCKS": "512",
            "LAMINAR_CONTINUOUS_KV_BLOCK_TOKENS": "16",
            "LAMINAR_CONTINUOUS_PREFIX_CACHE_TOKENS": "2",
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "15ms",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
    Scenario(
        name="continuous tiny llm, admission control overload",
        env={
            "LAMINAR_WORKER_BACKEND": "continuous_tiny_llm",
            "LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS": "8",
            "LAMINAR_TINY_LLM_TOP_K": "5",
            "LAMINAR_TINY_LLM_TEMPERATURE": "0.8",
            "LAMINAR_CONTINUOUS_MAX_PREFILL_TOKENS_PER_STEP": "32",
            "LAMINAR_CONTINUOUS_MAX_DECODE_SEQUENCES_PER_STEP": "8",
            "LAMINAR_CONTINUOUS_KV_CACHE_BLOCKS": "512",
            "LAMINAR_CONTINUOUS_KV_BLOCK_TOKENS": "16",
            "LAMINAR_CONTINUOUS_PREFIX_CACHE_TOKENS": "2",
            "LAMINAR_ADMISSION_ENABLED": "true",
            "LAMINAR_ADMISSION_MAX_IN_FLIGHT_TOKENS": "40",
            "LAMINAR_ADMISSION_ESTIMATED_TOKENS_PER_BYTE": "0.25",
            "LAMINAR_ADMISSION_ESTIMATED_OUTPUT_TOKENS": "8",
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "15ms",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
    Scenario(
        name="onnx runtime, no batching",
        requires_onnx=True,
        env={
            "LAMINAR_WORKER_BACKEND": "onnx",
            "LAMINAR_ONNX_MODEL_PATH": "models/logreg_iris.onnx",
            "LAMINAR_BATCH_POLICY": "fixed",
            "LAMINAR_MAX_BATCH_SIZE": "1",
            "LAMINAR_MAX_BATCH_WAIT": "1ms",
        },
    ),
    Scenario(
        name="onnx runtime, adaptive batching",
        requires_onnx=True,
        env={
            "LAMINAR_WORKER_BACKEND": "onnx",
            "LAMINAR_ONNX_MODEL_PATH": "models/logreg_iris.onnx",
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "10ms",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
    Scenario(
        name="onnx runtime, adaptive batching, two workers",
        worker_count=2,
        requires_onnx=True,
        env={
            "LAMINAR_WORKER_BACKEND": "onnx",
            "LAMINAR_ONNX_MODEL_PATH": "models/logreg_iris.onnx",
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "10ms",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
    Scenario(
        name="llama.cpp gguf, no batching",
        requires_llama_cpp=True,
        env={
            "LAMINAR_WORKER_BACKEND": "llama_cpp",
            "LAMINAR_LLAMA_CPP_MAX_TOKENS": "32",
            "LAMINAR_LLAMA_CPP_CONTEXT_SIZE": "2048",
            "LAMINAR_LLAMA_CPP_TEMPERATURE": "0.2",
            "LAMINAR_LLAMA_CPP_TIMEOUT_MS": "120000",
            "LAMINAR_BATCH_POLICY": "fixed",
            "LAMINAR_MAX_BATCH_SIZE": "1",
            "LAMINAR_MAX_BATCH_WAIT": "1ms",
        },
    ),
    Scenario(
        name="llama.cpp gguf, adaptive batching",
        requires_llama_cpp=True,
        env={
            "LAMINAR_WORKER_BACKEND": "llama_cpp",
            "LAMINAR_LLAMA_CPP_MAX_TOKENS": "32",
            "LAMINAR_LLAMA_CPP_CONTEXT_SIZE": "2048",
            "LAMINAR_LLAMA_CPP_TEMPERATURE": "0.2",
            "LAMINAR_LLAMA_CPP_TIMEOUT_MS": "120000",
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "5s",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
    Scenario(
        name="llama.cpp server, no batching",
        requires_llama_server=True,
        env={
            "LAMINAR_WORKER_BACKEND": "llama_server",
            "LAMINAR_LLAMA_SERVER_MAX_TOKENS": "32",
            "LAMINAR_LLAMA_SERVER_TEMPERATURE": "0.2",
            "LAMINAR_LLAMA_SERVER_TIMEOUT_MS": "120000",
            "LAMINAR_LLAMA_SERVER_CACHE_PROMPT": "true",
            "LAMINAR_BATCH_POLICY": "fixed",
            "LAMINAR_MAX_BATCH_SIZE": "1",
            "LAMINAR_MAX_BATCH_WAIT": "1ms",
        },
    ),
    Scenario(
        name="llama.cpp server, adaptive batching",
        requires_llama_server=True,
        env={
            "LAMINAR_WORKER_BACKEND": "llama_server",
            "LAMINAR_LLAMA_SERVER_MAX_TOKENS": "32",
            "LAMINAR_LLAMA_SERVER_TEMPERATURE": "0.2",
            "LAMINAR_LLAMA_SERVER_TIMEOUT_MS": "120000",
            "LAMINAR_LLAMA_SERVER_CACHE_PROMPT": "true",
            "LAMINAR_BATCH_POLICY": "adaptive",
            "LAMINAR_MAX_BATCH_SIZE": "32",
            "LAMINAR_MAX_BATCH_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_MIN_WAIT": "1ms",
            "LAMINAR_ADAPTIVE_MAX_WAIT": "10ms",
            "LAMINAR_ADAPTIVE_TARGET_LATENCY": "5s",
            "LAMINAR_ADAPTIVE_QUEUE_HIGH_WATERMARK": "32",
        },
    ),
]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--requests", type=int, default=200)
    parser.add_argument("--concurrency", type=int, default=50)
    parser.add_argument("--output", type=pathlib.Path, default=RESULTS_DIR / "latest.md")
    parser.add_argument("--skip-build", action="store_true")
    parser.add_argument(
        "--scenario",
        action="append",
        default=[],
        help="Run only scenarios whose name contains this text. Repeat to include multiple filters.",
    )
    args = parser.parse_args()

    if args.requests <= 0 or args.concurrency <= 0:
        parser.error("--requests and --concurrency must be positive")

    if not args.skip_build:
        run(["bazel", "build", "//backend:worker", "//gateway:gateway"])

    args.output.parent.mkdir(parents=True, exist_ok=True)
    scenarios = filter_scenarios(SCENARIOS, args.scenario)
    results = []
    skipped = []
    for index, scenario in enumerate(scenarios):
        if scenario.requires_onnx and not onnx_runtime_available():
            print(f"skipping {scenario.name}: ONNX Runtime is not installed")
            skipped.append((scenario.name, "ONNX Runtime is not installed; run ./scripts/setup_onnxruntime.sh"))
            continue
        if scenario.requires_llama_cpp:
            available, reason = llama_cpp_available()
            if not available:
                print(f"skipping {scenario.name}: {reason}")
                skipped.append((scenario.name, reason))
                continue
        if scenario.requires_llama_server:
            available, reason = llama_server_available()
            if not available:
                print(f"skipping {scenario.name}: {reason}")
                skipped.append((scenario.name, reason))
                continue
        ports = Ports(worker_base=50051 + index * 4, gateway=8080 + index)
        print(f"running {scenario.name} on gateway :{ports.gateway}")
        results.append(
            run_scenario(
                scenario,
                scenario_env(scenario),
                ports,
                args.requests,
                args.concurrency,
            )
        )

    report = render_report(results, skipped)
    args.output.write_text(report)
    print(f"wrote {args.output}")
    return 0


def filter_scenarios(scenarios: list[Scenario], filters: list[str]) -> list[Scenario]:
    normalized = [value.strip().lower() for value in filters if value.strip()]
    if not normalized:
        return list(scenarios)
    selected = [
        scenario
        for scenario in scenarios
        if any(value in scenario.name.lower() for value in normalized)
    ]
    if not selected:
        available = ", ".join(scenario.name for scenario in scenarios)
        raise SystemExit(
            "no scenarios matched --scenario filter; available scenarios: "
            + available
        )
    return selected


@dataclasses.dataclass(frozen=True)
class Ports:
    worker_base: int
    gateway: int


def run_scenario(
    scenario: Scenario,
    effective_env: dict[str, str],
    ports: Ports,
    requests: int,
    concurrency: int,
) -> RunResult:
    workers = start_workers(ports.worker_base, scenario, effective_env)
    gateway = None
    try:
        gateway = start_gateway(scenario, effective_env, ports)
        wait_for_http(f"http://localhost:{ports.gateway}/health")
        wait_for_http(f"http://localhost:{ports.gateway}/ready")

        start = time.time()
        latencies: list[float] = []
        status_counts: dict[str, int] = {}
        with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
            futures = [
                pool.submit(send_request, ports.gateway, i)
                for i in range(1, requests + 1)
            ]
            for future in concurrent.futures.as_completed(futures):
                status, latency = future.result()
                status_counts[status] = status_counts.get(status, 0) + 1
                if status.startswith("2"):
                    latencies.append(latency)
        end = time.time()

        metrics = parse_prometheus(fetch_text(f"http://localhost:{ports.gateway}/metrics"))
        stats = json.loads(fetch_text(f"http://localhost:{ports.gateway}/stats"))
        trace_sample = capture_trace_sample(ports.gateway, scenario.name)
        success_count = sum(count for status, count in status_counts.items() if status.startswith("2"))
        wall_time = max(end - start, 1e-9)

        total_requests = metrics.get("batch_size_distribution_sum", 0.0)
        total_batches = metrics.get("batch_size_distribution_count", 0.0)
        average_batch_size = total_requests / total_batches if total_batches else 0.0

        return RunResult(
            scenario=scenario,
            env=effective_env,
            request_count=requests,
            concurrency=concurrency,
            worker_count=scenario.worker_count,
            success_count=success_count,
            status_counts=status_counts,
            wall_time_seconds=wall_time,
            throughput_success_rps=success_count / wall_time,
            p50_seconds=percentile(latencies, 0.50),
            p95_seconds=percentile(latencies, 0.95),
            p99_seconds=percentile(latencies, 0.99),
            average_batch_size=average_batch_size,
            rejected_requests=metrics.get("requests_rejected_total", 0.0),
            worker_errors=metrics.get("worker_errors_total", 0.0),
            stats=stats,
            trace_sample=trace_sample,
        )
    finally:
        stop_process(gateway)
        for worker in workers:
            stop_process(worker)


def start_workers(
    worker_base_port: int,
    scenario: Scenario,
    effective_env: dict[str, str],
) -> list[subprocess.Popen[str]]:
    return [
        start_worker(worker_base_port + offset, effective_env)
        for offset in range(scenario.worker_count)
    ]


def start_worker(port: int, scenario_env: dict[str, str]) -> subprocess.Popen[str]:
    env = os.environ.copy()
    env.update(scenario_env)
    return subprocess.Popen(
        [str(ROOT / "bazel-bin" / "backend" / "worker"), f"127.0.0.1:{port}"],
        cwd=ROOT,
        env=env,
        text=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.STDOUT,
    )


def start_gateway(scenario: Scenario, scenario_env: dict[str, str], ports: Ports) -> subprocess.Popen[str]:
    env = os.environ.copy()
    env.update(scenario_env)
    worker_addresses = [
        f"localhost:{ports.worker_base + offset}"
        for offset in range(scenario.worker_count)
    ]
    env.update(
        {
            "LAMINAR_HTTP_ADDR": f":{ports.gateway}",
            "LAMINAR_WORKER_ADDR": worker_addresses[0],
            "LAMINAR_WORKER_ADDRS": ",".join(worker_addresses),
        }
    )
    return subprocess.Popen(
        [str(ROOT / "bazel-bin" / "gateway" / "gateway_" / "gateway")],
        cwd=ROOT,
        env=env,
        text=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.STDOUT,
    )


def onnx_runtime_available() -> bool:
    configured = os.environ.get("LAMINAR_ONNX_RUNTIME_LIB", "").strip()
    if configured:
        return pathlib.Path(configured).exists()
    if sys.platform == "darwin":
        return (ROOT / "third_party" / "onnxruntime" / "lib" / "libonnxruntime.dylib").exists()
    if sys.platform.startswith("linux"):
        return (ROOT / "third_party" / "onnxruntime" / "lib" / "libonnxruntime.so").exists()
    return False


LLAMA_CPP_EXTERNAL_ENV_KEYS = [
    "LAMINAR_LLAMA_CPP_BINARY",
    "LAMINAR_LLAMA_CPP_MODEL",
]


LLAMA_SERVER_EXTERNAL_ENV_KEYS = [
    "LAMINAR_LLAMA_SERVER_URL",
]


def scenario_env(scenario: Scenario) -> dict[str, str]:
    env = dict(scenario.env)
    if scenario.requires_llama_cpp:
        env.setdefault("LAMINAR_LLAMA_CPP_BINARY", os.environ.get("LAMINAR_LLAMA_CPP_BINARY", "llama-cli"))
        for key in LLAMA_CPP_EXTERNAL_ENV_KEYS:
            value = os.environ.get(key, "").strip()
            if value:
                env[key] = expand_llama_cpp_path_if_needed(value)
    if scenario.requires_llama_server:
        for key in LLAMA_SERVER_EXTERNAL_ENV_KEYS:
            value = os.environ.get(key, "").strip()
            if value:
                env[key] = value
    return env


def expand_llama_cpp_path_if_needed(value: str) -> str:
    if os.sep in value or (os.altsep is not None and os.altsep in value):
        return str(pathlib.Path(value).expanduser())
    return value


def llama_cpp_available() -> tuple[bool, str]:
    model = os.environ.get("LAMINAR_LLAMA_CPP_MODEL", "").strip()
    if not model:
        return False, "LAMINAR_LLAMA_CPP_MODEL is not set; provide a local GGUF model path"
    model_path = pathlib.Path(model).expanduser()
    if not model_path.exists():
        return False, f"LAMINAR_LLAMA_CPP_MODEL does not exist: {model}"

    binary = os.environ.get("LAMINAR_LLAMA_CPP_BINARY", "llama-cli").strip() or "llama-cli"
    binary_path = pathlib.Path(binary).expanduser()
    if os.sep in binary or (os.altsep is not None and os.altsep in binary):
        if not binary_path.exists():
            return False, f"LAMINAR_LLAMA_CPP_BINARY does not exist: {binary}"
        if not os.access(binary_path, os.X_OK):
            return False, f"LAMINAR_LLAMA_CPP_BINARY is not executable: {binary}"
        return True, ""

    if shutil.which(binary) is None:
        return False, f"llama.cpp binary not found on PATH: {binary}"
    return True, ""


def llama_server_available() -> tuple[bool, str]:
    url = os.environ.get("LAMINAR_LLAMA_SERVER_URL", "").strip()
    if not url:
        return False, "LAMINAR_LLAMA_SERVER_URL is not set; start llama-server and point to /completion"
    try:
        parsed = urllib.parse.urlparse(url)
    except Exception:
        return False, f"LAMINAR_LLAMA_SERVER_URL is invalid: {url}"
    if parsed.scheme != "http" or not parsed.netloc:
        return False, "LAMINAR_LLAMA_SERVER_URL must be an http:// URL"
    health_url = urllib.parse.urlunparse((parsed.scheme, parsed.netloc, "/health", "", "", ""))
    try:
        with urllib.request.urlopen(health_url, timeout=2) as response:
            response.read()
            if response.status < 500:
                return True, ""
    except Exception:
        pass
    return False, f"llama server is not reachable at {health_url}"


def send_request(port: int, request_id: int) -> tuple[str, float]:
    payload = json.dumps({"prompt": f"benchmark request {request_id}"}).encode()
    trace_id = f"{port:04x}{request_id:028x}"
    parent_span_id = f"{request_id:016x}"
    req = urllib.request.Request(
        f"http://localhost:{port}/predict",
        data=payload,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "Traceparent": f"00-{trace_id}-{parent_span_id}-01",
        },
    )
    start = time.time()
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            response.read()
            status = str(response.status)
    except urllib.error.HTTPError as exc:
        exc.read()
        status = str(exc.code)
    except Exception:
        status = "client_error"
    return status, time.time() - start


def capture_trace_sample(port: int, scenario_name: str) -> dict[str, object]:
    trace_id = hashlib.sha256(scenario_name.encode()).hexdigest()[:32]
    parent_span_id = hashlib.sha256((scenario_name + "-parent").encode()).hexdigest()[:16]
    payload = json.dumps({"prompt": f"trace sample for {scenario_name}"}).encode()
    req = urllib.request.Request(
        f"http://localhost:{port}/predict",
        data=payload,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "Traceparent": f"00-{trace_id}-{parent_span_id}-01",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            response_body = response.read().decode()
            status = response.status
        response_sample = compact_response(json.loads(response_body))
        trace = json.loads(fetch_text(f"http://localhost:{port}/traces?trace_id={trace_id}"))
    except Exception as exc:
        return {
            "trace_id": trace_id,
            "error": str(exc),
        }
    return {
        "trace_id": trace_id,
        "status": status,
        "response": response_sample,
        "spans": [compact_span(span) for span in trace.get("spans", [])],
        "otlp": compact_otlp(json.loads(fetch_text(f"http://localhost:{port}/traces/otlp?trace_id={trace_id}"))),
    }


def compact_response(payload: dict[str, object]) -> dict[str, object]:
    result = payload.get("result", "")
    if isinstance(result, str) and len(result) > 500:
        result = result[:497] + "..."
    compact = {
        "id": payload.get("id"),
        "result": result,
    }
    metadata = payload.get("metadata")
    if isinstance(metadata, dict):
        compact["metadata"] = metadata
    return compact


def compact_span(span: dict[str, object]) -> dict[str, object]:
    return {
        "name": span.get("name"),
        "span_id": span.get("span_id"),
        "parent_span_id": span.get("parent_span_id"),
        "status": span.get("status"),
        "duration_micros": span.get("duration_micros"),
        "attributes": span.get("attributes", {}),
    }


def compact_otlp(payload: dict[str, object]) -> dict[str, object]:
    resource_spans = payload.get("resourceSpans", [])
    if not isinstance(resource_spans, list) or not resource_spans:
        return {"resourceSpans": 0, "scopeSpans": 0, "span_count": 0}
    first_resource = resource_spans[0]
    if not isinstance(first_resource, dict):
        return {"resourceSpans": len(resource_spans), "scopeSpans": 0, "span_count": 0}
    scope_spans = first_resource.get("scopeSpans", [])
    if not isinstance(scope_spans, list) or not scope_spans:
        return {"resourceSpans": len(resource_spans), "scopeSpans": 0, "span_count": 0}
    first_scope = scope_spans[0]
    if not isinstance(first_scope, dict):
        return {"resourceSpans": len(resource_spans), "scopeSpans": len(scope_spans), "span_count": 0}
    spans = first_scope.get("spans", [])
    if not isinstance(spans, list):
        spans = []
    span_names = [
        span.get("name")
        for span in spans
        if isinstance(span, dict)
    ]
    return {
        "resourceSpans": len(resource_spans),
        "scopeSpans": len(scope_spans),
        "span_count": len(spans),
        "span_names": span_names,
        "first_span": compact_otlp_span(spans[0]) if spans else {},
    }


def compact_otlp_span(span: object) -> dict[str, object]:
    if not isinstance(span, dict):
        return {}
    return {
        "name": span.get("name"),
        "traceId": span.get("traceId"),
        "spanId": span.get("spanId"),
        "parentSpanId": span.get("parentSpanId"),
        "kind": span.get("kind"),
        "status": span.get("status"),
    }


def wait_for_http(url: str, timeout_seconds: float = 10.0) -> None:
    deadline = time.time() + timeout_seconds
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=1) as response:
                if response.status < 500:
                    return
        except Exception as exc:
            last_error = exc
        time.sleep(0.1)
    raise RuntimeError(f"timed out waiting for {url}: {last_error}")


def fetch_text(url: str) -> str:
    with urllib.request.urlopen(url, timeout=5) as response:
        return response.read().decode()


def parse_prometheus(text: str) -> dict[str, float]:
    metrics: dict[str, float] = {}
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "{" in line:
            continue
        parts = line.split()
        if len(parts) != 2:
            continue
        try:
            metrics[parts[0]] = float(parts[1])
        except ValueError:
            pass
    return metrics


def percentile(values: list[float], pct: float) -> float:
    if not values:
        return float("nan")
    values = sorted(values)
    index = round((len(values) - 1) * pct)
    return values[index]


def render_report(results: list[RunResult], skipped: list[tuple[str, str]]) -> str:
    generated = time.strftime("%Y-%m-%d %H:%M:%S %Z")
    lines = [
        "# Benchmark Results",
        "",
        f"Generated: `{generated}`",
        "",
        "These numbers are local-machine evidence, not universal performance claims.",
        "",
        "| Scenario | Workers | Requests | Concurrency | Success | Throughput (req/s) | p50 (s) | p95 (s) | p99 (s) | Avg Batch | Rejected | Worker Errors |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for result in results:
        lines.append(
            "| "
            + " | ".join(
                [
                    result.scenario.name,
                    str(result.worker_count),
                    str(result.request_count),
                    str(result.concurrency),
                    str(result.success_count),
                    f"{result.throughput_success_rps:.2f}",
                    f"{result.p50_seconds:.4f}",
                    f"{result.p95_seconds:.4f}",
                    f"{result.p99_seconds:.4f}",
                    f"{result.average_batch_size:.2f}",
                    f"{result.rejected_requests:.0f}",
                    f"{result.worker_errors:.0f}",
                ]
            )
            + " |"
        )

    if skipped:
        lines.extend(["", "## Skipped Scenarios", ""])
        for name, reason in skipped:
            lines.append(f"- `{name}`: {reason}")

    lines.extend(["", "## Scenario Configuration", ""])
    for result in results:
        lines.extend(
            [
                f"### {result.scenario.name}",
                "",
                "```json",
                json.dumps(result.env, indent=2, sort_keys=True),
                "```",
                "",
                f"Workers: `{result.worker_count}`",
                "",
                "Runtime policy snapshot:",
                "",
                "```json",
                json.dumps(result.stats.get("runtime", {}).get("batch_policy", {}), indent=2, sort_keys=True),
                "```",
                "",
                "Runtime admission snapshot:",
                "",
                "```json",
                json.dumps(result.stats.get("runtime", {}).get("admission", {}), indent=2, sort_keys=True),
                "```",
                "",
                "Runtime worker snapshot:",
                "",
                "```json",
                json.dumps(result.stats.get("runtime", {}).get("workers", []), indent=2, sort_keys=True),
                "```",
                "",
                "Trace sample:",
                "",
                "```json",
                json.dumps(result.trace_sample, indent=2, sort_keys=True),
                "```",
                "",
            ]
        )
    return "\n".join(lines)


def run(command: list[str]) -> None:
    subprocess.run(command, cwd=ROOT, check=True)


def stop_process(process: subprocess.Popen[str] | None) -> None:
    if process is None or process.poll() is not None:
        return
    with contextlib.suppress(ProcessLookupError):
        process.send_signal(signal.SIGTERM)
    try:
        process.wait(timeout=3)
    except subprocess.TimeoutExpired:
        with contextlib.suppress(ProcessLookupError):
            process.kill()
        process.wait(timeout=3)


if __name__ == "__main__":
    sys.exit(main())
