#!/usr/bin/env python3
"""Benchmark comparison and regression guard for GScript.

Runs suite benchmarks in VM, default JIT, no-filter JIT, and optional LuaJIT
modes. Each benchmark/mode is isolated so a timeout or crash records a row but
does not abort the full run.
"""

from __future__ import annotations

import argparse
import csv
import json
import os
import platform
import re
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Iterable


DEFAULT_BENCHMARKS = [
    "fib",
    "fib_recursive",
    "sieve",
    "mandelbrot",
    "ackermann",
    "matmul",
    "spectral_norm",
    "nbody",
    "fannkuch",
    "sort",
    "sum_primes",
    "mutual_recursion",
    "method_dispatch",
    "closure_bench",
    "string_bench",
    "binary_trees",
    "table_field_access",
    "table_array_access",
    "coroutine_bench",
    "fibonacci_iterative",
    "math_intensive",
    "object_creation",
]


TIME_RE = re.compile(r"^Time:\s*([0-9]+(?:\.[0-9]+)?)s\b", re.MULTILINE)
T2_ATTEMPTED_RE = re.compile(r"^\s*Tier 2 attempted:\s*([0-9]+)\b", re.MULTILINE)
T2_ENTERED_RE = re.compile(r"^\s*Tier 2 entered:\s*([0-9]+)\s+functions\b", re.MULTILINE)
T2_FAILED_RE = re.compile(r"^\s*Tier 2 failed:\s*([0-9]+)\s+functions\b", re.MULTILINE)
EXIT_TOTAL_RE = re.compile(r"^\s*total exits:\s*([0-9]+)\b", re.MULTILINE)


@dataclass
class RunSample:
    status: str
    seconds: float | None = None
    exit_code: int | None = None
    output_tail: str = ""
    t2_attempted: int = 0
    t2_entered: int = 0
    t2_failed: int = 0
    exit_total: int = 0


@dataclass
class ModeResult:
    status: str
    seconds: float | None = None
    samples: list[RunSample] = field(default_factory=list)
    t2_attempted: int = 0
    t2_entered: int = 0
    t2_failed: int = 0
    exit_total: int = 0


@dataclass
class BenchmarkResult:
    benchmark: str
    vm: ModeResult | None = None
    default: ModeResult | None = None
    no_filter: ModeResult | None = None
    luajit: ModeResult | None = None
    baseline_seconds: float | None = None
    regression_pct: float | None = None
    regression: bool = False


CSV_COLUMNS = [
    "benchmark",
    "vm_seconds",
    "default_seconds",
    "no_filter_seconds",
    "luajit_seconds",
    "jit_vm_speedup",
    "jit_luajit_ratio",
    "baseline_seconds",
    "regression_pct",
    "regression",
    "default_status",
    "t2_attempted",
    "t2_entered",
    "t2_failed",
    "exit_total",
]


def parse_time(output: str) -> float | None:
    match = TIME_RE.search(output)
    if not match:
        return None
    return float(match.group(1))


def parse_seconds(value: object) -> float | None:
    if value is None:
        return None
    text = str(value).strip()
    if text in {"", "ERROR", "FAILED", "HANG", "N/A", "SKIP", "TIMEOUT", "n/a"}:
        return None
    if text.startswith("Time:"):
        text = text.split("Time:", 1)[1].strip()
    try:
        if text.endswith("us"):
            return float(text[:-2]) * 1e-6
        if text.endswith("ms"):
            return float(text[:-2]) * 1e-3
        if text.endswith("s"):
            return float(text[:-1])
        return float(text)
    except ValueError:
        return None


def parse_counter(pattern: re.Pattern[str], output: str) -> int:
    match = pattern.search(output)
    if not match:
        return 0
    return int(match.group(1))


def parse_sample(output: str, status: str, exit_code: int | None = None) -> RunSample:
    lines = [line for line in output.strip().splitlines() if line.strip()]
    return RunSample(
        status=status,
        seconds=parse_time(output) if status == "ok" else None,
        exit_code=exit_code,
        output_tail="\n".join(lines[-8:]),
        t2_attempted=parse_counter(T2_ATTEMPTED_RE, output),
        t2_entered=parse_counter(T2_ENTERED_RE, output),
        t2_failed=parse_counter(T2_FAILED_RE, output),
        exit_total=parse_counter(EXIT_TOTAL_RE, output),
    )


