# SSA: Trivial / Redundant Phi Cleanup

**Scope**: post-construction phi simplification in Braun-style SSA builders.

## Problem

`graph_builder_ssa.go::tryRemoveTrivialPhi` handles the classical trivial
case `Phi(self, …, v)` → `v` where all non-self inputs agree. But in
**nested loops** (and sometimes in reducible loops after later rewrites),
the builder can leave behind **redundant-phi SCCs** — a cycle of phis
each of which references only *other* phis in the SCC and one single
outer value `v`. None of them is individually trivial under Braun's
Algorithm 3 definition, so `tryRemoveTrivialPhi`'s user-chain recursion
terminates before reaching them.

Braun et al. §3.1 flags this explicitly: "This SCC also will appear
after performing copy propagation on a program constructed with Cytron's
algorithm." §3.2 gives **Algorithm 5 (removeRedundantPhis)** as the fix.

## Canonical fix: Algorithm 5

Tarjan SCC over the phi-induced subgraph in topological order:

```
for each SCC S in topological order:
    outer = { v | v ∈ phi.Args for phi in S, v ∉ S, v ∉ previously collapsed }
    if |outer| == 1:
        let v = the single outer value
        replace every use of every phi in S with v
        remove all phis in S
    elif |S| > 1:
        recurse into subgraph induced by S with phi cycles removed
```

Runs in ~O(|phis| + |edges|) with Tarjan. Convergence: applying it to
reducible CFGs reproduces Cytron's minimal SSA form (Braun §3.2).

## When to run it

- **After graph construction** (`BuildGraph`) as a safety net for the
  builder's sealing order.
- **After `Inline`** — inlining introduces new merge blocks, fresh
  phis, and can re-create redundant SCCs from previously-simple code.
- **Before LICM** — LICM explicitly skips phis, so any value stuck in
  a trivial-phi cycle inside the loop body is invisible to LICM and
  cannot be hoisted. Collapsing trivial phis unlocks downstream LICM.

## Observed impact (sieve, R31)

Inner j-loop of sieve benchmark had three self-referential phis for
loop-invariant values (table, step, n) that survived construction.
Cost per 2.58M iterations:
- 3× STR Xn,[X26,#imm] (self-copy spills) at back-edge
- 2× LDR Xn,[X26,#imm] (reload step from spill) mid-iteration
- 2× SBFX + 3× ORR/UBFX/STR re-box cycles (step phi copy sequence)

Removing these (post-construction pass) directly enables LICM to hoist
the SetTable validation tower in a follow-up round because v77 (the
phi output) becomes v74 (outer-loop carrier, defined outside the
j-loop body), and `canHoistOp(OpGetTable)` with alias-check already
handles that case.

## Related production compilers

- **LLVM**: `SimplifyCFG` + `InstructionSimplify::simplifyPHINode`
  implement Braun's trivial-phi rule as a pass-level safety net.
- **V8 TurboFan**: `CommonOperatorReducer::ReducePhi` folds uniform-input
  phis as part of the reducer stack.
- **SpiderMonkey Ion**: `EliminatePhis` in `IonAnalysis.cpp` runs
  Braun-style cleanup after MIR construction.
- **LuaJIT**: not applicable (trace JIT, no mergeable phis).

## References

- Braun et al., "Simple and Efficient Construction of Static Single
  Assignment Form" (CC 2013): https://pp.ipd.kit.edu/uploads/publikationen/braun13cc.pdf
- Cornell CS6120 class notes on Braun SSA:
  https://www.cs.cornell.edu/courses/cs6120/2025sp/blog/efficient-ssa/

## GScript application

New pass: `internal/methodjit/pass_simplify_phis.go`. Placement in
`RunTier2Pipeline`: immediately after `BuildGraph → Validate`, before
the first `TypeSpec`. Re-run after `Inline`. ~60 LOC impl + ~80 LOC tests.
