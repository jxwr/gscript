#!/usr/bin/env python3
"""Strict benchmark harness for statistically meaningful GScript comparisons.

Runs suite benchmarks in VM, default JIT, no-filter JIT, and optional LuaJIT
modes. Unlike regression_guard.py, this harness keeps timing quality explicit:
zero/too-small script times are either calibrated with repeated invocations or
reported as low_resolution instead of being treated as wins.
"""

from __future__ import annotations

import argparse
import json
import math
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

DEFAULT_MODES = ["vm", "default", "no_filter", "luajit"]

TIME_RE = re.compile(r"^Time:\s*([0-9]+(?:\.[0-9]+)?)s\b", re.MULTILINE)
T2_ATTEMPTED_RE = re.compile(r"^\s*Tier 2 attempted:\s*([0-9]+)\b", re.MULTILINE)
T2_ENTERED_RE = re.compile(r"^\s*Tier 2 entered:\s*([0-9]+)\s+functions\b", re.MULTILINE)
T2_FAILED_RE = re.compile(r"^\s*Tier 2 failed:\s*([0-9]+)\s+functions\b", re.MULTILINE)
EXIT_TOTAL_RE = re.compile(r"^\s*total exits:\s*([0-9]+)\b", re.MULTILINE)


@dataclass
class Stats:
    n: int = 0
    median: float | None = None
    min: float | None = None
    max: float | None = None
    mean: float | None = None
    stdev: float | None = None
    mad: float | None = None
    cv_pct: float | None = None


@dataclass
class CommandRun:
    status: str
    seconds: float | None = None
    wall_seconds: float | None = None
    exit_code: int | None = None
    output_tail: str = ""
    t2_attempted: int = 0
    t2_entered: int = 0
    t2_failed: int = 0
    exit_total: int = 0


@dataclass
class Sample:
    status: str
    seconds: float | None = None
    repeat: int = 1
    time_source: str = ""
    script_total_seconds: float | None = None
    wall_total_seconds: float | None = None
    note: str = ""
    runs: list[CommandRun] = field(default_factory=list)


@dataclass
class ModeResult:
    status: str
    repeat: int = 1
    stats: Stats = field(default_factory=Stats)
    samples: list[Sample] = field(default_factory=list)
    warmups: list[Sample] = field(default_factory=list)
    t2_attempted: int = 0
    t2_entered: int = 0
    t2_failed: int = 0
    exit_total: int = 0
    note: str = ""


@dataclass
class BenchmarkResult:
    benchmark: str
    modes: dict[str, ModeResult] = field(default_factory=dict)


def parse_time(output: str) -> float | None:
    match = TIME_RE.search(output)
    if not match:
        return None
    return float(match.group(1))


def parse_counter(pattern: re.Pattern[str], output: str) -> int:
    match = pattern.search(output)
    if not match:
        return 0
    return int(match.group(1))


def output_tail(output: str, limit: int = 8) -> str:
    lines = [line for line in output.strip().splitlines() if line.strip()]
    return "\n".join(lines[-limit:])


def parse_command_run(output: str, status: str, exit_code: int | None = None) -> CommandRun:
    seconds = parse_time(output) if status == "ok" else None
    if status == "ok" and seconds is None:
        status = "no_time"
    return CommandRun(
        status=status,
        seconds=seconds,
        exit_code=exit_code,
        output_tail=output_tail(output),
        t2_attempted=parse_counter(T2_ATTEMPTED_RE, output),
        t2_entered=parse_counter(T2_ENTERED_RE, output),
        t2_failed=parse_counter(T2_FAILED_RE, output),
        exit_total=parse_counter(EXIT_TOTAL_RE, output),
    )


def median_absolute_deviation(values: list[float]) -> float | None:
    if not values:
        return None
    med = statistics.median(values)
    return statistics.median([abs(v - med) for v in values])


def compute_stats(values: list[float]) -> Stats:
    if not values:
        return Stats()
    mean = statistics.fmean(values)
    stdev = statistics.stdev(values) if len(values) > 1 else 0.0
    return Stats(
        n=len(values),
        median=statistics.median(values),
        min=min(values),
        max=max(values),
        mean=mean,
        stdev=stdev,
        mad=median_absolute_deviation(values),
        cv_pct=(stdev / mean * 100.0) if mean > 0 else None,
    )


