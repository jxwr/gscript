#!/bin/bash
# dashboard.sh — Single-screen terminal dashboard for GScript JIT optimization loop.
# Refreshes every 5 seconds. Shows round status, token usage, commits, codebase
# stats, benchmark snapshot, and active session at a glance.
#
# Usage:
#   bash .claude/dashboard.sh           # normal mode (alternate screen, refresh loop)
#   bash .claude/dashboard.sh --once    # render one frame to stdout and exit
#
# Dependencies: jq, git, standard Unix tools
# Designed for ~120 columns, ~40 rows. Uses ANSI colors + Unicode box drawing.

set -uo pipefail

ONCE=false
[ "${1:-}" = "--once" ] && ONCE=true

# ─── project paths ─────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_DIR"

STATE_FILE="$PROJECT_DIR/opt/state.json"
PLAN_FILE="$PROJECT_DIR/opt/current_plan.md"
LATEST_JSON="$PROJECT_DIR/benchmarks/data/latest.json"

CWD_SLUG="$(echo "$PROJECT_DIR" | sed 's|[/_.]|-|g')"
SESSION_DIR="$HOME/.claude/projects/$CWD_SLUG"

# ─── terminal setup ───────────────────────────────────────────────────
COLS=$(tput cols 2>/dev/null || echo 120)
ROWS=$(tput lines 2>/dev/null || echo 40)
[ "$COLS" -lt 80 ] && COLS=80

# ─── ANSI colors ──────────────────────────────────────────────────────
RST=$'\033[0m'
BOLD=$'\033[1m'
DIM=$'\033[2m'
ITAL=$'\033[3m'

# Foreground colors
FG_RED=$'\033[38;5;196m'
FG_GREEN=$'\033[38;5;82m'
FG_YELLOW=$'\033[38;5;220m'
FG_CYAN=$'\033[38;5;45m'
FG_BLUE=$'\033[38;5;39m'
FG_MAGENTA=$'\033[38;5;141m'
FG_ORANGE=$'\033[38;5;214m'
FG_WHITE=$'\033[38;5;255m'
FG_GRAY=$'\033[38;5;245m'
FG_DARKGRAY=$'\033[38;5;240m'
FG_DIMWHITE=$'\033[38;5;252m'

# Background
BG_DARKGRAY=$'\033[48;5;236m'

# ─── box drawing helpers ──────────────────────────────────────────────
# BOX_W: inner content width between "│ " and " │"  (total row = BOX_W + 4)
# BOX_BORDER: fill width for horizontal lines between ┌/└/├ and ┐/┘/┤ (total = BOX_BORDER + 2)
# Both should produce lines of exactly COLS display columns.
BOX_W=$((COLS - 4))
BOX_BORDER=$((COLS - 2))

# Draw a horizontal line with left/right/fill characters (uses BOX_BORDER width)
hline() {
    local left="$1" right="$2" fill="$3"
    local line=""
    for ((i=0; i<BOX_BORDER; i++)); do
        line+="$fill"
    done
    printf "%s%s%s\n" "$left" "$line" "$right"
}

# Draw a horizontal line with a middle junction
# The junction position matches row2's middle │, which is at offset (1 + split_at) from the left border
hline_mid() {
    local left="$1" mid="$2" right="$3" fill="$4" split_at="$5"
    local junction=$((split_at + 1))  # +1 for the leading space in content rows
    local line=""
    for ((i=0; i<BOX_BORDER; i++)); do
        if [ "$i" -eq "$junction" ]; then
            line+="$mid"
        else
            line+="$fill"
        fi
    done
    printf "%s%s%s\n" "$left" "$line" "$right"
}

# Pad a string to exact display width (pad with spaces if short, truncate if long)
pad() {
    local str="$1" width="$2"
    local visible_len
    # Strip ANSI codes for length calculation
    visible_len=$(printf "%s" "$str" | sed $'s/\033\\[[0-9;]*m//g' | wc -m | tr -d ' ')
    if [ "$visible_len" -gt "$width" ]; then
        # Truncate: strip ANSI, cut to width, re-add reset
        local plain
        plain=$(printf "%s" "$str" | sed $'s/\033\\[[0-9;]*m//g')
        printf "%s%s" "${plain:0:$((width-1))}" "${RST}"
    elif [ "$visible_len" -lt "$width" ]; then
        local padding=$((width - visible_len))
        printf "%s%*s" "$str" "$padding" ""
    else
        printf "%s" "$str"
    fi
}

# Print a row inside the box (single column, full width)
row() {
    local content="$1"
    printf "%s %s %s\n" "│" "$(pad "$content" $BOX_W)" "│"
}

