# ADR — Self Tail Call Optimization (Self-TCO, Session 5 / R169)

**Status**: open, arc in flight (R168-R?).
**Date**: 2026-04-21.
**Supersedes**: none.

## Context

Ackermann (ack) is at 65× LuaJIT's JIT with the body dominated by
the recursive pattern `return ack(m-1, ack(m, n-1))` where the
outer call is in tail position but the inner is not. Mutual
recursion (mut_recursion, 45× gap) has similar structure across
two protos. R107 already ships a frame-replacing tail-call emit
(`emitCallNativeTail`) that covers generic callees; for the
static-self case it still pays ~55 insns per recursive call
(IC + bounds + CallCount + ctx + full epilogue + BR + callee's
full prologue).

Self-TCO — converting `return self(new_args)` into
`param_slots = new_args; goto entry_block` — collapses this to
~5-7 insns. For ack's outer call (~50% of recursive calls are
tail), the expected payoff is 20-40% wall-time reduction,
possibly up to 2-3× if depth-reduction compounds I-cache and
branch-prediction wins.

## Decision

Ship Self-TCO as a new dispatch fork in `emitOpCall`:

```go
func (ec *emitContext) emitOpCall(instr *Instr) {
    tail := ec.tailCallInstrs[instr.ID]
    self := ec.fn.Proto.HasSelfCalls && ec.isStaticSelfCall(instr)
    switch {
    case tail && self:
        ec.emitCallNativeTailSelf(instr)  // NEW (Self-TCO)
    case tail:
        ec.emitCallNativeTail(instr)      // R107 frame-replacing
    default:
        ec.emitCallNative(instr)          // generic
    }
    // ... existing cache invalidation ...
}
```

### `emitCallNativeTailSelf` — design

```
Step 1: resolve new arg values.
  For each instr.Args[1..N], use resolveRawInt or resolveValueNB.
  Result: each arg available in a scratch register OR directly
  in its current SSA physical register.

Step 2: parallel move into param slots.
  Target: regs[0..N-1] = new arg values.
  Use a swap-based parallel move to handle aliasing:
    - Topological-sort writes; cyclic dependencies resolved via
      X3 scratch.
  Writes as STR to memory (not to register aliases) since B0's
  LoadSlot will re-read.

Step 3: clear emit-state that depends on caller iteration.
  Since we're looping back to B0 at RUNTIME, the SSA values from
  THIS invocation are dead. At EMIT TIME we're leaving this
  block — the subsequent OpReturn's emit can be skipped (it's
  dead code after the B).

Step 4: emit `asm.B(blockLabelFor(ec.fn.Entry))`.
  Same label mechanism loops use.

Step 5: skip emission of the following OpReturn.
  emitOpCall already handles this semantic (the return after a
  tail call is dead, but is kept for slow-path correctness in
  the current R107 design). For Self-TCO the slow-path fallback
  is `emitCallNative` (generic; non-tail). We need to emit it
  inline as a guarded slow path.
```

### Correctness invariants

1. **GuardType re-fires at B0**. The entry block begins with
   GuardType ops on each LoadSlot. After Self-TCO writes new
   arg values to param slots, B0's re-entry sees those values,
   LoadSlots them, GuardTypes them. For same-signature
   recursion (ack's m/n are both ints throughout) this re-check
   always passes. If somehow the types changed (impossible for
   ack's static shape), normal deopt fires. No new hazard.

2. **mRegRegs/mRegConsts/mRegCtx unchanged**. B0 does not
   reload these (only `t2_direct_entry` does — and we're not
   jumping there). Our pinned registers remain valid across
   the B.

3. **rawIntRegs emit-state preserved**. The emit-time map
   tracks which SSA values are raw-int-typed. Jumping back to
   B0 at runtime doesn't affect emit state — B0's LoadSlot
   emit already handles raw/boxed per its own analysis.

4. **Slow-path fallback**. When IC check or type guard fails
   in the emitted code (runtime), fall back to the generic
   `emitCallNative` path. The post-Self-TCO OpReturn is
   normally dead but reachable via slow-path-fallback. Same
   pattern as R107.

### Parallel move algorithm

For `return ack(m-1, inner_result)`:
- Arg 1 = `m-1` = SubInt(v32, v7). SSA value in X22 (or wherever
  regalloc put it). Target slot: regs[0] (param m).
- Arg 2 = `inner_result` = Call v28. SSA value in X23. Target:
  regs[1] (param n).

If neither arg reads from its target slot, simple sequential STR.
If Arg 1's source SSA value is in regs[0] (stale m), it's
harmless — we're ABOUT to overwrite regs[0] and we already have
the new value in a register. If Arg 1's source is derived from
something in regs[1] (e.g., computed from n), we must read regs[1]
BEFORE overwriting it. Standard parallel-move topological sort:

```
for each target t in order:
  if source[t] is a slot in the target set AND not yet written:
    defer until after dependency
  else: emit STR source[t] → regs[t]
finally: resolve cycles with X3 scratch (2-cycle at most)
```

For ack/fib/mut, the arg shapes are small (N ≤ 3) and cycles are
rare; a simpler greedy algorithm + fallback to explicit
save-to-X3 handles it.

### Halt conditions (trigger halt + Session close)

1. **Cost-model violation**: R171 pre-flight (hand-written ack
   with goto-B0 spliced) must show ≥15% wall-time drop vs
   baseline. If not, halt — cost model was wrong.

2. **Correctness regression**: any existing methodjit test red
   after Self-TCO lands. Halt + revert.

3. **Ack bench regression**: post-R175 ack 5-sample median must
   be < R163 baseline's 0.416s by ≥3%. If flat or worse, halt.

4. **Scope breach**: cumulative insn added to emit > 200 LOC
   (MVP). Split into multi-arc if overshooting.

## Consequences

If Session 5 lands cleanly:
- Ack: 0.416s → ~0.25-0.30s (estimated 30-40% improvement, matches
  Schwaighofer 2009 + half-recursion-depth hypothesis).
- Mut_recursion: unchanged (needs IP-TCO extension, future arc).
- Fib: unchanged (no tail calls in fib's shape).
- Depth stress tests: ack with larger m may now complete in less
  recursion (Goto fused outer calls).

If Session 5 halts early:
- No harm — emitCallNativeTail unchanged, only new fork added.

## Non-decisions

- **IP-TCO / mutual-recursion merge**: deferred to future arc
  (Session 6 candidate). Self-TCO is the prerequisite.
- **General tail-call type specialization**: deferred. Self-TCO
  exploits that the target IS this proto; no cross-proto type
  reasoning needed.
- **Combining with R107 frame-replace**: R107 stays for cross-
  proto tail calls. Self-TCO replaces it only for the static-self
  subset.

## Sources

- R168 audit (this session).
- Schwaighofer 2009, "Tail Call Optimization in the Java HotSpot
  VM" — PLDI-style per-call insn savings.
- R107 commit logs (computeTailCalls, emitCallNativeTail).
- V8 WebAssembly tail call docs (`v8.dev/blog/wasm-tail-call`).
