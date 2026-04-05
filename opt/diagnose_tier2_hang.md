# Diagnosis: Tier 2 Hang on Recursive Call-Heavy Functions

> Date: 2026-04-05
> Cycle: 2026-04-05-tier2-recursion-diagnose (Task 2)
> Confidence: **HIGH**

## Summary

Root cause identified: **the graph builder drops arguments of `OP_CALL`
instructions whose `B=0` (variadic args threaded via `top`)**. This is
independent of hypotheses H1–H5 in the plan (none matched exactly) — it is
a latent bug in `graph_builder.go` exposed by Tier 2 compilation of any
recursive function that uses the nested-call-as-argument pattern
(`outer(x, inner(...))`). Ackermann, Hofstadter F/M, and similar patterns
all hit this; plain fib escapes because its calls use `B=2 C=2` (fixed arity).

## What We Tried

### Iteration 1 — Deeper fib via unit-test harness (PASSED, no hang)

Extended `TestTier2RecursionDeeperFib` with fib(10), fib(20), fib(25),
fib(30), fib(20)×10reps. All completed in <200ms, producing correct values.
Conclusion: **harness test alone cannot reproduce the hang** — Signal 4
candidate, but we pushed further.

### Iteration 2 — Policy flip + real CLI

Re-applied commit `6bd0385`'s `func_profile.go` clause
(`CallCount>0 && !HasLoop && ArithCount>=1 && BytecodeCount<=40`) and built
`/tmp/gscript_diag`. Ran three benchmarks via `perl -e 'alarm 20; exec...'`:

| Benchmark | Result |
|-----------|--------|
| fib_recursive.gs | **COMPLETED** in 17.9s (fib(35)=9227465, correct but 12x slower than Tier 1) |
| ackermann.gs | **HUNG** (processes left running at 5+ minutes CPU, had to `kill -9`) |
| mutual_recursion.gs | **HUNG** (same) |

`GSCRIPT_JIT_DEBUG=1` showed all three functions compiled to Tier 2
successfully (`tier2: compiled "ack"`, `"F"`, `"M"`). Compile is NOT
the hang site. This rules out H3 (inline fixpoint) and H5 (compile blowup).

### Iteration 3 — Unit-test harness with Tier 2 forced pre-call

Created a direct harness test: compile top-level → force
`tm.CompileTier2(ackProto)` → then invoke `result := ack(m, n)` on a fresh
proto. Results with 3s timeout:

| Call | Result | Elapsed |
|------|--------|---------|
| ack(2,2) | 7 (correct) | 172µs |
| ack(2,3) | 9 (correct) | 233µs |
| ack(2,4) | 11 (correct) | 243µs |
| ack(2,5) | 13 (correct) | 533µs |
| ack(3,0) | 5 (correct) | 230µs |
| ack(3,1) | 13 (correct) | 513µs |
| ack(3,2) | 29 (correct) | **1.11s** |
| ack(3,3) | **HANG** | >30s |
| ack(3,4) | **HANG** | >30s |

Timing shows **exponential-ish explosion** (1.1s at 541 calls → 2000× per
call overhead). Also: the CLI earlier printed ack(3,1) as producing EIGHT
return values (`13 3 1 nil nil 3 1 13`), proving Tier 2 ack produces
garbage multi-values.

### Iteration 4 — Stripped-down reproduction (minimal)

Wrote a minimal recursive function with nested-call-as-arg pattern:

```
func f(n) {
    if n < 1 { return 0 }
    return 1 + f(f(n-1))
}
```

Mathematically f(n)=n for n≥0. With Tier 2 forced:

| n | Result | Elapsed |
|---|--------|---------|
| 5 | 5 (correct) | 151µs |
| 10 | 10 (correct) | 1.8ms |
| 15 | 15 (correct) | 71ms |
| 18 | — | — |
| 20 | **0.0 (FLOAT zero!)** | HANG |

f(20) returns 0.0 as a *float* (type corruption) and still couldn't finish
in 3s. **This is exponential blowup from wrong per-call recursion depth**,
because the inner call's wrong result causes the outer call to recur
incorrectly.

### Iteration 5 — Pipeline-stage isolation (IR inspection)

Dumped ack's IR after BuildGraph and after TypeSpec+ConstProp+DCE+Range.
The offending pattern is in block B4 of ack:

