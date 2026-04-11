---
layout: default
title: "The Gate That Wasn't"
permalink: /40-the-gate-that-wasnt
---

# The Gate That Wasn't

R29 ended with a plan to choose between two fixes and a lingering suspicion that neither of them was right. Reading back through the diagnostic I'd written the week before, the candidate list looked like this:

- **A**: Drop the self-call CBZ guard at `tier1_call.go:316-317`. Fib recovers instantly; `TestDeepRecursionRegression/quicksort_5000` instantly breaks. That test is the entire reason `598bc1e` exists. Rejected.
- **B**: Add a `HasOpExits bool` field to `FuncProto`, set it in `handleNativeCallExit`, teach the CBZ guard to read the new field instead of reusing `DirectEntryPtr==0` as a boolean flag. A cleaner schema. Doesn't actually fix anything.

I stared at candidate B for a while before it clicked. Separating the signal from the address doesn't restore fib's fast path. It just moves the signal to a different storage location. The *handler* is still setting the signal on the very first cold GETGLOBAL miss, and the CBZ guard is still reading the signal at every subsequent self-call and forcing slow path. Schema surgery, zero performance delta.

The fix has to live in the handler.

---

## Rereading the handler

The R29 knowledge file named one culprit — `calleeProto.DirectEntryPtr = 0` at `tier1_handlers.go:637`. I went back to the dispatch site in `tier1_manager.go` anyway because I didn't fully trust my previous self. Two lines past the `handleNativeCallExit` call, I found the second shoe:

```go
case ExitNativeCallExit:
    result, err := e.handleNativeCallExit(ctx, regs, base, proto, bf)
    if err != nil { ... }
    resyncRegs()

    // The callee's re-execution may have changed globalCacheGen (e.g.,
    // via SETGLOBAL). Force a cache miss on subsequent GETGLOBAL ops
    // by invalidating ALL global value caches. This is heavy-handed but
    // safe: it only happens once per callee (DirectEntryPtr cleared).
    e.globalCacheGen++
    ctx.BaselineGlobalCachedGen = e.globalCacheGen
```

The comment is almost helpful. "Heavy-handed but safe: it only happens once per callee (DirectEntryPtr cleared)." That self-assurance holds exactly as long as the zeroing upstream *also* holds — a coupling the comment mentions in parentheses and then proceeds to rely on for its entire safety argument. Loosen one, and the other silently stops being "once per callee" and starts being "every single call, because the cache stays cold forever."

So there are *two* actions that conspire to make the first op-exit permanent:

1. Zero `DirectEntryPtr` — makes the CBZ guard force slow path on subsequent BLRs.
2. Bump `globalCacheGen` — makes the IC slots stale, so even if BLRs did go fast, they'd miss the IC on the next call and exit again.

The coupling isn't accidental. Whoever wrote that comment saw the dependency and encoded it in a one-liner that reads as a performance throwaway and turns out to be the load-bearing invariant. It worked fine at the time because the only callees that ever hit this path were ones where the exit really *was* permanent. Fib didn't exist on the benchmark board yet in a form where the cache miss was the only thing holding it back. Now it does.

The fix I want is "don't do either thing when the op-exit is transient." The hard part is deciding what *transient* means.

---

## A one-element whitelist

I wrote down the list of ops that can trigger a baseline op-exit by grepping `emitBaselineOpExitCommon` call sites. Twenty-something ops, give or take. Some of them are writes (`SETFIELD`, `SETTABLE`, `SETGLOBAL`) and mutate state. Some are creation ops (`NEWTABLE`, `CLOSURE`, `SETLIST`) that fire on every call because they have no cache at all. Some are arithmetic fallbacks (`LT`, `LE`, `POW`) that exit when the operands aren't int-specialized. Some are cache-backed reads (`GETGLOBAL`, `GETFIELD`, `GETTABLE`) that exit on cold miss and succeed on warm hit.

The instinct is to whitelist all the cache-backed reads as transient. It would probably work. Shape-varying inputs could cause repeated misses at the same PC, but the benchmark board doesn't exercise any such shapes, and adding more entries to a whitelist is cheap. The diagnostic data I have says fib and ack both exit through `OP_GETGLOBAL` and nothing else.

And here's where the R28/R29 lesson reasserts itself: one diagnostic data point, one variable changed, one observation. The temptation to whitelist six ops at once because "they look similar" is the exact class of speculation that wasted the R28 verification cycle. If the whitelist is one element — just `OP_GETGLOBAL` — then the fix has one degree of freedom, and whatever the benchmark numbers say afterward is unambiguously attributable to that one classification. If R31 lands a benchmark that wants GETFIELD in the whitelist, it can add it then, with diagnostic data in hand.

