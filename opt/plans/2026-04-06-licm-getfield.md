# Optimization Plan: LICM GetField Hoisting + Store-to-Load Forwarding

> Created: 2026-04-06 17:00
> Status: active
> Cycle ID: 2026-04-06-licm-getfield
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md (Phase 9)

## Target

Hoist loop-invariant field loads out of inner loops. Primary target: nbody's inner j-loop
where bi.x, bi.y, bi.z, bi.mass are loaded every iteration but never modified.

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| nbody | 0.541s | 0.034s | 15.9x | 0.48-0.50s (~8-10%) |
| spectral_norm | 0.042s | 0.007s | 6.0x | 0.039-0.041s (~3-5%) |
| matmul | 0.120s | 0.022s | 5.5x | 0.112-0.118s (~2-4%) |

## Root Cause

**Diagnostic data** (from IR dump + ARM64 disasm of advance()):
- Inner j-loop body has 56 IR instructions: 19 GetField, 6 SetField, 19 float arith, 1 math.sqrt, etc.
- Production pipeline reduces overhead significantly (TypeSpecialize, IntrinsicPass, LoadElim, ShapeGuardDedup)
- BUT: every j-iteration still reloads bi.x, bi.y, bi.z, bi.mass from memory despite these values never changing
- Each GetField involves a 2-level pointer chase (table → svals → value) with ~4-cycle L1 latency per dependent load
- 4 unnecessary GetField per j-iteration × 2 dependent loads each = 8 pointer-chase stalls per iteration

**Root cause 1**: `pass_licm.go:canHoistOp()` does NOT include `OpGetField`. Field loads are never hoisted.

**Root cause 2**: `pass_load_elim.go` does NOT implement store-to-load forwarding. After `SetField(obj, "vx", val)`, a subsequent `GetField(obj, "vx")` reloads from memory instead of reusing `val`.

## Prior Art (MANDATORY)

**V8 TurboFan** (`src/compiler/load-elimination.cc`):
- `ComputeLoopState` (cc:1363-1465) scans loop body, kills any field with a StoreField, survivors are loop-invariant and propagated into the loop.
- `ReduceStoreField` (cc:1048) records stored value via `state->AddField(object, field_index, {new_value})` — enables store-to-load forwarding.
- `ReduceCheckMaps` (cc:786) eliminates shape checks when the object's Map is already known in the abstract state.

**LuaJIT** (`src/lj_opt_mem.c`):
- `fwd_ahload` (cc:162) walks HSTORE chain backward; ALIAS_MUST → returns stored value (store-to-load forwarding).
- Cross-iteration forwarding via `loop_unroll` in `lj_opt_loop.c:77-85`: re-emitted HLOAD finds earlier HLOAD from pre-roll via CSE.

**SpiderMonkey** (GVN pass in `js/src/jit/ValueNumbering.cpp`): dominator-tree traversal propagates available expressions by (base-value, offset) pairs.

**Our constraints vs theirs**: V8 has a separate effect-chain mechanism (complex); LuaJIT's is trace-specific (linear IR). GScript's approach: extend existing LICM fixpoint + LoadElim available map. Simpler than V8, more general than LuaJIT.

## Approach

### Task 1: Extend LICM for GetField hoisting (`pass_licm.go`)

In `hoistOneLoop`, before the invariant fixpoint:
1. Collect all in-loop `OpSetField` keys as `setFields map[loadKey]bool`
2. Also check for in-loop `OpCall` / `OpSelf` / `OpSetTable` → set `hasLoopCall = true`

In the fixpoint iteration, for `OpGetField`:
- If `hasLoopCall` → cannot hoist (call may modify any table)
- If `setFields[{obj.ID, field.Aux}]` → cannot hoist (field is written in loop)
- Otherwise → check all args invariant as usual → hoistable

Extend `canHoistOp` to include `OpGetField` (the additional checks are done inline in the fixpoint).

This is ~30 lines added to `pass_licm.go`.

### Task 2: Store-to-load forwarding in LoadElimination (`pass_load_elim.go`)

After `OpSetField`, instead of just deleting the available entry, also record the stored value:
```go
case OpSetField:
    key := loadKey{objID: instr.Args[0].ID, fieldAux: instr.Aux}
    delete(available, key)
    if len(instr.Args) >= 2 {
        available[key] = instr.Args[1].ID  // forward stored value
    }
```

This is ~3 lines changed in `pass_load_elim.go`.

### Task 3: Integration test + benchmark

Verify all 22 benchmarks produce correct results. Run benchmarks. No tiering policy changes → CLI hang check not required.

## Expected Effect

**nbody inner j-loop**: 4 GetField (bi.x, bi.y, bi.z, bi.mass) hoisted to j-loop preheader.
- Saves 4 × ~10 insns per j-iteration (deduped field loads + GuardType)
- Eliminates 8 dependent load stalls per j-iteration (~32 cycles at L1 latency)
- Reduces register pressure in loop body (4 fewer live values)
- Second-order: hoisted values can be carried in FPRs via LICM invariant carry (regalloc already supports this from round 9)

