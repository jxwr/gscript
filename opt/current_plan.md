# Optimization Plan: Tier 1 Float/Bool Array Fast Paths + Feedback Collection

> Created: 2026-04-06
> Status: active
> Cycle ID: 2026-04-06-tier1-feedback-fast-paths
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md (Phase 3 unblock)

## Target

Two-pronged: (1) eliminate exit-resume for float/bool arrays at Tier 1, (2) collect type feedback during Tier 1 execution so Tier 2 can specialize heap loads.

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| matmul | 0.985s | 0.023s | 42.8x | 0.6-0.8s (Tier 1 only — exit-resume elimination) |
| spectral_norm | 0.335s | 0.008s | 41.9x | 0.25-0.30s (feedback → Tier 2 typed loads) |
| nbody | 0.677s | 0.035s | 19.3x | 0.55-0.65s (partial — GETTABLE feedback only) |
| sieve | 0.186s | 0.012s | 15.5x | 0.17-0.18s (Tier 1 bool fast path, minor) |

## Root Cause

**Two independent bottlenecks, one root cause: Tier 1 has no fast path for Float/Bool arrays and collects no type feedback.**

1. **Exit-resume for float/bool arrays at Tier 1**: `emitBaselineGetTable` (tier1_table.go:258-262) dispatches only Mixed(0) and Int(1). Float(2) and Bool(3) fall to slow path (exit-resume). For matmul (stuck at Tier 1, called once, 27M inner iterations doing GETTABLE on float arrays), every inner-loop access goes through a full Go function call (~100-200ns) instead of a ~5ns native load.

2. **Empty FeedbackVector at Tier 2 compile time**: BaselineCompileThreshold=1 means the interpreter never runs, so feedback is never collected. Tier 1's ARM64 templates don't collect feedback. When Tier 2 compiles, `proto.Feedback[pc].Result == FBUnobserved` for all PCs. The graph builder's GuardType insertion code (graph_builder.go:622-657, landed in round 12) finds nothing to specialize. Result: `GetTable` returns `:any`, `Mul(any, any)` stays generic, no type cascade.

**Observation data**: Round 12 proved the IR-level mechanism works (graph builder reads feedback, inserts OpGuardType, TypeSpecialize cascades). Round 13 proved the Tier 2 array-kind fast path pattern works. This round connects the two by (a) extending fast paths to Tier 1, and (b) collecting feedback during those fast paths.

## Prior Art (MANDATORY)

**V8 (Sparkplug → TurboFan):** Sparkplug is V8's baseline JIT. It routes ALL property accesses through Inline Caches which record type feedback into FeedbackVector slots. When TurboFan compiles, it reads this feedback via BytecodeGraphBuilder and inserts CheckMaps/representation nodes. The feedback collection is always-on at baseline tier — no warmup period needed.

**LuaJIT:** The interpreter records type info into per-instruction "hints" that the trace recorder reads when deciding specialization. For table accesses, the element kind (TNUM, TSTR, etc.) is recorded.

**SpiderMonkey (Warp):** Baseline JIT records CacheIR snippets per property access. WarpBuilder reads these during Ion compilation to determine result types and insert GuardShape/GuardType nodes.

**JSC (DFG/FTL):** LLInt and Baseline JIT record ValueProfile per bytecode. DFG reads SpeculatedType from these profiles.

**Universal pattern across all engines:** baseline tier collects per-bytecode type feedback; optimizing tier reads it to insert speculative guards. GScript's missing link is step 1 — Tier 1 doesn't collect feedback.

Our constraints vs theirs: V8/SpiderMonkey use IC-based feedback (polymorphic tracking); we only need monotonic type lattice (much simpler). V8's FeedbackVector has complex slot types; ours is 3 bytes per PC.

## Approach

### Task 0: Prerequisite — no file splits needed
`tier1_table.go` is 545 lines. Adding ~120 lines for new fast paths + feedback stubs = ~665 lines. Well under 1000-line limit.

