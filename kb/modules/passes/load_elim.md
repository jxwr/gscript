---
module: passes/load_elim
description: Block-local load elimination (GetField CSE) + store-to-load forwarding + GuardType CSE. Conservative on calls.
files:
  - path: internal/methodjit/pass_load_elim.go
  - path: internal/methodjit/pass_load_elim_test.go
last_verified: 2026-04-13
---

# LoadElim Pass

## Purpose

Eliminate redundant `OpGetField` loads within a single basic block by tracking an "available" map keyed on `(object value ID, field Aux)`. When a GetField matches an available entry, all uses of the redundant GetField are rewritten to the original value, making the redundant instruction dead for `DCE`. Also forwards stored values (`OpSetField` → subsequent `OpGetField`) and de-duplicates `OpGuardType` on the same value/type pair within a block.

## Public API

- `func LoadEliminationPass(fn *Function) (*Function, error)`
- `type loadKey struct { objID int; fieldAux int64 }` — field-load identity
- `type guardKey struct { argID int; guardType int64 }` — guard identity

## Invariants

- **MUST**: the pass is block-local — a fresh `available` map and `guardAvail` map are allocated per block, so nothing propagates across block boundaries.
- **MUST**: on `OpGetField`, the key is `loadKey{objID: Args[0].ID, fieldAux: Aux}`. If the key is in `available`, `replaceAllUses(fn, instr.ID, origInstr)` rewrites every user; the redundant GetField is then dead and will be removed by `DCEPass`.
- **MUST**: on `OpSetField(obj, field, val)`: first `delete(available, key)` (the old stored value is no longer the one at that slot), then record `available[key] = val.ID` so a later GetField on the same pair forwards to the stored value.
- **MUST**: on `OpCall` or `OpSelf`: clear BOTH `available` and `guardAvail` — a call can mutate any table or change any runtime type.
- **MUST**: on redundant `OpGuardType` hit, the duplicate guard is rewritten to `OpNop` (`instr.Op = OpNop; Args = nil; Aux = 0`) — because `DCE` treats `OpGuardType` as side-effecting it would otherwise be kept.
- **MUST**: `replaceAllUses` walks every block's instructions and rewrites any `Args[i]` whose `ID == oldID` to point to the new `Value`. It is the single canonical use-rewriting helper for the methodjit package.
- **MUST NOT**: forward a stored value across a call — the call clears the map before the following GetField is considered.
- **MUST NOT**: cross block boundaries. `OpGetTable` with a dynamic key is not tracked at all (only `OpGetField` with a fixed `Aux` index).

## Hot paths

- `nbody` — position/velocity field loads are the inner loop. Redundant `p.x` / `p.y` / `p.z` loads in the distance computation fold away, halving memory traffic.
- `object_creation` — feedback-inserted `GuardType` at every field access deduplicates.
- `mandelbrot` — complex-number struct field loads fold within the inner iteration.
- `method_dispatch` — GetField for the method lookup is invariant after the first dispatch in the block.

## Known gaps

- **Block-local only.** A GetField in block A whose result is still valid in block B is re-loaded in B. A dedicated GCSE / PRE pass would subsume this; the current design relies on LICM hoisting loop-invariant loads instead.
- **No aliasing model for tables.** `OpSetTable` on any table conservatively kills only through the call path (OpCall), not via aliasing. (LICM does handle this separately for the hoist-safety check.)
- **No cross-block guard CSE.** GuardType on a parameter is re-guarded at every block.
- **No `OpGetTable` elimination.** Only `OpGetField` (constant-keyed) is tracked; dynamic-key loads go through every time.
- **Dynamic stores kill entire object.** A single SetField kills only its own key, but the pass does not model which instance of an object is actually pointed to — if two SSA values alias, invalidation is unsound in principle. In practice, the SSA from BuildGraph does not create aliases within a block.

## Tests

- `pass_load_elim_test.go` — redundant GetField, store-to-load forwarding, call-kills-map, GuardType CSE producing OpNop, no cross-block leakage.
