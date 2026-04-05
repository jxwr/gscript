# PLAN Phase

You are in the PLAN phase of the GScript optimization loop.

## Context
Read these files:
1. `.claude/analyze_report.md` — gap analysis from ANALYZE phase
2. `.claude/lessons-learned.md` — past mistakes
3. `docs-internal/architecture/overview.md` — current architecture
4. `docs-internal/known-issues.md` — known issues

## Task
1. If `analyze_report.md` says "Prior Art Needed", spawn a Researcher sub-agent FIRST:
   - Web search for how V8/LuaJIT/SpiderMonkey solve this problem
   - Search for relevant academic papers
   - Summarize findings

2. Copy `.claude/plan_template.md` to `.claude/current_plan.md`

3. Fill ALL mandatory sections:
   - **Target**: specific benchmarks and expected improvement
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
- Only write to `.claude/` directory
- The plan will be presented to a human for approval before IMPLEMENT
