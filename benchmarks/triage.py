#!/usr/bin/env python3
"""Create a focused performance triage bundle for one or more benchmarks.

This script orchestrates the existing tools instead of replacing them:

1. timing_compare.py decides whether a gap is real and records timing source.
2. profile_exits.py explains Tier 2 exit/deopt pressure for suite benchmarks.
3. scripts/diag.sh and pprof can be enabled when codegen/runtime detail is needed.
4. -jit-dump-warm plus jit_addr_map.py can map sampled JIT PCs back to IR.

The output is intended for optimization planning, not release gating.
"""

from __future__ import annotations

import argparse
import json
import re
import shutil
import subprocess
import sys
import tempfile
from collections import Counter
from dataclasses import dataclass, field
from pathlib import Path


PPROF_ROW_RE = re.compile(
    r"^\s*(?P<flat>[0-9.]+)(?P<flat_unit>ns|us|ms|s)?\s+"
    r"(?P<flat_pct>[0-9.]+)%\s+"
    r"(?P<sum_pct>[0-9.]+)%\s+"
    r"(?P<cum>[0-9.]+)(?P<cum_unit>ns|us|ms|s)?\s+"
    r"(?P<cum_pct>[0-9.]+)%\s+"
    r"(?P<name>\S+)"
)

EXIT_REASON_WORDS = (
    "deopt",
    "guard",
    "shape",
    "type",
    "fallback",
    "resume",
    "spec",
    "bail",
    "miss",
    "check",
)

RUNTIME_ALLOC_KEYS = ("alloc", "malloc", "heap", "gc", "bytes")
RUNTIME_CALL_KEYS = ("runtime_call", "native_call", "call_count", "calls", "dispatch")


@dataclass
class ArtifactStatus:
    path: str | None
    status: str
    note: str = ""


@dataclass
class Bottleneck:
    category: str
    priority: str
    confidence: str
    evidence: list[str] = field(default_factory=list)
    recommendation: str = ""


def run(cmd: list[str], cwd: Path, timeout: int | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        cmd,
        cwd=cwd,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=timeout,
        check=False,
    )


def bench_id_to_suite_name(bench: str) -> str | None:
    if "/" not in bench:
        return bench
    group, name = bench.split("/", 1)
    if group != "suite":
        return None
    return name


def timing_rows(timing_json: Path) -> list[dict]:
    data = json.loads(timing_json.read_text())
    rows: list[dict] = []
    for result in data.get("results", []):
        bench = f"{result.get('group')}/{result.get('benchmark')}"
        scale = result.get("scale") or {}
        for mode, subjects in (result.get("modes") or {}).items():
            current = subjects.get("current") or {}
            head = subjects.get("head") or {}
            luajit = subjects.get("luajit") or {}
            cur = ((current.get("stats") or {}).get("median"))
            hd = ((head.get("stats") or {}).get("median"))
            lj = ((luajit.get("stats") or {}).get("median"))
            rows.append(
                {
                    "benchmark": bench,
                    "scale": scale,
                    "mode": mode,
                    "current": cur,
                    "head": hd,
                    "luajit": lj,
                    "cur_head": cur / hd if cur and hd else None,
                    "cur_luajit": cur / lj if cur and lj else None,
                    "source": current.get("source") or "-",
                    "repeat": current.get("repeat") or 0,
                    "exits": current.get("exit_total") or 0,
                    "ci95": (current.get("stats") or {}).get("ci95_half_width_pct"),
                    "note": current.get("note") or "",
                }
            )
    return rows


def read_json(path: Path | None) -> dict | list | None:
    if not path or not path.exists():
        return None
    try:
        return json.loads(path.read_text())
    except json.JSONDecodeError:
        return None


def artifact_status(path: Path | None, enabled: bool = True, note: str = "") -> ArtifactStatus:
    if not enabled:
        return ArtifactStatus(None, "not-requested", note)
    if path is None:
        return ArtifactStatus(None, "missing", note)
    if path.exists():
        return ArtifactStatus(str(path), "ok", note)
    return ArtifactStatus(str(path), "missing", note)


def parse_time_value(value: str, unit: str | None) -> float:
    number = float(value)
    if unit == "ns":
        return number / 1e9
    if unit == "us":
        return number / 1e6
    if unit == "ms":
        return number / 1e3
    return number