def run_command(cmd: list[str], timeout: int, env: dict[str, str] | None = None) -> CommandRun:
    started = time.perf_counter()
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
        wall = time.perf_counter() - started
    except subprocess.TimeoutExpired as exc:
        wall = time.perf_counter() - started
        output = exc.stdout or ""
        if isinstance(output, bytes):
            output = output.decode(errors="replace")
        run = parse_command_run(output + f"\nTIMEOUT after {timeout}s", "timeout")
        run.wall_seconds = wall
        return run

    status = "ok" if proc.returncode == 0 else "error"
    run = parse_command_run(proc.stdout, status, proc.returncode)
    run.wall_seconds = wall
    return run


def summarize_repeated_runs(
    runs: list[CommandRun],
    repeat: int,
    timer_resolution: float,
    min_sample_seconds: float,
    allow_wall_time: bool,
) -> Sample:
    wall_total = sum(r.wall_seconds or 0.0 for r in runs)
    bad = [r for r in runs if r.status not in {"ok", "no_time"}]
    if bad:
        status = bad[-1].status
        return Sample(
            status=status,
            repeat=repeat,
            wall_total_seconds=wall_total,
            note=f"{len(bad)} of {repeat} command runs ended with {status}",
            runs=runs,
        )

    if any(r.status == "no_time" for r in runs):
        return Sample(
            status="no_time",
            repeat=repeat,
            wall_total_seconds=wall_total,
            note="benchmark completed but did not print a Time: line",
            runs=runs,
        )

    script_total = sum(r.seconds or 0.0 for r in runs)
    if script_total <= timer_resolution:
        if allow_wall_time and wall_total >= min_sample_seconds:
            return Sample(
                status="ok",
                seconds=wall_total / repeat,
                repeat=repeat,
                time_source="wall_repeat",
                script_total_seconds=script_total,
                wall_total_seconds=wall_total,
                note="script timer was below resolution; per-command wall time used",
                runs=runs,
            )
        return Sample(
            status="low_resolution",
            repeat=repeat,
            script_total_seconds=script_total,
            wall_total_seconds=wall_total,
            note="script timer was below resolution",
            runs=runs,
        )

    return Sample(
        status="ok",
        seconds=script_total / repeat,
        repeat=repeat,
        time_source="script",
        script_total_seconds=script_total,
        wall_total_seconds=wall_total,
        runs=runs,
    )


def run_sample(
    cmd: list[str],
    env: dict[str, str] | None,
    repeat: int,
    timeout: int,
    timer_resolution: float,
    min_sample_seconds: float,
    allow_wall_time: bool,
) -> Sample:
    runs = [run_command(cmd, timeout, env) for _ in range(repeat)]
    return summarize_repeated_runs(runs, repeat, timer_resolution, min_sample_seconds, allow_wall_time)


def sample_is_big_enough(sample: Sample, min_sample_seconds: float) -> bool:
    if sample.status != "ok":
        return False
    if sample.time_source == "script":
        return (sample.script_total_seconds or 0.0) >= min_sample_seconds
    if sample.time_source == "wall_repeat":
        return (sample.wall_total_seconds or 0.0) >= min_sample_seconds
    return False


def calibrate_repeat(
    cmd: list[str],
    env: dict[str, str] | None,
    timeout: int,
    timer_resolution: float,
    min_sample_seconds: float,
    max_repeat: int,
    allow_wall_time: bool,
) -> tuple[int, Sample]:
    repeat = 1
    last: Sample | None = None
    while repeat <= max_repeat:
        last = run_sample(cmd, env, repeat, timeout, timer_resolution, min_sample_seconds, allow_wall_time)
        if sample_is_big_enough(last, min_sample_seconds):
            return repeat, last
        if last.status in {"error", "timeout", "no_time"}:
            return repeat, last
        repeat *= 2
    assert last is not None
    return max_repeat, last


