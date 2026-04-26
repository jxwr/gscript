# Raw-int self-recursive ABI design

Date: 2026-04-26

## Goal

Add a general raw-int self-recursive calling convention for Tier 2 method JIT.
The first production slice must make Ackermann-like nested recursion correct and
measurably faster without relying on Ackermann-specific IR or bytecode patterns.

The ABI is intentionally limited to protos accepted by `AnalyzeSpecializedABI`:
fixed 1..4 int parameters, one raw-int result, static self calls only, and no
side-effecting opcodes. That purity condition is part of the fallback protocol:
if a raw callee exits, v1 restarts the whole call through the boxed VM call path
instead of resuming the native callee frame.

## Non-goals for v1

- Do not make `DirectEntryPtr` point at a raw entry. It must remain the boxed
  Tier 1 / Tier 2 direct-entry ABI.
- Do not store raw values in `ExecContext.BaselineReturnValue`.
- Do not teach Go call-exit handlers about raw arguments.
- Do not make the boxed public direct entry thin. Only the private raw-int
  self entry may use a thin frame, and only because raw callers now own
  live-register preservation.
- Do not resurrect `OpStaticSelfCall` or add Ack-specific lowering in the first
  slice.
- Do not exactly resume a raw callee frame after callee exit in v1.

## Two ABIs

Tier 2 must keep two explicit call conventions.

### Boxed VM ABI

Entry points:

- `t2_direct_entry`
- `t2_self_entry`

Arguments live in the VM register file. Return writes:

- `regs[0]`
- `ctx.BaselineReturnValue`

The caller reads a NaN-boxed result through the existing boxed convention.

### Raw-int self ABI

Entry point:

- `t2_numeric_self_entry_N`, where `N` is 1..4.

Arguments live in `X0..X(N-1)` as signed raw int64 values. Return writes:

- `X0` raw int64
- `ctx.ExitCode = 0`

It does not write `ctx.BaselineReturnValue` and does not write `regs[0]` on the
success path. The raw caller consumes `X0` immediately and stores it with
`storeRawInt`.

## Compile-time descriptor

`AnalyzeSpecializedABI` is the source of truth; `qualifyForNumeric` is now a
compatibility wrapper used by older call sites. The next cleanup is to carry a
per-function descriptor directly through codegen:

```go
type RawIntSelfABI struct {
    Eligible bool
    NumParams int
    ParamSlots []int
    Return SpecializedABIReturnRep
}
```

Each raw self call site also needs a call descriptor:

```go
type RawIntSelfCallPlan struct {
    InstrID int
    FuncSlot int
    NumArgs int
    ResultID int
    LiveGPRs map[int]bool
    LiveFPRs map[int]bool
    PreRawIntRegs map[int]bool
    PostRawIntRegs map[int]bool
}
```

`PreRawIntRegs` is the register representation before the call. `PostRawIntRegs`
is the representation expected at the merge point after success or fallback.
For v1, `PostRawIntRegs` is `PreRawIntRegs` plus the call result marked raw.

## Entry protocol

Before `BL t2_numeric_self_entry_N`, the caller must guarantee:

- `X0..X(N-1)` contain raw int64 arguments.
- The caller has captured the boxed function operand for fallback.
- `mRegCtx` is the same `ExecContext` pointer as the caller.
- `mRegTagInt` and `mRegTagBool` are valid pinned constants.
- `mRegRegs` has already been advanced to the callee VM frame base. The raw
  entry consumes this pinned register directly; it does not reload `ctx.Regs`.
- `ctx.Regs` may still point at the caller VM frame during the raw BL. Before
  any fallback to Go, the caller restores the caller base into `ctx.Regs`, so
  boxed resume handlers see normal VM ABI state.
- `ctx.BaselineClosurePtr` and `mRegConsts` remain invariant for v1 raw self
  calls. `AnalyzeSpecializedABI` rejects upvalues and nested protos, so the
  self closure/constant domain does not change on the success path.
- The caller saves/restores `ctx.CallMode`; numeric return does not use it
  because it branches to `num_epilogue`.
- `ctx.NativeCallDepth` has been incremented.

