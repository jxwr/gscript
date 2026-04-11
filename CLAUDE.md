# GScript

GScript is a dynamically-typed scripting language with a multi-tier JIT compiler targeting ARM64, implemented in Go. Architecture follows V8: **interpreter → baseline JIT → optimizing JIT**.

## Mission

**Surpass LuaJIT on all benchmarks.** V8-style Method JIT (not trace JIT) on Lua-like semantics. Open-ended, iterative — no "done," only the next milestone.

## Meta-Principle: Self-Evolving Workflow

**The harness workflow must be capable of self-evolution. All efforts serve this principle.**

The compiler goal matters, but the higher-order goal is that the *process* improves itself over time. A workflow requiring constant human redesign is brittle. A workflow that evolves its own prompts, tools, and structure based on what it learns each round is antifragile.

- Every round's outcome is feedback on the workflow, not just the compiler
- REVIEW reads user interventions and applies structural changes, not parameter tweaks
- If the user intervenes twice for the same class of problem, the workflow has failed to learn
- New capabilities should emerge from the workflow's own observations

## Workflow: 4-Phase Optimization Loop

Orchestrated by `bash .claude/optimize.sh`. Each phase is an **independent Claude session** — no context accumulation, state passes via files in `opt/`.

```
  REVIEW (every round, reads user session log for interventions)
    │
    ▼
  ANALYZE + PLAN (one session)
    Step 0: Architecture audit (full every 2 rounds / quick read on off-rounds)
    Step 1: Gap classification + target selection (ceiling rule, initiative rule)
    Step 2: External research (web search + reference engine source + knowledge base)
    Step 3: Project source reading (read the actual code that will change)
    Step 4: Micro diagnostics (IR dump, ARM64 disasm, instruction breakdown)
    Step 5: Write plan (concrete, bounded, with calibrated predictions)
    Step 6: Write analyze report
    │
    ▼
  IMPLEMENT (one session)
    Spawn Coder sub-agents per task. TDD. Commit per task.
    │
    ▼
  VERIFY + DOCUMENT (one session)
    Part 1: Run tests + benchmarks + evaluator → fill Results
    Part 2: Update state.json, INDEX.md, initiative, archive plan, commit
    │
    ▼
  SANITY (independent common-sense check, read-only)
    Reads plan + state + benchmark data with fresh context.
    Flags physics violations, prediction gaps, skipped mandated steps,
    stale baselines, scope explosion. Non-clean verdict blocks auto-continue.
```

Multi-round: `bash .claude/optimize.sh --rounds=5`

## Roles

The workflow uses **specialized sub-agents**, not a single monolithic agent:

| Context | Role | What it does |
|---------|------|-------------|
| REVIEW session | **Workflow Auditor** | Reads user session log, identifies intervention patterns, applies harness changes |
| ANALYZE session | **Analyst + Planner** | Top-down: architecture → strategy → research → source → diagnostics → plan |
| IMPLEMENT session | **Orchestrator** | Spawns bounded Coder sub-agents, tracks budget, checks scope |
| Coder sub-agent | **Implementer** | TDD within bounded scope. 3 attempts max, then failure report |
| Evaluator sub-agent | **Code Reviewer** | Reviews git diff against checklist. No intent from caller |
| Diagnostic sub-agent | **Profiler** | IR dumps, ARM64 disasm, instruction counting |
| SANITY session | **Skeptical Reviewer** | Fresh-context common-sense check on round artifacts — no trust in prior reports |

**Main conversation** (with the user): strategic direction, harness design, architectural decisions. Not implementation detail.

## Cross-Round Infrastructure

