#!/bin/bash
# archive_plan.sh — Archive current plan after completion
# Usage: bash .claude/hooks/archive_plan.sh

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PLAN="$ROOT/opt/current_plan.md"

if [ ! -f "$PLAN" ]; then
    echo "No current_plan.md to archive."
    exit 0
fi

# Extract cycle ID from the plan
CYCLE_ID=$(grep -oP 'Cycle ID: \K.*' "$PLAN" 2>/dev/null || echo "$(date +%Y-%m-%d)-unnamed")
CYCLE_ID=$(echo "$CYCLE_ID" | tr ' ' '-' | tr -cd 'a-zA-Z0-9-')

DEST="$ROOT/opt/plans/${CYCLE_ID}.md"
mv "$PLAN" "$DEST"
echo "Plan archived to: $DEST"
