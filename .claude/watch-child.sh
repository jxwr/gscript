#!/bin/bash
# watch-child.sh — Pretty-print Claude Code child session transcripts.
# Full content for text/thinking/user (word-wrapped); multi-line for tool results;
# compact single-line for tool calls.
#
# Usage:
#   bash .claude/watch-child.sh                  # interactive picker (TTY) or auto-pick (pipe)
#   bash .claude/watch-child.sh --list / -l      # list sessions and exit
#   bash .claude/watch-child.sh --pick / -p      # force interactive picker
#   bash .claude/watch-child.sh <uuid-prefix>    # watch specific session
#   bash .claude/watch-child.sh --main           # watch YOUR conversation (alias --all)
#   bash .claude/watch-child.sh --no-follow / -n # history only, no tail (default: follow)
#   bash .claude/watch-child.sh --status         # enable Haiku status bar (1-line summary)
#   bash .claude/watch-child.sh --status=5       # status bar every 5 seconds (default 15)
#   bash .claude/watch-child.sh --width=N        # override terminal width
#   bash .claude/watch-child.sh --full           # no truncation at all (verbose!)
#   bash .claude/watch-child.sh --think-lines=N  # max lines per thinking (default 50)

set -uo pipefail

# ─── locate project session dir ────────────────────────────────────────
CWD="$(pwd)"
PROJECT_SLUG="$(echo "$CWD" | sed 's|[/_.]|-|g')"
PROJECT_DIR="$HOME/.claude/projects/$PROJECT_SLUG"
SUBAGENT_WINDOW_MIN=120   # include subagent sessions modified within this window

if [ ! -d "$PROJECT_DIR" ]; then
    echo "No Claude Code session dir for CWD: $CWD" >&2
    echo "Expected: $PROJECT_DIR" >&2
    exit 1
fi