def summarize_samples(samples: list[RunSample]) -> ModeResult:
    ok = [s for s in samples if s.status == "ok" and s.seconds is not None]
    status = "ok"
    if not ok:
        status = samples[-1].status if samples else "missing"
    elif len(ok) != len(samples):
        status = "partial"
    seconds = statistics.median([s.seconds for s in ok]) if ok else None

    source = ok[len(ok) // 2] if ok else (samples[-1] if samples else RunSample("missing"))
    return ModeResult(
        status=status,
        seconds=seconds,
        samples=samples,
        t2_attempted=source.t2_attempted,
        t2_entered=source.t2_entered,
        t2_failed=source.t2_failed,
        exit_total=source.exit_total,
    )


def run_command(cmd: list[str], timeout: int, env: dict[str, str] | None = None) -> RunSample:
    try:
        proc = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=timeout,
            env=env,
            check=False,
        )
    except subprocess.TimeoutExpired as exc:
        output = exc.stdout or ""
        if isinstance(output, bytes):
            output = output.decode(errors="replace")
        return parse_sample(output + f"\nTIMEOUT after {timeout}s", "timeout")

    if proc.returncode != 0:
        return parse_sample(proc.stdout, "error", proc.returncode)
    sample = parse_sample(proc.stdout, "ok", proc.returncode)
    if sample.seconds is None:
        sample.status = "no_time"
    return sample


def run_mode(
    mode: str,
    bench: str,
    root: Path,
    gscript_bin: Path,
    luajit_bin: str | None,
    runs: int,
    timeout: int,
) -> ModeResult:
    samples: list[RunSample] = []
    suite_file = root / "benchmarks" / "suite" / f"{bench}.gs"
    lua_file = root / "benchmarks" / "lua" / f"{bench}.lua"

    if mode == "luajit":
        if luajit_bin is None:
            return ModeResult(status="skipped")
        if not lua_file.exists():
            return ModeResult(status="missing")
        cmd = [luajit_bin, str(lua_file)]
        env = None
    else:
        if not suite_file.exists():
            return ModeResult(status="missing")
        env = os.environ.copy()
        if mode == "vm":
            cmd = [str(gscript_bin), "-vm", str(suite_file)]
        elif mode == "default":
            cmd = [str(gscript_bin), "-jit", "-jit-stats", "-exit-stats", str(suite_file)]
        elif mode == "no_filter":
            env["GSCRIPT_TIER2_NO_FILTER"] = "1"
            cmd = [str(gscript_bin), "-jit", "-jit-stats", "-exit-stats", str(suite_file)]
        else:
            raise ValueError(f"unknown mode: {mode}")

    for _ in range(runs):
        samples.append(run_command(cmd, timeout, env))
    return summarize_samples(samples)


def build_gscript(root: Path, out: Path) -> None:
    proc = subprocess.run(
        ["go", "build", "-o", str(out), "./cmd/gscript/"],
        cwd=root,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        check=False,
    )
    if proc.returncode != 0:
        print(proc.stdout, file=sys.stderr)
        raise SystemExit(f"build failed with exit {proc.returncode}")


def load_baseline(path: Path | None) -> dict[str, float]:
    if path is None or not path.exists():
        return {}
    with path.open() as f:
        data = json.load(f)
    out: dict[str, float] = {}
    results = data.get("results", {})
    if isinstance(results, dict):
        for name, row in results.items():
            sec = parse_seconds(row.get("jit"))
            if sec is not None:
                out[name] = sec
    elif isinstance(results, list):
        for row in results:
            if not isinstance(row, dict):
                continue
            name = row.get("benchmark")
            default = row.get("default")
            if not isinstance(name, str) or not isinstance(default, dict):
                continue
            sec = parse_seconds(default.get("seconds"))
            if sec is not None:
                out[name] = sec
    return out


def fmt_seconds(value: float | None, status: str = "") -> str:
    if value is None:
        return status or "-"
    return f"{value:.3f}s"


def speedup(numer: float | None, denom: float | None) -> str:
    if numer is None or denom is None or denom == 0:
        return "-"
    return f"{numer / denom:.2f}x"


def ratio(numer: float | None, denom: float | None) -> float | None:
    if numer is None or denom is None or denom == 0:
        return None
    return numer / denom


