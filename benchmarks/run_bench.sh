#!/bin/bash
# GScript Benchmark Runner
# Runs all 22 benchmarks in VM and JIT modes, outputs a comparison table.
#
# Usage: bash benchmarks/run_bench.sh [--quick]
#   (default)  Full suite: VM + JIT + LuaJIT
#   --quick    Go warm benchmarks only (~15s)

set -uo pipefail
cd "$(dirname "$0")/.."

QUICK=false
if [[ "${1:-}" == "--quick" ]]; then
    QUICK=true
fi

GSCRIPT_BIN="/tmp/gscript_bench_$$"
TIMEOUT_SEC=60
RESULTS_DIR="/tmp/gscript_results_$$"
mkdir -p "$RESULTS_DIR"

run_with_timeout() {
    local timeout="$1"; shift
    perl -e "alarm $timeout; exec @ARGV" -- "$@" 2>&1
    return $?
}

extract_time() {
    local output="$1"
    local tl
    tl=$(echo "$output" | grep "Time:" | tail -1 | sed 's/.*Time: //' | awk '{print $1}')
    if [[ -n "$tl" ]]; then echo "$tl"; return; fi
    echo "n/a"
}

# Build
echo "Building gscript..."
if ! go build -o "$GSCRIPT_BIN" ./cmd/gscript/ 2>&1; then
    echo "ERROR: Build failed."
    exit 1
fi

echo "============================================"
echo "  GScript Benchmark Suite"
echo "  Date: $(date '+%Y-%m-%d %H:%M:%S')"
echo "  Platform: $(uname -m), $(go version 2>/dev/null | cut -d' ' -f3)"
echo "============================================"
echo ""

# Go warm benchmarks
echo ">>> Go warm benchmarks (JIT vs VM, benchtime=1s)..."
go test ./benchmarks/ -bench='Warm' -benchtime=1s -count=1 -run='^$' -timeout 120s 2>&1 | grep -v "^\[DEBUG\]"
echo ""

if $QUICK; then
    rm -f "$GSCRIPT_BIN"
    rm -rf "$RESULTS_DIR"
    exit 0
fi

# Full suite
BENCHMARKS="fib fib_recursive sieve mandelbrot ackermann matmul spectral_norm nbody fannkuch sort sum_primes mutual_recursion method_dispatch closure_bench string_bench binary_trees table_field_access table_array_access coroutine_bench fibonacci_iterative math_intensive object_creation"

# Run VM mode
echo ">>> VM mode (22 benchmarks)..."
for bench in $BENCHMARKS; do
    file="benchmarks/suite/${bench}.gs"
    if [[ ! -f "$file" ]]; then echo "MISSING" > "$RESULTS_DIR/vm_${bench}"; continue; fi
    output=$(run_with_timeout "$TIMEOUT_SEC" "$GSCRIPT_BIN" -vm "$file" 2>&1)
    ec=$?
    if [[ $ec -eq 142 ]] || [[ $ec -eq 137 ]]; then echo "TIMEOUT" > "$RESULTS_DIR/vm_${bench}"
    elif [[ $ec -ne 0 ]]; then echo "ERROR" > "$RESULTS_DIR/vm_${bench}"
    else extract_time "$output" > "$RESULTS_DIR/vm_${bench}"
    fi
done

