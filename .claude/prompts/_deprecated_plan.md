# PLAN Phase

You are in the PLAN phase of the GScript optimization loop.

## Context
Read these files:
1. `opt/analyze_report.md` — gap analysis from ANALYZE phase
2. `opt/research_report.md` — deep research findings (if RESEARCH phase ran; otherwise skip)
3. `docs-internal/lessons-learned.md` — project-level mistakes
4. `docs-internal/architecture/overview.md` — current architecture
5. `docs-internal/known-issues.md` — known issues
6. The initiative file referenced in analyze_report.md's `Initiative:` field (if any)

## Initiative Handling

If `analyze_report.md` → Initiative is:
- **Existing file** (`opt/initiatives/X.md`): Read it. Plan must advance its next Phase. Put `> Initiative: opt/initiatives/X.md` in the plan header.
- **`NEW: <name>`**: Create `opt/initiatives/<name>.md` from `_template.md`, fill it in, set Status=active. Put the path in the plan header.
- **`standalone`**: No initiative link needed.

## Task
1. If `analyze_report.md` says `research_depth: shallow` AND "Prior Art Needed" has entries, do quick web search first. If `deep`, read `opt/research_report.md` instead.

2. Copy `opt/plan_template.md` to `opt/current_plan.md`.

3. Fill ALL mandatory sections:
   - **Target**: specific benchmarks and expected improvement
   - **Category**: exactly one canonical category (match analyze_report)
   - **Initiative**: path to initiative file, or "standalone"
   - **Root Cause**: architectural bottleneck
   - **Prior Art**: MUST NOT be empty. Reference V8, SpiderMonkey, JSC, .NET, or academic work.
   - **Approach**: concrete file changes
   - **Expected Effect**: quantified predictions
   - **Failure Signals**: specific conditions that mean "stop and rethink"
   - **Task Breakdown**: each task = one Coder sub-agent invocation
   - **Budget**: max commits, max files, abort condition

4. If "Prior Art" section would be empty — STOP. You need more research first.

## Prior Art Research
Prioritize modern production compilers (V8, SpiderMonkey, JSC, .NET RyuJIT) and academic work.
Do NOT reference LuaJIT implementation techniques — our architecture is V8-style Method JIT, not trace-based.

## Rules
- Do NOT write any implementation code
- Do NOT modify any Go source files
- Only write to `opt/` directory
- The plan will be presented to a human for approval before IMPLEMENT