# True if $1 is a sub-agent session file (lives under */subagents/ within PROJECT_DIR).
is_subagent() {
    case "$1" in
        "$PROJECT_DIR"/*/subagents/*.jsonl) return 0 ;;
    esac
    return 1
}

# List all candidate jsonl files: project-level sessions + recent subagents (nested).
# Output sorted by mtime descending (newest first).
all_candidates() {
    {
        # Project-level sessions (top of PROJECT_DIR)
        ls "$PROJECT_DIR"/*.jsonl 2>/dev/null
        # Sub-agent sessions nested at PROJECT_DIR/<parent-uuid>/subagents/*.jsonl
        find "$PROJECT_DIR" -mindepth 2 -name "*.jsonl" -path "*/subagents/*" -mmin -$SUBAGENT_WINDOW_MIN 2>/dev/null
    } | while IFS= read -r f; do
        [ -f "$f" ] && printf "%s\t%s\n" "$(stat -f "%m" "$f" 2>/dev/null || stat -c "%Y" "$f" 2>/dev/null)" "$f"
    done | sort -rn | cut -f2
}

# ─── colors (auto-disable if no tty) ───────────────────────────────────
if [ -t 1 ]; then
    R=$'\033[0m'; B=$'\033[1m'; D=$'\033[2m'; I=$'\033[3m'
    C_TIME=$'\033[38;5;240m'
    C_USER=$'\033[38;5;82m'
    C_ASST=$'\033[38;5;45m'
    C_TOOL=$'\033[38;5;214m'
    C_TNAME=$'\033[38;5;220m'
    C_RSLT=$'\033[38;5;244m'
    C_ERR=$'\033[38;5;196m'
    C_THINK=$'\033[38;5;141m'
    C_HEAD=$'\033[38;5;39m'
    C_META=$'\033[38;5;245m'
    C_SEP=$'\033[38;5;238m'
else
    R=""; B=""; D=""; I=""
    C_TIME=""; C_USER=""; C_ASST=""; C_TOOL=""; C_TNAME=""
    C_RSLT=""; C_ERR=""; C_THINK=""; C_HEAD=""; C_META=""; C_SEP=""
fi

# ─── config ────────────────────────────────────────────────────────────
FOLLOW=true
LIST=false
PICK=false
INCLUDE_MAIN=false
SESSION_MATCH=""
TERM_WIDTH=$(tput cols 2>/dev/null || echo 160)
FULL=false
THINK_LINES=50
STATUS_BAR=false
STATUS_INTERVAL=15

while [ $# -gt 0 ]; do
    case "$1" in
        --list|-l) LIST=true ;;
        --pick|-p) PICK=true ;;
        --no-follow|-n) FOLLOW=false ;;
        --all|-a|--main) INCLUDE_MAIN=true ;;
        --width=*) TERM_WIDTH="${1#*=}" ;;
        --full) FULL=true ;;
        --follow|-f) FOLLOW=true ;;
        --status) STATUS_BAR=true ;;
        --status=*) STATUS_BAR=true; STATUS_INTERVAL="${1#*=}" ;;
        --think-lines=*) THINK_LINES="${1#*=}" ;;
        --help|-h)
            sed -n '3,15p' "$0" | sed 's/^# \?//'
            exit 0 ;;
        -*) echo "Unknown option: $1" >&2; exit 1 ;;
        *) SESSION_MATCH="$1" ;;
    esac
    shift
done

# Auto-pick mode: no args, interactive TTY, no explicit session
if [ -z "$SESSION_MATCH" ] && ! $LIST && ! $INCLUDE_MAIN && [ -t 0 ] && [ -t 1 ]; then
    PICK=true
fi

PREFIX_LEN=12                             # "HH:MM:SS  X "
INDENT="            "                      # 12 spaces for continuation lines
WRAP_WIDTH=$((TERM_WIDTH - PREFIX_LEN))
[ "$WRAP_WIDTH" -lt 40 ] && WRAP_WIDTH=40

# ─── session metadata helpers ──────────────────────────────────────────

# Derive a human-readable title from a session file.
# Phase children → "MEASURE Phase", Sub-agents → "Coder: <brief>", Main → first user text.
session_title() {
    local f="$1" max_chars="${2:-60}"
    local raw
    raw=$(head -200 "$f" 2>/dev/null \
        | jq -r 'select(.type=="user") | .message.content | if type == "string" then . else (.[0].text // "") end' 2>/dev/null \
        | head -1 | tr '\n\r' '  ')
    # Phase prompts: "# MEASURE Phase" → "MEASURE"
    if [[ "$raw" == "# "* ]]; then
        local phase
        phase=$(echo "$raw" | sed -n 's/^# \([A-Z]*\) Phase.*/\1/p')
        if [ -n "$phase" ]; then
            echo "$phase"
            return
        fi
    fi
    # Sub-agent prompts — derive short role label
    if [[ "$raw" == "You are a Coder"* ]] || [[ "$raw" == "You are a compiler"* ]]; then
        echo "Coder task"; return
    fi
    if [[ "$raw" == "You are an independent code evaluator"* ]] || [[ "$raw" == "You are an evaluator"* ]]; then
        echo "Evaluator"; return
    fi
    if [[ "$raw" == "You are implementing"* ]]; then
        local brief
        brief=$(echo "$raw" | sed 's/You are implementing //' | head -c 50)
        echo "Impl: $brief"; return
    fi
    if [[ "$raw" == *"web search"* ]] || [[ "$raw" == *"WebSearch"* ]] || [[ "$raw" == "Do a quick web search"* ]]; then
        echo "Web search"; return
    fi
    if [[ "$raw" == *"understand how"* ]] || [[ "$raw" == *"investigate"* ]] || [[ "$raw" == *"research"* ]]; then
        echo "Research"; return
    fi
    # Command messages (skill invocations)
    if [[ "$raw" == "<command-message>"* ]]; then
        local cmd
        cmd=$(echo "$raw" | sed 's/<[^>]*>//g' | head -c "$max_chars")
        echo "$cmd"
        return
    fi
    # Default: first N chars of user text
    echo "$raw" | head -c "$max_chars"
}

# Relative time from epoch seconds: "2m ago", "1h ago", etc.
relative_time() {
    local mtime_epoch="$1"
    local now_epoch
    now_epoch=$(date +%s)
    local diff=$(( now_epoch - mtime_epoch ))
    if [ "$diff" -lt 60 ]; then
        echo "${diff}s ago"
    elif [ "$diff" -lt 3600 ]; then
        echo "$(( diff / 60 ))m ago"
    elif [ "$diff" -lt 86400 ]; then
        echo "$(( diff / 3600 ))h ago"
    else
        echo "$(( diff / 86400 ))d ago"
    fi
}

