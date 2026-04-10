# Architecture Constraints & Notes

> **ANALYZE reads this every round.** Updated by Architecture Audit (every 2 rounds).
> Last full audit: Round 24 (2026-04-10)

## Tier Constraints

- **Tier 2 is net-negative for recursive functions** (Round 11): SSA construction + type guards + BLR ~15-20ns overhead > inlining gains. Recursive speedup needs Tier 1 BLR specialization or native recursive calling convention, NOT Tier 2 promotion.
- **Tier 1 has no forward type tracking** (Round 24 audit): every arith/compare template re-runs full int/float tag dispatch from scratch. `emitBaselineArith` is ~22 insns (10 dispatch + 12 int-path), `emitBaselineEQ` is ~35 insns (polymorphic compare). There is no intra-function SSA-like tracking of "slot X is known int," so even in obviously-int functions (ack, fib, mutual_recursion, fibonacci_iterative) every op pays the dispatch toll. This is the #1 Tier 1 bottleneck for int-arith-heavy code.
- **Tier 1 slot-file memory round-trip** (Round 24 diagnostic on ackermann): ~40% of hot-path instructions are LDR/STR against the NaN-boxed VM register file. Slots 0-1 are the only ones pinned (X22=R(0), X21=self closure). Pinning more hot params (R(1)..R(3)) into X20/X23/X28 is untapped.
- **8-FPR pool is a hard limit** (D4-D11): carried invariants + body temps share 8 registers. >5 carried invariants squeezes body temp space. Round 9's LICM-carry reserves up to 5, leaving 3 for body.
- **4-GPR pool** (X20-X23): int counter carry + loop bounds use 2-3, leaving 1-2 for body temps.

## Module Boundaries

- **`emit_table.go` SPLIT** (Round 19): Now `emit_table_field.go` (341 lines) + `emit_table_array.go` (692 lines). ✅ RESOLVED.
- **`emit_dispatch.go` 971 lines** ⚠ CRITICAL: 29 lines from limit. Unchanged since R21 (R22-R23 did not touch). Must still split before any dispatch-touching change — extract `emit_branch.go` for fused compare+branch.
- **`graph_builder.go` 955 lines** ⚠ CRITICAL: 45 lines from limit. Unchanged since R21 (R22-R23 did not touch — float param guard landed in a helper). Must split before next change — extract `graph_builder_feedback.go`.
- **`tier1_table.go` 829 lines** ⚠ Unchanged since R21.
- **`tier1_arith.go` 728 lines** — not at limit but already >700. Round 24's int-specialization will add templates here. Watch this file for growth past 800 next round.
- **`regalloc.go` ↔ `emit_loop.go` coupling**: `carried` map concept spans both files. Unchanged — invariant-carry infra from R9 is stable.
- **27 source files lack test files** (same count as Round 21 — no new test files added for `emit_call_exit.go`, `loops.go`, Tier 1 handlers). Test/source ratio is 88% (up from 86% at Round 19) — total lines grew faster in tests than source.

## Pass Pipeline Order

Current (from `compileTier2`):
```
BuildGraph → Validate → TypeSpec → Intrinsic → TypeSpec → Inline → TypeSpec → ConstProp → LoadElim → DCE → RangeAnalysis → LICM → Validate → RegAlloc → Emit
```

Ordering constraints:
- LICM AFTER RangeAnalysis (depends on `Int48Safe` flag)
- Inline AFTER TypeSpecialize (needs type info for inline decisions)
- ConstProp BEFORE DCE (fold first, then remove dead)
- RegAlloc is LAST before Emit (must see final CFG after all transforms)

## Known Performance Ceilings

- `recursive_call` (ceiling=2): needs fundamentally different mechanism (see Tier Constraints)
- `allocation_heavy` (binary_trees, object_creation): NEWTABLE is exit-resume, can't inline allocation; needs escape analysis + scalar replacement
- mandelbrot inner loop: FMUL/FADD dependency chain is the compute floor (~3ns/iter). All peephole opportunities exhausted (Rounds 7-10). Further gains need loop unrolling or software pipelining.
- **matmul Tier 2 via OSR** (Round 15, RESOLVED): Was stuck at Tier 1 (called once, threshold=2). Fixed by re-enabling OSR with LoopDepth >= 2 gate (commit 056607b). matmul now reaches Tier 2, mandelbrot -80%, spectral_norm -64%. Remaining gap: inner loop still uses generic GetTable dispatch — feedback-typed loads (Phase 3 infra from round 12) could further specialize once Tier 1 feedback collection covers GETTABLE mixed-array path.
- pprof is useless for JIT code (79% shows as opaque `runtime._ExternalCode`). ARM64 disasm via `tier2_float_profile_test.go` is the only reliable profiling method.

## Feedback System

