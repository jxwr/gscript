#!/bin/bash
# round_tokens.sh — Print total token count (integer) for the current round.
# Used by optimize.sh tripwire T1.
#
# Usage:
#   bash scripts/round_tokens.sh <start_unix_epoch>
#     prints total tokens for all Claude session JSONL files CREATED since
#     that timestamp. Output format: plain integer (no commas, no suffix).
#
# IMPORTANT: filters by file BIRTH time (stat -f %B on macOS / stat -c %W on
# Linux), NOT modification time. The main interactive conversation's JSONL
# file is constantly modified as the user talks; its total tokens include
# the entire conversation history, not just the current round. Only fresh
# per-phase session files (created by each `claude -p` spawn) belong to the
# current round, and those files have a birth epoch >= ROUND_START_EPOCH.
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

# Portable file birth epoch helper (macOS BSD + Linux GNU)
file_birth_epoch() {
    local f="$1"
    # macOS: stat -f %B
    local birth
    birth=$(stat -f %B "$f" 2>/dev/null) && { echo "$birth"; return 0; }
    # Linux: stat -c %W (0 if unknown; fall back to mtime)
    birth=$(stat -c %W "$f" 2>/dev/null)
    if [ -n "$birth" ] && [ "$birth" -gt 0 ]; then
        echo "$birth"
        return 0
    fi
    # Fallback: mtime (less accurate but works as a floor)
    stat -c %Y "$f" 2>/dev/null || echo 0
}

TOTAL=0
# Collect all jsonl files (main sessions + subagent sessions)
while IFS= read -r f; do
    [ -f "$f" ] || continue
    birth=$(file_birth_epoch "$f")
    # Skip files born before the round started
    if [ -z "$birth" ] || [ "$birth" -lt "$START_EPOCH" ]; then
        continue
    fi
    t=$(jq -r '
        select(.message.usage != null) | .message.usage |
        ((.input_tokens // 0)
         + (.cache_creation_input_tokens // 0)
         + (.cache_read_input_tokens // 0)
         + (.output_tokens // 0))
    ' "$f" 2>/dev/null | awk '{s+=$1} END {print s+0}')
    TOTAL=$((TOTAL + ${t:-0}))
done < <(find "$PROJECT_DIR" \( -name "*.jsonl" -or -path "*/subagents/*.jsonl" \) 2>/dev/null)

echo "$TOTAL"
