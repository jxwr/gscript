# Analyze Report — Round 16

> Date: 2026-04-06
> Cycle ID: 2026-04-06-nbody-load-elim

## Architecture Audit

Quick read (rounds_since_arch_audit=1). `scripts/arch_check.sh` results:

- ⚠ `emit_dispatch.go` 961 lines — approaching 1000-line limit
- ⚠ `graph_builder.go` 939 lines — approaching limit
- ⚠ `emit_table.go` 937 lines — approaching limit (grew in rounds 13-14)
- 25 source files lack test files (unchanged from last audit)
- 1 tech debt marker (cosmetic comment in emit_call.go)
- Test ratio: 82% (14716 test / 17879 source)

No new architectural issues. constraints.md current. This round's changes (new pass file + emit_call.go edits) won't push any file over the limit.

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| recursive_call | fib (1.43s), ackermann (0.32s), mutual_recursion (0.29s) | 2.04s | **YES** (failures=2) |
| tier2_float_loop | nbody (0.76s), spectral_norm (0.049s), mandelbrot (0.017s) | 0.83s | No (failures=1) |
| field_access | sieve (0.094s), matmul (0.128s), sort (0.063s), fannkuch (0.050s), sum_primes (0.004s) | 0.34s | No (failures=0) |
| allocation_heavy | binary_trees (1.59s regression), object_creation (0.17s regression) | 1.76s regression | No (needs escape analysis — arch_refactor) |
| gofunction_overhead | method_dispatch (0.076s regression vs VM) | 0.17s vs LuaJIT | No (failures=0) |

## Blocked Categories

- `recursive_call` (category_failures=2): Tier 2 is net-negative for recursive functions (SSA overhead > benefit). Needs fundamentally different mechanism.

## Active Initiatives

- **tier2-float-loops** (paused): Phases 1-4 complete, Phase 5 partially resolved, Phase 6 deferred. Resuming for nbody.
- **recursive-tier2-unlock** (paused, blocked by ceiling rule)

## Selected Target

- **Category**: tier2_float_loop
- **Initiative**: opt/initiatives/tier2-float-loops.md (resuming as Phase 8)
- **Reason**: nbody is the single largest non-blocked benchmark gap (0.76s wall-time gap, 22.1x from LuaJIT). The tier2_float_loop category has failures=1 (not at ceiling). nbody's inner loop is dominated by GETFIELD overhead (~64% of instruction count), with redundant field loads (bj.mass×3, bi.mass×3) and a correctness bug in emitGuardType for TypeFloat. Load Elimination is the natural next step in the initiative's "new direction" (memory traffic reduction).
- **Benchmarks**: nbody (primary), spectral_norm + matmul (secondary)

## Prior Art Research

### Web Search Findings

- **V8 TurboFan LoadElimination** (`src/compiler/load-elimination.cc`): Tracks abstract state — map of known field values per object. Eliminates redundant LoadField on same object+offset with no intervening store. CSEs CheckMaps on same object.
- **LuaJIT alias analysis** (`lj_opt_mem.c`): Load forwarding for HREF/HLOAD on same table+key.
- **JSC DFG CSEPhase**: Hashes GetByOffset by object+offset for block-local CSE.
- **Universal pattern**: All mature compilers eliminate redundant field loads within basic blocks.

### Reference Source Findings

V8's approach is most applicable: block-local abstract state tracking, killed by aliasing stores. GScript's shapeID-guarded inline caches are analogous to V8's CheckMaps.

### Knowledge Base Update

New: `opt/knowledge/load-elimination-field-cse.md` — technique details and cross-engine comparison.

## Source Code Findings

### Files Read

- `graph_builder.go:639-661` — GETFIELD feedback→GuardType insertion: correctly implemented
- `emit_call.go:40-74` — **BUG**: `emitGuardType` only handles TypeInt. TypeFloat falls to default (no-op pass-through). No runtime enforcement for float guards.
- `tier1_table.go:79-150` — Tier 1 GETFIELD feedback collection: correctly wired
- `tier1_table.go:710-774` — Feedback from-value extraction: monotonic update, correct offsets
- `tier1_manager.go:230-235` — BaselineFeedbackPtr set before Tier 1 execution in Execute loop
- `pass_typespec.go:319-365` — specialize(): OpMul→OpMulFloat when both args TypeFloat ✓
- `pass_typespec.go:119-120` — OpGuardType returns Type(instr.Aux) ✓
- `pass_intrinsic.go:11-72` — math.sqrt→OpSqrt: correctly intrinsified ✓
- `benchmarks/suite/nbody.gs` — advance() inner loop: 14+ GETFIELD, 6 SETFIELD, ~20 float ops, 1 sqrt per iteration

### Diagnostic Data

Source-level cost estimate for nbody advance() inner loop (per j-iteration):

| Category | Instructions/iter | % of total |
|----------|-------------------|------------|
| GETFIELD (14× ~16 insns: type check, shape guard, field load, NaN-box store) | ~224 | 45% |
| SETFIELD (6× ~16 insns: type check, shape guard, field store) | ~96 | 19% |
| Float arithmetic (20× ~6 insns: load, FMOV→FPR, compute, FMOV→GPR, store) | ~120 | 24% |
| GETTABLE bodies[i/j] (2× ~20 insns) | ~40 | 8% |
| Loop overhead + OpSqrt | ~20 | 4% |
| **Total** | **~500** | 100% |

Estimated: ~150ns/iter × 10 inner iters = 1.5μs/call × 500K calls = 0.75s (matches observed 0.796s).

LuaJIT: ~60 insns/iter (hoisted shape checks, FPR-resident values, no NaN-boxing) → 18ns/iter → 0.036s.

### Actual Bottleneck (data-backed)

**Field access overhead (GETFIELD+SETFIELD) is 64% of instruction count.** The per-access shape check and NaN-box load/store pattern dominates. Of the 14+ GETFIELD per iteration, 4 are redundant (bj.mass×3→1, bi.mass×3→1). Load Elimination saves ~64 insns (~13%).

Additionally, `emitGuardType` for TypeFloat is a no-op (correctness bug). Fixing it adds ~4 insns per guard but prevents silent wrong results if field types change.

## Plan Summary

Diagnostic-first round targeting nbody (22.1x from LuaJIT). Task 0 verifies the feedback→GuardType→TypeSpecialize cascade works end-to-end for GETFIELD (never tested — only GETTABLE has integration test). Task 1 fixes the `emitGuardType` TypeFloat no-op (correctness). Task 2 implements a Load Elimination pass that CSEs redundant GetField(same_obj, same_field) within basic blocks. Expected net: nbody −10-15% (halved for superscalar: ~8%), with larger gains if Task 0 reveals feedback is broken.
