---
layout: default
title: "The Debugger That Runs Production"
permalink: /48-the-debugger-that-runs-production
---

# The Debugger That Runs Production

Post 46 was about rebuilding the workflow after too many rounds spent trusting
diagnostics that were close to production, but not production. Post 47 was
about the call boundary: Ackermann stopped being a mystery only after the
recursive ABI became an explicit machine-code contract.

This post is about the missing layer between those two stories.

The compiler did not need another guess. It needed a debugger that could answer
the same questions every optimizing runtime asks:

- when did this function tier up?
- why did this function fail to tier up?
- which exit is hot?
- which IR op emitted this machine code?
- what did the optimizer believe?
- did the exit-resume path reconstruct exactly the state the VM expects?

Those sound like ordinary questions. They were not ordinary in this codebase.
Until now, answering them meant stitching together `-jit-stats`, ad hoc
environment variables, stale diagnostic dumps, disassembly, and hope.

That is how you get convincing wrong answers.

## The old failure mode

The recurring failure was not that a diagnostic tool was broken in an obvious
way. It was worse: the tool was plausible.

One round looked at a post-pipeline IR dump and found redundant phis. The fix
was correct for that IR. Production had already removed those phis.

Another round built a scalar-promotion pass against synthetic typed
`GetField` nodes. Production emitted `GetField : any` followed by
`GuardType float`, so the pass never saw the shape its tests were exercising.

Another round blamed a Tier 2 slowdown on a global-cache check. The
disassembly later showed that the global check was a tiny part of the real
cost; the call boundary and boxed slot traffic were the wall.

The common bug was not "bad engineering." The common bug was tool drift.
Diagnostics were observing something adjacent to the real execution path and
then speaking with production confidence.

The new rule is simple:

> A compiler diagnostic is allowed to be expensive. It is not allowed to lie.

That produced six tools.

## 1. The event timeline

The first tool is the production JIT timeline:

```bash
gscript -jit \
  -jit-timeline trace.jsonl \
  benchmarks/suite/ackermann.gs
```

It records tier events as they happen:

```json
{"event":"tier1_compile","tier":"tier1","proto":"<main>","attrs":{"call_count":1}}
{"event":"tier2_attempt","tier":"tier2","proto":"ack","attrs":{"attempt":2}}
{"event":"tier2_success","tier":"tier2","proto":"ack","attrs":{"direct_entry":true}}
{"event":"tier2_entered","tier":"tier2","proto":"ack","attrs":{"num_regs":18}}
```

This is not a profiler. It is a chronology.

That distinction matters. Performance questions often start with "why is this
slow," but the first useful answer is often "this code never entered the tier
you thought it entered." The timeline makes that visible without adding another
one-off `fmt.Fprintf` to the tiering manager.

It also separates three states that used to be blurred together:

- not compiled;
- compiled but never entered;
- entered, exited, and fell back.

That difference is the gap between a tiering bug and a codegen bug.

## 2. The warm production dump

The second tool is the warm Tier 2 dump:

```bash
gscript -jit \
  -jit-dump-warm /tmp/warm \
  -jit-dump-proto ack \
  benchmarks/suite/ackermann.gs
```

The important word is "warm."

This dump is captured from the real production compile triggered by normal
execution. It does not cold-recompile the proto through a parallel diagnostic
pipeline. It records what the tiering manager actually compiled:

```text
/tmp/warm/ack.ir.before.txt
/tmp/warm/ack.ir.after.txt
/tmp/warm/ack.regalloc.txt
/tmp/warm/ack.asm.txt
/tmp/warm/ack.bin
/tmp/warm/ack.feedback.txt
/tmp/warm/ack.status.json
/tmp/warm/manifest.json
```

That fixes the exact class of failures from the earlier workflow. If production
blocked Tier 2 because a loop contained a call, the warm dump records that
failure. If production compiled a numeric body with a raw self entry, the dump
records that body. If production saw different feedback than a unit test did,
the dump records the production feedback.

The dump can be wrong only if production is wrong. That is the right failure
mode.

## 3. Optimization remarks

The third tool is structured optimization remarks.

An optimizing compiler needs to explain itself. Not in prose. In data.

The new remark stream records pass-level decisions into the Tier 2 trace and
diagnostic artifacts: inline success and failure, tier gates, type
specialization, load elimination, LICM, and missed opportunities.

This changes the debugging question from:

> Did LICM do anything?

to:

> LICM saw this instruction, classified it this way, and either moved it or
> left a reason.

That sounds small until you are looking at a benchmark like `fannkuch` or
`math_intensive`, where the difference between "the pass did not run" and "the
pass ran but was blocked by this representation" decides the whole next round.

Remarks also make failures composable. The tiering manager can now say:

```text
Tier2Gate blocked: LoopDepth<2 candidate has performance-blocked Call inside loop
```

and that reason travels with the diagnostic artifact instead of disappearing
into stderr.

## 4. The IR/ASM/source map

The fourth tool connects the layers that used to be manually correlated:

```text
source line -> bytecode PC -> IR instruction -> machine-code byte range
```

The IR now carries source metadata. The emitter records byte ranges per IR
instruction. The diagnostic dump can produce a JSON map and annotated assembly.

That is the tool I wanted every time I stared at an ARM64 block and asked:

> Which source expression paid for these twelve loads?

Before this, the answer required triangulation. Find the bytecode. Guess the IR
node. Search for the op in the emitter. Count instructions in disassembly.
Hope no pass duplicated or deleted the node.

