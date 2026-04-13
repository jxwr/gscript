#!/bin/bash
# verify_dump.sh — Dump all context files VERIFY needs in one shot.
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "================================================================"
echo "=== VERIFY CONTEXT DUMP ==="
echo "================================================================"
echo ""

# state.json is rendered with previous_rounds capped to last 10 entries
# (R30 review: the tail is already summarized in INDEX.md; full history was
# ~50% of the 259-line dump and ANALYZE/VERIFY cache cost). INDEX.md is
# authoritative for older rounds.
render_state_trimmed() {
    python3 - "$ROOT/opt/state.json" <<'PY'
import json, sys
p = sys.argv[1]
with open(p) as f:
    data = json.load(f)
pr = data.get('previous_rounds', [])
if len(pr) > 10:
    data['previous_rounds'] = pr[-10:]
    data['_previous_rounds_truncated'] = f"kept last 10 of {len(pr)} — see opt/INDEX.md for full history"
print(json.dumps(data, indent=2))
PY
}

FILES=(
    "$ROOT/opt/current_plan.md"
    "$ROOT/benchmarks/data/baseline.json"
    "$ROOT/opt/INDEX.md"
    "$ROOT/opt/workflow_log.jsonl"
    "$ROOT/docs-internal/architecture/constraints.md"
)
# docs/index.html (21KB, step 2i only) + overview.md (8KB, step 2h conditional):
# read on-demand to keep dump under the 10K-token Read ceiling. R29 token
# reflection: VERIFY spent ~1-2M tokens re-reading the oversized dump.

# Render state.json first (trimmed)
if [ -f "$ROOT/opt/state.json" ]; then
    echo "──── opt/state.json (trimmed: previous_rounds last 10) ────"
    render_state_trimmed
    echo ""
fi

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
echo "Total files: $((${#FILES[@]} + 1))"
echo "================================================================"