def parse_pprof_top(path: Path | None) -> list[dict]:
    if not path or not path.exists():
        return []
    rows: list[dict] = []
    for line in path.read_text(errors="replace").splitlines():
        match = PPROF_ROW_RE.match(line)
        if not match:
            continue
        data = match.groupdict()
        rows.append(
            {
                "name": data["name"],
                "flat_seconds": parse_time_value(data["flat"], data["flat_unit"]),
                "flat_pct": float(data["flat_pct"]),
                "cum_seconds": parse_time_value(data["cum"], data["cum_unit"]),
                "cum_pct": float(data["cum_pct"]),
            }
        )
    return rows


def pprof_category(name: str) -> str:
    lowered = name.lower()
    if "internal/methodjit" in lowered or ".methodjit." in lowered:
        if any(word in lowered for word in ("emit", "compile", "assembler", "regalloc", "lower", "builder")):
            return "jit_codegen"
        return "jit_control"
    if "runtime.mallocgc" in lowered or "runtime.newobject" in lowered or "runtime.gc" in lowered:
        return "runtime_alloc"
    if "internal/runtime" in lowered or ".runtime." in lowered:
        return "runtime_call"
    if lowered.startswith("runtime.") or lowered.startswith("syscall.") or lowered == "[externalcode]":
        return "external"
    return "user_or_jit"


def summarize_pprof(rows: list[dict]) -> dict:
    by_category: Counter[str] = Counter()
    top_by_category: dict[str, list[dict]] = {}
    for row in rows:
        category = pprof_category(str(row.get("name", "")))
        by_category[category] += float(row.get("flat_pct") or 0.0)
        top_by_category.setdefault(category, []).append(row)
    for bucket in top_by_category.values():
        bucket.sort(key=lambda r: r.get("flat_pct") or 0.0, reverse=True)
    return {
        "flat_pct_by_category": dict(by_category),
        "top_by_category": {key: value[:5] for key, value in top_by_category.items()},
    }


def load_exit_summary(exit_json: Path | None) -> dict:
    data = read_json(exit_json)
    if not isinstance(data, dict):
        return {"total": 0, "by_code": {}, "by_reason": {}, "top_sites": [], "statuses": {}}
    by_code: Counter[str] = Counter()
    by_reason: Counter[str] = Counter()
    top_sites: list[dict] = []
    statuses: Counter[str] = Counter()
    for row in data.get("results", []):
        statuses[str(row.get("status", "unknown"))] += 1
        if row.get("status") != "ok":
            continue
        stats = row.get("stats") or {}
        for name, count in (stats.get("by_exit_code") or {}).items():
            by_code[str(name)] += int(count)
        for site in stats.get("sites", []):
            count = int(site.get("count", 0))
            reason = str(site.get("reason", ""))
            by_reason[reason] += count
            top_sites.append(
                {
                    "count": count,
                    "benchmark": row.get("benchmark"),
                    "exit_name": site.get("exit_name"),
                    "reason": reason,
                    "proto": site.get("proto"),
                    "pc": site.get("pc"),
                    "op_id": site.get("op_id"),
                }
            )
    top_sites.sort(key=lambda row: row["count"], reverse=True)
    return {
        "total": sum(by_code.values()) or sum(by_reason.values()),
        "by_code": dict(by_code),
        "by_reason": dict(by_reason),
        "top_sites": top_sites[:10],
        "statuses": dict(statuses),
    }


def flatten_numbers(data: object, prefix: str = "") -> dict[str, float]:
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


def parse_runtime_stats(path: Path | None) -> dict:
    if not path or not path.exists():
        return {"numbers": {}, "source": "missing"}
    data = read_json(path)
    if data is not None:
        return {"numbers": flatten_numbers(data), "source": "json"}
    numbers: dict[str, float] = {}
    for line in path.read_text(errors="replace").splitlines():
        match = re.match(r"\s*([A-Za-z0-9_.\-/]+)\s*[:=]\s*([0-9]+(?:\.[0-9]+)?)", line)
        if match:
            numbers[match.group(1)] = float(match.group(2))
    return {"numbers": numbers, "source": "text"}


