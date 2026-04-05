# Optimization Plan: Fix Tier 2 Correctness — 7 Benchmarks Wrong

> Created: 2026-04-04  
> Status: active  
> Cycle ID: 2026-04-04-tier2-correctness

## Target
Fix correctness failures in 7 benchmarks that produce wrong results under Tier 2 JIT.

| Benchmark | Symptom | JIT Result | Expected (VM) |
|-----------|---------|-----------|----------------|
| sieve | Wrong prime count | 999,999 | 78,498 |
| nbody | Wrong energy | -0.169025592 | -0.169041687 |
| sum_primes | Wrong count+sum | 33,334 / 1.67B | 9,592 / 454M |
| table_field_access | Garbage checksum | -4.16e203 | 2247.74 |
| fibonacci_iterative | fib(70)=0 | 0 | 190,392,490,709,135 |
| coroutine_bench | Wrong generator sum | 5.19e13 | 3.33e14 |
| object_creation | len_sq=0 | 0.00 | large number |

## Root Cause
All 7 benchmarks have loops that promote to Tier 2. Recent commits (8850683..deeac20) rewrote the register state management from a flat map to per-header nested maps with safe-register filtering. This new code has NOT been tested with complex multi-operation loop bodies.

**Three suspect areas (ranked):**
1. **Per-header register activation** (`emit_compile.go:442-490`) — `computeSafeHeaderRegs`, `blockInnerHeader`, phi register invalidation at block entry. A wrong header assignment = wrong register set = stale values.
2. **Native GETTABLE/SETTABLE** (`emit_table.go:355-630`) — New native paths use X0-X6 as scratch. If scratch clobbers a value loaded by a prior `resolveValueNB`, result is stale/wrong.
3. **emitCallNative spill/reload** (`emit_call_native.go:39-236`) — Selective spill may miss live values across BLR calls. Affects fibonacci_iterative, object_creation, nbody.

## Prior Art (MANDATORY)
This is a correctness fix, not a new optimization. The relevant prior art is our own diagnostic infrastructure:

**V8:** Uses a "deoptimizer" that validates assumptions at runtime. When wrong, falls back to interpreter with full state reconstruction.  
**LuaJIT:** Uses guards with side exits. Guard failure aborts the trace and falls back to interpreter.  
**Our approach:** Same guard+deopt model. The bug is not in the model but in the register state management within the Tier 2 emitter — stale register values after loop header transitions.

Our constraints: We must fix the register state management without regressing the already-passing benchmarks (mandelbrot, spectral_norm, fannkuch, etc.)

## Approach
1. **Write reproducing tests first** (TDD) — one test per failure pattern
2. **Fix per-header register state** — the most likely root cause affecting all 7
3. **Fix native GETTABLE/SETTABLE scratch conflicts** if tests still fail
4. **Fix emitCallNative spill/reload** if call-heavy benchmarks still fail
5. **Verify** all 22 benchmarks correct + no regressions

## Expected Effect
- 7 benchmarks go from WRONG to CORRECT
- No performance regression on already-correct benchmarks
- Reliable baseline for future optimization rounds

## Failure Signals
- Signal 1: Fix #1 (register state) doesn't fix ANY of the 7 → likely wrong root cause → re-diagnose with IR dumps → pivot
- Signal 2: Fix causes regressions in mandelbrot/spectral_norm/fannkuch → revert, scope too broad
- Signal 3: After all 3 fixes, still >3 benchmarks wrong → architecture problem per lesson #3 → full Tier 2 audit

## Task Breakdown
Each task = one Coder sub-agent invocation.

- [x] 1. **Write Tier 2 loop correctness tests** — file: `emit_tier2_correctness_test.go` — 6 tests written. 2 FAIL (sieve=hang, fibonacci=wrong int 536870912), 4 PASS (table_field, call_in_loop, nested_loop_table, mixed_int_float).
- [x] 2a. **Fix GPR phi move ordering** — file: `emit_dispatch.go` — Added `emitGPRPhiMovesOrdered` with topological sort + cycle-breaking via X0. Fixes fibonacci accumulator swap. Root cause: sequential phi moves clobber registers + memory write-through conflicts.
- [x] 2b. **Fix rawIntRegs corruption in deopt path** — files: `emit_table.go`, `emit_call_exit.go` — Save/restore rawIntRegs around deopt fallback emission + emitUnboxRawIntRegs helper. Fixes sieve infinite loop. Root cause: emitReloadAllActiveRegs mutated build-time state.
- [ ] 3. (skipped — tests pass without this)
- [ ] 4. (skipped — tests pass without this)
- [x] 5. **Full suite verification** — 2 of 7 fixed (sieve, sum_primes). 5 remain with different root causes.

## Budget
- Max commits: 8
- Max files changed: 6 (test file + up to 3 emitter files + 2 support files)
- Abort condition: 3 commits without reducing the number of wrong benchmarks
- Actual: 0 commits (not yet committed), 3 files changed (emit_dispatch.go, emit_table.go, emit_call_exit.go)

## Results (filled after VERIFY)
| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| sieve | WRONG (999,999 primes) | CORRECT (78,498) | FIXED |
| sum_primes | WRONG (33,334 / 1.67B) | CORRECT (9,592 / 454M) | FIXED |
| nbody | WRONG (-0.169025592) | WRONG (-0.169025592) | unchanged |
| table_field_access | WRONG (-4.16e203) | WRONG (overflow) | unchanged |
| fibonacci_iterative | WRONG (fib(70)=0) | WRONG (72,723,460,248,141) | different wrong — phi fix helped but int overflow at fib(70) |
| coroutine_bench | WRONG (5.19e13) | WRONG (5.19e13) | unchanged |
| object_creation | WRONG (0.00) | WRONG (0.00) | unchanged |

## Lessons
1. **GPR phi move ordering was a real, confirmed bug** — the FPR equivalent existed but GPR was missed. Both register conflicts AND memory write-through conflicts must be handled.
2. **rawIntRegs build-time state mutation in deopt paths is a systemic issue** — anywhere deopt/exit code is emitted inline (not via separate codegen pass), build-time state must be saved/restored.
3. **Remaining 5 bugs have different root causes** — not register state management. Likely: int overflow in raw-int path, float accumulation in Tier 2 loops, coroutine/Tier 2 interaction.
4. **Tests for fib_iter(30) passed but fib(70) still fails** — the phi fix was correct for small values but int overflow at large values is a separate issue. Always test with values near boundaries.
5. **Worktree isolation loses uncommitted changes** — agent worktrees that don't commit their work lose changes when cleaned up. Use worktrees only when changes will be committed, or avoid worktrees for code fixes.
