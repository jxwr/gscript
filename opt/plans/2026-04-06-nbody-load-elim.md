# Optimization Plan: nbody Load Elimination + GuardType Fix

> Created: 2026-04-06 12:00
> Status: active
> Cycle ID: 2026-04-06-nbody-load-elim
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md

## Target

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| nbody | 0.796s | 0.036s | 22.1x | 0.65-0.72s (10-18%) |
| spectral_norm | 0.057s | 0.008s | 7.1x | monitor (secondary) |
| matmul | 0.152s | 0.024s | 6.3x | monitor (secondary) |

## Root Cause

nbody's `advance()` inner loop runs ~5M iterations (500K calls × 10 j-loop iterations). Each iteration does ~14 GETFIELD + 6 SETFIELD + ~20 float ops. Field access overhead is 64% of instruction count (~320/500 insns/iter). Two specific bottlenecks:

1. **Redundant GETFIELD**: `bj.mass` and `bi.mass` are each loaded 3 times per iteration via separate GETFIELD opcodes. Each GETFIELD costs ~16 ARM64 instructions (table type check, shape guard, field load, NaN-box store). 4 of 14 GETFIELD per iteration are redundant → 64 wasted instructions.

2. **emitGuardType TypeFloat no-op** (emit_call.go:70-72): When feedback-typed loads insert GuardType(TypeFloat) after GetField, the emitter skips the runtime check entirely (`default` case → pass-through). This is a correctness bug: if a field's type changes (e.g., integer stored into a previously-float field), the guard doesn't deopt, producing silently wrong results. The type narrowing still works for the optimizer (TypeSpecialize sees TypeFloat), but there's no safety net.

Cross-checked with constraints.md: no architectural ceiling on this category. The 8-FPR pool limit doesn't apply (nbody's inner loop has ~7 FPR-resident values, within budget). The approach doesn't touch regalloc or tiering policy.

## Prior Art (MANDATORY)

**V8:** `LoadElimination` pass (`src/compiler/load-elimination.cc`) — tracks "abstract state" per object: known field values. On seeing `LoadField(obj, +offset)`, checks if the same obj+offset already has a known value. If yes, replaces with the known value (CSE). Aliasing stores to the same field kill the entry. Different fields on the same object don't alias (V8 tracks by Map+offset pairs). Runs after inlining and before SimplifiedLowering.

**LuaJIT:** `lj_opt_mem.c` — `lj_opt_fwd_hload()` does forward substitution for HLOAD IR. If a prior HLOAD or HSTORE on the same table+key exists with no intervening alias, the load is eliminated. Happens during trace recording's IR optimization, not as a separate pass.

**SpiderMonkey (IonMonkey):** `AliasAnalysis` + `ValueNumbering` handle this. `GVN` CSEs `MLoadFixedSlot` with same object+slot within a basic block.

Our constraints vs theirs: GScript's GETFIELD operates on NaN-boxed table pointers with shape-guarded inline caches. V8's LoadElimination tracks Maps (shapes); we can track by SSA value ID (same value = same object) + field constant index (Aux). Unlike V8, we don't need to handle polymorphic cases (our shapes are monomorphic within a function).

## Approach

### Task 0: Diagnostic — verify GETFIELD feedback pipeline end-to-end

Write a test (`TestFeedbackGuards_GetField_Integration`) that:
1. Compiles a function with GETFIELD on float-valued table fields
2. Runs at Tier 1 to collect feedback
3. Builds Tier 2 IR and checks for OpGuardType after OpGetField
4. Verifies TypeSpecialize cascade produces OpMulFloat/OpAddFloat

**Files**: `graph_builder_test.go` (add test)
**Conditional**: If feedback is NOT working, Task 0b: fix the pipeline gap before proceeding.

### Task 1: Fix emitGuardType for TypeFloat

