#!/bin/bash
# Strict benchmark comparison harness.
#
# Usage:
#   bash benchmarks/strict_guard.sh --bench=fib --runs=7 --warmup=2
#   bash benchmarks/strict_guard.sh --runs=9 --allow-wall-time
#   bash benchmarks/strict_guard.sh --dry-run

set -u
cd "$(dirname "$0")/.."
exec python3 benchmarks/strict_guard.py "$@"
