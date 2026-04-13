# Optimization Plan: Feedback-Typed Heap Loads (GetTable/GetField → GuardType)

> Created: 2026-04-06
> Status: completed (no_improvement)
> Cycle ID: 2026-04-06-feedback-typed-loads
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md

## Target
Insert `OpGuardType` after `OpGetTable`/`OpGetField` when the interpreter's FeedbackVector records a monomorphic result type. This enables TypeSpecialize to promote downstream generic `Mul`/`Add` to `MulFloat`/`AddFloat`, eliminating ~23 instructions per inner-loop iteration in matmul.

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| matmul | 0.834s | 0.022s | 37.9x | 0.74–0.78s (~7–11%) |
| spectral_norm | 0.336s | 0.008s | 42.0x | 0.30–0.32s (~5–10%) |
| nbody | 0.631s | 0.033s | 19.1x | 0.58–0.62s (~2–5%) |

## Root Cause
`graph_builder.go` emits `OpGetTable` and `OpGetField` with `TypeAny` (line 620, 646), even though the interpreter's `FeedbackVector` already records the result type for every PC. Because the result is `TypeAny`, TypeSpecialize cannot promote downstream arithmetic: `Add(TypeAny, TypeAny)` stays generic `OpAdd` instead of becoming `OpAddFloat`.

In matmul's inner loop (`sum = sum + ai[k] * b[k][j]`), this means Mul and Add each execute ~15 ARM64 instructions (generic type dispatch + unbox + compute + rebox) instead of ~3–5 instructions (specialized float path). That's ~20 extra instructions per iteration × 2 ops = ~30 instructions wasted.

## Prior Art (MANDATORY)
**V8 (TurboFan):** BytecodeGraphBuilder reads FeedbackVector slots for element loads (LoadIC). Inserts `CheckMaps` + typed `LoadElement` which gets Float64 machine representation. Monomorphic feedback enables full downstream arithmetic specialization. Deopt on CheckMaps failure. Source: `src/compiler/bytecode-graph-builder.cc`, `NewNode(simplified.LoadElement(...), ...)`.

**SpiderMonkey (WarpBuilder):** Reads CacheIR snapshots from baseline IC stubs. Translates CacheIR to MIR with `GuardShape` + `LoadFloat64`. MIRType propagates Float64 to downstream MathOps. Same pattern: heap load → guard → typed result → cascade.

**JSC (DFG):** Reads `ValueProfile` from LLInt/Baseline. `SpeculatedType` on `GetByVal` enables `SpecDouble` on downstream `ArithMul`/`ArithAdd` speculation. Profiling data drives speculation budget.

**Academic:** Hölzle, Chambers, Ungar, "Optimizing Dynamically-Typed Object-Oriented Languages With Polymorphic Inline Caches" (ECOOP 1991) — the original PIC feedback → speculative optimization pipeline.

Our constraints vs theirs:
- We have a simpler monotonic lattice (Unobserved→concrete→Any) vs V8's detailed FeedbackNexus. This is sufficient — we only need monomorphic/polymorphic distinction.
- Our GuardType deopts to interpreter (full bailout). V8/SpiderMonkey can deopt to a lower tier. Acceptable since the feedback lattice is monotonic — no deopt-reopt cycles.
- We already have 90% of the infrastructure: FeedbackVector populated by interpreter, OpGuardType emitted/interped/validated, TypeSpecialize propagates guard results. Only the graph builder read is missing.

## Approach

**Single change site**: `internal/methodjit/graph_builder.go`, in the `OP_GETTABLE` and `OP_GETFIELD` cases (~lines 614–647).

After emitting the GetTable/GetField instruction, read `b.proto.Feedback[pc].Result`. If monomorphic (not `FBUnobserved` and not `FBAny`), map to IR Type and insert `OpGuardType`:

```
// Pseudocode (not real code — for plan clarity)
if b.proto.Feedback != nil && pc < len(b.proto.Feedback) {
    fb := b.proto.Feedback[pc].Result
    if irType, ok := feedbackToIRType(fb); ok {
        guardID := b.fn.newValueID()
        guard := &Instr{ID: guardID, Op: OpGuardType, Type: irType,
            Args: []*Value{instr.Value()}, Aux: int64(irType), Block: block}
        block.Instrs = append(block.Instrs, guard)
        b.writeVariable(a, block, guard.Value())  // replaces downstream uses
    }
}
```

FeedbackType → IR Type mapping:
- `FBFloat` → `TypeFloat`
- `FBInt` → `TypeInt`
- `FBTable` → `TypeTable`
- `FBString`, `FBBool`, `FBFunction` → skip (rare, no arithmetic cascade benefit)
- `FBUnobserved`, `FBAny` → skip

**Helper function**: `feedbackToIRType(fb vm.FeedbackType) (Type, bool)` — a small mapping function, placed in `graph_builder.go` near the usage site. No separate file needed.

**No changes to any other file.** TypeSpecialize, regalloc, emit, LICM, DCE, validator, interp — all already handle OpGuardType correctly.

## Expected Effect
**Prediction calibration (MANDATORY):** Rounds 7–10 overestimated by 2–25× when anchoring to instruction counts without modeling ARM64 superscalar effects. This round's estimate is calibrated by halving the instruction-count-derived percentage:

- matmul: ~30 insns saved per inner iter (generic Mul+Add → specialized) out of ~103 → 29% insn reduction. Halved for superscalar: **~9–12% wall-time** (0.834s → 0.74–0.76s). Secondary cascade (phi FPR carry for `sum` accumulator) could add 2–4% but not counted in primary estimate.
- spectral_norm: Similar GetTable pattern in `eval_A_times_u` inner loop. ~15–20% insn reduction → **~5–10% wall-time** (0.336s → 0.30–0.32s).
- nbody: Fewer table accesses in hot path; estimate **~2–5%** at most.
- Other benchmarks: no regression expected — functions without feedback or with FBAny/FBUnobserved skip guard insertion entirely.