def parse_memprofile(path: Path | None, binary: Path | None, root: Path, timeout: int) -> tuple[Path | None, list[dict]]:
    if not path or not path.exists() or not binary:
        return None, []
    txt = path.with_suffix(path.suffix + ".txt")
    proc = run(["go", "tool", "pprof", "-top", "-nodecount=30", str(binary), str(path)], root, timeout)
    txt.write_text(proc.stdout)
    return txt, parse_pprof_top(txt)


def load_pcmap_summary(pcmap_json: Path | None) -> dict:
    data = read_json(pcmap_json)
    if not isinstance(data, list):
        return {"rows": 0, "cpu_nanos": 0, "top_ops": []}
    by_op: Counter[str] = Counter()
    total = 0
    for row in data:
        nanos = int(row.get("cpu_nanos") or 0)
        total += nanos
        op = str(row.get("ir_op") or row.get("bytecode_op") or "unknown")
        by_op[op] += nanos
    return {
        "rows": len(data),
        "cpu_nanos": total,
        "top_ops": [{"op": op, "cpu_seconds": nanos / 1e9} for op, nanos in by_op.most_common(10)],
    }


def classify(
    rows: list[dict],
    exit_summary: dict,
    pprof_summary: dict,
    pcmap_summary: dict,
    mem_summary: dict,
    runtime_stats: dict,
    artifacts: dict[str, ArtifactStatus],
) -> list[Bottleneck]:
    bottlenecks: list[Bottleneck] = []
    max_lj_gap = max((row.get("cur_luajit") or 0 for row in rows), default=0)
    wall_rows = [row for row in rows if str(row.get("source") or "").startswith("wall")]
    noisy_rows = [row for row in rows if row.get("ci95") is not None and row["ci95"] > 15]
    if wall_rows or noisy_rows:
        evidence = []
        if wall_rows:
            names = ", ".join(f"{row['benchmark']}[{row['mode']}]" for row in wall_rows[:5])
            evidence.append(f"{len(wall_rows)} timing cells used wall timing/startup-inclusive samples: {names}")
        if noisy_rows:
            evidence.append(f"{len(noisy_rows)} timing cells have CI95 half-width > 15%")
        bottlenecks.append(
            Bottleneck(
                "startup-noise",
                "P0",
                "high" if wall_rows else "medium",
                evidence,
                "Scale the workload or rerun with --time-source=script before optimizing code paths.",
            )
        )

    timing_exits = sum(int(row.get("exits") or 0) for row in rows)
    exit_total = int(exit_summary.get("total") or 0)
    if timing_exits > 100 or exit_total > 100:
        top = exit_summary.get("top_sites") or []
        evidence = [f"timing exit_total sum={timing_exits}", f"profile exit_total={exit_total}"]
        if top:
            evidence.append(f"top exit site: {top[0].get('count')}x {top[0].get('exit_name')} {top[0].get('reason')}")
        bottlenecks.append(
            Bottleneck(
                "exit-heavy",
                "P1" if max_lj_gap >= 2 else "P2",
                "high" if exit_total else "medium",
                evidence,
                "Inspect profile_exits top sites, then reduce the dominant fallback/exit mechanism before tuning generated code.",
            )
        )

    by_reason = exit_summary.get("by_reason") or {}
    deopt_count = sum(count for reason, count in by_reason.items() if any(word in reason.lower() for word in EXIT_REASON_WORDS))
    if deopt_count > 0 and (exit_total == 0 or deopt_count / max(1, exit_total) >= 0.35):
        bottlenecks.append(
            Bottleneck(
                "deopt-heavy",
                "P1",
                "high" if artifacts["exits_json"].status == "ok" else "medium",
                [f"{deopt_count} guard/deopt-like exits out of {max(exit_total, deopt_count)} profiled exits"],
                "Prioritize guard specialization, shape/type stability, or exit-resume fixes for the dominant reason.",
            )
        )

    pprof_cats = pprof_summary.get("flat_pct_by_category") or {}
    codegen_pct = float(pprof_cats.get("jit_codegen") or 0.0)
    external_pct = float(pprof_cats.get("external") or 0.0)
    if codegen_pct >= 10.0 or external_pct >= 20.0:
        label = "JIT codegen" if codegen_pct >= external_pct else "external/runtime frames"
        bottlenecks.append(
            Bottleneck(
                "external-code/JIT-codegen-heavy",
                "P1" if max(codegen_pct, external_pct) >= 25.0 else "P2",
                "high",
                [f"CPU pprof flat share: jit_codegen={codegen_pct:.1f}%, external={external_pct:.1f}%", f"dominant bucket: {label}"],
                "If codegen-heavy, cache/reduce compilation work; if external-heavy, collect warm pcmap/perf symbols to separate native JIT execution from Go/runtime frames.",
            )
        )
    elif artifacts["pprof_txt"].status != "ok" and max_lj_gap >= 2:
        bottlenecks.append(
            Bottleneck(
                "external-code/JIT-codegen-heavy",
                "P3",
                "low",
                ["CPU pprof was not available for a benchmark with a significant LuaJIT gap"],
                "Rerun with --pprof --warm-dump to distinguish Go/runtime/codegen overhead from native JIT execution.",
            )
        )

    alloc_pprof_pct = float(pprof_cats.get("runtime_alloc") or 0.0)
    mem_cats = (mem_summary.get("flat_pct_by_category") or {}) if mem_summary else {}
    mem_alloc_pct = float(mem_cats.get("runtime_alloc") or 0.0)
    numbers = runtime_stats.get("numbers") or {}
    alloc_keys = [key for key in numbers if any(word in key.lower() for word in RUNTIME_ALLOC_KEYS) and numbers[key] > 0]
    if alloc_pprof_pct >= 8.0 or mem_alloc_pct >= 15.0 or alloc_keys:
        evidence = [f"CPU alloc/runtime flat share={alloc_pprof_pct:.1f}%", f"memprofile alloc/runtime flat share={mem_alloc_pct:.1f}%"]
        if alloc_keys:
            evidence.append("runtime stat allocation keys: " + ", ".join(alloc_keys[:5]))
        bottlenecks.append(
            Bottleneck(
                "runtime-allocation-heavy",
                "P1" if max(alloc_pprof_pct, mem_alloc_pct) >= 20.0 else "P2",
                "high" if artifacts["memprofile_txt"].status == "ok" or artifacts["runtime_stats"].status == "ok" else "medium",
                evidence,
                "Target object/table/string allocation paths, reuse transient runtime values, or add allocation counters by hot runtime path.",
            )
        )

    runtime_call_pct = float(pprof_cats.get("runtime_call") or 0.0)
    call_keys = [key for key in numbers if any(word in key.lower() for word in RUNTIME_CALL_KEYS) and numbers[key] > 0]
    if runtime_call_pct >= 12.0 or call_keys:
        evidence = [f"CPU pprof internal/runtime flat share={runtime_call_pct:.1f}%"]
        if call_keys:
            evidence.append("runtime stat call keys: " + ", ".join(call_keys[:5]))
        bottlenecks.append(
            Bottleneck(
                "runtime-call-heavy",
                "P1" if runtime_call_pct >= 25.0 else "P2",
                "high" if artifacts["pprof_txt"].status == "ok" or artifacts["runtime_stats"].status == "ok" else "medium",
                evidence,
                "Specialize or inline the hottest runtime call path before low-level instruction scheduling work.",
            )
        )

    suspicious_wins = [row for row in rows if row.get("cur_luajit") is not None and row["cur_luajit"] < 0.70 and row["benchmark"].startswith("suite/")]
    no_filter_rows = [row for row in rows if row.get("mode") == "no_filter" and (row.get("exits") or 0) > 0]
    if suspicious_wins or no_filter_rows or deopt_count > 0:
        evidence = []
        if suspicious_wins:
            evidence.append("suite cells much faster than LuaJIT: " + ", ".join(row["benchmark"] for row in suspicious_wins[:5]))
        if no_filter_rows:
            evidence.append(f"{len(no_filter_rows)} no_filter cells still exit, so guards remain performance/correctness-sensitive")
        if deopt_count > 0:
            evidence.append("guard/deopt exits are present in the profile")
        bottlenecks.append(
            Bottleneck(
                "correctness-risk/needs-guard-stress",
                "P2",
                "medium",
                evidence,
                "Run strict_guard with suite plus variants before trusting wins from guard/speculation changes.",
            )
        )

    if pcmap_summary.get("rows", 0):
        top_ops = pcmap_summary.get("top_ops") or []
        if top_ops:
            bottlenecks.append(
                Bottleneck(
                    "warm-pcmap-hotspot",
                    "P2",
                    "high",
                    [f"warm pcmap mapped {pcmap_summary.get('cpu_nanos', 0) / 1e9:.6f}s CPU", "top op: " + str(top_ops[0])],
                    "Use the mapped IR/opcode rows to tune the specific generated instruction sequence.",
                )
            )

    if not bottlenecks:
        bottlenecks.append(
            Bottleneck(
                "insufficient-signal",
                "P3",
                "medium" if rows else "low",
                ["No rule crossed a bottleneck threshold with the available artifacts."],
                "For a real gap, rerun with scaled script timing plus --pprof --warm-dump and optional --memprofile/--runtime-stats.",
            )
        )

    order = {"P0": 0, "P1": 1, "P2": 2, "P3": 3}
    return sorted(bottlenecks, key=lambda b: (order.get(b.priority, 9), b.category))


