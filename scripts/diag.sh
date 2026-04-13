#!/bin/bash
# scripts/diag.sh — Production-parity Tier 2 diagnostic dump.
#
# For each benchmark under benchmarks/suite/, runs the full production
# Tier 2 compile pipeline on every Tier-2-promotable proto and writes:
#
#   diag/<bench>/<proto>.bin        — raw ARM64 code bytes
#   diag/<bench>/<proto>.ir.txt     — post-pipeline IR + regalloc + intrinsics
#   diag/<bench>/<proto>.asm.txt    — otool -tV disasm of the .bin
#   diag/<bench>/stats.json         — per-proto insn count + histogram
#
# Plus an aggregate diag/summary.md with top drifters vs reference.json.
#
# The Go side (internal/methodjit/diag_dump_test.go) calls
# TieringManager.CompileForDiagnostics, which shares compileTier2Pipeline
# with the production path — enforced by TestDiag_ProductionParity_*.
# This means every byte shown here is byte-for-byte what production Tier 2
# would install at runtime. Rule 5 of CLAUDE.md is load-bearing on this.
#
# Usage:
#   bash scripts/diag.sh all                   — dump every .gs in suite/
#   bash scripts/diag.sh <benchmark>           — dump a single benchmark
#                                                (e.g. sieve or sieve.gs)
#   bash scripts/diag.sh --no-asm all          — skip otool disasm step
#
# Runtime: ~3 seconds per benchmark, ~60 seconds for the full suite.

set -uo pipefail
cd "$(dirname "$0")/.."

BENCHMARK=""
for arg in "$@"; do
    case "$arg" in
        all) BENCHMARK="all" ;;
        *) BENCHMARK="$arg" ;;
    esac
done

if [ -z "$BENCHMARK" ]; then
    echo "Usage: $0 <benchmark|all>" >&2
    exit 2
fi

DIAG_ROOT="diag"
mkdir -p "$DIAG_ROOT"

# Resolve benchmark list (macOS bash 3.2 compatible — no mapfile).
BENCHES=()
if [ "$BENCHMARK" = "all" ]; then
    for f in benchmarks/suite/*.gs; do
        BENCHES+=("$(basename "$f")")
    done
else
    if [[ "$BENCHMARK" != *.gs ]]; then
        BENCHMARK="$BENCHMARK.gs"
    fi
    if [ ! -f "benchmarks/suite/$BENCHMARK" ]; then
        echo "No such benchmark: benchmarks/suite/$BENCHMARK" >&2
        exit 2
    fi
    BENCHES=("$BENCHMARK")
fi

echo "=== scripts/diag.sh ==="
echo "Benchmarks: ${BENCHES[*]}"
echo

failed=()
for bench in "${BENCHES[@]}"; do
    name="${bench%.gs}"
    out_dir="$DIAG_ROOT/$name"
    rm -rf "$out_dir"
    mkdir -p "$out_dir"

    echo "--- $bench ---"
    if ! DIAG_BENCH="$bench" DIAG_OUT="$(pwd)/$out_dir" \
         go test ./internal/methodjit/ -run '^TestDiagDump$' -count=1 >"$out_dir/gotest.log" 2>&1; then
        echo "  FAIL — see $out_dir/gotest.log"
        failed+=("$bench")
        continue
    fi
    # .asm.txt files are written by the Go harness via
    # golang.org/x/arch/arm64/arm64asm — self-contained, no external
    # disassembler required.

    # Summary line.
    total_insns=$(python3 -c "
import json, sys
d = json.load(open('$out_dir/stats.json'))
total = sum(p.get('insn_count', 0) for p in d['protos'])
protos = len(d['protos'])
promoted = sum(1 for p in d['protos'] if not p.get('skip_reason'))
print(f'  {promoted}/{protos} protos promoted, {total} total insns')
")
    echo "$total_insns"
done

if [ ${#failed[@]} -gt 0 ]; then
    echo
    echo "FAILED: ${failed[*]}"
    exit 1
fi

echo
echo "=== Writing diag/summary.md ==="
python3 scripts/diag_summary.py "$DIAG_ROOT" >"$DIAG_ROOT/summary.md" || {
    echo "diag_summary.py failed (non-fatal)"
}
echo "Done. See $DIAG_ROOT/"
