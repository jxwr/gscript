#!/bin/bash
# scripts/kb_check.sh — L2 KB card freshness check.
#
# For every card under kb/modules/*.md and kb/architecture.md:
#   1. Parse YAML frontmatter
#   2. For every `path:` entry under `files:`, verify the file exists
#   3. (Optional — currently not enforced) compare the current git blob SHA
#      against a recorded `sha:` subfield; mismatch means the card may be
#      stale and should be reviewed
#   4. Verify the card body has the required sections: Purpose / Public API /
#      Invariants / Hot paths / Known gaps / Tests
#
# Exit 0 = clean. Exit 1 = stale or malformed. A non-zero exit blocks
# round Step 3 per CLAUDE.md.
#
# Usage:
#   bash scripts/kb_check.sh            — check all cards
#   bash scripts/kb_check.sh --verbose  — print per-card results
#   bash scripts/kb_check.sh --strict   — require `sha:` subfield and enforce match

set -uo pipefail
cd "$(dirname "$0")/.."

VERBOSE=false
STRICT=false
for arg in "$@"; do
    case "$arg" in
        --verbose|-v) VERBOSE=true ;;
        --strict) STRICT=true ;;
    esac
done

python3 - "$VERBOSE" "$STRICT" <<'PY'
import os
import re
import sys
import subprocess
from pathlib import Path

verbose = sys.argv[1] == "true"
strict = sys.argv[2] == "true"

KB_ROOTS = [
    Path("kb/architecture.md"),
    Path("kb/modules"),
]

REQUIRED_SECTIONS = [
    "## Purpose",
    "## Public API",
    "## Invariants",
    "## Hot paths",
    "## Known gaps",
    "## Tests",
]

problems = []
cards_seen = 0

def git_blob_sha(path: Path) -> str | None:
    try:
        r = subprocess.run(
            ["git", "hash-object", str(path)],
            capture_output=True, text=True, check=True,
        )
        return r.stdout.strip()
    except subprocess.CalledProcessError:
        return None

def parse_frontmatter(text: str):
    if not text.startswith("---\n"):
        return None, text
    end = text.find("\n---\n", 4)
    if end == -1:
        return None, text
    fm_text = text[4:end]
    body = text[end + 5:]
    return fm_text, body

def check_card(card_path: Path):
    global cards_seen
    cards_seen += 1
    try:
        text = card_path.read_text()
    except OSError as e:
        problems.append(f"{card_path}: unreadable: {e}")
        return

    fm, body = parse_frontmatter(text)
    if fm is None:
        problems.append(f"{card_path}: missing YAML frontmatter")
        return

    # architecture.md uses `- path:` same as module cards.
    # Extract `files:` block — indented `- path:` entries.
    file_entries = []
    in_files = False
    for raw in fm.splitlines():
        line = raw.rstrip()
        if re.match(r"^files:\s*$", line):
            in_files = True
            continue
        if in_files:
            m = re.match(r"\s*-\s*path:\s*(.+)$", line)
            if m:
                file_entries.append(m.group(1).strip())
                continue
            m = re.match(r"\s*sha:\s*(.+)$", line)
            if m:
                if file_entries:
                    file_entries[-1] = (file_entries[-1], m.group(1).strip())
                continue
            if line and not line.startswith(" ") and not line.startswith("\t"):
                in_files = False

    # Normalize entries.
    for i, entry in enumerate(file_entries):
        if isinstance(entry, str):
            file_entries[i] = (entry, None)

    if not file_entries:
        problems.append(f"{card_path}: frontmatter has no `files:` entries")

    for path_str, recorded_sha in file_entries:
        p = Path(path_str)
        if not p.exists():
            problems.append(f"{card_path}: references missing file {path_str}")
            continue
        if strict and recorded_sha is None:
            problems.append(f"{card_path}: --strict requires sha: subfield for {path_str}")
            continue
        if recorded_sha is not None:
            current = git_blob_sha(p)
            if current is None:
                problems.append(f"{card_path}: cannot compute blob SHA for {path_str}")
            elif current != recorded_sha:
                problems.append(
                    f"{card_path}: STALE — {path_str} changed "
                    f"(recorded {recorded_sha[:10]}, current {current[:10]})"
                )

    for section in REQUIRED_SECTIONS:
        # architecture.md uses "## Hard rules" / "## Tier layout" etc.;
        # require schema only on cards under kb/modules/.
        if "modules" in str(card_path):
            if section not in body:
                problems.append(f"{card_path}: missing required section `{section}`")

    if verbose:
        ok = "OK " if not any(str(card_path) in p for p in problems) else "FAIL "
        print(f"  {ok}{card_path} — {len(file_entries)} file(s)")

# Walk cards.
for root in KB_ROOTS:
    if root.is_file() and root.suffix == ".md":
        check_card(root)
    elif root.is_dir():
        for md in sorted(root.rglob("*.md")):
            check_card(md)

# Verify kb/index/ exists (otherwise kb_check can't answer symbol questions).
idx = Path("kb/index/symbols.json")
if not idx.exists():
    problems.append("kb/index/symbols.json missing — run scripts/kb_index.sh first")

print()
if problems:
    print(f"kb_check: FAIL — {len(problems)} problem(s) across {cards_seen} card(s):")
    for p in problems:
        print(f"  - {p}")
    sys.exit(1)

print(f"kb_check: OK — {cards_seen} card(s) validated")
PY
