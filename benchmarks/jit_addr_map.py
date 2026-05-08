#!/usr/bin/env python3
"""Map JIT code addresses back to warm-dump IR/opcode metadata."""

from __future__ import annotations

import argparse
from collections import Counter
import json
import re
import subprocess
from pathlib import Path


def parse_int(value: str | int | None) -> int | None:
    if value is None:
        return None
    if isinstance(value, int):
        return value
    text = str(value).strip()
    if not text:
        return None
    return int(text, 0)


def load_ranges(warm_dir: Path) -> list[dict]:
    pcmap = warm_dir / "pcmap.json"
    if pcmap.exists():
        return load_pcmap_ranges(pcmap)
    symbols = warm_dir / "jit-symbols.txt"
    if symbols.exists():
        return load_symbol_ranges(symbols)
    manifest = json.loads((warm_dir / "manifest.json").read_text())
    ranges: list[dict] = []
    for proto in manifest.get("protos", []):
        files = proto.get("files") or {}
        source_map_name = files.get("sourcemap")
        code_start = parse_int(proto.get("code_start"))
        if not source_map_name or code_start is None:
            continue
        source_map = json.loads((warm_dir / source_map_name).read_text())
        for row in source_map:
            rel_start = row.get("code_start")
            rel_end = row.get("code_end")
            if rel_start is None or rel_end is None or rel_start < 0 or rel_end <= rel_start:
                continue
            ranges.append(
                {
                    "abs_start": code_start + rel_start,
                    "abs_end": code_start + rel_end,
                    "proto": proto.get("name") or row.get("proto") or "",
                    "ir_instr": row.get("ir_instr"),
                    "ir_op": row.get("ir_op") or "",
                    "ir_type": row.get("ir_type") or "",
                    "block": row.get("block"),
                    "bytecode_pc": row.get("bytecode_pc"),
                    "bytecode_op": row.get("bytecode_op") or "",
                    "source_line": row.get("source_line"),
                    "pass": row.get("pass") or "",
                    "symbol": "",
                }
            )
    ranges.sort(key=lambda r: (r["abs_start"], r["abs_end"]))
    return ranges


def load_pcmap_ranges(pcmap: Path) -> list[dict]:
    data = json.loads(pcmap.read_text())
    ranges: list[dict] = []
    for fn in data.get("functions", []):
        for row in fn.get("ranges", []):
            pc_start = parse_int(row.get("pc_start"))
            pc_end = parse_int(row.get("pc_end"))
            if pc_start is None or pc_end is None or pc_end <= pc_start:
                continue
            ranges.append(
                {
                    "abs_start": pc_start,
                    "abs_end": pc_end,
                    "proto": row.get("proto") or fn.get("name") or "",
                    "ir_instr": row.get("ir_instr"),
                    "ir_op": row.get("ir_op") or "",
                    "ir_type": row.get("ir_type") or "",
                    "block": row.get("block"),
                    "bytecode_pc": row.get("bytecode_pc"),
                    "bytecode_op": row.get("bytecode_op") or "",
                    "source_line": row.get("source_line"),
                    "pass": row.get("pass") or "",
                    "symbol": "",
                }
            )
    ranges.sort(key=lambda r: (r["abs_start"], r["abs_end"]))
    return ranges


def load_symbol_ranges(symbols: Path) -> list[dict]:
    ranges: list[dict] = []
    for line in symbols.read_text().splitlines():
        parts = line.split(maxsplit=2)
        if len(parts) != 3:
            continue
        start = int(parts[0], 16)
        size = int(parts[1], 16)
        symbol = parts[2]
        if size <= 0:
            continue
        meta = parse_symbol_meta(symbol)
        ranges.append(
            {
                "abs_start": start,
                "abs_end": start + size,
                "proto": meta.get("proto") or symbol.split(";", 1)[0],
                "ir_instr": parse_int(meta.get("ir")),
                "ir_op": meta.get("op", ""),
                "ir_type": meta.get("type", ""),
                "block": parse_int(meta.get("block")),
                "bytecode_pc": parse_int(meta.get("bc")),
                "bytecode_op": meta.get("bcop", ""),
                "source_line": parse_int(meta.get("line")),
                "pass": meta.get("pass", ""),
                "symbol": symbol,
            }
        )
    ranges.sort(key=lambda r: (r["abs_start"], r["abs_end"]))
    return ranges


