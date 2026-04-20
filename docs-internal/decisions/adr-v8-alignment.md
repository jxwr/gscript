# ADR — V8 architecture research and GScript alignment plan (R156)

**Status**: open; produces direction for next arc.
**Date**: 2026-04-21.
**Supersedes**: closes the "Path A / Path B / Path C" framing in
`project_call_path_arc_r147_r149.md` with a concrete four-path plan.

## Context

The R147-R155 arc definitively exhausted method-JIT emit-level call-path
tuning. Remaining LuaJIT gap: fib 34×, ackermann 73×, mutual_recursion
47×, nbody_dense 5.2×, sieve 7.6×, fannkuch 2.3×. Mandelbrot already
surpassed (R99).

Before scoping the next arc this ADR collects what V8 actually does for
each of these workload classes and maps the gap to concrete
infrastructure additions.

## Research findings (summary)

Full agent reports on file in `rounds/R156.yaml`. Headline:

1. **V8 uses 4 tiers, not 2.** Ignition (interp) → Sparkplug (Ignition-
   frame-identical baseline) → Maglev (CFG-SSA, ~10× faster than
   Sparkplug, ~10× slower to compile than Sparkplug) → TurboFan (sea-
   of-nodes full optimization). V8 is actively *retiring* TurboFan for
   Turbolev (Maglev's CFG fed to TF's backend) — confirming CFG-SSA
   over sea-of-nodes is the right architectural line. **GScript's Tier 2
   is architecturally equivalent to Maglev, not TurboFan.** Our
   choice of CFG-SSA + optimization pipeline is correct.

2. **V8 picked "more tiers" over trace JIT.** This is strong evidence
   that method JIT CAN close the recursion gap if the design is right.
   Trace JIT is not the answer. GScript should not pursue Path C.

3. **The single biggest non-trace-JIT gap is IC-patched direct call
   without ctx-setup.** At every call site, V8 patches the IC slot to a
   direct JMP/CALL to the target `Code` entry. The caller does not
   rebuild callee ctx (Constants, Regs, ClosurePtr, GlobalCache) — the
   callee's prologue fixes its own invariants. GScript's Tier 2
   emit_call_native.go spends ~50 insns rebuilding ctx even when the
   callee is known. R107-R120's mono-IC only shaved a few percent
   because the IC hit **still pays the full ctx-setup cost**. This is
   the single biggest structural gap on fib/ack.

4. **Escape analysis + scalar replacement is a 500-700 LOC well-scoped
   pass.** TurboFan's full pass is ~1300 LOC; ~60% handles things
   GScript can skip in v1 (deopt materialization, arguments-object, map
   checks). MVP for `object_creation`-shape benchmarks (single-basic-
   block allocation, fields known statically, never escapes through
   call/phi/heap-store) is ~500 LOC across 5-6 rounds. Published
   Choi-1999 numbers: 19% median total-runtime reduction on suite, up
   to 23% best case. GScript's `object_creation` with 3 `new_vec3`
   allocations per loop iteration is a textbook best case; expect -30%
   to -60% wall-time.

5. **OSR as restart-from-entry is strictly below V8's bar.** V8 has a
   dedicated OSR-entry prologue that materializes live locals (including
   the loop counter) from the Tier 1 frame and jumps to
   `restart_after_osr` — *inside* the loop body post-increment. Our
   R154 latent for-loop-emit bug (loop counter never advances on
   resume) is the exact symptom V8 avoids by requiring OSR/resume PCs
   to be validated safepoints (block entries, loop headers post-
   increment). Our restart-from-entry approach side-steps the bug only
   because we restart-from-entry, which has its own cost (side-effect
   replay). Fixing this enables the wider promotion gate that R154
   disproved in its current form.

6. **Inline heuristic shape matters more than budget.** V8 uses a
   *cumulative* bytecode cap (920 ops/compilation) + frequency-weighted
   per-site inlining + an absolute recursion-depth terminator. GScript's
   uniform `MaxRecursion=5` is wrong for asymmetric call trees: it
   under-inlines `fib` (depth 5 × ~15 bytecodes = ~75 ops, well below
   920) and exponentially over-inlines `ack`. Replacing the uniform
   budget with V8's shape costs ~1 round and unlocks both.