```
B4: ; preds: B2
    v20  = GetGlobal   globals[0] : any
    v24  = GetGlobal   globals[0] : any
    v25  = ConstInt    1 : int
    v27  = SubInt      v31, v25 : int
    v28  = Call        v24, v32, v27 : any      # inner ack(m, n-1) - correct
    v29  = Call        v20 : any                # outer ack - NO ARGS!
    Return      v29
```

**v29 is a `Call` with only the function value and no arguments.** The
outer `ack(m-1, ack(m, n-1))` should have emitted a Call with 3 args
(`v20, v23, v28`), but `v23 = SubInt(m-1)` was DCE-eliminated and v28
wasn't connected.

Dumping the bytecode confirms:
```
022: CALL A=4 B=3 C=0    # inner ack(m, n-1) — 2 args, variable returns
023: CALL A=2 B=0 C=0    # outer ack — B=0 = VARIADIC ARGS from top
```

Looking at `graph_builder.go` lines 532–544:
```go
case vm.OP_CALL:
    ...
    if bOp >= 2 {
        for i := a + 1; i <= a+bOp-1; i++ {
            args = append(args, b.readVariable(i, block))
        }
    }
    // ** NO else branch for bOp == 0 (variadic) **
    instr := b.emit(block, OpCall, TypeAny, args, ...)
```

**When `B=0`, the graph builder silently skips the arg loop.** The Call
emits with only the function value (`args = [fn]`). Downstream passes then
DCE the unused `m-1` Sub since nothing references it.

Compared with fib's bytecode (all `CALL A=2 B=2 C=2`), this explains why
fib escapes the bug and ack/F/M hit it. Additional repro `f(n) = 1+f(f(n-1))`
uses `CALL A=3 B=0 C=2` for the outer call → same bug, same hang.

## Evidence Table

| # | Hypothesis | Verdict | Evidence |
|---|-----------|---------|----------|
| H1 | Deopt ↔ recompile thrash | **Ruled out** | `Tier2Count=1` stays constant during hang; no recompile loop in stack traces |
| H2 | Tier 2 emit/regalloc hang in inlined IR | **Ruled out** | Compile succeeds (`tier2: compiled "ack"` prints); hang is in execute phase |
| H3 | Infinite inline fixpoint | **Ruled out** | Compile finishes in <1ms; verified by `GSCRIPT_JIT_DEBUG=1` output |
| H4 | Tier 2 runtime infinite loop | **Partially confirmed** | It's not infinite — it's exponential from wrong results. Stack trace shows nested `executeTier2 → executeCallExit → CallValue → ...` cycle consuming CPU proportional to exponential recursion depth |
| H5 | Bounded tree too large | **Ruled out** | ack's post-inline body is 29 registers / 5 blocks; small. Hang reproduces with `MaxRecursion=0` (no recursive inlining). |
| **NEW** | **Graph builder drops CALL B=0 args** | **CONFIRMED** | IR dump shows `Call v20` with 0 args; bytecode has `CALL A=2 B=0 C=0`; `graph_builder.go:539` only handles `bOp >= 2` |

## Verdict

**Localized fix possible** — single-file change in `graph_builder.go`.

### Fix Location

`/Users/jxwr/ai/ai_agent_experiment_gscript/gscript/internal/methodjit/graph_builder.go`
lines 532–553 (the `case vm.OP_CALL:` block).

### Fix Sketch

1. Track a graph-builder-local `top` variable that records the effective
   stack top after each variadic-result operation.
2. When OP_CALL/OP_VARARG has `c == 0`, update `top = a + <runtime-returns>`.
   Since the static count isn't known at graph-build time, model this as a
   multi-result Value: emit a special `OpCallV` (or attach an `Aux` flag)
   whose result represents "values from A..top", and track the producing
   instruction as the current top-supplier.
3. When OP_CALL has `b == 0`, read args from `a+1` up to the top-supplier,
   splicing in its variadic results. The simplest SSA model is:
   - Inner variadic call produces `(v, vTail...)` where `v` is result 0.
     Currently the graph builder writes instr.Value() to multiple slots
     (line 547–553) — we can lean on that.
   - Outer B=0 call reads args from `a+1..top_register_from_previous_call`.
