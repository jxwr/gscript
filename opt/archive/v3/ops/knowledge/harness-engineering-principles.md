# Harness Engineering Principles for Self-Improving AI Optimization Loops

**Compiled**: 2026-04-11 R33 post-mortem research
**Purpose**: Principles for gs-opt-loop v2 design — the layered scaffold that wraps the Opus ANALYZE/IMPLEMENT/VERIFY agents to ensure the loop converges on "beat LuaJIT" instead of drifting/stalling.

**Source synthesis**: Martin Fowler (Böckeler), Meta-Harness (Stanford/Superagentic), Schmid 2026, awesome-harness-engineering, Magentic-One, Reflexion, Voyager, Braintrust agent eval framework.

---

## Part 1: What a harness IS

> "Every component here exists because the model can't do it alone." — awesome-harness-engineering

> "Model = CPU. Context Window = RAM. Agent Harness = Operating System. Agent = Application." — Schmid

> "An Agent Harness is the infrastructure that wraps around an AI model to manage long-running tasks. It's not the agent itself but the software system that governs how the agent operates." — Schmid

**Implication for gs-opt-loop**: The optimize.sh + prompts + state files ARE the OS. Opus is an application running on top. Our job is **OS-level reliability**, not teaching Opus to think better.

---

## Part 2: Core principles (14)

### P1. Feedforward + Feedback controls (Böckeler / Ashby)

- **Feedforward** = steer *before* action. Examples: schemas, linters, type checkers, declarative scope. In gs-opt-loop: plan_template, analyze.md rules, user_priority.md.
- **Feedback** = observe *after* action. Examples: tests, evaluator reviews, sanity phase. In gs-opt-loop: sanity, verify, evaluator sub-agent.
- **Both are required**. Feedback-only means you catch failures but can't prevent them. Feedforward-only means you prevent known failures but can't catch novel ones.
- **Apply**: every mechanism we add should be classified as one or the other. Current gs-opt-loop is ~80% feedback, ~20% feedforward. Imbalanced.

### P2. Ashby's Law of Requisite Variety

> "A regulator must have at least as much variety as the system it governs."

If the system has more failure modes than the harness has detection mechanisms, failures slip through. Our 22 benchmarks × many bottleneck types × many pass interactions = high-variety system. Our 4 phases + 6 sanity checks = low-variety regulator. **Mismatch = drift**.
- **Apply**: enumerate failure modes explicitly. Each known failure mode needs at least one detection mechanism.

### P3. Convergence invariant + reference baseline

> "Production agent loops use multiple stopping conditions layered together... no-progress detection exits the loop when repeated iterations produce no new information." — Oracle developers blog

- A loop must have a **single scalar metric** that the harness monitors. Each round either moves the metric toward the goal or is explicitly exploratory.
- **Reference baseline** = a frozen snapshot the loop measures against, not a rolling per-round baseline.
- **Apply**: freeze `benchmarks/data/reference.json` at a known-good commit. Add sanity R7: cumulative regression vs reference > 2% → halt. Current rolling baseline HIDES drift.

### P4. Dual-loop architecture (Magentic-One)

> "Microsoft's Magentic-One adds a dual-loop approach where the outer loop can reset the entire strategy when the inner loop stalls, preventing the agent from spinning on a failed approach."

- **Inner loop** = pick target, plan, implement, verify, sanity (current gs-opt-loop)
- **Outer loop** = observe N inner-loop iterations, detect stall patterns, **reset the strategy** when stall detected
- Without an outer loop, the inner loop's REVIEW phase can only patch the last failure, never restructure
- **Apply**: add a stall_mode to REVIEW that activates after 3+ consecutive no_change rounds AND forces longitudinal analysis + 1 structural proposal + pause-for-user if the proposal is rejected

### P5. Outcome classification + silent-no-op detection (Meta-Harness)

> "Every candidate receives explicit outcomes — baseline, keep, discard, crash, timeout, no-change, scope-violation."

