# Workflow v4 — Rebuild Plan

**Date**: 2026-04-13
**Status**: drafted, pre-execution
**Supersedes**: harness v3 (`opt/archive/v3/` after Phase 1)

## Why rebuild

Eight consecutive rounds (R28–R35) produced zero production wins. Seven of those ended in `no_change`, `regressed`, `data-premise-error`, or `diagnostic-only` outcomes. The harness kept growing — plan_check evaluator-optimizer loops, 9-check sanity reports, confidence-label auditing, category failure counters, ceiling decay — and the elaboration was a substitute for forward motion, not a driver of it.

The root problems, in order of severity:

1. **ROI migrated from IR to emit/regalloc**, but the harness's "authoritative context" was still `RunTier2Pipeline` IR dumps. CONTEXT_GATHER kept finding "IR-level opportunities" that either were already handled or couldn't translate to wall-time (M4 superscalar hides them — R23 proved this with explicit A/B).
2. **AI-on-AI verification**. Sanity read AI-written plans and AI-written verify reports. Only R7 (cumulative drift vs frozen `reference.json`) was a truly independent signal — and it was the only check that caught real failures.
3. **State accumulated faster than it compressed**. `previous_rounds` grew to 30+ entries; `knowledge/` became a graveyard of abandoned observations; `initiatives/` looked like engineering management but never steered a round's target.
4. **User intervened every round**, violating the self-evolution meta-principle. That was the workflow shouting "I'm broken."

Meanwhile R15–R22 — the golden era — used a workflow so thin it barely existed: profile, fix, commit. The wins all came from emit/regalloc (native array fast paths, R(0) pinning, fused compare+branch, GetGlobal dispatch). The later harness grew in a direction that made those wins harder to find, not easier.

## Principles for v4

1. **One session per round.** No multi-session phase chaining. Phases collapse to steps inside a single Claude session. State flows through working memory, not JSON files.
2. **Diagnostic tools share production code paths.** Any tool that dumps IR/asm/stats for a benchmark must call the same `compileTier2()` path used at runtime. `profileTier2Func`-style parallel pipelines are banned because R31/R32 proved they silently diverge and invalidate everything downstream.
3. **Knowledge base replaces history.** The KB describes the code as it currently is (present tense, mechanically verifiable). `previous_rounds` is archived and not read. If a past insight matters, it lives as a grep-able invariant in a KB card or a hard rule in CLAUDE.md.
4. **Architecture-first target selection.** Every round asks three questions in priority order: (Q1) is there a global architecture question the diagnostics point to? (Q2) a module boundary / algorithm problem? (Q3) a local optimization? Only Q3 proceeds without user discussion.
5. **Mechanical signals only for gating.** The only things allowed to block a round are (a) `reference.json` drift, (b) `kb_check.sh` staleness, (c) test suite failure, (d) scope budget exceeded. No AI-narrated checklists.
6. **Blog as permanent journal.** Every non-trivial workflow change gets a blog post. Blog posts are the long-term memory for the "AI writes a compiler" exploration; they survive harness rewrites.

## KB design (informed by aider repo-map + 2025 hierarchical summarization research)

The KB has three layers. Each layer answers a different question and has a different update frequency.

### L1 — Mechanical symbol index (auto-generated)

