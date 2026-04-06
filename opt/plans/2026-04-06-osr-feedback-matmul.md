# Optimization Plan: Re-enable OSR for Single-Call Compute Functions

> Created: 2026-04-06 19:30
> Status: active
> Cycle ID: 2026-04-06-osr-feedback-matmul
> Category: field_access
> Initiative: standalone

## Target
What benchmark(s) are we trying to improve, and by how much?

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| matmul | 0.207s | 0.022s | 9.4x | 0.06–0.12s |

Secondary (regression check — should not regress):

| Benchmark | Current (JIT) | Expectation |
|-----------|--------------|-------------|
| mandelbrot | 0.383s | no regression |
| nbody | 0.620s | no regression |
| spectral_norm | 0.156s | no regression |
| sieve | 0.082s | no regression |
| fannkuch | 0.078s | no regression |

## Root Cause
matmul is the most compute-intensive single-call function in the benchmark suite. It has 27M inner-loop iterations (300³) doing `sum = sum + a[i][k] * b[k][j]` — all float arithmetic on float-array table accesses.

**Problem 1: Never reaches Tier 2.** matmul is called once. `shouldPromoteTier2` requires `runtimeCallCount >= 2` for pure-compute functions. OSR (which would promote mid-execution) is disabled (tiering_manager.go:151-155, commented out since mandelbrot regression).

**Problem 2: Even at Tier 2, inner loop would be untyped without feedback.** `GetTable` results are `:any` → `Mul(any, any)` → generic dispatch. But Round 14 landed Tier 1 GETTABLE feedback stubs: float-array accesses record FBFloat. Round 12 landed GuardType insertion in graph builder when feedback is monomorphic. The pipeline works end-to-end (TestFeedbackGuards_Integration), but matmul never reaches Tier 2 to use it.

**Combined fix**: Re-enable OSR → matmul runs at Tier 1 for ~1000 iterations (collecting FBFloat feedback on float-array GETTABLEs) → OSR fires → Tier 2 compiles with feedback → `GetTable → GuardType(float) → MulFloat/AddFloat` cascade → FPR-resident loop accumulator.

## Prior Art (MANDATORY)

**V8:** OSR in TurboFan fires via back-edge interrupt check. `JumpLoop` bytecode handler checks interrupt flag, which triggers `Runtime_CompileOptimizedOSR`. V8 enters the optimized function at the exact loop header (not restart-from-top). GScript's simplified OSR restarts from the function entry, which is fine for single outer loops but wastes initial setup work for deeply nested cases.

**LuaJIT:** Trace JIT naturally handles single-call functions — the trace recorder triggers on loop back-edges regardless of call count. Inner loops get traced after ~56 iterations (default `hotloop`). This is analogous to our OSR mechanism.

**SpiderMonkey:** Warp/IonMonkey use OSR with `osrEntryOffset` pointing to the loop header. Similar to V8's approach. Both engines use OSR as the primary mechanism for single-call long-running functions.

Our constraints vs theirs: GScript's OSR restarts the entire function (not loop-header entry), which means matmul redoes the outer i=0,1,2 iterations at Tier 2. For N=300, this is negligible (3 outer iterations vs 27M inner iterations).

## Approach
Concrete implementation plan. What changes, in what files.

### Task 1: Re-enable OSR with LoopDepth >= 2 gate
**File**: `internal/methodjit/tiering_manager.go`
- Uncomment lines 153-155 (SetOSRCounter call)
- Add `profile.LoopDepth >= 2` gate to target deeply-nested-loop functions
- This is safe because:
  - mandelbrot already reaches Tier 2 via call count (threshold=2, called 1000x) — OSR never fires
  - matmul has LoopDepth=3 and is called once — OSR fires after 1000 inner iterations
  - Simple single-loop functions (LoopDepth=1) are unaffected

### Task 2: Diagnostic test — verify feedback → typed Tier 2 IR for matmul
**File**: `internal/methodjit/osr_test.go` (or new test in existing file)
- Compile matmul-like function (triple nested loop with float-array GetTable)
- Run at Tier 1 to collect feedback
- Build Tier 2 IR and verify:
  - GuardType(TypeFloat) appears after inner-loop GetTable
  - TypeSpecialize produces MulFloat/AddFloat
  - IR interpreter gives correct result

### Task 3: Integration test via CLI
- Build CLI: `go build -o /tmp/gscript_r15 ./cmd/gscript`
- Run matmul: `perl -e 'alarm 15; exec' /tmp/gscript_r15 benchmarks/suite/matmul.gs`
- Verify correct result (center = 75.335838)
- Verify improvement from baseline 0.207s

### Task 4: Full benchmark suite regression check
- Run all benchmarks through CLI
- Verify no regressions (especially mandelbrot, nbody, spectral_norm)
- Record results

## Expected Effect
Quantified predictions for specific benchmarks.

**matmul**: Currently at Tier 1, inner loop uses generic MUL/ADD dispatch (~10 insns each). With OSR + feedback-typed Tier 2: MulFloat (1 FMUL) + AddFloat (1 FADD), plus loop accumulator in FPR (no per-iteration NaN-boxing). Saves ~20 insns per inner iteration on MUL+ADD alone. 27M iterations × 20 insns × 0.3ns/insn = ~160ms savings on a 207ms baseline.

