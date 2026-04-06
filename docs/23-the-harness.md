---
layout: default
title: "The Harness"
permalink: /23-the-harness
---

# The Harness

After twelve optimization rounds, the compiler is about 10% faster. The workflow that optimizes it is unrecognizable.

This post isn't about the compiler. It's about what happened when we tried to automate the process of making it faster, and discovered that the process was the real project all along.

---

Post 20 ended with a pivot: trace JIT → V8-style Method JIT. Two tiers — baseline (1:1 bytecode templates) and optimizing (SSA IR → ARM64). The architecture worked. We had native BLR calls, inline field caches, GETGLOBAL value caches, on-stack replacement. The machine was built. Now we needed to make it fast.

We built an automated optimization loop — each round a fresh Claude Code session cycling through phases: measure the gaps, analyze the bottleneck, plan a fix, implement it, verify, document. Every round independent, state passing through files on disk. Twelve rounds ran. Here's what actually happened:

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

72% overhead, 28% real compute. The specific cause: `carried` map in `regalloc.go` only tracks tight-body header phi values. LICM-hoisted constants defined in pre-headers aren't in the map. The fix was surgical: extend `carried` to include pre-header-defined values used inside the loop. **−12~15% on four benchmarks.**

Round 9 worked because we had data. Rounds 7-8 failed because we had theory.

---

After round 11 (predict 2-4×, have to revert), a pattern was undeniable: **the bottleneck wasn't the compiler. It was the process of deciding what to do to the compiler.**

Every round that failed had the same failure mode: read benchmark numbers → guess the root cause from theory → web-search how V8 solves it → write a plan assuming the V8 technique applies → implement → discover the plan was based on a wrong assumption about our own code.

Every round that succeeded had a different pattern: read the actual ARM64 disassembly → count instructions per category → identify the exact code path causing overhead → check if existing infrastructure supports the fix → plan a surgical change → implement.

The difference isn't technique knowledge. It's **whether you read your own code before planning.**

---

Once we saw this, the workflow itself became the project. Over two days, we rebuilt the entire harness.

**7 phases → 3.** The original loop had seven phases: MEASURE → ANALYZE → RESEARCH → PLAN → IMPLEMENT → VERIFY → DOCUMENT. Each was a separate Claude Code session. The redundancy was staggering — ANALYZE did web search, then PLAN did web search again; MEASURE ran benchmarks, then VERIFY ran the same benchmarks on the same commit; PLAN was just reformatting ANALYZE's output into a template. We collapsed it to three sessions: ANALYZE+PLAN, IMPLEMENT, VERIFY+DOCUMENT. 60% less context loading, zero redundant research.

**ANALYZE learns to read code.** The biggest single change. ANALYZE went from "read benchmark numbers and guess" to a six-step top-down flow: architecture audit → gap classification → external research → read THIS project's source code → micro diagnostics (IR dump, ARM64 disasm, instruction breakdown) → write plan. Step 3 — reading the actual files it plans to change — is the one that was missing for 12 rounds. Step 4 spawns a diagnostic agent that disassembles the hot block and counts instructions.

**The knowledge base.** Each round's research findings now persist in `opt/knowledge/<topic>.md`. Before, every round started from zero: web-search the same techniques, re-read the same V8 source. Now findings accumulate. Round 13's ANALYZE reads what Round 12 learned about feedback-typed loads and builds on it instead of rediscovering it.

**Architecture constraints.** A living document records structural limitations that affect target selection: "Tier 2 is net-negative for recursive functions." "8-FPR pool is a hard limit." "`emit_dispatch.go` 961 lines: approaching limit." Every round reads this before choosing a target. Round 11's lesson is encoded once and respected forever.

**REVIEW reads the user.** The most meta change. REVIEW (which runs every round) now reads the actual session log — the conversation between the user and Claude Code. It looks for interventions: moments where the user corrected or redirected the workflow. The goal isn't to list the user's requests. It's to understand *why* they intervened, check if the fix was already made, and identify remaining gaps. The principle: **if the user has to intervene twice for the same class of problem, the workflow has failed to learn.**

---

This became the project's meta-principle, written into `CLAUDE.md`:

> **The harness workflow must be capable of self-evolution. All efforts serve this principle.**
>
> Achieving the compiler goal matters, but the higher-order goal is that the *process* of achieving it improves itself over time. A workflow that delivers results but requires constant human redesign is brittle. A workflow that evolves its own prompts, tools, and structure based on what it learns each round is antifragile.

We're not there yet. Over 12 rounds, **every significant harness improvement came from human intervention**. REVIEW made parameter tweaks — calibration clauses, cooldown adjustments. But the structural changes — "read the code," "merge redundant phases," "add architecture audit" — all came from the user observing what went wrong and redesigning. The gap between "human-driven evolution" and "self-driven evolution" is the real project now.

---

We set out to build a JIT compiler that beats LuaJIT. We ended up building a framework for iterative improvement — `opt-loop` — that's completely generic. The framework doesn't know it's optimizing a compiler. It just knows: measure something, pick the biggest gap, research how others solved it, read your own code, plan a bounded fix, implement with TDD, verify and record.

We extracted it into a [standalone project](https://github.com/jxwr/opt-loop). You give it a `harness.md` describing what to measure and what categories your work falls into, and it runs the loop. We have example configs for bug-fix campaigns, test-coverage drives, and performance optimization. The anti-drift mechanisms — ceiling rule, initiative files, knowledge base, REVIEW that reads the user — are all generic. They solve the universal problem: long iterative work degrades into firefighting without structure.

---

After 12 rounds, the honest numbers:

| Metric | Value |
|--------|-------|
| Benchmarks correct | 21/22 (was 15/22) |
| Aggregate JIT wall-time improvement | ~10% |
| Best single-benchmark improvement | spectral_norm −16%, nbody −12%, matmul −13% |
| vs LuaJIT gap | Still 7-55× on tracked benchmarks |
| Rounds that delivered real perf gain | 3 out of 12 |
| Rounds that built infrastructure | 4 |
| Rounds that taught us what NOT to do | 3 |
| Rounds that were pure correctness | 3 |

10% aggregate improvement in 12 rounds is not impressive. LuaJIT is still 7-55× faster. But the infrastructure is in place (LICM pass, FPR/GPR carry, feedback-typed guards, pre-header blocks, range analysis), and the workflow now reads the code, runs diagnostics, calibrates predictions, and tracks constraints. The next 12 rounds will be faster because the first 12 taught the process how to work.

Round 13 is running as I write this. The new 3-phase pipeline, REVIEW every round, architecture audit. The ANALYZE phase is reading `emit_table.go` and diagnosing sieve's Tier 2 codegen — something that would have been skipped in the old workflow.

The compiler still needs to beat LuaJIT. The gap is 7-55×. But we're no longer guessing at what to fix. The harness reads the code, counts the instructions, and checks its own predictions.

The process is the product.

---

*Previous: [What Makes a JIT Compiler Fast](/22-jit-optimization-techniques)*

*This is post 23 in the [GScript JIT series](https://jxwr.github.io/gscript/). All numbers from a single-thread ARM64 Apple Silicon machine.*
