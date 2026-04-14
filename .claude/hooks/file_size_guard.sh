#!/bin/bash
# file_size_guard.sh — Check Go file sizes in modified files.
#
# Default rule (CLAUDE.md #13): no Go file exceeds 1000 lines; warn at 800.
# Pre-existing over-limit files are grandfathered via
# .claude/hooks/file_size_exempt.txt with a per-file ceiling — they
# may be edited freely as long as they stay at or below that ceiling.
# Growth past the ceiling is a hard block.
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
EXEMPT_FILE="$ROOT/.claude/hooks/file_size_exempt.txt"

get_exempt_ceiling() {
    local path="$1"
    [ -f "$EXEMPT_FILE" ] || { echo 0; return; }
    awk -v p="$path" '
        /^[[:space:]]*#/ { next }
        /^[[:space:]]*$/ { next }
        $1 == p { print $2; found=1; exit }
        END { if (!found) print 0 }
    ' "$EXEMPT_FILE"
}

WARNINGS=""
BLOCKERS=""

for file in $(git -C "$ROOT" diff --name-only HEAD 2>/dev/null | grep '\.go$'); do
    filepath="$ROOT/$file"
    [ -f "$filepath" ] || continue
    lines=$(wc -l < "$filepath" | tr -d ' ')

    ceiling=$(get_exempt_ceiling "$file")

    if [ "$ceiling" -gt 0 ]; then
        # Grandfathered file: block only if it grew past its ceiling.
        if [ "$lines" -gt "$ceiling" ]; then
            BLOCKERS="${BLOCKERS}\n  BLOCK: $file ($lines lines > grandfathered ceiling $ceiling)"
        fi
        continue
    fi

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
    echo "Split files exceeding 1000 lines before continuing, or add a"
    echo "grandfather ceiling to .claude/hooks/file_size_exempt.txt if"
    echo "the file is pre-existing debt."
    exit 2
fi

if [ -n "$WARNINGS" ]; then
    echo ""
    echo "=== File Size Check: WARNING ==="
    echo -e "$WARNINGS"
    echo ""
fi

exit 0
