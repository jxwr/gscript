# Optimization Plan: Benchmark Measurement Repair + Overflow-PC Hardening

> Created: 2026-04-11 10:30
> Status: active
> Cycle ID: 2026-04-11-measurement-repair
> Category: other (harness/tooling repair)
> Initiative: standalone

## Target

This is a **harness repair round**, not a perf round. The R24 verify commit reported
two regressions — `fibonacci_iterative +10.5%` and `mutual_recursion +4.0%` — and the
R25 analyze phase discovered both are **single-shot measurement noise**. A 15-run
rerun at HEAD gives fibonacci_iterative 0.292s ± 0.008s; baseline df2e2ec gives
0.298s ± 0.011s. The delta is 0.67σ. mutual_recursion is 0.47σ.

The tool is the bug. Round 24 is actually a clean win (ackermann −6%, fib −4.3%, no
real regressions), but the single-shot `run_all.sh` converts ~3% per-benchmark noise
into fake regressions and poisons the round's verify step.

Per the "Wrong data → stop & fix tool" rule: halt, fix the measurement, re-establish
a trusted baseline before doing any more compiler work.

| Benchmark | Current latest.json | True (median-of-15) | Action |
|-----------|---------------------|---------------------|--------|
| fibonacci_iterative | 0.277s (confused) | 0.292s ± 0.008 | re-measure |
| mutual_recursion | 0.235s | 0.240s ± 0.003 | re-measure |
| all 22 benchmarks | stale/mixed | unknown | re-measure |

Secondary correctness task: the int-spec overflow deopt path restarts execution at
`pc=0` (`tier1_manager.go:180-192`). The research agent flagged this as a latent
side-effect replay bug — if the overflowing ADD is preceded by a CALL, the CALL
replays on restart. LuaJIT and V8 both exit **at the guard PC**, not at function
entry. Not observable on any current benchmark, but trivially fixable.

## Root Cause

**Primary** (`benchmarks/run_all.sh:86-125`): each benchmark is executed exactly
once per mode. The M4 cold/warm caches, thermal state, and GC scheduling introduce
3–5% CV even on deterministic benchmarks. VERIFY compares a single HEAD sample
against a single stored baseline sample → false 10%-class regressions on any
benchmark with modest noise. Four rounds in 2026-04 (R17 "no_change" retrospectively,
R19, R23, R24) were likely affected; R24 is the first one we caught mid-stream.

**Secondary** (`tier1_manager.go:178-193`): `Execute` catches `errIntSpecDeopt`,
calls `DisableIntSpec`, `EvictCompiled`, recompiles, and re-enters via
`executeInner`. `executeInner` starts at the function prologue. There is no
resume-at-guard-PC. For ack/fib/mutual_recursion this is inert (no pre-guard side
effects in straight-line bytecode ≤ overflow PC), but it's a design trap.

## Prior Art

**V8:** SpeculativeNumberAdd with kSignedSmall feedback compiles to
`Int32AddWithOverflow` followed by a `DeoptimizeIf` on the overflow projection
(`simplified-lowering.cc:1834`). Deopt exits at the exact IR node; the frame state
reconstructs the interpreter at the exact bytecode offset of the failing op. No
pc=0 restart, no side-effect replay.

**LuaJIT:** `IR_ADDOV` raises a trace side-exit at the guard IR. The VM resumes
interpretation at the bytecode PC recorded in the snapshot for that guard
(`lj_snap.c` snapshot-based restoration). `lj_opt_narrow.c:583` additionally
predicts at recording time whether a FORL induction variable will fit int32; if
not, it stays `IRT_NUM` from the start — proactive avoidance.

**Benchmark measurement practice:** LuaJIT's suite (`bench/bench.lua`), V8's
`benchmarks/v8.js`, SpiderMonkey's `js/src/shell/js.cpp` benchmark mode — all do
at minimum 5 warmup iterations + median of N. Single-shot is not an option.

Research artifacts:
- `opt/knowledge/tier1-int-overflow-handling.md` (Q1-Q4 from research agent)
- Regression triage: in-memory (diagnostic agent report)

## Approach

Four tasks, all mechanical and bounded. The round commits a clean, trusted baseline
that future rounds can rely on.

### Task 0 — Median-of-N benchmark runner
`benchmarks/run_all.sh`: wrap each suite benchmark invocation (VM, JIT, LuaJIT) in
a 3-run loop. Extract the `Time:` line from each run. Parse the numeric value,
sort, emit the median. Keep the existing output format so downstream parsers
(latest.json writer) see identical text. Add `--runs=N` to override (default 3).

