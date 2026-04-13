#!/bin/bash
# scripts/round.sh — Workflow v4 round prep.
#
# This is NOT a multi-phase orchestrator. A v4 round is a single Claude
# session. This script runs the mechanical prep work (Steps 1–2 of the
# 6-step round) so the AI walks into a session with:
#
#   - Up-to-date diagnostic artifacts under diag/
#   - Up-to-date L1 symbol index under kb/index/
#   - A clean KB (kb_check.sh passes)
#
# After this script finishes, the AI reads diag/summary.md and
# kb/modules/architecture.md and starts Step 3 (three-level direction
# check) in the same session. No separate Claude invocation for Steps 4/5/6.
#
# Usage:
#   bash scripts/round.sh              — prep a full round
#   bash scripts/round.sh --quick      — skip diag (use stale diag/)
#   bash scripts/round.sh --no-bench   — skip benchmark re-run (use
#                                         last latest.json; still dumps
#                                         current IR/ASM via scripts/diag.sh)
#
# Exit codes:
#   0  — prep complete, session ready
#   1  — kb_check FAILED (stale card or missing file); fix kb/ first
#   2  — diag FAILED (build or pipeline error); fix code before routing
#   3  — benchmarks/latest.json is older than code (run benchmarks/run_all.sh)

set -uo pipefail
cd "$(dirname "$0")/.."

QUICK=false
NO_BENCH=false
for arg in "$@"; do
    case "$arg" in
        --quick) QUICK=true ;;
        --no-bench) NO_BENCH=true ;;
    esac
done

echo "=== Workflow v4 — round prep ==="
echo

# Step 1a: regenerate L1 index.
echo "[1/3] Regenerating L1 symbol index..."
if ! bash scripts/kb_index.sh; then
    echo "ERROR: kb_index failed" >&2
    exit 2
fi
echo

# Step 1b: refresh benchmark data.
if ! $NO_BENCH; then
    latest_age=$(stat -f %m benchmarks/data/latest.json 2>/dev/null || echo 0)
    src_age=0
    if [ -d internal/methodjit ]; then
        src_age=$(find internal/methodjit -name '*.go' -not -name '*_test.go' \
            -exec stat -f %m {} \; 2>/dev/null | sort -nr | head -1 || echo 0)
    fi
    if [ "$latest_age" -lt "$src_age" ]; then
        echo "[!] benchmarks/data/latest.json is older than source files."
        echo "    Run: bash benchmarks/run_all.sh --runs=3"
        echo "    Then re-run this script, or pass --no-bench to skip."
        exit 3
    fi
fi

# Step 1c: regenerate diag/.
if $QUICK; then
    echo "[2/3] (skipped, --quick) using existing diag/"
else
    echo "[2/3] Regenerating diag/ for all benchmarks..."
    if ! bash scripts/diag.sh all; then
        echo "ERROR: diag.sh failed" >&2
        exit 2
    fi
fi
echo

# Step 2: KB health check.
echo "[3/3] Running kb_check..."
if ! bash scripts/kb_check.sh; then
    echo
    echo "ERROR: kb_check failed. Fix stale cards in kb/ before proceeding." >&2
    exit 1
fi
echo

cat <<'EOF'

=== Round prep complete ===

Next (inside this same Claude session):

  Step 3  Three-level direction check
          Read diag/summary.md + kb/architecture.md + the kb/modules/*
          cards matching the top drifter. Answer Q1 (global architecture),
          Q2 (module boundary), Q3 (local optimization) in priority order.
          Only Q3 may proceed without user discussion.

          Output: docs-internal/round-direction.md (overwritten each round)

  Step 4  Act
          TDD, bounded by direction.md scope. Commit per task.

  Step 5  Verify
          Re-run scripts/diag.sh on affected benchmarks. Diff against the
          pre-round snapshot. Revert on failure.

  Step 6  KB update
          Edit any card whose semantics changed. Separate commit.

CLAUDE.md holds the 20 hard rules. 3-hour round budget. Do not skip
Steps 3 or 5. Do not create opt/state.json or any v3-style bookkeeping —
v4 has no persistent per-round state.

EOF
