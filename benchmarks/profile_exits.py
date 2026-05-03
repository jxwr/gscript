#!/usr/bin/env python3
"""Collect Tier 2 exit profiles for selected suite benchmarks.

This is a profiling helper, not a benchmark gate. It builds the CLI once, runs
each selected benchmark with -exit-stats-json, and aggregates exit codes,
reasons, and sites so optimization work can target common mechanisms.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from collections import Counter
from pathlib import Path


DEFAULT_BENCHMARKS = [
    "fib_recursive",
    "ackermann",
    "mutual_recursion",
    "sort",
    "table_array_access",
    "table_field_access",
    "string_bench",
    "matmul",
    "spectral_norm",
    "fibonacci_iterative",
]

TIME_RE = re.compile(r"^Time:\s*([0-9]+(?:\.[0-9]+)?)s\b", re.MULTILINE)


def extract_exit_json(output: str) -> dict:
    marker = '{\n  "total":'
    start = output.rfind(marker)
    if start < 0:
        raise ValueError("no exit-stats JSON object found")
    return json.loads(output[start:])


def parse_time(output: str) -> float | None:
    match = TIME_RE.search(output)
    return float(match.group(1)) if match else None


def build(root: Path, out: Path) -> None:
    proc = subprocess.run(
        ["go", "build", "-o", str(out), "./cmd/gscript/"],
        cwd=root,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    if proc.returncode != 0:
        print(proc.stdout, file=sys.stderr)
        raise SystemExit(proc.returncode)


def run_one(root: Path, gscript: Path, bench: str, mode: str, timeout: int) -> dict:
    path = root / "benchmarks" / "suite" / f"{bench}.gs"
    if not path.exists():
        return {"benchmark": bench, "status": "missing"}

    env = os.environ.copy()
    if mode == "no_filter":
        env["GSCRIPT_TIER2_NO_FILTER"] = "1"
    cmd = [str(gscript), "-jit", "-jit-stats", "-exit-stats-json", str(path)]
    try:
        proc = subprocess.run(
            cmd,
            cwd=root,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=timeout,
        )
    except subprocess.TimeoutExpired as exc:
        output = exc.stdout or ""
        if isinstance(output, bytes):
            output = output.decode(errors="replace")
        return {"benchmark": bench, "status": "timeout", "output_tail": output[-1000:]}

    if proc.returncode != 0:
        return {
            "benchmark": bench,
            "status": "error",
            "exit_code": proc.returncode,
            "output_tail": proc.stdout[-1000:],
        }
    try:
        stats = extract_exit_json(proc.stdout)
    except ValueError as exc:
        return {
            "benchmark": bench,
            "status": "parse_error",
            "error": str(exc),
            "output_tail": proc.stdout[-1000:],
        }
    return {
        "benchmark": bench,
        "status": "ok",
        "seconds": parse_time(proc.stdout),
        "stats": stats,
    }


def markdown_report(results: list[dict], top: int) -> str:
    by_code = Counter()
    by_reason = Counter()
    sites: list[tuple[int, str, str, str, int, int, str]] = []

    lines = [
        "# Tier 2 Exit Profile",
        "",
        "| Benchmark | Time | Total exits | By exit code |",
        "|---|---:|---:|---|",
    ]
    for row in results:
        if row.get("status") != "ok":
            lines.append(f"| {row['benchmark']} | {row.get('status')} | - | - |")
            continue
        stats = row["stats"]
        for name, count in stats.get("by_exit_code", {}).items():
            by_code[name] += int(count)
        for site in stats.get("sites", []):
            count = int(site.get("count", 0))
            reason = str(site.get("reason", ""))
            by_reason[reason] += count
            sites.append(
                (
                    count,
                    row["benchmark"],
                    str(site.get("proto", "")),
                    str(site.get("exit_name", "")),
                    int(site.get("pc", -1)),
                    int(site.get("op_id", -1)),
                    reason,
                )
            )
        codes = ", ".join(f"{k}={v}" for k, v in sorted(stats.get("by_exit_code", {}).items()))
        seconds = row.get("seconds")
        time_text = f"{seconds:.3f}s" if seconds is not None else "-"
        lines.append(f"| {row['benchmark']} | {time_text} | {stats.get('total', 0)} | {codes or '-'} |")

    lines += [
        "",
        "## Aggregate By Exit Code",
        "",
        "| Exit code | Count |",
        "|---|---:|",
    ]
    for name, count in by_code.most_common():
        lines.append(f"| {name} | {count} |")

    lines += [
        "",
        "## Aggregate By Reason",
        "",
        "| Reason | Count |",
        "|---|---:|",
    ]
    for reason, count in by_reason.most_common(top):
        lines.append(f"| {reason} | {count} |")

    lines += [
        "",
        "## Top Sites",
        "",
        "| Count | Benchmark | Proto | Exit | PC | OpID | Reason |",
        "|---:|---|---|---|---:|---:|---|",
    ]
    for count, bench, proto, exit_name, pc, op_id, reason in sorted(sites, reverse=True)[:top]:
        lines.append(f"| {count} | {bench} | {proto} | {exit_name} | {pc} | {op_id} | {reason} |")
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bench", action="append", help="suite benchmark name; repeatable")
    parser.add_argument("--mode", choices=("default", "no_filter"), default="default")
    parser.add_argument("--timeout", type=int, default=60)
    parser.add_argument("--json", type=Path, help="write raw profile JSON")
    parser.add_argument("--markdown", type=Path, help="write markdown summary")
    parser.add_argument("--top", type=int, default=20)
    args = parser.parse_args()

    root = Path(__file__).resolve().parents[1]
    benches = args.bench or DEFAULT_BENCHMARKS
    tempdir = Path(tempfile.mkdtemp(prefix="gscript_exit_profile_"))
    gscript = tempdir / "gscript"
    try:
        build(root, gscript)
        results = [run_one(root, gscript, bench, args.mode, args.timeout) for bench in benches]
    finally:
        shutil.rmtree(tempdir, ignore_errors=True)

    payload = {"mode": args.mode, "benchmarks": benches, "results": results}
    if args.json:
        args.json.parent.mkdir(parents=True, exist_ok=True)
        args.json.write_text(json.dumps(payload, indent=2) + "\n")
    report = markdown_report(results, args.top)
    if args.markdown:
        args.markdown.parent.mkdir(parents=True, exist_ok=True)
        args.markdown.write_text(report)
    print(report)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
