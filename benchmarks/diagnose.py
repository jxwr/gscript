#!/usr/bin/env python3
"""Collect a reusable benchmark diagnostics bundle.

The script is intentionally benchmark-agnostic. It discovers suite, extended,
and variant benchmarks with the same selector rules as timing_compare.py, then
stores per-benchmark artifacts for tiering, exits, runtime paths, speculation,
and optional CPU/warm JIT mapping.
"""

from __future__ import annotations

import argparse
import json
import re
import shutil
import subprocess
import sys
import tempfile
import time
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

import timing_compare as timing


TIME_RE = re.compile(r"^Time:\s*([0-9]+(?:\.[0-9]+)?)s\b", re.MULTILINE)
T2_ATTEMPTED_RE = re.compile(r"^\s*Tier 2 attempted:\s*([0-9]+)\b", re.MULTILINE)
T2_COMPILED_RE = re.compile(r"^\s*Tier 2 compiled:\s*([0-9]+)", re.MULTILINE)
T2_ENTERED_RE = re.compile(r"^\s*Tier 2 entered:\s*([0-9]+)\s+functions\b", re.MULTILINE)
T2_FAILED_RE = re.compile(r"^\s*Tier 2 failed:\s*([0-9]+)\s+functions\b", re.MULTILINE)
EXIT_TOTAL_RE = re.compile(r"^\s*total exits:\s*([0-9]+)\b", re.MULTILINE)


@dataclass
class CommandArtifact:
    name: str
    command: list[str]
    raw: str
    json: str | None = None
    status: str = "missing"
    exit_code: int | None = None
    wall_seconds: float | None = None


@dataclass
class DiagnosticRow:
    benchmark: str
    group: str
    script: str
    status: str
    time_seconds: float | None = None
    t2_attempted: int = 0
    t2_compiled: int = 0
    t2_entered: int = 0
    t2_failed: int = 0
    exit_total: int = 0
    top_exit: dict[str, Any] | None = None
    work_action: str = ""
    work_target: str = ""
    work_proto: str = ""
    work_priority: int = 0
    readiness: str = ""
    runtime_summary: dict[str, Any] = field(default_factory=dict)
    tier2_call_summary: dict[str, Any] = field(default_factory=dict)
    pprof_runs: int = 0
    pprof_script_repeat: int = 0
    pprof_samples_seconds: float = 0.0
    pprof_effective: bool = False
    artifact_dir: str = ""
    artifacts: dict[str, str] = field(default_factory=dict)


def run(cmd: list[str], cwd: Path, timeout: int) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        cmd,
        cwd=cwd,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=timeout,
        check=False,
    )


def parse_counter(pattern: re.Pattern[str], text: str) -> int:
    match = pattern.search(text)
    return int(match.group(1)) if match else 0


def parse_time(text: str) -> float | None:
    match = TIME_RE.search(text)
    return float(match.group(1)) if match else None


def extract_last_json(text: str) -> Any:
    decoder = json.JSONDecoder()
    last: Any = None
    index = 0
    while index < len(text):
        ch = text[index]
        if ch in "[{":
            try:
                value, end = decoder.raw_decode(text[index:])
            except json.JSONDecodeError:
                index += 1
                continue
            last = value
            index += end
            continue
        index += 1
    return last


def write_json(path: Path, value: Any) -> None:
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def run_artifact(
    name: str,
    cmd: list[str],
    root: Path,
    out_dir: Path,
    timeout: int,
    json_expected: bool = False,
) -> CommandArtifact:
    started = time.perf_counter()
    proc = run(cmd, root, timeout)
    wall = time.perf_counter() - started
    raw_path = out_dir / f"{name}.raw.txt"
    raw_path.write_text(proc.stdout)
    json_path: Path | None = None
    if json_expected:
        value = extract_last_json(proc.stdout)
        if value is not None:
            json_path = out_dir / f"{name}.json"
            write_json(json_path, value)
    return CommandArtifact(
        name=name,
        command=cmd,
        raw=str(raw_path),
        json=str(json_path) if json_path else None,
        status="ok" if proc.returncode == 0 else "error",
        exit_code=proc.returncode,
        wall_seconds=wall,
    )


def parse_pprof_total_samples_seconds(text: str) -> float:
    match = re.search(r"Total samples =\s*([0-9.]+)(ns|us|ms|s)?\b", text)
    if not match:
        return 0.0
    value = float(match.group(1))
    unit = match.group(2) or "s"
    if unit == "ns":
        return value / 1e9
    if unit == "us":
        return value / 1e6
    if unit == "ms":
        return value / 1e3
    return value