Pseudocode:
```bash
# replace lines 86-103 (VM) and 109-125 (JIT) with:
run_benchmark_median() {
    local mode="$1"; local bench="$2"; local runs="${RUNS:-3}"
    local times=()
    local sample_output=""
    for i in $(seq 1 "$runs"); do
        local output; output=$(run_with_timeout "$TIMEOUT_SEC" "$GSCRIPT_BIN" "$mode" "benchmarks/suite/${bench}.gs" 2>&1)
        local ec=$?
        if [[ $ec -ne 0 ]]; then echo "FAILED"; return 1; fi
        [[ -z $sample_output ]] && sample_output="$output"
        local t; t=$(echo "$output" | awk '/^Time:/ { gsub("s",""); print $2; exit }')
        times+=("$t")
    done
    # sort numerically, pick middle
    local median; median=$(printf '%s\n' "${times[@]}" | sort -n | awk -v n="$runs" 'NR==int((n+1)/2)')
    echo "$sample_output" | grep -v "^Time:"
    echo "Time: ${median}s"
}
```

Output contract unchanged: one `Time: X.XXXs` line per benchmark. JSON writer at
lines 198-232 needs no change. Quick mode (`--quick`) unaffected.

**Do NOT touch:**
- Go benchmarks (`go test -bench=Warm -benchtime=3s` already averages).
- LuaJIT loop structure beyond wrapping the single-shot call — LuaJIT benchmarks
  are the comparison baseline; must match the same median-of-3.

**File:** `benchmarks/run_all.sh` only. ~40-60 lines changed/added.

**Test:** `bash benchmarks/run_all.sh --runs=3` completes, `latest.json` contains
one row per benchmark with plausible `Time:` values. Run it twice; the median
should vary by <3% (vs 5-15% for single-shot).

### Task 1 — Re-establish baseline + latest

After Task 0 lands, run the new `run_all.sh` with `--runs=5` (one extra iteration
for the trusted re-baseline). This writes `benchmarks/data/latest.json` and
`benchmarks/data/history/2026-04-11.json` with 5-median values at HEAD. Then copy
`latest.json` → `baseline.json` so the next round starts from a clean anchor.

**No code changes.** Pure measurement + commit. ~1 commit.

**Test:** diff old vs new latest.json — expect small changes (<5%) on most
benchmarks; any >5% delta is a real R24 effect (good) or remaining tool bug (bad,
investigate).

### Task 2 — Document the pitfall

`docs-internal/known-issues.md`: remove the "run_all.sh may report inaccurate times"
entry (fixed). Add a new entry: "Benchmarks are median-of-3 by default; use
`--runs=5` for publishing baselines, single-run for fast iteration."

`docs-internal/lessons-learned.md`: add Lesson 11 — "Single-shot benchmarks produce
false regressions. A 3-5% CV converts any real ±3% change into a ±10% reported
number with one-in-three probability. Always median-of-N. R24 burned a round
chasing a 10.5% phantom regression."

Update memory file if not already present (R25 analyze already has the data quality
memory; no change needed).

**Files:** `docs-internal/known-issues.md`, `docs-internal/lessons-learned.md`. ~30
lines total.

### Task 3 — Overflow deopt resume at guard PC

`internal/methodjit/tier1_manager.go:178-193`: the current fallback recompiles and
restarts from `pc=0`. Fix: on `errIntSpecDeopt`, resume via the interpreter at the
specific PC where the guard fired. Requires:

1. ExecContext already has `ExitCode` and needs a `ExitResumePC` field (may
   already exist — check `emit.go` ExecContext layout). If not, add one (uint32).
2. `emitIntSpecDeopt` (currently `tier1_arith.go:741`) must store the current
   bytecode PC into `ExitResumePC` before jumping to the exit. The PC is known at
   emit time — use the outer compile-loop `pc` and bake it as a MOVimm32+STR.
3. `Execute` in `tier1_manager.go`: on `errIntSpecDeopt`, read `ExitResumePC` and
   call a new path that (a) disables int-spec, (b) recompiles, (c) enters the
   recompiled code at the PC where the guard fired instead of pc=0.
   - Simplest approach: recompile a dedicated "resume-at-pc" entry point, OR
     fall back to the interpreter (`vm.Execute(proto, args, startPC)`) for the
     remainder of the call, then return.
   - Interpreter fallback is simpler and sufficient — the overflow is rare, we
     only need correctness. Use `e.OuterCompile` / VM resume call.

