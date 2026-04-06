---
layout: default
title: "The Harness"
permalink: /23-the-harness
---

# The Harness

After twelve optimization rounds, the compiler is about 10% faster. The workflow that optimizes it is unrecognizable.

This post isn't about the compiler. It's about what happened when we tried to automate the process of making it faster, and discovered that the process was the real project all along.

---

## Where we left off

Post 20 ended with a pivot: trace JIT → V8-style Method JIT. Two tiers — baseline (1:1 bytecode templates) and optimizing (SSA IR → ARM64). The architecture worked. We had native BLR calls, inline field caches, GETGLOBAL value caches, on-stack replacement. The machine was built.

Now we needed to make it fast.

---

## Twelve rounds

We built an automated optimization loop — each round a fresh Claude Code session cycling through phases: measure the gaps, analyze the bottleneck, plan a fix, implement it, verify, document. Every round independent, state passing through files on disk.

Twelve rounds ran. Here's what actually happened:

| Round | Target | Predicted | Actual | What went wrong |
|-------|--------|-----------|--------|-----------------|
| 1-3 | Tier 2 correctness | — | 15→21 benchmarks correct | Nothing — this was necessary work |
| 4 | Recursive inlining | faster | **hung** | Tier-up policy flip triggered infinite loop |
| 5 | Diagnose the hang | — | Root cause found | BuildGraph silently drops variadic args |
| 6 | Float loop profiling | — | Diagnostic only | Per-op NaN box/unbox is #1 overhead |
| 7 | FPR-resident SSA | **−35%** | −1.88% | Didn't read the code. Assumed regalloc carries LICM values. It doesn't. |
| 8 | LICM pass | **−40%** | −1.6% | Same mistake. Hoisted constants correctly, but regalloc doesn't know about pre-headers. |
| 9 | Extend carried map | **−15%** | **−12~15%** ✓ | First round with actual diagnostic data. Finally read the ARM64 disasm. |
| 10 | GPR int counter | **−15~20%** | −1~7% | ARM64 superscalar hides instruction savings |
| 11 | Recursive Tier 2 | **2-4×** | **reverted** | Tier 2 BLR is 15-20ns vs Tier 1's 10ns. Net negative for recursion. Nobody checked. |
| 12 | Feedback-typed loads | **−7~11%** | no change | Feedback guard inserted but downstream TypeSpecialize didn't fire |

Look at rounds 7-8 and 11. **The predictions are off by 2× to 25×.** Not because the techniques are wrong — LICM is a real optimization, recursive inlining is a real technique. They fail because the analysis phase didn't read the code it was trying to optimize.

Round 7 predicted −35% on mandelbrot because "per-op box/unbox is the #1 overhead." True. But the fix targeted the wrong layer. The regalloc's `carried` map doesn't include LICM-hoisted values. You'd know this if you read `regalloc.go`. Nobody did. We read V8's source instead of our own.

Round 9 was different. Before writing the plan, we sat down and disassembled mandelbrot's hot inner block:

```
47 instructions per iteration:
  27.7%  real float compute (9 fmul + 3 fadd + 1 fsub)
  17.0%  LICM-hoisted values reloaded every iteration (regalloc doesn't carry them)
  17.0%  loop-carried phi spills
  23.4%  int counter overhead
  10.6%  float compare + bool-box tail
```

72% overhead, 28% real compute. And the specific cause was identifiable: `carried` map in `regalloc.go` only tracks tight-body header phi values. LICM-hoisted constants defined in pre-headers aren't in the map. The fix was surgical: extend `carried` to include pre-header-defined values used inside the loop. **−12~15% on four benchmarks.**

Round 9 worked because we had data. Rounds 7-8 failed because we had theory.

---

## The uncomfortable realization

After round 11 (predict 2-4×, have to revert), a pattern was undeniable:

**The bottleneck wasn't the compiler. It was the process of deciding what to do to the compiler.**

Every round that failed had the same failure mode:
1. Read benchmark numbers (wall-time gaps)
2. Guess the root cause from theory ("box/unbox overhead," "recursive inlining")
3. Web-search how V8 solves it
4. Write a plan assuming the V8 technique applies
5. Implement the plan
6. Discover the plan was based on a wrong assumption about our own code

Every round that succeeded had a different pattern:
1. Read the actual ARM64 disassembly
2. Count instructions per category
3. Identify the exact code path causing overhead
4. Check if existing infrastructure supports the fix
5. Plan a surgical change
6. Implement

The difference isn't technique knowledge. It's **whether you read your own code before planning.**

---

## The workflow rewrite

Once we saw this, the workflow itself became the project. Over two days, we rebuilt the entire harness:

### 7 phases → 3

The original loop had seven phases: MEASURE → ANALYZE → RESEARCH → PLAN → IMPLEMENT → VERIFY → DOCUMENT. Each was a separate Claude Code session.

The redundancy was staggering:
- ANALYZE did web search. PLAN did web search again.
- MEASURE ran benchmarks. VERIFY ran benchmarks again (on the same commit if nothing changed).
- PLAN was just reformatting ANALYZE's output into a template.
- DOCUMENT was just updating counters that VERIFY already computed.

We collapsed it to three sessions:
- **ANALYZE+PLAN**: research + read source + diagnose + write plan
- **IMPLEMENT**: execute the plan
- **VERIFY+DOCUMENT**: test + benchmark + close out

60% less context loading, zero redundant research.

### ANALYZE learns to read code

The biggest single change. ANALYZE went from "read benchmark numbers and guess" to a six-step top-down flow:

