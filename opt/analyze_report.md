# Analyze Report — Round 17

> Date: 2026-04-06
> Cycle ID: 2026-04-06-getfield-feedback-fix

## Architecture Audit

**FULL AUDIT** (rounds_since_arch_audit = 2).

### File sizes
| File | Lines | Status |
|------|-------|--------|
| emit_dispatch.go | 961 | ⚠ SPLIT (unchanged since R15) |
| graph_builder.go | 939 | ⚠ SPLIT (unchanged since R15) |
| emit_table.go | 937 | ⚠ SPLIT (unchanged since R15) |
| tiering_manager.go | 775 | OK |
| tier1_table.go | 774 | OK |
| pass_inline.go | 726 | OK |

### Key findings
1. **Source: 44 files, 17979 lines. Tests: 15130 lines. Ratio: 84%** (up from 81% at R12 audit).
2. **25 source files lack dedicated test files** (same as R15 audit — no change).
3. **Technical debt markers: 1** (down from historical). Codebase is clean.
4. **New finding**: GETFIELD feedback pipeline has a cold-start gap. Go exit handler (`handleGetField`) does NOT record type feedback into `proto.Feedback`. This means ALL benchmarks that reach Tier 2 via call-count threshold (not OSR) have empty GETFIELD feedback. nbody's advance() has generic arithmetic despite the full feedback infrastructure being in place.
5. **Pipeline order unchanged** since R15: BuildGraph → Validate �� TypeSpec → Intrinsic → TypeSpec �� Inline → TypeSpec → ConstProp → LoadElim ��� DCE → RangeAnalysis → LICM → Validate → RegAlloc → Emit
6. **No new infrastructure since R15** (no new files, passes, or data structures added).

### Diagnostic data (nbody advance() inner j-loop)
- **1887 ARM64 instructions** per j-iteration
- **27 float arithmetic ops** (1.4% compute)
- **20 GetField/SetField** with inline shape guards (320 insns, 17%)
- **80 lsr#48 arithmetic type checks** (~240 insns, 12.7%) — caused by GETFIELD results being TypeUnknown
- **22 table-exit stubs** (~550 insns of slow-path code)

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| recursive_call | fib (56x), ackermann (42x), mutual_recursion (37.8x) | ~1.8s total | YES (ceiling=2) |
| tier2_float_loop | nbody (17.4x), spectral_norm (6.6x), matmul (6.0x), fannkuch (2.7x), mandelbrot (1.22x) | ~0.75s total | no |
| field_access | sieve (8x), sort (5.3x), sum_primes (2x) | ~0.11s total | no |
| allocation_heavy | binary_trees (regression), object_creation (regression) | N/A (no LuaJIT baseline) | no (but known architectural wall — NEWTABLE exit-resume) |
| gofunction_overhead | method_dispatch (regression) | unknown | no |

## Blocked Categories
- `recursive_call` (ceiling=2): Tier 2 net-negative for recursive functions (Round 11). Needs native recursive BLR or Tier 1 specialization.

## Active Initiatives
- `tier2-float-loops.md` (paused): Phases 1-8 complete. Remaining: Phase 6 (range analysis for floats), Phase 9 (shape check hoisting), Phase 10 (store-to-load forwarding).
- `recursive-tier2-unlock.md` (paused, blocked by ceiling rule)

## Selected Target
- **Category**: tier2_float_loop
- **Initiative**: opt/initiatives/tier2-float-loops.md (Phase 9 prerequisite)
- **Reason**: nbody has the largest absolute gap (0.556s) in any non-blocked category. Root cause identified: GETFIELD feedback cold-start gap causes 240 insns/iter of unnecessary type dispatch overhead (12.7% of inner loop). Fix is bounded (~10 lines in Go handler). Shape guard dedup adds another ~162 insns/iter savings. Combined: ~400 insns/iter from 1887 = 21% instruction reduction.
- **Benchmarks**: nbody (primary), matmul/spectral_norm (secondary)

## Prior Art Research

### Web Search Findings
No new search needed — prior art well-documented in knowledge base from Rounds 6, 12, 16.

### Reference Source Findings
V8's feedback pipeline records type info in BOTH interpreter and Sparkplug baseline paths. SpiderMonkey's CacheIR records in both IC hit and miss paths. **All production engines record feedback on all code paths.** GScript's gap is unique: ARM64 fast path records feedback, Go slow path doesn't.

### Knowledge Base Update
No new knowledge file needed. Existing `opt/knowledge/feedback-typed-loads.md` already documents the full pipeline. The GAP (Go handler not recording) is captured in constraints.md.

## Source Code Findings

### Files Read
- `tier1_handlers.go:477-504` (handleGetField): Populates FieldCache but does NOT record feedback.
- `tier1_table.go:100-149` (emitBaselineGetField ARM64): Records feedback via `emitBaselineFeedbackResultFromValue` on inline cache HIT only.
- `graph_builder.go:655-660`: Correctly reads `proto.Feedback[pc].Result` and inserts `OpGuardType` when feedback is monomorphic.
- `tiering_manager.go:148-149, 172-173`: `EnsureFeedback()` called correctly before Tier 1 and Tier 2 compilation.
- `emit_call.go:42-89` (emitGuardType): TypeFloat guard correctly implemented (fixed in R16).
- `feedback_getfield_integration_test.go`: Passes because it uses VM interpreter to collect feedback, bypassing the Go exit handler gap.

### Diagnostic Data
See Architecture Audit section above. Key number: **80 lsr#48 type dispatch checks** in nbody's j-loop that would be eliminated by feedback-driven type specialization.

### Actual Bottleneck (data-backed)
nbody's inner j-loop: 1887 insns/iter, 27 float ops (1.4% compute).
- **~240 insns** (12.7%): arithmetic type dispatch (lsr#48 + cmp + branch per operand) — caused by GETFIELD returning TypeUnknown
- **~320 insns** (17%): GetField/SetField shape guards + type checks — 20 field ops × 16 insns each, 18 of which are redundant (same table, same shape)
- Fix Task 1 eliminates the first category. Fix Task 2 eliminates most of the second.

## Plan Summary

Fix the GETFIELD feedback cold-start gap by recording type feedback in the Go exit handler (`handleGetField`). This is a ~10-line change that unlocks the entire feedback → GuardType → TypeSpecialize cascade for all GETFIELD-heavy benchmarks. Combined with emitter-level shape guard deduplication (skip redundant type+shape checks on same table within a block), this eliminates ~400 of 1887 insns per nbody inner-loop iteration. Conservative estimate: nbody −10-13% (0.590s → ~0.51s), with compound effects on matmul/spectral_norm.
