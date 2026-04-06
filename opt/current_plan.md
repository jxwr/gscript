# Optimization Plan: Table access kind specialization + validation caching

> Created: 2026-04-07
> Status: active
> Cycle ID: 2026-04-07-table-kind-specialize
> Category: field_access
> Initiative: standalone

## Target

Reduce per-access overhead for GetTable/SetTable on typed arrays. sieve is the primary target (7.55x behind LuaJIT); benefits extend to all array-access-heavy benchmarks.

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| sieve | 0.083s | 0.011s | 7.55x | 0.060-0.070s (−16-28%) |
| matmul | 0.125s | 0.021s | 5.95x | ~0.110s (secondary) |
| spectral_norm | 0.045s | 0.008s | 5.63x | ~0.040s (secondary) |
| fannkuch | 0.051s | 0.019s | 2.68x | ~0.045s (secondary) |

## Root Cause

**Data-backed** (sieve diagnostic, confirmed by reading emitter source):

SetTable on ArrayBool emits **35 ARM64 instructions** per inner-loop iteration. Only **1 is the actual store**:

| Overhead | Insns | Notes |
|----------|-------|-------|
| Table type check (tag=0xFFFF, sub=0) | 10 | Checks NaN-box tag, extracts ptr |
| Nil check + metatable check | 3 | Table ptr null + metatable nil |
| Key validation (negative key, tag) | 6 | Already optimized for rawIntRegs |
| Array kind dispatch (4-way cascade) | 8 | CMP+B.EQ for Bool/Float/Int/Mixed |
| Bounds check + base load | 4 | Array bounds + data pointer |
| Actual store + dirty flag | 3 | STRB + dirty bit |
| Branch | 1 | Skip deopt fallback |

For sieve, the table `is_prime` is loop-invariant with stable ArrayBool kind. The type check (10), nil/metatable (3), and kind dispatch (8) are **all redundant** per iteration — they never change.

LuaJIT's equivalent: ~4-5 insns (direct byte store at known offset, bounds hoisted by SCEV). The 8x instruction gap explains most of the 7.55x wall-time gap (branch prediction helps but doesn't eliminate the fetch/decode pressure).

## Prior Art (MANDATORY)

**V8:** TurboFan does NOT hoist CheckMaps — it **eliminates redundant ones**. `LoadElimination::ReduceCheckMaps` (load-elimination.cc:786-799) checks if the abstract state already knows the object's maps. If so, the CheckMaps is replaced with its effect input (no-op). `ComputeLoopState` (load-elimination.cc:1363-1460) scans loop body — only `StoreField` to `kMapOffset` kills map knowledge. Element stores and other CheckMaps do not. The deopt safety issue is avoided because checks are eliminated, never moved. Element kind specialization uses `ElementsKindOf` from Maps in `SimplifiedLowering`.

**LuaJIT:** Explicitly rejects traditional LICM for guards (lj_opt_loop.c:28-31: "LICM is mostly useless for compiling dynamic languages"). Instead uses **copy-substitution**: the recorded trace is a pre-roll (prologue); the loop body is re-emitted through FOLD/CSE, and invariant guards (like FLOAD TAB_ASIZE, FLOAD TAB_HMASK) are CSE'd to pre-roll versions (lj_opt_loop.c:324-326). Table field alias analysis (lj_opt_mem.c:570-618) enables FLOAD forwarding: different field IDs never alias. The loop body has only variant instructions.

**SpiderMonkey (Warp):** CacheIR stubs record observed element kind and object shape. WarpBuilder emits kind-specific LoadElement with preceding GuardShape. GVN pass eliminates redundant guards when dominated by an identical check.

Our constraints vs theirs:
- GScript has no Map/shape for tables (only for named fields via shapeID). Array kind is a runtime property (`Table.arrayKind` byte).
- GScript has LICM but does NOT hoist guards (deopt PC issue). The V8 approach (eliminate, not hoist) is more applicable.
- GScript's table structure uses Go-managed memory with pointer indirection (Table → backing array slice → data).
- Long-term: split table ops into separate guards (GuardIsTable + GuardKind + IndexArray), then eliminate redundant guards via LoadElimination extension. This round implements the prerequisite: kind feedback + kind-specialized emit.

## Approach

Two complementary changes:

### A. Table validation dedup within blocks (`tableVerified`)

Mirror the existing `shapeVerified` mechanism (GetField dedup, round 17) for GetTable/SetTable:
- After first GetTable/SetTable on a table value validates it (type check, ptr extract, nil check, metatable check), record `tableVerified[valueID] = rawPtrSpillSlot` in emitContext.
- Subsequent GetTable/SetTable on the same table in the same block load the raw pointer from a spill slot (1 LDR) instead of re-validating (10 insns).
- Invalidated by OpCall, OpSelf, and block boundaries (same as shapeVerified).

This helps blocks with multiple table accesses on the same table (nbody j-loop: GetTable for `bodies[j]`, plus all the GetField on bj).

