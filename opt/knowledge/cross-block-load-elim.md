# Cross-Block / Dominator-Based Load Elimination

> Created: 2026-04-06 | Category: tier2_float_loop | Motivation: nbody at 15.9x behind LuaJIT; inner loop
> accesses 7-10 fields per iteration; existing block-local CSE insufficient.

---

## Problem Statement

GScript's `pass_load_elim.go` eliminates redundant `OpGetField` only **within a
single basic block**. For nbody's `advance()`, the critical inner loop body is
spread across multiple blocks (the `j` loop header, body, back-edge, and the
`i` loop header). Loads of `bi.x`, `bi.vx`, `bi.mass` etc. are defined in the
`i` loop body block and re-loaded repeatedly in the `j` inner loop body.
Block-local CSE cannot see across that block boundary.

Concretely in `advance()`:
- `bi.x`, `bi.y`, `bi.z`, `bi.mass` are loaded in the outer `i` loop body
- They are **re-loaded** in every iteration of the inner `j` loop
- `bj.mass` is loaded multiple times within the `j` loop body (already handled
  by block-local CSE)
- `bj.vx`, `bj.vy`, `bj.vz` are loaded and then immediately overwritten
  (`bj.vx = bj.vx + ...`) — **store-to-load forwarding** eliminates the second
  load

---

## Technique 1: V8 TurboFan — Effect-Chain Abstract State

**Source**: `src/compiler/load-elimination.cc` (1580 lines), `load-elimination.h`

### Mechanism

V8's `LoadElimination` is a **graph reducer** (not a CFG pass). It follows the
**effect chain** — a separate set of edges connecting every memory operation in
program order. Each node in the effect chain carries an `AbstractState`.

**Data structures** (`load-elimination.h:254`):
```
AbstractState = {
  maps_:         AbstractMaps     // object → known Map set
  fields_[32]:   AbstractField[]  // field-slot → map<object, FieldInfo>
  const_fields_: AbstractField[]  // for const fields
  elements_[8]:  AbstractElements // array elements (ring buffer)
}
```

Constants:
- `kMaxTrackedFieldsPerObject = 32` (h:190)
- `kMaxTrackedObjects = 100` (h:192)
- `kMaxTrackedFields = 300` (h:193) — total across all objects
- `kMaxTrackedElements = 8` (h:47) — ring buffer for array elements

**LoadField** (`cc:985-1046`):
1. Get `state = node_states_.Get(effect)` — the state before this node
2. Compute `field_index = FieldIndexOf(access)` — byte offset → slot index
3. Call `state->LookupField(object, field_index, const_field_info)`
4. If found and representation compatible → `ReplaceWithValue(node, replacement)`
5. Otherwise `state->AddField(object, field_index, info)` and propagate

**StoreField** (`cc:1048-1130`):
1. `state->KillField(object, field_index, name)` — invalidate potentially aliased entries
2. `state->AddField(object, field_index, new_info)` — record new value (store-to-load forwarding)

**Cross-block propagation via EffectPhi** (`cc:1262-1301`):

For a **join point** (Merge): computes intersection of incoming states
(`AbstractField::Merge` retains only entries present with same value in **all**
predecessor states, `cc:165-179`).

For a **loop** (Loop node, `cc:1267-1272`):
```cpp
if (control->opcode() == IrOpcode::kLoop) {
  // Take state from loop entry edge (first input = non-back-edge)
  AbstractState const* state = ComputeLoopState(node, state0);
  return UpdateState(node, state);
}
```
`ComputeLoopState` (`cc:1363-1465`) scans all nodes reachable on the back-edge
(the loop body) and conservatively kills any field that has a `StoreField` in
the loop. If any unhandled write is found, it calls `KillAll`. After this kill
pass, whatever **survives** is loop-invariant and usable inside the loop.

**Map (shape) check elimination** (`cc:786-817`):
- `ReduceCheckMaps`: if `state->LookupMaps(object)` already contains the
  required map set, replace the `CheckMaps` node with its effect input (no-op).
  Otherwise record: `state->SetMaps(object, maps)`.
- This means a shape check in the loop pre-header is propagated into the loop
  body and eliminates duplicate checks at every iteration.

**Key insight**: V8's abstract state is **per-effect-chain-node** (not per
basic-block). The reducer visits nodes in a topological order driven by the
effect chain. This naturally propagates state through linear sequences, and
`ReduceEffectPhi` handles joins and loops explicitly.

### V8 File:Line Citations

