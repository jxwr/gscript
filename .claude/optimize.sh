#!/bin/bash
# optimize.sh — GScript optimization loop orchestrator
# Each phase runs as an independent Claude session (no context accumulation).
# State is passed via files between phases.
#
# Usage:
#   bash .claude/optimize.sh                # full cycle
#   bash .claude/optimize.sh --from=analyze # start from a specific phase
#   bash .claude/optimize.sh --dry-run      # show what would run

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PROMPTS="$ROOT/.claude/prompts"
PHASES=(measure analyze plan implement verify document)

# --- Parse args ---
FROM="measure"
DRY_RUN=false
while [[ $# -gt 0 ]]; do
    case $1 in
        --from=*) FROM="${1#*=}" ;;
        --dry-run) DRY_RUN=true ;;
        --help) echo "Usage: optimize.sh [--from=phase] [--dry-run]"; exit 0 ;;
        *) echo "Unknown arg: $1"; exit 1 ;;
    esac
    shift
done

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

# --- Main ---
echo "GScript Optimization Loop"
echo "Starting from: $FROM"

SKIPPING=true
for phase in "${PHASES[@]}"; do
    [ "$phase" = "$FROM" ] && SKIPPING=false
    $SKIPPING && continue

    # Pre-phase gate checks
    case $phase in
        analyze)
            check_output "measure" ".claude/measure_report.md" || exit 1
            ;;
        plan)
            check_output "analyze" ".claude/analyze_report.md" || exit 1
            ;;
        implement)
            check_output "plan" ".claude/current_plan.md" || exit 1
            echo ""
            echo "=== GATE: PLAN → IMPLEMENT ==="
            echo "Review: .claude/current_plan.md"
            if $DRY_RUN; then
                echo "[DRY RUN] Would pause for human approval"
                run_phase "$phase"
                continue
            fi
            echo ""
            read -rp "Approve and proceed? (y/n) " answer
            if [ "$answer" != "y" ]; then
                echo "Aborted. Edit the plan and re-run: --from=implement"
                exit 0
            fi
            ;;
        verify)
            check_output "implement" ".claude/current_plan.md" || exit 1
            ;;
        document)
            check_output "verify" ".claude/current_plan.md" || exit 1
            ;;
    esac

    run_phase "$phase" || {
        echo ""
        echo "Stopped at phase: $phase"
        echo "Resume: bash .claude/optimize.sh --from=$phase"
        exit 1
    }
done

echo ""
echo "=== Optimization cycle complete ==="
echo "Start next cycle: bash .claude/optimize.sh"
