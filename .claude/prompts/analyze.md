# ANALYZE + PLAN Phase

You are in the ANALYZE+PLAN phase of the GScript optimization loop.
Top-down flow: architecture → strategy → research → source → diagnostics → plan.
**No code changes.** Output: `opt/analyze_report.md` + `opt/current_plan.md` + knowledge base + architecture notes.

## Context — Read These Files (MANDATORY, in order)

1. `CLAUDE.md` — project mission
2. `opt/state.json` — optimization state (`category_failures`, `rounds_since_arch_audit`)
3. `opt/INDEX.md` — **flat index of ALL past rounds — READ CAREFULLY**
4. `opt/initiatives/` — active multi-round initiatives (`ls opt/initiatives/*.md 2>/dev/null`; skip `_template.md`, `README.md`)
5. `docs-internal/architecture/overview.md` — architecture overview
6. `docs-internal/architecture/constraints.md` — **known constraints + ceilings + module notes**
7. `docs-internal/lessons-learned.md` + `docs-internal/known-issues.md`
8. `benchmarks/data/latest.json` + `benchmarks/data/baseline.json`
9. `opt/knowledge/` — **existing knowledge base** (read all .md, build on them)

---

## Step 0 — Architecture Audit (every 2 rounds: FULL / off-rounds: READ ONLY)

Check `opt/state.json` → `rounds_since_arch_audit`.

### If `rounds_since_arch_audit >= 2`: FULL AUDIT

This is a **thorough code reading session**. Goal: update your understanding of the codebase
and write findings into architecture documents. Budget: ~15 minutes.

#### 0a. Run `bash scripts/arch_check.sh`
Mechanical scan: file sizes, pipeline order, tech debt markers, test gaps.

#### 0b. Read key source files (not just target-related)
Walk through the major subsystems. For each, note what changed since last audit:
- **Tier 2 pipeline**: `tiering_manager.go` (compileTier2 function), pass ordering
- **Register allocation**: `regalloc.go` — carried map, FPR/GPR pools, spill strategy
- **Code emission**: `emit_compile.go`, `emit_dispatch.go`, `emit_arith.go` — hot paths
- **Graph builder**: `graph_builder.go` — what gets IR'd, what gets exit-resume'd
- **Loop infrastructure**: `loops.go`, `emit_loop.go`, `pass_licm.go`
- **Tiering policy**: `func_profile.go` — which functions get promoted when

Don't read everything — skim headers + key functions. Note:
- New infrastructure since last audit (new files, new passes, new data structures)
- Growing complexity (functions getting long, abstractions leaking)
- Opportunities spotted (low-hanging fruit, dead code, consolidation chances)

#### 0c. Update `docs-internal/architecture/constraints.md`
Add or revise entries based on what you found. Date each update.

#### 0d. Update `docs-internal/architecture/overview.md` (if pipeline/tiers changed)

#### 0e. Write audit summary
Include in your analyze_report under `## Architecture Audit`. This informs target selection.

### If `rounds_since_arch_audit < 2`: QUICK READ

Just read the existing documents:
- `docs-internal/architecture/constraints.md` (already in Context list)
- Run `bash scripts/arch_check.sh` and scan for ⚠ flags
- Note any constraint that affects target selection
- Include a 2-line summary in analyze_report under `## Architecture Audit`

---

## Step 1 — Gap Classification + Target Selection (strategic)

### Rules
- **Ceiling Rule**: `category_failures >= 2` → FORBIDDEN
- **Initiative Rule**: active initiative with non-empty `Next Step` → strong candidate
- **INDEX pattern check**: don't repeat failed patterns from last 5 rounds

### Task
1. Classify ALL benchmark gaps into canonical categories.
2. Per category: affected benchmarks + total wall-time gap.
3. **Cross-check with constraints.md**: is the proposed target blocked by a known architectural ceiling?
4. Pick target by: ceiling rule → constraints check → initiative rule → ROI.

---

## Step 2 — External Research (knowledge layer)

#### 2a. Web search
Use `WebSearch` for the specific technique. Specific, not generic:
- Good: `"V8 TurboFan escape analysis scalar replacement 2024"`
- Bad: `"how to optimize JIT compiler"`

#### 2b. Reference compiler source
Clone if not cached:
```bash
[ -d /tmp/research-cache/v8 ] || git clone --depth=1 --filter=blob:none https://chromium.googlesource.com/v8/v8.git /tmp/research-cache/v8
```
Grep + read relevant functions. **Cite file:line.**