def collect_effective_cpu_profile(
    root: Path,
    binary: Path,
    base_cmd: list[str],
    script: Path,
    out_dir: Path,
    timeout: int,
    min_samples_seconds: float,
    max_runs: int,
) -> tuple[int, int, float, bool]:
    source = script.read_text()
    profiles: list[Path] = []
    top_text = ""
    samples = 0.0
    script_repeat = 1
    last_repeat = 0
    for index in range(1, max_runs + 1):
        last_repeat = script_repeat
        profile_script = out_dir / f"profile_repeat_{script_repeat:04d}.gs"
        profile_script.write_text(("\n\n").join(source for _ in range(script_repeat)) + "\n")
        cpu = out_dir / f"cpu_{index:03d}.pprof"
        proc = run([*base_cmd, "-cpuprofile", str(cpu), str(profile_script)], root, timeout)
        (out_dir / f"cpu_profile_run_{index:03d}.raw.txt").write_text(proc.stdout)
        if not cpu.exists():
            continue
        profiles.append(cpu)
        top = run(["go", "tool", "pprof", "-top", "-nodecount=30", str(binary), str(cpu)], root, timeout)
        top_text = top.stdout
        samples = parse_pprof_total_samples_seconds(top_text)
        (out_dir / "cpu.pprof.txt").write_text(top_text)
        if samples >= min_samples_seconds:
            break
        script_repeat *= 2
    if profiles:
        (out_dir / "cpu.profiles.txt").write_text("\n".join(str(path) for path in profiles) + "\n")
    return len(profiles), last_repeat, samples, samples >= min_samples_seconds


def summarize_runtime_paths(data: Any) -> dict[str, Any]:
    if not isinstance(data, dict):
        return {}
    native = data.get("native_call") if isinstance(data.get("native_call"), dict) else {}
    coro = data.get("coroutine") if isinstance(data.get("coroutine"), dict) else {}
    table = data.get("table_array") if isinstance(data.get("table_array"), dict) else {}
    table_string = data.get("table_string") if isinstance(data.get("table_string"), dict) else {}
    string_format = data.get("string_format") if isinstance(data.get("string_format"), dict) else {}
    string_concat = data.get("string_concat") if isinstance(data.get("string_concat"), dict) else {}
    return {
        "native_fast": native.get("fast", 0),
        "native_fallback": native.get("fallback", 0),
        "coroutine_resume": coro.get("resume", 0),
        "coroutine_yield": coro.get("yield", 0),
        "table_array_get_hot": table.get("get_hot", 0),
        "table_array_get_fallback": table.get("get_fallback", 0),
        "table_array_set_hot": table.get("set_hot", 0),
        "table_array_set_fallback": table.get("set_fallback", 0),
        "table_string_get_cache_hit": table_string.get("get_cache_hit", 0),
        "table_string_get_scan_hit": table_string.get("get_scan_hit", 0),
        "table_string_get_map_hit": table_string.get("get_map_hit", 0),
        "table_string_get_miss": table_string.get("get_miss", 0),
        "table_string_set_cache_hit": table_string.get("set_cache_hit", 0),
        "table_string_set_scan_hit": table_string.get("set_scan_hit", 0),
        "table_string_set_append": table_string.get("set_append", 0),
        "table_string_set_map": table_string.get("set_map", 0),
        "table_string_set_promote": table_string.get("set_promote", 0),
        "string_format_fast": string_format.get("fast", 0),
        "string_format_fallback": string_format.get("fallback", 0),
        "string_concat_lazy": string_concat.get("lazy", 0),
        "string_concat_builder": string_concat.get("builder", 0),
    }


def summarize_tier2_calls(data: Any) -> dict[str, Any]:
    if not isinstance(data, dict):
        return {}
    calls = data.get("calls")
    if not isinstance(calls, list):
        return {}
    out: dict[str, Any] = {}
    for row in calls:
        if not isinstance(row, dict):
            continue
        kind = row.get("kind") or "call"
        outcome = row.get("outcome") or "unknown"
        key = f"{kind}_{outcome}"
        out[key] = int(out.get(key, 0)) + int(row.get("count") or 0)
    return out


def top_exit_site(exit_json: Any) -> dict[str, Any] | None:
    if not isinstance(exit_json, dict):
        return None
    sites = exit_json.get("sites")
    if not isinstance(sites, list) or not sites:
        return None
    candidates = [site for site in sites if isinstance(site, dict)]
    if not candidates:
        return None
    return max(candidates, key=lambda site: int(site.get("count") or 0))


def top_work_item(work_json: Any) -> dict[str, Any] | None:
    if not isinstance(work_json, list) or not work_json:
        return None
    first = work_json[0]
    return first if isinstance(first, dict) else None


