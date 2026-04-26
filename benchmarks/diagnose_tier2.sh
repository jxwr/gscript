#!/bin/bash
# Focused Tier 2 diagnostic runner.
#
# Runs suite benchmarks in VM mode, normal JIT mode, and with
# GSCRIPT_TIER2_NO_FILTER=1, preserving enough output to catch both
# performance changes and "faster but wrong" checksum/output changes.
#
# Usage:
#   bash benchmarks/diagnose_tier2.sh
#   bash benchmarks/diagnose_tier2.sh nbody table_field_access math_intensive

set -uo pipefail
cd "$(dirname "$0")/.."

GSCRIPT_BIN="/tmp/gscript_diag_tier2_$$"
RESULTS_DIR="/tmp/gscript_diag_tier2_results_$$"
TIMEOUT_SEC="${TIMEOUT_SEC:-60}"
mkdir -p "$RESULTS_DIR"

cleanup() {
    rm -f "$GSCRIPT_BIN"
    rm -rf "$RESULTS_DIR"
}
trap cleanup EXIT

run_with_timeout() {
    local timeout="$1"; shift
    if command -v gtimeout >/dev/null 2>&1; then
        gtimeout "$timeout" "$@" 2>&1
        return $?
    fi
    if command -v timeout >/dev/null 2>&1; then
        timeout "$timeout" "$@" 2>&1
        return $?
    fi
    perl -e '
        my $timeout = shift @ARGV;
        my $pid = fork();
        die "fork failed: $!" unless defined $pid;
        if ($pid == 0) {
            exec @ARGV;
            exit 127;
        }
        local $SIG{ALRM} = sub {
            kill "TERM", $pid;
            sleep 1;
            kill "KILL", $pid;
            exit 124;
        };
        alarm $timeout;
        waitpid($pid, 0);
        my $status = $?;
        alarm 0;
        exit($status >> 8);
    ' -- "$timeout" "$@" 2>&1
    return $?
}

extract_time() {
    local file="$1"
    local value
    value=$(grep "Time:" "$file" | tail -1 | sed 's/.*Time: //' | awk '{print $1}')
    if [[ -n "$value" ]]; then
        echo "$value"
    else
        echo "n/a"
    fi
}

extract_t2() {
    local file="$1"
    local entered
    entered=$(awk '/^  Tier 2 entered:/ {print $4}' "$file" | tail -1)
    echo "${entered:-0}"
}

extract_failed_count() {
    local file="$1"
    local failed
    failed=$(awk '/^  Tier 2 failed:/ {print $4}' "$file" | tail -1)
    echo "${failed:-0}"
}

extract_signal() {
    local file="$1"
    grep -Ei 'checksum|result|energy|stretch tree|long lived tree|Time:' "$file" \
        | grep -Ev '^  ' \
        | tail -5 \
        | tr '\n' ';' \
        | sed 's/;$/ /; s/|/\\|/g'
}

extract_failures() {
    local file="$1"
    awk '
        /^  Tier 2 failed:/ {in_failed=1; next}
        in_failed && /^  [^ ]/ {in_failed=0}
        in_failed && /^    / {
            sub(/^    /, "")
            print
        }
    ' "$file" | tr '\n' ';' | sed 's/;$/ /; s/|/\\|/g'
}

if ! go build -o "$GSCRIPT_BIN" ./cmd/gscript/ 2>&1; then
    echo "ERROR: build failed" >&2
    exit 1
fi

if [[ "$#" -gt 0 ]]; then
    BENCHMARKS="$*"
else
    BENCHMARKS="fib fib_recursive sieve mandelbrot ackermann matmul spectral_norm nbody fannkuch sort sum_primes mutual_recursion method_dispatch closure_bench string_bench binary_trees table_field_access table_array_access coroutine_bench fibonacci_iterative math_intensive object_creation"
fi

printf "| %-22s | %-9s | %-9s | %-10s | %-9s | %-45s | %-55s |\n" \
    "Benchmark" "Mode" "Time" "T2 entered" "T2 failed" "Signal" "Tier2 failures"
printf "| %-22s | %-9s | %-9s | %-10s | %-9s | %-45s | %-55s |\n" \
    "----------------------" "---------" "---------" "----------" "---------" "---------------------------------------------" "-------------------------------------------------------"

for bench in $BENCHMARKS; do
    file="benchmarks/suite/${bench}.gs"
    if [[ ! -f "$file" ]]; then
        printf "| %-22s | %-9s | %-9s | %-8s | %-45s | %-55s |\n" \
            "$bench" "missing" "n/a" "n/a" "missing suite file" ""
        continue
    fi

    for mode in vm default no-filter; do
        out="$RESULTS_DIR/${bench}_${mode}.out"
        case "$mode" in
            vm)
                run_with_timeout "$TIMEOUT_SEC" "$GSCRIPT_BIN" -vm "$file" >"$out" 2>&1
                ;;
            no-filter)
                GSCRIPT_TIER2_NO_FILTER=1 run_with_timeout "$TIMEOUT_SEC" "$GSCRIPT_BIN" -jit -jit-stats "$file" >"$out" 2>&1
                ;;
            default)
                run_with_timeout "$TIMEOUT_SEC" "$GSCRIPT_BIN" -jit -jit-stats "$file" >"$out" 2>&1
                ;;
        esac
        ec=$?
        if [[ $ec -eq 124 ]] || [[ $ec -eq 142 ]] || [[ $ec -eq 137 ]]; then
            time="TIMEOUT"
        elif [[ $ec -ne 0 ]]; then
            time="ERROR"
        else
            time=$(extract_time "$out")
        fi
        if [[ "$mode" == "vm" ]]; then
            entered="-"
            failed="-"
        else
            entered=$(extract_t2 "$out")
            failed=$(extract_failed_count "$out")
        fi
        signal=$(extract_signal "$out")
        failures=$(extract_failures "$out")
        printf "| %-22s | %-9s | %-9s | %-10s | %-9s | %-45s | %-55s |\n" \
            "$bench" "$mode" "$time" "$entered" "$failed" "${signal:- }" "${failures:- }"
    done
done
