# Optimization Round Index

**Read this before every ANALYZE.** One line per round, newest first.
This is the cross-round pattern detector — scan for repeat categories, ceilings, related rounds.

| # | Round ID | Date | Category | Target | Outcome | Key Commit | 1-line Lesson |
|---|----------|------|----------|--------|---------|------------|----------------|
| 17 | 2026-04-06-getfield-feedback-fix | 2026-04-06 | tier2_float_loop | GETFIELD feedback in Go exit handlers + shape guard dedup | improved | a5febca | Four rounds of dead pipeline: feedback mechanism worked perfectly but input data never existed; 4 lines fixed it |
| 16 | 2026-04-06-nbody-load-elim | 2026-04-06 | tier2_float_loop | Load Elimination (GetField CSE) + TypeFloat guard fix for nbody | improved | 364d733 | Block-local GetField CSE saves 17-49% across field-access-heavy benchmarks; compound effects dominate instruction-count estimates |
| 15 | 2026-04-06-osr-feedback-matmul | 2026-04-06 | field_access | Re-enable OSR with LoopDepth >= 2 gate for single-call compute functions | improved | 056607b | Tiering gates silently block entire benchmark classes — mandelbrot -80% from removing a 12-round-old disable |
| 14 | 2026-04-06-table-access-bypass | 2026-04-06 | field_access | Tier 1 float/bool fast paths + Tier 2 raw-int key/const-value bypasses + feedback infra | improved | 2c4ea80 | Tier 1 exit-resume elimination dominates: matmul -80%, sieve -56%, spectral -54%. Prediction model useless when it targets the wrong tier |
| 13 | 2026-04-06-native-array-kinds | 2026-04-06 | field_access | Native ArrayBool/ArrayFloat fast paths for GetTable/SetTable | improved | d89e9ed | Sieve -18-25%: exit-resume elimination is binary — either all table ops stay native or all exit; init loop (append) still exits |
| 12 | 2026-04-06-feedback-typed-loads | 2026-04-06 | tier2_float_loop | Feedback-typed heap loads (GuardType after GetTable/GetField) | no_change (IR-level mechanism works, but feedback never collected: Tier 1 has no feedback, interpreter never runs) | 644bd3c | Feedback availability is gated by tiering: BaselineCompileThreshold=1 means interpreter never runs, so FeedbackVector is always empty at Tier 2 compile time |
| 11 | 2026-04-06-recursive-tier2-phase3-5 | 2026-04-06 | recursive_call | B=0 graph builder fix + recursive Tier 2 policy flip | no_change (B=0 fix kept, policy reverted: Tier 2 net-negative for recursion 27-50%) | d9067bf | Tier 2 SSA overhead (guards, spill/reload BLR) exceeds benefit for recursive functions; need native recursive BLR or Tier 1 specialization |
| 10 | 2026-04-06-gpr-counter-fused-branch | 2026-04-06 | tier2_float_loop | GPR-resident int counter + fused compare+branch | improved (fibonacci_iterative -7.4%, matmul -2.7%, math_intensive -3.5%) | aba72f0 | Instruction count ≠ wall time; superscalar hides insn-level savings in float loops, but int-only loops benefit directly |
| 9 | 2026-04-06-tier2-licm-carry | 2026-04-06 | tier2_float_loop | Pin LICM-hoisted invariants in FPRs across loop body | improved (mandelbrot -6%, nbody -12%, spectral -15%, matmul -13%) | de874ce | Lazy harvest of pre-header FPR assignments beats pre-allocation; second-order effects (nbody/spectral) dominated primary target |
| 8 | 2026-04-06-tier2-licm | 2026-04-06 | tier2_float_loop | LICM pass — hoist loop-invariants (mandelbrot ≥35%) | no_change (mandelbrot -1.6%, infra landed) | 9da7d4c | 17 constants hoisted cleanly but wall-time unmoved — B3's bottleneck is the surviving FMUL/FADD chain, not constant materialisation |
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