def artifact_paths(artifacts: list[CommandArtifact]) -> dict[str, str]:
    out: dict[str, str] = {}
    for artifact in artifacts:
        out[f"{artifact.name}_raw"] = artifact.raw
        if artifact.json:
            out[f"{artifact.name}_json"] = artifact.json
    return out


def add_optional_artifact_paths(paths: dict[str, str], bench_out: Path) -> dict[str, str]:
    for name in ("cpu.pprof.txt", "cpu.profiles.txt", "warm_pcmap.json", "warm_pcmap.raw.txt"):
        path = bench_out / name
        if path.exists():
            paths[name.replace(".", "_")] = str(path)
    return paths


def bench_dir_name(spec: timing.BenchmarkSpec) -> str:
    return f"{spec.group}__{spec.name}"


def collect_for_benchmark(
    root: Path,
    binary: Path,
    spec: timing.BenchmarkSpec,
    script: Path,
    out_dir: Path,
    timeout: int,
    pprof: bool,
    pprof_min_samples_seconds: float,
    pprof_max_runs: int,
    warm_dump: bool,
) -> DiagnosticRow:
    bench_out = out_dir / bench_dir_name(spec)
    bench_out.mkdir(parents=True, exist_ok=True)
    base = [str(binary), "-jit"]
    artifacts: list[CommandArtifact] = []

    artifacts.append(
        run_artifact(
            "summary",
            [*base, "-jit-stats", "-exit-stats", str(script)],
            root,
            bench_out,
            timeout,
        )
    )
    artifacts.append(
        run_artifact(
            "exit_stats",
            [*base, "-exit-stats-json", str(script)],
            root,
            bench_out,
            timeout,
            json_expected=True,
        )
    )
    artifacts.append(
        run_artifact(
            "runtime_paths",
            [*base, "-runtime-path-stats-json", str(script)],
            root,
            bench_out,
            timeout,
            json_expected=True,
        )
    )
    artifacts.append(
        run_artifact(
            "tier2_perf",
            [*base, "-tier2-perf-stats-json", str(script)],
            root,
            bench_out,
            timeout,
            json_expected=True,
        )
    )
    artifacts.append(
        run_artifact(
            "spec_state",
            [*base, "-tier2-spec-state-json", str(script)],
            root,
            bench_out,
            timeout,
            json_expected=True,
        )
    )
    artifacts.append(
        run_artifact(
            "spec_worklist",
            [*base, "-tier2-spec-worklist-json", str(script)],
            root,
            bench_out,
            timeout,
            json_expected=True,
        )
    )

    pprof_runs = 0
    pprof_script_repeat = 0
    pprof_samples = 0.0
    pprof_effective = False
    if pprof:
        pprof_runs, pprof_script_repeat, pprof_samples, pprof_effective = collect_effective_cpu_profile(
            root,
            binary,
            base,
            script,
            bench_out,
            timeout,
            pprof_min_samples_seconds,
            pprof_max_runs,
        )

    if warm_dump:
        warm_dir = bench_out / "warm"
        warm_cpu = bench_out / "warm.pprof"
        artifacts.append(
            run_artifact(
                "warm_dump_run",
                [*base, "-cpuprofile", str(warm_cpu), "-jit-dump-warm", str(warm_dir), str(script)],
                root,
                bench_out,
                timeout,
            )
        )
        if warm_dir.exists() and warm_cpu.exists():
            mapped_json = bench_out / "warm_pcmap.json"
            proc = run(
                [
                    "python3",
                    "benchmarks/jit_addr_map.py",
                    "--binary",
                    str(binary),
                    "--warm-dir",
                    str(warm_dir),
                    "--profile",
                    str(warm_cpu),
                    "--json",
                    str(mapped_json),
                ],
                root,
                timeout,
            )
            (bench_out / "warm_pcmap.raw.txt").write_text(proc.stdout)

    summary_text = Path(artifacts[0].raw).read_text(errors="replace")
    exit_json = read_artifact_json(artifacts, "exit_stats")
    runtime_json = read_artifact_json(artifacts, "runtime_paths")
    tier2_perf_json = read_artifact_json(artifacts, "tier2_perf")
    work_json = read_artifact_json(artifacts, "spec_worklist")
    top_exit = top_exit_site(exit_json)
    work = top_work_item(work_json)
    readiness = ""
    if work:
        readiness_data = work.get("feedback_readiness")
        if isinstance(readiness_data, dict):
            readiness = str(readiness_data.get("kind") or "")
    row = DiagnosticRow(
        benchmark=spec.name,
        group=spec.group,
        script=str(script),
        status="ok" if all(a.status == "ok" for a in artifacts[:6]) else "partial",
        time_seconds=parse_time(summary_text),
        t2_attempted=parse_counter(T2_ATTEMPTED_RE, summary_text),
        t2_compiled=parse_counter(T2_COMPILED_RE, summary_text),
        t2_entered=parse_counter(T2_ENTERED_RE, summary_text),
        t2_failed=parse_counter(T2_FAILED_RE, summary_text),
        exit_total=parse_counter(EXIT_TOTAL_RE, summary_text),
        top_exit=top_exit,
        runtime_summary=summarize_runtime_paths(runtime_json),
        tier2_call_summary=summarize_tier2_calls(tier2_perf_json),
        pprof_runs=pprof_runs,
        pprof_script_repeat=pprof_script_repeat,
        pprof_samples_seconds=pprof_samples,
        pprof_effective=pprof_effective,
        artifact_dir=str(bench_out),
        artifacts=add_optional_artifact_paths(artifact_paths(artifacts), bench_out),
    )
    if work:
        row.work_action = str(work.get("action") or "")
        row.work_target = str(work.get("target") or "")
        row.work_proto = str(work.get("proto_name") or "")
        row.work_priority = int(work.get("priority") or 0)
        row.readiness = readiness
    return row