# Run JIT mode (with -jit-stats so R146 T2 column can report
# entered-vs-compiled counts per benchmark).
echo ">>> JIT mode (22 benchmarks)..."
for bench in $BENCHMARKS; do
    file="benchmarks/suite/${bench}.gs"
    if [[ ! -f "$file" ]]; then echo "MISSING" > "$RESULTS_DIR/jit_${bench}"; continue; fi
    output=$(run_with_timeout "$TIMEOUT_SEC" "$GSCRIPT_BIN" -jit -jit-stats "$file" 2>&1)
    ec=$?
    if [[ $ec -eq 142 ]] || [[ $ec -eq 137 ]]; then echo "TIMEOUT" > "$RESULTS_DIR/jit_${bench}"
    elif [[ $ec -ne 0 ]]; then echo "ERROR" > "$RESULTS_DIR/jit_${bench}"
    else extract_time "$output" > "$RESULTS_DIR/jit_${bench}"
    fi
    # Parse -jit-stats for the R146 T2 column: how many protos were
    # compiled at Tier 2, and how many of those had native code actually
    # execute at least once (proto.EnteredTier2 != 0).
    compiled=$(echo "$output" | awk '/^  Tier 2 compiled:/ {print $4}')
    entered=$(echo "$output" | awk '/^  Tier 2 entered:/  {print $4}')
    compiled=${compiled:-0}
    entered=${entered:-0}
    echo "${entered}/${compiled}" > "$RESULTS_DIR/t2_${bench}"
done

# Run LuaJIT (optional)
if command -v luajit &>/dev/null && [[ -d benchmarks/lua ]]; then
    echo ">>> LuaJIT mode..."
    for f in benchmarks/lua/*.lua; do
        name=$(basename "$f" .lua)
        [[ "$name" == "bench_all" ]] || [[ "$name" == "run_all" ]] || [[ "$name" == "fn_calls" ]] && continue
        output=$(run_with_timeout "$TIMEOUT_SEC" luajit "$f" 2>&1)
        ec=$?
        if [[ $ec -eq 142 ]] || [[ $ec -eq 137 ]]; then echo "TIMEOUT" > "$RESULTS_DIR/lj_${name}"
        elif [[ $ec -ne 0 ]]; then echo "ERROR" > "$RESULTS_DIR/lj_${name}"
        else extract_time "$output" > "$RESULTS_DIR/lj_${name}"
        fi
    done
fi

# Summary table
echo ""
echo "============================================"
echo "  RESULTS"
echo "============================================"
printf "| %-25s | %-10s | %-10s | %-8s | %-10s | %-6s |\n" "Benchmark" "VM" "JIT" "JIT/VM" "LuaJIT" "T2"
printf "| %-25s | %-10s | %-10s | %-8s | %-10s | %-6s |\n" "-------------------------" "----------" "----------" "--------" "----------" "------"

faster=0; total=0
for bench in $BENCHMARKS; do
    total=$((total + 1))
    vm=$(cat "$RESULTS_DIR/vm_${bench}" 2>/dev/null || echo "n/a")
    jit=$(cat "$RESULTS_DIR/jit_${bench}" 2>/dev/null || echo "n/a")
    lj=$(cat "$RESULTS_DIR/lj_${bench}" 2>/dev/null || echo "")
    t2=$(cat "$RESULTS_DIR/t2_${bench}" 2>/dev/null || echo "-/-")

    vm_num=$(echo "$vm" | sed 's/s$//')
    jit_num=$(echo "$jit" | sed 's/s$//')
    if echo "$vm" | grep -qE '^[0-9]' && echo "$jit" | grep -qE '^[0-9]'; then
        ratio=$(echo "$vm_num $jit_num" | awk '{if($2>0) printf "%.2fx", $1/$2; else print "--"}')
        is_f=$(echo "$vm_num $jit_num" | awk '{print ($1/$2 >= 1.0) ? 1 : 0}')
        faster=$((faster + is_f))
    else
        ratio="--"
    fi

    printf "| %-25s | %-10s | %-10s | %-8s | %-10s | %-6s |\n" "$bench" "$vm" "$jit" "$ratio" "$lj" "$t2"
done

echo ""
echo "Faster than VM: $faster / $total"
echo ""
echo "============================================"
echo "  Done: $(date '+%Y-%m-%d %H:%M:%S')"
echo "============================================"

rm -f "$GSCRIPT_BIN"
rm -rf "$RESULTS_DIR"