# Get mtime as epoch seconds
mtime_epoch() {
    stat -f "%m" "$1" 2>/dev/null || stat -c "%Y" "$1" 2>/dev/null || echo 0
}

# ─── list mode ─────────────────────────────────────────────────────────
# Collect session metadata into SESSION_FILES / SESSION_LABELS arrays.
# Called once; reused by both --list and --pick.
SESSION_FILES=()
SESSION_LABELS=()
collect_sessions() {
    local count=0
    while IFS= read -r f; do
        [ -z "$f" ] && continue
        count=$((count + 1))
        [ "$count" -gt 16 ] && break
        SESSION_FILES+=("$f")
    done < <(all_candidates)
}

list_sessions() {
    [ "${#SESSION_FILES[@]}" -eq 0 ] && collect_sessions
    printf "%s%sSessions (project + subagents within %dm)%s\n\n" \
           "$B" "$C_HEAD" "$SUBAGENT_WINDOW_MIN" "$R"
    printf "  %s#   %-10s %-8s  %7s  %s%s\n" "$C_META" "UPDATED" "KIND" "LINES" "TITLE" "$R"
    printf "  %s%s%s\n" "$C_SEP" "─────────────────────────────────────────────────────────────────────────────────" "$R"
    local i=0
    for f in "${SESSION_FILES[@]}"; do
        i=$((i + 1))
        local mepoch rel kind title lines
        mepoch=$(mtime_epoch "$f")
        rel=$(relative_time "$mepoch")
        lines=$(wc -l < "$f" | tr -d ' ')
        title=$(session_title "$f" 55)
        if [ "$f" = "$MAIN_SESSION" ]; then
            kind="main"
        elif is_subagent "$f"; then
            kind="agent"
        else
            kind="child"
        fi
        printf "  %s%s%-3d%s %s%-10s%s %s%-8s%s %s%5s%s  %s%s%s\n" \
               "$C_TNAME" "$B" "$i" "$R" \
               "$C_TIME" "$rel" "$R" \
               "$C_META" "$kind" "$R" \
               "$C_META" "$lines" "$R" \
               "" "$title" "$R"
    done
}

