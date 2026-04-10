#!/bin/bash
# GScript Full Benchmark Suite Runner
# Builds the CLI, runs warm Go benchmarks, then runs all suite benchmarks
# in VM and JIT modes SEQUENTIALLY.
#
# Usage: bash benchmarks/run_all.sh [--quick] [--runs=N]
#   --quick    Only run Go warm benchmarks (skip suite)
#   --runs=N   Runs per benchmark for median (default 3; use 5 for publish-grade baselines)
#
# Works on macOS (no coreutils timeout required).

set -uo pipefail
cd "$(dirname "$0")/.."

QUICK=false
RUNS=3
for arg in "$@"; do
    case "$arg" in
        --quick) QUICK=true ;;
        --runs=*) RUNS="${arg#--runs=}" ;;
    esac
done

GSCRIPT_BIN="/tmp/gscript_bench"
TIMEOUT_SEC=60

# macOS-compatible timeout using perl alarm
run_with_timeout() {
    local timeout="$1"
    shift
    perl -e "alarm $timeout; exec @ARGV" -- "$@" 2>&1
    return $?
}

# Run a GScript benchmark RUNS times, output sample stdout with median Time:.
# Returns 0 on success, 1 on failure, 2 on timeout.
run_benchmark_median() {
    local mode="$1"
    local bench="$2"
    local runs="${RUNS:-3}"
    local times=()
    local sample_output=""
    local i
    for i in $(seq 1 "$runs"); do
        local output
        output=$(run_with_timeout "$TIMEOUT_SEC" "$GSCRIPT_BIN" "$mode" "benchmarks/suite/${bench}.gs" 2>&1)
        local ec=$?
        if [[ $ec -eq 142 ]] || [[ $ec -eq 137 ]]; then
            echo "  TIMEOUT (>${TIMEOUT_SEC}s)"
            return 2
        elif [[ $ec -ne 0 ]]; then
            echo "  FAILED (exit $ec)"
            return 1
        fi
        [[ -z "$sample_output" ]] && sample_output="$output"
        local t
        t=$(echo "$output" | awk '/^Time:/ { gsub("s",""); print $2; exit }')
        times+=("$t")
    done
    local median
    median=$(printf '%s\n' "${times[@]}" | sort -n | awk -v n="$runs" 'NR==int((n+1)/2)')
    echo "$sample_output" | grep -v "^Time:"
    echo "Time: ${median}s"
}

# Run a LuaJIT benchmark RUNS times, output sample stdout with median Time:.
# Returns 0 on success, 1 on failure, 2 on timeout.
run_luajit_median() {
    local f="$1"
    local runs="${RUNS:-3}"
    local times=()
    local sample_output=""
    local i
    for i in $(seq 1 "$runs"); do
        local output
        output=$(run_with_timeout "$TIMEOUT_SEC" luajit "$f" 2>&1)
        local ec=$?
        if [[ $ec -eq 142 ]] || [[ $ec -eq 137 ]]; then
            echo "  TIMEOUT"
            return 2
        elif [[ $ec -ne 0 ]]; then
            echo "  FAILED"
            return 1
        fi
        [[ -z "$sample_output" ]] && sample_output="$output"
        local t
        t=$(echo "$output" | awk '/^Time:/ { gsub("s",""); print $2; exit }')
        times+=("$t")
    done
    local median
    median=$(printf '%s\n' "${times[@]}" | sort -n | awk -v n="$runs" 'NR==int((n+1)/2)')
    echo "$sample_output" | grep -v "^Time:"
    echo "Time: ${median}s"
}

# =========================================================================
echo "============================================"
echo "  GScript Full Benchmark Suite"
echo "  Date: $(date '+%Y-%m-%d %H:%M:%S')"
echo "  Platform: $(uname -m), $(go version 2>/dev/null | cut -d' ' -f3)"
echo "============================================"
echo ""

# Step 1: Build
echo ">>> Building gscript..."
if ! go build -o "$GSCRIPT_BIN" ./cmd/gscript/ 2>&1; then
    echo "ERROR: Build failed. Aborting."
    exit 1
fi
echo "    Built: $GSCRIPT_BIN"
echo ""

# Step 2: Go warm benchmarks
echo ">>> Go warm benchmarks (JIT vs VM, benchtime=3s)..."
echo "--------------------------------------------"
go test ./benchmarks/ -bench='Warm' -benchtime=3s -count=1 -run='^$' -timeout 120s 2>&1
echo ""

if $QUICK; then
    echo ">>> Quick mode -- skipping suite benchmarks"
    rm -f "$GSCRIPT_BIN"
    exit 0
fi

# Step 3: Suite benchmarks (VM, JIT -- sequential)
BENCHMARKS=(
    fib fib_recursive sieve mandelbrot ackermann matmul spectral_norm nbody fannkuch
    sort sum_primes mutual_recursion method_dispatch closure_bench string_bench binary_trees
    table_field_access table_array_access coroutine_bench fibonacci_iterative math_intensive object_creation
)

# Filter to only existing .gs files
EXISTING_BENCHMARKS=()
for bench in "${BENCHMARKS[@]}"; do
    if [[ -f "benchmarks/suite/${bench}.gs" ]]; then
        EXISTING_BENCHMARKS+=("$bench")
    fi
done

# bash 3.2 compat: use parallel indexed arrays (same ordering as EXISTING_BENCHMARKS)
VM_RESULTS=()
JIT_RESULTS=()
LUAJIT_RESULTS=()

echo ">>> Suite benchmarks (${#EXISTING_BENCHMARKS[@]} benchmarks x 2 modes, median-of-${RUNS}, sequential)..."
echo ""

