#!/usr/bin/env python3
"""Aggregate diag/<bench>/stats.json files + benchmarks/data/reference.json
into diag/summary.md for Step 1 of a round. Stdout is the summary markdown.

Written to be called from scripts/diag.sh with diag/ as the argument.
"""

import json
import re
import sys
from pathlib import Path


def parse_time(value):
    """Reference/latest JSON records 'jit' as '0.085s' or similar. Return
    float seconds or None."""
    if value is None:
        return None
    m = re.search(r"[\d.]+", str(value))
    return float(m.group()) if m else None


def main(diag_root):
    diag_root = Path(diag_root)
    if not diag_root.is_dir():
        print(f"error: {diag_root} is not a directory", file=sys.stderr)
        sys.exit(2)

    ref_path = Path("benchmarks/data/reference.json")
    ref = None
    if ref_path.exists():
        with open(ref_path) as f:
            ref = json.load(f)

    latest_path = Path("benchmarks/data/latest.json")
    latest = None
    if latest_path.exists():
        with open(latest_path) as f:
            latest = json.load(f)

    benches = sorted(
        d for d in diag_root.iterdir()
        if d.is_dir() and (d / "stats.json").exists()
    )

    lines = []
    lines.append("# diag/summary.md")
    lines.append("")
    lines.append(f"Generated across {len(benches)} benchmarks.")
    lines.append("")
    lines.append("## Insn counts (hottest proto per benchmark)")
    lines.append("")
    lines.append("| Benchmark | Hottest proto | Insns | Bytes | Load | Store | FP | Branch |")
    lines.append("|-----------|---------------|------:|------:|-----:|------:|---:|-------:|")

    per_bench_hottest = {}
    for bench_dir in benches:
        with open(bench_dir / "stats.json") as f:
            data = json.load(f)
        protos = [p for p in data.get("protos", []) if not p.get("skip_reason")]
        if not protos:
            continue
        hot = max(protos, key=lambda p: p.get("insn_count", 0))
        hist = hot.get("insn_histogram", {})
        per_bench_hottest[bench_dir.name] = hot
        lines.append("| {b} | {n} | {i} | {bytes} | {l} | {s} | {f} | {br} |".format(
            b=bench_dir.name,
            n=hot.get("name", "?"),
            i=hot.get("insn_count", 0),
            bytes=hot.get("code_bytes", 0),
            l=hist.get("load", 0),
            s=hist.get("store", 0),
            f=hist.get("fp", 0),
            br=hist.get("branch", 0),
        ))

    if ref and latest:
        lines.append("")
        lines.append("## Drift vs frozen reference.json")
        lines.append("")
        ref_meta = ref.get("_meta", {})
        excluded = set(ref_meta.get("excluded", []))
        flag_t = ref_meta.get("drift_threshold_flag_pct", 2.0)
        fail_t = ref_meta.get("drift_threshold_fail_pct", 5.0)

        drifters = []
        ref_results = ref.get("results", {})
        latest_results = latest.get("results", {})
        for k, v in ref_results.items():
            if k in excluded:
                continue
            rj = parse_time(v.get("jit"))
            lj = parse_time(latest_results.get(k, {}).get("jit"))
            if rj and lj and rj > 0:
                drifters.append((k, rj, lj, (lj - rj) / rj * 100))
        drifters.sort(key=lambda x: -x[3])

        lines.append(f"Flag threshold: {flag_t}%. Fail threshold: {fail_t}%.")
        lines.append("")
        lines.append("| Benchmark | Reference | Latest | Drift | Status |")
        lines.append("|-----------|----------:|-------:|------:|:-------|")
        for k, r, l, d in drifters[:10]:
            status = "FAIL" if d >= fail_t else ("FLAG" if d >= flag_t else "ok")
            lines.append(f"| {k} | {r:.3f}s | {l:.3f}s | {d:+.2f}% | {status} |")
    else:
        lines.append("")
        lines.append("## Drift vs frozen reference.json")
        lines.append("")
        if not ref:
            lines.append("_reference.json not found_")
        if not latest:
            lines.append("_latest.json not found — run benchmarks/run_all.sh first_")

    lines.append("")
    lines.append("## Histogram anomalies")
    lines.append("")
    anomalies = []
    for bench, hot in per_bench_hottest.items():
        hist = hot.get("insn_histogram", {})
        loads = hist.get("load", 0)
        stores = hist.get("store", 0)
        total = hot.get("insn_count", 1)
        mem_pct = 100 * (loads + stores) / total if total else 0
        if mem_pct > 50:
            anomalies.append(
                f"- **{bench}/{hot.get('name','?')}**: {loads}+{stores}={loads+stores} memory ops "
                f"out of {total} insns ({mem_pct:.0f}%) — dominant store/load, suspect allocation/field access."
            )
    if anomalies:
        lines.extend(anomalies)
    else:
        lines.append("_no benchmarks above 50% memory-op threshold._")

    print("\n".join(lines))


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("usage: diag_summary.py <diag_root>", file=sys.stderr)
        sys.exit(2)
    main(sys.argv[1])
