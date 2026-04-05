#!/bin/bash
# benchmark_diff.sh — Compare latest benchmark results against baseline
# Usage: bash benchmarks/benchmark_diff.sh
# Exit 0 = no regressions, Exit 1 = regressions found

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LATEST="$ROOT/benchmarks/data/latest.json"
BASELINE="$ROOT/benchmarks/data/baseline.json"

if [ ! -f "$LATEST" ]; then
    echo "ERROR: No latest.json found. Run benchmarks first."
    exit 1
fi

if [ ! -f "$BASELINE" ]; then
    echo "No baseline.json found. Setting latest as baseline."
    cp "$LATEST" "$BASELINE"
    exit 0
fi

python3 << PYEOF
import json, sys

def parse_time(s):
    """Parse time string like '0.142s' or '23.6us' or '2.36ms' to seconds."""
    s = s.strip()
    if s in ('ERROR', 'HANG', 'N/A', 'SKIP', ''):
        return None
    try:
        if s.endswith('us'):
            return float(s[:-2]) * 1e-6
        elif s.endswith('ms'):
            return float(s[:-2]) * 1e-3
        elif s.endswith('s'):
            return float(s[:-1])
        else:
            return float(s)
    except ValueError:
        return None

with open('$LATEST') as f:
    latest = json.load(f)
with open('$BASELINE') as f:
    baseline = json.load(f)

improved = []
regressed = []
unchanged = []
new_benchmarks = []

for name, ldata in latest.get('results', {}).items():
    bdata = baseline.get('results', {}).get(name)
    if not bdata:
        new_benchmarks.append(name)
        continue

    l_jit = parse_time(ldata.get('jit', ''))
    b_jit = parse_time(bdata.get('jit', ''))

    if l_jit is None or b_jit is None:
        unchanged.append((name, bdata.get('jit','?'), ldata.get('jit','?'), 'N/A'))
        continue

    if b_jit == 0:
        unchanged.append((name, bdata.get('jit','?'), ldata.get('jit','?'), 'N/A'))
        continue

    ratio = l_jit / b_jit
    pct = (ratio - 1) * 100

    if ratio < 0.9:  # >10% faster
        improved.append((name, bdata.get('jit'), ldata.get('jit'), f'{pct:+.1f}%'))
    elif ratio > 1.1:  # >10% slower
        regressed.append((name, bdata.get('jit'), ldata.get('jit'), f'{pct:+.1f}%'))
    else:
        unchanged.append((name, bdata.get('jit'), ldata.get('jit'), f'{pct:+.1f}%'))

print("=== Benchmark Diff (JIT mode, latest vs baseline) ===")
print(f"Baseline: {baseline.get('commit','?')[:8]}")
print(f"Latest:   {latest.get('commit','?')[:8]}")
print()

if improved:
    print(f"IMPROVED ({len(improved)}):")
    for name, old, new, pct in improved:
        print(f"  + {name}: {old} -> {new} ({pct})")
    print()

if regressed:
    print(f"REGRESSED ({len(regressed)}):")
    for name, old, new, pct in regressed:
        print(f"  - {name}: {old} -> {new} ({pct})")
    print()

if unchanged:
    print(f"UNCHANGED ({len(unchanged)}):")
    for name, old, new, pct in unchanged:
        print(f"  ~ {name}: {old} -> {new} ({pct})")
    print()

if new_benchmarks:
    print(f"NEW ({len(new_benchmarks)}):")
    for name in new_benchmarks:
        print(f"  * {name}")
    print()

if regressed:
    sys.exit(1)
else:
    print("No regressions detected.")
    sys.exit(0)
PYEOF