def parse_symbol_meta(symbol: str) -> dict[str, str]:
    meta: dict[str, str] = {}
    for part in symbol.split(";")[1:]:
        key, sep, value = part.partition("=")
        if sep:
            meta[key] = value
    return meta


def run_pprof_raw(binary: Path, profile: Path) -> str:
    proc = subprocess.run(
        ["go", "tool", "pprof", "-raw", str(binary), str(profile)],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        check=False,
    )
    if proc.returncode != 0:
        raise SystemExit(proc.stdout)
    return proc.stdout


def parse_pprof_raw(raw: str) -> tuple[dict[int, int], dict[int, list[str]], list[tuple[int, int, list[int]]]]:
    locations: dict[int, int] = {}
    location_names: dict[int, list[str]] = {}
    samples: list[tuple[int, int, list[int]]] = []
    section = ""
    current_loc: int | None = None
    for line in raw.splitlines():
        if line == "Samples:":
            section = "samples"
            current_loc = None
            continue
        if line == "Locations":
            section = "locations"
            current_loc = None
            continue
        if line == "Mappings":
            section = "mappings"
            current_loc = None
            continue
        if section == "locations":
            m = re.match(r"\s*(\d+):\s+(0x[0-9a-fA-F]+)", line)
            if m:
                current_loc = int(m.group(1))
                locations[current_loc] = int(m.group(2), 16)
                names = parse_location_function_names(line)
                if names:
                    location_names[current_loc] = names
                continue
            if current_loc is not None:
                names = parse_location_function_names(line)
                if names:
                    location_names.setdefault(current_loc, []).extend(names)
        elif section == "samples":
            m = re.match(r"\s*(\d+)\s+(\d+):\s+(.+)$", line)
            if m:
                loc_ids = [int(x) for x in re.findall(r"\b\d+\b", m.group(3))]
                samples.append((int(m.group(1)), int(m.group(2)), loc_ids))
    return locations, location_names, samples


def parse_location_function_names(line: str) -> list[str]:
    names: list[str] = []
    # go tool pprof -raw location lines look like either:
    #   1: 0x... M=1 runtime._ExternalCode /path/file.go:5581:0 s=5581
    #              runtime._System /path/file.go:5580:0 s=5580
    text = line.strip()
    if not text:
        return names
    if ":" in text and text.split(":", 1)[0].strip().isdigit():
        parts = text.split()
        for i, part in enumerate(parts):
            if part.startswith("0x") or part.startswith("M="):
                continue
            if i + 1 < len(parts) and looks_like_source_location(parts[i + 1]):
                names.append(part)
                break
        return names
    parts = text.split()
    if len(parts) >= 2 and looks_like_source_location(parts[1]):
        names.append(parts[0])
    return names


def looks_like_source_location(value: str) -> bool:
    return bool(re.search(r":\d+:\d+(?:$|\s)", value) or re.search(r":\d+:\d+$", value))


def find_range(ranges: list[dict], pc: int) -> dict | None:
    for row in ranges:
        if row["abs_start"] <= pc < row["abs_end"]:
            return row
    return None


def summarize(ranges: list[dict], locations: dict[int, int], samples: list[tuple[int, int, list[int]]]) -> list[dict]:
    buckets: dict[tuple, dict] = {}
    for count, nanos, loc_ids in samples:
        seen: set[tuple] = set()
        for loc_id in loc_ids:
            pc = locations.get(loc_id)
            if pc is None:
                continue
            row = find_range(ranges, pc)
            if row is None:
                continue
            key = (row["proto"], row["ir_instr"], row["ir_op"], row["block"], row["bytecode_pc"], row["pass"])
            if key in seen:
                continue
            seen.add(key)
            bucket = buckets.setdefault(
                key,
                {
                    "samples": 0,
                    "cpu_nanos": 0,
                    "proto": row["proto"],
                    "ir_instr": row["ir_instr"],
                    "ir_op": row["ir_op"],
                    "ir_type": row["ir_type"],
                    "block": row["block"],
                    "bytecode_pc": row["bytecode_pc"],
                    "bytecode_op": row["bytecode_op"],
                    "source_line": row["source_line"],
                    "pass": row["pass"],
                    "symbol": row.get("symbol", ""),
                    "first_pc": f"0x{pc:x}",
                },
            )
            bucket["samples"] += count
            bucket["cpu_nanos"] += nanos
    return sorted(buckets.values(), key=lambda row: row["cpu_nanos"], reverse=True)


