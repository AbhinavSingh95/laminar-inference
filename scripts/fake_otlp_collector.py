#!/usr/bin/env python3
"""Tiny OTLP/HTTP JSON collector for local Laminar demos."""

from __future__ import annotations

import argparse
import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class CollectorHandler(BaseHTTPRequestHandler):
    server_version = "LaminarFakeOTLP/1.0"

    def do_POST(self) -> None:
        if self.path != "/v1/traces":
            self.send_response(404)
            self.end_headers()
            return

        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        try:
            payload = json.loads(body)
        except json.JSONDecodeError:
            self.send_response(400)
            self.end_headers()
            return

        spans = list(iter_spans(payload))
        print(
            json.dumps(
                {
                    "time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                    "path": self.path,
                    "span_count": len(spans),
                    "span_names": [span.get("name") for span in spans],
                },
                sort_keys=True,
            ),
            flush=True,
        )

        self.send_response(202)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"accepted_spans": len(spans)}).encode())

    def log_message(self, format: str, *args: object) -> None:
        return


def iter_spans(payload: dict[str, object]):
    for resource_span in payload.get("resourceSpans", []):
        for scope_span in resource_span.get("scopeSpans", []):
            for span in scope_span.get("spans", []):
                yield span


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=4318)
    args = parser.parse_args()

    server = ThreadingHTTPServer((args.host, args.port), CollectorHandler)
    print(f"fake OTLP collector listening on http://{args.host}:{args.port}/v1/traces", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