| Item | Location |
|------|----------|
| AbstractState declaration | `load-elimination.h:254` |
| kMaxTracked constants | `load-elimination.h:190-193` |
| ReduceLoadField | `load-elimination.cc:985` |
| ReduceStoreField | `load-elimination.cc:1048` |
| ReduceEffectPhi (cross-block merge) | `load-elimination.cc:1262` |
| Loop handling (ComputeLoopState) | `load-elimination.cc:1267-1272` |
| ComputeLoopState implementation | `load-elimination.cc:1363-1465` |
| ReduceCheckMaps (shape elim) | `load-elimination.cc:786` |
| AbstractField::Merge (intersection) | `load-elimination.h:165-179` |

---

## Technique 2: LuaJIT — Trace-Wide Store-to-Load Forwarding

**Source**: `src/lj_opt_mem.c` (992 lines), `src/lj_opt_loop.c`

### Mechanism

LuaJIT does not have "cross-block" in the traditional CFG sense — its IR is
**linear** (single trace, no branches except guards). The IR uses a **chain
structure**: each IR opcode has a linked list `J->chain[IR_HSTORE]` of all
stores of that type, and similarly for loads.

**`fwd_ahload`** (`lj_opt_mem.c:162-255`) — the core forwarding function:

```
Given a hash/array reference (HLOAD at xref):
1. Walk J->chain[HSTORE] backward from xref.
   - aa_ahref(refa, refb) = ALIAS_NO  → skip, continue search
   - aa_ahref()           = ALIAS_MAY → set lim = ref, goto cselim
   - aa_ahref()           = ALIAS_MUST → return store->op2 (store-to-load forward)
2. If no conflict: check for const-fold from TNEW/TDUP
3. cselim: walk J->chain[HLOAD] from xref down to lim
   - If found matching load (same HREF ref), return it (CSE)
4. Return 0 (emit new load)
```

**`lj_opt_fwd_hload`** (`lj_opt_mem.c:291-297`): public entry point, calls
`fwd_ahload`.

**`lj_opt_fwd_fload`** (`lj_opt_mem.c:589-618`): same pattern for struct fields
(FLOAD/FSTORE, used for table metadata like `.meta`, `.nomm`).

**Alias analysis** (`lj_opt_mem.c:55-159`):
- `aa_table`: two different allocations (TNEW/TDUP) → `ALIAS_NO`; two
  parameters → `ALIAS_MAY`
- `aa_ahref`: same table + same constant key → `ALIAS_MUST`; different constant
  keys → `ALIAS_NO`; same table + variable key → `ALIAS_MAY`

**Loop body cross-iteration forwarding** (`lj_opt_loop.c:77-85`):
```
Load/store forwarding works across loop iterations, too. This is important if
loop-carried dependencies are kept in upvalues or tables. E.g. 'self.idx =
self.idx + 1' deep down in some OO-style method may become a forwarded
loop-recurrence after inlining.
```
The trick: `loop_unroll` re-emits the entire recorded instruction stream
through the fold/CSE/forward pipeline again. When the re-emitted HLOAD for
`bi.x` is processed, `fwd_ahload` walks backward and finds the earlier HLOAD
(from the pre-roll iteration), returning it directly — the second load is
eliminated.

**Shape check (`HREFK`) elimination** (`lj_opt_mem.c:299-323`):
`lj_opt_fwd_hrefk` checks if a matching HREFK already exists in the chain for
the same (table, key slot). If the table was created with TDUP (constant
template), the guard bit `IRT_GUARD` is cleared entirely — zero-cost key lookup.

### LuaJIT File:Line Citations

| Item | Location |
|------|----------|
| `fwd_ahload` (core forwarding) | `lj_opt_mem.c:162` |
| `lj_opt_fwd_hload` (entry point) | `lj_opt_mem.c:291` |
| `lj_opt_fwd_fload` (field/struct) | `lj_opt_mem.c:589` |
| `aa_table` (table alias) | `lj_opt_mem.c:55` |
| `aa_ahref` (hash/array alias) | `lj_opt_mem.c:103` |
| Loop comment (cross-iter forwarding) | `lj_opt_loop.c:77` |
| `lj_opt_fwd_hrefk` (shape check CSE) | `lj_opt_mem.c:299` |

---

## Technique 3: SpiderMonkey (IonMonkey/Warp) — Dominator-Based GVN

SpiderMonkey's MIR-level optimization uses **Global Value Numbering (GVN)**
with dominator tree traversal. The pass visits blocks in dominator order; a
load in dominator block D is available to all blocks D dominates.

