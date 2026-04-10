# Tier 1 Integer Specialization — Forward KnownInt Tracking

> Created: 2026-04-10 | Category: tier1_dispatch | Round 24, `2026-04-10-tier1-int-spec`)
> Status: active design doc
> Owner: Tier 1 baseline JIT

## Problem

Tier 1 is a stateless template compiler: every `OP_ADD`/`OP_SUB`/`OP_MUL`/`OP_EQ`/`OP_LT`/`OP_LE`
emits the full polymorphic template with int-tag dispatch (`LSR #48 → MOVimm16 → CMPreg → BCond`)
repeated twice (once per operand), then the int fast-path, then the float fallback.

On ackermann, the 4 dispatch sequences per recursive call (2× EQ + 2× SUB) amount to ~114 ARM64
instructions per call × 67M calls. The calls are all `ack(int,int)` in practice.

Tier 2 is blocked: `recursive_call` category is ceiling-blocked (failures=2). So we must
specialize *inside* Tier 1 — push a sliver of type state through the linear bytecode scan.

## Algorithm

A single forward linear scan of the function's bytecode computes, for each PC, the set of
VM register slots known to hold an int48-boxed integer *before* that instruction executes.

### Eligibility gate (pre-scan)

Return `(_, false)` if any of the following is true:

1. Any instruction is `OP_CONCAT`, `OP_LEN`, `OP_POW`, `OP_DIV`, or `OP_MOD`. These imply
   non-int operands or non-int results, or (for DIV) always produce a float. The conservative
   choice is to skip specialization entirely rather than reason about where these taint the
   register file.
2. Any `OP_LOADK` loads a non-int constant (float64 or string value) into a slot that later
   flows into an arith op. The simplest conservative policy: reject if *any* `OP_LOADK`
   loads a non-int constant. Floats and strings in the constant pool are rare in the target
   benchmarks (ack, fib, mutual_recursion) — they use `OP_LOADINT` for literals ≤ 32767.

