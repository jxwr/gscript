# Optimization Round Index

**Read this before every ANALYZE.** One line per round, newest first.
This is the cross-round pattern detector — scan for repeat categories, ceilings, related rounds.

| # | Round ID | Date | Category | Target | Outcome | Key Commit | 1-line Lesson |
|---|----------|------|----------|--------|---------|------------|----------------|
| 7 | 2026-04-05-tier2-fpr-resident | 2026-04-05 | tier2_float_loop | FPR-resident float SSA (mandelbrot ≥35%) | improved (aggregate -1.88%, target missed) | 686ba11 | Diagnostic-first killed Gap B hypothesis early; scratch-FPR cache alone is 1% — LICM is the real next bottleneck |
| 6 | 2026-04-05-tier2-float-profile | 2026-04-05 | tier2_float_loop | Profile Tier 2 float loops (5 benchmarks) | no_change (diagnostic) | 7f1c47d | Per-op NaN box/unbox dominates 5/5 float loops — IR + ASM dumps must agree before optimizing |
| 5 | 2026-04-05-tier2-recursion-diagnose | 2026-04-05 | recursive_call | Diagnose tier2 hang from round 4 | no_change + fix | 239f0d7 | BuildGraph drops `CALL B=0` args — Tier 2 IR silently wrong; `Unpromotable` gate blocks the proto |
| 4 | 2026-04-05-recursive-inlining | 2026-04-05 | recursive_call | Bounded recursive inlining + tier-up policy | abandoned | — | Tier-up policy flip hangs fib/ackermann; MaxRecursion=2 infra landed dormant |
| 3 | 2026-04-04-overflow-opt | 2026-04-04 | tier2_correctness | Overflow-check optimization | improved | — | Loop-counter Aux2=1 + range analysis fix fibonacci_iterative |
| 2 | 2026-04-04-tier2-correctness-r2 | 2026-04-04 | tier2_correctness | 5 tier2 correctness failures | improved | — | int48 overflow + resyncRegs BLR callee corruption + inline phi rewrite |
| 1 | 2026-04-04-tier2-correctness | 2026-04-04 | tier2_correctness | 7 tier2 correctness failures | partial | — | GPR phi move ordering + rawIntRegs deopt corruption |

## Categories (canonical taxonomy)

**Every plan MUST pick ONE category from this list.** Category tracks ceiling across rounds.

- `recursive_call` — fib, ackermann, mutual_recursion, fib_recursive (call-overhead dominated)
- `tier2_float_loop` — spectral_norm, nbody, mandelbrot, math_intensive (FPR + deopt + guards)
- `tier2_correctness` — wrong results or hangs from Tier 2 codegen
- `allocation_heavy` — binary_trees, object_creation (NEWTABLE / escape analysis)
- `gofunction_overhead` — method_dispatch, any GoFunction call in inner loop
- `field_access` — table lookup, IC effectiveness, shape stability
- `call_ic` — inline cache improvements for CALL
- `regalloc` — register spilling / phi conflicts / live range
- `missing_intrinsic` — math.sqrt-style fast paths
- `arch_refactor` — multi-round architectural rework (new pass, new IR op, new tier)
- `other` — use sparingly; prefer adding a new category
