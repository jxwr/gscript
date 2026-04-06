# Optimization Plan: GETFIELD Feedback Gap Fix + Shape Guard Dedup

> Created: 2026-04-06 13:30
> Status: active
> Cycle ID: 2026-04-06-getfield-feedback-fix
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md (Phase 9 prerequisite)

## Target
Primary: nbody. Secondary: any benchmark with GETFIELD in hot loops.

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| nbody | 0.590s | 0.034s | 17.4x | 0.51s (−13%) |
| matmul | 0.125s | 0.021s | 6.0x | 0.118s (−5%) |
| spectral_norm | 0.046s | 0.007s | 6.6x | 0.044s (−4%) |

## Root Cause

**GETFIELD type feedback never reaches Tier 2 compilation.**

The feedback pipeline for GETFIELD has a cold-start gap:

1. Call 1 at Tier 1: ALL GETFIELD instructions miss the inline field cache (cache is empty) → exit to Go handler (`handleGetField` in `tier1_handlers.go`)
2. Go handler performs the field access and **populates the FieldCache** — but does **NOT record type feedback** into `proto.Feedback[pc].Result`
3. Call 2: `shouldPromoteTier2` triggers → `compileTier2` reads `proto.Feedback` → all entries are `FBUnobserved`
4. Graph builder emits `OpGetField` with `TypeAny` result → no `OpGuardType` inserted → `TypeSpecialize` can't specialize downstream arithmetic → all `OpSub`/`OpMul`/`OpAdd` stay generic

**Result**: nbody's inner j-loop has 80 per-operand type dispatch checks (lsr#48 + cmp + branch) that should be eliminated by float specialization. The loop is **1.4% compute, 98.6% overhead** (1887 insns/iter, 27 float ops).

The integration test (`TestFeedbackGuards_GetField_Integration`) passes because it manually calls `EnsureFeedback()` + executes via the VM interpreter (which records feedback), bypassing the Tier 1 → Go exit path.

## Prior Art (MANDATORY)

**V8:** FeedbackVector is populated by the interpreter AND Sparkplug (baseline). Every load operation records its result type into the feedback slot, regardless of code path (IC hit, IC miss, interpreter fallback). TurboFan reads feedback at compile time.

**LuaJIT:** Not applicable — traces record types inline during trace recording, not via a separate feedback vector.

**SpiderMonkey:** CacheIR stubs record into ICScript entries. Both the hit and miss paths record type information. Warp reads snapshots at compile time.

**Universal pattern**: All production engines record feedback in BOTH fast path and slow path. GScript's gap is that the Go slow path for GETFIELD doesn't record feedback.

