---
name: tier1-int-overflow-handling
description: >
  Research findings on integer overflow handling in V8 TurboFan, LuaJIT, JSC baseline,
  and SpiderMonkey baseline; plus ranked fix candidates for GScript Tier 1 int-spec
  overflow/deopt correctness.
type: reference
created: 2026-04-11
round: 25
---

# Tier 1 Int-Spec Overflow Handling — Reference Research

## Q1 — V8 TurboFan: Smi/Int32 Arithmetic Overflow

**Decision point**: `SpeculativeNumberAdd` (with feedback hint `kSignedSmall`) lowers via
`VisitSpeculativeAdditiveOp` in `src/compiler/simplified-lowering.cc`. If both inputs are
statically typed as `Integral32OrMinusZero` and the result fits `Signed32`, it lowers to a
pure `Int32Add` with no overflow check (line ~1862). If the result may overflow 32 bits but
the output is still used as word32 (truncated), same — no check. Otherwise it emits
`ChangeToInt32OverflowOp` (line ~1834), which uses `Int32AddWithOverflow` in the machine
layer (machine-operator-reducer.cc:480). The overflow projection feeds a `DeoptimizeIf`
node — so **overflow always causes a full deopt frame exit**, not an in-place float
conversion.

The float path is a separate IR opcode: if type feedback yields `kNumber` (not
`kSignedSmall`), `VisitSpeculativeAdditiveOp` falls through to its "Default case =>
Float64Add/Sub" branch (simplified-lowering.cc:1918) and emits `CheckedNumberOrOddballAsFloat64`
inputs into a pure `Float64Add`. Crucially, **TurboFan never converts overflow to float
in-place mid-trace**; the int and float paths are separate compiled variants, selected by
type feedback before compilation. Int overflow => deopt => re-profile => recompile with float
feedback => Float64Add variant.

Key files: `src/compiler/simplified-lowering.cc:1741,1834,1918`;
`src/compiler/machine-operator-reducer.cc:480`.

## Q2 — LuaJIT: IR_ADD Integer Overflow

LuaJIT's default mode treats all numbers as `IRT_NUM` (float64). Narrowing to `IRT_INT` is
**demand-driven, not eager**: `lj_opt_narrow_arith` (lj_opt_narrow.c:526) only narrows
ADD/SUB/MUL if (a) both operands are already integer-typed AND (b) the concrete result at
recording time fits int32 (`lj_num2int_ok`). When those conditions hold, it emits
`IR_ADDOV` (overflow-checked integer add) rather than `IR_ADD`. If the overflow guard fires
at runtime, the trace exits via a **side-exit guard failure** — not a full deopt, but a
trace abort: the trace is abandoned at that exit, execution falls back to the interpreter
at the faulting PC, and LuaJIT may re-record a new trace that observes the float result.

In DUALNUM mode (integers stored natively), the recorder emits `IR_ADDOV` directly for
all int+int arithmetic; overflow guard fires => trace side-exit => interpreter fallback.
**There is no mid-trace int→float promotion.** LuaJIT also uses predictive loop-induction
narrowing (`lj_opt_narrow_forl`, lj_opt_narrow.c:583): if loop start/stop/step at trace
recording time all fit int32 AND stop+step cannot overflow, the FORL induction var is typed
`IRT_INT` from the start — otherwise it stays `IRT_NUM`.

Key: `lj_opt_narrow.c:526–540` (lj_opt_narrow_arith), `lj_opt_narrow.c:583–606`
(lj_opt_narrow_forl). Overflow guard exit is a standard trace side-exit.

## Q3 — JSC and SpiderMonkey Baseline: "Int-first, float-sticky" per-slot

**JSC Baseline JIT** (`JITAddGenerator.cpp`): the baseline JIT emits a single snippet that
tries int+int first (branchAdd32 Overflow → slowPathJumps), then immediately falls through
to a double+double path in the same code block. On int overflow, the snippet jumps to
`slowPathJumps` which re-executes the op via a C++ call that handles any type. **No per-slot
sticky tracking.** The ArithProfile (type feedback bitmap) is updated at runtime; on a
subsequent recompile, if `lhsObservedType` shows `withDouble`, the fast path starts with
double. So JSC baseline is stateless per-invocation: it does in-place int-then-double
dispatch within one snippet per opcode, not across opcodes.
(`JITAddGenerator.cpp:47–160`, generateFastPath.)