def report_row(row: BenchmarkResult) -> dict[str, object]:
    vm = row.vm or ModeResult("missing")
    default = row.default or ModeResult("missing")
    no_filter = row.no_filter or ModeResult("missing")
    luajit = row.luajit or ModeResult("missing")
    return {
        "benchmark": row.benchmark,
        "vm_seconds": vm.seconds,
        "default_seconds": default.seconds,
        "no_filter_seconds": no_filter.seconds,
        "luajit_seconds": luajit.seconds,
        "jit_vm_speedup": ratio(vm.seconds, default.seconds),
        "jit_luajit_ratio": ratio(default.seconds, luajit.seconds),
        "baseline_seconds": row.baseline_seconds,
        "regression_pct": row.regression_pct,
        "regression": row.regression,
        "default_status": default.status,
        "t2_attempted": default.t2_attempted,
        "t2_entered": default.t2_entered,
        "t2_failed": default.t2_failed,
        "exit_total": default.exit_total,
    }


def fmt_ratio(value: float | None) -> str:
    if value is None:
        return "-"
    return f"{value:.2f}x"


def fmt_pct(value: float | None) -> str:
    if value is None:
        return "-"
    return f"{value:+.1f}%"


def write_csv(path: Path, results: Iterable[BenchmarkResult]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=CSV_COLUMNS)
        writer.writeheader()
        for row in results:
            writer.writerow(report_row(row))


def markdown_table(results: Iterable[BenchmarkResult], threshold_pct: float) -> str:
    lines = [
        "| Benchmark | VM | Default JIT | NoFilter | LuaJIT | JIT/VM | JIT/LJ | Baseline | Regress | T2 a/e/f | Exits |",
        "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|",
    ]
    regressions = 0
    for row in results:
        vm = row.vm or ModeResult("missing")
        default = row.default or ModeResult("missing")
        no_filter = row.no_filter or ModeResult("missing")
        luajit = row.luajit or ModeResult("missing")
        if row.regression:
            regressions += 1
        t2 = f"{default.t2_attempted}/{default.t2_entered}/{default.t2_failed}"
        marker = ("REG " if row.regression else "") + fmt_pct(row.regression_pct)
        lines.append(
            "| "
            + " | ".join(
                [
                    row.benchmark,
                    fmt_seconds(vm.seconds, vm.status),
                    fmt_seconds(default.seconds, default.status),
                    fmt_seconds(no_filter.seconds, no_filter.status),
                    fmt_seconds(luajit.seconds, luajit.status),
                    fmt_ratio(ratio(vm.seconds, default.seconds)),
                    fmt_ratio(ratio(default.seconds, luajit.seconds)),
                    fmt_seconds(row.baseline_seconds),
                    marker,
                    t2,
                    str(default.exit_total),
                ]
            )
            + " |"
        )
    lines.extend(
        [
            "",
            f"Regression threshold: >{threshold_pct:.1f}% slower than baseline default JIT.",
            f"Regressions: {regressions}",
        ]
    )
    return "\n".join(lines) + "\n"


def write_markdown(path: Path, results: Iterable[BenchmarkResult], threshold_pct: float) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(markdown_table(results, threshold_pct))


