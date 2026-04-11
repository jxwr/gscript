#!/usr/bin/env python3
# Shared helper: render opt/state.json with previous_rounds capped to last 10.
# INDEX.md carries full history. Used by analyze_dump.sh, verify_dump.sh, review_dump.sh.
import json, sys

path = sys.argv[1] if len(sys.argv) > 1 else "opt/state.json"
with open(path) as f:
    data = json.load(f)
pr = data.get("previous_rounds", [])
if len(pr) > 10:
    data["previous_rounds"] = pr[-10:]
    data["_previous_rounds_truncated"] = f"kept last 10 of {len(pr)} - see opt/INDEX.md for full history"
print(json.dumps(data, indent=2))