Current outcomes: improved / no_change / regressed / abandoned / data-premise-error.
Missing: **silent-no-op** (unit tests pass but production IR shows no change), **scope-violation** (files touched outside declared scope), **prediction-failure** (measured delta < prediction by >3×).
- **Apply**: extend state.json `outcome` enum. Sanity phase writes one of the new values when appropriate. REVIEW tallies by outcome type across rounds.

### P6. Write scope mechanical enforcement (Meta-Harness)

> "Projects declare allowed paths for changes. The system automatically rejects edits outside scope as violations."

Currently our plans say "≤3 files, ≤200 LOC" but enforcement is by prompt, not by mechanism. R31 shipped 687 LOC, R32 shipped 820 LOC.
- **Apply**: pre-commit hook or verify step that reads `opt/current_plan.md`'s scope declaration and fails if `git diff --stat` exceeds it. Mechanical, not prompt-layer.

### P7. Filesystem-first audit trail (already present, strengthen)

> "stores the candidate workspace, proposal result, validation result, evaluation result, manifest, and diff on disk"

gs-opt-loop already writes to opt/plans/, opt/reviews/, opt/sanity_report.md. Good.
- **Gap**: no single `opt/round_N.manifest.json` that aggregates (plan, diff, benchmark_delta, evaluator, sanity_verdict, outcome) per round for cross-round analysis
- **Apply**: after VERIFY, generate opt/round_N.manifest.json. REVIEW and stall detector consume the manifest file, not hand-scraped summaries.

### P8. Cross-agent verification of plans (CodeX-Verify / CVCP)

> "The Verifier can be instantiated as another LLM (with a different prompt or model), a rule-based engine, or a symbolic checker — inspects plans for logical coherence, policy compliance, and alignment with system constraints."

