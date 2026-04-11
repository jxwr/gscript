#!/bin/bash
# round_tokens.sh — Print total token count (integer) for the current round.
# Used by optimize.sh tripwire T1.
#
# Usage:
#   bash scripts/round_tokens.sh <start_unix_epoch>
#     prints total tokens for all Claude session JSONL files modified since
#     that timestamp. Output format: plain integer (no commas, no suffix).
#
# Relies on ~/.claude/projects/<slug>/ JSONL session files and jq.

set -uo pipefail

START_EPOCH="${1:-0}"
if ! [[ "$START_EPOCH" =~ ^[0-9]+$ ]]; then
    echo "Usage: bash scripts/round_tokens.sh <start_unix_epoch>" >&2
    exit 1
fi

CWD="$(pwd)"
PROJECT_SLUG="$(echo "$CWD" | sed 's|[/_.]|-|g')"
PROJECT_DIR="$HOME/.claude/projects/$PROJECT_SLUG"

if [ ! -d "$PROJECT_DIR" ]; then
    echo 0
    exit 0
fi

# Find JSONL files modified since START_EPOCH.
# macOS BSD find doesn't accept "@<epoch>" directly; use a formatted date string.
# BSD date -r <epoch> "+<fmt>" works on macOS; GNU date uses different syntax,
# so we try BSD first and fall back to GNU.
if START_DATE=$(date -r "$START_EPOCH" "+%Y-%m-%d %H:%M:%S" 2>/dev/null); then
    :  # BSD (macOS)
else
    START_DATE=$(date -d "@$START_EPOCH" "+%Y-%m-%d %H:%M:%S" 2>/dev/null || echo "1970-01-01 00:00:00")
fi

TOTAL=0
while IFS= read -r f; do
    [ -f "$f" ] || continue
    t=$(jq -r '
        select(.message.usage != null) | .message.usage |
        ((.input_tokens // 0)
         + (.cache_creation_input_tokens // 0)
         + (.cache_read_input_tokens // 0)
         + (.output_tokens // 0))
    ' "$f" 2>/dev/null | awk '{s+=$1} END {print s+0}')
    TOTAL=$((TOTAL + ${t:-0}))
done < <(find "$PROJECT_DIR" \( -name "*.jsonl" -or -path "*/subagents/*.jsonl" \) -newermt "$START_DATE" 2>/dev/null)

echo "$TOTAL"
