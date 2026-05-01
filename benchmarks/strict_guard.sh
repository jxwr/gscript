#!/bin/bash
# Strict benchmark truth-pass harness.
#
# Usage:
#   bash benchmarks/strict_guard.sh --runs=3 --warmup=1
#   bash benchmarks/strict_guard.sh --bench=suite/fib --bench=variants/sort_mixed_numeric
#   bash benchmarks/strict_guard.sh --runs=9 --allow-wall-time
#   bash benchmarks/strict_guard.sh --dry-run

set -u
cd "$(dirname "$0")/.."
exec python3 benchmarks/strict_guard.py "$@"
