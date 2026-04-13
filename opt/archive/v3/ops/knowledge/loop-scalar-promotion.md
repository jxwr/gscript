# Loop Scalar Promotion (Mem2Reg for Loops)

> Created: 2026-04-11 | Category: tier2_float_loop | Round: 32 (research)
> Technique: SROA / register promotion for loop-carried (obj, field) pairs

## Problem

nbody inner j-loop: `bi.vx`, `bi.vy`, `bi.vz` are loop-carried across j-iterations.
Each iteration: `GetField(bi, "vx") → sub → SetField(bi, "vx", result)`.
LICM cannot hoist because `SetField` exists (R18 finding). Block-local CSE already
eliminates duplicate loads within a single j-iteration body (R16 finding).
Store-to-load forwarding within the same block also exists (pass_load_elim.go:102).

**Remaining cost**: each j-iteration still pays `GetField(bi, "vx")` because the
value from iteration j-1's `SetField` is not forwarded to the j-th iteration's
`GetField`. This is a cross-iteration (cross-block via back-edge) store-to-load
dependency — the exact thing phi nodes model.

## Algorithm: Scalar Replacement / Register Promotion Pass

This is `promoteLoopAccessesToScalars` from LLVM `lib/Transforms/Scalar/LICM.cpp`
adapted for GScript's SSA CFG IR.

**Preconditions** (checked per (obj, field) pair):
1. The object SSA value is loop-invariant (defined outside the loop body)
2. The field is accessed ONLY via `OpGetField` and `OpSetField` (no `OpCall` in loop
   that could alias; no `OpSetTable` on same object)
3. The field has at least one `GetField` AND at least one `SetField` in the loop body
   (otherwise LICM hoisting alone is sufficient)
4. No loop exit path can observe the field through a pointer that escapes the loop
   (in GScript: no call in loop body satisfies this conservatively)

**Algorithm** (per qualifying (obj, field) pair):

```
LoopScalarPromote(loop, obj, field):
  preHeader = loop.preHeader  // created by LICM pass
  header    = loop.header
  exits     = loop.exitBlocks // blocks outside loop with pred inside loop

  // 1. Insert load in pre-header: initial value before first iteration
  initVal = new OpGetField(obj, field) → insert at end of preHeader

  // 2. Insert phi at loop header: carries the value across back-edges
  phi = new OpPhi(type=TypeFloat) at top of header
  phi.args[preHeaderEdge] = initVal

  // 3. Replace all in-loop OpGetField(obj, field) with phi
  for each instr in loop.body:
    if instr.Op == OpGetField && instr.Args[0] == obj && instr.Aux == field:
      replaceAllUsesInLoop(instr.ID, phi)
      removeInstr(instr)

  // 4. Collect the last SetField value on (obj, field) along each back-edge path
  //    In GScript's single-exit inner j-loop, this is the SetField value
  //    that dominates the back-edge.
  backEdgeVal = findLastSetField(loop.body, obj, field)
  phi.args[backEdge] = backEdgeVal.Args[1]  // the stored value

  // 5. Remove all in-loop OpSetField(obj, field) — they're now dead
  //    (value is carried in phi, no longer written to memory each iter)
  for each instr in loop.body:
    if instr.Op == OpSetField && instr.Args[0] == obj && instr.Aux == field:
      removeInstr(instr)

  // 6. Insert store at each loop exit: materialize the phi value back to memory
  for each exitEdge (pred inside loop, succ outside loop):
    insertBefore(exitEdge.succ.firstInstr):
      new OpSetField(obj, field, phi)  // phi holds the last carried value
```

**Phi wiring for multi-SetField per iteration** (e.g., nbody has exactly one
SetField per field per iteration): if there are multiple SetField on the same
(obj, field) in the loop body, find the one that post-dominates the back-edge
(i.e., the last write along every path to the back-edge). If no single dominator
exists, insert a phi at the back-edge join — but for nbody this is not needed.