def read_artifact_json(artifacts: list[CommandArtifact], name: str) -> Any:
    for artifact in artifacts:
        if artifact.name == name and artifact.json:
            return json.loads(Path(artifact.json).read_text())
    return None


def render_summary(rows: list[DiagnosticRow]) -> str:
    lines = [
        "# Benchmark Diagnostics",
        "",
        "| Benchmark | Time | T2 a/c/e | Exits | Top Exit | Work Target | Runtime Hot Counters | CPU Profile | Artifacts |",
        "|---|---:|---:|---:|---|---|---|---|---|",
    ]
    for row in rows:
        top = "-"
        if row.top_exit:
            top = f"{row.top_exit.get('exit_name')} {row.top_exit.get('reason')} pc={row.top_exit.get('pc')} count={row.top_exit.get('count')}"
        work = "-"
        if row.work_action or row.work_target:
            work = f"{row.work_action}/{row.work_target} {row.work_proto} p={row.work_priority} {row.readiness}".strip()
        runtime_bits = []
        for key in (
            "native_fallback",
            "coroutine_resume",
            "coroutine_yield",
            "table_array_get_hot",
            "table_array_set_hot",
            "table_string_get_cache_hit",
            "table_string_get_scan_hit",
            "table_string_get_map_hit",
            "table_string_get_miss",
            "table_string_set_append",
            "table_string_set_map",
            "table_string_set_promote",
            "string_format_fast",
            "string_concat_lazy",
            "string_concat_builder",
        ):
            value = row.runtime_summary.get(key)
            if value:
                runtime_bits.append(f"{key}={value}")
        for key in sorted(row.tier2_call_summary):
            value = row.tier2_call_summary.get(key)
            if value:
                runtime_bits.append(f"tier2_{key}={value}")
        lines.append(
            "| "
            + " | ".join(
                [
                    f"{row.group}/{row.benchmark}",
                    "-" if row.time_seconds is None else f"{row.time_seconds:.3f}s",
                    f"{row.t2_attempted}/{row.t2_compiled}/{row.t2_entered}",
                    str(row.exit_total),
                    top,
                    work,
                    ", ".join(runtime_bits) or "-",
                    (
                        "-"
                        if row.pprof_runs == 0
                        else (
                            f"{'ok' if row.pprof_effective else 'low'} "
                            f"{row.pprof_samples_seconds:.3f}s/"
                            f"{row.pprof_runs} runs/repeat {row.pprof_script_repeat}"
                        )
                    ),
                    f"`{row.artifact_dir}`",
                ]
            )
            + " |"
        )
    return "\n".join(lines) + "\n"


def run_timing_compare(root: Path, specs: list[timing.BenchmarkSpec], args: argparse.Namespace, out_dir: Path) -> None:
    if args.no_timing:
        return
    cmd = [
        "python3",
        "benchmarks/timing_compare.py",
        "--runs",
        str(args.runs),
        "--warmup",
        str(args.warmup),
        "--timeout",
        str(args.timeout),
        "--json",
        str(out_dir / "timing_compare.json"),
        "--markdown",
        str(out_dir / "timing_compare.md"),
    ]
    for group in sorted({spec.group for spec in specs}):
        cmd += ["--group", group]
    for spec in specs:
        cmd += ["--bench", spec.benchmark_id]
    for value in args.scale or []:
        cmd += ["--scale", value]
    for value in args.param or []:
        cmd += ["--param", value]
    if args.scale_profile != "none":
        cmd += ["--scale-profile", args.scale_profile]
    proc = run(cmd, root, args.timeout * max(2, len(specs) * 4))
    (out_dir / "timing_compare.raw.txt").write_text(proc.stdout)