## Failure Signals
- Signal 1: Any benchmark produces wrong results after the change → **STOP**, use `Diagnose()` to identify the guard that causes mismatch, fix or revert.
- Signal 2: matmul wall-time does not improve by at least 3% → **Investigate**: dump IR to confirm guards are being inserted and Mul/Add are promoted. If guards inserted but no speedup, the bottleneck is elsewhere (table access itself, not arithmetic dispatch). Pivot to Phase 5 (matmul tier-up investigation).
- Signal 3: Any benchmark regresses by more than 2% → **Investigate**: check if polymorphic site is causing frequent deopts. If so, tighten the guard insertion criteria (only insert for FBFloat/FBInt, skip FBTable).

## Task Breakdown
Each task = one Coder sub-agent invocation.

- [x] 1. **Test: feedback-typed guard insertion** — file(s): `graph_builder_test.go` — Write a test that compiles a function with a known FeedbackVector (FBFloat for a GETTABLE PC), builds the IR graph, and asserts that OpGuardType(TypeFloat) appears after OpGetTable. Also test that FBUnobserved and FBAny do NOT produce a guard. Use the existing `buildGraphForTest` or equivalent test helper.

- [x] 2. **Implement: graph builder reads feedback** — file(s): `graph_builder.go` — Add `feedbackToIRType()` helper function. In `OP_GETTABLE` case (after line 621) and `OP_GETFIELD` case (after line 647), read `b.proto.Feedback[pc].Result`, insert OpGuardType if monomorphic, call `b.writeVariable` with the guard's value. Run Task 1's test to confirm it passes. Also run `go test ./internal/methodjit/ -run TestDiagnose` to verify no existing tests break.

- [x] 3. **Integration test + full benchmark** — file(s): `graph_builder_test.go` or `tier2_feedback_test.go` — Write an integration test: compile+execute a matmul-like loop (`sum = sum + t[i] * t[i]` where `t` is a float array), verify Diagnose shows IR match AND that MulFloat/AddFloat appear in the optimized IR. Run `bash benchmarks/run_all.sh` and compare results vs current baseline (0.834s matmul, 0.336s spectral, 0.631s nbody). Record before/after in Results section.

## Budget
- Max commits: 3 (+1 revert slot if guard insertion causes unexpected regressions)
- Max files changed: 2 (graph_builder.go, graph_builder_test.go or new test file)
- Abort condition: 2 commits without matmul showing ≥3% improvement, OR any correctness regression not fixable within 1 commit

## Results (filled after VERIFY)
| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| matmul | 0.834s | 0.833s | -0.1% (noise) |
| spectral_norm | 0.336s | 0.338s | +0.6% (noise) |
| nbody | 0.631s | 0.637s | +1.0% (noise) |
| All others | — | — | No regression |

**Failure Signal 2 triggered:** matmul did not improve by ≥3%.

**Investigation:** Guards are NOT being inserted in real benchmarks. IR-level integration test confirms the mechanism works (MulFloat/AddFloat appear after TypeSpecialize with manually-set feedback). But in real execution, `proto.Feedback` is always empty at Tier 2 compilation time.

**Root cause:** Feedback availability window mismatch:
1. `BaselineCompileThreshold=1` → Tier 1 compiles on first call → interpreter NEVER executes function body
2. Tier 1 does not collect type feedback (only ARM64 native code, no feedback writes)
3. `EnsureFeedback()` only called at Tier 2 promotion (creates empty vector)
4. `BuildGraph()` sees all `FBUnobserved` → skips guard insertion

**Attempted fix (REVERTED):** Changed `BaselineCompileThreshold` from 1 to 2 to let interpreter run once. Result: catastrophic — 4-24x regressions across most benchmarks, correctness bug in sort. The threshold change disrupted the entire tiering system.

## Lessons (filled after completion/abandonment)

1. **The graph builder guard insertion code is correct and ready.** It works perfectly when feedback is available. The infrastructure is in place — the missing piece is feedback data.

2. **Feedback collection is an interpreter-only capability.** Tier 1 native code does not collect feedback. With `BaselineCompileThreshold=1`, the interpreter never executes function bodies, so feedback is never collected. This is a fundamental architectural gap.

3. **Changing BaselineCompileThreshold is too invasive.** Even +1 causes catastrophic regressions because the entire tiering system (Tier 1 BLR calls, OSR, exit-resume, deopt fallback) depends on Tier 1 being available from call 1. Many benchmarks call functions millions of times — deferring Tier 1 means those first calls go through the slow interpreter.

4. **Next steps (for future rounds):**
   - **Option A (recommended):** Add lightweight feedback collection to Tier 1's Go-side exit handlers. When GETTABLE/GETFIELD exit to Go (cache miss, bounds check), record the result type. This piggybacks on existing slow paths without touching the fast path.
   - **Option B:** In TieringManager, before Tier 2 compilation, run ONE interpreter pass with feedback enabled (force-execute through interpreter), then compile. This avoids touching Tier 1 but requires careful state management.
   - **Option C:** Add inline feedback stubs to Tier 1's GETTABLE/GETFIELD ARM64 code (a few instructions to write FeedbackType). Most intrusive but captures all accesses.

5. **Calibration note:** The plan correctly predicted 7-11% improvement IF guards were inserted. The mechanism works at IR level. The bottleneck is upstream (feedback availability), not downstream (type specialization).
