#!/bin/bash
# optimize.sh — GScript optimization loop orchestrator
# Each phase runs as an independent Claude session (no context accumulation).
# State is passed via files between phases.
#
# Phases (4):
#   analyze   — classify gaps + research + read source + diagnose + write plan
#   implement — execute plan tasks (spawn Coder sub-agents)
#   verify    — tests + benchmarks + evaluator + close out round (INDEX/state/archive)
#   sanity    — independent common-sense check: did VERIFY actually close out? is the
#               data physically consistent with the plan? blocks auto-continue if not.
#
# Conditional:
#   review    — (every REVIEW_INTERVAL rounds) harness self-audit, runs before analyze
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
PHASES=(analyze implement verify sanity)

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
## Progress monitor: runs in background during each phase, prints status every MONITOR_INTERVAL seconds.
MONITOR_INTERVAL="${MONITOR_INTERVAL:-600}"  # default 10 minutes

start_monitor() {
    local phase=$1
    (
        sleep "$MONITOR_INTERVAL"
        while true; do
            echo ""
            echo "  [monitor $(date '+%H:%M:%S')] phase=$phase still running..."
            # Show latest child session activity
            local latest_child
            latest_child=$(ls -t "$ROOT/.claude/../.claude/projects/$(echo "$ROOT" | sed 's|[/_.]|-|g')"/*.jsonl 2>/dev/null | head -2 | tail -1)
            if [ -n "$latest_child" ] && [ -f "$latest_child" ]; then
                local lines mtime
                lines=$(wc -l < "$latest_child" | tr -d ' ')
                mtime=$(stat -f '%Sm' -t '%H:%M:%S' "$latest_child" 2>/dev/null || echo "?")
                echo "  [monitor] child session: ${lines}L, last write: $mtime"
            fi
            # Token usage
            if [ -f "$ROOT/scripts/token_usage.sh" ]; then
                local tokens
                tokens=$(bash "$ROOT/scripts/token_usage.sh" --last 2>/dev/null | grep "GRAND TOTAL" | awk '{print $NF}')
                [ -n "$tokens" ] && echo "  [monitor] tokens this round: $tokens"
            fi
            sleep "$MONITOR_INTERVAL"
        done
    ) &
    MONITOR_PID=$!
}

stop_monitor() {
    if [ -n "${MONITOR_PID:-}" ]; then
        kill "$MONITOR_PID" 2>/dev/null
        wait "$MONITOR_PID" 2>/dev/null
        MONITOR_PID=""
    fi
}

run_phase() {
    local phase=$1
    local prompt_file="$PROMPTS/${phase}.md"

    if [ ! -f "$prompt_file" ]; then
        echo "ERROR: prompt file not found: $prompt_file"
        exit 1
    fi

    # All phases use Opus 4.6. Sonnet downgrade for REVIEW/IMPLEMENT/VERIFY
    # proved too weak — workflow quality suffered more than it saved tokens.
    PHASE_MODEL="claude-opus-4-6"

    local start_iso start_epoch
    start_iso=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
    start_epoch=$(date '+%s')
    local cycle_id
    cycle_id=$(python3 -c "import json; print(json.load(open('$STATE')).get('cycle_id','') or '')" 2>/dev/null || echo "")

    echo ""
    echo "================================================"
    echo "  Phase: $phase  [model: $PHASE_MODEL]"
    echo "  Time:  $(date '+%H:%M:%S')"
    echo "================================================"
    echo ""

    if $DRY_RUN; then
        echo "[DRY RUN] Would run: claude -p \"$(head -1 "$prompt_file")...\""
        return 0
    fi

    # Append phase-start record to phase_log.jsonl (mechanical, independent of agent).
    python3 -c "
import json
rec={'cycle_id':'$cycle_id','phase':'$phase','model':'$PHASE_MODEL','event':'start','ts':'$start_iso'}
open('$ROOT/opt/phase_log.jsonl','a').write(json.dumps(rec)+'\n')
" 2>/dev/null || true

    start_monitor "$phase"

    claude -p "$(cat "$prompt_file")" \
        --model "$PHASE_MODEL" \
        --dangerously-skip-permissions \
        --allowedTools "Bash,Read,Write,Edit,Glob,Grep,WebSearch,Agent"

    local exit_code=$?
    stop_monitor

    local end_iso end_epoch duration
    end_iso=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
    end_epoch=$(date '+%s')
    duration=$((end_epoch - start_epoch))

    # After phase runs, cycle_id may have changed (verify clears it). Re-read for end record.
    local end_cycle_id
    end_cycle_id=$(python3 -c "import json; print(json.load(open('$STATE')).get('cycle_id','') or '')" 2>/dev/null || echo "")
    # Prefer the non-empty one
    local log_cycle_id="${cycle_id:-$end_cycle_id}"

    python3 -c "
import json
rec={'cycle_id':'$log_cycle_id','phase':'$phase','model':'$PHASE_MODEL','event':'end','ts':'$end_iso','duration_s':$duration,'exit_code':$exit_code}
open('$ROOT/opt/phase_log.jsonl','a').write(json.dumps(rec)+'\n')
" 2>/dev/null || true

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

    # Conditional REVIEW (before analyze, every REVIEW_INTERVAL rounds)
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
                # Pre-flight: clear stale stash entries and snapshot baseline test state
                if ! $DRY_RUN; then
                    stash_count=$(git stash list | wc -l | tr -d ' ')
                    if [ "$stash_count" -gt 0 ]; then
                        echo "  [pre-flight] Clearing $stash_count stale stash entries..."
                        git stash clear
                    fi
                    # Snapshot which tests fail on HEAD (so Coder knows what's pre-existing)
                    echo "  [pre-flight] Recording baseline test failures..."
                    go test ./internal/methodjit/... -short -count=1 -timeout 60s 2>&1 | grep -E "^(FAIL|PASS|---)" | head -20 > opt/baseline_test_snapshot.txt || true
                    echo "  [pre-flight] Baseline snapshot: opt/baseline_test_snapshot.txt"
                fi
                echo ""
                echo "=== ANALYZE+PLAN → IMPLEMENT ==="
                echo "Plan: opt/current_plan.md"
                echo ""
                ;;
            verify)
                check_output "implement" "opt/current_plan.md" || return 1
                ;;
            sanity)
                # sanity is independent — reads artifacts only, no code execution.
                # Runs even if verify didn't fully close out, so it can detect that.
                ;;
        esac

        run_phase "$phase" || {
            echo ""
            echo "Stopped at phase: $phase"
            echo "Resume: bash .claude/optimize.sh --from=$phase"
            return 1
        }

        # Post-sanity gate: if verdict is not clean, halt auto-continue.
        if [ "$phase" = "sanity" ] && ! $DRY_RUN; then
            verdict=$(python3 -c "import json; print(json.load(open('$STATE')).get('sanity_verdict','missing'))" 2>/dev/null || echo "missing")
            echo ""
            echo "=== Sanity verdict: $verdict ==="
            if [ "$verdict" != "clean" ]; then
                echo ""
                echo "Sanity check flagged issues. Review: opt/sanity_report.md"
                echo "Auto-continue is BLOCKED. Fix problems and resume manually, or re-run sanity."
                return 2
            fi
        fi
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

    run_cycle "$CYCLE_FROM"
    rc=$?
    if [ $rc -eq 2 ]; then
        echo ""
        echo "=== Multi-round run halted by sanity check at round $round/$ROUNDS ==="
        echo "Inspect opt/sanity_report.md, fix, then resume:  bash .claude/optimize.sh --rounds=$((ROUNDS-round+1))"
        exit 2
    elif [ $rc -ne 0 ]; then
        echo ""
        echo "=== Multi-round run stopped at round $round/$ROUNDS ==="
        exit 1
    fi

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
