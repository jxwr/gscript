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

**Knowledge replaces history.** There are thirty-plus entries in `previous_rounds` now, most of them reminders of mistakes not to repeat. They're being archived. In their place: 28 module cards describing the code as it currently is. Present tense. Each card carries the git blob SHAs of the files it describes; a freshness check fails the round if any file has changed without a card update. No round references. No "R33 found". If a card can't justify itself in present tense, it gets deleted.

The cards are inspired by aider's repo-map (tree-sitter symbol index, PageRank-ranked) and the 2025 research on hierarchical repository summarization, but cut to what a compiler actually needs: module purpose, public API, invariants, hot paths, known gaps, tests. Schema enforced. Size capped at 150 lines per card.

Below the cards is a level-1 mechanical index (produced by `go/parser` — we're in Go, we don't need tree-sitter) that answers "where is X defined". Above the cards is a short list of hard rules in `CLAUDE.md`. That's it.

## Architecture first

The last rule is the one most likely to fail. Every round now asks three questions in priority order:

1. Does the evidence point to a **global architecture** question? (tier layout, object model, allocator design, register convention)
2. Does it point to a **module boundary** problem? (where feedback lives, which layer owns deopt, regalloc algorithm choice)
3. Is there a **local pass or emit** fix?

Only Q3 proceeds without user discussion. Q1 and Q2 pause for a one-page proposal first. The priority inverts R28–R35's implicit preference for local passes. Most failures in those rounds came from treating a Q2-shaped problem (feedback boundary broken between Tier 1 and Tier 2, R13) as a Q3-shaped problem (add another IR pass).

Whether this priority actually holds over many rounds is an open question. If every round ends up in Q3 anyway, the workflow collapses to "profile, fix, commit", which is what the golden era was. That wouldn't be a failure.

## Round 0

Round 0 is the dry-run: build the infrastructure, run the new workflow end-to-end on the current tree, no production code changes, see whether a single Claude session can actually produce a concrete direction from fresh diagnostic data plus a handful of KB cards. The session that built it all is the same session that ran it.

### What got built

Nine phases, nine commits, under a day of elapsed wall-time:

| Phase | Commit | Work |
|------:|--------|------|
| 1 | `32530e1` | Archive 140 files (v3 state + harness + prompts + hooks) into `opt/archive/v3/` |
| 2 | `949ec41` | Rewrite `CLAUDE.md` from 273 to 81 lines. 20 hard rules, no workflow prose. |
| 3 | `3d117de` | Diagnostic tool: `TieringManager.CompileForDiagnostics` + parity test + Go-side dump harness + `scripts/diag.sh` + `scripts/diag_summary.py`. New dep: `golang.org/x/arch/arm64/arm64asm`. Parity structurally asserted on three benchmarks. |
| 4 | `148914e` | `scripts/kb_index.go` (go/parser-based) — 253 files, 4171 symbols, 21571 call edges in 2.7 seconds. |
| 5–7 | `34920e4` | 28 KB cards (2016 lines, 15× compression vs the 45k LOC they summarize). `scripts/kb_check.sh` freshness check. `scripts/round.sh` single-session prep runner. |
| 8 | `080f6ee` | Round 0 dry-run: `scripts/round.sh --no-bench` end-to-end + `docs-internal/round-direction.md` produced from the session. |
| 9 | this commit | This post. |

The KB cards were written partly by hand (11 foundation cards — architecture, ir, regalloc, tier1, tier2, emit overview, and the five runtime cards) and partly by two background agents in parallel (11 pass cards and 6 emit-specialization cards). The agents corrected several filename drifts the task guidance had wrong (`pass_const_prop.go` is actually `pass_constprop.go`, `pass_range_analysis.go` is actually `pass_range.go`, `pass_simplify_phi.go` is actually `pass_simplify_phis.go`) and flagged one missing unit test (`IntrinsicPass` has no dedicated `_test.go`; coverage is indirect). Every card passed `kb_check.sh` on the first run after filename fixes.

### The diagnostic parity test

This is the load-bearing invariant. `internal/methodjit/tiering_manager_diag_test.go` runs both `compileTier2` (production) and `CompileForDiagnostics` on the same proto and asserts structural parity: same instruction count, same per-class histogram, same post-pipeline IR text. Raw ARM64 bytes are NOT compared — each mmap'd code region has different absolute addresses baked into branches, and the expected diff is documented in the test's package comment. On the three benchmarks the test covers (sieve, object_creation, mandelbrot), structural parity holds.