# --- VM Mode ---
echo "============================================"
echo "  VM Mode"
echo "============================================"
for bench in "${EXISTING_BENCHMARKS[@]}"; do
    echo "--- $bench (VM) ---"
    output=$(run_benchmark_median "-vm" "$bench")
    ec=$?
    echo "$output"
    if [[ $ec -eq 2 ]]; then
        VM_RESULTS+=("timeout")
    elif [[ $ec -ne 0 ]]; then
        VM_RESULTS+=("FAILED")
    else
        time_line=$(echo "$output" | grep -i "Time:" | tail -1)
        VM_RESULTS+=("$time_line")
    fi
    echo ""
done

# --- JIT Mode ---
echo "============================================"
echo "  JIT Mode"
echo "============================================"
for bench in "${EXISTING_BENCHMARKS[@]}"; do
    echo "--- $bench (JIT) ---"
    output=$(run_benchmark_median "-jit" "$bench")
    ec=$?
    echo "$output"
    if [[ $ec -eq 2 ]]; then
        JIT_RESULTS+=("timeout")
    elif [[ $ec -ne 0 ]]; then
        JIT_RESULTS+=("FAILED")
    else
        time_line=$(echo "$output" | grep -i "Time:" | tail -1)
        JIT_RESULTS+=("$time_line")
    fi
    echo ""
done

# Step 4: LuaJIT benchmarks — parallel arrays of (name, value)
LUAJIT_NAMES=()
if command -v luajit &>/dev/null; then
    echo "============================================"
    echo "  LuaJIT Mode"
    echo "============================================"
    if [[ -d benchmarks/lua ]]; then
        for f in benchmarks/lua/*.lua; do
            name=$(basename "$f" .lua)
            LUAJIT_NAMES+=("$name")
            echo "--- $name (LuaJIT) ---"
            output=$(run_luajit_median "$f")
            ec=$?
            echo "$output"
            if [[ $ec -eq 2 ]]; then
                LUAJIT_RESULTS+=("timeout")
            elif [[ $ec -ne 0 ]]; then
                LUAJIT_RESULTS+=("FAILED")
            else
                time_line=$(echo "$output" | grep -i "Time:" | tail -1)
                LUAJIT_RESULTS+=("$time_line")
            fi
            echo ""
        done
    else
        echo "(benchmarks/lua/ directory not found, skipping)"
    fi
    echo ""
else
    echo ""
    echo "(luajit not in PATH, skipping LuaJIT comparison)"
    echo ""
fi

# Helper: look up LuaJIT result for a given benchmark name.
lookup_luajit() {
    local target="$1"
    local i=0
    while [[ $i -lt ${#LUAJIT_NAMES[@]} ]]; do
        if [[ "${LUAJIT_NAMES[$i]}" == "$target" ]]; then
            echo "${LUAJIT_RESULTS[$i]}"
            return 0
        fi
        i=$((i+1))
    done
    echo "N/A"
}

# Step 5: Summary
echo "============================================"
echo "  SUMMARY"
echo "============================================"
printf "%-25s | %-20s | %-20s | %-20s\n" "Benchmark" "VM" "JIT" "LuaJIT"
printf "%-25s-+-%-20s-+-%-20s-+-%-20s\n" "-------------------------" "--------------------" "--------------------" "--------------------"
i=0
while [[ $i -lt ${#EXISTING_BENCHMARKS[@]} ]]; do
    bench="${EXISTING_BENCHMARKS[$i]}"
    vm_r="${VM_RESULTS[$i]:-n/a}"
    jit_r="${JIT_RESULTS[$i]:-n/a}"
    luajit_r="$(lookup_luajit "$bench")"
    printf "%-25s | %-20s | %-20s | %-20s\n" "$bench" "$vm_r" "$jit_r" "$luajit_r"
    i=$((i+1))
done
echo ""
echo "============================================"
echo "  Done: $(date '+%Y-%m-%d %H:%M:%S')"
echo "============================================"

# --- JSON output ---
JSON_FILE="benchmarks/data/latest.json"
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
COMMIT=$(git rev-parse HEAD 2>/dev/null || echo "unknown")

# Build JSON using python3 (robust, handles escaping)
python3 -c "
import json, sys
data = {
    'timestamp': '$TIMESTAMP',
    'commit': '$COMMIT',
    'results': {}
}
# Read from environment/stdin
for line in sys.stdin:
    parts = line.strip().split('|')
    if len(parts) == 4:
        name, vm, jit, luajit = [p.strip() for p in parts]
        data['results'][name] = {'vm': vm, 'jit': jit, 'luajit': luajit}
json.dump(data, open('$JSON_FILE', 'w'), indent=2)

# Archive to history
import shutil
hist = f'benchmarks/data/history/{data[\"timestamp\"][:10]}.json'
shutil.copy('$JSON_FILE', hist)
" <<BENCH_DATA
$(i=0
while [[ $i -lt ${#EXISTING_BENCHMARKS[@]} ]]; do
    bench="${EXISTING_BENCHMARKS[$i]}"
    vm_r="${VM_RESULTS[$i]:-ERROR}"
    jit_r="${JIT_RESULTS[$i]:-ERROR}"
    luajit_r="$(lookup_luajit "$bench")"
    echo "${bench}|${vm_r}|${jit_r}|${luajit_r}"
    i=$((i+1))
done)
BENCH_DATA

echo ""
echo ">>> JSON results saved to $JSON_FILE"

# Cleanup
rm -f "$GSCRIPT_BIN"
