# GScript Benchmark Suite

15 benchmarks covering compute, recursion, table/array, function calls, strings, and closures.

## Recommended Entry Points

```bash
# Optimization timing: current worktree vs clean HEAD vs LuaJIT.
# This is the default entry point for deciding whether a local optimization
# is real. It supports suite/extended/variants, calibrated repeats, confidence
# intervals, parameter scaling, and explicit timing sources.
python3 benchmarks/timing_compare.py --all-groups --runs=5 --warmup=1 \
  --time-source=auto --min-sample-seconds=0.100 --max-repeat=128 \
  --sort=luajit-gap \
  --json /tmp/gscript_timing_compare.json \
  --markdown /tmp/gscript_timing_compare.md

# Hot-loop profile: temporary workload scaling for benchmarks whose default
# problem size is too small and would otherwise be dominated by process startup.
python3 benchmarks/timing_compare.py --runs=5 --warmup=1 \
  --scale-profile=hot --sort=luajit-gap \
  --json /tmp/gscript_hot_timing.json \
  --markdown /tmp/gscript_hot_timing.md

# Strict truth pass: suite + extended + variants, VM/JIT/no-filter/LuaJIT,
# median timing, output checksums, JIT stats, and overfit-win notes.
python3 benchmarks/strict_guard.py --runs=3 --warmup=1 --timeout=90 \
  --json benchmarks/data/strict_guard_latest.json \
  --markdown benchmarks/data/strict_guard_latest.md

# Representative strict subset while iterating
python3 benchmarks/strict_guard.py --runs=3 --warmup=0 --max-repeat=8 \
  --bench=suite/matmul --bench=variants/matmul_row_variant \
  --bench=extended/json_table_walk

# Focused hot-loop timing with explicit temporary parameter overrides.
python3 benchmarks/timing_compare.py --runs=5 --warmup=1 \
  --bench=suite/coroutine_bench \
  --param=suite/coroutine_bench:N1=1000000 \
  --param=suite/coroutine_bench:N2=500000 \
  --param=suite/coroutine_bench:N3=1000000

# Exit/deopt profile for Tier 2 diagnostics. Do not use exits as the success
# metric; use this to explain timing results from timing_compare.py.
python3 benchmarks/profile_exits.py --bench=suite/spectral_norm --top=30

# Focused triage bundle: timing + exit profile, with optional diag/pprof.
python3 benchmarks/triage.py --bench=suite/spectral_norm \
  --scale=suite/spectral_norm:N=2000 --time-source=script \
  --diag --pprof --memprofile --warm-dump --out-dir=/tmp/gscript-triage-spectral

# Runtime path counters for exits=0 but still-slow cases.
go run ./cmd/gscript -jit -runtime-path-stats-json benchmarks/suite/string_bench.gs

# Production-warm Tier 2 dump from a real run. The manifest includes JIT code
# address ranges, and each compiled proto writes sourcemap/pcmap JSON from
# native code PCs back to IR/opcode/source metadata.
go run ./cmd/gscript -jit -jit-dump-warm=/tmp/gscript-warm benchmarks/suite/spectral_norm.gs

# Offline join from a CPU profile to JIT IR/opcode PCs from the same run.
python3 benchmarks/jit_addr_map.py --warm-dir=/tmp/gscript-warm \
  --binary=/tmp/gscript --profile=/tmp/gscript.pprof

# Regression guard: VM + default JIT + no-filter JIT + LuaJIT, median-of-3
# Compatibility entry point; strict_guard.py is the broader truth pass.
bash benchmarks/regression_guard.sh --runs=3 \
  --json benchmarks/data/regression_guard_latest.json \
  --csv benchmarks/data/regression_guard_latest.csv \
  --markdown benchmarks/data/regression_guard_latest.md

# Publish-grade guard
bash benchmarks/regression_guard.sh --runs=5 --timeout=90

# Promote a reviewed guard JSON to the checked-in baseline
bash benchmarks/set_baseline.sh benchmarks/data/regression_guard_latest.json

# Suite benchmarks (CLI, one-shot timing; legacy)
cd benchmarks/suite && bash run_all.sh ../../cmd/gscript/main.go -trace

# Go micro-benchmarks (warm, no compilation overhead)
go test ./benchmarks/ -bench=Warm -benchtime=3s

# Compare with LuaJIT
luajit benchmarks/lua/run_all.lua
```

Performance results are in the [top-level README](../README.md).

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1

## Harness Roles

| Tool | Role | Use for optimization decisions? |
|------|------|----------------------------------|
| `timing_compare.py` | Current vs clean `HEAD` vs LuaJIT, calibrated repeats, scaling, CI, gap ranking | Yes, primary local timing harness |
| `strict_guard.py` | Broad truth pass: suite + extended + variants, checksums, suspicious-win review | Yes, release/regression gate |
| `triage.py` | Focused timing + exits + optional IR/ASM and pprof artifact bundle | Yes, for planning the next optimization |
| `jit_addr_map.py` | Offline join from pprof/raw PCs to production-warm JIT IR/opcode PC maps | Diagnostic only |
| `profile_exits.py` | Tier 2 exit/deopt attribution | Diagnostic only |
| `rank_luajit_gaps.py` | Report/history ranking helper | Diagnostic/reporting |
| `regression_guard.py` / `.sh` | Older baseline regression workflow | Compatibility |
| `diagnose_tier2.sh`, `scripts/diag.sh` | IR/ASM dumps | Diagnostic only |

Keep suite, extended, and variants as separate benchmark groups in reports.
They can be run together with `--all-groups`, but do not collapse them into one
score: suite is the stable LuaJIT comparison set, extended is workload coverage,
and variants are overfit/correctness pressure tests.

## Debugging Coverage