The raw entry uses a thin FP/LR frame. It does not preserve the allocator's
callee-saved registers (`X20..X23`, `X28`, `D4..D11`). The raw caller therefore
must spill every allocated value that is live across the call before the BL, and
reload/unbox those values after success or fallback resume.

## Liveness protocol

The fast raw call path treats scratch registers and allocated registers as
clobbered by `t2_numeric_self_entry_N`. This is different from the boxed public
ABI: raw self calls preserve caller values through explicit caller-side
selective spill/reload, not through callee full-frame save/restore.

At emission time:

1. Clone `ec.rawIntRegs` into `preRaw`.
2. Compute live-across-call GPR/FPR sets and spill those values to their boxed
   home slots.
3. Materialize raw args into `X0..X(N-1)`.
4. Save the raw args and boxed function operand on the 64-byte raw-call frame.
   This is for slow fallback and callee-exit fallback; do not store raw args in
   `ExecContext`, because nested recursion would overwrite global fields.
5. On success, read raw return `X0`, restore caller `mRegRegs`/`ctx.CallMode`,
   selectively reload live values, unbox the values that were raw in `preRaw`,
   and call
   `storeRawInt(X0, instr.ID)`.
6. Clone the resulting raw map into `postRaw`.
7. On fallback resume, selectively reload live values and call
   `emitUnboxRawIntRegs(preRaw)` before materializing the raw call result and
   joining the done label.

This makes success and fallback converge with the same physical register
representation.

## Return convention

Numeric body return:

```text
return value -> X0 raw int64
branch -> num_epilogue
```

Boxed/public/direct return remains unchanged:

```text
return value -> boxed
store regs[0]
store ctx.BaselineReturnValue
branch -> epilogue or t2_direct_epilogue
```

The raw call site must never read `BaselineReturnValue`. The boxed call site
must never expect `X0` to hold the semantic result after a boxed callee returns.

## Fallback protocol

The Go side remains boxed-only. Before taking `ExitCallExit`, a raw self-call
site must materialize a normal VM call frame:

```text
regs[funcSlot]     = boxed current closure
regs[funcSlot + 1] = box(rawArg0)
regs[funcSlot + 2] = box(rawArg1)
...
```

The function value is restored from the boxed function operand captured on the
native raw-call frame. Do not reconstruct it from `ctx.BaselineClosurePtr`:
direct Tier 2 test entry and some mid-tier transitions do not guarantee that
the outer context's closure pointer is authoritative. Arguments are restored
from the native raw-call frame and boxed with `EmitBoxIntFast`.

Fallback order:

1. Restore caller `mRegRegs`, `ctx.Regs`, and `ctx.CallMode`. `mRegConsts`,
   `ctx.Constants`, and `ctx.BaselineClosurePtr` are invariant for v1 raw self
   calls.
2. Set `ec.rawIntRegs = preRaw`.
3. Do not store all active registers after a raw callee exit: the thin callee
   may have clobbered allocated registers. Caller live values were already
   selectively spilled before the BL.
4. Materialize `regs[funcSlot..funcSlot+N]` as a boxed VM call frame.
5. Write normal `ExitCallExit` descriptor.
6. Exit through the current pass epilogue.
7. On resume, selectively reload live-across-call registers.
8. Restore `preRaw` register representation with `emitUnboxRawIntRegs(preRaw)`.
9. Check the boxed VM call result is an int. If it is not an int, deopt the
   current JIT execution; the raw continuation is not valid for float-promoted
   overflow results.
10. Unbox the int result, `storeRawInt`, and join the raw fast-path
    continuation.

This same fallback is used for:

- depth overflow before the raw BL;
- callee exit after the raw BL;
- any precondition failure discovered by the raw call path.

## Exit-resume protocol

`ResumeNumericPass` only selects pass-specific resume labels. It must not encode
return representation. v1 therefore uses this rule:

- Top-level exits from the numeric body can use existing numeric resume labels.
- Numeric resume labels use the same thin FP/LR frame shape as
  `t2_numeric_self_entry_N`; boxed pass resume labels keep the full public ABI
  frame.
