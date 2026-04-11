# ANALYZE + PLAN Phase

You are in the ANALYZE+PLAN phase of the GScript optimization loop.
Top-down flow: architecture → strategy → research → source → diagnostics → plan.
**No code changes.** Output: `opt/analyze_report.md` + `opt/current_plan.md` + knowledge base + architecture notes.

## Context — Load ALL data in ONE call

**IMPORTANT**: Do NOT read files one by one with the Read tool. Use ONE Bash call:

```bash
bash scripts/analyze_dump.sh
```

This dumps in one shot: state.json, INDEX.md, overview.md, constraints.md, lessons-learned,
known-issues, latest.json, baseline.json, active initiatives, and KB files matching last round's category.

Note: large KB files from other categories are skipped to save tokens. The dump footer lists what was skipped. If your selected target requires a skipped KB file, fetch it with `Read opt/knowledge/<file>.md`.

CLAUDE.md is already loaded as project instructions (system prompt) — do NOT read it again.

**Only use Read for additional files** discovered during Steps 2-4 (source code, diagnostics) or skipped KB files.

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
- **Constraints are cost, not block** (R24): file-size/style flags never filter out targets. High-ROI target in an oversized file → plan adds split Task 0. Hard blocks: `category_failures ≥ 2`, correctness bugs, broken subsystems. Nothing else.

### Task
1. Classify ALL benchmark gaps into canonical categories.
2. Per category: affected benchmarks + total wall-time gap.
3. **Cross-check with constraints.md**: is the proposed target blocked by a known architectural ceiling?
4. Pick target by: ceiling rule → constraints check → initiative rule → ROI.
5. **Initiative exhaustion check** (added R23 review): if the active initiative has ≥2 `no_change` outcomes in its last 4 rounds, you MUST either:
   - (a) propose closing the initiative this round, writing a retrospective sub-section in the analyze report under `## Initiative Retrospective`, OR
   - (b) justify continuing with a concrete, data-backed reason (not "we think the next phase will work"). Include the justification under `## Initiative Retrospective` in the analyze report.
   Rationale: `tier2_float_loop` delivered 4 of 5 rounds with only 2 improved (R20–R23). The harness must detect and escalate exhaustion patterns instead of grinding silently.

---

## Step 1b — Architectural Reasoning (before diving into code)

After selecting a target, step back and think at the **system design** level.
Don't look at instructions yet. Ask these questions:

1. **What DESIGN DECISION causes this gap?** Not "which instruction is slow" but "which architectural
   choice makes this class of code slow." Examples:
   - "The calling convention spills all registers before every call — this limits ALL call-heavy code"
   - "NaN-boxing means every value crosses a 64-bit boundary — this limits all tight loops"
   - "Tier 2 treats self-calls and foreign-calls identically — missed specialization opportunity"

2. **Is this bottleneck shared across benchmarks?** If fib/ackermann/mutual_recursion all have the
   same root cause (call overhead), the fix should be architectural (calling convention redesign),
   not per-benchmark (fib-specific hack).

3. **What would V8/JSC do here?** Not "what pass do they have" but "how is their system designed
   differently to avoid this class of problem."

4. **Read `docs/` blog posts** for prior art in THIS project — we may have solved this before
   under a different architecture (e.g., trace JIT had accumulator pinning for fib, deleted during pivot).

5. **Check `constraints.md` as OPPORTUNITY source**, not just blocklist. A constraint like "Tier 2 BLR
   is 15-20ns vs Tier 1's 10ns" isn't just a warning — it's pointing at a design flaw worth fixing.

Write 2-3 sentences in the analyze report under `## Architectural Insight`. If the answer is
"this is a local code issue, not architectural" — say that and move on. But don't skip the question.

---

## Step 2 — External Research (knowledge layer)

Spawn exactly **ONE Research sub-agent** (Sonnet model) for all of Step 2 (web search + reference source + knowledge base).
**HARD LIMIT: 50 tool calls per sub-agent.** Do NOT spawn a second Research agent.
At 40 calls, wrap up immediately with partial findings. Round 17 Research agents used 161+145 calls (29M tokens, 58% of total). This is the #1 token waste vector.

**Before any web search**: check `opt/knowledge/` first — if a file covers the topic, read it and skip web search entirely.
**Go runtime questions**: `grep -r 'keyword' $(go env GOROOT)/src/runtime/ | head -20` before web search — goroutine stack, morestack, scheduler internals are in source.
**Web fetch cap**: max 5 fetches per prior-art query. If V8/LuaJIT source is needed, use targeted grep + read ≤3 functions. Do NOT read entire files.

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

Spawn a diagnostic sub-agent (**Sonnet model** — needs ARM64 + JIT domain understanding for instruction classification) to get **actual data** from the target benchmark:

