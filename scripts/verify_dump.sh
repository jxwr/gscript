#!/bin/bash
# verify_dump.sh — Dump all context files VERIFY needs in one shot.
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "================================================================"
echo "=== VERIFY CONTEXT DUMP ==="
echo "================================================================"
echo ""

FILES=(
    "$ROOT/opt/current_plan.md"
    "$ROOT/benchmarks/data/baseline.json"
    "$ROOT/opt/state.json"
    "$ROOT/opt/INDEX.md"
    "$ROOT/opt/workflow_log.jsonl"
    "$ROOT/docs-internal/architecture/overview.md"
    "$ROOT/docs-internal/architecture/constraints.md"
    "$ROOT/docs/index.html"
)

for f in "${FILES[@]}"; do
    if [ -f "$f" ]; then
        rel="${f#$ROOT/}"
        lines=$(wc -l < "$f" | tr -d ' ')
        echo "──── $rel ($lines lines) ────"
        cat "$f"
        echo ""
    fi
done

echo "================================================================"
echo "=== Git diff for this round ==="
echo "================================================================"
git -C "$ROOT" diff --stat HEAD~3 2>/dev/null | tail -10
echo ""
echo "================================================================"
echo "Total files: ${#FILES[@]}"
echo "================================================================"
