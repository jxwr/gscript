# RESEARCH Phase (optional, deep dive)

You are in the RESEARCH phase of the GScript optimization loop.
This phase runs ONLY if ANALYZE marked `research_depth: deep` in its report.

## When This Fires

- ANALYZE's selected target needs technique-level prior art that web-search summaries can't provide.
- PLAN from a prior round was thin on prior art and got stuck.
- An initiative in `opt/initiatives/` flagged "needs source-level study".

## Context

Read these files:
1. `opt/analyze_report.md` — the selected target + what needs researching
2. `opt/initiatives/*.md` — any active initiatives that may need this research

## Task

1. **Identify the exact technique** needed (from analyze_report's "Prior Art Needed" list).

2. **Do deep research** — not just web summaries:
   - Read V8 / SpiderMonkey / JavaScriptCore / HotSpot source code for the technique. Prefer direct source reads over blog posts.
   - If a repo isn't available locally, `git clone --depth=1 --filter=blob:none` into `/tmp/research-cache/<repo>/`. Keep the cache across rounds.
   - Read the original papers if they exist (CiteSeerX, arXiv, ACM DL free mirrors).
   - Follow issue trackers / commit history for design decisions.

3. **Extract concrete numbers** — thresholds, bytecode budgets, recursion limits, specific heuristics. Generic advice is useless.

4. **Write `opt/research_report.md`** with:

```markdown
## Target Technique
[1-sentence description]

## Production-Compiler Implementations

### V8 (TurboFan/Maglev)
- File: `v8/src/compiler/...` (cite path:line)
- Algorithm: [steps]
- Thresholds: [concrete numbers]
- Caveats observed in code comments

### [Other engines similarly]

## Key Differences Across Implementations
[What's universal? What's engine-specific?]

## Application to GScript
- Closest existing code: `internal/methodjit/...`
- What we'd have to add/change
- Which existing abstractions accommodate this
- Which abstractions would have to bend

## Recommended Numbers (for PLAN to use)
- [Parameter]: [value] (justification: [source])

## Risks
[What's different about our architecture that might make this fail?]
```

## Restrictions

- Read-only phase — no Go code changes.
- `/tmp/research-cache/` writes allowed.
- Do NOT start writing a plan. That's PLAN's job.
- If after 1 hour you haven't found concrete numbers, output what you found with `research_status: incomplete`. The PLAN phase will either proceed with partial info or loop back.
