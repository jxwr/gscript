#!/bin/bash
# set_baseline.sh — promote latest.json to baseline + snapshot to history/
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

[ -f benchmarks/data/latest.json ] || { echo "No latest.json yet"; exit 1; }

cp benchmarks/data/latest.json benchmarks/data/baseline.json

# Snapshot into history/ — one file per day; later in the day overwrites
mkdir -p benchmarks/data/history
DATE=$(date +%Y-%m-%d)
cp benchmarks/data/latest.json "benchmarks/data/history/${DATE}.json"

echo "Baseline set from latest.json ($(date '+%Y-%m-%d %H:%M:%S'))"
echo "History snapshot: benchmarks/data/history/${DATE}.json"
