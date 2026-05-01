#!/usr/bin/env python3
"""Validate LuaJIT reference coverage for regression_guard defaults."""

from __future__ import annotations

import argparse
import shutil
import subprocess
import sys
from pathlib import Path

import regression_guard as guard


def validate_ref(root: Path, lua_bin: str, name: str, timeout: int) -> tuple[str, str]:
    lua_file = root / "benchmarks" / "lua" / f"{name}.lua"
    if not lua_file.exists():
        return "missing", f"{name}: missing {lua_file.relative_to(root)}"

    proc = subprocess.run(
        [lua_bin, str(lua_file)],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        timeout=timeout,
        check=False,
    )
    tail = "\n".join(line for line in proc.stdout.strip().splitlines()[-6:] if line)
    if proc.returncode != 0:
        return "error", f"{name}: exit {proc.returncode}\n{tail}"
    if guard.parse_time(proc.stdout) is None:
        return "no_time", f"{name}: no parseable Time: ...s line\n{tail}"
    return "ok", f"{name}: ok"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--lua-bin", default=shutil.which("luajit") or "luajit")
    parser.add_argument("--timeout", type=int, default=60)
    args = parser.parse_args(argv)

    root = Path(__file__).resolve().parents[1]
    failures: list[tuple[str, str]] = []

    for name in guard.DEFAULT_BENCHMARKS:
        try:
            status, message = validate_ref(root, args.lua_bin, name, args.timeout)
        except subprocess.TimeoutExpired:
            status = "timeout"
            message = f"{name}: timeout after {args.timeout}s"
        except FileNotFoundError:
            print(f"Lua binary not found: {args.lua_bin}", file=sys.stderr)
            return 2

        print(message)
        if status != "ok":
            failures.append((status, name))

    if failures:
        summary = ", ".join(f"{name}={status}" for status, name in failures)
        print(f"Lua reference validation failed: {summary}", file=sys.stderr)
        return 1

    print(f"Validated {len(guard.DEFAULT_BENCHMARKS)} Lua references.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