Location: `kb/index/`
Generator: `scripts/kb_index.sh` using `go/parser` from the standard library (no external deps, we're in Go)
Outputs:
- `symbols.json` — every top-level `func`/`type`/`const` with file + line + signature
- `file_map.json` — per-file metadata: package, LOC, test file (if any), public symbols
- `call_graph.json` — function-to-function edges within `internal/` (shallow; cross-package only)

L1 is never read directly by the AI. It exists to:
- Answer "where is `FooBar` defined?" via a helper script
- Feed `kb_check.sh`'s staleness detection
- Provide PageRank-ranked symbol lists if L2 cards grow too large (aider-style optimization, defer until needed)

Regenerated at the start of every round. Expected to run in <10 seconds.

### L2 — Concept cards (hand-curated, machine-validated)

Location: `kb/modules/`
Format: markdown with YAML frontmatter
Schema (hard-enforced by `kb_check.sh`):

```markdown
---
module: <dotted name>
files:
  - path: internal/methodjit/foo.go
    sha: <git blob SHA at last verification>
  - path: internal/methodjit/foo_test.go
    sha: <git blob SHA at last verification>
last_verified: 2026-04-13
---

# <Module name>

## Purpose
<≤3 sentences, present tense, no historical references>

## Public API
- `func Foo(x Bar) (Baz, error)` — <one line>
- `type Qux struct` — <one line>

## Invariants
- MUST: <each is grep-able or test-expressible>
- MUST NOT: ...

## Hot paths
- Consumed by benchmarks: sieve, matmul
- Hot lines: foo.go:145-170 (inner dispatch)

## Known gaps (forward-looking)
- <what is currently missing, no round history>

## Tests
- foo_test.go::TestFooPreservesInvariant — covers <invariant name>
```

Card inventory (target ≥20):

```
kb/modules/
├── architecture.md          (≤300 lines, top-level invariants)
├── ir.md                    (Function, Block, Value, RPO, SSA)
├── passes/
│   ├── overview.md          (pipeline order, pass contract)
│   ├── type_specialize.md
│   ├── intrinsic.md
│   ├── inline.md
│   ├── const_prop.md
│   ├── load_elim.md
│   ├── dce.md
│   ├── range_analysis.md
│   ├── licm.md
│   ├── scalar_promote.md
│   └── simplify_phi.md
├── regalloc.md              (linear scan, phi coalescing, spill)
├── tier1.md                 (baseline dispatcher, self-call, feedback recording)
├── tier2.md                 (TieringManager, compileTier2 entry, OSR)
├── emit/
│   ├── overview.md          (ARM64 conventions, X19-X28, X22=R(0), X21=closure cache)
│   ├── table.md             (native array fast paths, shape guards)
│   ├── call.md              (self-call, Go/JIT transitions, deopt)
│   ├── global.md            (GetGlobal dispatch)
│   ├── guard.md             (guard lowering, CSE, hoisting)
│   └── arith.md             (int48, overflow elision, fused compare+branch)
├── runtime/
│   ├── vm.md                (regs, ScanGCRoots, call stack)
│   ├── gc.md                (Go GC ceiling, write barrier)
│   ├── shape.md             (Shape, FieldMap, shapeID)
│   ├── table.md             (Table struct, native kinds)
│   └── coroutine.md
└── feedback.md              (Tier 1 recording → Tier 2 consumption)
```

That's 26 cards. Each ≤150 lines. Total KB: ~3k lines. Compare to the 45k LOC it summarizes — 15× compression.

### L3 — Hard rules (CLAUDE.md)

≤20 terse rules, each a compression of a hard-earned lesson. Examples:
- "Profile before optimizing. ARM64 disasm, not pprof — JIT code is opaque to pprof."
- "IR-layer instruction count ≠ wall-time. M4 superscalar hides guard removal. Validate with benchmarks."
- "Only `compileTier2()` or `RunTier2Pipeline` produces authoritative Tier 2 evidence. `profileTier2Func` is banned."
- "No Go file > 1000 lines."
- "TDD mandatory."
- "Architecture-first: global > module > local. Prefer higher level if there is a candidate."

Rules don't cite past rounds. They're present-tense invariants.

## Diagnostic tool design

Location: `scripts/diag.sh` + a new Go test harness at `internal/methodjit/diag/diag_test.go`

### Production parity constraint

The single non-negotiable: whatever IR/asm the tool dumps must come from the *same* code path production uses. R31 and R32 both wasted rounds because `profileTier2Func` was a stale parallel pipeline. To prevent recurrence, the tool will:

1. Add a new exported method on `TieringManager`:
   ```go
   func (tm *TieringManager) CompileForDiagnostics(proto *vm.FuncProto) (*DiagResult, error)
   ```
   which internally calls the same sequence as `compileTier2()` (BuildGraph → RunTier2Pipeline → AllocateRegisters → Compile → Finalize) but returns the intermediate artifacts instead of installing the compiled function.
2. The diag test harness calls `CompileForDiagnostics` exclusively. Any diagnostic that can't be produced through this method is not produced at all.
3. A unit test asserts that for a fixed input, the instruction bytes produced by `CompileForDiagnostics` are bit-identical to those produced by the real `compileTier2()` — regression guard against future divergence.

### Outputs (per benchmark)

```
diag/<benchmark>/
├── stats.json         # wall_ms, luajit_ms, gap_x, insn_total, insn_histogram, drift_pct
├── ir.txt             # post-RunTier2Pipeline IR dump of hottest function
├── asm.txt            # ARM64 disasm of same function
├── hotspot.md         # AI-readable: identified hottest basic block, loop depth, op mix
└── delta.md           # diff vs previous run, if exists
```

Aggregate `diag/summary.md`:
- Top-5 drift vs `reference.json`
- Top-5 gap vs `luajit`
- Anomalies (opposite-sign deltas, ratio drift)
- Per-benchmark one-liner with current direction hint

### Runtime budget

Target: full diag for 20 benchmarks in ≤3 minutes (parallelizable since each is independent). `scripts/diag.sh all` runs everything; `scripts/diag.sh <bench>` runs one. `diag/` is gitignored — it's regenerated data, not source.

## Round shape (single session, six steps)

```
Step 1: Refresh diagnostics          (≤5 min, mostly automated)
  bash scripts/diag.sh all
  → diag/<bench>/{ir,asm,stats,hotspot}.*
  → diag/summary.md

Step 2: KB health check              (≤2 min, automated)
  bash scripts/kb_index.sh           # regenerate L1
  bash scripts/kb_check.sh           # verify L2 freshness against L1 + git blob SHAs
  → stale cards block the round; fix KB first

Step 3: Three-level direction check  (≤15 min, AI)
  Read: diag/summary.md + kb/modules/architecture.md
  Q1 (global): does the evidence point to a whole-architecture question?
       YES → write ≤1-page proposal → STOP, discuss with user
  Q2 (module): module boundary / algorithm issue?
       YES → write ≤1-page proposal → discuss if scope >200 LOC
  Q3 (local): pass/emit-level candidate?
       proceed to Step 4 with bounded scope

Step 4: Act                          (≤2 h)
  Read only the kb/modules/*.md cards matching the target
  TDD, commit per task, scope locked by Step 3

Step 5: Verify                       (≤10 min)
  bash scripts/diag.sh <affected-benchmarks>
  diff diag/<bench>/delta.md
  pass → Step 6; fail → git revert commits

Step 6: KB update                    (≤15 min)
  Edit cards whose semantics changed
  Regenerate last_verified SHAs
  Separate commit for KB update
```

Hard cap: **3 hours total per round**. Over budget = auto-revert and mark round failed.

## Freshness / staleness strategy

Informed by the 2025-2026 LLM-memory research findings: staleness is a critical failure mode ("a memory-equipped agent can turn one mistake into a recurring one").

**Per-card freshness**: each card's frontmatter records a git blob SHA for every listed file. `kb_check.sh` compares current SHAs to recorded SHAs; mismatch = card flagged stale. Stale cards block the round — must be reconciled (either updated or explicitly acknowledged via a fresh `last_verified` date).

**Quarterly prune**: every 12 rounds (~quarterly), a dedicated round reads every card and asks "if this were deleted, what would break?". Cards that can't justify themselves are removed. Prevents KB from becoming the new `previous_rounds` graveyard.

**No round-indexed entries**: KB never references rounds, PRs, commit SHAs, or historical discoveries. If a past round found something, the finding is either (a) now a present-tense invariant in a card, (b) a hard rule in CLAUDE.md, or (c) deleted because it's obsolete.

## Execution phases

| Phase | Work | Commit |
|------:|------|--------|
| 0 | Plan (this file) + blog draft | (already committed as checkpoint 06e7898) |
| 1 | Archive `opt/*` + old harness into `opt/archive/v3/` | `workflow v4: archive v3 state` |
| 2 | Rewrite `CLAUDE.md` to ≤80 lines | `workflow v4: minimal CLAUDE.md` |
| 3 | Diagnostic tool (shell + Go harness + parity test) | `workflow v4: production-parity diagnostic tool` |
| 4 | L1 mechanical index | `workflow v4: L1 symbol index (go/parser)` |
| 5 | L2 KB seed (26 cards) | `workflow v4: KB seed (26 cards)` |
| 6 | `kb_check.sh` freshness script | `workflow v4: KB staleness check` |
| 7 | New round runner | `workflow v4: round runner` |
| 8 | Round 0 dry-run (no code changes, infrastructure validation) | (dry run: diag/ + direction.md, no commits) |
| 9 | Publish blog post 46 with actual results | `docs: post 46 rebuilding the workflow` |

## Success criteria

Round 0 is successful if, with no human intervention:

1. `scripts/diag.sh all` produces valid artifacts for all 20 benchmarks in under 5 minutes.
2. Sample comparison: run `compileTier2()` and `CompileForDiagnostics` on `sieve` → bit-identical ARM64 instruction bytes.
3. `scripts/kb_check.sh` passes on the 26 seed cards.
4. A single Claude session reads `diag/summary.md` + `kb/modules/architecture.md` + 2–3 module cards matching the top drift benchmark and produces a `direction.md` in <15 minutes that names a concrete Q1/Q2/Q3 target with evidence citations.
5. Total context read by the session: substantially less than reading raw source would be (target: <10k lines of KB + diag vs the current baseline of reading arbitrary source files).

If success criteria hold, the workflow is adopted. If not, the failures are diagnosed (in the blog post) and the plan is revised.

## Archival policy

Everything being archived in Phase 1 is kept at `opt/archive/v3/`. It is:
- Not read by any script, prompt, or round under v4
- Preserved for historical review only (if a future post-mortem needs it)
- Excluded from `kb_check.sh` scans and L1 indexing

`docs/draft.md` (post 45 draft — "The Dead Pointer") is preserved in place. When Round 0 under v4 is done, R36's actual forward fix will happen under the new workflow, and post 45 can be completed then.

## What this plan does NOT do

- It does not redesign the compiler. All IR passes, emit layer, runtime stay as-is.
- It does not change the mission (still: surpass LuaJIT absolute wall-time).
- It does not remove the frozen `reference.json` baseline — that's the single mechanical signal worth keeping.
- It does not write any production Go code. The only new Go code is `CompileForDiagnostics` on `TieringManager` (a thin wrapper over existing production code) and the diag test harness.

## Open risks

1. **KB maintenance cost**. If updating cards after every round turns out to exceed the 15-minute budget, the KB will fall out of date quickly. Mitigation: keep cards small, enforce schema, quarterly prune.
2. **Diagnostic tool drift**. If `CompileForDiagnostics` ever silently diverges from production, the whole workflow is invalidated. Mitigation: the bit-identical parity test is a CI gate, not a nice-to-have.
3. **Architectural candidates may not exist**. Q1 and Q2 assume there are global/module architecture questions to ask. If every round ends up in Q3 (local), the workflow collapses to "profile, fix, commit" — which is actually fine, because that's what the R15–R22 golden era was.
4. **Blog voice drift**. The blog is meant to be a permanent journal; if the voice becomes mechanical or celebratory, it loses its value as an honest record of a weird experiment. Mitigation: keep posts short, numbers-first, admit failures.
