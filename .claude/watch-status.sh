#!/bin/bash
# watch-status.sh — Real-time 1-line status summary of what the agent is doing.
# Reads watch-child.sh output, periodically summarizes via Haiku.
#
# Usage:
#   bash .claude/watch-status.sh           # default: summarize every 10s
#   bash .claude/watch-status.sh 5         # summarize every 5s
#   bash .claude/watch-status.sh 15 3      # every 15s, keep last 3 summaries

set -uo pipefail

INTERVAL="${1:-10}"
HISTORY="${2:-1}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TMPFILE=$(mktemp)
LAST_SUMMARY=""
SUMMARIES=()

# Colors
if [ -t 1 ]; then
    R=$'\033[0m'; B=$'\033[1m'; D=$'\033[2m'
    C_STATUS=$'\033[38;5;45m'
    C_TIME=$'\033[38;5;240m'
    C_IDLE=$'\033[38;5;243m'
    C_SWITCH=$'\033[38;5;220m'
    C_HEAD=$'\033[38;5;39m'
else
    R=""; B=""; D=""; C_STATUS=""; C_TIME=""; C_IDLE=""; C_SWITCH=""; C_HEAD=""
fi

cleanup() {
    kill "$WATCH_PID" 2>/dev/null
    wait "$WATCH_PID" 2>/dev/null
    rm -f "$TMPFILE"
}
trap cleanup EXIT INT TERM

# Start watch-child.sh in background, writing to temp file
bash "$SCRIPT_DIR/watch-child.sh" --no-follow 2>&1 > /dev/null  # discard history
bash "$SCRIPT_DIR/watch-child.sh" 2>&1 >> "$TMPFILE" &
WATCH_PID=$!

printf "%s%s⚡ watch-status: summarizing every %ss via Haiku%s\n" "$B" "$C_HEAD" "$INTERVAL" "$R"
printf "%s   Ctrl+C to stop%s\n\n" "$D" "$R"

PREV_LINES=0
IDLE_COUNT=0

while kill -0 "$WATCH_PID" 2>/dev/null; do
    sleep "$INTERVAL"

    TOTAL=$(wc -l < "$TMPFILE" 2>/dev/null | tr -d ' ')
    NOW=$(date '+%H:%M:%S')

    # No new content
    if [ "$TOTAL" -le "$PREV_LINES" ]; then
        IDLE_COUNT=$((IDLE_COUNT + 1))
        if [ "$IDLE_COUNT" -ge 3 ]; then
            printf "\r\033[K%s%s%s  %s⏸ idle (no new activity for %ds)%s" \
                "$C_TIME" "$NOW" "$R" "$C_IDLE" "$((IDLE_COUNT * INTERVAL))" "$R"
        fi
        continue
    fi
    IDLE_COUNT=0

    # Get recent new lines (max 40)
    NEW_COUNT=$((TOTAL - PREV_LINES))
    [ "$NEW_COUNT" -gt 40 ] && NEW_COUNT=40
    RECENT=$(tail -"$NEW_COUNT" "$TMPFILE" 2>/dev/null)
    PREV_LINES=$TOTAL

    # Check for session switch banners
    if echo "$RECENT" | grep -q "━━━.*━━━"; then
        SWITCH=$(echo "$RECENT" | grep "━━━" | tail -1 | sed 's/.*━━━ //' | sed 's/ ━━━.*//')
        printf "\n%s%s↻ switched: %s%s\n" "$C_SWITCH" "$B" "$SWITCH" "$R"
    fi

    # Summarize via Haiku
    PROMPT="Based on this agent activity log, write ONE sentence (under 80 chars, in Chinese) describing what the agent is currently doing. Be specific: mention file names, function names, techniques, benchmarks, test names. Output ONLY the sentence, no quotes.

LOG:
$RECENT"

    SUMMARY=$(printf "%s" "$PROMPT" | claude -p --model haiku 2>/dev/null | head -1 | head -c 100)

    if [ -z "$SUMMARY" ]; then
        SUMMARY="(summarizer unavailable)"
    fi

    # Display
    if [ "$HISTORY" -le 1 ]; then
        # Single-line mode: overwrite
        printf "\r\033[K%s%s%s  %s%s%s" "$C_TIME" "$NOW" "$R" "$C_STATUS" "$SUMMARY" "$R"
    else
        # Multi-line mode: keep last N summaries
        SUMMARIES+=("$NOW  $SUMMARY")
        # Trim to HISTORY size
        while [ "${#SUMMARIES[@]}" -gt "$HISTORY" ]; do
            SUMMARIES=("${SUMMARIES[@]:1}")
        done
        # Redraw
        printf "\033[%dA" "${#SUMMARIES[@]}" 2>/dev/null || true
        for s in "${SUMMARIES[@]}"; do
            printf "\033[K%s%s%s\n" "$C_STATUS" "$s" "$R"
        done
    fi
done

echo ""
echo "watch-child.sh exited."
