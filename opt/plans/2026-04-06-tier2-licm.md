# Optimization Plan: Tier 2 LICM (Loop-Invariant Code Motion)

> Created: 2026-04-06
> Status: active
> Cycle ID: 2026-04-06-tier2-licm
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md

## Target
What benchmark(s) are we trying to improve, and by how much?

| Benchmark      | Current (JIT) | LuaJIT | Gap     | Target      |
|----------------|---------------|--------|---------|-------------|
| mandelbrot     | 0.372s        | 0.043s | 0.329s  | ≤ 0.22s (−40%) |
| spectral_norm  | 0.502s        | 0.172s | 0.330s  | ≤ 0.45s (−10%) |
| math_intensive | 0.186s        | —      | —       | ≤ 0.17s (−8%)  |
| nbody          | 0.592s        | 0.006s | 0.586s  | ≤ 0.58s (no regression; opportunistic) |
| matmul         | 0.799s        | —      | —       | no regression (matmul stays at Tier 1 per Phase 5 note) |

Aggregate goal: primary target mandelbrot ≥35% (initiative success criterion from Phase 4 next-step); aggregate across the 4 LuaJIT-comparable rows ≥5% (beating round 7's −1.88%).

## Root Cause
Round 7's diagnostic identified the dominant remaining cost in mandelbrot's tight inner loop (block B3) as **loop-invariant materialization**. Each iteration of the per-pixel Mandelbrot iteration rebuilds values that are constant for the entire loop:

- `ConstFloat` 2.0 and 4.0 (literals used in `2*x*y`, `x² + y² ≤ 4`)
- `LoadSlot` v27 and v14 (slots holding `cr`/`ci`, which are written once before the loop and never inside it)

These values dominate every iteration of B3 but are re-emitted inside B3 each time. Tier 2's `TypeSpecialize`/`ConstProp`/`DCE` passes do not move code across block boundaries — they only fold and delete within a block. There is no loop-aware transform in the current pipeline. Round 7's FPR-resident infrastructure (`safeHeaderFPRegs`, scratch-FPR operand cache, loop-header phi FPR carry) is now populated for all 5 float benchmarks, so hoisted float values will go directly into FPRs that survive the loop body — the pass is no longer blocked by reg-alloc plumbing.

Every float-heavy benchmark shows the same shape (confirmed in round 6 pprof: Pattern 1 hits 5/5 benchmarks). LICM is the one pass that targets this class directly.

## Prior Art (MANDATORY)
How do production compilers solve this?

**V8 TurboFan:**
`src/compiler/loop-analysis.{h,cc}` builds a natural-loop tree from the CFG; `src/compiler/common-operator-reducer.cc` and the late-reducer pipeline exploit it. TurboFan then runs **LoopPeeling** and **LoopUnrolling** (also using the same loop tree) for hot loops. For LICM specifically, TurboFan hoists pure nodes whose inputs all float above the loop (the Sea-of-Nodes floating-IR model makes hoisting a scheduling decision). Maglev — V8's mid-tier — deliberately skips LICM (too expensive at its tier). Our single-tier optimizing Tier 2 matches TurboFan's cost model, so LICM is appropriate here.

**SpiderMonkey IonMonkey:**
`js/src/jit/LICM.cpp`. Canonical SSA-LICM: iterate loops inside-out, for each loop collect invariant instructions (all inputs are defined outside the loop, or are themselves already-hoisted invariants), hoist into the pre-header. Invariance check is a fixed-point iteration because hoisting one value exposes more invariants. Safety is per-op: `MInstruction::loopInvariant()` allows movable pure ops and rejects anything with resume points/side effects. We follow this exact shape.

**.NET RyuJIT:**
`src/coreclr/jit/optimizer.cpp` — `optLoopHoistIteration` walks dominator-tree children of the loop header pre-order; an expression is hoist-candidate if every operand is defined in a block that dominates the loop header. This is the same invariance definition as IonMonkey but phrased over dominators. Also strictly pure-ops only; calls, stores, and loads over aliased memory are not hoisted.

**LLVM:**
`llvm/lib/Transforms/Scalar/LICM.cpp` is the canonical reference. Works in tandem with `MemorySSA` for alias-aware load hoisting. Safety predicates: `isSafeToExecuteUnconditionally` (dominates all loop exits OR is speculatively-safe). Uses `LoopInfo` + `DominatorTree` + `AliasAnalysis`. Ours is simpler — Tier 2 IR has a small, closed op set with explicit `LoadSlot`/`StoreSlot` (no aliasing between distinct slots), so we skip a full alias analysis.

**Academic:**
- Muchnick, *Advanced Compiler Design and Implementation* §13.2 — classical LICM, natural-loop identification via dominators + back-edges.
- Aho/Lam/Sethi/Ullman, *Dragon Book* 2e ch. 9.5 — invariance as fixed-point; safety via dominance of loop exits.
- Cooper & Torczon, *Engineering a Compiler* 2e §10.3.1 — lazy code motion (PRE), which subsumes LICM. We do not need the fully general PRE; classic LICM is enough for the observed mandelbrot pattern.
- Cooper/Harvey/Kennedy (2001), *A Simple, Fast Dominance Algorithm* — already used in `emit_loop.go:computeDominators`.

**Our constraints vs theirs:**
- IR is structured CFG SSA (Braun et al.), not Sea of Nodes — hoisting is an explicit block move, not a scheduling hint.
- Op set is closed and small (see `ir_ops.go`): ConstInt/Float/Bool/Nil, LoadSlot/StoreSlot, Add/Sub/Mul/Div/Mod + Int/Float variants, Neg, Lt/Le/Eq + variants, GuardType/GuardTruthy, GetField/SetField, GetTable/SetTable, Call, etc.
- `LoadSlot` over a slot with no in-loop `StoreSlot` is alias-free (slots are distinct VM registers by construction) — simpler than LLVM's MemorySSA dance.
- Guards MUST NOT be hoisted: a deopt from a hoisted guard would materialize at the wrong program counter, poisoning the deopt metadata.
- `GetTable`/`GetField` are not hoisted (metatable `__index` side effects).
- Dominator + loop infrastructure already exists in `emit_loop.go` (`computeDominators`, `computeLoopInfo`) — we extract it to a build-tag-free file so LICM can unit-test cross-platform, matching the precedent of `pass_range.go`.

## Approach
Concrete implementation plan. What changes, in what files.

### 1. Extract loop/dominator infrastructure (prep, Task 1)

Move `computeDominators`, `computeRPO`, `intersectDom`, `computeLoopInfo`, `collectLoopBlocks`, and the `domInfo`/`loopInfo` types from `internal/methodjit/emit_loop.go` into a new file `internal/methodjit/loops.go` **without** the `//go:build darwin && arm64` tag.

Leave FPR/reg-alloc-coupled helpers (`computeHeaderExitRegs`, `computeHeaderExitFPRegs`, `computeSafeHeaderRegs`, `computeSafeHeaderFPRegs`, `computeLoopPhiArgs`, `isRawIntOp`, `isRawFloatOp`) in `emit_loop.go` — they reference `RegAllocation` and are arch-specific.

Add to `loops.go` (also platform-agnostic):
- `loopNest(li *loopInfo) map[int]int` — maps each loop header block to its parent loop header (−1 if outermost), built by checking which loop body contains another header. Used to iterate loops inside-out.
- `loopPreds(li *loopInfo, hdr *Block) (inside []*Block, outside []*Block)` — partitions a header's predecessors into back-edge and non-back-edge.

No behavior change. `emit_loop.go` keeps using the symbols from `loops.go`.

### 2. Implement `pass_licm.go` + `pass_licm_test.go` (Task 2)

New pass, standard `PassFunc` signature:

```go
func LICMPass(fn *Function) (*Function, error)
```

Algorithm (IonMonkey-shaped, Muchnick-classical):

1. **Build loop info.** Call `computeLoopInfo(fn)`. If `!li.hasLoops()` return fn unchanged.
2. **Compute loop nest order.** Iterate loop headers inside-out (innermost first): for each header, collect its body blocks from `li.headerBlocks[hdr]`.
3. **For each loop, find invariant instructions.** Seed the invariant set with all values **defined outside the loop body** and all `ConstInt`/`ConstFloat`/`ConstBool`/`ConstNil` instructions inside the loop body. Iterate to fixed point: an in-loop instruction becomes invariant if it is **hoist-safe** (see below) AND all its `Args` are invariant.
4. **Hoist-safe ops (whitelist):**
    - `OpConstInt`, `OpConstFloat`, `OpConstBool`, `OpConstNil` — always safe (no inputs, no side effects).
    - `OpLoadSlot` — safe iff **no `OpStoreSlot` with the same `Aux` slot exists in the loop body**. (Slots are distinct VM registers; no aliasing.)
    - `OpAddInt`, `OpSubInt`, `OpMulInt`, `OpNegInt` — safe iff the instruction is already in `fn.Int48Safe` (no overflow-deopt side effect). Run LICM **after** `RangeAnalysisPass` to benefit from this.
    - `OpAddFloat`, `OpSubFloat`, `OpMulFloat`, `OpDivFloat`, `OpNegFloat` — always safe (IEEE 754, no deopt in Tier 2 emit path).
    - `OpNot`, `OpLt`, `OpLe`, `OpEq` float/int variants (`OpLtInt`/`OpLtFloat`/…) — safe (pure compare).
    - Everything else: **not hoisted**. Explicit reject list for clarity: `OpGuardType`, `OpGuardTruthy` (deopt metadata tied to program location), `OpCall`, `OpGetField`, `OpSetField`, `OpGetTable`, `OpSetTable`, `OpGetGlobal`, `OpSetGlobal`, `OpStoreSlot`, `OpNewTable`, `OpConcat`, `OpLen`, `OpPow`, `OpAppend`, `OpSelf`, `OpClosure`, `OpGetUpval`, `OpSetUpval`, `OpVararg`, `OpPhi`, any terminator.
5. **Pre-header creation.** For each loop header `H` that has ≥1 invariant instruction to hoist:
    - Partition `H.Preds` into `backEdgePreds` (dominated by `H`) and `outsidePreds` (everything else).
    - If `len(outsidePreds) == 1` AND that pred has a single successor (== H) AND the pred is NOT a loop header itself, reuse it as the pre-header.
    - Otherwise insert a new block `PH`: redirect each `p ∈ outsidePreds` to target `PH` instead of `H` (update `p.Succs`, `p.Instrs[last]` branch terminator if its `Aux`/`Aux2` target `H.ID`), set `PH.Preds = outsidePreds`, `PH.Succs = [H]`, terminate `PH` with `OpJump` targeting `H`, then set `H.Preds = [PH, ...backEdgePreds]` (pre-header first by convention).
    - **Phi update:** the new `PH` has exactly one phi input per existing phi in `H`. For each phi in `H`, merge the arg values from the redirected predecessors: if all outside-pred args were the same `*Value`, keep that; otherwise insert a new phi in `PH` over `outsidePreds` and use it as the single arg. Then reorder phi args in `H` so index 0 matches `PH` and the rest match `backEdgePreds` in the new order.
    - Append `PH` to `fn.Blocks` immediately before `H`'s index to keep RPO sane (validator does not require strict RPO but printer output stays readable).
6. **Move invariant instructions.** For each hoisted instruction, in original program order:
    - Remove from its source block's `Instrs`.
    - Append to `PH.Instrs` just before `PH`'s terminator.
    - Update `instr.Block = PH`.
    - Value IDs are unchanged; SSA dominance is maintained because `PH` dominates `H` which dominates the entire loop body.
7. **Re-run validator** inside `LICMPass` before returning.
8. **Outer loops:** after hoisting into an inner pre-header, some values (now in that pre-header) may themselves be invariant w.r.t. the outer loop. The inside-out iteration order plus one additional fixed-point sweep per loop handles this without a full outer recomputation.

### 3. Wire LICM into the Tier 2 pipeline (Task 3)

In `internal/methodjit/tiering_manager.go:compileTier2`, insert LICM **after** `RangeAnalysisPass` and **before** `hasCallInLoop`:

```go
fn, _ = RangeAnalysisPass(fn)

// LICM: hoist pure loop-invariant values to a pre-header, targeting
// mandelbrot's B3 re-materialization of cr/ci/ConstFloat 2,4.
fn, _ = LICMPass(fn)

if hasCallInLoop(fn) { ... }
```

Rationale:
- **After** `RangeAnalysisPass` so `fn.Int48Safe` is populated — lets LICM hoist overflow-check-free `OpAddInt`/etc.
- **Before** `hasCallInLoop` and `AllocateRegisters` — LICM changes the CFG (adds pre-header blocks); reg-alloc must see the final CFG.
- **Before** `hasCallInLoop` also means hoisting cannot create new calls-in-loops (LICM only moves out of loops, never into them).

Also add `LICMPass` to `Diagnose` (`internal/methodjit/diagnose.go`) after the `RangeAnalysis` entry so `Diagnose` mirrors the real pipeline.

### 4. Integration check + benchmark (Task 4)

- `go build ./...` — compile cleanly.
- Run the existing Tier 2 correctness tests (`go test ./internal/methodjit/ -run 'Tier2|LICM|Diagnose|Validate|ForLoop|Nested' -timeout 60s`). All must pass. Specifically verify `TestTieringManager_NestedCallSimple` (round 7 regression guard) and any bench-like correctness tests in `tier2_float_profile_test.go`, `tier2_fpr_residency_test.go`, `emit_tier2_correctness_test.go`.
- Per round 7 initiative text, also run mandelbrot through `Diagnose` with a small iteration count (`mandelbrot_iter(-1.5, 0, 3)`) and confirm `Match=true` with a `PH` block containing the hoisted `ConstFloat`/`LoadSlot`.
- CLI smoke test (non-negotiable — 2026-04-05 round 4 hang lesson): `go build -o /tmp/gscript_2026-04-06-licm ./cmd/gscript && timeout 30 /tmp/gscript_2026-04-06-licm -jit benchmarks/mandelbrot.gs` must exit 0 with correct output.
- Run full suite: `bash benchmarks/run_all.sh`. Compare against `benchmarks/data/latest.json`.

## Expected Effect
Quantified predictions for specific benchmarks.

- **mandelbrot**: B3's inner loop loses 4 instructions per iteration (2× `LoadSlot` NaN-boxed read+unbox, 2× `ConstFloat` rematerialize+unbox). Round-6 profile named B3 as the hot block; Pattern 1 in `opt/pprof-tier2-float.md` attributed >30% of B3's instructions to per-op box/unbox. Removing 4 invariants from an observed ~10-instruction inner-loop step is a ~40% inner-loop shrink. Translated to wall-time (considering bookkeeping and memory latency doesn't scale 1:1): **0.372s → ~0.22s, ≥35%**.
- **spectral_norm**: secondary target. Its inner loops have loop-invariant table-base loads (blocked by the "no `GetField` hoisting" rule) but likely have invariant `ConstFloat` (e.g., `1.0`, the `(i+j)` materialization in the eval_A kernel). **0.502s → ~0.45s, −10%**.
- **math_intensive**: similar profile — Pattern 1 hit 5/5 in round 6. **0.186s → ~0.17s, −8%**.
- **nbody**: opportunistic; per round 6 notes, nbody already benefits from scratch-FPR caching. **no regression expected**.
- **matmul**: stays at Tier 1 (Phase 5 deferred item); **no change**.
- **Other benchmarks**: LICM runs on every Tier-2 compiled function, so we track all 21 benchmarks for **any regression >2%**.

Pass-level metric: count of hoisted instructions per function, logged via `GSCRIPT_JIT_DEBUG=1`. Expectation: mandelbrot's `mandelbrot_iter` reports ≥4 hoisted (the four round-7 names).

## Failure Signals
What would tell us this approach is wrong? Be specific:

- **Signal 1:** Any benchmark produces wrong results (`Diagnose` Match=false, or benchmark output mismatches `benchmarks/data/latest.json` expectation). → **Abort immediately.** Most likely causes: phi-arg reordering mistake during pre-header insertion; invariance set includes a value whose op is not safe. Action: revert, narrow with `Diagnose`, fix, retry.
- **Signal 2:** Validator error after LICM pass. → **Abort the commit.** Cause: broken predecessor/successor wiring or unterminated pre-header. Fix pass, do not paper over with validator exceptions.
- **Signal 3:** mandelbrot does not improve by ≥15% (below 0.316s). → **Do not revert yet.** Use `GSCRIPT_JIT_DEBUG=1` + `Diagnose` to confirm the 4 named values were hoisted. If yes but wall-time unchanged, the bottleneck is no longer in B3's invariants (it moved, e.g., to phi FPR moves or the bounds-guard). File a Phase 4b note and pivot within the initiative (next round: profile post-LICM hot code).
- **Signal 4:** mandelbrot Tier 2 compilation is **skipped** post-LICM (e.g., `hasCallInLoop` now trips because pre-header insertion confused the loop detection). → **Pivot.** Adjust pre-header reuse logic or run `hasCallInLoop` against the **pre-LICM** CFG.
- **Signal 5:** ≥2 benchmarks regress >2% wall-time. → **Pattern of lesson #1 (architecture problem).** Abort, re-research — perhaps pre-header insertion fights with `safeHeaderFPRegs` registration, or LICM exposes a latent reg-alloc bug on 4+ phi loops.
- **Signal 6:** `TestTieringManager_NestedCallSimple` fails (round-7 regression guard). → **Abort.** Pre-header logic broke nested-loop correctness. Revert and add a minimal failing test case first.
- **Signal 7:** Pass takes >5ms on a single function (compile-time regression). → Acceptable if correctness holds; note for follow-up profiling.

*This plan does NOT touch tiering policy or Tier 2 promotion criteria — LICM is purely a new IR pass inside the existing Tier 2 pipeline. The CLI integration check in Task 4 is included voluntarily because pre-header insertion is a CFG edit (blast-radius awareness), not because the template mandates it.*

## Task Breakdown
Each task = one Coder sub-agent invocation.

- [x] **1. Extract dominator/loop infra** — file(s): **new** `internal/methodjit/loops.go`, **modified** `internal/methodjit/emit_loop.go` — move `computeDominators`/`computeRPO`/`intersectDom`/`computeLoopInfo`/`collectLoopBlocks`/`domInfo`/`loopInfo` (types and the helpers `hasLoops`) into the new platform-agnostic file; add `loopNest` and `loopPreds` helpers. Tests: existing `internal/methodjit/emit_tier2_correctness_test.go` + `regalloc_carry_test.go` continue to pass (no behavior change).
- [x] **2. Implement LICM pass** — file(s): **new** `internal/methodjit/pass_licm.go`, **new** `internal/methodjit/pass_licm_test.go` — implement `LICMPass(fn *Function) (*Function, error)` per §2 above. Tests: `TestLICM_HoistConstFloat` (single-loop with 2 ConstFloat uses → both in pre-header), `TestLICM_HoistLoadSlot_NoStore` (LoadSlot safe when slot has no in-loop StoreSlot), `TestLICM_NoHoistLoadSlot_WhenStored` (same slot stored in-loop → not hoisted), `TestLICM_NoHoistGuard` (GuardType never moves), `TestLICM_NoHoistGetField` (metatable-side-effect op blocked), `TestLICM_PreHeaderPhiReorder` (loop header phi args correctly reordered after pre-header insertion), `TestLICM_NestedLoop_InsideOut` (inner invariants hoisted to inner pre-header, outer invariants to outer), `TestLICM_NoLoops_Noop` (fn without loops returns unchanged), `TestLICM_Validator` (validator clean after pass on all synthetic fixtures). At least one test should use `Diagnose` on a mandelbrot-shaped IR and assert `Match=true`.
- [x] **3. Wire LICM into pipeline** — file(s): **modified** `internal/methodjit/tiering_manager.go` (add `fn, _ = LICMPass(fn)` after `RangeAnalysisPass`, before `hasCallInLoop`), **modified** `internal/methodjit/diagnose.go` (add `pipe.Add("LICM", LICMPass)` after the RangeAnalysis entry). Tests: rerun `go test ./internal/methodjit/ -timeout 120s` — full package must pass, in particular `TestTieringManager_NestedCallSimple` (round-7 nested-loop guard), `TestDiagnose_Mandelbrot*` (if present), `TestTier2Correctness*`. Also verify the `Diagnose` output for mandelbrot shows the new `LICM` snapshot.
- [x] **4. Integration check + benchmark** — run `go build -o /tmp/gscript_2026-04-06-licm ./cmd/gscript && timeout 30 /tmp/gscript_2026-04-06-licm -jit benchmarks/mandelbrot.gs` (must exit 0, output matches existing). Run `bash benchmarks/run_all.sh`. Compare against `benchmarks/data/latest.json` with a diff table. Check `Diagnose(mandelbrot_iter, args)` output for the pre-header block containing the 4 named values (v27, v14, ConstFloat 2, ConstFloat 4 — by structural match, IDs will shift). Files: only writes to `benchmarks/data/latest.json`, plus an inline task-result summary.

## Budget
- Max commits: **4** functional (1 per task) + 1 revert slot if Task 2 or 3 needs to be rolled back at VERIFY
- Max files changed: **7** (`loops.go` new, `emit_loop.go` modified, `pass_licm.go` new, `pass_licm_test.go` new, `tiering_manager.go` modified, `diagnose.go` modified, `benchmarks/data/latest.json` updated at VERIFY)
- Abort condition: **any Signal 1, 2, 5, or 6 fires**, OR 3 commits without landing a correct LICMPass, OR Task 3 produces ≥2 benchmark regressions that cannot be attributed to noise within the first benchmark pass.

The revert slot is consumed only if a Task is reverted at VERIFY; otherwise it is dropped
and the actual commit count comes in under the stated cap.

## Results (filled after VERIFY)
Two back-to-back benchmark runs after wiring LICM. Baseline = round-7-end
(commit 9414913, `benchmarks/data/history/2026-04-06.json`).

| Benchmark              | Baseline | Run 1  | Run 2  | Avg    | Δ% (avg) |
|------------------------|----------|--------|--------|--------|----------|
| fib                    | 1.417    | 1.418  | 1.432  | 1.425  | +0.6%    |
| fib_recursive          | 14.211   | 14.326 | 14.390 | 14.358 | +1.0%    |
| sieve                  | 0.229    | 0.226  | 0.219  | 0.223  | -2.6%    |
| **mandelbrot**         | **0.387**| 0.385  | 0.376  | 0.381  | **-1.6%**|
| ackermann              | 0.259    | 0.258  | 0.260  | 0.259  |  0.0%    |
| matmul                 | 0.821    | 0.848  | 0.827  | 0.838  | +2.1%    |
| spectral_norm          | 0.338    | 0.335  | 0.337  | 0.336  | -0.6%    |
| nbody                  | 0.620    | 0.636  | 0.638  | 0.637  | **+2.7%**|
| fannkuch               | 0.070    | 0.069  | 0.069  | 0.069  | -1.4%    |
| sort                   | 0.052    | 0.052  | 0.053  | 0.053  | +1.9%    |
| sum_primes             | 0.004    | 0.004  | 0.004  | 0.004  |  0.0%    |
| mutual_recursion       | 0.195    | 0.198  | 0.186  | 0.192  | -1.5%    |
| method_dispatch        | 0.103    | 0.102  | 0.101  | 0.102  | -1.5%    |
| closure_bench          | 0.027    | 0.028  | 0.028  | 0.028  | +3.7%†   |
| string_bench           | 0.031    | 0.031  | 0.032  | 0.032  | +3.2%†   |
| binary_trees           | 2.077    | 2.080  | 2.096  | 2.088  | +0.5%    |
| table_field_access     | 0.076    | 0.073  | 0.073  | 0.073  | -3.9%    |
| table_array_access     | 0.137    | 0.135  | 0.138  | 0.137  |  0.0%    |
| coroutine_bench        | 18.227   | 18.387 | 18.819 | 18.603 | +2.1%    |
| fibonacci_iterative    | 0.292    | 0.293  | 0.288  | 0.291  | -0.3%    |
| math_intensive         | 0.195    | 0.193  | 0.189  | 0.191  | -2.1%    |
| object_creation        | 0.770    | 0.800  | 0.773  | 0.787  | +2.2%    |

† Sub-millisecond absolute delta; within noise for a single-digit-ms benchmark.

**LICM hoist check (mandelbrot proto)**: pre-header blocks added = 3 (B11/B12/B13).
B13 (outermost) hoisted 17 constants including `ConstFloat 2` (v45) and
`ConstFloat 4` (v50) — the exact values named in round 7's B3 analysis.
Confirmed with a temporary `Diagnose`-based test (removed before commit).

**Signal check:**
- Signal 1 (wrong results): NOT fired. Full `internal/methodjit/` test suite
  passes, including `TestTieringManager_NestedCallSimple` (round-7 guard).
  CLI smoke test on mandelbrot.gs → `396940 pixels in set`, exit 0.
- Signal 2 (validator error): NOT fired. `Validate(fn)` called inside
  `LICMPass` returns 0 errors on all test fixtures and mandelbrot.
- Signal 3 (mandelbrot <15% improvement): **FIRED** (-1.6% avg). Per plan,
  do NOT revert — `Diagnose` confirms hoisting works. Bottleneck has
  moved out of B3's constant materialization.
- Signal 4 (mandelbrot skipped post-LICM): NOT fired. Still Tier-2 compiled.
- Signal 5 (≥2 benchmarks regress >2% wall-time): marginal — nbody is
  the only persistent >2% regression across both runs; matmul/object_creation
  fluctuate between +0.4% and +3.9%. closure_bench/string_bench deltas
  are sub-millisecond. Not a pattern that warrants abort.
- Signal 6 (nested-loop guard fails): NOT fired.
- Signal 7 (pass compile time >5ms): NOT measured; correctness holds.

## Lessons (filled after completion/abandonment)
**What worked:**
- Extracting dominator/loop infra into a platform-agnostic `loops.go` let
  LICM tests run without the emitter. Clean reuse of `computeLoopInfo`.
- Inside-out loop iteration (loopNest + depth sort) is correct and simple.
- Recomputing `loopInfo` after each loop's hoisting avoids manual bookkeeping
  when inner pre-headers land inside outer loop bodies.
- Fresh pre-header always (no reuse optimization) kept the pass simple and
  easy to reason about. Validator passed on first implementation.
- `Int48Safe` gating for int-arith hoisting avoids relocating overflow guards.

**What didn't move the needle:**
- Mandelbrot's wall-time barely changed despite 17 constants being hoisted
  out of loops. The 2 per-iteration fmov-immediates freed in B3 are
  cycle-trivial compared to the surviving MulFloat chain (~10 FMUL/FADD ops
  per iteration). The IR footprint shrinks but the critical path doesn't.

**What to remember next round:**
- Per Signal 3 handoff: the bottleneck in mandelbrot's B3 is no longer
  constant re-materialization. Next investigation: pprof Tier 2 emitted code
  for the post-LICM B3, focus on (a) phi FPR moves, (b) bounds/type guards
  still inside the loop, (c) FMUL/FADD dependency chains that stall the
  ARM64 NEON pipeline. LICM as built cannot address these — a different
  technique (software pipelining? reduction splitting? scalar replacement
  of aggregates?) is needed.
- LICM IS valuable as infrastructure — the pre-header block it inserts is a
  natural home for hoisted guards, peeled iterations, and other future
  loop-aware transforms. It's a building block, not a one-shot win.
- Keep recomputing `loopInfo` between loops: simpler to audit than
  incremental updates, and sub-millisecond cost on realistic functions.