def fmt_s(value: float | None) -> str:
    return "-" if value is None else f"{value:.6f}s"


def fmt_x(value: float | None) -> str:
    return "-" if value is None else f"{value:.2f}x"


def fmt_pct(value: float | None) -> str:
    return "-" if value is None else f"{value:.2f}%"


def fmt_scale(scale: dict) -> str:
    if not scale:
        return "-"
    return ", ".join(f"{name}:{value}" for name, value in sorted(scale.items()))


def verdict(row: dict) -> str:
    gap = row.get("cur_luajit")
    source = row.get("source") or ""
    ci95 = row.get("ci95")
    exits = row.get("exits") or 0
    if source == "wall_repeat":
        return "startup-noise-risk: scale workload or use --time-source=script"
    if ci95 is not None and ci95 > 15:
        return "noisy: increase --runs or workload size"
    if gap is not None and gap >= 2:
        return "major gap: prioritize"
    if exits > 100:
        return "exit-heavy: inspect profile_exits"
    if gap is not None and gap < 1:
        return "faster than LuaJIT on this measurement"
    return "moderate/codegen-runtime investigation"


def bottleneck_to_dict(item: Bottleneck) -> dict:
    return {
        "category": item.category,
        "priority": item.priority,
        "confidence": item.confidence,
        "evidence": item.evidence,
        "recommendation": item.recommendation,
    }


