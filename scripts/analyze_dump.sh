#!/bin/bash
# analyze_dump.sh — Dump all context files ANALYZE needs in one shot.
# Reduces ~10 Read calls to 1 Bash call.
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "================================================================"
echo "=== ANALYZE CONTEXT DUMP ==="
echo "================================================================"
echo ""

FILES=(
    "$ROOT/opt/state.json"
    "$ROOT/opt/INDEX.md"
    "$ROOT/docs-internal/architecture/overview.md"
    "$ROOT/docs-internal/architecture/constraints.md"
    "$ROOT/docs-internal/lessons-learned.md"
    "$ROOT/docs-internal/known-issues.md"
    "$ROOT/benchmarks/data/latest.json"
    "$ROOT/benchmarks/data/baseline.json"
)

# Initiatives (skip template and README)
for f in "$ROOT"/opt/initiatives/*.md; do
    [ -f "$f" ] || continue
    bn=$(basename "$f")
    [[ "$bn" == "_template.md" || "$bn" == "README.md" ]] && continue
    FILES+=("$f")
done

# Knowledge base
for f in "$ROOT"/opt/knowledge/*.md; do
    [ -f "$f" ] || continue
    [[ "$(basename "$f")" == "README.md" ]] && continue
    FILES+=("$f")
done

# Current plan and reports (if exist)
[ -f "$ROOT/opt/current_plan.md" ] && FILES+=("$ROOT/opt/current_plan.md")
[ -f "$ROOT/opt/analyze_report.md" ] && FILES+=("$ROOT/opt/analyze_report.md")

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
echo "Total files: ${#FILES[@]}"
echo "================================================================"