def profile_stats(
    ranges: list[dict],
    locations: dict[int, int],
    location_names: dict[int, list[str]],
    samples: list[tuple[int, int, list[int]]],
) -> dict:
    matched_loc_ids: set[int] = set()
    external_samples = 0
    external_nanos = 0
    unmatched_samples = 0
    unmatched_nanos = 0
    total_samples = 0
    total_nanos = 0
    sampled_pcs: list[int] = []
    function_counts: Counter[str] = Counter()

    for count, nanos, loc_ids in samples:
        total_samples += count
        total_nanos += nanos
        sample_matched = False
        sample_external = False
        for loc_id in loc_ids:
            pc = locations.get(loc_id)
            if pc is not None:
                sampled_pcs.append(pc)
                if find_range(ranges, pc) is not None:
                    matched_loc_ids.add(loc_id)
                    sample_matched = True
            names = location_names.get(loc_id) or []
            for name in names:
                function_counts[name] += count
                if name == "runtime._ExternalCode":
                    sample_external = True
        if sample_external:
            external_samples += count
            external_nanos += nanos
        if not sample_matched:
            unmatched_samples += count
            unmatched_nanos += nanos

    return {
        "profile_samples": total_samples,
        "profile_cpu_nanos": total_nanos,
        "profile_locations": len(locations),
        "matched_locations": len(matched_loc_ids),
        "unmatched_samples": unmatched_samples,
        "unmatched_cpu_nanos": unmatched_nanos,
        "external_code_samples": external_samples,
        "external_code_cpu_nanos": external_nanos,
        "sampled_pc_min": f"0x{min(sampled_pcs):x}" if sampled_pcs else "",
        "sampled_pc_max": f"0x{max(sampled_pcs):x}" if sampled_pcs else "",
        "top_profile_functions": [
            {"name": name, "samples": samples}
            for name, samples in function_counts.most_common(10)
        ],
    }


def range_stats(ranges: list[dict]) -> dict:
    if not ranges:
        return {
            "jit_ranges": 0,
            "jit_functions": [],
            "jit_pc_min": "",
            "jit_pc_max": "",
        }
    by_proto: Counter[str] = Counter()
    for row in ranges:
        by_proto[str(row.get("proto") or "")] += 1
    return {
        "jit_ranges": len(ranges),
        "jit_functions": [
            {"name": name, "ranges": count}
            for name, count in by_proto.most_common()
        ],
        "jit_pc_min": f"0x{min(int(row['abs_start']) for row in ranges):x}",
        "jit_pc_max": f"0x{max(int(row['abs_end']) for row in ranges):x}",
    }


def failure_reason(rows: list[dict], ranges: list[dict], stats: dict) -> dict | None:
    if rows:
        return None
    if not ranges:
        return {
            "code": "no_warm_jit_ranges",
            "message": "warm dump did not contain any JIT code ranges; check warm/manifest.json for Tier2 compile status",
        }
    if stats.get("profile_samples", 0) == 0:
        return {
            "code": "no_profile_samples",
            "message": "CPU profile contained no samples to map",
        }
    if stats.get("external_code_samples", 0) and stats.get("matched_locations", 0) == 0:
        return {
            "code": "profile_external_code_without_native_pc",
            "message": (
                "Go CPU profile sampled runtime._ExternalCode, but the raw profile "
                "does not preserve the actual native JIT PC; warm JIT ranges are present "
                "but cannot be joined to IR/opcode rows from this profile"
            ),
        }
    return {
        "code": "profile_pcs_outside_warm_jit_ranges",
        "message": "CPU profile PCs did not fall inside any production-warm JIT code range",
    }