def write_report(
    out: Path,
    rows: list[dict],
    timing_md: Path,
    exit_md: Path | None,
    diag_log: Path | None,
    pprof_txt: Path | None,
    memprofile_txt: Path | None,
    warm_dir: Path | None,
    pcmap_md: Path | None,
    summary_json: Path,
    bottlenecks: list[Bottleneck],
    artifacts: dict[str, ArtifactStatus],
) -> None:
    lines = [
        "# Performance Triage",
        "",
        "## Optimization Priorities",
        "",
        "| Priority | Category | Confidence | Evidence | Next step |",
        "|---|---|---|---|---|",
    ]
    for item in bottlenecks:
        lines.append(
            "| "
            + " | ".join(
                [
                    item.priority,
                    item.category,
                    item.confidence,
                    "<br>".join(item.evidence) or "-",
                    item.recommendation,
                ]
            )
            + " |"
        )

    lines += [
        "",
        "## Timing",
        "",
        "| Benchmark | Scale | Mode | Current | HEAD | LuaJIT | Current/HEAD | Current/LuaJIT | Source | Repeat | Exits | CI95 | Verdict |",
        "|---|---|---|---:|---:|---:|---:|---:|---|---:|---:|---:|---|",
    ]
    for row in sorted(rows, key=lambda r: r.get("cur_luajit") or -1, reverse=True):
        lines.append(
            "| "
            + " | ".join(
                [
                    row["benchmark"],
                    fmt_scale(row["scale"]),
                    row["mode"],
                    fmt_s(row["current"]),
                    fmt_s(row["head"]),
                    fmt_s(row["luajit"]),
                    fmt_x(row["cur_head"]),
                    fmt_x(row["cur_luajit"]),
                    row["source"],
                    str(row["repeat"]),
                    str(row["exits"]),
                    fmt_pct(row["ci95"]),
                    verdict(row),
                ]
            )
            + " |"
        )

    lines += ["", "## Artifacts", "", f"- summary JSON: `{summary_json}`", f"- timing report: `{timing_md}`"]
    if exit_md:
        lines.append(f"- exit profile: `{exit_md}`")
    if diag_log:
        lines.append(f"- diag log: `{diag_log}`")
    if pprof_txt:
        lines.append(f"- pprof top: `{pprof_txt}`")
    if memprofile_txt:
        lines.append(f"- memprofile top: `{memprofile_txt}`")
    if warm_dir:
        lines.append(f"- warm JIT dump: `{warm_dir}`")
    if pcmap_md:
        lines.append(f"- JIT PC map summary: `{pcmap_md}`")
    lines += ["", "## Artifact Status", "", "| Artifact | Status | Path | Note |", "|---|---|---|---|"]
    for name, status in sorted(artifacts.items()):
        lines.append(f"| {name} | {status.status} | `{status.path or '-'}` | {status.note or '-'} |")
    out.write_text("\n".join(lines) + "\n")


