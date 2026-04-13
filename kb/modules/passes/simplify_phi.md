---
module: passes/simplify_phi
description: Global redundant-phi SCC elimination (Braun et al. 2013 Algorithm 5). Collapses cycles of phis whose only outside operand set is a single value.
files:
  - path: internal/methodjit/pass_simplify_phis.go
  - path: internal/methodjit/pass_simplify_phis_test.go
last_verified: 2026-04-13
---

# SimplifyPhis Pass

## Purpose

Remove redundant phi strongly-connected components from the SSA graph. The graph builder's per-phi `tryRemoveTrivialPhi` handles the local trivial case (a phi with one distinct non-self operand), but it cannot see a group of mutually-referencing phis that collectively have only one non-phi outer operand — e.g. nested loop headers whose loop-invariant phis reference each other across back-edges. Braun et al. 2013 Algorithm 5 is the minimum cleanup that collapses those SCCs.

## Public API

- `func SimplifyPhisPass(fn *Function) (*Function, error)`

## Invariants

- **MUST**: the pass runs twice in production — once right after `BuildGraph` / `Validate` (catching builder leftovers) and once right after `Inline` (catching SCCs created by callee splicing). Both runs appear in `RunTier2Pipeline`.
- **MUST**: the pass is idempotent. Running it on a function that contains no phis is a no-op (early return when `len(phis) == 0`).
- **MUST**: algorithm steps are (as implemented in the file comment):
  1. Collect all `OpPhi` instructions and build the phi-subgraph: edge `phi → arg.Def` iff `arg.Def` is also a phi.
  2. Tarjan SCC in reverse-topological order (children first).
  3. For each SCC, compute the "outer" set = the set of `Value.ID`s referenced by any phi in the SCC whose `Def` is NOT a phi in this SCC (honouring replacements already recorded for earlier SCCs via path-compressed `resolve`).
  4. If the outer set has exactly one element, mark every phi in the SCC as replaced by that outer value and remove it from its block.
  5. Rewrite every instruction's `Args` through the replacement map with path compression.
- **MUST**: replacement resolution uses path compression — `resolve` walks the chain and always returns the root value to avoid quadratic rewrite time on long chains.
- **MUST NOT**: mutate the CFG. The pass only removes phi `*Instr`s from their block and rewrites `Args`; no block is added, removed, or re-linked.
- **MUST NOT**: touch the graph builder's incomplete-phi bookkeeping (`block.defs`, `block.incomplete`) — those are already obsolete by the time Tier 2 pipeline runs.
- **MUST NOT**: skip computing the "outer set" using live replacement state — a later SCC may depend on an earlier SCC having been collapsed; reading stale pointers would falsely see a phi-only edge.
- **MUST NOT**: collapse an SCC whose outer set has zero elements (unreachable) or ≥2 elements (genuinely divergent).

## Hot paths

Not a steady-state hot path — runs during compilation only. Functions that exercise the pass most:
- `nbody` — nested-loop accumulators whose inner phi references outer phi across the back-edge
- `spectral_norm` — inlined `A(i,j)` body introduces mutually-referencing phis after splicing
- `mandelbrot` — complex iteration accumulators
- `method_dispatch` — after inlining, many trivial phis from callee-splice merge blocks

## Known gaps

- **Phi-only.** The pass does not simplify non-phi ops. It intentionally ignores regular instructions even if they could be folded (that is ConstProp's / DCE's job).
- **No cross-function.** It operates on one `*Function` at a time.
- **No structural CFG cleanup.** A block that becomes empty after phi removal is kept — unreachable-block elimination is not part of this pass.
- **Relies on explicit `Def` links.** If a pass (e.g. Inline) leaves placeholder `Value.Def` pointers, `relinkValueDefs` must have already run — otherwise the phi-subgraph edges are wrong and the SCC detection silently misses groups.

## Tests

- `pass_simplify_phis_test.go` — single trivial phi SCC, nested mutually-referencing phi SCC with unique outer value, SCCs with divergent outer sets (no collapse), idempotency on phi-free functions, path-compression correctness.