Our sanity phase checks PROCESS (did the round close out properly). It does NOT check PLAN LOGIC (does ANALYZE's model of the world match reality).
- **Apply**: add a new phase `plan_check` between ANALYZE and IMPLEMENT. Fresh Opus session, read-only, consumes only `opt/current_plan.md` + production IR dump. Task: "for each plan assumption, cite evidence from a production run, or flag it as unverified." Block IMPLEMENT if ≥1 unverified load-bearing assumption remains.

### P9. Calibration ledger (for long-horizon self-improvement)

> "Detect exactly when a model stops following instructions or reasoning correctly after the 100th step, feeding this data back into training" — Schmid

We have no way to know if Opus's predictions are miscalibrated. Plans say "−4% nbody" and we got 0% but no ledger tracks the gap systematically.
- **Apply**: `opt/prediction_ledger.jsonl` — one row per plan: (round, benchmark, predicted%, actual%, confidence). After N rounds, REVIEW computes mean |pred − actual| and reports calibration drift. If drift > 3×, ANALYZE is forced to pessimistic mode (halve all predictions until drift recovers).

### P10. Reflexion / episodic verbal memory

> "An LLM agent generates natural language reflections about its failures and stores them as episodic memory that guides future attempts."

Our opt/knowledge/ is a knowledge base but not "episodic." It has technical notes, not "R19 and R31 both failed because of stale profileTier2Func" style entries.
- **Apply**: after each round, sanity_report.md has a "reflection" section — 1 paragraph, first-person, "if I were doing this round again, what would I do differently." REVIEW reads all recent reflections together to detect cross-round patterns.

### P11. Voyager automatic curriculum

> "An automatic curriculum that maximizes exploration... ever-growing skill library of executable code..."

Our ANALYZE picks targets by ROI + ceiling rule. That's exploit-only. There's no exploration mechanism that says "do a cheap measurement round on an unknown-gap benchmark."
- **Apply**: new round type: `explore` mode. Triggered by stall detector or user directive. Plans in explore mode do NOT modify code — they produce measurement artifacts that feed the next non-explore round.

### P12. Capability-ceiling acknowledgement + escalation

Current gs-opt-loop will run forever even if the problem exceeds its capability. No "I don't know" signal.
- **Apply**: stall detector (P4) fires → enter plan_check mode → if plan_check proposes "no tractable single-round fix exists" → state.json `escalated=true` → halt + notify user. User decides whether to rescope the goal or invest in a multi-round initiative.

### P13. Progressive scaffolding removal

> "The best harnesses are designed knowing those components will become unnecessary as models improve." — awesome-harness-engineering

Don't over-engineer. Every scaffold should have an exit criterion: "remove this when the model can do X reliably without it." Otherwise the harness becomes its own maintenance burden.
- **Apply**: each new mechanism (stall_mode, plan_check, prediction_ledger) should have a documented "retirement condition" in its prompt. REVIEW audits retirement conditions periodically.

### P14. Meta-Harness = harness optimizing itself

> "Optimize the executable environment around the coding agent rather than endlessly tweaking prompts in isolation." — Meta-Harness Stanford

The harness itself is a candidate for optimization. Our REVIEW phase does this, but at per-round granularity (patches, not structural). The Stanford Meta-Harness runs an 8-step outer loop:
1. Baseline workspace → 2. Prepare candidate → 3. Bootstrap snapshot → 4. Request improvements from agent → 5. Validate candidate → 6. Evaluate deterministically → 7. Store artifacts → 8. Retain best within budget
- **Apply**: gs-opt-loop v2 REVIEW stall_mode is a (lightweight) Meta-Harness — it proposes harness changes, we validate them by running 1 probe round, we evaluate by "did the probe round show a different failure mode than the previous stall?", we retain if yes.

---

## Part 3: The 5 biggest gaps (to be closed in v2)

Based on P1–P14 vs R28–R32 evidence:

1. **P3 missing**: rolling baseline hides cumulative drift → 5 rounds of −1% are invisible. **Reference baseline + sanity R7**.
2. **P4 missing**: no outer loop / stall reset → REVIEW patches forever. **stall_mode REVIEW after 3 no_change**.
3. **P8 missing**: no cross-agent check on plan logic → R30 wrong premise, R31 stale tool, R32 synthetic IR all slipped through. **plan_check phase between ANALYZE and IMPLEMENT**.
4. **P9 missing**: no calibration tracking → Opus writes confident plans that miss by 5-10×. **prediction_ledger.jsonl + pessimistic mode**.
5. **P2 mismatched**: harness variety << system variety → failures of classes we haven't catalogued slip through. **explicit failure mode catalog in opt/known_failure_modes.md + sanity must map each round to a known class or report "novel"**.

---

## Part 4: Anti-principles (what NOT to do)

- **Don't add more phases for the sake of it** (P13). Each new phase is context overhead for every future round.
- **Don't make sanity soft** (P1 feedback must be enforceable). Soft flags become noise.
- **Don't trust Opus to self-reflect deeply** (P4 requires structural, not per-round, reflection). Opus is good at per-round analysis, poor at longitudinal pattern detection without explicit structure.
- **Don't optimize the harness while the inner loop is live** (P14 Meta-Harness principle). Harness changes and compiler changes must be interleaved, not concurrent — or causality becomes impossible to trace.

---

## Sources

- [Harness Engineering for Coding Agent Users (Birgitta Böckeler / Martin Fowler)](https://martinfowler.com/articles/harness-engineering.html)
- [Meta-Harness: Self-Optimizing Harness (Superagentic AI, 2026)](https://medium.com/superagentic-ai/meta-harness-a-self-optimizing-harness-around-coding-agents-928733644551)
- [The Importance of Agent Harness in 2026 (Philipp Schmid)](https://www.philschmid.de/agent-harness-2026)
- [awesome-harness-engineering (ai-boost, GitHub)](https://github.com/ai-boost/awesome-harness-engineering)
- [Reflexion: Language Agents with Verbal Reinforcement Learning (Shinn et al., 2023)](https://arxiv.org/abs/2303.11366)
- [Voyager: An Open-Ended Embodied Agent (Wang et al., 2023)](https://openreview.net/forum?id=ehfRiF0R3a)
- [AgentFixer: Failure Detection to Fix Recommendations in LLM Agentic Systems](https://arxiv.org/html/2603.29848)
- [MARC: Multi-Agent Reasoning with Consistency Verification](https://arxiv.org/html/2603.24481v1)
- [Braintrust AI Agent Evaluation Framework](https://www.braintrust.dev/articles/ai-agent-evaluation-framework)
