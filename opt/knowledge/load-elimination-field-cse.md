# Load Elimination / Field CSE

> Last Updated: 2026-04-06 | Round: 16 (analysis)

## Technique

Eliminate redundant `GetField(obj, field)` operations within a basic block when the same object+field has already been loaded with no intervening `SetField` to the same field on the same object.

## How V8 Does It

**LoadElimination** (`src/compiler/load-elimination.cc`) — separate optimization pass:

1. Maintains "abstract state" per program point: a map from (object, field-offset) → known-value
2. On `LoadField(obj, +offset)`: check state. If (obj, offset) has a known value, replace the load with the known value (CSE).
3. On `StoreField(obj, +offset, val)`: update state — (obj, offset) → val. Also enables store-to-load forwarding.
4. On calls or escaping operations: kill entries conservatively (the callee might modify any field).
5. Different fields on the same object don't alias (V8 tracks by Map+offset, Maps are immutable).
6. Runs after inlining, before SimplifiedLowering.

**Key insight**: V8 doesn't need a separate alias analysis because `Map` (hidden class) objects are immutable. If two loads have the same Map+offset, they access the same field. GScript's shapeID is analogous.

## How LuaJIT Does It

**`lj_opt_mem.c`** — load forwarding during trace optimization:

- `lj_opt_fwd_hload()`: walks backward from current HLOAD to find a prior HLOAD or HSTORE on the same table+key
- Uses alias analysis to determine if intervening stores might have changed the value
- If found, substitutes the prior value (CSE)
- Works across the entire trace (not just basic blocks), but traces are mostly linear

## How JSC Does It

**CSEPhase** (`Source/JavaScriptCore/dfg/DFGCSEPhase.cpp`):

- Hashes `GetByOffset` nodes by (base-object, offset) for block-local CSE
- More aggressive than GScript needs: handles heap cell types, butterfly array accesses, etc.

## Applicability to GScript

### What to implement (Round 16)

Block-local only (simplest correct version):

```
For each basic block:
  available = map[(objID, fieldAux) → valueID]
  For each instruction in order:
    OpGetField(obj, fieldAux):
      key = (obj.ID, fieldAux)
      if key in available:
        replace all uses of this result with available[key]
        mark dead (DCE removes)
      else:
        available[key] = this.ID
    OpSetField(obj, fieldAux, val):
      delete available[(obj.ID, fieldAux)]
      // Optionally: available[(obj.ID, fieldAux)] = val.ID (store-to-load forwarding)
```

### Why block-local is sufficient for nbody

nbody's inner loop body (`for j = i+1; j <= n; j++`) is a single basic block. All redundant GETFIELD ops (bj.mass×3, bi.mass×3) are within this block. Cross-block elimination is unnecessary for this round.

### Future extensions

1. **Store-to-load forwarding**: After `SetField(obj, "vx", newVal)`, a subsequent `GetField(obj, "vx")` returns `newVal` without memory access.
2. **Cross-block (dominator-based)**: If block A dominates block B and A loads `obj.field` with no aliasing store on any path A→B, B can reuse A's value.
3. **Shape check CSE**: If `GetField(obj, "x")` already verified obj's shapeID, subsequent `GetField(obj, "y")` on the same `obj` can skip the shape check.

### Impact estimate for nbody

Per inner j-iteration of advance():
- bj.mass: loaded at lines 72, 73, 74 → 3 GETFIELD → CSE saves 2 (32 insns)
- bi.mass: loaded at lines 75, 76, 77 → 3 GETFIELD → CSE saves 2 (32 insns)
- Total: 4 redundant GETFIELD eliminated → 64 insns/iter saved
- 64/500 = 12.8% instruction reduction, halved for superscalar ≈ 6-8% wall-time