The toolchain is sufficient for the next optimization iteration when used as a
workflow:

- `timing_compare.py` decides whether a gap is real and records timing source,
  repeat count, CI, parameter scaling, and current-vs-HEAD-vs-LuaJIT deltas.
- `profile_exits.py` explains guard/deopt/exit pressure when performance is
  limited by fallback behavior.
- `triage.py` bundles timing, exits, optional IR/ASM, pprof, warm JIT dumps, and
  JIT PC maps for a focused target. It writes `triage.json` with bottleneck
  categories and accepts `--runtime-stats` for external path counter JSON/text.
- `-runtime-path-stats` / `-runtime-path-stats-json` reports native-call,
  coroutine, table-array, and string-format fast/fallback counters for cases
  where exits alone do not explain runtime cost.
- `-jit-dump-warm` captures production Tier 2 artifacts from the actual run,
  including code addresses and PC maps; this avoids drift from offline-only
  diagnostic compiles.
- `jit_addr_map.py` maps raw sampled PCs, or explicit PCs, back to JIT
  IR/opcode/source metadata from the same run.

It is not a complete compiler observability stack yet. Remaining high-value
gaps are JIT symbol/perf-map integration so external profilers can name native
JIT frames directly, guard/deopt timelines correlated with tiering events,
allocation/call counters by runtime path, pass-by-pass optimization summaries
with code-size deltas, and automated differential/fuzz stress for optimized
fallback paths.

## Reading the output

`strict_guard.py` is the preferred truth pass before claiming benchmark wins.
It builds `cmd/gscript` once, discovers every `benchmarks/suite/*.gs`, every
extended benchmark in `benchmarks/extended/manifest.json`, and every
`benchmarks/variants/*.gs`, then runs each available cell in:

| Mode | Meaning |
|------|---------|
| `vm` | bytecode VM, no JIT |
| `default` | normal method JIT with `-jit-stats -exit-stats` |
| `no_filter` | method JIT with `GSCRIPT_TIER2_NO_FILTER=1` |
| `luajit` | matching Lua reference under `benchmarks/lua*`, when `luajit` is in `PATH` |

The strict report includes median timing, sample spread, repeat count, timing
source, output checksum hash, literal `checksum:` value when present, Tier 2
`attempted/entered/failed`, total exits, and a "Suspicious Kernel Wins" section.
That section calls out suite benchmarks that beat LuaJIT by a large margin but
are not confirmed by their structural variants. It is intentionally a review
signal, not a performance patch gate.

Use `--group=suite`, `--group=extended`, or `--group=variants` to narrow the
run; repeat `--bench=group/name` for a representative subset. `--dry-run`
prints the exact discovered matrix without building or running anything.

`timing_compare.py` is the preferred harness for local before/after timing when
the current worktree may differ from clean `HEAD`. It exports a clean `HEAD`
snapshot, builds both binaries, and compares `current`, `HEAD`, and LuaJIT for
selected benchmarks. Each cell automatically increases the repeat count until
the sample exceeds `--min-sample-seconds`; if summed script `Time:` remains
below `--timer-resolution`, the tool reports `wall_repeat` as the timing source
instead of treating `0.000s` as a real runtime. Wall fallback samples use at
least `--min-wall-repeat` invocations and are annotated because they include
process startup overhead. The table includes median, repeat count, CV, 95%
confidence interval half-width, and current Tier 2 exit counts; the Markdown
report also ranks selected benchmarks by current/LuaJIT gap. Use
`--mode=default`, `--mode=vm`, or `--mode=no_filter` to select GScript modes.
For sub-millisecond kernels, use `--scale=BENCH:VAR=VALUE`,
`--param=BENCH:VAR=VALUE`, or `--scale-profile=hot` to run temporary scaled
copies of matching GScript/Lua benchmarks. The report records each changed
parameter as `VAR:old->new`, so original and scaled problem sizes remain
explicit. `--time-source=script` rejects wall fallback and is best for hot-loop
claims; `--time-source=wall` uses high-resolution process wall time and is best
for end-to-end startup/compile measurements; `--time-source=auto` is the normal
default.

`regression_guard.sh` remains the preferred baseline regression tool for
method-JIT performance work. It builds `cmd/gscript` once, runs every core suite
benchmark in:

| Mode | Meaning |
|------|---------|
| `VM` | bytecode VM, no JIT |
| `Default` | normal method JIT with `-jit-stats -exit-stats` |
| `NoFilter` | method JIT with `GSCRIPT_TIER2_NO_FILTER=1` |
| `LuaJIT` | matching `benchmarks/lua/<name>.lua`, when `luajit` is in `PATH` |

The table reports median wall time, `JIT/VM`, `JIT/LJ`, Tier 2
`attempted/entered/failed`, total Tier 2 exits, and a regression marker when
default JIT is more than 10% slower than `benchmarks/data/baseline.json`.
Each benchmark/mode/sample has its own timeout; a timeout or crash records that
cell and the runner continues with the rest of the suite. The process exits
non-zero only after the table is complete and at least one regression is marked.
Use `--runs=N` to choose median sample count for the guard. `--count=N` is an
alias for the same setting; it does not invoke Go benchmark `-count`. For Go
micro-benchmarks, use `go test ./benchmarks/ -bench=Warm -count=N`.

For the push-before-push workflow and report examples, see
[docs/perf/bench-guard.md](../docs/perf/bench-guard.md).

When diagnosing a perf anomaly, **read `-jit-stats` Tier 2 entered/compiled
counts before reasoning about the wall time**. Several past rounds (R132, R133–R139, R145)
burned time analyzing Tier 2 emit for benchmarks that were not even
running Tier 2 native code. See `docs-internal/diagnostics/debug-jit-correctness.md`
§ Step 0.
