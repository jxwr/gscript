#!/bin/bash
# arch_check.sh — Quick mechanical scan of code architecture health.
# Called by ANALYZE Step 0. Output is plain text, piped to stdout.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
JIT="$ROOT/internal/methodjit"

echo "=== File Sizes (top 15, >800 flagged) ==="
find "$JIT" -name "*.go" ! -name "*_test.go" -exec wc -l {} + 2>/dev/null \
    | sort -rn | head -16 | while read -r lines file; do
    [ "$file" = "total" ] && continue
    flag=""
    [ "$lines" -gt 800 ] && flag=" ⚠ SPLIT"
    [ "$lines" -gt 1000 ] && flag=" 🚨 OVER LIMIT"
    printf "%6d  %s%s\n" "$lines" "$(basename "$file")" "$flag"
done

echo ""
echo "=== Pass Pipeline Order (from tiering_manager.go) ==="
grep -nE "Pass|Compile|Emit|Alloc|LICM|Inline|Range|Intrinsic" "$JIT/tiering_manager.go" 2>/dev/null \
    | grep -v "^.*://" | head -20

echo ""
echo "=== Technical Debt Markers ==="
count=$(grep -rn "TODO\|HACK\|FIXME\|workaround\|temporary" "$JIT"/*.go 2>/dev/null | wc -l | tr -d ' ')
echo "Total TODO/HACK/FIXME/workaround: $count"
if [ "$count" -gt 0 ]; then
    grep -rn "TODO\|HACK\|FIXME\|workaround\|temporary" "$JIT"/*.go 2>/dev/null | head -10
fi

echo ""
echo "=== Test Coverage Gaps (source files without _test.go) ==="
for f in "$JIT"/*.go; do
    [[ "$f" == *_test.go ]] && continue
    [[ "$(basename "$f")" == "doc.go" ]] && continue
    test_f="${f%.go}_test.go"
    [ ! -f "$test_f" ] && echo "  MISSING: $(basename "$f")"
done

echo ""
echo "=== Module Size Summary ==="
total=$(find "$JIT" -name "*.go" ! -name "*_test.go" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')
test_total=$(find "$JIT" -name "*_test.go" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')
file_count=$(find "$JIT" -name "*.go" ! -name "*_test.go" 2>/dev/null | wc -l | tr -d ' ')
echo "Source: ${file_count} files, ${total} lines"
echo "Tests: ${test_total} lines"
echo "Test ratio: $(python3 -c "print(f'{${test_total}/${total}*100:.0f}%' if $total > 0 else 'N/A')" 2>/dev/null || echo "?")"
