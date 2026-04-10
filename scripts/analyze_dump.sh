#!/bin/bash
# analyze_dump.sh — Dump all context files ANALYZE needs in one shot.
# Reduces ~10 Read calls to 1 Bash call.
#
# KB filtering: files with "| Category: X |" in header are only loaded if X matches
# the last round's category (from state.json). Files without a Category tag always load.
# Initiative filtering: files with "Status: paused/blocked/abandoned" are skipped.
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

# Last category hint from previous_rounds (used to filter large KB files)
LAST_CATEGORY=$(python3 -c "
import json, sys
try:
    d = json.load(open('$ROOT/opt/state.json'))
    pr = d.get('previous_rounds', [])
    print(pr[-1].get('category', 'general') if pr else 'general')
except: print('general')
" 2>/dev/null || echo "general")

# Initiatives: skip paused/blocked/abandoned
for f in "$ROOT"/opt/initiatives/*.md; do
    [ -f "$f" ] || continue
    bn=$(basename "$f")
    [[ "$bn" == "_template.md" || "$bn" == "README.md" ]] && continue
    if grep -qE "Status: (paused|blocked|abandoned)" "$f" 2>/dev/null; then
        continue
    fi
    FILES+=("$f")
done

# Knowledge base: always load untagged or small files (<150 lines);
# for tagged files load only if category matches last round's category.
for f in "$ROOT"/opt/knowledge/*.md; do
    [ -f "$f" ] || continue
    [[ "$(basename "$f")" == "README.md" ]] && continue
    line_count=$(wc -l < "$f" | tr -d ' ')
    kb_cat=$(grep -m1 "| Category:" "$f" 2>/dev/null | sed 's/.*| Category: *\([^ |]*\).*/\1/')
    if [[ -z "$kb_cat" || "$line_count" -lt 150 || "$kb_cat" == "$LAST_CATEGORY" ]]; then
        FILES+=("$f")
    fi
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
echo "Skipped (category mismatch or initiative paused): use Read to load additional KB files if target requires them."
echo "================================================================"
