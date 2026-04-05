#!/bin/bash
# file_size_guard.sh — Check Go file sizes in modified files
# Stop hook: warns at 800 lines, blocks at 1000 lines
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

WARNINGS=""
BLOCKERS=""

# Check only modified Go files
for file in $(git -C "$ROOT" diff --name-only HEAD 2>/dev/null | grep '\.go$'); do
    filepath="$ROOT/$file"
    [ -f "$filepath" ] || continue
    lines=$(wc -l < "$filepath" | tr -d ' ')

    if [ "$lines" -gt 1000 ]; then
        BLOCKERS="${BLOCKERS}\n  BLOCK: $file ($lines lines > 1000 limit)"
    elif [ "$lines" -gt 800 ]; then
        WARNINGS="${WARNINGS}\n  WARN: $file ($lines lines — approaching 1000 limit, consider splitting)"
    fi
done

if [ -n "$BLOCKERS" ]; then
    echo ""
    echo "=== File Size Check: BLOCKED ==="
    echo -e "$BLOCKERS"
    [ -n "$WARNINGS" ] && echo -e "$WARNINGS"
    echo ""
    echo "Split files exceeding 1000 lines before continuing."
    exit 2
fi

if [ -n "$WARNINGS" ]; then
    echo ""
    echo "=== File Size Check: WARNING ==="
    echo -e "$WARNINGS"
    echo ""
fi

exit 0
