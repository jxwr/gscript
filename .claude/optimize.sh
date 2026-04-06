#!/bin/bash
# optimize.sh — GScript optimization loop orchestrator
# Each phase runs as an independent Claude session (no context accumulation).
# State is passed via files between phases.
#
# Phases (3):
#   analyze   — classify gaps + research + read source + diagnose + write plan
#   implement — execute plan tasks (spawn Coder sub-agents)
#   verify    — tests + benchmarks + evaluator + close out round (INDEX/state/archive)
#
# Conditional:
#   review    — (every 5 rounds) harness self-audit, runs before analyze
#
# Usage:
#   bash .claude/optimize.sh                # one full cycle
#   bash .claude/optimize.sh --rounds=5     # run up to 5 cycles back-to-back
#   bash .claude/optimize.sh --from=implement  # resume from a specific phase
#   bash .claude/optimize.sh --dry-run      # show what would run
#   bash .claude/optimize.sh --review       # force review phase
#   bash .claude/optimize.sh --no-review    # skip review even if due
#
# Multi-round: round 1 honors --from=, rounds 2..N start from analyze.
# Any phase failure stops the run.

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PROMPTS="$ROOT/.claude/prompts"
STATE="$ROOT/opt/state.json"
PHASES=(analyze implement verify)

# --- Parse args ---
FROM="analyze"
DRY_RUN=false
FORCE_REVIEW=false
SKIP_REVIEW=false
ROUNDS=1
while [[ $# -gt 0 ]]; do
    case $1 in
        --from=*) FROM="${1#*=}" ;;
        --rounds=*) ROUNDS="${1#*=}" ;;
        --dry-run) DRY_RUN=true ;;
        --review) FORCE_REVIEW=true ;;
        --no-review) SKIP_REVIEW=true ;;
        --help)
            grep '^#' "$0" | sed 's/^# \?//' | head -25
            exit 0 ;;
        *) echo "Unknown arg: $1"; exit 1 ;;
    esac
    shift
done

if ! [[ "$ROUNDS" =~ ^[0-9]+$ ]] || [ "$ROUNDS" -lt 1 ]; then
    echo "Error: --rounds must be a positive integer (got: $ROUNDS)"
    exit 1
fi

# --- Validate --from phase ---
VALID=false
for p in "${PHASES[@]}"; do
    [ "$p" = "$FROM" ] && VALID=true
done
if ! $VALID; then
    echo "Error: unknown phase '$FROM'. Valid: ${PHASES[*]}"
    exit 1
fi

# --- Utility ---
run_phase() {
    local phase=$1
    local prompt_file="$PROMPTS/${phase}.md"

    if [ ! -f "$prompt_file" ]; then
        echo "ERROR: prompt file not found: $prompt_file"
        exit 1
    fi

    echo ""
    echo "================================================"
    echo "  Phase: $phase"
    echo "  Time:  $(date '+%H:%M:%S')"
    echo "================================================"
    echo ""

    if $DRY_RUN; then
        echo "[DRY RUN] Would run: claude -p \"$(head -1 "$prompt_file")...\""
        return 0
    fi

    claude -p "$(cat "$prompt_file")" \
        --dangerously-skip-permissions \
        --allowedTools "Bash,Read,Write,Edit,Glob,Grep,WebSearch,Agent"

    local exit_code=$?
    if [ $exit_code -ne 0 ]; then
        echo ""
        echo "ERROR: phase '$phase' exited with code $exit_code"
        return $exit_code
    fi

    echo ""
    echo "--- Phase $phase complete ---"
}

check_output() {
    local phase=$1
    local file="$ROOT/$2"
    if $DRY_RUN; then
        echo "[DRY RUN] Would check: $2 exists"
        return 0
    fi
    if [ ! -f "$file" ]; then
        echo "ERROR: phase '$phase' did not produce: $2"
        return 1
    fi
    echo "OK: $file exists"
}

# --- Read counters from state.json ---
rounds_since_review() {
    python3 -c "import json; print(json.load(open('$STATE')).get('rounds_since_review', 0))" 2>/dev/null || echo 0
}

# --- Run one cycle ---
run_cycle() {
    local cycle_from="$1"

    # Conditional REVIEW (before analyze, every 5 rounds)
    # REVIEW runs every round in early stage (REVIEW_INTERVAL=1).
    # Increase to 3-5 once workflow stabilizes.
    local REVIEW_INTERVAL=1
    if [ "$cycle_from" = "analyze" ] && ! $SKIP_REVIEW; then
        SINCE_REVIEW=$(rounds_since_review)
        if $FORCE_REVIEW || [ "$SINCE_REVIEW" -ge "$REVIEW_INTERVAL" ]; then
            echo ""
            echo "=== Triggering REVIEW phase (rounds_since_review=$SINCE_REVIEW, interval=$REVIEW_INTERVAL) ==="
            run_phase "review" || {
                echo "REVIEW phase failed. Auto-continuing."
            }
        fi
    fi

    local skipping=true
    for phase in "${PHASES[@]}"; do
        [ "$phase" = "$cycle_from" ] && skipping=false
        $skipping && continue

        # Pre-phase gate checks
        case $phase in
            implement)
                check_output "analyze" "opt/current_plan.md" || return 1
                echo ""
                echo "=== ANALYZE+PLAN → IMPLEMENT ==="
                echo "Plan: opt/current_plan.md"
                echo ""
                ;;
            verify)
                check_output "implement" "opt/current_plan.md" || return 1
                ;;
        esac

        run_phase "$phase" || {
            echo ""
            echo "Stopped at phase: $phase"
            echo "Resume: bash .claude/optimize.sh --from=$phase"
            return 1
        }
    done
    return 0
}

# --- Main ---
echo "GScript Optimization Loop (3-phase)"
if [ "$ROUNDS" -gt 1 ]; then
    echo "Running up to $ROUNDS rounds (starting from: $FROM)"
else
    echo "Starting from: $FROM"
fi

CYCLE_FROM="$FROM"
for ((round=1; round<=ROUNDS; round++)); do
    if [ "$ROUNDS" -gt 1 ]; then
        echo ""
        echo "################################################"
        echo "#  Round $round of $ROUNDS"
        echo "#  $(date '+%Y-%m-%d %H:%M:%S')"
        echo "################################################"
    fi

    run_cycle "$CYCLE_FROM" || {
        echo ""
        echo "=== Multi-round run stopped at round $round/$ROUNDS ==="
        exit 1
    }

    echo ""
    echo "=== Round $round complete ==="

    # Rounds 2..N always start from analyze
    CYCLE_FROM="analyze"
done

if [ "$ROUNDS" -gt 1 ]; then
    echo ""
    echo "=== All $ROUNDS rounds complete ==="
else
    echo "Start next cycle: bash .claude/optimize.sh"
fi
