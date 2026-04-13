# Unboxed Loop SSA / Representation Selection

> Last Updated: 2026-04-06 | Rounds: (research only, not yet applied)

## Technique

Keep loop-carried values (phi nodes) in their native machine representation (raw int64 in GPR, raw float64 in FPR) across loop back-edges, eliminating per-iteration NaN-boxing. Only re-box at loop exits and deoptimization points.

## How V8 TurboFan Does It

**SimplifiedLowering** (`src/compiler/simplified-lowering.cc`) — 3-phase approach:

1. **PROPAGATE** (backward): Push `UseInfo` from uses to definitions. Each consumer says "I need Float64" or "I need Word32". Fixpoint iteration resolves loop phis.
2. **RETYPE** (forward): Intersect operation type with restriction_type from PROPAGATE.
3. **LOWER** (forward): Replace abstract nodes with machine-specific ones. Insert `ChangeTaggedToFloat64` / `ChangeFloat64ToTagged` conversion nodes at representation boundaries.

**Phi representation** determined by type:
- Signed32/Unsigned32 -> kWord32
- Number -> kFloat64
- Otherwise -> kTagged (default, NaN-boxed)

**Deopt**: `FrameState` nodes + `DeoptMachineTypeOf(representation, type)` tell deoptimizer how to reconstruct tagged values. Float64 values in FPRs trigger `MaterializeHeapNumber`.

**Key insight**: Fixpoint iteration over PROPAGATE naturally handles loop phis — back-edge uses constrain phi representation, which cycles until stable.

## How V8 Maglev Does It

**MaglevPhiRepresentationSelector** — separate post-graph-building pass:

- `ProcessPhi`: intersects "possible inputs" with "allowed uses"
- **Loop phi special handling**: internal loop uses override external uses to avoid deopt oscillation:
  ```cpp
  if (node->is_loop_phi() && !node->same_loop_use_repr_hints().empty()) {
    use_reprs = node->same_loop_use_repr_hints(); // same-loop uses only
  }
  ```
- Stack frame split into tagged + untagged regions (not per-slot tracking)
- `SnapshotTable` tracks retagging state at control flow merges

## How LuaJIT Does It

**All values unboxed on-trace**. Boxing only on trace exit via snapshot restoration.

- **Backward register allocation** (`lj_asm.c`): processes IR end-to-start, so PHI register preferences are known before header allocation
- **PHI shuffle** at loop back-edge: `asm_phi_shuffle()` resolves register mismatches, breaks cycles with temporaries
- **Snapshot restoration** (`lj_snap_restore()` in `lj_snap.c`): re-boxes each value using IRType:
  - Integers: `setintV(o, (int32_t)ex->gpr[r])`
  - Floats: `setnumV(o, ex->fpr[r])`
  - Handles sunk allocations (reconstructed on demand)
- **ExitState**: captures all GPR + FPR + spill slot values at exit

**Key insight**: "Optimistic boxing" — values always unboxed on hot path, cost of re-boxing paid only on cold path (trace exit).

## How SpiderMonkey IonMonkey Does It

- **TypePolicy**: each MIR instruction specifies accepted input types; `adjustInputs()` inserts `MUnbox`/`MBox` at boundaries
- **Phi specialization pass**: if all phi inputs have same type, phi specializes; back-edge handled specially
- **FoldLoadsWithUnbox**: fuses NaN-boxed load + unbox into single operation (practical win for NaN-boxed runtimes)
- **Range Analysis -> Truncate Doubles**: strength-reduces double arithmetic to int32 when range proves safe

## LLVM Spill Cost Heuristics

**`CalcSpillWeights.cpp`**:
```
weight = (isDef + isUse) * blockFreqRelativeToEntry
```
- Block frequency embeds loop depth: `block_freq = mass * product(containing_loop_scales)`
- Loop-exiting writes get **3x multiplier**
- Rematerializable intervals get **0.5x discount**
- `looksLikeLoopIV()` in RegAllocGreedy adds extra protection for induction variables

## Key Cross-Cutting Themes

1. **Representation selection is a separate phase** from register allocation (all engines)
2. **Loop phis need fixpoint or two-pass** — back-edge inputs not available when phi first created
3. **Deopt metadata must track representation** — every engine stores whether each slot is boxed or raw
4. **Minimize moves at loop back-edges** — parallel move resolver breaks register cycles
5. **Spill cost should reflect loop depth** — LLVM's `blockFreq * uses` is the gold standard

## Applicability to GScript

GScript's current approach:
- TypeSpecialize pass runs fixpoint on phi types (up to 10 iterations) — handles loop phi convergence
- `rawIntRegs` / `activeFPRegs` track unboxed state within blocks
- `storeRawFloat` / `storeRawInt` write-through to memory for cross-block values (deopt safety)
- `loopPhiOnlyArgs` optimization skips write-through for phi-only values

**What's missing** (for future rounds, after feedback-typed loads):
1. **Deopt frame descriptors**: Currently, write-through to VM regs on every cross-block value ensures interpreter can resume. To eliminate write-through, need per-guard metadata saying "value X is in register R, type T" (like V8's FrameState).
2. **Loop-exit boxing**: Only re-box when exiting the loop (like LuaJIT's snapshot restore). Requires identifying all loop exits and emitting boxing code there.
3. **Spill cost by loop depth**: Current LRU eviction ignores loop depth. Even a simple `cost *= 10` for loop-interior values would help.

**Estimated effort**: Deopt frame descriptors are a medium-large architectural change (~500-800 lines). Loop-exit boxing is a smaller change building on existing `loopExitBoxPhis` infrastructure. Spill cost weighting is a small change to regalloc.go.
