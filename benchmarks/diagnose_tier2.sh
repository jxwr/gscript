#!/bin/bash
# Focused Tier 2 diagnostic runner.
#
# Runs selected suite benchmarks in normal JIT mode and with
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
    perl -e "alarm $timeout; exec @ARGV" -- "$@" 2>&1
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
    local compiled entered
    compiled=$(awk '/^  Tier 2 compiled:/ {print $4}' "$file" | tail -1)
    entered=$(awk '/^  Tier 2 entered:/ {print $4}' "$file" | tail -1)
    echo "${entered:-0}/${compiled:-0}"
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
    BENCHMARKS="nbody table_field_access math_intensive fannkuch matmul binary_trees"
fi

printf "| %-22s | %-9s | %-9s | %-8s | %-45s | %-55s |\n" \
    "Benchmark" "Mode" "Time" "T2" "Signal" "Tier2 failures"
printf "| %-22s | %-9s | %-9s | %-8s | %-45s | %-55s |\n" \
    "----------------------" "---------" "---------" "--------" "---------------------------------------------" "-------------------------------------------------------"

for bench in $BENCHMARKS; do
    file="benchmarks/suite/${bench}.gs"
    if [[ ! -f "$file" ]]; then
        printf "| %-22s | %-9s | %-9s | %-8s | %-45s | %-55s |\n" \
            "$bench" "missing" "n/a" "n/a" "missing suite file" ""
        continue
    fi

    for mode in default no-filter; do
        out="$RESULTS_DIR/${bench}_${mode}.out"
        if [[ "$mode" == "no-filter" ]]; then
            GSCRIPT_TIER2_NO_FILTER=1 run_with_timeout "$TIMEOUT_SEC" "$GSCRIPT_BIN" -jit -jit-stats "$file" >"$out" 2>&1
        else
            run_with_timeout "$TIMEOUT_SEC" "$GSCRIPT_BIN" -jit -jit-stats "$file" >"$out" 2>&1
        fi
        ec=$?
        if [[ $ec -eq 142 ]] || [[ $ec -eq 137 ]]; then
            time="TIMEOUT"
        elif [[ $ec -ne 0 ]]; then
            time="ERROR"
        else
            time=$(extract_time "$out")
        fi
        t2=$(extract_t2 "$out")
        signal=$(extract_signal "$out")
        failures=$(extract_failures "$out")
        printf "| %-22s | %-9s | %-9s | %-8s | %-45s | %-55s |\n" \
            "$bench" "$mode" "$time" "$t2" "${signal:- }" "${failures:- }"
    done
done