| File | Purpose |
|------|---------|
| `opt/state.json` | Counters: category_failures, rounds_since_review, rounds_since_arch_audit |
| `opt/INDEX.md` | Flat table of all rounds — ANALYZE's pattern detector |
| `opt/initiatives/*.md` | Multi-round engineering projects |
| `opt/knowledge/*.md` | Persistent knowledge base (techniques, algorithms, thresholds) |
| `opt/plans/*.md` | Archived plans for retrospectives |
| `opt/reviews/*.md` | Harness self-audit reports |
| `opt/workflow_log.jsonl` | Per-round metrics |
| `opt/phase_log.jsonl` | Per-phase model + duration (written mechanically by orchestrator) |
| `opt/sanity_report.md` | Per-round independent common-sense check output |
| `docs-internal/architecture/constraints.md` | Known architectural constraints + ceilings |
| `docs-internal/architecture/overview.md` | Tier/pipeline/register reference |
| `benchmarks/data/latest.json` | Current benchmark results (written by VERIFY) |
| `benchmarks/data/baseline.json` | Comparison baseline |
| `scripts/arch_check.sh` | Mechanical architecture scan (file sizes, tech debt, test gaps) |

## Anti-Drift Mechanisms

| Mechanism | Prevents |
|-----------|----------|
| **Ceiling Rule** (category_failures ≥ 2 → blocked) | Grinding on same wall |
| **Initiative files** | Losing multi-round architectural continuity |
| **Architecture audit** (every 2 rounds) | Structural drift without awareness |
| **Knowledge base** | Forgetting what was researched |
| **REVIEW every round** (reads user interventions) | Workflow rot |
| **Budget per round** | Scope creep |
| **Calibrated predictions** (halved for ARM64 superscalar) | Chronic overestimation |

## Non-Goals

- Trace JIT — deprecated, disconnected from CLI
- Hacks that sacrifice correctness for speed
- Arbitrary speedup ratios — target is LuaJIT's absolute times

## Coding Conventions

### File Size
- **No Go file exceeds 1000 lines.** Split proactively at 800 lines
- One concern per file. Every file starts with a doc comment
- Test files mirror source: `foo.go` → `foo_test.go`
- `scripts/arch_check.sh` scans for violations; ANALYZE Step 0 reads the output

### TDD (mandatory)
1. Write a failing test specifying desired behavior
2. Write minimum code to pass
3. Refactor without changing behavior
4. No exceptions. If you can't write a test, you don't understand the requirement

### Pass Pipeline
- Each pass: `pass_<name>.go` + `pass_<name>_test.go`
- Current order: `BuildGraph → Validate → TypeSpec → Intrinsic → TypeSpec → Inline → TypeSpec → ConstProp → LoadElim → DCE → RangeAnalysis → LICM → Validate → RegAlloc → Emit`
- Ordering constraints documented in `docs-internal/architecture/constraints.md`

### Standards
- **Profile before optimizing**: ARM64 disasm (not pprof — JIT code is opaque to pprof)
- **Diagnostic tools first**: IR printer, pipeline dump, validator before reading source code
- **Commit often**: each working step gets a commit
- **Observation beats reasoning**: 5 hours of guessing vs 5 minutes of looking at the data

## Hard-Won Rules

1. **Observation beats reasoning** — use diagnostic tools (Diagnose(), IR printer, ARM64 disasm). Don't read code and guess.
2. **Architecture over patches** — third bug in same subsystem? Redesign. Don't add special cases.
3. **Never stack on unverified code** — all tests pass before adding the next pass.
4. **Architecture audit every 2 rounds** — review file sizes, module boundaries, pipeline, test coverage. Findings go to `constraints.md`.
5. **Calibrate predictions** — halve instruction-count estimates on ARM64 superscalar. Cross-check with diagnostic data.
6. **Read the code before planning** — ANALYZE must read the source files it plans to change. Rounds 7-8 predicted −35% because nobody read regalloc.go.

## Reference Documents

- Architecture: `docs-internal/architecture/overview.md`
- Constraints: `docs-internal/architecture/constraints.md`
- ADRs: `docs-internal/decisions/`
- Debug JIT: `docs-internal/diagnostics/debug-jit-correctness.md`
- Debug deopt: `docs-internal/diagnostics/debug-deopt.md`
- Debug IR: `docs-internal/diagnostics/debug-ir-pipeline.md`
- Known issues: `docs-internal/known-issues.md`
- Lessons learned: `docs-internal/lessons-learned.md`
- Knowledge base: `opt/knowledge/`
