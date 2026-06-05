#!/usr/bin/env bash
#
# Run the local benchmark matrix and write docs/results/latest.md.

set -euo pipefail

exec ./scripts/benchmark_matrix.py "$@"