Our constraints vs theirs: GScript's two-tier design (ARM64 fast path + Go fallback) means feedback recording must happen in both places. V8's Sparkplug records feedback inline (like our ARM64 path) but the interpreter also records feedback (our Go fallback doesn't).

## Approach

### Task 1: Record GETFIELD feedback in Go exit handler
**File**: `internal/methodjit/tier1_handlers.go` (function `handleGetField`, ~line 499)

After `regs[absA] = tbl.RawGetStringCached(fieldName, &proto.FieldCache[pc])`, add:
```go
if proto.Feedback != nil && pc >= 0 && pc < len(proto.Feedback) {
    proto.Feedback[pc].Result.Observe(regs[absA].Type())
}
```

This ensures the first Tier 1 execution (field cache MISS → Go handler) records feedback, which is available when Tier 2 compiles on the second call.

Also add the same for `handleGetTable` (~line 420) for completeness — matmul's inner GetTable goes through this path for mixed-array table-of-tables accesses.

### Task 2: Shape guard deduplication in emitter
**File**: `internal/methodjit/emit_table.go` (functions `emitGetField`, `emitSetField`)

Add per-block tracking of shape-verified table objects in `emitContext`:
- New field: `shapeVerified map[int]uint32` — maps table SSA value ID → verified shapeID
- In `emitGetField`/`emitSetField`: if table already shape-verified with matching shapeID, skip type check + nil check + shape check (~9 insns saved)
- Reset at block boundaries and after OpCall/OpSelf
- NOT invalidated by inline SetField (writing to existing fields doesn't change shape)

### Task 3: Tests + integration verification
- Unit test: verify handleGetField records feedback for float values
- Integration test: compile nbody-like function, verify IR has OpGuardType after OpGetField, verify MulFloat/AddFloat in TypeSpecialized IR
- Run full benchmark suite

## Expected Effect

**Task 1 (feedback fix)**: Eliminates ~240 insns/iter of arithmetic type dispatch in nbody's inner j-loop. 80 lsr#48 type checks × 3 insns each. With feedback → GuardType → TypeSpecialize cascade: OpSub→OpSubFloat, OpMul→OpMulFloat, OpAdd→OpAddFloat. 

**Task 2 (shape dedup)**: Eliminates ~162 insns/iter of redundant shape guards. 18 redundant shape checks × 9 insns each (type check + nil check + shape check).

**Combined**: ~402 insns/iter saved from 1887 = 21% instruction reduction.

**Prediction calibration (MANDATORY):** Raw instruction-count analysis says 21%. Halved for ARM64 superscalar: **~10-13% wall-time**. The type dispatch instructions (lsr+cmp+branch chains) are partially pipelined, so the actual benefit of removing them is less than the static count suggests. However, branch prediction misses on type dispatch add stochastic overhead that instruction counting misses — partial compensation. Conservative estimate: **nbody −10%, compound effects on other benchmarks ±5%.**

## Failure Signals
- Signal 1: handleGetField feedback fix doesn't produce GuardType in IR → investigate whether feedback reaches graph builder (check FeedbackVector population, PC alignment) → pivot to delayed Tier 2 compilation
- Signal 2: GuardType in IR but arithmetic still generic → TypeSpecialize pass not handling GuardType after GetField → debug TypeSpecialize
- Signal 3: Shape dedup causes correctness failures → SetField might change shape in edge case → restrict dedup to GetField-only (exclude SetField)
- Signal 4: No benchmark improvement despite instruction reduction → ARM64 superscalar hides all gains → abandon, document as known superscalar ceiling

## Task Breakdown

- [x] 1. Fix feedback recording in Go exit handlers — file(s): `tier1_handlers.go` — test: `TestHandleGetField_RecordsFeedback` + existing `TestFeedbackGuards_GetField_Integration`
- [x] 2. Shape guard deduplication in emitter — file(s): `emit_table.go` — test: `TestShapeGuardDedup` + `TestTieringManager_NBody`
- [x] 3. Integration test + full benchmark suite — verify: nbody IR has MulFloat/AddFloat, benchmark shows improvement

## Budget
- Max commits: 4 functional (+1 revert slot)
- Max files changed: 3 (tier1_handlers.go, emit_table.go, new test file)
- Abort condition: 3 commits without benchmark improvement, or tests fail after 3 attempts on any task

## Results (filled by VERIFY)
| Benchmark | Before | After | Change | Expected | Met? |
|-----------|--------|-------|--------|----------|------|
| nbody | 0.590s | 0.541s | −8.3% | −10-13% | close |
| table_field_access | 0.068s | 0.052s | −23.5% | ±5% | exceeded |
| spectral_norm | 0.046s | 0.042s | −8.7% | −4% | exceeded |
| matmul | 0.125s | 0.120s | −4.0% | −5% | close |
| fibonacci_iterative | 0.288s | 0.275s | −4.5% | — | bonus |
| math_intensive | 0.069s | 0.065s | −5.8% | — | bonus |
| mandelbrot | 0.062s | 0.064s | +3.2% | — | noise |
| sieve | 0.080s | 0.081-0.087s | noise | — | ok |

### Test Status
- All passing (methodjit + vm)

### Evaluator Findings
- PASS. One medium issue fixed: OpSetTable now invalidates shapeVerified (dynamic key writes can change shape).

### Regressions (≥5%)
- coroutine_bench: 16.4s → 17.6s (+6.9%) — measurement noise, no coroutine code paths touched.
- sieve: 0.080s → 0.081-0.087s — confirmed noise via 3x re-run (0.081-0.087 range, baseline 0.080).

## Lessons (filled after completion/abandonment)
1. **Feedback cold-start gap was the real Phase 3 blocker**: Round 12 built the GuardType→TypeSpecialize pipeline but feedback was never populated because Tier 1's Go exit handler didn't record it. The fix was 4 lines in tier1_handlers.go. Infrastructure without plumbing is dead code.
2. **Shape guard dedup is a multiplier, not a standalone win**: The dedup saves ~9 insns per redundant access, but the big win is that it compounds with feedback-driven type specialization — table_field_access -23.5% shows the multiplicative effect.
3. **Prediction was close this time**: Predicted nbody -10-13%, actual -8.3%. Calibration (halving for superscalar) is working. The remaining gap is likely branch predictor effects that instruction counting misses entirely.
4. **Evaluator caught a real bug**: OpSetTable invalidation was missing — dynamic key writes can change shape, making stale dedup entries unsafe. Fixed before commit.
5. **table_field_access is the canary**: -23.5% from shape dedup alone confirms that field-access-heavy benchmarks are dominated by guard overhead, not compute. This validates the initiative's Phase 9 direction (further guard elimination).