**SpiderMonkey Baseline IC** (`CacheIR.cpp`): uses a polymorphic IC stub chain.
`BinaryArithIRGenerator::tryAttachStub` (CacheIR.cpp:14867) tries `tryAttachInt32` first.
`tryAttachInt32` (CacheIR.cpp:15013) emits `branchAdd32(Overflow, …, failure->label())` —
overflow goes to `failure->label()` which calls `EmitStubGuardFailure` and falls to the
fallback. The fallback (`DoBinaryArithFallback`, BaselineIC.cpp:2290) then calls
`TryAttachStub<BinaryArithIRGenerator>` which may attach a `tryAttachDouble` stub that
handles doubles for subsequent calls. **No per-slot sticky bit.** IC stubs are keyed on
observed value types at the call site, not on per-slot history.

**Summary**: neither JSC baseline nor SpiderMonkey baseline does "int-first, float-sticky
per slot" tracking. Both use IC/snippet re-specialization per opcode when the int path
fails, but the state lives in per-callsite type profiles, not in per-slot bitmaps that
persist across the function body.

## Q4 — Practical Fix Candidates for GScript (ranked)

**Context**: Tier 1 is linear, no IR, no SSA, no CFG. Overflow deopt currently restarts
from pc=0, replaying side effects. The acute problem is accumulator loops (fib_iter(70))
where the last ADD overflows.

### Ranking

**1. Option D (Thresholded/first-call deopt with correct restart semantics) — implement first.**
The deopt already exists. The correctness problem is restarting from pc=0 rather than
the overflowing PC. If the engine restores state to *before the overflowing ADD* and
re-executes generically from there (or just falls into the interpreter at that PC), D
is both correct and cheap. This is analogous to LuaJIT's trace side-exit at the guard PC.
Dealbreaker: current GScript deopt restarts the whole function from pc=0. Fix: record the
PC of the overflow exit and resume the interpreter at that PC with the current register
file. One-time cost at first overflow, zero overhead on the hot path.

**2. Option A (Reject int-spec if slot is a branch-reset accumulator written inside a loop).**
Statically detect: does R(A) of ADD appear as (i) written inside a FORLOOP back-edge body
AND (ii) re-read as input to a later int-spec op? If yes, exclude A from KnownInt at loop
entry. This prevents emitting int-spec on accumulators that will grow. Pure static analysis,
no runtime cost, no new exit paths. Dealbreaker: requires back-edge detection in
`computeKnownIntSlots`; adds complexity to the analysis but no new runtime machinery. The
conservative version (option C: just reject any proto containing FORLOOP) is simpler to
implement but too broad — it would disable int-spec on ackermann-style loops that do fit.
A: worth implementing as the primary guard against future loop benchmarks.

**3. Option C (Disable int-spec for all FORLOOP protos) — viable short-term blocker.**
Simple, one-line change to the eligibility gate. Safe. Leaves performance on the table for
integer FOR loops. Use as a temporary guard while implementing A or D.

**4. Option E (Runtime sticky bitmap) — high ROI but needs wiring.**
Correct and elegant in principle: on overflow, flip a dynamic per-slot bit; subsequent
uses of that slot dispatch generically. But it requires a per-frame runtime bitmap
(allocate or embed in the stack frame), and the int-spec templates must check it before
trusting KnownInt. This is a significant structural change (new calling convention,
new prologue overhead). Defer until Tier 1 has proven it needs dynamic types rather than
static.

**5. Option B (SCVTF in-place float fallback within int-spec overflow path) — DEALBREAKER.**
The generic template already does this (`tier1_arith.go:209–212`). The int-spec template
does not because subsequent ops at the same PC trust KnownInt statically. If we emit
SCVTF-and-continue in the int-spec overflow path, the next int-spec op reads a float-boxed
value through an SBFX path and computes garbage. Correct only if we also clear A from the
runtime KnownInt state (which is E) or fallback to generic for all subsequent ops (which
is D). B alone is unsound.

### Recommended implementation order

1. **D (correct restart semantics)**: record overflow PC, resume interpreter there.
   Fixes fib_iter(70) immediately. ~50 lines.
2. **A (accumulator detection)**: add back-edge loop body scan to
   `computeKnownIntSlots`; exclude accumulator slots from KnownInt at loop-header PCs.
   Prevents future regressions for growing-int loops. ~80 lines in analysis.
3. **C (FORLOOP gate)**: one-line safety valve — enable only after A is verified.