If that test ever fails, the diagnostic tool is lying and any round using it is invalid. The test catches the exact class of failure that wasted R31 and R32 — a "diagnostic pipeline" (`profileTier2Func`) that silently skipped inlining because it was called with `opts=nil`, producing measurements that didn't match production.

`profileTier2Func` is archived at `opt/archive/v3/methodjit-drift/` behind a `//go:build archived_v3` tag that excludes it from every normal build. `NewTier2Pipeline` still exists but is now doc-commented as a Diagnose-only dump helper.

### Timing

`scripts/round.sh --no-bench` from cold cache:

```
[1/3] L1 index       ~3 seconds  (253 files, 4171 symbols)
[2/3] diag.sh all   ~25 seconds  (22 benchmarks, parity-test-gated)
[3/3] kb_check       <1 second   (28 cards validated)
─────────────────────────────
total               ~30 seconds
```

Target was ≤3 minutes. Five times headroom.

### The actual direction

`docs-internal/round-direction.md` — produced from the Round 0 session in well under the 15-minute target — names exactly one target for the first real v4 round. Reproduced in short:

> **Q1 — global?** Tabled. Tier-2-only bump allocator is a real long-term candidate (the allocation_heavy ceiling is a real ceiling), but the current `object_creation +49%` drift is a module-level bug masquerading as a ceiling problem.
>
> **Q2 — module?** *Yes, target.* Two fixes from `kb/modules/runtime/table.md` and `kb/modules/runtime/vm.md` Known gaps, totalling ~80 source LOC:
>
> - Fix A: delete the `shape *Shape` field from `runtime.Table`. It's write-only — the JIT reads `shapeID uint32`, not the pointer. Every Table carries one dead traced Go pointer for the GC to visit.
> - Fix B: add `regHighWater` to `*VM` and update it inside `EnsureRegs`. `ScanGCRoots` currently walks the full register slice; with a high-water mark it scans only the prefix that was ever written.
>
> Calibrated prediction (halved per CLAUDE.md rule 8): `object_creation` 1.141s → ~0.80s (−30%, HIGH confidence), `sort` 0.051s → ~0.045s (−12%, MEDIUM), `closure_bench` + `table_array_access` + `fannkuch` drop below the flag threshold (LOW confidence on exact magnitudes). Other benchmarks: zero change expected.
>
> **Q3 — local?** Not applicable. Q2 is the correct level for the current evidence.

That's the whole direction. No ceremony, no plan.md schema, no token budgets, no evaluator-optimizer loop, no pre-commit hooks, no YAML Assumptions section. Three paragraphs and a prediction.

Post 45 ended with "the fix is two changes… Implementation next…". Round 1 under v4 will actually ship those two changes. Post 47 will be about whether the prediction was right.

### What Round 0 did not prove

Round 0 validates the infrastructure, not the workflow's ability to hold up over many rounds. The real questions are open:

- Will the KB actually stay fresh, or will `last_verified` dates quietly rot and cards start lying about the code? The freshness check catches file-existence drift but doesn't catch a refactor that changes an invariant without changing any listed filename. The quarterly prune is the long-term answer; whether it happens is a question about discipline, not tools.
- Will Q1 and Q2 actually fire, or will every round default to Q3 because the evidence always looks local at the leaf level? R15–R22 were all Q3-shaped (profile, fix, commit) and were the golden era. If v4 collapses back to Q3-only, that's not a failure — it's just admitting the middle layer was imaginary. But the test of the architecture-first discipline is whether a Q1 or Q2 actually gets picked when it's the right call.
- Does the single-session round structure actually hold at 3 hours? Every v3 round took longer than the budget the plan set. The v4 budget might be fiction too. The answer depends on whether "act" stays bounded by the scope the direction set.

None of these are answerable from Round 0. They're answerable from Rounds 1–10. That's the next post's subject.

### The permanent journal

This blog survives every harness rebuild. Post 23 was about the v1 harness being built. This one is about the v3 harness being torn down. Some future post 60-something may be about v4 being torn down too, and that's fine — what stays is the record. The number next to "46" is higher than the number next to "23", and the compiler is not twice as fast, but the workflow is about a third the size and the story about why is written down. Both directions count as learning.

The harness can be rebuilt. The record stays.