1. **IR dump**: `Diagnose()` from `internal/methodjit/diagnose.go`
2. **ARM64 disasm** — use existing tools, **NOT python manual decoding**:
   ```bash
   go test ./internal/methodjit/ -run TestProfile_<name> -v  # compile + dump .bin
   xcrun otool -tv /tmp/gscript_<name>_t2.bin                # disassemble (one call)
   ```
   **NEVER write python ARM64 decoders** — that wastes 100+ API calls for what otool does in one.
3. **Instruction breakdown**: classify hot-block insns (compute vs overhead)

State concretely: "Hot block has N insns/iter, M overhead. Overhead: X (N%), Y (N%).
Technique eliminates X → −P% estimated (halved for ARM64 superscalar)."

pprof is useless for JIT code. `otool -tv` / `objdump` is authoritative.

**Do NOT spawn duplicate diagnostic agents.** One agent per target. If you need IR + disasm, the same agent does both.
**Architecture audit sub-agent (Step 0b) MUST NOT run ARM64 disasm** — disasm work belongs to Step 4 diagnostic sub-agent only.

### Diagnostic cross-check (mandatory, R24)

Before publishing any number, the diagnostic sub-agent MUST verify:
1. `.bin` mtime matches current HEAD
2. bytes are from the intended tier (`Diagnose()` or tier marker)
3. disasm function = target function (first 2 insns + symbol)
4. instruction class counts sum to total ±2%
5. claimed bottleneck share × wall-time ≈ predicted speedup (within 2×)
6. key numbers reproducible on re-run (±5%)

Any failure → STOP, write `opt/diagnostic_failure.md` (failed check + root cause + fix), re-measure. If unrecoverable in ≤15 calls → `status: diagnostic-blocked`, stop the round. Forbidden: "the number was off but the conclusion still holds."

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
- **Infrastructure fixes**: if Steps 0-4 discovered broken/stale tooling (e.g. Diagnose() pipeline doesn't match compileTier2(), stale docs, missing test coverage), include as a Task in this plan. Don't just note it in the report — fix it this round.
- **Expected Effect**: quantified, halved for ARM64 superscalar
- **Failure Signals**: specific conditions
- **Task Breakdown**: each task = one Coder sub-agent, with file + test
  - **1-Coder rule (R27)**: the plan MUST have exactly ONE implementation Coder task. Infrastructure/diagnostic tasks (Task 0) don't count. If the optimization requires two changes, pick the safer/smaller one and defer the other to the next round. Reason: each additional Coder multiplies token cost by 2-3×; a focused single task is more likely to land cleanly.
  - **Surgical precision required** (R24): each task spec MUST include:
    - Exact file path + function name + line numbers to modify
    - The data structure fields/types being added or changed
    - The algorithm in pseudocode (not prose) — 5-10 lines is enough
    - Which existing test to extend, or exact test function to write
    - What NOT to touch (explicit scope boundary)
    - Reason: vague specs cause Coder to explore → repeated read-modify-test loops → 3-5× token cost
  - **Conceptual complexity cap** (added R23 review): any task meeting ANY of these must be split into two sub-tasks (a diagnostic task that writes findings to `opt/`, then an implementation task that consumes them):
    - requires reading >2 pass files
    - couples regalloc.go with emit_*.go
    - implements cross-block dataflow (shape/type/value propagation across basic blocks)
    - Coder is expected to exceed 80 tool calls
  - File/line caps (R22 review): ≤3 files, ≤200 lines changed per task. Still applies.
- **Budget**: max commits, max files, abort condition

**MANDATORY pre-plan checklist** (round 18 failed this — user intervened twice):
- [ ] If Diagnose() or arch_check found broken tooling / pipeline mismatches → is there a fix Task?
- [ ] If constraints.md flags files >800 lines that this plan touches → is there a split Task 0?
- [ ] If known-issues.md has items in this plan's category → are quick-fix items included?
Do NOT finalize the plan until all boxes are checked.

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

Create `docs/draft.md`. Read a few recent posts in `docs/` first to match the voice and style.

Write like a person — a programmer writing about their day. No fixed template. No `## What we found` / `## The plan` headers unless they feel natural. Some posts might be a narrative, some might lead with a surprise, some might start with code. Vary the structure.

**Must include** (weave in naturally, don't use as section headers):
- What the diagnostic data showed (actual numbers, code, IR)
- Why this is the target and what you're going to try
- Frontmatter: `layout: default`, `title:`, `permalink: /NN-slug`

End with `*[Implementation next...]*` so IMPLEMENT knows to append.

Use the next available post number (`ls docs/*.md | tail -1`).

## Restrictions
- Do NOT write implementation code
- Only write to `opt/` + `docs-internal/architecture/` + `docs/draft.md`
- If no non-blocked target exists: output `status: all-categories-blocked` and STOP
