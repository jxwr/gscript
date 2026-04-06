#!/bin/bash
# phase_guard.sh — PreToolUse hook: block edits during wrong optimization phases
# Triggered on Edit and Write tools.
# Exit 0 = allow, Exit 2 = block with message.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
STATE="$ROOT/opt/state.json"

# Read cycle phase from state.json
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
    # Edit tool: tool_input.file_path
    # Write tool: tool_input.file_path
    tool_input = data.get('tool_input', {})
    print(tool_input.get('file_path', ''))
except:
    print('')
" 2>/dev/null)

[ -z "$FILE_PATH" ] && exit 0

# Make relative path
REL_PATH=$(echo "$FILE_PATH" | sed "s|^$ROOT/||")

case "$CYCLE" in
    MEASURE|ANALYZE)
        # Only allow editing opt/ state files and test files
        if [[ "$REL_PATH" == opt/* ]]; then
            exit 0
        fi
        echo "BLOCKED: cycle=$CYCLE — read-only phase. Run MEASURE/ANALYZE first." >&2
        echo "If you need to write analysis output, use opt/ directory." >&2
        exit 2
        ;;
    PLAN)
        # Allow opt/ directory (plan files, state)
        if [[ "$REL_PATH" == opt/* ]]; then
            exit 0
        fi
        echo "BLOCKED: cycle=$PLAN — only opt/ files allowed during planning." >&2
        echo "Write your plan in opt/current_plan.md, then wait for user approval." >&2
        exit 2
        ;;
    IMPLEMENT)
        # Check if file is in the plan's scope (read plan for allowed files)
        PLAN="$ROOT/opt/current_plan.md"
        if [ -f "$PLAN" ]; then
            # Allow opt/ and test files always
            if [[ "$REL_PATH" == opt/* ]] || [[ "$REL_PATH" == *_test.go ]]; then
                exit 0
            fi
            # Extract file references from plan
            # Plan mentions files like `foo.go`, `internal/methodjit/foo.go`
            BASENAME=$(basename "$REL_PATH")
            if grep -q "$BASENAME" "$PLAN" 2>/dev/null; then
                exit 0
            fi
            # File not mentioned in plan — warn but allow (scope creep detection)
            echo "WARNING: '$REL_PATH' is not in current_plan.md. Scope creep?" >&2
            exit 0
        fi
        exit 0
        ;;
    VERIFY|DOCUMENT)
        # Allow opt/ files, test files
        if [[ "$REL_PATH" == opt/* ]] || [[ "$REL_PATH" == *_test.go ]]; then
            exit 0
        fi
        echo "BLOCKED: cycle=$CYCLE — implementation is frozen during VERIFY/DOCUMENT." >&2
        echo "Fix issues by returning to PLAN phase." >&2
        exit 2
        ;;
    *)
        exit 0
        ;;
esac