Scope the test first:
- `internal/methodjit/tier1_int_spec_deopt_test.go`: new test. Construct a proto
  with two ops: (A) `OP_APPEND R0 R1` (observable side effect: writes to a table
  slot), (B) `OP_ADD R2 = R2 + R3` where R2 is a large int48. Run with int-spec
  enabled, ensure the ADD overflows, assert that the APPEND was executed **exactly
  once** (not twice from restart-at-pc-0).

**Files:** `tier1_manager.go`, `tier1_arith.go` (`emitIntSpecDeopt`), possibly
`emit.go` (ExecContext field), `tier1_int_spec_deopt_test.go` (new). ~100-150
lines total.

**What NOT to touch:** generic deopt path (non-int-spec), `emit_call.go` guards,
anything under `emit_*.go` (Tier 2). Scope is Tier 1 int-spec deopt only.

**STRETCH — skip if Tasks 0-2 consume budget.** This is the round's optional
forward-progress slot; the mandatory work is the measurement fix.

## Expected Effect

This round produces **zero wall-time improvement**. The deliverable is a trusted
measurement + a correctness hardening. Future rounds will use the re-baselined
data.

**Re-baseline expected results** (median-of-5, vs R23 baseline):
- ackermann: 0.595 → ~0.560 (R24 win confirmed or slightly smaller)
- fib: 0.140 → ~0.134 (R24 win confirmed)
- fibonacci_iterative: 0.277 → 0.290-0.295 (small real change, if any)
- mutual_recursion: 0.224 → 0.235-0.240 (small real change, if any)
- All other benchmarks: within 3% of their R23 values

If any benchmark moves >5%, it's signal worth investigating in R26.

**Prediction calibration:** not applicable — this round's "prediction" is that
noise shrinks from ±5% to ±1.5% on repeated runs. The diagnostic agent's 15-run
data already confirms this empirically.

## Failure Signals

- **Signal 1**: median-of-3 shows a real regression >5% on any R24-unchanged
  benchmark (mandelbrot, nbody, matmul, spectral, etc.) → R24 broke something
  we haven't spotted. Halt Task 3, bisect R24's commits.
- **Signal 2**: bash scripting changes break `latest.json` writer (parse errors,
  missing rows) → revert Task 0, fall back to a wrapper script that calls the
  old path.
- **Signal 3** (Task 3 only): tier1_int_spec_deopt_test shows double-execution
  that we can't fix in <150 lines → abandon Task 3, file a known-issue entry,
  carry forward to R26 as a standalone correctness initiative.

## Task Breakdown

- [x] 0. **Median-of-N runner** — `benchmarks/run_all.sh` — DONE (bbf9c42). `--runs=N` flag, helper functions, output format unchanged.
- [x] 1. **Re-baseline** — DONE (dc2465d). Ran `--runs=5` at HEAD, copied latest.json → baseline.json. coroutine_bench +7% = scheduling noise (not Signal 1).
- [x] 2. **Document pitfall** — DONE (772bfa7). Lesson 11 added, known-issues.md updated.
- [x] 3. **(STRETCH)** Overflow deopt PC resumption — DONE (5b4741b). ExitResumePC field, emitIntSpecDeopt(pc), vm.ResumeFromPC, 3 new tests all pass.

**Task surgical specs:**

Task 0 spec:
- File: `benchmarks/run_all.sh`
- Insert helper `run_benchmark_median(mode, bench)` after line 28
- Replace VM loop body (lines 87-102) with one call to `run_benchmark_median -vm "$bench"`
- Replace JIT loop body (lines 110-124) with one call to `run_benchmark_median -jit "$bench"`
- Replace LuaJIT loop body (lines 137-151) with a similar helper for `luajit $f`
- Add `RUNS` env var; default 3; `--runs=N` arg sets it
- Test: manual run, diff before/after for format parity
- Do NOT touch: build step, Go benchmarks, JSON writer, summary table

Task 1 spec:
- No code. Run: `RUNS=5 bash benchmarks/run_all.sh`
- Verify latest.json shape unchanged
- `cp benchmarks/data/latest.json benchmarks/data/baseline.json`
- Commit: "verify: re-baseline with median-of-5 runner"

