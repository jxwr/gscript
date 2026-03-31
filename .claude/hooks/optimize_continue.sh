#!/bin/bash
# optimize_continue.sh — Auto-continue optimization loop
# Stop hook: detects progress via git commits, continues if optimization goals not met.
# Exit 0 = allow stop, Exit 2 = force agent to continue optimizing.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
STATE_FILE="/tmp/gscript_optimize_state"

# Current HEAD commit
CURRENT_HEAD=$(cd "$ROOT" && git rev-parse HEAD 2>/dev/null || echo "unknown")

# Read previous state
PREV_HEAD=""
NO_PROGRESS_COUNT=0
if [ -f "$STATE_FILE" ]; then
    PREV_HEAD=$(head -1 "$STATE_FILE" 2>/dev/null)
    NO_PROGRESS_COUNT=$(tail -1 "$STATE_FILE" 2>/dev/null)
    NO_PROGRESS_COUNT=${NO_PROGRESS_COUNT:-0}
fi

# Detect progress: did HEAD move since last stop?
if [ "$CURRENT_HEAD" = "$PREV_HEAD" ]; then
    NO_PROGRESS_COUNT=$((NO_PROGRESS_COUNT + 1))
else
    NO_PROGRESS_COUNT=0
fi

# Save state
echo "${CURRENT_HEAD}" > "$STATE_FILE"
echo "${NO_PROGRESS_COUNT}" >> "$STATE_FILE"

# Allow stop after 3 consecutive stops without new commits
if [ "$NO_PROGRESS_COUNT" -ge 3 ]; then
    echo ""
    echo "=== Optimization loop: 3 consecutive stops with no new commits. Pausing. ==="
    rm -f "$STATE_FILE"
    exit 0
fi

# Read known issues for context
KNOWN_ISSUES=""
if [ -f "$ROOT/docs-internal/known-issues.md" ]; then
    KNOWN_ISSUES=$(sed -n '/^## Current/,/^## Historical/p' "$ROOT/docs-internal/known-issues.md" | head -30)
fi

echo ""
echo "=== Optimization loop continuing (stop #${NO_PROGRESS_COUNT} without new commit) ==="
echo ""
echo "检查当前状态，选择最优的优化路径，按计划推进，直到测试和21个性能测试全部通过且超出上一阶段的性能，直到最终打败luajit。"
echo ""

if [ -n "$KNOWN_ISSUES" ]; then
    echo "Current known issues:"
    echo "$KNOWN_ISSUES"
    echo ""
fi

echo "Workflow:"
echo "1. Run tests: go test ./internal/methodjit/... ./internal/vm/..."
echo "2. Run benchmarks: bash benchmarks/run_all.sh"
echo "3. Pick highest-impact optimization from known issues"
echo "4. Implement, verify correctness, commit"
echo "5. Re-benchmark to confirm improvement"
echo ""
exit 2