# MAIN_SESSION: the project-level session the user is typing into.
# Can't rely on mtime (child sessions may be newer during active orchestrator phases).
# Heuristic: among recent project-level sessions, main is the one whose first user
# message is NOT a phase prompt ("# PHASE Phase"). It's also typically the longest.
detect_main_session() {
    for f in $(ls -t "$PROJECT_DIR"/*.jsonl 2>/dev/null | head -5); do
        local first_prompt
        first_prompt=$(head -50 "$f" \
            | jq -r 'select(.type=="user") | .message.content | if type == "string" then . else (.[0].text // "") end' 2>/dev/null \
            | head -1)
        # Phase prompts start with "# " followed by uppercase phase name
        if [[ "$first_prompt" != "# "* ]]; then
            echo "$f"
            return
        fi
    done
    # Fallback: most recent
    ls -t "$PROJECT_DIR"/*.jsonl 2>/dev/null | head -1
}
MAIN_SESSION=$(detect_main_session)

if $LIST; then
    collect_sessions
    list_sessions
    exit 0
fi

# ─── interactive picker ───────────────────────────────────────────────
if $PICK && [ -z "$SESSION_MATCH" ]; then
    collect_sessions
    if [ "${#SESSION_FILES[@]}" -eq 0 ]; then
        echo "No sessions found." >&2
        exit 1
    fi
    list_sessions
    echo ""
    printf "%s%s↳ Enter number (or Enter for 1): %s" "$B" "$C_HEAD" "$R"
    read -r PICK_NUM
    PICK_NUM="${PICK_NUM:-1}"
    if ! [[ "$PICK_NUM" =~ ^[0-9]+$ ]] || [ "$PICK_NUM" -lt 1 ] || [ "$PICK_NUM" -gt "${#SESSION_FILES[@]}" ]; then
        echo "Invalid selection: $PICK_NUM" >&2
        exit 1
    fi
    FILE="${SESSION_FILES[$((PICK_NUM - 1))]}"
# ─── explicit session match ───────────────────────────────────────────
elif [ -n "$SESSION_MATCH" ]; then
    FILE=$(all_candidates | grep -m1 "/${SESSION_MATCH}[^/]*\.jsonl$")
    if [ -z "$FILE" ]; then
        echo "No session matching: $SESSION_MATCH" >&2
        echo "Try: bash $0 --list" >&2
        exit 1
    fi
# ─── auto-pick (non-interactive / piped) ──────────────────────────────
else
    if $INCLUDE_MAIN; then
        FILE="$MAIN_SESSION"
    else
        FILE=$(all_candidates | grep -v "^${MAIN_SESSION}$" | head -1)
        [ -z "$FILE" ] && FILE="$MAIN_SESSION"
    fi
fi

[ -f "$FILE" ] || { echo "No session file found. Try --list" >&2; exit 1; }

SESSION_ID=$(basename "$FILE" .jsonl)
FILE_SIZE=$(wc -l < "$FILE" | tr -d ' ')

# Classify session: main / child (orchestrator phase) / sub-agent (Coder Task etc.)
if [ "$FILE" = "$MAIN_SESSION" ]; then
    SESSION_KIND="main"
    SESSION_HINT="your conversation — ❯ rows are what you typed"
elif is_subagent "$FILE"; then
    SESSION_KIND="sub-agent"
    SESSION_HINT="Task/Agent sub-agent (Coder etc.) — ❯ row is the task prompt"
else
    SESSION_KIND="child"
    SESSION_HINT="orchestrator phase child — ❯ rows are phase prompts. For your typed messages: bash $(basename "$0") --all"
fi

# ─── git log snapshot ──────────────────────────────────────────────────
print_git_log() {
    command -v git >/dev/null 2>&1 || return 0
    git rev-parse --git-dir >/dev/null 2>&1 || return 0
    printf "%s%s● git log (HEAD~10..HEAD)%s\n" "$B" "$C_HEAD" "$R"
    git log --oneline --decorate --color=always -10 2>/dev/null | sed "s/^/  /"
    local uncommitted
    uncommitted=$(git status --short 2>/dev/null | wc -l | tr -d ' ')
    if [ "$uncommitted" -gt 0 ]; then
        printf "  %s%s uncommitted%s\n" "$D$C_META" "$uncommitted" "$R"
    fi
}

# ─── header ────────────────────────────────────────────────────────────
SEP_LINE=$(printf '─%.0s' $(seq 1 "$TERM_WIDTH"))
printf "%s%s━━━ session %s [%s] ━━━%s\n" "$B" "$C_HEAD" "$SESSION_ID" "$SESSION_KIND" "$R"
printf "%s%s • %s lines • %s%s\n" "$C_META" "$FILE" "$FILE_SIZE" \
       "$(if $FOLLOW; then echo 'follow'; else echo 'no-follow'; fi)" "$R"
printf "%s↳ %s%s\n" "$C_META" "$SESSION_HINT" "$R"
echo ""
print_git_log
echo ""
printf "%s%s%s\n" "$C_SEP" "$SEP_LINE" "$R"

# ─── jq filter: emit TSV with base64-encoded content ───────────────────
# Columns: TIME \t KIND \t NAME \t BASE64CONTENT
FILTER='
  def basename(path): path | tostring | split("/") | .[-1] // "";
  def fmt_input(name; inp):
    if name == "Read" then basename(inp.file_path // "")
      + (if inp.offset then ":\(inp.offset)" else "" end)
      + (if inp.limit then "+\(inp.limit)" else "" end)
    elif name == "Edit" or name == "NotebookEdit" then basename(inp.file_path // "")
    elif name == "Write" then
      "\(basename(inp.file_path // "")) [\(((inp.content // "") | length))b]"
    elif name == "Bash" then
      "$ \(inp.command // "")"
    elif name == "Grep" then
      "/\(inp.pattern // "")/"
        + (if inp.glob then " \(inp.glob)" elif inp.type then " [\(inp.type)]" else "" end)
        + (if inp.path then " in \(inp.path)" else "" end)
    elif name == "Glob" then (inp.pattern // "")
    elif name == "TodoWrite" then (inp.todos // [] | length | tostring) + " items"
    elif name == "Task" or name == "Agent" then
      "\(inp.description // inp.subagent_type // "") • \((inp.prompt // "")[0:80])"
    elif name == "SendMessage" then
      "to=\(inp.to // "?") • \((inp.message // "")[0:80])"
    elif name == "WebFetch" or name == "WebSearch" then
      (inp.url // inp.query // "")
    elif name == "ToolSearch" then (inp.query // "")
    elif name | test("^Task") then (inp.title // inp.description // inp.id // "")
    else (inp | tostring)[0:150] end;

  select(.type == "user" or .type == "assistant") |
  .timestamp as $ts |
  .type as $role |
  ($ts | split("T") | (if length > 1 then .[1] else "" end) | split(".") | .[0] // "??:??:??") as $time |
  ((.message.content // []) | if type == "string" then [{type:"text", text:.}] else . end) as $items |
  $items[] |
  (
    if .type == "text" then
      {kind: (if $role == "user" then "user" else "text" end), name: "", content: (.text // "")}
    elif .type == "tool_use" then
      {kind: "tool", name: (.name // "?"), content: (fmt_input(.name // "?"; .input // {}))}
    elif .type == "tool_result" then
      (.content | if type == "array" then map(.text // "") | join("\n") elif type == "string" then . else tostring end) as $c |
      {kind: (if (.is_error // false) then "error" else "result" end), name: "", content: $c}
    elif .type == "thinking" then
      {kind: "think", name: "", content: (.thinking // "")}
    else empty end
  ) |
  select((.content | length) > 0) |
  "\($time)|\(.kind)|\(.name)|\(.content | @base64)"
'

# ─── formatter: read TSV, wrap/truncate, colorize ──────────────────────

# Print wrapped multi-line content with prefix on first line, indent on rest.
# Usage: print_wrapped TIME KIND ICON COLOR [LINE_COLOR] [MAX_LINES]
# Reads content from stdin.
print_wrapped() {
    local time="$1" kind="$2" icon="$3" color="$4"
    local line_color="$5" max_lines="${6:-0}"
    local first=1 line_num=0

    while IFS= read -r line || [ -n "$line" ]; do
        line_num=$((line_num + 1))
        if [ "$max_lines" -gt 0 ] && [ "$line_num" -gt "$max_lines" ]; then
            printf "%s%s%s%s  … (%d more lines)%s\n" "$INDENT" "$D" "$C_META" "$line_color" $((line_num - max_lines)) "$R"
            return 0
        fi
        if [ "$first" -eq 1 ]; then
            printf "%s%s%s  %s%s%s %s%s%s\n" \
                "$C_TIME" "$time" "$R" "$color" "$icon" "$R" \
                "$line_color" "$line" "$R"
            first=0
        else
            printf "%s%s%s%s\n" "$INDENT" "$line_color" "$line" "$R"
        fi
    done < <(fold -s -w "$WRAP_WIDTH")
}

format_event() {
    local time="$1" kind="$2" name="$3" content="$4"
    local icon color

    case "$kind" in
        user)   icon="❯"; color="$C_USER"  ;;
        text)   icon="▶"; color="$C_ASST"  ;;
        think)  icon="💭"; color="$C_THINK" ;;
        tool)   icon="⚙"; color="$C_TOOL"  ;;
        result) icon="↩"; color="$C_RSLT"  ;;
        error)  icon="✗"; color="$C_ERR"   ;;
        *)      icon="?"; color=""         ;;
    esac

    if [ "$kind" = "text" ] || [ "$kind" = "user" ]; then
        # Full content, word-wrapped, no truncation
        local lc=""
        [ "$kind" = "user" ] && lc="$color"
        printf "%s" "$content" | print_wrapped "$time" "$kind" "$icon" "$color" "$lc"

    elif [ "$kind" = "think" ]; then
        # Thinking: word-wrapped with optional line limit
        local lc="$D$I$C_THINK"
        local ml=0
        $FULL || ml="$THINK_LINES"
        printf "%s" "$content" | print_wrapped "$time" "$kind" "$icon" "$color" "$lc" "$ml"

    elif [ "$kind" = "tool" ]; then
        # Tool use: compact single-line, truncation only for very long commands
        local flat
        flat=$(printf "%s" "$content" | tr '\n\t\r' '   ' | tr -s ' ')
        local max_w="$TERM_WIDTH"
        $FULL && max_w=$((TERM_WIDTH * 3))
        if [ "${#flat}" -gt "$max_w" ]; then
            flat="${flat:0:$((max_w-1))}…"
        fi
        printf "%s%s%s  %s%s%s %s%s%s %s%s%s\n" \
            "$C_TIME" "$time" "$R" \
            "$color" "$icon" "$R" \
            "$C_TNAME" "$name" "$R" \
            "$D" "$flat" "$R"

    elif [ "$kind" = "result" ] || [ "$kind" = "error" ]; then
        # Tool result/error: multi-line word-wrapped, header with byte/line summary
        local byte_count=${#content}
        local line_count
        line_count=$(printf "%s" "$content" | wc -l | tr -d ' ')
        [ "$line_count" -eq 0 ] && line_count=1

        local lc="$D$color"
        local summary="[${byte_count}b ${line_count}L]"
        printf "%s%s%s  %s%s%s %s%s%s\n" \
            "$C_TIME" "$time" "$R" \
            "$color" "$icon" "$R" \
            "$C_META" "$summary" "$R"

        # Print full content indented, word-wrapped, no line limit
        local content_lines
        content_lines=$(printf "%s" "$content" | fold -s -w "$WRAP_WIDTH")
        while IFS= read -r line || [ -n "$line" ]; do
            printf "%s%s%s%s\n" "$INDENT" "$lc" "$line" "$R"
        done <<< "$content_lines"
    fi
}

process_stream() {
    jq -r --unbuffered "$FILTER" 2>/dev/null | while IFS='|' read -r time kind name b64; do
        [ -z "$b64" ] && continue
        content=$(printf "%s" "$b64" | base64 -d 2>/dev/null)
        [ -z "$content" ] && continue
        format_event "$time" "$kind" "$name" "$content"
    done
}

# Process lines [from, to] of a file through the formatter.
process_lines() {
    local f="$1" from="$2" to="$3"
    local count=$((to - from))
    [ "$count" -le 0 ] && return
    tail -n +"$((from + 1))" "$f" | head -n "$count" | process_stream
}

# Print a compact session-switch banner.
print_switch_banner() {
    local f="$1"
    local title kind rel mepoch
    title=$(session_title "$f" 50)
    mepoch=$(mtime_epoch "$f")
    rel=$(relative_time "$mepoch")
    if [ "$f" = "$MAIN_SESSION" ]; then kind="main"
    elif is_subagent "$f"; then kind="agent"
    else kind="child"; fi
    printf "\n%s%s━━━ %s [%s] %s ━━━%s\n" "$B" "$C_HEAD" "$title" "$kind" "$rel" "$R"
}

# Find the best non-main candidate right now.
pick_active() {
    all_candidates | grep -v "^${MAIN_SESSION}$" | head -1
}

# ─── play history + optionally follow ──────────────────────────────────
if ! $FOLLOW; then
    # One-shot: dump and exit
    process_stream < "$FILE"
else
    # Auto-follow mode: poll every second, switch sessions when a newer one appears.
    # No background processes, no tail -f — just incremental line reads.
    AUTO_FOLLOW=true
    # If user explicitly picked a session (via uuid arg or --main), don't auto-switch
    if [ -n "$SESSION_MATCH" ] || $INCLUDE_MAIN; then
        AUTO_FOLLOW=false
    fi

    current_file="$FILE"
    offset=0

    # --- Status bar state ---
    STATUS_TMPDIR=""
    STATUS_PID=""
    STATUS_RESULT=""
    STATUS_TICK=0

    if $STATUS_BAR; then
        STATUS_TMPDIR=$(mktemp -d)
        trap "rm -rf '$STATUS_TMPDIR' 2>/dev/null; kill '$STATUS_PID' 2>/dev/null" EXIT
        printf "%s%s⚡ status bar: summarizing every %ss via Haiku%s\n" "$B" "$C_HEAD" "$STATUS_INTERVAL" "$R"
    fi

    # Check for completed Haiku result and print it.
    check_status_result() {
        $STATUS_BAR || return
        if [ -n "$STATUS_PID" ] && ! kill -0 "$STATUS_PID" 2>/dev/null; then
            if [ -f "$STATUS_TMPDIR/result" ]; then
                local result
                result=$(head -1 "$STATUS_TMPDIR/result" 2>/dev/null | head -c 100)
                if [ -n "$result" ] && [ "$result" != "$STATUS_RESULT" ]; then
                    STATUS_RESULT="$result"
                    printf "%s%s%s  %s⚡ %s%s\n" \
                        "$C_TIME" "$(date '+%H:%M:%S')" "$R" \
                        "$B$C_HEAD" "$result" "$R"
                fi
            fi
            STATUS_PID=""
            rm -f "$STATUS_TMPDIR/result" "$STATUS_TMPDIR/prompt"
        fi
    }

    # Extract concise recent activity from the current JSONL session file.
    # Output: last ~8 tool calls as "ToolName: brief_input" lines (lightweight, no full content).
    extract_recent_activity() {
        local f="$1"
        tail -50 "$f" 2>/dev/null | jq -r '
            select(.type == "assistant") |
            (.message.content // []) | if type == "array" then .[] else empty end |
            select(.type == "tool_use") |
            if .name == "Bash" then "Bash: \(.input.command // "" | .[0:80])"
            elif .name == "Read" then "Read: \(.input.file_path // "" | split("/") | .[-1])"
            elif .name == "Edit" then "Edit: \(.input.file_path // "" | split("/") | .[-1])"
            elif .name == "Write" then "Write: \(.input.file_path // "" | split("/") | .[-1])"
            elif .name == "Grep" then "Grep: /\(.input.pattern // "")/"
            elif .name == "Glob" then "Glob: \(.input.pattern // "")"
            elif .name == "Agent" then "Agent: \(.input.description // "")"
            elif .name == "WebSearch" then "WebSearch: \(.input.query // "")"
            else "\(.name): \(.input | tostring | .[0:60])"
            end
        ' 2>/dev/null | tail -8
    }

    # Launch a Haiku summarization (non-blocking, background).
    launch_status_summary() {
        $STATUS_BAR || return
        [ -n "$STATUS_PID" ] && kill -0 "$STATUS_PID" 2>/dev/null && return

        # Prefer parent child session for status (sub-agents switch too fast).
        # Fall back to current_file if no parent child found.
        local summary_file
        summary_file=$(all_candidates | grep -v "^${MAIN_SESSION}$" | grep -v "/subagents/" | head -1)
        [ -z "$summary_file" ] && summary_file="$current_file"

        local recent
        recent=$(extract_recent_activity "$summary_file")
        [ -z "$recent" ] && return

        cat > "$STATUS_TMPDIR/prompt" <<PROMPT_EOF
Based on these recent tool calls from a coding agent, write ONE sentence (under 80 chars, in Chinese) describing what the agent is currently doing. Be specific: mention file names, function names, techniques. Output ONLY the sentence.

RECENT TOOL CALLS:
$recent
PROMPT_EOF

        # Run from /tmp so Haiku session doesn't pollute the project's session dir
        (cd /tmp && claude -p "$(cat "$STATUS_TMPDIR/prompt")" --model haiku > "$STATUS_TMPDIR/result" 2>/dev/null) &
        STATUS_PID=$!
    }

    # Process existing content first
    total=$(wc -l < "$current_file" | tr -d ' ')
    if [ "$total" -gt 0 ]; then
        process_lines "$current_file" 0 "$total"
        offset=$total
    fi

    # Poll loop
    while true; do
        sleep 1
        STATUS_TICK=$((STATUS_TICK + 1))

        # Check for completed status summary
        check_status_result

        # Check for session switch (only in auto mode)
        if $AUTO_FOLLOW; then
            new_best=$(pick_active)
            if [ -n "$new_best" ] && [ "$new_best" != "$current_file" ]; then
                # Drain remaining lines from old session
                total=$(wc -l < "$current_file" | tr -d ' ')
                if [ "$total" -gt "$offset" ]; then
                    process_lines "$current_file" "$offset" "$total"
                fi
                # Switch
                print_switch_banner "$new_best"
                current_file="$new_best"
                offset=0
                STATUS_BUFFER=""
                total=$(wc -l < "$current_file" | tr -d ' ')
                if [ "$total" -gt 0 ]; then
                    process_lines "$current_file" 0 "$total"
                    offset=$total
                fi
                continue
            fi
        fi

        # Process new lines in current file
        total=$(wc -l < "$current_file" | tr -d ' ')
        if [ "$total" -gt "$offset" ]; then
            process_lines "$current_file" "$offset" "$total"
            offset=$total
        fi

        # Launch status summary periodically
        if $STATUS_BAR && [ "$((STATUS_TICK % STATUS_INTERVAL))" -eq 0 ]; then
            launch_status_summary
        fi
    done
fi
