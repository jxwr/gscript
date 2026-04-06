#!/bin/bash
# review_dump.sh — Dump all files REVIEW needs to read, in one shot.
# Reduces API calls from ~25 (one per Read) to 1 (one Bash).
#
# Usage: bash scripts/review_dump.sh [--session] [--files] [--all]
#   --session   Dump user session messages only
#   --files     Dump workflow files only
#   --all       Both (default)

set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MODE="${1:---all}"

# ─── Session log extraction ───────────────────────────────────────────
dump_session() {
    echo "================================================================"
    echo "=== USER SESSION LOG (last 100 messages) ==="
    echo "================================================================"

    local PROJECT_SLUG
    PROJECT_SLUG="$(echo "$ROOT" | sed 's|[/_.]|-|g')"
    local PROJECT_DIR="$HOME/.claude/projects/$PROJECT_SLUG"

    # Find main session (first non-phase-prompt session)
    local MAIN=""
    for f in $(ls -t "$PROJECT_DIR"/*.jsonl 2>/dev/null | head -5); do
        local first
        first=$(head -50 "$f" | jq -r 'select(.type=="user") | .message.content | if type == "string" then . else (.[0].text // "") end' 2>/dev/null | head -1)
        if [[ "$first" != "# "* ]]; then
            MAIN="$f"
            break
        fi
    done

    if [ -n "$MAIN" ]; then
        echo "Source: $(basename "$MAIN")"
        echo ""
        jq -r 'select(.type=="user" and (.message.content|type)=="string") | "\(.timestamp | split("T")[1][:8]) \(.message.content)"' "$MAIN" 2>/dev/null | tail -100
    else
        echo "(no main session found)"
    fi
    echo ""
}

# ─── Workflow files dump ──────────────────────────────────────────────
dump_files() {
    echo "================================================================"
    echo "=== WORKFLOW FILES (batch read) ==="
    echo "================================================================"
    echo ""

    # List of all files REVIEW needs for consistency audit
    local FILES=(
        "$ROOT/README.md"
        "$ROOT/CLAUDE.md"
        "$ROOT/.claude/optimize.sh"
        "$ROOT/.claude/prompts/analyze.md"
        "$ROOT/.claude/prompts/implement.md"
        "$ROOT/.claude/prompts/verify.md"
        "$ROOT/.claude/prompts/review.md"
        "$ROOT/.claude/hooks/phase_guard.sh"
        "$ROOT/docs-internal/architecture/overview.md"
        "$ROOT/docs-internal/architecture/constraints.md"
        "$ROOT/docs-internal/diagnostics/debug-ir-pipeline.md"
        "$ROOT/docs-internal/known-issues.md"
        "$ROOT/docs-internal/lessons-learned.md"
        "$ROOT/opt/state.json"
        "$ROOT/opt/INDEX.md"
        "$ROOT/opt/plan_template.md"
        "$ROOT/opt/initiatives/_template.md"
        "$ROOT/opt/workflow_log.jsonl"
    )

    # Skills (dynamic)
    for f in "$ROOT"/.claude/skills/*/SKILL.md; do
        [ -f "$f" ] && FILES+=("$f")
    done

    # Recent review (most recent only)
    local latest_review
    latest_review=$(ls -t "$ROOT"/opt/reviews/2026-*.md 2>/dev/null | head -1)
    [ -n "$latest_review" ] && FILES+=("$latest_review")

    # Dump each file with clear separators
    local total=0
    for f in "${FILES[@]}"; do
        if [ -f "$f" ]; then
            local rel="${f#$ROOT/}"
            local lines
            lines=$(wc -l < "$f" | tr -d ' ')
            echo "──── $rel ($lines lines) ────"
            cat "$f"
            echo ""
            total=$((total + 1))
        fi
    done

    echo "================================================================"
    echo "=== DIRECTORY LISTINGS ==="
    echo "================================================================"
    echo ""
    echo "── opt/initiatives/ ──"
    ls -la "$ROOT"/opt/initiatives/*.md 2>/dev/null | grep -v "_template\|README"
    echo ""
    echo "── opt/plans/ (last 5) ──"
    ls -t "$ROOT"/opt/plans/*.md 2>/dev/null | head -5
    echo ""
    echo "── opt/reviews/ ──"
    ls -t "$ROOT"/opt/reviews/*.md 2>/dev/null | head -5
    echo ""
    echo "── .claude/skills/ ──"
    ls -d "$ROOT"/.claude/skills/*/ 2>/dev/null
    echo ""
    echo "── opt/reviews/pending-changes/ ──"
    ls "$ROOT"/opt/reviews/pending-changes/ 2>/dev/null || echo "(empty)"
    echo ""
    echo "── opt/ top-level (check for stale temp files) ──"
    ls "$ROOT"/opt/*.md "$ROOT"/opt/*.json "$ROOT"/opt/*.jsonl 2>/dev/null

    echo ""
    echo "================================================================"
    echo "Total files dumped: $total"
    echo "================================================================"
}

# ─── Main ─────────────────────────────────────────────────────────────
case "$MODE" in
    --session) dump_session ;;
    --files)   dump_files ;;
    --all)     dump_session; dump_files ;;
    *)         dump_session; dump_files ;;
esac