4. Alternatively (simpler but possibly less-optimizable): **force the
   bytecode compiler to emit fixed-arity CALL everywhere** (bail on `B=0`
   cases during Tier 2). Cleaner fallback: if `bOp==0` or `c==0`, the graph
   builder can emit a `Nop`-producing placeholder and **mark the function
   as not-Tier-2-eligible**, forcing Tier 1.

### Minimum-Risk Recommendation

The JSC-style fix is long (a few hundred lines + new IR op). The
**minimum-viable correctness fix** is:

**Short path (TASK 3)**: In `graph_builder.go`, when `OP_CALL B==0` is
encountered, **set a "not-Tier-2-eligible" flag on the Function being
built** (add a `Function.Unpromotable` field or similar). Then
`compileTier2` rejects the function early. This preserves current behavior
for existing benchmarks while preventing the hang. fib/ackermann/mutual
would stay at Tier 1 (as they did before the policy flip).

**Long path (future round)**: Properly model variadic CALL in the IR.
Introduce `OpCallV` with Aux for "args from A+1 to top" and an SSA phi-like
mechanism to track top. This enables Tier 2 to compile these functions
correctly and unlocks the benchmark speedups predicted in the plan.

### Regression Test Added

`TestTier2NestedCallArgBug` in
`/Users/jxwr/ai/ai_agent_experiment_gscript/gscript/internal/methodjit/tier2_recursion_hang_test.go`
asserts that the last `OpCall` in `ack`'s IR has ≥3 args. **Currently
FAILS (expected)**: `len(lastCall.Args) == 1`. Will pass when the fix
lands.

## Artifacts Left in Repo

- `internal/methodjit/tier2_recursion_hang_test.go` (MODIFIED, +120 lines):
  - Added `TestTier2RecursionDeeperFib` (deeper fib variants, all pass)
  - Added `TestTier2NestedCallArgBug` (root-cause regression, **fails
    intentionally** until fix lands — documents the assertion that the fix
    must satisfy)
- `opt/diagnose_tier2_hang.md` (THIS FILE)

## Reverted / Not-Committed

- `internal/methodjit/func_profile.go`: temporary policy flip reverted
  (`git checkout`)
- `internal/methodjit/tiering_manager.go`: briefly changed `MaxRecursion=0`
  to test inliner hypothesis, reverted to `MaxRecursion=2`
- `internal/methodjit/diag_ack_test.go`: scratch test file, deleted

## Recommendation for Task 3

**Option A (recommended): short-path correctness fix.**

1. Add `Function.Unpromotable bool` field in `graph.go`.
2. In `graph_builder.go:532` OP_CALL handler, when `bOp==0` or `c==0`,
   set `b.fn.Unpromotable = true` and emit a best-effort Call (keep the
   current args-from-A+1..A+bOp-1 loop).
