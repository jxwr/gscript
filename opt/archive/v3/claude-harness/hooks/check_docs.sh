#!/bin/bash
# check_docs.sh — Documentation consistency checker for GScript
# Runs as a Stop hook: scans for drift between CLAUDE.md and supporting docs.
# Exit 0 = clean, exit 2 = issues found (session continues, Claude must fix)

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
ISSUES=""

log_issue() {
    ISSUES="${ISSUES}
[DOC DRIFT] $1"
}

log_warn() {
    ISSUES="${ISSUES}
[WARN] $1"
}

# --- Check 1: CLAUDE.md @import references must exist ---
check_references() {
    local claude_md="$ROOT/CLAUDE.md"
    [ -f "$claude_md" ] || return 0

    grep -oE '@[a-zA-Z0-9_./-]+\.md' "$claude_md" 2>/dev/null | sed 's/^@//' | while read -r ref; do
        if [ ! -f "$ROOT/$ref" ]; then
            log_issue "CLAUDE.md references @$ref but file does not exist"
        fi
    done || true
}

# --- Check 2: Deprecated modules should not appear as active ---
check_deprecated_refs() {
    local deprecated_patterns=(
        "internal/jit/"
        "TraceRecorder"
        "CompiledTrace"
        "ssa_codegen"
        "trace_record\.go"
        "trace_exec\.go"
    )

    for pattern in "${deprecated_patterns[@]}"; do
        if grep -q "$pattern" "$ROOT/CLAUDE.md" 2>/dev/null; then
            local context=$(grep -B2 -A2 "$pattern" "$ROOT/CLAUDE.md" 2>/dev/null)
            if ! echo "$context" | grep -qi "deprecated\|scheduled for deletion\|do not\|not.*use\|old\|legacy"; then
                log_warn "CLAUDE.md mentions '$pattern' without deprecation context"
            fi
        fi
    done || true
}

# --- Check 3: ADR cross-references must exist ---
check_adr_refs() {
    for adr in "$ROOT"/docs-internal/decisions/*.md; do
        [ -f "$adr" ] || continue
        local basename
        basename=$(basename "$adr")
        grep -oE 'docs-internal/[a-zA-Z0-9_./-]+\.md' "$adr" 2>/dev/null | while read -r ref; do
            if [ ! -f "$ROOT/$ref" ]; then
                log_issue "$basename references $ref which does not exist"
            fi
        done || true
        grep -oE 'docs/[0-9]+-[a-zA-Z0-9_-]+\.md' "$adr" 2>/dev/null | while read -r ref; do
            if [ ! -f "$ROOT/$ref" ]; then
                log_issue "$basename references blog $ref which does not exist"
            fi
        done || true
    done || true
}

# --- Check 4: Tier count consistency ---
check_tier_consistency() {
    local claude="$ROOT/CLAUDE.md"
    local overview="$ROOT/docs-internal/architecture/overview.md"
    [ -f "$claude" ] || return 0

    local tier_in_claude=0
    tier_in_claude=$(grep -c "Tier [012]" "$claude.md" 2>/dev/null) || tier_in_claude=0

    if [ -f "$overview" ]; then
        local tier_in_overview=0
        tier_in_overview=$(grep -c "Tier [012]" "$overview" 2>/dev/null) || tier_in_overview=0
        if [ "$tier_in_claude" -gt 0 ] && [ "$tier_in_overview" -gt 0 ]; then
            if [ "$tier_in_claude" != "$tier_in_overview" ]; then
                log_issue "Tier count differs: CLAUDE.md has $tier_in_claude tier refs, overview.md has $tier_in_overview"
            fi
        fi
    fi
}

# --- Check 5: Staleness warning (60+ days) ---
check_staleness() {
    local threshold_days=60
    local now
    now=$(date +%s)

    find "$ROOT/docs-internal" -name "*.md" -type f 2>/dev/null | while read -r doc; do
        local mtime
        mtime=$(stat -f '%m' "$doc" 2>/dev/null || echo 0)
        local age_days=$(( (now - mtime) / 86400 ))
        if [ "$age_days" -gt "$threshold_days" ]; then
            local relpath="${doc#$ROOT/}"
            log_warn "$relpath not updated in ${age_days} days — may be stale"
        fi
    done || true
}

# --- Run all checks ---
check_references
check_deprecated_refs
check_adr_refs
check_tier_consistency
check_staleness

# --- Report ---
if [ -n "$ISSUES" ]; then
    echo ""
    echo "=== Documentation Consistency Check ==="
    echo "$ISSUES"
    echo ""
    echo "Fix these issues before ending the session."
    echo ""
    exit 2
fi

echo "Documentation consistency check: OK"
exit 0
