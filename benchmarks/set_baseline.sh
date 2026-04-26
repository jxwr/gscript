#!/bin/bash
# set_baseline.sh — promote a benchmark JSON file to baseline + snapshot to history/
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

SOURCE="${1:-benchmarks/data/latest.json}"
[ -f "$SOURCE" ] || { echo "No benchmark JSON found: $SOURCE"; exit 1; }

cp "$SOURCE" benchmarks/data/baseline.json

# Snapshot into history/ — one file per day; later in the day overwrites
mkdir -p benchmarks/data/history
DATE=$(date +%Y-%m-%d)
cp "$SOURCE" "benchmarks/data/history/${DATE}.json"

echo "Baseline set from $SOURCE ($(date '+%Y-%m-%d %H:%M:%S'))"
echo "History snapshot: benchmarks/data/history/${DATE}.json"
