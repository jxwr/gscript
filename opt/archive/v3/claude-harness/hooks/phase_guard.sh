#!/bin/bash
# phase_guard.sh — PreToolUse hook: block edits during wrong optimization phases.
# Reads opt/state.json → `cycle`. No cycle = free mode.
# Valid cycles: ANALYZE, IMPLEMENT, VERIFY, REVIEW.
# Exit 0 = allow, Exit 2 = block with message.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
STATE="$ROOT/opt/state.json"

CYCLE=""
if [ -f "$STATE" ]; then
    CYCLE=$(python3 -c "import json; d=json.load(open('$STATE')); print(d.get('cycle',''))" 2>/dev/null)
fi

# No active cycle = free mode, allow everything
[ -z "$CYCLE" ] && exit 0

# Read the file path being edited (from stdin JSON)
INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    tool_input = data.get('tool_input', {})
    print(tool_input.get('file_path', ''))
except Exception:
    print('')
" 2>/dev/null)

[ -z "$FILE_PATH" ] && exit 0

REL_PATH=$(echo "$FILE_PATH" | sed "s|^$ROOT/||")

case "$CYCLE" in
    ANALYZE|REVIEW)
        # Read-only phases — allow opt/ and docs-internal/ writes only
        if [[ "$REL_PATH" == opt/* ]] || [[ "$REL_PATH" == docs-internal/* ]] || [[ "$REL_PATH" == .claude/* ]]; then
            exit 0
        fi
        echo "BLOCKED: cycle=$CYCLE — read-only phase. Write to opt/ or docs-internal/." >&2
        exit 2
        ;;
    IMPLEMENT)
        PLAN="$ROOT/opt/current_plan.md"
        if [ -f "$PLAN" ]; then
            # Always allow opt/, docs-internal/, and test files
            if [[ "$REL_PATH" == opt/* ]] || [[ "$REL_PATH" == docs-internal/* ]] || [[ "$REL_PATH" == *_test.go ]]; then
                exit 0
            fi
            # Allow if basename mentioned in plan
            BASENAME=$(basename "$REL_PATH")
            if grep -q "$BASENAME" "$PLAN" 2>/dev/null; then
                exit 0
            fi
            echo "WARNING: '$REL_PATH' is not in current_plan.md. Scope creep?" >&2
        fi
        exit 0
        ;;
    VERIFY)
        # Implementation frozen — allow opt/, docs-internal/, test files, .claude/
        if [[ "$REL_PATH" == opt/* ]] || [[ "$REL_PATH" == docs-internal/* ]] || [[ "$REL_PATH" == *_test.go ]] || [[ "$REL_PATH" == .claude/* ]]; then
            exit 0
        fi
        echo "BLOCKED: cycle=VERIFY — implementation frozen. Only test fixes allowed." >&2
        exit 2
        ;;
    *)
        exit 0
        ;;
esac