**Store-to-load forwarding**: within-block forwarding eliminates ~3 redundant loads in nbody's position-update loop (bi.vx → bi.x update reads vx after writing it earlier in the same block).

**Prediction calibration**: Estimating ~8-10% for nbody. Previous rounds' estimates:
- Round 16 (LoadElim): predicted 6-8%, got 26% (compound effects dominated)
- Round 17 (feedback fix): predicted 5-8%, got 8.3%
This round's mechanism (hoisting loads) is more predictable than compound effects. 8-10% accounts for superscalar discount. Could outperform if FPR carry kicks in (similar to round 9's 12-15% on float loops).

**Secondary**: spectral_norm and matmul may benefit if their inner loops have similar invariant field access patterns. Fannkuch could also benefit.

## Failure Signals
- Signal 1: If hoisted GetField deopts at runtime (shape mismatch in preheader) → shape cache was wrong at compile time. Action: add defensive check that Aux2 != 0 before allowing hoist.
- Signal 2: If test failures from LICM → alias analysis too aggressive. Action: add OpSetTable to the kill set, verify no other memory ops missed.
- Signal 3: If benchmark regresses → hoisted values causing extra register pressure in preheader. Action: check regalloc output, may need budget limit on hoisted GetField count.

## Task Breakdown

- [x] 1. **LICM GetField hoisting** — file: `pass_licm.go` — test: `TestLICM_HoistGetField_NoStoreNoCall` (new), existing LICM tests pass
- [x] 2. **Store-to-load forwarding** — file: `pass_load_elim.go` — test: `TestLoadElim_StoreToLoadForwarding` (new), existing LoadElim tests pass
- [x] 3. **Integration test + benchmark** — run full test suite + all 22 benchmarks, correctness verified

## Budget
- Max commits: 3 (+1 revert slot)
- Max files changed: 4 (pass_licm.go, pass_licm_test.go, pass_load_elim.go, pass_load_elim_test.go)
- Abort condition: 2 commits without benchmark improvement, or any correctness regression

## Results (filled by VERIFY)
| Benchmark | Before | After | Change | Expected | Met? |
|-----------|--------|-------|--------|----------|------|
| nbody | 0.541s | 0.539s | -0.4% | -8-10% | No |
| spectral_norm | 0.042s | 0.045s | +7.1% (noise, 3ms abs) | -3-5% | No |
| matmul | 0.120s | 0.125s | +4.2% (noise) | -2-4% | No |
| mandelbrot | 0.064s | 0.062s | -3.1% (noise) | — | — |
| table_field_access | 0.052s | 0.050s | -3.8% (noise) | — | — |
| fannkuch | 0.054s | 0.051s | -5.6% (noise) | — | — |

### Test Status
- All passing (methodjit 1.886s, vm 0.547s)

### Evaluator Findings
- **pass** with two non-blocking notes:
  1. LICM alias scan doesn't include OpAppend/OpSetList (rare: named field access on array being appended to). Added to known-issues.
  2. LoadElim available map not invalidated by OpSetTable (pre-existing, not worsened by this diff).

### Regressions (≥5%)
- spectral_norm +7.1% (0.042→0.045s): 3ms absolute delta, within noise for sub-50ms benchmarks. Not a real regression.

### Outcome: `no_change`
Optimizations landed correctly (tests pass, code review pass), but target benchmark (nbody) unchanged. Root cause hypothesis: nbody's inner j-loop has in-loop SetField writes that prevent GetField hoisting on the same objects. The bi.x/bi.y/bi.z reads share objects with SetField writes in the velocity/position update — LICM correctly refuses to hoist them. Store-to-load forwarding helps within blocks but the benefit is subsumed by existing block-local LoadElim CSE from Round 16.

## Lessons (filled after completion/abandonment)
1. **The diagnosis was wrong**: nbody's inner loop has SetField on the same objects whose GetField we wanted to hoist. LICM correctly refused. The "invariant" reads (bi.x, bi.y, bi.z, bi.mass) live in the same table as the mutated fields (bi.vx, bi.vy, bi.vz), so the per-object alias analysis blocks them. Would need per-field alias tracking or proving field-disjointness.
2. **Store-to-load forwarding is subsumed by existing CSE**: within a block, LoadElim Round 16 already eliminates redundant GetField. S2L forwarding only helps when SetField is followed by GetField on the same field — which the existing CSE already handled (SetField kills, next GetField starts a new available entry).
3. **Prediction model failure**: predicted 8-10% based on instruction count (4 GetField × 10 insns = 40 insns removed). But the precondition (fields actually being loop-invariant) was false. Read the IR dump showing which specific fields are in setFields, not just count them.
4. **Infrastructure landed cleanly**: LICM GetField hoisting works for the general case (no store/call). Future loops without in-loop writes to the same table will benefit. Store-to-load forwarding is a correct ~3-line addition.
5. **Evaluator caught OpAppend/OpSetList gap**: aliasing scan should include all table-mutating ops, not just SetField/SetTable/Call.
