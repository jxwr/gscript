---
module: emit.table
description: Table op emission — native typed-array fast paths, inline field cache with shape guards, dispatch between GETFIELD/SETFIELD/GETTABLE/SETTABLE.
files:
  - path: internal/methodjit/emit_table_array.go
  - path: internal/methodjit/emit_table_field.go
  - path: internal/methodjit/emit_dispatch.go
  - path: internal/runtime/table.go
  - path: internal/runtime/shape.go
last_verified: 2026-04-13
---

# Emit — Table Ops (Array + Field)

## Purpose

Lower OpGetTable/OpSetTable (dynamic integer-key access) and OpGetField/OpSetField (static string-key access) to ARM64. Dynamic keys branch on `runtime.Table.arrayKind` and emit a native fast path for each storage (Mixed/Int/Float/Bool); static-key access uses a shape-guarded inline cache. Non-int keys, metatables, out-of-bounds, and shape mismatches fall through to exit-resume via `ExitTableExit`.

## Public API

- `func (ec *emitContext) emitNewTableExit(instr *Instr)` — OpNewTable always table-exits (allocation is Go-side).
- `func (ec *emitContext) emitGetTableNative(instr *Instr)` — OpGetTable with kind-specialized fast paths.
- `func (ec *emitContext) emitSetTableNative(instr *Instr)` — OpSetTable with kind-specialized fast paths.
- `func (ec *emitContext) emitGetField(instr *Instr)` — OpGetField, shape-guarded svals load.
- `func (ec *emitContext) emitSetField(instr *Instr)` — OpSetField, shape-guarded svals store.
- `func (ec *emitContext) emitGetFieldExit(instr *Instr)` / `emitSetFieldExit` — table-exit fallback.

## Invariants

- **MUST**: GETFIELD/SETFIELD inline cache key is `Aux2 = (shapeID<<32) | fieldIndex`, with `shapeID` a `uint32` (not a `*Shape` pointer). Grep: `shapeID := uint32(instr.Aux2 >> 32)` in `emit_table_field.go`.
- **MUST**: shape guard loads `table.shapeID` via `LDRW` at `jit.TableOffShapeID` and deopts on mismatch to `getfield_deopt` / `setfield_deopt`.
- **MUST**: `ec.shapeVerified[tblValueID] = shapeID` is recorded after a successful guard so subsequent field accesses on the same SSA value in the same block skip the type-check + nil-check + shape-check sequence.
- **MUST**: `ec.shapeVerified` is cleared on `OpCall`, `OpSelf`, and `OpSetTable` (dynamic key write can add a new string key and bump the shape). Grep: `ec.shapeVerified = make(map[int]uint32)` in `emit_dispatch.go`.
- **MUST**: `ec.tableVerified[tblValueID]` is a per-block flag that records a completed table tag+nil+metatable check; reset on the same events as shapeVerified.
- **MUST**: `emitGetTableNative` and `emitSetTableNative` handle four array kinds — `ArrayMixed` (`[]Value`), `ArrayInt` (`[]int64`), `ArrayFloat` (`[]float64`), `ArrayBool` (`[]byte`, encoded 0=nil / 1=false / 2=true). Grep: `intArrayLabel`, `floatArrayLabel`, `boolArrayLabel`, `mixedArrayLabel`.
- **MUST**: `ArrayInt` path NaN-boxes the loaded `int64` via `EmitBoxIntFast` with `mRegTagInt`; `ArrayFloat` stores raw IEEE bits directly (float bits ARE the NaN-box representation, no conversion).
- **MUST**: When `instr.Aux2` carries feedback kind (`1..4` = `FBKindMixed..FBKindBool`), the emitter emits a single 3-instruction kind guard instead of the 4-way dispatch cascade.
- **MUST**: integer key must be `>= 0`; the emitter emits `CMPimm X1, 0; BCond CondLT, deopt` after key extraction.
- **MUST**: `OpSetTable` sets the `keysDirty` byte (`STRB X5, X0, TableOffKeysDirty`) after every successful array write on any kind.
- **MUST**: metatable check is a `LDR + CBNZ` against `TableOffMetatable`; any metatable → deopt (no native `__index` / `__newindex` support).
- **MUST NOT**: emit inline field cache when `shapeID == 0` or `instr.Aux2 == 0` — both paths bypass to `emitGetFieldExit` / `emitSetFieldExit`.
- **MUST NOT**: leave `rawIntRegs` state divergent between fast and slow paths. Both `emitGetField` and `emitGetTableNative` snapshot `rawIntRegs` into `savedRawIntRegs`, then re-emit unbox instructions on the reload path so both paths converge.

## Hot paths

- `sieve` — ArrayInt fast path; every inner-loop `t[i] = false` is a SetTable-ArrayBool and every `t[i]` read is a GetTable-ArrayBool (R15 win).
- `nbody`, `spectral_norm`, `mandelbrot` — GETFIELD inline cache on body records (`b.x`, `b.y`, `b.z`, etc.) drives shape-guarded `LDR X0, [svals, #idx*8]` sequences. Shape dedup across sibling fields in the same block is load-bearing.
- `object_creation` — NewTable exit-resume cost dominates; native table allocation has no fast path here.

## Known gaps

- **NewTable has no native path**: allocation always takes a `TableExit` round-trip. Tracked as a known gap; see `docs-internal/known-issues.md`.
- **Monomorphic inline cache only**: field cache stores exactly one shapeID per GETFIELD PC. Polymorphic sites fall through to the exit path every call.
- **No cross-block shape propagation**: `shapeVerified` is block-local; a guard before a loop does not dedup guards inside the loop body (LICM hoists invariant guards, but not per-iteration ones).
- **ArrayInt store requires tag check** unless the RHS is a `constInts` constant or a `rawIntRegs` raw-int value; otherwise the slow-path tag check before store is unavoidable.
- **`emitSetTableNative` clears `shapeVerified` but `emitGetTableNative` does not** — a GetTable read cannot change a shape, but calls inside the block still flush both maps.

## Tests

- `emit_table_test.go` — basic GETTABLE/SETTABLE emission and kind-dispatch correctness
- `emit_table_typed_test.go` — ArrayInt / ArrayFloat / ArrayBool fast-path paths
- `emit_tier2_correctness_test.go` — end-to-end Tier 2 correctness for table ops
- `feedback_getfield_integration_test.go` — feedback → GETFIELD GuardType cascade

## See also

- `kb/modules/emit/overview.md`
- `kb/modules/emit/guard.md` — shape guard dedup + LICM interaction
- `kb/modules/feedback.md` — where `Aux2` kind feedback originates
