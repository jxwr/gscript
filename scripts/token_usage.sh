#!/bin/bash
# token_usage.sh — Show token consumption per session / per phase.
#
# Usage:
#   bash scripts/token_usage.sh              # recent sessions (last 2h)
#   bash scripts/token_usage.sh --all        # all sessions in project
#   bash scripts/token_usage.sh <uuid>       # specific session
#   bash scripts/token_usage.sh --round      # group by round (ANALYZE/IMPLEMENT/VERIFY + subagents)

set -uo pipefail

CWD="$(pwd)"
PROJECT_SLUG="$(echo "$CWD" | sed 's|[/_.]|-|g')"
PROJECT_DIR="$HOME/.claude/projects/$PROJECT_SLUG"
WINDOW_MIN=120
MODE="recent"

while [ $# -gt 0 ]; do
    case "$1" in
        --all) MODE="all" ;;
        --round) MODE="round" ;;
        --help|-h)
            grep '^#' "$0" | sed 's/^# \?//' | head -6
            exit 0 ;;
        *) MODE="single"; TARGET="$1" ;;
    esac
    shift
done

# Sum tokens from a single JSONL file
sum_tokens() {
    local f="$1"
    jq -r '
        select(.message.usage != null) | .message.usage |
        [.input_tokens // 0, .cache_creation_input_tokens // 0, .cache_read_input_tokens // 0, .output_tokens // 0] |
        @tsv
    ' "$f" 2>/dev/null | awk '
    BEGIN { inp=0; cache_create=0; cache_read=0; out=0; calls=0 }
    {
        inp += $1; cache_create += $2; cache_read += $3; out += $4; calls++
    }
    END {
        total = inp + cache_create + cache_read + out
        printf "%d\t%d\t%d\t%d\t%d\t%d\n", total, inp, cache_create, cache_read, out, calls
    }'
}

# Get session title (reuse watch-child logic)
session_title() {
    local f="$1"
    local raw
    raw=$(head -200 "$f" 2>/dev/null \
        | jq -r 'select(.type=="user") | .message.content | if type == "string" then . else (.[0].text // "") end' 2>/dev/null \
        | head -1 | tr '\n\r' '  ')
    if [[ "$raw" == "# "* ]]; then
        echo "$raw" | sed -n 's/^# \([A-Z +]*\).*/\1/p' | head -c 25
    elif [[ "$raw" == "You are a Coder"* ]] || [[ "$raw" == "You are a compiler"* ]]; then
        echo "Coder"
    elif [[ "$raw" == "You are an"*"evaluator"* ]]; then
        echo "Evaluator"
    elif [[ "$raw" == *"web search"* ]] || [[ "$raw" == "Do a quick web"* ]]; then
        echo "WebSearch"
    elif [[ "$raw" == *"understand"* ]] || [[ "$raw" == *"research"* ]]; then
        echo "Research"
    else
        echo "$raw" | head -c 30
    fi
}

print_header() {
    printf "%-30s %10s %10s %10s %10s %10s %6s\n" "SESSION" "TOTAL" "INPUT" "CACHE_W" "CACHE_R" "OUTPUT" "CALLS"
    printf "%-30s %10s %10s %10s %10s %10s %6s\n" "------------------------------" "----------" "----------" "----------" "----------" "----------" "------"
}

print_row() {
    local label="$1" total="$2" inp="$3" cw="$4" cr="$5" out="$6" calls="$7"
    # Format large numbers with K suffix
    fmt() {
        local n=$1
        if [ "$n" -ge 1000000 ]; then
            printf "%.1fM" "$(echo "$n" | awk '{printf "%.1f", $1/1000000}')"
        elif [ "$n" -ge 1000 ]; then
            printf "%.1fK" "$(echo "$n" | awk '{printf "%.1f", $1/1000}')"
        else
            printf "%d" "$n"
        fi
    }
    printf "%-30s %10s %10s %10s %10s %10s %6s\n" \
        "$label" "$(fmt "$total")" "$(fmt "$inp")" "$(fmt "$cw")" "$(fmt "$cr")" "$(fmt "$out")" "$calls"
}

# Collect files based on mode
collect_files() {
    case "$MODE" in
        single)
            find "$PROJECT_DIR" -name "${TARGET}*.jsonl" 2>/dev/null | head -1
            ;;
        all)
            find "$PROJECT_DIR" -name "*.jsonl" 2>/dev/null
            ;;
        recent|round)
            find "$PROJECT_DIR" -name "*.jsonl" -mmin -$WINDOW_MIN 2>/dev/null
            ;;
    esac
}

