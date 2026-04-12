#!/bin/bash
# Witness script for `git bisect run`.
# Exit 0 = good (object_creation <= ref+5%).
# Exit 1 = bad (>5% slower). Exit 125 = skip (build failure).
set -e
cd "$(git rev-parse --show-toplevel)"
go build -o /tmp/gscript_bisect ./cmd/gscript/ 2>/dev/null || exit 125

THRESHOLD=0.802  # 0.764 * 1.05

run_once() {
    /tmp/gscript_bisect -jit benchmarks/suite/object_creation.gs 2>/dev/null | awk '/^Time:/ { gsub("s",""); print $2; exit }'
}

T1=$(run_once)
T2=$(run_once)
T3=$(run_once)
MEDIAN=$(printf '%s\n' "$T1" "$T2" "$T3" | sort -n | sed -n '2p')

echo "bisect-witness: median=$MEDIAN threshold=$THRESHOLD"

# awk exits 0 if median > threshold (bad), 1 otherwise
if awk -v m="$MEDIAN" -v t="$THRESHOLD" 'BEGIN{exit !(m > t)}'; then
    exit 1  # bad — regression present
else
    exit 0  # good — no regression
fi
