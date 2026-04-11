# Harness Core Principles — gs-opt-loop

**Status**: load-bearing. Every phase session MUST read this file. Violation of any principle = sanity FAIL.
**Version**: v1 (2026-04-11, R33 post-mortem)
**Authority**: these are derived from cited sources in `opt/harness-v3-synthesis.md`, not free-form invention. Changes to this file require a documented reason in the commit message and an update to the corresponding source section in `harness-v3-synthesis.md`.

**How to read**: 6 rules. Each rule has a name, a one-line statement, a why, an enforcement mechanism, a violation signal, a specific failure it prevents. The rules are ordered by dependency — rule 1 is the most fundamental, rule 6 is a meta-rule that protects the other 5.

---

## P1. Grounding — every architectural claim cites a source

**Rule**: any new phase, rule, mechanism, or taxonomy MUST cite a specific source: published paper, Anthropic official documentation, vendor engineering post, or clearly-labelled internal empirical observation. "Seems reasonable" is NOT a source.

**Why**: R33 produced a 6-mechanism "v2 design" where 7/8 of the enumerated paradigms were made up on the spot. Confident prose without grounding is the same failure mode as ANALYZE writing "Root cause confirmed" without evidence.

**Enforcement**: REVIEW, ANALYZE, PLAN_CHECK output every rule/proposal with `source: <URL or internal observation ID>`. Missing source → harness-violation.

**Violation signal**: any proposal with `source: (none)` or handwavy "best practices say..."

**Failure prevented**: the R33 post-mortem where I listed 8 "paradigms" (tree search, evolutionary, KB-first, etc.) with no literature grounding, then used the padded list to claim "I've searched broadly."

---

## P2. Evidence before action — verified, not assumed

**Rule**: every "root cause" or "premise" in a plan MUST cite verified evidence: a file:line the reader can open, a diagnostic output that can be reproduced, a test that ran and passed. Assumptions derived from "this should be true" are FORBIDDEN.

**Why**: R30 ("handleNativeCallExit only fires on cold miss" — wrong under Tier2→Tier1 crosscut), R31 (used stale `profileTier2Func` as evidence), R32 (assumed `GetField` returns `TypeFloat` — production emits `any + GuardType float`). Three consecutive rounds, same class, all shipped plans with unverified premises.

**Enforcement**: plan schema requires an `assumptions` section. Each entry has `claim` + `evidence` (cite a file:line or a reproducible command). PLAN_CHECK (the evaluator-optimizer loop) independently verifies each assumption. Missing evidence → NEEDS_IMPROVEMENT; disproved assumption → FAIL.

**Violation signal**: plan's assumptions section is empty, or any entry lacks `evidence:`.

**Failure prevented**: R30/R31/R32 silent-no-op class (Anthropic evaluator-optimizer pattern, cookbook ref: `evaluator_optimizer.ipynb`).

---

## P3. Authoritative context first, then extended thinking

**Rule**: before ANALYZE reasons about a target, a prior phase (CONTEXT_GATHER) MUST retrieve authoritative context from the production pipeline — real `compileTier2()` output, real benchmark disassembly, real bench breakdown. ANALYZE consumes this as its primary input. Synthetic fixtures (hand-constructed IR, `profileTier2Func`, `TestProfile_*`) are FORBIDDEN as evidence sources.