def write_summary(
    out: Path,
    rows: list[dict],
    bottlenecks: list[Bottleneck],
    artifacts: dict[str, ArtifactStatus],
    exit_summary: dict,
    pprof_summary: dict,
    pcmap_summary: dict,
    mem_summary: dict,
    runtime_stats: dict,
) -> None:
    payload = {
        "timing": rows,
        "bottlenecks": [bottleneck_to_dict(item) for item in bottlenecks],
        "recommendations": [item.recommendation for item in bottlenecks],
        "artifacts": {name: status.__dict__ for name, status in sorted(artifacts.items())},
        "exit_summary": exit_summary,
        "pprof_summary": pprof_summary,
        "pcmap_summary": pcmap_summary,
        "memprofile_summary": mem_summary,
        "runtime_stats": runtime_stats,
    }
    out.write_text(json.dumps(payload, indent=2) + "\n")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bench", action="append", required=True, help="benchmark name or group/name; repeatable")
    parser.add_argument("--mode", action="append", default=None)
    parser.add_argument("--runs", default="5")
    parser.add_argument("--warmup", default="1")
    parser.add_argument("--timeout", default="120")
    parser.add_argument("--min-sample-seconds", default="0.100")
    parser.add_argument("--max-repeat", default="128")
    parser.add_argument("--time-source", choices=("auto", "script", "wall"), default="auto")
    parser.add_argument("--scale", action="append", default=[])
    parser.add_argument("--param", action="append", default=[])
    parser.add_argument("--scale-profile", choices=("none", "hot"), default="none")
    parser.add_argument("--diag", action="store_true", help="also run scripts/diag.sh for suite benchmarks")
    parser.add_argument("--pprof", action="store_true", help="also collect a CPU profile for the first benchmark")
    parser.add_argument(
        "--memprofile",
        nargs="?",
        const="collect",
        default=None,
        help="collect heap profile for the first suite benchmark, or read an existing profile path",
    )
    parser.add_argument(
        "--runtime-stats",
        type=Path,
        help="optional runtime stats JSON/text file to fold into bottleneck classification",
    )
    parser.add_argument("--warm-dump", action="store_true", help="also collect production-warm JIT PC maps for the first suite benchmark")
    parser.add_argument("--out-dir", type=Path, default=Path("/tmp/gscript-triage"))
    args = parser.parse_args()

    root = Path(__file__).resolve().parents[1]
    out_dir = args.out_dir
    out_dir.mkdir(parents=True, exist_ok=True)
    timing_json = out_dir / "timing.json"
    timing_md = out_dir / "timing.md"
    report_md = out_dir / "triage.md"
    summary_json = out_dir / "triage.json"

    cmd = [
        sys.executable,
        "benchmarks/timing_compare.py",
        "--runs",
        args.runs,
        "--warmup",
        args.warmup,
        "--timeout",
        args.timeout,
        "--min-sample-seconds",
        args.min_sample_seconds,
        "--max-repeat",
        args.max_repeat,
        "--time-source",
        args.time_source,
        "--sort",
        "luajit-gap",
        "--json",
        str(timing_json),
        "--markdown",
        str(timing_md),
    ]
    for mode in args.mode or ["default"]:
        cmd += ["--mode", mode]
    for bench in args.bench:
        cmd += ["--bench", bench]
    for scale in args.scale:
        cmd += ["--scale", scale]
    for param in args.param:
        cmd += ["--param", param]
    if args.scale_profile != "none":
        cmd += ["--scale-profile", args.scale_profile]

    timing = run(cmd, root, int(args.timeout) * max(2, len(args.bench) * 4))
    if timing.returncode != 0:
        print(timing.stdout, file=sys.stderr)
        return timing.returncode

    suite_benches = [name for bench in args.bench if (name := bench_id_to_suite_name(bench))]
    exit_md: Path | None = None
    exit_json: Path | None = None
    if suite_benches:
        exit_md = out_dir / "exits.md"
        exit_json = out_dir / "exits.json"
        exit_cmd = [sys.executable, "benchmarks/profile_exits.py", "--timeout", args.timeout, "--json", str(exit_json), "--markdown", str(exit_md)]
        for bench in suite_benches:
            exit_cmd += ["--bench", bench]
        run(exit_cmd, root, int(args.timeout) * max(1, len(suite_benches)))

    diag_log: Path | None = None
    if args.diag and suite_benches:
        diag_log = out_dir / "diag.log"
        diag_cmd = ["bash", "scripts/diag.sh", *[f"suite/{bench}" for bench in suite_benches]]
        diag = run(diag_cmd, root, int(args.timeout) * max(1, len(suite_benches)))
        diag_log.write_text(diag.stdout)

    pprof_txt: Path | None = None
    pprof_json: Path | None = None
    warm_dir: Path | None = None
    pcmap_md: Path | None = None
    pcmap_json: Path | None = None
    memprofile_path: Path | None = None
    memprofile_txt: Path | None = None
    pprof_binary: Path | None = None
    if args.pprof:
        first = args.bench[0]
        suite_name = bench_id_to_suite_name(first)
        if suite_name:
            tempdir = Path(tempfile.mkdtemp(prefix="gscript_triage_pprof_"))
            try:
                binary = tempdir / "gscript"
                build = run(["go", "build", "-o", str(binary), "./cmd/gscript/"], root, int(args.timeout))
                if build.returncode == 0:
                    pprof_binary = out_dir / "gscript.pprof.bin"
                    shutil.copy2(binary, pprof_binary)
                    cpu = out_dir / f"{suite_name}.pprof"
                    run([str(binary), "-jit", "-cpuprofile", str(cpu), f"benchmarks/suite/{suite_name}.gs"], root, int(args.timeout))
                    pprof = run(["go", "tool", "pprof", "-top", "-nodecount=30", str(binary), str(cpu)], root, int(args.timeout))
                    pprof_txt = out_dir / f"{suite_name}.pprof.txt"
                    pprof_txt.write_text(pprof.stdout)
                    pprof_json = out_dir / f"{suite_name}.pprof.json"
                    pprof_json.write_text(json.dumps(parse_pprof_top(pprof_txt), indent=2) + "\n")
            finally:
                shutil.rmtree(tempdir, ignore_errors=True)

    if args.memprofile:
        first = args.bench[0]
        suite_name = bench_id_to_suite_name(first)
        if args.memprofile != "collect":
            memprofile_path = Path(args.memprofile)
        elif suite_name:
            tempdir = Path(tempfile.mkdtemp(prefix="gscript_triage_mem_"))
            try:
                binary = tempdir / "gscript"
                build = run(["go", "build", "-o", str(binary), "./cmd/gscript/"], root, int(args.timeout))
                if build.returncode == 0:
                    pprof_binary = out_dir / "gscript.pprof.bin"
                    shutil.copy2(binary, pprof_binary)
                    memprofile_path = out_dir / f"{suite_name}.memprofile"
                    run(
                        [str(binary), "-jit", "-memprofile", str(memprofile_path), f"benchmarks/suite/{suite_name}.gs"],
                        root,
                        int(args.timeout),
                    )
            finally:
                shutil.rmtree(tempdir, ignore_errors=True)
        if memprofile_path and pprof_binary:
            memprofile_txt, _ = parse_memprofile(memprofile_path, pprof_binary, root, int(args.timeout))

    if args.warm_dump:
        first = args.bench[0]
        suite_name = bench_id_to_suite_name(first)
        if suite_name:
            tempdir = Path(tempfile.mkdtemp(prefix="gscript_triage_warm_"))
            try:
                binary = tempdir / "gscript"
                build = run(["go", "build", "-o", str(binary), "./cmd/gscript/"], root, int(args.timeout))
                if build.returncode == 0:
                    pprof_binary = out_dir / "gscript.pprof.bin"
                    shutil.copy2(binary, pprof_binary)
                    warm_dir = out_dir / f"{suite_name}.warm"
                    if warm_dir.exists():
                        shutil.rmtree(warm_dir)
                    cpu = out_dir / f"{suite_name}.warm.pprof"
                    run(
                        [
                            str(binary),
                            "-jit",
                            "-cpuprofile",
                            str(cpu),
                            "-jit-dump-warm",
                            str(warm_dir),
                            f"benchmarks/suite/{suite_name}.gs",
                        ],
                        root,
                        int(args.timeout),
                    )
                    pcmap_md = out_dir / f"{suite_name}.jit-pcmap.md"
                    pcmap_json = out_dir / f"{suite_name}.jit-pcmap.json"
                    mapped = run(
                        [
                            sys.executable,
                            "benchmarks/jit_addr_map.py",
                            "--warm-dir",
                            str(warm_dir),
                            "--binary",
                            str(binary),
                            "--profile",
                            str(cpu),
                            "--json",
                            str(pcmap_json),
                        ],
                        root,
                        int(args.timeout),
                    )
                    pcmap_md.write_text(mapped.stdout)
            finally:
                shutil.rmtree(tempdir, ignore_errors=True)

    rows = timing_rows(timing_json)
    exit_summary = load_exit_summary(exit_json)
    pprof_rows = parse_pprof_top(pprof_txt)
    pprof_summary = summarize_pprof(pprof_rows)
    mem_rows = parse_pprof_top(memprofile_txt)
    mem_summary = summarize_pprof(mem_rows)
    runtime_stats = parse_runtime_stats(args.runtime_stats)
    pcmap_summary = load_pcmap_summary(pcmap_json)
    artifacts = {
        "timing_json": artifact_status(timing_json, True),
        "timing_md": artifact_status(timing_md, True),
        "exits_json": artifact_status(exit_json, bool(suite_benches), "only suite benchmarks support exit profiling"),
        "exits_md": artifact_status(exit_md, bool(suite_benches), "only suite benchmarks support exit profiling"),
        "diag_log": artifact_status(diag_log, args.diag, "requested with --diag"),
        "pprof_txt": artifact_status(pprof_txt, args.pprof, "requested with --pprof"),
        "pprof_json": artifact_status(pprof_json, args.pprof, "parsed pprof top rows"),
        "memprofile": artifact_status(memprofile_path, bool(args.memprofile), "requested/read with --memprofile"),
        "memprofile_txt": artifact_status(memprofile_txt, bool(args.memprofile), "go tool pprof -top for memprofile"),
        "runtime_stats": artifact_status(args.runtime_stats, args.runtime_stats is not None, "optional JSON/text input"),
        "warm_dir": artifact_status(warm_dir, args.warm_dump, "requested with --warm-dump"),
        "pcmap_md": artifact_status(pcmap_md, args.warm_dump, "warm pcmap Markdown"),
        "pcmap_json": artifact_status(pcmap_json, args.warm_dump, "warm pcmap JSON"),
    }
    bottlenecks = classify(rows, exit_summary, pprof_summary, pcmap_summary, mem_summary, runtime_stats, artifacts)
    write_summary(
        summary_json,
        rows,
        bottlenecks,
        artifacts,
        exit_summary,
        pprof_summary,
        pcmap_summary,
        mem_summary,
        runtime_stats,
    )
    write_report(
        report_md,
        rows,
        timing_md,
        exit_md,
        diag_log,
        pprof_txt,
        memprofile_txt,
        warm_dir,
        pcmap_md,
        summary_json,
        bottlenecks,
        artifacts,
    )
    print(report_md.read_text())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
