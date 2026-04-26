#!/bin/bash
# Robust full-suite comparison guard.
#
# Usage:
#   bash benchmarks/regression_guard.sh --runs=3
#   bash benchmarks/regression_guard.sh --runs=5 --json benchmarks/data/regression_guard_latest.json

set -u
cd "$(dirname "$0")/.."
exec python3 benchmarks/regression_guard.py "$@"