### Task 1: Add ArrayFloat/ArrayBool fast paths to Tier 1 GETTABLE
**File:** `internal/methodjit/tier1_table.go` — `emitBaselineGetTable`

Extend the arrayKind dispatch from `{Mixed(0), Int(1), else→slow}` to `{Bool(3), Float(2), Int(1), Mixed(0), else→slow}`. Same dispatch order as Tier 2 (emit_table.go:422-430).

- **ArrayFloat fast path**: load float64 from `TableOffFloatArray` + bounds check → raw float64 bits ARE the NaN-boxed value (no conversion). ~5 ARM64 instructions.
- **ArrayBool fast path**: load byte from `TableOffBoolArray` + bounds check → branch on 0/1/2 → produce NaN-boxed nil/false/true. ~12 ARM64 instructions.
- **Test:** `TestBaselineGetTable_FloatArray`, `TestBaselineGetTable_BoolArray`

### Task 2: Add ArrayFloat/ArrayBool fast paths to Tier 1 SETTABLE
**File:** `internal/methodjit/tier1_table.go` — `emitBaselineSetTable`

Same dispatch extension. SetTable patterns:
- **ArrayFloat**: check value is float (tag < 0xFFFC) → store raw bits to `TableOffFloatArray[key]`. ~8 ARM64 insns.
- **ArrayBool**: check value is bool → extract byte encoding (0=nil, 1=false, 2=true) → store to `TableOffBoolArray[key]`. ~10 ARM64 insns.
- **Test:** `TestBaselineSetTable_FloatArray`, `TestBaselineSetTable_BoolArray`

### Task 3: Add FeedbackPtr to ExecContext + feedback stubs in GETTABLE
**Files:** `internal/methodjit/emit.go` (ExecContext struct), `internal/methodjit/tier1_table.go`

3a. Add `BaselineFeedbackPtr uintptr` field to ExecContext (after BaselineGlobalCachedGen).
Add `execCtxOffBaselineFeedbackPtr` offset constant.

3b. In each GETTABLE array-kind fast path (Float, Int, Bool, Mixed), add feedback recording stubs:

For **typed-array paths** (Float, Int, Bool), the result type is known from the array kind. Stub is ~5-8 ARM64 insns in the common case:
```arm64
LDR  X5, [X19, #execCtxOffBaselineFeedbackPtr]  // 1: load feedback ptr
CBZ  X5, .skip_fb                                // 2: no feedback → skip
LDRB W6, [X5, #(pc*3+2)]                        // 3: load TypeFeedback[pc].Result
CMP  W6, #<expected_fb_type>                     // 4: already correct type?
B.EQ .skip_fb                                    // 5: yes → skip (most common after 1st iter)
// Cold path: update feedback (~4 insns, rarely taken)
```

For **ArrayMixed path**, skip feedback (type unknown without extraction). Mixed-array accesses are typically polymorphic anyway.

3c. In `BaselineJITEngine.Execute` (tier1_manager.go:161), add:
```go
if proto.Feedback != nil && len(proto.Feedback) > 0 {
    ctx.BaselineFeedbackPtr = uintptr(unsafe.Pointer(&proto.Feedback[0]))
}
```

3d. In `TieringManager.TryCompile`, when compiling Tier 1, ensure feedback is initialized:
```go
if proto.Feedback == nil {
    proto.EnsureFeedback()
}
```

**Test:** `TestBaselineFeedback_GetTable_Float`, `TestBaselineFeedback_GetTable_Int`

### Task 4: GETFIELD feedback stubs (result type from loaded value)
**File:** `internal/methodjit/tier1_table.go` — `emitBaselineGetField`

After the native fast path loads the value (svals[fieldIdx]), add feedback recording with type extraction from the NaN-boxed value:
```arm64
LSR  X_tag, X_value, #48     // extract tag
CMP  X_tag, #0xFFFC           // is it tagged?
B.LT .fb_float                // < 0xFFFC → float
CMP  X_tag, #0xFFFE
B.EQ .fb_int                  // int
MOV  W_type, #7               // else → FBAny
B    .fb_update
.fb_float: MOV W_type, #2     // FBFloat
B    .fb_update
.fb_int: MOV W_type, #1       // FBInt
.fb_update:
// Same update logic as Task 3
```

