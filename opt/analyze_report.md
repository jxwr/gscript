# ANALYZE Report — R35: object_creation +50.79% regression bisect (diagnostic round)

## Architecture Audit

**Quick read** (rounds_since_arch_audit=1). `bash scripts/arch_check.sh` flags:

- `emit_dispatch.go` 971, `graph_builder.go` 955, `tier1_arith.go` 903, `tier1_table.go` 829 — all carried from previous audits. **Not touched this round.** No split Task 0 needed.
- Test ratio 94% (up from 88% at R28). Test files lead lines.
- `constraints.md` current — Tier constraints + ceilings unchanged since R28 audit.

No new architectural issues to record. Full audit queued for R36 (rounds_since_arch_audit will hit 2 after this round).

## Gap Classification

| Category | R7 drifters | vs reference | Blocked? |
|----------|-------------|--------------|----------|
| allocation_heavy (regression) | object_creation | +50.79% (0.764→1.152s) | **MANDATED (R7)** |
| tier2_table (regression) | sort | +16.67% (0.042→0.049s) | R7 FAIL (secondary) |
| closure | closure_bench | +11.11% latest / live run 0.026s (BELOW ref) | Noise — explicitly deprioritized by sanity |
| tier2_float_loop | ceiling=2 | — | Deprioritize (3 rounds) |
| field_access | ceiling=2 | — | Deprioritize (3 rounds) |

**R7 harness-v3 P5 rule**: next target MUST be an R7-flagged benchmark. Chosen: **object_creation**. Largest drift (+50.79%), HIGH drift confidence, clearest evidence chain in `opt/authoritative-context.json`.

## Blocked Categories
- `tier2_float_loop`: category_failures=2 (R32, R33). Eligible again after 3 rounds off.
- `field_access`: category_failures=2 (R31). Eligible again after 3 rounds off.

## Active Initiatives
- `tier1-call-overhead.md`: Item 1a DONE (R27), Item 1 BLOCKED (goroutine-stack budget). Not targeted this round.
- `tier2-float-loops.md`: paused (category_failures=2 + ceiling decay in progress).
- `recursive-tier2-unlock.md`: paused (ceiling hit R4-R5).

**Initiative exhaustion check**: `tier2-float-loops` had R32 no_change + R33 data-premise-error back-to-back — exhaustion pattern confirmed. This round deliberately pivots AWAY from tier2_float_loop per category_failures=2 ceiling. No retrospective required this round because we are not continuing the initiative.

## Selected Target

- **Category**: `allocation_heavy` (canonical name for object_creation gap)
- **Initiative**: standalone (regression diagnostic round)
- **Benchmark**: object_creation
- **Reason**: R7 FAIL hard gate, +50.79% is the largest cumulative drift on any non-excluded benchmark. Evidence in `opt/authoritative-context.json` identifies specific commits as HIGH-suspects. No blocking constraints.

## Architectural Insight (Step 1b)

object_creation's regression is **not a missing optimization** — it's a **recent regression** (50.79% drift appeared within a ~6 hour window between `a388f782` @ 14:55:55Z and HEAD `b3d8824`). This is architecturally different from every prior round: R28-R34 all chased missing optimizations. R35 chases a **lost invariant** — something in the 8 post-reference code-changing commits is costing object_creation ~0.39 wall-seconds.

The design question isn't "what should we add" but "what did we break, and why was the break invisible to round-by-round verification". The correct first move is **bisect**, not **speculate-and-patch**. `opt/authoritative-context.json` already identified 2 HIGH-suspect commits (39b5ef3 Shape system rewrite, 4455fcf R30 revert) and 6 secondary candidates — a `git bisect run` with the benchmark as the witness will converge in ~3 bisect steps.

This is a pure **diagnostic round** — same pattern as R29 (root-caused fib +988%, produced knowledge doc, deferred fix to R30). Delivering a confident, commit-level root-cause to R36 is higher-value than a speculative fix that could miss.

## Prior Art Research

### Knowledge Base Check (no web search this round)

Workflow says "check `opt/knowledge/` first — if a file covers the topic, read it and skip web search entirely." Relevant KB entries:

- `opt/knowledge/r29-fib-root-cause.md` — **directly relevant**. R29 was the same pattern: root-cause a regression, defer fix. Pattern: instrument the symptom, write knowledge doc, no production code. Outcome `no_change` but enabled a targeted R30.
- `opt/knowledge/global-cache-stable-opt.md` — touches GetGlobal cache and self-call interaction; relevant to 4455fcf suspect.

**No web search performed this round.** Rationale: bisect is a mechanical, project-internal procedure with no external prior art. Research budget preserved for R36 when the fix direction is known.

### Reference Compiler Source
Skipped (no new mechanism to research this round).

### Knowledge Base Update
Will be written by Task 1: `opt/knowledge/r35-object-creation-regression.md`.

## Source Code Findings

