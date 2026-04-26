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
- Do not implement a thin recursive frame yet. Reuse the existing full frame
  until the ABI contract is proven.
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
- `ctx.Regs` points at the callee VM frame base.
- `ctx.BaselineClosurePtr` points at the self closure while the raw callee is
  running, derived from the boxed function operand rather than assumed from the
  outer VM state.
- `mRegRegs` may be caller base or callee base, but raw entry reloads it from
  `ctx.Regs` before entering the numeric body.
- `ctx.CallMode` may remain the boxed-direct value; numeric return does not use
  it because it branches to `num_epilogue`.
- `ctx.NativeCallDepth` has been incremented.

The raw entry reuses the existing full frame save/restore. Therefore the first
slice can rely on ARM64 callee-saved registers (`X20..X28`, `D8..D11`) surviving
across the raw call. This is why v1 does not need to spill caller allocated
registers on the fast path.

## Liveness protocol

The fast raw call path treats `X0..X7` and temporary scratch registers as
clobbered. It does not treat allocated callee-saved GPRs/FPRs as clobbered
because `t2_numeric_self_entry_N` saves and restores the full frame.

At emission time:

1. Clone `ec.rawIntRegs` into `preRaw`.
2. Materialize raw args into `X0..X(N-1)`.
3. Save the raw args and boxed function operand on the native stack before the
   call. This is for slow fallback and callee-exit fallback; do not store raw
   args in `ExecContext`, because nested recursion would overwrite global fields.
4. On success, read raw return `X0`, restore caller context, and call
   `storeRawInt(X0, instr.ID)`.
5. Clone the resulting raw map into `postRaw`.
6. On fallback resume, reload active regs from boxed memory and call
   `emitUnboxRawIntRegs(postRaw)` before joining the done label.

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

1. Restore caller `mRegRegs`, `mRegConsts`, `ctx.Regs`, `ctx.Constants`,
   `ctx.CallMode`, and `ctx.BaselineClosurePtr`.
2. Set `ec.rawIntRegs = preRaw`.
3. `emitStoreAllActiveRegs()` so caller live values are boxed in memory.
4. Materialize `regs[funcSlot..funcSlot+N]` as a boxed VM call frame.
5. Write normal `ExitCallExit` descriptor.
6. Exit through the current pass epilogue.
7. On resume, `emitReloadAllActiveRegs()`.
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
- The implementation keeps the full frame and callee VM frame window.
- The caller saves raw args and the boxed function operand on a small native
  stack frame.
- Fallback materializes a boxed VM call frame before `ExitCallExit`.

### Slice 3: metadata cleanup

- Store `SpecializedABI` or a compact `RawIntSelfABI` on `CompiledFunction`.
- Keep `qualifyForNumeric` as a thin compatibility wrapper until all call sites
  consume the descriptor directly, then remove it.
- Add an internal verifier that rejects raw call emission when the function
  descriptor and call-site descriptor disagree.

### Slice 4: performance pass

Only after correctness is stable:

- remove eager boxed arg stores from the fast path;
- avoid generic native-call IC/proto checks for static self;
- reduce save/restore frame for raw entry;
- optionally lower proven self calls to a raw-only call op to remove hot
  `GETGLOBAL` overhead.

## Once-feasible v1 rule

The first enabled raw ABI patch must change only one variable at a time:

- full frame stays;
- boxed fallback materialization is eager at fallback points;
- Go remains boxed-only;
- raw success returns through `X0`;
- success/fallback join with the same `rawIntRegs` state.

That is slower than the final target, but it makes the ABI mechanically
checkable. After this passes, performance work can remove the remaining boxed
stores and shrink the recursive entry frame.