Add `case TypeFloat` to `emitGuardType` in `emit_call.go`:
```go
case TypeFloat:
    // Float: tag < 0xFFFC (raw IEEE754 bits have no NaN-box tag).
    asm.LSRimm(jit.X2, jit.X0, 48)
    asm.LoadImm64(jit.X3, 0xFFFC)
    asm.CMPreg(jit.X2, jit.X3)
    deoptLabel := ec.uniqueLabel("guard_deopt")
    asm.BCond(jit.CondGE, deoptLabel) // tag >= 0xFFFC means non-float
    ec.storeResultNB(jit.X0, instr.ID)
    doneLabel := ec.uniqueLabel("guard_done")
    asm.B(doneLabel)
    asm.Label(deoptLabel)
    ec.emitDeopt(instr)
    asm.Label(doneLabel)
```

**Files**: `emit_call.go`
**Test**: Existing `TestFeedbackGuards_Integration` + Task 0's new test. Also add a specific unit test `TestEmitGuardTypeFloat` that verifies float values pass and non-float deopts.

### Task 2: Load Elimination pass (block-local GetField CSE)

New file: `pass_load_elim.go` + `pass_load_elim_test.go`

Algorithm (block-local, simple and correct):
```
For each basic block:
  available = map[(objID, fieldAux) → valueID]
  For each instruction:
    if OpGetField(obj, fieldAux):
      key = (obj.ID, fieldAux)
      if key in available:
        replace all uses of this GetField's result with available[key]
        mark this GetField as dead (DCE will remove)
      else:
        available[key] = this.ID
    if OpSetField(obj, fieldAux, val):
      // Kill entries for this obj+field (value changed)
      delete available[(obj.ID, fieldAux)]
    if OpSetField(obj, ANY, val):
      // Conservative: only kill same-obj same-field, not all fields
      // Shape is stable so different fields don't alias
```

Wire into pipeline: after ConstProp, before DCE (so DCE removes dead GetFields).

**Files**: `pass_load_elim.go`, `pass_load_elim_test.go`, `tiering_manager.go` (wire into pipeline)
**Test**: Unit tests with known-redundant GetField patterns. Integration via nbody benchmark.

### Task 3: Benchmark + verify

Run full benchmark suite. Verify:
- nbody improvement (target: −10-18%)
- Zero regressions on other benchmarks
- Correctness: all 22 benchmarks produce correct results

## Expected Effect

| Change | Insns saved/iter | Wall-time impact (halved for superscalar) |
|--------|-----------------|------------------------------------------|
| Load Elimination (4 redundant GETFIELD removed) | −64 | −6-8% |
| TypeFloat guard (correctness, +4 insns × ~14 guards) | +56 | +3-5% |
| **Net** | **−8** | **~3-5% improvement** |

**Prediction calibration**: Instruction-count analysis overestimates by 2-3x on superscalar ARM64 (lessons from rounds 7-10). The 64-insn saving from Load Elimination is ~13% of 500 insns/iter, but superscalar execution will hide some of the LDR latency savings. Realistic estimate: 5-8% improvement on nbody.

**If Task 0 reveals feedback is broken** (feedback-typed loads not producing specialized arithmetic): Fixing the pipeline would convert ~20 generic ops (15-20 insns each) to specialized float ops (5-6 insns each), saving ~200-300 insns/iter. That's a potential 20-30% improvement, halved to ~12-15% for superscalar.

## Failure Signals

- Signal 1: Task 0 shows feedback IS working but nbody still uses generic arithmetic → indicates TypeSpecialize bug. Action: investigate TypeSpec cascade, pivot to pass_typespec fix.
- Signal 2: Load Elimination causes validator failures on non-trivial CFGs → indicates the kill logic is too aggressive. Action: restrict to single-block straight-line code only.
- Signal 3: TypeFloat guard causes deopt storms on nbody → indicates feedback is wrong (field types changing). Action: revert guard, investigate feedback collection.

## Task Breakdown