### Files Read
- `opt/authoritative-context.json` — primary evidence source (P3)
- `git show --stat 39b5ef3` — top suspect commit. Touches `runtime/shape.go` (+102), `runtime/table.go` (-486, split), `runtime/table_int.go` (+466), `vm/vm.go` (+20). **Does NOT touch `internal/methodjit`** — if this is the culprit, the regression is runtime-side, not JIT-codegen-side.
- `opt/sanity_report.md` (R34) — R7 FAIL spec + required next-action
- `git log --format='%h %cI %s' a388f78..HEAD` — post-reference commit chain

### Diagnostic Data (from `opt/authoritative-context.json`)

| Function | IR blocks | GetGlobal | Call | GetField/Set | NewTable | Total insns | Memory % |
|----------|-----------|-----------|------|--------------|----------|-------------|----------|
| create_and_sum | 5 | 4 | 4 | 0/0 | 0 | 813 | 57.3% |
| transform_chain | 5 | 5 | 5 | 0/0 | 0 | 988 | 58.0% |
| new_vec3 | 1 | 0 | 0 | 0/3 | 1 | 208 | 62.0% |

**Key facts from CONTEXT_GATHER observations** (all HIGH confidence, citations in `authoritative-context.json#candidates[object_creation].observations`):

1. `create_and_sum` and `transform_chain` are **call-bound**, not allocation-bound — all allocation lives in the `new_vec3` callee.
2. `new_vec3` is 208 insns / 3 SetField ≈ **~43 memory ops per field write**. Much heavier than a V8-style "2-insn store + shape-stable update".
3. Memory% is 57-62% across all three functions. Memory-bound despite `NumSpills=0`. The memory ops are NaN-box/unbox, GetGlobal slot loads, and caller-saved save/restore around BL. Register pressure is NOT the issue.
4. GetGlobal in loop (2 in create_and_sum body B1, 3 in transform_chain body B1) is **not hoisted** by LICM despite being loop-invariant. Cross-cutting pattern also hits `sort` (2 GetGlobals in quicksort B6).

### Bisect Candidate Set (from `authoritative-context.json#bisect_candidates` + `git log a388f78..HEAD`)

| # | SHA | Commit | Suspect for object_creation | Prior |
|---|-----|--------|------------------------------|-------|
| 1 | 598bc1e | self-call DirectEntryPtr check | sort (recursive), not oc | LOW |
| 2 | 39b5ef3 | **Shape system rewrite + GC scan all regs** | **HIGH** | HIGH |
| 3 | 4b321fb | test fixtures only | not code | VERY LOW |
| 4 | 144c1a4 | R28 ctx.Regs lazy flush on self-call | self-call only | LOW |
| 5 | 903e505 | R30 transient OP_GETGLOBAL | GetGlobal in loop | HIGH (but reverted) |
| 6 | 4455fcf | Revert of 903e505 | **Net = zero if perfect revert** | HIGH |
| 7 | c375913 | R31 SimplifyPhisPass | reported no-op | LOW |
| 8 | 56b19e7 | R32 LoopScalarPromotionPass | reported no-op (upstream gate) | LOW |

### Actual Bottleneck (data-backed, regression-class)

**Unknown until bisect runs.** Hypothesis ranking (LOW confidence until Task 1 completes):

1. **39b5ef3 — Shape system rewrite**: either SetField lowering became heavier, OR `ScanGCRoots` scan-all-regs made every GC pause O(N-regs) instead of O(active-regs). object_creation does ~200K allocations and triggers GC frequently. Cannot be reverted (correctness fix) — forward fix required.
2. **903e505 / 4455fcf pair**: imperfect revert leaving a side-effect that costs GetGlobal-in-loop performance. Bisect will reveal either the pair is neutral (eliminating this hypothesis) or the revert has residual cost.
3. **Secondary candidates** (144c1a4, 56b19e7, c375913): unlikely but cheap to rule out via bisect.

## Plan Summary

**Pure diagnostic round**, R29 pattern. Task 0 (infra) commits a production-pipeline insn-count fixture locking in the current regression baseline (208/813/988 insns for new_vec3/create_and_sum/transform_chain) so R36 can assert "back to ≤ reference". Task 1 (one Coder) runs automated bisect across 8 post-reference code-changing commits using `benchmarks/run_all.sh --runs=3 -- object_creation` as the witness, identifies the culprit, reads its diff, writes `opt/knowledge/r35-object-creation-regression.md` with root cause + proposed R36 forward-fix. **No production `.go` code changed.** Expected outcome: `no_change` wall-time; knowledge delivery is the primary value; R36 ships the surgical fix.

Key risk: bisect may identify a correctness-fix commit (598bc1e, 39b5ef3's GC scan change) that cannot be reverted. In that case, the knowledge doc proposes a forward fix — e.g., "keep the ScanGCRoots semantic but narrow the scanned-regs window using frame-level tracking" — which R36 implements.
