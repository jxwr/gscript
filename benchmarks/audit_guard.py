#!/usr/bin/env python3
"""Audit a regression_guard JSON artifact for benchmark-comparison gaps."""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any


@dataclass
class AuditRow:
    name: str
    default_seconds: float | None
    luajit_seconds: float | None
    luajit_status: str
    exit_total: int

    @property
    def has_luajit_time(self) -> bool:
        return self.luajit_seconds is not None and self.luajit_seconds > 0

    @property
    def jit_luajit_ratio(self) -> float | None:
        if self.default_seconds is None or not self.has_luajit_time:
            return None
        return self.default_seconds / self.luajit_seconds


def _mode(row: dict[str, Any], name: str) -> dict[str, Any]:
    mode = row.get(name)
    if isinstance(mode, dict):
        return mode
    return {}


def load_rows(path: Path) -> list[AuditRow]:
    with path.open() as f:
        payload = json.load(f)
    rows = payload.get("results", [])
    if not isinstance(rows, list):
        raise ValueError("guard JSON must contain a list-valued 'results'")

    out: list[AuditRow] = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        name = str(row.get("benchmark", ""))
        default = _mode(row, "default")
        luajit = _mode(row, "luajit")
        out.append(
            AuditRow(
                name=name,
                default_seconds=default.get("seconds"),
                luajit_seconds=luajit.get("seconds"),
                luajit_status=str(luajit.get("status", "missing")),
                exit_total=int(default.get("exit_total") or 0),
            )
        )
    return out


def markdown_report(rows: list[AuditRow], *, low_resolution_cutoff: float = 0.001, exit_cutoff: int = 20) -> str:
    confirmed = sorted(
        [r for r in rows if r.jit_luajit_ratio is not None and r.jit_luajit_ratio < 1.0],
        key=lambda r: r.jit_luajit_ratio or 0,
    )
    unresolved = [r for r in rows if not r.has_luajit_time]
    low_resolution = [
        r for r in rows if r.default_seconds is not None and r.default_seconds <= low_resolution_cutoff
    ]
    exit_heavy = sorted([r for r in rows if r.exit_total >= exit_cutoff], key=lambda r: r.exit_total, reverse=True)

    lines: list[str] = [
        "# Benchmark Audit",
        "",
        "## Confirmed LuaJIT Comparisons",
    ]
    if confirmed:
        lines.append("| Benchmark | JIT | LuaJIT | JIT/LuaJIT |")
        lines.append("|---|---:|---:|---:|")
        for r in confirmed:
            lines.append(
                f"| {r.name} | {r.default_seconds:.3f}s | {r.luajit_seconds:.3f}s | {r.jit_luajit_ratio:.2f}x |"
            )
    else:
        lines.append("_No benchmark has a parseable LuaJIT timing where JIT is faster._")

    lines.extend(["", "## Unresolved Comparisons"])
    if unresolved:
        lines.append("| Benchmark | LuaJIT status | JIT |")
        lines.append("|---|---:|---:|")
        for r in unresolved:
            jit = "-" if r.default_seconds is None else f"{r.default_seconds:.3f}s"
            lines.append(f"| {r.name} | {r.luajit_status} | {jit} |")
    else:
        lines.append("_Every benchmark has a parseable LuaJIT timing._")

    lines.extend(["", "## Low-Resolution Measurements"])
    if low_resolution:
        lines.append("| Benchmark | JIT | Reason |")
        lines.append("|---|---:|---|")
        for r in low_resolution:
            lines.append(f"| {r.name} | {r.default_seconds:.3f}s | Needs calibrated repeats or ns/op bench |")
    else:
        lines.append("_No default JIT result is below the low-resolution cutoff._")

    lines.extend(["", "## Exit-Heavy Benchmarks"])
    if exit_heavy:
        lines.append("| Benchmark | Exits |")
        lines.append("|---|---:|")
        for r in exit_heavy:
            lines.append(f"| {r.name} | {r.exit_total} |")
    else:
        lines.append("_No benchmark exceeded the exit cutoff._")

    lines.extend(
        [
            "",
            "## Recommended Next Tests",
            "",
            "- Add or fix LuaJIT references for every unresolved comparison.",
            "- Use high-repeat or ns/op measurement for low-resolution rows before claiming a win.",
            "- Add renamed and parameter-varied structural variants for benchmarks accelerated by whole-call kernels.",
            "- Investigate exit-heavy rows even when wall time is already competitive.",
        ]
    )
    return "\n".join(lines) + "\n"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("json_file", type=Path)
    parser.add_argument("--markdown", type=Path, help="write markdown report to this path")
    parser.add_argument("--low-resolution-cutoff", type=float, default=0.001)
    parser.add_argument("--exit-cutoff", type=int, default=20)
    args = parser.parse_args(argv)

    rows = load_rows(args.json_file)
    report = markdown_report(rows, low_resolution_cutoff=args.low_resolution_cutoff, exit_cutoff=args.exit_cutoff)
    if args.markdown:
        args.markdown.parent.mkdir(parents=True, exist_ok=True)
        args.markdown.write_text(report)
    else:
        print(report, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