### B. Array kind feedback + kind-specialized emit

Add array kind to the feedback system, then emit kind-specific fast paths:

1. **Tier 1 recording**: In GETTABLE/SETTABLE Tier 1 fast paths (tier1_table.go), after the kind dispatch selects a specific path, record the observed kind in a new feedback field (`proto.Feedback[pc].Kind`).

2. **Graph builder propagation**: In graph_builder.go, when emitting GetTable/SetTable, read `proto.Feedback[pc].Kind`. If monomorphic (e.g., always ArrayBool), store the kind in `instr.Aux2` (upper bits, like GetField uses Aux2 for shape+field).

3. **Kind-specialized emit**: In emitGetTableNative/emitSetTableNative, if Aux2 carries a known kind:
   - Emit a kind guard: `LDRB kind; CMP kind, #expected; B.NE deopt` (3 insns)
   - Emit ONLY the matching kind path (no 4-way dispatch cascade)
   - Saves ~5 insns per access (eliminate 3 cascade branches + their CMPs)

4. **Combined with tableVerified**: For subsequent accesses on a verified table, even the kind guard might be skippable (kind doesn't change between accesses in same block).

### Prerequisite: emit_table.go split

emit_table.go is at 978 lines (⚠ CRITICAL). MUST split before adding table-access changes.
Split into: `emit_table_field.go` (GetField/SetField) + `emit_table_array.go` (GetTable/SetTable/NewTable).

## Expected Effect

**Instruction savings per SetTable/GetTable access:**
- Part A (tableVerified dedup, same-block): −10 insns for 2nd+ access on same table
- Part B (kind specialization): −5 insns per access (no cascade dispatch)
- Combined (verified + known kind): −15 insns per access

**Wall-time estimates (superscalar-discounted):**
- sieve: 35 insns → ~20 insns per SetTable (−43% insns). At superscalar discount → ~20-25% wall-time. Sieve 0.083s → 0.062-0.066s.
- nbody: secondary benefit from tableVerified on bodies[j] access. ~2-3%.
- matmul: GetTable array accesses benefit from kind specialization. ~3-5%.
- fannkuch: multiple array accesses per iteration benefit from both. ~5-8%.

**Prediction calibration:** These estimates are conservative. The primary mechanism is instruction elimination, not just ILP improvement. Branch prediction already helps the current dispatch cascade, so the wall-time gain from removing predicted branches is less than the instruction count suggests. Previous rounds (7-10) overestimated by 2-25x; we use the low end of the range.

## Failure Signals

- Signal 1: sieve's inner loop ALREADY uses raw-int carry + fused compare+branch in production → overhead is lower than diagnostic suggests → pivot to diagnostic test fix + re-measure
- Signal 2: Array kind is NOT stable across iterations (kind changes mid-loop) → kind guard fires often → remove kind specialization, keep tableVerified only
- Signal 3: emit_table.go split exceeds 2 hours → stop split, put changes in main file with TODO

## Task Breakdown

- [x] 0. **Extract RunTier2Pipeline()** — Create `internal/methodjit/pipeline.go` with shared `RunTier2Pipeline(fn *Function) (*Function, error)` running the full production pipeline. Refactor `compileTier2()` in `tiering_manager.go`, `Diagnose()` in `diagnose.go`, and `tier2_float_profile_test.go` to call it. Search for any other stale pipeline copies in `*_test.go` files and fix them. Test: `go test ./internal/methodjit/ -short -count=1 -timeout 120s` must pass.
- [x] 1. **Split emit_table.go** — Split into `emit_table_field.go` (GetField/SetField/shapeVerified) + `emit_table_array.go` (GetTable/SetTable/NewTable). Move ~500 lines each. All existing tests must pass.
- [x] 2. **Add tableVerified dedup** — file: `emit_table_array.go`, `emit_compile.go` — Add `tableVerified` map to emitContext (parallel to shapeVerified). After first GetTable/SetTable validates table, cache raw ptr in spill slot. Subsequent accesses load from spill. Test: new test verifying insn count reduction for 2-access blocks.
- [ ] 3. **Add array kind feedback** — files: `tier1_table.go`, `internal/vm/feedback.go`, `graph_builder.go` — Add `Kind` field to feedback entry. Record kind in Tier 1 GETTABLE/SETTABLE fast paths. Propagate to GetTable/SetTable Aux2 in graph builder. Test: `TestFeedbackKind_ArrayBool`.
- [ ] 4. **Kind-specialized emit** — file: `emit_table_array.go` — When Aux2 has known kind, emit kind guard + direct path. Test: verify correct results for all 4 array kinds + verify sieve produces correct count. Integration: run sieve benchmark end-to-end via TieringManager.

## Budget
- Max commits: 5 (+1 revert slot)
- Max files changed: 10
- Abort condition: 3 commits without test green, or emit_table.go split takes >2 hours

## Results (filled after VERIFY)
| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|

## Lessons (filled after completion/abandonment)