3. In `tiering_manager.go:compileTier2` after `BuildGraph`, check
   `fn.Unpromotable` and return an error ("variadic call unsupported at
   Tier 2"). Function stays at Tier 1.
4. Keep the `TestTier2NestedCallArgBug` regression test but update its
   assertion: "if graph has a Call with <3 args in a multi-call block,
   function is marked Unpromotable".

**Estimated scope**: 2 files, ~40 lines. Preserves correctness. No
benchmarks unlocked this round (fib/ack/F/M stay at Tier 1), but the
hang is eliminated and we can re-attempt the policy flip without
breaking the suite.

**Option B (defer Signal 1): full variadic IR model.**

If Task 3 budget doesn't fit Option A's scope, defer the fix. Next round's
work items:
1. Research V8/JSC/LuaJIT IR modeling of variadic calls (1 day)
2. Design `OpCallV` IR op + "top tracker" in graph builder (1 day)
3. Implement + test (2 days)
4. Integrate with inliner + regalloc + emit (2 days)
5. Re-run full benchmark suite (1 day)

This is **clearly a future-round architectural task**.

## Confidence: HIGH

The root cause is reproduced deterministically (unit-test harness triggers
it in 200ms), precisely localized (one function in one file), and
independently confirmed via both the IR dump AND the bytecode dump. The
`TestTier2NestedCallArgBug` assertion fails now and will pass when fixed;
it's a clean TDD regression gate.

## Round Outcome (Tasks 3–5, appended 2026-04-05)

### Task 3: Correctness fix LANDED (commit 239f0d7)

Implemented Option A from the Recommendation above. Added
`Function.Unpromotable bool`, set in `graph_builder.go` when
`OP_CALL B==0` is seen, rejected in `tiering_manager.go:compileTier2`.
4 files changed, 53 insertions / 31 deletions. `TestTier2NestedCallArgBug`
now passes: it asserts both `fn.Unpromotable == true` and that
`compileTier2(ackProto)` returns an error. Full `internal/methodjit/`
test suite: green.

### Task 4: Policy flip LANDED (commit df1812c) then REVERTED (commit f54ea63)

Restored the JSC-style call-heavy clause from 6bd0385. Targeted
benchmarks under `perl alarm 30s` all terminated correctly (no hangs):

- fib_recursive.gs: 17.667s (correct, fib(35)=9227465)
- ackermann.gs:       0.253s (correct, ack(3,4)=125)
- mutual_recursion:   0.194s (correct, F(25)=16)

JIT stats for fib.gs showed fib got promoted to Tier 2 (single Tier 2
function). ack and mutual_recursion were safely refused by Unpromotable.

### Task 5: Signal 3 FIRED — full suite

Full suite vs baseline (commit 6bd0385 pre-revert):

| Benchmark | Baseline JIT | With flip | Delta |
|-----------|--------------|-----------|-------|
| **fib**              | 1.407s  | 2.086s  | **-48.3%** |
| **fib_recursive**    | 13.999s | 17.755s | **-26.8%** |
| **coroutine_bench**  | 15.023s | 18.196s | **-21.1%** |
| **binary_trees**     | 2.052s  | 2.286s  | **-11.4%** |
| method_dispatch      | 0.103s  | 0.087s  | +15.5% |
| (other 17 benchmarks) | — | — | ≤6% change |

**4 regressions >10% vs 1 improvement >10%**. Per plan Signal 3:
"Action: revert the gate flip. Keep the localized bug fix."

Reverted df1812c in commit f54ea63. Correctness fix (239f0d7) remains.

### Why the policy flip regressed fib by 48%

Evidence from the targeted run and IR inspection:

1. **numRegs explosion**: inlined fib's IR grows from 4 to 40 registers
   at MaxRecursion=2 (2 levels of fib cloning).
2. **Residual calls remain**: MaxRecursion=2 flattens depth 0..2 but
   depth 3+ still emits OpCall → exit-resume (~30-80ns per depth-3 call).
   In the fib(35) call tree the majority of calls are at depth 3+.
3. **Tier 1 BLR is ~10ns**; Tier 2 exit-resume is 3–8x slower per call.
4. The policy flip replaced fast Tier 1 BLR with slow Tier 2 exit-resume
   without unlocking enough inline wins to compensate.

coroutine_bench and binary_trees regressed for the same reason:
they contain small call-heavy leaf functions that match the clause and
get promoted to inlined-Tier 2 but run slower there than at Tier 1.

### Next-Round Task List (category A: recursive call overhead)

**Do NOT re-attempt the policy flip** until at least ONE of these lands:

1. **Deep recursive inlining**: raise MaxRecursion from 2 to ~4-5 (JSC uses 2
   per-chain, JavaScriptCore inlines deeper with budget caps). For fib(35)
   with MaxRecursion=5, depth-5 inlining folds 2^5=32 calls into one body;
   residual exit-resume fires only at depth 6+. Requires careful budget
   control (see JSC's `maximumInliningDepth = 5`,
   `maximumFunctionForCallInlineCandidateBytecodeCostForDFG = 80`).

2. **Native BLR in Tier 2** for residual `OpCall` (close the exit-resume
   cost gap). Currently Tier 2 exits to the VM to handle unknown-callee
   calls. A direct BLR emit path matching Tier 1's (~10ns) would make the
   policy flip viable even without deeper inlining.

3. **Proper variadic IR model** (`OpCallV` + top tracker in graph builder)
   so ack / mutual_recursion / `f(g(...))` become Tier-2-eligible. Current
   Unpromotable gate is a safety net, not a solution. This is the
   architectural work deferred from Task 3 Option B.

Without at least one of the above, the call-heavy tier-up clause is a
net negative and should stay off.

### Budget note

This round consumed 5 commits (1 over the plan's 4-commit cap). The
extra commit is the Signal 3 revert — mandated by the plan itself
("Action: revert the gate flip"). No work was done outside the original
scope; the revert is a defensive action triggered by full-suite data
that was not available at planning time.
