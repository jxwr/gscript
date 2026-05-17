#!/usr/bin/env python3
"""Submission gate for timing_compare performance artifacts.

The gate is intentionally separate from the measurement runner.  It consumes a
JSON file produced by benchmarks/timing_compare.py and fails when any benchmark
misses the configured current/LuaJIT ratio.  With --baseline it also rejects
candidate results that regress relative to a previously accepted full run.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any


@dataclass(frozen=True)
class PerfRow:
    name: str
    current: float | None
    luajit: float | None
    current_status: str
    luajit_status: str

    @property
    def ratio(self) -> float | None:
        if self.current is None or self.luajit is None or self.luajit <= 0:
            return None
        return self.current / self.luajit


@dataclass(frozen=True)
class Violation:
    kind: str
    name: str
    value: str
    limit: str


def _subject_seconds(subject: dict[str, Any]) -> float | None:
    stats = subject.get("stats")
    if isinstance(stats, dict):
        median = stats.get("median")
        if isinstance(median, (int, float)):
            return float(median)
    seconds = subject.get("seconds")
    if isinstance(seconds, (int, float)):
        return float(seconds)
    return None


def _subject_status(subject: dict[str, Any]) -> str:
    status = subject.get("status")
    return str(status) if status is not None else "missing"


def _timing_compare_row(row: dict[str, Any], mode: str) -> PerfRow | None:
    modes = row.get("modes")
    if not isinstance(modes, dict):
        return None
    mode_row = modes.get(mode)
    if not isinstance(mode_row, dict):
        return None
    current = mode_row.get("current")
    luajit = mode_row.get("luajit")
    if not isinstance(current, dict):
        current = {}
    if not isinstance(luajit, dict):
        luajit = {}
    group = str(row.get("group") or "")
    bench = str(row.get("benchmark") or "")
    name = f"{group}/{bench}" if group and not bench.startswith(f"{group}/") else bench
    return PerfRow(
        name=name,
        current=_subject_seconds(current),
        luajit=_subject_seconds(luajit),
        current_status=_subject_status(current),
        luajit_status=_subject_status(luajit),
    )


def _flat_guard_row(row: dict[str, Any]) -> PerfRow | None:
    bench = row.get("benchmark")
    if not isinstance(bench, str):
        return None
    default = row.get("default")
    luajit = row.get("luajit")
    if not isinstance(default, dict):
        default = {}
    if not isinstance(luajit, dict):
        luajit = {}
    return PerfRow(
        name=bench,
        current=_subject_seconds(default),
        luajit=_subject_seconds(luajit),
        current_status=_subject_status(default),
        luajit_status=_subject_status(luajit),
    )


def load_rows(path: Path, *, mode: str = "default") -> dict[str, PerfRow]:
    with path.open() as f:
        payload = json.load(f)
    raw_rows = payload.get("results")
    if not isinstance(raw_rows, list):
        raise ValueError("performance JSON must contain a list-valued 'results'")

    rows: dict[str, PerfRow] = {}
    for raw in raw_rows:
        if not isinstance(raw, dict):
            continue
        row = _timing_compare_row(raw, mode) or _flat_guard_row(raw)
        if row is not None and row.name:
            rows[row.name] = row
    return rows


def check_rows(
    candidate: dict[str, PerfRow],
    *,
    baseline: dict[str, PerfRow] | None = None,
    ratio_threshold: float = 0.8,
    regression_tolerance: float = 0.03,
) -> list[Violation]:
    violations: list[Violation] = []
    for name, row in sorted(candidate.items()):
        ratio = row.ratio
        if ratio is None:
            violations.append(
                Violation("missing", name, f"current={row.current_status} luajit={row.luajit_status}", "timed current+luajit")
            )
        elif ratio > ratio_threshold:
            violations.append(Violation("luajit", name, f"{ratio:.3f}x", f"<={ratio_threshold:.3f}x"))

        if baseline is None:
            continue
        base = baseline.get(name)
        if base is None or base.current is None or row.current is None or base.current <= 0:
            continue
        change = row.current / base.current - 1.0
        if change > regression_tolerance:
            violations.append(Violation("regression", name, f"+{change * 100:.2f}%", f"<={regression_tolerance * 100:.2f}%"))
    return violations


def format_summary(rows: dict[str, PerfRow], violations: list[Violation]) -> str:
    worst = sorted(
        [row for row in rows.values() if row.ratio is not None],
        key=lambda row: row.ratio or -1.0,
        reverse=True,
    )[:12]
    lines = ["Worst current/LuaJIT ratios:", "Benchmark                          Current     LuaJIT    Cur/LJ"]
    lines.append("----------------------------------------------------------------")
    for row in worst:
        lines.append(f"{row.name:<34} {row.current:>8.6f}s {row.luajit:>8.6f}s {row.ratio:>7.3f}x")
    if violations:
        lines.extend(["", "Guard violations:", "Kind        Benchmark                          Value       Limit"])
        lines.append("----------------------------------------------------------------")
        for item in violations:
            lines.append(f"{item.kind:<11} {item.name:<34} {item.value:>10} {item.limit:>10}")
    else:
        lines.append("")
        lines.append("Guard passed.")
    return "\n".join(lines) + "\n"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("candidate", type=Path, help="timing_compare JSON to validate")
    parser.add_argument("--baseline", type=Path, help="previous accepted timing_compare JSON")
    parser.add_argument("--mode", default="default")
    parser.add_argument("--ratio-threshold", type=float, default=0.8)
    parser.add_argument("--regression-tolerance", type=float, default=0.03)
    args = parser.parse_args(argv)

    candidate = load_rows(args.candidate, mode=args.mode)
    baseline = load_rows(args.baseline, mode=args.mode) if args.baseline else None
    violations = check_rows(
        candidate,
        baseline=baseline,
        ratio_threshold=args.ratio_threshold,
        regression_tolerance=args.regression_tolerance,
    )
    print(format_summary(candidate, violations), end="")
    return 1 if violations else 0


if __name__ == "__main__":
    raise SystemExit(main())