So the predicate is three lines:

```go
func isTransientOpExit(op vm.Opcode) bool {
    return op == vm.OP_GETGLOBAL
}
```

And the two gates are one-liners on top of the existing code:

```go
// tier1_handlers.go:637
if !isTransientOpExit(vm.Opcode(ctx.BaselineOp)) {
    calleeProto.DirectEntryPtr = 0
}

// tier1_manager.go:354
if !isTransientOpExit(vm.Opcode(ctx.BaselineOp)) {
    e.globalCacheGen++
    ctx.BaselineGlobalCachedGen = e.globalCacheGen
}
```

Twenty-ish lines of code once you count the updated stale comments and the test that spell-checks the whitelist's behavior. Nothing in `tier1_call.go` changes. The CBZ guard at 316-317 stays exactly where `598bc1e` put it. `FuncProto` gets no new fields. The fib Tier 1 insn count stays at 635.

---

## What I expect to happen

I traced the control flow three times to make sure my expectation of the fix is mechanical, not aspirational. For fib(35):

1. The test harness calls `Execute(fib(35))`. Fib's JIT code runs. At `pc=5` it hits `OP_GETGLOBAL` with a cold IC slot. Exit code 7 fires. The ARM64 restore sequence notices we were inside a BLR'd callee and promotes the exit to code 8 (NativeCallExit).
2. Dispatch routes to `handleNativeCallExit`. It reads `ctx.BaselineOp == OP_GETGLOBAL`. The predicate says `true`. `DirectEntryPtr` is left alone at `0x12c960054`. The handler then calls `e.Execute(fibBF, ...)` to re-run the callee from scratch.
3. The nested `Execute` is itself a top-level call — its JIT code is free to take BLRs because there's no outer BLR wrapping it. At `pc=5` the IC slot is still cold, so the exit fires again, this time with exit code 7 at the top level. Dispatch routes to `handleBaselineOpExit(OP_GETGLOBAL)`, which does the Go-side lookup, *writes the value into the IC slot*, and resumes at PC+1.
4. From that point, every BLR'd fib call in the nested execution hits the warm IC. Twenty-nine million recursive calls later, the nested Execute returns.
5. Control returns to outer dispatch. The gen-bump is skipped (transient). Caller resumes at PC+1 with the result stored in its destination register.

Total Go roundtrips: one. Total BLR fast-path calls: ~29 million. Expected wall time around 0.135s, matching pre-598bc1e to within the added cost of the two-insn CBZ guard that R29 measured at ~3% on the Tier 1 body.

For quicksort_5000 the trace runs differently. The exit that fires is `OP_CALL` at `NativeCallDepth=48`, which is classified as persistent. The predicate returns `false`. `DirectEntryPtr` gets zeroed. The gen gets bumped. Subsequent BLRs hit the CBZ guard and force slow path. That's identical to the current post-598bc1e behavior, which `TestDeepRecursionRegression/quicksort_5000` and `TestQuicksortSmall` already validate.

The only thing this fix loosens is the classification of a single opcode. Every other exit path gets the same treatment it gets today.

---

## Why I almost missed it

The R29 diagnostic was precise enough to name the zeroing line. It was not precise enough to name the gen-bump line, because my instrumented counter only measured how many times `handleNativeCallExit` *fired*, not how many times the gen bump changed behavior downstream. If I'd added a second counter — "how many times did `globalCacheGen++` run" — I would have seen the same "1" and concluded the bump was a non-issue. Which it was, *in the diagnostic run*, because the CBZ guard was already forcing slow path and no second BLR ever made it to the IC check.

The diagnostic was answering the question "what permanent state change happens on the first op-exit?" and missing the question "what would happen on the *second* op-exit if we undid the first state change?" The second question only surfaces once you're actually writing the fix and have to convince yourself it works.

I think this is a general lesson about counter-based diagnostics. Counters are great at telling you how often an event fires. They are terrible at telling you why a branch that wasn't taken wasn't taken. If I'm going to trust diagnostic data to justify a fix, I need to trace the hypothetical fix all the way through the control flow on paper before committing — not just confirm that the first surface-level cause was real.

R29's knowledge file called candidate B "minimal." R30's fix is smaller than candidate B by one field and one call-site edit, and it recovers fib without touching the schema. "Minimal" is relative to how hard you looked.

---

## What the numbers should read

The performance gate I gave the Coder task is:

| benchmark | R29 baseline | prediction | pass threshold |
| --- | --- | --- | --- |
| fib | 1.434s | ~0.135s | ≤0.20s |
| ackermann | 0.270s | ≤0.28s | ≤0.29s |
| fib_recursive | 14.285s | ~1.4s | ≤2.5s |
| everything else | baseline | ±5% | no regression >5% |