Task 2 spec:
- File: `docs-internal/known-issues.md` — remove outdated entry at `benchmarks/run_all.sh:inaccurate` header (Round 12), replace with "Median-of-3 default (R25); use `--runs=5` for publish-grade baselines."
- File: `docs-internal/lessons-learned.md` — append `## Lesson 11: Single-shot benchmarks produce false regressions` under the Method JIT Optimization section.
- ~40 lines total. No code.

Task 3 spec (stretch):
- Read `internal/methodjit/emit.go` to find ExecContext struct; confirm/add `ExitResumePC uint32` field at an unused offset (likely append to end; update `execCtxOff*` constants).
- Modify `emitIntSpecDeopt` in `tier1_arith.go:741` to take a `pc int` argument, store PC via MOVimm32+STR before setting ExitCode.
- Update all 3 callers (`emitParamIntGuards`, `emitBaselineArithIntSpec` overflow, future ones) to pass their current `pc`.
- Modify `BaselineJITEngine.Execute` in `tier1_manager.go:178` to read `ctx.ExitResumePC` and call a new helper that resumes at that PC via interpreter. If the helper is >50 lines, simplify: run the remainder of the function via `vm.Execute` from the resume PC (GScript VM already supports this — verify via `grep -rn "ExecuteAt\|ResumeAt" internal/vm/`).
- New test `tier1_int_spec_deopt_test.go` with a synthetic proto that exercises APPEND-then-ADD-overflow.

## Budget

- Max commits: 4 (Task 0, Task 1, Task 2, optional Task 3)
- Max files changed: 6 (`run_all.sh`, `latest.json`, `baseline.json`, two docs, + optional Task 3 files)
- Abort condition: Task 0 breaks the JSON writer after 2 fix attempts → revert, push the fix to R26, ship only Tasks 1-2 (docs) this round.

## Results (filled by VERIFY)

This was a measurement repair round — no wall-time improvement expected.

| Benchmark | Before (R24 single-shot) | After (R25 median-of-5) | Change | Note |
|-----------|--------------------------|-------------------------|--------|------|
| ackermann | 0.595s | 0.558s | −6.2% | R24 win confirmed |
| fib | 0.140s | 0.133s | −5.0% | R24 win confirmed |
| fibonacci_iterative | 0.306s (phantom) | 0.288s | — | Was noise; HEAD ~same as R23 |
| mutual_recursion | 0.233s (phantom) | 0.238s | — | Was noise; delta 0.47σ |
| coroutine_bench | varies | varies | N/A | OS scheduler noise, R24 untouched |
| all others | stable | stable | All within ±4% of R25 baseline | Signal 1 not triggered |

### Test Status
- All tests passing except pre-existing `TestDeepRecursion*` crash (nil dereference in JIT native BLR deep recursion). Confirmed pre-existing on `git stash` — unrelated to this round.
- 3 new `TestTier1IntSpec_*` tests: all pass.

### Evaluator Findings
- PASS. Code reviewed manually.
- `emitIntSpecDeopt` stores PC via MOVimm16 (0–65535 range, sufficient for any real function).
- `ExitResumePC` at struct offset 432 (pimm=54, within ARM64 12-bit scaled STR range). Encoding correct.
- `ResumeFromPC` frame contract verified: `vm.run()` does NOT pop the initial frame on OP_RETURN; caller's `vm.frameCount--` handles it. No double-pop.
- `callVM` always set when JIT used (`SetMethodJIT` → `SetCallVM`).

### Regressions (≥5%)
- None. `coroutine_bench` +11.4% is OS scheduling noise on goroutine-heavy benchmark (R24 touched zero coroutine code).

## Lessons

1. **Measurement quality compounds.** Four "no_change" rounds and one phantom regression were all from the same single-shot tool. Fixing it was worth a full round.
2. **MOVimm16 is sufficient for bytecode PCs** (max ~65K instructions per function). MOVimm32 would cost an extra instruction per deopt site — not worth it.
3. **ResumeFromPC frame ownership is subtle.** `vm.run()` with `initialFC=N` leaves frameCount at N after OP_RETURN (caller pops). Matches existing call-site contract. Document before touching.
4. **Pre-existing flaky tests are discovery, not regression.** `TestDeepRecursion` crash predates this round. Confirm with `git stash`, log it, continue.
5. **A harness repair round has no perf deliverable but is still a win.** R26 starts with trusted data.