def positive_int(value: str) -> int:
    parsed = int(value)
    if parsed <= 0:
        raise argparse.ArgumentTypeError("must be > 0")
    return parsed


def groups_for_args(args: argparse.Namespace) -> list[str]:
    if args.all_groups:
        return list(timing.GROUPS)
    groups = list(args.group or ["suite"])
    for selector in args.bench or []:
        if "/" not in selector:
            continue
        group, _name = selector.split("/", 1)
        if group in timing.GROUPS and group not in groups:
            groups.append(group)
    return groups


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bench", action="append", help="benchmark name or group/name; repeatable")
    parser.add_argument("--group", action="append", choices=timing.GROUPS, help="benchmark group; repeatable")
    parser.add_argument("--all-groups", action="store_true", help="diagnose suite, extended, and variants")
    parser.add_argument("--out-dir", type=Path, default=None)
    parser.add_argument("--timeout", type=positive_int, default=120)
    parser.add_argument("--runs", type=positive_int, default=5, help="timing_compare measured runs")
    parser.add_argument("--warmup", type=int, default=1, help="timing_compare warmup samples")
    parser.add_argument("--scale", action="append", help="temporary GScript/Lua scale override")
    parser.add_argument("--param", action="append", help="alias for --scale")
    parser.add_argument("--scale-profile", choices=("none", "hot"), default="none")
    parser.add_argument("--no-timing", action="store_true", help="skip timing_compare")
    parser.add_argument("--pprof", action="store_true", help="collect CPU pprof for every selected benchmark")
    parser.add_argument(
        "--pprof-min-samples-ms",
        type=float,
        default=50.0,
        help="minimum CPU profile samples required before pprof is marked effective",
    )
    parser.add_argument(
        "--pprof-max-runs",
        type=positive_int,
        default=8,
        help="maximum repeated profile runs per benchmark used to reach --pprof-min-samples-ms",
    )
    parser.add_argument("--warm-dump", action="store_true", help="collect -jit-dump-warm and pcmap for every selected benchmark")
    args = parser.parse_args()

    if args.warmup < 0:
        parser.error("--warmup must be >= 0")

    root = Path(__file__).resolve().parents[1]
    groups = groups_for_args(args)
    specs = timing.select_specs(timing.discover_specs(root, groups), args.bench)
    out_dir = args.out_dir or Path(tempfile.mkdtemp(prefix="gscript_diagnose_"))
    out_dir.mkdir(parents=True, exist_ok=True)

    scale_values = [*(args.scale or []), *(args.param or [])]
    try:
        user_scale = timing.parse_scale_overrides(scale_values)
        profile_scale = timing.parse_scale_overrides(timing.HOT_SCALE_PROFILE if args.scale_profile == "hot" else [])
    except argparse.ArgumentTypeError as exc:
        parser.error(str(exc))
    timing.validate_scale_selectors(specs, user_scale)
    profile_scale = timing.filter_scale_overrides_for_specs(specs, profile_scale)
    scale_overrides = [*profile_scale, *user_scale]

    binary = out_dir / "gscript_diag"
    timing.build_gscript(root, binary)
    run_timing_compare(root, specs, args, out_dir)

    scale_tempdir = Path(tempfile.mkdtemp(prefix="gscript_diagnose_scale_"))
    rows: list[DiagnosticRow] = []
    for spec in specs:
        overrides = timing.scale_overrides_for(spec, scale_overrides)
        script, _changes = timing.scaled_path(root, spec.gscript_rel, spec, "diagnose", "gscript", scale_tempdir, overrides)
        if script is None:
            continue
        rows.append(
            collect_for_benchmark(
                root,
                binary,
                spec,
                script,
                out_dir,
                args.timeout,
                args.pprof,
                args.pprof_min_samples_ms / 1000.0,
                args.pprof_max_runs,
                args.warm_dump,
            )
        )

    summary = {
        "out_dir": str(out_dir),
        "benchmarks": [asdict(row) for row in rows],
    }
    write_json(out_dir / "diagnostics.json", summary)
    (out_dir / "diagnostics.md").write_text(render_summary(rows))
    print(f"Wrote diagnostics: {out_dir / 'diagnostics.json'}")
    print(f"Wrote markdown:    {out_dir / 'diagnostics.md'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
