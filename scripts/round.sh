#!/bin/bash
# scripts/round.sh — Workflow v5 round prep.
#
# A v5 round is a single Claude session with 7 steps (see CLAUDE.md).
# This script runs the mechanical prep (Steps 1–2) and gates Step 5 (Act)
# on Step 3 (Direction) having produced a fresh round card.
#
# After this script finishes, the AI:
#   - Step 0 (Recap) reads rounds/*.yaml + program/ledger.yaml
#   - Step 3 (Direction) writes rounds/NNN.yaml
#   - Step 5 (Act) is blocked by this script re-run if the card is stale
#
# Usage:
#   bash scripts/round.sh              — prep a full round
#   bash scripts/round.sh --quick      — skip diag (use stale diag/)
#   bash scripts/round.sh --no-bench   — skip benchmark re-run
#   bash scripts/round.sh --gate       — check direction gate only (Step 4)
#
# Exit codes:
#   0  — prep complete / gate passed
#   1  — kb_check FAILED
#   2  — diag FAILED
#   3  — benchmarks/latest.json older than code
#   4  — direction gate: round card missing or older than diag

set -uo pipefail
cd "$(dirname "$0")/.."

QUICK=false
NO_BENCH=false
GATE_ONLY=false
for arg in "$@"; do
    case "$arg" in
        --quick) QUICK=true ;;
        --no-bench) NO_BENCH=true ;;
        --gate) GATE_ONLY=true ;;
    esac
done

# --- direction gate (Step 4 of v5) ---
# The gate fails if the most recent rounds/NNN.yaml is older than
# diag/summary.md. This catches the v4 pathology where direction.md
# silently went missing (R6 → no R7 for 3 days).

check_direction_gate() {
    if [ ! -d rounds ]; then
        echo "ERROR: rounds/ directory missing. v5 bootstrap incomplete." >&2
        return 4
    fi
    latest_card=$(ls -1 rounds/R[0-9][0-9][0-9].yaml 2>/dev/null | sort | tail -1)
    if [ -z "$latest_card" ]; then
        echo "ERROR: no rounds/R*.yaml cards found. Run Step 3 (Direction)." >&2
        return 4
    fi
    if [ ! -f diag/summary.md ]; then
        echo "NOTE: diag/summary.md missing — run Steps 1–2 first." >&2
        return 0
    fi
    card_age=$(stat -f %m "$latest_card")
    diag_age=$(stat -f %m diag/summary.md)
    if [ "$card_age" -lt "$diag_age" ]; then
        echo "ERROR: latest round card ($latest_card) is older than diag/summary.md." >&2
        echo "       Run Step 3 (Direction) — write a new rounds/NNN.yaml." >&2
        return 4
    fi
    echo "Direction gate: $latest_card is fresh (newer than diag/summary.md)."
    return 0
}

if $GATE_ONLY; then
    check_direction_gate
    exit $?
fi

echo "=== Workflow v5 — round prep ==="
echo

echo "[1/3] Regenerating L1 symbol index..."
if ! bash scripts/kb_index.sh; then
    echo "ERROR: kb_index failed" >&2
    exit 2
fi
echo

if ! $NO_BENCH; then
    latest_age=$(stat -f %m benchmarks/data/latest.json 2>/dev/null || echo 0)
    src_age=0
    if [ -d internal/methodjit ]; then
        src_age=$(find internal/methodjit -name '*.go' -not -name '*_test.go' \
            -exec stat -f %m {} \; 2>/dev/null | sort -nr | head -1 || echo 0)
    fi
    if [ "$latest_age" -lt "$src_age" ]; then
        echo "[!] benchmarks/data/latest.json older than source."
        echo "    Run: bash benchmarks/run_all.sh --runs=3"
        exit 3
    fi
fi

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

  Step 0  Recap
          Read rounds/R*.yaml (last 8) + program/ledger.yaml + last 5
          revert autopsies. Identify class-repetition patterns.

  Step 3  Direction
          Read: program/ledger.yaml, all kb/modules/*.md, diag/summary.md,
          3-5 diag/<bench>/ dirs, last 20 rounds/*.yaml.
          Write: rounds/NNN.yaml (schema: rounds/TEMPLATE.yaml).
          Mandatory: class_gate.ledger_consulted = true.

  Step 4  (Wave 3) Pre-flight evidence. Optional in Wave 1.
          Run `bash scripts/round.sh --gate` to confirm card freshness.

  Step 5  Act — TDD, bounded by round card scope.

  Step 6  Verify — median-of-N bench + diag diff. Revert on failure.

  Step 7  Close — fill round card outcome + revert autopsy if applicable.
          Update program/ledger.yaml. Commit: "round N [type]: <one-liner>".

Hard rules live in CLAUDE.md. 3-hour round budget.
Do not write docs-internal/round-direction.md — deprecated in v5.

EOF
