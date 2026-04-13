---
layout: default
title: "Rebuilding the Workflow"
permalink: /46-rebuilding-the-workflow
---

# Rebuilding the Workflow

Post 23 was about the harness being built. This one is about the harness being torn down.

Between R28 and R35 the optimization loop ran eight times and produced zero production wins. Not "a couple of small improvements" — literally zero commits that moved a benchmark. Here is the ledger, copied straight from `state.json`:

| Round | Target | Outcome |
|------:|--------|---------|
| R28 | Tier 1 self-call overhead | `data-premise-error` — SP-floor approach fails under Go's 8KB goroutine stack |
| R29 | fib +988% root cause | `no_change` — diagnostic only, knowledge doc written |
| R30 | Transient op-exit classification | `regressed` — reverted after full-package tests crashed |
| R31 | Braun redundant phi cleanup | `no_change` — production pipeline already collapses these |
| R32 | Loop scalar promotion for nbody | `no_change` — pass was a silent no-op; float gate never fired |
| R33 | Scalar-promote float gate fix | `data-premise-error` — two other upstream gates bail first |
| R34 | (sanity missed because `claude -p` auth error) | — |
| R35 | object_creation regression bisect | `no_change` — diagnostic only, found the culprit commit |

Eight rounds. Three diagnostic-only. Two reverts. Three no-ops caused by plans that looked correct on paper and did nothing in production. And R34 is a special kind of failure — the harness couldn't even run its own sanity check because of a subprocess auth error, and I didn't notice until later.

This post is about why.

## The obvious mistake

The less-obvious version is that the harness grew elaborate *because* the underlying strategy was failing, and elaboration is a substitute for direction change. Every round added a new check, a new prompt, a new state field. R29 added a knowledge base. R31 added a "production-pipeline diagnostic test" rule. R32 added real-pipeline unit tests as a hard requirement. R33 added a structured evaluator-optimizer plan_check loop. R34 added frozen reference baselines + cumulative drift checks (this one actually helped). R35 added `authoritative-context.json` as a new input artifact.

Each addition was a reasonable response to the previous failure. The aggregate was a workflow so thick that reading the plan for one round took longer than actually writing the code would have.

The obvious mistake, though, is that the harness kept looking in the wrong place. All eight rounds chased IR-level optimizations — new passes, new pass gates, new inlining decisions. The "authoritative context" was a dump of post-`RunTier2Pipeline` IR. Every CONTEXT_GATHER session spent its budget reading that IR, looking for passes that didn't fire.

But R23 had already proved — with explicit A/B — that removing guards at the IR level produces **zero wall-time change** because M4 superscalar hides the removed instructions. That was the dead canary. It meant the frontier had moved to emit/regalloc/microarchitecture, not IR. The harness kept optimizing the IR anyway, because that's where its tools pointed.

## The golden era was thin

R15–R22 produced almost every wall-time win on the board. Native ArrayBool/ArrayFloat fast paths (−18–25% sieve). Tier 1 float/bool table fast paths (−80% matmul, −55% sieve). R(0) pinned to X22, closure cache pinned to X21 (8–23% across eighteen benchmarks). GPR int counter with fused compare+branch (−7.4% fibonacci_iterative). GetGlobal native dispatch (−49% nbody, −90% fib).

Not one of those touched an IR pass. They were all in `emit_*.go` or the register allocator. And the workflow that produced them was so thin it barely existed: look at the ARM64 disasm of the hot loop, notice what's expensive, fix it, commit, move on.

The harness that produced R15–R22 was about a tenth the size of the harness that failed at R28–R35.

## AI-on-AI verification

The sanity phase was supposed to be an independent check — a fresh Claude session with no context reading only artifacts. But eight of its nine red-flag rules read AI-written plans and AI-written verify reports. Only R7 (cumulative drift against the frozen `reference.json`) read mechanical data. Only R7 ever caught a real failure.

The others caught self-contradictions, but a plan can be wrong and self-consistent at the same time. R33's plan proposed a single-cause fix for a multi-cause problem, and sanity passed it — the contradiction wasn't on the page. "AI writes, AI audits" is not independent verification. It's one distribution checking itself.

## What's being rebuilt

The new workflow has three rules.

**One session per round.** No multi-phase orchestrator, no state files ferrying context between independent Claude invocations. Phases become steps inside a single session with working memory. When something needs to change mid-round, you change it — you don't submit a memo to the next phase.

**Diagnostic tools share production code paths.** R31 and R32 both wasted themselves because they used `profileTier2Func`, an older parallel pipeline that diverges from the real `compileTier2()`. The new tool adds one method — `TieringManager.CompileForDiagnostics` — that returns the intermediate artifacts from an *actual* production compile. A bit-identical parity test is the gate. If the tool ever diverges, the test fails and the round stops.

**Knowledge replaces history.** There are thirty-plus entries in `previous_rounds` now, most of them reminders of mistakes not to repeat. They're being archived. In their place: 26 module cards describing the code as it currently is. Present tense. Each card carries the git blob SHAs of the files it describes; a freshness check fails the round if any file has changed without a card update. No round references. No "R33 found". If a card can't justify itself in present tense, it gets deleted.

The cards are inspired by aider's repo-map (tree-sitter symbol index, PageRank-ranked) and the 2025 research on hierarchical repository summarization, but cut to what a compiler actually needs: module purpose, public API, invariants, hot paths, known gaps, tests. Schema enforced. Size capped at 150 lines per card.

Below the cards is a level-1 mechanical index (produced by `go/parser` — we're in Go, we don't need tree-sitter) that answers "where is X defined". Above the cards is a short list of hard rules in `CLAUDE.md`. That's it.

## Architecture first

The last rule is the one most likely to fail. Every round now asks three questions in priority order:

1. Does the evidence point to a **global architecture** question? (tier layout, object model, allocator design, register convention)
2. Does it point to a **module boundary** problem? (where feedback lives, which layer owns deopt, regalloc algorithm choice)
3. Is there a **local pass or emit** fix?

Only Q3 proceeds without user discussion. Q1 and Q2 pause for a one-page proposal first. The priority inverts R28–R35's implicit preference for local passes. Most failures in those rounds came from treating a Q2-shaped problem (feedback boundary broken between Tier 1 and Tier 2, R13) as a Q3-shaped problem (add another IR pass).

Whether this priority actually holds over many rounds is an open question. If every round ends up in Q3 anyway, the workflow collapses to "profile, fix, commit", which is what the golden era was. That wouldn't be a failure.

## What this post waits on

This is the announcement, not the results. Phase 1 archives the old harness. Phases 2–7 build the new infrastructure — diagnostic tool, L1 index, 26 KB cards, freshness check, round runner. Phase 8 is a Round 0 dry-run: no production code changes, just validate that a single Claude session reading `diag/summary.md` plus a handful of KB cards can identify a concrete direction in under fifteen minutes. Phase 9 updates this post with whatever that session actually produced.

If Round 0 hits its success criteria, the workflow is adopted and the first real round under v4 targets the `object_creation` regression that post 45 left unresolved. If it doesn't, the failures get diagnosed in another post, and the plan gets revised.

This blog is the permanent journal now. The harness can be rebuilt — the record stays.

---

## Round 0 results

*(to be filled in after Phases 1–8 complete)*