**Key invariant**: after promotion, the (obj, field) memory location is not read or
written inside the loop. The phi carries the value. The exit store materializes it
once per loop completion.

## Alias-Analysis Assumptions

| Assumption | Justification for GScript |
|------------ |--------------------------|
| Object is monomorphic (fixed shape) | TypeSpec pass emits GuardType before GetField; shapeID is fixed |
| No calls in loop | `hasLoopCall` already computed in LICM; reject pair if true |
| No cross-function aliases | GScript has no pointers / references; table identity is by SSA value node |
| SetTable(obj, dynamic_key) may alias | fieldAux=-1 sentinel already kills all fields in LICM |
| Field index is compile-time constant | Aux field stores constant pool index for field name |

## Production JIT References

### LLVM `promoteLoopAccessesToScalars`
**Source**: `lib/Transforms/Scalar/LICM.cpp` (~line 1800 in LLVM 17)

Key steps in LLVM's implementation:
1. `collectPromotionCandidates`: finds (alloca/pointer, loop) pairs where the address
   is loop-invariant and all in-loop accesses are loads or stores (no unknown effects)
2. For each candidate: creates SSA phi at loop header, replaces in-loop loads with phi,
   removes in-loop stores, inserts store at each loop exit block
3. Uses `MemorySSA` for alias queries; GScript equivalent is the `setFields` map in LICM

The algorithm is identical in structure to what we need; LLVM's `LoopAccessInfo` plays
the role of GScript's `(obj, field)` pair identification.

