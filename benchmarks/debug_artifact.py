#!/usr/bin/env python3
"""Build one JSON artifact from existing benchmark/debug outputs.

This is intentionally an offline aggregator. It does not build gscript or run
benchmarks; callers feed it files already produced by timing_compare.py,
strict_guard.py, profile_exits.py, runtime/perf stats flags, and
-jit-dump-warm.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


SCHEMA_VERSION = 1
TIME_RE = re.compile(r"Time:\s*([0-9]+(?:\.[0-9]+)?)s\b")
NUMBER_LINE_RE = re.compile(r"^\s*([A-Za-z0-9_.\-/]+)\s*[:=]\s*([0-9]+(?:\.[0-9]+)?)\s*$")
PERF_ROW_RE = re.compile(r"^\s*([A-Za-z0-9_.\-/]+):\s*count=(\d+)\s+total=(\d+)ns\s+avg=(\d+)ns\s*$")


def read_text(path: Path | None) -> str | None:
    if path is None or not path.exists():
        return None
    return path.read_text(errors="replace")


def read_json_or_embedded(path: Path | None) -> Any:
    text = read_text(path)
    if text is None:
        return None
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        pass

    decoder = json.JSONDecoder()
    last: Any = None
    for index, ch in enumerate(text):
        if ch not in "[{":
            continue
        try:
            value, _ = decoder.raw_decode(text[index:])
        except json.JSONDecodeError:
            continue
        last = value
    return last


def input_status(path: Path | None, kind: str, required: bool = False) -> dict[str, Any]:
    if path is None:
        return {"kind": kind, "path": None, "status": "not-provided" if not required else "missing"}
    if path.exists():
        size = path.stat().st_size if path.is_file() else None
        return {"kind": kind, "path": str(path), "status": "ok", "bytes": size}
    return {"kind": kind, "path": str(path), "status": "missing"}


def git_commit(root: Path) -> str | None:
    proc = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=root,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    if proc.returncode != 0:
        return None
    return proc.stdout.strip() or None


def seconds_from_value(value: Any) -> float | None:
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        return float(value)
    if isinstance(value, str):
        match = TIME_RE.search(value)
        if match:
            return float(match.group(1))
    return None


def normalize_benchmark_outputs(paths: list[Path]) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for path in paths:
        data = read_json_or_embedded(path)
        if not isinstance(data, dict):
            continue
        source = str(path)
        results = data.get("results")
        if isinstance(results, list):
            rows.extend(normalize_result_list(results, source))
        elif isinstance(results, dict):
            rows.extend(normalize_legacy_result_map(results, source))
    return rows


def normalize_result_list(results: list[Any], source: str) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for item in results:
        if not isinstance(item, dict):
            continue
        group = item.get("group") or "suite"
        benchmark = item.get("benchmark") or item.get("name")
        if not benchmark:
            continue
        modes = item.get("modes")
        if isinstance(modes, dict):
            for mode, subjects in modes.items():
                if isinstance(subjects, dict):
                    for subject, result in subjects.items():
                        rows.append(normalized_row(source, group, benchmark, str(mode), str(subject), result))
        else:
            for mode, result in item.items():
                if mode in {"benchmark", "name", "group", "scale", "samples"}:
                    continue
                if isinstance(result, dict):
                    rows.append(normalized_row(source, group, benchmark, str(mode), "current", result))
    return rows


def normalize_legacy_result_map(results: dict[str, Any], source: str) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for benchmark, modes in results.items():
        if not isinstance(modes, dict):
            continue
        for mode, value in modes.items():
            rows.append(
                {
                    "source": source,
                    "group": "suite",
                    "benchmark": str(benchmark),
                    "mode": "default" if mode == "jit" else str(mode),
                    "subject": "current",
                    "status": "ok" if seconds_from_value(value) is not None else "unknown",
                    "seconds": seconds_from_value(value),
                    "time_source": "script" if seconds_from_value(value) is not None else "",
                    "repeat": None,
                    "t2_attempted": 0,
                    "t2_entered": 0,
                    "t2_failed": 0,
                    "exit_total": 0,
                }
            )
    return rows


def normalized_row(source: str, group: Any, benchmark: Any, mode: str, subject: str, result: Any) -> dict[str, Any]:
    result = result if isinstance(result, dict) else {}
    stats = result.get("stats") if isinstance(result.get("stats"), dict) else {}
    return {
        "source": source,
        "group": str(group),
        "benchmark": str(benchmark),
        "mode": mode,
        "subject": subject,
        "status": str(result.get("status") or "unknown"),
        "seconds": result.get("seconds") if isinstance(result.get("seconds"), (int, float)) else stats.get("median"),
        "time_source": result.get("source") or result.get("time_source") or "",
        "repeat": result.get("repeat"),
        "t2_attempted": int(result.get("t2_attempted") or 0),
        "t2_entered": int(result.get("t2_entered") or 0),
        "t2_failed": int(result.get("t2_failed") or 0),
        "exit_total": int(result.get("exit_total") or result.get("exits") or 0),
    }


def summarize_benchmarks(rows: list[dict[str, Any]]) -> dict[str, Any]:
    statuses = Counter(str(row.get("status", "unknown")) for row in rows)
    return {
        "rows": len(rows),
        "benchmarks": len({(row.get("group"), row.get("benchmark")) for row in rows}),
        "statuses": dict(sorted(statuses.items())),
        "total_exits": sum(int(row.get("exit_total") or 0) for row in rows),
        "total_t2_entered": sum(int(row.get("t2_entered") or 0) for row in rows),
    }


def summarize_exit_stats(path: Path | None) -> dict[str, Any]:
    data = read_json_or_embedded(path)
    if data is None:
        return {"status": "missing", "total": 0, "by_exit_code": {}, "top_sites": []}
    if isinstance(data, dict) and isinstance(data.get("results"), list):
        by_code: Counter[str] = Counter()
        sites: list[dict[str, Any]] = []
        statuses: Counter[str] = Counter()
        for row in data["results"]:
            if not isinstance(row, dict):
                continue
            statuses[str(row.get("status", "unknown"))] += 1
            stats = row.get("stats") if isinstance(row.get("stats"), dict) else {}
            for name, count in (stats.get("by_exit_code") or {}).items():
                by_code[str(name)] += int(count)
            for site in stats.get("sites") or []:
                if isinstance(site, dict):
                    site = dict(site)
                    site.setdefault("benchmark", row.get("benchmark"))
                    sites.append(site)
        sites.sort(key=lambda site: int(site.get("count") or 0), reverse=True)
        return {
            "status": "ok",
            "total": sum(by_code.values()),
            "by_exit_code": dict(sorted(by_code.items())),
            "top_sites": sites[:20],
            "result_statuses": dict(sorted(statuses.items())),
        }
    if isinstance(data, dict):
        sites = data.get("sites") if isinstance(data.get("sites"), list) else []
        return {
            "status": "ok",
            "total": int(data.get("total") or 0),
            "by_exit_code": data.get("by_exit_code") or {},
            "top_sites": sites[:20],
        }
    return {"status": "parse_error", "total": 0, "by_exit_code": {}, "top_sites": []}


def flatten_numbers(data: Any, prefix: str = "") -> dict[str, float]:
    out: dict[str, float] = {}
    if isinstance(data, dict):
        for key, value in data.items():
            child = f"{prefix}.{key}" if prefix else str(key)
            out.update(flatten_numbers(value, child))
    elif isinstance(data, list):
        for index, value in enumerate(data):
            out.update(flatten_numbers(value, f"{prefix}[{index}]"))
    elif isinstance(data, (int, float)) and not isinstance(data, bool):
        out[prefix] = float(data)
    return out


def parse_number_text(path: Path | None) -> dict[str, float]:
    text = read_text(path) or ""
    numbers: dict[str, float] = {}
    scope: list[tuple[int, str]] = []
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        if stripped in {"Runtime Path Statistics:", "Tier 2 Performance Diagnostics:", "Tier 2 Exit Profile:"}:
            scope.clear()
            continue
        indent = len(line) - len(line.lstrip(" "))
        while scope and indent <= scope[-1][0]:
            scope.pop()
        if stripped.endswith(":") and not NUMBER_LINE_RE.match(stripped):
            scope.append((indent, stripped[:-1]))
            continue
        match = NUMBER_LINE_RE.match(line)
        if match:
            key = ".".join([part for _, part in scope] + [match.group(1)])
            numbers[key] = float(match.group(2))
    return numbers


def summarize_runtime_stats(path: Path | None) -> dict[str, Any]:
    data = read_json_or_embedded(path)
    if isinstance(data, (dict, list)):
        return {"status": "ok", "source": "json", "numbers": flatten_numbers(data)}
    if path is not None and path.exists():
        return {"status": "ok", "source": "text", "numbers": parse_number_text(path)}
    return {"status": "missing", "source": "missing", "numbers": {}}


def summarize_perf_stats(path: Path | None) -> dict[str, Any]:
    data = read_json_or_embedded(path)
    if isinstance(data, dict):
        rows = data.get("rows") if isinstance(data.get("rows"), list) else []
        return summarize_perf_rows(rows, data.get("enabled"), "json")
    if path is not None and path.exists():
        rows = []
        enabled: bool | None = None
        for line in (read_text(path) or "").splitlines():
            stripped = line.strip()
            if stripped.startswith("enabled:"):
                enabled = stripped.split(":", 1)[1].strip().lower() == "true"
            match = PERF_ROW_RE.match(line)
            if match:
                rows.append(
                    {
                        "name": match.group(1),
                        "count": int(match.group(2)),
                        "nanos": int(match.group(3)),
                        "avg_nanos": int(match.group(4)),
                    }
                )
        return summarize_perf_rows(rows, enabled, "text")
    return {"status": "missing", "source": "missing", "enabled": False, "rows": [], "total_nanos": 0}


def summarize_perf_rows(rows: list[Any], enabled: Any, source: str) -> dict[str, Any]:
    clean = [row for row in rows if isinstance(row, dict)]
    clean.sort(key=lambda row: int(row.get("nanos") or 0), reverse=True)
    return {
        "status": "ok",
        "source": source,
        "enabled": bool(enabled),
        "rows": clean,
        "total_nanos": sum(int(row.get("nanos") or 0) for row in clean),
        "total_count": sum(int(row.get("count") or 0) for row in clean),
    }


def summarize_speculation_state(path: Path | None) -> dict[str, Any]:
    data = read_json_or_embedded(path)
    if not isinstance(data, list):
        return {
            "status": "missing" if path is None or not path.exists() else "parse_error",
            "protos": 0,
            "compiled": 0,
            "failed": 0,
            "suppressed": 0,
            "states": [],
        }
    states = [row for row in data if isinstance(row, dict)]
    return {
        "status": "ok",
        "protos": len(states),
        "compiled": sum(1 for row in states if row.get("compiled")),
        "failed": sum(1 for row in states if row.get("failed")),
        "suppressed": sum(int(row.get("suppressed_count") or 0) for row in states),
        "top_suppressed": sorted(
            states,
            key=lambda row: int(row.get("suppressed_count") or 0),
            reverse=True,
        )[:20],
        "states": states,
    }


def summarize_warm_dump(path: Path | None) -> dict[str, Any]:
    if path is None or not path.exists():
        return {"status": "missing", "path": str(path) if path else None}
    manifest = read_json_or_embedded(path / "manifest.json")
    pcmap = read_json_or_embedded(path / "pcmap.json")
    if not isinstance(manifest, dict):
        return {"status": "parse_error", "path": str(path)}
    protos = manifest.get("protos") if isinstance(manifest.get("protos"), list) else []
    statuses = Counter(str(proto.get("status", "unknown")) for proto in protos if isinstance(proto, dict))
    file_kinds: Counter[str] = Counter()
    for proto in protos:
        if isinstance(proto, dict) and isinstance(proto.get("files"), dict):
            file_kinds.update(proto["files"].keys())
    functions = pcmap.get("functions") if isinstance(pcmap, dict) and isinstance(pcmap.get("functions"), list) else []
    return {
        "status": "ok",
        "path": str(path),
        "proto_filter": manifest.get("proto_filter", ""),
        "protos": len(protos),
        "statuses": dict(sorted(statuses.items())),
        "compiled": sum(1 for proto in protos if isinstance(proto, dict) and proto.get("compiled")),
        "entered": sum(1 for proto in protos if isinstance(proto, dict) and proto.get("entered")),
        "code_bytes": sum(int(proto.get("code_bytes") or 0) for proto in protos if isinstance(proto, dict)),
        "insn_count": sum(int(proto.get("insn_count") or 0) for proto in protos if isinstance(proto, dict)),
        "file_kinds": dict(sorted(file_kinds.items())),
        "pcmap_functions": len(functions),
        "pcmap_ranges": sum(len(fn.get("ranges") or []) for fn in functions if isinstance(fn, dict)),
    }


def summarize_tiering(rows: list[dict[str, Any]], warm_dump: dict[str, Any]) -> dict[str, Any]:
    return {
        "t2_attempted": sum(int(row.get("t2_attempted") or 0) for row in rows),
        "t2_entered": sum(int(row.get("t2_entered") or 0) for row in rows),
        "t2_failed": sum(int(row.get("t2_failed") or 0) for row in rows),
        "warm_dump_compiled": int(warm_dump.get("compiled") or 0),
        "warm_dump_entered": int(warm_dump.get("entered") or 0),
    }


def summarize_gates(rows: list[dict[str, Any]], exit_stats: dict[str, Any], warm_dump: dict[str, Any]) -> dict[str, Any]:
    reasons: Counter[str] = Counter()
    for row in rows:
        status = str(row.get("status") or "")
        if status and status != "ok":
            reasons[status] += 1
    for site in exit_stats.get("top_sites") or []:
        if isinstance(site, dict):
            reason = site.get("reason") or site.get("kind") or site.get("exit")
            if reason:
                reasons[str(reason)] += int(site.get("count") or 1)
    for status, count in (warm_dump.get("statuses") or {}).items():
        if status != "entered":
            reasons[f"warm_dump:{status}"] += int(count)
    return {
        "schema": "gate-summary-v1",
        "reason_counts": dict(sorted(reasons.items())),
    }


def summarize_profiles(warm_dump: dict[str, Any]) -> dict[str, Any]:
    return {
        "warm_dump_protos": int(warm_dump.get("protos") or 0),
        "warm_dump_insn_count": int(warm_dump.get("insn_count") or 0),
        "warm_dump_code_bytes": int(warm_dump.get("code_bytes") or 0),
        "pcmap_functions": int(warm_dump.get("pcmap_functions") or 0),
        "pcmap_ranges": int(warm_dump.get("pcmap_ranges") or 0),
    }


def build_artifact(args: argparse.Namespace, root: Path) -> dict[str, Any]:
    benchmark_inputs = args.benchmark_json or []
    inputs = {f"benchmark_json[{i}]": input_status(path, "benchmark_json") for i, path in enumerate(benchmark_inputs)}
    inputs.update(
        {
            "exit_stats": input_status(args.exit_stats, "exit_stats"),
            "runtime_path_stats": input_status(args.runtime_path_stats, "runtime_path_stats"),
            "tier2_perf_stats": input_status(args.perf_stats, "tier2_perf_stats"),
            "tier2_speculation_state": input_status(args.spec_state, "tier2_speculation_state"),
            "warm_dump": input_status(args.warm_dump, "warm_dump"),
        }
    )
    benchmark_rows = normalize_benchmark_outputs(benchmark_inputs)
    exit_stats = summarize_exit_stats(args.exit_stats)
    runtime_path_stats = summarize_runtime_stats(args.runtime_path_stats)
    perf_stats = summarize_perf_stats(args.perf_stats)
    spec_state = summarize_speculation_state(args.spec_state)
    warm_dump = summarize_warm_dump(args.warm_dump)
    benchmark_summary = summarize_benchmarks(benchmark_rows)
    tiering = summarize_tiering(benchmark_rows, warm_dump)
    gates = summarize_gates(benchmark_rows, exit_stats, warm_dump)
    profiles = summarize_profiles(warm_dump)
    return {
        "schema_version": SCHEMA_VERSION,
        "generated_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "label": args.label or "",
        "metadata": {
            "repo": str(root),
            "commit": git_commit(root),
        },
        "inputs": inputs,
        "benchmark_summary": benchmark_summary,
        "benchmarks": benchmark_rows,
        "timing": {
            "summary": benchmark_summary,
            "rows": benchmark_rows,
        },
        "tiering": tiering,
        "gates": gates,
        "exits": exit_stats,
        "runtime_paths": runtime_path_stats,
        "specialization": spec_state,
        "profiles": profiles,
        "debug": {
            "exit_stats": exit_stats,
            "runtime_path_stats": runtime_path_stats,
            "tier2_perf_stats": perf_stats,
            "tier2_speculation_state": spec_state,
            "warm_dump": warm_dump,
        },
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--benchmark-json", type=Path, action="append", help="timing_compare/strict_guard/regression_guard JSON; repeatable")
    parser.add_argument("--exit-stats", type=Path, help="profile_exits JSON, raw -exit-stats-json output, or embedded JSON")
    parser.add_argument("--runtime-path-stats", type=Path, help="raw -runtime-path-stats[-json] output")
    parser.add_argument("--perf-stats", type=Path, help="raw -tier2-perf-stats[-json] output")
    parser.add_argument("--spec-state", type=Path, help="raw -tier2-spec-state-json output")
    parser.add_argument("--warm-dump", type=Path, help="directory produced by -jit-dump-warm")
    parser.add_argument("--label", help="free-form artifact label")
    parser.add_argument("--out", type=Path, help="write artifact JSON to this path; stdout when omitted")
    args = parser.parse_args()

    root = Path(__file__).resolve().parents[1]
    artifact = build_artifact(args, root)
    body = json.dumps(artifact, indent=2, sort_keys=True) + "\n"
    if args.out:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(body)
    else:
        print(body, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
