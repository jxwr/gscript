#!/bin/bash
# Run extended real-world-ish GScript benchmarks in VM and JIT modes.

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

GSCRIPT_BIN="${GSCRIPT_BIN:-/tmp/gscript_extended_bench}"
TIMEOUT_SEC="${TIMEOUT_SEC:-90}"

run_with_timeout() {
    local timeout="$1"
    shift
    perl -e "alarm $timeout; exec @ARGV" -- "$@" 2>&1
}

echo ">>> Building gscript for extended benchmarks..."
go build -o "$GSCRIPT_BIN" ./cmd/gscript || exit 1

BENCHMARKS=(
    actors_dispatch_mutation
    groupby_nested_agg
    json_table_walk
    log_tokenize_format
    mixed_inventory_sim
    producer_consumer_pipeline
)

for mode in -vm -jit; do
    echo "============================================"
    echo "  GScript extended benchmarks (${mode})"
    echo "============================================"
    for bench in "${BENCHMARKS[@]}"; do
        echo "--- ${bench} (${mode}) ---"
        if ! run_with_timeout "$TIMEOUT_SEC" "$GSCRIPT_BIN" "$mode" "benchmarks/extended/${bench}.gs"; then
            echo "FAILED: ${bench} ${mode}" >&2
            exit 1
        fi
        echo ""
    done
done