Key: for loop-invariant object references, a `GetPropertyPolymorphic` or
`LoadSlot` in the loop pre-header (which dominates the loop body) is available
in every loop iteration. The GVN pass (similar to V8's effect-chain approach)
tracks available expressions by (base-value, offset) pairs and replaces
dominated loads.

Source: `js/src/jit/MIR.h` (MDefinition::congruentTo), `js/src/jit/ValueNumbering.cpp`.

---

## Store-to-Load Forwarding

All three engines handle this uniformly:

```
OpSetField(obj, "vx", newVal)
...
OpGetField(obj, "vx")  →  replaced with newVal
```

V8: `ReduceStoreField` calls `state->AddField(object, field_index, {new_value,
repr})` after the kill. When the subsequent `ReduceLoadField` runs,
`state->LookupField` returns `new_value` directly. No memory access emitted.

LuaJIT: `fwd_ahload` finds `ALIAS_MUST` match in the HSTORE chain and returns
`store->op2` — the stored value ref.

GScript currently does **not** implement store-to-load forwarding. After
`OpSetField(obj, "vx", val)`, `delete(available, key)` kills the entry but
never records `val` as the new value. A subsequent `OpGetField(obj, "vx")`
re-loads from memory.

---

## Gap Analysis: GScript vs Production Engines

| Feature | GScript | V8 TurboFan | LuaJIT | Missing? |
|---------|---------|-------------|--------|----------|
| Block-local load CSE | YES (`pass_load_elim.go`) | via effect chain | via IR chain | — |
| Store-to-load forwarding | NO | YES (`ReduceStoreField` AddField) | YES (`fwd_ahload` ALIAS_MUST) | **YES** |
| Cross-block load CSE (dominator) | NO | YES (effect phi merge) | N/A (linear) | **YES** |
| Shape check CSE within block | YES (`emit_table.go:shapeVerified`) | YES (`ReduceCheckMaps`) | YES (`lj_opt_fwd_hrefk`) | — |
| Shape check CSE across blocks | NO | YES (maps propagated through effect chain) | YES (HREFK chain) | **YES** |
| Loop-invariant load hoisting | NO (GetField not in `canHoistOp`) | YES (via `ComputeLoopState` survivor) | YES (loop_unroll re-emit) | **YES** |

---

## Applicability to GScript's nbody

### What to do: Three ranked improvements

**Improvement A — Store-to-Load Forwarding (Easy, ~2-3% impact)**

In `pass_load_elim.go`, change `OpSetField` handling:
```go
case OpSetField:
  if len(instr.Args) < 2 { continue }
  key := loadKey{objID: instr.Args[0].ID, fieldAux: instr.Aux}
  delete(available, key)
  // ADD: forward the stored value
  available[key] = instr.Args[1].ID  // Args[1] = value being stored
```

In nbody `advance()`, after `bi.vx = bi.vx - dx * bj.mass * mag`, the next
access to `bi.vx` in the same block would use the stored value directly.
Impact: eliminates 3 loads per `bi` per inner iteration (vx, vy, vz re-read
after write in outer position update loop).

**Improvement B — Loop-Invariant GetField Hoisting via LICM (Medium, ~10-15% impact)**

Add `OpGetField` to `canHoistOp` in `pass_licm.go` **with a key invariance
condition**: a `GetField` is loop-invariant if:
1. Its object operand is loop-invariant (defined outside the loop)
2. No `OpSetField` on the **same (obj, field)** key appears anywhere in the loop body

The LICM pass already has:
- Loop body identification (`bodyBlocks`)
- Invariant set with fixpoint iteration
- Pre-header creation

Missing: `OpGetField` is not in `canHoistOp`, and there is no check for
intervening `SetField` on the same field.

Implementation:
```go
// In collectSetFields():
setFields := make(map[loadKey]bool)
for _, b := range bodyList {
  for _, instr := range b.Instrs {
    if instr.Op == OpSetField && len(instr.Args) >= 1 {
      k := loadKey{objID: instr.Args[0].ID, fieldAux: instr.Aux}
      setFields[k] = true
    }
  }
}

// In canHoistOp extension or inline in hoistOneLoop:
case OpGetField:
  if len(instr.Args) < 1 { continue }
  k := loadKey{objID: instr.Args[0].ID, fieldAux: instr.Aux}
  if setFields[k] { continue }  // field mutated in loop: cannot hoist
  // all args invariant check runs normally
```

For nbody inner `j` loop, the outer `bi` object is loop-invariant (loaded once
before the `j` loop). Fields `bi.x`, `bi.y`, `bi.z`, `bi.mass`, `bi.vx`,
`bi.vy`, `bi.vz` are **not modified** in the `j` loop body — only `bj.*` fields
are modified. All 7 `bi.*` field loads hoist to the `i` loop body (pre-header
of the `j` loop).

Impact estimate: 7 GetField per j-iteration × 5 bodies × ~20 inner iters ≈
700 loads/outer-iter eliminated. Each GetField is ~3-4 instructions (load table
ptr, load svals, NaN-unbox). At superscalar discount: **~10-15% wall-time**.

**Improvement C — Cross-Block Shape Check Propagation (Medium, ~3-5% impact)**

The `shapeVerified` map in `emitContext` is reset at every block boundary
(`emit_dispatch.go:144,158,177`). A shape check for `bi` verified in the `i`
loop body should be valid in all blocks that it dominates (including the `j`
loop header, body, and back-edge) — as long as no `SetField` on `bi` uses a
different shape between those blocks.

Implementation approach: instead of resetting `shapeVerified` at every block
transition, propagate it along the dominator tree. The simpler version: when
entering a loop body block that is dominated by the pre-header, inherit the
pre-header's `shapeVerified` map.

This is lower priority than Improvement B because LICM-hoisted GetFields
eliminate the loads entirely (including their shape checks). Cross-block shape
propagation would only help the non-hoistable accesses (e.g., `bj.*` which
changes every `j` iteration).

---

## Implementation Notes for GScript

### Improvement B detailed steps

1. **Collect mutated fields** before the invariant fixpoint: walk all in-loop
   instructions, collect `(objID, fieldAux)` pairs from `OpSetField`.

2. **Extend `canHoistOp` or add special case**: in `hoistOneLoop`, before
   marking a `GetField` as invariant, check that its `(objID, fieldAux)` key is
   not in the mutated-fields set.

3. **Aux2 carries shapeID**: the hoisted `OpGetField` retains its `Aux2` field
   (shapeID + fieldIndex). The pre-header emitter calls `emitGetField` exactly
   as the in-loop emitter does. No change to the emitter needed.

4. **Store-to-load forwarding after hoist**: after hoisting `bi.vx` load out of
   the `j` loop, the outer `i` loop's update (`b.vx = b.vx + dt * b.vx`) still
   needs store forwarding. With Improvement A, that store records the new value,
   and the next load in the next outer iteration sees it.

5. **Interaction with shapeVerified**: the pre-header's emitted GetField
   performs the shape check and records `shapeVerified[bi_id] = shapeID`. When
   the `j` loop body emits other GetField on `bi`, it should inherit this
   verification. Requires propagating `shapeVerified` into dominated blocks —
   simplest: pass it down from pre-header before entering the loop body.

### Invariant object detection for nbody

In `advance()`:
```
bi := bodies[i]      // OpGetTable(bodies, i) → bi is defined OUTSIDE j-loop
bj := bodies[j]      // OpGetTable(bodies, j) → bj is defined INSIDE j-loop

bi.x  → obj=bi_id (defined outside j-loop) → INVARIANT → hoist
bj.x  → obj=bj_id (defined inside j-loop)  → VARIANT   → do not hoist
```

The invariant check `!invariant[a.ID]` already handles this correctly — `bi`'s
SSA value is defined in the `i` loop body, which is outside the `j` loop body.

### Complexity estimates

| Improvement | Lines of code | Estimated implementation time |
|------------|--------------|-------------------------------|
| A: Store-to-load forwarding | ~5 lines in `pass_load_elim.go` | 30 min |
| B: LICM GetField hoisting | ~30-40 lines in `pass_licm.go` | 2-3 hours (incl. tests) |
| C: Cross-block shape propagation | ~50-80 lines in `emit_dispatch.go` | 3-4 hours |

---

## Key Lessons from Production Engines

1. **V8's effect chain is equivalent to dominator-based propagation**: the
   effect chain captures program order; propagating `AbstractState` along it
   naturally handles dominator relationships. At join points (EffectPhi), the
   intersection of incoming states gives "available on all paths."

2. **Loop handling requires a two-pass approach**: V8 uses `ComputeLoopState`
   to scan the loop body first (kill anything written), then propagates the
   surviving state into the loop. This is equivalent to GScript's LICM
   invariant analysis — the invariant set is exactly what survives the kill pass.

3. **Store-to-load forwarding is symmetric with load CSE**: the `AddField` call
   in `ReduceStoreField` (V8) / the ALIAS_MUST return in `fwd_ahload` (LuaJIT)
   is the same mechanism as load CSE, but seeded by a store instead of a load.
   GScript has the infrastructure (`available` map) — just needs the AddField
   half.

4. **LuaJIT's comment is definitive**: "LICM is mostly useless for dynamic
   languages" because guards are not hoistable. But GScript's `OpGetField` is
   **not a guard** — it is a memory load with inline shape check. The shape check
   at emit time (not IR time) means the IR instruction itself is hoistable
   provided the object and field are invariant.

5. **GScript's LICM already has the right structure**: pre-header creation,
   invariant fixpoint, instruction movement — all present. The only missing
   piece is adding `OpGetField` to `canHoistOp` with the field-write exclusion.