def summarize_mode(samples: list[Sample], warmups: list[Sample], repeat: int) -> ModeResult:
    ok = [s for s in samples if s.status == "ok" and s.seconds is not None]
    stats = compute_stats([s.seconds for s in ok if s.seconds is not None])
    if not samples:
        status = "missing"
    elif ok and len(ok) == len(samples):
        status = "ok"
    elif ok:
        status = "partial"
    else:
        status = samples[-1].status

    source_runs = ok[len(ok) // 2].runs if ok else (samples[-1].runs if samples else [])
    counters = source_runs[-1] if source_runs else CommandRun("missing")
    return ModeResult(
        status=status,
        repeat=repeat,
        stats=stats,
        samples=samples,
        warmups=warmups,
        t2_attempted=counters.t2_attempted,
        t2_entered=counters.t2_entered,
        t2_failed=counters.t2_failed,
        exit_total=counters.exit_total,
    )


def mode_command(
    mode: str,
    bench: str,
    root: Path,
    gscript_bin: Path,
    luajit_bin: str | None,
) -> tuple[list[str] | None, dict[str, str] | None, str | None]:
    suite_file = root / "benchmarks" / "suite" / f"{bench}.gs"
    lua_file = root / "benchmarks" / "lua" / f"{bench}.lua"

    if mode == "luajit":
        if luajit_bin is None:
            return None, None, "skipped"
        if not lua_file.exists():
            return None, None, "missing"
        return [luajit_bin, str(lua_file)], None, None

    if not suite_file.exists():
        return None, None, "missing"

    env = os.environ.copy()
    if mode == "vm":
        return [str(gscript_bin), "-vm", str(suite_file)], env, None
    if mode == "default":
        return [str(gscript_bin), "-jit", "-jit-stats", "-exit-stats", str(suite_file)], env, None
    if mode == "no_filter":
        env["GSCRIPT_TIER2_NO_FILTER"] = "1"
        return [str(gscript_bin), "-jit", "-jit-stats", "-exit-stats", str(suite_file)], env, None
    raise ValueError(f"unknown mode: {mode}")


def run_mode(
    mode: str,
    bench: str,
    root: Path,
    gscript_bin: Path,
    luajit_bin: str | None,
    warmup_runs: int,
    measured_runs: int,
    timeout: int,
    timer_resolution: float,
    min_sample_seconds: float,
    max_repeat: int,
    allow_wall_time: bool,
    repeat_override: int | None = None,
) -> ModeResult:
    cmd, env, unavailable = mode_command(mode, bench, root, gscript_bin, luajit_bin)
    if unavailable:
        return ModeResult(status=unavailable, note=f"{mode} input unavailable")
    assert cmd is not None

    repeat = repeat_override or 1
    warmups: list[Sample] = []
    if repeat_override is None:
        repeat, calibration = calibrate_repeat(
            cmd, env, timeout, timer_resolution, min_sample_seconds, max_repeat, allow_wall_time
        )
        warmups.append(calibration)

    for _ in range(warmup_runs):
        warmups.append(
            run_sample(cmd, env, repeat, timeout, timer_resolution, min_sample_seconds, allow_wall_time)
        )

    samples = [
        run_sample(cmd, env, repeat, timeout, timer_resolution, min_sample_seconds, allow_wall_time)
        for _ in range(measured_runs)
    ]
    result = summarize_mode(samples, warmups, repeat)
    if result.status == "low_resolution":
        result.note = f"increase --max-repeat above {max_repeat} or enable --allow-wall-time"
    return result


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


def parse_repeat_overrides(values: list[str] | None) -> dict[tuple[str | None, str], int]:
    overrides: dict[tuple[str | None, str], int] = {}
    for value in values or []:
        if "=" not in value:
            raise argparse.ArgumentTypeError("--repeat entries must be BENCH=N or MODE/BENCH=N")
        key, raw_count = value.split("=", 1)
        try:
            count = int(raw_count)
        except ValueError as exc:
            raise argparse.ArgumentTypeError(f"invalid repeat count in {value!r}") from exc
        if count <= 0:
            raise argparse.ArgumentTypeError("repeat count must be > 0")
        if "/" in key:
            mode, bench = key.split("/", 1)
            overrides[(mode, bench)] = count
        else:
            overrides[(None, key)] = count
    return overrides


def repeat_for(overrides: dict[tuple[str | None, str], int], mode: str, bench: str) -> int | None:
    return overrides.get((mode, bench), overrides.get((None, bench)))


def fmt_seconds(value: float | None) -> str:
    if value is None:
        return "-"
    return f"{value:.6f}s"


def fmt_pct(value: float | None) -> str:
    if value is None:
        return "-"
    return f"{value:.2f}%"


def comparable_seconds(result: ModeResult | None) -> float | None:
    if result is None or result.status not in {"ok", "partial"}:
        return None
    return result.stats.median


def ratio(numer: float | None, denom: float | None) -> float | None:
    if numer is None or denom is None or denom == 0:
        return None
    return numer / denom


def markdown_summary(results: Iterable[BenchmarkResult], modes: list[str], args: argparse.Namespace) -> str:
    lines = [
        "# Strict Benchmark Summary",
        "",
        f"- Warmups: {args.warmup}",
        f"- Measured runs: {args.runs}",
        f"- Min sample seconds: {args.min_sample_seconds:.3f}",
        f"- Timer resolution floor: {args.timer_resolution:.6f}",
        f"- Wall-time fallback: {'enabled' if args.allow_wall_time else 'disabled'}",
        "",
        "## Comparisons",
        "",
        "| Benchmark | VM/Default | Default/LuaJIT | Notes |",
        "|---|---:|---:|---|",
    ]
    for row in results:
        vm = comparable_seconds(row.modes.get("vm"))
        default = comparable_seconds(row.modes.get("default"))
        luajit = comparable_seconds(row.modes.get("luajit"))
        notes = []
        for mode in modes:
            result = row.modes.get(mode)
            if result and result.status not in {"ok", "partial", "skipped"}:
                notes.append(f"{mode}:{result.status}")
        lines.append(
            "| "
            + " | ".join(
                [
                    row.benchmark,
                    fmt_ratio(ratio(vm, default)),
                    fmt_ratio(ratio(default, luajit)),
                    ", ".join(notes) or "-",
                ]
            )
            + " |"
        )

    lines.extend(
        [
            "",
            "## Measurements",
            "",
            "| Benchmark | Mode | Status | Source | Repeat | N | Median | Min | Max | Stdev | MAD | CV | T2 a/e/f | Exits | Note |",
            "|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|",
        ]
    )
    for row in results:
        for mode in modes:
            result = row.modes.get(mode) or ModeResult("missing")
            source = "-"
            notes = [result.note] if result.note else []
            sources = sorted({s.time_source for s in result.samples if s.time_source})
            if sources:
                source = ",".join(sources)
            sample_notes = sorted({s.note for s in result.samples if s.note})
            notes.extend(sample_notes)
            t2 = f"{result.t2_attempted}/{result.t2_entered}/{result.t2_failed}"
            lines.append(
                "| "
                + " | ".join(
                    [
                        row.benchmark,
                        mode,
                        result.status,
                        source,
                        str(result.repeat),
                        str(result.stats.n),
                        fmt_seconds(result.stats.median),
                        fmt_seconds(result.stats.min),
                        fmt_seconds(result.stats.max),
                        fmt_seconds(result.stats.stdev),
                        fmt_seconds(result.stats.mad),
                        fmt_pct(result.stats.cv_pct),
                        t2,
                        str(result.exit_total),
                        "; ".join(notes) or "-",
                    ]
                )
                + " |"
            )
    return "\n".join(lines) + "\n"


def fmt_ratio(value: float | None) -> str:
    if value is None or math.isinf(value) or math.isnan(value):
        return "-"
    return f"{value:.2f}x"


def print_brief(results: Iterable[BenchmarkResult], modes: list[str]) -> None:
    print(f"{'Benchmark':<22} {'Mode':<10} {'Status':<15} {'Median':>12} {'CV':>9} {'Repeat':>6} {'Source':<12}")
    print("-" * 92)
    for row in results:
        for mode in modes:
            result = row.modes.get(mode) or ModeResult("missing")
            sources = sorted({s.time_source for s in result.samples if s.time_source})
            print(
                f"{row.benchmark:<22} {mode:<10} {result.status:<15} "
                f"{fmt_seconds(result.stats.median):>12} {fmt_pct(result.stats.cv_pct):>9} "
                f"{result.repeat:>6} {(','.join(sources) or '-'):<12}"
            )


def dry_run_results(benchmarks: list[str], modes: list[str], root: Path, luajit_bin: str | None) -> list[BenchmarkResult]:
    rows: list[BenchmarkResult] = []
    fake_bin = Path("gscript")
    for bench in benchmarks:
        row = BenchmarkResult(bench)
        for mode in modes:
            _cmd, _env, unavailable = mode_command(mode, bench, root, fake_bin, luajit_bin)
            row.modes[mode] = ModeResult(status=unavailable or "planned")
        rows.append(row)
    return rows


def write_json(path: Path, payload: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2) + "\n")


