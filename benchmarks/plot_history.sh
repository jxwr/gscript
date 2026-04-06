#!/bin/bash
# plot_history.sh — ASCII trajectory of JIT wall-time per benchmark across rounds.
# Reads benchmarks/data/history/*.json and emits a compact trend table.
#
# Usage:
#   bash benchmarks/plot_history.sh                # all benchmarks
#   bash benchmarks/plot_history.sh fib matmul     # selected benchmarks

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HIST_DIR="$ROOT/benchmarks/data/history"
[ -d "$HIST_DIR" ] || { echo "No history dir: $HIST_DIR"; exit 0; }

# Collect sorted history files (oldest first)
FILES=()
while IFS= read -r f; do FILES+=("$f"); done < <(ls "$HIST_DIR"/*.json 2>/dev/null | sort)
if [ "${#FILES[@]}" -eq 0 ]; then
    echo "No history snapshots yet."
    exit 0
fi

# Extract benchmark names from latest snapshot
LAST_IDX=$((${#FILES[@]} - 1))
LATEST_FILE="${FILES[$LAST_IDX]}"
BENCHMARKS=$(jq -r '.results | keys[]' "$LATEST_FILE" 2>/dev/null)

# Optional filter
if [ $# -gt 0 ]; then
    FILTER="$*"
else
    FILTER=""
fi

printf "%-22s " "Benchmark"
for f in "${FILES[@]}"; do
    label=$(basename "$f" .json)
    # Shorten date labels (2026-04-05 -> 04-05)
    short="${label#*-}"
    printf "%9s " "$short"
done
printf "%9s\n" "LuaJIT"

printf "%-22s " "---------"
for f in "${FILES[@]}"; do printf "%9s " "----"; done
printf "%9s\n" "----"

for bench in $BENCHMARKS; do
    # Apply filter if set
    if [ -n "$FILTER" ]; then
        match=false
        for pat in $FILTER; do
            [[ "$bench" == *"$pat"* ]] && match=true
        done
        $match || continue
    fi

    printf "%-22s " "$bench"
    prev=""
    for f in "${FILES[@]}"; do
        # Extract JIT time: "Time: 1.403s" → 1.403
        t=$(jq -r ".results.\"$bench\".jit // \"\"" "$f" 2>/dev/null | grep -oE '[0-9]+\.[0-9]+' | head -1 || true)
        if [ -z "$t" ]; then
            printf "%9s " "—"
            continue
        fi
        # Delta arrow vs previous
        arrow=""
        if [ -n "$prev" ]; then
            delta=$(awk -v a="$t" -v b="$prev" 'BEGIN{d=(a-b)/b*100; printf "%.0f", d}')
            if [ "$delta" -le -5 ]; then arrow="↓"       # improved ≥5%
            elif [ "$delta" -ge 5 ]; then arrow="↑"      # regressed ≥5%
            else arrow="·"; fi
        fi
        printf "%7ss%s " "$t" "$arrow"
        prev="$t"
    done
    # LuaJIT from latest
    lj=$(jq -r ".results.\"$bench\".luajit // \"\"" "$LATEST_FILE" 2>/dev/null | grep -oE '[0-9]+\.[0-9]+' | head -1 || true)
    if [ -z "$lj" ]; then
        printf "%9s\n" "N/A"
    else
        printf "%8ss\n" "$lj"
    fi
done

echo ""
echo "Legend: ↓ improved ≥5%  ↑ regressed ≥5%  · flat"
echo "Files:  ${#FILES[@]} snapshots in $HIST_DIR"
