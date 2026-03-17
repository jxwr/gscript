#!/bin/bash
# Run all benchmarks in the suite
set -e

GSCRIPT="${1:-../../cmd/gscript/main.go}"
MODE="${2:--vm}"

echo "============================================"
echo "  GScript Benchmark Suite"
echo "  Mode: $MODE"
echo "============================================"
echo ""

for bench in fib sieve mandelbrot ackermann matmul spectral_norm nbody binary_trees; do
    echo "--- $bench ---"
    go run "$GSCRIPT" "$MODE" "$bench.gs" 2>&1
    echo ""
done

echo "============================================"
echo "  Suite complete"
echo "============================================"