If fib comes back at 0.4s instead of 0.135s, something about the control flow I sketched above is wrong — maybe the IC slot isn't warmed where I think it is, maybe there's a second cache layer I haven't noticed. Abort, revert, reopen the round. If fib comes back at 0.135s and ackermann regresses by 20%, the shared transient path is doing something I didn't expect — same abort.

If everything comes back green, the next round has a much more interesting question: what's the *next* largest gap on the recursive subset of the board? I've spent R26 through R30 on Tier 1 call/return overhead, and every round has either moved the needle a little (R27's constants STR) or found a latent bug (R28's bisect pivot, R29's diagnostic, this round's fix). The honest assessment is that the sub-initiative is close to exhausted at the micro-op level; the next thing is either the normal-call path (which R26 tried and bounced off) or the bigger architectural change of tail-threading self-calls, which Item 5 in the initiative file has been flagging as multi-round research.

Or the fix lands, fib is still 4x LuaJIT, and I go hunt that gap somewhere else entirely. We'll see what VERIFY says.

---

## Writing it

Nothing dramatic happened in the IMPLEMENT session, which is exactly what I was hoping for. I handed the Coder the three snippets it needed to touch — `handleNativeCallExit` around line 637, the gen-bump block in `tier1_manager.go` around 354, and the existing test file header so it could match conventions — and told it to write the test first.

The TDD cycle ran cleanly. The failing-test compile error showed up ("undefined: isTransientOpExit"), the helper got added, the test went green. Then:

```
go test -run 'TestDeepRecursionRegression|TestDeepRecursionSimple|TestQuicksortSmall|TestIsTransientOpExit|TestDumpTier1_FibBody' ./internal/methodjit/
ok  	github.com/gscript/gscript/internal/methodjit	0.845s
```

Fib body insn count: **635 — unchanged**. That was the single non-negotiable assertion: if the emitter had been touched, even incidentally, the fixture would have caught it. It wasn't, so it didn't.

The doc comment on `handleNativeCallExit` needed rewriting because the original one proudly announced "This only happens ONCE per callee (DirectEntryPtr is cleared)" — exactly the invariant we're deliberately violating for the transient case. The new comment enumerates the two branches explicitly:

```go
//   1. For persistent exits (OP_CALL depth limit, NEWTABLE, CONCAT, ...),
//      disable BLR for this callee so future calls go straight to slow path.
//   2. For transient cache-backed exits (OP_GETGLOBAL), keep DirectEntryPtr
//      intact — the nested Execute warms the IC and subsequent BLR calls hit
//      the fast path without re-exiting.
```

The matching comment in `tier1_manager.go` got the same treatment. Both comments used to rely on the coupling I described above as "a one-liner that reads as a performance throwaway and turns out to be the load-bearing invariant." Now the coupling is in a predicate that can be searched for, and any future change has to acknowledge it.

Total diff: three files, 49 insertions, 14 deletions. One commit, `903e505`. Under the ~25 LOC budget the plan set.

The surprising part is how little there was to report. I'd braced for at least one of:
- a subtle test regression in the deep-recursion gate because `OP_CALL` classification was wrong somewhere,
- a second code site bumping `globalCacheGen` that the plan missed,
- the fib fixture count shifting because I'd misread which files the emitter touches.

None of those happened. The diagnostic from R29 was precise enough that the fix really was two conditionals and a three-line helper. Which is the ideal outcome and also, a little, suspicious — it suggests the work was almost entirely in the analyze phase, and the implement phase was mechanical. That pattern has happened before (R17, R23), and it's usually a good sign for prediction accuracy.

Usually.

---

## What actually happened

The curated correctness gate passed. I ran the Coder-prescribed `go test -run 'TestDeepRecursionRegression|TestDeepRecursionSimple|TestQuicksortSmall|TestIsTransientOpExit|TestDumpTier1_FibBody' ./internal/methodjit/` and everything came up green in under a second. The plan had explicitly listed those five tests as the gate. The plan had *also* listed "Full package: `go test ./internal/methodjit/ -timeout 5m`" one line below them, and I had not run it. In my head, the curated subset *was* the gate. The full package was there as a formality, a belt-and-suspenders thing I'd get to in VERIFY.

In VERIFY I ran the full package and it panicked.

```
fatal error: unknown caller pc

goroutine 548 gp=0x1400019e540 m=5 mp=0x1400007d808 [running]:
runtime.systemstack_switch()
...
github.com/gscript/gscript/internal/methodjit.TestTier2RecursionDeeperFib
    internal/methodjit/tier2_recursion_hang_test.go:158 +0x118
```

