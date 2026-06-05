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
import json
import os
import pathlib
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request


ROOT = pathlib.Path(__file__).resolve().parents[1]
RESULTS_DIR = ROOT / "docs" / "results"


@dataclasses.dataclass(frozen=True)
class Scenario:
    name: str
    env: dict[str, str]
    worker_count: int = 1


@dataclasses.dataclass
class RunResult:
    scenario: Scenario
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
]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--requests", type=int, default=200)
    parser.add_argument("--concurrency", type=int, default=50)
    parser.add_argument("--output", type=pathlib.Path, default=RESULTS_DIR / "latest.md")
    parser.add_argument("--skip-build", action="store_true")
    args = parser.parse_args()

    if args.requests <= 0 or args.concurrency <= 0:
        parser.error("--requests and --concurrency must be positive")

    if not args.skip_build:
        run(["bazel", "build", "//backend:worker", "//gateway:gateway"])

    args.output.parent.mkdir(parents=True, exist_ok=True)
    results = []
    for index, scenario in enumerate(SCENARIOS):
        ports = Ports(worker_base=50051 + index * 4, gateway=8080 + index)
        print(f"running {scenario.name} on gateway :{ports.gateway}")
        results.append(run_scenario(scenario, ports, args.requests, args.concurrency))

    report = render_report(results)
    args.output.write_text(report)
    print(f"wrote {args.output}")
    return 0


@dataclasses.dataclass(frozen=True)
class Ports:
    worker_base: int
    gateway: int


def run_scenario(scenario: Scenario, ports: Ports, requests: int, concurrency: int) -> RunResult:
    workers = start_workers(ports.worker_base, scenario.worker_count)
    gateway = None
    try:
        gateway = start_gateway(scenario, ports)
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
        success_count = sum(count for status, count in status_counts.items() if status.startswith("2"))
        wall_time = max(end - start, 1e-9)

        total_requests = metrics.get("batch_size_distribution_sum", 0.0)
        total_batches = metrics.get("batch_size_distribution_count", 0.0)
        average_batch_size = total_requests / total_batches if total_batches else 0.0

        return RunResult(
            scenario=scenario,
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
        )
    finally:
        stop_process(gateway)
        for worker in workers:
            stop_process(worker)


def start_workers(worker_base_port: int, count: int) -> list[subprocess.Popen[str]]:
    return [start_worker(worker_base_port + offset) for offset in range(count)]


def start_worker(port: int) -> subprocess.Popen[str]:
    return subprocess.Popen(
        [str(ROOT / "bazel-bin" / "backend" / "worker"), f"127.0.0.1:{port}"],
        cwd=ROOT,
        text=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.STDOUT,
    )


def start_gateway(scenario: Scenario, ports: Ports) -> subprocess.Popen[str]:
    env = os.environ.copy()
    env.update(scenario.env)
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


def send_request(port: int, request_id: int) -> tuple[str, float]:
    payload = json.dumps({"prompt": f"benchmark request {request_id}"}).encode()
    req = urllib.request.Request(
        f"http://localhost:{port}/predict",
        data=payload,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "X-Trace-ID": f"matrix-{request_id}",
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


def render_report(results: list[RunResult]) -> str:
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

    lines.extend(["", "## Scenario Configuration", ""])
    for result in results:
        lines.extend(
            [
                f"### {result.scenario.name}",
                "",
                "```json",
                json.dumps(result.scenario.env, indent=2, sort_keys=True),
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
                "Runtime worker snapshot:",
                "",
                "```json",
                json.dumps(result.stats.get("runtime", {}).get("workers", []), indent=2, sort_keys=True),
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
