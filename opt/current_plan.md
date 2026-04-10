# Optimization Plan: Tier 1 Integer-Specialized Arith/Compare Templates

> Created: 2026-04-10
> Status: active
> Cycle ID: 2026-04-10-tier1-int-spec
> Category: tier1_dispatch (**NEW canonical category** — see INDEX.md update)
> Initiative: standalone (may spawn `tier1-int-specialization` initiative if results are positive)

## Target

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| ackermann | 0.595s | 0.006s | 99× | 0.52–0.56s (−6 to −12%) |
| fib | 0.140s | 0.024s | 5.8× | 0.125–0.135s (−4 to −10%) |
| mutual_recursion | 0.224s | 0.005s | 44× | 0.19–0.21s (−6 to −15%) |
| fibonacci_iterative | 0.277s | n/a | — | secondary, unquantified |
| sum_primes | 0.004s | 0.002s | 2× | secondary, unquantified |

**Primary**: ackermann. **Secondary goals**: zero regressions on float-heavy benchmarks (nbody, mandelbrot, spectral_norm, matmul) and correctness preservation across all 22 benchmarks.

## Root Cause

Tier 1 is a **stateless, template-based** 1:1 bytecode→ARM64 compiler. Every `OP_ADD`/`OP_SUB`/`OP_MUL` emits the full polymorphic template (~22 ARM64 insns): load both operands, LSR-MOV-CMP-BCond twice to check both are int, fall through to the int path (SBFX extract, op, overflow check, box back), or fall through to the float path. Every `OP_EQ`/`OP_LT`/`OP_LE` emits ~35 insns of polymorphic compare.

The diagnostic on ackermann (`/tmp/gscript_ack_tier1.disasm`, 846-insn body) confirms:
- 2× EQ dispatch + 2× SUB dispatch per recursive call ≈ 114 insns per call ≈ 13% of hot-path instructions.
- 340 memory LDR/STR against the NaN-boxed slot file (40% of hot path).
- Only 24 insns (2.8%) are the GetGlobal generation-check path — this is **not** the bottleneck despite the known-issues.md ackermann entry pointing at it.

Tier 1 has no way to know that `m` and `n` in `ack(m, n)` are always ints across 67 million calls, because there is no type-tracking state between op templates. Every op re-runs the dispatch from scratch.

The surrounding architectural wall: Tier 2 is net-negative for recursive functions (Round 11 proved this), and that category is ceiling-blocked (`category_failures[recursive_call]=2`). So Tier 2 specialization is off the table. The fix has to happen **inside Tier 1**.

## Prior Art (MANDATORY)

**V8 (Sparkplug)**: keeps baseline generic and relies on Maglev/TurboFan for feedback-based int specialization. Not applicable to our recursion case because we have no working higher tier for recursion.

**LuaJIT (interpreter)**: the LuaJIT DynASM interpreter has separate hand-coded dispatch paths for integer vs number operands, effectively doing what we want at the interpreter level. Function specialization happens at the trace recorder, which we've deprecated.

**JSC (LLInt / Baseline)**: LLInt has OSR into Baseline which has OSR into DFG which specializes by type feedback. Same story as V8 — relies on tiering.

**Academic**: "Type Feedback for Bytecode Interpreters" (Zhang et al., 2011) and "Efficient Inline Caching without Dynamic Translation" — both argue that modest per-op type state in a baseline can close most of the gap with optimizing JITs for well-typed dynamic code. Our KnownInt tracking is a minimalist version of this.

**Our constraints vs theirs**: GScript's Tier 2 rejects recursive functions as net-negative. That's unusual — V8/JSC get recursive-int optimization from TurboFan/DFG. Since we can't lean on Tier 2 for this benchmark class, we push a sliver of specialization down into Tier 1 itself. The amount of extra state is tiny (one `KnownInt` bitset over register slots per function) and the emit-time decision is local.

## Approach

The plan is split into **one diagnostic/analysis task** followed by **one implementation task** per the R23 review's conceptual-complexity rule (cross-file dataflow → split into findings-first + impl-second).

### Task 1 (analysis): KnownInt forward-tracking analysis

Write a new function `computeKnownIntSlots(proto *vm.FuncProto) knownIntInfo` in a NEW file `internal/methodjit/tier1_int_analysis.go` (≤150 lines).