- `FeedbackVector` records per-PC result types. Round 12 added `OpGuardType` insertion in graph builder after `GetTable`/`GetField` when feedback says monomorphic.
- **Round 14**: Tier 1 now collects feedback via ARM64 stubs in GETTABLE typed-array fast paths (Float→FBFloat, Int→FBInt, Bool→FBBool) and GETFIELD (runtime value type extraction). `BaselineFeedbackPtr` in ExecContext points to `proto.Feedback[0]`.
- **Coverage gap**: GETTABLE mixed-array path does NOT record feedback (line 279 of tier1_table.go has no stub). Mixed-array accesses returning tables or mixed values get FBUnobserved. This is acceptable for now — the important case is typed arrays returning floats/ints.
- **End-to-end pipeline verified** (TestFeedbackGuards_Integration): feedback → GuardType → TypeSpecialize → MulFloat/AddFloat cascade works.
- **Tier-up timing**: threshold=2 for pure-compute functions. First call at Tier 1 collects feedback. Second call at Tier 2 uses feedback. Functions called only once (matmul) need OSR to benefit.
- NOT covered: Call return type, ForLoop counter type.

## Feedback Pipeline

- **GETFIELD/GETTABLE feedback cold-start gap** (Round 17, **FIXED**): Go exit handlers now record type feedback via `proto.Feedback[pc].Result.Observe()`. Both `handleGetField` and `handleGetTable` in `tier1_handlers.go` record feedback on the slow path. End-to-end pipeline verified in production: feedback → GuardType → TypeSpecialize → MulFloat/AddFloat.
- **Shape guard deduplication** (Round 17): `emitContext.shapeVerified` tracks per-block shape-verified table SSA values. Subsequent GetField/SetField on same table with same shapeID skip type+nil+shape check (~11 insns saved). Invalidated by OpCall, OpSelf, OpSetTable, and block boundaries.
- **Remaining feedback gap**: GETTABLE mixed-array path does NOT record feedback (line 279 of tier1_table.go). Mixed-array accesses returning tables or mixed values get FBUnobserved. Acceptable — typed arrays are the important case.

## Table Access Overhead (Round 19 audit)

- **GetTable/SetTable per-access: ~35 ARM64 insns** (from diagnostic on sieve). Only 1-2 are the actual load/store. Overhead: table type check (10), nil/metatable check (3), key validation (6), array kind dispatch (8), bounds check + base load (4), dirty flag (3).
- **tableVerified dedup** (Round 19, commit 4202fac): `emitContext.tableVerified` now tracks per-block validated table SSA values for GetTable/SetTable. Subsequent accesses on same table skip type+nil+metatable check. Invalidated by OpCall, OpSelf, OpSetField, and block boundaries.
- **Array kind feedback** (Round 19, commit c7d0b76): Tier 1 GETTABLE/SETTABLE now records array kind (Mixed/Int/Float/Bool) in feedback. Tier 2 emits kind-specialized fast paths when feedback is monomorphic.
- **Kind specialization limited impact** (Round 19 lesson): Sieve unchanged — branch predictor on M4 makes predictable 4-way dispatch cascade free. Secondary benchmarks (fannkuch -10%, table_array -6%) showed modest gains.
- **No cross-block table validation** — even for loop-invariant tables, full validation runs every iteration. V8 hoists CheckMaps to loop preheaders; GScript does not.
- **Diagnostic test pipeline mismatch**: `tier2_float_profile_test.go:profileTier2Func` uses a simplified pipeline (no Intrinsic, Inline, LoadElim, RangeAnalysis, LICM, no feedback). Diagnostics from this test do NOT reflect production codegen. Use `Diagnose()` or TieringManager for production-accurate data.

## Tier 1 Self-Call Optimization (Round 20)

- **Self-call detection**: For each CALL bytecode, Tier 1 compares callee proto address with a compile-time constant of the caller proto. If match, uses `BL self_call_entry` (PC-relative direct branch) instead of `BLR X2` (indirect). Lighter 32-byte frame save/restore instead of 64-byte.
- **CallCount increment restored** (commit b094383): Self-call path now increments proto.CallCount to enable Tier 2 promotion for recursive functions.
- **ackermann +137% regression**: Ackermann calls `ack` via GetGlobal("ack") + CALL ~67M times. Self-call path adds LoadImm64 (2-3 insns) for proto comparison to EVERY call. Net per-call overhead estimated at 4-5 insns × 67M = 268-335M extra instructions. The Tier 1 GetGlobal cache also contributes ~10 insns × 2 GetGlobals × 67M = 1.34B insns. Combined overhead accounts for the regression.
- **Fix options**: (a) Skip GetGlobal generation check for modules without SetGlobal, (b) Hoist GetGlobal("ack") to function prologue (only load once), (c) Add Tier 1 "known-callee" optimization that caches the last resolved function value per CALL site.

## Technical Debt

- `benchmarks/run_all.sh` has a bug: VM/JIT suite benchmarks silently fail (discovered round 12). Individual benchmark runs work.
- `tier2_float_profile_test.go:profileTier2Func` uses stale simplified pipeline — does not match `compileTier2()` (missing 6 passes + feedback). Diagnostic data is misleading for type-specialized analysis.

## Test Coverage Notes

- 86% test-to-source ratio (15,834 test lines / 18,466 source lines) — up from 85% at Round 19
- 27 source files have no corresponding test file (mostly Tier 1 handlers and emit files)
- Key gap: `loops.go` (loop infrastructure) has no dedicated tests — tested indirectly via `pass_licm_test.go`
- New: `emit_call_exit.go` added (Round 20, GetGlobal native) without test file