### V8 TurboFan: No Direct Equivalent
V8's `LoadElimination` (`src/compiler/load-elimination.cc:1363`, `ComputeLoopState`)
kills fields that have a store in the loop, preventing forwarding — it does NOT do
scalar promotion (phi insertion). V8 handles this at a higher level via escape analysis
for heap-allocated objects, which sinks allocations and replaces fields with SSA values.
For non-escaping objects (like GScript's `bi` table), V8 would rely on the same
phi-insertion approach but this is done by `EscapeAnalysis` not `LoadElimination`.

**Escape Analysis** (`src/compiler/escape-analysis.cc`): analyzes whether an
object escapes its creating context. For objects that don't escape, it replaces
field loads/stores with direct SSA values. For GScript, `bi` is not created in the
loop and does escape (it's a global bodies table element), so full escape analysis
isn't applicable — but we can apply scalar promotion to the specific (obj, field)
pairs that are loop-carried.

### LuaJIT: lj_opt_loop.c
`lj_opt_loop.c:77-85` comment: "Load/store forwarding works across loop iterations.
`self.idx = self.idx + 1` may become a forwarded loop-recurrence after inlining."
LuaJIT achieves this via trace re-emission: re-emitting the trace through the
CSE/forwarding pipeline causes the second HLOAD to find the first HSTORE from the
prior iteration as a ALIAS_MUST match, returning the stored value directly. This is
equivalent to phi insertion but implicit in the trace IR's linear representation.

## Applicability to GScript Infrastructure

### What Already Exists (Reuse)

| Component | Location | Role in promotion |
|-----------|----------|-------------------|
| Pre-header creation | `pass_licm.go:337-380` | Step 1: insert initVal GetField here |
| `setFields` map | `pass_licm.go:173` | Identify qualifying pairs: has both GetField and SetField |
| `invariant` map | `pass_licm.go:141` | Confirm obj is loop-invariant |
| `hasLoopCall` flag | `pass_licm.go:175` | Alias safety gate |
| `loopInfo.loopPhis` | `loops.go:21` | Track newly inserted phi nodes |
| `replaceAllUses()` | `pass_load_elim.go:118` | Replace GetField uses with phi |
| `TypeFloat` | `ir.go:112` | Phi type for float fields |
| `CarryPreheaderInvariants` | `ir.go:55` | Signal regalloc to pin FPRs |
| `exitBoxPhis` | `emit_compile.go:40` | Store-at-exit materialization |

### What Is New (Must Implement)

1. **`LoopScalarPromotionPass`** (~120 LOC) — new pass `pass_scalar_promote.go`:
   - Identify qualifying (obj, field) pairs: invariant obj + SetField in body + GetField in body + no calls
   - Insert `OpGetField` in pre-header (initial load)
   - Insert `OpPhi(TypeFloat)` at loop header with pre-header edge = initVal
   - Replace all in-loop `OpGetField(obj, field)` with phi
   - Wire back-edge phi arg = last `SetField.Args[1]` along back-edge
   - Remove in-loop `OpSetField(obj, field)` (replaced by exit store)
   - Insert `OpSetField(obj, field, phi)` at each loop exit block entry

2. **Loop exit detection** — walk `li.headerBlocks[hdr]`, find edges (A → B) where
   A is in the loop body and B is not. These are the exit edges; insert stores there.

3. **Pipeline wiring** — run AFTER LICMPass (pre-header must exist), BEFORE RegAlloc.
   The promoted phi will naturally be allocated to an FPR by RegAlloc since its type
   is `TypeFloat` and `CarryPreheaderInvariants` is set.

4. **`pass_scalar_promote_test.go`** — TDD: construct nbody j-loop IR skeleton, verify
   that GetField(bi, vx/vy/vz) and SetField(bi, vx/vy/vz) are replaced by phi + exit store.

## Expected IR Transform: nbody Inner j-Loop

### Before (current):
```
; Pre-header of j-loop (bi is defined here):
;   bi = GetTable(bodies, i)

; j-loop header:
;   j_phi = Phi(j_init, j_next)

; j-loop body:
  bj      = GetTable(bodies, j_phi)
  bi_vx   = GetField(bi, "vx")        ; LOAD every iteration — 16 ARM64 insns
  ...arithmetic...
  new_vx  = SubFloat(bi_vx, ...)
  SetField(bi, "vx", new_vx)          ; STORE every iteration
  bi_vy   = GetField(bi, "vy")        ; LOAD every iteration
  ...
  SetField(bi, "vy", new_vy)
  bi_vz   = GetField(bi, "vz")
  ...
  SetField(bi, "vz", new_vz)
  ; back-edge to j-loop header
```

### After (with scalar promotion):
```
; Pre-header of j-loop:
  bi      = GetTable(bodies, i)
  init_vx = GetField(bi, "vx")        ; LOAD once before j-loop
  init_vy = GetField(bi, "vy")
  init_vz = GetField(bi, "vz")

; j-loop header:
  j_phi   = Phi(j_init, j_next)
  vx_phi  = Phi(init_vx, new_vx)     ; carries bi.vx in FPR — no memory
  vy_phi  = Phi(init_vy, new_vy)
  vz_phi  = Phi(init_vz, new_vz)

; j-loop body:
  bj      = GetTable(bodies, j_phi)
  ; vx_phi IS bi.vx — no GetField needed
  new_vx  = SubFloat(vx_phi, ...)    ; arithmetic on FPR value
  ; no SetField(bi, "vx") in loop body
  new_vy  = SubFloat(vy_phi, ...)
  new_vz  = SubFloat(vz_phi, ...)
  ; back-edge feeds new_vx → vx_phi, etc.

; j-loop exit (after j > n):
  SetField(bi, "vx", vx_phi)         ; STORE once after j-loop completes
  SetField(bi, "vy", vy_phi)
  SetField(bi, "vz", vz_phi)
```

**ARM64 impact**: eliminates 3 × GetField + 3 × SetField per j-iteration.
Each GetField is ~10-16 instructions (shape guard + LDR + NaN-unbox). Each SetField
is ~8-12 instructions (shape guard + NaN-box + STR). Conservatively 54-84 instructions
per j-iteration removed. At 5 bodies × ~4.5 average inner iterations: ~1200-1900
instructions removed per outer iteration. ARM64 M4 superscalar at 4-wide:
~300-475 cycles saved per outer iteration. Wall-time estimate: **15-25% on nbody**.

## Key Correctness Traps

1. **Exit store ordering**: the exit store must happen BEFORE any use of the field
   outside the loop (e.g., the outer i-loop position update reads `bi.vx`). Insert
   the exit store as the FIRST instruction in the exit block (before any GetField).

2. **Multiple exits**: the j-loop has exactly one exit (j > n). If a loop has multiple
   exits (e.g., early break), insert a store on EACH exit edge. If the exit edge is a
   critical edge (pred has multiple succs), split the edge first.

3. **Exception / deopt paths**: GScript's deopt (`ExitCode=2`) jumps to
   `deopt_epilogue` which restores from the VM register file. The promoted phi value
   must be written to the VM register file at deopt points. Current approach: the
   regalloc's write-through (`storeRawFloat`) ensures this. The promoted phi should
   be treated like any other loop phi for deopt purposes — `loopExitBoxPhis` already
   handles this for existing phis.

4. **Aliasing with bj fields**: `SetField(bj, "vx", ...)` must NOT kill the `bi.vx`
   phi. This is safe because `bj` is a different SSA value than `bi` (bj is defined
   inside the loop as `GetTable(bodies, j_phi)`; bi is defined outside). SSA identity
   is sufficient for alias separation.

5. **Shape mutation**: if a call inside the loop could change `bi`'s shape (add a new
   field, change its layout), the promoted GetField would use a stale shape check.
   Gate: `hasLoopCall = true` → skip promotion. This is already the LICM gate.

6. **Phi cycle in back-edge wiring**: the phi `vx_phi` references `new_vx`, which
   uses `vx_phi`. This is a valid SSA cycle (loop-carried dependence). The graph
   builder's `readVariable`/`writeVariable` mechanism handles this — use the same
   approach: write the phi value before looking up back-edge args.

## Implementation Plan (for R32)

**File**: `internal/methodjit/pass_scalar_promote.go` (new, ~120 LOC)
**Test**: `internal/methodjit/pass_scalar_promote_test.go` (new, ~200 LOC)

Pipeline position: in `pipeline.go`, run after `LICMPass` and before `RegAllocPass`.
Set `fn.CarryPreheaderInvariants = true` (already done by tiering_manager.go:465).

```go
func ScalarPromotionPass(fn *Function) (*Function, error)
```

Internal helpers:
- `findPromotablePairs(fn, li, hdr) []promoPair` — returns (obj SSA ID, fieldAux) pairs
- `insertLoopInitLoad(fn, ph, obj, field) *Instr` — GetField in pre-header
- `insertLoopPhi(fn, hdr, initVal) *Instr` — OpPhi at header top
- `replaceLoopGetFields(fn, body, obj, field, phi)` — substitute phi for all GetField
- `wireBackEdge(phi, lastSetVal)` — set phi's back-edge arg
- `removeLoopSetFields(fn, body, obj, field)` — strip SetField from body
- `insertExitStores(fn, li, hdr, obj, field, phi)` — SetField at each exit
```

## Sources

- LLVM `lib/Transforms/Scalar/LICM.cpp` — `promoteLoopAccessesToScalars` (~line 1800)
- V8 `src/compiler/escape-analysis.cc` — field replacement for non-escaping objects
- V8 `src/compiler/load-elimination.cc:1363` — `ComputeLoopState` (kills not promotes)
- LuaJIT `src/lj_opt_loop.c:77` — cross-iteration forwarding via trace re-emission
- GScript `internal/methodjit/pass_licm.go` — pre-header creation, setFields map
- GScript `internal/methodjit/pass_load_elim.go` — replaceAllUses, store-to-load forwarding
- GScript `internal/methodjit/emit_compile.go:40` — exitBoxPhis (model for exit stores)