7. **Deopt translation records solve the ABI hazard.** Our R149 Variant
   B broke on "Tier 1 caller boxed-memory ABI vs Tier 2 callee raw-
   register ABI" mid-execution transition. V8 handles this via a per-
   deopt-point translation metadata record specifying representation
   for every live value ("v3 is a raw int in X5 — box to SMI in slot 7
   on deopt"). GScript has ExitCode values (3/4/5/6/2/9) but no
   per-exit translation record. Adding this unlocks safe mid-execution
   tier transitions AND is a prerequisite for scalar-replaced-object
   deopt correctness in Path B.

8. **Fused untag-at-use makes R137's raw-int-return unnecessary.** V8
   keeps calls tagged (SMI in standard register) and relies on
   `CheckedSmiUntag → Int32AddWithOverflow → tag-on-write` fusing to
   ~3 insns. R137 Layer 4 proposed end-to-end raw-int return; V8 shows
   that's the wrong layer — the win comes from fusing untag+op+tag at
   the Tier 2 IR level, not from a new ABI.

## Decision — four concrete paths for next arcs

Ordered by expected ROI per round of investment:

### Path X (new) — IC-patched direct call without ctx-setup
**Estimate**: 3-5 rounds.
**Target**: fib, ackermann, mutual_recursion, method_dispatch, and the
recursive half of binary_trees.
**Deliverable**: rewrite emit_call_native.go's IC-hit branch to emit a
direct `BL imm26` to the cached code entry (or `BLR` to a cached PC)
and SKIP the ctx-setup block entirely. Callee prologue is responsible
for its own Constants/Regs/ClosurePtr reload. This is a direct
translation of V8's IC design.

**Expected gain**: fib 34× → ~15-20× (caller-side ~50 insns → ~5 insns
per call), ackermann ~flat (body-dominated per R154), mut_recursion
~unchanged until Path W extends the gate beyond `staticallyCallsOnlySelf`.

**Risk**: callee prologue invariants. Currently Tier 2's prologue
assumes ctx is pre-populated. Changing the ABI requires updating both
sides in lockstep — single-round atomic change or a behind-flag rollout.

**Why this is Path X, not "more call-path tuning"**: R147-R150 tuned the
existing ~50-insn caller-side path by skipping specific insns.
Path X **removes the entire block** by making the callee responsible.
Architectural, not emit-layer.

### Path B — Escape analysis + scalar replacement MVP
**Estimate**: 5-6 rounds (per agent 2 sub-round plan).
**Target**: object_creation, binary_trees (allocation half), table-
heavy benchmarks, cumulative ~10-20% on any `new X()` in hot loop.

**Sub-rounds**:
- **B.1**: Recognize single-block virtual `NewTable` allocations whose
  only uses are `GetField/SetField` with static keys.
- **B.2**: Field-Variable SSA tracking within block; rewrite `GetField`
  to last `SetField` value. (Extension of existing LoadElimPass.)
- **B.3**: Extend across if/else merges within one function.
- **B.4**: Loop support via EffectPhi-equivalent; fixpoint widening.
- **B.5**: Integration with inline pass (EA runs AFTER inline, BEFORE
  LICM).
- **B.6**: Benchmark + correctness. Skip deopt-materialization v1 by
  bailing on any allocation whose SSA value reaches a frame-state edge.

**Correctness traps** (per agent 2): identity semantics (`a == b`),
metatable side effects, GC root visibility, partial escape on
conditional arms, field-count mismatch at Phi. Safe v1: NewTable
without metatable, single-block scope, no `==` reach.

### Path W (new) — Dedicated OSR entry with safepoint-validated resume PCs
**Estimate**: 3-4 rounds.
**Target**: unblocks the promotion gate (R152/R154 showed widening is
unsafe with current OSR); fixes the R154 latent loop-counter bug;
enables Tier 2 compilation for single-depth driver loops that
currently stay Tier 1.

**Deliverable**: compile a dedicated `t2_osr_entry_<pc>` label for each
loop backedge where OSR can fire, with a prologue that materializes
live locals from the Tier 1 frame and jumps into the loop body *post-
increment*. Replaces restart-from-entry semantics. Adds a safepoint
validator to the emit pipeline.

**Dependency**: Path X and/or Path B likely land first to keep the
round shapes cleaner; Path W is a standalone win after either.

### Path I — Inline heuristic overhaul
**Estimate**: 1-2 rounds.
**Target**: fib, ackermann, call-heavy drivers (nbody_dense via
advance()).
**Deliverable**: replace `MaxRecursion=5` with V8's model — cumulative
bytecode budget per compilation (~1000 ops) + per-site frequency
threshold + absolute recursion terminator. Validate with existing
bench suite for regression on previously-helped shapes.

**Risk**: `ackermann` already expands 2^depth; enforcing a cumulative
cap will reject deep inlining and fall back to the IC-direct-call path.
This only helps if Path X ships first; otherwise the current ~50-insn
ctx-setup dominates anyway.

## Non-decisions

- **Path C (trace JIT)**: explicitly rejected. V8's shipped 4-tier
  design with Turbolev is strong evidence that method JIT closes the
  LuaJIT gap if the architecture is right. Trace JIT is not required.
- **Sparkplug-style frame-identical baseline**: deferred. GScript's
  Tier 1 already does native BLR with Tier 2-reachable DirectEntryPtr;
  Sparkplug adds a narrow win (Ignition→Sparkplug OSR is free) that
  GScript could copy later but isn't a priority.
- **Turbolev-style backend unification**: deferred. TurboFan parity
  isn't on GScript's roadmap.

## Consequences

If Path X lands:
- fib gap: 34× → ~18× (estimated per per-call-insn reduction).
- Ack / mut: body-dominated; small gains pending Path I and W.

If Path B lands:
- object_creation wall-time: -30% to -60% (Choi numbers + gscript alloc
  density).
- binary_trees: partial improvement (only the allocation half).

If Path W lands:
- Promotion gate can safely widen to LoopDepth>=1.
- sieve / table_field_access / table_array_access: could reach Tier 2
  and gain the typical 10-20% type-spec win (not the regressed +16%
  that R154 observed under the restart-from-entry OSR).

If Path I lands:
- fib ~flat (budget is already underutilized at depth=5).
- ack ~flat (exponential expansion is capped but that was harmful
  anyway).
- Primary value is *not* perf but *correctness of inline decisions* —
  preventing future over-inlining hazards.

## Recommendation

**Open Path X as the next arc.** Biggest identified single-structural
gap; 3-5 rounds; direct V8-validated design. Arc close criteria:
- fib 5-sample median drops below 0.70s (current 0.884s) WITHOUT
  correctness regression.
- Full methodjit / jit / vm / runtime suite stays green.
- If wall-time doesn't move by R+3, halt and pivot to Path B.

Sequence after Path X: Path B (allocation-heavy), Path W (OSR gate),
Path I (inline heuristic). Each is independent enough to schedule
based on benchmark priority at the time.

## Sources (research agent citations)

See `rounds/R156.yaml` for full inline citations. Primary references:

- Maglev — V8's Fastest Optimizing JIT (v8.dev, 2023)
- Sparkplug — a non-optimizing JavaScript compiler (v8.dev, 2021)
- Land ahoy: leaving the Sea of Nodes (v8.dev, 2025)
- Profile-Guided Tiering in V8 (Intel × Google, 2024)
- TurboFan escape-analysis.cc source (github.com/v8/v8)
- Tobias Tebbi — Escape Analysis in V8 (JFokus 2018)
- Choi et al. — Escape Analysis for Java (OOPSLA 1999)
- Kotzmann & Mössenböck — HotSpot EA+deopt PhD thesis (2005)
- on-stack replacement in v8 — wingolog (2011)
- V8 release v7.1 (escape analysis of contexts)