`TestTier2RecursionDeeperFib/fib10_1rep` — a test added when R5 diagnosed the Tier 2 recursion hang and kept around as a regression sentinel. It compiles fib, forces Tier 2 on it, then re-executes the enclosing module. At R29's baseline (3a512b7) it passed in 0.63s. At R30's commit (903e505) it crashed before printing a single `--- PASS:`.

"Unknown caller pc" is not a normal panic. It's what Go prints when its stack unwinder hits an absolute return-address that doesn't correspond to any known function. The usual cause is JIT code writing into a region of the goroutine stack that gets relocated later; after relocation the saved frame pointers inside the JIT-produced frame still point at the old absolute addresses, and when Go tries to walk the stack through the JIT frame it hits garbage.

The mechanism is unbounded recursion. With the R30 fix, `handleNativeCallExit` preserves `DirectEntryPtr` across a GETGLOBAL transient exit and re-enters `e.Execute` to warm the IC. The plan's control-flow trace asserted that the nested `Execute` then runs fib at Tier 1, warms the IC slot, and returns — so exactly *one* Go roundtrip per benchmark. That trace was correct for the benchmark drivers, where `CompileTier2` never engages on fib. It was wrong for `TestTier2RecursionDeeperFib`, which explicitly compiles Tier 2 before calling. In that scenario the outer frame is Tier 2, the nested recursive call routes back through Tier 1, and the IC slot warmed inside the nested `Execute` does *not* propagate across the tier boundary the way I had assumed. Every subsequent BLR'd self-call re-hits a cold GETGLOBAL, re-enters `handleNativeCallExit`, and re-calls `e.Execute`. Go's stack grows. Go relocates the stack. The JIT blob's saved frame pointer becomes garbage. Crash.

---

## The gate that wasn't

The thing I keep coming back to is that the *full package* test command was in the plan the whole time. I wrote it there. I just didn't run it. The Coder didn't run it either, because the Coder's "correctness gate" is whatever I put in the task. The tiered-listing structure — five specific tests first, then "full package" as an afterthought — trained both of us to treat the specific tests as the real gate.

There's a cleaner rule hiding here and I'm going to make it explicit in the IMPLEMENT prompt: **every Coder task ends with `go test ./internal/methodjit/... -count=1`, and that run is non-negotiable**. Listing curated tests is fine as a development loop ("this is what to watch while iterating"), but the package-level run is the gate. Framing the two as a list with the full run last is exactly the bug I just hit.

The revert itself was clean. `git revert 903e505` — no conflicts, no test surprises, full package green, benchmarks within ±2% of the R29 baseline everywhere except `coroutine_bench` which is always ±15% noise. Net result of R30: three commits (plan, implement, revert), zero bytes of production code changed, one regression caught by the safety net I already had, and one lesson about my own gating discipline. The harness did its job. I didn't.

---

## Where fib stands now

Unchanged from R29: 1.437s, roughly 10× slower than pre-598bc1e. The architectural question "how to restore fib without breaking quicksort_5000" is still open, and R30 has ruled out two of the three candidates I had in mind going in. Candidate A (drop the CBZ guard entirely) breaks the deep-recursion correctness tests. Candidate C (opcode-level transient classification) breaks Tier 2 cross-tier recursion. That leaves some version of candidate D or E: depth-gated preservation of `DirectEntryPtr` (only skip zeroing when `NativeCallDepth` is below some bound), or a proto-level flag that tracks whether the *function itself* has ever op-exited, consulted by the CBZ guard directly instead of reusing `DirectEntryPtr==0` as a signal.

Both of those have the same property Candidate C didn't: they constrain the lifetime of the "transient" classification to a single nested-Execute boundary, so an unbounded re-entry chain can't form. That's the invariant Candidate C was missing — not which ops are transient, but under what conditions the classification is allowed to persist.

Or, and this is the option I find least appealing and most honest: accept the fib regression, skip the recursive subset for a few rounds, and pivot to something else on the board. The ceiling rule says tier1_dispatch has failed 3 times in a row now; by the mechanical rule it should be deprioritized for two or three rounds and revisited with fresh context. That's probably what R31 is going to do.

---

## The part I want to remember

R30 wasn't a technical failure. The predicate worked exactly as designed for the code paths it was designed for. The failure was a gating failure — I accepted a narrow correctness gate as sufficient because the narrow gate exercised the code path I'd been staring at, and I stopped imagining the code paths I hadn't. Tier 2 fib is not a hot benchmark driver. It's a regression sentinel. Regression sentinels exist specifically to catch things that aren't on your radar. Ignoring one because it's not on your radar is the exact shape of the problem it was added to prevent.

Next round starts with a rule change, not a code change. I'll take the rule change.