# Print a row with two columns split at a position
# Layout: "│ " + left(split chars) + " │ " + right(rest) + " │"
# Total = 1 + 1 + split + 1 + 1 + 1 + right + 1 + 1 = split + right + 7... no.
# Simpler: "│ LEFT│ RIGHT │"  where LEFT is padded to split, RIGHT to the rest.
# Total: │(1) + " "(1) + LEFT(left_w) + │(1) + " "(1) + RIGHT(right_w) + " "(1) + │(1) = left_w + right_w + 6
# Want total = COLS, so left_w + right_w = COLS - 6
row2() {
    local left="$1" right="$2" split_at="$3"
    local left_w=$split_at
    local right_w=$((COLS - 6 - split_at))
    printf "│ %s│ %s │\n" "$(pad "$left" $left_w)" "$(pad "$right" $right_w)"
}

# ─── cache infrastructure ─────────────────────────────────────────────
CACHE_DIR=$(mktemp -d)
cleanup() {
    rm -rf "$CACHE_DIR"
    if ! $ONCE; then
        tput rmcup 2>/dev/null
        tput cnorm 2>/dev/null
    fi
    exit 0
}
trap cleanup INT TERM EXIT

# Get file mtime as epoch
mtime_epoch() {
    stat -f "%m" "$1" 2>/dev/null || stat -c "%Y" "$1" 2>/dev/null || echo 0
}

# ─── data collection functions ────────────────────────────────────────

