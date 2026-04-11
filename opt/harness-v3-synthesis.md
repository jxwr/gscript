# gs-opt-loop Harness v3 — Research-Grounded Synthesis + Design

**Date**: 2026-04-11
**Goal**: continuously improve performance across all 22 benchmarks until surpassing LuaJIT, cost no object
**Model**: Claude Opus 4.6 with adaptive thinking
**Constraint**: every architectural choice must cite a source; speculative mechanisms must be labelled as such

---

# Part A: Research Summary (with citations)

## A1. Anthropic's canonical agent patterns (authoritative for Claude)

Anthropic, "[Building Effective Agents](https://www.anthropic.com/research/building-effective-agents)", lists 5 canonical workflows:

1. **Prompt Chaining** — sequential steps, each processes prior output. Current gs-opt-loop = this.
2. **Routing** — classify inputs, dispatch to specialized handlers. Partial in our category system.
3. **Parallelization** — sectioning (subtasks) + voting (same task multiple times). **Missing in gs-opt-loop.**
4. **Orchestrator-Workers** — central LLM dynamically decomposes and delegates. Partial (IMPLEMENT spawns Coder).
5. **Evaluator-Optimizer** — generator proposes, evaluator scores, iterates until PASS. **Missing in gs-opt-loop.** ← critical

Three core principles they emphasize: **simplicity**, **transparency**, **thorough agent-computer interfaces**. Key warning: "potential for compounding errors" and "framework overuse obscures underlying prompts, making debugging hard."