def print_table(results: Iterable[BenchmarkResult], threshold_pct: float) -> None:
    header = (
        f"{'Benchmark':<22} {'VM':>9} {'Default':>9} {'NoFilter':>9} {'LuaJIT':>9} "
        f"{'JIT/VM':>8} {'JIT/LJ':>8} {'Baseline':>9} {'T2 a/e/f':>11} {'Exits':>7} {'Regress':>9}"
    )
    print(header)
    print("-" * len(header))
    regressions = 0
    for row in results:
        vm = row.vm or ModeResult("missing")
        default = row.default or ModeResult("missing")
        no_filter = row.no_filter or ModeResult("missing")
        luajit = row.luajit or ModeResult("missing")
        marker = "-"
        if row.regression_pct is not None:
            marker = f"{row.regression_pct:+.1f}%"
        if row.regression:
            marker = "REG " + marker
            regressions += 1
        t2 = f"{default.t2_attempted}/{default.t2_entered}/{default.t2_failed}"
        print(
            f"{row.benchmark:<22} "
            f"{fmt_seconds(vm.seconds, vm.status):>9} "
            f"{fmt_seconds(default.seconds, default.status):>9} "
            f"{fmt_seconds(no_filter.seconds, no_filter.status):>9} "
            f"{fmt_seconds(luajit.seconds, luajit.status):>9} "
            f"{speedup(vm.seconds, default.seconds):>8} "
            f"{speedup(default.seconds, luajit.seconds):>8} "
            f"{fmt_seconds(row.baseline_seconds):>9} "
            f"{t2:>11} "
            f"{default.exit_total:>7} "
            f"{marker:>9}"
        )
    print()
    print(f"Regression threshold: >{threshold_pct:.1f}% slower than baseline default JIT")
    print(f"Regressions: {regressions}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--runs",
        "--count",
        dest="runs",
        type=int,
        default=3,
        help="samples per benchmark/mode; --count is an alias for Go bench users",
    )
    parser.add_argument("--timeout", type=int, default=60, help="timeout per sample in seconds")
    parser.add_argument("--threshold", type=float, default=10.0, help="regression threshold percent")
    parser.add_argument("--baseline", type=Path, default=Path("benchmarks/data/baseline.json"))
    parser.add_argument("--json", type=Path, help="write machine-readable results to this path")
    parser.add_argument("--csv", type=Path, help="write flat per-benchmark summary to this path")
    parser.add_argument("--markdown", type=Path, help="write markdown summary table to this path")
    parser.add_argument("--bench", action="append", help="benchmark to run; repeatable")
    parser.add_argument("--no-luajit", action="store_true", help="skip LuaJIT even when installed")
    parser.add_argument("--keep-bin", action="store_true", help="keep temporary gscript binary")
    args = parser.parse_args(argv)

    if args.runs <= 0:
        parser.error("--runs must be > 0")
    if args.timeout <= 0:
        parser.error("--timeout must be > 0")

    root = Path(__file__).resolve().parents[1]
    benchmarks = args.bench or DEFAULT_BENCHMARKS
    baseline = load_baseline(root / args.baseline if not args.baseline.is_absolute() else args.baseline)

    tempdir = Path(tempfile.mkdtemp(prefix="gscript_bench_guard_"))
    gscript_bin = tempdir / "gscript"
    luajit_bin = None if args.no_luajit else shutil.which("luajit")

    started = time.time()
    try:
        build_gscript(root, gscript_bin)
        results: list[BenchmarkResult] = []
        for bench in benchmarks:
            row = BenchmarkResult(benchmark=bench)
            row.vm = run_mode("vm", bench, root, gscript_bin, luajit_bin, args.runs, args.timeout)
            row.default = run_mode("default", bench, root, gscript_bin, luajit_bin, args.runs, args.timeout)
            row.no_filter = run_mode("no_filter", bench, root, gscript_bin, luajit_bin, args.runs, args.timeout)
            row.luajit = run_mode("luajit", bench, root, gscript_bin, luajit_bin, args.runs, args.timeout)

            base = baseline.get(bench)
            row.baseline_seconds = base
            if base is not None and row.default and row.default.seconds is not None and base > 0:
                row.regression_pct = ((row.default.seconds / base) - 1.0) * 100.0
                row.regression = row.regression_pct > args.threshold
            results.append(row)

        print_table(results, args.threshold)

        payload = {
            "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "duration_seconds": round(time.time() - started, 3),
            "commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
            "platform": {
                "machine": platform.machine(),
                "system": platform.system(),
                "go": subprocess.check_output(["go", "version"], cwd=root, text=True).strip(),
                "luajit": luajit_bin or "",
            },
            "runs": args.runs,
            "timeout_seconds": args.timeout,
            "threshold_pct": args.threshold,
            "baseline": str(args.baseline),
            "results": [asdict(r) for r in results],
        }
        if args.json:
            out = root / args.json if not args.json.is_absolute() else args.json
            out.parent.mkdir(parents=True, exist_ok=True)
            out.write_text(json.dumps(payload, indent=2) + "\n")
            print(f"Wrote JSON: {out}")
        if args.csv:
            out = root / args.csv if not args.csv.is_absolute() else args.csv
            write_csv(out, results)
            print(f"Wrote CSV: {out}")
        if args.markdown:
            out = root / args.markdown if not args.markdown.is_absolute() else args.markdown
            write_markdown(out, results, args.threshold)
            print(f"Wrote Markdown: {out}")

        return 1 if any(r.regression for r in results) else 0
    finally:
        if args.keep_bin:
            print(f"Kept gscript binary: {gscript_bin}")
        else:
            shutil.rmtree(tempdir, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