Algorithm:
1. **Eligibility gate**: scan the proto's bytecode once. Reject (return zero info) if the proto contains any of:
   - `OP_CONCAT`, `OP_LEN` (string ops — type can't be int)
   - `OP_LOADK` where the constant is a float or string (body uses non-int constants for arithmetic — skip specialization for safety)
   - Params used as the `B` (target) of `OP_MOVE` from a float constant (reassigned to non-int)
2. **KnownInt slot set**: start with `{R(0), R(1), ..., R(NumParams-1)}`. After each instruction, update:
   - `OP_LOADINT` writes slot A → add A to set.
   - `OP_LOADK` with int constant → add A. Float/string constant → remove A.
   - `OP_ADD`/`OP_SUB`/`OP_MUL` with both operands in set (or integer RK constants) → add A.
   - `OP_ADD`/`OP_SUB`/`OP_MUL` with a non-KnownInt operand → remove A from set.
   - `OP_CALL` writes to R(A)..R(A+C-2) → remove those slots (call return types unknown).
   - `OP_MOVE` from KnownInt → add A; otherwise remove A.
   - Any instruction that writes A (not listed above) → remove A.
3. **Per-PC view**: because Tier 1 emits op-by-op, the analysis returns a map `PC → KnownIntSlotSet` representing the state **before** that PC. The emitter consults this to decide template choice.
4. **Unanalyzable proto**: if the analysis gives up (string, float constants, untrackable patterns), return `(zero_info, false)` and the emitter uses generic templates for the whole proto.

The algorithm is deliberately linear and conservative. No backward flow, no fixed-point. Branch targets reset KnownInt to the intersection of in-edges (or simpler: reset to empty at any JMP target for correctness — over-conservative but safe).

**Task 1 deliverable**: write findings + pseudocode to `opt/knowledge/tier1-int-spec.md`. Include: the algorithm, the eligibility gate, examples of what ack/fib/mutual_recursion compile to (using `go test -run TestDumpBytecode` or similar), and the expected coverage percentage for each benchmark's hot loop. Task 2 reads this file before writing code.

**Files touched**: `opt/knowledge/tier1-int-spec.md` (new, ~150 lines), `internal/methodjit/tier1_int_analysis.go` (new, scaffolding only — just the type definitions and a stub), `internal/methodjit/tier1_int_analysis_test.go` (new, test scaffolding). No production behavior change in Task 1. Commit: `analyze: tier1 int-spec forward tracking algorithm + skeleton`.

### Task 2 (implementation): int-specialized templates + emitter wiring

Consumes `opt/knowledge/tier1-int-spec.md`.

1. **Implement `computeKnownIntSlots`** in `tier1_int_analysis.go` per the algorithm from Task 1. Unit test: cover ack's bytecode shape (expect params in set, EQ/SUB operands in set), fib, a mixed-type function (expect ineligible), and a float-constant function (expect ineligible). ≤150 lines + ≤120 lines of test.
2. **Add int-spec templates** to `tier1_arith.go` (currently 728 lines; keep under 900 — Round 26 audit will flag 800+):
   - `emitBaselineArithIntSpec(asm, inst, op)` — assumes both operands are KnownInt, no dispatch. Starts with the load + SBFX extract, then op, overflow-check, box. Saves the ~10-insn dispatch. **~25 insns vs ~22 for generic** — wait, that's not smaller. The saving comes from skipping the two `LSR + MOV + CMP + BCond` sequences (10 insns). Net: **~12 insns vs ~22**. Still has to handle int48 overflow.
   - `emitBaselineEQIntSpec(asm, inst, pc, code)` — int-known EQ: SBFX both, CMP, BCond. ~8 insns vs ~35 for the polymorphic version.
   - `emitBaselineLTIntSpec` / `emitBaselineLEIntSpec` — same pattern.
3. **Parameter-entry guard helper**: add `emitParamIntGuards(asm, numParams)` to `tier1_arith.go`. For each param slot, LSR+MOV+CMP+BCond to a deopt-and-resume label that re-executes the function at Tier 0 (interpreter). ~8 insns per param, one-time at function entry.
4. **Wire into `tier1_compile.go`**: after the existing bytecode scan, call `computeKnownIntSlots`. If analysis is non-empty:
   - Emit `emitParamIntGuards` after the prologue (but before first bytecode).
   - During the op dispatch switch, for each ADD/SUB/MUL/EQ/LT/LE, look up the KnownInt state at that PC. If both operands are KnownInt (or int constants), dispatch to the Spec variant. Otherwise fall back to generic.
5. **Test**: add `tier1_int_spec_test.go` (new file) — smoke tests for each specialized template + an end-to-end test that compiles ack's proto through CompileBaseline with the new path and verifies that `emitBaselineArithIntSpec` is called for the expected PCs.

**Files touched**: `tier1_int_analysis.go` (extend, +100 lines), `tier1_int_analysis_test.go` (extend, +100 lines), `tier1_arith.go` (+180 lines of new emit helpers), `tier1_compile.go` (+30 lines of wiring), `tier1_int_spec_test.go` (new, +120 lines). **Total: 5 files, ~530 new lines.** This is over the 200-line-per-task guideline, but the work is additive (new helpers, no rewriting existing templates) and the diagnostic split in Task 1 keeps the conceptual load manageable. Commit: `implement: tier1 int-specialized arith/compare templates with forward KnownInt tracking`.

### Task 3 (integration): benchmark run + sanity checks

Run full bench suite. Verify zero regressions on nbody/mandelbrot/spectral_norm/matmul. Confirm ackermann/fib/mutual_recursion improvement. Update `benchmarks/data/latest.json`. Commit: `verify: tier1 int-spec benchmark results`.

## Expected Effect

**Prediction calibration**: halved for ARM64 superscalar M4 (per the Round 23 lesson that instruction-count reduction ≠ wall-time on branch-heavy code).

- **ackermann**: saves ~114 insns per recursive call × 67M calls = **7.6B fewer instructions total**. At a conservative 3 IPC blended (because removed instructions are dispatch branches which predict well and issue cheaply), that's **~2.5B cycles saved = ~830ms at 3GHz**. Halved: **~415ms saved theoretical, realistic 60–120ms (10–20%)**. Target 0.595s → **0.48–0.54s**. Conservative published target: 0.52–0.56s.
- **fib**: fewer per-call ops than ack (simpler body), smaller absolute saving. Target 0.140s → 0.125–0.135s.
- **mutual_recursion**: similar to fib but with 2 function bodies. Target 0.224s → 0.19–0.21s.
- **fibonacci_iterative**: int loop body — EQ and SUB on loop counter already benefit from some existing specialization, but `a = a + b` style updates may also be int-trackable. Secondary, 3–5% hoped.
- **Float benchmarks**: **zero change** — specialization is opt-in (analysis returns empty set for any proto touching float constants or OP_POW).

The primary risk is that I'm over-estimating again. The R23 lesson was that M4 hides IPC savings. The mitigation here: the EQ dispatch isn't just instructions, it's **dispatch branches** (BCond) that can mispredict. The int-spec version has **fewer branches, not just fewer instructions** — that's the kind of saving M4 does actually register as wall-time.

## Failure Signals

- **Signal 1**: ackermann unchanged or slower after Task 2. → abandon + document "Tier 1 template overhead is memory-bound, not dispatch-bound" + pivot to slot-pinning in R25.
- **Signal 2**: correctness failure on any benchmark. → Task 2 is rejected and Task 1's analysis needs a tighter eligibility gate. Look at the specific bytecode that slipped through.
- **Signal 3**: nbody/mandelbrot/spectral regresses by >2%. → the eligibility gate is wrongly accepting float functions. Add stricter constant-pool type checking.
- **Signal 4**: Task 2 Coder sub-agent exceeds 80 tool calls. → split Task 2 further into 2a (templates only) and 2b (emitter wiring + tests).

**Not a tiering policy change**. No `func_profile.go` edits. No integration-CLI-binary test required.

## Task Breakdown

- [x] **1. Analysis + knowledge doc** — files: `tier1_int_analysis.go` (skeleton), `tier1_int_analysis_test.go` (stub), `opt/knowledge/tier1-int-spec.md` (new) — deliverable: pseudocode + eligibility gate + expected coverage per benchmark. **Does NOT change production behavior.** Test: `TestKnownIntAnalysis_Skeleton`.
- [ ] **2. Implementation + wiring** — files: `tier1_int_analysis.go` (extend), `tier1_int_analysis_test.go` (extend), `tier1_arith.go`, `tier1_compile.go`, `tier1_int_spec_test.go` (new) — deliverable: working int-spec templates + emitter dispatch. Test: `TestTier1IntSpec_Ackermann`, `TestTier1IntSpec_NoRegressionOnFloat`.
- [ ] **3. Benchmark + bench-data refresh** — file: `benchmarks/data/latest.json` — run `scripts/bench_all.sh` or the individual benchmark runs; diff against baseline; populate Results table in this plan.

## Budget

- **Max commits**: 3 functional (+1 revert slot if Task 2 needs rollback after evaluator finds a correctness issue). Actual target: 3.
- **Max files changed**: 5 production + 2 test files + 1 knowledge doc + 1 bench-data refresh = 9 paths.
- **Abort condition**: 2 commits without any forward progress on ackermann, or evaluator returns any `hard_blocker` verdict.

## Results (filled after VERIFY)

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| ackermann | 0.595s | | |
| fib | 0.140s | | |
| mutual_recursion | 0.224s | | |
| fibonacci_iterative | 0.277s | | |
| sum_primes | 0.004s | | |
| nbody (regression watch) | 0.245s | | |
| mandelbrot (regression watch) | 0.063s | | |

## Lessons (filled after completion/abandonment)