- Exits from a raw callee called through `BL t2_numeric_self_entry_N` are handled
  by the caller as call-boundary fallback.
- The caller does not attempt to resume the callee native frame.
- Numeric self `GETGLOBAL` should not perform a global cache lookup. The raw
  call boundary has already provided the self closure, so the numeric body can
  materialize the boxed closure from `ctx.BaselineClosurePtr`. This prevents
  global-exit storms during mid-tier Ackermann promotion.

This is semantically valid only because `AnalyzeSpecializedABI` rejects
side-effecting and non-self-call opcodes. If the ABI is later widened to allow
side effects, this fallback rule must be replaced by precise callee-frame resume
metadata.

## Implementation slices

### Slice 1: contract and tests

- Completed. The gate stayed disabled while the contract tests were introduced,
  then `enableNumericSelfBL` was enabled after entry, resume, fallback, and
  return behavior were wired through codegen.
- Raw ABI guard tests cover fact, fib, Ackermann, depth fallback, overflow
  fallback, caller-live preservation, and non-eligible boxed behavior.
- This design doc is the checklist for the current v1 implementation.

### Slice 2: separate raw self-call emitter

- Completed for non-tail static self calls that pass `AnalyzeSpecializedABI`.
- `emitCallNativeRawIntSelf(instr *Instr)` is the only raw self-call BL path;
  generic `emitCallNative` remains boxed ABI only.
- The implementation now uses a thin FP/LR numeric entry frame while keeping the
  callee VM frame window.
- The caller saves raw args and the boxed function operand on a small native
  stack frame.
- Fallback materializes a boxed VM call frame before `ExitCallExit`.

### Slice 2.5: thin raw entry

- Completed. `t2_numeric_self_entry_N` saves only FP/LR.
- Numeric exit-resume entries also use the thin FP/LR frame, so
  `num_epilogue` and `num_deopt_epilogue` pop the same frame shape regardless
  of whether execution entered through raw BL or a Go-side resume.

### Slice 2.6: thin raw caller frame

- Completed. The raw self-call frame is now 64 bytes:
  caller `mRegRegs`, caller `CallMode`, raw args `X0..X3`, boxed function
  operand, and alignment padding.
- The frame no longer saves FP/LR, `mRegConsts`, or `BaselineClosurePtr`.
  FP/LR belong to the caller's own entry frame, while constants and closure are
  invariant for the v1 raw self ABI.
- The raw caller no longer writes the callee base to `ctx.Regs` before the BL,
  and `t2_numeric_self_entry_N` no longer reloads `mRegRegs` from `ctx.Regs`.
  The callee base is carried directly in the pinned `mRegRegs` register.
- Raw callers spill/reload live allocated GPR/FPR values around the BL.
- Fallback resume uses selective reload instead of storing/reloading all active
  registers, because a thin raw callee may clobber non-live active registers.

### Slice 3: metadata cleanup

- Store `SpecializedABI` or a compact `RawIntSelfABI` on `CompiledFunction`.
- Keep `qualifyForNumeric` as a thin compatibility wrapper until all call sites
  consume the descriptor directly, then remove it.
- Add an internal verifier that rejects raw call emission when the function
  descriptor and call-site descriptor disagree.

### Slice 4: performance pass

Only after correctness is stable:

- move fallback-only raw arg/function saves out of the hot path once precise
  callee-exit resume metadata exists;
- avoid generic native-call IC/proto checks for static self;
- remove or shrink the callee VM frame window on raw success;
- optionally lower proven self calls to a raw-only call op to remove hot
  `GETGLOBAL` overhead.

## Current v1 rule

The enabled raw ABI deliberately still changes one variable at a time:

- private raw entry is thin, but boxed public direct entries stay full-frame;
- boxed fallback materialization is eager at fallback points;
- Go remains boxed-only;
- raw success returns through `X0`;
- success/fallback join with the same `rawIntRegs` state.

That is slower than the final target, but it makes the ABI mechanically
checkable. Further performance work can remove the remaining boxed stores and
shrink or remove the callee VM frame window on raw success.