**Prediction calibration (MANDATORY):** Halving for ARM64 superscalar: ~80ms savings → target **0.06–0.12s** (40-70% improvement). This estimate is more reliable than instruction-count extrapolations from rounds 7-10 because the improvement mechanism is qualitatively different: eliminating generic dispatch (branch-heavy, pipeline-stalling) vs eliminating redundant loads (memory-latency-dominated). Superscalar cores are better at hiding load latency than branch misprediction costs.

**Other benchmarks**: No change expected. OSR only fires for functions with LoopDepth >= 2 that don't reach Tier 2 threshold. The only such function in the current benchmark suite is matmul.

## Failure Signals
What would tell us this approach is wrong? Be specific:
- Signal 1: matmul hangs or crashes after OSR → revert OSR re-enable, investigate handleOSR path. Action: abandon.
- Signal 2: matmul produces wrong result → GuardType deopt issue. Action: check if feedback is correct, verify deopt path works.
- Signal 3: matmul shows <10% improvement → feedback not reaching Tier 2. Action: add GSCRIPT_JIT_DEBUG=1 diagnostic, check if feedback vector is populated at OSR time.
- Signal 4: Other benchmarks regress >5% → OSR firing for wrong functions. Action: tighten LoopDepth gate or add function-specific exclusions.

## Task Breakdown
Each task = one Coder sub-agent invocation.

- [x] 1. Re-enable OSR with LoopDepth >= 2 gate — file(s): `tiering_manager.go` — test: `TestOSR*` existing tests ✓ commit 056607b
- [x] 2. Diagnostic: verify feedback-typed Tier 2 IR for matmul-like function — file(s): `osr_test.go` — test: new `TestOSR_FeedbackTypedMatmul` ✓ commit b4bf59a
- [x] 3. Integration test: CLI build + matmul benchmark + full regression suite ✓ results below

## Results (filled by VERIFY)

### Formal Benchmark (full suite, no debug)

| Benchmark | Baseline JIT | Now JIT | Change | LuaJIT | Gap |
|-----------|-------------|---------|--------|--------|-----|
| matmul | 0.215s | 0.152s | **-29%** | 0.024s | 6.3x |
| mandelbrot | 0.393s | 0.080s | **-80%** | 0.063s | 1.27x |
| spectral_norm | 0.156s | 0.057s | **-64%** | 0.008s | 7.1x |
| fannkuch | 0.086s | 0.072s | **-16%** | 0.022s | 3.3x |
| nbody | 0.638s | 0.796s | +25%* | 0.036s | 22x |
| sieve | 0.084s | 0.106s | +26%* | 0.012s | 8.8x |

*Baseline is stale (pre-round-13/14). IMPLEMENT A/B testing showed ~0% change for nbody/sieve from this round's OSR change specifically. Regressions are pre-existing.

### IMPLEMENT A/B (with JIT debug, isolates this round's changes)

| Benchmark | Before | After | Change | OSR effect |
|-----------|--------|-------|--------|------------|
| matmul | 0.312s | 0.167s | **-46%** | compiled via OSR |
| mandelbrot | 0.580s | 0.087s | **-85%** | compiled via OSR |
| spectral_norm | 0.236s | 0.063s | **-73%** | multiplyAtAv via OSR |
| fannkuch | 0.120s | 0.080s | **-33%** | compiled via OSR |
| nbody | 1.041s | 0.997s | ~0% | unchanged |
| sieve | 0.124s | 0.130s | ~0% | unchanged |

Key finding: plan assumed mandelbrot "called 1000x" but it's called ONCE with param 1000. LoopDepth >= 2 gate catches mandelbrot, spectral_norm, fannkuch in addition to matmul.

### Test Status
- All passing (methodjit + vm)

### Evaluator Findings
- PASS — flagged stale comment about mandelbrot (LoopDepth=1, called 1000x → actually LoopDepth=3, called once). Fixed.

### Regressions (≥5% from this round)
- None attributable to this round (A/B testing confirms)

## Outcome: improved

## Lessons
1. **Tiering gates can silently block entire benchmark classes.** OSR was disabled in round 3 for a mandelbrot regression that was fixed by round 7. 11 rounds of Tier 2 improvements were invisible to every single-call function.
2. **"Called N times" vs "called with parameter N" is a dangerous confusion.** The plan assumed mandelbrot was called 1000x. It's called once with param 1000. Diagnostic data (actual callCount) should be checked, not inferred from benchmark source.
3. **LoopDepth >= 2 is a better gate than expected.** It catches exactly the compute-heavy benchmarks (matmul, mandelbrot, spectral_norm, fannkuch) without affecting simple-loop benchmarks (sieve, nbody). Production compute kernels are deeply nested.
4. **The biggest win was mandelbrot (-80%), not the planned target matmul (-29%).** Sometimes unblocking a mechanism yields more than optimizing within a mechanism.
5. **mandelbrot is now 1.27x from LuaJIT.** First benchmark approaching production JIT parity.

## Budget
- Max commits: 2 (+1 revert slot)
- Max files changed: 3
- Abort condition: matmul hang, suite-wide regression, or 3 commits without matmul improvement