In the eligibility gate we scan the constant pool once (via the proto's `Constants` slice)
and reject if any non-int constant would end up loaded.

### Forward scan

State: `known map[int]bool` — currently live KnownInt slots (one set; not snapshot-per-PC).
Output: `perPC []bitset` — `perPC[pc]` is the snapshot of `known` *before* PC executes.

Initial `known` = `{0, 1, ..., NumParams-1}`. Parameters are assumed int; an entry guard at
the top of the function enforces this.

Per-instruction transfer function (covers the opcodes the target benchmarks actually use):

| Opcode | Transfer |
|---|---|
| `OP_LOADINT A sBx` | add A |
| `OP_LOADK A Bx` | if constant[Bx] is int → add A, else → remove A |
| `OP_MOVE A B` | if B in known → add A, else → remove A |
| `OP_ADD/SUB/MUL A B C` | if `isKnownIntOperand(B) && isKnownIntOperand(C)` → add A, else → remove A |
| `OP_EQ/LT/LE` | no effect on register file (pure compare + skip) |
| `OP_JMP` | no effect on register file |
| `OP_CALL A B C` | remove A..A+C-2 (return slots are untracked). If C=0 (use top), conservatively remove all slots ≥ A. |
| `OP_RETURN` | end of block; subsequent PCs are either unreachable-in-straight-line or branch targets |
| `OP_GETGLOBAL A Bx` | remove A (globals aren't tracked) |
| `OP_LOADNIL A B` | remove A..A+B |
| Any other write to A | remove A (conservative default) |

`isKnownIntOperand(idx)`:
  - `idx >= RKBit`: look up `constants[idx - RKBit]`, check if int.
  - else: return `idx in known`.

### Branch-target correctness

The linear scan doesn't do a fixed-point join. To stay correct, at every PC that is a
**branch target** (reached from a forward or backward JMP, or the fall-through of a
conditional skip), we clear `known` to `∅` before processing that PC.

To find branch targets cheaply: a first pre-pass records every `targetPC` for `OP_JMP`
and every `pc+2` for the conditional ops (`OP_EQ`/`OP_LT`/`OP_LE`/`OP_TEST`/`OP_TESTSET`).

This is over-conservative (we lose info across block boundaries even when both predecessors
agree). It's also fine for the target benchmarks: `ack`'s entire body is a 25-instruction
straight-line sequence *with two early-return branches*. The branches leave the function
entirely, so the fall-through continues with all params+locals still known-int.

For ack the KnownInt set stays `{0, 1}` across all the EQs and SUBs — exactly what we need.

## Expected coverage

Manual trace of ack bytecode (from TestDumpTier1_AckermannBody):

| pc | op | known (before) | notes |
|---|---|---|---|
| 0 | LOADINT R(2)=0 | {0,1} | |
| 1 | EQ R(0) R(2) | {0,1,2} | **int-spec EQ** (both known) |
| 2 | JMP | {0,1,2} | |
| 3 | LOADINT R(3)=1 | {0,1,2} | (pc=3 is branch target if not equal → cleared to ∅) |
| 4 | ADD R(2)=R(1)+R(3) | {0,1,2,3} | **int-spec ADD** |
| 5 | RETURN | {0,1,2,3} | |
| 6 | LOADINT R(2)=0 | ∅ (after JMP target reset) | Rebuild needed |
| ... | | | |

**Note on the reset**: pc=6 loses `{0,1}`. That's a problem. We need a smarter rule:
**fall-through from an unconditional JMP** is a branch target, but **fall-through past a
`OP_EQ/LT/LE` skip** is also a branch target. Since the target for the `if m == 0` branch is
the return at pc=5, control flow past pc=5 can only come from the `OP_EQ` falling through,
which means params `{0,1}` are still valid there.

**Simplification**: rather than track this, we do a **second pass** over the bytecode that
also propagates the post-JMP state at the jump source to the jump target via intersection,
iterating until fixpoint. But the plan's Task 1 rule says *linear, no fixed-point*. So:

**Compromise**: include params `{0..NumParams-1}` in `known` at every branch target. Params
never get written (they'd need MOVE/LOADINT to a param slot with a non-int source). We verify
this per-proto: during the scan, if any instruction *writes* a param slot with a non-KnownInt
value, we mark the proto ineligible.

With this rule, the ack coverage becomes:

| pc | op | specialization | dispatch insns saved |
|---|---|---|---|
| 0 | LOADINT R(2)=0 | — | 0 |
| 1 | EQ R(0)==R(2) | **int-spec** | ~25 |
| 4 | ADD R(2)=R(1)+R(3) | **int-spec** | ~10 |
| 7 | EQ R(1)==R(2) | **int-spec** | ~25 |
| 11 | SUB R(3)=R(0)-R(4) | **int-spec** (R(0) is param) | ~10 |
| 17 | SUB R(3)=R(0)-R(4) | **int-spec** | ~10 |
| 21 | SUB R(6)=R(1)-R(7) | **int-spec** (R(1) is param) | ~10 |

Total: ~90 dispatch insns saved per recursive-call body. Over 67M calls: **~6.0B fewer
instructions**. Calibrated (halved for M4 superscalar): ~3.0B → at 3 GHz blended 3 IPC,
about **330 ms saved**. Reality check: this is still optimistic because half of those
dispatch branches predict well. Realistic: **60–120 ms**, putting ackermann at 0.48–0.54s.

## New templates to add

### `emitBaselineArithIntSpec(asm, inst, op)`

Skip the two 4-insn dispatch blocks. Go directly into SBFX extract → arith op → int48
overflow check → box → store. Overflow still falls back to float.

Estimated insns: ~12 (vs 22 generic).

### `emitBaselineEQIntSpec(asm, inst, pc, code)`

- Fast path: raw CMPreg → BCond to skip/done. If equal, done. If not equal, we can directly
  branch to skip/done without checking "are they numbers" (they both *are* ints).
- Estimated: ~6 insns (vs ~35 generic).

### `emitBaselineLTIntSpec(asm, inst, pc, code)` / `LEIntSpec`

- SBFX both, CMPreg, BCond. No string-slow-path check (both known int).
- Estimated: ~8 insns (vs ~20–24 generic).

### `emitParamIntGuards(asm, numParams)`

At function entry (after prologue, before `pc_0`): for each param slot, LSR+MOVimm16+CMPreg+BCond
to a deopt label. The deopt label exits to Go with a dedicated `ExitBaselineParamTypeGuard`
exit code. The Go handler re-executes the call path through Tier 0 (interpreter).

Wait — there's no such exit code yet. **Simpler**: on guard failure, just fall back into the
generic templates *within the same function*. But that means we'd need TWO entry points, or
two copies of the body. Neither is a small change.

**Alternative**: skip the param guard entirely and instead make the Spec templates
**self-guarding** — they check the tag on first use, and if non-int, take a cold fallback
path. That's just the same as the generic templates and saves nothing.

**Chosen approach**: use an exit to Go. Add `ExitBaselineIntSpecGuardFail`. The Go handler
treats it like a deopt: mark the proto `IntSpecDisabled`, recompile without spec, re-enter.
That's a one-time cost at the first non-int call. For ack, where all real calls are
`ack(int, int)`, the guard never fires.

**Deferred to Task 2**: if wiring the deopt path is more than ~50 lines, fall back to
self-guarding templates (each spec template has its own inline tag check that branches to
the same Go-exit on mismatch). This keeps the code path simple even if it gives up some
of the saving.

### Wiring in `tier1_compile.go`

```go
// after prologue, before the bytecode loop:
intInfo, intSpecEnabled := computeKnownIntSlots(proto)
if intSpecEnabled {
    emitParamIntGuards(asm, proto.NumParams)
}

// inside the switch:
case vm.OP_ADD:
    if intSpecEnabled && intInfo.bothKnownInt(pc, inst) {
        emitBaselineArithIntSpec(asm, inst, "add")
    } else {
        emitBaselineArith(asm, inst, "add")
    }
```

## Risks

- **Param slot reassignment**: if the function reassigns a param slot with a non-int value
  and then uses it in arith, specialization is wrong. The scan must detect this case and
  reject the proto. (The "any write to a param slot of non-known source" rule above.)
- **Float arith result stored into a KnownInt slot**: if ADD overflows and writes a float,
  subsequent uses of that slot see a float, not an int. Mitigation: `emitBaselineArithIntSpec`
  on overflow takes the same fallback path (`SCVTF → store float`), and then *removes* the
  dest slot from KnownInt for subsequent PCs. The forward scan already handles this: we only
  add A to known if the op is guaranteed to produce int. But overflow→float happens at
  runtime, not statically. So we must either: (a) disable spec for any slot where a write
  could overflow (rare in ack — all values tiny), or (b) keep A in known but have the
  overflow path write a sentinel that the next reader checks.

  **Chosen**: option (a) is too conservative (ack values ≤ ~65535). Option (b) is complex.
  **Simplest correct**: after an int-spec arith overflow, exit to Go (via the same deopt
  path as param guard). For ack's tiny values (m≤3, n≤32765) this never fires. Document as
  a limitation for now.

- **Branch target params assumption**: we claim params are still known-int at branch targets.
  This holds only if no intervening instruction wrote a param slot non-monotonically. The
  pre-scan checks this and rejects protos that fail.

## Files produced in Task 1

- `internal/methodjit/tier1_int_analysis.go` — types + stub for `computeKnownIntSlots`
- `internal/methodjit/tier1_int_analysis_test.go` — failing test: `TestKnownIntAnalysis_Skeleton`
- `opt/knowledge/tier1-int-spec.md` — this doc

No production behavior changes in Task 1. Task 2 implements the algorithm and wires it.