def write_markdown(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def positive_int(value: str) -> int:
    parsed = int(value)
    if parsed <= 0:
        raise argparse.ArgumentTypeError("must be > 0")
    return parsed


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bench", action="append", help="benchmark to run; repeatable")
    parser.add_argument("--mode", action="append", choices=DEFAULT_MODES, help="mode to run; repeatable")
    parser.add_argument("--warmup", type=int, default=1, help="warmup samples after calibration")
    parser.add_argument("--runs", "--measured", dest="runs", type=positive_int, default=7)
    parser.add_argument("--timeout", type=positive_int, default=60, help="timeout per command invocation")
    parser.add_argument("--min-sample-seconds", type=float, default=0.020)
    parser.add_argument("--timer-resolution", type=float, default=0.001)
    parser.add_argument("--max-repeat", type=positive_int, default=32)
    parser.add_argument(
        "--repeat",
        action="append",
        help="force repeat count for BENCH=N or MODE/BENCH=N; disables calibration for that cell",
    )
    parser.add_argument(
        "--allow-wall-time",
        action="store_true",
        help="use repeated command wall time when script Time output is below resolution",
    )
    parser.add_argument("--no-luajit", action="store_true", help="skip LuaJIT even when installed")
    parser.add_argument("--dry-run", action="store_true", help="build no binary and run no benchmarks")
    parser.add_argument("--json", type=Path, default=Path("benchmarks/data/strict_guard_latest.json"))
    parser.add_argument("--markdown", type=Path, default=Path("benchmarks/data/strict_guard_latest.md"))
    parser.add_argument("--keep-bin", action="store_true", help="keep temporary gscript binary")
    args = parser.parse_args(argv)

    if args.warmup < 0:
        parser.error("--warmup must be >= 0")
    if args.min_sample_seconds <= 0:
        parser.error("--min-sample-seconds must be > 0")
    if args.timer_resolution < 0:
        parser.error("--timer-resolution must be >= 0")

    root = Path(__file__).resolve().parents[1]
    benchmarks = args.bench or DEFAULT_BENCHMARKS
    modes = args.mode or DEFAULT_MODES
    luajit_bin = None if args.no_luajit else shutil.which("luajit")
    try:
        repeat_overrides = parse_repeat_overrides(args.repeat)
    except argparse.ArgumentTypeError as exc:
        parser.error(str(exc))

    started = time.time()
    tempdir = Path(tempfile.mkdtemp(prefix="gscript_strict_guard_"))
    gscript_bin = tempdir / "gscript"
    results: list[BenchmarkResult]
    try:
        if args.dry_run:
            results = dry_run_results(benchmarks, modes, root, luajit_bin)
        else:
            build_gscript(root, gscript_bin)
            results = []
            for bench in benchmarks:
                row = BenchmarkResult(bench)
                for mode in modes:
                    row.modes[mode] = run_mode(
                        mode,
                        bench,
                        root,
                        gscript_bin,
                        luajit_bin,
                        args.warmup,
                        args.runs,
                        args.timeout,
                        args.timer_resolution,
                        args.min_sample_seconds,
                        args.max_repeat,
                        args.allow_wall_time,
                        repeat_for(repeat_overrides, mode, bench),
                    )
                results.append(row)

        print_brief(results, modes)

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
            "benchmarks": benchmarks,
            "modes": modes,
            "warmup_runs": args.warmup,
            "measured_runs": args.runs,
            "timeout_seconds": args.timeout,
            "timer_resolution_seconds": args.timer_resolution,
            "min_sample_seconds": args.min_sample_seconds,
            "max_repeat": args.max_repeat,
            "allow_wall_time": args.allow_wall_time,
            "dry_run": args.dry_run,
            "results": [asdict(r) for r in results],
        }

        json_path = root / args.json if not args.json.is_absolute() else args.json
        md_path = root / args.markdown if not args.markdown.is_absolute() else args.markdown
        write_json(json_path, payload)
        write_markdown(md_path, markdown_summary(results, modes, args))
        print(f"Wrote JSON: {json_path}")
        print(f"Wrote Markdown: {md_path}")

        bad_statuses = {"error", "timeout"}
        return 1 if any(m.status in bad_statuses for r in results for m in r.modes.values()) else 0
    finally:
        if args.keep_bin:
            print(f"Kept gscript binary: {gscript_bin}")
        else:
            shutil.rmtree(tempdir, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
