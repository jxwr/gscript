# GScript

A dynamically-typed scripting language with a V8-style multi-tier JIT targeting ARM64, implemented in Go. Architecture: **interpreter → baseline JIT (Tier 1) → optimizing JIT (Tier 2)**.

## Mission

**Surpass LuaJIT on wall-time for every benchmark.** No "done," only the next milestone.

## Hard rules

These are compressions of expensive lessons. Each is present-tense, grep-able, or test-expressible. They are not optional.

### Correctness
1. **TDD is mandatory.** Failing test first, then minimum code to pass, then refactor.
2. **Correctness first.** A wrong-but-fast compiler poisons every subsequent comparison.
3. **Never stack on unverified code.** All tests must pass before landing the next change.

### Diagnostics and measurement
4. **Profile before optimizing.** JIT code is opaque to `pprof` — use ARM64 disasm (`Diagnose()`), not Go profilers.
5. **Only `compileTier2()` / `RunTier2Pipeline` / `TieringManager.CompileForDiagnostics` produce authoritative Tier 2 evidence.** Parallel pipelines (`profileTier2Func`, hand-rolled `NewTier2Pipeline` sequences) are banned because they drift from production.
6. **Median-of-N for every benchmark comparison.** Single-shot numbers are ±10% noise on a ±5% signal. Default `--runs=5` for publish-grade, `--runs=3` for mid-round checks.
7. **Contradicted diagnostic data halts the round.** The phrase "the number was off but the conclusion still holds" is never valid. Root-cause the tool first.
8. **IR instruction-count savings do not imply wall-time savings.** M4 executes 6–8 instructions per cycle; removed guards are often free. Always validate with benchmarks.

### Architecture
9. **Architecture-first target selection.** Every round asks in order: (Q1) global architecture? (Q2) module boundary? (Q3) local pass/emit? Only Q3 proceeds without user discussion.
10. **Multiple regressions = architecture problem**, not implementation bug. Don't patch in one place.
11. **V8's architectural choices are the default.** Deviate only with explicit evidence.
12. **Tier 2 is not always faster than Tier 1.** BLR + SSA setup cost more than inlining gains for call-dominated code.

### Code hygiene
13. **No Go file exceeds 1000 lines.** Split at 800. Enforced by `.claude/hooks/file_size_guard.sh`.
14. **One concern per file.** Each file opens with a doc comment.
15. **Test files mirror source:** `foo.go` → `foo_test.go`.
16. **Commit per task.** Each working step is its own commit.

### Workflow
17. **One Claude session per round.** No multi-session phase chaining.
18. **Three-hour round budget.** Over budget → auto-revert.
19. **Only mechanical signals gate a round:** reference.json drift, `kb_check.sh` freshness, test failure, scope-budget breach.
20. **Sunk cost is never a reason to keep broken code.**

## Round shape

A round is a single session with six internal steps. No orchestrator.

```
1. Refresh diagnostics     scripts/diag.sh all          → diag/
2. KB health check         scripts/kb_index.sh          → kb/index/
                           scripts/kb_check.sh          (blocks on staleness)
3. Three-level direction   Read diag/summary.md + kb/modules/architecture.md
                           Q1 → Q2 → Q3 priority; write direction.md
4. Act                     TDD, bounded by direction.md scope
5. Verify                  Re-run diag; diff; pass or revert
6. KB update               Edit any card whose semantics changed; separate commit
```

## Directory pointers

| Path | Purpose |
|------|---------|
| `CLAUDE.md` | This file — hard rules + round shape |
| `docs-internal/workflow-v4-plan.md` | Full rationale for the v4 rebuild |
| `docs-internal/architecture/overview.md` | Tiers, pipeline, register convention |
| `docs-internal/architecture/constraints.md` | Mechanical architectural constraints |
| `docs-internal/decisions/` | ADRs |
| `docs-internal/diagnostics/` | `Diagnose()`, IR pipeline, deopt debugging |
| `docs-internal/known-issues.md` | Current known bugs |
| `docs/` | Blog journal — permanent record of the exploration |
| `kb/architecture.md` | Top-level invariants |
| `kb/modules/` | 26 module cards (schema-enforced) |
| `kb/index/` | L1 mechanical symbol index (auto-generated) |
| `scripts/diag.sh` | Production-parity diagnostic dump |
| `scripts/kb_index.sh` | Regenerate L1 index |
| `scripts/kb_check.sh` | Validate L2 cards against L1 + git blob SHAs |
| `benchmarks/data/reference.json` | Frozen baseline — never rotates |
| `opt/archive/v3/` | Dead v3 harness state. Do not read. |

## Doc-sync rule

See `.claude/rules/doc-sync.md`. On any architectural decision, update the relevant KB card or ADR in the same session. Never leave docs drifted.
