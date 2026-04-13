#!/bin/bash
# scripts/kb_index.sh — regenerate kb/index/ (L1 mechanical symbol index).
#
# Runs the Go program at scripts/kb_index.go over internal/, gscript/, and
# cmd/, producing:
#
#   kb/index/symbols.json     — every top-level func/method/type/const/var
#   kb/index/file_map.json    — per-file: package, LOC, public symbols
#   kb/index/call_graph.json  — best-effort call edges
#
# Fast — target <10 seconds on the full repo. Safe to run at the start of
# every round. The AI never reads kb/index/ directly; it's the substrate
# that kb_check.sh uses for staleness detection against L2 cards.

set -uo pipefail
cd "$(dirname "$0")/.."

mkdir -p kb/index

if ! go run scripts/kb_index.go -out kb/index internal gscript cmd; then
    echo "kb_index: Go program failed" >&2
    exit 1
fi

if [ -f kb/index/symbols.json ]; then
    syms=$(python3 -c "import json; print(len(json.load(open('kb/index/symbols.json'))))")
    files=$(python3 -c "import json; print(len(json.load(open('kb/index/file_map.json'))))")
    edges=$(python3 -c "import json; print(len(json.load(open('kb/index/call_graph.json'))))")
    echo "kb/index: $files files, $syms symbols, $edges call edges"
fi
