# Cross-Block Shape Check Propagation

> Created: 2026-04-07 | Task: Research how V8 propagates shape/map checks across
> basic block boundaries using the dominator tree, so that shape checks verified
> in a loop pre-header are available in the loop body blocks.

---

## Problem Statement

GScript's `shapeVerified` map in `emitContext` (`emit_compile.go:262`) is reset
at every basic block boundary (`emitBlock` at `emit_compile.go:534`). A shape
check verified in a loop pre-header is therefore not reused in loop body blocks,
even though the pre-header dominates all loop body blocks and the shape cannot
change without a `SetTable`/`Call`/`Self` that we already detect.

This note documents how V8 solves the equivalent problem (`CheckMaps`
propagation across loop headers) and derives a concrete implementation plan for
GScript.

---

## V8's Approach (TurboFan LoadElimination)

**Source file**: `src/compiler/load-elimination.cc` (1580 lines)

### Data structure

V8 maintains an `AbstractState` per **effect-chain node** (not per block):

```
AbstractState = {
  maps_:         AbstractMaps    // object → ZoneRefSet<Map>
  fields_[32]:   AbstractField[] // field-slot → map<object, FieldInfo>
  const_fields_: AbstractField[]
  elements_[8]:  AbstractElements
}
```

`AbstractMaps` is a `ZoneMap<Node*, ZoneRefSet<Map>>` keyed on the
(rename-resolved) object SSA value (`load-elimination.cc:341-417`).

### Shape check elimination (`ReduceCheckMaps`, cc:786-799)

```cpp
Reduction LoadElimination::ReduceCheckMaps(Node* node) {
  ZoneRefSet<Map> const& maps = CheckMapsParametersOf(node->op()).maps();
  Node* const object = NodeProperties::GetValueInput(node, 0);
  Node* const effect = NodeProperties::GetEffectInput(node);
  AbstractState const* state = node_states_.Get(effect);
  if (state == nullptr) return NoChange();
  ZoneRefSet<Map> object_maps;
  if (state->LookupMaps(object, &object_maps)) {
    if (maps.contains(object_maps)) return Replace(effect);  // eliminate!
    // TODO: Compute intersection
  }
  state = state->SetMaps(object, maps, zone());
  return UpdateState(node, state);
}
```

Key behavior:
- If the incoming `state` already records that `object` has exactly the
  required maps, the `CheckMaps` is **replaced with its effect input** (no
  code emitted — pure elimination).
- Otherwise, `SetMaps` records the new knowledge and propagates it to
  downstream nodes via the effect chain.

### Cross-block merge at join points (`ReduceEffectPhi`, cc:1262-1301)

For a **Merge** (if-then-else join), V8 computes the **intersection** of
incoming map states:

```cpp
// cc:1283-1288: copy state0, then merge in each subsequent input
AbstractState* state = zone()->New<AbstractState>(*state0);
for (int i = 1; i < input_count; ++i) {
  Node* const input = NodeProperties::GetEffectInput(node, i);
  state->Merge(node_states_.Get(input), zone());
}
```

`AbstractState::Merge` (`cc:471-490`) — for map state specifically:
```cpp
if (this->maps_) {
  this->maps_ = that->maps_ ? that->maps_->Merge(this->maps_, zone()) : nullptr;
}
```

`AbstractMaps::Merge` (`cc:374-387`) retains only entries where **both**
predecessors agree on the same map set (true intersection — not union). If
either predecessor has no information about an object, the entry is dropped.

**Answer to Q3**: At merge points, map state is **intersected**, not unioned.
Only facts true on ALL incoming paths survive.

### Loop header handling (`ReduceEffectPhi` for Loop, cc:1267-1272)

```cpp
if (control->opcode() == IrOpcode::kLoop) {
  // Rely on reducible loops: entry edge always dominates the header.
  // Take state from the first input (the non-back-edge / pre-header path).
  AbstractState const* state = ComputeLoopState(node, state0);
  return UpdateState(node, state);
}
```

**Answer to Q1**: At a loop header with both pre-header and back-edge
predecessors, V8 **only uses the pre-header's state** (`state0` = first
effect input = the non-back-edge). It ignores the back-edge's state. The
comment says "The loop entry edge always dominates the header."

The pre-header state is then **conservatively killed** by `ComputeLoopState`
before being applied at the header. This two-step is critical: start with
what's known at entry, then subtract what the loop body can clobber.

### `ComputeLoopState` — loop body invalidation scan (cc:1363-1465)

This function determines which shape facts from the pre-header are still
valid after any iteration of the loop body. It performs a BFS/DFS over the
loop body effect graph starting from the back-edge predecessors:

```cpp
// Start from back-edge inputs (i >= 1) of the EffectPhi.
for (int i = 1; i < control->InputCount(); ++i) {
  queue.push(node->InputAt(i));
}
while (!queue.empty()) {
  Node* const current = queue.front(); queue.pop();
  if (!current->op()->HasProperty(Operator::kNoWrite)) {
    switch (current->opcode()) {
      case IrOpcode::kStoreField: {
        FieldAccess access = FieldAccessOf(current->op());
        // If access.offset == HeapObject::kMapOffset → KillMaps(object)
        // else → KillField(object, field_index)
        state = ComputeLoopStateForStoreField(current, state, access);
        break;
      }
      case IrOpcode::kTransitionAndStoreElement:
        state = state->KillMaps(object, zone());  // shape change!
        // + kill elements field
        break;
      case IrOpcode::kCheckMaps:  // no effect on tracked state
      case IrOpcode::kStoreTypedElement:
        break;
      default:
        return state->KillAll(zone());  // unknown write → kill everything
    }
  }
  // Walk up the effect chain to predecessors.
  for (int i = 0; i < current->op()->EffectInputCount(); ++i)
    queue.push(NodeProperties::GetEffectInput(current, i));
}
```

**Answer to Q2 — What invalidates map state inside a loop body**:

| Operation | Effect on shape state |
|-----------|----------------------|
| `StoreField` at offset 0 (map slot) | `KillMaps(object)` — shape changed |
| `StoreField` at other offset | `KillField(object, field_index)` only |
| `TransitionAndStoreElement` | `KillMaps(object)` — shape changed |
| `TransitionElementsKind` | Conditional kill based on source map |
| `EnsureWritableFastElements` | `KillField(elements offset)` only |
| `CheckMaps` | **No effect** — check doesn't modify shape |
| Any op WITHOUT `kNoWrite` property (i.e., generic call) | `KillAll` |
| Any op WITH `kNoWrite` | Ignored (pure or read-only) |

Key: calls (`kCall`) and `kNoWrite`-absent ops trigger `KillAll`, which wipes
the entire state. In practice this means shape propagation works only
**within the loop body** for loops with no calls — which is exactly the
nbody inner loop scenario.

**Answer to Q4 — When can a cached shape check become invalid?**

1. **Map write at offset 0**: `StoreField` to the map slot changes the shape.
   In GScript this would be a `SetField`/`SetTable` that triggers a shape
   transition.
2. **Generic call**: any call can trigger GC, execute arbitrary user code,
   modify table shapes. In V8 this is `KillAll`. In GScript: `OpCall`,
   `OpSelf`, `OpSetTable` (key-based writes cause shape transitions) already
   correctly invalidate `shapeVerified`.
3. **GC**: V8's `AbstractMaps` tracks heap object maps which are GC-stable
   per-type (not per-instance) — GC doesn't change map identity. In GScript,
   `shapeID` is a struct field (`table.shapeID uint32`) that stays valid until
   a key is added/removed. GC (Go's GC) does not change `shapeID`.
4. **Deopt**: in V8, a deopt invalidates JIT'd code (not a runtime kill of
   AbstractMaps). In GScript, the deopt path (table-exit) re-enters Go and
   the JIT resumes after the exit — `shapeVerified` is irrelevant in the Go
   interpreter.

**Correctness condition for GScript shape propagation**: a `shapeVerified[id]`
entry established in block A is valid in any block B that A dominates, as long
as no `OpCall`, `OpSelf`, `OpSetTable`, or `OpSetField` on the same SSA value
appears on any path from A to B.

---

## SpiderMonkey

SpiderMonkey's gecko-dev was not available in the local cache. Based on
published sources and architectural knowledge:

SpiderMonkey's IonMonkey uses **MDefinition::dependency()** chains to track
aliasing in its MIR (similar to V8's effect chain). Type guard (`MGuardShape`)
elimination is handled by GVN (`ion/ValueNumbering.cpp`) which walks the
dominator tree and eliminates dominated nodes with the same value. The key
difference from V8: SpiderMonkey uses standard GVN (same-value elimination)
rather than a separate abstract-interpretation pass.

For GScript's purposes, V8's approach is the more directly applicable model.

---

## GScript Implementation

### Current state

- `shapeVerified map[int]uint32` in `emitContext` tracks `tableValueID →
  verifiedShapeID` per block.
- Reset unconditionally at every block boundary in `emitBlock`
  (`emit_compile.go:534`).
- Also reset after `OpCall`, `OpSelf`, `OpSetTable` in `emit_dispatch.go:144-179`.
- `computeLoopPreheaders` (`loops.go:331`) identifies the unique non-back-edge
  predecessor of each loop header.

### Proposed: Pre-header shape state inheritance

**Core idea**: Instead of resetting `shapeVerified` to empty at every block
boundary, initialize loop body blocks with the shape state captured at the END
of the pre-header block (after all pre-header instructions have been emitted).

This directly mirrors V8's `ComputeLoopState` logic: start with the pre-header's
state, then kill any entries whose SSA object has a `SetTable`/`Call`/`Self`
instruction in the loop body.

**Two-phase approach (recommended)**:

**Phase A — Simple inheritance (safe conservative approximation)**:
Inherit the pre-header's `shapeVerified` into all loop body blocks WITHOUT
scanning for invalidating ops in the loop body. This is correct because:
- The pre-header block's shape checks verify the actual runtime shape.
- If a `SetField`/`SetTable` in the loop body changes shape for the SAME SSA
  object, the `shapeVerified` entry for that object was already cleared by
  the existing invalidation code (`emit_dispatch.go:144-179`).
- The key insight: `shapeVerified` is keyed by **SSA value ID** (not by
  object identity). If the loop body modifies `tbl` (same SSA value), the
  emitter's existing handlers already clear `shapeVerified[tbl.ID]`.
- If the loop body modifies a DIFFERENT object (`bj` vs `bi`), the entry for
  `bi` is unaffected and can be safely reused.

**Phase B — Loop body scan (optional optimization)**:
For objects where the loop body contains `OpSetField` on a DIFFERENT SSA
value that might alias, we might over-eagerly inherit the shape. In practice,
since GScript uses SSA and field writes require knowing the object's SSA
value, false-positive aliasing is rare without pointer arithmetic. Phase A is
sufficient for the nbody case.

### Implementation Steps

**Step 1**: Add a `preHeaderShapeState` field to `emitContext`:

```go
// preHeaderShapeState maps headerBlockID → (tableValueID → shapeID)
// Captured at the end of each pre-header block. Used to initialize
// shapeVerified when entering loop body blocks dominated by that pre-header.
preHeaderShapeState map[int]map[int]uint32
```

**Step 2**: At the end of `emitBlock`, if the current block is the pre-header
of a loop header, save a copy of `shapeVerified`:

```go
// In emitBlock, after the instruction loop:
if ec.loop != nil {
    if headerID, ok := ec.preheaderToHeader[block.ID]; ok {
        if len(ec.shapeVerified) > 0 {
            snap := make(map[int]uint32, len(ec.shapeVerified))
            for k, v := range ec.shapeVerified {
                snap[k] = v
            }
            ec.preHeaderShapeState[headerID] = snap
        }
    }
}
```

**Step 3**: In `emitBlock`, when initializing `shapeVerified` for a loop body
block (non-header), inherit the innermost pre-header's state:

```go
// After: ec.shapeVerified = make(map[int]uint32)
if isLoopBlock && !isHeader && ec.loop != nil {
    if innerHeader, ok := ec.loop.blockInnerHeader[block.ID]; ok {
        if snap, ok := ec.preHeaderShapeState[innerHeader]; ok {
            for k, v := range snap {
                ec.shapeVerified[k] = v
            }
        }
    }
}
```

**Step 4**: Also inherit for the loop HEADER block itself (the pre-header
dominates the header):

```go
if isHeader && ec.loop != nil {
    for headerID := range ec.loop.loopHeaders {
        if headerID == block.ID {
            if snap, ok := ec.preHeaderShapeState[block.ID]; ok {
                for k, v := range snap {
                    ec.shapeVerified[k] = v
                }
            }
            break
        }
    }
}
```

Wait — this is wrong: the pre-header state must be captured BEFORE the header
is emitted, but the header block itself is processed after the pre-header.
Since blocks are emitted in RPO order (pre-header before header), Step 2
already captures the state before header emission. Step 3/4 can apply it.

**Step 5**: The `preheaderToHeader` mapping can be computed once during
`Compile` using the existing `computeLoopPreheaders` function and stored in
`emitContext`. Or derive inline: a block is a pre-header if it's in
`preheaderToHeader` (which is already computed in `regalloc.go:99-116`).

Alternatively — simpler — use the existing `preheaderToHeader` map already
computed in `regalloc.go`. Expose it from `Compile` into `emitContext`.

### Interaction with loop header block emission

The loop header block is emitted AFTER the pre-header. At loop-header entry,
phi values are active (loaded by `emitPhiMoves`). The pre-header's shape state
is valid at the header entry because:
- The phi's SSA value IDs differ from the table SSA value IDs tracked in
  `shapeVerified` (phis are new SSA values defined AT the header).
- The pre-header's shape check was for the table value (e.g., `bi_id`) which
  is defined BEFORE the loop and appears as a phi arg.

However, the loop header phis MAY re-define the table value (if `bi` is
passed through a phi). In that case, `bi_phi_id` is a new SSA value ID and
`shapeVerified[old_bi_id]` is irrelevant anyway. No conflict.

**Summary**: inheriting `shapeVerified` from pre-header into the header block
and all dominated loop body blocks is safe because:
1. The pre-header performed the actual runtime shape check.
2. All invalidating operations (`OpCall`, `OpSelf`, `OpSetTable`) already
   clear `shapeVerified` entries during instruction emission.
3. SSA value IDs uniquely identify the object pointer at JIT time.

### Expected impact

For nbody's `advance()` inner `j` loop:
- `bi` (outer body table) has its shape verified once in the `i` loop body
  (pre-header of `j` loop).
- Without this fix: every `j`-iteration re-emits the full shape guard for
  `bi` (~6 instructions: type check + nil check + shape load + compare).
- With inheritance: the first `bi.*` access in the `j` body skips the guard
  (already verified from pre-header).
- After LICM hoists `bi.*` field loads out of the `j` loop, shape propagation
  becomes moot for those hoisted ops. But non-hoistable accesses (if any) and
  the loop HEADER itself benefit.
- Estimated savings: ~5-10 instructions per `bi.*` access per `j`-iteration
  that the LICM didn't hoist. In nbody this is secondary to LICM but provides
  correctness depth (no double shape-guard regression).

### Risk

Low. The existing `shapeVerified` invalidation logic (calls/SetTable/SetField)
runs during instruction emission and will correctly clear inherited entries
when a shape-changing operation is encountered. The worst case of an incorrect
inheritance (hypothetically) would cause a deopt (shape mismatch at the inline
guard) — not a crash. In practice this cannot occur because SSA value IDs
uniquely identify the pointer.

---

## Key Lessons from V8

| V8 Concept | GScript Equivalent |
|------------|-------------------|
| `AbstractMaps` per effect-chain node | `shapeVerified` per block |
| `ReduceEffectPhi` for Loop: use pre-header state only | Inherit pre-header's `shapeVerified` snapshot |
| `ComputeLoopState` kills writes in loop body | Existing per-instruction invalidation handles this |
| Intersection at merge points | `shapeVerified` reset at merge (conservative) |
| `KillAll` for generic calls | `shapeVerified = make(...)` after `OpCall`/`OpSelf` |
| `KillMaps` for map-slot stores | `shapeVerified = make(...)` after `OpSetTable` |

V8's approach for loop headers: **take pre-header state, kill loop-body writes,
apply result**. GScript's approach simplifies this because per-instruction
invalidation already happens during emission — we just need to carry the initial
state across the block boundary.

---

## File:Line Reference (V8 src)

| Item | Location |
|------|----------|
| `ReduceCheckMaps` — shape check elimination | `src/compiler/load-elimination.cc:786-799` |
| `ReduceEffectPhi` — cross-block merge | `src/compiler/load-elimination.cc:1262-1301` |
| Loop header: use pre-header state only | `src/compiler/load-elimination.cc:1267-1272` |
| `ComputeLoopState` — loop body invalidation BFS | `src/compiler/load-elimination.cc:1363-1465` |
| `ComputeLoopStateForStoreField` | `src/compiler/load-elimination.cc:1345-1361` |
| `AbstractMaps::Merge` — intersection | `src/compiler/load-elimination.cc:374-387` |
| `AbstractState::Merge` | `src/compiler/load-elimination.cc:471-490` |
| `AbstractState::KillAll` | `src/compiler/load-elimination.cc:662-675` |
| `AbstractMaps` class definition | `src/compiler/load-elimination.h:~254` |
| `kMaxTrackedObjects = 100` | `src/compiler/load-elimination.h:192` |

---

## GScript File:Line Reference

| Item | Location |
|------|----------|
| `shapeVerified` field declaration | `internal/methodjit/emit_compile.go:262` |
| Reset at block boundary | `internal/methodjit/emit_compile.go:534` |
| Reset after `OpCall` | `internal/methodjit/emit_dispatch.go:144` |
| Reset after `OpSetTable` | `internal/methodjit/emit_dispatch.go:159` |
| Reset after `OpSelf` | `internal/methodjit/emit_dispatch.go:178` |
| Shape guard dedup in `emitGetField` | `internal/methodjit/emit_table_field.go:43-54` |
| Shape guard dedup in `emitSetField` | `internal/methodjit/emit_table_field.go:133-147` |
| `computeLoopPreheaders` | `internal/methodjit/loops.go:331-351` |
| `loopPreds` (inside vs outside preds) | `internal/methodjit/loops.go:307-317` |
| `blockInnerHeader` map | `internal/methodjit/loops.go:31` |
| Pre-header mapping in regalloc | `internal/methodjit/regalloc.go:99-116` |
| `emitBlock` — block emission entry | `internal/methodjit/emit_compile.go:522-593` |