**Why**: Direct Anthropic Opus 4.6 best practice: *"retrieve authoritative context first, then enable extended thinking when the answer must be correct and defensible"* ([Anthropic API docs](https://platform.claude.com/docs/en/build-with-claude/extended-thinking)). We violated this — ANALYZE has always enabled thinking but never mechanically retrieved authoritative context before thinking.

**Enforcement**: optimize.sh PHASES array includes `context_gather` between `analyze` step 0 and step 1. CONTEXT_GATHER produces `opt/authoritative-context.json`. ANALYZE reads this file as input. PLAN_CHECK verifies that every plan claim can be traced back to a field in authoritative-context.json.

**Violation signal**: ANALYZE plan references a tool output not in `opt/authoritative-context.json` or not reproducible by CONTEXT_GATHER's command list.

**Failure prevented**: R31 stale-tool reuse; R32 synthetic-IR type gate.

---

## P4. Honest confidence labels — no default-confident prose

**Rule**: every mechanism, claim, prediction, and recommendation MUST be explicitly labelled `confidence: HIGH | MEDIUM | LOW` with a one-line justification. HIGH = directly cited pattern with reference code (e.g. Anthropic cookbook). MEDIUM = cited but adapted to new domain. LOW = internal observation or speculative extension.

**Why**: Opus 4.6 defaults to confident prose even when reasoning is speculative. R33 "v2 design" was written in HIGH-confidence tone for mechanisms that were actually LOW. I did the exact thing I criticized ANALYZE of doing. Recursive failure mode.

**Enforcement**: plan schema requires `confidence:` on every predicted delta and every proposed mechanism. PLAN_CHECK and REVIEW both audit that HIGH-confidence claims have a matching source citation (P1). Unlabelled = default to LOW.

**Violation signal**: any prediction or proposal without a `confidence:` field.

**Failure prevented**: R33 overconfident v2 proposal. Any future "this will definitely work" plan where the writer actually has no evidence.

---

## P5. Frozen reference baseline — does not rotate

**Rule**: `benchmarks/data/reference.json` is IMMUTABLE once frozen. VERIFY's `set_baseline.sh` updates `latest.json` and `baseline.json` per round, but NEVER `reference.json`. Sanity R7 computes cumulative drift as `(latest - reference) / reference` for each non-excluded benchmark.

**Why**: R25's measurement repair (median-of-5) fixed single-shot noise but introduced rolling-baseline drift. R28-R32 accumulated 3-7% regressions on nbody/sieve/matmul/spectral/mandelbrot, each round "within noise" individually, cumulative invisible. 5 rounds of −0.5% compounds to −3% that no sanity pass detected.

**Enforcement**: reference.json has a SHA-256 integrity hash recorded in `state.json.reference_baseline.hash`. Any change triggers sanity R7 FAIL. Re-freezing requires explicit user action via a new command `bash .claude/freeze-reference.sh <commit>` that logs the reason.

**Violation signal**: reference.json hash mismatch, or any commit to reference.json without a corresponding freeze-command invocation.

**Failure prevented**: R28-R32 cumulative drift invisibility. Any future rolling-baseline regression.

---

## P6. No invented taxonomies — meta-rule protecting the other 5

**Rule**: when enumerating alternatives (paradigms, strategies, options, techniques), every item on the list MUST have a specific source (P1). If fewer than 3 sources exist for a given dimension, the list MUST be presented as incomplete: "I found N options in the literature, here they are; the space is larger than what I've enumerated." NEVER pad a list with pattern-matched names from adjacent fields to make it look comprehensive.

**Why**: the R33 failure that triggered this entire v3 exercise was me listing 8 "paradigms" where only 1 was grounded. The user caught it. This rule is the fence against that specific failure class.

**Enforcement**: any enumeration in a plan, analysis, or review with >3 items MUST have >=3 sources. Otherwise prefix with "**Incomplete enumeration**: I found N sources. Space is larger than this list."

**Violation signal**: any plan section that lists >3 options without corresponding >=3 sources.

**Failure prevented**: padding-induced false confidence about search breadth. The R33 recursive local optimum.

---

# Permanent anti-patterns (NEVER do these)

These are prior-round failures codified as absolute prohibitions. Adding to this list requires a round where the pattern repeated.

1. **Use `profileTier2Func` or `TestProfile_*` as evidence** — R19, R31 wasted. Use `Diagnose()` on real `compileTier2()` or production TieringManager.
2. **Write plan predictions without a `confidence:` label** — R28-R32 all had confident prose, all missed by 5-10×.
3. **Re-point reference.json during VERIFY** — automatic rolling hides drift.
4. **Enumerate alternatives without citations** — the R33 failure.
5. **Mark a round `improved` based on rolling baseline comparison** — only `reference.json` comparisons count as "improved."
6. **Sub-agents running ARM64 disasm in architecture-audit mode** — disasm is Step 4 diagnostic sub-agent only (added analyze.md rule R29).
7. **Coder sub-agents running curated subset tests instead of full-package** — R30 regressed through this exact gap.
8. **REVIEW proposing 5 local patches when the pattern is structural** — if the same class repeats twice, REVIEW must enter stall_mode (P4 meta-rule from v3).

---

# Preservation mechanism

This file is load-bearing. It must be:

1. **Committed to git** in `opt/harness-core-principles.md` (this file).
2. **Referenced from `CLAUDE.md`** under a new `## Core Principles` section so every phase session loads it via project instructions.
3. **Referenced from every `.claude/prompts/*.md`** prompt, at the top, with the text: `**Core principles**: read opt/harness-core-principles.md BEFORE any reasoning. Violations are sanity FAILs.`
4. **Saved to user auto-memory** (`~/.claude/projects/.../memory/`) so it persists across Claude sessions and conversations on this project.
5. **Checked for drift**: REVIEW phase every round validates the file exists, the CLAUDE.md reference exists, the phase prompt references exist. Drift = harness-violation.

**Revision policy**: any change to this file requires:
- a commit message starting with `principles:` and explaining the rule's source update
- a matching section in `opt/harness-v3-synthesis.md` citing the new/revised source
- REVIEW in the next round audits the change and either accepts or flags

---

**This file is the most important thing in opt/. If you are a Claude session reading this for the first time, stop, read it fully, and honor it. Your entire judgment about what to do next depends on it.**
