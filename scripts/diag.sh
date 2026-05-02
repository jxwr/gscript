#!/bin/bash
# scripts/diag.sh — Production-parity Tier 2 diagnostic dump.
#
# For each benchmark under benchmarks/{suite,extended,variants}/, runs the
# full production Tier 2 compile pipeline on every Tier-2-promotable proto
# and writes (nested under the originating subdirectory):
#
#   diag/<suite>/<bench>/<proto>.bin        — raw ARM64 code bytes
#   diag/<suite>/<bench>/<proto>.ir.txt     — post-pipeline IR + regalloc + intrinsics
#   diag/<suite>/<bench>/<proto>.asm.txt    — golang.org/x/arch ARM64 disasm
#   diag/<suite>/<bench>/stats.json         — per-proto insn count + histogram
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
#   bash scripts/diag.sh all                  — dump every .gs in suite/, extended/, variants/
#   bash scripts/diag.sh suite                — dump suite/ only
#   bash scripts/diag.sh extended             — dump extended/ only
#   bash scripts/diag.sh variants             — dump variants/ only
#   bash scripts/diag.sh <benchmark>          — dump a single benchmark.
#                                                Forms accepted:
#                                                  sieve, sieve.gs
#                                                  suite/sieve, suite/sieve.gs
#                                                  extended/json_table_walk
#                                                  variants/ack_nested_shifted
#                                                Bare basenames are searched in
#                                                suite/ → extended/ → variants/.
#
# Runtime: ~3 seconds per benchmark, ~2 minutes for the full all-suite dump.

set -uo pipefail
cd "$(dirname "$0")/.."

BENCHMARK=""
for arg in "$@"; do
    case "$arg" in
        all|suite|extended|variants) BENCHMARK="$arg" ;;
        *) BENCHMARK="$arg" ;;
    esac
done

if [ -z "$BENCHMARK" ]; then
    echo "Usage: $0 <benchmark|all|suite|extended|variants>" >&2
    exit 2
fi

DIAG_ROOT="diag"
mkdir -p "$DIAG_ROOT"

# Discover which top-level benchmark dirs actually exist (variants/ is optional).
SUITE_DIRS=()
for d in suite extended variants; do
    if [ -d "benchmarks/$d" ]; then
        SUITE_DIRS+=("$d")
    fi
done

# Resolve benchmark list. Each entry is "<suite>/<file>.gs", relative to
# benchmarks/. macOS bash 3.2 compatible (no mapfile, no associative arrays).
BENCHES=()
collect_dir() {
    local sub="$1"
    if [ ! -d "benchmarks/$sub" ]; then
        return
    fi
    local f
    for f in "benchmarks/$sub"/*.gs; do
        [ -f "$f" ] || continue
        BENCHES+=("$sub/$(basename "$f")")
    done
}

case "$BENCHMARK" in
    all)
        for d in "${SUITE_DIRS[@]}"; do
            collect_dir "$d"
        done
        # Wipe everything inside diag/ — both the new nested layout and any legacy
        # flat dirs from prior runs — so a renamed/removed source can't leave a
        # stale stats.json behind. summary.md is regenerated at the end.
        find "$DIAG_ROOT" -mindepth 1 -maxdepth 1 ! -name summary.md -exec rm -rf {} +
        ;;
    suite|extended|variants)
        if [ ! -d "benchmarks/$BENCHMARK" ]; then
            echo "No such suite: benchmarks/$BENCHMARK" >&2
            exit 2
        fi
        collect_dir "$BENCHMARK"
        rm -rf "$DIAG_ROOT/$BENCHMARK"
        ;;
    *)
        # Single benchmark. Accept: name | name.gs | suite/name | suite/name.gs.
        rel=""
        if [[ "$BENCHMARK" == */* ]]; then
            rel="$BENCHMARK"
            [[ "$rel" != *.gs ]] && rel="${rel}.gs"
            if [ ! -f "benchmarks/$rel" ]; then
                echo "No such benchmark: benchmarks/$rel" >&2
                exit 2
            fi
        else
            base="${BENCHMARK%.gs}"
            for d in "${SUITE_DIRS[@]}"; do
                if [ -f "benchmarks/$d/$base.gs" ]; then
                    rel="$d/$base.gs"
                    break
                fi
            done
            if [ -z "$rel" ]; then
                echo "No such benchmark in suite/, extended/, or variants/: $BENCHMARK" >&2
                exit 2
            fi
        fi
        BENCHES=("$rel")
        ;;
esac

echo "=== scripts/diag.sh ==="
echo "Benchmarks: ${#BENCHES[@]}"
echo

failed=()
for bench in "${BENCHES[@]}"; do
    sub="${bench%%/*}"               # suite | extended | variants
    file="${bench#*/}"               # foo.gs
    name="${file%.gs}"               # foo
    out_dir="$DIAG_ROOT/$sub/$name"
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