Now the mapping is explicit. Synthetic instructions still show up as
synthetic. Lowered nodes can copy source from their origin. Instructions that
do not emit machine code still appear with `code_start = -1`, which is useful
because "this IR node emitted nothing" is itself an answer.

The point is not prettier dumps. The point is avoiding the oldest compiler
debugging trap: optimizing the instruction you can see instead of the
instruction that is hot.

## 5. The exit/deopt profile

The fifth tool is the production exit profile:

```bash
gscript -jit -exit-stats benchmarks/suite/ackermann.gs
gscript -jit -exit-stats-json benchmarks/suite/ackermann.gs
```

It records exits on the real `executeTier2` path and aggregates them by:

- proto;
- exit code;
- IR instruction ID;
- bytecode PC;
- reason;
- count.

A sample shape looks like this:

```text
Tier 2 Exit Profile:
  total exits: 2029
  by exit code:
    ExitCallExit: 506
    ExitGlobalExit: 1011
    ExitOpExit: 508
    ExitTableExit: 4
  sites:
    500  proto=<main> exit=ExitCallExit id=18 pc=17 reason=Call
    500  proto=<main> exit=ExitGlobalExit id=15 pc=14 reason=GetGlobal
    500  proto=<main> exit=ExitOpExit id=19 pc=18 reason=SetGlobal
```

That one table changes the next optimization round.

If a benchmark is slow because of `ExitOpExit Len`, do not spend the round on
register allocation. If it is slow because the hot loop is taking
`ExitGlobalExit`, do not write another IR pass. If the exit count is zero and
the code is still slow, the problem is inside generated code, not at the VM
boundary.

The profile also makes tiering gates less mysterious. A function can compile
and enter Tier 2, but still lose because one site exits thousands of times.
That is a different bug from a function that never entered Tier 2 at all.

## 6. The exit-resume checker

The sixth tool is the one I least wanted to need:

```bash
GSCRIPT_EXIT_RESUME_CHECK=1 go test ./internal/methodjit -run TestExitResumeCheck
```

Exit-resume bugs are the worst kind of JIT bug. The generated code leaves to
Go, Go performs an operation, and then the JIT resumes with a reconstructed
register file. If one live slot is stale, or one raw int is not boxed before
fallback, the program may keep running for thousands of instructions before it
breaks somewhere unrelated.

The checker adds debug-only metadata and a shadow register file. It verifies:

- live GPR and FPR home slots before exit;
- raw-int materialization as boxed ints;
- raw-float materialization as float bits;
- fallback call frames;
- raw self-call fallback arguments;
- table-exit descriptor ranges;
- that non-target live slots are not clobbered by the exit handler.

It is off by default. When disabled, compiled functions carry no checker
metadata and the normal path stays cheap. When enabled, it makes the exit
contract executable.

This matters especially for the raw-int self-recursive ABI. A fast happy path
is easy to write:

```text
X0..X3 -> BL raw self entry -> X0
```

The hard part is proving that fallback, resume, liveness, and return convention
all agree when the happy path stops being happy. The checker is the safety net
for that class of work.

## One combined smoke test

The tools are separate, but they can run together:

```bash
gscript -jit \
  -jit-timeline /tmp/trace.jsonl \
  -jit-dump-warm /tmp/warm \
  -jit-dump-proto ack \
  -exit-stats \
  -exit-stats-json \
  benchmarks/suite/ackermann.gs
```

That command exercises the real program, writes the tier chronology, captures
the warm production compile, prints the exit profile, and emits JSON for
machine comparison.

This is the workflow change: first observe the production path, then optimize.
Not the other way around.

## What this does not solve

These tools do not make the compiler faster.

That is worth saying plainly. A timeline does not remove an exit. A warm dump
does not lower a table access. A remark does not hoist a load. The
exit-resume checker can even make debug runs slower by design.

The value is that they change the cost of being wrong.

Before, a wrong hypothesis could survive for a whole round because the tool
confirming it was looking at the wrong pipeline. Now the first question is
mechanical:

> Did production do the thing?

If no, the optimization target changes. If yes, the next question is:

> Where did production spend the boundary cost?

That is a much smaller search space.

## The next optimization loop

The immediate targets are still the same ones:

- Ackermann needs the raw self-recursive path to lose more frame and fallback
  overhead.
- `fibonacci_iterative` needs its loop-carried integer state audited against
  the numeric lowering and phi materialization.
- `method_dispatch` needs method lookup plus call dispatch to stop behaving
  like a general table/call boundary on the hot path.
- `fannkuch` and `math_intensive` need the alleged regressions separated into
  tiering choice, exit frequency, and generated-code cost.

The difference is that each target now starts with a command, not a story.

For Ackermann, the first question is no longer "is Tier 2 faster than Tier 1?"
It is:

```bash
gscript -jit -exit-stats -jit-dump-warm /tmp/ack benchmarks/suite/ackermann.gs
```

For method dispatch, it is not "maybe inline methods." It is:

```bash
gscript -jit -exit-stats benchmarks/suite/method_dispatch.gs
```

For a suspected regression, it is not "what pass changed recently?" It is:

```bash
gscript -jit -jit-timeline trace.jsonl -exit-stats benchmark.gs
```

and then reading what actually happened.

That is the whole point of this round. The compiler still has to become faster
than LuaJIT. But from here on, every performance round has to earn its premise
against production evidence first.