Direct Anthropic cookbook reference implementation: [`evaluator_optimizer.ipynb`](https://github.com/anthropics/anthropic-cookbook/blob/main/patterns/agents/evaluator_optimizer.ipynb). Core loop:

```python
while True:
    evaluation, feedback = evaluate(evaluator_prompt, result, task)
    if evaluation == "PASS":
        return result
    context = accumulated_attempts + feedback
    result = generate(generator_prompt, task, context)
```

The evaluator's prompt uses XML extraction (`<evaluation>PASS|NEEDS_IMPROVEMENT|FAIL</evaluation>`, `<feedback>...</feedback>`) for reliable condition parsing.

## A2. ComPilot — the most directly applicable 2025 research

[ComPilot](https://arxiv.org/html/2511.00592v1) (Agentic Auto-Scheduling, 2025) is LLM-guided loop optimization via closed-loop interaction with the Tiramisu compiler. This is the closest published work to gs-opt-loop's actual situation.

**Architecture**:
- **Phase 1 — Context initialization**: system prompt, extract loop nest, **chain-of-thought analysis of the code BEFORE any transformation proposal**. Ablation (RQ10) confirms removing this phase degrades performance.
- **Phase 2 — Iterative optimization**: LLM proposes transformations, parser validates syntax, compiler performs legality check (polyhedral dependence analysis), execution measures speedup, categorized feedback returned:
  - `invalid` (malformed)
  - `illegal` (violates dependencies)
  - `solver_failure`
  - `compiler_crash`
  - `success + measured speedup`

**Search strategy**: multi-run best-of-K, typically **K=5**. Each run is independent. Final answer = best across K. LLM receives explicit prompt to issue `no_further_transformations` when exhausted, but system overrides to encourage exploration.

**Hard numbers** (directly relevant to our calibration):
- Single-run geometric mean speedup: **2.66×** (95% CI 2.60–2.77)
- Best-of-5 geometric mean: **3.54×** (+33% over single-run)
- Success rate per attempt: **36.1%** — failure is the norm
- Invalid: 31.4%, illegal: 32.5%, success: 36.1%
- Illegality is ~60% at iteration 1 but drops with dialogue progression
- Removing feedback loop: 2.66× → 2.01× (**−23%**, proving feedback is critical)
- Direct (non-interactive) code generation: **5.3× more tokens** than iterative
- 30 iterations ≈ 8.9 minutes per benchmark; compiler backend is 78.5% of wall time

**Key takeaways for us**:
- Iterative feedback is cheaper AND more effective than one-shot generation
- Best-of-K doubles the floor; K=5 is a well-established sweet spot
- CoT analysis BEFORE plan proposals is load-bearing, not optional
- 36% success per attempt = we should expect ~2/3 of plans to be wrong, and build for it

## A3. Claude Opus 4.6 specific best practices

From [Anthropic API docs — Adaptive Thinking](https://platform.claude.com/docs/en/build-with-claude/adaptive-thinking) and [Extended Thinking](https://platform.claude.com/docs/en/build-with-claude/extended-thinking):

1. **Adaptive thinking is the recommended mode for agentic workflows** with multi-step tool use. Enables interleaved thinking between tool calls.
2. **"Retrieve authoritative context first, then enable extended thinking when the answer must be correct and defensible."** ← direct applicability: gs-opt-loop currently enables thinking but doesn't retrieve authoritative context first.
3. **Long-horizon state tracking** is Opus 4.6's strength. It can save state and continue with a fresh context window. Our `state.json` + `phase_log.jsonl` + `opt/knowledge/` already leverage this.
4. **Parallel subtasks** handled well — adaptive thinking maintains clear awareness of dependencies.
5. **1M context with cache** — we can hold vastly more state than we currently pass.

## A4. OpenTuner — ensemble search (inspiration, not direct fit)

[OpenTuner](https://commit.csail.mit.edu/papers/2014/ansel-pact14-opentuner.pdf) (Ansel et al., PACT 2014) runs an **ensemble of disparate search techniques** in parallel (random search, simulated annealing, differential evolution, pattern search, genetic algorithms). Dynamic budget allocation: techniques that find better points get more trials. Applied to Halide, TACO, TVM, RISE.

**Not directly applicable** — we have one LLM-backed optimizer, not N gradient-free search algorithms. **But the principle applies**: multiple strategies competing with dynamic credit assignment. We could run different ANALYZE prompt variants and score them.

## A5. AlphaDev — RL from scratch

[AlphaDev](https://deepmind.google/blog/alphadev-discovers-faster-sorting-algorithms/) (Nature 2023) uses AlphaZero-based RL to play a "single-player assembly game." Transformer encoder for instructions + CPU state encoder. Discovered faster sort algorithms now in LLVM standard library.

**Not applicable** — requires training from scratch, millions of trials, huge compute. We have 30 rounds of history and Opus 4.6 inference, not RL training.

**Concept that IS applicable**: precise state encoding. AlphaDev represents CPU state (registers, memory) explicitly. Our "state" passed to ANALYZE is mostly prose (state.json, INDEX.md). A more structured state representation (a graph) would enable better reasoning.

## A6. Temporal knowledge graph memory (AriGraph, Zep)

[AriGraph](https://arxiv.org/abs/2407.04363) (2024) and [Zep](https://blog.getzep.com/content/files/2025/01/ZEP__USING_KNOWLEDGE_GRAPHS_TO_POWER_LLM_AGENT_MEMORY_2025011700.pdf) (2025) both argue for temporal knowledge graph memory over vector DBs or flat files. Structure:

- **Episodic tier**: raw events (per-round plan, outcome, commit, delta)
- **Semantic tier**: extracted entities/relationships (this pass works, that diagnostic is stale, this benchmark is bottlenecked by X)
- **Temporal edges**: when was this fact true, when did it become stale

Our current `opt/knowledge/` is a flat file directory. R31 reused R19's stale `profileTier2Func` because there was no mechanism to mark it "stale as of R19." A temporal KG with a "deprecated by R<N>" edge would have caught it.

## A7. OpenAI's million-line Codex project

From [36kr article](https://eu.36kr.com/en/p/3715546375188870) and [OpenAI Codex docs](https://developers.openai.com/codex):

OpenAI built an internal product with ~1M LOC, zero manually written. Key architectural insight: **AI-friendly architecture is MORE rigid than human-friendly**. They enforced strict hierarchy (Types → Config → Repo → Service → Runtime → UI). Humans formulated tasks in natural language, Codex generated code, humans reviewed outputs and corrected queries. Tagline: **"Humans steer, agents execute."**

Direct applicability: our plans are currently free-form markdown. Converting to strict schema (e.g. YAML-front-matter with required fields) would constrain Opus and make plans machine-checkable.

## A8. Anthropic's own Claude Code architecture

From [ZenML LLMOps case study](https://www.zenml.io/llmops-database/claude-code-agent-architecture-single-threaded-master-loop-for-autonomous-coding):

Claude Code uses "a simple, single-threaded master loop combined with disciplined tools and planning delivers controllable autonomy." Real-time steering via async dual-buffer queue. **Design philosophy**: simplicity beats complexity for autonomous coding.

**Note**: Claude Code is interactive (human steers during the loop). gs-opt-loop is autonomous (steers itself). Claude Code's simplicity works because humans provide the steering signal. We need to internalize that signal.

## A9. Production-scale constraints (from all sources)

- Token budget: agents use 4× standard chat, multi-agent systems up to 15× ([Oracle AI agent loop](https://blogs.oracle.com/developers/what-is-the-ai-agent-loop-the-core-architecture-behind-autonomous-ai-systems))
- Observability is required — trace every reasoning step, tool call, decision
- Cost compounds quickly with best-of-N and evaluator-optimizer loops

---

# Part B: Current-State Diagnosis (grounded in research above)

## B1. Which canonical patterns are we using

| Pattern | Source | Current state | Evidence |
|---|---|---|---|
| Prompt chaining | Anthropic | ✅ core structure | optimize.sh PHASES array |
| Routing | Anthropic | ⚠️ partial | category_failures rules in analyze.md |
| Parallelization | Anthropic | ❌ missing | single plan per round, no voting |
| Orchestrator-workers | Anthropic | ⚠️ partial | IMPLEMENT spawns Coder mechanically, not dynamically |
| **Evaluator-optimizer** | **Anthropic** | **❌ missing** | **plan is written once, never re-generated on feedback** |
| CoT analysis before action | ComPilot (RQ10) | ⚠️ partial | ANALYZE has rules but not enforced pre-proposal CoT |
| Best-of-K sampling | ComPilot | ❌ missing | one plan per round |
| Closed-loop feedback | ComPilot | ⚠️ partial | no_change is opaque; next round starts blind |
| Authoritative context first | Opus 4.6 best practice | ❌ missing | ANALYZE reads stale `profileTier2Func`, synthetic IR |
| Adaptive thinking mode | Opus 4.6 docs | unknown | not explicitly enabled in claude -p calls |
| Temporal KG memory | AriGraph / Zep | ❌ missing | flat files, no temporal edges |
| Strict plan schema | OpenAI million-line | ❌ missing | plans are free-form markdown |
| Meta-loop / outer reset | Meta-Harness, Magentic-One | ⚠️ partial | REVIEW fires every round but only patches locally |
| Frozen reference baseline | Oracle, Meta-Harness | ❌ missing | rolling baseline hides cumulative drift |

**Summary: 4 missing, 5 partial, 2 OK, 3 unknown.** We are using the simplest possible subset (prompt chaining) of a much richer pattern space.

## B2. Why the losing streak maps to missing patterns

- **R30 regressed → reverted**: ANALYZE wrote a plan with a wrong premise about Tier2→Tier1 crosscut. No evaluator-optimizer loop to catch it before IMPLEMENT. No authoritative context retrieval to verify the premise. → **missing P5 (evaluator-optimizer) + missing P9 (authoritative context)**
- **R31 no_change → silent no-op**: ANALYZE used stale `profileTier2Func` as evidence. No temporal KG to mark it deprecated. R19 already wasted a round on the same tool. → **missing P11 (temporal KG)**
- **R32 no_change → silent no-op**: ANALYZE assumed `GetField` returns `TypeFloat`, unit tests used hand-constructed IR, production IR is `any + GuardType float`. No authoritative-context-first phase. → **missing P9**
- **R28-R32 cumulative drift**: 5 rounds of −0.5% not caught. Rolling baseline. → **missing P13 (frozen reference)**
- **REVIEW never proposed structural change**: 5 patches in 5 rounds, each local. → **missing meta-loop (P10 in Meta-Harness sense)**

**Every failure maps to a missing canonical pattern. The fix is to implement the missing patterns, not invent new mechanisms.**

---

# Part C: v3 Design

## C1. Design principles (derived from research)

1. **Match Anthropic canonical patterns first** before inventing new ones. Start with evaluator-optimizer + authoritative context + best-of-K — these are authoritative and have reference implementations.
2. **Calibrate on ComPilot's real numbers**: expect 36% plan success, expect best-of-5 to deliver ~33% gain over best-of-1, expect feedback ablation to cost ~23%. Our plans should not predict >50% success without evidence.
3. **Simplicity is a feature (Anthropic)**. Only add a mechanism when a specific failure mode in B2 maps to it. Do not speculatively add.
4. **Grounded in specific failures**, not abstract principles. Each mechanism cites an R-round it would have prevented.
5. **Stage by confidence**. High-confidence mechanisms (directly cited patterns with reference code) land first. Speculative mechanisms (our own extensions) land later or not at all.
6. **Humans steer, agents execute** (OpenAI million-line). The harness enforces structural rigidity; Opus fills in content within the rigid frame.

## C2. Proposed v3 architecture

### Phase graph

```
   REVIEW (existing, strengthened)
        ↓
   CONTEXT_GATHER  (NEW) ────── retrieves production IR, disasm, bench breakdown
        ↓                       for each candidate target. Runs real compileTier2()
        ↓                       not profileTier2Func. Outputs authoritative-context.json
        ↓
   ANALYZE (existing, refactored)
        ↓                       best-of-K plan generation: K=3 sibling ANALYZE sessions
        ↓                       with slightly different seeds/emphases
        ↓
   PLAN_CHECK (NEW, evaluator-optimizer loop)
        ↓                       for each of K plans:
        ↓                         verify each assumption cited in authoritative-context.json
        ↓                         evaluate: PASS / NEEDS_IMPROVEMENT / FAIL
        ↓                       if all FAIL → loop back to ANALYZE with feedback (max 3 rewrites)
        ↓                       if any PASS → pick best-scoring PASS
        ↓                       if no PASS after 3 rewrites → halt round, escalate
        ↓
   IMPLEMENT (existing, strengthened)
        ↓                       mandatory full-package test (already added R30)
        ↓                       mandatory production-IR delta test (new)
        ↓
   VERIFY (existing, strengthened)
        ↓                       writes closed-loop feedback to KG:
        ↓                         (plan_id, expected_delta, actual_delta, outcome_class)
        ↓                       appends to prediction_ledger.jsonl
        ↓
   SANITY (existing, strengthened)
        ↓                       + R7 cumulative drift check vs frozen reference
        ↓                       + R8 silent-no-op detection (pass touched, IR unchanged)
        ↓                       + R9 plan-check verdict audit (was plan_check too lenient?)
        ↓
   [auto-continue or halt]
```

Every 5 rounds: **META_REFLECT** phase runs after SANITY. Reads last 5 rounds from KG, writes a longitudinal report, proposes ONE structural harness change (not per-round patch). Halts if the proposal requires user input.

### Data layer

**New:**
- `opt/kg/` — temporal knowledge graph, hierarchical:
  - `opt/kg/episodes/` — one file per round (`R33.jsonl`): plan, outcome, delta, lessons
  - `opt/kg/semantic/` — extracted facts with validity windows (`deprecated: {tool: profileTier2Func, since: R31}`)
  - `opt/kg/entities/` — per-benchmark profile summary (last update, hot loop IR, bottleneck class, improvement history)
- `opt/prediction_ledger.jsonl` — every plan's prediction vs measurement
- `benchmarks/data/reference.json` — frozen, does not rotate with VERIFY

**Strengthened:**
- `opt/current_plan.md` → strict YAML frontmatter + markdown body. Machine-checkable fields.
- `state.json` → add `reference_baseline`, `stall_count`, `escalated`, `last_meta_reflect_round`

### Phase budgets

| Phase | Model | Max tool calls | Thinking mode |
|---|---|---|---|
| REVIEW | Opus 4.6 | 40 | adaptive |
| CONTEXT_GATHER | Opus 4.6 | 30 (real-IR-only; no profileTier2Func) | adaptive |
| ANALYZE × K=3 | Opus 4.6 | 50 each, 150 total | adaptive, interleaved |
| PLAN_CHECK × ≤3 iterations | Opus 4.6 | 15 each, ≤45 total | adaptive |
| IMPLEMENT | Opus 4.6 (+Coder sub-agent) | 60 + 30 | adaptive |
| VERIFY | Opus 4.6 | 30 | adaptive |
| SANITY | Opus 4.6 | 20 | adaptive |
| META_REFLECT (every 5) | Opus 4.6 | 40 | adaptive |

**Predicted cost per round**: ~40-60M tokens (vs current ~20-30M). Yes, this is more. User authorized "cost no object" for finding the right direction. We measure whether this investment produces improved outcomes vs v1.

---

# Part D: Staged Implementation Plan

**Principle**: don't ship all 14 mechanisms at once. Stage by confidence. Measure after each stage.

## Stage 1 — HIGH confidence (directly cited patterns, reference code exists)

**Goal**: close the three missing canonical Anthropic patterns that directly map to R30/R31/R32 failures.

| # | Commit | Closes | Reference |
|---|---|---|---|
| S1.1 | `harness v3: freeze reference baseline + sanity R7 cumulative drift` | R28-R32 cumulative drift | Oracle AI agent loop, Meta-Harness |
| S1.2 | `harness v3: add CONTEXT_GATHER phase — authoritative-context-first` | R30/R31/R32 ANALYZE assumptions | Opus 4.6 docs: "retrieve authoritative context first" |
| S1.3 | `harness v3: PLAN_CHECK evaluator-optimizer loop with K=1 initial` | R30/R31/R32 plan slip-through | [Anthropic evaluator_optimizer.ipynb](https://github.com/anthropics/anthropic-cookbook/blob/main/patterns/agents/evaluator_optimizer.ipynb) |
| S1.4 | `harness v3: plan schema — strict YAML frontmatter with Assumptions section` | plan quality + PLAN_CHECK input | OpenAI million-line project |

**Estimated effort**: 4-6 hours. **Estimated new failure surface**: low (each change is a localized prompt/script edit with explicit rollback).

**Stage 1 validation**: before Stage 2, run ONE full round with v3.1 harness. Compare vs last v1 round: plan_check caught anything? context_gather produced different IR? Write a post-mortem.

## Stage 2 — MEDIUM confidence (cited adaptations)

**Prerequisite**: Stage 1 validation shows at least ONE of: (a) plan_check rejected an assumption that would have slipped through v1, (b) context_gather revealed data ANALYZE would have missed, (c) reference baseline flagged cumulative drift.

| # | Commit | Closes | Reference |
|---|---|---|---|
| S2.1 | `harness v3: best-of-K=3 ANALYZE sampling` | single-plan local optima | ComPilot best-of-5 results |
| S2.2 | `harness v3: closed-loop benchmark feedback → prediction_ledger` | opaque no_change | ComPilot feedback ablation |
| S2.3 | `harness v3: pessimistic mode on calibration drift > 3×` | Opus optimism bias | our R28-R32 data (5-10× miscalibration) |
| S2.4 | `harness v3: stall_count + halt on 3 consecutive no_change` | 5-round stalls without halt | Magentic-One dual-loop |

**Estimated effort**: 6-8 hours. **Stage 2 validation**: 3 rounds with v3.2 harness. Compare outcome distribution vs v1.

## Stage 3 — LOW confidence (our extensions, speculative)

**Prerequisite**: Stage 2 validation shows measurable improvement in plan quality or convergence.

| # | Commit | Closes | Notes |
|---|---|---|---|
| S3.1 | `harness v3: temporal KG lite (opt/kg/episodes + semantic)` | R19/R31 stale tool reuse | AriGraph inspired; our own schema |
| S3.2 | `harness v3: META_REFLECT every 5 rounds + structural proposal template` | no cross-round pattern detection | Meta-Harness; adapted |
| S3.3 | `harness v3: capability-ceiling escalation when 2 meta proposals converge` | runaway inner loop | our own design |

**Stage 3 may be deferred indefinitely** if Stages 1-2 resolve the crisis. Only build it if Stage 2 validation shows convergence is still blocked.

---

# Part E: Explicit Confidence Ledger

| Mechanism | Confidence | Grounding |
|---|---|---|
| S1.1 reference baseline | **HIGH** | Oracle, Meta-Harness all recommend; pure data mechanism |
| S1.2 CONTEXT_GATHER | **HIGH** | Direct Opus 4.6 best practice, one-line quote from Anthropic docs |
| S1.3 PLAN_CHECK loop | **HIGH** | Anthropic canonical pattern with reference implementation |
| S1.4 plan schema | **HIGH** | OpenAI million-line precedent, mechanical enforcement |
| S2.1 best-of-K | **MEDIUM** | ComPilot shows +33% in a different domain. May not transfer. |
| S2.2 feedback ledger | **MEDIUM** | ComPilot ablation shows 23% degradation without feedback. Applies to our situation likely but unverified. |
| S2.3 pessimistic mode | **MEDIUM** | Our own R28-R32 data supports it, no external study. |
| S2.4 stall_count | **MEDIUM** | Magentic-One dual-loop cited; our threshold chosen heuristically. |
| S3.1 temporal KG | **LOW** | AriGraph architecture, our schema is improvised. |
| S3.2 META_REFLECT | **LOW** | Meta-Harness concept, our template is improvised. |
| S3.3 capability escalation | **LOW** | Our own invention with no direct precedent. |

---

# Part F: What v3 is NOT (explicit non-goals)

- **Not a rewrite**. 4 existing phases stay, 2-3 new phases are added, data structures extend but don't replace.
- **Not tree search / beam search / evolutionary**. I considered these, found no authoritative precedent for applying them to this specific problem, and Anthropic recommends starting simpler.
- **Not RL-trained**. AlphaDev-style RL requires training budget we don't have.
- **Not multi-agent debate**. Evaluator-optimizer is the Anthropic canonical pattern; we don't need multiple generators debating.
- **Not temporal KG with full vector retrieval**. Stage 3 is "KG lite" — hand-curated facts with temporal edges, not a full vector store.

---

# Part G: Open questions the user must decide

These are choices where the research doesn't give a deterministic answer. User input required.

### G1. Reference baseline anchor

Which commit freezes `benchmarks/data/reference.json`?

- (a) `a388f78` (R25 baseline, pre-598bc1e fib regression) + explicitly exclude fib/fib_recursive/mutual_recursion/ackermann — my recommendation; shows full cumulative drift
- (b) current HEAD — accepts current state as baseline, only detects future drift
- (c) `b46c4cd` (R27 close, post-ctx.Constants improvement, pre-598bc1e) — "last true good state"

**I recommend (a).**

### G2. K in best-of-K for Stage 2

ComPilot uses K=5. Our token budget is higher-cost per call. Reasonable range: K=2 (minimal) to K=5 (ComPilot). My recommendation: **K=3** as a first try; measure cost/benefit; adjust.

### G3. PLAN_CHECK loop termination

Max rewrite cycles when all K plans fail verification. ComPilot uses 30 iterations per problem but with different semantics. My recommendation: **3 rewrite cycles then abandon round**. Too few = harsh. Too many = runaway cost.

### G4. META_REFLECT cadence

Every N rounds. N=5 is my guess. Could be every 3 (tight), every 10 (relaxed). No literature specifies.

### G5. Should Stage 3 be attempted at all?

Stage 3 is my most speculative work. If Stages 1-2 resolve the crisis, Stage 3 adds complexity for unclear benefit. Alternative: **never build Stage 3**, leave those failure modes for a future revision informed by Stages 1-2 data.

### G6. Hard halt threshold

If after Stage 2 we have 5 more no_change rounds, should the harness hard-halt and demand human direction? Or continue trying? My recommendation: **yes, hard halt after 5 no_change rounds regardless of stage**.

---

# Part H: What happens next

1. User reviews this design (task #10)
2. User answers G1-G6 (or accepts recommendations)
3. I implement Stage 1 (4 commits, ~4-6 hours)
4. Run ONE round with v3.1
5. Write Stage 1 validation post-mortem
6. User decides whether Stage 2 is justified
7. Stage 2 if approved (~6-8 hours)
8. Validation round for Stage 2
9. Stage 3 if the earlier stages don't resolve convergence

**I will NOT start Stage 1 implementation until the user approves this design.**

---

## Sources (full citations)

- [Anthropic — Building Effective Agents](https://www.anthropic.com/research/building-effective-agents)
- [Anthropic Cookbook — Evaluator-Optimizer pattern](https://github.com/anthropics/anthropic-cookbook/blob/main/patterns/agents/evaluator_optimizer.ipynb)
- [Anthropic API Docs — Adaptive Thinking](https://platform.claude.com/docs/en/build-with-claude/adaptive-thinking)
- [Anthropic API Docs — Extended Thinking](https://platform.claude.com/docs/en/build-with-claude/extended-thinking)
- [Claude Opus 4.6 announcement](https://www.anthropic.com/news/claude-opus-4-6)
- [Claude Code master loop architecture (ZenML case study)](https://www.zenml.io/llmops-database/claude-code-agent-architecture-single-threaded-master-loop-for-autonomous-coding)
- [ComPilot — Agentic Auto-Scheduling](https://arxiv.org/html/2511.00592v1)
- [POLO — Project-Level Code Performance Optimization](https://www.ijcai.org/proceedings/2025/0814.pdf)
- [SuperCoder — Assembly Program Superoptimization](https://arxiv.org/html/2505.11480)
- [OpenTuner (PACT 2014)](https://commit.csail.mit.edu/papers/2014/ansel-pact14-opentuner.pdf)
- [BaCO — Bayesian Compiler Optimization](https://weiya711.github.io/publications/asplos2024-baco.pdf)
- [AlphaDev — DeepMind (Nature 2023)](https://deepmind.google/blog/alphadev-discovers-faster-sorting-algorithms/)
- [AriGraph — Knowledge Graph World Models (2024)](https://arxiv.org/abs/2407.04363)
- [Zep — Temporal Knowledge Graph for Agent Memory (2025)](https://blog.getzep.com/content/files/2025/01/ZEP__USING_KNOWLEDGE_GRAPHS_TO_POWER_LLM_AGENT_MEMORY_2025011700.pdf)
- [Meta-Harness — Superagentic AI (2026)](https://medium.com/superagentic-ai/meta-harness-a-self-optimizing-harness-around-coding-agents-928733644551)
- [Martin Fowler (Böckeler) — Harness Engineering](https://martinfowler.com/articles/harness-engineering.html)
- [Schmid — Agent Harness in 2026](https://www.philschmid.de/agent-harness-2026)
- [awesome-harness-engineering](https://github.com/ai-boost/awesome-harness-engineering)
- [Reflexion (arXiv 2023)](https://arxiv.org/abs/2303.11366)
- [Voyager (OpenReview 2023)](https://openreview.net/forum?id=ehfRiF0R3a)
- [OpenAI million-line Codex project (36kr)](https://eu.36kr.com/en/p/3715546375188870)
- [Oracle — AI Agent Loop Architecture](https://blogs.oracle.com/developers/what-is-the-ai-agent-loop-the-core-architecture-behind-autonomous-ai-systems)