#### 2c. Update knowledge base
Write or update `opt/knowledge/<topic>.md` with concrete findings.

---

## Step 3 — Project Source Reading (implementation layer)

Read the specific files that this round's target will touch.
Use the architecture overview to locate them. For each file:
- What data structures exist
- What the code already handles vs doesn't
- Existing infrastructure to build on
- Performance-relevant details + design constraints from comments

---

## Step 4 — Micro Diagnostics (instruction layer)

Spawn a sub-agent to get **actual data** from the target benchmark:

1. **IR dump**: `Diagnose()` from `internal/methodjit/diagnose.go`
2. **ARM64 disasm**: Tier 2 disasm harness (`tier2_float_profile_test.go`)
3. **Instruction breakdown**: classify hot-block insns (compute vs overhead)

State concretely: "Hot block has N insns/iter, M overhead. Overhead: X (N%), Y (N%).
Technique eliminates X → −P% estimated (halved for ARM64 superscalar)."

pprof is useless for JIT code. ARM64 disasm is authoritative.

---

## Step 5 — Write Plan (synthesis)

Write `opt/current_plan.md` using `opt/plan_template.md`. Fill ALL sections:

- **Target**: benchmarks + calibrated expected improvement
- **Category**: one canonical category
- **Initiative**: path or "standalone" or "NEW: <name>"
- **Root Cause**: from Step 3-4 data, cross-checked with constraints.md
- **Prior Art**: from Step 2, with file:line citations
- **Approach**: concrete file changes, based on Step 3 source reading
- **Prerequisite**: if arch_check flagged files >800 lines that this round touches → plan includes split as Task 0
- **Expected Effect**: quantified, halved for ARM64 superscalar
- **Failure Signals**: specific conditions
- **Task Breakdown**: each task = one Coder sub-agent, with file + test
- **Budget**: max commits, max files, abort condition

If initiative is **new**, create `opt/initiatives/<name>.md` from `_template.md`.

---

## Step 6 — Write Analyze Report

Write `opt/analyze_report.md`:

```markdown
## Architecture Audit
[Full audit summary OR "Quick read: no new issues. constraints.md current."]
[Flag any ⚠ from arch_check.sh]

## Gap Classification
| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|

## Blocked Categories
- [list or "none"]

## Active Initiatives
- [list or "none"]

## Selected Target
- **Category**: ...
- **Initiative**: ...
- **Reason**: ... (including constraints check)
- **Benchmarks**: ...

## Prior Art Research
### Web Search Findings
### Reference Source Findings
### Knowledge Base Update

## Source Code Findings
### Files Read
### Diagnostic Data
### Actual Bottleneck (data-backed)

## Plan Summary
[1-paragraph: what, expected impact, key risk]
```

---

## Counter Updates

At the end of this phase, update `opt/state.json`:
- If full audit was done: set `rounds_since_arch_audit` to `0`
- Otherwise: leave it (VERIFY+DOCUMENT will increment it)

## Step 7 — Start the round blog post

Create `docs/draft.md` for this round's blog post. This will be updated by IMPLEMENT and VERIFY.

**Writing style**: You're a programmer telling a colleague what you found today. Be specific,
opinionated, show real data. NOT a status report. NOT corporate. Think "engineering notebook
that someone would actually want to read." Reference existing posts in `docs/` for tone.

Write the first section of the blog:

```markdown
---
layout: default
title: "[short evocative title — NOT 'Round N Update']"
permalink: /NN-slug
---

# [Title]

[Opening hook — 1-2 sentences that make someone want to keep reading.
State the problem or surprise, not the solution.]

## What we found

[Describe the analysis: what the numbers showed, what the code revealed,
what the diagnostic data said. Include the actual data (tables, code blocks,
instruction counts). Explain WHY this is the bottleneck — the mechanism,
not just "it's slow." Show what you expected vs what you found.]

## The plan

[What we're going to try and why. Reference prior art (V8/JSC/etc).
What's the risk? What's the expected payoff? Be honest about uncertainty.]

*[This post is being written live. Implementation next...]*
```

Use the next available post number (check `ls docs/*.md | tail -1` for the latest).

## Restrictions
- Do NOT write implementation code
- Only write to `opt/` + `docs-internal/architecture/` + `docs/draft.md`
- If no non-blocked target exists: output `status: all-categories-blocked` and STOP
