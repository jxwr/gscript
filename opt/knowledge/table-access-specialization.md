# Table Access Specialization in Production JIT Compilers

> Research date: 2026-04-06 | Sources: V8 (TurboFan+Turboshaft), LuaJIT 2

## 1. V8 TurboFan: Element Kind Specialization

### How element access works

V8 specializes array element access through `JSNativeContextSpecialization::ReduceElementAccess` (file: `v8/src/compiler/js-native-context-specialization.cc:2148`). The flow is:

1. **Feedback-driven**: IC feedback records which maps (shapes) were seen at each access site, grouped into "transition groups" by element kind.
2. **CheckMaps insertion**: For monomorphic access, `BuildCheckMaps` emits a single `CheckMaps` node that verifies the receiver's map matches (line 2288). For polymorphic access, multiple `CompareMaps`+`MapGuard` branches are created (lines 2343-2353).
3. **Element kind dispatch**: `BuildElementAccess` (line 3438) reads `access_info.elements_kind()` and specializes:
   - `IsSmiElementsKind` -> `MachineType::TaggedSigned` (no boxing)
   - `IsDoubleElementsKind` -> `MachineType::Float64` (unboxed double)
   - Otherwise -> `MachineType::AnyTagged`
4. **Bounds check**: `CheckBounds` node inserted against the array length (line 3510).
5. **Direct memory access**: `LoadElement`/`StoreElement` with type-specific `ElementAccess` descriptor (line 3532).

### How CheckMaps interacts with loops

V8 does NOT use traditional LICM for CheckMaps. Instead, it uses two complementary mechanisms:

**Mechanism A: Load Elimination (effect-chain-based map tracking)**

`LoadElimination::ReduceCheckMaps` (file: `v8/src/compiler/load-elimination.cc:786`) maintains an abstract state mapping objects to their known maps. When a `CheckMaps` node is encountered and the object's maps are already known to be a subset of the checked maps, the CheckMaps is eliminated entirely (replaced by its effect input):

```cpp
if (state->LookupMaps(object, &object_maps)) {
    if (maps.contains(object_maps)) return Replace(effect);  // line 794
}
```

At loop headers (`ReduceEffectPhi`, line 1262), `ComputeLoopState` (line 1363) walks all nodes in the loop body and conservatively kills map knowledge for any object whose map could change. Crucially, `CheckMaps` nodes are listed as NOT killing any state (line 1452-1455), so a CheckMaps at the loop entry propagates its knowledge through the entire loop body. If no `StoreField` to `kMapOffset` or `TransitionElementsKind` exists in the loop, the CheckMaps at the top of the loop is sufficient and all subsequent CheckMaps on the same object are eliminated.

**Mechanism B: Loop Peeling (Turboshaft)**

`LoopPeelingReducer` (file: `v8/src/compiler/turboshaft/loop-peeling-reducer.h:34`) explicitly states: "The goal of this is mainly to hoist checks out of the loop (such as Smi-checks, type-checks, bound-checks, etc)." Loop peeling extracts the first iteration, which moves all checks to the peeled prologue. Combined with Turboshaft's `LateLoadEliminationReducer`, redundant checks in the unpeeled body are eliminated.

**Mechanism C: Map inference through effect chains**

`NodeProperties::InferMapsUnsafe` (file: `v8/src/compiler/node-properties.cc:406`) walks backwards through the effect chain. When it encounters a `CheckMaps` or `MapGuard`, it returns the known maps for that object. This means any subsequent optimization that queries "what map does this object have?" will find the answer from the dominating CheckMaps, even across basic blocks.

### Key insight for GScript

V8 does NOT hoist CheckMaps. Instead, it makes CheckMaps at the loop header sufficient by ensuring the map knowledge propagates through the loop body. Subsequent redundant CheckMaps inside the loop are eliminated by load elimination. This is safer than hoisting because:
- No risk of deopt at the wrong PC
- The first iteration validates the map, all subsequent iterations benefit
- Map-changing stores in the loop body correctly invalidate knowledge

## 2. LuaJIT: Table Access Specialization

### IR for table access

LuaJIT represents table access with distinct IR ops for each access path (defined in `lj_ir.h:193-204`):

| IR Op | Purpose | Guards |
|-------|---------|--------|
| `FLOAD TAB_ASIZE` | Load table's array size | None (mutable field) |
| `FLOAD TAB_HMASK` | Load hash mask | None (mutable field) |
| `FLOAD TAB_ARRAY` | Load array data pointer | None (mutable field) |
| `FLOAD TAB_NODE` | Load hash node pointer | None (mutable field) |
| `FLOAD TAB_META` | Load metatable pointer | None (mutable field) |
| `IR_AREF` | Array element reference | Guarded bounds check via `IR_ABC` |
| `IR_HREF` | Hash lookup (dynamic key) | Guarded (may return niltv) |
| `IR_HREFK` | Hash lookup (constant key) | Guarded hmask check |

### How table access is recorded