~12 ARM64 insns total. This enables nbody's GETFIELD results to be typed at Tier 2.

**Test:** `TestBaselineFeedback_GetField_Float`

### Task 5: Integration test + benchmark
- Verify end-to-end pipeline: Tier 1 runs → feedback populated → Tier 2 reads feedback → inserts GuardType → TypeSpecialize cascade
- Build CLI binary: `go build -o /tmp/gscript_r14 ./cmd/gscript`
- Run matmul, spectral_norm, nbody, sieve benchmarks
- Confirm no regressions across full suite

## Expected Effect

**Prediction calibration**: ARM64 superscalar hides ~50% of instruction-level savings (lesson from rounds 7-10). All estimates below are already halved.

| Benchmark | Mechanism | Est. Improvement | Reasoning |
|-----------|-----------|-----------------|-----------|
| matmul | Tier 1 exit-resume → native float load | -15% to -25% | 2 GETTABLE/iter × 27M iters; exit-resume ~150ns → native ~5ns. But Tier 1 NaN-boxing per-op limits the gain |
| spectral_norm | Tier 2 typed loads via feedback | -5% to -15% | GetTable `:any` → `:float` → MulFloat/AddFloat cascade. But the typed path still has NaN-box overhead |
| nbody | Tier 2 typed GETFIELD via feedback | -3% to -8% | GETFIELD results typed → partial cascade. Nbody's outer GETTABLE (bodies[i]) returns table, not float |
| sieve | Tier 1 bool fast path (minor, already at Tier 2) | -2% to -5% | Only helps the 1-2 Tier 1 calls before Tier 2 promotion |

**Total estimated aggregate improvement**: -5% to -15% across LuaJIT-comparable benchmarks.

**Primary value**: This round is **foundational** — it connects round 12's GuardType infrastructure to actual runtime data. Future rounds can build on typed loads for deeper optimizations (FPR-resident accumulator across typed GetTable results, typed loop phis, etc.).

## Failure Signals

- Signal 1: Tier 1 feedback stubs add >5% regression on benchmarks WITHOUT table ops (fibonacci_iterative, math_intensive) → remove stubs from non-table functions (conditional compilation based on FuncProfile.TableOpCount)
- Signal 2: matmul shows <5% improvement despite fast paths → profile Tier 1 ARM64 to verify exit-resume was actually the bottleneck (may be NaN-boxing per-op instead)
- Signal 3: Tier 2 GuardType insertion doesn't trigger despite populated feedback → debug by dumping proto.Feedback before Tier 2 compile; check feedbackToIRType mapping

## Task Breakdown

- [ ] 1. Tier 1 GETTABLE ArrayFloat/ArrayBool fast paths — file: `tier1_table.go` — test: `TestBaselineGetTable_FloatArray`, `TestBaselineGetTable_BoolArray`
- [ ] 2. Tier 1 SETTABLE ArrayFloat/ArrayBool fast paths — file: `tier1_table.go` — test: `TestBaselineSetTable_FloatArray`, `TestBaselineSetTable_BoolArray`
- [ ] 3. FeedbackPtr in ExecContext + GETTABLE feedback stubs — files: `emit.go`, `tier1_table.go`, `tier1_manager.go`, `tiering_manager.go` — test: `TestBaselineFeedback_GetTable_Float`
- [ ] 4. GETFIELD feedback stubs — file: `tier1_table.go` — test: `TestBaselineFeedback_GetField_Float`
- [ ] 5. Integration test + full benchmark suite — CLI build + tiering verification + regression check

## Budget

- Max commits: 4 functional (+1 revert slot)
- Max files changed: 4 (`emit.go`, `tier1_table.go`, `tier1_manager.go`, `tiering_manager.go`)
- Abort condition: 3 commits without any benchmark improvement, OR any regression >3% on non-table benchmarks

## Results (filled after VERIFY)
| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|

## Lessons (filled after completion/abandonment)