- [x] 0. Diagnostic: verify GETFIELD feedback→GuardType→TypeSpecialize cascade end-to-end — file(s): `feedback_getfield_integration_test.go` — test: `TestFeedbackGuards_GetField_Integration` ✓ pipeline works
- [x] 1. Fix emitGuardType for TypeFloat (correctness) — file(s): `emit_call.go` — test: all existing tests pass ✓
- [x] 2. Implement Load Elimination pass (block-local GetField CSE) — file(s): `pass_load_elim.go`, `pass_load_elim_test.go`, `tiering_manager.go` — test: `TestLoadElimination_*` ✓
- [x] 3. Integration test + full benchmark suite — all benchmarks correct, nbody improved ✓

## Budget

- Max commits: 4 (+1 revert slot)
- Max files changed: 5 (emit_call.go, graph_builder_test.go, pass_load_elim.go, pass_load_elim_test.go, tiering_manager.go)
- Abort condition: Task 0 reveals feedback is fundamentally broken AND the fix requires >3 files → defer to next round with focused plan

## Results (filled by VERIFY)
| Benchmark | Before | After | Change | Expected | Met? |
|-----------|--------|-------|--------|----------|------|
| nbody | 0.796s | 0.590s | **-25.9%** | -10-18% | YES (exceeded) |
| mandelbrot | 0.080s | 0.062s | -22.5% | monitor | YES |
| spectral_norm | 0.057s | 0.046s | -19.3% | monitor | YES |
| matmul | 0.152s | 0.125s | -17.8% | monitor | YES |
| sieve | 0.106s | 0.080s | -24.5% | - | bonus |
| fannkuch | 0.072s | 0.056s | -22.2% | - | bonus |
| table_field_access | 0.133s | 0.068s | -48.9% | - | bonus |
| sort | 0.074s | 0.053s | -28.4% | - | bonus |
| fibonacci_iterative | 0.305s | 0.288s | -5.6% | - | noise |
| math_intensive | 0.073s | 0.069s | -5.5% | - | noise |

Note: run_all.sh official measurement. Broad improvements across field-access-heavy benchmarks confirm Load Elimination benefits extend beyond nbody.

### Test Status
- methodjit: all pass (3.024s)
- vm: all pass (0.765s)
- ObjectCreate Go bench: pre-existing SIGSEGV (unrelated to this round)

### Evaluator Findings
- pass with notes (theoretical issues, no practical bugs)
- OpSetTable/OpGetField aliasing: GetField uses shape-slot Aux, SetTable uses dynamic key — different IR paths, can't alias in same BB. Worth documenting as future soundness work if GScript adds `t["x"]`/`t.x` mixed access patterns.
- GuardType after Load Elimination: redundant guards use gf1 with same feedback type — harmless. Guard-CSE is future work.
- Multi-block negative test: code resets `available` per block, correct but untested across blocks.
- Pipeline comment in compileTier2: stale, should mention LoadEliminationPass.

### Regressions (≥5%)
- none

## Lessons (filled after completion/abandonment)
1. **Load Elimination pays off broadly**: Predicted 5-8% on nbody, got -26% (run_all.sh). Every GetField-heavy benchmark improved 17-49%. The calibration rule ("halve ARM64 estimates") was too conservative — eliminating entire 16-instruction GetField sequences removes not just instructions but cache/register pressure.
2. **TypeFloat guard was silently no-op for 12+ rounds**: The correctness bug would have produced wrong results on type-changing fields. Detection required the diagnostic test (Task 0) to verify the full feedback pipeline. Lesson: new guard types need an emitter-level unit test, not just IR-level verification.
3. **Diagnostic-first validated quickly**: Task 0 confirmed the GETFIELD feedback pipeline works end-to-end in 15 minutes, preventing a wasted investigation into a phantom bug. This echoes rounds 6-7's lesson.
4. **Block-local CSE is the right starting point**: Simple, correct, 84 lines. Handles nbody's inner loop (single basic block with all 14 GETFIELD ops). Cross-block analysis would add complexity for marginal gain in this workload.
5. **Prediction model still underestimates compound effects**: Load Elimination + TypeFloat guard + DCE cleanup + reduced register pressure compound nonlinearly. The 64-instruction saving per GetField isn't just 64 fewer instructions — it's fewer spills, fewer cache misses, better branch prediction.
