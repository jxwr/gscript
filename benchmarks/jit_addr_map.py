#!/usr/bin/env python3
"""Map JIT code addresses back to warm-dump IR/opcode metadata."""

from __future__ import annotations

import argparse
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


def parse_pprof_raw(raw: str) -> tuple[dict[int, int], list[tuple[int, int, list[int]]]]:
    locations: dict[int, int] = {}
    samples: list[tuple[int, int, list[int]]] = []
    section = ""
    for line in raw.splitlines():
        if line == "Samples:":
            section = "samples"
            continue
        if line == "Locations":
            section = "locations"
            continue
        if line == "Mappings":
            section = "mappings"
            continue
        if section == "locations":
            m = re.match(r"\s*(\d+):\s+(0x[0-9a-fA-F]+)", line)
            if m:
                locations[int(m.group(1))] = int(m.group(2), 16)
        elif section == "samples":
            m = re.match(r"\s*(\d+)\s+(\d+):\s+(.+)$", line)
            if m:
                loc_ids = [int(x) for x in re.findall(r"\b\d+\b", m.group(3))]
                samples.append((int(m.group(1)), int(m.group(2)), loc_ids))
    return locations, samples


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
        locations, samples = parse_pprof_raw(raw)
        rows.extend(summarize(ranges, locations, samples))

    if args.json:
        args.json.write_text(json.dumps(rows, indent=2) + "\n")
    if args.pprof_functions_json:
        args.pprof_functions_json.write_text(json.dumps(pprof_function_summary(rows), indent=2) + "\n")

    if not rows:
        print("No JIT PCs matched warm-dump code ranges.")
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
