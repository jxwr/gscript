# Initiative: Recursive Tier 2 Unlock

> Status: active
> Created: 2026-04-05
> Owner: gs-opt-loop
> Category: recursive_call

## Motivation

Five benchmarks (fib 1.40s, ackermann 0.26s, mutual_recursion 0.18s, fib_recursive 14.0s, method_dispatch 0.10s) are stuck at Tier 1 forever. Aggregate wall-time gap vs LuaJIT: ~15s across these rows. Root cause is architectural, not a single bug — `func_profile.go` gates Tier 2 on `HasLoop`, recursive inliner was absent, and graph builder drops `OP_CALL B=0` args entirely.

Rounds 4 and 5 tried and failed at the simple policy flip:
- Round 4 (2026-04-05-recursive-inlining): abandoned, suite hang.
- Round 5 (2026-04-05-tier2-recursion-diagnose): diagnosis-only, landed `Unpromotable` correctness gate (commit 239f0d7), reverted policy flip (commit f54ea63).

Currently **paused** — `category_failures["recursive_call"]=2` blocks category for next round per Ceiling Rule.

## Expected Impact

| Benchmark | Current | Target (post-unlock) | Mechanism |
|-----------|---------|---------------------|-----------|
| fib | 1.40s | 0.40–0.60s | 2-level inline + int-specialized arith across inline boundary |
| ackermann | 0.26s | 0.08–0.12s | per-chain recursion counter + variadic IR for `OP_CALL B=0` |
| mutual_recursion | 0.18s | 0.06–0.10s | per-chain counter handles f/g alternation correctly |
| fib_recursive | 14.0s | 4–6s | linear scaling from fib fix |
| method_dispatch | 0.10s | 0.08–0.09s | inner bodies inline, BLR overhead drops |

## Phases

- [x] Phase 1: **Bounded recursive inliner infrastructure** — Shipped dormant in round 4 (commit 1c71784). `MaxRecursion=2` param + DFS cycle detect. Tests green.
- [x] Phase 2: **Diagnose hang + land correctness gate** — Round 5. `Unpromotable=true` on `OP_CALL B=0` in BuildGraph. Hang is now impossible. (commit 239f0d7)
- [x] Phase 3: **Fix variadic-arg graph builder (`OpCallV` / threaded-top model)** — graph builder must emit correct IR for `OP_CALL B=0` (nested-call-as-argument pattern). Done: top-tracking resolves to fixed-arity OpCall (commit d9067bf).
- [ ] Phase 4: **Per-chain recursion counter** — convert per-proto counter in `pass_inline.go` to per-caller-chain (JSC-style). Required before enabling the policy flip, because mutual-recursion cycles may loop on per-proto but terminate on per-chain.
- [ ] Phase 5: **Re-attempt func_profile gate flip** — with Phase 3+4 landed, re-apply 6bd0385's clause (`CallCount>0 && !HasLoop && ArithCount>=1 && BytecodeCount<=40 → promote at runtimeCallCount>=2`). Verify full benchmark suite green.
- [ ] Phase 6: **Tier 2 native BLR for recursive** — eliminate spill/reload overhead around recursive BLR sites (currently costs ~15–20ns per call).

## Prior Art

- **JSC (`OptionsList.h`)**: `maximumInliningRecursion=2`, `maximumInliningDepth=5`, `maximumFunctionForCallInlineCandidateBytecodeCostForDFG=80`. Per-caller-chain, not per-proto.
- **V8 TurboFan**: `--max-inlined-bytecode-size=460` single-callee, `=920` cumulative. Budget-based.
- **HotSpot C2**: `MaxRecursiveInlineLevel=1`, invocation counter decayed periodically to bound deopt thrash.
- **SpiderMonkey Warp Trial Inlining**: per-callsite ICScript snapshots — our closest analogue if we later add call-site feedback.

## Rounds

| Round | Phase | Outcome | Notes |
|-------|-------|---------|-------|
| 2026-04-05-recursive-inlining | 1 | abandoned | Inliner infra landed dormant; policy flip hung suite |
| 2026-04-05-tier2-recursion-diagnose | 2 | no_change+fix | Root cause localized; Unpromotable gate shipped |
| 2026-04-06-recursive-tier2-phase3-5 | 3+5 | no_change | B=0 fix kept (Phase 3 done). Policy flip reverted: Tier 2 27-50% slower for recursive fns |

## Next Step

**Phase 5 is invalidated.** Round 11 proved Tier 2 is net-negative for recursive functions (27-50% regressions). The SSA overhead (guards, type checks, spill/reload around BLR) exceeds the benefit from 2-level inlining. Options:
- **Phase 6 first**: Native recursive BLR in Tier 2 (skip spill/reload for known-recursive callees) — may close the gap enough to make Phase 5 viable.
- **Alternative**: Tier 1 recursive specialization (skip type-check in BLR for known-recursive targets) — lighter approach, no SSA overhead.
- Phase 4 (per-chain counter) is still useful but only after Tier 2 recursion is net-positive.

## Risks / Failure Signals

- If Phase 3 requires >3 file changes or touches regalloc/emit core paths, defer to arch_refactor initiative.
- If per-chain counter (Phase 4) can't bound mutual-recursion within inliner fixpoint budget, revisit with cumulative-cost budget (JSC-style).
- Abandon if 2 consecutive post-resumption rounds can't move fib below 1.0s with Phases 3+4 landed.