# --- Round Status ---
get_round_status() {
    local cycle_id="" phase="" target="" category="" initiative="" started=""

    # --- Primary: state.json top-level fields (set during active round) ---
    if [ -f "$STATE_FILE" ]; then
        cycle_id=$(jq -r '.cycle_id // ""' "$STATE_FILE" 2>/dev/null)
        phase=$(jq -r '.cycle // ""' "$STATE_FILE" 2>/dev/null)
        target=$(jq -r '.target // ""' "$STATE_FILE" 2>/dev/null)
        category=$(jq -r '.category // ""' "$STATE_FILE" 2>/dev/null)
        initiative=$(jq -r '.initiative // ""' "$STATE_FILE" 2>/dev/null)
        started=$(jq -r '.started // ""' "$STATE_FILE" 2>/dev/null)
    fi

    # --- Fallback 1: current_plan.md (plan written, IMPLEMENT/VERIFY in progress) ---
    local plan_title="" plan_category="" plan_initiative=""
    if [ -f "$PLAN_FILE" ]; then
        plan_title=$(head -1 "$PLAN_FILE" | sed 's/^# Optimization Plan: //' | head -c 50)
        plan_category=$(grep -m1 '^> Category:' "$PLAN_FILE" 2>/dev/null | sed 's/^> Category: //' | awk '{print $1}')
        plan_initiative=$(grep -m1 '^> Initiative:' "$PLAN_FILE" 2>/dev/null | sed 's/^> Initiative: //')
    fi

    # --- Fallback 2: analyze_report.md (ANALYZE done, plan may not exist yet) ---
    local report_file="$PROJECT_DIR/opt/analyze_report.md"
    local report_target="" report_category="" report_initiative=""
    if [ -f "$report_file" ]; then
        report_category=$(grep -m1 '^\- \*\*Category\*\*' "$report_file" 2>/dev/null | sed 's/.*: //' | awk '{print $1}')
        report_initiative=$(grep -m1 '^\- \*\*Initiative\*\*' "$report_file" 2>/dev/null | sed 's/.*: //')
        report_target=$(grep -m1 '^\- \*\*Benchmarks\*\*\|^\- \*\*Reason\*\*' "$report_file" 2>/dev/null | sed 's/.*: //' | head -c 50)
    fi

    # --- Fallback 3: previous_rounds[-1] (between rounds) ---
    local prev_category="" prev_initiative=""
    if [ -f "$STATE_FILE" ]; then
        prev_category=$(jq -r '(.previous_rounds[-1].category) // ""' "$STATE_FILE" 2>/dev/null)
        prev_initiative=$(jq -r '(.previous_rounds[-1].initiative) // ""' "$STATE_FILE" 2>/dev/null)
    fi

    # --- Merge: prefer state.json > current_plan > analyze_report > previous_round ---
    [ -z "$target" ] && target="$plan_title"
    [ -z "$target" ] && target="$report_target"

    [ -z "$category" ] || [ "$category" = "null" ] && category="$plan_category"
    [ -z "$category" ] && category="$report_category"
    [ -z "$category" ] && category="$prev_category"

    [ -z "$initiative" ] || [ "$initiative" = "null" ] && initiative="$plan_initiative"
    [ -z "$initiative" ] && initiative="$report_initiative"
    [ -z "$initiative" ] && initiative="$prev_initiative"

    # Count previous rounds
    local round_num=0
    if [ -f "$STATE_FILE" ]; then
        round_num=$(jq -r '.previous_rounds | length' "$STATE_FILE" 2>/dev/null)
        round_num=$((round_num + 1))
    fi

    # Extract start time — fallback to ANALYZE session mtime
    local start_time=""
    if [ -n "$started" ] && [ "$started" != "null" ] && [ "$started" != "" ]; then
        start_time=$(echo "$started" | grep -oE 'T[0-9]{2}:[0-9]{2}' | sed 's/T//')
    fi
    if [ -z "$start_time" ] && [ -d "$SESSION_DIR" ]; then
        # Find the most recent ANALYZE child session, use its creation time
        local analyze_session
        analyze_session=$(ls -t "$SESSION_DIR"/*.jsonl 2>/dev/null | while read f; do
            local fp
            fp=$(head -50 "$f" 2>/dev/null | jq -r 'select(.type=="user") | .message.content | if type == "string" then . else (.[0].text // "") end' 2>/dev/null | head -1)
            [[ "$fp" == "# ANALYZE"* ]] && echo "$f" && break
        done)
        if [ -n "$analyze_session" ]; then
            start_time=$(head -5 "$analyze_session" 2>/dev/null | jq -r '.timestamp // empty' 2>/dev/null | head -1 | grep -oE 'T[0-9]{2}:[0-9]{2}' | sed 's/T//')
        fi
    fi

    # Detect active phase from child sessions
    local detected_phase=""
    if [ -d "$SESSION_DIR" ]; then
        local newest_child=""
        newest_child=$(ls -t "$SESSION_DIR"/*.jsonl 2>/dev/null | while read f; do
            local first_line
            first_line=$(head -50 "$f" 2>/dev/null | jq -r 'select(.type=="user") | .message.content | if type == "string" then . else (.[0].text // "") end' 2>/dev/null | head -1)
            if [[ "$first_line" == "# "* ]]; then
                echo "$f"
                break
            fi
        done)
        if [ -n "$newest_child" ]; then
            local child_prompt
            child_prompt=$(head -50 "$newest_child" 2>/dev/null | jq -r 'select(.type=="user") | .message.content | if type == "string" then . else (.[0].text // "") end' 2>/dev/null | head -1)
            case "$child_prompt" in
                "# ANALYZE"*) detected_phase="ANALYZE" ;;
                "# IMPLEMENT"*) detected_phase="IMPLEMENT" ;;
                "# VERIFY"*) detected_phase="VERIFY" ;;
                "# MEASURE"*) detected_phase="MEASURE" ;;
                "# REVIEW"*) detected_phase="REVIEW" ;;
            esac
        fi
    fi

    # Use detected phase if available, otherwise fall back to state.json
    [ -n "$detected_phase" ] && phase="$detected_phase"
    [ -z "$phase" ] && phase="IDLE"

    # Shorten target for display (prefer state.json target, fall back to plan)
    # Left column is ~split width, label "  Target: " is 10 chars, so max ~(split-12)
    local short_target="${target:-$plan_title}"
    local max_target=25
    [ ${#short_target} -gt "$max_target" ] && short_target="${short_target:0:$((max_target-3))}..."

    # Store results
    ROUND_NUM="$round_num"
    ROUND_PHASE="$phase"
    ROUND_TARGET="$short_target"
    ROUND_CATEGORY="${category:-—}"
    ROUND_INITIATIVE="${initiative:-—}"
    ROUND_START="${start_time:-—}"
    ROUND_CYCLE_ID="${cycle_id:-—}"
}

# --- Token Usage ---
get_token_usage() {
    # Cache: only recompute if session files changed
    local cache_file="$CACHE_DIR/tokens.cache"
    local cache_mtime_file="$CACHE_DIR/tokens.mtime"

    local newest_mtime=0
    if [ -d "$SESSION_DIR" ]; then
        newest_mtime=$(ls -t "$SESSION_DIR"/*.jsonl 2>/dev/null | head -1 | xargs stat -f "%m" 2>/dev/null || echo 0)
    fi

    if [ -f "$cache_file" ] && [ -f "$cache_mtime_file" ]; then
        local cached_mtime
        cached_mtime=$(cat "$cache_mtime_file")
        if [ "$cached_mtime" = "$newest_mtime" ]; then
            # Use cached values
            eval "$(cat "$cache_file")"
            return
        fi
    fi

    TOKEN_ANALYZE_TOTAL=0; TOKEN_ANALYZE_CALLS=0
    TOKEN_IMPLEMENT_TOTAL=0; TOKEN_IMPLEMENT_CALLS=0
    TOKEN_VERIFY_TOTAL=0; TOKEN_VERIFY_CALLS=0
    TOKEN_REVIEW_TOTAL=0; TOKEN_REVIEW_CALLS=0
    TOKEN_GRAND=0

    if [ ! -d "$SESSION_DIR" ]; then
        echo "$newest_mtime" > "$cache_mtime_file"
        return
    fi

    # Find most recent ANALYZE session
    local analyze_file="" analyze_mtime=0
    for f in $(ls -t "$SESSION_DIR"/*.jsonl 2>/dev/null); do
        local first
        first=$(head -50 "$f" 2>/dev/null | jq -r 'select(.type=="user") | .message.content | if type == "string" then . else (.[0].text // "") end' 2>/dev/null | head -1)
        if [[ "$first" == "# ANALYZE"* ]]; then
            analyze_file="$f"
            analyze_mtime=$(mtime_epoch "$f")
            break
        fi
    done

    # Sum tokens from a JSONL file
    _sum_file() {
        local f="$1"
        jq -r 'select(.message.usage != null) | .message.usage | [.input_tokens // 0, .cache_creation_input_tokens // 0, .cache_read_input_tokens // 0, .output_tokens // 0] | add' "$f" 2>/dev/null | awk '{s+=$1} END {print s+0}'
    }

    _count_calls() {
        local f="$1"
        jq -r 'select(.message.usage != null) | 1' "$f" 2>/dev/null | wc -l | tr -d ' '
    }

    # Sum tokens including subagents
    _sum_with_subagents() {
        local f="$1"
        local total=0 calls=0
        local ft fc
        ft=$(_sum_file "$f")
        fc=$(_count_calls "$f")
        total=$((total + ft))
        calls=$((calls + fc))

        local stem subdir
        stem=$(basename "$f" .jsonl)
        subdir="$SESSION_DIR/$stem/subagents"
        if [ -d "$subdir" ]; then
            for sf in "$subdir"/*.jsonl; do
                [ -f "$sf" ] || continue
                ft=$(_sum_file "$sf")
                fc=$(_count_calls "$sf")
                total=$((total + ft))
                calls=$((calls + fc))
            done
        fi
        echo "$total $calls"
    }

    if [ -n "$analyze_file" ]; then
        read TOKEN_ANALYZE_TOTAL TOKEN_ANALYZE_CALLS <<< "$(_sum_with_subagents "$analyze_file")"

        # Find IMPLEMENT/VERIFY/REVIEW sessions newer than this ANALYZE
        for f in $(ls -t "$SESSION_DIR"/*.jsonl 2>/dev/null); do
            [ "$f" = "$analyze_file" ] && continue
            local fmtime
            fmtime=$(mtime_epoch "$f")
            [ "$fmtime" -lt "$analyze_mtime" ] && continue
            local first
            first=$(head -50 "$f" 2>/dev/null | jq -r 'select(.type=="user") | .message.content | if type == "string" then . else (.[0].text // "") end' 2>/dev/null | head -1)
            case "$first" in
                "# IMPLEMENT"*)
                    read t c <<< "$(_sum_with_subagents "$f")"
                    TOKEN_IMPLEMENT_TOTAL=$((TOKEN_IMPLEMENT_TOTAL + t))
                    TOKEN_IMPLEMENT_CALLS=$((TOKEN_IMPLEMENT_CALLS + c))
                    ;;
                "# VERIFY"*)
                    read t c <<< "$(_sum_with_subagents "$f")"
                    TOKEN_VERIFY_TOTAL=$((TOKEN_VERIFY_TOTAL + t))
                    TOKEN_VERIFY_CALLS=$((TOKEN_VERIFY_CALLS + c))
                    ;;
                *"REVIEW"*)
                    read t c <<< "$(_sum_with_subagents "$f")"
                    TOKEN_REVIEW_TOTAL=$((TOKEN_REVIEW_TOTAL + t))
                    TOKEN_REVIEW_CALLS=$((TOKEN_REVIEW_CALLS + c))
                    ;;
            esac
        done
    fi

    TOKEN_GRAND=$((TOKEN_ANALYZE_TOTAL + TOKEN_IMPLEMENT_TOTAL + TOKEN_VERIFY_TOTAL + TOKEN_REVIEW_TOTAL))

    # Write cache
    cat > "$cache_file" <<CACHE_EOF
TOKEN_ANALYZE_TOTAL=$TOKEN_ANALYZE_TOTAL
TOKEN_ANALYZE_CALLS=$TOKEN_ANALYZE_CALLS
TOKEN_IMPLEMENT_TOTAL=$TOKEN_IMPLEMENT_TOTAL
TOKEN_IMPLEMENT_CALLS=$TOKEN_IMPLEMENT_CALLS
TOKEN_VERIFY_TOTAL=$TOKEN_VERIFY_TOTAL
TOKEN_VERIFY_CALLS=$TOKEN_VERIFY_CALLS
TOKEN_REVIEW_TOTAL=$TOKEN_REVIEW_TOTAL
TOKEN_REVIEW_CALLS=$TOKEN_REVIEW_CALLS
TOKEN_GRAND=$TOKEN_GRAND
CACHE_EOF
    echo "$newest_mtime" > "$cache_mtime_file"
}

# Format token count: "12.3M", "450K", "0"
fmt_tokens() {
    local n=$1
    if [ "$n" -ge 1000000 ]; then
        awk "BEGIN { printf \"%.1fM\", $n/1000000 }"
    elif [ "$n" -ge 1000 ]; then
        awk "BEGIN { printf \"%.1fK\", $n/1000 }"
    elif [ "$n" -gt 0 ]; then
        echo "$n"
    else
        echo "—"
    fi
}

# --- Recent Commits ---
get_commits() {
    COMMITS=$(git log --oneline -5 2>/dev/null || echo "  (no git history)")
}

# --- Codebase Stats ---
get_codebase_stats() {
    local cache_file="$CACHE_DIR/codebase.cache"

    # Check if any source file changed
    local newest_src=0
    newest_src=$(find internal/methodjit -name "*.go" -exec stat -f "%m" {} + 2>/dev/null | sort -rn | head -1)
    [ -z "$newest_src" ] && newest_src=0

    local cache_mtime_file="$CACHE_DIR/codebase.mtime"
    if [ -f "$cache_file" ] && [ -f "$cache_mtime_file" ]; then
        local cached
        cached=$(cat "$cache_mtime_file")
        if [ "$cached" = "$newest_src" ]; then
            eval "$(cat "$cache_file")"
            return
        fi
    fi

    CODEBASE_FILES=$(find internal/methodjit -name "*.go" ! -name "*_test.go" 2>/dev/null | wc -l | tr -d ' ')
    CODEBASE_SRC_LINES=$(find internal/methodjit -name "*.go" ! -name "*_test.go" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')
    CODEBASE_TEST_LINES=$(find internal/methodjit -name "*_test.go" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')

    local total=$((CODEBASE_SRC_LINES + CODEBASE_TEST_LINES))
    if [ "$total" -gt 0 ]; then
        CODEBASE_TEST_PCT=$((CODEBASE_TEST_LINES * 100 / total))
    else
        CODEBASE_TEST_PCT=0
    fi

    # Largest file (skip the "total" summary line from wc)
    local largest_info
    largest_info=$(find internal/methodjit -name "*.go" ! -name "*_test.go" -exec wc -l {} + 2>/dev/null | grep -v ' total$' | sort -rn | head -1)
    CODEBASE_LARGEST_LINES=$(echo "$largest_info" | awk '{print $1}')
    CODEBASE_LARGEST_FILE=$(echo "$largest_info" | awk '{print $2}' | xargs basename 2>/dev/null)

    # Round diff: commits since head_commit in state.json
    CODEBASE_DIFF_ADD=0
    CODEBASE_DIFF_DEL=0
    if [ -f "$STATE_FILE" ]; then
        local head_commit
        head_commit=$(jq -r '.head_commit // ""' "$STATE_FILE" 2>/dev/null)
        if [ -n "$head_commit" ] && git rev-parse "$head_commit" >/dev/null 2>&1; then
            local diffstat
            diffstat=$(git diff --shortstat "$head_commit"..HEAD 2>/dev/null)
            CODEBASE_DIFF_ADD=$(echo "$diffstat" | grep -oE '[0-9]+ insertion' | grep -oE '[0-9]+' || echo 0)
            CODEBASE_DIFF_DEL=$(echo "$diffstat" | grep -oE '[0-9]+ deletion' | grep -oE '[0-9]+' || echo 0)
            [ -z "$CODEBASE_DIFF_ADD" ] && CODEBASE_DIFF_ADD=0
            [ -z "$CODEBASE_DIFF_DEL" ] && CODEBASE_DIFF_DEL=0
        fi
    fi

    cat > "$cache_file" <<CACHE_EOF
CODEBASE_FILES=$CODEBASE_FILES
CODEBASE_SRC_LINES=$CODEBASE_SRC_LINES
CODEBASE_TEST_LINES=$CODEBASE_TEST_LINES
CODEBASE_TEST_PCT=$CODEBASE_TEST_PCT
CODEBASE_LARGEST_LINES=$CODEBASE_LARGEST_LINES
CODEBASE_LARGEST_FILE=$CODEBASE_LARGEST_FILE
CODEBASE_DIFF_ADD=$CODEBASE_DIFF_ADD
CODEBASE_DIFF_DEL=$CODEBASE_DIFF_DEL
CACHE_EOF
    echo "$newest_src" > "$cache_mtime_file"
}

# --- Benchmark Snapshot ---
get_benchmarks() {
    if [ ! -f "$LATEST_JSON" ]; then
        BENCH_LINES=()
        return
    fi

    # Parse benchmarks into arrays
    BENCH_LINES=()
    while IFS='|' read -r name jit_raw luajit_raw; do
        # Extract time value from "Time: 1.234s" or "Time: 1.234s (3 reps)"
        local jit_time luajit_time
        jit_time=$(echo "$jit_raw" | grep -oE '[0-9]+\.[0-9]+' | head -1)
        luajit_time=$(echo "$luajit_raw" | grep -oE '[0-9]+\.[0-9]+' | head -1)

        [ -z "$jit_time" ] && continue

        local ratio_str=""
        local color="$FG_GRAY"
        # Check luajit_time is present, non-empty, and not zero
        local luajit_nonzero=false
        if [ -n "$luajit_time" ]; then
            local is_zero
            is_zero=$(awk "BEGIN { print ($luajit_time == 0) ? 1 : 0 }")
            [ "$is_zero" = "0" ] && luajit_nonzero=true
        fi

        if $luajit_nonzero; then
            local ratio
            ratio=$(awk "BEGIN { r = $jit_time / $luajit_time; printf \"%.0f\", r }")
            ratio_str="${ratio}x"
            if [ "$ratio" -le 5 ]; then
                color="$FG_GREEN"
            elif [ "$ratio" -le 15 ]; then
                color="$FG_YELLOW"
            elif [ "$ratio" -le 30 ]; then
                color="$FG_ORANGE"
            else
                color="$FG_RED"
            fi
        else
            ratio_str="-"
            color="$FG_GRAY"
        fi

        # Shorten name with meaningful abbreviations
        local short_name="$name"
        case "$short_name" in
            spectral_norm)     short_name="spectral" ;;
            fibonacci_iterative) short_name="fib_iter" ;;
            fib_recursive)     short_name="fib_rec" ;;
            mutual_recursion)  short_name="mutual_rec" ;;
            method_dispatch)   short_name="method_disp" ;;
            closure_bench)     short_name="closure" ;;
            string_bench)      short_name="string" ;;
            binary_trees)      short_name="bintrees" ;;
            table_field_access) short_name="tbl_field" ;;
            table_array_access) short_name="tbl_array" ;;
            coroutine_bench)   short_name="coroutine" ;;
            math_intensive)    short_name="math_int" ;;
            object_creation)   short_name="obj_create" ;;
        esac
        [ ${#short_name} -gt 12 ] && short_name="${short_name:0:12}"

        BENCH_LINES+=("${color}|${short_name}|${jit_time}s|${ratio_str}")
    done < <(jq -r '.results | to_entries[] | "\(.key)|\(.value.jit // "")|\(.value.luajit // "")"' "$LATEST_JSON" 2>/dev/null)
}

# --- Active Session ---
get_active_session() {
    ACTIVE_SESSION_LINE=""

    [ -d "$SESSION_DIR" ] || return

    # Find the main session (first user prompt doesn't start with "# ")
    local main_session=""
    for f in $(ls -t "$SESSION_DIR"/*.jsonl 2>/dev/null | head -5); do
        local first_prompt
        first_prompt=$(head -50 "$f" 2>/dev/null | jq -r 'select(.type=="user") | .message.content | if type == "string" then . else (.[0].text // "") end' 2>/dev/null | head -1)
        if [[ "$first_prompt" != "# "* ]]; then
            main_session="$f"
            break
        fi
    done

    # Find most recent non-main session
    local active_file="" active_mtime=0
    for f in $(ls -t "$SESSION_DIR"/*.jsonl 2>/dev/null | head -10); do
        [ "$f" = "$main_session" ] && continue
        active_file="$f"
        active_mtime=$(mtime_epoch "$f")
        break
    done

    # Also check subagent sessions
    local newest_sub=""
    newest_sub=$(find "$SESSION_DIR" -mindepth 2 -name "*.jsonl" -path "*/subagents/*" -mmin -60 2>/dev/null | while read sf; do
        printf "%s\t%s\n" "$(mtime_epoch "$sf")" "$sf"
    done | sort -rn | head -1 | cut -f2)

    if [ -n "$newest_sub" ]; then
        local sub_mtime
        sub_mtime=$(mtime_epoch "$newest_sub")
        if [ "$sub_mtime" -gt "$active_mtime" ]; then
            active_file="$newest_sub"
            active_mtime=$sub_mtime
        fi
    fi

    if [ -z "$active_file" ]; then
        ACTIVE_SESSION_LINE="${DIM}No active child session${RST}"
        ACTIVE_STATUS_LINE=""
        return
    fi

    # Get session kind and title
    local kind title rel_time
    local first_prompt
    first_prompt=$(head -50 "$active_file" 2>/dev/null | jq -r 'select(.type=="user") | .message.content | if type == "string" then . else (.[0].text // "") end' 2>/dev/null | head -1)

    case "$first_prompt" in
        "# ANALYZE"*)  kind="ANALYZE"; title="Analyzer" ;;
        "# IMPLEMENT"*) kind="IMPLEMENT"; title="Coder" ;;
        "# VERIFY"*)   kind="VERIFY"; title="Verifier" ;;
        "# REVIEW"*)   kind="REVIEW"; title="Reviewer" ;;
        "# MEASURE"*)  kind="MEASURE"; title="Profiler" ;;
        "You are a Coder"*|"You are a compiler"*) kind="IMPLEMENT"; title="Coder" ;;
        "You are an"*"evaluator"*) kind="VERIFY"; title="Evaluator" ;;
        *) kind="AGENT"; title="Agent" ;;
    esac

    # Relative time
    local now_epoch diff_s
    now_epoch=$(date +%s)
    diff_s=$((now_epoch - active_mtime))
    if [ "$diff_s" -lt 60 ]; then
        rel_time="${diff_s}s ago"
    elif [ "$diff_s" -lt 3600 ]; then
        rel_time="$((diff_s / 60))m ago"
    else
        rel_time="$((diff_s / 3600))h ago"
    fi

    # Get last tool call as activity hint
    local last_activity=""
    last_activity=$(tail -30 "$active_file" 2>/dev/null | jq -r '
        select(.type == "assistant") |
        (.message.content // []) | if type == "array" then .[] else empty end |
        select(.type == "tool_use") |
        if .name == "Bash" then "$ \(.input.command // "" | .[0:60])"
        elif .name == "Read" then "reading \(.input.file_path // "" | split("/") | .[-1])"
        elif .name == "Edit" then "editing \(.input.file_path // "" | split("/") | .[-1])"
        elif .name == "Write" then "writing \(.input.file_path // "" | split("/") | .[-1])"
        elif .name == "Grep" then "searching /\(.input.pattern // "")/"
        else "\(.name)"
        end
    ' 2>/dev/null | tail -1)

    # Truncate activity to fit in box
    local max_activity=$((BOX_W - 30))
    [ ${#last_activity} -gt "$max_activity" ] && last_activity="${last_activity:0:$((max_activity-3))}..."

    ACTIVE_SESSION_LINE="${FG_CYAN}[${kind}|${title}]${RST} ${FG_DIMWHITE}${last_activity:-working...}${RST} ${DIM}(${rel_time})${RST}"
    ACTIVE_STATUS_LINE=""
}

# ─── render the dashboard ─────────────────────────────────────────────
render() {
    local now
    now=$(date '+%H:%M:%S')

    # Collect all data
    get_round_status
    get_token_usage
    get_commits
    get_codebase_stats
    get_benchmarks
    get_active_session

    # Column split position (for 2-column sections)
    local split=$((BOX_W / 2))

    # ─── Title bar ────────────────────────────────────────────────
    # Layout: "┌─ TITLE fill_chars TIME ─┐" = COLS chars total
    # "┌─ " = 3, " ─┐" = 3, so fill = COLS - 6 - title_len - time_len
    local title_text="GScript Optimization Dashboard"
    local title_len=${#title_text}
    local time_text="$now"
    local time_len=${#time_text}
    # "┌─ " (3) + title + " " (1) + fill + " " (1) + time + " ─┐" (3) = COLS
    local fill_len=$((COLS - 8 - title_len - time_len))
    local fill=""
    for ((i=0; i<fill_len; i++)); do fill+="─"; done
    printf "${FG_CYAN}${BOLD}┌─ %s %s %s ─┐${RST}\n" "$title_text" "$fill" "$time_text"

    # ─── Round Status + Token Usage ───────────────────────────────
    row ""

    # Phase color
    local phase_color="$FG_GRAY"
    case "$ROUND_PHASE" in
        ANALYZE)   phase_color="$FG_BLUE" ;;
        IMPLEMENT) phase_color="$FG_GREEN" ;;
        VERIFY)    phase_color="$FG_YELLOW" ;;
        REVIEW)    phase_color="$FG_MAGENTA" ;;
        MEASURE)   phase_color="$FG_ORANGE" ;;
        IDLE)      phase_color="$FG_DARKGRAY" ;;
    esac

    # Largest file warning
    local largest_warn=""
    if [ "${CODEBASE_LARGEST_LINES:-0}" -ge 900 ]; then
        largest_warn=" ${FG_RED}!!${RST}"
    elif [ "${CODEBASE_LARGEST_LINES:-0}" -ge 800 ]; then
        largest_warn=" ${FG_YELLOW}!${RST}"
    fi

    row2 \
        "${BOLD}${FG_CYAN}  ROUND STATUS${RST}" \
        "${BOLD}${FG_CYAN}  TOKEN USAGE (this round)${RST}" \
        "$split"
    row2 \
        "  ${FG_GRAY}Round:${RST} ${FG_WHITE}${ROUND_NUM}${RST}" \
        "  ${FG_GRAY}ANALYZE:${RST}    $(pad "$(fmt_tokens $TOKEN_ANALYZE_TOTAL)" 8) ${DIM}(${TOKEN_ANALYZE_CALLS} calls)${RST}" \
        "$split"
    row2 \
        "  ${FG_GRAY}Phase:${RST} ${phase_color}${BOLD}${ROUND_PHASE}${RST}" \
        "  ${FG_GRAY}IMPLEMENT:${RST}  $(pad "$(fmt_tokens $TOKEN_IMPLEMENT_TOTAL)" 8) ${DIM}(${TOKEN_IMPLEMENT_CALLS} calls)${RST}" \
        "$split"
    row2 \
        "  ${FG_GRAY}Target:${RST} ${FG_DIMWHITE}${ROUND_TARGET}${RST}" \
        "  ${FG_GRAY}VERIFY:${RST}     $(pad "$(fmt_tokens $TOKEN_VERIFY_TOTAL)" 8) ${DIM}(${TOKEN_VERIFY_CALLS} calls)${RST}" \
        "$split"
    row2 \
        "  ${FG_GRAY}Category:${RST} ${FG_DIMWHITE}${ROUND_CATEGORY}${RST}" \
        "  ${FG_GRAY}REVIEW:${RST}     $(pad "$(fmt_tokens $TOKEN_REVIEW_TOTAL)" 8) ${DIM}(${TOKEN_REVIEW_CALLS} calls)${RST}" \
        "$split"
    row2 \
        "  ${FG_GRAY}Initiative:${RST} ${FG_DIMWHITE}${ROUND_INITIATIVE}${RST}" \
        "  ${FG_GRAY}Total:${RST}      ${BOLD}${FG_WHITE}$(fmt_tokens $TOKEN_GRAND)${RST}" \
        "$split"
    row2 \
        "  ${FG_GRAY}Started:${RST} ${FG_DIMWHITE}${ROUND_START}${RST}" \
        "" \
        "$split"
    row ""

    # ─── Separator (with column split) ──────────────────────────
    printf "${FG_CYAN}"
    hline_mid "├" "┼" "┤" "─" "$split"
    printf "${RST}"

    # ─── Commits + Codebase ───────────────────────────────────────
    row2 \
        "${BOLD}${FG_CYAN}  RECENT COMMITS${RST}" \
        "${BOLD}${FG_CYAN}  CODEBASE${RST}" \
        "$split"

    # Process commits (up to 5)
    local commit_lines=()
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        local hash msg
        hash=$(echo "$line" | cut -c1-7)
        msg=$(echo "$line" | cut -c9- | head -c $((split - 14)))
        commit_lines+=("  ${FG_YELLOW}${hash}${RST} ${FG_DIMWHITE}${msg}${RST}")
    done <<< "$COMMITS"

    # Codebase lines
    local code_lines=(
        "  ${FG_GRAY}Source:${RST} ${FG_WHITE}${CODEBASE_FILES} files, ${CODEBASE_SRC_LINES} lines${RST}"
        "  ${FG_GRAY}Tests:${RST}  ${FG_WHITE}${CODEBASE_TEST_LINES} lines (${CODEBASE_TEST_PCT}%)${RST}"
        "  ${FG_GRAY}Round:${RST}  ${FG_GREEN}+${CODEBASE_DIFF_ADD}${RST} / ${FG_RED}-${CODEBASE_DIFF_DEL}${RST} ${FG_GRAY}lines${RST}"
        "  ${FG_GRAY}Largest:${RST} ${FG_DIMWHITE}${CODEBASE_LARGEST_FILE} (${CODEBASE_LARGEST_LINES})${largest_warn}${RST}"
    )

    # Render side by side (max of both lengths)
    local max_rows=${#commit_lines[@]}
    [ ${#code_lines[@]} -gt "$max_rows" ] && max_rows=${#code_lines[@]}

    for ((i=0; i<max_rows; i++)); do
        local left="${commit_lines[$i]:-}"
        local right="${code_lines[$i]:-}"
        row2 "$left" "$right" "$split"
    done

    row ""

    # ─── Separator ────────────────────────────────────────────────
    printf "${FG_CYAN}"
    hline "├" "┤" "─"
    printf "${RST}"

    # ─── Active Session ───────────────────────────────────────────
    row "  ${BOLD}${FG_CYAN}ACTIVE:${RST} ${ACTIVE_SESSION_LINE}"

    # ─── Bottom border ────────────────────────────────────────────
    printf "${FG_CYAN}${BOLD}"
    hline "└" "┘" "─"
    printf "${RST}"
}

# ─── main loop ─────────────────────────────────────────────────────────
if $ONCE; then
    render
    exit 0
fi

# Enter alternate screen buffer, hide cursor
tput smcup 2>/dev/null
tput civis 2>/dev/null

while true; do
    # Move cursor to top-left and render
    tput cup 0 0 2>/dev/null
    render
    # Clear any leftover lines below the dashboard
    tput ed 2>/dev/null
    sleep 5
done
