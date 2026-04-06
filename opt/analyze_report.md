# Analyze Report — Round 14

> Date: 2026-04-06
> Cycle ID: 2026-04-06-tier1-feedback-fast-paths

## Architecture Audit

Quick read (rounds_since_arch_audit=1). Ran `scripts/arch_check.sh`:

- `emit_dispatch.go` 961 lines ⚠ — not touched this round
- `graph_builder.go` 939 lines ⚠ — not touched this round
- `emit_table.go` 872 lines ⚠ — not touched this round (Tier 2 only)
- `tier1_table.go` 545 lines — target file, will grow to ~665 lines. OK.
- Test ratio: 81% (14309 test lines / 17562 source lines)
- No new constraint violations. constraints.md is current from round 12 audit.

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| recursive_call | fib (57x), ackermann (44x), mutual_recursion (33x) | 1.87s aggregate | YES (failures=2) |
| tier2_float_loop | matmul (42.8x), spectral_norm (41.9x), nbody (19.3x), mandelbrot (6.7x) | 2.26s aggregate | No (failures=1) |
| field_access | sieve (15.5x) | 0.17s aggregate | No (failures=0) |
| allocation_heavy | binary_trees, object_creation | N/A (no LuaJIT baseline) | No (never attempted) |
| gofunction_overhead | method_dispatch (∞x — LuaJIT reports 0.000s) | 0.10s | No (never attempted) |
| other | fannkuch (3.5x), sort (4.9x), sum_primes (2x) | 0.07s aggregate | No |

## Blocked Categories

- `recursive_call` (category_failures=2): Tier 2 is net-negative for recursive functions (round 11). Needs fundamentally different mechanism (Tier 1 BLR specialization or native recursive calling convention).

## Active Initiatives

- **tier2-float-loops** (paused): Phase 3 blocked on feedback availability. B3 peephole items exhausted. This round directly unblocks Phase 3.
- **recursive-tier2-unlock** (paused, blocked): category_failures=2. Phase 5 invalidated. Phase 6 (native recursive BLR) is the next viable path.

## Selected Target

- **Category**: tier2_float_loop
- **Initiative**: opt/initiatives/tier2-float-loops.md (Phase 3 unblock)
- **Reason**: Largest aggregate gap (2.26s) in non-blocked category. The round 12 feedback-typed-loads infrastructure is complete at IR level but inert because Tier 1 doesn't collect feedback. This round closes the loop by (a) adding missing Tier 1 array fast paths and (b) adding feedback collection stubs. Cross-checked with constraints.md: no architectural ceiling applies — this is a concrete missing-implementation gap, not a fundamental limit.
- **Benchmarks**: matmul (primary — Tier 1 speedup), spectral_norm (secondary — Tier 2 typed loads), nbody (tertiary — GETFIELD feedback)

## Prior Art Research

### Web Search Findings
V8 Sparkplug always collects IC feedback during baseline execution. LuaJIT interpreter records type hints. JSC Baseline records ValueProfile per bytecode. All production engines collect feedback at the baseline tier — GScript is the outlier here.

### Reference Source Findings
Round 12's knowledge entry (`opt/knowledge/feedback-typed-loads.md`) already documents V8/SpiderMonkey/JSC approaches in detail with file:line citations. The universal pattern: baseline tier records per-PC result type → optimizing tier reads it to insert speculative guards.

### Knowledge Base Update
No new knowledge file needed — `feedback-typed-loads.md` and `array-kind-table-access.md` already cover the technique. Will update `feedback-typed-loads.md` after implementation to reflect Tier 1 collection approach.

## Source Code Findings

### Files Read
1. **tier1_table.go** (545 lines): `emitBaselineGetTable` dispatches only Mixed(0)/Int(1). Line 258-262 show the CBNZ fallthrough for anything else. SetTable same pattern at line 332-336.
2. **emit_table.go** (872 lines): Tier 2 `emitGetTableNative` already has Float/Bool fast paths (lines 422-490, round 13). Pattern to replicate.
3. **emit.go** (ExecContext struct, lines 45-129): No FeedbackPtr field exists. Struct ends with `Tier2GlobalGenPtr`/`GlobalCacheIdx`. BaselineFeedbackPtr would go after `BaselineGlobalCachedGen` (line 84).
4. **tier1_manager.go** (353 lines): `BaselineJITEngine.Execute` sets up ExecContext at lines 161-234. FeedbackPtr would be set alongside other Baseline* fields.
5. **tiering_manager.go** (769 lines): `TryCompile` calls `proto.EnsureFeedback()` only before Tier 2 compilation (line 167). Need to move this earlier.
6. **graph_builder.go** (939 lines): Lines 622-657 already insert OpGuardType from feedback. `feedbackToIRType` (line 925) maps FBFloat→TypeFloat, FBInt→TypeInt. Ready to consume feedback.
7. **feedback.go** (79 lines): TypeFeedback is 3 bytes (Left/Right/Result, each uint8). FeedbackType lattice: Unobserved→concrete→Any.
8. **func_profile.go** (142 lines): `shouldPromoteTier2` gates: pure-compute+loop→callCount≥2, loop+table→callCount≥3.

### Diagnostic Data

**spectral_norm inner loop**: `v[j]` is GETTABLE on float array (ArrayFloat), `av[i] = sum` is SETTABLE on float array. With Tier 1 Float fast path, these become native. With feedback, Tier 2 inserts GuardType(float) → TypeSpecialize turns `Mul(any,any)` → `MulFloat(float,float)`.

**nbody inner loop**: `bodies[i]` is GETTABLE on Mixed array (table pointers). Then `bi.x`, `bi.y`, `bi.z` are GETFIELD returning float. GETTABLE feedback for bodies[i] would be FBTable. GETFIELD feedback for bi.x would be FBFloat — this is what enables typed arithmetic.

**matmul inner loop**: `a[i][k]` and `b[k][j]` are double-GETTABLE: first access (row lookup) is on Mixed array (returns table), second access (element) is on Float array (returns float). Currently at Tier 1: the second GETTABLE exits to Go on every inner iteration (ArrayFloat not handled).

### Actual Bottleneck (data-backed)

matmul: each inner iteration does 2 GETTABLE on float arrays (a_row[k], b_row[j]), each going through exit-resume at ~100-200ns. 27M inner iterations × 2 exit-resumes × ~150ns = ~8s of overhead. But matmul only takes 0.985s, suggesting either the inner loop count is smaller or exit-resume is faster (~35ns). Either way, eliminating exit-resume for float arrays removes the dominant per-iteration overhead.

spectral_norm/nbody: these reach Tier 2, but the optimized code has `Mul(any, any)` because GETTABLE/GETFIELD results are untyped. Each generic Mul dispatches on type at runtime (~15-20 insns). With feedback→GuardType→MulFloat, the dispatch is eliminated (~3-4 insns).

## Plan Summary

Extend Tier 1's GETTABLE/SETTABLE with ArrayFloat/ArrayBool native fast paths (eliminating exit-resume) and add inline feedback collection stubs that record result types into FeedbackVector during Tier 1 execution. This closes the feedback pipeline: Tier 1 collects → Tier 2 reads → GuardType inserts → TypeSpecialize cascades. Expected -5-15% aggregate improvement, with matmul seeing the largest immediate gain (-15-25% from exit-resume elimination). Primary value is foundational: enables all future GetTable/GetField type specialization work.
