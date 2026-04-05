#!/bin/bash
# budget_check.sh — Check optimization round budget from current_plan.md
# Stop hook: warns if approaching budget, blocks if exceeded.
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PLAN="$ROOT/.claude/current_plan.md"
STATE="$ROOT/.claude/state.json"

# Skip if no active plan
[ -f "$PLAN" ] || exit 0

# Extract budget from plan (look for "Max commits: N" and "Max files changed: N")
MAX_COMMITS=$(grep -oP 'Max commits:\s*\K\d+' "$PLAN" 2>/dev/null || echo 0)
MAX_FILES=$(grep -oP 'Max files changed:\s*\K\d+' "$PLAN" 2>/dev/null || echo 0)

# Skip if no budget defined
[ "$MAX_COMMITS" -eq 0 ] && [ "$MAX_FILES" -eq 0 ] && exit 0

# Count actual commits and files from state.json
CURRENT_COMMITS=0
CURRENT_FILES=0
if [ -f "$STATE" ]; then
    CURRENT_COMMITS=$(python3 -c "import json; d=json.load(open('$STATE')); print(d.get('plan_budget',{}).get('current_commits',0))" 2>/dev/null || echo 0)
    CURRENT_FILES=$(python3 -c "import json; d=json.load(open('$STATE')); print(d.get('plan_budget',{}).get('current_files',0))" 2>/dev/null || echo 0)
fi

# Also count uncommitted changed files as current scope
UNCOMMITTED_FILES=$(git -C "$ROOT" diff --name-only HEAD 2>/dev/null | wc -l | tr -d ' ')
TOTAL_FILES=$((CURRENT_FILES + UNCOMMITTED_FILES))

ISSUES=""

if [ "$MAX_COMMITS" -gt 0 ] && [ "$CURRENT_COMMITS" -gt "$MAX_COMMITS" ]; then
    ISSUES="${ISSUES}\n  OVER BUDGET: $CURRENT_COMMITS commits (max $MAX_COMMITS)"
fi

if [ "$MAX_FILES" -gt 0 ] && [ "$TOTAL_FILES" -gt "$MAX_FILES" ]; then
    ISSUES="${ISSUES}\n  OVER BUDGET: $TOTAL_FILES files changed (max $MAX_FILES)"
fi

if [ -n "$ISSUES" ]; then
    echo ""
    echo "=== Budget Check: EXCEEDED ==="
    echo -e "$ISSUES"
    echo ""
    echo "Return to ANALYZE phase and re-evaluate direction."
    exit 2
fi

# Warn at 80% of budget
if [ "$MAX_COMMITS" -gt 0 ]; then
    THRESHOLD=$(( MAX_COMMITS * 80 / 100 ))
    if [ "$CURRENT_COMMITS" -ge "$THRESHOLD" ]; then
        echo "Budget warning: $CURRENT_COMMITS/$MAX_COMMITS commits used"
    fi
fi

exit 0