def pprof_function_summary(rows: list[dict]) -> list[dict]:
    out = []
    for row in rows:
        name = row.get("symbol")
        if not name:
            name = (
                f"gscript_jit::{row.get('proto', '')};ir={row.get('ir_instr', '')};"
                f"op={row.get('ir_op', '')};bc={row.get('bytecode_pc', '')};"
                f"bcop={row.get('bytecode_op', '')};pass={row.get('pass', '')}"
            )
        out.append(
            {
                "name": name,
                "system_name": name,
                "filename": row.get("proto", ""),
                "start_line": row.get("source_line") or 0,
                "samples": row.get("samples", 0),
                "cpu_nanos": row.get("cpu_nanos", 0),
                "first_pc": row.get("first_pc") or row.get("pc") or "",
            }
        )
    return out


def output_document(rows: list[dict], ranges: list[dict], stats: dict) -> dict:
    status = "ok" if rows else "unmatched"
    failure = failure_reason(rows, ranges, stats)
    return {
        "version": 1,
        "status": status,
        "failure": failure,
        "summary": {**range_stats(ranges), **stats, "mapped_rows": len(rows)},
        "rows": rows,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--warm-dir", type=Path, required=True, help="directory produced by -jit-dump-warm")
    parser.add_argument("--binary", type=Path, help="binary used to produce --profile")
    parser.add_argument("--profile", type=Path, help="CPU profile to decode with go tool pprof -raw")
    parser.add_argument("--pprof-raw", type=Path, help="precomputed go tool pprof -raw output")
    parser.add_argument("--pc", action="append", default=[], help="explicit native PC to resolve, e.g. 0x1234")
    parser.add_argument("--json", type=Path, help="write JSON summary")
    parser.add_argument("--pprof-functions-json", type=Path, help="write pprof-function-like JSON summary")
    parser.add_argument("--top", type=int, default=30)
    args = parser.parse_args()

    ranges = load_ranges(args.warm_dir)
    explicit_pcs = [int(pc, 0) for pc in args.pc]
    rows: list[dict] = []

    for pc in explicit_pcs:
        row = find_range(ranges, pc)
        if row:
            rows.append({"pc": f"0x{pc:x}", **row})

    if args.profile or args.pprof_raw:
        if args.pprof_raw:
            raw = args.pprof_raw.read_text()
        else:
            if not args.binary or not args.profile:
                parser.error("--profile requires --binary")
            raw = run_pprof_raw(args.binary, args.profile)
        locations, location_names, samples = parse_pprof_raw(raw)
        rows.extend(summarize(ranges, locations, samples))
        stats = profile_stats(ranges, locations, location_names, samples)
    else:
        stats = {
            "profile_samples": 0,
            "profile_cpu_nanos": 0,
            "profile_locations": 0,
            "matched_locations": 0,
            "unmatched_samples": 0,
            "unmatched_cpu_nanos": 0,
            "external_code_samples": 0,
            "external_code_cpu_nanos": 0,
            "sampled_pc_min": "",
            "sampled_pc_max": "",
            "top_profile_functions": [],
        }

    if args.json:
        args.json.write_text(json.dumps(output_document(rows, ranges, stats), indent=2) + "\n")
    if args.pprof_functions_json:
        args.pprof_functions_json.write_text(json.dumps(pprof_function_summary(rows), indent=2) + "\n")

    if not rows:
        doc = output_document(rows, ranges, stats)
        failure = doc.get("failure") or {}
        print(f"No JIT PCs matched warm-dump code ranges: {failure.get('code', 'unknown')}")
        print(json.dumps(doc["summary"], sort_keys=True))
        return 0

    print("| Samples | CPU | Proto | IR | Op | Block | BC | Pass | PC |")
    print("|---:|---:|---|---:|---|---:|---:|---|---|")
    for row in rows[: args.top]:
        cpu = row.get("cpu_nanos")
        cpu_s = "-" if cpu is None else f"{cpu / 1e9:.6f}s"
        print(
            "| "
            + " | ".join(
                [
                    str(row.get("samples", "-")),
                    cpu_s,
                    str(row.get("proto", "")),
                    str(row.get("ir_instr", "")),
                    str(row.get("ir_op", "")),
                    str(row.get("block", "")),
                    str(row.get("bytecode_pc", "")),
                    str(row.get("pass", "")),
                    str(row.get("first_pc") or row.get("pc") or ""),
                ]
            )
            + " |"
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