```
Step 0: Architecture audit (every 2 rounds — thorough code reading)
Step 1: Gap classification + target selection
Step 2: External research (web search + reference engine source)
Step 3: Read THIS project's source code
Step 4: Micro diagnostics (IR dump, ARM64 disasm, instruction breakdown)
Step 5: Write plan
Step 6: Write report
```

Step 3 is the one that was missing for 12 rounds. ANALYZE now reads the actual files it plans to change — `regalloc.go`, `emit_dispatch.go`, `graph_builder.go` — before writing a plan. Step 4 spawns a diagnostic agent that disassembles the hot block and counts instructions.

### The knowledge base

Each round's research findings now persist in `opt/knowledge/<topic>.md`. Before this, every round started from zero: web-search the same techniques, re-read the same V8 source. Now findings accumulate. Round 13's ANALYZE reads what Round 12 learned about feedback-typed loads and builds on it instead of rediscovering it.

### Architecture constraints

A living document — `docs-internal/architecture/constraints.md` — records structural limitations that affect target selection:

> Tier 2 is net-negative for recursive functions (Round 11): SSA construction + type guards + BLR ~15-20ns overhead > inlining gains.

> 8-FPR pool is a hard limit: carried invariants + body temps share 8 registers.

> `emit_dispatch.go` 961 lines: approaching 1000-line limit. Next change must split first.

Every round reads this before choosing a target. Round 11's lesson — "don't try to promote recursive functions to Tier 2" — is encoded once and respected forever.

### REVIEW reads the user

The most meta change. REVIEW (which runs every round) now reads the actual session log — the conversation between the user and Claude Code. It looks for **interventions**: moments where the user corrected or redirected the workflow.

```
| "ANALYZE没有阅读本工程的代码" | workflow was guessing, not reading | implemented |
| "这几个阶段会不会重复劳动"   | 7→3 phase consolidation            | implemented |
| "架构快检改为架构审查"       | need top-down, not just micro       | implemented |
```

The goal isn't to list the user's requests. It's to understand *why* they intervened, check if the fix was already made, and identify remaining gaps. The principle: **if the user has to intervene twice for the same class of problem, the workflow has failed to learn.**

---

## The self-evolution principle

This became the project's meta-principle, written into `CLAUDE.md`:

> **The harness workflow must be capable of self-evolution. All efforts serve this principle.**

> Achieving the compiler goal matters, but the higher-order goal is that the *process* of achieving it improves itself over time. A workflow that delivers results but requires constant human redesign is brittle. A workflow that evolves its own prompts, tools, and structure based on what it learns each round is antifragile.

We're not there yet. Over 12 rounds, **every significant harness improvement came from human intervention**. REVIEW made parameter tweaks — calibration clauses, cooldown adjustments. But the structural changes — "read the code," "merge redundant phases," "add architecture audit" — all came from the user observing what went wrong and redesigning.

The gap between "human-driven evolution" and "self-driven evolution" is the real project now.

---

## What's interesting about this

We set out to build a JIT compiler that beats LuaJIT. We ended up building a framework for iterative improvement — `opt-loop` — that's completely generic. The framework doesn't know it's optimizing a compiler. It just knows:

1. Measure something
2. Pick the biggest gap
3. Research how others solved it
4. Read your own code (!)
5. Plan a bounded fix
6. Implement with TDD
7. Verify and record

We extracted it into a [standalone project](https://github.com/jxwr/opt-loop). You give it a `harness.md` describing what to measure and what categories your work falls into, and it runs the loop. We have example configs for bug-fix campaigns, test-coverage drives, and performance optimization.

The anti-drift mechanisms — ceiling rule, initiative files, knowledge base, REVIEW that reads the user — are all generic. They solve the universal problem: long iterative work degrades into firefighting without structure.

---

## The numbers (honest)

After 12 rounds:

| Metric | Value |
|--------|-------|
| Benchmarks correct | 21/22 (was 15/22) |
| Aggregate JIT wall-time improvement | ~10% |
| Best single-benchmark improvement | spectral_norm −16%, nbody −12%, matmul −13% |
| vs LuaJIT gap | Still 7-55× on tracked benchmarks |
| Rounds that delivered real perf gain | 3 out of 12 |
| Rounds that built infrastructure (LICM, carried map, feedback guards) | 4 |
| Rounds that taught us what NOT to do | 3 |
| Rounds that were pure correctness | 3 |

10% aggregate improvement in 12 rounds is not impressive. LuaJIT is still 7-55× faster on everything. But:

1. The infrastructure is in place: LICM pass, FPR/GPR carry, feedback-typed guards, pre-header blocks, range analysis
2. The workflow now reads the code, runs diagnostics, calibrates predictions, and tracks constraints
3. The next 12 rounds will be faster because the first 12 taught the process how to work

---

## What's next

Round 13 is running as I write this. The new 3-phase pipeline, REVIEW every round, architecture audit. The ANALYZE phase is reading `emit_table.go` and diagnosing sieve's Tier 2 codegen — something that would have been skipped in the old workflow.

The compiler still needs to beat LuaJIT. The gap is 7-55×. But we're no longer guessing at what to fix. The harness reads the code, counts the instructions, and checks its own predictions.

The process is the product.

---

*Previous: [What Makes a JIT Compiler Fast](/22-jit-optimization-techniques) — switching from trace JIT to method JIT*

*This is post 21 in the [GScript JIT series](https://jxwr.github.io/gscript/). All numbers from a single-thread ARM64 Apple Silicon machine.*
