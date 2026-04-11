#!/bin/bash
# freeze-reference.sh — freeze a reference benchmark baseline for sanity R7
#
# Usage: bash .claude/freeze-reference.sh <commit> [reason]
#   <commit>  — git commit whose benchmarks/data/baseline.json becomes the reference
#   [reason]  — short human reason for freezing (stored in _meta.frozen_reason)
#
# Effect:
#   1. Writes benchmarks/data/reference.json from historical baseline at <commit>
#   2. Computes SHA-256 of the file (excluding the volatile _meta section)
#   3. Updates opt/state.json.reference_baseline with {commit, timestamp, sha256, excluded}
#
# Excluded benchmarks (fib, fib_recursive, mutual_recursion, ackermann) are sanity-R7
# ignored because they are dominated by the 598bc1e correctness fix. They remain in
# reference.json for reference but sanity does not check cumulative drift on them.
#
# Per harness-core-principles P5: reference.json is IMMUTABLE once frozen. This
# script is the ONLY sanctioned way to re-freeze. Any direct edit to reference.json
# without re-running this script triggers sanity R7 FAIL via SHA mismatch.
#
# Safety:
#   - Refuses to overwrite existing reference.json unless --force is passed
#   - Appends a record to opt/knowledge/reference-history.jsonl on every invocation

set -uo pipefail
cd "$(dirname "$0")/.."

COMMIT="${1:-}"
REASON="${2:-no reason provided}"
FORCE=false
for arg in "$@"; do
    [ "$arg" = "--force" ] && FORCE=true
done

if [ -z "$COMMIT" ]; then
    echo "Usage: bash .claude/freeze-reference.sh <commit> [reason] [--force]"
    echo ""
    echo "Current reference:"
    if [ -f benchmarks/data/reference.json ]; then
        python3 -c "import json; d=json.load(open('benchmarks/data/reference.json')); m=d.get('_meta',{}); print(f\"  commit: {m.get('frozen_from_commit','?')[:8]}, at: {m.get('frozen_at','?')}, reason: {m.get('frozen_reason','?')}\")"
    else
        echo "  (none)"
    fi
    exit 1
fi

if [ -f benchmarks/data/reference.json ] && ! $FORCE; then
    echo "ERROR: benchmarks/data/reference.json already exists. Use --force to overwrite."
    echo "Current:"
    python3 -c "import json; d=json.load(open('benchmarks/data/reference.json')); m=d.get('_meta',{}); print(f\"  commit: {m.get('frozen_from_commit','?')[:8]}, reason: {m.get('frozen_reason','?')}\")"
    exit 1
fi

if ! git rev-parse --verify "$COMMIT" >/dev/null 2>&1; then
    echo "ERROR: $COMMIT is not a valid git commit."
    exit 1
fi

FULL_COMMIT=$(git rev-parse "$COMMIT")
SHORT_COMMIT=${FULL_COMMIT:0:8}
NOW=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

echo "Freezing reference from commit $SHORT_COMMIT..."

# Extract historical baseline.json from that commit
HISTORICAL=$(git show "$FULL_COMMIT":benchmarks/data/baseline.json 2>/dev/null)
if [ -z "$HISTORICAL" ]; then
    echo "ERROR: benchmarks/data/baseline.json does not exist at commit $SHORT_COMMIT"
    exit 1
fi

# Write reference.json with _meta section
python3 - <<PY
import json, hashlib, sys

historical = json.loads('''$HISTORICAL''')
reference = {
    "_meta": {
        "frozen_at": "$NOW",
        "frozen_from_commit": "$FULL_COMMIT",
        "frozen_from_baseline_commit": historical.get("commit", "?"),
        "frozen_from_baseline_ts": historical.get("timestamp", "?"),
        "frozen_reason": """$REASON""",
        "excluded": ["fib", "fib_recursive", "mutual_recursion", "ackermann"],
        "excluded_reason": "Dominated by 598bc1e correctness fix (deep-recursion goroutine stack overflow prevention). ackermann/mutual_recursion improved as side-effect; fib/fib_recursive regressed. Sanity R7 ignores these 4 benchmarks; they remain under separate tier1-call-overhead initiative Item 8.",
        "drift_threshold_flag_pct": 2.0,
        "drift_threshold_fail_pct": 5.0,
    },
    "results": historical.get("results", {}),
}

# Canonical serialization for SHA stability
canonical = json.dumps(reference, indent=2, sort_keys=True, ensure_ascii=False)
with open("benchmarks/data/reference.json", "w") as f:
    f.write(canonical + "\n")

# SHA-256 of the file (including _meta, for detecting ANY edit)
sha = hashlib.sha256(canonical.encode("utf-8") + b"\n").hexdigest()
print(f"  reference.json written ({len(historical.get('results',{}))} benchmarks)")
print(f"  sha256: {sha}")
print(f"  excluded: fib, fib_recursive, mutual_recursion, ackermann")

# Update state.json
with open("opt/state.json") as f:
    state = json.load(f)
state["reference_baseline"] = {
    "path": "benchmarks/data/reference.json",
    "sha256": sha,
    "frozen_at": "$NOW",
    "frozen_from_commit": "$FULL_COMMIT",
    "frozen_from_baseline_commit": historical.get("commit", "?"),
    "frozen_reason": """$REASON""",
    "excluded": ["fib", "fib_recursive", "mutual_recursion", "ackermann"],
}
with open("opt/state.json", "w") as f:
    json.dump(state, f, indent=2, ensure_ascii=False)
print(f"  state.json.reference_baseline updated")

# Append history record
with open("opt/knowledge/reference-history.jsonl", "a") as f:
    rec = {
        "frozen_at": "$NOW",
        "from_commit": "$FULL_COMMIT",
        "from_baseline_commit": historical.get("commit","?"),
        "reason": """$REASON""",
        "sha256": sha,
    }
    f.write(json.dumps(rec, ensure_ascii=False) + "\n")
print(f"  history appended to opt/knowledge/reference-history.jsonl")
PY

echo ""
echo "Done. Reference frozen."
echo "To check: python3 -c \"import json; print(json.load(open('opt/state.json'))['reference_baseline'])\""
