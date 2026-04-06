# ANALYZE + PLAN Phase

You are in the ANALYZE+PLAN phase of the GScript optimization loop.
Your job: classify gaps → pick a target → research it → read the code → diagnose → write a concrete plan.
**No code changes.** Output: `opt/analyze_report.md` + `opt/current_plan.md` + knowledge base updates.

## Context — Read These Files (MANDATORY, in order)

1. `CLAUDE.md` — project mission
2. `opt/state.json` — optimization state (check `category_failures` counters)
3. `opt/INDEX.md` — **flat index of ALL past rounds — READ CAREFULLY, this is the pattern detector**
4. `opt/initiatives/` — active multi-round initiatives. `ls opt/initiatives/*.md 2>/dev/null`; skip `_template.md` and `README.md`
5. `docs-internal/lessons-learned.md` — project-level detours and mistakes
6. `docs-internal/known-issues.md` — current known issues
7. `benchmarks/data/latest.json` — current benchmark results (produced by last round's VERIFY)
8. `benchmarks/data/baseline.json` — previous baseline
9. `opt/measure_report.md` — if exists, read for context (may be stale)
10. `opt/knowledge/` — **existing knowledge base** (read all .md files, build on them)

## Category Taxonomy (from opt/INDEX.md)

Every target MUST be classified into ONE canonical category:
`recursive_call`, `tier2_float_loop`, `tier2_correctness`, `allocation_heavy`,
`gofunction_overhead`, `field_access`, `call_ic`, `regalloc`,
`missing_intrinsic`, `arch_refactor`, `other`.

## Ceiling Rule (MANDATORY)

Read `opt/state.json` → `category_failures`. Any category with `count >= 2`:
- **FORBIDDEN.** Pick a different category or continue an active initiative in a different category.
- State in your report: "Category X blocked (failures=N, last: <round-id>)".

## Initiative Rule (MANDATORY)

For each initiative with `Status: active` AND non-empty `Next Step`:
- **Strong candidate.** Prefer over opportunistic new targets when ROI is comparable.
- If you skip an active initiative, explain why.

---

## Step 1 — Classify gaps and pick target

1. Classify ALL benchmark gaps into canonical categories.
2. Per category: count affected benchmarks + total wall-time gap.
3. Pick target by: ceiling rule → initiative rule → INDEX pattern check → ROI.

## Step 2 — Web search + reference engine source

#### 2a. Web search
Use `WebSearch` for the specific technique. Specific queries:
- Good: `"V8 TurboFan escape analysis scalar replacement 2024"`
- Bad: `"how to optimize JIT compiler"`

#### 2b. Reference compiler source
Clone if not cached:
```bash
[ -d /tmp/research-cache/v8 ] || git clone --depth=1 --filter=blob:none https://chromium.googlesource.com/v8/v8.git /tmp/research-cache/v8
```
Grep and read the relevant functions. **Cite file:line.**

#### 2c. Update knowledge base
Write or update `opt/knowledge/<topic>.md` with concrete findings (thresholds, algorithms, file:line).

## Step 3 — Read THIS project's source code (MANDATORY)

**Most important step.** Without this, predictions will be off by 2-25×.

#### 3a. Read relevant source files
Based on the target, read the files that will need to change. Use `docs-internal/architecture/overview.md` to locate them.
- Look at actual data structures, what the code already handles vs doesn't
- Find existing infrastructure to build on
- Note performance-relevant details and design constraints

#### 3b. Run diagnostic tools on target benchmark
Spawn a sub-agent to get **actual data**:
1. **IR dump**: `Diagnose()` from `internal/methodjit/diagnose.go`
2. **ARM64 disasm**: Tier 2 disasm harness (`tier2_float_profile_test.go`)
3. **Instruction breakdown**: classify hot-block insns (compute vs overhead)

pprof is useless for JIT code (opaque `runtime._ExternalCode`). ARM64 disasm is authoritative.

#### 3c. Identify ACTUAL bottleneck with data
State concretely: "Hot block has N insns/iter, M overhead. Overhead: X (N%), Y (N%). Technique eliminates X → −P% estimated (halved for ARM64 superscalar)."

## Step 4 — Write the plan

Now write `opt/current_plan.md` using `opt/plan_template.md`. Fill ALL sections:

- **Target**: specific benchmarks + expected improvement (calibrated — halve instruction-count estimates on ARM64)
- **Category**: one canonical category
- **Initiative**: path or "standalone" or "NEW: <name>"
- **Root Cause**: from Step 3 data, not guesswork
- **Prior Art**: from Step 2, with file:line citations
- **Approach**: concrete file changes, based on Step 3a source reading
- **Expected Effect**: quantified, cross-checked against Step 3c diagnostic data
- **Failure Signals**: specific conditions
- **Task Breakdown**: each task = one Coder sub-agent, with file + test
- **Budget**: max commits, max files, abort condition

If an initiative is referenced and it's **new**, create `opt/initiatives/<name>.md` from `_template.md`.

## Step 5 — Write analyze report

Write `opt/analyze_report.md`:

```markdown
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
- **Reason**: ...
- **Benchmarks**: ...

## Prior Art Research
### Web Search Findings
[...]
### Reference Source Findings
[engine, file:line, algorithm, thresholds]
### Knowledge Base Update
[which opt/knowledge/ file updated]

## Source Code Findings
### Files Read
[project files read + key observations]
### Diagnostic Data
[hot block breakdown, instruction counts]
### Actual Bottleneck
[data-backed: "N insns/iter, M% overhead from X"]

## Plan Summary
[1-paragraph: what IMPLEMENT will do, expected impact, key risk]
```

## Restrictions
- Do NOT write implementation code
- Only write to `opt/` directory
- If no non-blocked target exists, output `status: all-categories-blocked` and STOP
