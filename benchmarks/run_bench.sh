#!/bin/bash
# GScript Benchmark Runner
# Runs all benchmarks in parallel, outputs structured results
# Usage: bash benchmarks/run_bench.sh [--quick]
#
# Modes:
#   (default)  Full suite: VM + Trace + LuaJIT, Go warm benchmarks
#   --quick    Go warm benchmarks only (fastest, ~15s)

set -euo pipefail
cd "$(dirname "$0")/.."

QUICK=false
if [[ "${1:-}" == "--quick" ]]; then
    QUICK=true
fi

RESULTS_DIR="/tmp/gscript_bench_$$"
mkdir -p "$RESULTS_DIR"

GSCRIPT_BIN="/tmp/gscript_bench_bin"

# Step 1: Build the binary once (avoids repeated go run compilation)
echo "Building gscript..."
go build -o "$GSCRIPT_BIN" ./cmd/gscript/ 2>&1

echo "============================================"
echo "  GScript Benchmark Suite"
echo "  Date: $(date '+%Y-%m-%d %H:%M:%S')"
echo "  Platform: $(uname -m), $(go version | cut -d' ' -f3)"
echo "============================================"
echo ""

# ---------------------------------------------------------------------------
# Go warm benchmarks (JIT + VM, ~14s)
# ---------------------------------------------------------------------------
echo ">>> Go warm benchmarks (JIT vs VM)..."
go test ./benchmarks/ -bench='Warm' -benchtime=1s -count=1 -timeout 60s 2>&1 | tee "$RESULTS_DIR/go_warm.txt"
echo ""

if $QUICK; then
    echo ">>> Quick mode — skipping suite and LuaJIT benchmarks"
    echo ""
    cat "$RESULTS_DIR/go_warm.txt"
    rm -rf "$RESULTS_DIR"
    rm -f "$GSCRIPT_BIN"
    exit 0
fi

# ---------------------------------------------------------------------------
# Suite benchmarks: VM and Trace modes in parallel
# ---------------------------------------------------------------------------
# Known broken: binary_trees (stack overflow)
BENCHMARKS="fib sieve mandelbrot ackermann matmul spectral_norm nbody fannkuch sort sum_primes mutual_recursion method_dispatch closure_bench string_bench"

echo ">>> Suite benchmarks (VM + Trace in parallel)..."

# Run VM suite in background
(
    echo "=== VM Mode ==="
    for bench in $BENCHMARKS; do
        echo "--- $bench ---"
        "$GSCRIPT_BIN" -vm "benchmarks/suite/${bench}.gs" 2>&1 || echo "FAILED: $bench"
        echo ""
    done
) > "$RESULTS_DIR/suite_vm.txt" 2>&1 &
PID_VM=$!

# Run Trace suite in background
(
    echo "=== Trace Mode ==="
    for bench in $BENCHMARKS; do
        echo "--- $bench ---"
        timeout 30 "$GSCRIPT_BIN" -trace "benchmarks/suite/${bench}.gs" 2>&1 || echo "FAILED/TIMEOUT: $bench"
        echo ""
    done
) > "$RESULTS_DIR/suite_trace.txt" 2>&1 &
PID_TRACE=$!

# Run LuaJIT benchmarks in background (if luajit is available)
if command -v luajit &>/dev/null; then
    (
        echo "=== LuaJIT ==="
        if [[ -f benchmarks/lua/run_all.lua ]]; then
            cd benchmarks/lua
            luajit run_all.lua 2>&1
        fi
    ) > "$RESULTS_DIR/luajit.txt" 2>&1 &
    PID_LUAJIT=$!
else
    PID_LUAJIT=""
    echo "(luajit not found, skipping LuaJIT comparison)"
fi

# Wait for all
wait $PID_VM
echo "  VM suite done"
wait $PID_TRACE
echo "  Trace suite done"
if [[ -n "$PID_LUAJIT" ]]; then
    wait $PID_LUAJIT
    echo "  LuaJIT done"
fi

echo ""
echo "============================================"
echo "  RESULTS"
echo "============================================"
echo ""

echo "--- Go Warm Benchmarks ---"
grep "^Benchmark" "$RESULTS_DIR/go_warm.txt" || true
echo ""

echo "--- Suite: VM Mode ---"
cat "$RESULTS_DIR/suite_vm.txt"
echo ""

echo "--- Suite: Trace Mode ---"
cat "$RESULTS_DIR/suite_trace.txt"
echo ""

if [[ -n "$PID_LUAJIT" ]] && [[ -f "$RESULTS_DIR/luajit.txt" ]]; then
    echo "--- LuaJIT ---"
    cat "$RESULTS_DIR/luajit.txt"
    echo ""
fi

echo "============================================"
echo "  Full results saved to: $RESULTS_DIR/"
echo "============================================"

# Cleanup
rm -f "$GSCRIPT_BIN"
