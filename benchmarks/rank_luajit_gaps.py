#!/usr/bin/env python3
"""Rank LuaJIT gaps from a regression_guard.py JSON artifact."""

from __future__ import annotations

import argparse
import csv
import json
import sys
from pathlib import Path
from typing import Any


def seconds(mode: dict[str, Any] | None) -> float | None:
    if not isinstance(mode, dict):
        return None
    value = mode.get("seconds")
    if isinstance(value, (int, float)) and value > 0:
        return float(value)
    return None


def mode_status(mode: dict[str, Any] | None) -> str:
    if not isinstance(mode, dict):
        return "missing"
    return str(mode.get("status") or "missing")


def fmt_seconds(value: float | None, status: str = "-") -> str:
    if value is None:
        return status
    return f"{value:.3f}s"


def fmt_ratio(value: float | None) -> str:
    if value is None:
        return "-"
    return f"{value:.2f}x"


def collect_rows(payload: dict[str, Any]) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for result in payload.get("results", []):
        if not isinstance(result, dict):
            continue
        default = result.get("default")
        luajit = result.get("luajit")
        default_s = seconds(default)
        luajit_s = seconds(luajit)
        if default_s is None or luajit_s is None:
            continue

        vm = result.get("vm")
        no_filter = result.get("no_filter")
        vm_s = seconds(vm)
        no_filter_s = seconds(no_filter)

        rows.append(
            {
                "benchmark": result.get("benchmark", ""),
                "vm_seconds": vm_s,
                "default_seconds": default_s,
                "no_filter_seconds": no_filter_s,
                "luajit_seconds": luajit_s,
                "default_luajit_ratio": default_s / luajit_s,
                "no_filter_luajit_ratio": (
                    no_filter_s / luajit_s if no_filter_s is not None else None
                ),
                "jit_vm_speedup": vm_s / default_s if vm_s is not None else None,
                "t2_attempted": int(default.get("t2_attempted", 0))
                if isinstance(default, dict)
                else 0,
                "t2_entered": int(default.get("t2_entered", 0))
                if isinstance(default, dict)
                else 0,
                "t2_failed": int(default.get("t2_failed", 0))
                if isinstance(default, dict)
                else 0,
                "exit_total": int(default.get("exit_total", 0))
                if isinstance(default, dict)
                else 0,
                "vm_status": mode_status(vm),
                "default_status": mode_status(default),
                "no_filter_status": mode_status(no_filter),
                "luajit_status": mode_status(luajit),
            }
        )
    rows.sort(key=lambda row: row["default_luajit_ratio"], reverse=True)
    return rows


def write_markdown(rows: list[dict[str, Any]], out) -> None:
    print(
        "| Rank | Benchmark | VM | Default | NoFilter | LuaJIT | Default/LuaJIT | NoFilter/LuaJIT | JIT/VM | T2 a/e/f | Exits |",
        file=out,
    )
    print("|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|", file=out)
    for idx, row in enumerate(rows, start=1):
        t2 = f"{row['t2_attempted']}/{row['t2_entered']}/{row['t2_failed']}"
        print(
            "| "
            + " | ".join(
                [
                    str(idx),
                    str(row["benchmark"]),
                    fmt_seconds(row["vm_seconds"], row["vm_status"]),
                    fmt_seconds(row["default_seconds"], row["default_status"]),
                    fmt_seconds(row["no_filter_seconds"], row["no_filter_status"]),
                    fmt_seconds(row["luajit_seconds"], row["luajit_status"]),
                    fmt_ratio(row["default_luajit_ratio"]),
                    fmt_ratio(row["no_filter_luajit_ratio"]),
                    fmt_ratio(row["jit_vm_speedup"]),
                    t2,
                    str(row["exit_total"]),
                ]
            )
            + " |",
            file=out,
        )


def write_csv(rows: list[dict[str, Any]], out) -> None:
    fields = [
        "rank",
        "benchmark",
        "vm_seconds",
        "default_seconds",
        "no_filter_seconds",
        "luajit_seconds",
        "default_luajit_ratio",
        "no_filter_luajit_ratio",
        "jit_vm_speedup",
        "t2_attempted",
        "t2_entered",
        "t2_failed",
        "exit_total",
    ]
    writer = csv.DictWriter(out, fieldnames=fields)
    writer.writeheader()
    for idx, row in enumerate(rows, start=1):
        writer.writerow({field: idx if field == "rank" else row.get(field) for field in fields})


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("json_file", type=Path, help="regression_guard.py JSON output")
    parser.add_argument("--top", type=int, default=0, help="limit rows; 0 means all")
    parser.add_argument("--format", choices=("markdown", "csv"), default="markdown")
    args = parser.parse_args(argv)

    with args.json_file.open() as f:
        payload = json.load(f)
    rows = collect_rows(payload)
    if args.top > 0:
        rows = rows[: args.top]

    if args.format == "csv":
        write_csv(rows, sys.stdout)
    else:
        write_markdown(rows, sys.stdout)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