`rec_idx_key` (file: `lj_record.c:1437`) dispatches based on the observed table state at recording time:

1. **Array path** (line 1456-1463): When `(MSize)k < t->asize`, emits:
   - `FLOAD TAB_ASIZE` to load asize
   - `IR_ABC` bounds check (guarded)
   - `FLOAD TAB_ARRAY` to load array data pointer
   - `IR_AREF` to compute element address
   
2. **Hash path with constant key** (line 1493-1506): When the key is constant and falls in the hash part:
   - `FLOAD TAB_HMASK` + `IR_EQ` guard (verifies hmask hasn't changed)
   - `FLOAD TAB_NODE` to load node array
   - `IR_HREFK` with computed slot index

3. **Empty array guard** (line 1474-1477): When `t->asize == 0`, emits `FLOAD TAB_ASIZE` + `IR_EQ 0` guard to ensure the array part stays empty.

### How LuaJIT avoids per-iteration table validation

LuaJIT uses **copy-substitution loop unrolling** (file: `lj_opt_loop.c:22-89`), NOT traditional LICM. This is the key architectural insight:

1. The recorded trace (one iteration) becomes the **pre-roll** (prologue).
2. The pre-roll is re-emitted through the FOLD/CSE pipeline with substituted operands.
3. When an instruction is re-emitted with the **same operands** as a pre-roll instruction, CSE matches it to the existing instruction. The guard is not re-emitted -- it's CSE'd to the pre-roll version.

Specifically (`lj_opt_loop.c:324-326`):
```c
if (irm_kind(lj_ir_mode[ir->o]) == IRM_N &&
    op1 == ir->op1 && op2 == ir->op2) {  /* Regular invariant ins? */
    subst[ins] = (IRRef1)ins;  /* Shortcut. */
```

For a loop like `for i=1,n do x = t[i] end`, the guards that are invariant include:
- **Type check on `t`** (that it's a table): CSE'd because `t` doesn't change
- **`FLOAD TAB_ASIZE`**: If no FSTORE to TAB_ASIZE in the trace, the FLOAD is CSE'd to the pre-roll's FLOAD
- **`FLOAD TAB_ARRAY`**: Same reasoning
- **hmask guard** (for hash accesses): CSE'd if hmask doesn't change

The **bounds check** (`IR_ABC`) is NOT hoisted because the index `i` changes each iteration.

### Alias analysis for table fields

`lj_opt_mem.c:570-618` implements FLOAD forwarding. Table field loads (`IRFL_TAB_META` through `IRFL_TAB_NOMM`) use table-specific alias analysis (`aa_table`, line 582-583). Fields at different offsets never alias (line 578-579). Same field on the same table is `ALIAS_MUST` (line 580-581).

For ALOAD/HLOAD (array/hash element loads), `aa_ahref` (line 104-158) performs key-based disambiguation:
- Same key, same table -> MUST alias
- Different constant keys -> NO alias  
- Array refs with different base+offset arithmetic -> NO alias
- Different key types for hash refs -> NO alias

## 3. Guard Hoisting for Invariant Table References

### V8's approach

V8 does NOT hoist CheckMaps (shape guards) out of loops. The strategy is:

1. **CheckMaps at loop entry**: The first CheckMaps inside the loop validates the object's map.
2. **Effect-chain propagation**: Load Elimination propagates map knowledge from that CheckMaps through the loop body via the abstract state.
3. **Redundant check elimination**: Subsequent CheckMaps on the same object within the loop are eliminated because the state already knows the maps.
4. **Loop body analysis**: `ComputeLoopState` (line 1363) scans the loop body. Only `StoreField` to `kMapOffset` (line 1349) kills map knowledge. Regular element stores, CheckMaps, and typed element stores do NOT kill maps (lines 1446-1456).

**Deopt safety**: Since CheckMaps is never hoisted, its FrameState always corresponds to the correct bytecode PC. This completely avoids the "deopt at wrong PC" problem.

### LuaJIT's approach

LuaJIT achieves the equivalent of guard hoisting through CSE during loop unrolling (not LICM):

1. The pre-roll contains all guards from the first iteration.
2. When the loop body is copy-substituted, invariant guards have the same operands as pre-roll guards.
3. CSE matches them -> the loop body references the pre-roll's guard, effectively "hoisting" it.
4. The guard fires in the pre-roll (before the loop), never inside the loop body.

**Deopt safety**: LuaJIT uses snapshots (not FrameStates). Each snapshot records the register/stack state at that point. The pre-roll's snapshot is valid because it was recorded at the actual execution point. Side exits from pre-roll guards jump to the interpreter with correct state.

### Risk: deopt at the wrong PC

- **V8**: No risk, because CheckMaps stays in place. It's eliminated (replaced by its effect), not moved.
- **LuaJIT**: No risk, because the pre-roll guard has its own snapshot. The loop body simply doesn't contain the guard at all (it was CSE'd away).
- **JSC (JavaScriptCore/FTL)**: Uses "abstract heap" categories for alias analysis. CheckStructure (equivalent of CheckMaps) is eliminated by B3's load elimination when the structure is known. Loop peeling achieves guard hoisting similar to V8 Turboshaft.

## 4. What GScript Should Do

### Current state

GScript already has:
- `OpGetField` with shape guard (type check + nil check + shapeID comparison) -- `emit_table.go:40-118`
- LICM that can hoist `OpGetField` when no `SetField`/`SetTable`/`Call` exists in the loop -- `pass_licm.go:222-237`
- Block-local shape guard dedup (`shapeVerified` map in emitter) -- `emit_table.go:55-66`
- Four-way `arrayKind` dispatch for `GetTable`/`SetTable`

### Recommended optimizations (ordered by impact)

**1. Load Elimination for shape guards (medium effort, high impact)**

Follow V8's approach: extend `LoadElimination` to track known shape IDs per table SSA value. When a `GetField` is encountered and the table's shape is already known (from a prior GetField or SetField on the same SSA value), eliminate the type check + shape guard. This works across blocks (not just within a single block like the current `shapeVerified` dedup).

Key: `ComputeLoopState` equivalent -- when scanning the loop body, only `SetField`, `SetTable`, and `Call` should kill shape knowledge.

**2. Separate type check from shape guard in IR (medium effort, prerequisite)**

Currently `OpGetField` bundles type-check + nil-check + shape-guard + field-load into one IR op. To enable per-component elimination, split into:
- `OpGuardIsTable(v) -> tablePtr` (type check + nil check + ptr extract)  
- `OpGuardShape(tablePtr, shapeID)` (shape validation)
- `OpLoadField(tablePtr, fieldIdx)` (raw field access)

This makes each piece independently subject to CSE, load elimination, and LICM.

**3. LICM for split guards (low effort after split)**

After splitting, `OpGuardIsTable` and `OpGuardShape` become hoistable when:
- The table SSA value is loop-invariant
- No in-loop operation can change the table's type or shape

This matches LuaJIT's approach (CSE eliminates invariant guards) and V8's approach (load elimination removes redundant checks).

**4. Array kind feedback specialization (medium effort, high impact for numeric loops)**

Record the observed `arrayKind` at each `GetTable`/`SetTable` site in the feedback profile. At Tier 2 compilation:
- If feedback says "always ArrayInt", emit only the int fast path + a kind guard
- The kind guard checks `table.arrayKind == ArrayInt` once per loop iteration
- The kind guard is hoistable (table ref invariant + no in-loop resize)

This eliminates the four-way dispatch branch on every element access.

**5. Metatable check hoisting (low effort)**

For inner loops accessing `t[i]` where `t` is loop-invariant, the metatable pointer load and nil-check (`t.metatable == nil`) is invariant. After IR splitting, this becomes a simple LICM candidate.

### Implementation order

1. Split OpGetField into GuardIsTable + GuardShape + LoadField (prerequisite)
2. Extend LoadElimination to track shape IDs across blocks
3. Add arrayKind to feedback profile, specialize GetTable/SetTable
4. LICM for GuardIsTable + GuardShape (falls out naturally from split)

### Source citations

| Engine | File | Lines | What |
|--------|------|-------|------|
| V8 | `js-native-context-specialization.cc` | 2148-2408 | ReduceElementAccess: feedback-driven element access lowering |
| V8 | `js-native-context-specialization.cc` | 3438-3534 | BuildElementAccess: element kind dispatch to specialized loads |
| V8 | `load-elimination.cc` | 786-799 | ReduceCheckMaps: eliminate if maps already known |
| V8 | `load-elimination.cc` | 1363-1460 | ComputeLoopState: conservative map kill across loop body |
| V8 | `node-properties.cc` | 406-485 | InferMapsUnsafe: walk effect chain to find dominating map info |
| V8 | `turboshaft/loop-peeling-reducer.h` | 34-151 | Loop peeling to hoist checks into prologue |
| V8 | `turboshaft/late-load-elimination-reducer.h` | 90-110 | AssumeMap-based aliasing with loop revisitation |
| LuaJIT | `lj_record.c` | 1437-1510 | rec_idx_key: array/hash path dispatch during recording |
| LuaJIT | `lj_opt_loop.c` | 22-89 | Loop optimization comment: why CSE-based "hoisting" beats LICM |
| LuaJIT | `lj_opt_loop.c` | 311-374 | loop_unroll: copy-substitution with CSE for invariant guards |
| LuaJIT | `lj_opt_mem.c` | 104-158 | aa_ahref: alias analysis for array/hash element access |
| LuaJIT | `lj_opt_mem.c` | 570-618 | FLOAD forwarding and field alias analysis |
| LuaJIT | `lj_opt_mem.c` | 935-968 | Previous value analysis to skip metatable checks |
| LuaJIT | `lj_ir.h` | 193-218 | IRFLDEF: table field offsets for FLOAD |