# Main
echo "Token Usage Report"
echo ""

if [ "$MODE" = "round" ]; then
    # Group: project-level sessions + their subagents
    echo "Sessions grouped by phase (last ${WINDOW_MIN}m):"
    echo ""
    print_header
    GRAND_TOTAL=0
    for f in $(ls -t "$PROJECT_DIR"/*.jsonl 2>/dev/null); do
        mtime=$(stat -f "%m" "$f" 2>/dev/null || stat -c "%Y" "$f" 2>/dev/null || echo 0)
        now=$(date +%s)
        age=$(( (now - mtime) / 60 ))
        [ "$age" -gt "$WINDOW_MIN" ] && continue

        title=$(session_title "$f")
        IFS=$'\t' read -r total inp cw cr out calls <<< "$(sum_tokens "$f")"
        [ "$total" -eq 0 ] && continue
        print_row "$title" "$total" "$inp" "$cw" "$cr" "$out" "$calls"
        GRAND_TOTAL=$((GRAND_TOTAL + total))

        # Check for subagents
        stem=$(basename "$f" .jsonl)
        subdir="$PROJECT_DIR/$stem/subagents"
        if [ -d "$subdir" ]; then
            for sf in "$subdir"/*.jsonl; do
                [ -f "$sf" ] || continue
                stitle="  └ $(session_title "$sf")"
                IFS=$'\t' read -r stotal sinp scw scr sout scalls <<< "$(sum_tokens "$sf")"
                [ "$stotal" -eq 0 ] && continue
                print_row "$stitle" "$stotal" "$sinp" "$scw" "$scr" "$sout" "$scalls"
                GRAND_TOTAL=$((GRAND_TOTAL + stotal))
            done
        fi
    done
    echo ""
    printf "%-30s %10s\n" "GRAND TOTAL" "$(echo "$GRAND_TOTAL" | awk '{if($1>=1000000) printf "%.1fM",$1/1000000; else if($1>=1000) printf "%.1fK",$1/1000; else print $1}')"
else
    print_header
    GRAND_TOTAL=0
    while IFS= read -r f; do
        [ -z "$f" ] && continue
        [ -f "$f" ] || continue
        title=$(session_title "$f")
        id8=$(basename "$f" .jsonl | cut -c1-8)
        label="${title} (${id8})"
        IFS=$'\t' read -r total inp cw cr out calls <<< "$(sum_tokens "$f")"
        [ "$total" -eq 0 ] && continue
        print_row "$label" "$total" "$inp" "$cw" "$cr" "$out" "$calls"
        GRAND_TOTAL=$((GRAND_TOTAL + total))
    done < <(collect_files | while read f; do
        mtime=$(stat -f "%m" "$f" 2>/dev/null || stat -c "%Y" "$f" 2>/dev/null || echo 0)
        echo "$mtime $f"
    done | sort -rn | cut -d' ' -f2-)
    echo ""
    printf "%-30s %10s\n" "GRAND TOTAL" "$(echo "$GRAND_TOTAL" | awk '{if($1>=1000000) printf "%.1fM",$1/1000000; else if($1>=1000) printf "%.1fK",$1/1000; else print $1}')"
fi
