#!/bin/bash
# checkpoint.sh — Stop hook that writes opt/state.json
# Saves git + project state. Preserves optimization loop fields set by skills.
# Always exits 0 — must never block stopping.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
STATE_FILE="$ROOT/opt/state.json"

# --- Gather git state ---
HEAD_COMMIT=""
HEAD_COMMIT=$(git -C "$ROOT" rev-parse HEAD 2>/dev/null) || HEAD_COMMIT=""

BRANCH=""
BRANCH=$(git -C "$ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null) || BRANCH=""

# Uncommitted/modified files relative to HEAD
UNCOMMITTED_FILES=()
while IFS= read -r line; do
    [ -n "$line" ] && UNCOMMITTED_FILES+=("$line")
done < <(git -C "$ROOT" diff --name-only HEAD 2>/dev/null)

TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date +"%Y-%m-%dT%H:%M:%SZ")

# --- Build updated state.json using python3 ---
python3 - <<PYEOF
import json, sys

state_file = "$STATE_FILE"
head_commit = "$HEAD_COMMIT"
branch = "$BRANCH"
timestamp = "$TIMESTAMP"

# Uncommitted files passed via shell array
import subprocess
result = subprocess.run(
    ["git", "-C", "$ROOT", "diff", "--name-only", "HEAD"],
    capture_output=True, text=True
)
uncommitted_files = [f for f in result.stdout.splitlines() if f]

# Load existing state (preserve fields set by optimization loop)
existing = {}
try:
    with open(state_file, "r") as f:
        existing = json.load(f)
except Exception:
    pass

# Default structure
default = {
    "cycle": "",
    "cycle_id": "",
    "target": "",
    "started": "",
    "baseline": {},
    "plan_budget": {
        "max_commits": 0,
        "max_files": 0,
        "current_commits": 0,
        "current_files": 0
    },
    "completed_steps": [],
    "next_action": "",
    "uncommitted_files": [],
    "head_commit": "",
    "branch": "",
    "last_checkpoint": ""
}

# Merge: start from default, overlay existing, then update checkpoint fields
state = {**default, **existing}
state["head_commit"] = head_commit
state["branch"] = branch
state["uncommitted_files"] = uncommitted_files
state["last_checkpoint"] = timestamp

with open(state_file, "w") as f:
    json.dump(state, f, indent=2)
    f.write("\n")

print(f"Checkpoint saved: {timestamp} branch={branch} commit={head_commit[:8] if head_commit else 'none'} uncommitted={len(uncommitted_files)}")
PYEOF

# Always succeed — checkpoint must never block the stop event
exit 0
